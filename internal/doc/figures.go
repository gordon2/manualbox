package doc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/gordon2/manualbox/internal/extern"
)

// A picture is found from the shapes the page draws, and on both fixtures it is
// not an embedded image at all.
//
// That is the measurement this file exists on top of, and it inverts the obvious
// plan. `pdfimages` extracts embedded rasters, and over 628 pages of two real
// manuals it yields **not one illustration**:
//
//	                        embedded images   on pages   what they are
//	68-page columns manual        1,358         31 of 68  see below
//	560-page sequential manual       54          4 of 560 certification badges
//
// Of the columns manual's 1,358, exactly 1,301 are on pages 11 and 12 — 650 and
// 651 tiny 12x4-to-13x3 slivers in separation and indexed colour spaces, the mesh
// a gradient decomposes into. Of the remaining 57, one object (a 97x73 grey JPEG,
// the corner logo) accounts for 30, appearing once on each of pages 2-6, 15-51 odd
// and 52, 57, 62; the rest are the cover's five 88x52 icons, an RGB award badge and
// three CCITT wordmark stencils. The sequential manual's 54 sit only on pages 530,
// 531, 551 and 552 and run from 3x3 to 212x72 pixels: CE marks and recycling
// symbols. Neither set contains a diagram.
//
// Meanwhile the pictures a reader would name are all vector. Page 42 of the
// columns manual prints four framed line drawings of the appliance and reports
// **zero** embedded images; so do pages 18, 20, 22, 28, 34 and 44. `pdfimages -list`
// on page 57 reports one 97x73 grey JPEG, which is the corner logo and not that
// page's artwork — and `pdftohtml -xml` without -i agrees, emitting exactly one
// <image> there, at top=5 left=20, while pages 22 and 42 get none at all.
//
// So the geometry comes from the same place the ruled lines do — `pdftocairo -svg`,
// read with the walker rules.go already has — and what is collected is every drawn
// shape's bounding box rather than only the axis-aligned thin ones. The bytes then
// come from `pdftoppm`, rendering the rectangle that geometry found.
//
// Two consequences of taking the vector route are real and are not worked around:
//
// **An embedded raster is invisible here.** parseSVG drops <image> structurally,
// so a manual whose illustrations are photographs finds nothing. That is accepted
// rather than overlooked, on the measurement above: on the two documents this
// project has, every embedded raster is furniture and every illustration is drawn.
// A photographic manual needs the raster path added, and `pdfimages -list` plus
// pdftohtml's <image> elements are where its geometry would come from — noting
// that pdftohtml writes the raster to a file beside the blob store unless it is
// run with -i, which is why [ExtractRuns] passes -i.
//
// **A shape's box is its visible extent, because the clip is read.** This was the
// one real cost of the vector route and it is now paid: clip.go resolves each
// shape's effective clip and [inkWalker.add] intersects the path's extent with it,
// so a drawing whose artwork runs past its frame is recorded at the frame.
//
// What that was worth, measured end to end with `manualbox verify` on both
// manuals:
//
//	                              columns manual   sequential manual
//	figures                         46 -> 59          163 -> 168
//	figures cut off by their crop   22 -> 15           74 -> 71
//	figures with a blank band        4 -> 0             6 -> 2
//
// The figure count rises because the unclipped extent *merged neighbouring
// pictures*, and the pages that were counted by eye now agree with the print: page
// 42 of the columns manual returns its four framed drawings where it returned
// three, page 22 three for three, and page 16 four for four — which the render
// settles and conversion.md had wrong twice over, since that page prints four
// panels rather than the three it records. Both documents keep the same number of
// pages carrying figures, 27 and 23, which is what says these are splits rather
// than newly admitted furniture.
//
// The residual counts are not the clip. On the columns manual all 15 are
// [trimToPicture] cutting into a drawing that has a label at its edge — with
// trimming off the same measurement is 2 — and on the sequential manual they are
// leader lines on its crowded diagram pages, where a line more than half inside
// one figure's box belongs to the drawing beside it. See the note on
// [trimToPicture].
//
// Nothing here emits a block and nothing here writes to the blob store. This file
// answers only "where are the pictures, and what are their bytes"; the digest is
// carried because the store is content-addressed and that is where they go next.

