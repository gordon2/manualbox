package doc_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
)

// Hermetic tests for the furniture pass. No PDF and no poppler: the pages are
// built here, so each clause and each threshold is stated where it can be read
// against the reasoning in furniture.go. Every shape below is one the two real
// manuals actually print, and the fixture tests in furniture_fixture_test.go hold
// the same rules against them.
//
// The geometry is the sequential manual's, measured: a 918x620 page setting its
// tab at x=28 y=58 in 11pt, its running head at x=55 y=52 in 21pt, its body at
// x=55 from y=95 on a 22.5-unit pitch, and its folio at x=55 y=576 in 12pt.

// furnitureSection builds one language's section: n pages, each carrying the
// lines build returns for it, and one whole-page region per page.
func furnitureSection(n int, lang string, build func(page, i int) []line) ([]doc.PageRuns, []doc.Region) {
	pages := make([]doc.PageRuns, 0, n)
	regions := make([]doc.Region, 0, n)
	for i := 0; i < n; i++ {
		no := 23 + i
		pages = append(pages, *blockPage(no, build(no, i)...))
		regions = append(regions, doc.Region{
			Page: no, X0: 0, X1: testBlockPageWidth, Lang: lang, Source: doc.SourceRepertoire,
		})
	}
	return pages, regions
}

// tabLine is the sequential manual's language tab: 11pt medium in the top-left
// margin, at the same y on every page of a section.
func tabLine(text string) line {
	return line{y: 58, x: 28, w: 14, size: 11, weight: doc.WeightMedium, text: text}
}

// headLine is a 21pt semibold heading across the top of the measure.
func headLine(text string) line {
	return line{y: 52, x: 55, w: 202, size: 21, weight: doc.WeightSemibold, bold: true, text: text}
}

// folioLine is the printed page number in the footer.
func folioLine(text string) line {
	return line{y: 576, x: 55, w: 11, size: 12, text: text}
}

func blockTextsOf(blocks []doc.Block) []string {
	out := make([]string, len(blocks))
	for i := range blocks {
		out[i] = blocks[i].Text
	}
	return out
}

func furnitureOf(blocks []doc.Block) []string {
	var out []string
	for i := range blocks {
		if blocks[i].Furniture {
			out = append(out, blocks[i].Text)
		}
	}
	return out
}

func contentOf(blocks []doc.Block) []string {
	var out []string
	for i := range blocks {
		if !blocks[i].Furniture {
			out = append(out, blocks[i].Text)
		}
	}
	return out
}

// TestFurnitureFindsATabOnEveryPage is clause 1 at its plainest: the same two
// letters at the same height on all 16 pages of a section, which is what the
// sequential manual prints on all 34 of its sections.
func TestFurnitureFindsATabOnEveryPage(t *testing.T) {
	pages, regions := furnitureSection(16, "de", func(page, i int) []line {
		lines := []line{tabLine("DE"), headLine(fmt.Sprintf("Kapitel %d", i))}
		return append(lines, bodyLines(95, 22.5, 6, fmt.Sprintf("Absatz auf Seite %d", page))...)
	})

	fur := doc.FindFurniture(pages, regions, nil, nil)
	if fur.Tabs != 16 {
		t.Errorf("claimed %d tab run(s) over 16 pages printing one each", fur.Tabs)
	}
	if fur.Folios != 0 {
		t.Errorf("claimed %d folio(s) where no folios were supplied", fur.Folios)
	}

	blocks := doc.RegionsBlocks(pages, regions, nil, nil, fur)
	got := furnitureOf(blocks)
	if len(got) != 16 {
		t.Fatalf("%d furniture block(s), want 16: %v", len(got), got)
	}
	for _, s := range got {
		if s != "DE" {
			t.Errorf("furniture block reads %q, want the tab", s)
		}
	}
	for i := range blocks {
		if strings.Contains(blocks[i].Text, "DE") && !blocks[i].Furniture {
			t.Errorf("the tab reached a content block: %q", blocks[i].Text)
		}
	}
}

