package doc

import (
	"math"
	"testing"
)

// Unit tests for the clip reader. No poppler and no PDF: figures_fixture_test.go
// drives the real tool against the real manuals.
//
// clipSVG is written to hold the four things that can go wrong, each with numbers
// far enough apart that a wrong answer is a different coordinate rather than a
// different count:
//
//	group-1  a clip defined in <defs> and applied inside a hoisted compositing
//	         group, which is the trap rules.go's header records. The two
//	         translations cancel exactly, as they do in cairo's output, so what
//	         this case pins is that the clip is found and applied through the
//	         reference at all: walking <defs> from the top instead would shift the
//	         clip and the rule together by (20, 10).
//	scaled   a clip on a group whose own transform does NOT cancel, which is what
//	         pins the composition itself. The clip admits x=10-30 in the group's
//	         user space, and that space is scaled by two and shifted, so the rule
//	         survives from x=20 to 60 in page units. Resolving the clip in page
//	         space instead — the second of the two wrong answers rules.go records —
//	         gives x=10-30 and a rule half the length in the wrong place.
//	nested   two clips, one inside the other, on a rule that spans the page. The
//	         effective clip is the intersection: reading only the inner one gives
//	         x=50-150 and only the outer one x=20-120, where the answer is 50-120.
//	curved   a clip whose edge is a Bézier that bulges 20 units past its
//	         endpoints. The rule it clips lies inside the bulge and outside the
//	         endpoints, so a clip box built by flattening the curve — which is
//	         what [subpaths] would give — drops the rule entirely.
//	gone     a rule drawn wholly outside its clip, which paints nothing and must
//	         not be recorded at all.
const clipSVG = `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200pt" height="100pt" viewBox="0 0 200 100">
<defs>
<filter id="filter-0" x="0%" y="0%" width="100%" height="100%">
<feImage xlink:href="#compositing-group-1" result="source" x="0" y="0" width="240" height="120"/>
<feBlend in="source" in2="destination" mode="multiply" color-interpolation-filters="sRGB"/>
</filter>
<g id="compositing-group-1" transform="translate(20, 10)">
<g clip-path="url(#clip-hoisted)">
<path fill="none" stroke-width="0.5" stroke-linecap="butt" stroke="rgb(100%, 100%, 100%)" stroke-opacity="1" d="M 10 20 L 70 20 "/>
</g>
</g>
<clipPath id="clip-hoisted">
<rect x="0" y="0" width="40" height="40"/>
</clipPath>
<clipPath id="clip-outer">
<path clip-rule="nonzero" d="M 20 50 L 120 50 L 120 70 L 20 70 Z M 20 50 "/>
</clipPath>
<clipPath id="clip-inner">
<path clip-rule="nonzero" d="M 50 50 L 150 50 L 150 70 L 50 70 Z M 50 50 "/>
</clipPath>
<clipPath id="clip-curved">
<path clip-rule="nonzero" d="M 10 80 C 10 100, 90 100, 90 80 Z M 10 80 "/>
</clipPath>
<clipPath id="clip-scaled">
<rect x="10" y="0" width="20" height="100"/>
</clipPath>
<clipPath id="clip-elsewhere">
<rect x="150" y="0" width="40" height="40"/>
</clipPath>
</defs>
<g filter="url(#filter-0)" transform="translate(-20, -10)"/>
<g clip-path="url(#clip-outer)">
<g clip-path="url(#clip-inner)">
<path fill="none" stroke-width="0.5" stroke-linecap="butt" stroke="rgb(0%, 0%, 0%)" stroke-opacity="1" d="M 0 60 L 200 60 "/>
</g>
</g>
<g transform="translate(0, 10) scale(2, 1)" clip-path="url(#clip-scaled)">
<path fill="none" stroke-width="0.5" stroke-linecap="butt" stroke="rgb(0%, 0%, 0%)" stroke-opacity="1" d="M 0 60 L 100 60 "/>
</g>
<g clip-path="url(#clip-curved)">
<path fill="none" stroke-width="0.5" stroke-linecap="butt" stroke="rgb(0%, 0%, 0%)" stroke-opacity="1" d="M 0 90 L 200 90 "/>
</g>
<g clip-path="url(#clip-elsewhere)">
<path fill="none" stroke-width="0.5" stroke-linecap="butt" stroke="rgb(0%, 0%, 0%)" stroke-opacity="1" d="M 0 95 L 100 95 "/>
</g>
</svg>
`

