package verify

// SCRATCH: does the blank band the user saw on page 14 show up in the rendered
// pixels, given that it does not show up in the ink boxes?

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/fixture"
)

func TestScratchPixels(t *testing.T) {
	m, err := fixture.Load(scratchFixturesDir, "thomas-drybox-amfibia")
	if err != nil {
		t.Skip(err)
	}
	path, err := m.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pages, err := doc.ExtractRuns(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range pages {
		p := &pages[i]
		switch p.No {
		case 14, 16, 42, 52, 34:
		default:
			continue
		}
		figs, err := doc.PageFigures(ctx, path, p)
		if err != nil {
			t.Fatal(err)
		}
		ink, err := doc.ExtractInk(ctx, path, p.No)
		if err != nil {
			t.Fatal(err)
		}
		for j := range figs {
			f := &figs[j]
			img, err := png.Decode(bytes.NewReader(f.PNG))
			if err != nil {
				t.Fatal(err)
			}
			bx := inkBox(img)
			sx := float64(f.PixelWidth) / f.Rect.Width()
			t.Logf("page %d fig %d rect %.1f,%.1f-%.1f,%.1f (%.0fx%.0f) px %dx%d ink=%d",
				p.No, j, f.Rect.X0, f.Rect.Y0, f.Rect.X1, f.Rect.Y1,
				f.Rect.Width(), f.Rect.Height(), f.PixelWidth, f.PixelHeight, f.Ink)
			t.Logf("   painted px box %v -> gaps left %.1f right %.1f top %.1f bottom %.1f units",
				bx, float64(bx.Min.X)/sx, float64(f.PixelWidth-bx.Max.X)/sx,
				float64(bx.Min.Y)/sx, float64(f.PixelHeight-bx.Max.Y)/sx)
			// how the ink boxes compare
			var in int
			for k := range ink {
				if overlapFraction(ink[k].Rect, f.Rect) >= minClipOverlap {
					in++
				}
			}
			t.Logf("   matched ink shapes %d", in)
		}
	}
}

// inkBox is the bounding box of pixels that are not the white background.
func inkBox(img image.Image) image.Rectangle {
	b := img.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r > 0xf000 && g > 0xf000 && bl > 0xf000 {
				continue
			}
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x >= maxX {
				maxX = x + 1
			}
			if y >= maxY {
				maxY = y + 1
			}
		}
	}
	if maxX <= minX || maxY <= minY {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX, maxY)
}
