package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gordon2/manualbox/internal/db"
	"github.com/gordon2/manualbox/internal/db/gen"
	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/id"
	"github.com/gordon2/manualbox/internal/store"
)

// Document states. A document moves through these as the pipeline works on it.
const (
	// StateUploaded means the bytes are stored and a probe is queued.
	StateUploaded = "uploaded"
	// StateProbing means a worker is reading it.
	StateProbing = "probing"
	// StateAwaitingScope means the probe finished and the user must decide what to
	// process. This is the gate: nothing is spent before it.
	StateAwaitingScope = "awaiting_scope"
	// StateDeclined means the user chose not to process it. The original is kept.
	StateDeclined = "declined"
	// StateReady means nothing further will happen automatically.
	StateReady = "ready"
	// StateFailed means probing failed permanently.
	StateFailed = "failed"
)

// Document kinds. The kind is not cosmetic: receipts and warranties are never
// sent to a cloud provider, so the class must be known before one is called.
const (
	KindManual   = "manual"
	KindReceipt  = "receipt"
	KindWarranty = "warranty"
	KindPhoto    = "photo"
	KindOther    = "other"
)

// Document is an uploaded file belonging to a device.
type Document struct {
	ID         string `json:"id"`
	DeviceID   string `json:"deviceId"`
	BlobSHA256 string `json:"blobSha256"`
	Filename   string `json:"filename,omitempty"`
	MediaType  string `json:"mediaType,omitempty"`
	Kind       string `json:"kind"`
	State      string `json:"state"`
	LastError  string `json:"lastError,omitempty"`

	// Probe results, nil until the document has been probed.
	PageCount          *int  `json:"pageCount,omitempty"`
	Encrypted          *bool `json:"encrypted,omitempty"`
	Tagged             *bool `json:"tagged,omitempty"`
	HasTextLayer       *bool `json:"hasTextLayer,omitempty"`
	MedianCharsPerPage *int  `json:"medianCharsPerPage,omitempty"`
	ContentStartPage   *int  `json:"contentStartPage,omitempty"`
	ContentEndPage     *int  `json:"contentEndPage,omitempty"`

	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	ProbedAt  *time.Time `json:"probedAt,omitempty"`
}

// Probed reports whether the free stages have run.
func (d *Document) Probed() bool { return d.ProbedAt != nil }

// NewDocument is the input to [Service.CreateDocument].
type NewDocument struct {
	DeviceID   string
	BlobSHA256 string
	Filename   string
	MediaType  string
	Kind       string
}

// CreateDocument records an uploaded file against a device.
//
// It is idempotent by content: the same bytes uploaded twice against the same
// device return the existing document rather than creating a second one. That is
// enforced by a unique index, so two concurrent uploads cannot both win.
func (s *Service) CreateDocument(ctx context.Context, in NewDocument) (*Document, bool, error) {
	switch {
	case in.DeviceID == "":
		return nil, false, fmt.Errorf("%w: a document needs a device", ErrInvalid)
	case in.BlobSHA256 == "":
		return nil, false, fmt.Errorf("%w: a document needs stored content", ErrInvalid)
	}
	if in.Kind == "" {
		in.Kind = KindManual
	}

	now := db.Millis(s.now())
	q := gen.New(s.db.Write())
	inserted, err := q.CreateDocument(ctx, gen.CreateDocumentParams{
		ID:         id.New(id.Document),
		DeviceID:   in.DeviceID,
		BlobSha256: in.BlobSHA256,
		Filename:   in.Filename,
		MediaType:  in.MediaType,
		Kind:       in.Kind,
		State:      StateUploaded,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		return nil, false, fmt.Errorf("registry: create document: %w", err)
	}

	row, err := q.GetDocumentByDeviceAndBlob(ctx, gen.GetDocumentByDeviceAndBlobParams{
		DeviceID:   in.DeviceID,
		BlobSha256: in.BlobSHA256,
	})
	if err != nil {
		return nil, false, fmt.Errorf("registry: read back document: %w", err)
	}
	return documentFrom(row), inserted == 1, nil
}

// RecordBlob indexes stored bytes so a document can reference them.
//
// The blob table is the metadata for the content-addressed store on disk, and the
// insert is a no-op when the digest is already present: identical bytes are one
// blob however many documents point at it.
func (s *Service) RecordBlob(ctx context.Context, ref store.Ref, mediaType string) error {
	err := gen.New(s.db.Write()).UpsertBlob(ctx, gen.UpsertBlobParams{
		Sha256:    ref.SHA256,
		SizeBytes: ref.Size,
		MediaType: mediaType,
		CreatedAt: db.Millis(s.now()),
	})
	if err != nil {
		return fmt.Errorf("registry: record blob: %w", err)
	}
	return nil
}

// GetDocument returns one document.
func (s *Service) GetDocument(ctx context.Context, documentID string) (*Document, error) {
	row, err := gen.New(s.db.Read()).GetDocument(ctx, documentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: document %s", ErrNotFound, documentID)
		}
		return nil, fmt.Errorf("registry: get document: %w", err)
	}
	return documentFrom(row), nil
}

