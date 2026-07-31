package verify_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/verify"
)

// Every check gets two tests: one proving it fires on a real fault and one
// proving it stays quiet on correct input. A check that cannot be made to fire is
// worse than no check, and one that fires on everything is the same thing with
// more output — so both halves are asserted for all five.
//
// All of it is hermetic. The input is hand-built, so these run in the default
// suite with no fixture, no poppler and no network, which is what lets them guard
// the checks in CI. The fixture-backed tests measure the same code against the two
// real manuals; see verify_fixture_test.go.

// page builds one page of pdftotext's reading.
func page(no int, text string) doc.Page {
	return doc.Page{No: no, Text: text, Chars: len([]rune(text))}
}

// block builds one converted block. Chars is derived rather than passed, because
// two of the checks read it and a test that set it inconsistently would be
// asserting on a block that cannot exist.
func block(pg, idx int, x0, x1, y0 float64, text string) doc.Block {
	return doc.Block{
		Page: pg, Index: idx, Kind: doc.BlockParagraph, Text: text,
		X0: x0, X1: x1, Y0: y0, Y1: y0 + 12,
		Chars: len([]rune(text)), Lines: 1,
	}
}

func count(t *testing.T, in verify.Input, k verify.Kind) int {
	t.Helper()
	return verify.Inspect(in).Count(k)
}

// --- 1. coverage

const prose = "Der Gehäusedeckel wird abgenommen und der Filter herausgezogen. " +
	"Anschließend den Frischwassertank mit klarem Wasser ausspülen."

func TestCoverageFiresWhenAPageLosesText(t *testing.T) {
	// Half of the page's text never became a block, which is what dropping a
	// column of a page looks like from outside.
	half := prose[:len(prose)/2]
	in := verify.Input{
		Blocks: []doc.Block{block(7, 0, 40, 300, 100, half)},
		Text:   []doc.Page{page(7, prose)},
	}
	rep := verify.Inspect(in)
	if got := rep.Count(verify.KindCoverage); got != 1 {
		t.Fatalf("want one coverage finding, got %d: %+v", got, rep.Findings)
	}
	f := rep.Findings[0]
	if f.Page != 7 || f.Got >= f.Want || f.Total <= f.Count {
		t.Errorf("finding does not carry the numbers behind it: %+v", f)
	}
	if len(rep.Coverage) != 1 || rep.Coverage[0].Ratio <= 0 {
		t.Errorf("the measurement was not kept: %+v", rep.Coverage)
	}
}

func TestCoverageQuietWhenThePageIsWhole(t *testing.T) {
	in := verify.Input{
		Blocks: []doc.Block{block(7, 0, 40, 300, 100, prose)},
		Text:   []doc.Page{page(7, prose)},
	}
	if got := count(t, in, verify.KindCoverage); got != 0 {
		t.Fatalf("want no coverage finding, got %d", got)
	}
}

func TestCoverageIgnoresAPageWithAlmostNoText(t *testing.T) {
	// A folio and a language badge are a page's whole text on 34 pages of the
	// sequential manual, and their ratio means nothing.
	in := verify.Input{
		Blocks: []doc.Block{block(7, 0, 40, 60, 800, "18")},
		Text:   []doc.Page{page(7, "DE 18")},
	}
	if got := count(t, in, verify.KindCoverage); got != 0 {
		t.Fatalf("a page of furniture was judged for coverage: %d", got)
	}
}

// TestCoverageDoesNotCountPageFurniture is what makes this check able to refute
// doc's furniture rule, and it is the whole reason the exclusion is deliberate
// rather than an oversight.
//
// The furniture the rule claims really is printed, so `pdftotext` reports it and
// counting it would leave every ratio exactly where it was. Counting it would also
// make a rule that wrongly claims a paragraph invisible here — the paragraph would
// still be in the sum. So a page whose whole text is flagged reads as a page that
// dropped its whole text, which is what a coverage finding is for.
func TestCoverageDoesNotCountPageFurniture(t *testing.T) {
	claimed := block(7, 0, 40, 300, 100, prose)
	claimed.Furniture = true
	in := verify.Input{
		Blocks: []doc.Block{claimed},
		Text:   []doc.Page{page(7, prose)},
	}
	if got := count(t, in, verify.KindCoverage); got != 1 {
		t.Fatalf("a page whose only block is claimed as furniture reported %d coverage "+
			"finding(s); counting furniture would hide a rule that eats a paragraph", got)
	}

	// And the same block unflagged is the page being whole, which is the control:
	// the finding above is the flag and not the text.
	in.Blocks[0].Furniture = false
	if got := count(t, in, verify.KindCoverage); got != 0 {
		t.Fatalf("the same block unflagged reported %d coverage finding(s)", got)
	}
}

