package doc_test

import (
	"context"
	"os"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/extern"
	"github.com/gordon2/manualbox/internal/fixture"
)

// These drive the ruled-line reader against both real manuals, and the numbers
// they assert are the ones a human counted off a render — not the ones the code
// happened to produce. docs/design/conversion.md records the count and the misses
// for each page; where a count is short of what is printed, the shortfall is
// asserted too, so that a change which silently loses a different set of cells
// cannot pass by arriving at the same total.
//
// Both manuals are needed and neither is enough. The columns manual is where the
// hard cases are — tables with no outer border, tables drawn in a blend group,
// pages of framed illustrations ruled exactly like tables. The sequential manual
// is where the volume is, and its 34 translations of the same 5 table pages give
// the one arithmetic check available on the whole pipeline: 34 times 5 is 170.

// rulesFixture loads a fixture and the tools this file needs.
func rulesFixture(t *testing.T, name string) (path string, pages []doc.PageRuns) {
	t.Helper()
	if os.Getenv(fixture.EnableEnv) == "" {
		t.Skipf("set %s=1 to download the fixture and run the real-document tests", fixture.EnableEnv)
	}
	for _, tool := range []extern.Tool{extern.PDFToHTML, extern.PDFToCairo} {
		if !extern.Available(tool) {
			t.Skipf("%s is not installed", tool.Name)
		}
	}
	m, err := fixture.Load(fixturesDir, name)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	cached, err := m.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch fixture: %v", err)
	}
	runs, err := doc.ExtractRuns(context.Background(), cached)
	if err != nil {
		t.Fatalf("ExtractRuns: %v", err)
	}
	return cached, runs
}

// pageOf finds one page's runs by its printed number.
func pageOf(t *testing.T, pages []doc.PageRuns, no int) *doc.PageRuns {
	t.Helper()
	for i := range pages {
		if pages[i].No == no {
			return &pages[i]
		}
	}
	t.Fatalf("the document has no page %d", no)
	return nil
}

// tablesOf reads one page's tables.
func tablesOf(t *testing.T, path string, pages []doc.PageRuns, no int) []doc.RuledTable {
	t.Helper()
	tables, err := doc.PageTables(context.Background(), path, pageOf(t, pages, no))
	if err != nil {
		t.Fatalf("PageTables page %d: %v", no, err)
	}
	return tables
}

func cellCount(tables []doc.RuledTable) int {
	var n int
	for i := range tables {
		n += len(tables[i].Cells)
	}
	return n
}

// TestRecoveredCellsMatchTheCountedPages is the ground truth. Every expected
// number here was arrived at by drawing the cells on a `pdftoppm -r 108` render
// and comparing them with the printed table, and the two pages that come back
// short are short for a reason that is understood and recorded.
func TestRecoveredCellsMatchTheCountedPages(t *testing.T) {
	for _, f := range []struct {
		fixture string
		pages   []struct {
			no, printed, want int
			why               string
		}
	}{
		{"thomas-drybox-amfibia", []struct {
			no, printed, want int
			why               string
		}{
			{57, 29, 25, "the misses are two header rows whose top border is not drawn"},
		}},
		{"dreame-l40-ultra", []struct {
			no, printed, want int
			why               string
		}{
			{20, 12, 12, ""},
			{100, 16, 16, ""},
			{21, 32, 32, ""},
			{15, 47, 37, "the misses are exactly the vertically merged cells"},
		}},
	} {
		path, pages := rulesFixture(t, f.fixture)
		for _, p := range f.pages {
			tables := tablesOf(t, path, pages, p.no)
			got := cellCount(tables)
			if got != p.want {
				t.Errorf("%s page %d: recovered %d cells, want %d of the %d printed (%s)",
					f.fixture, p.no, got, p.want, p.printed, p.why)
				continue
			}
			if len(tables) == 0 {
				t.Errorf("%s page %d: %d cells but no table", f.fixture, p.no, got)
			}
			// Every cell of every one of these tables holds text. That is a
			// stronger statement than the guard's, and it is what makes the counts
			// mean "cells you could read something out of".
			for i := range tables {
				tb := &tables[i]
				if tb.CellsWithText() != len(tb.Cells) {
					t.Errorf("%s page %d table %d: %d of %d cells hold no text",
						f.fixture, p.no, i, len(tb.Cells)-tb.CellsWithText(), len(tb.Cells))
				}
			}
		}
	}
}

