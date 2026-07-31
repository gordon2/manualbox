package registry_test

import (
	"context"
	"os"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/extern"
	"github.com/gordon2/manualbox/internal/fixture"
	"github.com/gordon2/manualbox/internal/registry"
)

// TestHebrewIsFoundTypedForwards is the one search question the corpus could not
// answer, and it is a search test only in where it fails: the index was always
// right and what it held was backwards.
//
// docs/design/search.md recorded the hole as measured — the word for "manual" was
// "findable by a query typed backwards (5 blocks) and not by one a Hebrew speaker
// would type (0 blocks)" — and named it as extraction's, not the index's. That is
// exactly what doc/bidi.go fixed, and the measurement is now the exact inverse of
// what that sentence describes: **5 blocks forwards and 0 backwards**.
//
// It got there in two steps and the middle one is worth keeping, because it is why
// this test asserts a page number. When the repair first landed it read 4 and 1: one
// block was still stored backwards, page 188, where the support URL and a Hebrew
// sentence share a line and doc's lineIsRightToLeft gave the line to its Latin
// majority. verify reported the same page from the other side off a comparison that
// shares no code with this one. Letting the region's language decide direction closed
// both at once, and page 188 now reads
// `למדריך אלקטרוני מפורט, יש לעיין בכתובת הבאה: https://…`.
//
// It converts the document for Hebrew alone rather than for the household of 34,
// because one language is all this question needs and it is the whole cost.
func TestHebrewIsFoundTypedForwards(t *testing.T) {
	if os.Getenv(fixture.EnableEnv) == "" {
		t.Skipf("set %s=1 to download the fixture and run the real-document tests",
			fixture.EnableEnv)
	}
	for _, tool := range []extern.Tool{extern.PDFInfo, extern.PDFToText, extern.PDFToHTML} {
		if !extern.Available(tool) {
			t.Skipf("%s is not installed", tool.Name)
		}
	}

	ctx := context.Background()
	manifest, err := fixture.Load(fixturesDir, "dreame-l40-ultra")
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	path, err := manifest.Fetch(ctx)
	if err != nil {
		t.Fatalf("fetch fixture: %v", err)
	}
	res, err := doc.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	conv, err := doc.Convert(ctx, path, res, []string{"he"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	t.Logf("Hebrew conversion: %s", conv.Summary())

	s := newService(t)
	docID := newDocumentOnDevice(t, s, "Robot vacuum", "dreame-l40-ultra.pdf", "a")
	if err := s.SaveConversion(ctx, docID, conv.Blocks, nil, nil,
		registry.StateReady); err != nil {
		t.Fatalf("save conversion: %v", err)
	}

	// מדריך, "manual" — the word search.md measured the hole on. Its own reverse is
	// ךירדמ, which is what the index used to hold and what a Hebrew speaker would
	// never type.
	const (
		forwards  = "מדריך"
		backwards = "ךירדמ"
	)
	fw := search(t, s, registry.SearchQuery{Text: forwards})
	bw := search(t, s, registry.SearchQuery{Text: backwards})
	t.Logf("%q found %d block(s) by %s, %q found %d", forwards, len(fw.Hits), fw.Mode,
		backwards, len(bw.Hits))
	for i := range fw.Hits {
		t.Logf("  forwards  page %d: %s", fw.Hits[i].Page, fw.Hits[i].Snippet)
	}
	for i := range bw.Hits {
		t.Logf("  backwards page %d: %s", bw.Hits[i].Page, bw.Hits[i].Snippet)
	}

	// The number that matters is that this is not zero. It was zero, for every Hebrew
	// word, and search.md said so.
	if len(fw.Hits) != 5 {
		t.Errorf("%q found %d block(s) of the Hebrew section, want 5 — it found 0 "+
			"before doc/bidi.go and 4 while a line decided its own direction, and it "+
			"is the hole search.md records", forwards, len(fw.Hits))
	}
	// And nothing is stored backwards any more. Naming the page in the failure is the
	// point: page 188 was the last one, so if this comes back it says whether the same
	// line lost ground or a new one did.
	if len(bw.Hits) != 0 {
		for i := range bw.Hits {
			t.Errorf("still backwards on page %d: %s", bw.Hits[i].Page, bw.Hits[i].Snippet)
		}
		t.Errorf("the word typed backwards finds %d block(s), want 0 — it found 5 "+
			"before doc/bidi.go and 1, on page 188, while a line's own characters "+
			"decided its direction", len(bw.Hits))
	}
}
