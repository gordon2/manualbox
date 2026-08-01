package doc_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/gordon2/manualbox/internal/doc"
)

// The furniture pass against both real manuals. Everything asserted here was
// measured by running it; the counts are quoted at each assertion together with
// what the number was before the pass existed, so that a change to the rule shows
// up as a moved number and not as a silent improvement.

// wholeDocumentFurniture probes a fixture and reads every region of it for every
// language, which is what the pass needs: a share is a share of a language's own
// pages, and reading one household's languages would hide the other sections.
func wholeDocumentFurniture(t *testing.T, name string) ([]doc.Block, *doc.Furniture) {
	t.Helper()
	var path string
	if name == "thomas-drybox-amfibia" {
		_, path = columnFixture(t)
	} else {
		_, path = loadFixture(t)
	}
	res, err := doc.Analyze(context.Background(), path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	pages, err := doc.ExtractRuns(context.Background(), path)
	if err != nil {
		t.Fatalf("ExtractRuns: %v", err)
	}
	fur := doc.FindFurniture(pages, res.Regions, nil, doc.FoliosOf(res.Pages))
	return doc.RegionsBlocks(pages, res.Regions, nil, nil, fur), fur
}

// TestFurnitureClaimsOnlyTabsFoliosAndHeadsOnBothManuals is the exhaustive
// false-positive check, and it is the assertion that matters most: over all 628
// pages of both documents and all 39 language sections, EVERY block the rule
// claims is a printed language tab, a bare number, or a running head — and the
// tabs and the numbers are still named exhaustively. Not a sample: every one.
//
// Measured. The column manual: 172 blocks, being "D" on 26 of German's 26 pages,
// "PL" on 22 of Polish's 27, "UA" on 22 of Ukrainian's 26, 41 folios, and 61
// running heads. The three tab shares are 1.00, 0.81 and 0.85, and the two below 1
// are not the tab being absent — they are usableRuns dropping it as sub-legible on
// the pages whose median run is a heading's. The sequential manual: 1,289, being
// its 34 tabs on every page of every section (553 in all), 552 folios, and 184
// running heads.
//
// Clause 3 gets no list of strings here, because the list would be 97 chapter and
// section titles in 39 languages and would assert only that they had been copied
// out of a previous run. What holds clause 3 is the invariant, in
// [TestFurnitureKeepsEveryTitleItClaims].
func TestFurnitureClaimsOnlyTabsFoliosAndHeadsOnBothManuals(t *testing.T) {
	for _, tc := range []struct {
		name                string
		blocks              int
		tabs, folios, heads int
		wantTabStrings      []string
	}{
		{
			name: "thomas-drybox-amfibia", blocks: 172, tabs: 70, folios: 41, heads: 61,
			wantTabStrings: []string{"D", "PL", "UA"},
		},
		{
			name: "dreame-l40-ultra", blocks: 1289, tabs: 553, folios: 552, heads: 184,
			// Every code the manual prints in its corner, including the two it prints
			// non-canonically: CZ for Czech and UA for Ukrainian.
			wantTabStrings: []string{"AR", "CZ", "DA", "DE", "EL", "EN", "ES", "FI", "FR",
				"HE", "HU", "ID", "IT", "JA", "KK", "LT", "LV", "MS", "NL", "NO", "PL",
				"PT", "RO", "RU", "SK", "SL", "SR", "SV", "TH", "TR", "UA", "UZ", "VI",
				"ZH-HK"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			blocks, fur := wholeDocumentFurniture(t, tc.name)
			if fur.Tabs != tc.tabs || fur.Folios != tc.folios || fur.Heads != tc.heads {
				t.Errorf("claimed %d tab run(s), %d folio(s) and %d head(s), was %d, %d and %d",
					fur.Tabs, fur.Folios, fur.Heads, tc.tabs, tc.folios, tc.heads)
			}

			tabs := map[string]int{}
			numeric, heads := 0, 0
			for i := range blocks {
				b := &blocks[i]
				if !b.Furniture {
					continue
				}
				switch {
				case isRunningHead(b):
					heads++
				case allDigits(b.Text):
					numeric++
				default:
					tabs[b.Text]++
				}
			}
			total := numeric + heads
			for _, n := range tabs {
				total += n
			}
			if total != tc.blocks || heads != tc.heads {
				t.Errorf("%d furniture block(s) of which %d head(s), was %d and %d",
					total, heads, tc.blocks, tc.heads)
			}

			// The whole point: apart from the heads, nothing but a tab and a number.
			got := make([]string, 0, len(tabs))
			for s := range tabs {
				got = append(got, s)
			}
			sort.Strings(got)
			want := append([]string(nil), tc.wantTabStrings...)
			sort.Strings(want)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("the furniture that is neither a number nor a head is %v\nwant %v", got, want)
			}
			t.Logf("%s: %d furniture blocks — %d numeric, %d heads, %d tabs over %d distinct codes",
				tc.name, total, numeric, heads, total-numeric-heads, len(tabs))
		})
	}
}

