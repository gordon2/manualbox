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
	"slices"
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
// pages carrying figures, which is what says these are splits rather than newly
// admitted furniture — 27 and 23, counted over what [FindFigures] returns for every
// page.
//
// **A candidate whose box overlaps another's is a piece of it.** Reading the clip
// split drawings that had been merged; it also revealed that some of them had been
// in pieces all along, because a group's box is the union of its shapes and can
// cover a neighbouring group entirely without any shape of the two touching. See
// [mergeOverlapping], which is the second pass that fixes it, and what it was worth
// end to end:
//
//	                              columns manual   sequential manual
//	figures                         59 -> 59         168 -> 134
//	figures cut off by their crop    3 -> 3            70 -> 25
//	figures with a blank band        0 -> 0             2 -> 2
//
// The columns manual does not move at all, at any merge threshold: it has no page
// where two candidates overlap. On the sequential manual the pages carrying figures
// again do not move, and the clipped count falls by two thirds, because a piece of a
// drawing is crossed by the shapes of the piece beside it.
//
// Note the level: the tables above are what `manualbox verify` converts, and
// conversion keeps 134 of the sequential manual's 195 figures, landing on 20 of
// those 23 pages. Both counts are pinned, in TestGuardSweep and in verify's
// fixture tests respectively.
//
// The residual counts were not the clip either, and most of the columns manual's
// have since gone: 15 of them were [trimToPicture] cutting into a drawing that had
// a label at its edge, and teaching the trim to leave a label the artwork encloses
// alone took that document to 3. What remains is three classes, none of them the
// clip. Pages 11 and 12 of the columns manual report one shape crossing out of
// 2,741, which is a page-sized path the geometric matching in `internal/verify`
// cannot attribute; page 1 is the cover, whose artwork genuinely runs behind the
// title block the trim excludes; and the sequential manual's 25 are leader lines on
// its crowded diagram pages, where a line more than half inside one figure's box
// belongs to the drawing beside it. See the note on [trimToPicture].
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

	// figureMergeOverlap is how much of the smaller of two candidate boxes may lie
	// inside the other before the two are read as one picture, as a fraction.
	//
	// Zero: any positive overlap merges, and a box that merely touches another does
	// not. The measurement behind that — 53 overlapping pairs over both documents,
	// every one of them a single drawing that clustered in pieces, and no pair
	// anywhere that needs the opposite answer — is at [mergeOverlapping].
	figureMergeOverlap = 0.0

	// maxFigureClusterInk caps how many drawn shapes one page's clustering will
	// consider, because the clustering is quadratic in them and a real page reaches
	// six figures. Page 42 of the columns manual returns 165,759 shapes, 83,014 of
	// them on-page, because its water-spray gradients are meshes of tens of
	// thousands of hairlines; page 5 of the sequential manual returns 16,014.
	// 200,000 is above the largest measured and bounds the pass at a few seconds
	// rather than minutes.
	maxFigureClusterInk = 200_000

	// trimReachSlack is how far a line of text may poke out of a candidate's box
	// and still count as printed inside it, in units. Zero: the comparison is exact.
	//
	// A tolerance is the obvious thing to want here, because the two rectangles come
	// from two tools — the text box from pdftohtml, the ink box from pdftocairo — and
	// a unit is what this project allows elsewhere when it compares one measurement
	// of a drawing against another. It is not free, and that is why it is not taken.
	// Swept at 0, 1 and 2 over both documents, the columns manual does not move at
	// all; the sequential one loses a trim at 1, on page 523, where a two-line Russian
	// caption clears the box's top edge by 0.5 units and so stops being seen as
	// reaching over it. Its own right-hand exclusion is blocked by [maxTrimFraction],
	// so the tolerance is the difference between excluding that caption and keeping
	// it. Nothing is gained anywhere in exchange, so the exact test stands.
	trimReachSlack = 0.0

	// labelTerminator is how large a leader's end mark may be, in units, and it is
	// the signal the whole of [growToLabels] turns on.
	//
	// Both documents draw one: a small open circle where a leader line stops, just
	// short of the label it points at. Measured, they are 3.3 to 3.4 units square on
	// page 521 of the sequential manual and 3.4 on its plate pages. 8 is chosen to
	// admit a mark of twice that with a stroke's width on top, and the sweep in
	// TestGrowSweep says the value is not on a cliff: at 4, 6, 8 and 12 the
	// sequential manual grows 48, 53, 55 and 66 figures. 12 is where it starts
	// admitting a drawing's own small details as terminators, and 4 misses marks that
	// carry a stroke.
	labelTerminator = 8.0

	// labelAlign is how far off a run's midline a terminator may sit, in units.
	// A leader points AT its label, so the mark and the label's middle line up; 4 is
	// about a third of a line of body text on either document (12 to 14 units).
	// Swept: 2, 4, 6 and 8 grow 48, 55, 59 and 62 figures of the sequential manual,
	// and the overlapping crops it creates rise from 12 to 18 over that range.
	labelAlign = 4.0

	// labelCorridor is how far outside a figure's edge a label may sit and still be
	// that figure's, in units.
	//
	// Measured on page 521 of the sequential manual, where the labels of three
	// drawings sit 0.1 to 35.3 units out: the box's edge is the leader's terminator,
	// so the near ones are 3 units away, and the far ones are labels whose leader
	// ends short of the drawing. 40 covers all of them. It cannot be much tighter:
	// at 20 the underside diagram's six left labels are out of reach. It must not be
	// much wider either, and the reason is a document rather than a preference — see
	// the note on the parts list in [growToLabels].
	labelCorridor = 40.0

	// maxLabelGrowth is how far one edge may move to take in labels, as a fraction
	// of the side it is on. One: an edge may not move further than the drawing's own
	// width or height.
	//
	// Swept over the sequential manual as figures grown / labels taken: 18/51 at
	// 0.25, 32/110 at 0.5, 55/233 at 1, 63/259 at 2 and 64/266 with no cap at all,
	// where the largest single growth reaches 3.56 of a side. 1 is where the
	// document's own labelled diagrams are all served — page 521's three drawings
	// need 0.26, 0.68 and 0.65 — and it is a bound with a meaning rather than a
	// fitted number: past it the labels are larger than the picture and the crop is
	// no longer a picture with its labels.
	maxLabelGrowth = 1.0

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
	// Rect is what was rendered, in the 1.5-scaled space: the drawn extent plus any
	// labels [growToLabels] took in. Carried beside the bytes because it is half the
	// answer — a picture has to land in the right place in a column's reading order —
	// and it is the rectangle PixelWidth and PixelHeight describe, so a caller
	// scaling the pixels back onto the page is scaling them onto this.
	Rect CellRect `json:"rect"`
	// InkRect is the drawn extent alone, before any label was taken in, and it is
	// what the two guards judged.
	//
	// It is carried separately because one caller must not use Rect: [attribute]
	// decides which language a picture belongs to by which region its box lies
	// inside, and a box grown sideways onto a label could reach out of its own
	// column and be served to every household — which is the one failure the funnel
	// may not have. The language question is asked of the drawing, the crop is what
	// a reader is shown.
	//
	// Not stored. Nothing reading a figure back out of the database asks the
	// language question again; it was answered when the conversion was made.
	InkRect CellRect `json:"inkRect,omitzero"`
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

