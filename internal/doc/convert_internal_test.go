package doc

import (
	"reflect"
	"strings"
	"testing"
)

// The three rules of Convert, stated against geometry alone. They are the whole
// decision content of this file — everything else is spawning poppler — so they
// are tested where no poppler is needed and no fixture has to be downloaded.
//
// The coordinates are the real ones. Page 14 of the column manual is a
// three-column spread whose leftmost column is pictures: German at x=323-584,
// Polish at x=604-862, and two photographs of the machine at x=43-288 belonging
// to neither. Verified against a 108 dpi render of that page.

// page14 is that page's regions.
func page14() []Region {
	return []Region{
		{Page: 14, X0: 323, X1: 584, Code: "DE", Lang: "de"},
		{Page: 14, X0: 604, X1: 862, Code: "PL", Lang: "pl"},
	}
}

func figureAt(x0, x1 float64) *Figure {
	return &Figure{Page: 14, Rect: CellRect{X0: x0, Y0: 241, X1: x1, Y1: 431}}
}

func attributeOn(f *Figure, regions []Region, scope ...string) (ConvertedFigure, bool) {
	inScope := map[string]bool{}
	for _, l := range scope {
		inScope[l] = true
	}
	onPage := make([]int, len(regions))
	for i := range regions {
		onPage[i] = i
	}
	return attribute(f, regions, onPage, inScope, scope)
}

// TestAFigureInsideARegionBelongsToItsLanguage is rule 2's first half.
func TestAFigureInsideARegionBelongsToItsLanguage(t *testing.T) {
	got, ok := attributeOn(figureAt(340, 560), page14(), "de", "pl")
	if !ok {
		t.Fatal("a figure inside the German column was dropped")
	}
	if !reflect.DeepEqual(got.Langs, []string{"de"}) {
		t.Errorf("langs = %v, want just de: a figure inside one language's column is that language's", got.Langs)
	}
	if got.Neutral {
		t.Error("a figure inside a region reported itself as language-neutral")
	}
	if got.RegionX0 != 323 {
		t.Errorf("RegionX0 = %v, want the German region's 323 so the figure can be placed among its blocks", got.RegionX0)
	}
}

// TestALanguageNeutralFigureBelongsToEveryLanguage is rule 2's second half, and
// the decision docs/design/conversion.md records as the user's: a reader must not
// lose a diagram because the diagram has no language of its own.
func TestALanguageNeutralFigureBelongsToEveryLanguage(t *testing.T) {
	got, ok := attributeOn(figureAt(43, 288), page14(), "de", "pl")
	if !ok {
		t.Fatal("the shared picture column was dropped")
	}
	if !got.Neutral {
		t.Error("a figure inside no region did not report itself as language-neutral")
	}
	if !reflect.DeepEqual(got.Langs, []string{"de", "pl"}) {
		t.Errorf("langs = %v, want both languages in scope", got.Langs)
	}

	// And for a household reading one language it is still that language's, which
	// is what a reader of German alone must see on this page.
	one, ok := attributeOn(figureAt(43, 288), page14(), "de")
	if !ok || !reflect.DeepEqual(one.Langs, []string{"de"}) || !one.Neutral {
		t.Errorf("for a German-only household the shared picture came back ok=%t neutral=%t langs=%v",
			ok, one.Neutral, one.Langs)
	}
}

// TestAFigureInAnotherLanguagesColumnIsDropped is rule 1, the funnel, applied to
// pictures rather than to text. A German household must not be handed the Polish
// column's screenshot.
func TestAFigureInAnotherLanguagesColumnIsDropped(t *testing.T) {
	if got, ok := attributeOn(figureAt(620, 850), page14(), "de"); ok {
		t.Errorf("a figure inside the Polish column was given to a German household: %v", got.Langs)
	}
}

// TestAFigureStraddlingTwoRegionsIsNeutral is the case the containment test has
// to get right for the reason regions.md gives: a diagram set across the full
// measure of a parallel-columns page is inside no column and is everybody's.
func TestAFigureStraddlingTwoRegionsIsNeutral(t *testing.T) {
	got, ok := attributeOn(figureAt(400, 700), page14(), "de", "pl")
	if !ok || !got.Neutral {
		t.Errorf("a figure spanning the German and Polish columns came back ok=%t neutral=%t", ok, got.Neutral)
	}
}

