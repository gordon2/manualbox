package doc

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// Unit tests for the SVG rule reader and the cell walk. No poppler and no PDF:
// rules_fixture_test.go drives the real tool against the real manuals.
//
// compositingSVG reproduces the shapes cairo actually emits, and every element
// in it is there because a real page has one. The attribute spellings are copied
// from the columns fixture's page 57 rather than invented — `stroke="rgb(100%,
// 100%, 100%)"`, a `matrix` with a negative y scale, a filled path whose last
// subpath is a bare moveto — because each of those broke an earlier reading of
// it.
//
// The table is drawn inside a hoisted blend group, which is the trap this file's
// header describes: `<g id="compositing-group-1" transform="translate(20, 10)">`
// lives in <defs> and is pulled back by `<g filter="url(#filter-0)"
// transform="translate(-20, -10)">`. The two translations cancel, so a correct
// reading returns the coordinates as written and either mistake is visible in
// the numbers rather than in a count: skipping <defs> loses the table entirely,
// and entering it without composing the use site's matrix shifts every
// coordinate by (30, 15) output units.
const compositingSVG = `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200pt" height="100pt" viewBox="0 0 200 100">
<defs>
<filter id="filter-0" x="0%" y="0%" width="100%" height="100%">
<feImage xlink:href="#compositing-group-1" result="source" x="0" y="0" width="285" height="145"/>
<feBlend in="source" in2="destination" mode="multiply" color-interpolation-filters="sRGB"/>
</filter>
<g id="compositing-group-1" transform="translate(20, 10)">
<path fill="none" stroke-width="0.5" stroke-linecap="butt" stroke="rgb(100%, 100%, 100%)" stroke-opacity="1" d="M 0 0 L 100 0 " transform="matrix(1, 0, 0, -1, 20, 20)"/>
<path fill="none" stroke-width="0.5" stroke-linecap="butt" stroke="rgb(100%, 100%, 100%)" stroke-opacity="1" d="M 20 40 L 120 40 "/>
<path fill="none" stroke-width="0.5" stroke-linecap="butt" stroke="rgb(100%, 100%, 100%)" stroke-opacity="1" d="M 20 60 L 120 60 "/>
<path fill="none" stroke-width="0.5" stroke-linecap="butt" stroke="rgb(100%, 100%, 100%)" stroke-opacity="1" d="M 20 20 L 20 60 "/>
<path fill="none" stroke-width="0.5" stroke-linecap="butt" stroke="rgb(100%, 100%, 100%)" stroke-opacity="1" d="M 70 20 L 70 60 "/>
<path fill="none" stroke-width="0.5" stroke-linecap="butt" stroke="rgb(100%, 100%, 100%)" stroke-opacity="1" d="M 120 20 L 120 60 "/>
</g>
<g id="glyph-0-0">
<path fill-rule="nonzero" fill="rgb(0%, 0%, 0%)" fill-opacity="1" d="M 190 10 L 190.5 10 L 190.5 90 L 190 90 Z M 190 10 "/>
</g>
<clipPath id="clip-0">
<path d="M 5 5 L 195 5 L 195 95 L 5 95 Z M 5 5 "/>
</clipPath>
</defs>
<g filter="url(#filter-0)" transform="translate(-20, -10)"/>
<path fill="none" stroke-width="1" stroke-linecap="butt" stroke="rgb(13.729858%, 12.159729%, 12.548828%)" stroke-opacity="1" d="M 10 95 L 80 95 "/>
<path fill-rule="nonzero" fill="rgb(13.729858%, 12.159729%, 12.548828%)" fill-opacity="1" d="M 10 85 L 80 85 L 80 85.5 L 10 85.5 Z M 10 85 "/>
<use xlink:href="#glyph-0-0" x="0" y="0"/>
</svg>
`

