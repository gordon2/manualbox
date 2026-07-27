package verify

// SCRATCH: clip guard sweep.

import (
	"fmt"
	"math"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
)

func TestScratchClipSweep(t *testing.T) {
	for _, name := range []string{"thomas-drybox-amfibia", "dreame-l40-ultra"} {
		t.Run(name, func(t *testing.T) {
			in := scratchInput(t, name)
			for _, ov := range []float64{0, 0.25, 0.5, 0.75, 1.0} {
				line := ""
				for _, sl := range []float64{0, 0.5, 1, 2, 4, 8} {
					n := 0
					for i := range in.Figures {
						f := &in.Figures[i]
						cross := 0
						for _, k := range in.Ink[f.Page] {
							if overlapFraction(k.Rect, f.Rect) < ov {
								continue
							}
							if outside(k.Rect, f.Rect) > sl {
								cross++
							}
						}
						if cross > 0 {
							n++
						}
					}
					line += fmt.Sprintf("  slack%.1f:%d", sl, n)
				}
				t.Logf("overlap>=%.2f %s", ov, line)
			}
			// how far past the box the worst shape reaches, per figure
			var worsts []float64
			for i := range in.Figures {
				f := &in.Figures[i]
				w := 0.0
				for _, k := range in.Ink[f.Page] {
					if overlapFraction(k.Rect, f.Rect) < minClipOverlap {
						continue
					}
					w = math.Max(w, outside(k.Rect, f.Rect))
				}
				worsts = append(worsts, w)
			}
			hist := map[string]int{}
			for _, w := range worsts {
				switch {
				case w <= 0:
					hist["0"]++
				case w <= 1:
					hist["0-1"]++
				case w <= 4:
					hist["1-4"]++
				case w <= 16:
					hist["4-16"]++
				case w <= 64:
					hist["16-64"]++
				default:
					hist["64+"]++
				}
			}
			t.Logf("worst overreach per figure: %v (of %d figures)", hist, len(worsts))
			_ = doc.Ink{}
		})
	}
}
