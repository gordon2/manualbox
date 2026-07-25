// Package ingest runs the document pipeline as background work and answers the
// question the pre-flight gate asks.
//
// The division of labour: internal/doc knows how to read a document and says
// nothing about databases; this package persists what it found, reports progress,
// and stops at the gate. Nothing here spends money, calls a model, or touches the
// network — the whole point of the funnel in docs/design/ingest.md is that the
// expensive step is the last one and the user authorises it first.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gordon2/manualbox/internal/config"
	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/jobs"
	"github.com/gordon2/manualbox/internal/registry"
	"github.com/gordon2/manualbox/internal/store"
)

// JobProbe is the job kind that runs stages 0 to 2 over an uploaded document.
const JobProbe = "doc.probe"

// ProbePayload is the job payload.
type ProbePayload struct {
	DocumentID string `json:"documentId"`
}

// Service coordinates the pipeline.
type Service struct {
	cfg      config.Config
	registry *registry.Service
	store    *store.Store
	queue    *jobs.Queue
	log      *slog.Logger
}

// Deps are the collaborators the service needs.
type Deps struct {
	Config   config.Config
	Registry *registry.Service
	Store    *store.Store
	Jobs     *jobs.Queue
	Logger   *slog.Logger
}

// New returns an ingest service.
func New(d Deps) *Service {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	return &Service{
		cfg: d.Config, registry: d.Registry, store: d.Store,
		queue: d.Jobs, log: d.Logger,
	}
}

// Register wires the pipeline's handlers into a worker pool.
func (s *Service) Register(pool *jobs.Pool) {
	pool.Register(JobProbe, s.handleProbe)
}

// EnqueueProbe queues the free stages for a document.
//
// The dedupe key is the document, so uploading the same file twice — or a client
// retrying — does not queue two probes. An identical pending job is a success for
// the caller, since the work will happen either way.
func (s *Service) EnqueueProbe(ctx context.Context, documentID string) (*jobs.Job, error) {
	job, err := s.queue.Enqueue(ctx, JobProbe, ProbePayload{DocumentID: documentID}, jobs.EnqueueOptions{
		DedupeKey: JobProbe + ":" + documentID,
		// Probing is cheap and everything the user sees waits on it, so it runs
		// ahead of any bulk work queued behind it.
		Priority: 10,
	})
	if err != nil && !errors.Is(err, jobs.ErrAlreadyQueued) {
		return nil, fmt.Errorf("ingest: queue probe: %w", err)
	}
	return job, nil
}

// handleProbe runs stages 0 to 2 and stops at the gate.
//
// Idempotent, as every handler must be: a worker can be killed after doing the
// work but before recording success, and the reclaimed job runs again. Re-running
// re-derives the same facts from the same immutable bytes and upserts them, so a
// second run converges rather than duplicating.
func (s *Service) handleProbe(ctx context.Context, job *jobs.Job, report jobs.Reporter) error {
	var payload ProbePayload
	if err := job.Unmarshal(&payload); err != nil {
		return err
	}
	if payload.DocumentID == "" {
		return errors.New("ingest: probe job has no document")
	}

	document, err := s.registry.GetDocument(ctx, payload.DocumentID)
	if err != nil {
		return err
	}

	log := s.log.With("document", document.ID, "device", document.DeviceID)

	if err := report.Progress(ctx, 0.05, "reading the document"); err != nil {
		return err
	}
	if err := s.registry.SetDocumentState(ctx, document.ID, registry.StateProbing, ""); err != nil {
		return err
	}

	path, err := s.store.Path(document.BlobSHA256)
	if err != nil {
		// The blob is gone: the row and the bytes have diverged, which no retry
		// will repair.
		return s.probeFailed(ctx, job, document.ID,
			fmt.Errorf("ingest: document %s content is missing: %w", document.ID, err))
	}

	if err := report.Progress(ctx, 0.2, "looking for a text layer"); err != nil {
		return err
	}

	result, err := doc.Analyze(ctx, path)
	if err != nil {
		return s.probeFailed(ctx, job, document.ID,
			fmt.Errorf("ingest: analyse document %s: %w", document.ID, err))
	}

	if err := report.Progress(ctx, 0.7, s.progressNote(result)); err != nil {
		return err
	}

	// Every probed document stops here in this milestone: conversion does not
	// exist yet, so there is nothing to advance to even for a small document that
	// ingest.max_pages_auto would permit. The gate's own answer about whether
	// approval is needed is computed for the UI by [Service.Gate].
	if err := s.registry.SaveProbe(ctx, document.ID, result, registry.StateAwaitingScope); err != nil {
		return s.probeFailed(ctx, job, document.ID, err)
	}

	log.Info("document probed",
		"pages", result.Info.Pages,
		"text_layer", result.HasTextLayer,
		"languages", len(result.Languages()),
		"unlabelled_pages", result.Unlabelled)

	return report.Progress(ctx, 1, s.progressNote(result))
}

// progressNote describes the outcome in the terms the activity view shows.
func (s *Service) progressNote(res *doc.Result) string {
	switch {
	case res.Info.Encrypted:
		return "the document is password-protected; stored without processing"
	case !res.HasTextLayer:
		return fmt.Sprintf("%d pages, no text layer — needs OCR", res.Info.Pages)
	default:
		scope := res.ScopeFor(s.cfg.Content.Languages)
		return fmt.Sprintf("%d pages, %d languages; %d pages in yours",
			res.Info.Pages, len(res.Languages()), scope.Pages)
	}
}

// probeFailed marks a document as failed once no further attempt will be made,
// and returns the error so the queue can retry until then.
//
// Without this a document whose job exhausts its attempts stays in "probing"
// forever, and the UI truthfully reports what the row says — "reading…" — while
// nothing is reading it. The failure has to be recorded where the user is looking,
// not only in the job row.
func (s *Service) probeFailed(ctx context.Context, job *jobs.Job, documentID string, cause error) error {
	if job.Attempts >= job.MaxAttempts {
		// Deliberately not ctx: on a cancelled context this write is the last
		// chance to leave an explanation behind.
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.registry.SetDocumentState(writeCtx, documentID, registry.StateFailed, cause.Error()); err != nil {
			s.log.Error("recording document failure failed", "document", documentID, "error", err)
		}
	}
	return cause
}
