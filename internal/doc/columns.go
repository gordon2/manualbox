package doc

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Text columns are found by projecting text runs onto the x-axis and looking
// for the bands that almost nothing crosses.
//
// Two simpler things were tried first and both fail on a real manual, which is
// why this is more than a one-liner.
//
// **Binary coverage fails.** Splitting wherever no run at all covers an x gets
// 63 of 68 pages of the measured fixture right and merges the rest. On its
// page 63 a single section heading runs across the top of both columns; on its
// page 68 a banner heading crosses all three. One run out of eighty is enough
// to weld two columns together for ever, and no choice of gap width repairs it.
//
// **Left-alignment peaks fail**, the other way, by over-splitting. A parts list
// set with a hanging indent puts markers at x=30 and their text at x=60, so a
// three-column page offers six peaks; a table nested inside a column adds a
// seventh at x=192. No "merge peaks closer than N" rule separates a 30-unit
// hanging indent from a 162-unit table indent and still keeps two real columns
// 280 units apart.
//
// What works is counting, not testing for emptiness: for each x, how many runs
// cross it. A gutter is then a band that *few* runs cross rather than none,
// which absorbs the spanning heading, and it is a page-wide statistic rather
// than a local one, which is what makes it immune to indents. Measured on the
// fixture's page 13, the hanging indent at x=43-59 is crossed by 10 runs —
// the lines of that same column that are set to the full measure — while its
// real gutter at x=290-309 is crossed by none. On page 63 the nested table's
// gap at x=186-191 is crossed by 17 runs, the paragraphs printed above the
// table. Neither can be mistaken for a column boundary once you count.
//
// See docs/design/layouts.md for why column geometry is needed at all: in a
// parallel-columns manual a column is a language, and a page is not.

