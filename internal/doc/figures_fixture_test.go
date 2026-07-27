package doc_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/extern"
)

// These drive the figure reader against both real manuals, and they are the
// valuable half of its verification: a synthetic PDF cannot reproduce a page of
// framed line drawings, a cover ornament that every area-based guard reads as the
// page's largest picture, or 1,301 gradient-mesh slivers on two pages.
//
// The numbers asserted are the ones counted off renders of the pages, and the
// header of figures.go records where each came from. Where a count is a known
// over- or under-reading it is asserted as it stands, with the reason named, so a
// change that silently moves a different set of figures cannot pass by arriving at
// the same total.

// figuresOf reads one page's figures.
func figuresOf(t *testing.T, path string, pages []doc.PageRuns, no int) []doc.Figure {
	t.Helper()
	if !extern.Available(extern.PDFToPPM) {
		t.Skipf("%s is not installed", extern.PDFToPPM.Name)
	}
	figs, err := doc.PageFigures(context.Background(), path, pageOf(t, pages, no))
	if err != nil {
		t.Fatalf("PageFigures page %d: %v", no, err)
	}
	return figs
}

// areasOf reads one page's figure geometry without rendering anything, which is
// what the whole-document sweeps below use: rendering 201 figures would add half
// a minute to them and the guards are what is under test.
func areasOf(t *testing.T, path string, page *doc.PageRuns) []doc.Figure {
	t.Helper()
	ink, err := doc.ExtractInk(context.Background(), path, page.No)
	if err != nil {
		t.Fatalf("ExtractInk page %d: %v", page.No, err)
	}
	return doc.FindFigures(ink, page)
}

// TestFiguresOfTheColumnsManualAreItsLineDrawings is the acceptance check on the
// document whose pictures are hardest: every illustration in it is vector, so
// pdfimages returns none of them, and its pages of framed drawings are ruled
// exactly the way its tables are.
func TestFiguresOfTheColumnsManualAreItsLineDrawings(t *testing.T) {
	path, pages := rulesFixture(t, "thomas-drybox-amfibia")

	// Counted off renders of the pages. Page 42 prints four framed drawings and
	// returns four, and page 22 three for three — both were one short until the
	// clip was read, because a path drawn past its frame bridged the gap to the
	// next drawing. Page 16 prints four panels and returns four, which is the case
	// conversion.md recorded as "1 for 3" and had wrong twice over: it returned one
	// figure, and the page prints four rather than three. Page 11 is one framed
	// parts diagram with the loose accessory drawings inside the same frame.
	want := map[int]int{
		1: 1, 11: 1, 12: 1, 16: 4, 22: 3, 42: 4,
		// The five ruled troubleshooting pages print no illustration at all, though
		// each carries the two largest ink clusters in the document. Which guard
		// rejects them is not the one it looks like — see
		// TestARuledTablePageYieldsNoPicture.
		57: 0, 58: 0, 59: 0, 60: 0, 61: 0,
		// Prose and an unruled specification table.
		62: 0,
	}
	for _, no := range sortedPageNumbers(want) {
		figs := areasOf(t, path, pageOf(t, pages, no))
		if len(figs) != want[no] {
			t.Errorf("page %d: %d figures, expected %d", no, len(figs), want[no])
			for i := range figs {
				t.Logf("    %s", describe(&figs[i]))
			}
		}
	}
}

