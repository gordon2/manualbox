package doc_test

import (
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
)

// Hermetic tests for the four rules in doc.PageRegions. No PDF and no poppler:
// runs are built here, so the rules are stated where they can be read. The
// real-document acceptance lives in regions_fixture_test.go, and every case below
// is drawn from something one of those two manuals actually does.

const testRegionPageWidth = 892

// regionPage builds a page whose columns each hold the given lines, spaced so the
// projection finds a real gutter between them and each column clears the minimum
// run count.
func regionPage(no int, columns ...[]string) *doc.PageRuns {
	p := &doc.PageRuns{No: no, Width: testRegionPageWidth, Height: 850}
	for i, lines := range columns {
		x := 30 + float64(i)*290
		for j, line := range lines {
			p.Runs = append(p.Runs, doc.TextRun{
				X: x, Y: float64(20 + j*18), Width: 250, Height: 14, Text: line,
			})
		}
	}
	return p
}

// fill repeats a line enough times to make a column, since eight runs is the floor.
func fill(line string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = line
	}
	return out
}

func onlyRegion(t *testing.T, regions []doc.Region) doc.Region {
	t.Helper()
	if len(regions) != 1 {
		t.Fatalf("got %d regions, want 1", len(regions))
	}
	return regions[0]
}

// TestPageRegionsPageLevelAnswerWins is rule 1. This is the sectioned manual's
// every page: the printed tab names the whole page, and a short table cell whose
// alphabet reads as something else must not overturn it.
func TestPageRegionsPageLevelAnswerWins(t *testing.T) {
	page := regionPage(7, fill(german, 10), fill(polish, 10))
	got := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{
		Code: "DE", Lang: "de", Source: doc.SourceReconciled,
	}))

	if got.Lang != "de" {
		t.Errorf("region language = %q, want de from the page-level answer", got.Lang)
	}
	if got.X0 != 0 || got.X1 != testRegionPageWidth {
		t.Errorf("region spans %.0f-%.0f, want the whole page 0-%d",
			got.X0, got.X1, testRegionPageWidth)
	}
	// The disagreement must survive as a conflict. Overriding a column silently is
	// what the design forbids; overriding it and saying so is the decision.
	if !got.Conflict {
		t.Error("a column read as another language and no conflict was recorded")
	}
	if got.Note == "" {
		t.Error("the conflict has no note saying what disagreed")
	}
}

// TestPageRegionsIgnoreAPageLevelAnswerThatNamesNoLanguage is the FAX case,
// measured on the column manual: its service-address page is read as a contents
// table, FAX becomes an index entry, "fax" parses as a language tag, and two pages
// were labelled with it over columns that read correctly.
func TestPageRegionsIgnoreAPageLevelAnswerThatNamesNoLanguage(t *testing.T) {
	page := regionPage(46, fill(german, 10), fill(polish, 10))
	got := doc.PageRegions(page, nil, doc.PageResolution{
		Code: "FAX", Lang: "fax", Source: doc.SourceReconciled,
	})

	if len(got) != 2 {
		t.Fatalf("got %d regions, want the two columns to decide the page", len(got))
	}
	for i, want := range []string{"de", "pl"} {
		if got[i].Lang != want {
			t.Errorf("region %d = %q, want %q", i, got[i].Lang, want)
		}
	}
}

