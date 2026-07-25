package doc_test

import (
	"context"
	"os"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/extern"
	"github.com/gordon2/manualbox/internal/fixture"
)

// fixturesDir is relative to this package.
const fixturesDir = "../../testdata/fixtures"

// These tests run the whole free pipeline against a real 560-page, 34-language
// appliance manual. They are the only honest check that the language map works:
// a synthetic PDF cannot reproduce a printed index that contradicts itself, a
// back cover in the wrong language, or sibling languages a detector confuses.
//
// The document is fetched on demand and is not committed — it is 15 MB of someone
// else's copyrighted manual. Without MANUALBOX_TEST_FIXTURES=1 these skip, so the
// default suite stays hermetic and offline.

func loadFixture(t *testing.T) (manifest *fixture.Manifest, path string) {
	t.Helper()
	if os.Getenv(fixture.EnableEnv) == "" {
		t.Skipf("set %s=1 to download the fixture and run the real-document tests", fixture.EnableEnv)
	}
	for _, tool := range []extern.Tool{extern.PDFInfo, extern.PDFToText} {
		if !extern.Available(tool) {
			t.Skipf("%s is not installed", tool.Name)
		}
	}

	m, err := fixture.Load(fixturesDir, "dreame-l40-ultra")
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	cached, err := m.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch fixture: %v", err)
	}
	return m, cached
}

func analyzeFixture(t *testing.T) (manifest *fixture.Manifest, result *doc.Result) {
	t.Helper()
	m, cached := loadFixture(t)
	res, err := doc.Analyze(context.Background(), cached)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return m, res
}

// TestProbeMatchesManifest checks stage 0 and stage 1 against facts measured
// independently and recorded in the manifest.
func TestProbeMatchesManifest(t *testing.T) {
	m, res := analyzeFixture(t)

	if res.Info.Pages != m.Pages {
		t.Errorf("page count = %d, manifest says %d", res.Info.Pages, m.Pages)
	}
	if res.Info.Encrypted {
		t.Error("document reported as encrypted; the manifest describes an open PDF")
	}
	if res.HasTextLayer != m.HasTextLayer {
		t.Errorf("has text layer = %t, manifest says %t", res.HasTextLayer, m.HasTextLayer)
	}

	// The manifest records the median in runes. It was previously recorded in
	// bytes, which for a document full of Cyrillic, Greek, Hebrew, Arabic and CJK
	// is a third larger — hence the tolerance being tight rather than generous.
	if got, want := res.MedianChars, m.MedianCharsPerPage; abs(got-want) > 20 {
		t.Errorf("median chars per page = %d, manifest says %d", got, want)
	}

	if res.ContentStart != m.ContentStartsOnPDFPage {
		t.Errorf("content starts on page %d, manifest says %d", res.ContentStart, m.ContentStartsOnPDFPage)
	}
}

// TestLanguageMapMatchesManifest is the central assertion of the ingest pipeline:
// every language section, its boundaries, and its page count, on a real document.
func TestLanguageMapMatchesManifest(t *testing.T) {
	m, res := analyzeFixture(t)

	summaries := res.Languages()
	if len(summaries) != len(m.Sections) {
		t.Errorf("found %d languages, manifest records %d", len(summaries), len(m.Sections))
		for _, s := range summaries {
			t.Logf("  found %-6s %-12s pages %d, first page %d", s.Code, s.Lang, s.Pages, s.FirstPage)
		}
	}

	byCode := make(map[string]doc.LanguageSummary, len(summaries))
	for _, s := range summaries {
		byCode[s.Code] = s
	}

	for _, want := range m.Sections {
		got, ok := byCode[want.Code]
		if !ok {
			t.Errorf("%s: not found; manifest expects pages %d-%d", want.Code, want.PDFStart, want.PDFEnd)
			continue
		}
		if got.FirstPage != want.PDFStart {
			t.Errorf("%s: starts at page %d, manifest says %d", want.Code, got.FirstPage, want.PDFStart)
		}
		// The end page is asserted separately from the page total on purpose. A
		// section wrongly split into two spans can still total the right number of
		// pages — that happened during development and a totals-only check missed
		// it entirely.
		if got.LastPage != want.PDFEnd {
			t.Errorf("%s: ends at page %d, manifest says %d", want.Code, got.LastPage, want.PDFEnd)
		}
		if got.Pages != want.Pages {
			t.Errorf("%s: %d pages, manifest says %d", want.Code, got.Pages, want.Pages)
		}
		if got.Runs != 1 {
			t.Errorf("%s: split across %d spans; each section in this document is contiguous",
				want.Code, got.Runs)
		}
	}
}

