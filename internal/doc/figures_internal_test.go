package doc

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/extern"
	"github.com/gordon2/manualbox/internal/fixture"
)

// TestWhatTheTextGuardIsStillWorth removes the guard over both whole documents and
// records what changes, because twice now the answer has not been what was
// expected and both times the wrong belief was written down first.
//
// It was first claimed to be what rejects page 57 of the columns manual — two ruled
// tables, the largest ink clusters in that document. It is not: cairo draws each of
// those tables as about fourteen rectangles, under [minFigureInk], so the shape
// guard rejects them and the text guard never sees them.
//
// Then [trimToPicture] arrived and took most of the rest. Before it, the guard
// decided 1 cluster of the columns manual and 4 of the sequential one; after it, 0
// and 1 — because a candidate that had reached over a text column now has that
// column trimmed off instead of being thrown away whole, which is the better of the
// two outcomes. What is left is one cluster in 275 across both documents.
//
// Narrowing the trim to lines the box has reached over did not move that: the trims
// it stopped making are the ones that cut a label out of the middle of a drawing,
// and a drawing keeping its own label is nowhere near [maxFigureTextFraction]. The
// counts below are the same on both documents before and after.
//
// Reading the clip moved both totals — 46 to 59 figures on the columns manual and
// 229 to 238 on the sequential one, because drawings that had been merged into a
// neighbour are now separate — and merging candidate boxes that overlap moved the
// sequential one back to 195, because pieces of one drawing are one drawing again.
// Neither moved the verdict: the guard still decides nothing on the columns manual
// and still decides page 53 alone on the other.
//
// So this test asserts the measured numbers rather than "the guard does something",
// and the guard is kept on the reasoning [ruleWalker.filled] sets out: the shape it
// handles — a ruled table with more parts than minFigureInk and cells full of
// text — is an ordinary thing for a document to contain, and the next manual gets no
// say in which cases this code understands.
func TestWhatTheTextGuardIsStillWorth(t *testing.T) {
	if os.Getenv(fixture.EnableEnv) == "" {
		t.Skipf("set %s=1 to download the fixtures and run this", fixture.EnableEnv)
	}
	for _, tool := range []extern.Tool{extern.PDFToHTML, extern.PDFToCairo} {
		if !extern.Available(tool) {
			t.Skipf("%s is not installed", tool.Name)
		}
	}

	for _, tc := range []struct {
		name       string
		with, none int
		pages      []int
	}{
		{"thomas-drybox-amfibia", 59, 59, nil},
		// Page 53 prints the French recycling label — a picture with a paragraph
		// set inside it, 34.7% text, which is exactly the case the guard cannot
		// tell from a table and the reason its remaining decision is a loss rather
		// than a save.
		{"dreame-l40-ultra", 195, 196, []int{53}},
	} {
		name := tc.name
		t.Run(name, func(t *testing.T) {
			m, err := fixture.Load("../../testdata/fixtures", name)
			if err != nil {
				t.Fatalf("load manifest: %v", err)
			}
			path, err := m.Fetch(context.Background())
			if err != nil {
				t.Fatalf("fetch fixture: %v", err)
			}
			pages, err := ExtractRuns(context.Background(), path)
			if err != nil {
				t.Fatalf("ExtractRuns: %v", err)
			}

			var withGuard, withoutGuard int
			var gained []int
			for i := range pages {
				p := &pages[i]
				ink, err := ExtractInk(context.Background(), path, p.No)
				if err != nil {
					t.Fatalf("ExtractInk page %d: %v", p.No, err)
				}
				noText := defaultGuards
				noText.maxText = 1
				on := len(findFigures(ink, p, defaultGuards))
				off := len(findFigures(ink, p, noText))
				withGuard += on
				withoutGuard += off
				if off > on {
					gained = append(gained, p.No)
					for _, f := range findFigures(ink, p, noText) {
						if f.TextFraction > maxFigureTextFraction {
							t.Logf("  page %d rejected %v ink=%d text=%.1f%%",
								p.No, f.Rect, f.Ink, 100*f.TextFraction)
						}
					}
				}
			}
			t.Logf("%s: %d figures with the text guard, %d without; "+
				"the extra ones are on pages %v", name, withGuard, withoutGuard, gained)
			if withGuard != tc.with || withoutGuard != tc.none {
				t.Errorf("%d figures with the guard and %d without, expected %d and %d",
					withGuard, withoutGuard, tc.with, tc.none)
			}
			if len(gained) != len(tc.pages) {
				t.Errorf("the guard decides pages %v, expected %v", gained, tc.pages)
			}
			for i := range tc.pages {
				if i < len(gained) && gained[i] != tc.pages[i] {
					t.Errorf("the guard decides pages %v, expected %v", gained, tc.pages)
					break
				}
			}
		})
	}
}