// TestPageRegionsSplitOnLanguage is rule 2: the column manual's page 2, three
// languages side by side.
func TestPageRegionsSplitOnLanguage(t *testing.T) {
	page := regionPage(2, fill(german, 10), fill(polish, 10), fill(rus, 10))
	got := doc.PageRegions(page, nil, doc.PageResolution{})

	if len(got) != 3 {
		t.Fatalf("got %d regions, want 3", len(got))
	}
	for i, want := range []string{"de", "pl", "ru"} {
		if got[i].Lang != want {
			t.Errorf("region %d = %q, want %q", i, got[i].Lang, want)
		}
		if got[i].Page != 2 {
			t.Errorf("region %d is on page %d, want 2", i, got[i].Page)
		}
		if got[i].Chars == 0 {
			t.Errorf("region %d holds no characters", i)
		}
		if got[i].Runs != 10 {
			t.Errorf("region %d holds %d runs, want the 10 written into it", i, got[i].Runs)
		}
	}

	// Boxes must be ordered and disjoint, because the natural key is the page and
	// the left edge: two regions sharing an x0 would collide in storage.
	for i := 1; i < len(got); i++ {
		if got[i].X0 <= got[i-1].X0 {
			t.Errorf("region %d starts at %.0f, not to the right of region %d at %.0f",
				i, got[i].X0, i-1, got[i-1].X0)
		}
		if got[i].X0 < got[i-1].X1 {
			t.Errorf("regions %d and %d overlap: %.0f-%.0f and %.0f-%.0f",
				i-1, i, got[i-1].X0, got[i-1].X1, got[i].X0, got[i].X1)
		}
	}
}

// TestPageRegionsDoNotSplitColumnsOfOneLanguage is the other half of rule 2, and
// the reason the rule is about language and not geometry: the column manual sets
// two columns of German on pages 6 to 10 and three of Polish on 53, and the
// sectioned manual sets hundreds of pages as side-by-side tables.
func TestPageRegionsDoNotSplitColumnsOfOneLanguage(t *testing.T) {
	page := regionPage(6, fill(german, 10), fill(german, 10))
	got := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{}))

	if got.Lang != "de" {
		t.Errorf("region language = %q, want de", got.Lang)
	}
	if got.X0 != 0 || got.X1 != testRegionPageWidth {
		t.Errorf("two columns of one language gave a boxed region %.0f-%.0f, want the whole page",
			got.X0, got.X1)
	}
	// Both columns' text must be counted, or the page's size is understated by
	// however many columns it is set in.
	if got.Runs != 20 {
		t.Errorf("region holds %d runs, want all 20 across both columns", got.Runs)
	}
}

// TestPageRegionsNameASingleColumnPage is rule 3: the column manual's page 12 is
// one column beside a full-height image, and nothing per-page names it.
func TestPageRegionsNameASingleColumnPage(t *testing.T) {
	page := regionPage(12, fill(ukr, 10))
	got := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{}))

	if got.Lang != "uk" {
		t.Errorf("region language = %q, want uk from the column", got.Lang)
	}
	if got.X1 != testRegionPageWidth {
		t.Errorf("region ends at %.0f, want the page width", got.X1)
	}
}

// TestPageRegionsRefuseToNameAContentsPage is the exception to rule 3. Measured:
// the sectioned manual's contents pages 2 to 5 read as Swedish and Turkish, and the
// column manual's page of service addresses reads as Turkish. All are wrong.
func TestPageRegionsRefuseToNameAContentsPage(t *testing.T) {
	page := regionPage(2, fill(german, 10))
	got := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{Contents: true}))

	if got.Lang != "" {
		t.Errorf("a contents page was named %q from its own letters", got.Lang)
	}
	// It must still say what it read and refused, or this is indistinguishable from
	// a page nothing could be made of.
	if got.Note == "" {
		t.Error("no note explains why the page was left unnamed")
	}
	// And its characters still count: the text is there whether or not it was named.
	if got.Chars == 0 {
		t.Error("a contents page's characters were not counted")
	}
}

// TestPageRegionsLeaveAnUnnameablePageUnnamed is rule 4. The column manual's back
// page of service addresses in six languages is genuinely unnameable, and saying so
// is the honest outcome.
func TestPageRegionsLeaveAnUnnameablePageUnnamed(t *testing.T) {
	page := regionPage(68, fill("Service 1234 5678 90", 10))
	got := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{}))

	if got.Lang != "" {
		t.Errorf("region language = %q, want none established", got.Lang)
	}
	if got.Chars == 0 {
		t.Error("an unnamed page's characters were not counted; size does not depend on naming")
	}
}

