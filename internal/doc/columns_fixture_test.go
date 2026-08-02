package doc_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/extern"
	"github.com/gordon2/manualbox/internal/fixture"
)

// These run the column pipeline against the real parallel-columns manual, from
// the PDF rather than from coordinates written by hand.
//
// That distinction is the point of the file. DetectColumns and ColumnLanguages
// were both developed against runs typed into a test, and the fixture's own
// per-page entries were produced by a script that no longer exists — so nothing
// until now checked that what poppler actually reports for this document is what
// those tests assumed. The eight pages a human compared against their rendered
// images are the ones asserted here; the rest of the manifest's pages were
// produced by the detector and holding it to those would be circular. See the
// provenance note in the manifest.

// columnFixture loads the column manual and its ground truth.
func columnFixture(t *testing.T) (manifest *fixture.Manifest, path string) {
	t.Helper()
	if os.Getenv(fixture.EnableEnv) == "" {
		t.Skipf("set %s=1 to download the fixture and run the real-document tests", fixture.EnableEnv)
	}
	for _, tool := range []extern.Tool{extern.PDFInfo, extern.PDFToText, extern.PDFToHTML} {
		if !extern.Available(tool) {
			t.Skipf("%s is not installed", tool.Name)
		}
	}

	m, err := fixture.Load(fixturesDir, "thomas-drybox-amfibia")
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	cached, err := m.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch fixture: %v", err)
	}
	return m, cached
}