// TestFigureCountsOverBothWholeDocuments is the measurement the design rests on,
// and it is a whole-document sweep on purpose: a guard tuned on the pages someone
// looked at is exactly the thing that fires 500 times on the pages nobody did.
func TestFigureCountsOverBothWholeDocuments(t *testing.T) {
	for _, tc := range []struct {
		name          string
		pagesWith     int
		figures       int
		smallest      float64 // the smallest figure's shorter side, in units
		leastInk      int
		mostOnAPage   int
		maxTextOfReal float64
	}{
		// The columns manual: 59 figures on 27 of 68 pages, 3 to 4 on the pages of
		// framed drawings. Its smallest figure's short side is 130 units — this
		// document draws nothing small, which is why the size floor decides nothing
		// on it at any value from 10 to 120.
		//
		// It was 46 on the same 27 pages before the clip was read, and the extra 13
		// are drawings that had been merged into a neighbour: the count rises where
		// the page count does not, which is what tells a split from a new find.
		// Its least-inked figure falls from 28 shapes to 26 for the same reason — a
		// merged cluster held both drawings' shapes.
		//
		// Two of these moved when trimToPicture stopped cutting a drawing away from
		// its own labels, and both moved because the old numbers were measuring the
		// cut rather than the document. The smallest side was 128, which was page
		// 52's process diagram amputated to 128.9 units tall; the real smallest
		// drawing is page 48's second panel at 130.4, and no trim has ever touched
		// it. The text ceiling rises from 9% to 10% for the same reason: the most
		// texted accepted figure is now page 53's Polish process diagram at 9.9%,
		// which keeps the three-line label block printed inside it. That is still
		// far under maxFigureTextFraction's 15%, which is what this bound is for,
		// and page 57's rejected tables are still at 37-39%.
		//
		// Merging candidate boxes that overlap moved nothing here at all, and that
		// is a measurement rather than an omission: this document has no page where
		// two candidates overlap, at any merge threshold from 0 to 1. Every number
		// on this row is the same before and after, which is what says the merge
		// pass cannot lose a picture on the document whose pictures were counted by
		// eye. See mergeOverlapping in figures.go.
		{"thomas-drybox-amfibia", 27, 59, 130, 26, 4, 0.10},
		// The sequential manual: 195 figures on 23 of 560 pages, and up to 31 on one
		// page — its front matter carries two pages that are nothing but grids of
		// small diagrams. Every figure in it is in the front matter or the back
		// matter: the 34 language sections print prose and ruled tables and no
		// illustration at all, which is docs/design/conversion.md's open problem of
		// language-neutral content measured from the other side, and it is the reason
		// a language-scoped conversion of this document would show a reader no
		// pictures at all.
		//
		// 229 before the clip and 238 after, on the same 23 pages. 195 since
		// candidate boxes that overlap are merged: 43 of those 238 were pieces of a
		// drawing that had already been found, and the page count does not move,
		// which is what tells a merge from a lost picture. Page 522 was rendered and
		// counted by eye — 9 printed drawings, 13 figures before and 9 after — and so
		// was page 524, which returned the hand out of its own drawing as a separate
		// picture and now returns 4 boxes for its 4 drawings.
		//
		// Three of the columns below move with it, all in the same direction and for
		// the same reason: the smallest and leanest candidates were fragments, and
		// they are inside something else now. The smallest side rises from 20 units
		// to 22, the least-inked figure from 28 shapes to 30, and the most-texted
		// accepted figure from 6.0% to 6.3% — a merged box is larger, so a caption
		// printed beside the drawing covers more of it.
		{"dreame-l40-ultra", 23, 195, 22, 30, 31, 0.07},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, pages := rulesFixture(t, tc.name)

			var total, withFigures, mostOnAPage, leastInk int
			smallest, maxText := 1e9, 0.0
			for i := range pages {
				figs := areasOf(t, path, &pages[i])
				if len(figs) == 0 {
					continue
				}
				withFigures++
				total += len(figs)
				if len(figs) > mostOnAPage {
					mostOnAPage = len(figs)
				}
				for j := range figs {
					f := &figs[j]
					side := f.Rect.Width()
					if f.Rect.Height() < side {
						side = f.Rect.Height()
					}
					if side < smallest {
						smallest = side
					}
					if leastInk == 0 || f.Ink < leastInk {
						leastInk = f.Ink
					}
					if f.TextFraction > maxText {
						maxText = f.TextFraction
					}
				}
			}
			t.Logf("%s: %d figures on %d of %d pages; most on one page %d; "+
				"smallest side %.0f units; least ink %d shapes; most text %.1f%%",
				tc.name, total, withFigures, len(pages), mostOnAPage,
				smallest, leastInk, 100*maxText)

			if total != tc.figures {
				t.Errorf("%d figures, expected %d", total, tc.figures)
			}
			if withFigures != tc.pagesWith {
				t.Errorf("figures on %d pages, expected %d", withFigures, tc.pagesWith)
			}
			if mostOnAPage != tc.mostOnAPage {
				t.Errorf("most figures on one page = %d, expected %d", mostOnAPage, tc.mostOnAPage)
			}
			// The smallest and least-inked figures are asserted because they are
			// what the two shape guards were set from. A change that raises either
			// threshold shows up here as a lost figure rather than as a total that
			// happens to still add up.
			if int(smallest) != int(tc.smallest) {
				t.Errorf("smallest figure side = %.0f units, expected %.0f", smallest, tc.smallest)
			}
			if leastInk != tc.leastInk {
				t.Errorf("least ink in a figure = %d shapes, expected %d", leastInk, tc.leastInk)
			}
			// Every accepted figure is far under the text guard, and the tables it
			// rejects are far over it. Asserting the accepted side pins the margin:
			// if a table starts being accepted, this moves long before a count does.
			if maxText > tc.maxTextOfReal {
				t.Errorf("an accepted figure is %.1f%% text, expected all under %.1f%%",
					100*maxText, 100*tc.maxTextOfReal)
			}
		})
	}
}