// compositingPage is text sitting in all four cells of compositingSVG's table,
// in the output coordinate space, so the text guard has something to read.
func compositingPage() *PageRuns {
	page := &PageRuns{No: 1, Width: 300, Height: 150}
	for _, cell := range [][2]float64{{40, 35}, {115, 35}, {40, 65}, {115, 65}} {
		page.Runs = append(page.Runs, TextRun{
			X: cell[0], Y: cell[1], Width: 50, Height: 10, Text: "content",
		})
	}
	return page
}

func TestRulesInsideACompositingGroupKeepTheirCoordinates(t *testing.T) {
	rules, err := parseRules([]byte(compositingSVG))
	if err != nil {
		t.Fatalf("parseRules: %v", err)
	}

	// The table's six rules, at the coordinates written in the group, times 1.5.
	// If <defs> were skipped these would all be missing; if the use site's
	// transform were not composed every one would be 30 further right and 15
	// further down.
	want := []Rule{
		{Dir: Horizontal, At: 30, Start: 30, End: 180},
		{Dir: Horizontal, At: 60, Start: 30, End: 180},
		{Dir: Horizontal, At: 90, Start: 30, End: 180},
		{Dir: Vertical, At: 30, Start: 30, End: 90},
		{Dir: Vertical, At: 105, Start: 30, End: 90},
		{Dir: Vertical, At: 180, Start: 30, End: 90},
	}
	for _, w := range want {
		if !hasRule(rules, w) {
			t.Errorf("missing %s rule at %.1f spanning %.1f..%.1f\ngot %s",
				w.Dir, w.At, w.Start, w.End, formatRules(rules))
		}
	}

	// The body's own two rules: one stroked, one a filled sliver. Both are real
	// forms in both fixtures — 800 of the columns manual's 4,360 rules are filled —
	// and this is the only test that fails if the filled branch is removed, since
	// no table in either fixture depends on one. See [ruleWalker.filled].
	if !hasRule(rules, Rule{Dir: Horizontal, At: 142.5, Start: 15, End: 120}) {
		t.Errorf("the body's stroked rule is missing\ngot %s", formatRules(rules))
	}
	var filled *Rule
	for i := range rules {
		if rules[i].Filled {
			filled = &rules[i]
		}
	}
	if filled == nil {
		t.Fatalf("the filled sliver was not read as a rule\ngot %s", formatRules(rules))
	}
	if filled.Dir != Horizontal || math.Abs(filled.At-127.875) > 0.01 ||
		math.Abs(filled.Start-15) > 0.01 || math.Abs(filled.End-120) > 0.01 {
		t.Errorf("filled sliver is %s at %.3f spanning %.2f..%.2f, want horizontal at "+
			"127.875 spanning 15..120", filled.Dir, filled.At, filled.Start, filled.End)
	}

	// A glyph outline is a filled path of exactly the shape a hairline rule has,
	// which is why it is excluded structurally. This one is a 0.5 by 80 sliver at
	// x=190pt, so if the <g id="glyph-..."> were walked — directly or through the
	// <use> that references it — there would be a vertical rule at x=285.
	for i := range rules {
		if rules[i].Dir == Vertical && math.Abs(rules[i].At-285) < 1 {
			t.Errorf("a glyph outline was read as a rule at x=%.1f", rules[i].At)
		}
	}
	if len(rules) != 8 {
		t.Errorf("got %d rules, want 8 (six table, one stroked, one filled)\n%s",
			len(rules), formatRules(rules))
	}
}

