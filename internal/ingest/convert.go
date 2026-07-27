package ingest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/jobs"
	"github.com/gordon2/manualbox/internal/registry"
)

// JobConvert is the job kind that turns the pages in scope into readable blocks.
// It is the first work in this pipeline the user has to authorise, which is what
// [Service.Approve] does; nothing enqueues it on its own.
const JobConvert = "doc.convert"

// ConvertPayload is the job payload.
//
// It carries the document and nothing else — in particular not the languages.
// That is deliberate twice over. The household is read from configuration in the
// handler because the gate showed the user a specific scope and approving must
// mean that scope rather than one a caller chose; and because the dedupe key is
// the document, a payload that could vary would let two different scopes collapse
// into whichever job was queued first.
type ConvertPayload struct {
	DocumentID string `json:"documentId"`
}

// EnqueueConvert queues the conversion of a document.
//
// The dedupe key is the document, so approving twice — or a client retrying —
// does not convert twice. Priority is below the probe's: a probe is what a user is
// waiting on to make a decision, a conversion is work they have already decided to
// have done.
func (s *Service) EnqueueConvert(ctx context.Context, documentID string) (*jobs.Job, error) {
	job, err := s.queue.Enqueue(ctx, JobConvert, ConvertPayload{DocumentID: documentID}, jobs.EnqueueOptions{
		DedupeKey: JobConvert + ":" + documentID,
		Priority:  5,
	})
	if err != nil && !errors.Is(err, jobs.ErrAlreadyQueued) {
		return nil, fmt.Errorf("ingest: queue conversion: %w", err)
	}
	return job, nil
}

// handleConvert converts the pages in scope and moves the document to ready.
//
// Idempotent, as every handler must be. [doc.Convert] is a pure function of the
// document's bytes and the household's languages, and [registry.Service.SaveConversion]
// replaces a document's blocks and figures wholesale inside one transaction, so a
// worker killed after doing the work leaves a reclaimed job that converges on the
// same rows rather than doubling them.
//
// # Why the probe's result is re-derived rather than read back
//
// [doc.Convert] needs a *doc.Result, and the probe stored its findings as rows
// rather than keeping the Result whole. This re-runs [doc.Analyze] instead of
// rebuilding one from doc_pages and doc_regions. Analyze is a pure function of the
// bytes — it is what makes the probe idempotent in the first place — whereas a
// reconstruction would be a second implementation of the same object, free to
// drift from the real one in ways no test compares. Measured on the two real
// manuals: about 3.6 s for the 560-page sequential one and about 8 s for the
// 68-page parallel-columns one, against conversions of 13 s and 26 s. It is a
// visible share of a job the user has authorised, and it buys the guarantee that
// what is converted is what the document says today.
func (s *Service) handleConvert(ctx context.Context, job *jobs.Job, report jobs.Reporter) error {
	var payload ConvertPayload
	if err := job.Unmarshal(&payload); err != nil {
		return err
	}
	if payload.DocumentID == "" {
		return errors.New("ingest: convert job has no document")
	}

	document, err := s.registry.GetDocument(ctx, payload.DocumentID)
	if err != nil {
		return err
	}
	log := s.log.With("document", document.ID, "device", document.DeviceID)

	// Set on every attempt rather than only the first: a reclaimed job is a
	// document that is being converted again, whatever the row said when it was
	// picked up.
	if err := s.registry.SetDocumentState(ctx, document.ID, registry.StateConverting, ""); err != nil {
		return err
	}
	if err := report.Progress(ctx, 0.02, "preparing to convert"); err != nil {
		return err
	}

	path, err := s.store.Path(document.BlobSHA256)
	if err != nil {
		return s.jobFailed(ctx, job, document.ID,
			fmt.Errorf("ingest: document %s content is missing: %w", document.ID, err))
	}

	if err := report.Progress(ctx, 0.05, "re-reading the document"); err != nil {
		return err
	}
	started := time.Now()
	result, err := doc.Analyze(ctx, path)
	if err != nil {
		return s.jobFailed(ctx, job, document.ID,
			fmt.Errorf("ingest: re-read document %s: %w", document.ID, err))
	}
	analyzed := time.Since(started)

	// The household from configuration, never from a caller. This is the promise
	// the gate made concrete: it showed a specific scope, and approving means that
	// scope.
	household := s.cfg.Content.Languages
	scope := result.ScopeFor(household)
	if err := report.Progress(ctx, 0.15, fmt.Sprintf(
		"converting %d of %d pages", scope.Pages, result.Info.Pages)); err != nil {
		return err
	}

	// One call with no progress inside it, which is the honest shape: internal/doc
	// takes no callback, and inventing per-page movement here would mean either
	// changing that package or reporting a fraction nothing measured. The two
	// stages either side of it are what a watching user sees move.
	conv, err := doc.Convert(ctx, path, result, household)
	if err != nil {
		return s.jobFailed(ctx, job, document.ID,
			fmt.Errorf("ingest: convert document %s: %w", document.ID, err))
	}
	converted := time.Since(started) - analyzed

	if err := report.Progress(ctx, 0.9, fmt.Sprintf(
		"saving %d blocks and %d pictures", len(conv.Blocks), len(conv.Figures))); err != nil {
		return err
	}

	// The state is passed into SaveConversion rather than set after it, so that the
	// claim and the content it rests on land in one transaction. A document cannot
	// say "ready" with no blocks behind it.
	if err := s.registry.SaveConversion(ctx, document.ID, conv.Blocks,
		figuresOf(conv), s.store, registry.StateReady); err != nil {
		return s.jobFailed(ctx, job, document.ID,
			fmt.Errorf("ingest: save conversion of document %s: %w", document.ID, err))
	}

	log.Info("document converted",
		"blocks", len(conv.Blocks),
		"figures", len(conv.Figures),
		"pages", len(conv.Pages),
		"languages", len(conv.Scope.Languages),
		"notes", len(conv.Notes),
		"analyze_ms", analyzed.Milliseconds(),
		"convert_ms", converted.Milliseconds())
	for _, note := range conv.Notes {
		log.Warn("conversion note", "note", note)
	}

	return report.Progress(ctx, 1, conv.Summary())
}

// figuresOf drops the language attribution a conversion worked out and hands the
// pictures on as plain figures.
//
// Nothing is lost by that: doc_figures deliberately has no language column,
// because a picture belonging to no language belongs to every language, and the
// attribution is re-derived at read time from the same stored regions
// [doc.Convert] used. See [registry.Service.FiguresByLang].
func figuresOf(conv *doc.Conversion) []doc.Figure {
	out := make([]doc.Figure, 0, len(conv.Figures))
	for i := range conv.Figures {
		out = append(out, conv.Figures[i].Figure)
	}
	return out
}