// loadFigureInk reads every page's ink once, so a sweep over thresholds does not
// re-run pdftocairo 560 times per value.
func loadFigureInk(t *testing.T, name string) (pages []PageRuns, ink [][]Ink) {
	t.Helper()
	if os.Getenv(fixture.EnableEnv) == "" {
		t.Skipf("set %s=1 to download the fixtures and run this", fixture.EnableEnv)
	}
	for _, tool := range []extern.Tool{extern.PDFToHTML, extern.PDFToCairo} {
		if !extern.Available(tool) {
			t.Skipf("%s is not installed", tool.Name)
		}
	}
	m, err := fixture.Load("../../testdata/fixtures", name)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	path, err := m.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch fixture: %v", err)
	}
	pages, err = ExtractRuns(context.Background(), path)
	if err != nil {
		t.Fatalf("ExtractRuns: %v", err)
	}
	ink = make([][]Ink, len(pages))
	for i := range pages {
		ink[i], err = ExtractInk(context.Background(), path, pages[i].No)
		if err != nil {
			t.Fatalf("ExtractInk page %d: %v", pages[i].No, err)
		}
	}
	return pages, ink
}

// TestGuardSweep prints how each threshold behaves over both whole documents, one
// constant at a time with the rest held at their defaults. It is the measurement
// the constants in figures.go are set from, and it is a test rather than a script
// so that a later change can re-run it instead of trusting the numbers written
// down.
//
// It asserts only the one thing a sweep can assert: that the chosen value is not
// on a cliff. A threshold one step either side of the default must not move the
// figure count by more than a tenth.
func TestGuardSweep(t *testing.T) {
	for _, name := range []string{"thomas-drybox-amfibia", "dreame-l40-ultra"} {
		t.Run(name, func(t *testing.T) {
			pages, ink := loadFigureInk(t, name)
			count := func(g figureGuards) int {
				var n int
				for i := range pages {
					n += len(findFigures(ink[i], &pages[i], g))
				}
				return n
			}
			base := count(defaultGuards)
			t.Logf("%s: %d figures at the defaults", name, base)

			for _, v := range []int{2, 5, 10, 15, 20, 25, 30, 40, 60, 100, 200} {
				g := defaultGuards
				g.minInk = v
				t.Logf("  minFigureInk=%-3d -> %d figures", v, count(g))
			}
			for _, v := range []float64{10, 20, 30, 40, 50, 60, 80, 120} {
				g := defaultGuards
				g.minWidth, g.minHeight = v, v
				t.Logf("  minFigureSize=%-4.0f -> %d figures", v, count(g))
			}
			for _, v := range []float64{0.02, 0.05, 0.1, 0.15, 0.2, 0.3, 0.5, 1} {
				g := defaultGuards
				g.maxText = v
				t.Logf("  maxFigureTextFraction=%-4.2f -> %d figures", v, count(g))
			}

			// How many figures overlap a line of text at all. Zero would be the
			// ideal, and the shortfall is this file's main known cost — see
			// [minFigureWidth]'s neighbours in figures.go for the cause.
			var overlapping, figures int
			for i := range pages {
				var dropped DroppedRuns
				runs := usableRuns(pages[i].Runs, pages[i].Width, pages[i].Height, &dropped)
				for _, f := range findFigures(ink[i], &pages[i], defaultGuards) {
					figures++
					for j := range runs {
						r := &runs[j]
						if len([]rune(strings.TrimSpace(r.Text))) < 5 {
							continue
						}
						if textFraction(f.Rect, runs[j:j+1]) > 0 {
							overlapping++
							break
						}
					}
				}
			}
			t.Logf("  %d of %d figures overlap a line of five characters or more",
				overlapping, figures)

			if os.Getenv("MANUALBOX_FIGURE_SHOW") != "" {
				small := withSize(defaultGuards, 20)
				for i := range pages {
					for _, f := range findFigures(ink[i], &pages[i], small) {
						if f.Rect.Width() < minFigureWidth || f.Rect.Height() < minFigureHeight {
							t.Logf("  under the size floor: page %d %v %.0fx%.0f ink=%d",
								f.Page, f.Rect, f.Rect.Width(), f.Rect.Height(), f.Ink)
						}
					}
				}
			}

			// The ink guard sits on a plateau and that is asserted, because it is the
			// guard that separates a picture from page furniture and a value on a
			// cliff there would be a value fitted to these two documents.
			for _, step := range []struct {
				name string
				g    figureGuards
			}{
				{"minInk one lower", withInk(defaultGuards, minFigureInk-5)},
				{"minInk one higher", withInk(defaultGuards, minFigureInk+5)},
			} {
				got := count(step.g)
				if d := got - base; d > base/10 || -d > base/10 {
					t.Errorf("%s changes the count from %d to %d, more than a tenth; "+
						"the default is on a cliff", step.name, base, got)
				}
			}

			// The size floor deliberately is not asserted that way, because it has no
			// plateau to sit on: see [minFigureWidth]. What is asserted is the one
			// measured property of it — that on the columns manual it decides nothing
			// at all, so a change to it can only be a change to the other document.
			if name == "thomas-drybox-amfibia" {
				for _, v := range []float64{10, 40, 120} {
					if got := count(withSize(defaultGuards, v)); got != base {
						t.Errorf("the size floor at %.0f gives %d figures where the default "+
							"gives %d; on this document it used to decide nothing", v, got, base)
					}
				}
			}
		})
	}
}

