package doc_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/fixture"
)

// The acceptance tests docs/design/conversion.md asks for, in its own words: the
// column manual's German must come back as readable content in reading order,
// from the German column alone, with no Polish, Russian, Ukrainian or Kazakh text
// in it; and the sequential manual's German section must come back the same way
// from a page it owns outright.
//
// The negative is the one the contract calls the failure a reader would notice
// immediately, so it is asserted directly rather than inferred from a count.
//
// Both were also checked against 108 dpi renders while this was written — pages 62
// and 14 of the column manual and pages 23 and 24 of the sequential one — and the
// heading counts those pages produce are pinned below so that a change which
// silently loses them says so.

// blocksOfFixture reads a document's regions and the runs they were measured from.
//
// Two poppler passes, because doc.Result deliberately does not carry the runs:
// they are 3.8 MB of coordinates for the sequential manual and nothing stored
// needs them. Analyze is the only thing that resolves a page's language, and
// regions are what carry it into a block.
func blocksOfFixture(t *testing.T, name string) (m *fixture.Manifest, pages []doc.PageRuns, regions []doc.Region) {
	t.Helper()
	m, pages, regions, _ = blocksAndTablesOfFixture(t, name)
	return m, pages, regions
}

// blocksAndTablesOfFixture adds the ruled lines of the pages a scope will read.
//
// Only those pages, because the ruled lines cost a pdftocairo spawn each — 5.9 s
// over the column manual's 68 pages, 42.3 s over the sequential manual's 560 — and
// conversion.md's cost argument is precisely that they are read for the pages in
// scope and no others. A German scope is 26 pages of the one and 16 of the other.
func blocksAndTablesOfFixture(t *testing.T, name string) (m *fixture.Manifest,
	pages []doc.PageRuns, regions []doc.Region, tables map[int][]doc.RuledTable) {
	t.Helper()
	m, pages, regions, path := regionsOfFixture(t, name)

	tables = map[int][]doc.RuledTable{}
	want := map[int]bool{}
	for i := range regions {
		if doc.BaseLanguage(regions[i].Lang) == "de" {
			want[regions[i].Page] = true
		}
	}
	for i := range pages {
		if !want[pages[i].No] {
			continue
		}
		got, err := doc.PageTables(context.Background(), path, &pages[i])
		if err != nil {
			t.Skipf("the ruled lines of page %d could not be read: %v", pages[i].No, err)
		}
		if len(got) > 0 {
			tables[pages[i].No] = got
		}
	}
	return m, pages, regions, tables
}

func regionsOfFixture(t *testing.T, name string) (m *fixture.Manifest, pages []doc.PageRuns,
	regions []doc.Region, path string) {
	t.Helper()
	if name == "thomas-drybox-amfibia" {
		m, path = columnFixture(t)
	} else {
		m, path = loadFixture(t)
	}

	res, err := doc.Analyze(context.Background(), path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.RegionNote != "" {
		t.Skipf("no regions were produced: %s", res.RegionNote)
	}
	pages, err = doc.ExtractRuns(context.Background(), path)
	if err != nil {
		t.Fatalf("ExtractRuns: %v", err)
	}
	return m, pages, res.Regions, path
}

// scriptsIn reports which of the alphabets these two manuals use appear in a
// string, so that "no Russian in the German blocks" can be asserted on the letters
// rather than on a detector's opinion of them.
//
// Cyrillic is the discriminator that matters and it is exact: the column manual's
// five languages are German, Polish, Russian, Ukrainian and Kazakh, and the last
// three are the only Cyrillic ones. A single Cyrillic letter in a German block is
// the funnel leaking.
func scriptsIn(s string) map[string]int {
	out := map[string]int{}
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cyrillic, r):
			out["cyrillic"]++
		case unicode.Is(unicode.Greek, r):
			out["greek"]++
		case unicode.Is(unicode.Han, r), unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r):
			out["cjk"]++
		case unicode.Is(unicode.Arabic, r):
			out["arabic"]++
		case unicode.Is(unicode.Hebrew, r):
			out["hebrew"]++
		}
	}
	return out
}

