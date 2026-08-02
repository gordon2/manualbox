package doc_test

import (
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
)

// TestATableWiderThanItsWordsStaysInItsStrip is the sequential manual's page 537: a
// spec table whose ruled box is drawn x=478-862 in a strip whose words reach only
// 845, because the box comes from the strokes and the strip's bounds come from the
// text. Under containment the table belonged to no strip and fell to the banner band,
// which reads first — so the reader was shown the whole spec table and only then the
// page's own title.
func TestATableWiderThanItsWordsStaysInItsStrip(t *testing.T) {
	// Two strips of a page that the column detector will split, each with a heading
	// and a table under it, and each table drawn 18 units wider than its own words.
	left := gridTable(60, 200, 450, 120, 5)
	right := gridTable(478, 620, 862, 120, 5)

	lines := []line{
		{y: 20, x: 63, w: 316, size: 21, weight: doc.WeightSemibold, bold: true,
			text: "Technische Daten"},
		{y: 70, x: 72, w: 40, size: 15, weight: doc.WeightSemibold, bold: true, text: "Robot"},
		{y: 70, x: 487, w: 115, size: 15, weight: doc.WeightSemibold, bold: true,
			text: "Basisstation"},
	}
	for r := 0; r < 5; r++ {
		y := 130 + float64(r)*40
		lines = append(lines,
			line{y: y, x: 70, w: 100, size: 11, text: "Feld " + itoa(r)},
			line{y: y, x: 210, w: 120, size: 11, text: "Wert " + itoa(r)},
			line{y: y, x: 488, w: 100, size: 11, text: "Feldb " + itoa(r)},
			// Short of the drawn right edge, which is the whole point.
			line{y: y, x: 630, w: 195, size: 11, text: "Wertb " + itoa(r)})
	}

	page := blockPage(537, lines...)
	got := doc.RegionBlocks(page, wholePage(537), []doc.RuledTable{left, right}, nil)
	if len(got) == 0 {
		t.Fatal("no blocks")
	}
	if !strings.Contains(got[0].Text, "Technische Daten") {
		t.Errorf("the page reads %q first, want its own title — a table whose drawn box "+
			"overhangs its words fell through to the banner band\n%s",
			got[0].Text, strings.Join(texts(got), "\n"))
	}
	// And each table reads under its own heading rather than both under one.
	robot := strings.Index(blockTexts(got), "Robot")
	basis := strings.Index(blockTexts(got), "Basisstation")
	feld0 := strings.Index(blockTexts(got), "Feld 0")
	feldb0 := strings.Index(blockTexts(got), "Feldb 0")
	if !(robot < feld0 && feld0 < basis && basis < feldb0) {
		t.Errorf("the two tables are not each under their own heading: %s", blockTexts(got))
	}
}

// gridTable is a plain two-column ruled grid.
func gridTable(x0, mid, x1, y0 float64, rows int) doc.RuledTable {
	tab := doc.RuledTable{
		Box:  doc.CellRect{X0: x0, Y0: y0, X1: x1, Y1: y0 + float64(rows)*40},
		Rows: rows, Cols: 2,
	}
	for r := 0; r < rows; r++ {
		top := y0 + float64(r)*40
		tab.Cells = append(tab.Cells,
			doc.RuledCell{Row: r, Col: 0, ColSpan: 1,
				Rect: doc.CellRect{X0: x0, Y0: top, X1: mid, Y1: top + 40}},
			doc.RuledCell{Row: r, Col: 1, ColSpan: 1,
				Rect: doc.CellRect{X0: mid, Y0: top, X1: x1, Y1: top + 40}})
	}
	return tab
}
