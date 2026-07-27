package verify

// SCRATCH: the distribution of blank margins in the rendered figures of both
// manuals, in the 1.5-scaled units the figure box is in.

import (
	"bytes"
	"context"
	"image/png"
	"math"
	"sort"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/fixture"
)

func TestScratchMargins(t *testing.T) {
	for _, name := range []string{"thomas-drybox-amfibia", "dreame-l40-ultra"} {
		t.Run(name, func(t *testing.T) {
			m, err := fixture.Load(scratchFixturesDir, name)
			if err != nil {
				t.Skip(err)
			}
			path, err := m.Fetch(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			res, err := doc.Analyze(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			conv, err := ConvertAll(ctx, path, res)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s", conv.Summary())

			type row struct {
				page, idx    int
				worst        float64
				l, r, tp, bt float64
				w, h         float64
				emptyPNG     bool
				pxw, pxh     int
				textFrac     float64
			}
			var rows []row
			var worsts []float64
			for i := range conv.Figures {
				f := &conv.Figures[i]
				if len(f.PNG) == 0 {
					continue
				}
				img, err := png.Decode(bytes.NewReader(f.PNG))
				if err != nil {
					t.Fatal(err)
				}
				bx := inkBox(img)
				sx := float64(f.PixelWidth) / f.Rect.Width()
				if bx.Empty() {
					rows = append(rows, row{page: f.Page, idx: f.Index, emptyPNG: true})
					continue
				}
				l := float64(bx.Min.X) / sx
				r := float64(f.PixelWidth-bx.Max.X) / sx
				tp := float64(bx.Min.Y) / sx
				bt := float64(f.PixelHeight-bx.Max.Y) / sx
				w := math.Max(math.Max(l, r), math.Max(tp, bt))
				rows = append(rows, row{f.Page, f.Index, w, l, r, tp, bt,
					f.Rect.Width(), f.Rect.Height(), false, f.PixelWidth, f.PixelHeight,
					f.TextFraction})
				worsts = append(worsts, w)
			}
			sort.Float64s(worsts)
			if len(worsts) > 0 {
				t.Logf("margins over %d figures: min %.1f median %.1f p90 %.1f max %.1f",
					len(worsts), worsts[0], worsts[len(worsts)/2],
					worsts[len(worsts)*9/10], worsts[len(worsts)-1])
			}
			for _, th := range []float64{2, 4, 8, 12, 16, 24, 40} {
				n := 0
				for _, v := range worsts {
					if v > th {
						n++
					}
				}
				t.Logf("  figures with a margin over %.0f units: %d", th, n)
			}
			sort.Slice(rows, func(a, b int) bool { return rows[a].worst > rows[b].worst })
			for i := 0; i < len(rows) && i < 20; i++ {
				x := rows[i]
				t.Logf("  page %d fig %d box %.0fx%.0f px %dx%d margins l%.1f r%.1f t%.1f b%.1f textFrac %.2f empty=%v",
					x.page, x.idx, x.w, x.h, x.pxw, x.pxh, x.l, x.r, x.tp, x.bt, x.textFrac, x.emptyPNG)
			}
		})
	}
}