// ListDocumentsForDevice returns a device's documents, newest first.
func (s *Service) ListDocumentsForDevice(ctx context.Context, deviceID string) ([]Document, error) {
	rows, err := gen.New(s.db.Read()).ListDocumentsForDevice(ctx, deviceID)
	if err != nil {
		return nil, fmt.Errorf("registry: list documents: %w", err)
	}
	out := make([]Document, 0, len(rows))
	for i := range rows {
		out = append(out, *documentFrom(rows[i]))
	}
	return out, nil
}

// SetDocumentState moves a document to a new state.
func (s *Service) SetDocumentState(ctx context.Context, documentID, state, lastError string) error {
	err := gen.New(s.db.Write()).SetDocumentState(ctx, gen.SetDocumentStateParams{
		State:     state,
		LastError: lastError,
		UpdatedAt: db.Millis(s.now()),
		ID:        documentID,
	})
	if err != nil {
		return fmt.Errorf("registry: set document state: %w", err)
	}
	return nil
}

// SaveProbe records everything the free stages discovered, in one transaction.
//
// All of it or none of it: a document whose row claims it was probed but whose
// pages are missing would look complete and behave as though the manual had no
// languages. The write is also idempotent — page rows and language runs are keyed
// naturally and upserted, and each signal's runs are replaced wholesale — because
// a worker can die after doing the work and have the job run again.
func (s *Service) SaveProbe(ctx context.Context, documentID string, res *doc.Result, state string) error {
	now := db.Millis(s.now())

	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		if err := q.RecordDocumentProbe(ctx, gen.RecordDocumentProbeParams{
			PageCount:          intPtr(res.Info.Pages),
			Encrypted:          boolToInt(res.Info.Encrypted),
			Tagged:             boolToInt(res.Info.Tagged),
			HasTextLayer:       boolToInt(res.HasTextLayer),
			MedianCharsPerPage: intPtr(res.MedianChars),
			ContentStartPage:   intPtrOrNil(res.ContentStart),
			ContentEndPage:     intPtrOrNil(res.ContentEnd),
			State:              state,
			ProbedAt:           &now,
			UpdatedAt:          now,
			ID:                 documentID,
		}); err != nil {
			return fmt.Errorf("record probe: %w", err)
		}

		for i := range res.Pages {
			p := &res.Pages[i]
			lang, source := res.PageLang(p.No)
			if err := q.UpsertDocPage(ctx, gen.UpsertDocPageParams{
				DocumentID:   documentID,
				PageNo:       int64(p.No),
				Chars:        int64(p.Chars),
				Script:       p.Script,
				PageTag:      p.Tag,
				PrintedFolio: intPtrFrom(p.Folio),
				Lang:         lang,
				LangSource:   string(source),
			}); err != nil {
				return fmt.Errorf("save page %d: %w", p.No, err)
			}
		}

		// Every signal's view is stored, not just the reconciled one, so that
		// "this manual also contains FR, IT, ES..." is answerable without
		// re-probing and a conflict stays inspectable afterwards.
		all := make(map[doc.Source][]doc.Run, len(res.BySource)+1)
		for source, runs := range res.BySource {
			all[source] = runs
		}
		all[doc.SourceReconciled] = res.Runs

		for source, runs := range all {
			// Replace rather than merge: a run the latest probe no longer believes
			// in must disappear, not linger from the previous attempt.
			if err := q.DeleteDocLangsBySource(ctx, gen.DeleteDocLangsBySourceParams{
				DocumentID: documentID,
				Source:     string(source),
			}); err != nil {
				return fmt.Errorf("clear %s runs: %w", source, err)
			}
			for _, r := range runs {
				if err := q.UpsertDocLang(ctx, gen.UpsertDocLangParams{
					DocumentID:  documentID,
					Source:      string(source),
					PdfStart:    int64(r.Start),
					PdfEnd:      int64(max(r.End, r.Start)),
					Code:        r.Code,
					Lang:        r.Lang,
					Title:       r.Title,
					PrintedPage: intPtrFrom(r.PrintedPage),
					Confidence:  r.Confidence,
					Conflict:    boolInt(r.Conflict),
					Note:        r.Note,
					CreatedAt:   now,
				}); err != nil {
					return fmt.Errorf("save %s run %s: %w", source, r.Code, err)
				}
			}
		}
		return nil
	})
}