// Bounds on what counts as a text run, a gutter and a column.
//
// Every one of these is measured against the Thomas DryBox Amfibia fixture
// (68 pages, testdata/fixtures/thomas-drybox-amfibia.json), read through
// `pdftohtml -xml`, whose 892-unit page space matches a `pdftoppm -r 108`
// raster 1:1 so that a detected box can be drawn on the page and looked at.
const (
	// minRunHeightFraction is how short a run may be, against the page's own
	// median run height, and still count as text.
	//
	// This is the artifact filter, and it is not optional. The fixture's text
	// layer carries an InDesign filename slug and an export timestamp 260 times
	// each across 67 of its 68 pages — 8% of all runs — because most of its
	// illustrations are placed PDFs that each brought their own slug along,
	// scaled down with the artwork. Several sit in a gutter.
	//
	// Height separates them cleanly where their text does not: 520 of the 522
	// artifact runs are 2 to 6 units tall against a body median of 17 to 21
	// (0.12 to 0.35 of it), while the shortest genuine text on any page is 9
	// (0.53). 0.4 sits in that gap. Measured effect: without it the right-hand
	// column of 65 of the 68 pages stretches into the margin to swallow a slug.
	//
	// Repetition, the more obvious filter, is wrong in both directions. Keying
	// on "appears many times on this page" misses the artifact entirely on the
	// pages carrying only one placed graphic (two occurrences), and on the back
	// page of service addresses it would delete 742 of 769 genuine runs,
	// because "Robert Thomas" really is printed twelve times there. Keying on
	// "appears on many pages" is worse: it also matches the printed language
	// tags D, PL and UA, which are the most valuable signal in the document.
	minRunHeightFraction = 0.4

	// maxGutterCrossings is how many runs may cross a band and leave it still a
	// gutter. It is a count of spanning furniture — a banner heading, a footer,
	// a caption set across the measure — not a fraction of the page.
	//
	// Measured: the fixture's page 63 carries one such run (the section heading
	// over both columns), page 68 two. 4 leaves headroom for a page with a
	// heading, a footer and a caption. The eight ground-truth pages all come out
	// right for any value from 2 to 7.
	maxGutterCrossings = 4

	// minGutterFraction is how wide a low-crossing band must be, as a fraction
	// of page width, before it is believed to be a gutter rather than a chance
	// alignment of word spaces.
	//
	// 1% is 8.9 units on the fixture's 892-unit page. The narrowest true gutter
	// measured there is 13 units (page 68, between its second and third address
	// columns); the eight ground-truth pages come out right for anything from 4
	// to 12 units. Held as a fraction, not a length, so it survives a page
	// rendered at another resolution.
	minGutterFraction = 0.01

	// minColumnRuns is how many text runs a region needs before it is a column.
	//
	// Without it an exploded diagram becomes a page of columns: the fixture's
	// page 12 scatters numbered callouts across two thirds of its width, in
	// clusters of one to five runs, beside a single real text column of 94.
	// docs/design/layouts.md records a four-run margin element already having
	// been miscounted as a column once, and the fixture manifest settled on
	// eight for the same reason. Nothing between 1 and 16 changes the answer on
	// the eight ground-truth pages, so this is a guard against pages not yet
	// seen rather than a tuned value.
	minColumnRuns = 8

	// minColumnWidthFraction is how wide a region must be, against the page,
	// before it is a column rather than a strip.
	//
	// This guards a case the fixture only narrowly avoids. A list set with a
	// hanging indent puts its markers in a strip of their own — x=30 to 42, with
	// the text from x=60 — and the space between is a page-wide band no text
	// crosses, which is exactly the definition of a gutter used here. On the
	// fixture's page 13 ten lines of that column happen to be set to the full
	// measure and cross it, so the strip never separates; a list without such
	// lines would hand its bullets back as a column.
	//
	// Width settles it where crossings cannot. Measured on the fixture: the
	// narrowest real column is 227 units, 25% of the page, and the narrowest
	// cell column of its troubleshooting tables 116, or 13%; a marker strip is
	// 13 units, 1.5%. 5% lies between them with a wide margin either way, and it
	// changes no answer on the 68 measured pages.
	minColumnWidthFraction = 0.05

	// baselineToleranceFraction and minBaselineShare decide when a strip too
	// narrow to be a column is really part of the column beside it.
	//
	// A list marker and the text it labels sit on one line — poppler reports
	// both with the same top — and that shared baseline is what makes them one
	// column. The projection cannot see it, because the space between a marker
	// and its text is as empty as any gutter. So a narrow strip is folded into
	// its neighbour when most of its runs line up with that neighbour's.
	//
	// The tolerance is small on purpose. Markers align exactly, so it needs to
	// absorb rounding and nothing more; at 15% of the median run height it is
	// about 2.5 units against a 21-unit line pitch, which leaves a stray figure
	// callout roughly one chance in four of matching any given line by accident
	// and almost none of matching four fifths of them.
	baselineToleranceFraction = 0.15
	minBaselineShare          = 0.8

	// maxProjectionBuckets caps the projection array. Page width reaches this
	// code from a caller's coordinates, and a nonsense value would otherwise
	// turn into a nonsense allocation — the same reasoning as maxExtractedBytes
	// in pdf.go. 100,000 is an A0 page at 300 dpi with room to spare.
	maxProjectionBuckets = 100_000
)

// TextRun is one positioned run of text on a page.
//
// X and Y are its top-left corner in the same units as the page dimensions
// passed to [DetectColumns]; the detector never assumes what those units are,
// beyond needing enough of them across a page to resolve a gutter. Poppler's
// `pdftohtml -xml` reports exactly these four numbers per run, which is where
// the shape comes from.
type TextRun struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Text   string  `json:"text"`
	// Font is what the run is set in, and it is what will tell a heading from a
	// paragraph. Nothing in this file reads it — the column detector is pure
	// geometry — but it travels with the run because resolving it needs the
	// document's font table, which only [ExtractRuns] sees.
	//
	// Its zero value means "not known", which is what a run built by hand in a
	// test carries. That is why it comes last and why the five fields above are
	// unchanged: every existing caller constructs those positionally or by name
	// and must keep compiling and meaning the same thing.
	Font Font `json:"font,omitzero"`
}

