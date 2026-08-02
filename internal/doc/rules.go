package doc

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/gordon2/manualbox/internal/extern"
)

// A table is found from the lines the page draws, which is a different input
// rather than a cleverer reading of the old one.
//
// [DetectColumns] and docs/design/layouts.md both record that the *geometry of
// the text* cannot tell a table cell from a text column, and that stands: a
// two-column page and a two-column table project identically. What separates
// them here is not a better statistic over the same runs but a second source —
// the vector graphics. A ruled table is ruled because the document drew lines,
// and those lines are recoverable exactly.
//
// They are not in the input the probe already reads, and that was measured
// rather than assumed. On the parallel-columns fixture's page 57, a page whose
// tables are plainly visible in a render, `pdftohtml -xml` emits four kinds of
// element — pdf, page, fontspec, text — and no path of any sort. So the rules
// are absent from that output, not merely awkward to extract. `pdftocairo -svg`
// reports every one of them.
//
// Coordinates are multiplied by [svgPointScale] on the way out, because cairo
// writes the PDF's own points and everything else in this package works in
// poppler's 1.5-scaled space. That is what keeps a cell checkable: a
// `pdftoppm -r 108` raster matches the output of this file 1:1, so a detected
// cell can be drawn on the rendered page and looked at, which is how the counts
// in docs/design/conversion.md were arrived at.
//
// Five properties of real cairo output shape this code, and every one of them
// was found by reading output that came back wrong first.
//
// **Glyph outlines are paths too.** Cairo writes each glyph as a filled path in
// <defs> under `<g id="glyph-N-M">` and draws text by referencing it. A page of
// text is therefore tens of thousands of paths, some of them thin slivers. They
// are excluded structurally, by that id, rather than by any thinness heuristic —
// a heuristic would have to distinguish the stem of an l from a hairline rule,
// and cannot.
//
// **Some table rules are also inside <defs>, and skipping <defs> loses them.**
// The columns fixture draws page 57's tables in a blend group. Cairo hoists such
// a group out into `<g id="compositing-group-N">` and pulls it back at the use
// site with `<g filter="url(#filter-N)" transform="translate(-tx,-ty)">`, while
// the hoisted group itself carries `translate(+tx,+ty)`. Measured, both wrong
// answers first: skipping <defs> returns 18 rules for that page, all of them
// footer crop marks, and every table rule is lost; entering <defs> without
// composing the transform of the element that referenced it shifts every
// coordinate by the filter region's origin, on that page by about (89, 85)
// units, a fifth of the page. So <defs> is entered, but only through the
// reference that pulls it in, carrying that reference's matrix.
//
// **A rule may be stroked or filled.** A hairline border arrives as a stroked
// path; a thick divider or a shaded rule arrives as a filled sliver whose
// bounding box is thin in one direction. Both occur in both fixtures and both
// are collected — though the filled half turns out to matter less than it looks,
// and [ruleWalker.filled] records how much.
//
// **A table's outer border may not be drawn at all.** The columns fixture's
// troubleshooting tables stroke every row rule and one interior column rule and
// nothing else. The page reads as bordered only because every row rule stops at
// the same x. So a cell boundary counts as present when a rule is drawn there,
// or when the row rules above and below the cell both terminate there — see
// [cellsOfTable].
//
// **A path is not drawn at its own length; it is drawn inside a clip.** Cairo
// writes `clip-path` on the group and nests two of them around most page content,
// so a stroke's extent in the file can run past what is painted. It is read by
// clip.go and intersected in both walkers, and it is measured on page 38 of the
// columns manual: that page's frame draws a left edge to y=268.7 where a 432 dpi
// render shows the stroke ending at 238, and that 30 units of phantom rule was
// closing a table cell on a page of framed illustrations. Every cell count of the
// five ground-truth pages is unchanged by reading it.
//
// Nothing here knows what a block is. This file answers only "where are the
// lines, and what cells do they enclose"; assembling a cell's text into readable
// content is a separate stage.