func TestCompositingGroupYieldsATable(t *testing.T) {
	rules, err := parseRules([]byte(compositingSVG))
	if err != nil {
		t.Fatalf("parseRules: %v", err)
	}
	tables := FindRuledTables(rules, compositingPage())
	if len(tables) != 1 {
		t.Fatalf("got %d tables, want 1: %+v", len(tables), tables)
	}
	got := &tables[0]
	if got.Rows != 2 || got.Cols != 2 || len(got.Cells) != 4 {
		t.Errorf("got %dx%d with %d cells, want 2x2 with 4",
			got.Rows, got.Cols, len(got.Cells))
	}
	want := []CellRect{
		{X0: 30, Y0: 30, X1: 105, Y1: 60}, {X0: 105, Y0: 30, X1: 180, Y1: 60},
		{X0: 30, Y0: 60, X1: 105, Y1: 90}, {X0: 105, Y0: 60, X1: 180, Y1: 90},
	}
	for i, w := range want {
		if i >= len(got.Cells) {
			break
		}
		if !sameRect(got.Cells[i].Rect, w) {
			t.Errorf("cell %d is %+v, want %+v", i, got.Cells[i].Rect, w)
		}
		if got.Cells[i].Chars == 0 {
			t.Errorf("cell %d holds no text, but a run was placed in it", i)
		}
	}
	if got.Box != (CellRect{X0: 30, Y0: 30, X1: 180, Y1: 90}) {
		t.Errorf("box is %+v, want 30,30-180,90", got.Box)
	}
}

// TestTheTextGuardRejectsAGridOfFrames is the hermetic form of what pages 22, 38
// and 44 of the columns fixture are: the same rules, the same grid, and no words
// in the cells. Geometry cannot tell the two apart and the text is the only
// difference, so the same SVG is used for both and only the runs change.
func TestTheTextGuardRejectsAGridOfFrames(t *testing.T) {
	rules, err := parseRules([]byte(compositingSVG))
	if err != nil {
		t.Fatalf("parseRules: %v", err)
	}

	// One cell of the four holds a caption; the other three are pictures. That is
	// 25% and the guard wants half.
	framed := &PageRuns{No: 1, Width: 300, Height: 150, Runs: []TextRun{
		{X: 40, Y: 35, Width: 50, Height: 10, Text: "Fig. 1"},
	}}
	if got := FindRuledTables(rules, framed); len(got) != 0 {
		t.Errorf("a grid with text in 1 of 4 cells was read as a table: %+v", got)
	}

	// Two of four is exactly half, which passes: a real table often has an empty
	// cell, and page 15 of the sequential fixture has three.
	half := &PageRuns{No: 1, Width: 300, Height: 150, Runs: []TextRun{
		{X: 40, Y: 35, Width: 50, Height: 10, Text: "left"},
		{X: 115, Y: 65, Width: 50, Height: 10, Text: "right"},
	}}
	if got := FindRuledTables(rules, half); len(got) != 1 {
		t.Errorf("a grid with text in 2 of 4 cells was rejected, want one table: %+v", got)
	}

	// And with no page at all the shape guard stands alone, which is what the
	// discrimination measurement in docs/design/conversion.md was taken with.
	if got := FindRuledTables(rules, nil); len(got) != 1 {
		t.Errorf("the shape guard alone found %d tables, want 1", len(got))
	}
}

// TestATableWithNoOuterVerticals is the columns fixture's own table shape, which
// draws no left or right border at all: the row rules simply stop together, and
// the page reads as bordered because of it. Its section rows also interrupt the
// column rule, which fragments one printed table into three connected components
// and must be rejoined.
func TestATableWithNoOuterVerticals(t *testing.T) {
	// Three row bands. The middle one is a full-width section heading, so the
	// column rule is drawn only above and below it.
	var rules []Rule
	for _, y := range []float64{100, 130, 160, 190} {
		rules = append(rules, Rule{Dir: Horizontal, At: y, Start: 30, End: 430, Thickness: 0.75})
	}
	rules = append(rules,
		Rule{Dir: Vertical, At: 170, Start: 100, End: 130, Thickness: 0.75},
		Rule{Dir: Vertical, At: 170, Start: 160, End: 190, Thickness: 0.75},
	)

	page := &PageRuns{No: 1, Width: 892, Height: 850}
	for _, r := range [][2]float64{{40, 105}, {200, 105}, {40, 135}, {40, 165}, {200, 165}} {
		page.Runs = append(page.Runs, TextRun{
			X: r[0], Y: r[1], Width: 60, Height: 17, Text: "cell text",
		})
	}

	tables := FindRuledTables(rules, page)
	if len(tables) != 1 {
		t.Fatalf("got %d tables, want 1 — the fragments were not rejoined: %+v",
			len(tables), tables)
	}
	got := &tables[0]
	if len(got.Cells) != 5 {
		t.Fatalf("got %d cells, want 5 (two, one spanning, two): %+v",
			len(got.Cells), got.Cells)
	}
	// The middle row is one cell across the whole width, which is what walking a
	// row at a time expresses and a grid-rectangle walk cannot.
	middle := got.Cells[2]
	if middle.Rect.X0 != 30 || middle.Rect.X1 != 430 {
		t.Errorf("the section row is %+v, want one cell from 30 to 430", middle.Rect)
	}
	if middle.ColSpan != 2 {
		t.Errorf("the section row spans %d columns, want 2", middle.ColSpan)
	}
	// The outer edges at x=30 and x=430 are drawn by nothing; they are believed
	// because every row rule stops there.
	if got.Box.X0 != 30 || got.Box.X1 != 430 {
		t.Errorf("box is %+v, want the implied outer edges 30 and 430", got.Box)
	}
}

