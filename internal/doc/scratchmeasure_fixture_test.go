package doc_test

// Scratch measurement harness for the furniture pass. Deleted before the branch
// lands.

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
)

func TestScratchFurnitureCounts(t *testing.T) {
	if os.Getenv("MANUALBOX_SCRATCH_DIR") == "" {
		t.Skip("set MANUALBOX_SCRATCH_DIR")
	}
	cases := []struct {
		fix   string
		langs []string
	}{
		{"thomas-drybox-amfibia", []string{"de"}},
		{"thomas-drybox-amfibia", []string{"de", "uk"}},
		{"dreame-l40-ultra", []string{"de"}},
		{"dreame-l40-ultra", []string{"ru"}},
		{"dreame-l40-ultra", []string{"de", "ru", "ja"}},
	}
	for _, c := range cases {
		conv := convertFixture(t, c.fix, c.langs...)
		kinds := map[string]int{}
		furKinds := map[string]int{}
		for i := range conv.Blocks {
			b := &conv.Blocks[i]
			if b.Furniture {
				furKinds[fmt.Sprintf("%q", b.Text)]++
				continue
			}
			kinds[string(b.Kind)]++
		}
		t.Logf("%s %v: content kinds %v", c.fix, c.langs, sorted(kinds))
		t.Logf("   furniture %d (tabs %d, folios %d)", len(conv.FurnitureBlocks()),
			conv.Furniture.Tabs, conv.Furniture.Folios)
		texts := make([]string, 0, len(furKinds))
		for k := range furKinds {
			texts = append(texts, k)
		}
		sort.Strings(texts)
		for _, k := range texts {
			t.Logf("      %3d x %s", furKinds[k], k)
		}
	}
}

func sorted(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, fmt.Sprintf("%s=%d", k, v))
	}
	sort.Strings(out)
	return out
}

// TestScratchPrintPages prints the first 15 blocks of the three documented pages.
func TestScratchPrintPages(t *testing.T) {
	if os.Getenv("MANUALBOX_SCRATCH_DIR") == "" {
		t.Skip("set MANUALBOX_SCRATCH_DIR")
	}
	for _, c := range []struct {
		fix   string
		lang  string
		pages []int
	}{
		{"thomas-drybox-amfibia", "de", []int{14, 57}},
		{"dreame-l40-ultra", "de", []int{24}},
	} {
		conv := convertFixture(t, c.fix, c.lang)
		for _, pg := range c.pages {
			t.Logf("=== %s page %d", c.fix, pg)
			n := 0
			for i := range conv.Blocks {
				b := &conv.Blocks[i]
				if b.Page != pg {
					continue
				}
				n++
				if n > 15 {
					break
				}
				mark := " "
				if b.Furniture {
					mark = "F"
				}
				t.Logf("  %s %2d %-10s L%d %q", mark, b.Index, b.Kind, b.Level, trunc(b.Text, 70))
			}
		}
	}
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// TestScratchFurnitureThresholdSweep prints the share of each language's pages
// that the top non-tab bucket occupies, over every language of both manuals.
func TestScratchFurnitureSweep(t *testing.T) {
	if os.Getenv("MANUALBOX_SCRATCH_DIR") == "" {
		t.Skip("set MANUALBOX_SCRATCH_DIR")
	}
	for _, fix := range []string{"thomas-drybox-amfibia", "dreame-l40-ultra"} {
		var path string
		if fix == "thomas-drybox-amfibia" {
			_, path = columnFixture(t)
		} else {
			_, path = loadFixture(t)
		}
		res, err := doc.Analyze(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		pages, err := doc.ExtractRuns(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		fur := doc.FindFurniture(pages, res.Regions, nil, doc.FoliosOf(res.Pages))
		t.Logf("%s: %d furniture runs (tabs %d, folios %d)", fix, fur.Total(), fur.Tabs, fur.Folios)

		// Every distinct furniture text and how many blocks carry it, per language,
		// over the whole document read for every language it holds.
		blocks := doc.RegionsBlocks(pages, res.Regions, nil, nil, fur)
		byLang := map[string]map[string]int{}
		content := map[string]int{}
		for i := range blocks {
			b := &blocks[i]
			if !b.Furniture {
				content[b.Lang]++
				continue
			}
			if byLang[b.Lang] == nil {
				byLang[b.Lang] = map[string]int{}
			}
			byLang[b.Lang][b.Text]++
		}
		langs := make([]string, 0, len(byLang))
		for l := range byLang {
			langs = append(langs, l)
		}
		sort.Strings(langs)
		for _, l := range langs {
			digits, other := 0, map[string]int{}
			for txt, n := range byLang[l] {
				if isAllDigits(txt) {
					digits += n
					continue
				}
				other[txt] += n
			}
			t.Logf("   %-6s content %4d  furniture: %d numeric, %v", l, content[l], digits, other)
		}
	}
}