// TestMergeThresholdSweep is the evidence behind [figureMergeOverlap], and the
// evidence is that there is nothing for a threshold to separate.
//
// It sweeps how much of the smaller of two candidate boxes may lie inside the other
// before they are read as one picture, from 1 — which disables the pass, since no
// overlap can exceed it — down to 0, and prints the count over each whole document.
// Two things are asserted rather than only printed.
//
// The first is that the parallel-columns manual does not move at any value. It has
// no page where two candidates overlap, so this whole change is the other document's
// and the manual whose pictures were counted by eye cannot lose one to it.
//
// The second is that the sequential manual sits on a plateau at the bottom of the
// range rather than on a cliff: 0, 0.01, 0.05 and 0.1 give 195, 194, 196 and 195
// figures. That is the claim the value rests on. The 53 overlapping pairs on that
// document run 1.00, 0.96, 0.91 … 0.11, 0.10, 0.01 with no gap, every one of them
// was rendered as a crop of the two boxes' union and looked at, and every one is a
// single printed drawing that clustered in pieces — so a threshold anywhere in that
// range would be deciding a case that does not exist, and the counts say the same
// thing from the other side.
//
// The counts are NOT monotonic in the threshold and that is expected rather than a
// fault: merging happens before the guards, so two candidates that were each under
// [minFigureInk] can merge into one that passes, and a document can gain a figure by
// merging. The sequential manual does, at 0.75.
func TestMergeThresholdSweep(t *testing.T) {
	for _, name := range []string{"thomas-drybox-amfibia", "dreame-l40-ultra"} {
		t.Run(name, func(t *testing.T) {
			pages, ink := loadFigureInk(t, name)
			count := func(v float64) int {
				g := defaultGuards
				g.mergeOverlap = v
				var n int
				for i := range pages {
					n += len(findFigures(ink[i], &pages[i], g))
				}
				return n
			}
			off := count(1)
			base := count(figureMergeOverlap)
			t.Logf("%s: %d figures with the merge off, %d at the default", name, off, base)
			for _, v := range []float64{0.999, 0.9, 0.75, 0.5, 0.25, 0.1, 0.05, 0.01, 0} {
				t.Logf("  mergeOverlap=%-5.3g -> %d figures", v, count(v))
			}

			if name == "thomas-drybox-amfibia" {
				for _, v := range []float64{0, 0.25, 0.5, 0.999} {
					if got := count(v); got != off {
						t.Errorf("at %.3f this document gives %d figures against %d with "+
							"the merge off; it has no overlapping candidates and must not move",
							v, got, off)
					}
				}
			}
			lo, hi := base, base
			for _, v := range []float64{0.01, 0.05, 0.1} {
				got := count(v)
				lo, hi = min(lo, got), max(hi, got)
			}
			if hi-lo > 2 {
				t.Errorf("between 0 and 0.1 the count ranges over %d..%d; the default "+
					"is on a cliff, and it was chosen because there is no case in that "+
					"range for a threshold to decide", lo, hi)
			}
		})
	}
}

