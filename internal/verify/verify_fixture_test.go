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

	if len(conv.Blocks) != 2180 || len(conv.Figures) != 59 {
		t.Errorf("the conversion under test moved: %d blocks and %d figures, "+
			"was 2180 and 59", len(conv.Blocks), len(conv.Figures))
	}

	// No page loses text. The lowest score is page 5 at 0.80, which is a page of
	// framed illustrations whose captions the run filter drops, and the median is
	// 0.97 — so the floor of 0.75 leaves headroom and reports nothing here.
	if got := rep.Count(verify.KindCoverage); got != 0 {
		t.Errorf("coverage reported %d page(s) on a manual that drops none", got)
	}
	if m := rep.MedianCoverage(); m < 0.95 || m > 1.0 {
		t.Errorf("median coverage %.3f, was 0.974", m)
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
// arrive broken, and 32 pages of Hebrew and Arabic arrive backwards.
func TestCheckTheSequentialManual(t *testing.T) {
	conv, rep := checked(t, "dreame-l40-ultra")

	if len(conv.Blocks) != 15951 || len(conv.Figures) != 168 {
		t.Errorf("the conversion under test moved: %d blocks and %d figures, "+
			"was 15951 and 168", len(conv.Blocks), len(conv.Figures))
	}

	if got := rep.Count(verify.KindCoverage); got != 0 {
		t.Errorf("coverage reported %d page(s); its worst page scores 0.95", got)
	}
	if m := rep.MedianCoverage(); m < 0.99 {
		t.Errorf("median coverage %.3f, was 1.000", m)
	}

	// The right-to-left defect, named once per page instead of once per word: 32
	// pages, and it would otherwise be over eight thousand findings.
	if got := rep.Count(verify.KindRightToLeft); got != 32 {
		t.Errorf("right-to-left: %d page(s), was 32", got)
	}
	rtl := 0
	for i := range rep.Findings {
		if rep.Findings[i].Kind != verify.KindRightToLeft {
			continue
		}
		rtl += rep.Findings[i].Count
		// Got is how many of the absent words are present reversed, which is the
		// evidence that this is the pdftohtml visual-order defect and not damage.
		if rep.Findings[i].Got < 0.8*float64(rep.Findings[i].Count) {
			t.Errorf("page %d: only %.0f of %d absent words are present reversed",
				rep.Findings[i].Page, rep.Findings[i].Got, rep.Findings[i].Count)
		}
	}
	if rtl < 8000 {
		t.Errorf("the right-to-left pages hold %d absent words, was 8120", rtl)
	}

	// A defect nothing had recorded, and this check is how it was found: 142 of these
	// 153 blocks are on pages 473-488, the Thai section, where `pdftohtml -xml`
	// returns an unmapped glyph for SARA AA (U+FFFD) that `pdftotext` maps correctly
	// — so the block's words are broken where that vowel belongs, "ล้�งผ้�ถูพื้น"
	// against the printed "ล้างผ้าถูพื้น". The other 11 are Latin pages where the two
	// tools divide a hyphenated compound differently.
	if got := rep.Count(verify.KindInvented); got != 153 {
		t.Errorf("invented text: %d block(s), was 153", got)
	}

	if got := rep.Count(verify.KindJoinHyphen); got != 72 {
		t.Errorf("hyphen joins: %d block(s), was 72", got)
	}
	if got := rep.Count(verify.KindJoinGlued); got != 3 {
		t.Errorf("glued words: %d, was 3", got)
	}

	// 2 blank bands where there were 6 before the clip was read, and 71 clipped
	// where there were 74 — barely moved, and that is the honest reading of this
	// document rather than a disappointment to explain away. Its residual findings
	// are on the crowded diagram pages (521-531), where a leader line more than half
	// inside one small figure's box is drawn for the drawing beside it, so what they
	// report is the geometric matching this package has to do without knowing which
	// shapes doc assigned to which figure. Page 522 was rendered and read: 13
	// figures over about eight printed drawings, each crop a sensible picture with
	// leader-line stubs reaching its edge.
	if got := rep.Count(verify.KindFigureBand); got != 2 {
		t.Errorf("blank bands: %d figure(s), was 6 before the clip and 2 after", got)
	}
	// 70 since trimToPicture stopped pulling an edge in off a label the artwork
	// encloses, which on this document is worth exactly one figure: page 545 figure
	// 5, whose box used to stop at x=245 and cut the leader line running out to the
	// label "QR コード" at 245-272. It now stops at 285, where the Wi-Fi caption
	// really does reach in from outside. That one figure is the whole difference,
	// and that is the same reading as above — this document's clipped figures are
	// leader lines between crowded drawings, not trimming.
	if got := rep.Count(verify.KindFigureClipped); got != 70 {
		t.Errorf("clipped figures: %d of 168, was 74 of 163 before the clip "+
			"and 71 while the trim cut labels off", got)
	}
	// 20, and the 23 that doc/figures.go's header quotes is a different count at a
	// different level: doc finds 238 figures over 23 pages, and conversion keeps the
	// 168 that fall inside a language region, which land on 20 of those pages. Both
	// are right and they are not the same number.
	if got := figurePages(conv); got != 20 {
		t.Errorf("figures land on %d page(s), was 20", got)
	}

	// The one reading-order class either manual has: the routine-maintenance page
	// of each language section lays its intervals out as an unruled grid, which
	// conversion.md records as invisible to the table detector, and reading it in
	// columns puts the intervals out of order. 37 findings over 34 sections.
	if got := rep.Count(verify.KindReadingOrder); got != 37 {
		t.Errorf("reading order: %d finding(s), was 37", got)
	}
	if got := rep.PagesFlagged(verify.KindReadingOrder); got < 24 {
		t.Errorf("reading-order findings cover %d pages, was 26 — a class this "+
			"concentrated on one page per section is what makes it explainable", got)
	}
}