// TestFurnitureIsLastInItsRegionAndKeepsContentContiguous pins the ordering
// decision RegionBlocks records: content keeps 0..n-1 so that "paragraph 4 of the
// German region of page 62" means the fourth paragraph a reader sees.
func TestFurnitureIsLastInItsRegionAndKeepsContentContiguous(t *testing.T) {
	pages, regions := furnitureSection(8, "de", func(page, i int) []line {
		lines := []line{tabLine("DE"), headLine(fmt.Sprintf("Kapitel %d", i))}
		lines = append(lines, bodyLines(95, 22.5, 3, fmt.Sprintf("Erster Absatz %d", page))...)
		return append(lines, folioLine(fmt.Sprintf("%d", page-6)))
	})
	folios := map[int]int{}
	for i := 0; i < 8; i++ {
		folios[23+i] = 23 + i - 6
	}

	fur := doc.FindFurniture(pages, regions, nil, folios)
	blocks := doc.RegionsBlocks(pages, regions, nil, nil, fur)

	perPage := map[int][]doc.Block{}
	for i := range blocks {
		perPage[blocks[i].Page] = append(perPage[blocks[i].Page], blocks[i])
	}
	for page, bs := range perPage {
		seenFurniture := false
		for i := range bs {
			if bs[i].Index != i {
				t.Errorf("page %d block %d carries index %d", page, i, bs[i].Index)
			}
			if bs[i].Furniture {
				seenFurniture = true
				continue
			}
			if seenFurniture {
				t.Errorf("page %d: content block %q sits after furniture", page, bs[i].Text)
			}
		}
		if !seenFurniture {
			t.Errorf("page %d produced no furniture at all", page)
		}
	}
}

// TestFurnitureUnGluesATabSetOnAHeadingsBaseline is the case that decides where
// this pass belongs. The column manual sets its tab on the SAME baseline as the
// running head, so the tab is not a block of its own: page 14 arrives as one
// heading reading "D Trockensaugen". Removing a block is wrong; taking two
// characters off the front of the text is unsafe. Removing the RUN is neither.
func TestFurnitureUnGluesATabSetOnAHeadingsBaseline(t *testing.T) {
	heads := []string{"Trockensaugen", "Waschsaugen", "Wartung", "Fehlerbehebung",
		"Trockensaugen", "Waschsaugen"}
	pages, regions := furnitureSection(6, "de", func(page, i int) []line {
		lines := []line{
			{y: 16, x: 340, w: 10, size: 11, weight: doc.WeightMedium, text: "D"},
			{y: 16, x: 380, w: 120, size: 17, weight: doc.WeightMedium, text: heads[i]},
		}
		return append(lines, bodyLines(95, 16, 5, fmt.Sprintf("Absatz auf Seite %d", page))...)
	})

	fur := doc.FindFurniture(pages, regions, nil, nil)
	if fur.Tabs != 6 {
		t.Fatalf("claimed %d tab run(s) over 6 glued pages", fur.Tabs)
	}

	blocks := doc.RegionsBlocks(pages, regions, nil, nil, fur)
	for i := range blocks {
		b := &blocks[i]
		if b.Furniture {
			if b.Text != "D" {
				t.Errorf("page %d furniture reads %q, want the bare tab", b.Page, b.Text)
			}
			continue
		}
		if strings.HasPrefix(b.Text, "D ") {
			t.Errorf("page %d still serves the tab glued to content: %q", b.Page, b.Text)
		}
	}
	// And what remains on the first page is the head the printer set, not a
	// substring of it.
	first := contentOf(blocks)
	if len(first) == 0 || first[0] != "Trockensaugen" {
		t.Errorf("page 23's first content block is %q, want %q", first[0], "Trockensaugen")
	}
}