func withInk(g figureGuards, v int) figureGuards      { g.minInk = v; return g }
func withSize(g figureGuards, v float64) figureGuards { g.minWidth, g.minHeight = v, v; return g }

// TestTrimOnlyPullsOffALineItReachedOver drives [trimToPicture] with the four
// arrangements measured on the columns manual, at their real coordinates, so the
// rule is pinned without a PDF.
//
// The four are the whole argument for the rule and each one is a page:
//
//	page 16 fig 2  »click« printed inside the panel, artwork on all four sides
//	page 24 fig 0  the same label at the drawing's RIGHT EDGE, no artwork past it
//	page 52 fig 0  a line of body text above the diagram, reaching in from outside
//	page 1  fig 0  the cover title block, reaching in from outside AND above
//
// Page 24 is why the rule is containment and not ink on more than one side: that
// »click« has ink only to its left and below, exactly like page 1's title, and the
// two must come out opposite ways. What separates them is that one is inside the box
// and the other is not.
func TestTrimOnlyPullsOffALineItReachedOver(t *testing.T) {
	for _, tc := range []struct {
		name string
		area CellRect
		text []TextRun
		want CellRect
	}{{
		// The label sits at 209-238 within a panel running to 288. It used to cost
		// the drawing everything past x=209.
		name: "a label the panel encloses is left alone",
		area: CellRect{42.8, 466.5, 288.4, 643.4},
		text: []TextRun{{X: 209, Y: 530, Width: 29, Height: 17, Text: "»click«"}},
		want: CellRect{42.8, 466.5, 288.4, 643.4},
	}, {
		// The same label flush against the drawing's right edge, inside it by half a
		// unit. Ink cannot tell this from prose; containment can.
		name: "a label at the very edge is still inside",
		area: CellRect{42.8, 197.0, 288.4, 373.0},
		text: []TextRun{{X: 262, Y: 304, Width: 25, Height: 14, Text: "»click«"}},
		want: CellRect{42.8, 197.0, 288.4, 373.0},
	}, {
		// One line of German body text ending just inside the diagram's top edge.
		// The top comes down off it and nothing else moves.
		name: "a line of prose reaching in from above is trimmed off",
		area: CellRect{323.1, 379.3, 582.5, 567.5},
		text: []TextRun{
			{X: 323, Y: 363, Width: 31, Height: 17, Text: "erzielen:"},
			// The diagram's own labels, which used to go with it.
			{X: 357, Y: 471, Width: 37, Height: 13, Text: "Absaugen und"},
			{X: 390, Y: 554, Width: 60, Height: 13, Text: "Lösen und Auswaschen"},
		},
		want: CellRect{323.1, 380.0, 582.5, 567.5},
	}, {
		// The cover: the whole title block, five lines stepping down and to the
		// right out of the art. The cheaper edge is taken each round, which is why
		// this needs all five rather than a sample — the order they come off in is
		// the behaviour.
		name: "the cover title block is trimmed off two edges",
		area: CellRect{37.7, 324.0, 663.0, 819.2},
		text: []TextRun{
			{X: 55, Y: 301, Width: 387, Height: 24,
				Text: "29924_Saugerbeschriftungen_DryBoxAmfibia.ind"},
			{X: 534, Y: 321, Width: 163, Height: 34, Text: "GEBRAUCHSANLEITUNG"},
			{X: 568, Y: 354, Width: 152, Height: 34, Text: "INSTRUKCJA OBSŁUGI"},
			{X: 602, Y: 386, Width: 248, Height: 34, Text: "РУКОВОДСТВО ПО ЭКСПЛУАТАЦИИ"},
			{X: 636, Y: 418, Width: 201, Height: 34, Text: "ІНСТРУКЦІЯ З ЕКСПЛУАТАЦІЇ"},
		},
		want: CellRect{37.7, 355.0, 568.0, 819.2},
	}, {
		// The floor under the rule: a run of three runes is never trimmed for, even
		// when it does reach over the edge. Page 11's diagram numbers its parts 1 to
		// 39 and several sit against the frame.
		name: "a short run is not trimmed for even when it reaches over",
		area: CellRect{100, 100, 300, 300},
		text: []TextRun{{X: 60, Y: 150, Width: 50, Height: 14, Text: "12"}},
		want: CellRect{100, 100, 300, 300},
	}, {
		// And the cap above it, which the two rules enforce together: the line reaches
		// over the LEFT edge only, so the left edge is the only one that may move, and
		// moving it past a third of the side is refused. The candidate is left whole
		// for the text guard to reject rather than whittled into a plausible picture.
		// Before the reach rule this trimmed the top instead — an edge the line never
		// crossed — and that is what "whittled" meant.
		name: "no edge moves by more than a third of its side",
		area: CellRect{100, 100, 300, 300},
		text: []TextRun{{X: 0, Y: 150, Width: 200, Height: 14, Text: "a whole line of prose"}},
		want: CellRect{100, 100, 300, 300},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got := trimToPicture(tc.area, tc.text)
			if got != tc.want {
				t.Errorf("trimToPicture(%v) = %v, expected %v", tc.area, got, tc.want)
			}
		})
	}
}