// Bounds on what counts as a picture, all measured against the two fixtures in
// the 1.5-scaled space [ExtractRuns] documents.
//
// The two guards below are the same pair rules.go needs and for the same reason:
// a shape guard alone keeps page furniture, and a text guard alone keeps a ruled
// table. What is different is which shape signal works. Area does not: the
// smallest real illustration measured is 0.15% of its page (34x22 units, one of
// the thirty small diagrams on page 5 of the sequential manual) while the printed
// D and PL language badges the columns manual repeats on 110 pages are 0.5% and its
// cover ornament is 26.4%, so no area threshold separates them — a threshold that
// admits the diagram admits both. Ink volume does, by an order of magnitude: see
// [minFigureInk].
const (
	// figureDPI is what a figure is rendered at. Twice the 108 dpi the run
	// coordinates are in, chosen for that ratio and not for a picture quality
	// target: the crop rectangle poppler wants is in output pixels, so an exact
	// factor of two turns a rect in this package's space into a crop with no
	// rounding to argue about. Measured output: the columns manual's largest
	// figure, page 11's parts diagram, is 1077x1510 pixels and 353 KB of PNG.
	figureDPI = 216

	// figureScale is figureDPI over the 108 dpi of [PageRuns]. Not a tunable.
	figureScale = figureDPI / 108

	// minFigureWidth and minFigureHeight are how small a picture may be, and this
	// is a soft cut with no gap to put it in — the same shape of problem
	// docs/design/conversion.md records for a heading's share of the measure, and
	// it is recorded here rather than presented as a threshold.
	//
	// The sweep is in TestGuardSweep and says two things. On the columns manual the
	// guard discriminates *nothing whatever*: every value from 10 to 120 returns the
	// same 59 figures, because that document draws no picture smaller than 128 units
	// on its short side. On the sequential manual it is a smooth continuum with no
	// step anywhere — 293 figures at 10, 238 at 20, 201 at 30, 161 at 40, 127 at 50,
	// 93 at 60, 52 at 80, 15 at 120.
	//
	// So the value is chosen by looking at what falls out, and 40 was wrong. Page 5
	// of the sequential manual is a grid of nine panels holding about thirty small
	// diagrams, and at 40 sixteen of them are lost — three were rendered and looked
	// at: a 32x26 cutaway of the base station, a 32x32 wheel, a 34x22 robot under a
	// hand. All three are pictures a reader would want. 20 units is about one line of
	// body text on either document (17 units at the columns manual's 14pt, 17 at the
	// sequential's 11pt), which is the smallest thing that can be a picture rather
	// than a mark, and it is what the guard is actually for: a dashed rule is a
	// hundred small shapes and would otherwise pass [minFigureInk].
	//
	// The cost of going lower is not measured on these documents and is stated
	// rather than dismissed: at 10 the sequential manual gains 52 more clusters,
	// which were not inspected.
	minFigureWidth  = 20.0
	minFigureHeight = 20.0

	// minFigureInk is how many drawn shapes an area must contain to be a picture
	// rather than a piece of page furniture, and it is the guard that does the work.
	//
	// What it separates is not size but complexity, and the furniture measured here
	// is uniformly two or three shapes: the columns manual's printed D and PL
	// language badges are 3 each and appear on 110 pages, its cover wordmark is 1,
	// its three award badges 2, 3 and 5. The case that settles it is the sequential
	// manual's cover, where a single grey decorative swash covers 26.4% of the page —
	// the largest cluster there by a wide margin, which every area threshold accepts
	// as that page's picture — and is exactly 1 shape.
	//
	// The value is chosen off the sweep in TestGuardSweep rather than off a gap,
	// because there is no gap: the counts fall smoothly, 291 / 286 / 244 / 238 / 236
	// on the sequential manual at 10 / 15 / 20 / 25 / 30. 25 is still the one value
	// in that range that is not on a step — five either side moves both documents by
	// under 3%, where 20 gains 20% on a step down to 15 — and that stability is
	// asserted rather than described. Reading the clip moved every number in that
	// sweep and moved neither the shape of it nor the value chosen.
	minFigureInk = 25

	// maxFigureTextFraction is how much of a candidate's area may be covered by
	// text before it is a table rather than a picture, as a fraction.
	//
	// The same discrimination rules.go's [tableHasText] makes, from the other side.
	// The separation is wide — no accepted figure of either document is over 9%
	// text, while page 57 of the columns manual draws two tables that are 37.4% and
	// 38.7% — and 0.15 sits in the middle of it.
	//
	// It is also, measured, nearly dead, and that is stated rather than left to be
	// discovered. [trimToPicture] took most of its work: a candidate that has reached
	// over a column of prose now has the prose trimmed off instead of being rejected
	// whole, which is the better outcome. What is left is one decision in 297 figures
	// across both documents — page 53 of the sequential manual, the French recycling
	// label, which is a picture with a paragraph inside it and therefore a loss
	// rather than a save. TestWhatTheTextGuardIsStillWorth holds both numbers.
	//
	// It is kept on the reasoning [ruleWalker.filled] sets out for a branch measured
	// to be worth almost nothing: a ruled table with more parts than [minFigureInk]
	// and cells full of text is an ordinary thing for a document to contain, the
	// guard is three lines, and the next manual gets no say in which cases this code
	// understands. What it cannot catch either way is an *empty* table, and that is
	// measured too — see the note at the end of this file.
	maxFigureTextFraction = 0.15

	// washFraction is how wide a drawn shape may be, as a fraction of the page,
	// before it is read as a background rather than as part of a picture. It is
	// structural rather than fitted: the columns manual paints its alternating
	// section bands as filled rects exactly the page's width — 891.8 on an 892-unit
	// page — and a band touches every picture it runs behind, so leaving them in
	// merges the whole page into one cluster. 0.98 admits the band and nothing real:
	// the widest figure measured is 807 units on a 918-unit page, 0.88.
	washFraction = 0.98

	// maxFigureClusterInk caps how many drawn shapes one page's clustering will
	// consider, because the clustering is quadratic in them and a real page reaches
	// six figures. Page 42 of the columns manual returns 165,759 shapes, 83,014 of
	// them on-page, because its water-spray gradients are meshes of tens of
	// thousands of hairlines; page 5 of the sequential manual returns 16,014.
	// 200,000 is above the largest measured and bounds the pass at a few seconds
	// rather than minutes.
	maxFigureClusterInk = 200_000

	// maxFigurePNGBytes caps one rendered figure held in memory. Measured over
	// every figure of both fixtures the largest is 353 KB, page 11's parts diagram
	// at 1077x1510; 32 MB is two orders above that and still bounds a page-sized
	// crop of a hostile document at 216 dpi.
	maxFigurePNGBytes = 32 << 20
)