// DrawnExtent is the figure's drawn box: [Figure.InkRect] when it is known, and
// [Figure.Rect] when it is not.
//
// The fallback is there for the two callers that legitimately have no ink box. A
// figure read back out of the database has only the rectangle that was stored,
// because the drawn extent is not stored — nothing reading a conversion asks the
// language question again. And a figure built by hand in a test states the box it
// is about. Both mean the same thing when nothing has been grown, which is why
// this is a fallback rather than an error: before [growToLabels] the two rects
// were one rect.
func (f *Figure) DrawnExtent() CellRect {
	if f.InkRect == (CellRect{}) {
		return f.Rect
	}
	return f.InkRect
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
	// mergeOverlap is how much of the smaller of two candidate boxes may lie
	// inside the other before they are read as one picture. See
	// [mergeOverlapping] for why it is zero.
	mergeOverlap float64
	// The label-growth rule's four numbers, here for the same reason: TestGrowSweep
	// moves them over both whole documents. growth of zero turns the pass off, which
	// is how the sweep measures what it is worth.
	terminator, align, corridor, growth float64
}

var defaultGuards = figureGuards{
	minWidth: minFigureWidth, minHeight: minFigureHeight,
	minInk: minFigureInk, maxText: maxFigureTextFraction,
	mergeOverlap: figureMergeOverlap,
	terminator:   labelTerminator, align: labelAlign,
	corridor: labelCorridor, growth: maxLabelGrowth,
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
	for _, area := range clusterInk(drawn, g.mergeOverlap) {
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
		// Growth comes last, after both guards have judged the drawing. Deliberately:
		// a diagram's own labels are text, so a box grown onto them is legitimately
		// over [maxFigureTextFraction] — page 521's lidar diagram reaches 0.162 with
		// its eleven labels — and re-testing the grown box would reject the very
		// pictures this pass exists to complete.
		out = append(out, Figure{
			Page: page.No, Index: len(out),
			Rect:    growToLabels(area, text, drawn, g),
			InkRect: area,
			Ink:     count, TextFraction: fraction,
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
func clusterInk(ink []Ink, overlap float64) []CellRect {
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
	byReadingOrder := func(a, b CellRect) bool {
		if a.Y0 != b.Y0 {
			return a.Y0 < b.Y0
		}
		return a.X0 < b.X0
	}
	// Sorted BEFORE the merge as well as after, and that is correctness rather than
	// tidiness: the groups come out of a map, so their order is random.
	//
	// At the shipped threshold of zero the order cannot change the answer, and the
	// reason is worth stating because it is what makes that value safe: merging only
	// ever grows a box, so it can never destroy an intersection, and the result is
	// the connected components of "these two boxes intersect" however they are
	// visited. Above zero that stops holding — a merged box is wider, so the smaller
	// box's SHARE of it falls, and a merge can put a pair below the threshold that
	// was above it. Measured: with the boxes left in map order, TestMergeThresholdSweep
	// returned 195, 197 and 196 figures at 0.01, 0.05 and 0.1 in one pass and 194 to
	// 200 in the next, in the same run. These are the pictures a reader is served out
	// of a content-addressed store, so the sweep has to be reproducible too.
	//
	// Reading order is used because it is already the order this function ends in;
	// nothing depends on which order it is, only that it is always the same one.
	sort.Slice(out, func(i, j int) bool { return byReadingOrder(out[i], out[j]) })
	out = mergeOverlapping(out, overlap)
	sort.Slice(out, func(i, j int) bool { return byReadingOrder(out[i], out[j]) })
	return out
}

// mergeOverlapping joins candidate boxes that overlap into one, until none of
// them does.
//
// This is [clusterInk]'s own rule applied to its own output, and the second pass
// is needed because the first cannot see it. A group's box is the union of its
// shapes and is far larger than any of them, so two groups can share most of a
// rectangle while no shape of one touches a shape of the other — which is exactly
// how the fault the user reported arises. Page 524 of the sequential manual draws
// a hand holding a pin over the robot's underside; the hand's strokes reach none
// of the robot's, so it clusters alone, and 90.8% of its box lies inside the
// robot's. It was served as a separate picture: a duplicate scrap of the drawing
// above it.
//
// # Why any overlap at all, and not a fraction of one
//
// A threshold is the obvious thing to want, and the measurement says there is
// nothing for it to separate. Over both whole documents the parallel-columns
// manual has NO overlapping pair of candidates at all — every change here is the
// sequential manual's — and that document has 53, whose overlap as a fraction of
// the smaller box runs 1.00, 0.96, 0.91, 0.91, 0.88 … 0.20, 0.19, 0.14, 0.11,
// 0.10, 0.01 with no gap anywhere. Each was rendered as a crop of the two boxes'
// union and looked at. Every one of the 53 is a single printed drawing that
// clustered in pieces: at 0.91 the hand above, at 0.57 the base station of page
// 522 split at its own waist, at 0.39 the station of page 5 and the wall socket
// it is being plugged into, at 0.01 the water tank of page 522 and the magnified
// detail circle its leader lines run to. Not one is two drawings that merely sit
// close together, so no threshold in the range has a case to decide and every
// value from 0 to 0.01 gives the same answer as containment plus 46 more merges
// that a reader wants.
//
// Two drawings printed close together do exist on these pages and are not
// affected, because their boxes do not overlap: the two mop pads of page 522 are
// 46 units apart, and the two halves of page 524's top illustration 23. That is
// the same fact [clusterInk] records about a gap tolerance, from the other side —
// what does NOT hold a picture together is proximity.
//
// So the threshold is kept as a parameter and set to zero: any positive overlap
// merges. It is a parameter because that sweep is the evidence, and
// TestMergeThresholdSweep re-runs it.
//
// Repeated to a fixpoint, because a merged box is larger and can reach a third
// group that neither part reached. That cannot run away: every round but the last
// removes at least one box, so it terminates.
//
// The result depends on the order the boxes are considered in — absorbing B into A
// can make a box that reaches C where absorbing C into B first need not reach A —
// so the order is fixed by the caller and this pass preserves it. See [clusterInk].
func mergeOverlapping(boxes []CellRect, overlap float64) []CellRect {
	gone := make([]bool, len(boxes))
	for {
		merged := false
		for i := range boxes {
			if gone[i] {
				continue
			}
			for j := i + 1; j < len(boxes); j++ {
				if gone[j] || boxOverlap(boxes[i], boxes[j]) <= overlap {
					continue
				}
				boxes[i] = CellRect{
					math.Min(boxes[i].X0, boxes[j].X0), math.Min(boxes[i].Y0, boxes[j].Y0),
					math.Max(boxes[i].X1, boxes[j].X1), math.Max(boxes[i].Y1, boxes[j].Y1),
				}
				gone[j] = true
				merged = true
			}
		}
		if !merged {
			break
		}
	}
	// Compacted in place, keeping the order the boxes arrived in. The order is what
	// makes the answer reproducible; see the note in [clusterInk].
	out := boxes[:0]
	for i := range boxes {
		if !gone[i] {
			out = append(out, boxes[i])
		}
	}
	return out
}

// boxOverlap is how much of the smaller box lies inside the larger, 1 when one
// contains the other.
//
// Measured per axis and multiplied, rather than as an area ratio, for the reason
// [verify.overlapFraction] gives: a candidate can be degenerate. A single hairline
// clusters alone and its box has zero height, so an area ratio divides by zero;
// per axis the question becomes containment on that axis, which is the same
// question asked of a shape with no thickness.
func boxOverlap(a, b CellRect) float64 {
	return axisOverlap(a.X0, a.X1, b.X0, b.X1) * axisOverlap(a.Y0, a.Y1, b.Y0, b.Y1)
}

// axisOverlap is the share of the shorter of two intervals that lies inside the
// other.
func axisOverlap(a0, a1, b0, b1 float64) float64 {
	// A zero-length interval has no share to take, so the question becomes whether
	// its one point lies inside the other — the same reading [verify.overlap1D]
	// gives a shape with no thickness. Asked before the width test below, because
	// an interval of zero length overlaps nothing by measure.
	if a1 <= a0 {
		return inside(a0, b0, b1)
	}
	if b1 <= b0 {
		return inside(b0, a0, a1)
	}
	in := math.Min(a1, b1) - math.Max(a0, b0)
	if in <= 0 {
		// Touching exactly is not overlapping. The shape-level pass has already
		// joined everything whose boxes meet, so a second pass that merged on
		// contact would only undo its own answer.
		return 0
	}
	return in / math.Min(a1-a0, b1-b0)
}

// inside reports 1 when a point lies within an interval and 0 when it does not.
func inside(p, lo, hi float64) float64 {
	if p >= lo && p <= hi {
		return 1
	}
	return 0
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

// trimToPicture pulls a candidate's edges in off a line of text the box has
// REACHED OVER — a line that starts or ends outside the box — and leaves a line
// printed wholly within the artwork alone.
//
// That distinction is the whole function, and it is what was missing. Trimming was
// written as the remedy for the clip this code could not read: an ink box was a
// path's unclipped extent, so a drawing whose artwork ran past its frame reached
// into the text column beside it, and the crop of page 18's first figure contained
// a slab of German prose and the printed D badge. clip.go removed that cause, and
// what was left was a rule that could not tell a picture's own callout from the
// prose next to it. It cut into 13 of the columns manual's 59 drawings to exclude
// 6 lines of prose — page 16 figure 2 lost its right third to the label »click«,
// printed inside the illustration with artwork around it.
//
// # What separates a callout from prose, measured
//
// The signal tried first was ink: a label inside a drawing should have drawn shapes
// on more than one side of it. It does not separate these documents. The »click« of
// page 16 has ink on all four sides, but the same label on pages 24, 26 and 36 sits
// at the drawing's right edge and has ink only to its left and below — while page
// 1's "GEBRAUCHSANLEITUNG", which is prose the box reached over, also has ink on two
// sides. Those four readings are the cases in TestTrimOnlyPullsOffALineItReachedOver.
//
// What does separate them is containment, and it follows from where a candidate's
// box comes from: the box IS the bounding box of the drawn ink. So a line the box
// merely reached over sticks out of it — the edge that touches the line was set by
// a stroke, not by the line — while a label set inside the artwork has ink beyond
// it on the side that fixes that edge, and is therefore wholly inside. What the
// test drops, measured over both whole documents, is every trim that cut a printed
// callout and no trim that excluded a line of prose:
//
//	                                    old rule   reaching lines only
//	columns manual, trims made            13            6
//	  ...of which cut a printed callout    7            0
//	  lines of prose excluded              6            6
//	sequential manual, trims made         12           11
//
// The six prose lines are page 1's cover title block and the one line of body text
// above the process diagram on each of pages 52-56, and they are excluded either
// way. The seven that stop being cut are »click« on pages 16, 24 (twice), 26 and 36
// (twice) and "1,8 l"/"max. 30° C" on page 28. The sequential manual loses one trim
// of its twelve, on page 53, and one other stops short of a label: page 545's box
// held its right edge at x=245, through the leader line running out to "QR コード",
// and now stops at 285 where the Wi-Fi caption genuinely reaches in.
//
// End to end with `manualbox verify`, figures cut off by their crop fall from 15 to
// 3 on the columns manual and from 71 to 70 on the sequential one, with the figure
// count, the pages carrying figures and the blank-band count unmoved on both.
//
// The figure-overlaps-text count that used to be quoted here cannot show this and
// is not quoted any more: it counts any run of five runes or more, so a picture
// keeping its own »click« reads to it exactly like a picture swallowing a
// paragraph. On the columns manual it moves 9 -> 14 while the prose excluded stays
// at 6. TestGuardSweep still prints it, as a bound rather than as a verdict.
//
// Two guards are kept underneath. Only a run of [minTrimRunes] or more is trimmed
// for, because a callout number is one or two characters and page 11's parts diagram
// carries 73 of them; containment already protects those, and the floor is the
// second lock on a diagram whose numbering runs to the frame. And no edge moves by
// more than [maxTrimFraction] of the side it is on, so a candidate that is genuinely
// half prose — page 34's over-merged cluster — is not whittled into a plausible
// picture but left for the text guard to reject.
//
// The edge that costs the least area is chosen each round, among only the edges the
// run actually reaches past, because a line at a corner can be excluded two ways and
// the cheaper one keeps more of the drawing.
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
			// Which edges the line reaches past. A line wholly inside reaches past
			// none of them and is the picture's own label, so it is left alone; this
			// is the test the whole function turns on. Compared exactly rather than
			// with a tolerance — see [trimReachSlack] for what a tolerance costs.
			reaches := [4]bool{
				r.X < area.X0-trimReachSlack,
				r.X+r.Width > area.X1+trimReachSlack,
				r.Y < area.Y0-trimReachSlack,
				r.Y+r.Height > area.Y1+trimReachSlack,
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
				if !reaches[edge] {
					continue
				}
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

// growToLabels grows a figure's box to take in the labels its leader lines point
// at, and never takes in a line of prose.
//
// This is [trimToPicture]'s opposite and it exists because the trim was only ever
// half the problem. A figure's box is the bounding box of the drawn ink, and a
// callout number is not ink: it is a text run printed just outside the drawing, at
// the end of a leader line. So the box covers every leader and excludes every label
// they point at, and the user's report is exactly that — "the crop keeps the lines
// and loses every number, so the leaders end in nothing and the diagram cannot be
// read against its parts list."
//
// Measured on page 521 of the sequential manual, the RU product overview, whose
// three drawings carry 31 labels between them: the box's right edge is at 263.0,
// the leader terminators are at 259.6-263.0, and all eleven of that drawing's
// labels start at **266.0**. Three units, every one of them. The box does not need
// to reach the leader's end — it is already there. It needs to cross the gap.
//
// # What says a run is this figure's label, and what says it is prose
//
// The terminator: a small mark, [labelTerminator] units at most, sitting in the
// corridor between the figure's edge and the run, on that run's own midline. Both
// documents draw one at the end of every leader.
//
// It has to be that rather than the gap, and the case that settles it is a document
// rather than an argument. Page 11 of the columns manual prints its parts list — 39
// numbers and 39 German names, "1 Gehäusedeckel", "2 Tragegriff" — in a column
// 22.3 units to the right of the exploded view. That is nearer than the underside
// diagram's own labels on page 521, which are 20.3 to 35.3 units out. Any rule that
// grows onto text within some distance swallows the whole parts list; the terminator
// test refuses all 78 of its runs, because a legend is not pointed at.
//
// The second half of the signal is that **a label wraps**. Its second and third
// lines carry no terminator of their own, and left unclaimed they are obstacles to
// the label they belong to: page 521's lidar drawing claims nine of its eleven
// labels by terminator, and the two continuation lines block the edge from moving at
// all. A run flush with a claimed label, on the next line, alone on its baseline, is
// part of it. Alone matters: "Кнопка сброса" is a label and the five lines under it
// are its description, and what separates them is that a bullet has its text beside
// it while a label's continuation does not.
//
// # What it will not do, which is the conservative half
//
// An edge moves only if everything the growth region touches is a claimed label.
// One line of prose in the way and the edge stays where it is. That is why page
// 521's lid-open drawing keeps its three right-hand labels cropped: the corridor
// holds "Кнопка сброса" and then the five bullet lines that explain it, so growing
// right would drag a paragraph into a picture. Its left edge grows and its right
// does not.
//
// A claimed label may still be cut, and prose may not. The edge goes as far as the
// farthest label it can reach cleanly, which on a page whose two label columns
// interleave in x is not far enough for the longest of them: page 521's lidar
// drawing reaches 397, where its own longest label ends at 469, because the
// neighbouring drawing's labels start at 400. Refusing to cut a label at all was
// measured and costs the whole page — with it, that drawing does not grow, and
// neither does its neighbour's left edge. A leader ending in a word cut short is a
// large improvement on a leader ending in nothing; a picture with a paragraph in it
// is not.
//
// # What it is worth, over both whole documents
//
//	                                  columns manual   sequential manual
//	figures                                 59               195
//	figures with a label outside them         2                79
//	figures grown                             0                55
//	labels taken in                           0               233
//
// The columns manual does not move at any setting, which is the same shape of
// evidence [mergeOverlapping] rests on: its two claims are both blocked by prose in
// the corridor, so this change is the other document's entirely. Of the sequential
// manual's 55, **22 are on pages 5 and 6** — the front-matter diagram plates, which
// fall outside every language region and are never converted — leaving 33 on pages a
// reader is served.
//
// The cost is overlapping crops, and it is confined: 13 pairs of grown boxes
// overlap, every single one of them on those two plate pages, and none on any page a
// conversion serves. Page 5 is 31 figures on one sheet with labels between them, and
// two boxes there now overlap wholly. Recorded rather than fixed, because no page a
// reader sees is affected and the alternative — arbitrating which of two drawings a
// shared corridor belongs to — would be a rule invented for one plate.
//
// Edges are taken in a fixed order and each one's region is judged against the box
// as already grown, which is what keeps two independently clean edges from admitting
// a run diagonally outside both.
func growToLabels(area CellRect, text []TextRun, drawn []Ink, g figureGuards) CellRect {
	if g.growth <= 0 {
		return area
	}
	// The terminator candidates, once for the page rather than once per run: a
	// leader's mark is small, and page 42 of the columns manual draws 82,626 shapes
	// that a per-run scan would walk for every label on every figure.
	marks := make([]CellRect, 0, 64)
	for i := range drawn {
		if r := drawn[i].Rect; r.Width() <= g.terminator && r.Height() <= g.terminator {
			marks = append(marks, r)
		}
	}

	out := area
	for side := range 4 {
		// Claims come from the box the guards judged, so which runs are this
		// figure's labels does not depend on the order the edges are taken in.
		claimed := claimLabels(area, text, marks, side, g)
		if len(claimed) == 0 {
			continue
		}
		// The region and the reach are judged against the box as already grown,
		// which is what keeps two independently clean edges from admitting a run
		// diagonally outside both. The cap is against the drawing, so an edge's
		// allowance does not grow because another edge moved first.
		want, ok := labelExtent(out, text, claimed, side)
		if !ok {
			continue
		}
		if edgeMove(area, side, want) > g.growth*edgeSpan(area, side) {
			continue
		}
		out = moveEdge(out, side, want)
	}
	return out
}

// claimLabels collects the runs beyond one edge that belong to the figure: those a
// leader points at, and the continuation lines of those.
func claimLabels(area CellRect, text []TextRun, marks []CellRect, side int, g figureGuards) []*TextRun {
	var claimed []*TextRun
	for i := range text {
		r := &text[i]
		gap, outside := runBeyond(area, r, side)
		if !outside || gap > g.corridor {
			continue
		}
		if terminatorAt(marks, area, r, side, g) {
			claimed = append(claimed, r)
		}
	}
	if len(claimed) == 0 {
		return nil
	}
	// A wrapped label's later lines, to a fixpoint: a third line continues a second
	// that was itself only just claimed.
	for again := true; again; {
		again = false
		for i := range text {
			r := &text[i]
			gap, outside := runBeyond(area, r, side)
			if !outside || gap > g.corridor || claims(claimed, r) {
				continue
			}
			if len([]rune(strings.TrimSpace(r.Text))) < minWrapRunes {
				continue
			}
			if continuesLabel(claimed, r, side, text, area) {
				claimed = append(claimed, r)
				again = true
			}
		}
	}
	return claimed
}

// labelExtent is how far the edge may move: the farthest claimed label whose
// growth region holds nothing but claimed labels.
func labelExtent(area CellRect, text []TextRun, claimed []*TextRun, side int) (float64, bool) {
	extents := make([]float64, 0, len(claimed))
	for _, r := range claimed {
		extents = append(extents, farEdge(r, side))
	}
	sort.Float64s(extents)
	if side == edgeRight || side == edgeBottom {
		slices.Reverse(extents)
	}
	for _, e := range extents {
		if !outward(area, side, e) {
			continue // already inside the box, from another edge's growth
		}
		if unclaimedRun(growthRegion(area, side, e), text, claimed) == nil {
			return e, true
		}
	}
	return 0, false
}

// The four edges, in the order [growToLabels] takes them.
const (
	edgeLeft = iota
	edgeRight
	edgeTop
	edgeBottom
)

// minWrapRunes is the shortest run that may be claimed as a label's next line.
// Three: "колесо" and "щетки" are continuation lines on page 521 and a bullet's
// "•" is not, and the floor is the second lock on that after [continuesLabel]'s
// own test. A claim by terminator has no floor, because a callout number is one
// character.
const minWrapRunes = 3

// runBeyond reports whether a run lies wholly beyond one edge — within the band the
// figure occupies on the other axis — and by how far.
func runBeyond(area CellRect, r *TextRun, side int) (gap float64, ok bool) {
	switch side {
	case edgeLeft:
		if r.right() <= area.X0 && r.bottom() > area.Y0 && r.Y < area.Y1 {
			return area.X0 - r.right(), true
		}
	case edgeRight:
		if r.X >= area.X1 && r.bottom() > area.Y0 && r.Y < area.Y1 {
			return r.X - area.X1, true
		}
	case edgeTop:
		if r.bottom() <= area.Y0 && r.right() > area.X0 && r.X < area.X1 {
			return area.Y0 - r.bottom(), true
		}
	case edgeBottom:
		if r.Y >= area.Y1 && r.right() > area.X0 && r.X < area.X1 {
			return r.Y - area.Y1, true
		}
	}
	return 0, false
}

// terminatorAt reports whether a leader's end mark sits between the figure's edge
// and this run, on the run's own midline.
//
// The mark may be just inside the edge or out in the corridor, and both happen in
// one document: page 521's lidar drawing ends AT its terminators, which is what
// sets its box edge, while its underside drawing's marks are 28 units outside the
// box because the leader lines running to them are perfectly horizontal and a
// horizontal hairline has no height, so [onPageInk] never saw them. See the note
// at the end of this file.
func terminatorAt(marks []CellRect, area CellRect, r *TextRun, side int, g figureGuards) bool {
	midX, midY := r.X+r.Width/2, r.Y+r.Height/2
	for _, s := range marks {
		cx, cy := (s.X0+s.X1)/2, (s.Y0+s.Y1)/2
		switch side {
		case edgeLeft:
			if cx <= area.X0+g.terminator && cx >= r.right()-g.terminator &&
				math.Abs(cy-midY) <= g.align {
				return true
			}
		case edgeRight:
			if cx >= area.X1-g.terminator && cx <= r.X+g.terminator &&
				math.Abs(cy-midY) <= g.align {
				return true
			}
		case edgeTop:
			if cy <= area.Y0+g.terminator && cy >= r.bottom()-g.terminator &&
				math.Abs(cx-midX) <= g.align {
				return true
			}
		case edgeBottom:
			if cy >= area.Y1-g.terminator && cy <= r.Y+g.terminator &&
				math.Abs(cx-midX) <= g.align {
				return true
			}
		}
	}
	return false
}

// continuesLabel reports whether r is the next line of a label already claimed: set
// flush with it, on the adjacent line, and alone on its own baseline.
func continuesLabel(claimed []*TextRun, r *TextRun, side int, text []TextRun, area CellRect) bool {
	const (
		// flush is how far two lines of one label's near edges may differ. Three:
		// page 521 sets "Модуль" and "MopExtend" against a right margin two units
		// apart, because the glyphs do not end at the same place.
		flush = 3.0
		// step is how far apart two lines of one label may sit. Six: a run is taller
		// than the pitch it is set at, so consecutive lines of these labels overlap
		// rather than leaving a gap, and this bounds the case where they do not.
		step = 6.0
		// beside is how near another run must be to count as sharing this line. A
		// bullet's text starts 1 unit after it; the next label column on page 521
		// starts 147 units away and must not count, or a three-line label is blocked
		// by a run it has nothing to do with.
		beside = 12.0
		// sameLine compares BASELINES, not bands. Two consecutive lines of one label
		// overlap vertically, so a band test reports a label's own third line as
		// something sharing the second's line, which blocked every growth on the page
		// this pass was written for.
		sameLine = 2.0
	)
	for _, c := range claimed {
		if math.Abs(nearEdge(r, side)-nearEdge(c, side)) > flush {
			continue
		}
		var apart float64
		if side == edgeLeft || side == edgeRight {
			apart = math.Max(r.Y-c.bottom(), c.Y-r.bottom())
		} else {
			apart = math.Max(r.X-c.right(), c.X-r.right())
		}
		if apart > step {
			continue
		}
		alone := true
		for i := range text {
			o := &text[i]
			if o == r || strings.TrimSpace(o.Text) == "" || claims(claimed, o) {
				continue
			}
			if _, outside := runBeyond(area, o, side); !outside {
				continue
			}
			var shares, near bool
			if side == edgeLeft || side == edgeRight {
				shares = math.Abs(o.Y-r.Y) <= sameLine
				near = o.X < r.right()+beside && r.X < o.right()+beside
			} else {
				shares = math.Abs(o.X-r.X) <= sameLine
				near = o.Y < r.bottom()+beside && r.Y < o.bottom()+beside
			}
			if shares && near {
				alone = false
				break
			}
		}
		if alone {
			return true
		}
	}
	return false
}

// claims reports whether a run is already claimed. By identity: two runs of a page
// can hold the same text at the same size, and it is this one that is claimed.
func claims(claimed []*TextRun, r *TextRun) bool {
	return slices.Contains(claimed, r)
}

// nearEdge is the run's side facing the figure, farEdge the side away from it.
func nearEdge(r *TextRun, side int) float64 {
	switch side {
	case edgeLeft:
		return r.right()
	case edgeRight:
		return r.X
	case edgeTop:
		return r.bottom()
	default:
		return r.Y
	}
}

func farEdge(r *TextRun, side int) float64 {
	switch side {
	case edgeLeft:
		return r.X
	case edgeRight:
		return r.right()
	case edgeTop:
		return r.Y
	default:
		return r.bottom()
	}
}

// growthRegion is the strip an edge would add by moving out to want.
func growthRegion(area CellRect, side int, want float64) CellRect {
	switch side {
	case edgeLeft:
		return CellRect{want, area.Y0, area.X0, area.Y1}
	case edgeRight:
		return CellRect{area.X1, area.Y0, want, area.Y1}
	case edgeTop:
		return CellRect{area.X0, want, area.X1, area.Y0}
	default:
		return CellRect{area.X0, area.Y1, area.X1, want}
	}
}

// unclaimedRun is the first run inside a region that no label claimed, or nil when
// the region holds nothing else.
func unclaimedRun(region CellRect, text []TextRun, claimed []*TextRun) *TextRun {
	for i := range text {
		r := &text[i]
		if strings.TrimSpace(r.Text) == "" || claims(claimed, r) {
			continue
		}
		if math.Min(region.X1, r.right()) > math.Max(region.X0, r.X) &&
			math.Min(region.Y1, r.bottom()) > math.Max(region.Y0, r.Y) {
			return r
		}
	}
	return nil
}

// outward reports whether want is further out than the edge already is.
func outward(area CellRect, side int, want float64) bool {
	switch side {
	case edgeLeft:
		return want < area.X0
	case edgeRight:
		return want > area.X1
	case edgeTop:
		return want < area.Y0
	default:
		return want > area.Y1
	}
}

func edgeMove(area CellRect, side int, want float64) float64 {
	switch side {
	case edgeLeft:
		return area.X0 - want
	case edgeRight:
		return want - area.X1
	case edgeTop:
		return area.Y0 - want
	default:
		return want - area.Y1
	}
}

func edgeSpan(area CellRect, side int) float64 {
	if side == edgeLeft || side == edgeRight {
		return area.Width()
	}
	return area.Height()
}

func moveEdge(area CellRect, side int, want float64) CellRect {
	switch side {
	case edgeLeft:
		area.X0 = want
	case edgeRight:
		area.X1 = want
	case edgeTop:
		area.Y0 = want
	default:
		area.Y1 = want
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
// **A picture can still be cut by a caption printed over its artwork.** Not by its
// own labels any more: [trimToPicture] leaves a line the artwork encloses alone, and
// page 16 figure 2 of the columns manual — the case that used to lose its right
// third to the label »click« — now returns whole, which took that document from 15
// figures cut to 3. What is left is the opposite arrangement, where the drawing
// really does run under the text: page 1's cover art continues behind the title
// block, so excluding the titles cuts it, and that figure is one of the 3. Nothing
// here can have both, because both are one rectangle.
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
