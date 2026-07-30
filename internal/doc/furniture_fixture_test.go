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

// TestFurnitureClaimsOnlyTabsAndFoliosOnBothManuals is the exhaustive
// false-positive check, and it is the assertion that matters most: over all 628
// pages of both documents and all 39 language sections, EVERY block the rule
// claims is either a printed language tab or a bare number. Not a sample — every
// one.
//
// Measured. The column manual: 111 blocks, being "D" on 26 of German's 26 pages,
// "PL" on 22 of Polish's 27, "UA" on 22 of Ukrainian's 26, and 41 folios. The
// three shares are 1.00, 0.81 and 0.85, and the two below 1 are not the tab being
// absent — they are usableRuns dropping it as sub-legible on the pages whose
// median run is a heading's. The sequential manual: 1,105, being its 34 tabs on
// every page of every section (553 in all) and 552 folios. If a change to the
// rule ever admits a word, this names it.
func TestFurnitureClaimsOnlyTabsAndFoliosOnBothManuals(t *testing.T) {
	for _, tc := range []struct {
		name           string
		blocks         int
		tabs, folios   int
		wantTabStrings []string
	}{
		{
			name: "thomas-drybox-amfibia", blocks: 111, tabs: 70, folios: 41,
			wantTabStrings: []string{"D", "PL", "UA"},
		},
		{
			name: "dreame-l40-ultra", blocks: 1105, tabs: 553, folios: 552,
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
			if fur.Tabs != tc.tabs || fur.Folios != tc.folios {
				t.Errorf("claimed %d tab run(s) and %d folio(s), was %d and %d",
					fur.Tabs, fur.Folios, tc.tabs, tc.folios)
			}

			tabs := map[string]int{}
			numeric := 0
			for i := range blocks {
				b := &blocks[i]
				if !b.Furniture {
					continue
				}
				if allDigits(b.Text) {
					numeric++
					continue
				}
				tabs[b.Text]++
			}
			total := numeric
			for _, n := range tabs {
				total += n
			}
			if total != tc.blocks {
				t.Errorf("%d furniture block(s), was %d", total, tc.blocks)
			}

			// The whole point: nothing but a tab and a number was claimed.
			got := make([]string, 0, len(tabs))
			for s := range tabs {
				got = append(got, s)
			}
			sort.Strings(got)
			want := append([]string(nil), tc.wantTabStrings...)
			sort.Strings(want)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("the non-numeric furniture is %v\nwant                        %v", got, want)
			}
			t.Logf("%s: %d furniture blocks — %d numeric, %d tabs over %d distinct codes",
				tc.name, total, numeric, total-numeric, len(tabs))
		})
	}
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
// Both were read against a 108 dpi render while this was written. What remains is
// the chapter head the paper prints, which is furniture too and is NOT claimed —
// see furniture.go for the measurement that says nothing separates it from a
// repeated heading.
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
	content, furniture := len(conv.ContentBlocks()), len(conv.FurnitureBlocks())
	if content != 443 || furniture != 33 {
		t.Errorf("%d content and %d furniture blocks, was 443 and 33 (427 before the "+
			"contents page came apart, 432 before the furniture pass)", content, furniture)
	}
	if conv.Furniture.Tabs != 26 || conv.Furniture.Folios != 7 {
		t.Errorf("claimed %d tab(s) and %d folio(s) in German, was 26 and 7",
			conv.Furniture.Tabs, conv.Furniture.Folios)
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
// against a render bullet for bullet: one heading and 12 list items printed. It
// arrived as 15 blocks, the two extra being the documented furniture — the tab as
// a level-2 heading and the folio "18" as a paragraph. It now arrives as exactly
// what is printed, with those two flagged and last.
func TestFurnitureOnTheSequentialManualsPage24(t *testing.T) {
	conv := convertFixture(t, "dreame-l40-ultra", "de")

	if conv.Furniture.Tabs != 16 || conv.Furniture.Folios != 16 {
		t.Errorf("claimed %d tab(s) and %d folio(s) over German's 16 pages, was 16 and 16",
			conv.Furniture.Tabs, conv.Furniture.Folios)
	}
	// 481 blocks before the pass; 453 content and 32 furniture after.
	if content, furniture := len(conv.ContentBlocks()), len(conv.FurnitureBlocks()); content != 453 ||
		furniture != 32 {
		t.Errorf("%d content and %d furniture blocks, was 453 and 32 (481 before the pass)",
			content, furniture)
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
	want := []string{"heading1"}
	for i := 0; i < 12; i++ {
		want = append(want, "list-item0")
	}
	if strings.Join(kinds, " ") != strings.Join(want, " ") {
		t.Errorf("page 24's content is\n  %v\nwant one heading and 12 list items\n  %v",
			kinds, want)
	}
	if strings.Join(furniture, "|") != "DE|18" {
		t.Errorf("page 24's furniture is %v, want the tab and the folio 18", furniture)
	}
}

// TestFurnitureThresholdSweepOnBothManuals prints the measurement behind
// furnitureMinShare, over every language section of both documents: the share of a
// section's pages held by its most-repeated line, and the share held by the most
// repeated line that is NOT the tab. The two populations are what the constant
// sits between, and a document that closed the gap would show up here.
func TestFurnitureThresholdSweepOnBothManuals(t *testing.T) {
	for _, name := range []string{"thomas-drybox-amfibia", "dreame-l40-ultra"} {
		blocks, _ := wholeDocumentFurniture(t, name)
		// Pages per language, and the furniture blocks per language, which is the
		// numerator the rule used.
		pages := map[string]map[int]bool{}
		claimed := map[string]int{}
		for i := range blocks {
			b := &blocks[i]
			if pages[b.Lang] == nil {
				pages[b.Lang] = map[int]bool{}
			}
			pages[b.Lang][b.Page] = true
			if b.Furniture && !allDigits(b.Text) {
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
