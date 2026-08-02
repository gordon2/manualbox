package doc_test

import (
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
)

// The real pages behind blocksstrips_test.go, pinned by the strings they print.
//
// These are the eight two-column Russian maintenance pages of the sequential manual
// and the two parts pages of the column manual: every page of either document where
// a block used to cross the gutter. The reading below was checked against
// `pdftoppm -r 108`, which is this coordinate space 1:1.

// TestTheRussianMaintenancePagesReadInColumns is the defect, named page by page.
// Before reading order got its own strips, page 530's two section banners were one
// block reading "Мешок для сбора пыли Основная щетка" and its two columns' first
// step was one sentence ending "…мешок для сбора 1. Надавите на".
func TestTheRussianMaintenancePagesReadInColumns(t *testing.T) {
	_, pages, regions, _ := regionsOfFixture(t, "dreame-l40-ultra")

	// Page, then the pairs of strings that must not end up in one block: each pair is
	// the left column's text and the right column's on the same printed baseline.
	welds := map[int][][2]string{
		525: {{"6. Добавление чистящего раствора", "7. Заполните бак"}},
		530: {{"Мешок для сбора пыли", "Основная щетка"},
			{"утилизируйте мешок для сбора", "Надавите на"}},
		531: {{"Боковая щетка", "Держатели насадок"}, {"Всенаправленное колесо", "Насадка для швабры"}},
		532: {{"Контейнер для пыли и фильтр", "Датчики робота"}},
		533: {{"Зарядные контакты", "Бак для отработанной воды"}},
		537: {{"Робот", "Базовая станция"}},
	}
	for page, pairs := range welds {
		blocks := blocksOfPage(t, pages, regions, page, "ru")
		for i := range blocks {
			for _, pair := range pairs {
				if strings.Contains(blocks[i].Text, pair[0]) && strings.Contains(blocks[i].Text, pair[1]) {
					t.Errorf("page %d block %d welds the two columns: %q",
						page, blocks[i].Index, truncate(blocks[i].Text, 120))
				}
			}
		}
		if t.Failed() || page == 537 {
			// 537 is left out of the order check and only of that: it is the
			// specification page, whose reading order comes from its ruled tables, and
			// these blocks are built without them. Its welds are still asserted above,
			// because those are a property of the text and not of the strokes.
			continue
		}
		// And the columns do not interleave: with the gutter at x=440-495 on all of
		// these pages, nothing left of it may be read after anything right of it.
		lastLeft, firstRight := -1, len(blocks)
		for i := range blocks {
			switch {
			case blocks[i].X1 < 460:
				lastLeft = i
			case blocks[i].X0 > 460 && i < firstRight:
				firstRight = i
			}
		}
		if lastLeft > firstRight {
			t.Errorf("page %d interleaves: block %d is in the left column and is read "+
				"after block %d in the right", page, lastLeft, firstRight)
		}
	}
}

// TestTheRussianBannersAreTheirOwnBlocks is the positive form, with the strings the
// page prints. A section banner reaching only its own column is a heading, and there
// are two of them on page 530 rather than one of both.
func TestTheRussianBannersAreTheirOwnBlocks(t *testing.T) {
	_, pages, regions, _ := regionsOfFixture(t, "dreame-l40-ultra")
	blocks := blocksOfPage(t, pages, regions, 530, "ru")

	want := []string{
		"Мешок для сбора пыли",
		"1. Снимите крышку отсека для пыли и утилизируйте мешок для сбора",
		"3. Установите новый мешок для сбора пыли, затем установите крышку",
		"Основная щетка",
		"1. Надавите на зажимы защиты щетки, чтобы извлечь защиту щетки и",
	}
	var got []string
	for i := range blocks {
		got = append(got, blocks[i].Text)
	}
	at := -1
	for _, w := range want {
		next := -1
		for i, g := range got {
			if i > at && g == w {
				next = i
				break
			}
		}
		if next < 0 {
			t.Errorf("page 530 does not print %q as a block of its own, after the one before "+
				"it\n%s", w, strings.Join(got, "\n"))
			return
		}
		at = next
	}
}

// TestTheColumnManualsPartsListIsItems is the same defect on the other document. Page
// 11's numbered parts list arrived as two run-together blocks of 7 and 19 printed
// lines, each with the diagram's callout numbers spliced into the words:
// "17 Staubbehälter für Grobschmutz und Feinstaub 7 18 Saugschlauch*…".
func TestTheColumnManualsPartsListIsItems(t *testing.T) {
	_, pages, regions, _ := regionsOfFixture(t, "thomas-drybox-amfibia")
	blocks := blocksOfPage(t, pages, regions, 11, "de")

	items := 0
	for i := range blocks {
		b := &blocks[i]
		if b.Kind == doc.BlockListItem && b.X0 > 500 {
			items++
		}
		if strings.Contains(b.Text, "Staubbehälter für Grobschmutz") &&
			strings.Contains(b.Text, "Saugschlauch") {
			t.Errorf("block %d welds the parts list to the diagram's callouts: %q",
				b.Index, truncate(b.Text, 140))
		}
	}
	// The page prints 39 numbered items in a column of its own, right of the diagram.
	//
	// 37 and not 39: two of the printed items wrap onto a second line and are folded
	// into the item above by the paragraph rule, which is a different question from
	// this one. Before the strips they were 6.
	if items < 37 {
		t.Errorf("the parts list came back as %d list items in its own column, want the 37 "+
			"measured — it was 6 while the column was welded to the diagram's callouts",
			items)
	}
}
