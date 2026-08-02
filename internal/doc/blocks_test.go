package doc_test

import (
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
)

// Hermetic tests for doc.RegionBlocks. No PDF and no poppler: the runs are built
// here, so each rule is stated where it can be read against the reasoning in
// blocks.go. The real-document acceptance lives in blocks_fixture_test.go, and
// every shape below is drawn from something one of the two manuals actually does.
//
// The font sizes are the ones poppler reports for these documents, not point
// sizes: 11, 15, 17 and 21 in the 1.5-scaled space [doc.Font] describes.

const (
	testBlockPageWidth  = 918
	testBlockPageHeight = 620
)

// blockPage builds a page of lines, one run each, at a fixed pitch.
type line struct {
	y      float64
	x      float64
	w      float64
	size   float64
	weight doc.Weight
	bold   bool
	text   string
}

func blockPage(no int, lines ...line) *doc.PageRuns {
	p := &doc.PageRuns{No: no, Width: testBlockPageWidth, Height: testBlockPageHeight}
	for _, l := range lines {
		p.Runs = append(p.Runs, doc.TextRun{
			X: l.x, Y: l.y, Width: l.w, Height: l.size + 5, Text: l.text,
			Font: doc.Font{
				Size: l.size, Family: "Test-Face", Weight: l.weight, MarkedBold: l.bold,
			},
		})
	}
	return p
}

// wholePage is the region a page in one language produces — rule 1 or 3 of
// doc.PageRegions, which is most of both manuals.
func wholePage(page int) *doc.Region {
	return &doc.Region{Page: page, X0: 0, X1: testBlockPageWidth, Lang: "de"}
}

// bodyLines returns n body lines at the given pitch, each set to the full measure.
func bodyLines(y0, pitch float64, n int, text string) []line {
	out := make([]line, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, line{y: y0 + float64(i)*pitch, x: 55, w: 700, size: 17, text: text})
	}
	return out
}

func kinds(blocks []doc.Block) []doc.BlockKind {
	out := make([]doc.BlockKind, len(blocks))
	for i := range blocks {
		out[i] = blocks[i].Kind
	}
	return out
}

// TestRegionBlocksHeadingThenParagraphs is the ordinary shape of a manual page:
// the sequential manual's page 23, a 21pt semibold heading over 17pt prose.
func TestRegionBlocksHeadingThenParagraphs(t *testing.T) {
	lines := []line{{y: 20, x: 55, w: 200, size: 21, weight: doc.WeightSemibold, bold: true,
		text: "Sicherheitshinweise"}}
	lines = append(lines, bodyLines(70, 22, 4, "Lesen Sie die Bedienungsanleitung vor der Verwendung")...)
	lines = append(lines, bodyLines(200, 22, 4, "Bewahren Sie sie zum spaeteren Nachschlagen auf")...)

	got := doc.RegionBlocks(blockPage(23, lines...), wholePage(23), nil, nil)

	if len(got) != 3 {
		t.Fatalf("got %d blocks, want a heading and two paragraphs: %v", len(got), kinds(got))
	}
	if got[0].Kind != doc.BlockHeading {
		t.Errorf("first block is %s, want a heading — %s", got[0].Kind, got[0].Note)
	}
	if got[0].Level != 1 {
		t.Errorf("heading level = %d, want 1: it is set larger than the body", got[0].Level)
	}
	if got[0].Text != "Sicherheitshinweise" {
		t.Errorf("heading text = %q", got[0].Text)
	}
	for i := 1; i < 3; i++ {
		if got[i].Kind != doc.BlockParagraph {
			t.Errorf("block %d is %s, want a paragraph — %s", i, got[i].Kind, got[i].Note)
		}
		if got[i].Lines != 4 {
			t.Errorf("block %d folded %d lines, want the 4 written into it", i, got[i].Lines)
		}
	}
}