// --- 2. invented text

func TestInventedTextFiresOnWordsThePageNeverPrinted(t *testing.T) {
	in := verify.Input{
		Blocks: []doc.Block{block(62, 3, 43, 443, 400,
			"Verpackung schützt Beanspruchung gleichzusetzender")},
		Text: []doc.Page{page(62, "Die Verpackung schützt das Gerät.")},
	}
	rep := verify.Inspect(in)
	if got := rep.Count(verify.KindInvented); got != 1 {
		t.Fatalf("want one invented-text finding, got %d: %+v", got, rep.Findings)
	}
	f := rep.Findings[0]
	if f.Count != 2 || f.Total != 4 {
		t.Errorf("want 2 of 4 words absent, got %d of %d", f.Count, f.Total)
	}
	if !strings.Contains(f.Sample, "beanspruchung") {
		t.Errorf("the sample does not name the absent words: %q", f.Sample)
	}
}

func TestInventedTextQuietWhenEveryWordIsPrinted(t *testing.T) {
	in := verify.Input{
		Blocks: []doc.Block{block(62, 3, 43, 443, 400, "Die Verpackung schützt das Gerät")},
		Text:   []doc.Page{page(62, "Die Verpackung schützt das Gerät.")},
	}
	if got := count(t, in, verify.KindInvented); got != 0 {
		t.Fatalf("want no invented-text finding, got %d", got)
	}
}

func TestInventedTextToleratesOneOddWordInALongBlock(t *testing.T) {
	// The two extractions disagree about a ligature or a soft hyphen from time to
	// time, measured at 0.45% of the sequential manual's words, so one absence in
	// a long block is not a finding.
	in := verify.Input{
		Blocks: []doc.Block{block(62, 3, 43, 443, 400,
			"Die Verpackung schützt das Gerät gegen Transportschäden xyzzy")},
		Text: []doc.Page{page(62, "Die Verpackung schützt das Gerät gegen Transportschäden.")},
	}
	if got := count(t, in, verify.KindInvented); got != 0 {
		t.Fatalf("one absent word in eight was reported: %d", got)
	}
}

// --- 2b. right to left, which is a known defect and must stay one finding

// rtlEmbed and popDirectional are the bidi controls pdftotext wraps a
// right-to-left line in, written as escapes because they are invisible: a test
// whose input cannot be seen in the source is a test nobody can check.
const (
	rtlEmbed       = "\u202b"
	popDirectional = "\u202c"
)

func TestRightToLeftIsOneNamedFindingPerPage(t *testing.T) {
	// pdftohtml returns the line in visual order; pdftotext returns it logically,
	// wrapped in bidi controls. So every word of the block is absent, and every one
	// of them is present reversed — which is the finding's evidence.
	printed := rtlEmbed + "הגבלות שימוש על המכשיר" + popDirectional
	visual := "שומיש תולבגה רישכמה לע"
	in := verify.Input{
		Blocks: []doc.Block{block(185, 0, 55, 800, 95, visual)},
		Text:   []doc.Page{page(185, printed)},
	}
	rep := verify.Inspect(in)
	if got := rep.Count(verify.KindRightToLeft); got != 1 {
		t.Fatalf("want one right-to-left finding, got %d: %+v", got, rep.Findings)
	}
	if got := rep.Count(verify.KindInvented); got != 0 {
		t.Errorf("a right-to-left page also reported %d generic invented-text findings, "+
			"which is what naming the defect is meant to prevent", got)
	}
	f := rep.Findings[0]
	if f.Count != 4 || f.Got != 4 {
		t.Errorf("want 4 absent words all 4 reversible, got %d absent and %.0f reversible",
			f.Count, f.Got)
	}
}