// TestPage57IsTwoTablesWithNoOuterVerticals pins the shape of the hardest page,
// not just its total. It is the page whose tables are drawn in a blend group
// hoisted into <defs>, whose row rules stop at x=29.7 and x=428.1 with no
// vertical drawn there at all, and whose full-width section rows fragment one
// printed table into three components.
func TestPage57IsTwoTablesWithNoOuterVerticals(t *testing.T) {
	path, pages := rulesFixture(t, "thomas-drybox-amfibia")
	tables := tablesOf(t, path, pages, 57)

	if len(tables) != 2 {
		t.Fatalf("got %d tables, want 2 side by side: %+v", len(tables), tables)
	}
	left, right := &tables[0], &tables[1]
	if len(left.Cells) != 12 || len(right.Cells) != 13 {
		t.Errorf("tables hold %d and %d cells, want 12 and 13",
			len(left.Cells), len(right.Cells))
	}
	// The implied outer edges. Nothing draws a vertical at either, and the whole
	// table depends on believing the x where every row rule stops.
	if !near(left.Box.X0, 29.7, 0.5) || !near(left.Box.X1, 428.1, 0.5) {
		t.Errorf("left table spans x=%.1f..%.1f, want the implied 29.7..428.1",
			left.Box.X0, left.Box.X1)
	}
	if left.Box.X1 > right.Box.X0 {
		t.Errorf("the two tables overlap: %.1f..%.1f and %.1f..%.1f",
			left.Box.X0, left.Box.X1, right.Box.X0, right.Box.X1)
	}
	// The rules were found at all, which is what fails if <defs> is skipped: that
	// reading returns 18 rules for this page, every one of them a footer crop mark.
	var rules int
	for i := range tables {
		rules += len(tables[i].Rules)
	}
	if rules < 40 {
		t.Errorf("the two tables were built from %d rules; skipping <defs> leaves 18 "+
			"crop marks and no table rule at all", rules)
	}
	// A spanning cell exists, because a section row interrupts the column rule.
	var spanning int
	for i := range tables {
		for j := range tables[i].Cells {
			if tables[i].Cells[j].ColSpan > 1 {
				spanning++
			}
		}
	}
	if spanning == 0 {
		t.Error("no cell spans a column, but this page's section rows run the full width")
	}
}

// TestFramedIllustrationsAreNotTables is the text guard, on the pages that need
// it. They are grids of boxed pictures: ruled in rows and columns, aligned, and
// empty. Geometry passes them and only the words reject them.
func TestFramedIllustrationsAreNotTables(t *testing.T) {
	path, pages := rulesFixture(t, "thomas-drybox-amfibia")
	// 38 was the third of these until the clip was read. Its frame's left edge is
	// drawn 30 units past where it is painted, and that over-long rule was what
	// closed a cell; clipped, the page does not pass the shape guard, so it can no
	// longer test what happens after it. It still comes back as no table, which is
	// asserted below for all three.
	for _, no := range []int{22, 44} {
		page := pageOf(t, pages, no)
		rules, err := doc.ExtractRules(context.Background(), path, no)
		if err != nil {
			t.Fatalf("ExtractRules page %d: %v", no, err)
		}
		if got := doc.FindRuledTables(rules, page); len(got) != 0 {
			t.Errorf("page %d is a grid of framed illustrations but came back as %d "+
				"table(s) with %d cells", no, len(got), cellCount(got))
		}
		// And the reason it must be the text guard that rejects them: the shape
		// guard passes. If this stops being true the guard above has stopped being
		// tested by this page, even though the page still comes out right.
		shaped := doc.FindRuledTables(rules, nil)
		if len(shaped) == 0 {
			t.Errorf("page %d no longer passes the shape guard, so it no longer "+
				"exercises the text guard", no)
		}
	}

	// Page 38 keeps its half of the claim: still a grid of framed illustrations,
	// still no table, now rejected by the shape guard instead.
	page := pageOf(t, pages, 38)
	rules, err := doc.ExtractRules(context.Background(), path, 38)
	if err != nil {
		t.Fatalf("ExtractRules page 38: %v", err)
	}
	if got := doc.FindRuledTables(rules, page); len(got) != 0 {
		t.Errorf("page 38 came back as %d table(s) with %d cells", len(got), cellCount(got))
	}
}

