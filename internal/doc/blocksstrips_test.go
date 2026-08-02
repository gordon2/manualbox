package doc_test

import (
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
)

// The pages that have two columns and no second column.
//
// [doc.DetectColumns] answers "what are this page's text columns", and a Column is a
// published fact — language attribution reads it — so it insists on [minColumnRuns]
// runs before it names one. Reading order asks a narrower question and the same gate
// is wrong for it: a maintenance page with six runs in its right-hand column is still
// two columns to a reader, and read as one strip its two banners come back spliced.
//
// Every shape here is drawn from a page of one of the two manuals and is measured in
// blocksstrips_fixture_test.go against that page.

// sparsePage is the sequential manual's page 530, to the shape that matters: two
// columns of a maintenance page, each a banner and a few lines, sharing baselines
// across a gutter at x=440-495.
//
// Six runs on the right and nine on the left, which is what defeats minColumnRuns=8,
// and 15 runs on the whole page, which is what defeats maxGutterCrossings=4 — at that
// density no x of the page is crossed by more than four runs, so the projection reads
// the right-hand half as one gutter.
func sparsePage(no int) *doc.PageRuns { return blockPage(no, sparseLines(0)...) }

// sparseLines returns the page's lines, shifted down the page by dy.
func sparseLines(dy float64) []line {
	out := []line{
		line{y: 20, x: 65, w: 130, size: 15, weight: doc.WeightSemibold, bold: true,
			text: "Mешок для сбора"},
		line{y: 45, x: 65, w: 365, size: 14, text: "1. Снимите крышку отсека для пыли"},
		line{y: 60, x: 65, w: 30, size: 14, text: "пыли."},
		line{y: 200, x: 65, w: 355, size: 14, text: "Примечание. Потяните ручку вверх"},
		line{y: 215, x: 66, w: 260, size: 14, text: "2. Очистите пыль и грязь с фильтра"},
		line{y: 350, x: 65, w: 370, size: 14, text: "3. Установите новый мешок для сбора"},
		line{y: 365, x: 65, w: 135, size: 14, text: "отсека для пыли на место."},
		line{y: 385, x: 65, w: 300, size: 14, text: "4. Закройте крышку отсека для пыли."},
		line{y: 400, x: 65, w: 180, size: 14, text: "и проверьте фиксацию."},

		line{y: 20, x: 504, w: 90, size: 15, weight: doc.WeightSemibold, bold: true,
			text: "Основная щетка"},
		line{y: 46, x: 497, w: 370, size: 14, text: "1. Надавите на зажимы защиты щетки"},
		line{y: 61, x: 497, w: 130, size: 14, text: "достать щетку из робота."},
		line{y: 300, x: 496, w: 348, size: 14, text: "2. Снимите крышки щетки с обоих"},
		line{y: 315, x: 496, w: 340, size: 14, text: "рисунке. Для удаления запутавшихся"},
	}
	for i := range out {
		out[i].y += dy
	}
	return out
}

func texts(blocks []doc.Block) []string {
	out := make([]string, len(blocks))
	for i := range blocks {
		out[i] = blocks[i].Text
	}
	return out
}

// TestASparsePageIsNotOneColumn is the defect, stated as the reading it produced. Two
// section banners on one baseline either side of a gutter were one block reading
// "Mешок для сбора Основная щетка", and the two columns' step 1 was one sentence.
func TestASparsePageIsNotOneColumn(t *testing.T) {
	page := sparsePage(530)

	// The premise, asserted rather than assumed: the column detector really does
	// report one column here, so nothing downstream can lean on it.
	if got := doc.DetectColumns(page.Runs, page.Width, page.Height); len(got.Columns) != 1 {
		t.Fatalf("DetectColumns found %d columns on the sparse page, want 1 — this test "+
			"pins what happens when it finds one, and the page no longer does that: %s",
			len(got.Columns), got.Note)
	}

	got := doc.RegionBlocks(page, &doc.Region{Page: 530, X0: 0, X1: page.Width, Lang: "ru"}, nil, nil)
	for i := range got {
		if strings.Contains(got[i].Text, "сбора") && strings.Contains(got[i].Text, "Основная") {
			t.Errorf("block %d welds the two columns' banners: %q", i, got[i].Text)
		}
		if strings.Contains(got[i].Text, "Снимите крышку") && strings.Contains(got[i].Text, "Надавите") {
			t.Errorf("block %d welds the two columns' first step: %q", i, got[i].Text)
		}
	}

	// And the whole left column is read before any of the right one, which is the
	// positive form of the same claim.
	lastLeft, firstRight := -1, len(got)
	for i := range got {
		switch {
		case got[i].X1 < 460 && i > lastLeft:
			lastLeft = i
		case got[i].X0 > 460 && i < firstRight:
			firstRight = i
		}
	}
	if lastLeft > firstRight {
		t.Errorf("the columns interleave: left-hand block %d is read after right-hand "+
			"block %d\n%s", lastLeft, firstRight, strings.Join(texts(got), "\n"))
	}
}

// TestAStripNeedsAnEmptyCorridor is the other half of the bound, and it is what keeps
// the fallback from cutting a page wherever the text happens to be thin. The gutter of
// a sparse page is empty; the space inside a spec table's row is not, because the rows
// above and below reach across it.
func TestAStripNeedsAnEmptyCorridor(t *testing.T) {
	// A specification table set as one column: a label at x=65 and a value at x=380,
	// 216 units apart — the widest within-column gap either manual prints, from the
	// sequential manual's "Model … RLL77SE". Nothing crosses that space either, until
	// one row is set to the full measure, which is what a real spec page does.
	var lines []line
	for i := 0; i < 6; i++ {
		y := 20 + float64(i)*20
		lines = append(lines,
			line{y: y, x: 65, w: 120, size: 14, text: "Modell" + itoa(i)},
			line{y: y, x: 380, w: 90, size: 14, text: "RLL77SE" + itoa(i)})
	}
	lines = append(lines, line{y: 160, x: 65, w: 405, size: 14,
		text: "Bei normalem Gebrauch ist zwischen der Antenne und dem Koerper"})

	page := blockPage(21, lines...)
	got := doc.RegionBlocks(page, wholePage(21), nil, nil)

	for i := range got {
		if strings.Contains(got[i].Text, "Modell0") && !strings.Contains(got[i].Text, "RLL77SE0") {
			t.Errorf("block %d split a table row at its own column divider: %q", i, got[i].Text)
		}
	}
}