// errFigureTooLarge is returned when a rendered figure exceeds the cap.
var errFigureTooLarge = errors.New("doc: rendered figure exceeds the size limit")

// Ink is one drawn shape's bounding box, in the same 1.5-scaled coordinate space
// as [PageRuns] and a `pdftoppm -r 108` raster.
//
// It is deliberately only a box. A picture is recognised by where its shapes are
// and how many there are, never by what they draw, and carrying the path would
// invite a caller to ask a question this file cannot answer — the flattening
// [subpaths] does means the box is exact only for straight edges.
type Ink struct {
	// Rect is the shape's extent.
	Rect CellRect `json:"rect"`
	// Stroked reports that this came from a stroked path rather than a fill, the
	// same distinction [Rule.Filled] records and for the same reason: a wrong
	// answer can be traced back to the half of the walker that produced it.
	Stroked bool `json:"stroked,omitempty"`
}

// Figure is one illustration found on a page: where it is, and its bytes.
type Figure struct {
	// Page is the 1-based page number in the original PDF.
	Page int `json:"page"`
	// Index is the figure's position in the page's reading order, from 0, sorted
	// down then across. It is not a document-wide figure number: nothing here has
	// the whole document in view, and numbering across pages is the caller's.
	Index int `json:"index"`
	// Rect is where the figure sits, in the 1.5-scaled space. Carried beside the
	// bytes because it is half the answer: a picture has to land in the right place
	// in a column's reading order, and on a parallel-columns manual it has to be
	// attributable to a language region.
	Rect CellRect `json:"rect"`
	// Ink is how many drawn shapes the figure holds — the shape guard's evidence,
	// kept rather than reduced to the verdict, so a rejected page can be shown to
	// have been rejected for the right reason.
	Ink int `json:"ink"`
	// TextFraction is how much of the figure's area is covered by text, the text
	// guard's evidence.
	TextFraction float64 `json:"textFraction"`
	// DPI, PixelWidth and PixelHeight describe the render. The pixel size is read
	// back out of the PNG rather than computed, so a caller comparing it against
	// Rect is comparing what poppler did with what was asked for.
	DPI         int `json:"dpi"`
	PixelWidth  int `json:"pixelWidth"`
	PixelHeight int `json:"pixelHeight"`
	// Digest is the lowercase hex SHA-256 of PNG. The blob store's filename is the
	// SHA-256, so this is the name these bytes will have if a later stage stores
	// them — this file deliberately stores nothing.
	Digest string `json:"digest"`
	// PNG is the rendered figure.
	PNG []byte `json:"-"`
}

