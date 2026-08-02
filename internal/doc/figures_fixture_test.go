package doc_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
					// The drawn box, not the crop. The size floor is a guard and it
					// judges the drawing, so the census of what it admitted has to ask
					// the same box — since growToLabels this reads 24 off Rect, because
					// the smallest drawing grew on one edge, and that number is a
					// measurement of the crop rather than of the threshold.
					drawn := f.DrawnExtent()
					side := drawn.Width()
					if drawn.Height() < side {
						side = drawn.Height()
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
// Before the merge pass the columns manual had 0 overlapping pairs on that test and
// the sequential one 53, of which 7 were one box wholly inside another. The strict
// containment census is kept separate because it was the case the report named.
//
// # The box the property is about is now the drawn one
//
// It was asserted of Rect, and Rect stopped being the box the merge pass produces
// once growToLabels started moving edges outward. Measured at both rects over both
// whole documents: the drawn boxes overlap in 0 pairs and nest in 0 on either
// document, exactly as before, and it is the rendered crops that overlap — 11 pairs,
// every one of them on page 5 or 6 of the sequential manual, with one crop wholly
// inside another on page 5.
//
// So the merge pass's property is intact and is asserted where it belongs, of the
// drawn box, at zero on both documents and on every page.
//
// The crops are asserted as the property and not as a census: no two crops may
// overlap on any page except the two front-matter plate pages, which fall outside
// every language region and are never converted. How many overlap on those two is
// pinned by TestGrowSweep, which sweeps the thresholds that produce them, and
// deliberately not restated here — one number, one place. What this adds from
// outside the package is the zero everywhere else, which is the half that matters: a
// crop overlap on page 521 would be a picture served twice to a reader.
func TestNoFigureOverlapsAnotherOnEitherManual(t *testing.T) {
	// The two plate pages of the sequential manual. Recorded rather than fixed:
	// arbitrating which of two drawings a shared corridor belongs to would be a rule
	// invented for one plate, and no page a conversion serves is affected.
	plate := map[int]bool{5: true, 6: true}

	for _, name := range []string{"thomas-drybox-amfibia", "dreame-l40-ultra"} {
		t.Run(name, func(t *testing.T) {
			path, pages := rulesFixture(t, name)
			inkOverlaps, inkNested := 0, 0
			cropOverlaps, cropNested := map[int]int{}, map[int]int{}
			for i := range pages {
				no := pages[i].No
				figs := areasOf(t, path, &pages[i])
				for a := range figs {
					// Growth only ever grows. This is the invariant that says a moved edge
					// is a crop widened onto a label and never a drawing cut away, which is
					// what trimToPicture does and what growth is the opposite of. Checked
					// here rather than in its own sweep because this test already walks
					// every figure of both documents.
					if !within(figs[a].DrawnExtent(), figs[a].Rect) {
						t.Errorf("page %d: %s has a crop that does not contain its drawn box "+
							"(%.1f,%.1f)-(%.1f,%.1f)", no, describe(&figs[a]),
							figs[a].InkRect.X0, figs[a].InkRect.Y0, figs[a].InkRect.X1, figs[a].InkRect.Y1)
					}
					for b := range figs {
						if a == b {
							continue
						}
						fa, fb := &figs[a], &figs[b]
						if a < b && overlaps(fa.DrawnExtent(), fb.DrawnExtent()) {
							inkOverlaps++
							t.Errorf("page %d: the drawn boxes of figures %d and %d overlap\n    %s\n    %s",
								no, a, b, describe(fa), describe(fb))
						}
						if within(fa.DrawnExtent(), fb.DrawnExtent()) {
							inkNested++
						}
						if a < b && overlaps(fa.Rect, fb.Rect) {
							cropOverlaps[no]++
						}
						if within(fa.Rect, fb.Rect) {
							cropNested[no]++
						}
					}
				}
			}
			t.Logf("%s: drawn boxes %d overlapping pair(s), %d nested; "+
				"crops %v overlapping pair(s) by page, %v nested by page",
				name, inkOverlaps, inkNested, cropOverlaps, cropNested)

			if inkNested != 0 {
				t.Errorf("%d drawn box(es) sit wholly inside another; a box inside a box "+
					"is a fragment of that drawing, served to a reader as a second picture",
					inkNested)
			}
			for _, c := range []struct {
				what      string
				census    map[int]int
				complaint string
			}{
				{"overlap", cropOverlaps,
					"two crops overlapping on a page a conversion serves is one picture served twice"},
				{"nest", cropNested,
					"a crop wholly inside another is a scrap of that drawing served as a picture of its own"},
			} {
				for _, no := range sortedPageNumbers(c.census) {
					if !plate[no] {
						t.Errorf("page %d: %d crop %s(s), expected none — %s. Growth's overlaps "+
							"are confined to the plate pages 5 and 6; this page is one a reader is served",
							no, c.census[no], c.what, c.complaint)
					}
				}
			}
		})
	}
}

// TestTheReportedPageKeepsItsCalloutLabels is the fault the user reported, on the
// page it was reported on and against the three drawings it was reported against:
// PDF page 521 of the sequential manual, the RU product overview, whose crops kept
// every leader line and lost all 34 of the labels those leaders point at.
//
// The three rows below are the whole of the change made visible on one page. Each
// drawn box is the bounding box of the ink and is what both guards judged; each crop
// is what is now rendered; and the label count is how many of the page's text runs
// the crop reaches that the drawn box did not.
//
// The grown edges land on exact integers — 484, 397, 459, 872 — because pdftohtml
// reports a run's box in whole units, and an edge moves to a label's far edge.
func TestTheReportedPageKeepsItsCalloutLabels(t *testing.T) {
	path, pages := rulesFixture(t, "dreame-l40-ultra")
	page := pageOf(t, pages, 521)

	figs := areasOf(t, path, page)
	if len(figs) != 3 {
		t.Fatalf("page 521 returned %d figures; it prints three drawings", len(figs))
	}
	for i := range figs {
		f := &figs[i]
		t.Logf("figure %d: drawn (%.4f,%.4f)-(%.4f,%.4f) crop (%.4f,%.4f)-(%.4f,%.4f) %d label(s)",
			f.Index, f.InkRect.X0, f.InkRect.Y0, f.InkRect.X1, f.InkRect.Y1,
			f.Rect.X0, f.Rect.Y0, f.Rect.X1, f.Rect.Y1, len(labelsTakenIn(f, page)))
	}
	for i, c := range []struct {
		ink, crop doc.CellRect
		labels    int
	}{
		// The base station, seen from the front. Its left edge takes in the two label
		// columns printed over the station's own footprint; its right edge is the one
		// that does not move, asserted separately below.
		{doc.CellRect{X0: 539.0625, Y0: 96.0820, X1: 748.3594, Y1: 277.5645},
			doc.CellRect{X0: 484.0000, Y0: 96.0820, X1: 748.3594, Y1: 277.5645}, 9},
		// The lidar drawing, the one figures.go measured: its box ends at 263.0, its
		// leader terminators are the marks at 259.6-263.0 that set that edge, and all
		// eleven of its labels begin at 266.0. Three units, every one of them. The
		// right edge reaches 397 and not the 469 its longest label ends at, because
		// the neighbouring drawing's labels start at 400 — a claimed label may be cut
		// short, prose may not.
		{doc.CellRect{X0: 65.9355, Y0: 116.9121, X1: 263.0098, Y1: 364.4355},
			doc.CellRect{X0: 65.9355, Y0: 116.9121, X1: 397.0000, Y1: 364.4355}, 11},
		// The underside, whose labels sit on both sides of it. It is the drawing that
		// grows on two edges and takes in the most.
		{doc.CellRect{X0: 579.2813, Y0: 359.4258, X1: 765.1171, Y1: 522.7383},
			doc.CellRect{X0: 459.0000, Y0: 359.4258, X1: 872.0000, Y1: 522.7383}, 14},
	} {
		f := &figs[i]
		// A thousandth of a unit, which is two orders tighter than the difference any
		// of these numbers is about: the smallest edge move on the page is figure 0's
		// 55 units.
		if !sameBox(f.InkRect, c.ink, 0.001) {
			t.Errorf("figure %d's drawn box is (%.4f,%.4f)-(%.4f,%.4f), measured at "+
				"(%.4f,%.4f)-(%.4f,%.4f)", i,
				f.InkRect.X0, f.InkRect.Y0, f.InkRect.X1, f.InkRect.Y1,
				c.ink.X0, c.ink.Y0, c.ink.X1, c.ink.Y1)
		}
		if !sameBox(f.Rect, c.crop, 0.001) {
			t.Errorf("figure %d's crop is (%.4f,%.4f)-(%.4f,%.4f), measured at "+
				"(%.4f,%.4f)-(%.4f,%.4f)", i,
				f.Rect.X0, f.Rect.Y0, f.Rect.X1, f.Rect.Y1,
				c.crop.X0, c.crop.Y0, c.crop.X1, c.crop.Y1)
		}
		if n := len(labelsTakenIn(f, page)); n != c.labels {
			t.Errorf("figure %d's crop took in %d label(s), measured at %d", i, n, c.labels)
			for _, r := range labelsTakenIn(f, page) {
				t.Logf("    %q at (%.0f,%.0f)", strings.TrimSpace(r.Text), r.X, r.Y)
			}
		}
	}

	// The edge that did not move, which is the conservative rule working and not a
	// shortfall. Figure 0's right corridor holds the label "Кнопка сброса" at x=754,
	// 5.6 units out, and then the five lines of the bullet description explaining it,
	// so growing right would drag a paragraph into a picture.
	if figs[0].Rect.X1 != figs[0].InkRect.X1 {
		t.Errorf("figure 0's right edge moved from %.4f to %.4f; the corridor beyond it "+
			"holds a label and then five lines of prose, so it must stay where the ink put it",
			figs[0].InkRect.X1, figs[0].Rect.X1)
	}
	if reached(figs[0].Rect, runContaining(t, page, "Кнопка сброса")) {
		t.Error(`figure 0's crop reaches "Кнопка сброса"; the five bullet lines under it ` +
			"are prose, and taking the label means taking them")
	}

	// And the labels that do land in a crop now, named rather than counted: a count
	// alone would pass if growth took in nine of something else.
	for _, c := range []struct {
		figure int
		label  string
		whole  bool
	}{
		{0, "Разъемы", false}, // clipped by the drawing's own bottom edge, 4.4 units below it
		{1, "Микрофон", true},
		{1, "Крышка лидара", true},
		{2, "Датчик ковра", true},
	} {
		r := runContaining(t, page, c.label)
		f := &figs[c.figure]
		if reached(f.InkRect, r) {
			t.Errorf("%q is already inside figure %d's drawn box; it is meant to be a "+
				"label the crop had lost", c.label, c.figure)
		}
		if !reached(f.Rect, r) {
			t.Errorf("%q at (%.0f,%.0f) is outside figure %d's crop (%.1f,%.1f)-(%.1f,%.1f); "+
				"it is one of the labels the leaders point at", c.label, r.X, r.Y, c.figure,
				f.Rect.X0, f.Rect.Y0, f.Rect.X1, f.Rect.Y1)
		}
		if c.whole && !boxed(f.Rect, r) {
			t.Errorf("%q is only partly inside figure %d's crop; this one is printed clear "+
				"of the crop's other three edges and must arrive whole", c.label, c.figure)
		}
	}
}

// TestGrowthDoesNotChangeWhichLanguageAPictureBelongsTo is the funnel's promise
// applied to the one thing growth could break: a box grown sideways onto a label
// must not reach out of its own language column and be served to every household.
// attribute asks Figure.DrawnExtent for exactly that reason, and this checks the
// answer through doc.Convert rather than through the geometry.
//
// It is honest about what these two documents can and cannot show, because the
// answer is not what it looks like. The only fixture with side-by-side language
// columns is the columns manual, and it grows nothing at all — so on these fixtures
// the failure DrawnExtent prevents is unreachable, and this test cannot refute an
// attribute that read Rect. What it can do is pin that: the count is asserted
// together with "no served figure of that document is grown", so if a future change
// makes the columns manual grow, this stops being a vacuous pass and the counts move
// with it. The refutation that does not depend on a document behaving this way is
// TestAGrownCropDoesNotChangeAFiguresLanguage in convert_internal_test.go, which
// builds the case these fixtures cannot supply: page 14's real German and Polish
// columns and a figure whose drawn box is German while its crop reaches 76 units
// into Polish. The two tests deliberately say different things about the same rule —
// that one can fail, this one pins that the real documents still come back at the
// numbers they came back at.
//
// The sequential manual is the other side of the same fact: 14 of the 65 figures its
// Russian conversion serves ARE grown, so these counts are the counts of a corpus
// that actually contains grown boxes. Its regions are whole-page, though, which is
// why growth cannot move its attribution either.
//
// One note for whoever reads these numbers next. The 54, 53, 52 and 51 below are
// measured; conversion.md and CLAUDE.md carried 41, 40, 39 and 38 for a while, and
// those were stale from before the clip was read and candidate boxes were merged —
// convert_fixture_test.go already asserted the post-merge 53 for German alone and 65
// for Russian while the docs still said 40 and 81. Growth moved none of them.
func TestGrowthDoesNotChangeWhichLanguageAPictureBelongsTo(t *testing.T) {
	// The de+uk conversion of the columns manual, which is the case that has no test
	// elsewhere: a household reading two of its five languages. 54 figures, of which
	// German sees 53 and Ukrainian 52, overlapping in the 51 that sit inside no
	// region of their page — page 14's two photographs among them, which the render
	// shows belong to neither text column.
	both := convertFixture(t, "thomas-drybox-amfibia", "de", "uk")
	neutral := 0
	for i := range both.Figures {
		if both.Figures[i].Neutral {
			neutral++
		}
	}
	if len(both.Figures) != 54 || len(both.FiguresFor("de")) != 53 ||
		len(both.FiguresFor("uk")) != 52 || neutral != 51 {
		t.Errorf("de+uk stores %d figures, German sees %d and Ukrainian %d, %d neutral; "+
			"measured at 54, 53, 52 and 51", len(both.Figures), len(both.FiguresFor("de")),
			len(both.FiguresFor("uk")), neutral)
	}
	// Which is a vacuous pass unless it is said so: nothing in this document grows,
	// so no figure it serves can have been attributed by a grown box.
	for i := range both.Figures {
		if f := &both.Figures[i]; f.Rect != f.DrawnExtent() {
			t.Errorf("page %d figure %d of the columns manual grew, from "+
				"(%.1f,%.1f)-(%.1f,%.1f) to (%.1f,%.1f)-(%.1f,%.1f). That is not a failure in "+
				"itself, but the counts above were measured on a document that grows nothing, "+
				"and whether a grown box stayed inside its own column is now a live question",
				f.Page, f.Index,
				f.DrawnExtent().X0, f.DrawnExtent().Y0, f.DrawnExtent().X1, f.DrawnExtent().Y1,
				f.Rect.X0, f.Rect.Y0, f.Rect.X1, f.Rect.Y1)
		}
	}

	// The sequential manual's Russian, which is where the grown boxes are. Its 65 and
	// its page range 517-538 are asserted by TestConvertTheSequentialManualForRussian,
	// as the columns manual's German 53 is by TestConvertTheColumnManualForGerman, and
	// neither is restated here; what is new is that grown boxes are among
	// what a reader is served, and that each one arrives with the drawn box the
	// language question was asked of.
	ru := convertFixture(t, "dreame-l40-ultra", "ru")
	grown := 0
	for i := range ru.Figures {
		f := &ru.Figures[i]
		if f.InkRect == (doc.CellRect{}) {
			t.Errorf("page %d figure %d carries no drawn box; attribute asked DrawnExtent "+
				"and would have fallen back to the crop", f.Page, f.Index)
		}
		if !within(f.DrawnExtent(), f.Rect) {
			t.Errorf("page %d figure %d has a drawn box outside its crop", f.Page, f.Index)
		}
		if f.Rect != f.DrawnExtent() {
			grown++
		}
	}
	if grown != 14 {
		t.Errorf("%d of the Russian conversion's figures are grown, measured at 14; if this "+
			"is zero the counts above no longer say anything about growth", grown)
	}
}

func describe(f *doc.Figure) string {
	return fmt.Sprintf("page %d figure %d (%.1f,%.1f)-(%.1f,%.1f) %.0fx%.0f ink=%d text=%.1f%%",
		f.Page, f.Index, f.Rect.X0, f.Rect.Y0, f.Rect.X1, f.Rect.Y1,
		f.Rect.Width(), f.Rect.Height(), f.Ink, 100*f.TextFraction)
}

// overlaps reports whether two boxes share any area.
func overlaps(a, b doc.CellRect) bool {
	return math.Min(a.X1, b.X1) > math.Max(a.X0, b.X0) &&
		math.Min(a.Y1, b.Y1) > math.Max(a.Y0, b.Y0)
}

// within reports whether the first box lies wholly inside the second.
func within(inner, outer doc.CellRect) bool {
	return inner.X0 >= outer.X0 && inner.X1 <= outer.X1 &&
		inner.Y0 >= outer.Y0 && inner.Y1 <= outer.Y1
}

// sameBox compares two boxes edge by edge, to a tolerance.
func sameBox(a, b doc.CellRect, tol float64) bool {
	return near(a.X0, b.X0, tol) && near(a.Y0, b.Y0, tol) &&
		near(a.X1, b.X1, tol) && near(a.Y1, b.Y1, tol)
}

func boxOf(r *doc.TextRun) doc.CellRect {
	return doc.CellRect{X0: r.X, Y0: r.Y, X1: r.X + r.Width, Y1: r.Y + r.Height}
}

// reached reports whether a box takes in any part of a run, and boxed whether it
// takes in the whole of it.
func reached(box doc.CellRect, r *doc.TextRun) bool { return overlaps(box, boxOf(r)) }
func boxed(box doc.CellRect, r *doc.TextRun) bool   { return within(boxOf(r), box) }

// labelsTakenIn is the page's text runs a figure's crop reaches that its drawn box
// did not — the labels growth bought, counted rather than described.
//
// It asks whether the crop reaches the run and not whether it holds all of it, and
// that is the difference between 34 labels on page 521 and 23. A claimed label may
// be cut short: figure 1 there reaches x=397 where its own longest label ends at
// 469, because the neighbouring drawing's labels start at 400. A containment test
// reads that label as not taken in when a reader can see most of it and, crucially,
// can see which part the leader points at.
func labelsTakenIn(f *doc.Figure, page *doc.PageRuns) []*doc.TextRun {
	var out []*doc.TextRun
	for i := range page.Runs {
		r := &page.Runs[i]
		if strings.TrimSpace(r.Text) == "" {
			continue
		}
		if reached(f.Rect, r) && !reached(f.DrawnExtent(), r) {
			out = append(out, r)
		}
	}
	return out
}

// runContaining finds the one run holding a piece of text, and fails if the page
// does not print it exactly once — a label asserted by name is worth nothing if the
// name matches two runs or none.
func runContaining(t *testing.T, page *doc.PageRuns, text string) *doc.TextRun {
	t.Helper()
	var found *doc.TextRun
	n := 0
	for i := range page.Runs {
		if strings.Contains(page.Runs[i].Text, text) {
			found = &page.Runs[i]
			n++
		}
	}
	if n != 1 {
		t.Fatalf("page %d prints %q in %d runs, expected exactly one", page.No, text, n)
	}
	return found
}

func sortedPageNumbers(m map[int]int) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