// isRunningHead reports whether a furniture block was claimed by clause 3. The
// note is the only thing that records which clause claimed a block, which is what
// [doc.Furniture.Note] is for.
func isRunningHead(b *doc.Block) bool {
	return b.Furniture && strings.Contains(b.Note, "the running head")
}

// TestFurnitureKeepsEveryTitleItClaims is clause 3's false-positive check, and it
// is an invariant rather than a list of strings for the reason given above.
//
// The invariant is the whole promise of the clause. A running head is claimed only
// where the page BEFORE it in the same section printed the same line, so the first
// page of every run keeps it — which means every string clause 3 removes must
// still be served as content somewhere in the same language. If one is not, a title
// was deleted, and that is precisely the failure that kept this clause unbuilt.
//
// Measured: 61 claims on the column manual over 20 distinct titles, 184 on the
// sequential manual over 77, and 0 of the 245 has no surviving content copy.
//
// The reversed comparison is not a nicety. [doc.FindFurniture] does not put
// right-to-left text back into logical order — furniture reaches neither a reader
// nor the search index, so nothing has ever needed it to — while a content block
// does. So the Hebrew and Arabic heads are held the two ways round, and comparing
// them naively reports 10 losses on the sequential manual that are not losses.
func TestFurnitureKeepsEveryTitleItClaims(t *testing.T) {
	for _, tc := range []struct {
		name          string
		heads, titles int
	}{
		{name: "thomas-drybox-amfibia", heads: 61, titles: 20},
		{name: "dreame-l40-ultra", heads: 184, titles: 77},
	} {
		t.Run(tc.name, func(t *testing.T) {
			blocks, _ := wholeDocumentFurniture(t, tc.name)

			content := map[string]map[string]bool{}
			for i := range blocks {
				b := &blocks[i]
				if b.Furniture {
					continue
				}
				if content[b.Lang] == nil {
					content[b.Lang] = map[string]bool{}
				}
				content[b.Lang][b.Text] = true
			}

			titles := map[string]bool{}
			heads, lost := 0, 0
			for i := range blocks {
				b := &blocks[i]
				if !isRunningHead(b) {
					continue
				}
				heads++
				titles[b.Text] = true
				if content[b.Lang][b.Text] || content[b.Lang][reverseRunes(b.Text)] {
					continue
				}
				lost++
				t.Errorf("page %d: %q was claimed as %s's running head and is served nowhere "+
					"in that language as content — a title was deleted", b.Page, b.Text, b.Lang)
			}
			if heads != tc.heads || len(titles) != tc.titles {
				t.Errorf("%d head(s) over %d distinct title(s), was %d and %d",
					heads, len(titles), tc.heads, tc.titles)
			}
			t.Logf("%s: %d head(s) over %d distinct title(s), %d with no surviving content copy",
				tc.name, heads, len(titles), lost)
		})
	}
}

// reverseRunes reverses a string by rune, for the right-to-left comparison
// [TestFurnitureKeepsEveryTitleItClaims] explains.
func reverseRunes(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// TestFurnitureKeepsTheSequentialManualsLetteredSections is the false positive
// the rule is arranged to avoid, held against the document that contains it.
//
// The manual labels the parts of its product overview "A" to "E", set in 15pt, one
// letter per section per page — measured: A on 31 pages of the document, B on 30,
// C on 30, D on 30, E on 30, which is once or twice in each of the 34 sections.
// Those are the pages that make "a one-letter line near the top is a tab" false,
// and D in particular sits at x=59 y=56 on the German section's page 29, two units
// from where that section prints its own "DE" tab.
func TestFurnitureKeepsTheSequentialManualsLetteredSections(t *testing.T) {
	blocks, _ := wholeDocumentFurniture(t, "dreame-l40-ultra")

	for _, letter := range []string{"A", "B", "C", "D", "E"} {
		content, furniture := 0, 0
		for i := range blocks {
			if !hasBareWord(blocks[i].Text, letter) {
				continue
			}
			if blocks[i].Furniture {
				furniture++
				t.Errorf("page %d: the section letter %q was claimed as furniture in %q",
					blocks[i].Page, letter, truncate(blocks[i].Text, 60))
				continue
			}
			content++
		}
		if content < 20 {
			t.Errorf("the section letter %q survives in %d content block(s); it is printed "+
				"in 30 of this manual's sections", letter, content)
		}
		t.Logf("%q: %d content block(s), %d claimed as furniture", letter, content, furniture)
	}
}

// hasBareWord reports whether s contains word as a standalone token.
func hasBareWord(s, word string) bool {
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if f == word {
			return true
		}
	}
	return false
}

