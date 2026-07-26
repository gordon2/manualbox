package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
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
// languages. The write is also idempotent — page rows, language runs and regions
// are keyed naturally and upserted, and runs and regions are replaced wholesale —
// because a worker can die after doing the work and have the job run again.
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

		if err := saveRegions(ctx, q, documentID, res, now); err != nil {
			return err
		}
		return nil
	})
}

// saveRegions stores the language territories the probe read, inside SaveProbe's
// transaction.
//
// WHETHER TO WRITE AT ALL IS THE DECISION HERE, and it turns on RegionNote rather
// than on len(Regions).
//
// A non-empty RegionNote means positioned text could not be read at all: pdftohtml
// is absent or failed, so the probe has no opinion about regions rather than the
// opinion that there are none. Existing rows are then left exactly as they are.
// Deleting a good region map because an optional tool went missing from the host
// would be destructive, and it is the likely case — poppler is optional at runtime
// here, so the same document can be probed with regions available and then without.
//
// An empty RegionNote means the probe did read the document, so its answer replaces
// what was there even when that answer is no regions at all. An encrypted document
// and one with no text layer both land here legitimately: doc.Analyze returns early
// for them with no regions and no note, and "this document has none" is a real
// result that must overwrite a stale map rather than hide behind it.
//
// Replace, not merge: source is part of the primary key and a region's attribution
// can change between probes, so an upsert alone would leave the superseded row
// behind at the same x0 and the page would report itself twice. The delete is
// load-bearing, not tidying. See the note at the foot of 00004_doc_regions.sql.
func saveRegions(ctx context.Context, q *gen.Queries, documentID string, res *doc.Result, now int64) error {
	if res.RegionNote != "" {
		return nil
	}

	if err := q.DeleteDocRegions(ctx, documentID); err != nil {
		return fmt.Errorf("clear regions: %w", err)
	}
	for i := range res.Regions {
		r := &res.Regions[i]
		if err := q.UpsertDocRegion(ctx, gen.UpsertDocRegionParams{
			DocumentID: documentID,
			Source:     string(r.Source),
			Page:       int64(r.Page),
			X0:         roundCoord(r.X0),
			X1:         roundCoord(r.X1),
			Code:       r.Code,
			Lang:       r.Lang,
			Chars:      int64(r.Chars),
			Runs:       int64(r.Runs),
			Conflict:   boolInt(r.Conflict),
			Note:       r.Note,
			CreatedAt:  now,
		}); err != nil {
			return fmt.Errorf("save region on page %d at x %.0f: %w", r.Page, r.X0, err)
		}
	}
	return nil
}

// roundCoord narrows a region's float coordinate to the integer the schema stores.
//
// Rounded, not truncated. A float in a primary key would need two probes to produce
// bit-identical floats before the upsert converged, and one unit here is one pixel
// of a pdftoppm -r 108 raster, so sub-unit precision describes nothing about a
// column boundary. Truncation would instead bias every edge left by up to a unit.
// Negative coordinates are clamped: the schema requires x0 >= 0, and a run parked
// off the left edge of the page is furniture, not a column that starts at -3.
func roundCoord(v float64) int64 {
	if v <= 0 {
		return 0
	}
	return int64(math.Round(v))
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

// PageFact is what the probe stored about one page: the facts that genuinely are
// per page, which is why they did not move to doc_regions. Language is here too,
// and it is the one field that can be absent where a region names something: a
// page holding three languages has no honest per-page answer, so a
// parallel-columns manual stores ” on every page and its languages live only in
// its regions.
type PageFact struct {
	Page  int `json:"page"`
	Chars int `json:"chars"`
	// Script is the dominant Unicode script, and Tag the language code printed on
	// the page, both empty when nothing was read.
	Script string `json:"script,omitempty"`
	Tag    string `json:"tag,omitempty"`
	// PrintedFolio is the page number the page prints, which is usually offset from
	// the PDF's own.
	PrintedFolio *int `json:"printedFolio,omitempty"`
	// Lang is the reconciled per-page language, empty when the per-page signals
	// named nothing, and LangSource says which signal named it.
	Lang       string `json:"lang,omitempty"`
	LangSource string `json:"langSource,omitempty"`
}

// Pages returns the stored per-page facts in page order.
//
// The gate reads these to answer two questions that need a page's size rather than
// its language: how many characters a language covers when no regions were stored,
// and how many content pages carry text that nothing could name.
func (s *Service) Pages(ctx context.Context, documentID string) ([]PageFact, error) {
	rows, err := gen.New(s.db.Read()).ListDocPages(ctx, documentID)
	if err != nil {
		return nil, fmt.Errorf("registry: list pages: %w", err)
	}
	out := make([]PageFact, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, PageFact{
			Page:         int(r.PageNo),
			Chars:        int(r.Chars),
			Script:       r.Script,
			Tag:          r.PageTag,
			PrintedFolio: intFromPtr(r.PrintedFolio),
			Lang:         r.Lang,
			LangSource:   r.LangSource,
		})
	}
	return out, nil
}

// Region is one stored language territory on a page.
//
// X0 and X1 are integers here because that is what is stored, and the coordinate
// space is poppler's: one unit is one pixel of a pdftoppm -r 108 raster, 1.5 times
// the PDF's own points. A whole-page region runs from 0 to the page width, which is
// why no field says whether a region is boxed — a caller clipping to the box gets
// the whole page and needs no special case.
type Region struct {
	Page     int    `json:"page"`
	X0       int    `json:"x0"`
	X1       int    `json:"x1"`
	Source   string `json:"source"`
	Code     string `json:"code"`
	Lang     string `json:"lang"`
	Name     string `json:"name"`
	Chars    int    `json:"chars"`
	Runs     int    `json:"runs"`
	Conflict bool   `json:"conflict"`
	Note     string `json:"note,omitempty"`
}

// Regions returns a document's language territories in reading order: down the
// page, then left to right across it.
//
// Empty is not the same claim as absent. A document probed without pdftohtml
// available has no regions stored and its per-page language map is still complete,
// so a caller must not read an empty result as "this manual has one language".
func (s *Service) Regions(ctx context.Context, documentID string) ([]Region, error) {
	rows, err := gen.New(s.db.Read()).ListDocRegions(ctx, documentID)
	if err != nil {
		return nil, fmt.Errorf("registry: list regions: %w", err)
	}
	out := make([]Region, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, Region{
			Page: int(r.Page),
			X0:   int(r.X0), X1: int(r.X1),
			Source: r.Source,
			Code:   r.Code,
			Lang:   r.Lang,
			// The UI shows "Ukrainian", not "uk", and the manual's own label may be
			// neither: it prints UA.
			Name:     doc.DisplayName(r.Lang),
			Chars:    int(r.Chars),
			Runs:     int(r.Runs),
			Conflict: r.Conflict == 1,
			Note:     r.Note,
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