// ExtractInk reads every shape one page draws, as bounding boxes.
//
// Like [ExtractRules] it never mutates the file and calls nothing remote, so it
// is a pure function of the bytes and safe to re-run, which is what lets the job
// that calls it be idempotent. One page per invocation, forced by the same
// property of `pdftocairo -svg` [ExtractRules] records.
//
// A caller that wants both the rules and the ink of a page pays for pdftocairo
// twice. That is left as it is rather than fused: the two halves are built
// separately and joined later, which is the split that let the table work and the
// block work proceed without agreeing on anything, and the fusion is one function
// away once there is a caller that needs both.
func ExtractInk(ctx context.Context, path string, page int) ([]Ink, error) {
	if page < 1 {
		return nil, fmt.Errorf("doc: page %d is not a page number", page)
	}
	bin, err := extern.Require(extern.PDFToCairo)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()

	// The flags are exactly [ExtractRules]'s, for the reasons it gives: "-" keeps
	// the SVG in memory rather than writing a derived file beside the immutable
	// blob store, and -f/-l bound the range to one page.
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

	ink, err := parseInk(out.buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("doc: reading pdftocairo output for %s page %d: %w",
			redact(path, path), page, err)
	}
	return ink, nil
}

// PageFigures finds one page's pictures and renders each of them.
//
// The page's text is taken as a parameter rather than extracted again, for the
// reason [PageTables] gives: the text guard needs it and [ExtractRuns] has
// already paid for it. A nil page is an error here rather than a skipped guard,
// because without the page box there is no way to tell a background wash from a
// picture and the answer would be one figure covering the page.
func PageFigures(ctx context.Context, path string, page *PageRuns) ([]Figure, error) {
	if page == nil {
		return nil, errors.New("doc: PageFigures needs a page to read")
	}
	ink, err := ExtractInk(ctx, path, page.No)
	if err != nil {
		return nil, err
	}
	found := FindFigures(ink, page)
	for i := range found {
		if err := renderFigure(ctx, path, &found[i]); err != nil {
			return nil, err
		}
	}
	return found, nil
}

// FindFigures groups a page's ink into pictures and returns those that pass both
// guards, in reading order.
//
// Pure geometry: it spawns nothing and reads no file, so the guards are testable
// without poppler. The returned figures carry no bytes — see [PageFigures] for
// those.
func FindFigures(ink []Ink, page *PageRuns) []Figure {
	return findFigures(ink, page, defaultGuards)
}

// figureGuards are the two guards' thresholds, taken as a value rather than read
// from the constants directly so that a test can sweep them over both whole
// documents. That is how every threshold in this package was set, and it is what
// makes the sensitivity ranges quoted above checkable rather than remembered.
type figureGuards struct {
	minWidth, minHeight float64
	minInk              int
	maxText             float64
}

var defaultGuards = figureGuards{
	minWidth: minFigureWidth, minHeight: minFigureHeight,
	minInk: minFigureInk, maxText: maxFigureTextFraction,
}

func findFigures(ink []Ink, page *PageRuns, g figureGuards) []Figure {
	if page == nil || page.Width <= 0 || page.Height <= 0 {
		return nil
	}
	drawn := onPageInk(ink, page.Width, page.Height)
	if len(drawn) > maxFigureClusterInk {
		return nil
	}

	var dropped DroppedRuns
	text := usableRuns(page.Runs, page.Width, page.Height, &dropped)

	var out []Figure
	for _, area := range clusterInk(drawn) {
		if area.Width() < g.minWidth || area.Height() < g.minHeight {
			continue
		}
		count := 0
		for i := range drawn {
			if contains(area, drawn[i].Rect) {
				count++
			}
		}
		if count < g.minInk {
			continue
		}
		area = trimToPicture(area, text)
		if area.Width() < g.minWidth || area.Height() < g.minHeight {
			continue
		}
		fraction := textFraction(area, text)
		if fraction > g.maxText {
			continue
		}
		out = append(out, Figure{
			Page: page.No, Index: len(out), Rect: area,
			Ink: count, TextFraction: fraction,
		})
	}
	return out
}

// onPageInk drops the shapes that cannot be part of a picture: those outside the
// page box, and those as wide as the page.
//
// Both are structural rather than fitted, and both were found by reading a wrong
// answer. Cairo's compositing machinery paints rects larger than the page — on
// page 57 of the columns manual, 1286x1225 on an 892x850 page, starting at
// (-196.4,-187.1) — and a shape covering the page touches everything on it, so
// unfiltered every page clusters into one figure. See [washFraction] for the
// second.
func onPageInk(ink []Ink, width, height float64) []Ink {
	out := make([]Ink, 0, len(ink))
	for i := range ink {
		r := ink[i].Rect
		switch {
		case r.Width() <= 0 || r.Height() <= 0:
		case r.X0 < -1 || r.Y0 < -1 || r.X1 > width+1 || r.Y1 > height+1:
		case r.Width() >= washFraction*width:
		default:
			out = append(out, ink[i])
		}
	}
	return out
}