// TestRegionBlocksSplitParagraphsOnAGap is the paragraph rule on its own: the
// same face throughout, so only the vertical gap can separate them.
func TestRegionBlocksSplitParagraphsOnAGap(t *testing.T) {
	var lines []line
	lines = append(lines, bodyLines(20, 22, 5, "erster Absatz")...)
	// 44 is two pitches down, which is what a blank line looks like.
	lines = append(lines, bodyLines(20+5*22+44, 22, 5, "zweiter Absatz")...)

	got := doc.RegionBlocks(blockPage(30, lines...), wholePage(30), nil, nil)

	if len(got) != 2 {
		t.Fatalf("got %d blocks, want 2: %v", len(got), kinds(got))
	}
	if !strings.Contains(got[0].Text, "erster") || strings.Contains(got[0].Text, "zweiter") {
		t.Errorf("the first paragraph reads %q; the gap did not end it", got[0].Text)
	}
}

// TestRegionBlocksKeepAParagraphWhoseLinesAreOnePitchApart is the other side of
// that rule, and the one a too-eager gap threshold breaks: every line of a
// paragraph is a gap, and turning each into a block is worse than merging two.
func TestRegionBlocksKeepAParagraphWhoseLinesAreOnePitchApart(t *testing.T) {
	got := doc.RegionBlocks(blockPage(31, bodyLines(20, 22, 8, "eine Zeile")...), wholePage(31), nil, nil)

	if len(got) != 1 {
		t.Fatalf("got %d blocks for one paragraph of 8 lines: %v", len(got), kinds(got))
	}
	if got[0].Lines != 8 {
		t.Errorf("paragraph folded %d lines, want 8", got[0].Lines)
	}
}

// TestRegionBlocksReadAListWithHangingIndents is the column manual's guarantee
// clauses and the sequential manual's safety bullets: a marker line, then lines
// indented under it that belong to the same item.
func TestRegionBlocksReadAListWithHangingIndents(t *testing.T) {
	lines := []line{
		{y: 20, x: 55, w: 700, size: 17, text: "• Bei Beschaedigung des Netzkabels muss es ersetzt"},
		{y: 42, x: 77, w: 600, size: 17, text: "werden, die Sie beim Hersteller erhalten koennen."},
		{y: 64, x: 55, w: 700, size: 17, text: "• Benutzen Sie den Roboter nicht in einem Bereich,"},
		{y: 86, x: 77, w: 600, size: 17, text: "der ueber dem Boden freisteht."},
		{y: 108, x: 55, w: 700, size: 17, text: "• Stellen Sie den Roboter nicht auf den Kopf."},
		{y: 130, x: 55, w: 700, size: 17, text: "• Halten Sie Haare und Finger fern."},
	}
	got := doc.RegionBlocks(blockPage(24, lines...), wholePage(24), nil, nil)

	if len(got) != 4 {
		t.Fatalf("got %d blocks, want 4 list items: %v", len(got), kinds(got))
	}
	for i := range got {
		if got[i].Kind != doc.BlockListItem {
			t.Errorf("block %d is %s, want a list item — %s", i, got[i].Kind, got[i].Note)
		}
	}
	// The indented continuation must be inside its item, not a paragraph after it.
	if !strings.Contains(got[0].Text, "beim Hersteller") {
		t.Errorf("the first item reads %q; its second line was orphaned", got[0].Text)
	}
	if got[0].Lines != 2 || got[2].Lines != 1 {
		t.Errorf("items folded %d and %d lines, want 2 and 1", got[0].Lines, got[2].Lines)
	}
}

// TestRegionBlocksReadAListNumberedWithoutPunctuation is the column manual's
// parts lists, which print " 1 " and then the part's name with nothing between
// but a tab. Without the gap test those numbers are prose and page 11's nine
// parts fold into one paragraph.
func TestRegionBlocksReadAListNumberedWithoutPunctuation(t *testing.T) {
	p := &doc.PageRuns{No: 11, Width: testBlockPageWidth, Height: testBlockPageHeight}
	names := []string{"Gehaeusedeckel", "Tragegriff", "Ansaugstutzen", "Schnellkupplung",
		"Laufraeder", "Netzstecker", "Frischwassertank", "Hauptschalter"}
	for i, name := range names {
		y := 62 + float64(i)*15
		f := doc.Font{Size: 14, Family: "Test-Face", Weight: doc.WeightLight}
		p.Runs = append(p.Runs,
			doc.TextRun{X: 591, Y: y, Width: 10, Height: 17, Text: " " + itoa(i+1) + " ", Font: f},
			doc.TextRun{X: 621, Y: y, Width: 120, Height: 17, Text: name, Font: f})
	}

	got := doc.RegionBlocks(p, &doc.Region{Page: 11, X0: 0, X1: testBlockPageWidth, Lang: "de"}, nil, nil)

	if len(got) != len(names) {
		t.Fatalf("got %d blocks for %d numbered parts: %v", len(got), len(names), kinds(got))
	}
	for i := range got {
		if got[i].Kind != doc.BlockListItem {
			t.Errorf("part %d is %s, want a list item — %s", i+1, got[i].Kind, got[i].Note)
		}
		if !strings.Contains(got[i].Text, names[i]) {
			t.Errorf("part %d reads %q, want it to contain %q", i+1, got[i].Text, names[i])
		}
	}
}