// polishOnlyLetters are the letters Polish uses and German does not. German has
// no way to produce any of them, so one in a German block came from the column
// beside it.
const polishOnlyLetters = "ąćęłńśźżĄĆĘŁŃŚŹŻ"

// TestBlocksOfTheColumnManualsGermanAreGerman is the first half of acceptance and
// the negative the contract insists on: the German regions of a page holding five
// languages must produce German and nothing else.
func TestBlocksOfTheColumnManualsGermanAreGerman(t *testing.T) {
	_, pages, regions, tables := blocksAndTablesOfFixture(t, "thomas-drybox-amfibia")

	german := doc.RegionsBlocks(pages, regions, map[string]bool{"de": true}, tables)
	if len(german) == 0 {
		t.Fatal("the German regions produced no blocks at all")
	}
	t.Logf("German: %s", doc.BlockSummary(german))

	// The whole promise. Not a threshold and not a fraction: one Cyrillic letter in
	// a German block means a Russian, Ukrainian or Kazakh column was read.
	leaked := 0
	for i := range german {
		b := &german[i]
		if n := scriptsIn(b.Text)["cyrillic"]; n > 0 {
			leaked++
			if leaked <= 5 {
				t.Errorf("page %d block %d holds %d Cyrillic letters: %q",
					b.Page, b.Index, n, truncate(b.Text, 120))
			}
		}
	}
	if leaked > 0 {
		t.Errorf("%d of %d German blocks carry Cyrillic text; this manual's other three "+
			"languages are Russian, Ukrainian and Kazakh", leaked, len(german))
	}

	// Polish shares the Latin alphabet, so it needs its own letters rather than its
	// script. It is the column immediately beside German on most of these pages.
	polish := 0
	for i := range german {
		b := &german[i]
		if strings.ContainsAny(b.Text, polishOnlyLetters) {
			polish++
			if polish <= 5 {
				t.Errorf("page %d block %d holds Polish-only letters: %q",
					b.Page, b.Index, truncate(b.Text, 120))
			}
		}
	}
	if polish > 0 {
		t.Errorf("%d of %d German blocks carry Polish letters", polish, len(german))
	}

	// German has to actually be there, or a test that only forbids other languages
	// passes on an empty result. Umlauts and eszett are what German writes and none
	// of the other four does.
	umlauts := 0
	for i := range german {
		umlauts += strings.Count(german[i].Text, "ä") + strings.Count(german[i].Text, "ö") +
			strings.Count(german[i].Text, "ü") + strings.Count(german[i].Text, "ß")
	}
	if umlauts < 200 {
		t.Errorf("the German blocks carry only %d umlauts and eszetts over %d blocks; "+
			"this is 26 pages of German", umlauts, len(german))
	}
	t.Logf("  %d umlauts and eszetts, no Cyrillic, no Polish-only letters", umlauts)
}

