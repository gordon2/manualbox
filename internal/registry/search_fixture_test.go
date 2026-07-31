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
// exactly what doc/bidi.go fixed, and the measurement has turned over: those same 5
// blocks are now 4 found forwards and 1 still found backwards.
//
// The 1 is not slack in the test, it is the residual named everywhere else, and
// this is the second check to land on it independently: page 188 sets the manual's
// support URL and a Hebrew sentence on one line, doc's lineIsRightToLeft decides
// direction by majority of strong characters, the URL wins, and the line is never
// repaired. verify's [verify.minReversibleWords] reports that page from the other
// side, off a different comparison. Both go to zero together, and the day they do
// this test wants 5 and 0.
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

	// The number that matters is that this is not zero. It was zero, for every
	// Hebrew word, and search.md said so.
	if len(fw.Hits) != 4 {
		t.Errorf("%q found %d block(s) of the Hebrew section, want 4 — it found 0 "+
			"before doc/bidi.go, which is the hole search.md records", forwards,
			len(fw.Hits))
	}
	// Exactly one block is still stored backwards, and naming the page is the point:
	// a second one appearing means the repair lost ground somewhere new.
	if len(bw.Hits) != 1 || bw.Hits[0].Page != 188 {
		t.Errorf("the word typed backwards finds %d block(s), want 1 on page 188 — "+
			"the support-URL line whose direction the majority rule gets wrong; "+
			"was 5 before doc/bidi.go and wants to be 0", len(bw.Hits))
	}
}
