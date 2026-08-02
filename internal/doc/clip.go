package doc

import (
	"encoding/xml"
	"errors"
	"io"
	"math"
)

// A clip is what makes a drawn shape's box the shape a reader sees.
//
// rules.go's walker reads a path's geometric extent, and that is not what the
// page paints: cairo writes `clip-path` on the group holding the artwork, and a
// drawing whose strokes run past its frame is cut back to the frame before
// anything reaches the paper. Ignoring it was measured and it is the largest
// visible defect in the conversion — 22 of the columns manual's 46 figures and 74
// of the sequential manual's 163 arrived cut off by their own crop, neighbouring
// drawings merged into one figure, and a figure reached over the text beside it.
// This file is what [inkWalker] and [ruleWalker] consult so that a shape's box is
// its *visible* extent.
//
// Four properties of the SVG shape this code, and each is a decision rather than
// a detail.
//
// **A clip is a reference, and clips nest.** `clip-path="url(#clip-9)"` names a
// <clipPath> element elsewhere in the file, and an element is clipped by its own
// clip *and* by every clip on an ancestor. The effective clip is therefore the
// intersection, which is what [clipBox.intersect] accumulates as the walk
// descends. Cairo nests exactly two deep on both fixtures — a coarse integer
// window outside a tight one — and both are read.
//
// **The clip's bounding box is used, not the clip.** A <clipPath> may hold any
// shape, and clip-12 of the columns manual's page 16 is a Bézier ellipse. The box
// is the honest simplification: intersecting with it can only ever make a
// figure's box SMALLER than the unclipped extent and never wrongly larger, so the
// worst it can do is leave some of the old over-reach in place. It cannot cut
// away something the page paints. What it does not do is find the empty corners
// of a non-rectangular clip — a figure clipped to a circle keeps its bounding
// square, which is what a reader would crop by hand anyway.
//
// **A curve's control points are inside the box on purpose.** [subpaths]
// flattens a curve to its endpoints, which is right for a rule and wrong here: a
// clip's box built from endpoints alone can be smaller than the region the clip
// admits, and a clip that is too small cuts a real drawing. A Bézier lies inside
// the hull of its control points, so including them can only overstate the clip,
// which is the direction that cannot lose ink. [pathExtent] is that reading, and
// it is why this file does not simply call [subpaths].
//
// **A clip that cannot be read is no clip at all.** An unresolvable reference, a
// <clipPath> with `clipPathUnits="objectBoundingBox"` — which needs a bounding box
// this walker does not have — or one holding no geometry leaves the shape
// unclipped. That is the old behaviour, which is wrong in a known and recorded
// direction, rather than a guess that could erase a picture.
//
// The compositing-group trap rules.go's header records applies here in full and
// is the reason no clip is resolved at parse time. A <clipPath> is stored in the
// coordinates of whatever referenced it, and cairo hoists content into <defs>
// with an offsetting transform at the use site — so the same <clipPath> resolves
// to two different page rectangles depending on which reference pulled it in.
// The definition is therefore kept in its own user space and composed with the
// walker's current matrix at the moment of use, exactly as the shapes already are.

// clipDef is one <clipPath>'s extent in its own user space, with the element's
// own transform already applied so that the stored rectangle is in the
// coordinates of whatever references it.
type clipDef struct {
	rect CellRect
	ok   bool
}

// clipBox is the effective clip at a point in the walk, in the same output space
// as [Ink.Rect] — that is, after the current matrix and [svgPointScale].
//
// The zero value is "no clip", which is what the top of the page is.
type clipBox struct {
	rect CellRect
	set  bool
}

// intersect adds one more clip to the effective one.
func (c clipBox) intersect(r CellRect) clipBox {
	if !c.set {
		return clipBox{rect: r, set: true}
	}
	return clipBox{set: true, rect: CellRect{
		X0: math.Max(c.rect.X0, r.X0), Y0: math.Max(c.rect.Y0, r.Y0),
		X1: math.Min(c.rect.X1, r.X1), Y1: math.Min(c.rect.Y1, r.Y1),
	}}
}

// empty reports a clip that admits nothing, so every shape under it is invisible
// and the subtree can be abandoned.
func (c clipBox) empty() bool {
	return c.set && (c.rect.X1 <= c.rect.X0 || c.rect.Y1 <= c.rect.Y0)
}