// clusterInk groups shapes that overlap or touch into candidate pictures, and
// returns each group's bounding box in reading order.
//
// Touching rather than within a gap, deliberately. A tolerance was measured and
// costs more than it buys: at 2 units page 42 of the columns manual returns 3
// figures where 0 returns the same 3, and at that page's other extreme a gap
// large enough to join a drawing to its own caption also joins two drawings 27
// units apart. What holds a real picture together is that its strokes meet, and
// they do.
func clusterInk(ink []Ink) []CellRect {
	n := len(ink)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	// Iterative rather than recursive, halving the path as it goes: a page of
	// 83,014 shapes can chain deeply enough for a recursive find to be a real
	// stack.
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}

	// Sweeping down the page rather than comparing every pair: a page of 83,014
	// shapes is 3.4 billion pairs, and the quadratic version of this took minutes
	// on page 42 of the columns manual. Sorted by top edge, a shape can only touch
	// one already seen whose bottom edge has not yet passed it, so the active set
	// stays small.
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		return ink[order[a]].Rect.Y0 < ink[order[b]].Rect.Y0
	})
	active := make([]int, 0, 64)
	for _, i := range order {
		r := ink[i].Rect
		// Compacted in place: everything whose bottom edge is above this shape's
		// top can never touch anything later, so it leaves the active set.
		live := 0
		for _, j := range active {
			s := ink[j].Rect
			if s.Y1 < r.Y0 {
				continue
			}
			active[live] = j
			live++
			if s.X0 <= r.X1 && r.X0 <= s.X1 {
				if a, b := find(i), find(j); a != b {
					parent[a] = b
				}
			}
		}
		active = append(active[:live], i)
	}

	boxes := make(map[int]CellRect, n)
	for i := range ink {
		root := find(i)
		r := ink[i].Rect
		if cur, ok := boxes[root]; ok {
			boxes[root] = CellRect{
				math.Min(cur.X0, r.X0), math.Min(cur.Y0, r.Y0),
				math.Max(cur.X1, r.X1), math.Max(cur.Y1, r.Y1),
			}
			continue
		}
		boxes[root] = r
	}

	out := make([]CellRect, 0, len(boxes))
	for _, r := range boxes {
		out = append(out, r)
	}
	// Down then across, which is the reading order [DetectColumns] establishes for
	// text and the order a figure has to take its place in.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Y0 != out[j].Y0 {
			return out[i].Y0 < out[j].Y0
		}
		return out[i].X0 < out[j].X0
	})
	return out
}

// contains reports whether inner sits wholly inside outer.
func contains(outer, inner CellRect) bool {
	const slack = 0.01 // arithmetic slack; both boxes came from the same maxima
	return inner.X0 >= outer.X0-slack && inner.X1 <= outer.X1+slack &&
		inner.Y0 >= outer.Y0-slack && inner.Y1 <= outer.Y1+slack
}

// textFraction is how much of a rectangle's area the text inside it covers.
//
// A run is charged by how much of it overlaps, not by whether its centre is
// inside, and that is not a refinement — the centre rule was written first and it
// is wrong by a factor of ten on a real page. [countCellText] can use the centre
// because a cell is at least as wide as the text set in it; a figure is not. Page
// 529 of the sequential manual prints a small diagram 91 units wide under a
// caption line 400 units wide, and the caption's midpoint lands inside the
// diagram's box: charged whole, one line of text covered 95.3% of a picture and
// the text guard rejected it, while the very same diagram on page 550 — the same
// content in another language, with the caption a few units higher — was accepted.
// A guard that decides opposite ways about the same picture is measuring the wrong
// thing.
//
// Run rectangles are still summed without subtracting where two overlap, which can
// only overstate the fraction. That direction is deliberate: it errs towards
// calling a picture a table, and rendering a page of prose as an illustration is
// the worse of the two failures.
func textFraction(area CellRect, text []TextRun) float64 {
	size := area.Width() * area.Height()
	if size <= 0 {
		return 0
	}
	var covered float64
	for i := range text {
		r := &text[i]
		w := math.Min(area.X1, r.X+r.Width) - math.Max(area.X0, r.X)
		h := math.Min(area.Y1, r.Y+r.Height) - math.Max(area.Y0, r.Y)
		if w > 0 && h > 0 {
			covered += w * h
		}
	}
	return covered / size
}