func (r *TextRun) right() float64  { return r.X + r.Width }
func (r *TextRun) bottom() float64 { return r.Y + r.Height }

// Column is one text column of a page.
type Column struct {
	// Min and Max are the x-range the column's text actually occupies, not the
	// band it was cut from: they are the leftmost and rightmost edges of the
	// runs assigned to it. A caller clipping text to this range gets the
	// column's own words and no others.
	Min float64 `json:"min"`
	Max float64 `json:"max"`
	// Runs is how many text runs the column holds. It is the density evidence:
	// a column of five runs is margin furniture, not a column.
	Runs int `json:"runs"`
	// Note says in checkable terms why this is a column.
	Note string `json:"note,omitempty"`
}

// Width is the column's horizontal extent.
func (c *Column) Width() float64 { return c.Max - c.Min }

// Gutter is a band that separates two columns.
type Gutter struct {
	// Min and Max are the band's x-range.
	Min float64 `json:"min"`
	Max float64 `json:"max"`
	// Crossings is how many runs cross it — the spanning headings and footers
	// that binary coverage mistakes for proof that the columns are one.
	Crossings int `json:"crossings"`
}

// ColumnLayout is what the detector concluded about one page.
//
// An empty Columns is a normal outcome, not a failure: a full-page photograph
// and a diagram with nothing but callouts both have no text column, and saying
// so is more useful than naming one. Note explains which it was.
type ColumnLayout struct {
	// Columns are the page's text columns, left to right.
	Columns []Column `json:"columns,omitempty"`
	// Gutters are the bands the columns were cut at, left to right.
	Gutters []Gutter `json:"gutters,omitempty"`
	// Runs is how many runs survived filtering and were projected.
	Runs int `json:"runs"`
	// Dropped counts the runs excluded before projection, by reason. Present so
	// that a page whose text vanished can be explained rather than guessed at.
	Dropped DroppedRuns `json:"dropped"`
	// Spanning is how many runs crossed a gutter and so belong to no single
	// column — the headings and footers set across the measure.
	Spanning int `json:"spanning"`
	// Note says in checkable terms how the page was read.
	Note string `json:"note,omitempty"`
}

// DroppedRuns records why runs were excluded from the projection.
type DroppedRuns struct {
	// Blank is runs with no visible text.
	Blank int `json:"blank"`
	// Rotated is runs with no horizontal extent. Poppler reports rotated text
	// with width 0, which is a marginal note turned on its side — real text,
	// but not evidence of a column, and it must not stretch one.
	Rotated int `json:"rotated"`
	// OffPage is runs lying outside the page box. These are not a curiosity:
	// the fixture's back page carries seven lines of a superseded address list
	// parked above the top edge, and counting them merges two of its three
	// columns.
	OffPage int `json:"offPage"`
	// Small is runs too short to be body text — see [minRunHeightFraction].
	Small int `json:"small"`
}

// Total is how many runs were dropped for all reasons.
func (d *DroppedRuns) Total() int { return d.Blank + d.Rotated + d.OffPage + d.Small }

