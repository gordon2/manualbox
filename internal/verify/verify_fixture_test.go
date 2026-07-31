package verify_test

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/extern"
	"github.com/gordon2/manualbox/internal/fixture"
	"github.com/gordon2/manualbox/internal/verify"
)

// These run every check over the two real manuals, and their log IS the report:
// the counts per kind, the coverage distribution, and a sample of each finding.
// The assertions pin what was measured while the thresholds were chosen, so that a
// change in `doc` that alters any of it fails here with the number that moved.
//
// The documents are fetched on demand and are not committed. Without
// MANUALBOX_TEST_FIXTURES=1 these skip, so the default suite stays hermetic.

const fixturesDir = "../../testdata/fixtures"

// checked converts a fixture for every language it holds and verifies it. Every
// language, not one household's, for the reason [verify.Check] gives: a page of
// the column manual holds five languages and coverage is measured against all the
// text on the page.
func checked(t *testing.T, name string) (*doc.Conversion, *verify.Report) {
	t.Helper()
	if os.Getenv(fixture.EnableEnv) == "" {
		t.Skipf("set %s=1 to download the fixtures and run the real-document tests",
			fixture.EnableEnv)
	}
	for _, tool := range []extern.Tool{extern.PDFInfo, extern.PDFToText, extern.PDFToHTML,
		extern.PDFToCairo, extern.PDFToPPM} {
		if !extern.Available(tool) {
			t.Skipf("%s is not installed", tool.Name)
		}
	}
	m, err := fixture.Load(fixturesDir, name)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	path, err := m.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch fixture: %v", err)
	}

	ctx := context.Background()
	res, err := doc.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	start := time.Now()
	conv, err := verify.ConvertAll(ctx, path, res)
	if err != nil {
		t.Fatalf("ConvertAll: %v", err)
	}
	t.Logf("%v converting every language: %s", time.Since(start).Round(time.Millisecond),
		conv.Summary())

	start = time.Now()
	rep, err := verify.Check(ctx, path, conv)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	t.Logf("%v checking: %s", time.Since(start).Round(time.Millisecond), rep.Summary())
	report(t, rep)
	return conv, rep
}

// report logs everything one pass found, which is the point of these tests.
func report(t *testing.T, rep *verify.Report) {
	t.Helper()
	for _, n := range rep.Notes {
		t.Logf("  note: %s", n)
	}
	kinds := rep.Kinds()
	for _, k := range verify.AllKinds {
		if kinds[k] > 0 {
			t.Logf("  %-24s %4d finding(s) over %d page(s)", k, kinds[k], rep.PagesFlagged(k))
		}
	}

	cov := make([]verify.PageCoverage, len(rep.Coverage))
	copy(cov, rep.Coverage)
	sort.Slice(cov, func(a, b int) bool { return cov[a].Ratio < cov[b].Ratio })
	t.Logf("  median coverage %.3f", rep.MedianCoverage())
	for i := 0; i < len(cov) && i < 6; i++ {
		t.Logf("    least covered: page %d at %.3f (%d block characters against %d)",
			cov[i].Page, cov[i].Ratio, cov[i].Blocks, cov[i].Text)
	}

	// Up to three examples of each kind, so the log is readable on a document that
	// reports a thousand findings.
	shown := make(map[verify.Kind]int, len(kinds))
	for i := range rep.Findings {
		f := &rep.Findings[i]
		if shown[f.Kind] >= 3 {
			continue
		}
		shown[f.Kind]++
		t.Logf("    %s", f.Detail)
		if f.Sample != "" {
			t.Logf("      %s", f.Sample)
		}
	}
}

// figurePages is how many distinct pages carry a figure. Counted rather than
// derived from the figure count because the two move independently: a geometry
// change that splits one drawing into two raises the figures and not the pages,
// while one that admits page furniture raises both.
func figurePages(conv *doc.Conversion) int {
	seen := make(map[int]bool, len(conv.Figures))
	for i := range conv.Figures {
		seen[conv.Figures[i].Page] = true
	}
	return len(seen)
}

