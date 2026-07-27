package verify

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"

	"github.com/gordon2/manualbox/internal/doc"
)

// Bounds on a figure's geometry, in the 1.5-scaled space [doc.PageRuns] and a
// `pdftoppm -r 108` raster share, so a unit here is a pixel on a render a person
// can look at. Measured on both fixtures; the numbers are at each constant.
const (
	// maxBlankBand is how much empty space a figure's render may carry on one side
	// of the picture, in units.
	//
	// The cause is the clip-path limitation conversion.md records: a figure's box is a
	// path's UNCLIPPED extent, so a drawing clipped to a smaller window reports a box
	// bigger than anything painted in it, and the crop carries the difference as a
	// blank band.
	//
	// Measured on the RENDERED PIXELS of every figure of both manuals — see
	// [paintedMargins] for why the ink boxes cannot answer this — as the largest of
	// the four blank margins:
	//
	//	column manual   46 figures: 35 at 0.0, 11 over 2, 9 over 4, 4 over 12, 3 over 16, 1 over 40
	//	sequential     163 figures: 17 over 2, 12 over 4, 7 over 8, 6 over 12, 4 over 16, 1 over 40
	//
	// The largest are page 46 figure 1 of the column manual (34 units blank at the
	// right, 64 at the foot) and page 530 figure 0 of the sequential one (33 left, 49
	// right) — which is the fault the user reports, of the size they report it at.
	//
	// 12 is chosen because pdftoppm rounds the crop outwards by up to a unit on each
	// side, a hairline's own stroke width is one or two, and half a line of body text
	// on either manual is 8: below 12 the check would report rounding. Above it every
	// case seen is a real band. It is not a gap in a distribution — there is no gap;
	// the counts above are a smooth tail, the same shape doc/figures.go records for
	// its own size guard.
	maxBlankBand = 12.0

	// whiteCutoff is how light a pixel may be and still count as background, out of
	// 65535 per channel.
	//
	// pdftoppm renders on opaque white, so the background is 0xffff exactly and any
	// cutoff below that would work for a solid drawing. It is lower than that for the
	// anti-aliased edge of a hairline, which is the only thing painted in the outer
	// pixels of a line drawing. Measured over both manuals, moving it from 0xffff to
	// 0xf000 changes no figure's verdict; see the sweep in the fixture test.
	whiteCutoff = 0xf000

	// clipSlack is how far a shape may reach past the figure's box before it counts
	// as clipped rather than as touching it.
	//
	// A stroke has width, and a box derived from path extents sits within a unit of
	// the strokes that made it, so an exact comparison reports every figure. 1.0 is
	// the same one unit [doc.Convert] allows a figure against a region's edge, and
	// for the same reason: this is comparing one measurement of a drawing against
	// another.
	clipSlack = 1.0

	// minClipOverlap is how much of a shape must fall inside the figure's box before
	// the shape is treated as part of that figure at all.
	//
	// Both ends of this range are degenerate, which is what fixes the value in the
	// middle. Swept over both manuals, as figures reported clipped (column of 46 /
	// sequential of 163):
	//
	//	overlap >= 0.00   46 / 163 — every figure: a page-sized background path
	//	                  "crosses the edge" of all of them
	//	overlap >= 0.25   29 / 92
	//	overlap >= 0.50   22 / 74
	//	overlap >= 0.75   16 / 39
	//	overlap >= 1.00    0 / 0  — containment cannot detect clipping at all,
	//	                  since a contained shape crosses nothing by definition
	//
	// 0.5 is the midpoint of the usable range: a shape more than half inside the box
	// is the figure's, one mostly outside belongs to whatever else is on the page.
	// There is no plateau to sit on, which is stated rather than hidden.
	//
	// The verdict was cross-checked against a signal it shares no code with: whether
	// the render's own paint reaches the crop's edge, which is what being cut off
	// looks like. Of the column manual's 46 figures, all 22 flagged clipped have paint
	// at the edge and none is flagged without it; on the sequential manual 73 of 74
	// do. The converse does not hold and should not — the crop is derived from the
	// ink, so a picture's paint routinely reaches its own edge — and that is exactly
	// what the ink comparison adds: it says the drawing CONTINUES past the crop.
	minClipOverlap = 0.5
)

