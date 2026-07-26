package doc_test

import (
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
)

// Hermetic tests for the table walk. Every shape here is the column manual's page
// 57 reduced to what makes the case, because that is the page
// docs/design/conversion.md measured this join against: two side-by-side
// troubleshooting tables of two cell columns each, a header row printed above a top
// border the document does not draw, and a running head across the top.
//
// The page builders and the region helper come from blocks_test.go, so a table page
// and an ordinary one are built the same way and can be compared.

// trouble builds one two-column troubleshooting table: dividers at x0, mid and x1,
// a row per fault, and a full-width spanning cell at the top for the section label,
// which is what page 57 prints as "Allgemein (alle Funktionen)".
func trouble(x0, mid, x1, y0 float64, rows int) doc.RuledTable {
	t := doc.RuledTable{
		Box:  doc.CellRect{X0: x0, Y0: y0, X1: x1, Y1: y0 + float64(rows+1)*40},
		Rows: rows + 1, Cols: 2,
	}
	t.Cells = append(t.Cells, doc.RuledCell{Row: 0, Col: 0, ColSpan: 2,
		Rect: doc.CellRect{X0: x0, Y0: y0, X1: x1, Y1: y0 + 40}})
	for r := 1; r <= rows; r++ {
		top := y0 + float64(r)*40
		t.Cells = append(t.Cells,
			doc.RuledCell{Row: r, Col: 0, ColSpan: 1,
				Rect: doc.CellRect{X0: x0, Y0: top, X1: mid, Y1: top + 40}},
			doc.RuledCell{Row: r, Col: 1, ColSpan: 1,
				Rect: doc.CellRect{X0: mid, Y0: top, X1: x1, Y1: top + 40}})
	}
	return t
}

// troubleRows is how many fault rows the built table has. Eight, because that is
// [doc] minColumnRuns: below it a cell column is not enough runs to be read as a text
// column at all, and the whole point of the first test is that it IS read as one when
// the ruled lines are not available. Page 57's tables have seven rows.
const troubleRows = 8

// troublePage is page 57's shape: a running head, a header row above the table's
// undrawn top border, the spanning label cell, and a question and an answer per row.
func troublePage(no int) (*doc.PageRuns, doc.RuledTable) {
	table := trouble(30, 170, 430, 100, troubleRows)
	lines := []line{
		{y: 16, x: 40, w: 140, size: 17, weight: doc.WeightBold, text: "Fehlerbehebung"},
		{y: 70, x: 32, w: 120, size: 11, text: "Aufgetretene Störungen"},
		{y: 70, x: 175, w: 100, size: 11, text: "Grund / Abhilfe"},
		{y: 108, x: 34, w: 200, size: 11, text: "Allgemein (alle Funktionen)"},
	}
	for r := 1; r <= troubleRows; r++ {
		y := float64(108 + r*40)
		lines = append(lines,
			line{y: y, x: 34, w: 120, size: 11, text: "Fehler " + itoa(r)},
			line{y: y, x: 174, w: 240, size: 11, text: "Abhilfe " + itoa(r)})
	}
	return blockPage(no, lines...), table
}

// TestRegionBlocksReadATableAcrossItsRows is the fix for the limitation blocks.go
// used to record. Without the ruled lines the two cell columns are two text columns,
// so both questions read before both answers; with them each row is read across, and
// a question is beside its own remedy.
func TestRegionBlocksReadATableAcrossItsRows(t *testing.T) {
	page, table := troublePage(57)

	down := blockTexts(doc.RegionBlocks(page, wholePage(57), nil))
	if strings.Index(down, "Fehler "+itoa(troubleRows)) > strings.Index(down, "Abhilfe 1") {
		t.Fatalf("without ruled lines this page is meant to read down every question and "+
			"then down every answer, which is the limitation being fixed; it read: %s", down)
	}

	got := doc.RegionBlocks(page, wholePage(57), []doc.RuledTable{table})
	var cells []string
	for i := range got {
		if got[i].Kind != doc.BlockTable {
			continue
		}
		cells = append(cells, got[i].Text)
		if !strings.Contains(got[i].Note, "row ") {
			t.Errorf("table block %d does not say where in the grid it sits: %q",
				got[i].Index, got[i].Note)
		}
	}
	want := []string{"Allgemein (alle Funktionen)"}
	for r := 1; r <= troubleRows; r++ {
		want = append(want, "Fehler "+itoa(r), "Abhilfe "+itoa(r))
	}
	if strings.Join(cells, "|") != strings.Join(want, "|") {
		t.Errorf("table cells read\n  %q\nwant row-major\n  %q", cells, want)
	}
}

