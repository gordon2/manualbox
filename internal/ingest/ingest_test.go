package ingest_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/gordon2/manualbox/internal/config"
	"github.com/gordon2/manualbox/internal/db"
	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/extern"
	"github.com/gordon2/manualbox/internal/ingest"
	"github.com/gordon2/manualbox/internal/jobs"
	"github.com/gordon2/manualbox/internal/registry"
	"github.com/gordon2/manualbox/internal/store"
	"github.com/gordon2/manualbox/internal/testpdf"
)

// These tests drive the whole pipeline the way the server does: store an upload,
// run the probe job, then read the gate. They need poppler, which CI installs, and
// they generate their own PDFs so nothing is downloaded and nothing is committed.

type harness struct {
	registry *registry.Service
	ingest   *ingest.Service
	store    *store.Store
	queue    *jobs.Queue
	pool     *jobs.Pool
	// db is here for one purpose: deleting a document's regions, which is how a
	// host without pdftohtml is simulated without uninstalling poppler.
	db *db.DB
}

func newHarness(t *testing.T, household []string) *harness {
	t.Helper()
	if !extern.Available(extern.PDFInfo) || !extern.Available(extern.PDFToText) {
		t.Skip("poppler is not installed")
	}

	ctx := context.Background()
	database, err := db.Open(ctx, db.Options{Path: filepath.Join(t.TempDir(), "ingest.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	blobs, err := store.New(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	cfg := config.Default()
	cfg.Content.Languages = household
	// Low enough that the multi-language fixtures in these tests exceed it, which
	// is the case the gate exists for.
	cfg.Ingest.MaxPagesAuto = 8

	reg := registry.New(database, registry.Options{})
	queue := jobs.NewQueue(database, nil)
	t.Cleanup(func() { queue.Broker().Close() })

	svc := ingest.New(ingest.Deps{Config: cfg, Registry: reg, Store: blobs, Jobs: queue})
	pool := jobs.NewPool(queue, cfg.Jobs, nil)
	svc.Register(pool)

	return &harness{registry: reg, ingest: svc, store: blobs, queue: queue, pool: pool, db: database}
}

// upload stores a generated document against a new device and returns the
// document, exactly as the HTTP handler would.
func (h *harness) upload(t *testing.T, name string, d testpdf.Doc) *registry.Document {
	t.Helper()
	ctx := context.Background()

	device, err := h.registry.CreateDevice(ctx, registry.NewDevice{Name: "Test device"})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	ref, err := h.store.Put(ctx, bytes.NewReader(d.Build()))
	if err != nil {
		t.Fatalf("store upload: %v", err)
	}
	if err := h.registry.RecordBlob(ctx, ref, "application/pdf"); err != nil {
		t.Fatalf("record blob: %v", err)
	}

	document, _, err := h.registry.CreateDocument(ctx, registry.NewDocument{
		DeviceID: device.ID, BlobSHA256: ref.SHA256,
		Filename: name, MediaType: "application/pdf", Kind: registry.KindManual,
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	return document
}

// runProbe executes the queued probe job synchronously.
func (h *harness) runProbe(t *testing.T, documentID string) {
	t.Helper()
	ctx := context.Background()

	if _, err := h.ingest.EnqueueProbe(ctx, documentID); err != nil {
		t.Fatalf("enqueue probe: %v", err)
	}
	ran, err := h.pool.RunOnce(ctx)
	if err != nil {
		t.Fatalf("run probe job: %v", err)
	}
	if !ran {
		t.Fatal("no probe job was queued")
	}

	// A handler that failed leaves the reason on the document, which is far more
	// useful in a test failure than a bare assertion later on.
	document, err := h.registry.GetDocument(ctx, documentID)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	if document.State == registry.StateFailed {
		t.Fatalf("probe failed: %s", document.LastError)
	}
}

func TestProbeBuildsTheLanguageMapAndStopsAtTheGate(t *testing.T) {
	h := newHarness(t, []string{"en", "de"})
	ctx := context.Background()

	// Five languages, three pages each, with a contents table: the shape of a real
	// multi-language appliance manual in miniature.
	document := h.upload(t, "manual.pdf",
		testpdf.TaggedSections([]string{"EN", "DE", "FR", "IT", "ES"}, 3, true))

	h.runProbe(t, document.ID)

	gate, err := h.ingest.Gate(ctx, document.ID)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}

	if gate.State != registry.StateAwaitingScope {
		t.Errorf("state = %q, want %q: the pipeline must stop and ask",
			gate.State, registry.StateAwaitingScope)
	}
	if !gate.Probed {
		t.Error("gate reports the document as unprobed")
	}
	if gate.Pages != 16 {
		t.Errorf("pages = %d, want 16 (1 contents + 5 sections x 3)", gate.Pages)
	}
	if !gate.HasTextLayer {
		t.Error("has text layer = false, but the document is generated with text")
	}

	if len(gate.InScope) != 2 {
		t.Errorf("in scope = %d languages, want 2 (en, de): %+v", len(gate.InScope), gate.InScope)
	}
	if len(gate.Other) != 3 {
		t.Errorf("other = %d languages, want 3 (fr, it, es): %+v", len(gate.Other), gate.Other)
	}
	if gate.ScopePages != 6 {
		t.Errorf("scope pages = %d, want 6", gate.ScopePages)
	}

	// A 16-page document against max_pages_auto of 8 must require approval.
	if !gate.RequiresApproval {
		t.Errorf("requires approval = false for a %d-page document with max_pages_auto=%d",
			gate.Pages, gate.MaxPagesAuto)
	}

	// No provider is configured, so there must be no cost figure — and a reason.
	if gate.Cost.Available {
		t.Error("a cost estimate was offered with no provider configured")
	}
	if gate.Cost.Reason == "" {
		t.Error("cost is unavailable but no reason was given")
	}
}

func TestProbeIsIdempotent(t *testing.T) {
	// A worker can be killed after doing its work but before recording success, so
	// the reclaimed job runs again. Running twice must converge, not duplicate.
	h := newHarness(t, []string{"en"})
	ctx := context.Background()

	document := h.upload(t, "manual.pdf",
		testpdf.TaggedSections([]string{"EN", "DE"}, 3, true))

	h.runProbe(t, document.ID)
	first, err := h.ingest.Gate(ctx, document.ID)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}

	h.runProbe(t, document.ID)
	second, err := h.ingest.Gate(ctx, document.ID)
	if err != nil {
		t.Fatalf("gate after re-probe: %v", err)
	}

	if first.Pages != second.Pages || first.ScopePages != second.ScopePages {
		t.Errorf("re-probing changed the result: %d/%d pages then %d/%d",
			first.ScopePages, first.Pages, second.ScopePages, second.Pages)
	}
	if len(first.InScope) != len(second.InScope) || len(first.Other) != len(second.Other) {
		t.Errorf("re-probing changed the language map: %d+%d then %d+%d",
			len(first.InScope), len(first.Other), len(second.InScope), len(second.Other))
	}
}

func TestUploadingTheSameBytesTwiceIsOneDocument(t *testing.T) {
	h := newHarness(t, []string{"en"})
	ctx := context.Background()

	d := testpdf.TaggedSections([]string{"EN"}, 2, false)
	device, err := h.registry.CreateDevice(ctx, registry.NewDevice{Name: "Kettle"})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	var ids []string
	for range 2 {
		ref, err := h.store.Put(ctx, bytes.NewReader(d.Build()))
		if err != nil {
			t.Fatalf("store: %v", err)
		}
		if err := h.registry.RecordBlob(ctx, ref, "application/pdf"); err != nil {
			t.Fatalf("record blob: %v", err)
		}
		document, created, err := h.registry.CreateDocument(ctx, registry.NewDocument{
			DeviceID: device.ID, BlobSHA256: ref.SHA256, Filename: "m.pdf", Kind: registry.KindManual,
		})
		if err != nil {
			t.Fatalf("create document: %v", err)
		}
		ids = append(ids, document.ID)
		if len(ids) == 2 && created {
			t.Error("the second upload of identical bytes reported itself as new")
		}
	}

	if ids[0] != ids[1] {
		t.Errorf("identical uploads produced two documents: %s and %s", ids[0], ids[1])
	}
	documents, err := h.registry.ListDocumentsForDevice(ctx, device.ID)
	if err != nil {
		t.Fatalf("list documents: %v", err)
	}
	if len(documents) != 1 {
		t.Errorf("device has %d documents, want 1", len(documents))
	}
}

func TestScanWithNoTextLayerIsReportedNotFailed(t *testing.T) {
	// A scan is a normal input, not an error. The probe must record that there is
	// no text layer and say so, leaving OCR as a separate decision.
	h := newHarness(t, []string{"en"})
	ctx := context.Background()

	document := h.upload(t, "scan.pdf", testpdf.Blank(4))
	h.runProbe(t, document.ID)

	gate, err := h.ingest.Gate(ctx, document.ID)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if gate.State == registry.StateFailed {
		t.Errorf("a scan was treated as a failure: %s", gate.State)
	}
	if gate.HasTextLayer {
		t.Error("has text layer = true for a document with no text")
	}
	if gate.Pages != 4 {
		t.Errorf("pages = %d, want 4", gate.Pages)
	}
	if len(gate.InScope) != 0 {
		t.Errorf("languages were claimed for a document with no text: %+v", gate.InScope)
	}
	if gate.Summary == "" {
		t.Error("no summary explaining what happened")
	}
}

func TestEverySignalsViewIsStored(t *testing.T) {
	// "This manual also contains FR, IT, ES..." must be answerable without
	// re-probing, and a disagreement must stay inspectable afterwards. That means
	// each signal's own runs are persisted, not just the reconciled ones.
	h := newHarness(t, []string{"en"})
	ctx := context.Background()

	document := h.upload(t, "manual.pdf",
		testpdf.TaggedSections([]string{"EN", "DE", "FR"}, 3, true))
	h.runProbe(t, document.ID)

	for _, source := range []struct {
		name string
		want int
	}{
		{"page-tag", 3},
		{"index", 3},
		{"reconciled", 3},
	} {
		runs, err := h.registry.LanguageRuns(ctx, document.ID, doc.Source(source.name))
		if err != nil {
			t.Fatalf("language runs for %s: %v", source.name, err)
		}
		if len(runs) != source.want {
			t.Errorf("%s produced %d runs, want %d: %+v", source.name, len(runs), source.want, runs)
		}
	}
}

func TestAPermanentlyFailedProbeExplainsItselfOnTheDocument(t *testing.T) {
	// A job that exhausts its attempts must leave the reason where the user is
	// looking. Without this the document stays in "probing" for ever and the UI
	// truthfully reports "reading…" while nothing is reading it — which is worse
	// than an error, because it never resolves.
	h := newHarness(t, []string{"en"})
	ctx := context.Background()

	document := h.upload(t, "manual.pdf", testpdf.TaggedSections([]string{"EN"}, 2, false))

	// Remove the stored bytes so the probe cannot succeed. Row and content have
	// diverged, which no retry repairs.
	if err := h.store.Delete(document.BlobSHA256); err != nil {
		t.Fatalf("delete blob: %v", err)
	}

	// One attempt, so the first failure is the permanent one.
	if _, err := h.queue.Enqueue(ctx, ingest.JobProbe,
		ingest.ProbePayload{DocumentID: document.ID},
		jobs.EnqueueOptions{MaxAttempts: 1}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := h.pool.RunOnce(ctx); err != nil {
		t.Fatalf("run job: %v", err)
	}

	after, err := h.registry.GetDocument(ctx, document.ID)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	if after.State != registry.StateFailed {
		t.Errorf("state = %q, want %q — a document whose probe gave up must say so",
			after.State, registry.StateFailed)
	}
	if after.LastError == "" {
		t.Error("the document carries no explanation of why it failed")
	}
}

func TestAProbeThatWillRetryDoesNotMarkTheDocumentFailed(t *testing.T) {
	// The converse: while attempts remain, the document must not be declared
	// failed. Doing so would flash an error at the user for a job that is about to
	// succeed on its next attempt.
	h := newHarness(t, []string{"en"})
	ctx := context.Background()

	document := h.upload(t, "manual.pdf", testpdf.TaggedSections([]string{"EN"}, 2, false))
	if err := h.store.Delete(document.BlobSHA256); err != nil {
		t.Fatalf("delete blob: %v", err)
	}

	if _, err := h.queue.Enqueue(ctx, ingest.JobProbe,
		ingest.ProbePayload{DocumentID: document.ID},
		jobs.EnqueueOptions{MaxAttempts: 3}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := h.pool.RunOnce(ctx); err != nil {
		t.Fatalf("run job: %v", err)
	}

	after, err := h.registry.GetDocument(ctx, document.ID)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	if after.State == registry.StateFailed {
		t.Errorf("document was declared failed on attempt 1 of 3")
	}
}

func TestDecliningKeepsTheDocument(t *testing.T) {
	// Declining is a decision about processing, never about storage.
	h := newHarness(t, []string{"en"})
	ctx := context.Background()

	document := h.upload(t, "manual.pdf", testpdf.TaggedSections([]string{"FR"}, 3, false))
	h.runProbe(t, document.ID)

	if err := h.ingest.Decline(ctx, document.ID); err != nil {
		t.Fatalf("decline: %v", err)
	}

	after, err := h.registry.GetDocument(ctx, document.ID)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	if after.State != registry.StateDeclined {
		t.Errorf("state = %q, want %q", after.State, registry.StateDeclined)
	}
	if !h.store.Exists(after.BlobSHA256) {
		t.Error("declining deleted the original; the upload must be kept regardless")
	}
}