// TestFurnitureOnTheColumnManualsGluedPages is the case that put this pass beside
// [doc.Convert] rather than inside RegionBlocks. The column manual sets its "D"
// tab on the SAME baseline as the running head, so before the pass page 14's first
// block read "D Trockensaugen" and page 57's "D Fehlerbehebung" — one block each,
// folded from one printed line. The tab could not be removed as a block, and
// stripping it from the front of the text would be a rule that eats a real word.
//
// Both were read against a 108 dpi render while this was written. What remains on
// page 14 is the chapter head the paper prints — "Trockensaugen" in the grey
// banner, where the chapter starts — and clause 3 now takes it off pages 16, 18,
// 20 and 22, which are the pages that only continue it.
func TestFurnitureOnTheColumnManualsGluedPages(t *testing.T) {
	conv := convertFixture(t, "thomas-drybox-amfibia", "de")

	// The funnel is unmoved: the pass reads no page the gate did not charge for.
	if len(conv.Pages) != 26 {
		t.Errorf("converted %d pages, the gate charges this household for 26", len(conv.Pages))
	}

	// 432 blocks before the pass; 427 content and 33 furniture after it. The content
	// falls by 5 and not by 33 because 28 of the 33 were already blocks of their own
	// and the other 5 were glued into a block that survives without them.
	//
	// 443 since the contents page came apart: its 17 printed entries were one
	// run-together block of dot leaders, and each is now its own, which is +16 on the
	// one page of this section that has a table of contents.
	//
	// 431 and 45 since clause 3: German's four chapter heads are printed on 16 of its
	// 26 pages and the first page of each of the four runs keeps its own, so 12 move
	// from content to furniture and the two totals move by 12 in opposite directions.
	content, furniture := len(conv.ContentBlocks()), len(conv.FurnitureBlocks())
	if content != 431 || furniture != 45 {
		t.Errorf("%d content and %d furniture blocks, was 431 and 45 (443 and 33 before "+
			"the running-head clause, 427 before the contents page came apart, 432 "+
			"before the furniture pass)", content, furniture)
	}
	if conv.Furniture.Tabs != 26 || conv.Furniture.Folios != 7 || conv.Furniture.Heads != 12 {
		t.Errorf("claimed %d tab(s), %d folio(s) and %d head(s) in German, was 26, 7 and 12",
			conv.Furniture.Tabs, conv.Furniture.Folios, conv.Furniture.Heads)
	}

	for _, tc := range []struct {
		page int
		want string
	}{
		{page: 14, want: "Trockensaugen"},
		{page: 57, want: "Fehlerbehebung"},
	} {
		first := ""
		for _, b := range conv.ContentBlocks() {
			if b.Page == tc.page {
				first = b.Text
				break
			}
		}
		if first != tc.want {
			t.Errorf("page %d's first content block is %q, want %q — the tab was %q",
				tc.page, first, tc.want, "D "+tc.want)
		}
	}

	// Nothing anywhere in the German conversion still serves the tab as content.
	for _, b := range conv.ContentBlocks() {
		if b.Text == "D" || strings.HasPrefix(b.Text, "D ") && len(b.Text) < 30 {
			t.Errorf("page %d block %d still reads %q", b.Page, b.Index, b.Text)
		}
	}
}