// TestRightToLeftNeedsAReversalAndNotJustHebrew is the sharpening the bidi repair
// forced, and it is the half of the check that measured worst: for as long as every
// Hebrew page arrived backwards, "is this page right to left" and "is this page
// reversed" were the same question, and once doc/bidi.go split them the check kept
// answering the first while claiming the second. On the sequential manual that was
// 25 pages reported over 220 absent words in 6,834, three of them on one page of
// 510 — see [verify.minReversibleWords].
//
// Here the page is Hebrew and correctly ordered, and one word of it disagrees with
// the reference the way the two extractions ordinarily do. Nothing about it is
// backwards, so it is not a right-to-left finding; it is judged block by block like
// any other page.
func TestRightToLeftNeedsAReversalAndNotJustHebrew(t *testing.T) {
	printed := rtlEmbed + "הגבלות שימוש על המכשיר בטמפרטורה" + popDirectional
	in := verify.Input{
		// "בטמפרטורה" against the printed "בטמפרטורה" — one word the reference
		// spells differently, which is what a combining mark or a shaping difference
		// looks like. Its reverse is nowhere on the page.
		Blocks: []doc.Block{block(185, 0, 55, 800, 95, "הגבלות שימוש על המכשיר בטמפרטורת")},
		Text:   []doc.Page{page(185, printed)},
	}
	rep := verify.Inspect(in)
	if got := rep.Count(verify.KindRightToLeft); got != 0 {
		t.Fatalf("a Hebrew page with no reversed word on it was named "+
			"right-to-left-reversed: %+v", rep.Findings)
	}
	// One absent word in five is under maxInventedShare, so the block check is quiet
	// too — which is the point: the page is fine and the report says nothing.
	if got := rep.Count(verify.KindInvented); got != 0 {
		t.Errorf("invented text reported %d block(s) on one ordinary disagreement", got)
	}

	// The same page with a block that really is assembled wrong falls through to the
	// block check rather than disappearing, so nothing is hidden by the sharpening.
	in.Blocks = []doc.Block{block(185, 0, 55, 800, 95, "אבגד הוזח חטיכ למנס")}
	if got := verify.Inspect(in).Count(verify.KindInvented); got != 1 {
		t.Errorf("a right-to-left block full of words the page never printed reported "+
			"%d invented-text finding(s), want 1", got)
	}
}

func TestRightToLeftQuietWhenTheOrderIsRight(t *testing.T) {
	printed := rtlEmbed + "הגבלות שימוש על המכשיר" + popDirectional
	in := verify.Input{
		Blocks: []doc.Block{block(185, 0, 55, 800, 95, "הגבלות שימוש על המכשיר")},
		Text:   []doc.Page{page(185, printed)},
	}
	rep := verify.Inspect(in)
	if got := rep.Count(verify.KindRightToLeft); got != 0 {
		t.Fatalf("a correctly ordered Hebrew page reported %d findings: %+v",
			got, rep.Findings)
	}
	if got := rep.Count(verify.KindInvented); got != 0 {
		t.Fatalf("a correctly ordered Hebrew page reported %d invented-text findings", got)
	}
}

// --- 3. suspicious joins

func TestJoinsFireOnEachShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want verify.Kind
	}{
		{"a hyphen followed by a space mid-word", "Der Gehäusede- ckel wird abgenommen",
			verify.KindJoinHyphen},
		{"two words glued together", "Der Filter derDüse wird gereinigt",
			verify.KindJoinGlued},
		{"a doubled space", "Der Filter  wird gereinigt", verify.KindJoinSpace},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := verify.Input{
				Blocks: []doc.Block{block(4, 0, 43, 443, 200, tc.text)},
				Text: []doc.Page{page(4, "Der Gehäusedeckel wird abgenommen. "+
					"Der Filter der Düse wird gereinigt.")},
			}
			rep := verify.Inspect(in)
			if got := rep.Count(tc.want); got != 1 {
				t.Fatalf("want one %s finding, got %d: %+v", tc.want, got, rep.Findings)
			}
		})
	}
}

func TestJoinsQuietOnCleanText(t *testing.T) {
	// A dash used as punctuation and no doubled space. Two of these hang directly
	// off a letter — "230V- 50" and "Typ M- Amfibia" — which is what makes them the
	// case the shape gets wrong: only the digit and the capital after the space say
	// they are not a broken word.
	const clean = "Spannungsversorgung: 230V- 50 Hz, Typ M- Amfibia, Modell 788/M - 2024"
	in := verify.Input{
		Blocks: []doc.Block{block(4, 0, 43, 443, 200, clean)},
		Text:   []doc.Page{page(4, clean)},
	}
	rep := verify.Inspect(in)
	for _, k := range []verify.Kind{verify.KindJoinHyphen, verify.KindJoinGlued,
		verify.KindJoinSpace} {
		if got := rep.Count(k); got != 0 {
			t.Errorf("%s fired on clean text: %+v", k, rep.Findings)
		}
	}
}