// TestFurnitureUnGluesATabThatJoinedTheBlockBelow is the same defect with the tab
// at the END. The sequential manual's pages 34 and 35 set the running head at
// y=52 and the tab at y=58, close enough for the paragraph rule to fold them into
// one block reading "Fehlersuche DE" — so a rule that strips a leading token
// would leave both of those untouched.
func TestFurnitureUnGluesATabThatJoinedTheBlockBelow(t *testing.T) {
	pages, regions := furnitureSection(6, "de", func(page, i int) []line {
		lines := []line{
			{y: 52, x: 28, w: 150, size: 17, weight: doc.WeightMedium,
				text: fmt.Sprintf("Fehlersuche %d", i)},
			tabLine("DE"),
		}
		return append(lines, bodyLines(120, 22.5, 5, fmt.Sprintf("Absatz %d", page))...)
	})

	before := doc.RegionsBlocks(pages, regions, nil, nil, nil)
	glued := 0
	for i := range before {
		if strings.Contains(before[i].Text, " DE") {
			glued++
		}
	}
	if glued != 6 {
		t.Fatalf("the fixture does not reproduce the glued shape: %d of 6 pages, %v",
			glued, blockTextsOf(before)[:3])
	}

	fur := doc.FindFurniture(pages, regions, nil, nil)
	after := doc.RegionsBlocks(pages, regions, nil, nil, fur)
	for i := range after {
		b := &after[i]
		if !b.Furniture && strings.Contains(b.Text, "DE") {
			t.Errorf("page %d still serves the tab as content: %q", b.Page, b.Text)
		}
		if !b.Furniture && strings.HasPrefix(b.Text, "Fehlersuche") &&
			b.Text != fmt.Sprintf("Fehlersuche %d", b.Page-23) {
			t.Errorf("page %d's running head came back as %q", b.Page, b.Text)
		}
	}
	if got := len(furnitureOf(after)); got != 6 {
		t.Errorf("%d furniture block(s) over 6 pages", got)
	}
}

// TestFurnitureClaimsARunningHeadThatNeverChanges pins that clause 1 is stated
// generally on purpose, and it is the one place a caller can be surprised: a
// section printing the SAME running head at the same height on most of its pages
// loses it, tab or not. Neither fixture does that — the column manual's head
// names the chapter and changes every few pages, and the sequential manual's
// names the section and repeats on at most 4 of 16 — so the behaviour is
// asserted here rather than measured there.
//
// It was found by accident, by a version of the test above that put one heading
// on all 16 pages, and it is the correct reading: a line the printer set on every
// page of a section because of where the page is IS furniture, and the fact that
// this one happens to be words rather than two letters changes nothing about the
// evidence.
func TestFurnitureClaimsARunningHeadThatNeverChanges(t *testing.T) {
	pages, regions := furnitureSection(10, "de", func(page, i int) []line {
		lines := []line{headLine("Sicherheitshinweise")}
		return append(lines, bodyLines(95, 22.5, 5, fmt.Sprintf("Absatz %d", page))...)
	})
	fur := doc.FindFurniture(pages, regions, nil, nil)
	if fur.Tabs != 10 {
		t.Errorf("claimed %d run(s) for a head printed identically on all 10 pages", fur.Tabs)
	}
	blocks := doc.RegionsBlocks(pages, regions, nil, nil, fur)
	for _, s := range furnitureOf(blocks) {
		if s != "Sicherheitshinweise" {
			t.Errorf("furniture block reads %q", s)
		}
	}
}

// TestFurnitureKeepsASectionGenuinelyTitledA is the false positive the whole
// design is arranged around. The sequential manual titles sections "A", "B", "C"
// and "E" — 28 of its pages head a page with a bare "A" — so a one-letter line at
// the top of a page is not evidence of a tab, and only repetition across a
// section's own pages is.
func TestFurnitureKeepsASectionGenuinelyTitledA(t *testing.T) {
	titles := []string{"A", "B", "C", "A", "D", "E", "B", "C"}
	pages, regions := furnitureSection(8, "de", func(page, i int) []line {
		lines := []line{headLine(titles[i])}
		return append(lines, bodyLines(95, 22.5, 5, fmt.Sprintf("Absatz auf Seite %d", page))...)
	})

	fur := doc.FindFurniture(pages, regions, nil, nil)
	if fur.Total() != 0 {
		t.Errorf("claimed %d run(s) of furniture on a section whose titles are single "+
			"letters printed 2 or 3 times each", fur.Total())
	}
	blocks := doc.RegionsBlocks(pages, regions, nil, nil, fur)
	seen := map[string]bool{}
	for i := range blocks {
		if blocks[i].Furniture {
			t.Errorf("page %d: %q was called furniture", blocks[i].Page, blocks[i].Text)
		}
		seen[blocks[i].Text] = true
	}
	for _, want := range []string{"A", "B", "C", "D", "E"} {
		if !seen[want] {
			t.Errorf("the section titled %q was lost", want)
		}
	}
}