// TestRegionBlocksKeepAHeadingPrintedAcrossATableExactlyOnce is the constraint
// conversion.md states twice, because both ways of getting it wrong are tempting: a
// heading printed across a table must not be placed in a cell, and banner blocks
// must not be suppressed wherever a table covers the region. Either makes it vanish.
//
// The running head and the header row are both such text: they belong to no cell, so
// they read with the prose, before the table, once each.
func TestRegionBlocksKeepAHeadingPrintedAcrossATableExactlyOnce(t *testing.T) {
	page, table := troublePage(57)
	got := doc.RegionBlocks(page, wholePage(57), []doc.RuledTable{table})

	seen, firstTable := 0, len(got)
	for i := range got {
		if strings.Contains(got[i].Text, "Fehlerbehebung") {
			seen++
			if got[i].Kind == doc.BlockTable {
				t.Errorf("the heading printed across the table came back as a table cell: %q",
					got[i].Text)
			}
		}
		if got[i].Kind == doc.BlockTable && i < firstTable {
			firstTable = i
		}
	}
	if seen != 1 {
		t.Errorf("the heading appears %d times, want exactly once: %s", seen, blockTexts(got))
	}
	if firstTable == 0 {
		t.Errorf("the table reads before the heading printed above it: %s", blockTexts(got))
	}
	// The header row above the table's undrawn top border is not lost either.
	if !strings.Contains(blockTexts(got), "Aufgetretene Störungen") {
		t.Errorf("the header row above the table's top border was lost: %s", blockTexts(got))
	}
}

// TestRegionBlocksClipATableToTheRegion is the constraint that a table may span
// regions in DIFFERENT languages, and it is page 57 exactly as it stood before the
// cell dividers were recognised: a Finnish region on the question cells and a German
// one on the answers, with one table spanning both.
//
// Assuming one table sits inside one region would pull the Finnish column into a
// German conversion. The join is geometric for the reason conversion.md gives — a
// block keys on its region's left edge and a table area has none to key on — so what
// clips the table is that a cell is only ever offered the region's own runs.
func TestRegionBlocksClipATableToTheRegion(t *testing.T) {
	page, table := troublePage(57)
	tables := []doc.RuledTable{table}

	left := doc.RegionBlocks(page, &doc.Region{Page: 57, X0: 30, X1: 160, Lang: "fi"}, tables)
	right := doc.RegionBlocks(page, &doc.Region{Page: 57, X0: 170, X1: 430, Lang: "de"}, tables)
	if len(left) == 0 || len(right) == 0 {
		t.Fatalf("got %d blocks left and %d right; both halves of the table must be read",
			len(left), len(right))
	}
	for _, side := range []struct {
		name       string
		blocks     []doc.Block
		x0, x1     float64
		wants, not string
	}{
		{"the Finnish region", left, 30, 160, "Fehler 1", "Abhilfe 1"},
		{"the German region", right, 170, 430, "Abhilfe 1", "Fehler 1"},
	} {
		for i := range side.blocks {
			b := &side.blocks[i]
			if b.X0 < side.x0-1 || b.X1 > side.x1+1 {
				t.Errorf("%s: block %d spans x=%.1f-%.1f, outside x=%.0f-%.0f: %q",
					side.name, b.Index, b.X0, b.X1, side.x0, side.x1, b.Text)
			}
		}
		texts := blockTexts(side.blocks)
		if !strings.Contains(texts, side.wants) {
			t.Errorf("%s does not hold %q: %s", side.name, side.wants, texts)
		}
		if strings.Contains(texts, side.not) {
			t.Errorf("%s holds %q, which is the cell column beside it: %s",
				side.name, side.not, texts)
		}
	}
}