// TestARuledTablePageYieldsNoPicture records which guard actually rejects the
// columns manual's ruled tables, because the first version of this code asserted
// the wrong one.
//
// Page 57's two tables are the largest ink clusters in the document, 398x756 units
// each, and 37-39% of each is text — so the text guard would reject them. It never
// gets the chance: cairo draws each table as about fourteen rectangles, and
// fourteen is under minFigureInk, so the shape guard rejects them first. Taking the
// page's text away therefore changes nothing, which is the assertion here and the
// opposite of what was expected. Where the text guard does decide is measured by
// TestWhatTheTextGuardIsStillWorth.
func TestARuledTablePageYieldsNoPicture(t *testing.T) {
	path, pages := rulesFixture(t, "thomas-drybox-amfibia")
	page := pageOf(t, pages, 57)

	ink, err := doc.ExtractInk(context.Background(), path, page.No)
	if err != nil {
		t.Fatalf("ExtractInk: %v", err)
	}
	if figs := doc.FindFigures(ink, page); len(figs) != 0 {
		t.Errorf("page 57 returned %d figures; it prints two tables and no picture", len(figs))
	}

	blind := *page
	blind.Runs = nil
	if figs := doc.FindFigures(ink, &blind); len(figs) != 0 {
		t.Errorf("with no text page 57 returned %d figures; its tables are rejected on "+
			"shape, not on text, so this must not change", len(figs))
	}
}