// checkFigures reports the two distinct faults a figure's box can have.
//
// Both come from one cause — conversion.md's "clip paths are not read" — and they
// are opposite failures of the same box, which is why they are counted separately:
//
//	[KindFigureBand]    the box is bigger than the drawing, so the picture arrives
//	                    with an empty band around it
//	[KindFigureClipped] shapes drawn inside the box cross its edge, so part of the
//	                    picture is cut off by the crop
//
// # What this has to work around
//
// [doc.Figure] carries how many shapes it holds and not which, so the shapes have
// to be matched to the figure here, by geometry. That is the one thing this package
// wanted from `internal/doc` and did not have; see the report. Matching is by area
// overlap ([minClipOverlap]) rather than by containment, because a containment test
// would define away the clipped case it is looking for.
func checkFigures(in Input) []Finding {
	var out []Finding
	for i := range in.Figures {
		f := &in.Figures[i]
		out = append(out, blankBand(f)...)
		out = append(out, clipped(f, in.Ink[f.Page])...)
	}
	return out
}

// blankBand reports a figure whose render is mostly margin on one side.
//
// It reads the PNG the conversion already carries and finds the box of pixels that
// are not the background. Nothing is decoded that a reader will not see: this is
// the same bytes the reader is served, which is what makes the finding a statement
// about the picture rather than about the geometry behind it.
func blankBand(f *doc.ConvertedFigure) []Finding {
	l, r, t, b, ok := paintedMargins(f)
	if !ok {
		return nil
	}
	worst := math.Max(math.Max(l, r), math.Max(t, b))
	if worst <= maxBlankBand {
		return nil
	}
	return []Finding{{
		Kind: KindFigureBand, Page: f.Page, Index: f.Index,
		Got: worst, Want: maxBlankBand,
		Count: f.Ink, Total: f.PixelWidth * f.PixelHeight,
		Detail: fmt.Sprintf("page %d figure %d: its %.0fx%.0f box renders with blank "+
			"margins of %.0f left, %.0f right, %.0f top and %.0f bottom units "+
			"(want at most %.0f) — the box is bigger than the picture in it",
			f.Page, f.Index, f.Rect.Width(), f.Rect.Height(), l, r, t, b, maxBlankBand),
	}}
}

// paintedMargins is how much blank space each side of a figure's render carries,
// in the units its box is in, and whether the render could be read at all.
//
// # Why the pixels and not the ink boxes
//
// The obvious measurement is the bounding box of [doc.Ink] against the figure's
// box, and it does not work, because the figure's box IS that bounding box:
// doc.FindFigures clusters the ink and takes its extent. Measured on the column
// manual, comparing the two gives 0.0 for 38 of its 46 figures and a negative
// number for several more, and page 14's two photographs — where a band was
// reported by eye — come out at 0.0 and 2.5. The comparison cannot see the fault
// because the fault is in the ink: a clipped path reports an extent larger than
// anything it paints, and both sides of that comparison are built from the same
// inflated extent.
//
// The pixels are downstream of the clip. Whatever poppler painted is what a reader
// sees, so a band in the render is a band, and the same measurement on the same 46
// figures finds the four the eye finds.
func paintedMargins(f *doc.ConvertedFigure) (left, right, top, bottom float64, ok bool) {
	if len(f.PNG) == 0 || f.PixelWidth <= 0 || f.Rect.Width() <= 0 {
		return 0, 0, 0, 0, false
	}
	img, err := png.Decode(bytes.NewReader(f.PNG))
	if err != nil {
		// A figure whose bytes will not decode is a different fault, and it is
		// [doc.PageFigures]'s to report: it read the size out of the same bytes.
		return 0, 0, 0, 0, false
	}
	box, painted := paintedBox(img)
	if !painted {
		return 0, 0, 0, 0, false
	}
	// Pixels per unit, read off the render rather than assumed: doc renders at twice
	// the coordinate space's dpi, and taking the ratio means this stays right if that
	// changes.
	scale := float64(f.PixelWidth) / f.Rect.Width()
	return float64(box.Min.X) / scale, float64(f.PixelWidth-box.Max.X) / scale,
		float64(box.Min.Y) / scale, float64(f.PixelHeight-box.Max.Y) / scale, true
}