// TestAFigureInAnUnnamedRegionIsNeutral keeps an unnamed region from becoming a
// language. A region no signal could name is a reportable state everywhere else
// in this package, and a picture inside one has no language either.
func TestAFigureInAnUnnamedRegionIsNeutral(t *testing.T) {
	regions := []Region{
		{Page: 14, X0: 40, X1: 300}, // no Code, no Lang: nothing was established
		{Page: 14, X0: 323, X1: 584, Code: "DE", Lang: "de"},
	}
	got, ok := attributeOn(figureAt(43, 288), regions, "de")
	if !ok || !got.Neutral {
		t.Errorf("a figure inside an unnamed region came back ok=%t neutral=%t; an unnamed "+
			"region is not a language", ok, got.Neutral)
	}
}

// TestAWholePageRegionOwnsEveryFigureOnIt is the sequential manual's shape, where
// a page is one language from edge to edge. The Russian maintenance pages depend
// on it: their eight line drawings each are inside the page's Russian region.
func TestAWholePageRegionOwnsEveryFigureOnIt(t *testing.T) {
	regions := []Region{{Page: 533, X0: 0, X1: 918, Code: "RU", Lang: "ru"}}
	f := &Figure{Page: 533, Rect: CellRect{X0: 88, Y0: 200, X1: 400, Y1: 420}}
	got, ok := attribute(f, regions, []int{0}, map[string]bool{"ru": true}, []string{"ru"})
	if !ok || got.Neutral || !reflect.DeepEqual(got.Langs, []string{"ru"}) {
		t.Errorf("a figure on a whole-page Russian region came back ok=%t neutral=%t langs=%v",
			ok, got.Neutral, got.Langs)
	}
}

// TestARegionsEdgeHasTheSameSlackABlockGets pins the one tolerance here against
// the one RegionBlocks allows, because two different slacks would put a figure
// outside a region whose text is inside it.
func TestARegionsEdgeHasTheSameSlackABlockGets(t *testing.T) {
	// Half a unit over the German region's right edge: inside.
	if got, _ := attributeOn(figureAt(323, 584.5), page14(), "de"); got.Neutral {
		t.Error("half a unit past the region's edge read as outside it")
	}
	// Two units over: outside, and so neutral.
	if got, _ := attributeOn(figureAt(323, 586), page14(), "de"); !got.Neutral {
		t.Error("two units past the region's edge still read as inside it")
	}
}

func TestConvertNeedsAResult(t *testing.T) {
	if _, err := Convert(t.Context(), "irrelevant.pdf", nil, []string{"de"}); err == nil {
		t.Error("a nil probe result was accepted")
	}
}

// TestConvertSaysWhyItConvertedNothing is the stance the rest of this package
// takes: a document is reported on, never failed, when something is missing.
func TestConvertSaysWhyItConvertedNothing(t *testing.T) {
	res := &Result{
		Info:       Info{Pages: 4},
		Runs:       []Run{{Lang: "de", Start: 1, End: 4, Source: SourceReconciled}},
		RegionNote: "pdftohtml is not installed",
	}

	// A household reading a language the document does not hold.
	got, err := Convert(t.Context(), "irrelevant.pdf", res, []string{"ja"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(got.Notes) != 1 || len(got.Blocks) != 0 {
		t.Errorf("notes = %v over %d blocks; expected one note and nothing converted",
			got.Notes, len(got.Blocks))
	}

	// A document whose regions could not be read at all. The note has to carry the
	// probe's own reason, or the user is told "nothing" without being told why.
	got, err = Convert(t.Context(), "irrelevant.pdf", res, []string{"de"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "pdftohtml") {
		t.Errorf("notes = %v; expected the probe's own RegionNote to be carried through", got.Notes)
	}
	if len(got.Scope.Languages) != 1 {
		t.Errorf("the scope was not reported: %v", got.Scope.Languages)
	}
}
