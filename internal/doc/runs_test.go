package doc_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/extern"
	"github.com/gordon2/manualbox/internal/testpdf"
)

// These drive real poppler over a PDF generated in memory, which is the only
// place the two halves of the column pipeline meet: what testpdf writes, what
// pdftohtml reports back, and what DetectColumns makes of it. Every other test of
// the detector supplies coordinates directly and so cannot catch a disagreement
// between them.
//
// No network and nothing committed, so these run in the default suite — they skip
// only where poppler is absent, which is the same condition the rest of the
// document pipeline already skips on.

func requirePDFToHTML(t *testing.T) {
	t.Helper()
	if !extern.Available(extern.PDFToHTML) {
		t.Skip("pdftohtml is not installed")
	}
}

// writePDF renders a generated document to a temporary file.
func writePDF(t *testing.T, d testpdf.Doc) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "generated.pdf")
	if err := os.WriteFile(path, d.Build(), 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	return path
}

func extract(t *testing.T, d testpdf.Doc) []doc.PageRuns {
	t.Helper()
	pages, err := doc.ExtractRuns(context.Background(), writePDF(t, d))
	if err != nil {
		t.Fatalf("ExtractRuns: %v", err)
	}
	return pages
}

// repeat builds n identical lines, enough of them to clear minColumnRuns.
func repeat(line string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = line
	}
	return out
}

// TestExtractRunsFindsTwoColumnsInAGeneratedPDF is the end-to-end assertion: a
// two-column page written here comes back as two columns, at the coordinates it
// was written at.
func TestExtractRunsFindsTwoColumnsInAGeneratedPDF(t *testing.T) {
	requirePDFToHTML(t)

	const heading = "A heading set across the whole measure of this page here"
	pages := extract(t, testpdf.Doc{Pages: []testpdf.Page{{
		Lines: []string{heading},
		Columns: []testpdf.Column{
			{X: 60, Lines: repeat("Left column line of ordinary text", 10)},
			{X: 320, Lines: repeat("Right column line of ordinary text", 10)},
		},
	}}})

	if len(pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(pages))
	}
	p := &pages[0]

	// The page box is the PDF's 612 by 792 scaled by exactly 1.5. This is asserted
	// exactly, unlike the run counts below, because the scale is a property of the
	// tool's output format rather than of font metrics.
	if p.Width != 918 || p.Height != 1188 {
		t.Errorf("page box = %gx%g, want 918x1188 — poppler reports 1.5x the PDF's "+
			"612x792, which is what makes a pdftoppm -r 108 raster match 1:1",
			p.Width, p.Height)
	}

	layout := doc.DetectColumns(p.Runs, p.Width, p.Height)
	if len(layout.Columns) != 2 {
		t.Fatalf("found %d columns, want 2: %s", len(layout.Columns), layout.Note)
	}

	// Left edges are asserted exactly: they are the offsets this test asked for,
	// scaled. A right edge depends on Helvetica's metrics for the string, which is
	// not this test's subject.
	for i, want := range []float64{60 * 1.5, 320 * 1.5} {
		if got := layout.Columns[i].Min; got != want {
			t.Errorf("column %d starts at x=%g, want %g", i, got, want)
		}
		if got := layout.Columns[i].Runs; got != 10 {
			t.Errorf("column %d holds %d runs, want the 10 lines written into it", i, got)
		}
	}

	// The heading crosses the gutter, so it belongs to neither column. A detector
	// that welded the two columns together over it would report one column, and
	// binary coverage does exactly that — see columns.go.
	if layout.Spanning != 1 {
		t.Errorf("%d runs span the gutter, want 1 (the heading)", layout.Spanning)
	}
	if !strings.Contains(strings.Join(runTexts(p.Runs), "\n"), heading) {
		t.Error("the spanning heading's text did not survive extraction")
	}
}

// TestExtractRunsOnASingleColumnPage checks the ordinary case is not
// over-split, since every threshold that finds a gutter can also invent one.
func TestExtractRunsOnASingleColumnPage(t *testing.T) {
	requirePDFToHTML(t)

	pages := extract(t, testpdf.Doc{Pages: []testpdf.Page{{
		Lines: repeat("A single column of body text running the full measure.", 12),
	}}})

	layout := doc.DetectColumns(pages[0].Runs, pages[0].Width, pages[0].Height)
	if len(layout.Columns) != 1 {
		t.Errorf("found %d columns on a single-column page: %s", len(layout.Columns), layout.Note)
	}
}

// TestExtractRunsKeepsPagesWithNoText records that a page with nothing on it is
// reported rather than dropped. The page numbers must stay aligned with the
// original PDF: a missing page would shift every later one, and a page number is
// what the language map is keyed on.
func TestExtractRunsKeepsPagesWithNoText(t *testing.T) {
	requirePDFToHTML(t)

	pages := extract(t, testpdf.Doc{Pages: []testpdf.Page{
		{Lines: repeat("First page of text.", 4)},
		{},
		{Lines: repeat("Third page of text.", 4)},
	}})

	if len(pages) != 3 {
		t.Fatalf("got %d pages, want 3 including the blank one", len(pages))
	}
	for i, want := range []bool{true, false, true} {
		if got := pages[i].HasText(); got != want {
			t.Errorf("page %d has text = %t, want %t", pages[i].No, got, want)
		}
		if pages[i].No != i+1 {
			t.Errorf("page at index %d is numbered %d; numbering must follow the PDF", i, pages[i].No)
		}
	}
}

