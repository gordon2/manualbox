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

// TestCheckTheColumnManual is the parallel-columns fixture: 68 pages, five
// languages sharing most of them, 46 figures.
func TestCheckTheColumnManual(t *testing.T) {
	conv, rep := checked(t, "thomas-drybox-amfibia")

	if len(conv.Blocks) != 2180 || len(conv.Figures) != 46 {
		t.Errorf("the conversion under test moved: %d blocks and %d figures, "+
			"was 2180 and 46", len(conv.Blocks), len(conv.Figures))
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

	// The figure geometry, which is the clip-path limitation conversion.md records.
	if got := rep.Count(verify.KindFigureBand); got != 4 {
		t.Errorf("blank bands: %d figure(s), was 4", got)
	}
	if got := rep.Count(verify.KindFigureClipped); got != 22 {
		t.Errorf("clipped figures: %d of 46, was 22", got)
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

	if len(conv.Blocks) != 15951 || len(conv.Figures) != 163 {
		t.Errorf("the conversion under test moved: %d blocks and %d figures, "+
			"was 15951 and 163", len(conv.Blocks), len(conv.Figures))
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

	if got := rep.Count(verify.KindFigureBand); got != 6 {
		t.Errorf("blank bands: %d figure(s), was 6", got)
	}
	if got := rep.Count(verify.KindFigureClipped); got != 74 {
		t.Errorf("clipped figures: %d of 163, was 74", got)
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