// Bounds on what counts as a rule, a table and a cell.
//
// Every one is measured against the two fixtures, in the 1.5-scaled space
// described above, on the pages whose printed cells were counted by eye in
// docs/design/conversion.md: page 57 of testdata/fixtures/thomas-drybox-amfibia
// (25 cells) and pages 15, 20, 21 and 100 of dreame-l40-ultra (37, 12, 32, 16).
// The sensitivity range on each is from a sweep over those five pages, one
// constant at a time, holding the rest: the range is every value for which all
// five counts stay right. Two things it says are worth reading before tuning
// anything. Most of these are wide, so they are guards against a page not yet
// seen rather than values fitted to these two documents. And the three that are
// narrow — snapTolerance, maxSegmentGap, minSideCoverage — are narrow because a
// real page sits just outside them, which is recorded on each.
const (
	// svgPointScale converts cairo's PDF points into the space the rest of this
	// package uses. Not a tunable: it is 108 dpi over 72, the same ratio
	// [ExtractRuns] documents, and it is checked against the fixtures' page boxes
	// by TestRuleCoordinateSpaceMatchesTheRuns rather than trusted.
	svgPointScale = 1.5

	// axisTolerance is how far from level a segment may be and still be a rule,
	// in output units. It is arithmetic slack, not a tolerance for sloping lines:
	// every ruled line in both fixtures is exactly axis-aligned in the file, but a
	// composed matrix — cairo writes scales like 0.998785 and negative y — leaves
	// a level line off level in the last bits.
	//
	// Which is the whole measurement, and it surprised the first guess. The five
	// pages are unchanged for every value from 0.01 to 60, so on these documents
	// this threshold discriminates nothing whatever. The one value that breaks it
	// is exactly 0, which returns no cells at all on any of the five. Set for a
	// document not yet seen, then: 0.6 is a fifteenth of the thinnest rule here,
	// so a line sloping enough to be a real diagonal is still rejected.
	axisTolerance = 0.6

	// maxRuleThickness is how thick a filled sliver may be and still be read as
	// a rule rather than as a filled area, and the cap binds: the thickest filled
	// sliver accepted is 3.93 units in the columns manual and 3.99 in the
	// sequential one, both just inside it.
	//
	// The five pages are unchanged from 0.5 up to 22. At 24 page 57 gains 3
	// spurious cells, because at that thickness the shaded background behind its
	// header row is read as a rule and adds a row boundary through the middle of
	// it. So 4 is well clear of the upper edge, and the lower edge is not a
	// constraint on these pages at all — see [ruleWalker.filled] for what filled
	// slivers are actually worth here, which is less than expected.
	maxRuleThickness = 4.0

	// minRuleLength is how long a segment must be to be a rule at all. It drops
	// the corner joins and one-unit stubs a rounded rectangle decomposes into.
	// The five pages are unchanged from 0 to 24 and page 15 of the sequential
	// manual loses a cell at 25, which puts its shortest real rule at about
	// 25 units — a quarter of the way across one of its cells. 6 is a third of a
	// line of body text and leaves a factor of four of headroom.
	minRuleLength = 6.0

	// snapTolerance is how far apart two rules may be and still be the same
	// printed line. A printed rule is drawn once but arrives twice when a table is
	// drawn in a blend group, and the two copies do not land in the same place.
	//
	// Narrow, and both edges are a real page. The five are correct from 0.4 to
	// 3.5. At 0.3 page 57 returns *no cells at all*: its rules arrive in pairs
	// about 0.35 apart, and unclustered every row boundary becomes two with an
	// unusable sliver between them. At 3.6 that page drops to 21, because its
	// header rules are close enough to start welding. 2.5 is near the middle of a
	// window whose ends are 9x apart.
	snapTolerance = 2.5

	// minSideCoverage is the fraction of a candidate cell edge that must actually
	// be drawn. A fraction rather than a gap, because a cell edge is as long as
	// the cell and a 5-unit undrawn stretch means one thing in a 30-unit cell and
	// another in a 300-unit one.
	//
	// The upper edge is what matters and it is close: the five pages hold from
	// 0.05 to 0.98, and at 0.99 page 20 of the sequential manual drops from 12
	// cells to 10 while page 21 drops from 32 to 27. At 1.0 every page collapses —
	// 9 cells instead of 25 on page 57. What that measures is real: these tables
	// have rounded corners, so a rule genuinely stops a unit short of the corner
	// it appears to reach, and demanding the whole edge rejects every corner cell.
	// 0.9 leaves a tenth of every edge undrawn without asking why.
	minSideCoverage = 0.9

	// minCellSize is how small a rectangle may be and still be a cell. The five
	// pages are unchanged from 0 to 20 and break at 25, where page 57 drops to 22
	// cells and page 15 of the sequential manual to 36: the smallest genuine cell
	// on them is a row about 22 units tall. 8 is a comfortable third of that and
	// still discards the 3-unit slivers a doubled rule leaves.
	//
	// This is not the legibility threshold — see [minLegibleCellWidth], which is
	// larger and applies only to the guards.
	minCellSize = 8.0

	// minSharedTermini is how many of a table's row rules must stop at the same x
	// before that x is believed to be a column boundary nobody drew.
	//
	// The five pages are unchanged from 1 to 8 and page 57 returns nothing at 10,
	// which is the measurement that matters: its left table has exactly 9 row
	// rules stopping at x=29.7 and x=428.1, and those two implied edges are the
	// only thing holding the table together, since it draws no outer vertical at
	// all. 2 is the smallest value that can mean "several agree", and going lower
	// would let a single heading underline's right-hand end cut a column.
	minSharedTermini = 2

	// maxSegmentGap is how far apart two collinear rules may be and still be one
	// printed line. This is what stops a joint being read as a terminus: a row
	// rule crossing a column divider arrives as two segments meeting at it, and
	// unmerged, that meeting point looks like a place where the rule stops.
	//
	// Both edges are real pages and the window is wide between them. The five are
	// correct from 0.1 to 21. At 0 page 57 gains 3 cells and loses every spanning
	// cell it has, because its full-width section rows get cut at the divider they
	// cross. At 21.5 three of the five drop sharply — page 15 to 15 cells, page 20
	// to 7 — because its two side-by-side tables both place a row rule at y=103.5
	// with 22 units of white between them, and beyond that the two weld into one
	// grid. The segments of a single printed rule, by contrast, meet within 0.01.
	maxSegmentGap = 3.0

	// maxSVGDepth bounds the recursion through <use> and filter references. Cairo
	// nests about six deep on these documents; the depth limit is there because
	// the references come from an untrusted file and could be made to form a
	// cycle. The visited set below already breaks a simple cycle, so this is the
	// second of two guards, not the only one.
	maxSVGDepth = 40

	// maxRuleSVGBytes caps one page's SVG held in memory, for the reason
	// [maxExtractedBytes] caps a document's text — except that this is per page
	// and needs a much larger headroom than the page count suggests.
	//
	// Measured over both fixtures, whole documents: the columns manual's 68 pages
	// come to 80.4 MB of SVG, and 30.4 MB of that is page 42 alone, a single page
	// of exploded parts diagrams. The sequential manual's largest of 560 pages is
	// 10.1 MB. So a cap sized from the average would reject a real page of a real
	// manual; 64 MB is twice the largest measured and still bounds a hostile file.
	maxRuleSVGBytes = 64 << 20

	// minTableRows, minTableCols and minTableCells are the shape guard.
	//
	// It is not a formality. "This page draws a ruled line" is true of 68 of the
	// columns manual's 68 pages — every page carries footer crop marks — so on its
	// own it separates nothing at all. Requiring a grid of legible cells leaves 13
	// of the 68. See [tableHasText] for why 13 is still three too many.
	minTableRows  = 2
	minTableCols  = 2
	minTableCells = 4

	// minLegibleCellWidth and minLegibleCellHeight are what "legible" means in
	// the shape guard: big enough to have held a word. They are a second, larger
	// threshold than [minCellSize], which only asks whether a rectangle is a cell
	// at all, and the difference between the two is worth 5 pages of the columns
	// manual: at 8 by 8 the shape guard passes 18 of its 68 pages, at 24 by 10 it
	// passes 13. The five it drops — 18, 20, 28, 34 and 42 — are exploded parts
	// diagrams whose leader lines and callout boxes enclose grids of 9-to-20-unit
	// slivers, too narrow to print a word in.
	//
	// 24 units is about a word and a half of body text at this document's 14pt,
	// and 10 is under one line, so a real single-line cell clears both. The sweep
	// over the five ground-truth pages holds up to 70 wide and 20 tall before any
	// count moves — at 100 wide page 100 of the sequential manual stops being a
	// table, and at 30 tall page 21 loses half its cells — so 24 by 10 sits with a
	// factor of three of clearance on the side that matters.
	//
	// They bound the guard only. A narrow cell inside a table that passes is still
	// returned, because it holds real content — a numbering column is narrow on
	// purpose — and dropping it would lose text to make a count tidier.
	minLegibleCellWidth  = 24.0
	minLegibleCellHeight = 10.0

	// minCellsWithText is the fraction of a table's cells that must contain some
	// text. See [tableHasText]; this is the second guard and it is what separates
	// a table from a grid of framed illustrations.
	minCellsWithText = 0.5

	// cellTextMargin is how far outside a cell a run may start and still be
	// counted as inside it, in output units. A cell rectangle is the centre line
	// of the rules that enclose it, so text set tight against a border sits a
	// fraction outside. 2 units is a fifth of a line of body text.
	cellTextMargin = 2.0
)

// RuleDirection is whether a ruled line runs across the page or down it. Only
// these two exist here: a table is enclosed by axis-aligned lines, and anything
// diagonal is not a table rule.
type RuleDirection uint8

// Horizontal is the zero value, which carries no meaning beyond being one of the
// two — a Rule is never usefully zero, since it needs coordinates.
const (
	Horizontal RuleDirection = iota
	Vertical
)

func (d RuleDirection) String() string {
	if d == Vertical {
		return "vertical"
	}
	return "horizontal"
}

// Rule is one axis-aligned line the page draws, in the same 1.5-scaled
// coordinate space as [PageRuns] and a `pdftoppm -r 108` raster.
type Rule struct {
	// Dir is which way the line runs.
	Dir RuleDirection `json:"dir"`
	// At is the coordinate the line holds constant: its y when Horizontal, its x
	// when Vertical.
	At float64 `json:"at"`
	// Start and End are the coordinates it spans in its own direction — x when
	// Horizontal, y when Vertical — with Start <= End always, so a caller never
	// has to normalise one.
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	// Thickness is how thick the line is drawn. Kept because it is the only
	// evidence available for telling a table's outer border from a hairline
	// divider, and because a caller that disagrees with [maxRuleThickness] can
	// filter on it rather than re-extract.
	Thickness float64 `json:"thickness"`
	// Filled reports that this came from a thin filled shape rather than a
	// stroked path. Both are real rules in both fixtures; this says which, so a
	// wrong answer can be traced back to the right half of [ruleWalker].
	Filled bool `json:"filled,omitempty"`
}

