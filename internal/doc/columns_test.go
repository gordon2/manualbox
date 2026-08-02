package doc

import (
	"fmt"
	"strings"
	"testing"
)

// The fixtures below are synthetic, but their geometry is not invented: every
// coordinate is taken from the Thomas DryBox Amfibia manual read through
// `pdftohtml -xml`, whose page is 892 by 850 units. Each named page reproduces
// one page of that document, including the traps — the hanging indent, the
// nested table, the spanning heading, the production artifacts and the
// off-page ghosts — so that the properties being asserted are the ones that
// actually broke earlier attempts, and so the suite stays hermetic.
//
// The expected answers are the human-verified column counts and starts
// recorded for those pages, checked against page images.

const (
	testPageW   = 892
	testPageH   = 850
	testLineH   = 17 // body text height throughout the fixture
	testPitch   = 21 // baseline-to-baseline
	testBodyTop = 65
)

// textBlock stacks lines of body text at x, each set to the full measure
// except the last line of every paragraph, which is short — the taper that
// makes a column's right-hand crossing count fall away.
func textBlock(x, width, top float64, lines int) []TextRun {
	out := make([]TextRun, 0, lines)
	for i := range lines {
		w := width
		if i%7 == 6 {
			w = width * 0.55
		}
		out = append(out, TextRun{
			X: x, Y: top + float64(i)*testPitch,
			Width: w, Height: testLineH,
			Text: fmt.Sprintf("body line %d", i),
		})
	}
	return out
}

// hangingList sets a numbered list the way the fixture's parts lists are set:
// a narrow marker at x, its text indented by hang, and every so often a
// sub-heading run at the outer margin set to the full measure. Those
// full-measure lines are the reason the indent is not a gutter, and leaving
// them out is a distinct test below.
func hangingList(x, hang, width, top float64, items, fullMeasureEvery int) []TextRun {
	var out []TextRun
	y := top
	for i := range items {
		if fullMeasureEvery > 0 && i%fullMeasureEvery == fullMeasureEvery-1 {
			out = append(out, TextRun{
				X: x, Y: y, Width: width, Height: testLineH,
				Text: fmt.Sprintf("sub-heading %d, set to the full measure", i),
			})
			y += testPitch
			continue
		}
		out = append(out,
			TextRun{X: x, Y: y, Width: 12, Height: testLineH, Text: fmt.Sprintf("%d", i+1)},
			TextRun{X: x + hang, Y: y, Width: width - hang, Height: testLineH,
				Text: fmt.Sprintf("part name %d", i)},
		)
		y += testPitch
	}
	return out
}

// productionSlug is the InDesign filename slug and export timestamp that the
// fixture's placed graphics drag onto 67 of its 68 pages, scaled down with the
// artwork they belong to. Two to six units tall against a body median of 17,
// and this pair is deliberately laid across a gutter.
func productionSlug(x, y float64) []TextRun {
	return []TextRun{
		{X: x, Y: y, Width: 59, Height: 4, Text: "29924_Saugerbeschriftungen_DryBoxAmfibia.indd   1"},
		{X: x + 135, Y: y, Width: 18, Height: 2, Text: "16.08.17   13:43"},
	}
}

// rotatedNote is a marginal note printed on its side. Poppler reports rotated
// text with width 0, at a single x.
func rotatedNote(x, top float64, lines int) []TextRun {
	out := make([]TextRun, 0, lines)
	for i := range lines {
		out = append(out, TextRun{
			X: x, Y: top + float64(i)*testPitch, Width: 0, Height: testLineH,
			Text: "text turned on its side",
		})
	}
	return out
}