// apply cuts a shape's box back to what the clip admits, reporting whether
// anything is left to paint.
//
// A degenerate shape is the case that needs stating: a hairline rule has zero
// height, so an area test would reject it. The comparison is therefore on each
// axis independently and a zero-extent axis survives as long as it lies inside
// the clip, which is the same question asked of a shape with no thickness that
// [verify.overlap1D] answers the same way.
func (c clipBox) apply(r CellRect) (CellRect, bool) {
	if !c.set {
		return r, true
	}
	out := CellRect{
		X0: math.Max(r.X0, c.rect.X0), Y0: math.Max(r.Y0, c.rect.Y0),
		X1: math.Min(r.X1, c.rect.X1), Y1: math.Min(r.Y1, c.rect.Y1),
	}
	if out.X1 < out.X0 || out.Y1 < out.Y0 {
		return CellRect{}, false
	}
	return out, true
}

// clipAt resolves a clip-path attribute value under the current matrix, giving
// the rectangle it admits in output space.
//
// The four corners of the definition's box are transformed rather than its
// opposite pair, because a matrix with rotation would otherwise produce a
// rectangle that is not the box of the transformed shape. Cairo writes only
// scales, translations and axis flips on these fixtures, for which the two agree
// exactly; under a real rotation this overstates the clip, which is the direction
// that cannot cut a drawing away.
func (d *svgDoc) clipAt(attr string, m matrix) (CellRect, bool) {
	ref, ok := refID(attr)
	if !ok {
		return CellRect{}, false
	}
	def, ok := d.clips[ref]
	if !ok || !def.ok {
		return CellRect{}, false
	}
	r := def.rect
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, p := range [4]point{
		{r.X0, r.Y0}, {r.X1, r.Y0}, {r.X1, r.Y1}, {r.X0, r.Y1},
	} {
		x, y := m.apply(p.x, p.y)
		x, y = x*svgPointScale, y*svgPointScale
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	return CellRect{X0: minX, Y0: minY, X1: maxX, Y1: maxY}, true
}

// readClipPath consumes one <clipPath> element and returns the extent of the
// geometry inside it, in the coordinates of whatever references the clip.
//
// It reads the element from the stream instead of keeping its children in the
// tree, for the reason [svgNode] gives about attributes it does not use: page 42
// of the columns manual carries 34,920 <clipPath> elements in 30 MB of SVG, and a
// rectangle per clip is a few hundred kilobytes where their subtrees are several
// megabytes.
//
// A transform on the <clipPath> itself and on any element inside it is composed,
// because a clip is geometry like any other and cairo is free to place it with a
// matrix. Nested groups are handled by the stack rather than assumed away.
func readClipPath(dec *xml.Decoder, start *xml.StartElement) (clipDef, error) {
	var def clipDef
	base := identity
	for _, a := range start.Attr {
		switch a.Name.Local {
		case "transform":
			base = parseTransform(a.Value)
		case "clipPathUnits":
			// The units are the object's own bounding box, which is the box this
			// walker is trying to compute. Unresolvable rather than guessed: the
			// element is still consumed, and the shape it clips stays unclipped.
			if a.Value == "objectBoundingBox" {
				return clipDef{}, dec.Skip()
			}
		}
	}

	extend := func(m matrix, r CellRect) {
		minX, minY := math.Inf(1), math.Inf(1)
		maxX, maxY := math.Inf(-1), math.Inf(-1)
		for _, p := range [4]point{
			{r.X0, r.Y0}, {r.X1, r.Y0}, {r.X1, r.Y1}, {r.X0, r.Y1},
		} {
			x, y := m.apply(p.x, p.y)
			minX, maxX = math.Min(minX, x), math.Max(maxX, x)
			minY, maxY = math.Min(minY, y), math.Max(maxY, y)
		}
		box := CellRect{X0: minX, Y0: minY, X1: maxX, Y1: maxY}
		if !def.ok {
			def.rect, def.ok = box, true
			return
		}
		def.rect = CellRect{
			X0: math.Min(def.rect.X0, box.X0), Y0: math.Min(def.rect.Y0, box.Y0),
			X1: math.Max(def.rect.X1, box.X1), Y1: math.Max(def.rect.Y1, box.Y1),
		}
	}

	// Several shapes in one <clipPath> are a union, so their boxes are unioned —
	// which is again the direction that overstates the clip rather than cutting
	// something the page paints.
	stack := []matrix{base}
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return def, nil
			}
			return clipDef{}, err
		}
		switch v := tok.(type) {
		case xml.StartElement:
			m := stack[len(stack)-1]
			var d string
			var x, y, w, h float64
			for _, a := range v.Attr {
				switch a.Name.Local {
				case "transform":
					m = m.compose(parseTransform(a.Value))
				case "d":
					d = a.Value
				case "x":
					x = parseFloat(a.Value)
				case "y":
					y = parseFloat(a.Value)
				case "width":
					w = parseFloat(a.Value)
				case "height":
					h = parseFloat(a.Value)
				}
			}
			switch v.Name.Local {
			case "path":
				if box, ok := pathExtent(d); ok {
					extend(m, box)
				}
			case "rect":
				extend(m, CellRect{X0: x, Y0: y, X1: x + w, Y1: y + h})
			}
			stack = append(stack, m)
		case xml.EndElement:
			if len(stack) <= 1 {
				return def, nil
			}
			stack = stack[:len(stack)-1]
		}
	}
}