// DetectColumns finds the text columns of one page.
//
// The runs are one page's text with coordinates, in any consistent unit;
// pageWidth and pageHeight are that page's box in the same unit. Column widths
// are not assumed to be equal, and neither is their number, their pitch, nor
// that a page has any: all three vary within a single real manual, sometimes
// between facing pages. See docs/design/layouts.md.
//
// There is deliberately no confidence score. The honest evidence is countable —
// how many runs a column holds, how many crossed each gutter, how many runs
// were dropped and why — and a number in 0 to 1 synthesised from those would
// only hide them. Every field here is something a reader can check against the
// page.
func DetectColumns(runs []TextRun, pageWidth, pageHeight float64) ColumnLayout {
	var out ColumnLayout

	kept := usableRuns(runs, pageWidth, pageHeight, &out.Dropped)
	out.Runs = len(kept)
	if len(kept) == 0 {
		out.Note = fmt.Sprintf("no usable text runs (%s)", dropSummary(&out.Dropped))
		return out
	}

	buckets := int(math.Ceil(pageWidth)) + 1
	if buckets < 2 || buckets > maxProjectionBuckets {
		out.Note = fmt.Sprintf("page width %g cannot be projected", pageWidth)
		return out
	}

	crossings := project(kept, buckets)
	minGutter := minGutterFraction * pageWidth
	gutters := findGutters(crossings, minGutter)

	inkMin, inkMax := extent(kept)
	regions := between(gutters, inkMin, inkMax)

	out.Columns, out.Spanning = assign(kept, regions, gutters,
		minColumnWidthFraction*pageWidth, baselineToleranceFraction*medianHeight(kept))
	out.Gutters = keepInnerGutters(gutters, crossings, out.Columns)
	out.Note = layoutNote(&out, minGutter)
	return out
}

// usableRuns drops everything that is not body text, recording why.
func usableRuns(runs []TextRun, pageWidth, pageHeight float64, dropped *DroppedRuns) []TextRun {
	texted := make([]TextRun, 0, len(runs))
	for i := range runs {
		if strings.TrimSpace(runs[i].Text) == "" {
			dropped.Blank++
			continue
		}
		texted = append(texted, runs[i])
	}
	if len(texted) == 0 {
		return nil
	}

	// The page's own median height is the reference, not a fixed size: a cover
	// set in 34-unit type and a body page set in 17 must both be judged against
	// what is normal for themselves.
	minHeight := minRunHeightFraction * medianHeight(texted)

	kept := make([]TextRun, 0, len(texted))
	for i := range texted {
		r := &texted[i]
		switch {
		case r.Width <= 0:
			dropped.Rotated++
		case r.X < 0 || r.right() > pageWidth || r.Y < 0 || r.bottom() > pageHeight:
			dropped.OffPage++
		case r.Height < minHeight:
			dropped.Small++
		default:
			kept = append(kept, *r)
		}
	}
	return kept
}

func medianHeight(runs []TextRun) float64 {
	hs := make([]float64, len(runs))
	for i := range runs {
		hs[i] = runs[i].Height
	}
	sort.Float64s(hs)
	n := len(hs)
	if n%2 == 1 {
		return hs[n/2]
	}
	return (hs[n/2-1] + hs[n/2]) / 2
}

// project counts, for each x, how many runs cross it.
func project(runs []TextRun, buckets int) []int {
	crossings := make([]int, buckets)
	for i := range runs {
		lo, hi := bucketRange(&runs[i], buckets)
		for x := lo; x <= hi; x++ {
			crossings[x]++
		}
	}
	return crossings
}

func bucketRange(r *TextRun, buckets int) (lo, hi int) {
	lo = int(math.Floor(r.X))
	hi = int(math.Floor(r.right()))
	if lo < 0 {
		lo = 0
	}
	if hi > buckets-1 {
		hi = buckets - 1
	}
	return lo, hi
}

// span is a half-open-free inclusive x-range in bucket coordinates.
type span struct{ lo, hi int }

func (s span) width() int { return s.hi - s.lo + 1 }

// findGutters returns the bands few enough runs cross, wide enough to believe.
func findGutters(crossings []int, minWidth float64) []span {
	var out []span
	start := -1
	for x := 0; x <= len(crossings); x++ {
		low := x < len(crossings) && crossings[x] <= maxGutterCrossings
		switch {
		case low && start < 0:
			start = x
		case !low && start >= 0:
			if s := (span{start, x - 1}); float64(s.width()) >= minWidth {
				out = append(out, s)
			}
			start = -1
		}
	}
	return out
}

