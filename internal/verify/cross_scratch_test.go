package verify

// SCRATCH: does the ink-based "clipped" verdict agree with paint touching the
// crop edge, which is what being cut off looks like in the render?

import (
	"bytes"
	"context"
	"image/png"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/fixture"
)

func TestScratchClipVersusPaint(t *testing.T) {
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
			ink := map[int][]doc.Ink{}
			for i := range conv.Figures {
				p := conv.Figures[i].Page
				if _, ok := ink[p]; ok {
					continue
				}
				got, err := doc.ExtractInk(ctx, path, p)
				if err != nil {
					t.Fatal(err)
				}
				ink[p] = got
			}

			var both, clipOnly, paintOnly, neither int
			var band int
			for i := range conv.Figures {
				f := &conv.Figures[i]
				clip := len(clipped(f, ink[f.Page])) > 0
				img, err := png.Decode(bytes.NewReader(f.PNG))
				if err != nil {
					t.Fatal(err)
				}
				box, ok := paintedBox(img)
				if !ok {
					continue
				}
				b := img.Bounds()
				touch := box.Min.X <= b.Min.X || box.Min.Y <= b.Min.Y ||
					box.Max.X >= b.Max.X || box.Max.Y >= b.Max.Y
				switch {
				case clip && touch:
					both++
				case clip:
					clipOnly++
				case touch:
					paintOnly++
				default:
					neither++
				}
				if len(blankBand(f)) > 0 {
					band++
				}
			}
			t.Logf("%s: %d figures — clipped&paint-at-edge %d, clipped only %d, "+
				"paint-at-edge only %d, neither %d; blank band %d",
				name, len(conv.Figures), both, clipOnly, paintOnly, neither, band)
		})
	}
}
