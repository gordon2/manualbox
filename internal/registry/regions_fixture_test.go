package registry_test

import (
	"context"
	"os"
	"sort"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/extern"
	"github.com/gordon2/manualbox/internal/fixture"
	"github.com/gordon2/manualbox/internal/registry"
)

const fixturesDir = "../../testdata/fixtures"

// TestColumnManualRegionsSurviveStorage is docs/design/regions.md's ACCEPTANCE
// CRITERION, which is deliberately not "the migration applies": the pipeline must
// store and read back the real parallel-columns manual's five languages across its
// parallel columns.
//
// It runs the whole seam this commit builds -- doc.Analyze on the actual PDF, then
// SaveProbe, then the read-back -- because every other test in this package feeds
// SaveProbe regions typed by hand, and a struct built in a test cannot show that
// what internal/doc really produces for a 68-page manual survives a round trip
// through a STRICT table with an integer primary key.
//
// The eight pages a human compared against their rendered images are the ones held
// to their column count; the manifest's other pages were produced by the detector
// and holding it to those would be circular. See the provenance note in the
// manifest.
func TestColumnManualRegionsSurviveStorage(t *testing.T) {
	if os.Getenv(fixture.EnableEnv) == "" {
		t.Skipf("set %s=1 to download the fixture and run the real-document tests", fixture.EnableEnv)
	}
	for _, tool := range []extern.Tool{extern.PDFInfo, extern.PDFToText, extern.PDFToHTML} {
		if !extern.Available(tool) {
			t.Skipf("%s is not installed", tool.Name)
		}
	}

	ctx := context.Background()
	manifest, err := fixture.Load(fixturesDir, "thomas-drybox-amfibia")
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
	if res.RegionNote != "" {
		t.Fatalf("regions were not read: %s", res.RegionNote)
	}
	if len(res.Regions) == 0 {
		t.Fatal("the column manual produced no regions")
	}

	s := newService(t)
	docID := newProbedDocument(t, s, "99")
	if err := s.SaveProbe(ctx, docID, res, registry.StateAwaitingScope); err != nil {
		t.Fatalf("save probe: %v", err)
	}

	stored, err := s.Regions(ctx, docID)
	if err != nil {
		t.Fatalf("read back regions: %v", err)
	}

	// Nothing may be lost to the integer primary key. Two same-language columns on
	// one page are the case that collides under doc_langs' key, and this document
	// really sets them, so a short read-back is the failure this table exists to
	// prevent rather than a rounding curiosity.
	if len(stored) != len(res.Regions) {
		t.Errorf("stored %d regions, read back %d: %d were lost, most likely to a "+
			"primary-key collision", len(res.Regions), len(stored), len(res.Regions)-len(stored))
	}

	// The five languages the manifest records as ground truth.
	got := make(map[string]bool)
	for i := range stored {
		if stored[i].Lang != "" {
			got[doc.BaseLanguage(stored[i].Lang)] = true
		}
	}
	for _, want := range manifest.Languages {
		if !got[want] {
			names := make([]string, 0, len(got))
			for l := range got {
				names = append(names, l)
			}
			sort.Strings(names)
			t.Errorf("%s did not survive storage; read back %v", want, names)
		}
	}

	// Characters, not pages, is the unit -- and it must be preserved exactly, since
	// it is what the pre-flight gate will price the job from.
	var wantChars, gotChars int
	for i := range res.Regions {
		wantChars += res.Regions[i].Chars
	}
	for i := range stored {
		gotChars += stored[i].Chars
	}
	if gotChars != wantChars {
		t.Errorf("characters survived as %d, want %d", gotChars, wantChars)
	}

	// Column for column on the human-verified pages: every region internal/doc put
	// on such a page must come back, at the rounded x0 it went in with.
	byPage := make(map[int][]registry.Region, len(stored))
	for i := range stored {
		byPage[stored[i].Page] = append(byPage[stored[i].Page], stored[i])
	}
	for _, fact := range manifest.VerifiedPages() {
		var want []doc.Region
		for i := range res.Regions {
			if res.Regions[i].Page == fact.Page {
				want = append(want, res.Regions[i])
			}
		}
		have := byPage[fact.Page]
		if len(have) != len(want) {
			t.Errorf("page %d: %d regions computed, %d read back", fact.Page, len(want), len(have))
			continue
		}
		for i := range want {
			if x0 := int(want[i].X0 + 0.5); have[i].X0 != x0 {
				t.Errorf("page %d region %d: x0 read back as %d, want %d",
					fact.Page, i, have[i].X0, x0)
			}
			if have[i].Lang != want[i].Lang || have[i].Chars != want[i].Chars {
				t.Errorf("page %d region %d: read back %s/%d chars, want %s/%d",
					fact.Page, i, have[i].Lang, have[i].Chars, want[i].Lang, want[i].Chars)
			}
		}
	}

	// Re-probing a real document must converge, not accumulate. The idempotency
	// tests above use three hand-written regions; this is the same property over the
	// document's real region set, where a single colliding pair would show up.
	if err := s.SaveProbe(ctx, docID, res, registry.StateAwaitingScope); err != nil {
		t.Fatalf("re-save probe: %v", err)
	}
	again, err := s.Regions(ctx, docID)
	if err != nil {
		t.Fatalf("read back after re-probe: %v", err)
	}
	if len(again) != len(stored) {
		t.Errorf("re-probing changed the row count from %d to %d", len(stored), len(again))
	}
	for i := range stored {
		if i < len(again) && again[i] != stored[i] {
			t.Errorf("region %d changed on re-probe:\nfirst  %+v\nsecond %+v", i, stored[i], again[i])
		}
	}

	t.Logf("column manual: %d regions computed, %d stored, %d characters, languages %v",
		len(res.Regions), len(stored), gotChars, manifest.Languages)
}