// TestCheckTheColumnManual is the parallel-columns fixture: 68 pages, five
// languages sharing most of them, 59 figures.
func TestCheckTheColumnManual(t *testing.T) {
	conv, rep := checked(t, "thomas-drybox-amfibia")

	// 2,336 blocks, of which 111 are page furniture — the three language tabs this
	// manual prints in its columns, and 41 folios. It was 2,180 before doc's
	// furniture pass existed, and the rise of 76 to 2,256 was not text appearing: 35
	// content blocks lost a tab that was glued to them and 111 furniture blocks took
	// its place. Counted apart because a change to the furniture rule must move the
	// second number and not the first.
	//
	// 2,336 since the contents pages came apart. This document prints its table of
	// contents once per language, 17 entries each, and each was one run-together
	// block of dot leaders: +16 per language over five languages is exactly the 80.
	// Coverage did not move — the dots are still in the text, only grouped
	// differently — and neither did the figures.
	if len(conv.Blocks) != 2336 || len(conv.Figures) != 59 {
		t.Errorf("the conversion under test moved: %d blocks and %d figures, "+
			"was 2336 and 59 (2256 before the contents pages came apart)",
			len(conv.Blocks), len(conv.Figures))
	}
	if got := len(conv.FurnitureBlocks()); got != 111 {
		t.Errorf("%d furniture block(s), was 111", got)
	}

	// No page loses text. The lowest score is page 5 at 0.80, which is a page of
	// framed illustrations whose captions the run filter drops, and the median is
	// 0.97 — so the floor of 0.75 leaves headroom and reports nothing here.
	//
	// Coverage now excludes the furniture it counted before, and on this manual that
	// costs almost nothing: the median moved from 0.974 to 0.973 and the floor stayed
	// at 0.801, because a tab and a folio are four characters against a page of three
	// thousand. checkCoverage records why it is excluded anyway.
	if got := rep.Count(verify.KindCoverage); got != 0 {
		t.Errorf("coverage reported %d page(s) on a manual that drops none", got)
	}
	if m := rep.MedianCoverage(); m < 0.95 || m > 1.0 {
		t.Errorf("median coverage %.3f, was 0.973 (0.974 before furniture was excluded)", m)
	}

	// Four blocks hold words the page never printed, and all four are table cells
	// where the two tools disagree about where a Cyrillic or Kazakh word divides.
	if got := rep.Count(verify.KindInvented); got != 4 {
		t.Errorf("invented text: %d finding(s), was 4", got)
	}
	// Nothing here reads right to left.
	if got := rep.Count(verify.KindRightToLeft); got != 0 {
		t.Errorf("right-to-left: %d finding(s) on a manual with no such script", got)
	}

	// Hyphenation is deliberate in doc, and this is its cost: 276 blocks carrying
	// 313 hyphens followed by a space. Reported, not fixed.
	if got := rep.Count(verify.KindJoinHyphen); got != 276 {
		t.Errorf("hyphen joins: %d block(s), was 276", got)
	}
	if got := rep.Count(verify.KindJoinGlued) + rep.Count(verify.KindJoinSpace); got != 0 {
		t.Errorf("glued words or doubled spaces: %d, was 0", got)
	}

	// The figure geometry, in two steps. Both of these were the clip-path limitation
	// conversion.md recorded; reading the clip took them from 4 blank bands and 22
	// clipped to 0 and 15 while the figure count rose from 46 to 59, and narrowing
	// trimToPicture to lines the box had reached over took the 15 to 3.
	//
	// Zero is asserted on the band because that check reads the RENDERED PIXELS and
	// so is independent of the geometry that produced them: it is the one number
	// here that cannot improve by the box and the ink agreeing with each other.
	if got := rep.Count(verify.KindFigureBand); got != 0 {
		t.Errorf("blank bands: %d figure(s), was 4 before the clip and 0 after", got)
	}
	// The 3 that remain are three different things and none is the trim cutting a
	// drawing away from its own label. Pages 11 and 12 report one and two shapes
	// crossing out of 2,741 — a page-sized path this package's geometric matching
	// cannot attribute, and the same 2 that stand with trimming switched off
	// entirely. Page 1 is the cover, whose art really does continue behind the
	// title block the trim excludes; that one is a genuine trade and it is taken
	// deliberately, because the alternative is a cover crop full of headline type.
	if got := rep.Count(verify.KindFigureClipped); got != 3 {
		t.Errorf("clipped figures: %d of 59, was 22 of 46 before the clip and 15 "+
			"while the trim cut labels off", got)
	}
	// The pages carrying figures, which is what says a change to the geometry split
	// or merged pictures rather than admitting or losing them. 27 since the clip was
	// read, and the trim change did not move it.
	if got := figurePages(conv); got != 27 {
		t.Errorf("figures land on %d page(s), was 27", got)
	}

	// Reading order is clean, including on the parts pages whose callouts scatter
	// across the measure and on the ten table pages.
	if got := rep.Count(verify.KindReadingOrder); got != 0 {
		t.Errorf("reading order reported %d finding(s) on a manual read correctly", got)
	}
}

