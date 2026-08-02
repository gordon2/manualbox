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
	}, nil))

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
	}, nil)

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
	got := doc.PageRegions(page, nil, doc.PageResolution{}, nil)

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
	got := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{}, nil))

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
	got := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{}, nil))

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
	got := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{Contents: true}, nil))

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
	got := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{}, nil))

	if got.Lang != "" {
		t.Errorf("region language = %q, want none established", got.Lang)
	}
	if got.Chars == 0 {
		t.Error("an unnamed page's characters were not counted; size does not depend on naming")
	}
}

func TestPageRegionsSkipAPageWithNothingOnIt(t *testing.T) {
	page := &doc.PageRuns{No: 3, Width: testRegionPageWidth, Height: 850}
	if got := doc.PageRegions(page, nil, doc.PageResolution{}, nil); len(got) != 0 {
		t.Errorf("got %d regions for an empty page, want none", len(got))
	}
}

// TestRegionCharsExcludeWhatIsNotText guards the measurement the size unit rests
// on. The column manual's text layer carries 522 sub-legible production slugs and
// parks 218 runs above the top edge of one page; counting those overstates that
// page by half.
func TestRegionCharsExcludeWhatIsNotText(t *testing.T) {
	page := regionPage(9, fill(german, 10))
	clean := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{}, nil))

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

	withJunk := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{}, nil))
	if withJunk.Chars != clean.Chars {
		t.Errorf("characters went from %d to %d when a sub-legible slug and an off-page "+
			"run were added; neither is text on the page", clean.Chars, withJunk.Chars)
	}
}

// TestRegionCharsCountRunesNotBytes is the convention this project has already been
// bitten by: half a real manual is Cyrillic or CJK, where the same writing runs a
// third more bytes.
func TestRegionCharsCountRunesNotBytes(t *testing.T) {
	latin := onlyRegion(t, doc.PageRegions(regionPage(1, fill("aaaaa", 10)), nil, doc.PageResolution{}, nil))
	cyrillic := onlyRegion(t, doc.PageRegions(regionPage(1, fill("ааааа", 10)), nil, doc.PageResolution{}, nil))

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
	got := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{}, nil))

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
	if got := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{}, nil)); got.Lang != "de" {
		t.Errorf("region language = %q, want de; both columns named it", got.Lang)
	}
}

// The cell-divider rule. These four are page 57 of the column manual, reduced to
// the shape that produces its one language error and back again.
//
// That page has no language columns at all: it has two ruled tables, and
// doc.DetectColumns found their cell dividers. Its narrow question-cell column is
// 289 runes of short German labels carrying ä and ö and no ü or ß, which reads as
// Finnish on its own and as German once the table's two cell columns are read as
// one thing. So the case is built the same way here — labels that really do read as
// Finnish alone — rather than with text chosen to make the test pass.
const tableLabels = "Saugkraft lässt allmählich nach. Viele Wassertropfen an der " +
	"Innenseite des Gehäusedeckels. Beim Saugen tritt Staub aus."

// ruledTable builds a table spanning x0 to x1 whose cells divide at each of the
// given interior x positions, with two rows so it has a grid at all.
func ruledTable(x0, x1, y0, y1 float64, dividers ...float64) doc.RuledTable {
	edges := append(append([]float64{x0}, dividers...), x1)
	t := doc.RuledTable{
		Box:  doc.CellRect{X0: x0, Y0: y0, X1: x1, Y1: y1},
		Rows: 2,
		Cols: len(edges) - 1,
	}
	for row := 0; row < 2; row++ {
		top := y0 + float64(row)*(y1-y0)/2
		for c := 0; c+1 < len(edges); c++ {
			t.Cells = append(t.Cells, doc.RuledCell{
				Row: row, Col: c, ColSpan: 1, Chars: 40,
				Rect: doc.CellRect{X0: edges[c], Y0: top, X1: edges[c+1], Y1: top + (y1-y0)/2},
			})
		}
	}
	return t
}

