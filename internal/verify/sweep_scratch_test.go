package verify

// SCRATCH: guard sweeps.

import (
	"fmt"
	"testing"
)

func TestScratchOrderSweep(t *testing.T) {
	for _, name := range []string{"thomas-drybox-amfibia", "dreame-l40-ultra"} {
		t.Run(name, func(t *testing.T) {
			in := scratchInput(t, name)
			for _, gap := range []float64{0, 4, 8, 12, 20, 30} {
				line := ""
				for _, ch := range []int{0, 8, 16, 24, 40} {
					n := len(checkOrderWith(in.Blocks,
						orderGuards{slack: orderSlack, minGap: gap, minChars: ch}))
					line += fmt.Sprintf("  chars>=%d: %d", ch, n)
				}
				t.Logf("gap>=%.0f %s", gap, line)
			}
			f := checkOrderWith(in.Blocks, defaultOrderGuards)
			t.Logf("at the defaults: %d findings", len(f))
			for i := range f {
				if i >= 25 {
					break
				}
				t.Logf("  %s | %s", f[i].Detail, f[i].Sample)
			}
		})
	}
}

func TestScratchTextSweep(t *testing.T) {
	for _, name := range []string{"thomas-drybox-amfibia", "dreame-l40-ultra"} {
		t.Run(name, func(t *testing.T) {
			in := scratchInput(t, name)
			scope := pageScope(in)

			// token floor: how many words are absent in total, at each floor
			for _, mt := range []int{1, 2, 3} {
				var toks, absent int
				printed := map[int]map[string]bool{}
				for i := range in.Text {
					printed[in.Text[i].No] = tokenSetMin(in.Text[i].Text, mt)
				}
				rtl := map[int]bool{}
				for i := range in.Blocks {
					b := &in.Blocks[i]
					n, r := 0, 0
					for _, tk := range tokensMin(b.Text, mt) {
						n++
						if isRightToLeft(tk) {
							r++
						}
					}
					if n > 0 && float64(r)/float64(n) > rtlShare {
						rtl[b.Page] = true
					}
				}
				for i := range in.Blocks {
					b := &in.Blocks[i]
					if rtl[b.Page] {
						continue
					}
					for _, tk := range tokensMin(b.Text, mt) {
						toks++
						if !printed[b.Page][tk] {
							absent++
						}
					}
				}
				t.Logf("minToken=%d: %d ltr tokens, %d absent (%.2f%%)",
					mt, toks, absent, 100*float64(absent)/float64(toks))
			}

			// invented-share sweep
			for _, sh := range []float64{0.0, 0.1, 0.2, 0.34, 0.5, 0.75} {
				for _, ma := range []int{1, 2, 3} {
					g := defaultTextGuards
					g.maxInvented, g.minAbsent = sh, ma
					f := checkTextWith(in, scope, g)
					inv, r := 0, 0
					for i := range f {
						switch f[i].Kind {
						case KindInvented:
							inv++
						case KindRightToLeft:
							r++
						}
					}
					t.Logf("share>%.2f absent>=%d: %d invented blocks, %d rtl pages", sh, ma, inv, r)
				}
			}

			// glue floor sweep
			for _, fl := range []int{2, 3, 4, 5} {
				n, count := 0, 0
				var samples []string
				for i := range in.Blocks {
					b := &in.Blocks[i]
					var have map[string]bool
					for j := range in.Text {
						if in.Text[j].No == b.Page {
							have = tokenSet(in.Text[j].Text)
							break
						}
					}
					if have == nil {
						continue
					}
					for _, f := range gluedWords(b, have, fl) {
						n++
						count += f.Count
						if len(samples) < 8 {
							samples = append(samples, fmt.Sprintf("p%d %s", b.Page, f.Sample))
						}
					}
				}
				t.Logf("glueFloor=%d: %d blocks, %d words %v", fl, n, count, samples)
			}
		})
	}
}