// TestPagesThatMustNotBeTables covers the two other ways a page can look like
// one: a contents page ruled with leader lines and a page of three parallel
// language columns, which is the case layouts.md records that geometry cannot
// distinguish from a table. Plus a page with no vector rules at all, which must
// come back empty rather than fail.
func TestPagesThatMustNotBeTables(t *testing.T) {
	colPath, colPages := rulesFixture(t, "thomas-drybox-amfibia")
	for _, no := range []int{2, 13} {
		if got := tablesOf(t, colPath, colPages, no); len(got) != 0 {
			t.Errorf("columns manual page %d came back as %d table(s) with %d cells; "+
				"it is a contents page or parallel language columns, not a table",
				no, len(got), cellCount(got))
		}
	}

	seqPath, seqPages := rulesFixture(t, "dreame-l40-ultra")
	rules, err := doc.ExtractRules(context.Background(), seqPath, 8)
	if err != nil {
		t.Fatalf("ExtractRules: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("sequential manual page 8 draws %d rules, and it draws none", len(rules))
	}
	if got := tablesOf(t, seqPath, seqPages, 8); len(got) != 0 {
		t.Errorf("a page with no rules produced %d tables", len(got))
	}
}

// TestBothGuardsAreNeededOverTheWholeDocument is the discrimination measurement,
// asserted rather than only recorded. It is the test that fails if either guard
// is dropped, and the numbers are what docs/design/conversion.md argues from.
//
// It reads both documents page by page, so it is the slow one here: about 6
// seconds for 68 pages and 37 for 560.
func TestBothGuardsAreNeededOverTheWholeDocument(t *testing.T) {
	for _, want := range []struct {
		fixture              string
		pages                int
		anyRule, shape, both int
		note                 string
	}{
		// Every page carries footer crop marks, so "has a ruled line" is the whole
		// document and separates nothing.
		// 12 pass the shape guard where 13 did before the clip was read: page 38's
		// frame has a left edge drawn 30 units longer than it is painted, and that
		// over-long rule was closing a cell. Clipped to what the page prints, the
		// page no longer looks like a table at all — checked against a 432 dpi
		// render of x=85-145, y=150-290, where the stroke ends at y=238 and the
		// unclipped extent ran to 268.7. The page's answer is unchanged either way:
		// it is a grid of framed illustrations and produces no table.
		{"thomas-drybox-amfibia", 68, 68, 12, 10,
			"the 2 pages between the shape guard and the text guard are 22 and 44"},
		// 170 is 34 languages times 5 table pages, exactly.
		{"dreame-l40-ultra", 560, 226, 171, 170, "170 = 34 languages x 5 table pages"},
	} {
		path, pages := rulesFixture(t, want.fixture)
		var anyRule, shape, both int
		var tablePages []int
		for i := range pages {
			page := &pages[i]
			rules, err := doc.ExtractRules(context.Background(), path, page.No)
			if err != nil {
				t.Fatalf("ExtractRules page %d: %v", page.No, err)
			}
			if len(rules) > 0 {
				anyRule++
			}
			if len(doc.FindRuledTables(rules, nil)) > 0 {
				shape++
			}
			if len(doc.FindRuledTables(rules, page)) > 0 {
				both++
				tablePages = append(tablePages, page.No)
			}
		}
		if len(pages) != want.pages {
			t.Fatalf("%s: %d pages, want %d", want.fixture, len(pages), want.pages)
		}
		if anyRule != want.anyRule || shape != want.shape || both != want.both {
			t.Errorf("%s: %d pages draw a rule, %d pass the shape guard, %d pass both; "+
				"want %d, %d, %d (%s)", want.fixture, anyRule, shape, both,
				want.anyRule, want.shape, want.both, want.note)
		}
		if want.fixture == "thomas-drybox-amfibia" {
			// Its tables are pages 52 to 61, which conversion.md corrects from the
			// 57-61 the manifest recorded.
			for i, no := range tablePages {
				if no != 52+i {
					t.Errorf("table pages are %v, want 52 to 61 contiguous", tablePages)
					break
				}
			}
		}
	}
}

// TestRuleCoordinateSpaceMatchesTheRuns is the check every count above depends
// on: cairo writes PDF points and everything else in this package works in
// poppler's 1.5-scaled space, so if the ratio were wrong every cell would be
// two thirds of its size and no text would fall inside one.
func TestRuleCoordinateSpaceMatchesTheRuns(t *testing.T) {
	for _, f := range []struct {
		name string
		page int
	}{
		{"thomas-drybox-amfibia", 57},
		{"dreame-l40-ultra", 20},
	} {
		path, pages := rulesFixture(t, f.name)
		page := pageOf(t, pages, f.page)
		rules, err := doc.ExtractRules(context.Background(), path, f.page)
		if err != nil {
			t.Fatalf("ExtractRules: %v", err)
		}
		if len(rules) == 0 {
			t.Fatalf("%s page %d draws no rules", f.name, f.page)
		}
		// Every rule must land on the page poppler reports, within a small
		// fraction of it. The slack is not politeness: the columns manual is a
		// print PDF and draws its footer crop marks on the trim, measured at
		// y=852.1 to 858.3 on a page poppler calls 850 tall, because cairo's own
		// media box is 566.929pt — 850.39 — and the marks sit outside even that.
		// A wrong scale is off by a third, not by 1%, so this still catches one.
		for i := range rules {
			r := &rules[i]
			along, across := page.Width, page.Height
			if r.Dir == doc.Vertical {
				along, across = page.Height, page.Width
			}
			slack := 0.02 * across
			if r.At < -slack || r.At > across+slack ||
				r.Start < -0.02*along || r.End > along+0.02*along {
				t.Errorf("%s page %d: %s rule at %.1f spanning %.1f..%.1f is off a "+
					"%.0fx%.0f page — the coordinate scale is wrong",
					f.name, f.page, r.Dir, r.At, r.Start, r.End, page.Width, page.Height)
				break
			}
		}
	}
}

func near(a, b, tol float64) bool { return a-b < tol && b-a < tol }