// TestBlocksNeverReachOutsideTheirRegion states the funnel geometrically, which is
// the only way to state it that does not depend on which languages a document
// happens to hold.
//
// The test above it guards Cyrillic and Polish-only letters, and that is not
// enough. Page 57 of this manual USED to divide into four regions — Finnish at
// x=36-178 and German at 179-424, 457-589 and 601-846 — and Finnish shares the
// Latin alphabet AND ä and ö with German, so a Finnish column bleeding into a
// German block passes every letter test there is.
//
// Those four boundaries turned out to be the cell dividers of the page's two
// tables, measured at x=29.7-428.1 and x=450.2-848.7, and the page is now one
// German region because of it. But the property this test states is the one that
// outlives that: a table's area is handed to the row walk, and a table CAN reach
// past the region being read. The left table did exactly that, across two regions
// in two different languages. So every table of this document is read here against
// every region of its page, and a block that reaches outside its region's box is a
// failure however the page came to be divided.
//
// The hermetic twin of the case that no longer occurs on this document is
// TestRegionBlocksClipATableToTheRegion, which rebuilds page 57's pre-fix shape and
// asserts the same thing about it.
func TestBlocksNeverReachOutsideTheirRegion(t *testing.T) {
	_, pages, regions, path := regionsOfFixture(t, "thomas-drybox-amfibia")

	byNo := make(map[int]*doc.PageRuns, len(pages))
	for i := range pages {
		byNo[pages[i].No] = &pages[i]
	}

	// Every page, not only the ones in a scope: the point is to run every table the
	// document draws past every region, and 68 pdftocairo spawns is 6 s.
	tables := map[int][]doc.RuledTable{}
	tabled := 0
	for i := range pages {
		got, err := doc.PageTables(context.Background(), path, &pages[i])
		if err != nil {
			t.Skipf("the ruled lines of page %d could not be read: %v", pages[i].No, err)
		}
		if len(got) > 0 {
			tables[pages[i].No] = got
			tabled++
		}
	}

	checked, boxed, tableBlocks := 0, 0, 0
	for i := range regions {
		r := &regions[i]
		p := byNo[r.Page]
		if p == nil {
			continue
		}
		if r.X0 != 0 {
			boxed++
		}
		for _, b := range doc.RegionBlocks(p, r, tables[r.Page]) {
			checked++
			if b.Kind == doc.BlockTable {
				tableBlocks++
			}
			// The one-unit slack is runsInBox's own tolerance, which absorbs the
			// rounding between a column's reported extent and the runs that produced it.
			if b.X0 < r.X0-1 || b.X1 > r.X1+1 {
				t.Errorf("page %d block %d spans x=%.1f-%.1f, outside its %q region at "+
					"x=%.1f-%.1f: %q", b.Page, b.Index, b.X0, b.X1, r.Lang, r.X0, r.X1,
					truncate(b.Text, 100))
			}
		}
	}
	if checked == 0 || boxed == 0 || tableBlocks == 0 {
		t.Fatalf("checked %d blocks over %d boxed regions, %d of them table cells; all "+
			"three must be non-zero or this test asserts nothing", checked, boxed, tableBlocks)
	}
	t.Logf("%d blocks over %d regions, %d of them boxed, %d table cells from %d tabled "+
		"pages, none reaching outside its box", checked, len(regions), boxed, tableBlocks, tabled)
}

// TestBlocksOfTheColumnManualsPage62 pins what one page produces, because a
// document-level count can stay right while every page goes wrong. This page was
// rendered at 108 dpi and read: a two-column German page with four headings
// (Hinweis zur Entsorgung, Kundendienst, Technische Daten, Garantie), four
// bulleted disposal items on the left, six numbered guarantee clauses on the
// right, and the unruled specification table between them.
func TestBlocksOfTheColumnManualsPage62(t *testing.T) {
	_, pages, regions := blocksOfFixture(t, "thomas-drybox-amfibia")

	blocks := blocksOfPage(t, pages, regions, 62, "de")
	for i := range blocks {
		t.Logf("  [%2d] %-10s L%d %q", blocks[i].Index, blocks[i].Kind, blocks[i].Level,
			truncate(blocks[i].Text, 90))
	}

	var headings []string
	for i := range blocks {
		if blocks[i].Kind == doc.BlockHeading {
			headings = append(headings, blocks[i].Text)
		}
	}
	want := []string{"Garantie", "Hinweis zur Entsorgung", "Kundendienst", "Technische Daten"}
	got := append([]string(nil), headings...)
	sort.Strings(got)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("headings = %q, the render of this page shows %q", got, want)
	}

	// The six numbered guarantee clauses, each with its hanging-indented body. Not
	// six one-line items followed by six orphaned paragraphs, which is what this
	// looked like before the hanging indent was handled.
	numbered := 0
	for i := range blocks {
		if blocks[i].Kind == doc.BlockListItem && strings.HasPrefix(blocks[i].Text, "1.") {
			if blocks[i].Lines < 2 {
				t.Errorf("guarantee clause 1 is one line: %q", blocks[i].Text)
			}
			if !strings.Contains(blocks[i].Text, "gewerblicher Benutzung") {
				t.Errorf("guarantee clause 1 lost its continuation: %q", blocks[i].Text)
			}
		}
		if blocks[i].Kind == doc.BlockListItem {
			numbered++
		}
	}
	// Four bulleted plus six numbered.
	if numbered != 10 {
		t.Errorf("%d list items, the render shows four bulleted disposal items and six "+
			"numbered guarantee clauses", numbered)
	}

	// The specification table has no ruling anywhere and nothing detects it as a
	// table — conversion.md records that and accepts it, on the grounds that the
	// page still reads correctly as lines of text. That is only true if each row is
	// its own block, so it is asserted rather than assumed.
	for _, row := range []string{
		"Spannungsversorgung: 230 V, 50 Hz",
		"Länge Stromzuleitung: ca. 8 m",
	} {
		found := false
		for i := range blocks {
			if blocks[i].Text == row {
				found = true
			}
		}
		if !found {
			t.Errorf("no block reads exactly %q; an unruled specification row has to be "+
				"its own line of text or the page does not read correctly", row)
		}
	}
}