// TestPNGSizeReadsTheHeader covers the one piece of byte-level parsing here
// without a PDF, including the two malformed cases that would otherwise be stored
// as a figure: an empty stdout with a zero exit status, and output that is not a
// PNG at all.
func TestPNGSizeReadsTheHeader(t *testing.T) {
	// A 3x2 PNG, IHDR and all, written out by hand: signature, then the IHDR
	// chunk's length, type, width, height and the rest.
	good := []byte("\x89PNG\r\n\x1a\n" +
		"\x00\x00\x00\rIHDR" +
		"\x00\x00\x00\x03\x00\x00\x00\x02\x08\x06\x00\x00\x00")
	w, h, err := pngSize(good)
	if err != nil {
		t.Fatalf("pngSize: %v", err)
	}
	if w != 3 || h != 2 {
		t.Errorf("pngSize = %dx%d, expected 3x2", w, h)
	}

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"truncated", good[:12]},
		{"not a png", []byte("%PDF-1.4\nnot an image at all really")},
		{"png but no IHDR first", append([]byte("\x89PNG\r\n\x1a\n"),
			[]byte("\x00\x00\x00\rpHYs\x00\x00\x00\x03\x00\x00\x00\x02\x08")...)},
	} {
		if _, _, err := pngSize(tc.data); err == nil {
			t.Errorf("%s: pngSize accepted it", tc.name)
		}
	}
}