// TestRegionBlocksNeverPromoteSafetyCopyToAHeading is the measurement the whole
// heading rule exists for, and the revert check for it.
//
// docs/design/conversion.md records it in numbers: 17pt regular is 14.1% of the
// sequential manual at 65 characters a run, and it is safety prose. A rule that
// promotes what is larger than the body turns every line of it into a heading.
// The real heading here is smaller than that prose and heavier, which is also
// measured — "Nutzungsbeschränkungen" is 15pt semibold on a page set in 17.
// This is page 23 of the sequential manual in miniature, which is the shape that
// discriminates: the safety prose IS the body of its own region, so the real
// heading is SMALLER than the text it heads, and the display line above it is
// larger than the body in the same weight. Size alone gets both backwards, and
// the assertion is deliberately two-sided so that reverting the weight test fails
// twice rather than looking like a rounding difference.
func TestRegionBlocksNeverPromoteSafetyCopyToAHeading(t *testing.T) {
	lines := []line{
		// A short display line, larger than the body and in the body's own weight.
		// This is the safety copy set at a size above the measure on the page it
		// leads — the case conversion.md measured at 14.1% of a 560-page manual.
		{y: 20, x: 55, w: 260, size: 21, weight: doc.WeightRegular, text: "Wichtiger Hinweis"},
		// The body of this region: 17pt regular, the most characters by far.
		{y: 60, x: 55, w: 700, size: 17, weight: doc.WeightRegular,
			text: "Lesen Sie die Bedienungsanleitung vor der Verwendung sorgfaeltig"},
		{y: 82, x: 55, w: 700, size: 17, weight: doc.WeightRegular,
			text: "durch und bewahren Sie sie zum spaeteren Nachschlagen auf damit"},
		{y: 104, x: 55, w: 700, size: 17, weight: doc.WeightRegular,
			text: "Stromschlaege Braende oder Verletzungen durch unsachgemaessen"},
		{y: 126, x: 55, w: 700, size: 17, weight: doc.WeightRegular,
			text: "Gebrauch des Geraetes vermieden werden in Wohnraeumen und auch"},
		{y: 148, x: 55, w: 700, size: 17, weight: doc.WeightRegular,
			text: "ausserhalb davon auf allen Bodenbelaegen die dafuer geeignet sind"},
		// The real heading: smaller than the body it heads, and heavier.
		{y: 190, x: 55, w: 190, size: 15, weight: doc.WeightSemibold, bold: true,
			text: "Nutzungsbeschraenkungen"},
		{y: 220, x: 55, w: 700, size: 17, weight: doc.WeightRegular,
			text: "Um einen sicheren Betrieb dieses Produkts zu gewaehrleisten darf"},
		{y: 242, x: 55, w: 700, size: 17, weight: doc.WeightRegular,
			text: "es nicht von Kindern unter acht Jahren benutzt werden oder von"},
	}
	got := doc.RegionBlocks(blockPage(23, lines...), wholePage(23), nil, nil)

	var headings []string
	for i := range got {
		if got[i].Kind == doc.BlockHeading {
			headings = append(headings, got[i].Text)
		}
	}
	if len(headings) != 1 || headings[0] != "Nutzungsbeschraenkungen" {
		t.Errorf("headings = %q, want only the semibold one. The 15pt semibold heading is "+
			"SMALLER than the 17pt body it heads, and the 21pt regular display line is "+
			"larger in the body's own weight, so size alone gets both backwards", headings)
		for i := range got {
			t.Logf("  %-10s L%d %q — %s", got[i].Kind, got[i].Level, got[i].Text, got[i].Note)
		}
	}
	// And its level: at 15pt against a 17pt body it is not the most prominent thing
	// on the page, which is what level 2 records.
	if len(headings) == 1 && got[len(got)-2].Level == 1 {
		t.Errorf("the heading is level 1 although it is set smaller than the body")
	}
}