// Length is how far the rule runs.
func (r *Rule) Length() float64 { return r.End - r.Start }

// CellRect is an axis-aligned rectangle: a cell's own bounds, or a table's.
type CellRect struct {
	X0, Y0, X1, Y1 float64
}

// Width and Height are the rectangle's extent.
func (c CellRect) Width() float64  { return c.X1 - c.X0 }
func (c CellRect) Height() float64 { return c.Y1 - c.Y0 }

// RuledCell is one cell of a [RuledTable]: where it is, where it sits in the
// grid, and how much text it holds.
type RuledCell struct {
	// Row and Col are 0-based positions in the table's own grid, Row counting
	// down and Col counting across.
	Row int `json:"row"`
	Col int `json:"col"`
	// ColSpan is how many grid columns the cell covers, at least 1. A cell spans
	// when the column rule does not run alongside it, which is how a full-width
	// section heading inside a table is expressed — page 57's "Allgemeine (alle
	// Funktionen)" is one cell across the whole table, not two.
	//
	// There is deliberately no RowSpan. A vertically merged cell is currently
	// dropped rather than spanned, 10 of 47 on one measured page; that is an
	// omission in the row walk recorded in docs/design/conversion.md, and adding
	// the field before the walk finds them would promise something untrue.
	ColSpan int `json:"colSpan"`
	// Rect is the cell in page coordinates, on the centre lines of the rules that
	// enclose it.
	Rect CellRect `json:"rect"`
	// Chars is how many runes of text sit inside the cell — runes rather than
	// bytes, for the reason CONTRIBUTING.md gives: half of a real manual is not
	// Latin, and a byte count would make a Cyrillic cell look half again fuller
	// than the German one beside it.
	//
	// It is the text guard's evidence, kept per cell rather than reduced to the
	// verdict, so that a page rejected as a grid of illustrations can be shown to
	// have been rejected for the right reason.
	Chars int `json:"chars"`
}

// RuledTable is a grid of cells recovered from the lines a page draws.
type RuledTable struct {
	// Box bounds every cell.
	Box CellRect `json:"box"`
	// Rows and Cols are the grid's dimensions. Cols counts grid columns, so a
	// table whose every row is one spanning cell still reports the columns its
	// boundaries imply.
	Rows int `json:"rows"`
	Cols int `json:"cols"`
	// Cells are in reading order: down, then across.
	Cells []RuledCell `json:"cells"`
	// Rules are the lines this table was built from, for checking a wrong answer
	// against a render.
	Rules []Rule `json:"rules,omitempty"`
}

// CellsWithText is how many of the table's cells hold any text at all — the
// numerator of the guard in [tableHasText].
func (t *RuledTable) CellsWithText() int {
	var n int
	for i := range t.Cells {
		if t.Cells[i].Chars > 0 {
			n++
		}
	}
	return n
}

// ExtractRules reads one page's ruled lines with pdftocairo.
//
// Like [ExtractRuns] it never mutates the file and calls nothing remote, so it
// is a pure function of the bytes and safe to re-run, which is what lets the job
// that calls it be idempotent.
//
// One page per invocation, which is the opposite of the choice [ExtractRuns] and
// [ExtractText] make, and it is forced rather than preferred: `pdftocairo -svg`
// writes one SVG document per page and, given a range, concatenates them into
// something that is not valid XML. The cost of that decision is real and
// measured — see docs/design/conversion.md, which also records the untested
// PostScript route that would avoid it.
//
// pdftocairo is optional at runtime. The error from a missing tool is returned
// plainly so a caller can convert a document without cell structure rather than
// fail it.
func ExtractRules(ctx context.Context, path string, page int) ([]Rule, error) {
	if page < 1 {
		return nil, fmt.Errorf("doc: page %d is not a page number", page)
	}
	bin, err := extern.Require(extern.PDFToCairo)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()

	// "-" is the output file, which keeps the SVG in memory: writing it would put
	// a derived file beside the content-addressed blob store, which is immutable
	// by design. -f and -l bound the range to the one page, for the reason above.
	// #nosec G204 -- see ProbeInfo: bin comes from extern's own tool table, path
	// is a blob-store path derived from a validated SHA-256 digest, and page is
	// an int.
	cmd := exec.CommandContext(ctx, bin, "-svg",
		"-f", strconv.Itoa(page), "-l", strconv.Itoa(page), path, "-")
	out := &limitedBuffer{limit: maxRuleSVGBytes}
	var errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = out, &errOut
	if err := cmd.Run(); err != nil {
		if errors.Is(err, errOutputTooLarge) {
			return nil, fmt.Errorf("%w (limit %d bytes)", errOutputTooLarge, maxRuleSVGBytes)
		}
		return nil, fmt.Errorf("doc: pdftocairo failed on page %d: %w: %s",
			page, err, redact(strings.TrimSpace(errOut.String()), path))
	}

	rules, err := parseRules(out.buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("doc: reading pdftocairo output for %s page %d: %w",
			redact(path, path), page, err)
	}
	return rules, nil
}

// PageTables reads one page's ruled lines and returns the tables they enclose.
//
// The page's text is taken as a parameter rather than extracted again: the text
// guard needs it, [ExtractRuns] already produces it, and the probe has already
// paid for that call. Passing nil skips the text guard, which is what the
// discrimination measurements in docs/design/conversion.md were taken with —
// and what the numbers there show is not safe in production.
func PageTables(ctx context.Context, path string, page *PageRuns) ([]RuledTable, error) {
	if page == nil {
		return nil, errors.New("doc: PageTables needs a page to read")
	}
	rules, err := ExtractRules(ctx, path, page.No)
	if err != nil {
		return nil, err
	}
	return FindRuledTables(rules, page), nil
}

// FindRuledTables groups rules into tables and returns those that pass both
// guards.
//
// Both are needed and neither is sufficient, which is measured rather than
// argued. See [minTableCells] for the shape guard and [tableHasText] for the
// text guard.
//
// page supplies the text the second guard reads, and the page box the run filter
// needs. A nil page applies the shape guard alone.
func FindRuledTables(rules []Rule, page *PageRuns) []RuledTable {
	var text []TextRun
	if page != nil {
		// The same filter [DetectColumns] uses, and for a reason that bites here
		// specifically: the columns fixture's illustrations are placed PDFs that
		// each brought an InDesign filename slug along, scaled down with the
		// artwork. Those slugs sit inside the illustration frames — which are
		// exactly the shapes the text guard exists to reject — so counting them as
		// text would let three pages of framed pictures through as tables.
		text = usableRuns(page.Runs, page.Width, page.Height, &DroppedRuns{})
	}

	var out []RuledTable
	for _, group := range rejoinTableFragments(ruleComponents(rules)) {
		table := cellsOfTable(group)
		if table == nil {
			continue
		}
		countCellText(table, text)
		// Both guards judge the table by its legible cells, and only those. A
		// sliver cell is evidence about the drawing, not about whether this is a
		// table, and letting a dozen of them outvote four real cells is how a
		// diagram of callout boxes gets called a table.
		legible := legibleCells(table)
		if !hasTableShape(legible) {
			continue
		}
		if page != nil && !tableHasText(legible) {
			continue
		}
		out = append(out, *table)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Box.Y0 != out[j].Box.Y0 {
			return out[i].Box.Y0 < out[j].Box.Y0
		}
		return out[i].Box.X0 < out[j].Box.X0
	})
	return out
}