// TestOnPageInkDropsWhatCannotBePartOfAPicture pins the two structural filters
// with the real coordinates that motivated them.
func TestOnPageInkDropsWhatCannotBePartOfAPicture(t *testing.T) {
	const w, h = 892, 850
	ink := []Ink{
		// Cairo's compositing rect, from page 57 of the columns manual.
		{Rect: CellRect{-196.4, -187.1, 1089.4, 1037.5}},
		// One of the columns manual's full-width section bands.
		{Rect: CellRect{0, 311.5, 891.8, 508.3}},
		// A real drawing, from page 42.
		{Rect: CellRect{42.8, 45.5, 288.4, 241.4}},
		// A degenerate shape: a moveto with no extent.
		{Rect: CellRect{100, 100, 100, 100}},
	}
	kept := onPageInk(ink, w, h)
	if len(kept) != 1 {
		t.Fatalf("kept %d of 4 shapes, expected only the drawing", len(kept))
	}
	if kept[0].Rect.X0 != 42.8 {
		t.Errorf("kept %v, expected the drawing at x=42.8", kept[0].Rect)
	}
}

// TestFindFiguresAppliesBothGuards drives the geometry with hand-built ink, so the
// guards are checked without poppler and without a PDF. The shapes are the measured
// ones: a language badge is three shapes, a picture is many.
func TestFindFiguresAppliesBothGuards(t *testing.T) {
	page := &PageRuns{No: 7, Width: 892, Height: 850}

	// A picture: 40 small strokes chained into a 200x200 area at (100,100).
	var ink []Ink
	for i := range 40 {
		x := 100 + float64(i)*5
		ink = append(ink, Ink{Rect: CellRect{x, 100, x + 6, 300}, Stroked: true})
	}
	// A badge: three shapes in a 67x60 area, far from the picture.
	for range 3 {
		ink = append(ink, Ink{Rect: CellRect{600, 20, 667, 80}})
	}
	// A thin rule: long, but under the height floor.
	ink = append(ink, Ink{Rect: CellRect{100, 700, 500, 702}})

	figs := FindFigures(ink, page)
	if len(figs) != 1 {
		t.Fatalf("found %d figures, expected 1", len(figs))
		return
	}
	got := figs[0]
	if got.Page != 7 || got.Index != 0 {
		t.Errorf("figure is page %d index %d, expected page 7 index 0", got.Page, got.Index)
	}
	if got.Ink != 40 {
		t.Errorf("ink = %d, expected the 40 strokes", got.Ink)
	}
	if got.Rect != (CellRect{100, 100, 301, 300}) {
		t.Errorf("rect = %v, expected the strokes' bounding box", got.Rect)
	}

	// Now fill the same area with text. It must stop being a picture.
	page.Runs = []TextRun{
		{X: 105, Y: 105, Width: 190, Height: 190, Text: "a whole paragraph's worth"},
	}
	if figs := FindFigures(ink, page); len(figs) != 0 {
		t.Errorf("an area %.0f%% covered by text came back as a figure",
			100*figs[0].TextFraction)
	}
}