// --- 4. figure geometry, which is two faults

// figurePNG renders a white image with a black rectangle in it, which is enough to
// stand in for a line drawing: the check reads where the paint is, not what it
// draws.
func figurePNG(t *testing.T, w, h int, painted image.Rectangle) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.White)
		}
	}
	for y := painted.Min.Y; y < painted.Max.Y; y++ {
		for x := painted.Min.X; x < painted.Max.X; x++ {
			img.Set(x, y, color.Black)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

func figure(t *testing.T, rect doc.CellRect, painted image.Rectangle) doc.ConvertedFigure {
	t.Helper()
	pw := int(rect.Width() * 2)
	ph := int(rect.Height() * 2)
	return doc.ConvertedFigure{Figure: doc.Figure{
		Page: 14, Index: 0, Rect: rect, DPI: 216,
		PixelWidth: pw, PixelHeight: ph, Ink: 40,
		PNG: figurePNG(t, pw, ph, painted),
	}}
}

func TestFigureBandFiresOnARenderThatIsMostlyMargin(t *testing.T) {
	// A 100x100 box rendered at 200x200 pixels, painted only below y=100 — which is
	// 50 units of blank band at the top, the fault the user reports.
	fig := figure(t, doc.CellRect{X0: 43, Y0: 241, X1: 143, Y1: 341},
		image.Rect(0, 100, 200, 200))
	rep := verify.Inspect(verify.Input{Figures: []doc.ConvertedFigure{fig}})
	if got := rep.Count(verify.KindFigureBand); got != 1 {
		t.Fatalf("want one blank-band finding, got %d: %+v", got, rep.Findings)
	}
	if f := rep.Findings[0]; f.Got < 49 || f.Got > 51 {
		t.Errorf("want a band of about 50 units, got %.1f", f.Got)
	}
}

func TestFigureBandQuietWhenThePictureFillsItsBox(t *testing.T) {
	fig := figure(t, doc.CellRect{X0: 43, Y0: 241, X1: 143, Y1: 341},
		image.Rect(0, 0, 200, 200))
	if got := count(t, verify.Input{Figures: []doc.ConvertedFigure{fig}},
		verify.KindFigureBand); got != 0 {
		t.Fatalf("want no blank-band finding, got %d", got)
	}
}

func TestFigureClippedFiresWhenAShapeCrossesTheBox(t *testing.T) {
	box := doc.CellRect{X0: 0, Y0: 0, X1: 100, Y1: 100}
	fig := figure(t, box, image.Rect(0, 0, 200, 200))
	in := verify.Input{
		Figures: []doc.ConvertedFigure{fig},
		Ink: map[int][]doc.Ink{14: {
			{Rect: doc.CellRect{X0: 10, Y0: 10, X1: 90, Y1: 90}},
			{Rect: doc.CellRect{X0: 50, Y0: 40, X1: 150, Y1: 60}, Stroked: true},
		}},
	}
	rep := verify.Inspect(in)
	if got := rep.Count(verify.KindFigureClipped); got != 1 {
		t.Fatalf("want one clipped finding, got %d: %+v", got, rep.Findings)
	}
	if f := rep.Findings[0]; f.Count != 1 || f.Total != 2 || f.Got < 49 {
		t.Errorf("want 1 of 2 shapes crossing by about 50 units, got %d of %d by %.0f",
			f.Count, f.Total, f.Got)
	}
}

func TestFigureClippedQuietWhenEveryShapeIsInside(t *testing.T) {
	box := doc.CellRect{X0: 0, Y0: 0, X1: 100, Y1: 100}
	fig := figure(t, box, image.Rect(0, 0, 200, 200))
	in := verify.Input{
		Figures: []doc.ConvertedFigure{fig},
		Ink: map[int][]doc.Ink{14: {
			{Rect: doc.CellRect{X0: 10, Y0: 10, X1: 90, Y1: 90}},
			// A horizontal rule: zero height, and inside. It must not read as
			// crossing, which an area comparison would make it.
			{Rect: doc.CellRect{X0: 10, Y0: 50, X1: 90, Y1: 50}},
			// A page-sized background path, mostly outside: not this figure's.
			{Rect: doc.CellRect{X0: -500, Y0: -500, X1: 900, Y1: 900}},
		}},
	}
	if got := count(t, in, verify.KindFigureClipped); got != 0 {
		t.Fatalf("want no clipped finding, got %d", got)
	}
}

// --- 5. reading order

func TestReadingOrderFiresOnInterleavedColumns(t *testing.T) {
	// The page-62 failure conversion.md describes: two columns read line by line,
	// so the second block is in the other column and no higher up the page.
	left := "Die Verpackung schützt das Gerät gegen Transportschäden"
	right := "Gerät Garantie gemäß nachstehenden Bedingungen"
	in := verify.Input{Blocks: []doc.Block{
		block(62, 0, 43, 443, 100, left),
		block(62, 1, 463, 863, 118, right),
	}}
	rep := verify.Inspect(in)
	if got := rep.Count(verify.KindReadingOrder); got != 1 {
		t.Fatalf("want one reading-order finding, got %d: %+v", got, rep.Findings)
	}
	if f := rep.Findings[0]; f.Index != 1 || f.Got < f.Want {
		t.Errorf("finding does not carry the two positions: %+v", f)
	}
}

func TestReadingOrderQuietWhenColumnsAreReadInTurn(t *testing.T) {
	// Correct output: down the left column, then back up to the top of the right
	// one. Going back up is right, and must not be reported.
	in := verify.Input{Blocks: []doc.Block{
		block(62, 0, 43, 443, 100, "Die Verpackung schützt das Gerät gegen Transport"),
		block(62, 1, 43, 443, 300, "Bitte entsorgen Sie das Material umweltgerecht"),
		block(62, 2, 463, 863, 100, "Gerät Garantie gemäß nachstehenden Bedingungen"),
		block(62, 3, 463, 863, 300, "Bei gewerblicher Benutzung oder gleichzusetzender"),
	}}
	rep := verify.Inspect(in)
	if got := rep.Count(verify.KindReadingOrder); got != 0 {
		t.Fatalf("correct column order reported %d findings: %+v", got, rep.Findings)
	}
}

func TestReadingOrderIgnoresTableCellsAndFurniture(t *testing.T) {
	// A table is read row-major on purpose, which is this check's violation shape,
	// and a two-letter language badge below a heading is the same shape again.
	cells := []doc.Block{
		{Page: 57, Index: 0, Kind: doc.BlockTable, Text: "Gerät saugt nicht", Chars: 17,
			X0: 30, X1: 173, Y0: 100, Y1: 130},
		{Page: 57, Index: 1, Kind: doc.BlockTable, Text: "Filter reinigen und wieder einsetzen",
			Chars: 36, X0: 200, X1: 428, Y0: 100, Y1: 130},
	}
	badge := []doc.Block{
		block(24, 0, 55, 243, 52, "Routine Maintenance"),
		block(24, 1, 27, 41, 58, "DE"),
		block(24, 2, 55, 813, 95, "Die Wartung erfolgt in den beschriebenen Abständen"),
	}
	in := verify.Input{Blocks: append(cells, badge...)}
	rep := verify.Inspect(in)
	if got := rep.Count(verify.KindReadingOrder); got != 0 {
		t.Fatalf("table cells or page furniture were reported: %+v", rep.Findings)
	}
}

// --- the report itself

func TestReportSaysWhatItCouldNotCheck(t *testing.T) {
	// No pdftotext reading and no ink: the checks that need them are skipped and
	// said to be skipped, rather than passing silently.
	in := verify.Input{
		Blocks:  []doc.Block{block(1, 0, 40, 300, 100, prose)},
		Figures: []doc.ConvertedFigure{{Figure: doc.Figure{Page: 1}}},
	}
	rep := verify.Inspect(in)
	if len(rep.Notes) != 3 {
		t.Fatalf("want three notes, got %v", rep.Notes)
	}
	joined := strings.Join(rep.Notes, " | ")
	for _, want := range []string{"pdftotext", "ink", "rendered bytes"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no note about %s: %v", want, rep.Notes)
		}
	}
	if rep.Count(verify.KindCoverage) != 0 {
		t.Error("coverage was judged with nothing to judge it against")
	}
}

func TestSummaryCountsEveryKind(t *testing.T) {
	in := verify.Input{
		Blocks: []doc.Block{block(7, 0, 40, 300, 100, prose[:len(prose)/2])},
		Text:   []doc.Page{page(7, prose)},
	}
	rep := verify.Inspect(in)
	s := rep.Summary()
	for _, want := range []string{"1 finding(s)", string(verify.KindCoverage), "median coverage"} {
		if !strings.Contains(s, want) {
			t.Errorf("Summary() = %q, want it to mention %q", s, want)
		}
	}
}