// pathExtent is the box containing a path, control points included.
//
// Deliberately not [subpaths]: that flattens a curve to its endpoints, which
// gives a box a curve can bulge out of. Here the box must contain the whole path,
// because it becomes a clip and a clip that is too small cuts away real ink. A
// Bézier lies within the hull of its control points, so including them is
// sufficient rather than approximate.
//
// The one shape this cannot bound tightly is an elliptical arc, whose bulge is
// implied by radii rather than drawn with control points: only its endpoint is
// read, so an `A` command can understate the box. Cairo emits no arcs — both
// fixtures' 36,000 clip paths are lines and cubics — and stating it is cheaper
// than implementing an arc parameterisation nothing here produces.
func pathExtent(d string) (CellRect, bool) {
	toks := tokenizePath(d)
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	found := false
	add := func(p point) {
		minX, maxX = math.Min(minX, p.x), math.Max(maxX, p.x)
		minY, maxY = math.Min(minY, p.y), math.Max(maxY, p.y)
		found = true
	}

	var pt, start point
	cmd := byte('M')
	for i := 0; i < len(toks); {
		if toks[i].isCmd {
			cmd = toks[i].cmd
			i++
			if cmd == 'Z' || cmd == 'z' {
				pt = start
			}
			continue
		}
		var nums []float64
		for i < len(toks) && !toks[i].isCmd {
			nums = append(nums, toks[i].num)
			i++
		}
		upper := cmd &^ 0x20
		k := commandArity(upper)
		rel := cmd >= 'a'
		for j := 0; j+k <= len(nums); j += k {
			a := nums[j : j+k]
			switch upper {
			case 'H':
				if rel {
					pt = point{pt.x + a[0], pt.y}
				} else {
					pt = point{a[0], pt.y}
				}
				add(pt)
			case 'V':
				if rel {
					pt = point{pt.x, pt.y + a[0]}
				} else {
					pt = point{pt.x, a[0]}
				}
				add(pt)
			case 'A':
				// Only the endpoint is a coordinate; the leading five arguments are
				// radii and flags. See the note above about what that costs.
				next := point{a[5], a[6]}
				if rel {
					next = point{pt.x + next.x, pt.y + next.y}
				}
				pt = next
				add(pt)
			default:
				// Every coordinate pair of the command, so a curve's control points
				// are in the box. A relative command's pairs are all relative to the
				// point the command started at, which is why pt moves only once, on
				// the last pair.
				var last point
				for p := 0; p+1 < k; p += 2 {
					q := point{a[p], a[p+1]}
					if rel {
						q = point{pt.x + q.x, pt.y + q.y}
					}
					add(q)
					last = q
				}
				if upper == 'M' && j == 0 {
					start = last
				}
				pt = last
			}
		}
	}
	if !found {
		return CellRect{}, false
	}
	return CellRect{X0: minX, Y0: minY, X1: maxX, Y1: maxY}, true
}