// between returns the regions left over once the gutters are removed, clipped
// to where there is ink. The clipping is what keeps a page's blank margins from
// being offered as columns.
func between(gutters []span, inkMin, inkMax float64) []span {
	lo, hi := int(math.Floor(inkMin)), int(math.Floor(inkMax))
	var out []span
	cur := lo
	for _, g := range gutters {
		if g.hi < cur || g.lo > hi {
			continue
		}
		if g.lo > cur {
			out = append(out, span{cur, g.lo - 1})
		}
		cur = g.hi + 1
	}
	if cur <= hi {
		out = append(out, span{cur, hi})
	}
	return out
}

func extent(runs []TextRun) (lo, hi float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for i := range runs {
		lo = math.Min(lo, runs[i].X)
		hi = math.Max(hi, runs[i].right())
	}
	return lo, hi
}

// assign puts each run in a region and turns the regions that earn it into
// columns. A run crossing a gutter belongs to no column and is counted instead:
// a heading printed across two columns is evidence about neither.
func assign(runs []TextRun, regions, gutters []span, minWidth, baselineTol float64) (cols []Column, spanning int) {
	members := make([][]int, len(regions))

	for i := range runs {
		r := &runs[i]
		if crossesAny(r, gutters) {
			spanning++
			continue
		}
		if k := regionOf(r, regions); k >= 0 {
			members[k] = append(members[k], i)
		}
	}

	absorbStrips(runs, members, minWidth, baselineTol)

	for k := range regions {
		mine := members[k]
		if len(mine) < minColumnRuns {
			continue
		}
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, i := range mine {
			lo = math.Min(lo, runs[i].X)
			hi = math.Max(hi, runs[i].right())
		}
		if hi-lo < minWidth {
			continue
		}
		cols = append(cols, Column{
			Min: lo, Max: hi, Runs: len(mine),
			Note: fmt.Sprintf("%d text runs between x=%.0f and x=%.0f", len(mine), lo, hi),
		})
	}
	return cols, spanning
}

// absorbStrips folds a region too narrow to be a column into the column beside
// it, when the two sit on the same lines.
//
// This is the hanging indent. A parts list puts its markers at x=30 and their
// text at x=60, and the space between is page-wide and empty — a gutter by
// every test the projection can apply. The fixture's page 13 escapes only
// because ten lines of each column are set to the full measure and cross the
// indent; a list without such lines would otherwise lose its markers from the
// column's x-range, and a caller clipping to that range would lose the item
// numbers with them.
//
// A figure callout sitting beside a column is not absorbed, because it does not
// share the column's baselines. That is the whole distinction, and it is a
// property of the page rather than a threshold: a marker and its text are one
// line of one column, a callout and a column are not.
func absorbStrips(runs []TextRun, members [][]int, minWidth, baselineTol float64) {
	for k := range members {
		if len(members[k]) == 0 || spread(runs, members[k]) >= minWidth {
			continue
		}
		best, bestShared := -1, 0
		for _, n := range []int{k - 1, k + 1} {
			if n < 0 || n >= len(members) || len(members[n]) == 0 {
				continue
			}
			if spread(runs, members[n]) < minWidth {
				continue
			}
			shared := sharedBaselines(runs, members[k], members[n], baselineTol)
			if shared > bestShared {
				best, bestShared = n, shared
			}
		}
		if best < 0 || float64(bestShared) < minBaselineShare*float64(len(members[k])) {
			continue
		}
		members[best] = append(members[best], members[k]...)
		members[k] = nil
	}
}

// spread is how wide the runs of a region reach.
func spread(runs []TextRun, idx []int) float64 {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, i := range idx {
		lo = math.Min(lo, runs[i].X)
		hi = math.Max(hi, runs[i].right())
	}
	return hi - lo
}

// sharedBaselines counts how many of a strip's runs sit on a line that the
// other region also occupies.
func sharedBaselines(runs []TextRun, strip, other []int, tol float64) int {
	n := 0
	for _, i := range strip {
		for _, j := range other {
			if sameBaseline(runs[i].Y, runs[j].Y, tol) {
				n++
				break
			}
		}
	}
	return n
}