func concat(groups ...[]TextRun) []TextRun {
	var out []TextRun
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// wantColumn is an expected column, as x-range.
type wantColumn struct{ min, max float64 }

func checkColumns(t *testing.T, got ColumnLayout, want []wantColumn) {
	t.Helper()
	if len(got.Columns) != len(want) {
		t.Errorf("got %d columns, want %d\nnote: %s", len(got.Columns), len(want), got.Note)
		for i := range got.Columns {
			t.Logf("  column %d: x=%.0f-%.0f runs=%d", i+1,
				got.Columns[i].Min, got.Columns[i].Max, got.Columns[i].Runs)
		}
		return
	}
	for i := range want {
		c := &got.Columns[i]
		if c.Min != want[i].min || c.Max != want[i].max {
			t.Errorf("column %d: got x=%.0f-%.0f, want x=%.0f-%.0f",
				i+1, c.Min, c.Max, want[i].min, want[i].max)
		}
		if c.Note == "" {
			t.Errorf("column %d has no note explaining itself", i+1)
		}
	}
}

// TestDetectColumnsGroundTruth reproduces the eight pages of the fixture whose
// columns a human verified against the page images.
func TestDetectColumnsGroundTruth(t *testing.T) {
	tests := []struct {
		name string
		runs []TextRun
		want []wantColumn
	}{
		{
			// Contents page: three equal columns, 262 wide, gutters 17 wide.
			name: "page 2, three columns with clear gutters",
			runs: concat(
				textBlock(43, 262, testBodyTop, 34),
				textBlock(323, 262, testBodyTop, 34),
				textBlock(604, 262, testBodyTop, 34),
				productionSlug(300, 780), // laid across the first gutter
			),
			want: []wantColumn{{43, 305}, {323, 585}, {604, 866}},
		},
		{
			// The facing page: two columns of the same measure, and a blank
			// right third that must not be offered as a column.
			name: "page 3, two columns and an empty third of the page",
			runs: concat(
				textBlock(30, 262, testBodyTop, 34),
				textBlock(310, 263, testBodyTop, 34),
			),
			want: []wantColumn{{30, 292}, {310, 573}},
		},
		{
			// Safety notices: two wide columns, 403 units each. Column widths
			// vary within one document and this is the widest pair.
			name: "page 6, two wide columns",
			runs: concat(
				textBlock(43, 403, testBodyTop, 33),
				textBlock(463, 403, testBodyTop, 19),
			),
			want: []wantColumn{{43, 446}, {463, 866}},
		},
		{
			// An exploded diagram fills two thirds of the page with numbered
			// callouts. None of them is a column; the one real column is the
			// parts list on the right, itself hanging-indented.
			name: "page 12, one text column beside a diagram of callouts",
			runs: concat(
				figureCallouts(),
				hangingList(604, 30, 262, testBodyTop, 30, 5),
				rotatedNote(874, 426, 14),
				productionSlug(560, 810),
			),
			want: []wantColumn{{604, 866}},
		},
		{
			// Parts lists in three languages, each hanging-indented 30 units
			// for its numbered markers, with rotated notes down the gutters.
			// The gutters here are 18 units, and the indents are 17 — telling
			// them apart by width alone is impossible, which is the point.
			name: "page 13, three columns each with a hanging indent",
			runs: concat(
				hangingList(30, 30, 261, testBodyTop, 32, 4),
				hangingList(310, 30, 260, testBodyTop, 32, 4),
				hangingList(591, 30, 260, testBodyTop, 32, 4),
				rotatedNote(300, 549, 6),
				rotatedNote(581, 549, 6),
				productionSlug(292, 800),
			),
			want: []wantColumn{{30, 291}, {310, 570}, {591, 851}},
		},
		{
			name: "page 41, three columns",
			runs: concat(
				textBlock(30, 262, testBodyTop, 26),
				textBlock(310, 262, testBodyTop, 26),
				textBlock(591, 260, testBodyTop, 26),
			),
			want: []wantColumn{{30, 292}, {310, 572}, {591, 851}},
		},
		{
			// One language in two columns, with a section heading printed
			// across both — the single run that binary coverage lets weld them
			// together — and a technical table nested in the left column whose
			// value alignment at x=192 is not a column.
			name: "page 63, two columns under a spanning heading, with a nested table",
			runs: concat(
				textBlock(30, 395, testBodyTop, 20),
				nestedTable(30, 192, testBodyTop+20*testPitch, 12),
				textBlock(451, 400, testBodyTop, 33),
				[]TextRun{{X: 91, Y: 16, Width: 376, Height: 23,
					Text: "Wskazówki dotyczące utylizacji | Obsługa serwisowa | Gwarancja"}},
			),
			want: []wantColumn{{30, 425}, {451, 851}},
		},
		{
			// Service addresses. Three columns of unequal width and irregular
			// pitch — 271 then 230 — so nothing here can lean on a regular
			// grid. The third column holds a second alignment at x=603 that is
			// not a fourth column. A banner heading spans all three, and seven
			// lines of a superseded address list are parked above the top edge
			// of the page where no reader will ever see them.
			name: "page 68, three unequal columns with an inner alignment",
			runs: concat(
				addressBlock(60, 235, testBodyTop+40, 20),
				addressBlock(331, 228, testBodyTop+40, 20),
				addressBlock(564, 276, testBodyTop+40, 12),
				addressBlock(603, 237, testBodyTop+320, 20),
				[]TextRun{{X: 62, Y: 102, Width: 623, Height: 23,
					Text: "Kundendienststellen | Serwis | Служба сервиса"}},
				offPageGhosts(),
				rotatedNote(873, 737, 7),
			),
			want: []wantColumn{{60, 295}, {331, 559}, {564, 840}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectColumns(tt.runs, testPageW, testPageH)
			checkColumns(t, got, tt.want)
		})
	}
}

// figureCallouts scatters numbered labels across an exploded diagram, the way
// the fixture's page 12 does: clusters of one to five short runs, never enough
// of them together to be a column.
func figureCallouts() []TextRun {
	at := [][2]float64{
		{144, 195}, {248, 210}, {90, 293}, {144, 318}, {90, 347}, {450, 293},
		{450, 347}, {450, 396}, {450, 436}, {60, 292}, {202, 100}, {467, 135},
		{487, 170}, {300, 209}, {152, 530}, {264, 530}, {202, 605}, {318, 578},
		{469, 599}, {537, 596}, {410, 690}, {487, 669}, {80, 636}, {166, 626},
		{78, 722}, {166, 722}, {247, 745}, {295, 715}, {345, 745}, {392, 745},
		{449, 771}, {541, 731}, {306, 686}, {357, 620}, {434, 650},
	}
	out := make([]TextRun, 0, len(at))
	for i, p := range at {
		out = append(out, TextRun{
			X: p[0], Y: p[1], Width: 14, Height: testLineH,
			Text: fmt.Sprintf("%d", i+1),
		})
	}
	return out
}

// nestedTable sets a two-column technical table inside a text column: labels
// at the column's own left edge, values aligned at valueX. The gap between
// them is a real gap in these rows, and it is not a column boundary, because
// the paragraphs above the table cross it.
func nestedTable(labelX, valueX, top float64, rows int) []TextRun {
	out := make([]TextRun, 0, rows*2)
	for i := range rows {
		y := top + float64(i)*testPitch
		out = append(out,
			TextRun{X: labelX, Y: y, Width: 155, Height: testLineH,
				Text: fmt.Sprintf("property %d:", i)},
			TextRun{X: valueX, Y: y, Width: 148, Height: testLineH,
				Text: fmt.Sprintf("value %d", i)},
		)
	}
	return out
}

// addressBlock sets short ragged lines, as a postal address is set: only its
// longest line reaches the full measure, so the column has almost no ink at its
// right edge and the gutter beside it is far wider than the nominal gap. This
// is why the fixture's back page has a 13-unit gap between two columns whose
// text is 4 units apart.
func addressBlock(x, width, top float64, lines int) []TextRun {
	ratios := []float64{1.0, 0.62, 0.71, 0.55, 0.68, 0.60, 0.74, 0.58}
	out := make([]TextRun, 0, lines)
	for i := range lines {
		out = append(out, TextRun{
			X: x, Y: top + float64(i)*testPitch,
			Width: width * ratios[i%len(ratios)], Height: testLineH,
			Text: fmt.Sprintf("address line %d", i),
		})
	}
	return out
}

// offPageGhosts is a superseded address list left above the top edge of the
// page. It is invisible in print and in the raster, but it is in the text
// layer, and it lies straight across two of the three gutters.
func offPageGhosts() []TextRun {
	tops := []float64{-357, -307, -234, -179, -126, -97, -56}
	out := make([]TextRun, 0, len(tops))
	for i, y := range tops {
		out = append(out, TextRun{
			X: 62, Y: y, Width: 380, Height: 18,
			Text: fmt.Sprintf("Kundendienststellen / After Sales Service Addresses %d", i),
		})
	}
	return out
}

func TestDetectColumnsEmptyPage(t *testing.T) {
	for _, tt := range []struct {
		name string
		runs []TextRun
	}{
		{"no runs at all", nil},
		{"empty slice", []TextRun{}},
		{"only whitespace", []TextRun{
			{X: 30, Y: 65, Width: 200, Height: 17, Text: "   "},
			{X: 30, Y: 86, Width: 200, Height: 17, Text: "\n\t"},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectColumns(tt.runs, testPageW, testPageH)
			if len(got.Columns) != 0 {
				t.Errorf("got %d columns, want none", len(got.Columns))
			}
			if got.Note == "" {
				t.Error("an empty page must still explain itself")
			}
		})
	}
}

func TestDetectColumnsSingleColumn(t *testing.T) {
	got := DetectColumns(textBlock(43, 806, testBodyTop, 30), testPageW, testPageH)
	checkColumns(t, got, []wantColumn{{43, 849}})
	if len(got.Gutters) != 0 {
		t.Errorf("got %d gutters on a one-column page, want none", len(got.Gutters))
	}
}

// TestDetectColumnsAllCallouts covers a page that is nothing but a diagram.
// Reporting no column is the right answer; naming one would be worse than
// silence, because nothing downstream could tell it was wrong.
func TestDetectColumnsAllCallouts(t *testing.T) {
	got := DetectColumns(figureCallouts(), testPageW, testPageH)
	if len(got.Columns) != 0 {
		t.Errorf("got %d columns from figure callouts alone, want none", len(got.Columns))
		for i := range got.Columns {
			t.Logf("  column %d: x=%.0f-%.0f runs=%d",
				i+1, got.Columns[i].Min, got.Columns[i].Max, got.Columns[i].Runs)
		}
	}
	if !strings.Contains(got.Note, "text runs a column needs") {
		t.Errorf("note should say the density floor was not met, got %q", got.Note)
	}
}

// TestDetectColumnsHangingIndentWithoutFullMeasureLines is the case the
// fixture only narrowly avoids: a list where every single line is indented, so
// nothing crosses the space between the markers and their text. The band is a
// gutter by every test but one, and only its width says otherwise.
func TestDetectColumnsHangingIndentWithoutFullMeasureLines(t *testing.T) {
	got := DetectColumns(
		hangingList(30, 30, 261, testBodyTop, 32, 0),
		testPageW, testPageH)
	checkColumns(t, got, []wantColumn{{30, 291}})
}

// TestDetectColumnsStripOffBaselineIsNotAbsorbed is the other half of the
// hanging-indent rule. A narrow strip beside a column is folded into it only
// when the two share lines; a vertical run of figure labels that happens to sit
// there does not, and must not stretch the column to reach it.
func TestDetectColumnsStripOffBaselineIsNotAbsorbed(t *testing.T) {
	// Labels at half the line pitch, so no two of them land on a body line.
	var labels []TextRun
	for i := range 12 {
		labels = append(labels, TextRun{
			X: 560, Y: testBodyTop + 10 + float64(i)*testPitch*2, Width: 14, Height: testLineH,
			Text: fmt.Sprintf("%d", i),
		})
	}
	got := DetectColumns(concat(textBlock(604, 262, testBodyTop, 30), labels),
		testPageW, testPageH)
	checkColumns(t, got, []wantColumn{{604, 866}})
}

// TestDetectColumnsSubIndentIsNotAColumn isolates the nested-table trap: a
// value alignment 162 units into a column, which a left-alignment peak finder
// cannot tell from a column start.
func TestDetectColumnsSubIndentIsNotAColumn(t *testing.T) {
	got := DetectColumns(concat(
		textBlock(30, 395, testBodyTop, 20),
		nestedTable(30, 192, testBodyTop+20*testPitch, 12),
	), testPageW, testPageH)
	checkColumns(t, got, []wantColumn{{30, 425}})
}

// TestDetectColumnsSpanningHeadingDoesNotMerge is the failure that binary
// coverage cannot escape: one heading run laid over both columns.
func TestDetectColumnsSpanningHeadingDoesNotMerge(t *testing.T) {
	body := concat(
		textBlock(30, 395, testBodyTop, 30),
		textBlock(451, 400, testBodyTop, 30),
	)
	heading := TextRun{X: 91, Y: 16, Width: 376, Height: 23, Text: "one heading over both"}

	before := DetectColumns(body, testPageW, testPageH)
	checkColumns(t, before, []wantColumn{{30, 425}, {451, 851}})

	after := DetectColumns(append(body, heading), testPageW, testPageH)
	checkColumns(t, after, []wantColumn{{30, 425}, {451, 851}})
	if after.Spanning != 1 {
		t.Errorf("got %d spanning runs, want 1", after.Spanning)
	}
	if len(after.Gutters) != 1 || after.Gutters[0].Crossings != 1 {
		t.Errorf("the gutter should record the one run that crosses it, got %+v", after.Gutters)
	}
}

// TestDetectColumnsProductionArtifactInGutter checks that a slug lying across
// a gutter neither merges the columns nor stretches one into the margin.
func TestDetectColumnsProductionArtifactInGutter(t *testing.T) {
	body := concat(
		textBlock(43, 262, testBodyTop, 34),
		textBlock(323, 262, testBodyTop, 34),
		textBlock(604, 262, testBodyTop, 34),
	)
	want := []wantColumn{{43, 305}, {323, 585}, {604, 866}}
	checkColumns(t, DetectColumns(body, testPageW, testPageH), want)

	// Four placed graphics, each dragging the slug along; one pair sits in
	// each gutter and one runs off the right edge of the page.
	withSlugs := concat(body,
		productionSlug(300, 300), productionSlug(300, 640),
		productionSlug(586, 480), productionSlug(805, 854))
	got := DetectColumns(withSlugs, testPageW, testPageH)
	checkColumns(t, got, want)
	if got.Dropped.Small+got.Dropped.OffPage != 8 {
		t.Errorf("got %d artifact runs set aside, want 8 (%+v)",
			got.Dropped.Small+got.Dropped.OffPage, got.Dropped)
	}
}

// TestDetectColumnsRepeatedTextIsNotAnArtifact guards the filter that the
// artifacts tempt you into writing. On the fixture's back page "Robert Thomas"
// is printed twelve times, once per service address; a filter keyed on
// repetition within a page would delete 742 of its 769 runs.
func TestDetectColumnsRepeatedTextIsNotAnArtifact(t *testing.T) {
	var runs []TextRun
	for _, x := range []float64{60, 331, 610} {
		for row := range 4 {
			top := testBodyTop + float64(row)*140
			for line := range 5 {
				runs = append(runs, TextRun{
					X: x, Y: top + float64(line)*testPitch, Width: 230, Height: testLineH,
					Text: "Robert Thomas",
				})
			}
		}
	}
	got := DetectColumns(runs, testPageW, testPageH)
	checkColumns(t, got, []wantColumn{{60, 290}, {331, 561}, {610, 840}})
}

// TestDetectColumnsOffPageRunsIgnored covers the ghosts above the page edge.
// Without this the fixture's back page reports two columns instead of three.
func TestDetectColumnsOffPageRunsIgnored(t *testing.T) {
	body := concat(
		addressBlock(60, 235, testBodyTop+40, 20),
		addressBlock(331, 228, testBodyTop+40, 20),
		addressBlock(564, 276, testBodyTop+40, 20),
	)
	want := []wantColumn{{60, 295}, {331, 559}, {564, 840}}
	checkColumns(t, DetectColumns(body, testPageW, testPageH), want)

	got := DetectColumns(concat(body, offPageGhosts()), testPageW, testPageH)
	checkColumns(t, got, want)
	if got.Dropped.OffPage != 7 {
		t.Errorf("got %d off-page runs, want 7", got.Dropped.OffPage)
	}
}

// TestDetectColumnsRotatedRunsDoNotStretchAColumn: a note printed on its side
// in the margin is real text, but it is not a column and must not widen one.
func TestDetectColumnsRotatedRunsDoNotStretchAColumn(t *testing.T) {
	got := DetectColumns(concat(
		textBlock(604, 262, testBodyTop, 30),
		rotatedNote(874, 426, 14),
	), testPageW, testPageH)
	checkColumns(t, got, []wantColumn{{604, 866}})
	if got.Dropped.Rotated != 14 {
		t.Errorf("got %d rotated runs, want 14", got.Dropped.Rotated)
	}
}

// TestDetectColumnsUnequalWidths states the property the whole exercise exists
// for: nothing may assume a fixed width, count or pitch.
func TestDetectColumnsUnequalWidths(t *testing.T) {
	got := DetectColumns(concat(
		textBlock(30, 120, testBodyTop, 20),
		textBlock(180, 300, testBodyTop, 20),
		textBlock(520, 340, testBodyTop, 20),
	), testPageW, testPageH)
	checkColumns(t, got, []wantColumn{{30, 150}, {180, 480}, {520, 860}})
}

func TestDetectColumnsDegeneratePageWidth(t *testing.T) {
	runs := textBlock(30, 200, testBodyTop, 20)
	for _, w := range []float64{0, -5, 1, maxProjectionBuckets + 10} {
		got := DetectColumns(runs, w, testPageH)
		if len(got.Columns) != 0 {
			t.Errorf("page width %g: got %d columns, want none", w, len(got.Columns))
		}
		if got.Note == "" {
			t.Errorf("page width %g: no note explaining the refusal", w)
		}
	}
}

// TestDetectColumnsNoteIsCheckable: every number a caller is shown must be one
// they could verify against the page.
func TestDetectColumnsNoteIsCheckable(t *testing.T) {
	got := DetectColumns(concat(
		textBlock(43, 262, testBodyTop, 34),
		textBlock(323, 262, testBodyTop, 34),
		productionSlug(300, 300),
	), testPageW, testPageH)

	for _, want := range []string{"2 text columns", "43-305", "323-585", "sub-legible"} {
		if !strings.Contains(got.Note, want) {
			t.Errorf("note %q does not mention %q", got.Note, want)
		}
	}
	if got.Runs != 68 {
		t.Errorf("got %d projected runs, want 68", got.Runs)
	}
	if total := got.Dropped.Total(); total != 2 {
		t.Errorf("got %d dropped runs, want 2", total)
	}
}