// trimToPicture pulls a candidate's edges in off any line of text it has reached
// over. It was written as the remedy for the clip this code could not read, and it
// has outlived that cause — which makes it the one thing here whose keep is now a
// judgement rather than a measurement.
//
// It was added because an ink box was a path's unclipped extent, so a drawing whose
// artwork ran past its frame reached into the text column beside it: 19 of the
// columns manual's 45 figures overlapped a line of five characters or more, and the
// crop of page 18's first figure contained a slab of German prose and the printed D
// badge. clip.go now cuts that box back to what the page paints, so the cause is
// gone. What trimming is worth on top of it was measured over both whole documents,
// as figures overlapping a line of five runes or more, and figures with ink of their
// own crossing their box:
//
//	                        trimming on        trimming off
//	columns manual, 59      9 over prose,      15 over prose,
//	                        15 cut by the trim  2 cut
//	sequential, 238         0 over prose,      10 over prose,
//	                        101 crossing        96 crossing
//
// So it is not redundant and it is not free: it removes six prose overlaps on one
// document and ten on the other, and it cuts into thirteen columns-manual drawings
// that the clip alone would have returned whole — page 16 figure 2 loses its right
// third to the label »click«. Which of those a reader minds more is a decision for
// whoever owns the reader, not one to settle inside this function, so the behaviour
// is left exactly as it was and the numbers are recorded here so the decision can
// be taken on them.
//
// Only a run of [minTrimRunes] or more is trimmed away, which is the whole reason
// this does not destroy a diagram: a callout number is one or two characters, and
// page 11's parts diagram carries 73 of them inside its frame. And no edge moves by
// more than [maxTrimFraction] of the side it is on, so a candidate that is genuinely
// half prose — page 34's over-merged cluster — is not whittled into a plausible
// picture but left for the text guard to reject.
//
// The edge that costs the least area is chosen each round, because a line of text
// at a corner can be excluded two ways and the cheaper one keeps more of the
// drawing.
func trimToPicture(area CellRect, text []TextRun) CellRect {
	const (
		// minTrimRunes is the shortest run worth trimming for. Four rather than one
		// because the labels printed inside these documents' diagrams are short —
		// "OFF" on page 34 of the columns manual, the digits 1 to 39 on page 11 —
		// and a figure is not improved by being cut away from its own labels.
		minTrimRunes = 4
		// maxTrimFraction is how much of a side may be trimmed away in total. A
		// third: past that the candidate was not a picture with text at its edge, it
		// was a region containing both, and shrinking it to fit is fabricating a
		// figure boundary the page does not draw.
		maxTrimFraction = 1.0 / 3
	)
	minW := area.Width() * (1 - maxTrimFraction)
	minH := area.Height() * (1 - maxTrimFraction)

	// Bounded rather than "until clean": each round removes at least one run, and a
	// page of these documents holds a few hundred.
	for range 32 {
		var worst *TextRun
		var bestCost float64
		var bestEdge int
		for i := range text {
			r := &text[i]
			if len([]rune(strings.TrimSpace(r.Text))) < minTrimRunes {
				continue
			}
			x0, y0 := math.Max(area.X0, r.X), math.Max(area.Y0, r.Y)
			x1, y1 := math.Min(area.X1, r.X+r.Width), math.Min(area.Y1, r.Y+r.Height)
			if x1 <= x0 || y1 <= y0 {
				continue
			}
			// Four ways to put the run outside: pull in the left, right, top or
			// bottom edge to the far side of it. Cost is the area given up.
			costs := [4]float64{
				(x1 - area.X0) * area.Height(), // left edge moves right to x1
				(area.X1 - x0) * area.Height(), // right edge moves left to x0
				(y1 - area.Y0) * area.Width(),  // top edge moves down to y1
				(area.Y1 - y0) * area.Width(),  // bottom edge moves up to y0
			}
			for edge, cost := range costs {
				if worst == nil || cost < bestCost {
					// Only consider an edge that leaves the figure big enough.
					next := area
					switch edge {
					case 0:
						next.X0 = x1
					case 1:
						next.X1 = x0
					case 2:
						next.Y0 = y1
					case 3:
						next.Y1 = y0
					}
					if next.Width() < minW || next.Height() < minH {
						continue
					}
					worst, bestCost, bestEdge = r, cost, edge
				}
			}
		}
		if worst == nil {
			return area
		}
		x0, y0 := math.Max(area.X0, worst.X), math.Max(area.Y0, worst.Y)
		x1, y1 := math.Min(area.X1, worst.X+worst.Width), math.Min(area.Y1, worst.Y+worst.Height)
		switch bestEdge {
		case 0:
			area.X0 = x1
		case 1:
			area.X1 = x0
		case 2:
			area.Y0 = y1
		case 3:
			area.Y1 = y0
		}
	}
	return area
}