// LanguageRun is one stored language run.
type LanguageRun struct {
	Source      string  `json:"source"`
	Code        string  `json:"code"`
	Lang        string  `json:"lang"`
	Name        string  `json:"name"`
	Title       string  `json:"title,omitempty"`
	Start       int     `json:"start"`
	End         int     `json:"end"`
	Pages       int     `json:"pages"`
	PrintedPage *int    `json:"printedPage,omitempty"`
	Confidence  float64 `json:"confidence"`
	Conflict    bool    `json:"conflict"`
	Note        string  `json:"note,omitempty"`
}

// LanguageRuns returns a document's runs for one signal. Passing
// doc.SourceReconciled gives the map manualbox believes.
func (s *Service) LanguageRuns(ctx context.Context, documentID string, source doc.Source) ([]LanguageRun, error) {
	rows, err := gen.New(s.db.Read()).ListDocLangsBySource(ctx, gen.ListDocLangsBySourceParams{
		DocumentID: documentID,
		Source:     string(source),
	})
	if err != nil {
		return nil, fmt.Errorf("registry: list language runs: %w", err)
	}
	out := make([]LanguageRun, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, LanguageRun{
			Source: r.Source, Code: r.Code, Lang: r.Lang,
			Name:  doc.DisplayName(r.Lang),
			Title: r.Title,
			Start: int(r.PdfStart), End: int(r.PdfEnd),
			Pages:       pageSpan(r.PdfStart, r.PdfEnd),
			PrintedPage: intFromPtr(r.PrintedPage),
			Confidence:  r.Confidence,
			Conflict:    r.Conflict == 1,
			Note:        r.Note,
		})
	}
	return out, nil
}

func documentFrom(r gen.Document) *Document {
	return &Document{
		ID:                 r.ID,
		DeviceID:           r.DeviceID,
		BlobSHA256:         r.BlobSha256,
		Filename:           r.Filename,
		MediaType:          r.MediaType,
		Kind:               r.Kind,
		State:              r.State,
		LastError:          r.LastError,
		PageCount:          intFromPtr(r.PageCount),
		Encrypted:          boolFromPtr(r.Encrypted),
		Tagged:             boolFromPtr(r.Tagged),
		HasTextLayer:       boolFromPtr(r.HasTextLayer),
		MedianCharsPerPage: intFromPtr(r.MedianCharsPerPage),
		ContentStartPage:   intFromPtr(r.ContentStartPage),
		ContentEndPage:     intFromPtr(r.ContentEndPage),
		CreatedAt:          db.Time(r.CreatedAt),
		UpdatedAt:          db.Time(r.UpdatedAt),
		ProbedAt:           db.TimePtr(r.ProbedAt),
	}
}

// pageSpan is how many pages a stored run covers.
//
// A start of 0 means the signal named a language but could not place it, which the
// schema documents and the API must not turn into a section: the arithmetic span
// reported the fixture's unplaceable HE, AR and CZ index entries as one-page
// Arabic, Hebrew and Czech sections spanning 0-0.
func pageSpan(start, end int64) int {
	if start == 0 {
		return 0
	}
	return int(end - start + 1)
}

func intPtr(n int) *int64 {
	v := int64(n)
	return &v
}

// intPtrOrNil keeps 0 out of the database as a claim. A content range of 0 means
// "not established", which NULL says and 0 does not.
func intPtrOrNil(n int) *int64 {
	if n == 0 {
		return nil
	}
	return intPtr(n)
}

func intPtrFrom(n *int) *int64 {
	if n == nil {
		return nil
	}
	return intPtr(*n)
}

func intFromPtr(n *int64) *int {
	if n == nil {
		return nil
	}
	v := int(*n)
	return &v
}

func boolToInt(b bool) *int64 {
	var v int64
	if b {
		v = 1
	}
	return &v
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func boolFromPtr(n *int64) *bool {
	if n == nil {
		return nil
	}
	v := *n == 1
	return &v
}