// TestRegionBlocksWithoutRuledLinesAreUnchanged is the compatibility property that
// matters most here: pdftocairo is optional, so no tables must produce exactly what
// the release before this one produced, on a page that draws a table and on one that
// does not.
func TestRegionBlocksWithoutRuledLinesAreUnchanged(t *testing.T) {
	tabled, _ := troublePage(57)
	plain := blockPage(62,
		line{y: 40, x: 40, w: 120, size: 17, weight: doc.WeightBold, text: "Technische Daten"},
		line{y: 80, x: 40, w: 400, size: 11, text: "Spannungsversorgung: 230 V, 50 Hz"},
		line{y: 96, x: 40, w: 400, size: 11, text: "Leistungsaufnahme: siehe Typenschild"},
	)
	for _, p := range []*doc.PageRuns{tabled, plain} {
		none := doc.RegionBlocks(p, wholePage(p.No), nil)
		empty := doc.RegionBlocks(p, wholePage(p.No), []doc.RuledTable{})
		if blockTexts(none) != blockTexts(empty) {
			t.Errorf("page %d reads differently for nil tables and no tables:\n  %s\n  %s",
				p.No, blockTexts(none), blockTexts(empty))
		}
		for i := range none {
			if none[i].Kind == doc.BlockTable {
				t.Errorf("page %d produced a table block with no ruled lines read", p.No)
			}
		}
	}
}

// TestRegionBlocksReadNoTextTwice is what keeps the reader inside what the gate
// charged for. Both walks draw from one filtered run set, so a cell and a block can
// never show the same text and neither can show text the other could not see — the
// region's character count comes through that same filter.
func TestRegionBlocksReadNoTextTwice(t *testing.T) {
	page, table := troublePage(57)
	// One of the production slugs usableRuns exists to drop, placed inside a cell.
	// This manual's illustrations are placed PDFs that each brought an InDesign
	// filename along, scaled down with the artwork, and 522 of them are in its text
	// layer. A table walk with a filter of its own would put this in the reader.
	page.Runs = append(page.Runs, doc.TextRun{
		X: 200, Y: 150, Width: 60, Height: 3, Text: "Amfibia_57.indd",
		Font: doc.Font{Size: 3, Family: "Test-Face"},
	})

	got := doc.RegionBlocks(page, wholePage(57), []doc.RuledTable{table})
	if strings.Contains(blockTexts(got), "Amfibia") {
		t.Errorf("a sub-legible production slug reached a table cell, so the table walk "+
			"is not reading through the region's own filter: %s", blockTexts(got))
	}
	page.Runs = page.Runs[:len(page.Runs)-1]

	// The page's own words against the blocks' words, as multisets. Nothing may be
	// counted twice and nothing may be missing, which is both halves of the property
	// in one comparison.
	onPage := map[string]int{}
	for i := range page.Runs {
		for _, word := range strings.Fields(page.Runs[i].Text) {
			onPage[word]++
		}
	}
	inBlocks := map[string]int{}
	for i := range got {
		for _, word := range strings.Fields(got[i].Text) {
			inBlocks[word]++
		}
	}
	for word, n := range onPage {
		if inBlocks[word] != n {
			t.Errorf("%q is printed %d times and read %d times", word, n, inBlocks[word])
		}
	}
	for word, n := range inBlocks {
		if onPage[word] != n {
			t.Errorf("%q is read %d times and printed %d times", word, n, onPage[word])
		}
	}
}

func blockTexts(blocks []doc.Block) string {
	out := make([]string, 0, len(blocks))
	for i := range blocks {
		out = append(out, string(blocks[i].Kind)+":"+blocks[i].Text)
	}
	return strings.Join(out, " | ")
}