// paintedBox is the bounding box of pixels that are not the background.
func paintedBox(img image.Image) (image.Rectangle, bool) {
	b := img.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r > whiteCutoff && g > whiteCutoff && bl > whiteCutoff {
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
		return image.Rectangle{}, false
	}
	return image.Rect(minX, minY, maxX, maxY), true
}

// clipped reports a figure whose drawn shapes cross the box the crop was taken
// from, so the picture is cut off at the edge.
//
// [doc.Figure] carries how many shapes it holds and not which, so the shapes are
// matched to the figure here, by geometry — that is the one thing this package
// wanted from internal/doc and did not have; see the report. Matching is by area
// overlap ([minClipOverlap]) rather than by containment, because a containment test
// would define away the case it is looking for.
func clipped(f *doc.ConvertedFigure, ink []doc.Ink) []Finding {
	var inside, crossing int
	var worstOver float64
	var worstShape doc.CellRect
	for j := range ink {
		r := ink[j].Rect
		if overlapFraction(r, f.Rect) < minClipOverlap {
			continue
		}
		inside++
		if over := outside(r, f.Rect); over > clipSlack {
			crossing++
			if over > worstOver {
				worstOver, worstShape = over, r
			}
		}
	}
	if crossing == 0 {
		return nil
	}
	return []Finding{{
		Kind: KindFigureClipped, Page: f.Page, Index: f.Index,
		Got: worstOver, Want: clipSlack, Count: crossing, Total: inside,
		Detail: fmt.Sprintf("page %d figure %d: %d of %d shapes cross the box "+
			"x=%.0f-%.0f y=%.0f-%.0f, the worst by %.0f units "+
			"(x=%.0f-%.0f y=%.0f-%.0f), so the crop cuts the picture",
			f.Page, f.Index, crossing, inside,
			f.Rect.X0, f.Rect.X1, f.Rect.Y0, f.Rect.Y1, worstOver,
			worstShape.X0, worstShape.X1, worstShape.Y0, worstShape.Y1),
	}}
}

// overlapFraction is how much of inner falls inside outer, 1 for a shape wholly
// inside it, measured per axis and multiplied.
//
// Per axis because a drawn shape is routinely degenerate: a horizontal rule has
// zero height and a vertical one zero width, and an area comparison divides by
// zero on both. On a degenerate axis the question becomes containment, which is the
// same question asked of a shape with no thickness.
func overlapFraction(inner, outer doc.CellRect) float64 {
	return overlap1D(inner.X0, inner.X1, outer.X0, outer.X1) *
		overlap1D(inner.Y0, inner.Y1, outer.Y0, outer.Y1)
}

// overlap1D is the share of [a0,a1] lying inside [b0,b1].
func overlap1D(a0, a1, b0, b1 float64) float64 {
	if a1 <= a0 {
		if a0 >= b0 && a0 <= b1 {
			return 1
		}
		return 0
	}
	in := math.Min(a1, b1) - math.Max(a0, b0)
	if in <= 0 {
		return 0
	}
	return in / (a1 - a0)
}

// outside is how far a shape reaches past a box, on its worst side.
func outside(inner, outer doc.CellRect) float64 {
	return math.Max(math.Max(outer.X0-inner.X0, inner.X1-outer.X1),
		math.Max(outer.Y0-inner.Y0, inner.Y1-outer.Y1))
}
