package doc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/testpdf"
)

// The whole of Convert against a PDF this package writes, so the default suite
// covers the assembly offline with nothing committed. What it cannot cover is
// the judgement — a generated document has no shared picture column, no cell
// dividers shaped like language boundaries and no section that is illustrated
// where the other thirty-three are not. Those are what convert_fixture_test.go
// is for.

// TestConvertReadsOnlyTheHouseholdsPages is the funnel end to end. Two languages,
// two pages each, a drawing on one page of each: a German household gets the
// German pages and the German drawing, and nothing of the Polish ones.
func TestConvertReadsOnlyTheHouseholdsPages(t *testing.T) {
	d := testpdf.TaggedSections([]string{"DE", "PL"}, 2, true)
	// The contents page is page 1, so DE is pages 2-3 and PL is 4-5. A drawing on
	// the first page of each section, well over the ink guard.
	d.Pages[1].Drawings = []testpdf.Drawing{{X: 100, Y: 400, W: 200, H: 150, Strokes: 40}}
	d.Pages[3].Drawings = []testpdf.Drawing{{X: 100, Y: 400, W: 200, H: 150, Strokes: 40}}
	path := figurePDF(t, d)

	res, err := doc.Analyze(context.Background(), path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	conv, err := doc.Convert(context.Background(), path, res, []string{"de"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	t.Logf("%s", conv.Summary())
	if len(conv.Notes) > 0 {
		t.Logf("notes: %v", conv.Notes)
	}

	// The pages. Not "fewer than five": exactly the two the German section owns,
	// because a page too many is a page the household is charged for.
	if len(conv.Pages) != 2 || conv.Pages[0] != 2 || conv.Pages[1] != 3 {
		t.Errorf("converted pages %v, want the German section's 2 and 3", conv.Pages)
	}

	// Every block German, and every block from a German page.
	if len(conv.Blocks) == 0 {
		t.Fatal("the German section produced no blocks")
	}
	for i := range conv.Blocks {
		b := &conv.Blocks[i]
		if doc.BaseLanguage(b.Lang) != "de" {
			t.Errorf("page %d block %d is %q, not German", b.Page, b.Index, b.Lang)
		}
		if b.Page < 2 || b.Page > 3 {
			t.Errorf("block from page %d, outside the German section", b.Page)
		}
	}

	// One drawing, the German one, and it belongs to German because it sits inside
	// the page's whole-page German region — not because it was the only one left.
	if len(conv.Figures) != 1 {
		t.Fatalf("got %d figures, want the one drawing on the German section's first page", len(conv.Figures))
	}
	f := &conv.Figures[0]
	if f.Page != 2 {
		t.Errorf("the figure is on page %d, want page 2; page 4's drawing is Polish", f.Page)
	}
	if f.Neutral {
		t.Error("the figure reported itself as language-neutral; it is inside a whole-page German region")
	}
	if len(f.Langs) != 1 || f.Langs[0] != "de" {
		t.Errorf("the figure's languages are %v, want just de", f.Langs)
	}
	if len(f.PNG) == 0 || len(f.Digest) != 64 {
		t.Errorf("the figure carries %d bytes and the digest %q; a converted figure has to carry its picture",
			len(f.PNG), f.Digest)
	}
	if len(conv.FiguresFor("de")) != 1 || len(conv.FiguresFor("pl")) != 0 {
		t.Errorf("FiguresFor: %d for German, %d for Polish", len(conv.FiguresFor("de")), len(conv.FiguresFor("pl")))
	}
	// The regional form a household may have configured: a reader of de-AT reads
	// the de section, which is the matching ScopeFor already did and which the
	// accessors must not undo.
	if len(conv.BlocksFor("de-AT")) != len(conv.Blocks) || len(conv.BlocksFor("pl")) != 0 {
		t.Errorf("BlocksFor: %d blocks for de-AT and %d for Polish, out of %d",
			len(conv.BlocksFor("de-AT")), len(conv.BlocksFor("pl")), len(conv.Blocks))
	}
}

// TestConvertStopsWhenItsJobIsCancelled separates the two kinds of bad news. A
// missing tool is a note; a cancelled job is an error, and it must not come back
// as a page-by-page complaint that the document could not be read.
func TestConvertStopsWhenItsJobIsCancelled(t *testing.T) {
	d := testpdf.TaggedSections([]string{"DE"}, 3, true)
	d.Pages[1].Drawings = []testpdf.Drawing{{X: 100, Y: 400, W: 200, H: 150, Strokes: 40}}
	path := figurePDF(t, d)

	res, err := doc.Analyze(context.Background(), path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conv, err := doc.Convert(ctx, path, res, []string{"de"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Convert returned %v with %v; a cancelled context is an error, not a note",
			err, conv)
	}
}

// TestConvertReportsALanguageTheDocumentDoesNotHold is the state a household
// reaches by configuring a language this manual was never printed in. It is
// reported, not failed.
func TestConvertReportsALanguageTheDocumentDoesNotHold(t *testing.T) {
	path := figurePDF(t, testpdf.TaggedSections([]string{"DE", "PL"}, 2, true))

	res, err := doc.Analyze(context.Background(), path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	conv, err := doc.Convert(context.Background(), path, res, []string{"ja"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(conv.Blocks) != 0 || len(conv.Figures) != 0 || len(conv.Pages) != 0 {
		t.Errorf("%d blocks, %d figures over %d pages for a language the document does not hold",
			len(conv.Blocks), len(conv.Figures), len(conv.Pages))
	}
	if len(conv.Notes) != 1 {
		t.Errorf("notes = %v; the user has to be told why nothing came back", conv.Notes)
	}
	if len(conv.Scope.OtherLanguages) != 2 {
		t.Errorf("%d other languages reported; the two the document does hold must still be offered",
			len(conv.Scope.OtherLanguages))
	}
}