// TestBlocksOfTheColumnManualsPage14 is the boxed case: a page of three parallel
// columns where German is the middle one. Rendered at 108 dpi and read: an
// underlined section heading, three warning paragraphs, and bold step captions
// each followed by their instruction.
func TestBlocksOfTheColumnManualsPage14(t *testing.T) {
	_, pages, regions := blocksOfFixture(t, "thomas-drybox-amfibia")

	blocks := blocksOfPage(t, pages, regions, 14, "de")
	for i := range blocks {
		t.Logf("  [%2d] %-10s L%d %q", blocks[i].Index, blocks[i].Kind, blocks[i].Level,
			truncate(blocks[i].Text, 90))
	}

	if blocks[0].RegionX0 == 0 {
		t.Errorf("the German region of this page starts at x=0; it is the middle column of "+
			"three and must be boxed — %d blocks", len(blocks))
	}
	for _, want := range []string{"Bedienung zum Trockensaugen", "Öffnen Sie den Gehäusedeckel."} {
		found := false
		for i := range blocks {
			if blocks[i].Kind == doc.BlockHeading && blocks[i].Text == want {
				found = true
			}
		}
		if !found {
			t.Errorf("no heading reads %q; the render shows it set as one", want)
		}
	}
	// Nothing from the Polish column to its right.
	for i := range blocks {
		if strings.ContainsAny(blocks[i].Text, polishOnlyLetters) {
			t.Errorf("block %d of the middle column holds Polish letters: %q",
				i, truncate(blocks[i].Text, 120))
		}
	}
}

// TestBlocksOfTheSequentialManualsGermanSection is the other half of acceptance:
// the same reading from a document that gives German pages of its own, 23 to 38.
func TestBlocksOfTheSequentialManualsGermanSection(t *testing.T) {
	_, pages, regions, tables := blocksAndTablesOfFixture(t, "dreame-l40-ultra")

	german := doc.RegionsBlocks(pages, regions, map[string]bool{"de": true}, tables)
	if len(german) == 0 {
		t.Fatal("the German section produced no blocks at all")
	}
	t.Logf("German: %s", doc.BlockSummary(german))

	// The pages must be the section the manifest records, and only those.
	seen := map[int]bool{}
	for i := range german {
		seen[german[i].Page] = true
	}
	var outside []int
	for page := range seen {
		if page < 23 || page > 38 {
			outside = append(outside, page)
		}
	}
	sort.Ints(outside)
	if len(outside) > 0 {
		t.Errorf("German blocks appear on pages %v, outside the section's 23-38", outside)
	}

	// No other writing system. This document has Greek, Arabic, Hebrew and CJK
	// sections, so a block escaping its region here is loud.
	for i := range german {
		b := &german[i]
		for script, n := range scriptsIn(b.Text) {
			t.Errorf("page %d block %d holds %d %s characters: %q",
				b.Page, b.Index, n, script, truncate(b.Text, 120))
			break
		}
	}

	umlauts := 0
	for i := range german {
		umlauts += strings.Count(german[i].Text, "ä") + strings.Count(german[i].Text, "ö") +
			strings.Count(german[i].Text, "ü") + strings.Count(german[i].Text, "ß")
	}
	if umlauts < 200 {
		t.Errorf("the German blocks carry only %d umlauts and eszetts; this is 16 pages "+
			"of German", umlauts)
	}
	t.Logf("  %d umlauts and eszetts over pages %v", umlauts, sortedKeys(seen))
}