// tableHasText reports whether enough of a table's cells hold words.
//
// This guard is not optional and the measurement is the whole argument for it.
// After the shape guard, 13 pages of the columns fixture look like tables, and
// three of them — 22, 38 and 44 — are grids of framed illustrations. They are
// ruled by exactly the evidence a table is ruled by: a border round each
// picture, aligned in rows and columns, because that is what a figure grid is.
// No amount of geometry separates them, and none should be expected to.
//
// What separates them is whether the cells hold words. Measured: 14 of those
// three pages' 15 cells contain zero characters, while every cell of page 57's
// twelve-cell table contains some, the smallest 27 runes. With this guard the
// columns fixture yields 10 table pages and the sequential one 170 — and 170 is
// 34 languages times 5 table pages, exactly, which is the strongest evidence
// available that the guard is not simply discarding awkward pages.
//
// Half is a deliberately loose threshold. A real table often has an empty cell —
// a blank corner above a row-label column, an unanswered row — and page 15 of
// the sequential fixture has three. What it never has is almost all of them
// empty.
//
// The failure this leaves is recorded in docs/design/conversion.md: a figure
// grid whose captions sit inside the frames would pass, and nothing here would
// catch it.
func tableHasText(legible []RuledCell) bool {
	var withText int
	for i := range legible {
		if legible[i].Chars > 0 {
			withText++
		}
	}
	return float64(withText) >= minCellsWithText*float64(len(legible))
}

// legibleCells are the cells big enough to be evidence about whether this is a
// table. See [minLegibleCellWidth].
func legibleCells(t *RuledTable) []RuledCell {
	out := make([]RuledCell, 0, len(t.Cells))
	for i := range t.Cells {
		if t.Cells[i].Rect.Width() >= minLegibleCellWidth &&
			t.Cells[i].Rect.Height() >= minLegibleCellHeight {
			out = append(out, t.Cells[i])
		}
	}
	return out
}

// hasTableShape is the shape guard: enough legible cells, spread over at least
// two rows and two columns. See [minTableCells] for why it is needed and
// [tableHasText] for why it is not enough.
func hasTableShape(legible []RuledCell) bool {
	rows, cols := make(map[int]bool), make(map[int]bool)
	for i := range legible {
		rows[legible[i].Row] = true
		cols[legible[i].Col] = true
	}
	return len(legible) >= minTableCells &&
		len(rows) >= minTableRows && len(cols) >= minTableCols
}

// countCellText fills in each cell's Chars from the page's text.
//
// A run belongs to the cell that contains it whole, within [cellTextMargin]. A
// run straddling a border belongs to neither, which is the honest reading: it is
// either a heading printed over the table or evidence that the cell rectangle is
// wrong, and counting it would hide both.
func countCellText(t *RuledTable, text []TextRun) {
	for i := range t.Cells {
		c := &t.Cells[i]
		c.Chars = 0
		for j := range text {
			r := &text[j]
			if r.X >= c.Rect.X0-cellTextMargin && r.right() <= c.Rect.X1+cellTextMargin &&
				r.Y >= c.Rect.Y0-cellTextMargin && r.bottom() <= c.Rect.Y1+cellTextMargin {
				c.Chars += len([]rune(r.Text))
			}
		}
	}
}

// ruleSpan is a stretch of one printed line, in the line's own direction. It
// is not columns.go's span, which counts integer projection buckets.
type ruleSpan struct{ lo, hi float64 }

// ruleComponents groups a page's rules into candidate tables.
//
// One grid per page is wrong, and page 57 is why. It carries two independent
// tables at different row positions, plus a heading underline and footer crop
// marks. Building one grid from every rule on the page fragments the real
// tables: the underline beneath the left table's title ends at x=128, which
// becomes a column boundary and splits the first column of a table it is not
// part of.
//
// So rules are grouped first, two ways. A vertical rule joins every horizontal
// it crosses, which is what a grid is. And two collinear rules join when they
// touch, because one printed line arrives in segments — but only when they
// touch: page 57's two side-by-side tables both put a row rule at y=103.5, and
// joining those on collinearity alone welds the two tables into one grid. They
// are 22 units apart while the segments of one rule meet exactly, so the gap
// decides. See [maxSegmentGap].
func ruleComponents(rules []Rule) [][]Rule {
	parent := make([]int, len(rules))
	for i := range parent {
		parent[i] = i
	}
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(i, j int) { parent[find(i)] = find(j) }

	var hs, vs []int
	for i := range rules {
		if rules[i].Dir == Vertical {
			vs = append(vs, i)
		} else {
			hs = append(hs, i)
		}
	}
	for _, i := range hs {
		for _, j := range vs {
			if rulesCross(&rules[i], &rules[j]) {
				union(i, j)
			}
		}
	}
	for _, group := range [][]int{hs, vs} {
		for a := range group {
			for b := a + 1; b < len(group); b++ {
				ra, rb := &rules[group[a]], &rules[group[b]]
				if math.Abs(ra.At-rb.At) > snapTolerance {
					continue
				}
				if math.Min(ra.End, rb.End)-math.Max(ra.Start, rb.Start) >= -maxSegmentGap {
					union(group[a], group[b])
				}
			}
		}
	}

	byRoot := make(map[int][]Rule)
	var roots []int
	for i := range rules {
		r := find(i)
		if _, seen := byRoot[r]; !seen {
			roots = append(roots, r)
		}
		byRoot[r] = append(byRoot[r], rules[i])
	}
	out := make([][]Rule, 0, len(roots))
	for _, r := range roots {
		out = append(out, byRoot[r])
	}
	return out
}

// rulesCross reports whether a horizontal and a vertical rule meet.
func rulesCross(h, v *Rule) bool {
	return h.Start-snapTolerance <= v.At && v.At <= h.End+snapTolerance &&
		v.Start-snapTolerance <= h.At && h.At <= v.End+snapTolerance
}

// rejoinTableFragments merges the stacked fragments of one table.
//
// A row that spans every column interrupts the column rule, so the rules alone
// see a table with section headings as several stacked grids — page 57's left
// table breaks into three. Fragments of one table share their horizontal extent
// exactly, which is what identifies them; a different table on the same page
// sits at a different x, and page 57's second table is 420 units to the right.
func rejoinTableFragments(groups [][]Rule) [][]Rule {
	var out [][]Rule
	for _, g := range groups {
		lo, hi, ok := horizontalExtent(g)
		if !ok {
			out = append(out, g)
			continue
		}
		var merged bool
		for i := range out {
			olo, ohi, ook := horizontalExtent(out[i])
			if ook && math.Abs(lo-olo) <= snapTolerance && math.Abs(hi-ohi) <= snapTolerance {
				out[i] = append(out[i], g...)
				merged = true
				break
			}
		}
		if !merged {
			out = append(out, g)
		}
	}
	return out
}

// horizontalExtent is the x-range the group's horizontal rules span.
func horizontalExtent(rules []Rule) (lo, hi float64, ok bool) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for i := range rules {
		if rules[i].Dir != Horizontal {
			continue
		}
		lo, hi = math.Min(lo, rules[i].Start), math.Max(hi, rules[i].End)
		ok = true
	}
	return lo, hi, ok
}