// TestFurnitureShareIsWhereTheMeasurementPutIt walks the share threshold. The
// numbers are the ones furnitureMinShare records: the widest thing in either
// manual that is NOT furniture repeats on 0.29 of its language's pages, and the
// narrowest tab on 0.96.
//
// It asks the question of [doc.Furniture.Tabs] and not of Total, and that is the
// point rather than a detail. The pages carrying the tab here are the FIRST `on` of
// the section — consecutive — so below the cut clause 3 reads them as a running
// head and claims all but the first, which is correct and is asserted just below.
// Reading Total would let clause 3's answer stand in for clause 1's and the share
// could be moved anywhere without this failing.
func TestFurnitureShareIsWhereTheMeasurementPutIt(t *testing.T) {
	const n = 20
	for _, tc := range []struct {
		on   int
		want bool
	}{
		{on: 5, want: false}, // 0.25 -- a running head repeated over five pages
		{on: 6, want: false}, // 0.30 -- the ceiling of everything real, measured
		{on: 9, want: false}, // 0.45
		{on: 10, want: true}, // 0.50 -- the cut
		{on: 19, want: true}, // 0.95
		{on: 20, want: true}, // 1.00 -- every page, which is what a tab does
	} {
		pages, regions := furnitureSection(n, "de", func(page, i int) []line {
			lines := []line{}
			if i < tc.on {
				lines = append(lines, tabLine("DE"))
			}
			return append(lines, bodyLines(95, 22.5, 5, fmt.Sprintf("Absatz %d", page))...)
		})
		fur := doc.FindFurniture(pages, regions, nil, nil)
		if got := fur.Tabs > 0; got != tc.want {
			t.Errorf("a tab on %d of %d pages (%.2f): clause 1 claimed it = %v, want %v",
				tc.on, n, float64(tc.on)/n, got, tc.want)
		}
		// Whichever clause owns it, a line printed on `on` consecutive pages leaves
		// exactly one of them: clause 1 takes all `on` and clause 3 takes `on`-1.
		wantHeads := tc.on - 1
		if tc.want {
			wantHeads = 0
		}
		if fur.Heads != wantHeads {
			t.Errorf("a tab on %d of %d pages: clause 3 claimed %d head(s), want %d",
				tc.on, n, fur.Heads, wantHeads)
		}
	}
}

// TestFurnitureNeedsFourPagesWhateverTheShare is the other guard, and it is not
// belt and braces. The column manual has a two-page spread of service addresses
// whose language no signal could name; at a share of 0.5 and no page floor, every
// line printed on both of them is furniture, because one page out of two is a
// half.
func TestFurnitureNeedsFourPagesWhateverTheShare(t *testing.T) {
	for _, n := range []int{2, 3, 4} {
		pages, regions := furnitureSection(n, "de", func(page, i int) []line {
			lines := []line{tabLine("DE")}
			return append(lines, bodyLines(95, 22.5, 4, "Kundendienststellen")...)
		})
		fur := doc.FindFurniture(pages, regions, nil, nil)
		if got, want := fur.Total() > 0, n >= 4; got != want {
			t.Errorf("a tab on all %d pages of a %d-page section: furniture=%v, want %v",
				n, n, got, want)
		}
	}
}

// TestFurnitureIsPositionalNotTextual: the same words at a different height on
// each page are not furniture, however often they repeat. This is what stops a
// stock phrase — "Hinweis:" opens a note on ten pages of the sequential manual's
// German section — from being taken for a running head.
func TestFurnitureIsPositionalNotTextual(t *testing.T) {
	pages, regions := furnitureSection(10, "de", func(page, i int) []line {
		lines := []line{{y: 95 + float64(i)*22.5, x: 55, w: 80, size: 17,
			weight: doc.WeightMedium, text: "Hinweis:"}}
		return append(lines, bodyLines(300, 22.5, 5, fmt.Sprintf("Absatz %d", page))...)
	})
	fur := doc.FindFurniture(pages, regions, nil, nil)
	if fur.Total() != 0 {
		t.Errorf("claimed %d run(s) for a phrase that repeats at a different height "+
			"on every page", fur.Total())
	}
}