// TestPageRegionsDivideATablesCellsWithoutItsRuledLines is the state that shipped,
// pinned so that the fix below is visibly a fix and not a restatement. Given no
// tables — pdftocairo absent, or the ruled lines not read — the two cell columns
// are two languages, and the left one is wrong.
func TestPageRegionsDivideATablesCellsWithoutItsRuledLines(t *testing.T) {
	page := regionPage(57, fill(tableLabels, 10), fill(german, 10))
	got := doc.PageRegions(page, nil, doc.PageResolution{}, nil)

	if len(got) != 2 {
		t.Fatalf("got %d regions, want the 2 this page produced before ruled lines were read", len(got))
	}
	if got[0].Lang != "fi" || got[1].Lang != "de" {
		t.Fatalf("columns read as %q and %q; this test only means something if the left "+
			"one is the wrong answer the fix removes", got[0].Lang, got[1].Lang)
	}
}

// TestPageRegionsDoNotDivideOnATablesCellDividers is the fix: the same page, with
// the ruled lines read. One region, in the language the table's whole text is in,
// and the page's characters all still counted.
func TestPageRegionsDoNotDivideOnATablesCellDividers(t *testing.T) {
	page := regionPage(57, fill(tableLabels, 10), fill(german, 10))
	// The gutter runs 280 to 320, so a cell divider at 300 is the boundary the
	// detector reported, drawn by the table.
	table := ruledTable(20, 870, 10, 800, 300)

	got := onlyRegion(t, doc.PageRegions(page, nil, doc.PageResolution{}, []doc.RuledTable{table}))
	if got.Lang != "de" {
		t.Errorf("region language = %q, want de: read as one table the text is German", got.Lang)
	}
	if got.X0 != 0 || got.X1 != testRegionPageWidth {
		t.Errorf("region is boxed at x=%.0f-%.0f; a page that does not divide is whole-page",
			got.X0, got.X1)
	}
	// The table's area is excluded from deciding where the page divides, never from
	// what the page holds. Both cell columns' text is still charged for.
	if got.Chars < 1000 {
		t.Errorf("region holds %d characters; both cell columns' text must still be counted",
			got.Chars)
	}
}

// TestPageRegionsKeepColumnsATableDoesNotExplain is the guard that keeps this from
// running the other way, and it is the case the fix could most easily break: a
// table printed across two genuine language columns must not weld them into one.
//
// The discriminator is measured rather than assumed. On page 57 every one of the
// four coincidences is within about five units, so a table only merges a boundary
// it can be shown to have drawn.
func TestPageRegionsKeepColumnsATableDoesNotExplain(t *testing.T) {
	page := regionPage(2, fill(german, 10), fill(polish, 10))
	// A table across the whole measure whose only interior divider is at 150 —
	// nowhere near the 300 the page divides at.
	table := ruledTable(20, 870, 10, 800, 150)

	got := doc.PageRegions(page, nil, doc.PageResolution{}, []doc.RuledTable{table})
	if len(got) != 2 {
		t.Fatalf("got %d regions, want 2: this table explains neither boundary", len(got))
	}
	if got[0].Lang != "de" || got[1].Lang != "pl" {
		t.Errorf("regions read as %q and %q, want de and pl", got[0].Lang, got[1].Lang)
	}
}

// TestPageRegionsIgnoreATableInsideOneColumn is the column manual's pages 52 to 56:
// a small parts table set inside one of three same-language columns. A table that
// covers one column has nothing to merge, and the page reads as it did.
func TestPageRegionsIgnoreATableInsideOneColumn(t *testing.T) {
	page := regionPage(52, fill(german, 10), fill(polish, 10), fill(rus, 10))
	table := ruledTable(25, 285, 400, 700, 150)

	got := doc.PageRegions(page, nil, doc.PageResolution{}, []doc.RuledTable{table})
	if len(got) != 3 {
		t.Fatalf("got %d regions, want the 3 languages this page prints", len(got))
	}
}