// TestExtractRunsReadsEveryPageOfAMultiSectionDocument checks the whole-document
// invocation against a document shaped like a real manual, because one invocation
// covering every page is the measurement the design rests on.
func TestExtractRunsReadsEveryPageOfAMultiSectionDocument(t *testing.T) {
	requirePDFToHTML(t)

	codes := []string{"EN", "DE", "FR", "PL"}
	const perSection = 3
	pages := extract(t, testpdf.TaggedSections(codes, perSection, true))

	want := len(codes)*perSection + 1 // sections plus the contents page
	if len(pages) != want {
		t.Fatalf("got %d pages, want %d", len(pages), want)
	}
	for i := range pages {
		if !pages[i].HasText() {
			t.Errorf("page %d yielded no runs", pages[i].No)
		}
	}

	// The printed language tab is the first line of each section page, so it must
	// arrive as a run of its own for ColumnLanguages to find it.
	second := runTexts(pages[1].Runs)
	if len(second) == 0 || strings.TrimSpace(second[0]) != codes[0] {
		t.Errorf("first run of the first section page = %q, want the printed tag %q",
			second, codes[0])
	}
}

// TestExtractRunsReadsTheFontOfEachRun drives the font through real poppler
// rather than through XML written by hand, which is the only way to catch the
// reader agreeing with an assumption instead of with the tool.
//
// It needed testpdf to grow a second face: everything it wrote was Helvetica at
// one size, so no generated document could vary either. A standard Type 1 font
// costs one more object and no embedded file, which is why that was preferred to
// making this test fixture-only — the fixtures measure the distribution, but they
// skip without MANUALBOX_TEST_FIXTURES and cannot guard the default suite.
//
// What it can and cannot reach is worth stating. Poppler reports both faces as
// the family "Helvetica" — a standard font is not embedded, so there is no subset
// name carrying a weight — so this exercises size and poppler's own <b> verdict,
// and the family-name weight is unit-tested against the real fixture families in
// runs_internal_test.go instead.
func TestExtractRunsReadsTheFontOfEachRun(t *testing.T) {
	requirePDFToHTML(t)

	const headingText = "A heading in the heavier face"
	const bodyText = "Ordinary body text on this page."
	pages := extract(t, testpdf.Doc{Pages: []testpdf.Page{{
		Headings: []string{headingText},
		Lines:    []string{bodyText},
	}}})

	byText := make(map[string]doc.Font, 2)
	for i := range pages[0].Runs {
		r := &pages[0].Runs[i]
		byText[strings.TrimSpace(r.Text)] = r.Font
	}
	heading, ok := byText[headingText]
	if !ok {
		t.Fatalf("the heading did not come back as a run of its own; got %q", runTexts(pages[0].Runs))
	}
	body := byText[bodyText]

	// Exact, because these are the sizes written scaled by the same 1.5 the page
	// box is scaled by — 17pt and 11pt. That the size shares the coordinate space
	// rather than being the PDF's point size is a property of the format, and a
	// caller comparing a size against a run height depends on it.
	if heading.Size != 26 || body.Size != 17 {
		t.Errorf("heading size %g and body size %g, want 26 and 17 — poppler scales "+
			"the fontspec size by 1.5 exactly as it scales the coordinates",
			heading.Size, body.Size)
	}
	if heading.Family == "" || body.Family == "" {
		t.Errorf("family missing: heading %q, body %q", heading.Family, body.Family)
	}

	// The weight, which is the whole reason the font is read at all. Poppler names
	// both faces "Helvetica", so its own verdict is the only signal here — and that
	// is exactly the case measured on the real manual as FuturaBQ, 78 runs of 78
	// marked bold with nothing in the name to say so.
	if !heading.MarkedBold {
		t.Errorf("the heading was not marked bold: %+v — without a weight, a larger "+
			"size cannot tell a heading from the larger regular face that real "+
			"manuals set safety text in", heading)
	}
	if body.MarkedBold {
		t.Errorf("body text was marked bold: %+v", body)
	}
	if heading.MarkedItalic || body.MarkedItalic {
		t.Error("nothing on this page is italic")
	}
}

func TestExtractRunsRejectsAMissingFile(t *testing.T) {
	requirePDFToHTML(t)

	_, err := doc.ExtractRuns(context.Background(), filepath.Join(t.TempDir(), "absent.pdf"))
	if err == nil {
		t.Fatal("extracting a file that does not exist succeeded")
	}
	// The message reaches documents.last_error and the log, so it must not carry
	// the directory: a blob path sits under the data directory and so under a home
	// directory, which names the operating-system user. See privacy.md threat 2.
	if strings.Contains(err.Error(), os.TempDir()) {
		t.Errorf("error leaks the containing directory: %v", err)
	}
}

func runTexts(runs []doc.TextRun) []string {
	out := make([]string, 0, len(runs))
	for i := range runs {
		out = append(out, runs[i].Text)
	}
	return out
}