// TestBlocksOfTheSequentialManualsPages23And24 pins the two pages that were
// rendered and read. Both are safety pages set entirely in 17pt with one 21pt
// heading, which is the measurement the heading rule turns on: against the
// document's 11pt body every line of them is "larger than body".
func TestBlocksOfTheSequentialManualsPages23And24(t *testing.T) {
	_, pages, regions := blocksOfFixture(t, "dreame-l40-ultra")

	for _, tc := range []struct {
		page         int
		wantHeadings []string
		wantItems    int
	}{
		// Page 23: the section heading, the printed DE tab, one intro paragraph, the
		// subheading, then seven bullets. The tab is furniture this pass does not
		// identify — see the file comment.
		{23, []string{"Sicherheitshinweise", "Nutzungsbeschränkungen"}, 7},
		// Page 24 is twelve bullets under the same heading and nothing else.
		{24, []string{"Sicherheitshinweise"}, 12},
	} {
		blocks := blocksOfPage(t, pages, regions, tc.page, "de")
		t.Logf("--- page %d", tc.page)
		for i := range blocks {
			t.Logf("  [%2d] %-10s L%d %q", blocks[i].Index, blocks[i].Kind, blocks[i].Level,
				truncate(blocks[i].Text, 90))
		}

		for _, want := range tc.wantHeadings {
			found := false
			for i := range blocks {
				if blocks[i].Kind == doc.BlockHeading && blocks[i].Text == want {
					found = true
				}
			}
			if !found {
				t.Errorf("page %d: no heading reads %q", tc.page, want)
			}
		}
		items := 0
		for i := range blocks {
			if blocks[i].Kind == doc.BlockListItem {
				items++
			}
		}
		if items != tc.wantItems {
			t.Errorf("page %d: %d list items, the render shows %d bullets",
				tc.page, items, tc.wantItems)
		}

		// Every bullet must keep its own text. A bullet whose second line was
		// orphaned reads as a truncated instruction, which is the failure mode that
		// matters on a safety page.
		for i := range blocks {
			b := &blocks[i]
			if b.Kind == doc.BlockListItem && b.Chars < 20 {
				t.Errorf("page %d block %d is a %d-character list item: %q",
					tc.page, b.Index, b.Chars, b.Text)
			}
		}
	}
}