// renderFigure renders one figure's rectangle with pdftoppm and fills in its
// bytes, pixel size and digest.
//
// One invocation per figure rather than one render of the page cropped in Go, and
// the cost of that is real: measured on the sequential manual, a crop takes 0.19 s
// and a whole page 0.39 s, so a page carrying 33 figures costs 6 s this way against
// 0.4 s the other, because poppler renders the whole page either way and only the
// output is clipped. It is chosen anyway, for two reasons that outweigh it. The
// bytes are exactly what poppler wrote, so the digest does not depend on Go's PNG
// encoder reproducing poppler's output — and this is a content-addressed store,
// where a re-encoded byte means the same picture stored twice. And the total stays
// the same order as what the stage it belongs to already costs: the columns
// manual's 46 figures take 14 s end to end including the pdftocairo passes, against
// the 8.6 s reading that document's ruled lines takes, and conversion runs only over
// the pages in scope.
func renderFigure(ctx context.Context, path string, fig *Figure) error {
	bin, err := extern.Require(extern.PDFToPPM)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()

	// -x -y -W -H are in the rendered image's own pixels, which at figureDPI are
	// exactly figureScale times this package's coordinates. Rounded outwards so
	// the crop never cuts a stroke the geometry included.
	x := int(math.Floor(fig.Rect.X0 * figureScale))
	y := int(math.Floor(fig.Rect.Y0 * figureScale))
	w := int(math.Ceil(fig.Rect.X1*figureScale)) - x
	h := int(math.Ceil(fig.Rect.Y1*figureScale)) - y
	if x < 0 {
		w, x = w+x, 0
	}
	if y < 0 {
		h, y = h+y, 0
	}
	if w <= 0 || h <= 0 {
		return fmt.Errorf("doc: figure on page %d has no extent: %v", fig.Page, fig.Rect)
	}

	// -png rather than -jpeg: a line drawing is what these are, and JPEG rings
	// around a hairline. The output prefix is omitted entirely, which is how
	// pdftoppm is told to write the image to stdout — it keeps the bytes out of the
	// directory the immutable blob store lives in, and it is not the "-" the other
	// poppler tools here take: passing "-" makes pdftoppm write a file called
	// "-.png" beside the process's working directory and return nothing, measured.
	// #nosec G204 -- see ProbeInfo: bin comes from extern's own tool table, path
	// is a blob-store path derived from a validated SHA-256 digest, and every
	// other argument is an int.
	cmd := exec.CommandContext(ctx, bin, "-png", "-r", strconv.Itoa(figureDPI),
		"-f", strconv.Itoa(fig.Page), "-l", strconv.Itoa(fig.Page),
		"-x", strconv.Itoa(x), "-y", strconv.Itoa(y),
		"-W", strconv.Itoa(w), "-H", strconv.Itoa(h),
		"-singlefile", path)
	out := &limitedBuffer{limit: maxFigurePNGBytes}
	var errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = out, &errOut
	if err := cmd.Run(); err != nil {
		if errors.Is(err, errOutputTooLarge) {
			return fmt.Errorf("%w (limit %d bytes)", errFigureTooLarge, maxFigurePNGBytes)
		}
		return fmt.Errorf("doc: pdftoppm failed on page %d: %w: %s",
			fig.Page, err, redact(strings.TrimSpace(errOut.String()), path))
	}

	png := out.buf.Bytes()
	pw, ph, err := pngSize(png)
	if err != nil {
		return fmt.Errorf("doc: pdftoppm output for page %d: %w", fig.Page, err)
	}
	sum := sha256.Sum256(png)
	fig.PNG, fig.DPI = png, figureDPI
	fig.PixelWidth, fig.PixelHeight = pw, ph
	fig.Digest = hex.EncodeToString(sum[:])
	return nil
}

// pngSize reads a PNG's pixel dimensions out of its IHDR chunk.
//
// Eight bytes of a fixed header rather than image/png.DecodeConfig, because
// decoding is the one thing this file must not do: the bytes are stored verbatim
// and the size is the only fact needed about them. It doubles as the check that
// pdftoppm wrote a PNG at all — an empty stdout with a zero exit status would
// otherwise be stored as a figure.
func pngSize(data []byte) (width, height int, err error) {
	const sigAndIHDR = 8 + 8 + 8
	if len(data) < sigAndIHDR {
		return 0, 0, fmt.Errorf("expected a PNG, got %d bytes", len(data))
	}
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return 0, 0, errors.New("expected a PNG signature")
	}
	if !bytes.Equal(data[12:16], []byte("IHDR")) {
		return 0, 0, errors.New("expected IHDR as the PNG's first chunk")
	}
	be := func(b []byte) int {
		return int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3])
	}
	return be(data[16:20]), be(data[20:24]), nil
}

// parseInk reads every drawn shape's bounding box out of cairo's SVG.
func parseInk(data []byte) ([]Ink, error) {
	doc, err := parseSVG(data)
	if err != nil {
		return nil, err
	}
	w := &inkWalker{doc: doc, visited: make(map[visitKey]bool)}
	// Only the body from the top, entering <defs> exclusively through the
	// reference that pulls a definition back in — the compositing-group trap
	// rules.go's header documents applies here identically, and a figure's box is
	// wrong by the filter region's origin without it.
	for _, kid := range doc.root.kids {
		w.walkBody(kid, identity, clipBox{}, 0)
	}
	return w.ink, nil
}