// TestRegionBlocksNeverPromoteAParagraphTail is the second half of that rule, and
// the correction the measurement forced: the last line of a paragraph is short by
// definition, so a paragraph set in a face heavier than the body hands its final
// line over as a heading. Measured, that produced 280 headings on the column
// manual reading "Umgebungen benutzt werden." and the like.
func TestRegionBlocksNeverPromoteAParagraphTail(t *testing.T) {
	lines := []line{
		{y: 20, x: 30, w: 700, size: 14, text: "Der leichte Grundtext dieser Region traegt die meisten Zeichen"},
		{y: 36, x: 30, w: 700, size: 14, text: "und legt damit fest was hier als Grundschrift gilt und was nicht"},
		{y: 52, x: 30, w: 700, size: 14, text: "so dass jede schwerere Schrift als Auszeichnung gelesen wird"},
		{y: 68, x: 30, w: 700, size: 14, text: "und nicht schon deshalb als Ueberschrift durchgehen darf"},
		// A whole paragraph in the heavier face, whose last line is short.
		{y: 100, x: 30, w: 700, size: 14, weight: doc.WeightMedium,
			text: "Das Geraet darf nicht in explosionsgefaehrdeten Raeumen oder in"},
		{y: 116, x: 30, w: 700, size: 14, weight: doc.WeightMedium,
			text: "unmittelbarer Naehe von brennbaren Stoffen und in feuchten"},
		{y: 132, x: 30, w: 250, size: 14, weight: doc.WeightMedium,
			text: "Umgebungen benutzt werden."},
	}
	got := doc.RegionBlocks(blockPage(4, lines...), wholePage(4), nil, nil)

	for i := range got {
		if got[i].Kind == doc.BlockHeading {
			t.Errorf("block %d is a heading reading %q — %s; it is the last line of the "+
				"paragraph above it, and a heading has to start a block",
				i, got[i].Text, got[i].Note)
		}
	}
	if len(got) != 2 {
		t.Errorf("got %d blocks, want the light paragraph and the medium one: %v",
			len(got), kinds(got))
	}
}

// TestRegionBlocksNeverPromoteAFigureCallout is the third: an exploded diagram's
// numbers are larger AND heavier than the page's body and two characters long, so
// they pass every typographic test. 26 of them are on the column manual's page 11.
func TestRegionBlocksNeverPromoteAFigureCallout(t *testing.T) {
	lines := []line{
		{y: 20, x: 30, w: 400, size: 14, weight: doc.WeightLight, text: "Der Grundtext dieser Seite"},
		{y: 36, x: 30, w: 400, size: 14, weight: doc.WeightLight, text: "traegt die meisten Zeichen"},
		{y: 52, x: 30, w: 400, size: 14, weight: doc.WeightLight, text: "und legt die Grundschrift fest"},
		{y: 68, x: 30, w: 400, size: 14, weight: doc.WeightLight, text: "damit sie vergleichbar wird"},
		// A callout: bigger, heavier, and set well away from anything.
		{y: 200, x: 184, w: 13, size: 17, weight: doc.WeightMedium, text: "14"},
		{y: 240, x: 452, w: 13, size: 17, weight: doc.WeightMedium, text: "16"},
	}
	got := doc.RegionBlocks(blockPage(11, lines...), wholePage(11), nil, nil)

	for i := range got {
		if got[i].Kind == doc.BlockHeading {
			t.Errorf("block %d is a heading reading %q — %s; a figure callout is a number "+
				"and a heading is words", i, got[i].Text, got[i].Note)
		}
	}
}

