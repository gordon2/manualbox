package doc_test

import (
	"context"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/fixture"
)

// The acceptance tests docs/design/regions.md asks for, in its own words: the
// column manual's five languages must read back across its parallel columns with
// the eight human-verified pages matching column for column, and the sectioned
// manual's 34 sequential sections must be unchanged in what they report. A change
// that improves the second by altering the first has broken something, so both are
// asserted here rather than one.

func analyzeColumnFixture(t *testing.T) (manifest *fixture.Manifest, result *doc.Result) {
	t.Helper()
	m, path := columnFixture(t)
	res, err := doc.Analyze(context.Background(), path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.RegionNote != "" {
		t.Fatalf("no regions were produced: %s", res.RegionNote)
	}
	return m, res
}

// TestRegionsSplitTheColumnManualByLanguage is the first half of acceptance: a
// page holding three languages must yield three regions, and one holding three
// columns of a single language must yield one.
func TestRegionsSplitTheColumnManualByLanguage(t *testing.T) {
	m, res := analyzeColumnFixture(t)

	byPage := make(map[int][]doc.Region, m.Pages)
	for i := range res.Regions {
		r := &res.Regions[i]
		byPage[r.Page] = append(byPage[r.Page], *r)
	}
	t.Logf("%s", doc.RegionSummary(res.Regions))

	for _, want := range m.VerifiedPages() {
		got := byPage[want.Page]

		// How many languages the render shows on this page, from the ground truth.
		langs := make(map[string]bool, len(want.Cols))
		for _, c := range want.Cols {
			if c.Lang != "" {
				langs[c.Lang] = true
			}
		}

		switch {
		case len(langs) > 1:
			// A genuinely multi-language page: one region per column, in order, each
			// boxed at the column the human confirmed.
			if len(got) != len(want.Cols) {
				t.Errorf("page %d holds %d languages in %d columns but produced %d regions",
					want.Page, len(langs), len(want.Cols), len(got))
				continue
			}
			for i, wantCol := range want.Cols {
				if int(got[i].X0) != wantCol.X0 || int(got[i].X1) != wantCol.X1 {
					t.Errorf("page %d region %d: x=%.0f-%.0f, the render shows the column at %d-%d",
						want.Page, i, got[i].X0, got[i].X1, wantCol.X0, wantCol.X1)
				}
				if wantCol.Lang != "" && !doc.SameLanguage(got[i].Lang, wantCol.Lang) {
					t.Errorf("page %d region %d: %s, the manifest says %s — %s",
						want.Page, i, got[i].Lang, wantCol.Lang, got[i].Note)
				}
				if got[i].Chars == 0 {
					t.Errorf("page %d region %d holds no characters, but a column needs "+
						"runs to exist at all", want.Page, i)
				}
			}

		default:
			// One language, however many columns it is set in. Pages 6 and 12 of this
			// manual are the case: two columns of German, and one column beside a
			// full-height image. Both are one region covering the page.
			if len(got) != 1 {
				t.Errorf("page %d holds %d language across %d columns but produced %d regions; "+
					"column count is not language count", want.Page, len(langs), len(want.Cols), len(got))
				continue
			}
			if got[0].X0 != 0 || got[0].X1 == 0 {
				t.Errorf("page %d: a whole-page region must span 0 to the page width, got %.0f-%.0f",
					want.Page, got[0].X0, got[0].X1)
			}
		}
	}
}

// TestRegionsFindEveryLanguageOfTheColumnManual asserts the document-level claim:
// all five languages are present in the regions. Before regions, a page could
// carry only one language, so at most one of the three on page 2 could be stored.
func TestRegionsFindEveryLanguageOfTheColumnManual(t *testing.T) {
	m, res := analyzeColumnFixture(t)

	found := make(map[string]int, 8)
	for i := range res.Regions {
		if lang := doc.BaseLanguage(res.Regions[i].Lang); lang != "" {
			found[lang]++
		}
	}

	for _, want := range m.Languages {
		if found[want] == 0 {
			t.Errorf("%s is in the manifest but no region records it", want)
		}
	}
	for lang, n := range found {
		t.Logf("  %-3s %d regions", lang, n)
	}

	// Every language of a parallel-columns manual must reach a boxed region
	// somewhere, or the columns were never really separated.
	boxed := 0
	for i := range res.Regions {
		if res.Regions[i].X0 != 0 {
			boxed++
		}
	}
	if boxed == 0 {
		t.Error("no region is boxed; the whole point of this manual is that a page holds several")
	}
}

// TestRegionsLeaveTheSectionedManualUnchanged is the other half of acceptance, and
// the one that fails if the column work was bought at the sectioned manual's
// expense: every page holds one language, so every page must be one whole-page
// region carrying exactly the language the per-page map already believed.
func TestRegionsLeaveTheSectionedManualUnchanged(t *testing.T) {
	m, path := loadFixture(t)
	res, err := doc.Analyze(context.Background(), path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.RegionNote != "" {
		t.Skipf("no regions were produced: %s", res.RegionNote)
	}
	t.Logf("%s", doc.RegionSummary(res.Regions))

	// The language map itself must be untouched. This repeats what
	// TestLanguageMapMatchesManifest asserts, deliberately: that test would still
	// pass if regions quietly disagreed with it, and the two must not diverge.
	summaries := res.Languages()
	if len(summaries) != len(m.Sections) {
		t.Errorf("%d languages after regions landed, manifest records %d",
			len(summaries), len(m.Sections))
	}

	perPage := make(map[int]int, m.Pages)
	for i := range res.Regions {
		perPage[res.Regions[i].Page]++
	}

	// Not one region per page in general — a page with no text at all is nothing to
	// record — but never more than one, because no page of this manual holds two
	// languages. 199 of its pages read as three columns, and if those became three
	// regions each this is where it would show.
	var split []int
	for page, n := range perPage {
		if n > 1 {
			split = append(split, page)
		}
	}
	if len(split) > 0 {
		show := split
		if len(show) > 10 {
			show = show[:10]
		}
		t.Errorf("%d pages produced more than one region, e.g. %v; every page of this "+
			"manual is a single language, and its multi-column pages are tables",
			len(split), show)
	}

	// Each region's language must be the one the per-page map resolved, or regions
	// have become a second opinion rather than a finer-grained record of the same one.
	disagreed := 0
	for i := range res.Regions {
		r := &res.Regions[i]
		want, _ := res.PageLang(r.Page)
		if want != r.Lang {
			if disagreed < 5 {
				t.Errorf("page %d: region says %q, the page map says %q", r.Page, r.Lang, want)
			}
			disagreed++
		}
	}
	if disagreed > 0 {
		t.Errorf("%d regions disagree with the reconciled page language", disagreed)
	}
}

// TestScopeCharsCountOnlyTheColumnsInScope is what the whole change is for. A
// household reading one of a page's three languages should be charged for its
// column, not for the page.
func TestScopeCharsCountOnlyTheColumnsInScope(t *testing.T) {
	_, res := analyzeColumnFixture(t)

	german := res.ScopeFor([]string{"de"})
	all := res.ScopeFor([]string{"de", "pl", "ru", "uk", "kk"})

	if german.Chars == 0 || all.Chars == 0 {
		t.Fatalf("no characters counted: de=%d, all=%d", german.Chars, all.Chars)
	}
	t.Logf("German alone: %d chars; all five languages: %d chars (%.0f%%)",
		german.Chars, all.Chars, 100*float64(german.Chars)/float64(all.Chars))

	// The strict inequality is the assertion. Before regions both numbers were the
	// same, because a page in scope contributed all of its characters however many
	// languages shared it.
	if german.Chars >= all.Chars {
		t.Errorf("one language of five counts %d characters and all five count %d; "+
			"a single column cannot be the whole page", german.Chars, all.Chars)
	}

	// Sanity on the magnitude rather than a tuned figure: five languages sharing a
	// document, so one of them should be a minority of the text by some real margin.
	if fraction := float64(german.Chars) / float64(all.Chars); fraction > 0.6 {
		t.Errorf("German is %.0f%% of the document's in-scope characters, which is too "+
			"much of a five-language manual to be one language's columns", 100*fraction)
	}
}
