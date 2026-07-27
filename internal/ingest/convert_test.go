package ingest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/ingest"
	"github.com/gordon2/manualbox/internal/jobs"
	"github.com/gordon2/manualbox/internal/registry"
	"github.com/gordon2/manualbox/internal/testpdf"
)

// approveAndConvert approves a document and runs the queued conversion job
// synchronously, the way the server's worker pool would.
func (h *harness) approveAndConvert(t *testing.T, documentID string) {
	t.Helper()
	ctx := context.Background()

	if _, err := h.ingest.Approve(ctx, documentID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Approving must move the document out of the gate immediately, or a user who
	// has just approved is offered the same decision again while the queue picks
	// the job up.
	pending, err := h.registry.GetDocument(ctx, documentID)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	if pending.State != registry.StateConverting {
		t.Fatalf("state after approving = %q, want %q", pending.State, registry.StateConverting)
	}

	ran, err := h.pool.RunOnce(ctx)
	if err != nil {
		t.Fatalf("run convert job: %v", err)
	}
	if !ran {
		t.Fatal("approving queued no conversion job")
	}

	document, err := h.registry.GetDocument(ctx, documentID)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	if document.State == registry.StateFailed {
		t.Fatalf("conversion failed: %s", document.LastError)
	}
}

func TestApprovingConvertsOnlyTheHouseholdsLanguages(t *testing.T) {
	// The funnel's whole promise: a household that reads German gets the German
	// section and nothing from the four beside it. This is the one failure a reader
	// would notice immediately.
	h := newHarness(t, []string{"de"})
	ctx := context.Background()

	document := h.upload(t, "manual.pdf",
		testpdf.TaggedSections([]string{"EN", "DE", "FR", "IT", "ES"}, 3, true))
	h.runProbe(t, document.ID)
	h.approveAndConvert(t, document.ID)

	after, err := h.registry.GetDocument(ctx, document.ID)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	if after.State != registry.StateReady {
		t.Errorf("state = %q, want %q", after.State, registry.StateReady)
	}

	blocks, err := h.registry.Blocks(ctx, document.ID)
	if err != nil {
		t.Fatalf("blocks: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("the document is ready with no blocks at all")
	}

	for i := range blocks {
		b := &blocks[i]
		if b.Lang != "de" {
			t.Errorf("block %d on page %d is in %q, not German: %q",
				b.Index, b.Page, b.Lang, b.Text)
		}
		// A section's own body text names its code, so a leaked section is visible
		// in the text rather than only in the label.
		for _, foreign := range []string{"Section EN", "Section FR", "Section IT", "Section ES"} {
			if strings.Contains(b.Text, foreign) {
				t.Errorf("a block carries text from a language nobody asked for: %q", b.Text)
			}
		}
	}

	// And the German section did arrive, rather than the funnel having emptied
	// everything: three pages of it, each naming itself.
	pages := make(map[int]bool, 3)
	for i := range blocks {
		pages[blocks[i].Page] = true
	}
	if len(pages) != 3 {
		t.Errorf("German converted to %d pages, want 3: %v", len(pages), pages)
	}
}

func TestConvertingTwiceConvergesRatherThanDuplicating(t *testing.T) {
	// A worker can die after doing the work but before recording success, so the
	// reclaimed job runs again. SaveConversion replaces wholesale; this checks the
	// handler as a whole preserves that.
	h := newHarness(t, []string{"de"})
	ctx := context.Background()

	document := h.upload(t, "manual.pdf",
		testpdf.TaggedSections([]string{"DE", "FR"}, 3, true))
	h.runProbe(t, document.ID)

	h.approveAndConvert(t, document.ID)
	first, err := h.registry.Blocks(ctx, document.ID)
	if err != nil {
		t.Fatalf("blocks: %v", err)
	}

	h.approveAndConvert(t, document.ID)
	second, err := h.registry.Blocks(ctx, document.ID)
	if err != nil {
		t.Fatalf("blocks after re-converting: %v", err)
	}

	if len(first) != len(second) {
		t.Errorf("re-converting produced %d blocks where the first run produced %d",
			len(second), len(first))
	}
	for i := range first {
		if i >= len(second) {
			break
		}
		if first[i].Text != second[i].Text || first[i].Index != second[i].Index {
			t.Errorf("block %d changed on re-conversion: %q then %q",
				i, first[i].Text, second[i].Text)
			break
		}
	}
}

func TestAReadyDocumentAlwaysHasItsContent(t *testing.T) {
	// The state and the content land in one transaction, so there is no moment at
	// which a document claims to be readable and is not. Checked by asserting the
	// pairing rather than the ordering, which is what a caller can actually observe:
	// ready implies blocks, and not-ready implies none.
	h := newHarness(t, []string{"de"})
	ctx := context.Background()

	document := h.upload(t, "manual.pdf", testpdf.TaggedSections([]string{"DE"}, 2, false))
	h.runProbe(t, document.ID)

	atGate, err := h.registry.Blocks(ctx, document.ID)
	if err != nil {
		t.Fatalf("blocks: %v", err)
	}
	if len(atGate) != 0 {
		t.Errorf("a document at the gate already has %d blocks; nothing may be "+
			"converted before the user approves it", len(atGate))
	}

	h.approveAndConvert(t, document.ID)

	after, err := h.registry.GetDocument(ctx, document.ID)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	blocks, err := h.registry.Blocks(ctx, document.ID)
	if err != nil {
		t.Fatalf("blocks: %v", err)
	}
	if after.State == registry.StateReady && len(blocks) == 0 {
		t.Error("the document says ready and has no blocks, which reads as an empty manual")
	}
	if after.State != registry.StateReady {
		t.Errorf("state = %q, want %q", after.State, registry.StateReady)
	}
}

func TestApprovingAScanIsRefusedRatherThanConvertedToNothing(t *testing.T) {
	// A user who authorises spending on a scan must be told it needs OCR. Silently
	// converting it to nothing and calling the document ready would show them an
	// empty manual and no reason for it.
	h := newHarness(t, []string{"en"})
	ctx := context.Background()

	document := h.upload(t, "scan.pdf", testpdf.Blank(4))
	h.runProbe(t, document.ID)

	_, err := h.ingest.Approve(ctx, document.ID)
	if err == nil {
		t.Fatal("approving a scan was accepted")
	}
	if !strings.Contains(err.Error(), "OCR") {
		t.Errorf("the refusal does not say why: %v", err)
	}

	after, err := h.registry.GetDocument(ctx, document.ID)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	if after.State != registry.StateAwaitingScope {
		t.Errorf("state = %q after a refused approval, want %q — a refusal must not "+
			"move the document", after.State, registry.StateAwaitingScope)
	}
}

func TestApprovingADocumentInNoneOfTheHouseholdsLanguagesIsRefused(t *testing.T) {
	h := newHarness(t, []string{"de"})
	ctx := context.Background()

	document := h.upload(t, "manual.pdf", testpdf.TaggedSections([]string{"FR", "IT"}, 3, true))
	h.runProbe(t, document.ID)

	if _, err := h.ingest.Approve(ctx, document.ID); err == nil {
		t.Fatal("approving a document with nothing in scope was accepted")
	}
}

func TestAPermanentlyFailedConversionExplainsItselfOnTheDocument(t *testing.T) {
	// The same requirement the probe has one stage earlier: a document whose job
	// gave up must not sit in "converting" for ever while nothing is converting it.
	h := newHarness(t, []string{"de"})
	ctx := context.Background()

	document := h.upload(t, "manual.pdf", testpdf.TaggedSections([]string{"DE"}, 2, false))
	h.runProbe(t, document.ID)

	// Remove the stored bytes so the conversion cannot succeed. Row and content
	// have diverged, which no retry repairs.
	if err := h.store.Delete(document.BlobSHA256); err != nil {
		t.Fatalf("delete blob: %v", err)
	}
	// Queued directly with one attempt, so the first failure is the permanent one.
	// Approve would use the default, and a job with retries left must not declare
	// the document failed.
	if _, err := h.queue.Enqueue(ctx, ingest.JobConvert,
		ingest.ConvertPayload{DocumentID: document.ID},
		jobs.EnqueueOptions{MaxAttempts: 1}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := h.pool.RunOnce(ctx); err != nil {
		t.Fatalf("run convert job: %v", err)
	}

	after, err := h.registry.GetDocument(ctx, document.ID)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	if after.State == registry.StateConverting {
		t.Error("the document is still converting with no job able to convert it")
	}
	if after.State != registry.StateFailed {
		t.Errorf("state = %q, want %q", after.State, registry.StateFailed)
	}
	if after.LastError == "" {
		t.Error("the document carries no explanation of why it failed")
	}
}