// TestRegionBlocksReadColumnsOneAtATime is the correction to what the contract
// reads like it says. A whole-page region can hold several text columns —
// regions.md rule 3 deliberately stores a page of same-language columns as one
// region — so sorting inside the region interleaves them, which is the mistake
// pdftotext -layout makes and the contract names.
func TestRegionBlocksReadColumnsOneAtATime(t *testing.T) {
	var lines []line
	for i := 0; i < 8; i++ {
		y := 20 + float64(i)*18
		lines = append(lines,
			line{y: y, x: 40, w: 350, size: 14, text: "links Zeile " + itoa(i)},
			// A slightly different baseline, which is what the column manual does:
			// its two columns drift apart by two units down the page.
			line{y: y + 2, x: 470, w: 350, size: 14, text: "rechts Zeile " + itoa(i)})
	}
	got := doc.RegionBlocks(blockPage(62, lines...), wholePage(62), nil, nil)

	if len(got) != 2 {
		t.Fatalf("got %d blocks, want one paragraph per column: %v", len(got), kinds(got))
	}
	if strings.Contains(got[0].Text, "rechts") {
		t.Errorf("the first block mixes both columns: %q", got[0].Text)
	}
	if strings.Contains(got[1].Text, "links") {
		t.Errorf("the second block mixes both columns: %q", got[1].Text)
	}
	// And in the right order: left column first.
	if !strings.HasPrefix(got[0].Text, "links") {
		t.Errorf("the first block is %q, want the left column", got[0].Text)
	}
}

// TestRegionBlocksReadOnlyInsideTheBox is the funnel, and the one failure a reader
// notices immediately. A boxed region is one language's column of a page that
// holds five, and a block built from a run outside the box is text in a language
// nobody asked for.
func TestRegionBlocksReadOnlyInsideTheBox(t *testing.T) {
	var lines []line
	for i := 0; i < 8; i++ {
		y := 20 + float64(i)*18
		lines = append(lines,
			line{y: y, x: 30, w: 250, size: 14, text: "deutsch Zeile " + itoa(i)},
			line{y: y, x: 320, w: 250, size: 14, text: "polnisch Zeile " + itoa(i)},
			line{y: y, x: 610, w: 250, size: 14, text: "russisch Zeile " + itoa(i)})
	}
	got := doc.RegionBlocks(blockPage(2, lines...),
		&doc.Region{Page: 2, X0: 30, X1: 280, Lang: "de"}, nil, nil)

	if len(got) == 0 {
		t.Fatal("the German column produced no blocks")
	}
	for i := range got {
		for _, other := range []string{"polnisch", "russisch"} {
			if strings.Contains(got[i].Text, other) {
				t.Errorf("block %d of the German region reads %q; it holds %s text",
					i, got[i].Text, other)
			}
		}
		if got[i].Lang != "de" {
			t.Errorf("block %d carries language %q, want the region's de", i, got[i].Lang)
		}
		if got[i].RegionX0 != 30 {
			t.Errorf("block %d records region x0 %.0f, want 30", i, got[i].RegionX0)
		}
	}
}

// TestRegionBlocksSingleLineRegion is a real shape: a caption beside a diagram,
// or a page holding one line of text.
func TestRegionBlocksSingleLineRegion(t *testing.T) {
	got := doc.RegionBlocks(
		blockPage(12, line{y: 40, x: 55, w: 300, size: 17, text: "Abb. A-1"}),
		wholePage(12), nil, nil)

	if len(got) != 1 {
		t.Fatalf("got %d blocks for one line: %v", len(got), kinds(got))
	}
	if got[0].Text != "Abb. A-1" {
		t.Errorf("text = %q", got[0].Text)
	}
	if got[0].Lines != 1 || got[0].Chars != 8 {
		t.Errorf("block reports %d lines and %d chars, want 1 and 8", got[0].Lines, got[0].Chars)
	}
}

func TestRegionBlocksEmptyRegion(t *testing.T) {
	page := &doc.PageRuns{No: 3, Width: testBlockPageWidth, Height: testBlockPageHeight}
	if got := doc.RegionBlocks(page, wholePage(3), nil, nil); len(got) != 0 {
		t.Errorf("got %d blocks for a page with no runs", len(got))
	}

	// A region whose box holds nothing, on a page that does hold text elsewhere.
	page = blockPage(4, bodyLines(20, 22, 6, "text weit rechts")...)
	if got := doc.RegionBlocks(page, &doc.Region{Page: 4, X0: 800, X1: 890}, nil, nil); len(got) != 0 {
		t.Errorf("got %d blocks for a box containing no runs", len(got))
	}
}