// TestCheckTheSequentialManual is the 560-page, 34-language fixture, and it is
// where the checks find defects nothing had recorded: the Thai section's words
// arrive broken, and its Hebrew and Arabic used to arrive backwards. That second one
// is fixed and this test is where it stays fixed.
func TestCheckTheSequentialManual(t *testing.T) {
	conv, rep := checked(t, "dreame-l40-ultra")

	// 16,055 blocks, of which 1,105 are page furniture: the 34 language tabs, one on
	// every page of every section, and 552 folios.
	//
	// This number has been up and back down, for reasons that are all in doc/bidi.go
	// after the first two. Every move is on a Hebrew or Arabic page, measured page by
	// page against each previous conversion; the furniture count and the figures have
	// never moved.
	//
	//	15,951  before the furniture pass
	//	16,055  after it, the tab un-glued from a running head on 104 pages
	//	16,097  right-to-left lines read in logical order: +42 over ten pages, because
	//	        a list marker leads its line only in logical order, so
	//	        `– يجب إزالة البطارية` became the list item it is printed as
	//	16,098  +1 on page 191, when the region's language took over deciding
	//	        direction and gave that page's `Dreamehome אפלקציית` line to the repair
	//	16,055  −43 over six pages: a REGRESSION, and the only thing that caught it
	//	16,098  the same 43 back, page for page identical to the reading above it
	//
	// THE SEQUENCE IS THE POINT, because the number alone lies twice. 16,055 appears
	// twice and means opposite things: once as the honest total before any right-to-left
	// repair, and once as a regression that had cost six pages their list structure —
	// page 194 was 7 blocks BELOW its original at that point, so even the distribution
	// was not the same document. Anyone moving this number should read the sequence
	// before deciding which direction is good. Higher has meant better every time so
	// far, because every rise has been a printed list becoming list blocks.
	//
	// The regression and its repair are both in bidi.go's run-level island test, and
	// conversion.md carries them. `hasRightToLeft` asks whether a run holds a
	// right-to-left LETTER, so a run of only digits, punctuation or spaces answered no
	// and was held in printed order as though it were left-to-right content: page 211's
	// marker is two such runs, the digit at x=859 and the period at x=850, and it came
	// out `. 1 مستشعر المسافة بالليزر ( )LDS` where the page prints
	// `1. مستشعر المسافة بالليزر (LDS)`. An island is now the part BETWEEN the outermost
	// runs carrying a left-to-right letter, so those two reverse with the Arabic beside
	// them, `leadingMarker` sees `1.` again, and the six list items are six blocks.
	if len(conv.Blocks) != 16098 || len(conv.Figures) != 134 {
		t.Errorf("the conversion under test moved: %d blocks and %d figures, "+
			"was 16098 and 134 — read the sequence above before deciding which way is "+
			"better, because 16055 has been both the honest total and a regression that "+
			"cost six pages their lists", len(conv.Blocks), len(conv.Figures))
	}
	if got := len(conv.FurnitureBlocks()); got != 1105 {
		t.Errorf("%d furniture block(s), was 1105", got)
	}

	// Excluding the furniture moved the median from 1.000 to 0.997 and the worst
	// judged page from 0.952 to 0.949, against a floor of 0.75. The page that moves
	// furthest is page 558, which holds nothing but a tab and a folio and so scores
	// 0.500 — it is under minCoverageText and is not judged, which is the page that
	// constant was written for.
	if got := rep.Count(verify.KindCoverage); got != 0 {
		t.Errorf("coverage reported %d page(s); its worst judged page scores 0.949", got)
	}
	if m := rep.MedianCoverage(); m < 0.99 {
		t.Errorf("median coverage %.3f, was 0.997 (1.000 before furniture was excluded)", m)
	}

	// The right-to-left defect is GONE, and this is where that is asserted as a
	// number. Three measurements over this whole document:
	//
	//	                                       before   majority   region
	//	pages reported right-to-left-reversed    32          6         0
	//	words absent from pdftotext on them   8,120         80         —
	//	...of those, present when reversed    7,938         18         0
	//
	// The last row is the one that means it: a word absent from the reference but
	// present in it backwards is the signature of visual order, and there are none.
	// The "before" column was re-measured rather than quoted, by putting joinRuns back
	// at doc/blocks.go's one call site; the middle column is what a line deciding its
	// own direction by majority left behind, six lines whose Latin outweighed their
	// Hebrew.
	//
	// Zero here is not zero absent words: 202 remain over 25 right-to-left pages, and
	// they are Arabic shaping and combining-mark disagreement, reported block by block
	// as [verify.KindInvented]. The whole-document assertion and the distribution
	// behind this number live in verify.TestNoTextIsStoredReversed.
	if got := rep.Count(verify.KindRightToLeft); got != 0 {
		for i := range rep.Findings {
			if rep.Findings[i].Kind == verify.KindRightToLeft {
				t.Errorf("%s | %s", rep.Findings[i].Detail, rep.Findings[i].Sample)
			}
		}
		t.Errorf("right-to-left: %d page(s), want 0 — was 6 while a line's own "+
			"characters decided its direction, and 32 before the lines were read in "+
			"order at all", got)
	}

	// A defect nothing had recorded, and this check is how it was found: 142 of these
	// blocks are on pages 473-488, the Thai section, where `pdftohtml -xml`
	// returns an unmapped glyph for SARA AA (U+FFFD) that `pdftotext` maps correctly
	// — so the block's words are broken where that vowel belongs, "ล้�งผ้�ถูพื้น"
	// against the printed "ล้างผ้าถูพื้น". 11 more are Latin pages where the two
	// tools divide a hyphenated compound differently.
	//
	// 160 and not 153 because the right-to-left pages are no longer named as pages
	// and are judged block by block like every other page, which is the point of the
	// sharpening: 7 of their blocks hold more than [maxInventedShare] of words the
	// reference does not have, and those 7 are the same Arabic shaping and
	// combining-mark disagreements the Latin 11 are, not a reversal. The number did
	// not move again when the region took over deciding direction, which is worth
	// asserting: those 7 were never the reversal either.
	if got := rep.Count(verify.KindInvented); got != 160 {
		t.Errorf("invented text: %d block(s), was 160 (153 while every right-to-left "+
			"page was named instead of judged)", got)
	}

	// Back to 72. It was 73 for one commit, when page 204's support URL arrived with
	// its seventeen runs reversed and this check caught it sideways as
	// `إىل faqs-and- manuals-us`. The URL is whole again — the line now reads
	// `يُرجى االنتقال إىل https://global.dreametech.com/pages/user-manuals -and-faqs`
	// — and that finding is gone with it.
	if got := rep.Count(verify.KindJoinHyphen); got != 72 {
		t.Errorf("hyphen joins: %d block(s), was 72 (73 while page 204's URL was "+
			"stored run-reversed)", got)
	}
	// 7. Three arrived with the bidi repair and are not new damage — Hebrew page 200
	// and Arabic 206, 207 print two columns that the conversion interleaves into one
	// line, and in visual order the two halves met inside a word the comparison could
	// not recognise, so reading the line logically is what makes `סוללות|מדריך` legible
	// as a glued pair.
	//
	// It was 7 for two commits, when page 204's laser standard lost the space in
	// `IEC 60825` and transposed the halves of `EN 60825- 1:2014/`. Both came from
	// passing a run through visualToLogical after deciding to emit it in PRINTED order:
	// such a run is already in logical order, and reversing it splits it at any space
	// whose neighbours are not both strongly left-to-right. Only a run that reverses
	// gets that repair now, and the standard reads `IEC 60825-1:2014/ EN 60825- 1:2014/`.
	if got := rep.Count(verify.KindJoinGlued); got != 6 {
		t.Errorf("glued words: %d, was 6 (7 while page 204's laser standard was "+
			"repaired twice over, 3 before right-to-left lines were read in order)", got)
	}

	// 2 blank bands where there were 6 before the clip was read. Merging candidate
	// boxes that overlap did not move this, which is worth stating, because a
	// merged box is bigger than either of its parts and could easily have arrived
	// with empty space in it: it does not, because the parts overlap.
	if got := rep.Count(verify.KindFigureBand); got != 2 {
		t.Errorf("blank bands: %d figure(s), was 6 before the clip and 2 after", got)
	}
	// 24, down from 70, and this is where merging overlapping candidates pays off
	// twice. The residual findings of this document were never the trim and never
	// the clip: they were the crowded diagram pages 521-531, where a drawing had
	// clustered in pieces and each piece's box was crossed by the shapes of the
	// piece beside it. Merging the pieces removes the crossing along with the
	// duplicate picture. What is left is the leader-line case this package has to
	// guess at, matching a shape to a figure by geometry because doc.Figure carries
	// how many shapes it holds and not which.
	//
	// 25 until growing a box onto its labels took one more away, and that number is
	// worth keeping here because the wrong reading of it was 27. A grown crop reaches
	// over whatever sits in the corridor beside it, so asking BOTH questions of the
	// rendered box makes a figure adopt the neighbouring drawing's leader and then
	// report itself as cut by it. [verify.clipped] matches a shape by the drawn
	// extent and tests it against the rendered one, and that is what makes this
	// number fall as the crop grows rather than rise.
	if got := rep.Count(verify.KindFigureClipped); got != 24 {
		t.Errorf("clipped figures: %d of 134, was 74 of 163 before the clip, "+
			"71 while the trim cut labels off, 70 of 168 before overlapping "+
			"candidates were merged and 25 before a crop grew onto its labels", got)
	}
	// 20, and the 23 that doc/figures.go's header quotes is a different count at a
	// different level: doc finds 195 figures over 23 pages, and conversion keeps the
	// 134 that fall inside a language region, which land on 20 of those pages. Both
	// are right and they are not the same number. The page count did not move when
	// the figure count fell from 168 to 134, which is what says those 34 were pieces
	// of pictures already found rather than pictures lost.
	if got := figurePages(conv); got != 20 {
		t.Errorf("figures land on %d page(s), was 20", got)
	}

	// The one reading-order class either manual has: the routine-maintenance page
	// of each language section lays its intervals out as an unruled grid, which
	// conversion.md records as invisible to the table detector, and reading it in
	// columns puts the intervals out of order. 36 findings over 34 sections — it was
	// 37 until the furniture pass took a tab out of the block that carried it, which
	// left that block under minOrderChars.
	//
	// 38 since the bidi repair, and the 2 are a second real class this document had
	// been hiding rather than a regression. Arabic page 216, the battery-disposal
	// page, prints two columns; the conversion reads them interleaved, block 3 in the
	// right column at x=664-863, block 4 in the LEFT at x=351-587 and further down,
	// then block 5 back in the right. That interleave was always there — its Hebrew
	// twin on page 200 still has it, invisible, because those two columns are joined
	// inside one block. What changed is that a list marker leads its line in logical
	// order, so page 216's line came apart into the per-column blocks the check can
	// see between.
	if got := rep.Count(verify.KindReadingOrder); got != 38 {
		t.Errorf("reading order: %d finding(s), was 38 (36 before right-to-left lines "+
			"were read in order, 37 before the furniture pass)", got)
	}
	if got := rep.PagesFlagged(verify.KindReadingOrder); got < 24 {
		t.Errorf("reading-order findings cover %d pages, was 26 — a class this "+
			"concentrated on one page per section is what makes it explainable", got)
	}
}
