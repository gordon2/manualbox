package doc

import (
	"context"
	"fmt"
	"math"
	"os"
	"slices"
	"sort"
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
// range rather than on a cliff: 0, 0.01, 0.05 and 0.1 give 195, 194, 197 and 196
// figures, against 213 at 0.5 and 229 at containment. That is the claim the value
// rests on. The 53 overlapping pairs on that
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
			// A twentieth, the same shape of bound TestGuardSweep puts on the ink
			// guard. Measured spread on this document is 194..197 against a default
			// of 195, which is under 2%.
			if hi-lo > base/20 {
				t.Errorf("between 0 and 0.1 the count ranges over %d..%d against a "+
					"default of %d; the default is on a cliff, and it was chosen "+
					"because there is no case in that range for a threshold to decide",
					lo, hi, base)
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

// TestFindFiguresIsReproducible is here because it was not, and it is asserted at a
// threshold the shipped one does not use, on purpose.
//
// [clusterInk] collects its groups in a map, so they come out in a random order. At
// [figureMergeOverlap]'s zero that cannot matter — merging only grows a box, so it
// never destroys an intersection, and the answer is the connected components of the
// overlap relation whatever order they are visited in. Above zero it matters a
// great deal, because a merged box is wider and the smaller box's share of it falls:
// TestMergeThresholdSweep, with the boxes left in map order, reported 195, 197 and
// 196 figures at 0.01, 0.05 and 0.1 and then 194 to 200 for the same three
// thresholds later in the same run.
//
// So this drives the merge at 0.5, where the order decides the outcome, and pins
// that twenty runs agree. Reproducibility is not a nicety here: these bytes go into
// a content-addressed store, and a page that clusters differently on a re-run stores
// the same picture twice.
func TestFindFiguresIsReproducible(t *testing.T) {
	page := &PageRuns{No: 1, Width: 892, Height: 850}
	g := defaultGuards
	g.mergeOverlap = 0.5

	// A row of overlapping corners of different sizes, which is the arrangement
	// where a merge can drop a later pair below the threshold.
	var ink []Ink
	for i := range 8 {
		x := 100 + float64(i)*55
		y := 100 + float64(i)*9
		w := 60 + float64(i)*12
		ink = append(ink, chainX(x, x+w, y, y+5, 5)...)
		ink = append(ink, chainY(y, y+w, x, x+5, 5)...)
	}

	first := findFigures(ink, page, g)
	if len(first) < 2 {
		t.Fatalf("the arrangement collapsed to %d figure(s); it has to leave several "+
			"for the order to decide between", len(first))
	}
	for range 20 {
		got := findFigures(ink, page, g)
		if len(got) != len(first) {
			t.Fatalf("%d figures on one run and %d on another", len(first), len(got))
		}
		for i := range got {
			if got[i].Rect != first[i].Rect {
				t.Fatalf("figure %d is %v on one run and %v on another",
					i, first[i].Rect, got[i].Rect)
			}
		}
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

// The two fixtures, by what they are rather than by their filenames: which of them
// a number belongs to is the whole point of every assertion below.
const (
	columnsManual    = "thomas-drybox-amfibia"
	sequentialManual = "dreame-l40-ultra"
)

// frontMatterPlates are the sequential manual's two diagram plates. They fall
// outside every language region, so no conversion ever serves them, and they are
// where every cost this pass has lands.
var frontMatterPlates = []int{5, 6}

// TestGrowSweep prints what each of the growth rule's four numbers does over both
// whole documents, one at a time with the rest at their defaults, in the same shape
// TestGuardSweep and TestMergeThresholdSweep use. It is the measurement
// [labelTerminator], [labelAlign], [labelCorridor] and [maxLabelGrowth] are set
// from.
//
// What it asserts is only what is measured and stable.
//
// **The columns manual does not move at any setting.** 0 of its 59 figures grow, for
// every value of every one of the four. Both of its claims are blocked by prose in
// the corridor, so this pass is the other document's entirely and the manual whose
// pictures were counted by eye cannot lose one to it. That is the same shape of
// evidence [mergeOverlapping] rests on and it is the strongest safety property this
// change has.
//
// **Growth never changes the figure COUNT**, on either document: 59 and 195 with the
// pass on and off. It moves edges, and it runs after both guards, so a page cannot
// gain or lose a picture to it.
//
// **The shipped values give 55 grown figures and 229 labels taken in** on the
// sequential manual, 22 of the 55 on the plate pages.
//
// **The cap is a smooth continuum with no cliff**, so its shape is asserted rather
// than a gap: 18/51, 32/107, 55/229, 63/255 and 64/262 figures/labels at 0.25, 0.5,
// 1, 2 and no cap at all.
//
// **The overlapping crops are confined to the plates**: 11 pairs on pages 5 and 6,
// 0 on every other page of either document. Measured both ways — counting every
// overlapping pair of grown boxes and counting only the pairs whose drawings do not
// themselves overlap gives 11 either way, because no two of these figures' drawings
// overlap at all.
func TestGrowSweep(t *testing.T) {
	for _, name := range []string{columnsManual, sequentialManual} {
		t.Run(name, func(t *testing.T) {
			pages, ink := loadFigureInk(t, name)
			show := func(label string, g figureGuards) growStats {
				s := growSweepStats(pages, ink, g)
				t.Logf("  %-16s -> %3d figures, %2d grown, %3d labels, "+
					"overlapping pairs on %v",
					label, s.figures, s.grown, s.labels, growPages(s.pairsOn))
				return s
			}
			base := show("the defaults", defaultGuards)
			t.Logf("  grown per page: %v", base.grownOn)
			t.Logf("  overlapping pairs per page: %v", base.pairsOn)

			var swept []growStats
			for _, s := range []struct {
				name string
				vals []float64
				set  func(*figureGuards, float64)
			}{
				{"terminator", []float64{4, 6, 8, 12},
					func(g *figureGuards, v float64) { g.terminator = v }},
				{"align", []float64{2, 4, 6, 8},
					func(g *figureGuards, v float64) { g.align = v }},
				{"corridor", []float64{20, 40, 60, 80},
					func(g *figureGuards, v float64) { g.corridor = v }},
				{"growth", []float64{0, 0.25, 0.5, 1, 2, math.Inf(1)},
					func(g *figureGuards, v float64) { g.growth = v }},
			} {
				for _, v := range s.vals {
					g := defaultGuards
					s.set(&g, v)
					swept = append(swept, show(fmt.Sprintf("%s=%g", s.name, v), g))
				}
			}

			// The figure count is growth's invariant, at every setting of every one
			// of the four: the pass runs after both guards and only moves edges.
			for i := range swept {
				if swept[i].figures != base.figures {
					t.Errorf("a swept value gives %d figures against %d at the "+
						"defaults; growth may move an edge and never decide a picture",
						swept[i].figures, base.figures)
				}
			}

			if name == columnsManual {
				// The safety property. Not "few" and not "no regression": none, at
				// every value of every threshold.
				for i := range swept {
					if swept[i].grown != 0 || swept[i].labels != 0 {
						t.Errorf("this document grew %d figures and took %d labels at "+
							"some swept value; both of its claims are blocked by prose "+
							"in the corridor and it must not move at any setting",
							swept[i].grown, swept[i].labels)
						break
					}
				}
				if base.figures != 59 {
					t.Errorf("%d figures, expected 59", base.figures)
				}
				return
			}

			if base.figures != 195 || base.grown != 55 || base.labels != 229 {
				t.Errorf("%d figures, %d grown, %d labels; expected 195, 55 and 229",
					base.figures, base.grown, base.labels)
			}
			if plates := growTotal(base.grownOn) - growTotal(base.grownOn, frontMatterPlates...); plates != 22 {
				t.Errorf("%d grown figures on pages %v, expected 22 — the front-matter "+
					"plates carry most of what this pass does and none of what a "+
					"reader is served", plates, frontMatterPlates)
			}

			// The cap, as a continuum rather than a gap. Each step gains figures and
			// labels over the one below it, and no step is a cliff.
			for _, tc := range []struct {
				growth        float64
				grown, labels int
			}{
				{0.25, 18, 51}, {0.5, 32, 107}, {1, 55, 229}, {2, 63, 255},
				{math.Inf(1), 64, 262},
			} {
				g := defaultGuards
				g.growth = tc.growth
				s := growSweepStats(pages, ink, g)
				if s.grown != tc.grown || s.labels != tc.labels {
					t.Errorf("growth=%g gives %d grown and %d labels, expected %d and %d",
						tc.growth, s.grown, s.labels, tc.grown, tc.labels)
				}
			}

			// The cost, and the reason it is recorded rather than fixed: it is not on
			// a page any conversion serves. Page 5 is 31 figures on one sheet with
			// labels between them.
			if off := growTotal(base.pairsOn, frontMatterPlates...); off != 0 {
				t.Errorf("%d overlapping pairs of grown boxes off pages %v (%v); every "+
					"one of them used to be on a plate, and a reader is served none of "+
					"those pages", off, frontMatterPlates, base.pairsOn)
			}
			if all := growTotal(base.pairsOn); all != 11 {
				t.Errorf("%d overlapping pairs in total, expected 11 on the plates", all)
			}
		})
	}
}

// TestALabelOutsideTheFinalCropIsTheResidual counts what is left: figures on a page
// a conversion serves that still have a label a leader points at, sitting outside
// the final crop.
//
// It is a floor rather than a bug, and every one of them is [growToLabels]'s
// conservative half doing its job. Page 521 figure 0 is the case to read: its right
// corridor holds a label and then five lines of that label's own bullet description,
// so the edge correctly refuses to move rather than pull a paragraph into a picture.
//
// The number can only go down. Nothing in this pass can raise it — growth only moves
// edges outwards, so a label inside the crop stays inside it — which is why this is
// asserted as an exact count with a direction rather than a bound: a change that
// takes more labels in must edit the number down, and one that takes fewer has
// regressed.
//
// Converted pages are every page except the two front-matter plates, which are the
// only pages either document has that fall outside every language region. That is a
// proxy for the region computation, which lives in another package.
func TestALabelOutsideTheFinalCropIsTheResidual(t *testing.T) {
	for _, tc := range []struct {
		name string
		// figures with a clipped label, and the labels themselves, off the plates.
		figures, labels int
	}{
		// Both of this document's two claims are refused, so both of its figures
		// with a label outside them are residual. Pages 1 and 22.
		{columnsManual, 2, 2},
		// 41 of this document's figures on a converted page still hold a clipped
		// label, over 15 pages; 88 labels in all. The plates hold 16 more figures
		// and 29 more labels, which no reader is served.
		{sequentialManual, 41, 88},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pages, ink := loadFigureInk(t, tc.name)
			s := growSweepStats(pages, ink, defaultGuards)
			figures := growTotal(s.residualOn, frontMatterPlates...)
			labels := growTotal(s.residualLabels, frontMatterPlates...)
			t.Logf("%s: %d figures still hold a clipped label, %d labels in all, "+
				"on pages %v", tc.name, figures, labels, growPages(s.residualOn))
			t.Logf("  labels per page: %v", s.residualLabels)
			if figures != tc.figures || labels != tc.labels {
				t.Errorf("%d figures and %d labels, expected %d and %d; this number "+
					"can only go down, so a change that improves the crop must edit it",
					figures, labels, tc.figures, tc.labels)
			}
			if tc.name != sequentialManual {
				return
			}
			// The case the comment above names, checked rather than described: page
			// 521 figure 0's right corridor holds a label and then five lines of its
			// own bullet description, so its right edge correctly refuses to move.
			for i := range pages {
				if pages[i].No != 521 {
					continue
				}
				var dropped DroppedRuns
				text := usableRuns(pages[i].Runs, pages[i].Width, pages[i].Height, &dropped)
				marks := marksOf(onPageInk(ink[i], pages[i].Width, pages[i].Height))
				figs := findFigures(ink[i], &pages[i], defaultGuards)
				if len(figs) == 0 {
					t.Fatalf("page 521 has no figures; it is the page this pass was " +
						"written for and it prints three drawings")
				}
				if n := clippedLabels(&figs[0], text, marks, defaultGuards); n == 0 {
					t.Errorf("page 521 figure 0 holds every label it is pointed at; " +
						"the residual it is the example of has moved")
				}
			}
		})
	}
}

// growStats is what one setting of the growth rule does to a whole document.
type growStats struct {
	figures int
	grown   int
	// labels is how many runs the crops took in: a run that intersects a figure's
	// grown box and does not touch the drawing itself. A label the edge cut short
	// counts, because it WAS taken in — see [growToLabels] on why a claimed label may
	// be cut and prose may not.
	labels int
	// grownOn is how many grown figures each page carries.
	grownOn map[int]int
	// pairsOn is how many pairs of grown boxes overlap on each page. Counted over
	// pairs whose drawings do not themselves overlap, so the number is growth's own
	// doing; on these two documents no two drawings overlap, so it is also simply
	// every overlapping pair.
	pairsOn map[int]int
	// residualOn and residualLabels are the figures each page still has with a label
	// outside the final crop, and how many such labels.
	residualOn     map[int]int
	residualLabels map[int]int
}

// growSweepStats runs one setting over every page of a document.
func growSweepStats(pages []PageRuns, ink [][]Ink, g figureGuards) growStats {
	s := growStats{grownOn: map[int]int{}, pairsOn: map[int]int{},
		residualOn: map[int]int{}, residualLabels: map[int]int{}}
	for i := range pages {
		p := &pages[i]
		var dropped DroppedRuns
		text := usableRuns(p.Runs, p.Width, p.Height, &dropped)
		marks := marksOf(onPageInk(ink[i], p.Width, p.Height))
		figs := findFigures(ink[i], p, g)
		s.figures += len(figs)
		for j := range figs {
			f := &figs[j]
			if n := clippedLabels(f, text, marks, g); n > 0 {
				s.residualOn[p.No]++
				s.residualLabels[p.No] += n
			}
			if f.Rect == f.InkRect {
				continue
			}
			s.grown++
			s.grownOn[p.No]++
			for k := range text {
				box := runBox(&text[k])
				if boxOverlap(box, f.InkRect) == 0 && boxOverlap(box, f.Rect) > 0 {
					s.labels++
				}
			}
		}
		for a := range figs {
			for b := a + 1; b < len(figs); b++ {
				if boxOverlap(figs[a].Rect, figs[b].Rect) > 0 &&
					boxOverlap(figs[a].InkRect, figs[b].InkRect) == 0 {
					s.pairsOn[p.No]++
				}
			}
		}
	}
	return s
}

// clippedLabels is how many of a figure's labels a leader points at and the final
// crop still does not hold whole. Terminator claims only: a continuation line
// without its first line is not a label this can report on.
func clippedLabels(f *Figure, text []TextRun, marks []CellRect, g figureGuards) int {
	var n int
	for side := range 4 {
		for i := range text {
			r := &text[i]
			gap, outside := runBeyond(f.InkRect, r, side)
			if !outside || gap > g.corridor {
				continue
			}
			if terminatorAt(marks, f.InkRect, r, side, g) && boxOverlap(runBox(r), f.Rect) < 1 {
				n++
			}
		}
	}
	return n
}

// growPages is the pages a map of per-page counts covers, in order.
func growPages(m map[int]int) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// growTotal sums a map of per-page counts, optionally excluding some pages.
func growTotal(m map[int]int, except ...int) int {
	var n int
	for k, v := range m {
		if !slices.Contains(except, k) {
			n += v
		}
	}
	return n
}

// runBox is a run as a rectangle, so the two can be compared with [boxOverlap].
func runBox(r *TextRun) CellRect {
	return CellRect{r.X, r.Y, r.right(), r.bottom()}
}

// growthDrawing lays a drawing's border as chained strokes so the cluster's
// bounding box is exactly r, with no stroke small enough to be read as a leader's
// terminator: every rect is 10 units thick, and a mark is admitted only when BOTH
// sides are [labelTerminator] or under. That matters more than it looks. Built with
// 6-unit strokes the border's own 8x6 pieces are terminator candidates, and one of
// them lands on any label's midline — which would make TestAPartsListIsRefused pass
// for the wrong reason.
func growthDrawing(r CellRect) []Ink {
	ink := chainX(r.X0, r.X1, r.Y0, r.Y0+10, 10)
	ink = append(ink, chainX(r.X0, r.X1, r.Y1-10, r.Y1, 10)...)
	ink = append(ink, chainY(r.Y0, r.Y1, r.X0, r.X0+10, 10)...)
	ink = append(ink, chainY(r.Y0, r.Y1, r.X1-10, r.X1, 10)...)
	return ink
}

// leaderMark is a leader's end mark at its measured size: the open circles on page
// 521 of the sequential manual are 3.3 to 3.4 units square.
func leaderMark(cx, cy float64) Ink {
	return Ink{Rect: CellRect{cx - 1.7, cy - 1.7, cx + 1.7, cy + 1.7}}
}

// runMidY is a run's midline, which is what a leader points at.
func runMidY(r TextRun) float64 { return r.Y + r.Height/2 }

// marksOf picks the terminator candidates out of a drawing the same way
// [growToLabels] does, for the tests that call [claimLabels] directly.
func marksOf(drawn []Ink) []CellRect {
	var marks []CellRect
	for i := range drawn {
		if r := drawn[i].Rect; r.Width() <= labelTerminator && r.Height() <= labelTerminator {
			marks = append(marks, r)
		}
	}
	return marks
}

// TestALabelALeaderPointsAtIsTakenIn drives the whole pass through FindFigures at
// page 521's measured geometry: the box's right edge is at 263.0, the terminator
// sits at 259.6-263.0 and SETS that edge, and the label starts at 266.0. Three
// units, and before this pass nothing in findFigures ever grew a box.
//
// It also pins the two rectangles apart. Rect is what will be rendered; InkRect is
// the drawn extent the guards judged, and attribution reads that one.
func TestALabelALeaderPointsAtIsTakenIn(t *testing.T) {
	const drawn = 263.0
	label := TextRun{X: 266, Y: 200, Width: 10, Height: 13, Text: "12"}
	page := &PageRuns{No: 521, Width: 918, Height: 631, Runs: []TextRun{label}}

	ink := growthDrawing(CellRect{100, 100, drawn, 300})
	ink = append(ink, leaderMark(261.3, runMidY(label)))

	figs := FindFigures(ink, page)
	if len(figs) != 1 {
		t.Fatalf("found %d figures, expected 1", len(figs))
	}
	if got := figs[0].Rect; got != (CellRect{100, 100, 276, 300}) {
		t.Errorf("Rect = %v, expected the right edge out at the label's far edge 276", got)
	}
	if got := figs[0].InkRect; got != (CellRect{100, 100, drawn, 300}) {
		t.Errorf("InkRect = %v, expected the drawing alone, right edge at %.1f", got, drawn)
	}
}

// TestWithoutATerminatorNothingMoves is the same geometry with the mark taken out,
// and it is the whole signal: a run three units from the edge is not a label unless
// something points at it.
func TestWithoutATerminatorNothingMoves(t *testing.T) {
	const drawn = 263.0
	label := TextRun{X: 266, Y: 200, Width: 10, Height: 13, Text: "12"}
	page := &PageRuns{No: 521, Width: 918, Height: 631, Runs: []TextRun{label}}

	figs := FindFigures(growthDrawing(CellRect{100, 100, drawn, 300}), page)
	if len(figs) != 1 {
		t.Fatalf("found %d figures, expected 1", len(figs))
	}
	if figs[0].Rect != figs[0].InkRect {
		t.Errorf("Rect = %v against InkRect = %v; with no mark the box must not move",
			figs[0].Rect, figs[0].InkRect)
	}
}

// TestAPartsListIsRefused is the case that rules a distance rule out, and it is a
// document rather than an argument.
//
// Page 11 of the columns manual prints its parts list — 39 numbers and 39 German
// names — in a column 22.3 units to the right of the exploded view, with no
// terminator anywhere: a legend is not pointed at. 22.3 sits INSIDE the range page
// 521's underside diagram holds its own labels at, 20.3 to 35.3 units out — not
// further away than them, which is the stronger fact: no "grow onto text within N
// units" rule can take one and refuse the other, at any N.
func TestAPartsListIsRefused(t *testing.T) {
	area := CellRect{100, 100, 300, 400}
	const listX = 322.3 // 22.3 units right of the drawing's edge
	text := []TextRun{
		{X: listX, Y: 110, Width: 6, Height: 13, Text: "1"},
		{X: listX + 12, Y: 110, Width: 80, Height: 13, Text: "Gehäusedeckel"},
		{X: listX, Y: 128, Width: 6, Height: 13, Text: "2"},
		{X: listX + 12, Y: 128, Width: 62, Height: 13, Text: "Tragegriff"},
		{X: listX, Y: 146, Width: 6, Height: 13, Text: "3"},
		{X: listX + 12, Y: 146, Width: 70, Height: 13, Text: "Saugschlauch"},
	}
	got := growToLabels(area, text, growthDrawing(area), defaultGuards)
	if got != area {
		t.Errorf("grew to %v; a parts list is not pointed at and nothing may move", got)
	}
}

// TestProseInTheCorridorStopsTheEdgeDead is the conservative half of the rule, and
// it is a decision rather than a detail: an edge moves only if everything the growth
// region touches is a claimed label.
//
// Page 521's lid-open drawing is the case. Its corridor holds "Кнопка сброса" and
// then the five bullet lines that explain it, so growing right would drag a
// paragraph into a picture; its left edge grows and its right does not.
func TestProseInTheCorridorStopsTheEdgeDead(t *testing.T) {
	area := CellRect{100, 100, 300, 300}
	label := TextRun{X: 303, Y: 190, Width: 12, Height: 13, Text: "12"}
	prose := TextRun{X: 305, Y: 140, Width: 60, Height: 13, Text: "a whole line of prose"}
	drawn := append(growthDrawing(area), leaderMark(296, runMidY(label)))

	got := growToLabels(area, []TextRun{label, prose}, drawn, defaultGuards)
	if got != area {
		t.Errorf("grew to %v; one line of prose in the corridor and the edge stays", got)
	}
}

// TestAWrappedLabelIsTakenWhole covers the second half of the signal: a label's
// later lines carry no terminator of their own, and left unclaimed they are
// obstacles to the label they belong to. Page 521's lidar drawing claims nine of its
// eleven labels by terminator, and its two continuation lines block the edge from
// moving at all.
//
// The counter-case is what stops the rule swallowing a bulleted description. What
// separates them is that a bullet has its text beside it on the same baseline and a
// continuation line does not.
func TestAWrappedLabelIsTakenWhole(t *testing.T) {
	area := CellRect{100, 100, 300, 300}
	first := TextRun{X: 303, Y: 190, Width: 40, Height: 13, Text: "Модуль"}
	second := TextRun{X: 303, Y: 201, Width: 30, Height: 13, Text: "на основе ИИ"}
	drawn := append(growthDrawing(area), leaderMark(296, runMidY(first)))

	got := growToLabels(area, []TextRun{first, second}, drawn, defaultGuards)
	if got != (CellRect{100, 100, 343, 300}) {
		t.Errorf("grew to %v, expected the right edge at the first line's far edge 343 "+
			"with the second line claimed rather than blocking it", got)
	}

	// The counter-case. The bullet is 1.5 units wide and its text starts 1 unit past
	// it on the same baseline, so the description IS flush with the label above it
	// and IS on the adjacent line — the only thing left to refuse it is that it is
	// not alone on its own baseline.
	bullet := TextRun{X: 303, Y: 201, Width: 1.5, Height: 13, Text: "•"}
	desc := TextRun{X: 305.5, Y: 201, Width: 60, Height: 13, Text: "Нажмите кнопку сброса"}
	text := []TextRun{first, bullet, desc}
	marks := []CellRect{leaderMark(296, runMidY(first)).Rect}
	claimed := claimLabels(area, text, marks, edgeRight, defaultGuards)
	if len(claimed) != 1 || claimed[0] != &text[0] {
		got := make([]string, 0, len(claimed))
		for _, c := range claimed {
			got = append(got, c.Text)
		}
		t.Errorf("claimed %q, expected only the label; a bullet's description is not "+
			"its continuation", got)
	}
	if grown := growToLabels(area, text, drawn, defaultGuards); grown != area {
		t.Errorf("grew to %v; the description is unclaimed and sits in the region", grown)
	}
}

// TestGrowthComparesBaselinesNotBands pins the reading a band comparison gets wrong.
// Two consecutive lines of one label overlap vertically, because a run is taller
// than the pitch it is set at — 13 units of height on a 10-unit pitch here — so a
// band test reports a label's own third line as something sharing the second's line,
// and that blocked every growth on the page this pass was written for.
func TestGrowthComparesBaselinesNotBands(t *testing.T) {
	area := CellRect{100, 100, 300, 300}
	text := []TextRun{
		{X: 303, Y: 190, Width: 40, Height: 13, Text: "Модуль"},
		{X: 303, Y: 200, Width: 36, Height: 13, Text: "на основе"},
		{X: 303, Y: 210, Width: 32, Height: 13, Text: "3D-датчики"},
	}
	// Each line's band overlaps the next by 3 units, and only the first is pointed at.
	for i := range text[:len(text)-1] {
		if text[i].bottom() <= text[i+1].Y {
			t.Fatalf("line %d ends at %.1f before line %d starts at %.1f; the point of "+
				"this test is that they overlap", i, text[i].bottom(), i+1, text[i+1].Y)
		}
	}
	drawn := append(growthDrawing(area), leaderMark(296, runMidY(text[0])))

	if got := growToLabels(area, text, drawn, defaultGuards); got != (CellRect{100, 100, 343, 300}) {
		t.Errorf("grew to %v, expected the right edge at 343; a three-line label whose "+
			"lines overlap must still grow", got)
	}
}

// TestTheCapIsAgainstTheDrawing covers [maxLabelGrowth] from both sides, and then
// the trap underneath it: the cap is measured against the drawing, so an edge's
// allowance does not grow because another edge moved first.
func TestTheCapIsAgainstTheDrawing(t *testing.T) {
	// 100 wide, so at maxLabelGrowth of 1 the right edge may move 100 units.
	area := CellRect{100, 100, 200, 300}
	grow := func(labelWidth float64) CellRect {
		label := TextRun{X: 203, Y: 190, Width: labelWidth, Height: 13, Text: "Bezeichnung"}
		drawn := append(growthDrawing(area), leaderMark(196, runMidY(label)))
		return growToLabels(area, []TextRun{label}, drawn, defaultGuards)
	}
	// Far edge at 299: the edge moves 99 of its allowed 100.
	if got := grow(96); got != (CellRect{100, 100, 299, 300}) {
		t.Errorf("just inside the cap: grew to %v, expected the edge at 299", got)
	}
	// Far edge at 301: 101 units, and the whole growth is refused rather than
	// clipped back to the cap. A label cut at an arbitrary line is not the point.
	if got := grow(98); got != area {
		t.Errorf("just outside the cap: grew to %v, expected no move at all", got)
	}

	// Two edges. The left label needs 80 units and is allowed; the right label needs
	// 150, which is over the drawing's own 100 and must stay refused even though the
	// box is 180 wide by the time the right edge is judged.
	left := TextRun{X: 20, Y: 190, Width: 60, Height: 13, Text: "links"}
	right := TextRun{X: 203, Y: 220, Width: 147, Height: 13, Text: "rechts"}
	drawn := append(growthDrawing(area),
		leaderMark(96, runMidY(left)), leaderMark(204, runMidY(right)))
	got := growToLabels(area, []TextRun{left, right}, drawn, defaultGuards)
	if got != (CellRect{20, 100, 200, 300}) {
		t.Errorf("grew to %v, expected the left edge out to 20 and the right edge "+
			"unmoved; the first edge's growth must not enlarge the second's allowance", got)
	}
}

// TestAClaimedLabelMayBeCutAndProseMayNot is the asymmetry that makes the pass work
// on a page whose two label columns interleave in x. Page 521's lidar drawing
// reaches 397 where its own longest label ends at 469, because the neighbouring
// drawing's labels start at 400. Refusing to cut a label at all was measured and
// costs the whole page.
func TestAClaimedLabelMayBeCutAndProseMayNot(t *testing.T) {
	area := CellRect{100, 100, 300, 300}
	short := TextRun{X: 303, Y: 190, Width: 27, Height: 13, Text: "Deckel"}
	long := TextRun{X: 303, Y: 220, Width: 97, Height: 13, Text: "Absaugen und Lösen"}
	prose := TextRun{X: 340, Y: 150, Width: 50, Height: 13, Text: "the next column's prose"}
	drawn := append(growthDrawing(area),
		leaderMark(296, runMidY(short)), leaderMark(296, runMidY(long)))

	got := growToLabels(area, []TextRun{short, long, prose}, drawn, defaultGuards)
	if got != (CellRect{100, 100, 330, 300}) {
		t.Errorf("grew to %v, expected the edge at the shorter label's 330 — cutting "+
			"the longer label rather than reaching over the prose at 340", got)
	}
}

// TestEachEdgeIsJudgedAgainstTheBoxAsAlreadyGrown is a real trap rather than a
// hypothetical: a prototype that computed all four edges from the original box
// admitted a run diagonally outside two of them on 2 figures.
//
// The run below is beyond neither edge on its own — it clears the left edge but does
// not reach the figure's vertical band, and clears the top edge but not its
// horizontal band — so it is never a candidate label. It only lands in the way once
// the left edge has moved, which is why the top edge must be judged against the
// grown box.
func TestEachEdgeIsJudgedAgainstTheBoxAsAlreadyGrown(t *testing.T) {
	area := CellRect{100, 100, 300, 300}
	left := TextRun{X: 60, Y: 190, Width: 35, Height: 13, Text: "links"}
	top := TextRun{X: 190, Y: 60, Width: 40, Height: 13, Text: "oben"}
	corner := TextRun{X: 65, Y: 70, Width: 25, Height: 13, Text: "diagonal"}
	for _, side := range []int{edgeLeft, edgeRight, edgeTop, edgeBottom} {
		if _, outside := runBeyond(area, &corner, side); outside {
			t.Fatalf("the corner run is beyond edge %d; it has to be beyond none of "+
				"them for this test to mean anything", side)
		}
	}
	// Each mark on its own label's midline: 96 is the left label's, and 210 is the
	// top label's — a leader points AT its label, and [labelAlign] is 4 units.
	drawn := append(growthDrawing(area),
		leaderMark(96, runMidY(left)), leaderMark(top.X+top.Width/2, 96))

	claimedTop := claimLabels(area, []TextRun{left, top, corner},
		marksOf(drawn), edgeTop, defaultGuards)
	if len(claimedTop) != 1 {
		t.Fatalf("the top edge claimed %d labels, expected the one; without a claim "+
			"there is nothing for the growth region to be judged against", len(claimedTop))
	}

	got := growToLabels(area, []TextRun{left, top, corner}, drawn, defaultGuards)
	if got != (CellRect{60, 100, 300, 300}) {
		t.Errorf("grew to %v, expected the left edge out to 60 and the top refused; "+
			"the corner run is only in the way once the left edge has moved", got)
	}
}

// TestTheGuardsJudgeTheDrawingNotTheCrop pins the order the pass runs in. A
// diagram's own labels are text, so a box grown onto them is legitimately over
// [maxFigureTextFraction] — page 521's lidar diagram reaches 0.162 with its eleven
// labels — and re-testing the grown box would reject the very pictures this pass
// exists to complete.
func TestTheGuardsJudgeTheDrawingNotTheCrop(t *testing.T) {
	drawn := CellRect{100, 100, 200, 200}
	var runs []TextRun
	ink := growthDrawing(drawn)
	for _, y := range []float64{105, 120, 135, 150, 165, 180} {
		r := TextRun{X: 203, Y: y, Width: 85, Height: 13, Text: "Bezeichnung"}
		runs = append(runs, r)
		ink = append(ink, leaderMark(196, runMidY(r)))
	}
	page := &PageRuns{No: 521, Width: 918, Height: 631, Runs: runs}

	figs := FindFigures(ink, page)
	if len(figs) != 1 {
		t.Fatalf("found %d figures, expected 1", len(figs))
	}
	got := figs[0]
	if got.Rect != (CellRect{100, 100, 288, 200}) {
		t.Fatalf("Rect = %v, expected the edge out at the labels' far edge 288", got.Rect)
	}
	if crop := textFraction(got.Rect, runs); crop <= maxFigureTextFraction {
		t.Fatalf("the grown box is %.3f text, under the %.2f guard; this test needs a "+
			"crop the guard would have rejected", crop, maxFigureTextFraction)
	}
	if got.InkRect != drawn {
		t.Errorf("InkRect = %v, expected the drawing %v", got.InkRect, drawn)
	}
	if want := textFraction(drawn, runs); got.TextFraction != want {
		t.Errorf("TextFraction = %.3f, expected %.3f — the drawing's, not the crop's",
			got.TextFraction, want)
	}
	// Every shape, the six terminators included: a leader's mark is what SETS the
	// edge it sits on, so it is inside the drawing's box, not out in the corridor.
	if got.Ink != len(ink) {
		t.Errorf("Ink = %d, expected all %d shapes of the drawing", got.Ink, len(ink))
	}
}

// TestDrawnExtentFallsBackToRect covers the two callers that legitimately have no
// ink box: a figure read back out of the database, where the drawn extent is not
// stored, and a figure built by hand in a test. Before this pass the two rects were
// one rect, which is why the fallback is right rather than an error.
func TestDrawnExtentFallsBackToRect(t *testing.T) {
	crop := CellRect{100, 100, 276, 300}
	drawn := CellRect{100, 100, 263, 300}
	stored := Figure{Rect: crop}
	if got := stored.DrawnExtent(); got != crop {
		t.Errorf("DrawnExtent = %v with no InkRect, expected Rect %v", got, crop)
	}
	fresh := Figure{Rect: crop, InkRect: drawn}
	if got := fresh.DrawnExtent(); got != drawn {
		t.Errorf("DrawnExtent = %v, expected InkRect %v", got, drawn)
	}
}
