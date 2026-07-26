package doc_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/extern"
	"github.com/gordon2/manualbox/internal/testpdf"
)

// These run the whole figure path — pdftocairo, the guards, pdftoppm — against a
// PDF this package writes, so the default suite covers it offline with nothing
// committed.
//
// What they cannot cover is what the fixture tests are for. A generated drawing is
// clean: no clip path inflating its box, no gradient mesh of 80,000 hairlines, no
// language badge repeated on 110 pages, no ruled table shaped exactly like a framed
// illustration. Every constant in figures.go came from the real manuals, and these
// tests check the plumbing between them rather than the judgement.
//
// testpdf grew a Drawings field for this. It wrote text only, and a document with
// no vector graphics exercises none of this — which is also the finding that
// justified generating vector rather than embedding a raster: over 628 pages of the
// two real manuals, pdfimages yields no illustration at all.

func figurePDF(t *testing.T, d testpdf.Doc) string {
	t.Helper()
	for _, tool := range []extern.Tool{extern.PDFToHTML, extern.PDFToCairo, extern.PDFToPPM} {
		if !extern.Available(tool) {
			t.Skipf("%s is not installed", tool.Name)
		}
	}
	path := filepath.Join(t.TempDir(), "generated.pdf")
	if err := os.WriteFile(path, d.Build(), 0o600); err != nil {
		t.Fatalf("write the generated PDF: %v", err)
	}
	return path
}

// TestAGeneratedDrawingComesBackAsAFigure is the end-to-end check: a drawing is
// written into a PDF, and the same drawing comes back with a rectangle in the
// right place and bytes that are a PNG of the right size.
func TestAGeneratedDrawingComesBackAsAFigure(t *testing.T) {
	// 200x150 points at (100, 400) on the 612x792 page, with 40 strokes inside —
	// well over minFigureInk. Poppler's coordinates are 1.5x and measured down from
	// the top, so the frame arrives at x 150-450 and y 1.5*(792-550)=363 to
	// 1.5*(792-400)=588.
	path := figurePDF(t, testpdf.Doc{Pages: []testpdf.Page{{
		Lines:    []string{"A page with a picture on it"},
		Drawings: []testpdf.Drawing{{X: 100, Y: 400, W: 200, H: 150, Strokes: 40}},
	}}})

	pages, err := doc.ExtractRuns(context.Background(), path)
	if err != nil {
		t.Fatalf("ExtractRuns: %v", err)
	}
	figs, err := doc.PageFigures(context.Background(), path, &pages[0])
	if err != nil {
		t.Fatalf("PageFigures: %v", err)
	}
	if len(figs) != 1 {
		t.Fatalf("found %d figures, expected the one drawing", len(figs))
	}
	f := &figs[0]

	// The rectangle, in the 1.5-scaled space. A stroke has width, so the box runs a
	// fraction outside the frame's centre line; a point of slack covers that.
	for _, c := range []struct {
		name      string
		got, want float64
		slack     float64
	}{
		{"left", f.Rect.X0, 150, 2},
		{"right", f.Rect.X1, 450, 2},
		{"top", f.Rect.Y0, 363, 2},
		{"bottom", f.Rect.Y1, 588, 2},
	} {
		if d := c.got - c.want; d > c.slack || d < -c.slack {
			t.Errorf("%s edge = %.1f, expected about %.0f", c.name, c.got, c.want)
		}
	}

	// The frame plus its 40 strokes.
	if f.Ink != 41 {
		t.Errorf("ink = %d, expected the frame and its 40 strokes", f.Ink)
	}
	if f.Page != 1 || f.Index != 0 {
		t.Errorf("figure is page %d index %d, expected page 1 index 0", f.Page, f.Index)
	}

	// The bytes: a PNG, at 216 dpi, the rectangle doubled.
	if f.DPI != 216 {
		t.Errorf("rendered at %d dpi, expected 216", f.DPI)
	}
	if f.PixelWidth < 599 || f.PixelWidth > 606 {
		t.Errorf("width = %d pixels, expected about 600", f.PixelWidth)
	}
	if f.PixelHeight < 449 || f.PixelHeight > 456 {
		t.Errorf("height = %d pixels, expected about 450", f.PixelHeight)
	}
	if len(f.Digest) != 64 || len(f.PNG) == 0 {
		t.Errorf("digest %q over %d bytes; expected 64 hex characters over a PNG",
			f.Digest, len(f.PNG))
	}
	if string(f.PNG[:4]) != "\x89PNG" {
		t.Errorf("the bytes do not start with a PNG signature")
	}
}