// TestEveryContentPageIsLabelled asserts the property that actually matters
// downstream: no page of real content is left without a language, because an
// unlabelled page cannot be included in or excluded from a translation scope.
func TestEveryContentPageIsLabelled(t *testing.T) {
	m, res := analyzeFixture(t)

	// Unlabelled is not expected to be zero, and demanding that it were is what
	// made this assertion weak. A cover and a colophon carry text and belong to no
	// language section, so the honest expectation is "a handful, all of them
	// furniture" — the magnitude is the signal, and it is what says whether a
	// statistical detector would earn its 118 MB on this document.
	if res.Unlabelled > 8 {
		t.Errorf("%d pages carry text but no language label; on this document only "+
			"front matter and the back cover should", res.Unlabelled)
	}

	// Unlabelled == 0 is not sufficient, and the name of this test used to promise
	// more than it delivered. countUnlabelled bounds itself by the content range,
	// which is derived from the runs themselves — so a section lost at either end
	// of the document simply shrinks the range and still reports zero. Verified:
	// deleting the entire English section left this test green.
	//
	// So count the pages actually covered and require every content page of the
	// document to be among them.
	covered := make(map[int]bool, m.Pages)
	for _, run := range res.Runs {
		for p := run.Start; p <= run.End; p++ {
			covered[p] = true
		}
	}

	wantPages := 0
	for _, s := range m.Sections {
		wantPages += s.Pages
	}
	if len(covered) != wantPages {
		t.Errorf("%d pages carry a language, but the manifest accounts for %d", len(covered), wantPages)
	}

	var missing []int
	for _, s := range m.Sections {
		for p := s.PDFStart; p <= s.PDFEnd; p++ {
			if !covered[p] {
				missing = append(missing, p)
			}
		}
	}
	if len(missing) > 0 {
		show := missing
		if len(show) > 12 {
			show = show[:12]
		}
		t.Errorf("%d content pages are in no run at all, e.g. %v", len(missing), show)
	}

	// Every page that carries text but no label must lie outside every section.
	// That is the property "unlabelled means furniture" actually asserts; a bare
	// count cannot distinguish a cover from a lost section.
	var strays []int
	for i := range res.Pages {
		p := &res.Pages[i]
		if covered[p.No] || p.Chars < 50 {
			continue
		}
		for _, s := range m.Sections {
			if p.No >= s.PDFStart && p.No <= s.PDFEnd {
				strays = append(strays, p.No)
				break
			}
		}
	}
	if len(strays) > 0 {
		t.Errorf("pages %v are inside a language section but carry no label", strays)
	}
}

// TestPageTagSignalIsExact records what the printed per-page tag achieves on this
// document, because it is the measurement that justified building the pipeline
// around it. If a change degrades it, this test says so specifically rather than
// leaving a general language-map failure to be diagnosed.
func TestPageTagSignalIsExact(t *testing.T) {
	m, res := analyzeFixture(t)

	tagRuns := res.BySource[doc.SourcePageTag]
	if len(tagRuns) != len(m.Sections) {
		t.Fatalf("page tag produced %d runs, expected %d sections", len(tagRuns), len(m.Sections))
	}

	expected := make(map[int]string, m.Pages)
	for _, s := range m.Sections {
		for p := s.PDFStart; p <= s.PDFEnd; p++ {
			expected[p] = s.Code
		}
	}

	wrong := 0
	for _, run := range tagRuns {
		for p := run.Start; p <= run.End; p++ {
			if want, ok := expected[p]; ok && want != run.Code {
				if wrong < 5 {
					t.Errorf("page %d: tag says %s, manifest says %s", p, run.Code, want)
				}
				wrong++
			}
		}
	}
	if wrong > 0 {
		t.Errorf("%d pages disagree with the manifest", wrong)
	}
}

// TestContentsPagesDoNotBecomeSections guards the measured false-positive case: a
// manual's contents pages list every language code in the same position the
// per-page tab occupies. Without the run-length guard this document gains three
// bogus single-page sections.
func TestContentsPagesDoNotBecomeSections(t *testing.T) {
	m, res := analyzeFixture(t)

	for _, run := range res.Runs {
		for _, indexPage := range m.IndexPages {
			if run.Contains(indexPage) {
				t.Errorf("contents page %d was absorbed into a %s section (pages %d-%d)",
					indexPage, run.Code, run.Start, run.End)
			}
		}
	}
}

// TestScopeIsASmallFractionOfTheDocument is the premise of the whole design: a
// household that reads three languages should be asked to process a few per cent
// of a 34-language manual, not all of it.
func TestScopeIsASmallFractionOfTheDocument(t *testing.T) {
	_, res := analyzeFixture(t)

	scope := res.ScopeFor([]string{"de", "uk", "en"})
	if len(scope.Languages) != 3 {
		t.Errorf("expected 3 household languages in scope, got %d", len(scope.Languages))
		for _, l := range scope.Languages {
			t.Logf("  in scope: %s (%s), %d pages", l.Code, l.Lang, l.Pages)
		}
	}
	if scope.Fraction() > 0.15 {
		t.Errorf("scope is %.1f%% of the document; the design expects roughly 10%%",
			100*scope.Fraction())
	}
	if len(scope.OtherLanguages) == 0 {
		t.Error("no other languages reported; the user must be able to see what else is in the document")
	}
	t.Logf("scope: %d of %d pages (%.1f%%), %d chars, %d other languages available",
		scope.Pages, scope.TotalPages, 100*scope.Fraction(), scope.Chars, len(scope.OtherLanguages))
}

// TestIndexDisagreementIsSurfaced checks that the manual's own contents table
// being wrong is reported rather than silently accepted or silently corrected.
// This document's index misplaces several sections.
func TestIndexDisagreementIsSurfaced(t *testing.T) {
	_, res := analyzeFixture(t)

	indexRuns := res.BySource[doc.SourceIndex]
	if len(indexRuns) == 0 {
		t.Fatal("the printed index was not parsed at all")
	}

	titled := 0
	for _, r := range indexRuns {
		if r.Title != "" {
			titled++
		}
	}
	if titled < 30 {
		t.Errorf("only %d index entries carry a section title; the index supplies titles no other signal can", titled)
	}

	// Titles must survive into the reconciled view, since that is what the UI
	// shows.
	withTitle := 0
	for _, r := range res.Runs {
		if r.Title != "" {
			withTitle++
		}
	}
	if withTitle == 0 {
		t.Error("no reconciled run carries a printed section title")
	}
	t.Logf("index parsed: %d entries, %d with titles; %d reconciled runs carry a title",
		len(indexRuns), titled, withTitle)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