// TestRegionBlocksIgnoreWhatIsNotText guards the shared filter. The column
// manual's text layer carries 522 sub-legible production slugs and parks 218 runs
// of a superseded address list above the top edge of one page; a block built from
// those puts text in the reader that is not on the paper — and that the gate never
// charged for, since Region.Chars comes through the same filter.
func TestRegionBlocksIgnoreWhatIsNotText(t *testing.T) {
	lines := bodyLines(20, 22, 6, "echter Text auf der Seite")
	page := blockPage(9, lines...)
	clean := doc.RegionBlocks(page, wholePage(9), nil, nil)

	page.Runs = append(page.Runs,
		// A production slug: real text in the file, two units tall, invisible on paper.
		doc.TextRun{X: 55, Y: 300, Width: 250, Height: 2,
			Text: "Job_4417_Manual_v3_export_2019-11-08.indd   1   08.11.19   10:16"},
		// A run parked above the page, which is where a superseded address list lives.
		doc.TextRun{X: 55, Y: -38, Width: 250, Height: 22, Text: "Superseded address list line"})

	got := doc.RegionBlocks(page, wholePage(9), nil, nil)
	if len(got) != len(clean) {
		t.Errorf("blocks went from %d to %d when a sub-legible slug and an off-page run "+
			"were added; neither is text on the page", len(clean), len(got))
	}
	for i := range got {
		if strings.Contains(got[i].Text, "indd") || strings.Contains(got[i].Text, "Superseded") {
			t.Errorf("block %d reads %q", i, got[i].Text)
		}
	}
}

// TestRegionBlocksJoinRunsAsThePageShowsThem covers the two halves of joinRuns:
// poppler splits a run at every font change, so touching runs are one word and
// separated ones are two.
func TestRegionBlocksJoinRunsAsThePageShowsThem(t *testing.T) {
	p := &doc.PageRuns{No: 5, Width: testBlockPageWidth, Height: testBlockPageHeight}
	f := doc.Font{Size: 17, Family: "Test-Face"}
	// "Sollte" then a bold "THOMAS" then " einmal": three runs, a real gap between
	// the first two and none between the last two.
	p.Runs = append(p.Runs,
		doc.TextRun{X: 55, Y: 40, Width: 60, Height: 22, Text: "Sollte", Font: f},
		doc.TextRun{X: 120, Y: 40, Width: 70, Height: 22, Text: "THOMAS", Font: f},
		doc.TextRun{X: 190, Y: 40, Width: 60, Height: 22, Text: "-Geraet", Font: f})

	got := doc.RegionBlocks(p, wholePage(5), nil, nil)
	if len(got) != 1 {
		t.Fatalf("got %d blocks: %v", len(got), kinds(got))
	}
	if want := "Sollte THOMAS-Geraet"; got[0].Text != want {
		t.Errorf("text = %q, want %q", got[0].Text, want)
	}
}

// TestRegionBlocksAreNaturallyKeyed is the property conversion.md asks for: a
// second run over the same bytes must converge on the first rather than insert a
// parallel set, which requires the key to come out of the page and not out of a
// counter.
func TestRegionBlocksAreNaturallyKeyed(t *testing.T) {
	page := blockPage(62, bodyLines(20, 22, 6, "eine Zeile Text")...)
	region := &doc.Region{Page: 62, X0: 30, X1: 800, Lang: "de"}

	first := doc.RegionBlocks(page, region, nil, nil)
	second := doc.RegionBlocks(page, region, nil, nil)

	if len(first) != len(second) {
		t.Fatalf("two runs produced %d and %d blocks", len(first), len(second))
	}
	for i := range first {
		a, b := &first[i], &second[i]
		if a.Page != b.Page || a.RegionX0 != b.RegionX0 || a.Index != b.Index || a.Text != b.Text {
			t.Errorf("block %d differs between runs: %+v against %+v", i, *a, *b)
		}
		if a.Page != 62 || a.RegionX0 != 30 || a.Index != i {
			t.Errorf("block %d is keyed (page %d, x0 %.0f, index %d), want (62, 30, %d)",
				i, a.Page, a.RegionX0, a.Index, i)
		}
	}
}