// TestFurnitureFindsTheFolioByAgreeingWithPdftotext is clause 2. The values
// differ page to page, so clause 1 cannot see them; what identifies them is that
// the second extraction of the same bytes read the same string as this page's
// printed page number.
func TestFurnitureFindsTheFolioByAgreeingWithPdftotext(t *testing.T) {
	const n = 8
	folios := map[int]int{}
	pages, regions := furnitureSection(n, "de", func(page, i int) []line {
		folios[page] = page - 6
		lines := []line{folioLine(fmt.Sprintf("%d", page-6))}
		return append(lines, bodyLines(95, 22.5, 5, fmt.Sprintf("Absatz %d", page))...)
	})

	fur := doc.FindFurniture(pages, regions, nil, folios)
	if fur.Folios != n {
		t.Errorf("claimed %d folio(s) over %d pages each printing one", fur.Folios, n)
	}
	if fur.Tabs != 0 {
		t.Errorf("clause 1 claimed %d run(s); every folio prints a different number", fur.Tabs)
	}

	// Without the folios there is nothing to agree with, and the numbers stay.
	if bare := doc.FindFurniture(pages, regions, nil, nil); bare.Total() != 0 {
		t.Errorf("claimed %d run(s) with no printed folios supplied", bare.Total())
	}

	blocks := doc.RegionsBlocks(pages, regions, nil, nil, fur)
	for _, s := range furnitureOf(blocks) {
		if len(s) > 2 {
			t.Errorf("furniture block %q is not a folio", s)
		}
	}
}

// TestFurnitureLeavesANumberThatIsNotTheFolio: a table cell or a callout that
// happens to be a number at a repeated height is content, because it does not
// agree with what the page prints as its own page number.
func TestFurnitureLeavesANumberThatIsNotTheFolio(t *testing.T) {
	const n = 8
	folios := map[int]int{}
	pages, regions := furnitureSection(n, "de", func(page, i int) []line {
		folios[page] = page - 6
		// A callout numbered 1..8 at a fixed height beside a diagram, and NOT this
		// page's folio except by accident on one page.
		lines := []line{{y: 300, x: 55, w: 11, size: 12, text: fmt.Sprintf("%d", i+1)}}
		return append(lines, bodyLines(95, 22.5, 5, fmt.Sprintf("Absatz %d", page))...)
	})

	fur := doc.FindFurniture(pages, regions, nil, folios)
	if fur.Folios != 0 {
		t.Errorf("claimed %d folio(s) among callout numbers at a fixed height", fur.Folios)
	}
}

// TestFurnitureCountsAgainstTheLanguagesOwnPages is the denominator the first
// attempt got wrong. Two languages of 8 pages each, converted together as one
// household of 16: each tab is on 8 of 16 pages of the conversion and on 8 of 8
// pages of its own section.
func TestFurnitureCountsAgainstTheLanguagesOwnPages(t *testing.T) {
	var pages []doc.PageRuns
	var regions []doc.Region
	for i := 0; i < 16; i++ {
		no := 23 + i
		lang, tab := "de", "DE"
		if i >= 8 {
			lang, tab = "ru", "RU"
		}
		lines := []line{tabLine(tab)}
		lines = append(lines, bodyLines(95, 22.5, 5, fmt.Sprintf("Absatz %d", no))...)
		pages = append(pages, *blockPage(no, lines...))
		regions = append(regions, doc.Region{Page: no, X0: 0, X1: testBlockPageWidth,
			Lang: lang, Source: doc.SourceRepertoire})
	}

	inScope := map[string]bool{"de": true, "ru": true}
	fur := doc.FindFurniture(pages, regions, inScope, nil)
	if fur.Tabs != 16 {
		t.Errorf("claimed %d tab run(s); each of two 8-page sections prints one on "+
			"every page, and 8 of 16 converted pages is 0.5 only by accident", fur.Tabs)
	}

	// And the same section read alone reaches the same conclusion, which is what
	// makes the pass independent of who is reading.
	alone := doc.FindFurniture(pages[:8], regions[:8], map[string]bool{"de": true}, nil)
	if alone.Tabs != 8 {
		t.Errorf("claimed %d tab run(s) reading German alone", alone.Tabs)
	}
}