// TestFiguresAreInReadingOrder pins the order, because a figure's whole value to
// the reader is landing in the right place.
func TestFiguresAreInReadingOrder(t *testing.T) {
	page := &PageRuns{No: 1, Width: 892, Height: 850}
	// Three pictures: two side by side at the top, one below. Built bottom-right
	// first so the sort has something to do.
	corners := [][2]float64{{500, 500}, {500, 100}, {100, 100}}
	var ink []Ink
	for _, c := range corners {
		for i := range 25 {
			x := c[0] + float64(i)*4
			ink = append(ink, Ink{Rect: CellRect{x, c[1], x + 5, c[1] + 120}})
		}
	}
	figs := FindFigures(ink, page)
	if len(figs) != 3 {
		t.Fatalf("found %d figures, expected 3", len(figs))
	}
	want := [][2]float64{{100, 100}, {500, 100}, {500, 500}}
	for i := range figs {
		if figs[i].Rect.X0 != want[i][0] || figs[i].Rect.Y0 != want[i][1] {
			t.Errorf("figure %d starts at (%.0f,%.0f), expected (%.0f,%.0f)",
				i, figs[i].Rect.X0, figs[i].Rect.Y0, want[i][0], want[i][1])
		}
		if figs[i].Index != i {
			t.Errorf("figure at position %d carries index %d", i, figs[i].Index)
		}
	}
}

// TestAFragmentDrawnInsideADrawingIsNotItsOwnPicture is the fault the user
// reported, at the coordinates it was reported at.
//
// Page 524 of the sequential manual draws a hand holding a pin over the robot's
// underside. The hand's strokes touch none of the robot's, so the shape-level pass
// clusters it alone, and it was served as a picture of its own: a duplicate scrap
// of the drawing it came out of. The two boxes are the measured ones, x=279-412
// y=134-256 for the robot and x=375-416 y=146-184 for the hand — which is 90.8% of
// the hand inside the robot and NOT containment. The pin pokes 4 units past the
// robot's right edge, which is why a containment test alone would not have fixed
// the case it was reported on.
func TestAFragmentDrawnInsideADrawingIsNotItsOwnPicture(t *testing.T) {
	page := &PageRuns{No: 524, Width: 918, Height: 631}

	// The robot, drawn as chains of overlapping strokes along the top, the bottom
	// and the left of x=279-412 y=134-256. Deliberately open on the right between
	// y=146 and y=184, so no shape of the robot is anywhere near the hand.
	ink := chainX(279, 412, 134, 140, 7)
	ink = append(ink, chainX(279, 412, 250, 256, 7)...)
	ink = append(ink, chainY(134, 256, 279, 285, 7)...)
	// The hand: a chain of its own, dense enough to clear the ink guard by itself —
	// which is what made it a picture — reaching 4 units past the robot's right edge
	// and touching nothing the robot drew.
	hand := chainY(146, 184, 375, 416, 1.5)
	if len(hand) < minFigureInk {
		t.Fatalf("the hand is %d shapes; it has to pass the ink guard alone", len(hand))
	}
	ink = append(ink, hand...)

	figs := FindFigures(ink, page)
	if len(figs) != 1 {
		t.Fatalf("found %d figures, expected the drawing and its hand to be one", len(figs))
	}
	// The merged box is the union, so the parent keeps everything it had and gains
	// only what the fragment reached past it.
	if got := figs[0].Rect; got != (CellRect{279, 134, 416, 256}) {
		t.Errorf("rect = %v, expected the union x=279-416 y=134-256", got)
	}
	if figs[0].Ink != len(ink) {
		t.Errorf("ink = %d, expected all %d shapes counted inside the merged box",
			figs[0].Ink, len(ink))
	}
}