// inkWalker accumulates bounding boxes while walking the tree. The same walk
// [ruleWalker] makes, keeping every shape rather than only the axis-aligned thin
// ones; glyph outlines are already gone, dropped structurally by [parseSVG].
type inkWalker struct {
	doc     *svgDoc
	ink     []Ink
	visited map[visitKey]bool
}

func (w *inkWalker) walkBody(n *svgNode, m matrix, clip clipBox, depth int) {
	if n.tag == "defs" {
		return
	}
	w.walk(n, m, clip, depth)
}

func (w *inkWalker) walk(n *svgNode, m matrix, clip clipBox, depth int) {
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
	// The element's clip narrows whatever it inherited, in the user space its own
	// transform establishes. A clip admitting nothing means every shape below is
	// invisible, so the subtree is abandoned rather than walked and discarded.
	if box, ok := w.doc.clipAt(n.clip, m); ok {
		clip = clip.intersect(box)
		if clip.empty() {
			return
		}
	}

	if id, ok := refID(n.filter); ok {
		for _, ref := range w.doc.filterRefs[id] {
			if target := w.doc.byID[ref]; target != nil {
				w.walk(target, m, clip, depth+1)
			}
		}
	}
	if n.tag == "use" && strings.HasPrefix(n.href, "#") {
		if target := w.doc.byID[n.href[1:]]; target != nil {
			w.walk(target, m, clip, depth+1)
		}
	}

	painted := func(v string) bool { return v != "" && v != "none" }
	switch {
	case n.tag == "path" && (painted(n.stroke) || painted(n.fill)):
		stroked := painted(n.stroke)
		for _, sub := range subpaths(n.d) {
			w.add(m, clip, sub, stroked)
		}
	case n.tag == "rect" && painted(n.fill):
		w.add(m, clip, []point{
			{n.x, n.y}, {n.x + n.w, n.y}, {n.x + n.w, n.y + n.h}, {n.x, n.y + n.h},
		}, false)
	}

	for _, kid := range n.kids {
		w.walkBody(kid, m, clip, depth+1)
	}
}

// add records one shape's visible box: its geometric extent cut back to the clip
// in force where it is drawn.
//
// A shape clipped away entirely is dropped rather than recorded with an empty
// box, because a figure is recognised by how many shapes are inside it — see
// [minFigureInk] — and counting ink the page never paints is the same error as
// including it in the extent.
func (w *inkWalker) add(m matrix, clip clipBox, sub []point, stroked bool) {
	if len(sub) == 0 {
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
	w.ink = append(w.ink, Ink{Rect: box, Stroked: stroked})
}

// What this deliberately does not solve, each measured rather than supposed.
//
// **An empty ruled table reads as a picture.** The text guard separates a table
// from a framed illustration by whether the cells hold words, and a blank form has
// none: page 558 of the sequential manual prints two warranty-registration forms
// whose labels sit only in the left column, and both come back as figures — 2 of
// that document's 238. Excluding anything the ruled-table shape guard claims would
// fix it and cost more than it saves, and that number is already recorded in
// docs/design/conversion.md: the shape guard alone passes 12 pages of the columns
// manual, and 2 of those — 22 and 44 — are the grids of framed illustrations this
// file exists to find.
//
// **A picture can still be cut by its own labels.** Not the clip any more — that is
// read — but [trimToPicture], which pulls an edge in off a line of four runes or
// more. Page 16 figure 2 of the columns manual is the case: the printed panel runs
// to x=288 and carries the label »click« at its right, so the box stops at 209 and
// the crop loses the right third of the drawing. 15 of that document's 59 figures
// are cut this way against 2 with trimming off, and the trade is measured in the
// note on [trimToPicture] rather than decided here.
//
// **Page furniture repeated in the same place is not identified as such.** The
// ink guard rejects every logo and badge in these two documents because they are
// two or three shapes, not because they repeat. A vector logo drawn with a hundred
// strokes, on every page, would come back as a figure on every page. What
// identifies furniture is repetition in the same position across pages, which is
// the same conclusion docs/design/conversion.md reaches about a running head and
// the same different input: a pass with the whole document in view.
//
// **A figure has no caption and no reading position within a region.** Both need
// the block work this file deliberately does not touch. The rectangle is here so
// that join can be geometric, which is what conversion.md records it has to be.
//
// **Nothing is stored.** The digest is computed and the bytes are returned; who
// writes them to the content-addressed store, and what row points at them, is the
// storage step.