// TestFurnitureSkipsARegionWithNoLanguage: a region no signal could name has no
// section for a share to be a share of, so it is left entirely alone. This is
// what keeps the column manual's unnamed two-page spread of service addresses out
// of the pass, which is the accident the page floor also guards.
func TestFurnitureSkipsARegionWithNoLanguage(t *testing.T) {
	pages, regions := furnitureSection(8, "", func(page, i int) []line {
		lines := []line{tabLine("DE")}
		return append(lines, bodyLines(95, 22.5, 5, "Kundendienststellen")...)
	})
	if fur := doc.FindFurniture(pages, regions, nil, nil); fur.Total() != 0 {
		t.Errorf("claimed %d run(s) inside regions with no language", fur.Total())
	}
}

// TestRegionBlocksWithNilFurnitureIsUnchanged: nil is a normal argument and it
// must produce exactly the reading that shipped before this existed, furniture
// and all. Without this the pass could be "fixing" a defect it introduced.
func TestRegionBlocksWithNilFurnitureIsUnchanged(t *testing.T) {
	pages, regions := furnitureSection(8, "de", func(page, i int) []line {
		lines := []line{tabLine("DE"), headLine(fmt.Sprintf("Kapitel %d", i))}
		return append(lines, bodyLines(95, 22.5, 4, fmt.Sprintf("Absatz %d", page))...)
	})

	bare := doc.RegionsBlocks(pages, regions, nil, nil, nil)
	if len(furnitureOf(bare)) != 0 {
		t.Fatalf("nil furniture produced %d flagged block(s)", len(furnitureOf(bare)))
	}
	tabs := 0
	for i := range bare {
		if bare[i].Text == "DE" {
			tabs++
		}
	}
	if tabs != 8 {
		t.Errorf("the tab came back as a block on %d of 8 pages with no pass run", tabs)
	}
}

// TestFurnitureIsDeterministic: the pass walks maps, and a conversion whose block
// indices depend on map order is a conversion that inserts a parallel set of rows
// the second time a job runs.
func TestFurnitureIsDeterministic(t *testing.T) {
	folios := map[int]int{}
	pages, regions := furnitureSection(12, "de", func(page, i int) []line {
		folios[page] = page - 6
		lines := []line{tabLine("DE"), headLine(fmt.Sprintf("Kapitel %d", i)),
			folioLine(fmt.Sprintf("%d", page-6))}
		return append(lines, bodyLines(95, 22.5, 5, fmt.Sprintf("Absatz %d", page))...)
	})

	first := doc.RegionsBlocks(pages, regions,
		nil, nil, doc.FindFurniture(pages, regions, nil, folios))
	for run := 0; run < 5; run++ {
		again := doc.RegionsBlocks(pages, regions,
			nil, nil, doc.FindFurniture(pages, regions, nil, folios))
		if len(again) != len(first) {
			t.Fatalf("run %d produced %d blocks against %d", run, len(again), len(first))
		}
		for i := range first {
			if first[i].Text != again[i].Text || first[i].Index != again[i].Index ||
				first[i].Furniture != again[i].Furniture {
				t.Fatalf("run %d block %d diverged: %+v against %+v", run, i, again[i], first[i])
			}
		}
	}
}

// TestFurnitureNoteNamesItsEvidence: a note that cannot be held against the page
// is not evidence, which is the stance every other note in this package takes.
func TestFurnitureNoteNamesItsEvidence(t *testing.T) {
	pages, regions := furnitureSection(10, "de", func(page, i int) []line {
		lines := []line{tabLine("DE")}
		return append(lines, bodyLines(95, 22.5, 4, fmt.Sprintf("Absatz %d", page))...)
	})
	blocks := doc.RegionsBlocks(pages, regions, nil, nil,
		doc.FindFurniture(pages, regions, nil, nil))
	for i := range blocks {
		if !blocks[i].Furniture {
			continue
		}
		note := blocks[i].Note
		for _, want := range []string{"page furniture", `"DE"`, "y=58", "10 of this language's 10"} {
			if !strings.Contains(note, want) {
				t.Errorf("note %q does not say %q", note, want)
			}
		}
	}
}