func TestPageRegionsSkipAPageWithNothingOnIt(t *testing.T) {
	page := &doc.PageRuns{No: 3, Width: testRegionPageWidth, Height: 850}
	if got := doc.PageRegions(page, nil, doc.PageResolution{}); len(got) != 0 {
		t.Errorf("got %d regions for an empty page, want none", len(got))
	}
}

// TestRegionCharsExcludeWhatIsNotText guards the measurement the size unit rests
// on. The column manual's text layer carries 522 sub-legible production slugs and
// parks 218 runs above the top edge of one page; counting those overstates that
// page by half.
func TestRegionCharsExcludeWhatIsNotText(t *testing.T) {
	page := regionPage(9, fill(german, 10))
	clean := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{}))

	page.Runs = append(page.Runs,
		// A production slug: real text in the file, two units tall, invisible on paper.
		doc.TextRun{
			X: 30, Y: 400, Width: 250, Height: 2,
			Text: "Job_4417_Manual_v3_export_2019-11-08.indd   1   08.11.19   10:16",
		},
		// A run parked above the page, which is where a superseded address list lives.
		doc.TextRun{
			X: 30, Y: -38, Width: 250, Height: 14, Text: "Superseded address list line",
		})

	withJunk := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{}))
	if withJunk.Chars != clean.Chars {
		t.Errorf("characters went from %d to %d when a sub-legible slug and an off-page "+
			"run were added; neither is text on the page", clean.Chars, withJunk.Chars)
	}
}

// TestRegionCharsCountRunesNotBytes is the convention this project has already been
// bitten by: half a real manual is Cyrillic or CJK, where the same writing runs a
// third more bytes.
func TestRegionCharsCountRunesNotBytes(t *testing.T) {
	latin := onlyRegion(t, doc.PageRegions(regionPage(1, fill("aaaaa", 10)), nil, doc.PageResolution{}))
	cyrillic := onlyRegion(t, doc.PageRegions(regionPage(1, fill("ааааа", 10)), nil, doc.PageResolution{}))

	if latin.Chars != cyrillic.Chars {
		t.Errorf("five Latin letters counted %d and five Cyrillic %d; runes, not bytes",
			latin.Chars, cyrillic.Chars)
	}
}

// TestPageRegionsRefuseAPageNamedByAMinorityOfItsColumns is the column manual's
// back page: three columns of service addresses in six languages, one of which the
// alphabet reads as Turkish while the other two decline. Naming the page from a
// third of it is naming it on weak evidence.
//
// This was previously suppressed by accident — the address page was misread as a
// contents table, and the contents guard caught it. Fixing the index parser removed
// that accident and exposed the reading, which is why the refusal is now explicit.
func TestPageRegionsRefuseAPageNamedByAMinorityOfItsColumns(t *testing.T) {
	page := regionPage(68,
		fill("Service 1234 5678 90", 10),
		fill("Servis 9876 5432 10", 10),
		fill("Huolto 5555 4444 33 Jyväskylä Töölö", 10),
	)
	got := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{}))

	if got.Lang != "" {
		t.Errorf("page named %q from one of three columns; the other two established nothing",
			got.Lang)
	}
	if !strings.Contains(got.Note, "minority") {
		t.Errorf("note does not say why the reading was refused: %q", got.Note)
	}
	if got.Chars == 0 {
		t.Error("the page's characters must still be counted; size does not depend on naming")
	}
}

// TestPageRegionsStillNameAPageAllOfWhoseColumnsAgree is the other side of that
// guard: it must not refuse the ordinary case of two columns in one language, which
// is pages 6 to 10 of the same manual.
func TestPageRegionsStillNameAPageAllOfWhoseColumnsAgree(t *testing.T) {
	page := regionPage(6, fill(german, 10), fill(german, 10))
	if got := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{})); got.Lang != "de" {
		t.Errorf("region language = %q, want de; both columns named it", got.Lang)
	}
}