// TestRegionsBlocksReadOnlyWhatIsInScope is the funnel at document level. A
// household reading German must not be given the Polish column of the same page.
func TestRegionsBlocksReadOnlyWhatIsInScope(t *testing.T) {
	var lines []line
	for i := 0; i < 8; i++ {
		y := 20 + float64(i)*18
		lines = append(lines,
			line{y: y, x: 30, w: 250, size: 14, text: "deutsch Zeile " + itoa(i)},
			line{y: y, x: 320, w: 250, size: 14, text: "polnisch Zeile " + itoa(i)})
	}
	pages := []doc.PageRuns{*blockPage(2, lines...)}
	regions := []doc.Region{
		{Page: 2, X0: 30, X1: 280, Lang: "de"},
		{Page: 2, X0: 320, X1: 570, Lang: "pl"},
	}

	got := doc.RegionsBlocks(pages, regions, map[string]bool{"de": true}, nil, nil)
	if len(got) == 0 {
		t.Fatal("no blocks for the German region")
	}
	for i := range got {
		if got[i].Lang != "de" || strings.Contains(got[i].Text, "polnisch") {
			t.Errorf("block %d is %q in %q, but only German was in scope",
				i, got[i].Text, got[i].Lang)
		}
	}

	// And with no scope, both regions are read, in left-edge order.
	all := doc.RegionsBlocks(pages, regions, nil, nil, nil)
	if len(all) <= len(got) {
		t.Errorf("reading every region gave %d blocks and German alone %d", len(all), len(got))
	}
	if all[0].RegionX0 != 30 {
		t.Errorf("first block comes from the region at x0 %.0f, want the leftmost at 30",
			all[0].RegionX0)
	}
}

// TestBlockSummaryDescribesTheShape keeps the log line honest, since it is what a
// fixture test reports instead of every row.
func TestBlockSummaryDescribesTheShape(t *testing.T) {
	if got := doc.BlockSummary(nil); got != "no blocks" {
		t.Errorf("BlockSummary(nil) = %q", got)
	}
	got := doc.BlockSummary([]doc.Block{
		{Page: 1, Kind: doc.BlockHeading, Chars: 10},
		{Page: 1, Kind: doc.BlockParagraph, Chars: 90},
		{Page: 2, Kind: doc.BlockListItem, Chars: 5},
	})
	for _, want := range []string{"3 blocks", "2 pages", "105 chars", "1 heading", "1 list-item"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q does not mention %q", got, want)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestEachContentsEntryIsItsOwnBlock is the defect itself: seventeen entries sit at
// exactly the line pitch, so the paragraph rule has nothing to separate them by and
// glued the whole contents page into one block of run-together dot leaders.
func TestEachContentsEntryIsItsOwnBlock(t *testing.T) {
	// Page 2 of the columns manual, the Russian column: left edge 604, 16-unit pitch,
	// the first three entries at their measured tops.
	page := &doc.PageRuns{No: 2, Width: 892, Height: 850, Runs: []doc.TextRun{
		{X: 604, Y: 62, Width: 259, Height: 17, Text: "Мы поздравляем Вас  ..............................2"},
		{X: 604, Y: 78, Width: 259, Height: 17, Text: "Использование по назначению  ....................4"},
		{X: 604, Y: 94, Width: 259, Height: 17, Text: "Указания по технике безопасности  ..............8"},
	}}
	blocks := doc.RegionBlocks(page, &doc.Region{Page: 2, X0: 604, X1: 866, Lang: "ru"}, nil, nil)

	var entries int
	for i := range blocks {
		if blocks[i].Kind == doc.BlockListItem && doc.IsContentsEntry(blocks[i].Note) {
			entries++
		}
	}
	if entries != 3 {
		t.Errorf("%d contents entries from 3 printed lines; %s", entries, doc.BlockSummary(blocks))
		for i := range blocks {
			t.Logf("  %s %q", blocks[i].Kind, blocks[i].Text)
		}
	}
}