// gridLines are the distinct printed lines of one candidate table: a clustered
// position, and the union of the segments drawn along it.
type gridLines struct {
	at    []float64
	spans [][]ruleSpan
}

// linesOf clusters a group's rules into printed lines in one direction.
func linesOf(rules []Rule, dir RuleDirection) gridLines {
	var positions []float64
	for i := range rules {
		if rules[i].Dir == dir {
			positions = append(positions, rules[i].At)
		}
	}
	out := gridLines{at: clusterPositions(positions)}
	out.spans = make([][]ruleSpan, len(out.at))
	for i := range rules {
		if rules[i].Dir != dir {
			continue
		}
		k := nearestIndex(out.at, rules[i].At)
		out.spans[k] = append(out.spans[k], ruleSpan{rules[i].Start, rules[i].End})
	}
	for i := range out.spans {
		out.spans[i] = mergeSpans(out.spans[i])
	}
	return out
}

// lookup returns the segments of the printed line nearest to at, or nil when
// none is within [snapTolerance].
func (g *gridLines) lookup(at float64) []ruleSpan {
	if len(g.at) == 0 {
		return nil
	}
	k := nearestIndex(g.at, at)
	if math.Abs(g.at[k]-at) > snapTolerance {
		return nil
	}
	return g.spans[k]
}

// clusterPositions groups coordinates that are the same printed line into one,
// returned sorted. Clustering is chained deliberately — a rule 2 units from its
// neighbour and 4 from the next is one line with the first, not a line of its
// own — which is what handles a rule drawn twice for a blend.
func clusterPositions(vals []float64) []float64 {
	if len(vals) == 0 {
		return nil
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)

	var out []float64
	group := []float64{sorted[0]}
	flush := func() {
		var sum float64
		for _, v := range group {
			sum += v
		}
		out = append(out, sum/float64(len(group)))
	}
	for _, v := range sorted[1:] {
		if v-group[len(group)-1] <= snapTolerance {
			group = append(group, v)
			continue
		}
		flush()
		group = []float64{v}
	}
	flush()
	return out
}

func nearestIndex(at []float64, v float64) int {
	best := 0
	for i := 1; i < len(at); i++ {
		if math.Abs(at[i]-v) < math.Abs(at[best]-v) {
			best = i
		}
	}
	return best
}

// mergeSpans unions the touching or overlapping segments of one printed line.
//
// This is what stops a joint being read as a terminus. A row rule crossing a
// column divider arrives as two segments meeting at it — 29.7 to 173.3 and 173.3
// to 428.1 on page 57 — and unmerged, 173.3 looks like a place where the rule
// stops, so the full-width section row above it is wrongly cut in two.
func mergeSpans(spans []ruleSpan) []ruleSpan {
	if len(spans) == 0 {
		return nil
	}
	sorted := make([]ruleSpan, len(spans))
	copy(sorted, spans)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].lo < sorted[j].lo })

	out := []ruleSpan{sorted[0]}
	for _, s := range sorted[1:] {
		last := &out[len(out)-1]
		if s.lo-last.hi <= maxSegmentGap {
			last.hi = math.Max(last.hi, s.hi)
			continue
		}
		out = append(out, s)
	}
	return out
}

// coveredFraction is how much of [lo,hi] the union of spans covers.
func coveredFraction(spans []ruleSpan, lo, hi float64) float64 {
	if hi <= lo {
		return 0
	}
	var total, cur float64
	cur = lo
	clipped := make([]ruleSpan, 0, len(spans))
	for _, s := range spans {
		if s.hi > lo && s.lo < hi {
			clipped = append(clipped, ruleSpan{math.Max(s.lo, lo), math.Min(s.hi, hi)})
		}
	}
	sort.Slice(clipped, func(i, j int) bool { return clipped[i].lo < clipped[j].lo })
	for _, s := range clipped {
		if s.hi <= cur {
			continue
		}
		total += s.hi - math.Max(s.lo, cur)
		cur = math.Max(cur, s.hi)
	}
	return total / (hi - lo)
}

// cellsOfTable turns one group of rules into a table, or nil if it is not a grid.
//
// Cells are walked a row at a time rather than tested rectangle by rectangle
// over the whole grid, and that is what expresses a spanning cell: page 57's
// section rows interrupt the column rule, so between them the full width is one
// cell rather than two empty ones. A grid-rectangle walk would report the pair.
func cellsOfTable(rules []Rule) *RuledTable {
	rows := linesOf(rules, Horizontal)
	cols := linesOf(rules, Vertical)
	if len(rows.at) < 2 {
		return nil
	}
	top, bottom := rows.at[0], rows.at[len(rows.at)-1]

	// Column boundaries nobody drew: an x where several row rules stop. See
	// [minSharedTermini], and [Rule] for why a table may have no outer verticals
	// at all — the columns fixture's tables draw none.
	termini := make(map[float64]map[int]bool)
	for i, spans := range rows.spans {
		for _, s := range spans {
			for _, e := range []float64{s.lo, s.hi} {
				k := math.Round(e*10) / 10
				if termini[k] == nil {
					termini[k] = make(map[int]bool)
				}
				termini[k][i] = true
			}
		}
	}
	var candidates []float64
	for k, atLines := range termini {
		if len(atLines) >= minSharedTermini {
			candidates = append(candidates, k)
		}
	}

	// A drawn vertical that does not run alongside the rows is furniture rather
	// than a column boundary. The columns fixture's footer crop marks sit at
	// x=29.8 and 188.2 spanning y=821-828, two units below its last table rule at
	// 819.7 — close enough to have been joined to the table, and enough to cut
	// its wide right-hand column in two.
	for i, at := range cols.at {
		if coveredFraction(cols.spans[i], top, bottom)*(bottom-top) >= minCellSize {
			candidates = append(candidates, at)
		}
	}
	xs := clusterPositions(candidates)
	if len(xs) < 2 {
		return nil
	}

	out := &RuledTable{Cols: len(xs) - 1, Rules: rules}
	box := CellRect{X0: math.Inf(1), Y0: math.Inf(1), X1: math.Inf(-1), Y1: math.Inf(-1)}
	for j := 0; j+1 < len(rows.at); j++ {
		y0, y1 := rows.at[j], rows.at[j+1]
		if y1-y0 < minCellSize {
			continue
		}
		topSpans, botSpans := rows.spans[j], rows.spans[j+1]

		edges := make(map[int]bool)
		for i, at := range xs {
			if coveredFraction(cols.lookup(at), y0, y1) >= minSideCoverage {
				edges[i] = true
				continue
			}
			// Or the row rules above and below both stop here, which is the only
			// evidence an undrawn outer border leaves.
			if spansEndNear(topSpans, at) && spansEndNear(botSpans, at) {
				edges[i] = true
			}
		}
		ordered := make([]int, 0, len(edges))
		for i := range edges {
			ordered = append(ordered, i)
		}
		sort.Ints(ordered)

		var rowCells []RuledCell
		for k := 0; k+1 < len(ordered); k++ {
			a, b := ordered[k], ordered[k+1]
			x0, x1 := xs[a], xs[b]
			if x1-x0 < minCellSize {
				continue
			}
			if coveredFraction(topSpans, x0, x1) < minSideCoverage ||
				coveredFraction(botSpans, x0, x1) < minSideCoverage {
				continue
			}
			rowCells = append(rowCells, RuledCell{
				Row: out.Rows, Col: a, ColSpan: b - a,
				Rect: CellRect{X0: x0, Y0: y0, X1: x1, Y1: y1},
			})
		}
		if len(rowCells) == 0 {
			// A band that encloses nothing is not a row. Numbering it would leave
			// gaps in Row and make a table of three rows report five.
			continue
		}
		out.Rows++
		out.Cells = append(out.Cells, rowCells...)
		box.X0, box.Y0 = math.Min(box.X0, rowCells[0].Rect.X0), math.Min(box.Y0, y0)
		box.X1 = math.Max(box.X1, rowCells[len(rowCells)-1].Rect.X1)
		box.Y1 = math.Max(box.Y1, y1)
	}
	if len(out.Cells) == 0 {
		return nil
	}
	out.Box = box
	return out
}