// TestBoxesThatOnlyTouchAreNotMerged is the other side of the rule, and it is what
// keeps two drawings printed side by side apart.
//
// Exactly touching is not overlapping: the shape-level pass has already joined
// everything whose boxes meet, so a second pass that merged on contact would only
// undo its own answer. The gaps that carry two real drawings apart on these
// documents are much wider than this — page 524's two halves are 23 units apart and
// page 522's two mop pads 46 — so this pins the boundary at its tightest.
func TestBoxesThatOnlyTouchAreNotMerged(t *testing.T) {
	for _, tc := range []struct {
		name  string
		boxes []CellRect
		want  int
	}{
		{"sharing a vertical edge",
			[]CellRect{{100, 100, 200, 200}, {200, 100, 300, 200}}, 2},
		{"sharing a horizontal edge",
			[]CellRect{{100, 100, 200, 200}, {100, 200, 200, 300}}, 2},
		{"meeting at a corner",
			[]CellRect{{100, 100, 200, 200}, {200, 200, 300, 300}}, 2},
		{"a unit apart",
			[]CellRect{{100, 100, 200, 200}, {201, 100, 300, 200}}, 2},
		// Overlapping on one axis only is not overlapping: two drawings printed
		// side by side share a horizontal band and are still two drawings.
		{"overlapping on one axis only",
			[]CellRect{{100, 100, 200, 200}, {201, 150, 300, 250}}, 2},
		{"overlapping by one unit on both axes",
			[]CellRect{{100, 100, 200, 200}, {199, 199, 300, 300}}, 1},
	} {
		got := mergeOverlapping(append([]CellRect(nil), tc.boxes...), figureMergeOverlap)
		if len(got) != tc.want {
			t.Errorf("%s: %d box(es), expected %d — %v", tc.name, len(got), tc.want, got)
		}
	}
}

// TestMergingRunsToAFixpoint covers the case one pass cannot: a merged box is
// bigger than either of its parts and can reach a third that neither part reached.
func TestMergingRunsToAFixpoint(t *testing.T) {
	// Three boxes on a diagonal, each overlapping only the next. Built out of
	// order, because the merge must not depend on the order they arrive in.
	boxes := []CellRect{{280, 280, 380, 380}, {100, 100, 200, 200}, {190, 190, 290, 290}}
	got := mergeOverlapping(boxes, figureMergeOverlap)
	if len(got) != 1 {
		t.Fatalf("%d boxes, expected the chain to collapse to one: %v", len(got), got)
	}
	if got[0] != (CellRect{100, 100, 380, 380}) {
		t.Errorf("box = %v, expected the whole chain x=100-380 y=100-380", got[0])
	}
}

// chainX lays overlapping strokes along a horizontal line from lo to hi, ending
// exactly on hi, so that they cluster into one shape group. A picture's strokes
// meet, which is what [clusterInk] turns on, and a hand-built test that forgets
// that measures nothing.
func chainX(lo, hi, y0, y1, step float64) []Ink {
	var ink []Ink
	for x := lo; x < hi; x += step {
		end := x + step + 1
		if end > hi {
			end = hi
		}
		ink = append(ink, Ink{Rect: CellRect{x, y0, end, y1}, Stroked: true})
	}
	return ink
}

// chainY is [chainX] down the page.
func chainY(lo, hi, x0, x1, step float64) []Ink {
	var ink []Ink
	for y := lo; y < hi; y += step {
		end := y + step + 1
		if end > hi {
			end = hi
		}
		ink = append(ink, Ink{Rect: CellRect{x0, y, x1, end}, Stroked: true})
	}
	return ink
}

// TestBoxOverlapOnADegenerateAxis pins the case an area ratio cannot answer: a
// single hairline clusters alone and its box has zero height.
func TestBoxOverlapOnADegenerateAxis(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b CellRect
		want float64
	}{
		{"a flat rule inside a box", CellRect{10, 50, 90, 50}, CellRect{0, 0, 100, 100}, 1},
		{"a flat rule half inside", CellRect{50, 50, 150, 50}, CellRect{0, 0, 100, 100}, 0.5},
		{"a flat rule above the box", CellRect{10, 150, 90, 150}, CellRect{0, 0, 100, 100}, 0},
		{"touching along an edge", CellRect{100, 0, 200, 100}, CellRect{0, 0, 100, 100}, 0},
		{"one box inside the other", CellRect{10, 10, 20, 20}, CellRect{0, 0, 100, 100}, 1},
	} {
		if got := boxOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: overlap = %.3f, expected %.3f", tc.name, got, tc.want)
		}
		if got := boxOverlap(tc.b, tc.a); got != tc.want {
			t.Errorf("%s, the other way round: overlap = %.3f, expected %.3f",
				tc.name, got, tc.want)
		}
	}
}