// extractColumnFixture reads the document both ways: positioned runs for the
// geometry, and plain text for the printed index whose vocabulary of codes is
// what makes a single-letter tag like "D" usable.
func extractColumnFixture(t *testing.T) (manifest *fixture.Manifest, pages []doc.PageRuns, knownCodes map[string]bool) {
	t.Helper()
	m, path := columnFixture(t)

	runs, err := doc.ExtractRuns(context.Background(), path)
	if err != nil {
		t.Fatalf("ExtractRuns: %v", err)
	}
	res, err := doc.Analyze(context.Background(), path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return m, runs, doc.IndexCodes(res.BySource[doc.SourceIndex])
}

// TestExtractedRunsMatchTheMeasuredDocument checks the coordinate space and the
// volume of text against facts recorded in the manifest, before anything is
// concluded from them. A change in either invalidates every threshold in
// columns.go, all of which are fractions of the page.
func TestExtractedRunsMatchTheMeasuredDocument(t *testing.T) {
	m, pages, _ := extractColumnFixture(t)

	if len(pages) != m.Pages {
		t.Fatalf("extracted %d pages, manifest says %d", len(pages), m.Pages)
	}
	if m.PageBox == nil {
		t.Fatal("the manifest records no page box to check against")
	}

	total := 0
	for i := range pages {
		p := &pages[i]
		total += len(p.Runs)
		// Exact: the box is the PDF's page size scaled by 1.5, a property of the
		// output format rather than of the document's typesetting.
		if p.Width != m.PageBox.Width || p.Height != m.PageBox.Height {
			t.Errorf("page %d box = %gx%g, manifest says %gx%g",
				p.No, p.Width, p.Height, m.PageBox.Width, m.PageBox.Height)
			break
		}
	}

	// Tolerant: how a version of poppler breaks a line into runs may shift, and a
	// few runs either way changes no conclusion. An order-of-magnitude change means
	// the tool is doing something else entirely.
	if diff := abs(total - m.TextRuns); diff > m.TextRuns/20 {
		t.Errorf("extracted %d text runs, manifest says %d (%d apart)", total, m.TextRuns, diff)
	}
	t.Logf("%d pages, %d runs, page box %gx%g", len(pages), total, pages[0].Width, pages[0].Height)
}

// TestColumnGeometryMatchesTheVerifiedPages is the acceptance test for the
// detector on a real document: the eight pages checked by eye, column for column.
func TestColumnGeometryMatchesTheVerifiedPages(t *testing.T) {
	m, pages, _ := extractColumnFixture(t)

	byNo := make(map[int]*doc.PageRuns, len(pages))
	for i := range pages {
		byNo[pages[i].No] = &pages[i]
	}

	verified := m.VerifiedPages()
	if len(verified) == 0 {
		t.Fatal("the manifest records no human-verified pages, so nothing here is ground truth")
	}

	for _, want := range verified {
		page, ok := byNo[want.Page]
		if !ok {
			t.Errorf("page %d is in the manifest but was not extracted", want.Page)
			continue
		}
		layout := doc.DetectColumns(page.Runs, page.Width, page.Height)

		if len(layout.Columns) != want.Columns {
			t.Errorf("page %d: found %d columns, the render shows %d — %s",
				want.Page, len(layout.Columns), want.Columns, layout.Note)
			continue
		}
		if layout.Spanning != want.Spanning {
			t.Errorf("page %d: %d runs span a gutter, manifest says %d",
				want.Page, layout.Spanning, want.Spanning)
		}
		for i, wantCol := range want.Cols {
			got := layout.Columns[i]
			// Column edges are the extent of the runs assigned to the column, so
			// they are exact integers in this space. A tolerance of 1 absorbs the
			// rounding in how the manifest recorded them.
			if math.Abs(got.Min-float64(wantCol.X0)) > 1 || math.Abs(got.Max-float64(wantCol.X1)) > 1 {
				t.Errorf("page %d column %d: x=%.0f-%.0f, manifest says %d-%d",
					want.Page, i, got.Min, got.Max, wantCol.X0, wantCol.X1)
			}
			if got.Runs != wantCol.Runs {
				t.Errorf("page %d column %d: %d runs, manifest says %d",
					want.Page, i, got.Runs, wantCol.Runs)
			}
		}
	}
}

// TestColumnLanguagesMatchTheVerifiedPages checks the other half against the same
// eight pages: not only where the columns are, but what language each one is.
//
// This is what could not be checked before. The per-column languages were
// established against runs supplied by hand, so agreement with the manifest here
// is the first evidence that the signal works on what the tool actually reports.
func TestColumnLanguagesMatchTheVerifiedPages(t *testing.T) {
	m, pages, knownCodes := extractColumnFixture(t)

	byNo := make(map[int]*doc.PageRuns, len(pages))
	for i := range pages {
		byNo[pages[i].No] = &pages[i]
	}

	named, total := 0, 0
	for _, want := range m.VerifiedPages() {
		page, ok := byNo[want.Page]
		if !ok {
			continue
		}
		layout := doc.DetectColumns(page.Runs, page.Width, page.Height)
		got := doc.ColumnLanguages(page.Runs, layout.Columns, knownCodes)
		if len(got) != len(want.Cols) {
			// Geometry is asserted by its own test; this one only reports what it
			// could not line up.
			t.Logf("page %d: %d columns against %d in the manifest, skipping languages",
				want.Page, len(got), len(want.Cols))
			continue
		}

		for i, wantCol := range want.Cols {
			total++
			if wantCol.Lang == "" {
				// The manifest records some columns as unestablished on purpose,
				// including one the signal reads wrongly. Naming one of those is not a
				// failure here, but claiming the manifest agreed would be.
				t.Logf("page %d column %d: manifest records no language (%s); read as %q",
					want.Page, i, wantCol.Note, got[i].Lang)
				continue
			}
			if got[i].Lang == "" {
				t.Errorf("page %d column %d: no language established, manifest says %s — %s",
					want.Page, i, wantCol.Lang, got[i].Note)
				continue
			}
			if !doc.SameLanguage(got[i].Lang, wantCol.Lang) {
				t.Errorf("page %d column %d: read as %s, manifest says %s — %s",
					want.Page, i, got[i].Lang, wantCol.Lang, got[i].Note)
				continue
			}
			named++
		}
	}

	if total == 0 {
		t.Fatal("no column was compared")
	}
	t.Logf("%d of %d columns on the human-verified pages named correctly", named, total)
}

// TestColumnLanguageAttributionIsRecorded pins which signal names each of the
// document's 169 columns, because the totals alone hide two things.
//
// First, the printed tab and the alphabet cover nearly the same columns here, so a
// change that destroys one of them barely moves the count. Reading the runs with
// Go's plain `,chardata` — which loses every styled run, and the printed tabs are
// styled — still names 166 of 169 columns, one fewer than reading them correctly.
// Only the attribution collapses, from 53 tag-named columns to none. A test on the
// total would have called that healthy.
//
// Second, and not a defect in this file: the 53 is short of what the printed tabs
// could give. columnTag believes a single-letter code only where the document's own
// contents table lists it, and IndexRuns cannot parse this manual's contents page —
// it yields the vocabulary [FAX GA NDE UA VIA Z], of which only UA is a language.
// So every German column's printed "D" is rejected for want of corroboration and
// falls back to its alphabet. Supplying the real vocabulary by hand raises tag
// naming to 79 and drops alphabet naming to 88, which is what the commit that
// introduced ColumnLanguages recorded — measured with a hand-supplied list rather
// than through the assembled pipeline. Fixing the index parser for this shape of
// contents page is separate work; this test records the true current reading so the
// gap is visible instead of inferred.
func TestColumnLanguageAttributionIsRecorded(t *testing.T) {
	_, pages, knownCodes := extractColumnFixture(t)

	bySource := make(map[doc.Source]int, 4)
	columns, named, conflicts := 0, 0, 0
	for i := range pages {
		p := &pages[i]
		layout := doc.DetectColumns(p.Runs, p.Width, p.Height)
		for _, col := range doc.ColumnLanguages(p.Runs, layout.Columns, knownCodes) {
			columns++
			if col.Lang != "" {
				named++
				bySource[col.Source]++
			}
			if col.Conflict {
				conflicts++
			}
		}
	}

	t.Logf("%d columns, %d named (%d by printed tag, %d by alphabet), %d conflicting",
		columns, named, bySource[doc.SourcePageTag], bySource[doc.SourceRepertoire], conflicts)

	if columns != 169 {
		t.Errorf("found %d columns across the document, previously measured 169", columns)
	}
	if named < 165 {
		t.Errorf("only %d of %d columns named; 167 were measured", named, columns)
	}
	// Both signals must keep contributing. Either one reaching zero is the failure
	// the totals cannot show, and it is exactly what a regression in run extraction
	// or in tag matching looks like.
	if got := bySource[doc.SourcePageTag]; got < 40 {
		t.Errorf("%d columns named by their printed tab, measured 53 — a collapse here "+
			"is invisible in the total, because the alphabet covers the same columns", got)
	}
	if got := bySource[doc.SourceRepertoire]; got < 90 {
		t.Errorf("%d columns named by their alphabet, measured 114", got)
	}
}

// TestSectionedManualExtractsEveryPage reads the other manual — the sequential one
// — because improving one document by altering the other is the regression the
// design names, and extraction is where that would start.
//
// It deliberately makes no claim about how many columns its pages have. The first
// version of this test asserted they were single-column and failed: 199 of its 560
// pages read as three columns and only 148 as one. Rendering pages 20 and 100 at
// `pdftoppm -r 108` settled it — both are two side-by-side troubleshooting tables,
// and the regions the detector returns are their cells, correctly located. On page
// 20 it returns only the two wide answer cells, because the narrow question cells
// hold fewer runs than minColumnRuns allows, which is that guard working.
//
// So the assumption was wrong and the code was right. The distribution is recorded
// in the manifest and logged here; what must not change on this manual is its
// language map, and that is asserted where the language map is built.
func TestSectionedManualExtractsEveryPage(t *testing.T) {
	m, path := loadFixture(t)
	if !extern.Available(extern.PDFToHTML) {
		t.Skip("pdftohtml is not installed")
	}

	pages, err := doc.ExtractRuns(context.Background(), path)
	if err != nil {
		t.Fatalf("ExtractRuns: %v", err)
	}
	if len(pages) != m.Pages {
		t.Fatalf("extracted %d pages, manifest says %d", len(pages), m.Pages)
	}
	if m.PageBox != nil && pages[0].Width != m.PageBox.Width {
		t.Errorf("page box width = %g, manifest says %g", pages[0].Width, m.PageBox.Width)
	}

	total := 0
	counts := make(map[int]int, 6)
	for i := range pages {
		p := &pages[i]
		total += len(p.Runs)
		counts[len(doc.DetectColumns(p.Runs, p.Width, p.Height).Columns)]++
	}

	if diff := abs(total - m.TextRuns); m.TextRuns > 0 && diff > m.TextRuns/20 {
		t.Errorf("extracted %d text runs, manifest says %d (%d apart)", total, m.TextRuns, diff)
	}

	// Every page must yield its box and its number, whatever its layout. A page
	// lost here shifts every later page number, and a page number is what the
	// language map is keyed on.
	for i := range pages {
		if pages[i].No != i+1 {
			t.Fatalf("page at index %d is numbered %d; numbering must follow the PDF",
				i, pages[i].No)
		}
	}

	// Recorded, not asserted — see this test's own comment for why a claim here was
	// wrong. The manifest holds the same distribution, so a change shows up as a
	// disagreement with a written-down measurement rather than as a silent drift.
	t.Logf("%d pages, %d runs; column counts: %s", len(pages), total, summarizeCounts(counts))
}

func summarizeCounts(counts map[int]int) string {
	out := ""
	for n := 0; n <= 6; n++ {
		if counts[n] == 0 {
			continue
		}
		if out != "" {
			out += ", "
		}
		out += fmt.Sprintf("%d columns: %d pages", n, counts[n])
	}
	return out
}