// TestTheShapeGuardNeedsMoreThanALine covers what "has a ruled line" is worth on
// its own: nothing. Every page of the columns fixture draws footer crop marks,
// so the guard has to ask for a grid.
func TestTheShapeGuardNeedsMoreThanALine(t *testing.T) {
	cropMarks := []Rule{
		{Dir: Horizontal, At: 825, Start: 30, End: 188, Thickness: 0.5},
		{Dir: Vertical, At: 30, Start: 821, End: 828, Thickness: 0.5},
		{Dir: Vertical, At: 188, Start: 821, End: 828, Thickness: 0.5},
	}
	if got := FindRuledTables(cropMarks, nil); len(got) != 0 {
		t.Errorf("footer crop marks were read as a table: %+v", got)
	}

	// A grid of four cells too narrow to hold a word is the other half of it:
	// the exploded parts diagrams of five of the columns fixture's pages enclose
	// grids of 9-to-20-unit slivers exactly like this one.
	var slivers []Rule
	for _, y := range []float64{100, 120, 140} {
		slivers = append(slivers, Rule{Dir: Horizontal, At: y, Start: 100, End: 140})
	}
	for _, x := range []float64{100, 120, 140} {
		slivers = append(slivers, Rule{Dir: Vertical, At: x, Start: 100, End: 140})
	}
	if got := FindRuledTables(slivers, nil); len(got) != 0 {
		t.Errorf("a 20-unit grid of slivers was read as a table: %+v", got)
	}
}

// TestTwoTablesSideBySideStaySeparate is the fragmenting case from page 57: two
// independent tables that share a row position. Joining collinear rules without
// asking whether they touch welds them into one grid.
func TestTwoTablesSideBySideStaySeparate(t *testing.T) {
	var rules []Rule
	for _, x0 := range []float64{30, 450} {
		for _, y := range []float64{100, 140, 180} {
			rules = append(rules, Rule{Dir: Horizontal, At: y, Start: x0, End: x0 + 380})
		}
		for _, dx := range []float64{0, 190, 380} {
			rules = append(rules, Rule{Dir: Vertical, At: x0 + dx, Start: 100, End: 180})
		}
	}
	got := FindRuledTables(rules, nil)
	if len(got) != 2 {
		t.Fatalf("got %d tables, want 2 — a shared row position welded them: %+v",
			len(got), got)
	}
	if got[0].Box.X1 > got[1].Box.X0 {
		t.Errorf("the two tables overlap: %+v and %+v", got[0].Box, got[1].Box)
	}
	for i := range got {
		if len(got[i].Cells) != 4 {
			t.Errorf("table %d has %d cells, want 4", i, len(got[i].Cells))
		}
	}
}