// TestAPageOfTextAloneHasNoFigures is the negative, and it is the one that would
// catch the worst failure this code can have: reporting the page itself as a
// picture. Nothing here draws, so nothing here is a figure.
func TestAPageOfTextAloneHasNoFigures(t *testing.T) {
	path := figurePDF(t, testpdf.TaggedSections([]string{"EN", "DE"}, 2, true))

	pages, err := doc.ExtractRuns(context.Background(), path)
	if err != nil {
		t.Fatalf("ExtractRuns: %v", err)
	}
	for i := range pages {
		figs, err := doc.PageFigures(context.Background(), path, &pages[i])
		if err != nil {
			t.Fatalf("PageFigures page %d: %v", pages[i].No, err)
		}
		if len(figs) != 0 {
			t.Errorf("page %d of a text-only document returned %d figures: %v",
				pages[i].No, len(figs), figs[0].Rect)
		}
	}
}

// TestTwoDrawingsAreTwoFiguresInReadingOrder checks the plumbing the real
// documents cannot: separated drawings staying separate, and numbered down the
// page. On the columns manual this is exactly what a clip path defeats.
func TestTwoDrawingsAreTwoFiguresInReadingOrder(t *testing.T) {
	path := figurePDF(t, testpdf.Doc{Pages: []testpdf.Page{{
		Drawings: []testpdf.Drawing{
			// Lower on the page first, so the sort has something to do. In PDF
			// points Y counts up, so Y=120 is below Y=500.
			{X: 100, Y: 120, W: 180, H: 120, Strokes: 30},
			{X: 100, Y: 500, W: 180, H: 120, Strokes: 30},
		},
	}}})

	pages, err := doc.ExtractRuns(context.Background(), path)
	if err != nil {
		t.Fatalf("ExtractRuns: %v", err)
	}
	figs := doc.FindFigures(mustInk(t, path, 1), &pages[0])
	if len(figs) != 2 {
		t.Fatalf("found %d figures, expected 2", len(figs))
	}
	if figs[0].Rect.Y0 >= figs[1].Rect.Y0 {
		t.Errorf("figures came back bottom-first: %.0f then %.0f",
			figs[0].Rect.Y0, figs[1].Rect.Y0)
	}
	if figs[0].Index != 0 || figs[1].Index != 1 {
		t.Errorf("indexes are %d and %d, expected 0 and 1", figs[0].Index, figs[1].Index)
	}
}

// TestASimpleShapeIsNotAFigure is the shape guard end to end. Three strokes is a
// logo; the guard is what stops every page badge in a real manual becoming a
// picture.
func TestASimpleShapeIsNotAFigure(t *testing.T) {
	path := figurePDF(t, testpdf.Doc{Pages: []testpdf.Page{{
		Drawings: []testpdf.Drawing{{X: 100, Y: 400, W: 200, H: 150, Strokes: 2}},
	}}})

	pages, err := doc.ExtractRuns(context.Background(), path)
	if err != nil {
		t.Fatalf("ExtractRuns: %v", err)
	}
	if figs := doc.FindFigures(mustInk(t, path, 1), &pages[0]); len(figs) != 0 {
		t.Errorf("a frame with two strokes in it came back as a figure: %v", figs[0].Rect)
	}
}

// TestExtractInkRejectsANonPage covers the argument check, which is the one error
// path a caller can hit without poppler being involved.
func TestExtractInkRejectsANonPage(t *testing.T) {
	if _, err := doc.ExtractInk(context.Background(), "irrelevant.pdf", 0); err == nil {
		t.Error("page 0 was accepted")
	}
	if _, err := doc.PageFigures(context.Background(), "irrelevant.pdf", nil); err == nil {
		t.Error("a nil page was accepted")
	}
}

func mustInk(t *testing.T, path string, page int) []doc.Ink {
	t.Helper()
	ink, err := doc.ExtractInk(context.Background(), path, page)
	if err != nil {
		t.Fatalf("ExtractInk page %d: %v", page, err)
	}
	return ink
}
