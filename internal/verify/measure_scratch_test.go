package verify

// SCRATCH: measurement harness, deleted before the final commit. It caches the
// gathered Input for each fixture in a directory named by MANUALBOX_VERIFY_CACHE
// so thresholds can be swept without re-converting a 560-page manual.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/fixture"
)

const scratchFixturesDir = "../../testdata/fixtures"

func scratchInput(t *testing.T, name string) Input {
	t.Helper()
	dir := os.Getenv("MANUALBOX_VERIFY_CACHE")
	if dir == "" {
		t.Skip("set MANUALBOX_VERIFY_CACHE")
	}
	cache := filepath.Join(dir, name+".json")
	if data, err := os.ReadFile(cache); err == nil {
		var in Input
		if err := json.Unmarshal(data, &in); err != nil {
			t.Fatalf("cache: %v", err)
		}
		t.Logf("%s: from cache, %d blocks %d figures %d text pages %d ink pages",
			name, len(in.Blocks), len(in.Figures), len(in.Text), len(in.Ink))
		return in
	}

	m, err := fixture.Load(scratchFixturesDir, name)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	path, err := m.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	ctx := context.Background()
	start := time.Now()
	res, err := doc.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	t.Logf("%s: analyze %v", name, time.Since(start).Round(time.Millisecond))
	start = time.Now()
	conv, err := ConvertAll(ctx, path, res)
	if err != nil {
		t.Fatalf("ConvertAll: %v", err)
	}
	t.Logf("%s: convert-all %v: %s", name, time.Since(start).Round(time.Millisecond), conv.Summary())

	in := Input{Blocks: conv.Blocks, Figures: conv.Figures, Pages: conv.Pages}
	in.Text, err = doc.ExtractText(ctx, path, res.Info.Pages)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	withFig := map[int]bool{}
	for i := range in.Figures {
		withFig[in.Figures[i].Page] = true
	}
	in.Ink = map[int][]doc.Ink{}
	start = time.Now()
	for p := range withFig {
		ink, err := doc.ExtractInk(ctx, path, p)
		if err != nil {
			t.Fatalf("ExtractInk %d: %v", p, err)
		}
		in.Ink[p] = ink
	}
	t.Logf("%s: ink for %d pages in %v", name, len(withFig), time.Since(start).Round(time.Millisecond))

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(cache, data, 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	return in
}

func TestScratchMeasure(t *testing.T) {
	for _, name := range []string{"thomas-drybox-amfibia", "dreame-l40-ultra"} {
		t.Run(name, func(t *testing.T) {
			in := scratchInput(t, name)
			scope := pageScope(in)
			t.Logf("pages in scope: %d, blocks %d, figures %d", len(scope), len(in.Blocks), len(in.Figures))

			// --- coverage distribution
			cov, _ := checkCoverage(in, scope)
			var ratios []float64
			below := map[string]int{}
			for i := range cov {
				c := cov[i]
				if c.Text < minCoverageText {
					below["thin page (<50 chars)"]++
					continue
				}
				ratios = append(ratios, c.Ratio)
			}
			sort.Float64s(ratios)
			if len(ratios) > 0 {
				t.Logf("coverage over %d pages with text: min %.3f p05 %.3f p25 %.3f median %.3f p75 %.3f max %.3f",
					len(ratios), ratios[0], ratios[len(ratios)*5/100], ratios[len(ratios)/4],
					ratios[len(ratios)/2], ratios[len(ratios)*3/4], ratios[len(ratios)-1])
				for _, th := range []float64{0.5, 0.6, 0.7, 0.75, 0.8, 0.85, 0.9, 0.95} {
					n := 0
					for _, r := range ratios {
						if r < th {
							n++
						}
					}
					t.Logf("  pages below %.2f: %d", th, n)
				}
			}
			t.Logf("  thin pages skipped: %d", below["thin page (<50 chars)"])
			// worst 12 pages
			type pr struct {
				p          int
				r          float64
				blks, text int
			}
			var worst []pr
			for i := range cov {
				if cov[i].Text >= minCoverageText {
					worst = append(worst, pr{cov[i].Page, cov[i].Ratio, cov[i].Blocks, cov[i].Text})
				}
			}
			sort.Slice(worst, func(a, b int) bool { return worst[a].r < worst[b].r })
			for i := 0; i < len(worst) && i < 12; i++ {
				t.Logf("  worst: page %d ratio %.3f (%d blocks vs %d text)", worst[i].p, worst[i].r, worst[i].blks, worst[i].text)
			}

			// --- token miss rates, split by direction
			inScope := map[int]bool{}
			for _, p := range scope {
				inScope[p] = true
			}
			printed := map[int]map[string]bool{}
			for i := range in.Text {
				if inScope[in.Text[i].No] {
					printed[in.Text[i].No] = tokenSet(in.Text[i].Text)
				}
			}
			type ps struct{ toks, rtl, absent, rev int }
			pages := map[int]*ps{}
			type bstat struct {
				page, idx, absent, toks int
				sample                  string
			}
			var bad []bstat
			for i := range in.Blocks {
				b := &in.Blocks[i]
				st := pages[b.Page]
				if st == nil {
					st = &ps{}
					pages[b.Page] = st
				}
				toks := tokens(b.Text)
				var absent []string
				for _, tk := range toks {
					st.toks++
					if isRightToLeft(tk) {
						st.rtl++
					}
					if printed[b.Page][tk] {
						continue
					}
					absent = append(absent, tk)
					st.absent++
					if printed[b.Page][reverse(tk)] {
						st.rev++
					}
				}
				if len(absent) > 0 {
					bad = append(bad, bstat{b.Page, b.Index, len(absent), len(toks), fmt.Sprint(absent)})
				}
			}
			var ltrToks, ltrAbs, rtlPages, rtlToks, rtlAbs, rtlRev int
			var rtlShares []float64
			var ltrMaxShare float64
			for _, p := range scope {
				st := pages[p]
				if st == nil || st.toks == 0 {
					continue
				}
				share := float64(st.rtl) / float64(st.toks)
				if share > rtlShare {
					rtlPages++
					rtlToks += st.toks
					rtlAbs += st.absent
					rtlRev += st.rev
					rtlShares = append(rtlShares, share)
				} else {
					ltrToks += st.toks
					ltrAbs += st.absent
					if share > ltrMaxShare {
						ltrMaxShare = share
					}
				}
			}
			t.Logf("ltr pages: %d tokens, %d absent (%.2f%%); max rtl share on an ltr page %.3f",
				ltrToks, ltrAbs, 100*float64(ltrAbs)/math.Max(1, float64(ltrToks)), ltrMaxShare)
			sort.Float64s(rtlShares)
			if len(rtlShares) > 0 {
				t.Logf("rtl pages: %d, %d tokens, %d absent, %d of those reversible; rtl share %.2f-%.2f",
					rtlPages, rtlToks, rtlAbs, rtlRev, rtlShares[0], rtlShares[len(rtlShares)-1])
			}
			sort.Slice(bad, func(a, b int) bool {
				sa := float64(bad[a].absent) / float64(bad[a].toks)
				sb := float64(bad[b].absent) / float64(bad[b].toks)
				if sa != sb {
					return sa > sb
				}
				return bad[a].absent > bad[b].absent
			})
			shown := 0
			for i := range bad {
				p := bad[i].page
				if st := pages[p]; st != nil && float64(st.rtl)/float64(st.toks) > rtlShare {
					continue
				}
				if shown >= 14 {
					break
				}
				shown++
				t.Logf("  ltr absent: page %d block %d %d/%d %s", bad[i].page, bad[i].idx, bad[i].absent, bad[i].toks, bad[i].sample)
			}

			// --- joins
			jh, jg, js := 0, 0, 0
			var hs, gs []string
			for i := range in.Blocks {
				b := &in.Blocks[i]
				if f := hyphenJoins(b); len(f) > 0 {
					jh += f[0].Count
					if len(hs) < 25 {
						hs = append(hs, fmt.Sprintf("p%d %s", b.Page, f[0].Sample))
					}
				}
				if f := doubleSpaces(b); len(f) > 0 {
					js += f[0].Count
				}
				if have := printed[b.Page]; have != nil {
					for _, f := range gluedWords(b, have, minGluedPart) {
						jg += f.Count
						if len(gs) < 25 {
							gs = append(gs, fmt.Sprintf("p%d %s", b.Page, f.Sample))
						}
					}
				}
			}
			t.Logf("joins: %d hyphen-space, %d glued, %d doubled space", jh, jg, js)
			for _, s := range hs {
				t.Logf("  hyphen: %s", s)
			}
			for _, s := range gs {
				t.Logf("  glued: %s", s)
			}

			// --- figure geometry distribution
			var bands []float64
			clipped, band12 := 0, 0
			type fg struct {
				page, idx, cross, inside int
				band, worst              float64
			}
			var fgs []fg
			for i := range in.Figures {
				f := &in.Figures[i]
				ink := in.Ink[f.Page]
				var inside, cross int
				x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
				worst := 0.0
				for j := range ink {
					r := ink[j].Rect
					if overlapFraction(r, f.Rect) < minClipOverlap {
						continue
					}
					inside++
					x0, y0 = math.Min(x0, r.X0), math.Min(y0, r.Y0)
					x1, y1 = math.Max(x1, r.X1), math.Max(y1, r.Y1)
					if o := outside(r, f.Rect); o > clipSlack {
						cross++
						if o > worst {
							worst = o
						}
					}
				}
				if inside == 0 {
					t.Logf("  figure with no matched ink: page %d idx %d ink=%d", f.Page, f.Index, f.Ink)
					continue
				}
				b := math.Max(math.Max(x0-f.Rect.X0, f.Rect.X1-x1), math.Max(y0-f.Rect.Y0, f.Rect.Y1-y1))
				bands = append(bands, b)
				if b > maxBlankBand {
					band12++
				}
				if cross > 0 {
					clipped++
				}
				fgs = append(fgs, fg{f.Page, f.Index, cross, inside, b, worst})
			}
			sort.Float64s(bands)
			t.Logf("figures: %d measured, %d with band > %.0f, %d clipped", len(bands), band12, maxBlankBand, clipped)
			buckets := []float64{0.5, 2, 4, 8, 12, 16, 24, 40, 80, 1e9}
			prev := 0.0
			for _, hi := range buckets {
				n := 0
				for _, v := range bands {
					if v >= prev && v < hi {
						n++
					}
				}
				t.Logf("  band %.1f-%.1f: %d", prev, hi, n)
				prev = hi
			}
			sort.Slice(fgs, func(a, b int) bool { return fgs[a].band > fgs[b].band })
			for i := 0; i < len(fgs) && i < 12; i++ {
				t.Logf("  biggest band: page %d fig %d band %.1f inside %d cross %d worst %.1f",
					fgs[i].page, fgs[i].idx, fgs[i].band, fgs[i].inside, fgs[i].cross, fgs[i].worst)
			}
			sort.Slice(fgs, func(a, b int) bool { return fgs[a].worst > fgs[b].worst })
			for i := 0; i < len(fgs) && i < 12; i++ {
				t.Logf("  worst clip: page %d fig %d worst %.1f cross %d of %d band %.1f",
					fgs[i].page, fgs[i].idx, fgs[i].worst, fgs[i].cross, fgs[i].inside, fgs[i].band)
			}

			// --- reading order, with and without tables
			all := checkOrder(in.Blocks)
			t.Logf("reading order: %d findings excluding table cells", len(all))
			for i := 0; i < len(all) && i < 10; i++ {
				t.Logf("  %s | %s", all[i].Detail, all[i].Sample)
			}
			withTables := 0
			{
				keep := make([]doc.Block, len(in.Blocks))
				copy(keep, in.Blocks)
				for i := range keep {
					if keep[i].Kind == doc.BlockTable {
						keep[i].Kind = doc.BlockParagraph
					}
				}
				withTables = len(checkOrder(keep))
			}
			t.Logf("reading order including table cells: %d findings", withTables)

			rep := Inspect(in)
			t.Logf("REPORT: %s", rep.Summary())
		})
	}
}