// TestBlocksConvergeOnASecondRun is the idempotence conversion.md requires of a
// job handler, checked on a real document rather than on a built page: the key is
// the page, the region's left edge and the index, so a second conversion has to
// produce the same keys and the same text.
func TestBlocksConvergeOnASecondRun(t *testing.T) {
	_, pages, regions, tables := blocksAndTablesOfFixture(t, "thomas-drybox-amfibia")

	first := doc.RegionsBlocks(pages, regions, map[string]bool{"de": true}, tables)
	second := doc.RegionsBlocks(pages, regions, map[string]bool{"de": true}, tables)

	if len(first) != len(second) {
		t.Fatalf("two runs produced %d and %d blocks", len(first), len(second))
	}
	for i := range first {
		a, b := &first[i], &second[i]
		if a.Page != b.Page || a.RegionX0 != b.RegionX0 || a.Index != b.Index || a.Text != b.Text {
			t.Fatalf("block %d differs between runs: page %d/%d x0 %.0f/%.0f index %d/%d",
				i, a.Page, b.Page, a.RegionX0, b.RegionX0, a.Index, b.Index)
		}
	}

	// The keys have to be unique, or two blocks collide in storage and the second
	// conversion overwrites rather than converges.
	seen := make(map[string]bool, len(first))
	for i := range first {
		b := &first[i]
		key := itoa(b.Page) + "/" + itoa(int(b.RegionX0)) + "/" + itoa(b.Index)
		if seen[key] {
			t.Errorf("two blocks share the key %s", key)
		}
		seen[key] = true
	}
}

// TestBlocksHeadingLengthIsASoftCut pins the honest state of the length rule
// rather than a gap that is not there.
//
// The comment on headingMaxMeasureFraction records the measurement: the share of
// the measure that heading candidates occupy is a smooth continuum from 5% to
// 100% on both manuals, in runes as well as in width, so 0.6 is a cut chosen for
// precision and not a valley. Two things follow that a test can hold:
//
//   - Real headings reach the cut. The widest German heading that survives is at
//     60%, so anyone lowering the threshold is trading headings away and should see
//     that in a failure rather than in a diff.
//   - Nothing near the full measure is a heading. That is the property the cut
//     buys, and it is what keeps the column manual's medium-face warning
//     paragraphs — 17.2% of its characters — out of the reader as furniture.
func TestBlocksHeadingLengthIsASoftCut(t *testing.T) {
	_, pages, regions, tables := blocksAndTablesOfFixture(t, "thomas-drybox-amfibia")

	blocks := doc.RegionsBlocks(pages, regions, map[string]bool{"de": true}, tables)
	widest, widestText := 0.0, ""
	for i := range blocks {
		b := &blocks[i]
		if b.Kind != doc.BlockHeading {
			continue
		}
		// The note carries the share of the measure the heading's first line occupies.
		var pct float64
		if _, err := sscanPercent(b.Note, &pct); err != nil {
			t.Errorf("heading %q reports no share of the measure: %q", b.Text, b.Note)
			continue
		}
		if pct > widest {
			widest, widestText = pct, b.Text
		}
		if pct > 60 {
			t.Errorf("heading %q occupies %.0f%% of its measure, above the cut", b.Text, pct)
		}
	}

	t.Logf("the widest German heading occupies %.0f%% of its measure: %q", widest, widestText)
	// A heading reaching the cut is the expected state, not a defect. If the widest
	// one drops well below it, the cut has started to cost headings that used to be
	// found and the trade should be looked at again rather than inherited.
	if widest < 55 {
		t.Errorf("the widest German heading occupies only %.0f%% of its measure against a "+
			"cut at 60%%; headings that reached the cut have been lost", widest)
	}
}

func blocksOfPage(t *testing.T, pages []doc.PageRuns, regions []doc.Region, page int, lang string) []doc.Block {
	t.Helper()
	for i := range regions {
		r := &regions[i]
		if r.Page != page || doc.BaseLanguage(r.Lang) != lang {
			continue
		}
		for j := range pages {
			if pages[j].No == page {
				got := doc.RegionBlocks(&pages[j], r, nil)
				if len(got) == 0 {
					t.Fatalf("page %d region x=%.0f produced no blocks", page, r.X0)
				}
				return got
			}
		}
	}
	t.Fatalf("no %s region on page %d", lang, page)
	return nil
}

// sscanPercent reads the trailing "NN% of the measure" out of a heading's note.
func sscanPercent(note string, out *float64) (int, error) {
	i := strings.LastIndex(note, ", ")
	if i < 0 {
		return 0, fmt.Errorf("note %q carries no measure share", note)
	}
	return fmt.Sscanf(note[i+2:], "%g%%", out)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