func TestParseTransformComposesInWritingOrder(t *testing.T) {
	// A translate then a scale: the scale applies inside the translate, so the
	// point (1,1) lands at 10+2, 20+3 rather than at (10+1)*2.
	m := parseTransform("translate(10, 20) scale(2, 3)")
	if x, y := m.apply(1, 1); x != 12 || y != 23 {
		t.Errorf("translate then scale put (1,1) at (%g,%g), want (12,23)", x, y)
	}
	// The form cairo actually writes for a flipped page.
	m = parseTransform("matrix(0.998785, 0, 0, -0.998785, 19.771253, 68.984759)")
	x, y := m.apply(0, 0)
	if math.Abs(x-19.771253) > 1e-9 || math.Abs(y-68.984759) > 1e-9 {
		t.Errorf("matrix put the origin at (%g,%g), want its translation", x, y)
	}
	// A negative determinant must not make the stroke width negative.
	if s := m.scale(); math.Abs(s-0.998785) > 1e-9 {
		t.Errorf("scale of a y-flipped matrix is %g, want 0.998785", s)
	}
	if got := parseTransform(""); got != identity {
		t.Errorf("an absent transform is %v, want the identity", got)
	}
	if got := parseTransform("skewX(30)"); got != identity {
		t.Errorf("an unhandled transform is %v, want the identity", got)
	}
}

func TestSubpathsFlattenCurvesAndSplitOnMoveto(t *testing.T) {
	// A moveto's extra coordinate pairs are implicit linetos, which is how cairo
	// writes a table border, and a curve contributes only its endpoint.
	got := subpaths("M 10 10 20 10 L 30 10 C 40 10 50 10 60 10 Z M 100 100 L 110 100")
	if len(got) != 2 {
		t.Fatalf("got %d subpaths, want 2: %v", len(got), got)
	}
	// M(10,10) 20,10 30,10 60,10 and the close back to the start.
	want := []point{{10, 10}, {20, 10}, {30, 10}, {60, 10}, {10, 10}}
	if len(got[0]) != len(want) {
		t.Fatalf("first subpath is %v, want %v", got[0], want)
	}
	for i := range want {
		if got[0][i] != want[i] {
			t.Errorf("first subpath point %d is %v, want %v", i, got[0][i], want[i])
		}
	}
	// Relative commands, and the horizontal and vertical shorthands.
	got = subpaths("m 10 10 h 20 v 5 l -20 0 z")
	if len(got) != 1 {
		t.Fatalf("got %d subpaths, want 1: %v", len(got), got)
	}
	if last := got[0][3]; last != (point{10, 15}) {
		t.Errorf("relative path reached %v, want 10,15", last)
	}
	if got := subpaths(""); got != nil {
		t.Errorf("an empty path is %v, want nothing", got)
	}
}

func TestScanFloatReadsTheFormsCairoWrites(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want float64
		used int
	}{
		{"0.691406", 0.691406, 8},
		{"-0.000986825 ", -0.000986825, 12},
		{"13.729858%", 13.729858, 9},
		{"1e-5", 1e-5, 4},
		{"1.5E+2x", 150, 6},
		{".5", 0.5, 2},
		{"5.", 5, 2},
		{"-", 0, 0},
		{"none", 0, 0},
		{"", 0, 0},
		// An exponent with no digits is not part of the number: "2e" is 2.
		{"2e", 2, 1},
	} {
		got, used := scanFloat(tc.in)
		if got != tc.want || used != tc.used {
			t.Errorf("scanFloat(%q) = %g after %d bytes, want %g after %d",
				tc.in, got, used, tc.want, tc.used)
		}
	}
}

// TestParseSVGRejectsNothingItCannotRead checks the degradation, since the input
// is derived from an untrusted PDF: malformed XML is an error, but an SVG with
// nothing recognisable in it is simply a page with no rules.
func TestParseSVGDegradesRatherThanPanics(t *testing.T) {
	if _, err := parseRules([]byte("<svg><g>")); err == nil {
		t.Error("truncated XML parsed without error")
	}
	for _, in := range []string{
		`<svg></svg>`,
		`<svg><path d=""/></svg>`,
		`<svg><path fill="none" stroke="black" d="M 0 0 L 1 1"/></svg>`,
		`<svg><rect fill="black"/></svg>`,
		`<svg><use xlink:href="#nothing"/></svg>`,
		// A reference cycle, which the visited set has to break.
		`<svg><defs><g id="a"><use xlink:href="#b"/></g><g id="b"><use xlink:href="#a"/></g></defs><use xlink:href="#a"/></svg>`,
	} {
		got, err := parseRules([]byte(in))
		if err != nil {
			t.Errorf("parseRules(%q): %v", in, err)
		}
		if len(got) != 0 {
			t.Errorf("parseRules(%q) found %d rules, want none", in, len(got))
		}
	}
}