// spansEndNear reports whether any of the line's segments terminates at at.
func spansEndNear(spans []ruleSpan, at float64) bool {
	for _, s := range spans {
		if math.Abs(s.lo-at) <= snapTolerance || math.Abs(s.hi-at) <= snapTolerance {
			return true
		}
	}
	return false
}

// --- reading cairo's SVG ---------------------------------------------------

// matrix is an SVG transform: a b c d e f, applied as x' = ax + cy + e.
type matrix [6]float64

var identity = matrix{1, 0, 0, 1, 0, 0}

// compose returns m then n, i.e. the matrix that applies n's mapping inside m's.
func (m matrix) compose(n matrix) matrix {
	return matrix{
		m[0]*n[0] + m[2]*n[1], m[1]*n[0] + m[3]*n[1],
		m[0]*n[2] + m[2]*n[3], m[1]*n[2] + m[3]*n[3],
		m[0]*n[4] + m[2]*n[5] + m[4], m[1]*n[4] + m[3]*n[5] + m[5],
	}
}

func (m matrix) apply(x, y float64) (px, py float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

// scale is the mean of the matrix's two axis scale factors, for turning a
// stroke width in user units into one on the page.
func (m matrix) scale() float64 {
	return (math.Hypot(m[0], m[1]) + math.Hypot(m[2], m[3])) / 2
}

// svgNode is the part of an SVG element this code reads. Attributes it does not
// read — colour, opacity, stroke-linecap — are dropped at parse time rather than
// carried, because a 30 MB page makes the difference measurable.
type svgNode struct {
	tag         string
	id          string
	transform   string
	filter      string
	href        string
	d           string
	stroke      string
	fill        string
	strokeWidth string
	// clip is the element's own `clip-path` attribute, unresolved. It is kept
	// because a shape's box has to be its visible extent rather than its
	// geometric one — see clip.go, which is where the reference is followed.
	clip       string
	x, y, w, h float64
	kids       []*svgNode
}

// skippedTags are elements whose contents are never page ink: masking and
// gradient machinery, embedded rasters, stylesheets. <filter> is deliberately
// not here — it is not walked for ink either, but its feImage references are how
// a hoisted compositing group is found again.
//
// <clipPath> is not here either, and used to be. Its contents are not ink, but
// they are geometry a shape's box depends on, so it is read by [readClipPath]
// into a rectangle instead of being skipped — see clip.go.
var skippedTags = map[string]bool{
	"mask": true, "linearGradient": true, "radialGradient": true,
	"pattern": true, "symbol": true, "image": true, "style": true,
}

// svgDoc is a parsed page: the element tree, and the two indexes needed to
// follow a reference into <defs>.
type svgDoc struct {
	root *svgNode
	// byID resolves an href or a filter reference. First declaration wins, which
	// matches how a browser resolves a duplicate id.
	byID map[string]*svgNode
	// filterRefs maps a filter's id to the ids its feImage children pull in.
	filterRefs map[string][]string
	// clips maps a <clipPath>'s id to the extent it admits, in its own user space.
	// Kept unresolved for the reason clip.go's header gives: the same definition
	// resolves to two different page rectangles depending on which reference
	// pulled it in.
	clips map[string]clipDef
}

// parseSVG reads cairo's SVG into a tree.
//
// Go's decoder never resolves an external entity, which is the obvious worry
// about parsing XML derived from an untrusted PDF and is worth stating rather
// than leaving to be rediscovered: there is no entity expansion to disable here,
// and the DOCTYPE cairo emits is inert. What is not free is size, which is why
// the caller caps the bytes — see [maxRuleSVGBytes].
//
// Glyph outlines are dropped as the tree is built rather than filtered later.
// Cairo writes every glyph of the page as a filled path under
// `<g id="glyph-N-M">`, and on a page of text those are almost all of the
// document: skipping the subtree is both the correct exclusion — see [Rule] for
// why it must be structural — and what keeps a 30 MB page from becoming a tree
// of a million nodes.
func parseSVG(data []byte) (*svgDoc, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	doc := &svgDoc{
		root:       &svgNode{tag: "#document"},
		byID:       make(map[string]*svgNode),
		filterRefs: make(map[string][]string),
		clips:      make(map[string]clipDef),
	}
	stack := []*svgNode{doc.root}

	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		switch v := tok.(type) {
		case xml.StartElement:
			// A <clipPath> becomes a rectangle rather than a subtree, and is
			// consumed here so its children never reach the tree at all.
			if v.Name.Local == "clipPath" {
				id := attrValue(&v, "id")
				def, err := readClipPath(dec, &v)
				if err != nil {
					return nil, err
				}
				// First declaration wins, matching how byID resolves a duplicate.
				if _, seen := doc.clips[id]; !seen && id != "" {
					doc.clips[id] = def
				}
				continue
			}
			node := &svgNode{tag: v.Name.Local}
			for _, a := range v.Attr {
				switch a.Name.Local {
				case "id":
					node.id = a.Value
				case "transform":
					node.transform = a.Value
				case "filter":
					node.filter = a.Value
				case "clip-path":
					node.clip = a.Value
				case "href":
					// Both the xlink form and the plain one; cairo writes xlink:href,
					// but the plain attribute is the current spelling and costs nothing
					// to accept.
					node.href = a.Value
				case "d":
					node.d = a.Value
				case "stroke":
					node.stroke = a.Value
				case "fill":
					node.fill = a.Value
				case "stroke-width":
					node.strokeWidth = a.Value
				case "x":
					node.x = parseFloat(a.Value)
				case "y":
					node.y = parseFloat(a.Value)
				case "width":
					node.w = parseFloat(a.Value)
				case "height":
					node.h = parseFloat(a.Value)
				}
			}
			if skippedTags[node.tag] || strings.HasPrefix(node.id, "glyph-") {
				if err := dec.Skip(); err != nil {
					return nil, err
				}
				continue
			}
			parent := stack[len(stack)-1]
			parent.kids = append(parent.kids, node)
			if node.id != "" {
				if _, seen := doc.byID[node.id]; !seen {
					doc.byID[node.id] = node
				}
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		}
	}

	for _, f := range collectByTag(doc.root, "filter") {
		var refs []string
		for _, fe := range collectAll(f) {
			if strings.HasPrefix(fe.href, "#") {
				refs = append(refs, fe.href[1:])
			}
		}
		doc.filterRefs[f.id] = refs
	}
	return doc, nil
}

// attrValue reads one attribute off an element that is being consumed from the
// stream rather than built into a node.
func attrValue(e *xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func collectByTag(n *svgNode, tag string) []*svgNode {
	var out []*svgNode
	if n.tag == tag {
		out = append(out, n)
	}
	for _, k := range n.kids {
		out = append(out, collectByTag(k, tag)...)
	}
	return out
}

func collectAll(n *svgNode) []*svgNode {
	out := []*svgNode{n}
	for _, k := range n.kids {
		out = append(out, collectAll(k)...)
	}
	return out
}

// visitKey identifies one visit to a node under one matrix. The same node is
// legitimately drawn twice under different transforms, and must be collected
// both times; visiting it twice under the same one is a reference cycle.
type visitKey struct {
	node *svgNode
	m    [6]int64
}

// ruleWalker accumulates rules while walking the tree.
type ruleWalker struct {
	doc     *svgDoc
	rules   []Rule
	visited map[visitKey]bool
}

// parseRules reads every axis-aligned rule a page draws.
func parseRules(data []byte) ([]Rule, error) {
	doc, err := parseSVG(data)
	if err != nil {
		return nil, err
	}
	w := &ruleWalker{doc: doc, visited: make(map[visitKey]bool)}
	// Only the body is walked from the top. <defs> is entered exclusively through
	// the reference that pulls a definition back in, because that reference
	// carries the matrix which cancels the definition's own — see the compositing
	// group trap in this file's header.
	for _, kid := range doc.root.kids {
		w.walkBody(kid, identity, clipBox{}, 0)
	}
	return dedupeRules(w.rules), nil
}

// walkBody walks a subtree, skipping <defs>, which walk enters by reference.
func (w *ruleWalker) walkBody(n *svgNode, m matrix, clip clipBox, depth int) {
	if n.tag == "defs" {
		return
	}
	w.walk(n, m, clip, depth)
}

func (w *ruleWalker) walk(n *svgNode, m matrix, clip clipBox, depth int) {
	if depth > maxSVGDepth || strings.HasPrefix(n.id, "glyph-") {
		return
	}
	key := visitKey{node: n}
	for i, v := range m {
		key.m[i] = int64(math.Round(v * 1e4))
	}
	if w.visited[key] {
		return
	}
	w.visited[key] = true

	m = m.compose(parseTransform(n.transform))
	// The element's own clip narrows its ancestors' — resolved after its transform,
	// which is the user space the clip is written in, and the same composition that
	// keeps a hoisted compositing group's coordinates right.
	if box, ok := w.doc.clipAt(n.clip, m); ok {
		clip = clip.intersect(box)
		if clip.empty() {
			return
		}
	}

	// A filtered group's real content was hoisted into <defs>; follow it carrying
	// this element's matrix, which is what cancels the hoisted group's own.
	if id, ok := refID(n.filter); ok {
		for _, ref := range w.doc.filterRefs[id] {
			if target := w.doc.byID[ref]; target != nil {
				w.walk(target, m, clip, depth+1)
			}
		}
	}
	if n.tag == "use" {
		if strings.HasPrefix(n.href, "#") {
			if target := w.doc.byID[n.href[1:]]; target != nil {
				w.walk(target, m, clip, depth+1)
			}
		}
	}

	switch {
	case n.tag == "path" && n.stroke != "" && n.stroke != "none":
		width := 1.0
		if n.strokeWidth != "" {
			width = parseFloat(n.strokeWidth)
		}
		width *= m.scale() * svgPointScale
		for _, sub := range subpaths(n.d) {
			for i := 0; i+1 < len(sub); i++ {
				w.stroked(m, clip, sub[i], sub[i+1], width)
			}
		}
	case n.tag == "path" && n.fill != "" && n.fill != "none":
		for _, sub := range subpaths(n.d) {
			w.filled(m, clip, sub)
		}
	case n.tag == "rect" && n.fill != "" && n.fill != "none":
		w.filled(m, clip, []point{
			{n.x, n.y}, {n.x + n.w, n.y}, {n.x + n.w, n.y + n.h}, {n.x, n.y + n.h},
		})
	}

	for _, kid := range n.kids {
		w.walkBody(kid, m, clip, depth+1)
	}
}

// stroked records a stroked segment if it is an axis-aligned rule.
//
// The clip is applied to the segment's own extent, so a rule drawn longer than
// the window it is painted in is recorded at the length the page prints. It is
// applied here rather than only in [inkWalker] because a rule that is clipped
// away is not a printed line, and a cell boundary is read off where the rules
// end — see [cellsOfTable]. Measured on the five ground-truth pages, every cell
// count is unchanged, which says the tables of these two documents draw their
// rules inside their clips.
func (w *ruleWalker) stroked(m matrix, clip clipBox, p, q point, width float64) {
	x0, y0 := m.apply(p.x, p.y)
	x1, y1 := m.apply(q.x, q.y)
	x0, y0, x1, y1 = x0*svgPointScale, y0*svgPointScale, x1*svgPointScale, y1*svgPointScale
	box, visible := clip.apply(CellRect{
		X0: math.Min(x0, x1), Y0: math.Min(y0, y1),
		X1: math.Max(x0, x1), Y1: math.Max(y0, y1),
	})
	if !visible {
		return
	}
	dx, dy := math.Abs(x1-x0), math.Abs(y1-y0)
	switch {
	case dy <= axisTolerance && box.Width() >= minRuleLength:
		w.rules = append(w.rules, Rule{
			Dir: Horizontal, At: (y0 + y1) / 2,
			Start: box.X0, End: box.X1, Thickness: width,
		})
	case dx <= axisTolerance && box.Height() >= minRuleLength:
		w.rules = append(w.rules, Rule{
			Dir: Vertical, At: (x0 + x1) / 2,
			Start: box.Y0, End: box.Y1, Thickness: width,
		})
	}
}

// filled records a filled subpath if it is a thin sliver, which is one of the
// two ways a rule is drawn. Subpaths of more than six points are not slivers;
// bounding a curve's flattened outline would call a filled logo a rule.
//
// How much this is worth was measured by removing it, and the answer is less
// than the volume suggests. 800 of the columns manual's 4,360 rules are filled
// slivers and 103 of the sequential manual's 16,959, so 18% of one document's
// rules come from here. But with this branch disabled, both documents return the
// same tables: 13 pages passing the shape guard and 10 passing both in one, 171
// and 170 in the other, and all five ground-truth pages come back with their
// exact cell counts. The only figure that moves is how many pages draw a rule at
// all, 226 down to 195 in the sequential manual.
//
// So no table in either fixture depends on a filled rule. It is kept because the
// shape is real, cheap to read and documented in poppler's output rather than
// guessed — a document that rules its tables with filled slivers instead of
// strokes is an ordinary thing for a designer to produce, and the next manual
// gets no say in which of the two this code understands.
func (w *ruleWalker) filled(m matrix, clip clipBox, sub []point) {
	if len(sub) > 6 || len(sub) < 2 {
		return
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, p := range sub {
		x, y := m.apply(p.x, p.y)
		x, y = x*svgPointScale, y*svgPointScale
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	box, visible := clip.apply(CellRect{X0: minX, Y0: minY, X1: maxX, Y1: maxY})
	if !visible {
		return
	}
	minX, minY, maxX, maxY = box.X0, box.Y0, box.X1, box.Y1
	width, height := maxX-minX, maxY-minY
	switch {
	case width >= minRuleLength && height > 0 && height <= maxRuleThickness:
		w.rules = append(w.rules, Rule{
			Dir: Horizontal, At: (minY + maxY) / 2,
			Start: minX, End: maxX, Thickness: height, Filled: true,
		})
	case height >= minRuleLength && width > 0 && width <= maxRuleThickness:
		w.rules = append(w.rules, Rule{
			Dir: Vertical, At: (minX + maxX) / 2,
			Start: minY, End: maxY, Thickness: width, Filled: true,
		})
	}
}

// dedupeRules collapses one printed line drawn several times, keeping the
// thickest. A table drawn in a blend group is drawn twice by construction — once
// as the hoisted definition and once at the use site — so this is not a tidying
// pass but part of reading that output correctly.
func dedupeRules(rules []Rule) []Rule {
	type key struct {
		dir            RuleDirection
		at, start, end int64
	}
	best := make(map[key]int, len(rules))
	var order []key
	for i := range rules {
		r := &rules[i]
		k := key{r.Dir, int64(math.Round(r.At * 2)),
			int64(math.Round(r.Start * 2)), int64(math.Round(r.End * 2))}
		if j, seen := best[k]; seen {
			if r.Thickness > rules[j].Thickness {
				best[k] = i
			}
			continue
		}
		best[k] = i
		order = append(order, k)
	}
	out := make([]Rule, 0, len(order))
	for _, k := range order {
		out = append(out, rules[best[k]])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir < out[j].Dir
		}
		if out[i].At != out[j].At {
			return out[i].At < out[j].At
		}
		return out[i].Start < out[j].Start
	})
	return out
}

// point is a coordinate in an SVG element's own user space.
type point struct{ x, y float64 }

// parseTransform reads an SVG transform list. The functions compose left to
// right, which is the order they are written in.
func parseTransform(t string) matrix {
	m := identity
	for i := 0; i < len(t); {
		// The function name runs to the opening parenthesis.
		for i < len(t) && (t[i] == ' ' || t[i] == ',' || t[i] == '\t' || t[i] == '\n') {
			i++
		}
		start := i
		for i < len(t) && t[i] != '(' {
			i++
		}
		if i >= len(t) {
			break
		}
		name := strings.TrimSpace(t[start:i])
		i++
		argStart := i
		for i < len(t) && t[i] != ')' {
			i++
		}
		args := scanFloats(t[argStart:min(i, len(t))])
		if i < len(t) {
			i++
		}
		m = m.compose(transformMatrix(name, args))
	}
	return m
}

func transformMatrix(name string, a []float64) matrix {
	at := func(i int) float64 {
		if i < len(a) {
			return a[i]
		}
		return 0
	}
	switch name {
	case "matrix":
		if len(a) < 6 {
			return identity
		}
		return matrix{a[0], a[1], a[2], a[3], a[4], a[5]}
	case "translate":
		return matrix{1, 0, 0, 1, at(0), at(1)}
	case "scale":
		sx := at(0)
		sy := sx
		if len(a) > 1 {
			sy = a[1]
		}
		return matrix{sx, 0, 0, sy, 0, 0}
	case "rotate":
		r := at(0) * math.Pi / 180
		// Only the two-argument form's centre is ignored, and cairo never writes
		// it; a rotation about a point would shift a rule, so it is safer to
		// compose the pure rotation than to guess.
		return matrix{math.Cos(r), math.Sin(r), -math.Sin(r), math.Cos(r), 0, 0}
	default:
		return identity
	}
}

// refID reads the id out of a url(#id) attribute value.
func refID(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "url(#") || !strings.HasSuffix(v, ")") {
		return "", false
	}
	return v[len("url(#") : len(v)-1], true
}

// subpaths splits a path's d attribute into subpaths of points, with every
// curve flattened to its endpoint.
//
// Flattening is right rather than lazy here: a rule is a straight line, so a
// curve's interior cannot be part of one, and its endpoints are exactly what
// closes the rounded corner of a table border. What it costs is that a filled
// shape's bounding box is computed from endpoints alone, which is why
// [ruleWalker.filled] refuses subpaths of more than six points.
func subpaths(d string) [][]point {
	toks := tokenizePath(d)
	var out [][]point
	var cur []point
	var pt, start point
	cmd := byte('M')

	for i := 0; i < len(toks); {
		if toks[i].isCmd {
			cmd = toks[i].cmd
			i++
			if cmd == 'Z' || cmd == 'z' {
				if len(cur) > 0 {
					cur = append(cur, start)
					out = append(out, cur)
					cur = nil
				}
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
			case 'V':
				if rel {
					pt = point{pt.x, pt.y + a[0]}
				} else {
					pt = point{pt.x, a[0]}
				}
			default:
				nx, ny := a[k-2], a[k-1]
				if rel {
					pt = point{pt.x + nx, pt.y + ny}
				} else {
					pt = point{nx, ny}
				}
			}
			// Only the first pair of a moveto starts a subpath. The rest are
			// implicit linetos, which is the SVG rule and not a shortcut: cairo
			// writes a table border as one M followed by several coordinate pairs.
			if upper == 'M' && j == 0 {
				if len(cur) > 0 {
					out = append(out, cur)
				}
				cur = []point{pt}
				start = pt
				continue
			}
			cur = append(cur, pt)
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func commandArity(upper byte) int {
	switch upper {
	case 'C':
		return 6
	case 'A':
		return 7
	case 'S', 'Q':
		return 4
	case 'H', 'V':
		return 1
	default:
		return 2
	}
}

// pathToken is either a command letter or a number.
type pathToken struct {
	cmd   byte
	num   float64
	isCmd bool
}

// tokenizePath scans a d attribute. Hand-written rather than a regexp because
// path data is nearly all of a 30 MB page, and this is the inner loop over it.
func tokenizePath(d string) []pathToken {
	out := make([]pathToken, 0, len(d)/6)
	for i := 0; i < len(d); {
		c := d[i]
		switch {
		case c == ' ' || c == ',' || c == '\t' || c == '\n' || c == '\r':
			i++
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z'):
			out = append(out, pathToken{cmd: c, isCmd: true})
			i++
		default:
			v, n := scanFloat(d[i:])
			if n == 0 {
				// Not a number and not a letter: skip it rather than stall.
				i++
				continue
			}
			out = append(out, pathToken{num: v})
			i += n
		}
	}
	return out
}

// scanFloat reads one number from the front of s. used is how many bytes it
// consumed, and 0 means there was no number there.
func scanFloat(s string) (value float64, used int) {
	i := 0
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		i++
	}
	digits := false
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i, digits = i+1, true
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i, digits = i+1, true
		}
	}
	if !digits {
		return 0, 0
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '-' || s[j] == '+') {
			j++
		}
		expDigits := j
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j > expDigits {
			i = j
		}
	}
	v, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0, 0
	}
	return v, i
}

// scanFloats reads every number in s, ignoring whatever separates them.
func scanFloats(s string) []float64 {
	var out []float64
	for i := 0; i < len(s); {
		v, n := scanFloat(s[i:])
		if n == 0 {
			i++
			continue
		}
		out = append(out, v)
		i += n
	}
	return out
}

// parseFloat reads an SVG length, ignoring a unit suffix. A malformed one reads
// as 0, which is what an absent attribute means too — a rect with no width draws
// nothing either way.
func parseFloat(s string) float64 {
	v, _ := scanFloat(strings.TrimSpace(s))
	return v
}