// sameBaseline is the single definition of "these runs sit on one line".
//
// Poppler reports every run of a line with the same top, so this has rounding to
// absorb and nothing more — see [baselineToleranceFraction] for the tolerance and
// what it was measured against. Shared with blocks.go, which folds runs into lines
// for a different purpose: a marker and its text are one line whether the question
// being asked is which column they belong to or which paragraph.
func sameBaseline(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// crossesAny reports whether a run passes right over a gutter. Reaching into
// one is not crossing it: a column's longest lines routinely end inside the
// whitespace beside them.
func crossesAny(r *TextRun, gutters []span) bool {
	for _, g := range gutters {
		if r.X < float64(g.lo) && r.right() > float64(g.hi) {
			return true
		}
	}
	return false
}

// regionOf places a run by its left edge, which is where its column is: a run
// starting inside a gutter is flowing into the column on its right.
func regionOf(r *TextRun, regions []span) int {
	x := int(math.Floor(r.X))
	for k := range regions {
		if x <= regions[k].hi {
			if x >= regions[k].lo || r.right() >= float64(regions[k].lo) {
				return k
			}
			return -1
		}
	}
	return -1
}

// keepInnerGutters reports only the gutters that actually separate two reported
// columns. The rest are margins and the blank space around a diagram, which are
// true of the page but say nothing about its columns.
func keepInnerGutters(gutters []span, crossings []int, cols []Column) []Gutter {
	if len(cols) < 2 {
		return nil
	}
	var out []Gutter
	for _, g := range gutters {
		if !separatesTwo(g, cols) {
			continue
		}
		out = append(out, Gutter{
			Min:       float64(g.lo),
			Max:       float64(g.hi),
			Crossings: maxIn(crossings, g),
		})
	}
	return out
}

func separatesTwo(g span, cols []Column) bool {
	for i := 0; i+1 < len(cols); i++ {
		if cols[i].Max <= float64(g.hi) && cols[i+1].Min >= float64(g.lo) {
			return true
		}
	}
	return false
}

func maxIn(crossings []int, s span) int {
	best := 0
	for x := s.lo; x <= s.hi && x < len(crossings); x++ {
		if crossings[x] > best {
			best = crossings[x]
		}
	}
	return best
}

// layoutNote renders the reasoning in the terms a reader can check against the
// page: how many columns, cut where, and what was set aside to see them.
func layoutNote(l *ColumnLayout, minGutter float64) string {
	var b strings.Builder
	switch len(l.Columns) {
	case 0:
		fmt.Fprintf(&b, "no region holds the %d text runs a column needs", minColumnRuns)
	case 1:
		fmt.Fprintf(&b, "one text column, x=%.0f-%.0f", l.Columns[0].Min, l.Columns[0].Max)
	default:
		parts := make([]string, len(l.Columns))
		for i := range l.Columns {
			parts[i] = fmt.Sprintf("%.0f-%.0f", l.Columns[i].Min, l.Columns[i].Max)
		}
		fmt.Fprintf(&b, "%d text columns at x=%s, cut at %d gutter(s) at least %.0f wide "+
			"that at most %d runs cross",
			len(l.Columns), strings.Join(parts, ", "), len(l.Gutters), minGutter, maxGutterCrossings)
	}
	if l.Spanning > 0 {
		fmt.Fprintf(&b, "; %d run(s) span a gutter and belong to no column", l.Spanning)
	}
	if l.Dropped.Total() > 0 {
		fmt.Fprintf(&b, "; ignored %s", dropSummary(&l.Dropped))
	}
	return b.String()
}

func dropSummary(d *DroppedRuns) string {
	parts := make([]string, 0, 4)
	for _, p := range []struct {
		n    int
		what string
	}{
		{d.Blank, "blank"},
		{d.Rotated, "rotated"},
		{d.OffPage, "off-page"},
		{d.Small, "sub-legible"},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, ", ") + " runs"
}