// TestFurnitureOnTheSequentialManualsPage24 is the page conversion.md compares
// against a render bullet for bullet. It arrived as 15 blocks, the two extra being
// the documented furniture — the tab as a level-2 heading and the folio "18" as a
// paragraph.
//
// The reading it was pinned to, "one heading and 12 list items", was itself the
// defect and clause 3 is what showed it. Page 23 opens Sicherheitshinweise with an
// introduction and a sub-heading under the title; page 24 prints the same title at
// the same place and then 12 more bullets, and there is no new section on it. Both
// pages were re-read at 108 dpi. So the heading on page 24 is a running head, and
// what is printed on page 24 is 12 list items — the title being served here was a
// third piece of furniture that had gone unnoticed because it is a real word.
//
// Page 23 keeps its Sicherheitshinweise, and [TestFurnitureKeepsEveryTitleItClaims]
// is what holds that for every title in both documents.
func TestFurnitureOnTheSequentialManualsPage24(t *testing.T) {
	conv := convertFixture(t, "dreame-l40-ultra", "de")

	if conv.Furniture.Tabs != 16 || conv.Furniture.Folios != 16 || conv.Furniture.Heads != 5 {
		t.Errorf("claimed %d tab(s), %d folio(s) and %d head(s) over German's 16 pages, "+
			"was 16, 16 and 5", conv.Furniture.Tabs, conv.Furniture.Folios, conv.Furniture.Heads)
	}
	// 481 blocks before the pass; 453 content and 32 furniture after it, and 448 and
	// 37 since clause 3 moved German's five repeated section titles across.
	if content, furniture := len(conv.ContentBlocks()), len(conv.FurnitureBlocks()); content != 448 ||
		furniture != 37 {
		t.Errorf("%d content and %d furniture blocks, was 448 and 37 (453 and 32 before "+
			"the running-head clause, 481 before the pass)", content, furniture)
	}

	var kinds []string
	var furniture []string
	for i := range conv.Blocks {
		b := &conv.Blocks[i]
		if b.Page != 24 {
			continue
		}
		if b.Furniture {
			furniture = append(furniture, b.Text)
			continue
		}
		kinds = append(kinds, fmt.Sprintf("%s%d", b.Kind, b.Level))
	}
	var want []string
	for i := 0; i < 12; i++ {
		want = append(want, "list-item0")
	}
	if strings.Join(kinds, " ") != strings.Join(want, " ") {
		t.Errorf("page 24's content is\n  %v\nwant the 12 list items and nothing else\n  %v",
			kinds, want)
	}
	if strings.Join(furniture, "|") != "Sicherheitshinweise|DE|18" {
		t.Errorf("page 24's furniture is %v, want the running head, the tab and the folio 18",
			furniture)
	}

	// The title is not lost: page 23 is where the section starts and keeps it.
	first := ""
	for _, b := range conv.ContentBlocks() {
		if b.Page == 23 {
			first = b.Text
			break
		}
	}
	if first != "Sicherheitshinweise" {
		t.Errorf("page 23's first content block is %q, want the section title clause 3 "+
			"took off page 24", first)
	}
}

// TestFurnitureThresholdSweepOnBothManuals prints the measurement behind
// furnitureMinShare, over every language section of both documents: the share of a
// section's pages held by its most-repeated line, and the share held by the most
// repeated line that is NOT the tab. The two populations are what the constant
// sits between, and a document that closed the gap would show up here.
//
// It counts clause 1's claims only. A head is not a tab and shares the denominator
// without sharing the rule, and counting both put German on the column manual at
// 1.46 and its Kazakh at 0.46 — a share above 1 being the tell that two rules were
// being read as one.
func TestFurnitureThresholdSweepOnBothManuals(t *testing.T) {
	for _, name := range []string{"thomas-drybox-amfibia", "dreame-l40-ultra"} {
		blocks, _ := wholeDocumentFurniture(t, name)
		// Pages per language, and the tab blocks per language, which is the numerator
		// clause 1 used.
		pages := map[string]map[int]bool{}
		claimed := map[string]int{}
		for i := range blocks {
			b := &blocks[i]
			if pages[b.Lang] == nil {
				pages[b.Lang] = map[int]bool{}
			}
			pages[b.Lang][b.Page] = true
			if b.Furniture && !allDigits(b.Text) && !isRunningHead(b) {
				claimed[b.Lang]++
			}
		}
		langs := make([]string, 0, len(pages))
		for l := range pages {
			langs = append(langs, l)
		}
		sort.Strings(langs)
		low, high := 1.0, 0.0
		for _, l := range langs {
			n := len(pages[l])
			if n == 0 || claimed[l] == 0 {
				continue
			}
			share := float64(claimed[l]) / float64(n)
			if share < low {
				low = share
			}
			if share > high {
				high = share
			}
		}
		t.Logf("%s: the tab is on %.2f to %.2f of its language's pages over %d section(s) "+
			"that print one; the cut is %.2f", name, low, high, countClaimed(claimed), 0.5)
		if low < 0.5 {
			t.Errorf("%s: a claimed tab sits at %.2f, under the cut it had to pass", name, low)
		}
	}
}

func countClaimed(m map[string]int) int {
	n := 0
	for _, v := range m {
		if v > 0 {
			n++
		}
	}
	return n
}