func TestMergeSpansUnionsTheSegmentsOfOneRule(t *testing.T) {
	// The measured shape: a row rule crossing a column divider arrives as two
	// segments meeting at it. Unmerged, that meeting point reads as a terminus.
	got := mergeSpans([]ruleSpan{{173.3, 428.1}, {29.7, 173.3}})
	if len(got) != 1 || got[0] != (ruleSpan{29.7, 428.1}) {
		t.Errorf("got %v, want one span 29.7..428.1", got)
	}
	// Two rules 22 units apart are two lines, which is what keeps page 57's
	// side-by-side tables separate.
	got = mergeSpans([]ruleSpan{{29.7, 428.1}, {450.2, 848.7}})
	if len(got) != 2 {
		t.Errorf("got %v, want two spans", got)
	}
}

func TestCoveredFraction(t *testing.T) {
	for _, tc := range []struct {
		spans  []ruleSpan
		lo, hi float64
		want   float64
	}{
		{[]ruleSpan{{0, 10}}, 0, 10, 1},
		{[]ruleSpan{{0, 5}}, 0, 10, 0.5},
		{[]ruleSpan{{0, 6}, {4, 10}}, 0, 10, 1},
		{nil, 0, 10, 0},
		{[]ruleSpan{{0, 10}}, 10, 10, 0},
		{[]ruleSpan{{-100, 100}}, 0, 10, 1},
	} {
		if got := coveredFraction(tc.spans, tc.lo, tc.hi); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("coveredFraction(%v, %g, %g) = %g, want %g",
				tc.spans, tc.lo, tc.hi, got, tc.want)
		}
	}
}

func TestClusterPositionsChains(t *testing.T) {
	// One printed rule drawn twice for a blend comes back a fraction apart and
	// must be one line. Clustering chains, so a run of near neighbours is one
	// line rather than splitting at the first pair further than the tolerance.
	// 100, 102 and 104 are one line although the ends are 4 apart, because each
	// is within the tolerance of its neighbour; 143.2 is a line of its own.
	got := clusterPositions([]float64{104, 143.2, 100, 102})
	if len(got) != 2 {
		t.Fatalf("got %v, want two lines", got)
	}
	if got[0] != 102 || got[1] != 143.2 {
		t.Errorf("got %v, want the mean of the chain and 143.2", got)
	}
	if got := clusterPositions(nil); got != nil {
		t.Errorf("got %v, want nothing", got)
	}
}

func hasRule(rules []Rule, want Rule) bool {
	for i := range rules {
		r := &rules[i]
		if r.Dir == want.Dir && math.Abs(r.At-want.At) < 0.01 &&
			math.Abs(r.Start-want.Start) < 0.01 && math.Abs(r.End-want.End) < 0.01 {
			return true
		}
	}
	return false
}

func sameRect(a, b CellRect) bool {
	return math.Abs(a.X0-b.X0) < 0.01 && math.Abs(a.Y0-b.Y0) < 0.01 &&
		math.Abs(a.X1-b.X1) < 0.01 && math.Abs(a.Y1-b.Y1) < 0.01
}

func formatRules(rules []Rule) string {
	var b strings.Builder
	for i := range rules {
		r := &rules[i]
		fmt.Fprintf(&b, "\n  %s at %.3f from %.3f to %.3f thick %.3f",
			r.Dir, r.At, r.Start, r.End, r.Thickness)
		if r.Filled {
			b.WriteString(" filled")
		}
	}
	return b.String()
}