// TestRenderedFigureMatchesItsRectangle checks the half of the answer geometry
// cannot check: that the bytes come back, that they are a PNG, and that the pixels
// are the rectangle at the declared resolution. It renders rather than only
// measuring, which is what would catch a crop offset — the failure that produces a
// perfectly valid image of the wrong part of the page.
func TestRenderedFigureMatchesItsRectangle(t *testing.T) {
	path, pages := rulesFixture(t, "thomas-drybox-amfibia")

	figs := figuresOf(t, path, pages, 11)
	if len(figs) != 1 {
		t.Fatalf("page 11 returned %d figures, expected its one parts diagram", len(figs))
	}
	f := &figs[0]
	if f.DPI != 216 {
		t.Errorf("rendered at %d dpi, expected 216", f.DPI)
	}
	// 216 dpi is twice the 108 the coordinates are in, so the pixel size is the
	// rectangle doubled, to within the outward rounding of the crop.
	for _, c := range []struct {
		name       string
		units      float64
		pixels     int
		unitsScale float64
	}{
		{"width", f.Rect.Width(), f.PixelWidth, 2},
		{"height", f.Rect.Height(), f.PixelHeight, 2},
	} {
		want := c.units * c.unitsScale
		if d := float64(c.pixels) - want; d < 0 || d > 2 {
			t.Errorf("%s = %d pixels, expected %.0f (the rectangle at %d dpi)",
				c.name, c.pixels, want, f.DPI)
		}
	}
	if len(f.Digest) != 64 {
		t.Errorf("digest = %q, expected 64 hex characters", f.Digest)
	}
	if len(f.PNG) < 10000 {
		t.Errorf("the parts diagram rendered to %d bytes; it is a full-page drawing", len(f.PNG))
	}

	// Rendering twice must give the same digest, or a content-addressed store
	// gets a second copy of the same picture on every re-run of an idempotent job.
	again := figuresOf(t, path, pages, 11)
	if again[0].Digest != f.Digest {
		t.Errorf("two renders of the same figure gave different digests:\n  %s\n  %s",
			f.Digest, again[0].Digest)
	}

	// MANUALBOX_FIGURE_DIR=/some/scratch writes the figures out to be looked at.
	// Never inside the repository: these are pictures out of someone's copyrighted
	// manual, and CI rejects a committed image outright.
	if dir := os.Getenv("MANUALBOX_FIGURE_DIR"); dir != "" {
		name := filepath.Join(dir, fmt.Sprintf("p%d-%d.png", f.Page, f.Index))
		if err := os.WriteFile(name, f.PNG, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		t.Logf("wrote %s", name)
	}
}

// TestEveryFigureOfTheColumnsManualRenders is where the size cap comes from, and
// it is the only test that pays for every render: 59 figures, which is also the
// measurement of what a whole document's pictures cost.
//
// Set MANUALBOX_FIGURE_DIR to a scratch directory outside the repository to write
// them all out and look at them. Never inside it — these are pictures out of
// someone's copyrighted manual, and CI rejects a committed image outright.
func TestEveryFigureOfTheColumnsManualRenders(t *testing.T) {
	path, pages := rulesFixture(t, "thomas-drybox-amfibia")
	if !extern.Available(extern.PDFToPPM) {
		t.Skipf("%s is not installed", extern.PDFToPPM.Name)
	}
	dir := os.Getenv("MANUALBOX_FIGURE_DIR")

	seen := make(map[string]string)
	var total, largest int
	var largestName string
	var count int
	for i := range pages {
		figs, err := doc.PageFigures(context.Background(), path, &pages[i])
		if err != nil {
			t.Fatalf("PageFigures page %d: %v", pages[i].No, err)
		}
		for j := range figs {
			f := &figs[j]
			count++
			total += len(f.PNG)
			if len(f.PNG) > largest {
				largest, largestName = len(f.PNG), describe(f)
			}
			if f.PixelWidth <= 0 || f.PixelHeight <= 0 {
				t.Errorf("%s rendered to %dx%d pixels", describe(f), f.PixelWidth, f.PixelHeight)
			}
			// Two pictures with the same bytes are the same picture, and the store
			// is content-addressed, so a repeat is a saving rather than a bug — but
			// a repeat nobody expected is usually furniture that got through.
			if prev, ok := seen[f.Digest]; ok {
				t.Logf("same bytes as %s: %s", prev, describe(f))
			}
			seen[f.Digest] = describe(f)

			if dir != "" {
				name := filepath.Join(dir, fmt.Sprintf("p%02d-%d.png", f.Page, f.Index))
				if err := os.WriteFile(name, f.PNG, 0o600); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}
		}
	}
	t.Logf("%d figures, %d KB in total, largest %d KB (%s)",
		count, total/1024, largest/1024, largestName)
	if count != 59 {
		t.Errorf("rendered %d figures, expected 59", count)
	}
	// The cap is two orders above the largest measured. If a figure ever gets
	// within an order of it, the cap is the thing to revisit rather than this.
	if largest > 1<<20 {
		t.Errorf("largest figure is %d KB; the largest measured was 353 KB and "+
			"maxFigurePNGBytes was set from it", largest/1024)
	}
}

// TestNoFigureOverlapsAnotherOnEitherManual is the property the merge pass exists
// to establish, asserted over both whole documents rather than on the page the
// fault was reported on.
//
// A picture served twice is the worst thing this stage can do — a reader gets the
// drawing and then a scrap of the same drawing as if it were a second picture — and
// it cannot be caught by a count, because the count of a document nobody has looked
// at is unfalsifiable. This can be: no figure's box may share any area with
// another's on the same page.
//
// Before the merge pass the columns manual had 0 overlapping pairs and the
// sequential one 53, of which 7 were one box wholly inside another. The strict
// containment census is kept separate because it was the case the report named.
func TestNoFigureOverlapsAnotherOnEitherManual(t *testing.T) {
	for _, name := range []string{"thomas-drybox-amfibia", "dreame-l40-ultra"} {
		t.Run(name, func(t *testing.T) {
			path, pages := rulesFixture(t, name)
			var overlapping, nested int
			for i := range pages {
				figs := areasOf(t, path, &pages[i])
				for a := range figs {
					for b := range figs {
						if a == b {
							continue
						}
						x := math.Min(figs[a].Rect.X1, figs[b].Rect.X1) -
							math.Max(figs[a].Rect.X0, figs[b].Rect.X0)
						y := math.Min(figs[a].Rect.Y1, figs[b].Rect.Y1) -
							math.Max(figs[a].Rect.Y0, figs[b].Rect.Y0)
						if a < b && x > 0 && y > 0 {
							overlapping++
							t.Errorf("page %d: figures %d and %d overlap\n    %s\n    %s",
								pages[i].No, a, b, describe(&figs[a]), describe(&figs[b]))
						}
						if figs[a].Rect.X0 >= figs[b].Rect.X0 && figs[a].Rect.X1 <= figs[b].Rect.X1 &&
							figs[a].Rect.Y0 >= figs[b].Rect.Y0 && figs[a].Rect.Y1 <= figs[b].Rect.Y1 {
							nested++
						}
					}
				}
			}
			t.Logf("%s: %d overlapping pair(s), %d figure(s) wholly inside another",
				name, overlapping, nested)
			if nested != 0 {
				t.Errorf("%d figure(s) sit wholly inside another; a box inside a box "+
					"is a fragment of that drawing, served to a reader as a second picture",
					nested)
			}
		})
	}
}

func describe(f *doc.Figure) string {
	return fmt.Sprintf("page %d figure %d (%.1f,%.1f)-(%.1f,%.1f) %.0fx%.0f ink=%d text=%.1f%%",
		f.Page, f.Index, f.Rect.X0, f.Rect.Y0, f.Rect.X1, f.Rect.Y1,
		f.Rect.Width(), f.Rect.Height(), f.Ink, 100*f.TextFraction)
}

func sortedPageNumbers(m map[int]int) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