// TestClipCutsAShapeToWhatIsPainted is the whole point of clip.go: an ink box is
// the visible extent, not the path's own.
func TestClipCutsAShapeToWhatIsPainted(t *testing.T) {
	ink, err := parseInk([]byte(clipSVG))
	if err != nil {
		t.Fatalf("parseInk: %v", err)
	}
	// Every rectangle is in output units, which are the SVG's points times
	// [svgPointScale].
	want := []CellRect{
		// The hoisted group: the rule runs x=10-70 and the clip admits 0-40, so it
		// is painted from 10 to 40. Its two translations cancel, so this pins that
		// the clip is followed into <defs> at all rather than how it is composed —
		// the scaled case below pins that.
		{X0: 15, Y0: 30, X1: 60, Y1: 30},
		// Two nested clips intersect to x=50-120.
		{X0: 75, Y0: 90, X1: 180, Y1: 90},
		// The clip is read in the group's own space: x=10-30 there is x=20-60 on the
		// page under translate(0,10) scale(2,1). Read in page space it would be
		// x=10-30, which is 15-45 in output units.
		{X0: 30, Y0: 105, X1: 90, Y1: 105},
		// Inside the Bézier's bulge, so x=10-90 survives.
		{X0: 15, Y0: 135, X1: 135, Y1: 135},
	}
	if len(ink) != len(want) {
		t.Fatalf("%d shapes, want %d: %v", len(ink), len(want), ink)
	}
	for i := range want {
		got := ink[i].Rect
		if !sameRect(got, want[i]) {
			t.Errorf("shape %d is %v, want %v", i, got, want[i])
		}
	}
}

// TestARuleIsRecordedAtTheLengthItIsPainted is the same reading from the table
// side, because both walkers share the clip and a cell boundary is read off where
// a rule ends.
func TestARuleIsRecordedAtTheLengthItIsPainted(t *testing.T) {
	rules, err := parseRules([]byte(clipSVG))
	if err != nil {
		t.Fatalf("parseRules: %v", err)
	}
	want := []Rule{
		{Dir: Horizontal, At: 30, Start: 15, End: 60},
		{Dir: Horizontal, At: 90, Start: 75, End: 180},
		{Dir: Horizontal, At: 105, Start: 30, End: 90},
		{Dir: Horizontal, At: 135, Start: 15, End: 135},
	}
	if len(rules) != len(want) {
		t.Fatalf("%d rules, want %d: %v", len(rules), len(want), rules)
	}
	for i := range want {
		r := rules[i]
		if r.Dir != want[i].Dir || math.Abs(r.At-want[i].At) > 0.01 ||
			math.Abs(r.Start-want[i].Start) > 0.01 || math.Abs(r.End-want[i].End) > 0.01 {
			t.Errorf("rule %d is %s at=%.2f %.2f-%.2f, want %s at=%.2f %.2f-%.2f",
				i, r.Dir, r.At, r.Start, r.End,
				want[i].Dir, want[i].At, want[i].Start, want[i].End)
		}
	}
}

// TestAnUnreadableClipLeavesTheShapeAlone is the stance clip.go's header takes:
// a clip that cannot be resolved is no clip, because the old behaviour is wrong
// in a recorded direction where a guess could erase a picture.
func TestAnUnreadableClipLeavesTheShapeAlone(t *testing.T) {
	for _, tc := range []struct{ name, clip string }{
		{"a reference to nothing", `<g clip-path="url(#no-such-clip)">`},
		{"units this walker cannot resolve",
			`<g clip-path="url(#bbox)"><!-- --></g><g clip-path="url(#bbox)">`},
		{"an empty clipPath", `<g clip-path="url(#empty)">`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svg := `<svg xmlns="http://www.w3.org/2000/svg" width="100pt" height="100pt">
<defs>
<clipPath id="bbox" clipPathUnits="objectBoundingBox"><rect x="0" y="0" width="0.5" height="0.5"/></clipPath>
<clipPath id="empty"></clipPath>
</defs>
` + tc.clip + `
<path fill="none" stroke-width="1" stroke="rgb(0%, 0%, 0%)" d="M 10 10 L 90 10 "/>
</g>
</svg>`
			ink, err := parseInk([]byte(svg))
			if err != nil {
				t.Fatalf("parseInk: %v", err)
			}
			if len(ink) != 1 {
				t.Fatalf("%d shapes, want 1: %v", len(ink), ink)
			}
			if want := (CellRect{X0: 15, Y0: 15, X1: 135, Y1: 15}); !sameRect(ink[0].Rect, want) {
				t.Errorf("shape is %v, want the unclipped %v", ink[0].Rect, want)
			}
		})
	}
}

// TestPathExtentHoldsTheWholeCurve is why this file does not call [subpaths]: a
// clip box must contain the path, and a curve leaves its endpoints' box.
func TestPathExtentHoldsTheWholeCurve(t *testing.T) {
	// The same cubic the clip fixture uses. Its endpoints are at y=80 and its
	// control points at y=100, so the curve reaches below 80 and the box must too.
	box, ok := pathExtent("M 10 80 C 10 100, 90 100, 90 80 Z M 10 80 ")
	if !ok {
		t.Fatal("no extent read")
	}
	if want := (CellRect{X0: 10, Y0: 80, X1: 90, Y1: 100}); !sameRect(box, want) {
		t.Errorf("extent is %v, want %v", box, want)
	}

	// Relative commands take every control point from the point the command
	// started at, not from the previous pair.
	box, ok = pathExtent("m 10 10 c 0 20, 40 20, 40 0")
	if !ok {
		t.Fatal("no extent read")
	}
	if want := (CellRect{X0: 10, Y0: 10, X1: 50, Y1: 30}); !sameRect(box, want) {
		t.Errorf("relative extent is %v, want %v", box, want)
	}
}