// TestABannerAcrossTheMeasureIsNotSplit guards the shape the fallback must leave
// alone: a heading printed across both columns. It is one run, so no corridor lies
// inside it, and it also fills the corridor for every line beside it.
func TestABannerAcrossTheMeasureIsNotSplit(t *testing.T) {
	lines := []line{{y: 20, x: 65, w: 760, size: 21, weight: doc.WeightSemibold, bold: true,
		text: "Plановое обслуживание des ganzen Bogens"}}
	page := blockPage(63, append(lines, sparseLines(60)...)...)

	got := doc.RegionBlocks(page, &doc.Region{Page: 63, X0: 0, X1: page.Width, Lang: "ru"}, nil, nil)
	if len(got) == 0 {
		t.Fatal("no blocks")
	}
	if got[0].Text != "Plановое обслуживание des ganzen Bogens" {
		t.Errorf("the banner is %q, want it whole and read first\n%s",
			got[0].Text, strings.Join(texts(got), "\n"))
	}
}

// TestDenseColumnsAreUnchanged is the column manual's page 62, the whole-page German
// region of two columns that conversion.md names. The detector answers it and the
// fallback must never run.
func TestDenseColumnsAreUnchanged(t *testing.T) {
	var lines []line
	for i := 0; i < 8; i++ {
		y := 20 + float64(i)*18
		lines = append(lines,
			line{y: y, x: 43, w: 400, size: 14, text: "links Zeile " + itoa(i)},
			line{y: y + 2, x: 463, w: 400, size: 14, text: "rechts Zeile " + itoa(i)})
	}
	page := blockPage(62, lines...)
	if got := doc.DetectColumns(page.Runs, page.Width, page.Height); len(got.Columns) != 2 {
		t.Fatalf("DetectColumns found %d columns, want 2: %s", len(got.Columns), got.Note)
	}
	got := doc.RegionBlocks(page, wholePage(62), nil, nil)
	if len(got) != 2 {
		t.Fatalf("got %d blocks, want one paragraph per column:\n%s",
			len(got), strings.Join(texts(got), "\n"))
	}
	if !strings.HasPrefix(got[0].Text, "links") || strings.Contains(got[0].Text, "rechts") {
		t.Errorf("the first block is %q, want the left column whole", got[0].Text)
	}
}

// TestRightToLeftStripsAreReadRightToLeft is the sequential manual's page 216: an
// Arabic disposal page whose warning is in the right column and whose numbered
// removal guide is in the left. Read left first, the reader is handed step 1 before
// the paragraph that introduces it.
func TestRightToLeftStripsAreReadRightToLeft(t *testing.T) {
	lines := []line{
		{y: 20, x: 634, w: 230, size: 17, weight: doc.WeightSemibold, bold: true,
			text: "التخلص من البطارية"},
		{y: 60, x: 609, w: 255, size: 14, text: "تحتوي بطارية الليثيوم أيون المدمجة"},
		{y: 78, x: 664, w: 200, size: 14, text: "يجب إزالة البطارية من الجهاز"},
		{y: 96, x: 640, w: 224, size: 14, text: "يجب فصل الجهاز عن مصدر التيار"},
		{y: 114, x: 699, w: 165, size: 14, text: "يجب التخلص من البطارية بأمان"},

		{y: 60, x: 430, w: 157, size: 15, weight: doc.WeightSemibold, bold: true,
			text: "دليل الإزالة"},
		{y: 84, x: 340, w: 247, size: 14, text: "1. اقلب الروبوت واستخدم أداة مناسبة"},
		{y: 120, x: 351, w: 236, size: 14, text: "2. افصل الأطراف بين البطارية واللوحة"},
	}
	page := blockPage(216, lines...)
	got := doc.RegionBlocks(page, &doc.Region{Page: 216, X0: 0, X1: page.Width, Lang: "ar"}, nil, nil)
	if len(got) < 2 {
		t.Fatalf("got %d blocks", len(got))
	}
	// Asserted on the geometry rather than the words: bidi.go stores a right-to-left
	// line in logical order and this page's runs are built in it, so the text comes
	// back reversed and comparing strings here would be testing bidi.go instead.
	if got[0].X0 < 600 {
		t.Errorf("the first block is at x=%.0f-%.0f, want the RIGHT column of an Arabic "+
			"page\n%s", got[0].X0, got[0].X1, strings.Join(texts(got), "\n"))
	}
	if last := &got[len(got)-1]; last.X0 > 600 {
		t.Errorf("the last block is at x=%.0f-%.0f, want the LEFT column last",
			last.X0, last.X1)
	}

	// The same page in a left-to-right language reads the other way round, which is
	// what says the direction comes from the region and not from the geometry.
	ltr := doc.RegionBlocks(page, &doc.Region{Page: 216, X0: 0, X1: page.Width, Lang: "de"}, nil, nil)
	if len(ltr) == 0 || ltr[0].X0 > 600 {
		t.Errorf("a left-to-right region did not read its LEFT column first\n%s",
			strings.Join(texts(ltr), "\n"))
	}
}
