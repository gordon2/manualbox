package doc

import (
	"fmt"
	"math"
	"sort"
	"strconv"
)

// Page furniture: the text a manual prints because of where a page is, not
// because of what the page says — a language tab in the corner, a folio in the
// footer, a running head at the top.
//
// [RegionBlocks] serves all of it as content and cannot do otherwise. The reason
// is recorded in blocks.go and in docs/design/conversion.md and it is worth
// repeating because it is the whole design of this file: NOTHING ON A SINGLE PAGE
// SEPARATES FURNITURE FROM CONTENT. The sequential manual genuinely titles
// sections "A", "B", "C" and "E" — 28 pages of it head a page with a bare "A" —
// so "a one-letter heading is a tab" is false on the document this is for. What
// identifies furniture is that it repeats in the same place page after page,
// which is a property of the document and not of a page, so it belongs here,
// beside [Convert], where the whole document is in view.
//
// # The rule, and the denominator that had to be got right first
//
// Furniture is text that repeats at the same height on the pages of ONE
// LANGUAGE'S section, and the second half is the part the first attempt got
// wrong. Repetition across the pages a HOUSEHOLD converted is not the signal: a
// household reading German, Russian and Japanese converts 59 pages of the
// sequential manual, and the German tab is on 16 of them — a 0.27 share that no
// threshold can separate from a genuinely repeated heading. Measured the same way
// on the column manual, German plus Ukrainian is 52 pages and the German tab is
// 19, a 0.37 share. Counted against the language's OWN pages both are 1.00. So
// the pass is per language, and the denominator is the pages that language's
// regions occupy.
//
// Two clauses, because the two kinds of furniture differ in exactly one way — the
// tab prints the same characters on every page and the folio prints different
// ones — and one rule cannot have it both ways.
//
//  1. A TAB, OR ANY REPEATED LINE. The same text at the same height on at least
//     [furnitureMinShare] of a language's pages, and on at least
//     [furnitureMinPages] of them. Stated generally rather than as "a short
//     token": if a manual prints the same running head in the same place on most
//     pages of a section, that is furniture by the same evidence, and narrowing
//     the rule to two-letter tabs would be fitting it to the two documents in
//     hand. What the two documents in hand actually contain is measured at
//     [furnitureMinShare].
//
//  2. A FOLIO. A run whose text is exactly the page number that page prints, at a
//     height where such a run occurs on at least [furnitureMinPages] pages of the
//     language. It needs no share threshold, and it must not have one: the column
//     manual prints its folio in the outer margin, so the folio falls inside
//     whichever language holds the outer column of that page, and German gets it
//     on 7 of its 26 pages — a 0.27 share, below any cut that clause 1 can use.
//     What replaces the share is a second opinion. "The page number that page
//     prints" is [Page.Folio], which `pdftotext` read from the same bytes through
//     none of this code, so a run agreeing with it is not a coincidence being
//     believed on repetition alone.
//
// # What this deliberately does not identify
//
// **The running head, on the documents measured.** The column manual prints a
// chapter name beside the tab on the same baseline — "Trockensaugen",
// "Waschsaugen", "Reinigung der AQUA-Box" — and clause 1 does not reach it,
// because a given chapter name is at that height on at most 6 of German's 26
// pages, 0.23, which is exactly where a genuinely repeated heading sits
// ("Schließen Sie den Aquafilter." is a heading on 3). The only measurement that
// separates them is the OCCUPANCY of the height rather than the text at it: the
// column manual's head line is occupied on 20 of 26 pages carrying 8 different
// strings, against 19 of 26 carrying 19 different strings for the first line of
// its body. But applying that to the sequential manual removes its section
// titles, because there the running head IS the section title, printed at the
// same height and in the same face on the page where the section starts as on
// every page after it: "Sicherheitshinweise" at y=52 on 4 pages, "Fehlersuche" on
// 3, and no page distinguishes the first from the repeats. The occupancy figures
// that would have to separate the two documents are 0.77 against 0.63, a
// twelve-point gap with nothing in between and one document on each side. So the
// column manual's head survives as content, and page 14 reads "Trockensaugen"
// where it read "D Trockensaugen". That is a smaller wrong than deleting a
// section's titles.
//
// **A tab that only some pages of a section print.** The share is a share, so a
// section printing its tab on a third of its pages keeps it. Nothing in either
// manual does; a document that does would need this measured again rather than
// the threshold lowered, because 0.5 is where it is for a reason.

// Bounds on what repetition is, measured over every language section of both
// fixtures — 5 of the 68-page parallel-columns manual and 34 of the 560-page
// sequential one, 39 sections in all. TestFurnitureThresholdsOnBothManuals
// prints the sweep these came from.
const (
	// furnitureYTolerance is how far apart two runs' tops may be and still count
	// as the same height, in the 1.5-scaled space [ExtractRuns] reports.
	//
	// The same 2.0 [orderSlack] in internal/verify uses, and for the same reason:
	// two runs a typesetter put on one line differ by rounding, and the tabs
	// measured here differ by nothing at all — the sequential manual's tab is at
	// y=58.0 on all 16 pages of its German section and the column manual's at
	// y=16.0 on all 26. A tolerance is carried anyway because a document that
	// composes its head per page rather than on a master would jitter, and 2.0 is
	// an eighth of the tightest line pitch either manual sets (16), so it cannot
	// merge two lines.
	furnitureYTolerance = 2.0

	// furnitureMinShare is how many of a language's pages must carry the same text
	// at the same height before it is furniture.
	//
	// Measured over all 39 language sections, as (pages carrying it / the
	// language's pages), the two populations do not touch:
	//
	//	the printed tab            0.96 to 1.00 -- 37 of the 39 sections
	//	the widest anything else   0.29
	//
	// The 37 are every section of the sequential manual at 1.00 (its tab is on
	// every page of every section, 12 to 22 pages each) and German, Polish and
	// Ukrainian on the column manual at 1.00, 0.96 and 1.00. The two sections with
	// no tab bucket at all are the column manual's Russian and Kazakh, which print
	// none in their columns.
	//
	// The 0.29 is the ceiling of everything that is NOT furniture, and it is worth
	// naming what is at it, because these are what a lower threshold would eat:
	// the sequential manual's per-section running heads ("Плановое обслуживание" on
	// 6 of Russian's 22 pages, 0.27; "Sicherheitshinweise" on 4 of German's 16,
	// 0.25) and the column manual's chapter heads ("Waschsaugen" on 6 of 26, 0.23).
	// Every one is a line a reader wants.
	//
	// 0.5 is the middle of 0.29 and 0.96 on a log scale as well as a linear one:
	// 1.7x above everything real and 1.9x below every tab. It is not tuned to
	// either document, and there is nothing between the two populations to tune it
	// into.
	furnitureMinShare = 0.5

	// furnitureMinPages is how many pages must carry a thing before a share means
	// anything, and it is not belt and braces: without it the rule is worthless on
	// a short section.
	//
	// Measured. The column manual has a two-page spread of service addresses whose
	// language no signal could name. At a share of 0.5 and no page floor, EVERY
	// line printed on both pages is furniture — 400 buckets, the whole spread,
	// because one page out of two is a half. The sequential manual's front matter
	// does the same over 7 pages.
	//
	// 4 is above every accident measured and far below every real tab: the smallest
	// tab bucket in either document is the sequential manual's Chinese section at
	// 12 pages, and the smallest section of either document is 12 pages. It is also
	// what the folio clause uses in place of a share, where it is the only guard,
	// so it is stated once.
	furnitureMinPages = 4

	// maxFolioRunes bounds how long a run can be and still be compared against a
	// printed page number. The same 4 [maxRunesInFolio] allows, and deliberately
	// the same constant's value rather than a new one: a folio this does not
	// recognise is one [pageFolio] never reported either.
	maxFolioRunes = maxRunesInFolio
)

// Furniture is which runs of a document are page furniture, and why.
//
// It is built once for a whole document by [FindFurniture] and consulted per
// region by [RegionBlocks]. A nil *Furniture is a normal argument and means
// "furniture was not looked for", which is what every caller that has one page
// and not the document passes — a page cannot answer the question.
type Furniture struct {
	// notes is page -> the furniture on it -> why. Keyed on the height and the
	// text rather than on an index into the page's runs, because the runs
	// [RegionBlocks] asks about are copies twice removed: usableRuns and runsInBox
	// each return a new slice.
	notes map[int]map[furnitureKey]string

	// Tabs and Folios are how many runs each clause claimed, over the whole
	// document. Counted rather than derived so that a test and a report can hold
	// the two clauses apart, which is how the rule was measured in the first place.
	Tabs, Folios int
}

type furnitureKey struct {
	// line is the run's top, rounded to furnitureYTolerance.
	line int
	// text is the run's text, normalised the way [furnitureText] normalises it.
	text string
}

// furnitureText is how two runs' text is compared.
//
// [stripFormatting] first, for the reason it exists: a right-to-left page wraps
// its Latin furniture in bidi controls, so the Hebrew section's tab reading "HE"
// is really RLE LRE H E PDF PDF, and matching it against the same tab on the next
// page fails on the invisible characters. The sequential manual has a Hebrew and
// an Arabic section, and both were checked — 16 of 16 pages each.
func furnitureText(s string) string { return collapseSpaces(stripFormatting(s)) }

func furnitureLine(y float64) int { return int(math.Round(y / furnitureYTolerance)) }

func keyOf(r *TextRun) furnitureKey {
	return furnitureKey{line: furnitureLine(r.Y), text: furnitureText(r.Text)}
}

// Note says whether a run on a page is furniture, and in checkable terms why.
//
// The empty string means it is content. A nil receiver answers that for
// everything, which is what makes [RegionBlocks] work unchanged for a caller who
// has no document.
func (f *Furniture) Note(page int, r *TextRun) string {
	if f == nil {
		return ""
	}
	return f.notes[page][keyOf(r)]
}

// Total is how many runs were claimed, both clauses together.
func (f *Furniture) Total() int {
	if f == nil {
		return 0
	}
	return f.Tabs + f.Folios
}

func (f *Furniture) mark(page int, k furnitureKey, note string) bool {
	if f.notes[page] == nil {
		f.notes[page] = make(map[furnitureKey]string, 4)
	}
	if _, seen := f.notes[page][k]; seen {
		return false
	}
	f.notes[page][k] = note
	return true
}

// FindFurniture reads a whole document and says which of its runs are furniture.
//
// pages is the positioned text of every page, regions the language map, inScope
// the household's base languages — nil for every language, the same meaning
// [RegionsBlocks] gives it — and folios the page number each page prints, keyed
// on PDF page number, which is [Page.Folio] for the pages that print one. A nil
// or empty folios map costs the folio clause and nothing else: the tabs are found
// from the runs alone.
//
// The result is per document and is consulted per region, so it is computed once
// per conversion rather than once per page. Cost is one pass over the runs of the
// pages in scope, no tool spawned and nothing read twice.
func FindFurniture(pages []PageRuns, regions []Region, inScope map[string]bool,
	folios map[int]int) *Furniture {
	f := &Furniture{notes: make(map[int]map[furnitureKey]string, 16)}

	byPage := make(map[int]*PageRuns, len(pages))
	for i := range pages {
		byPage[pages[i].No] = &pages[i]
	}

	// Grouped by base language, the key ScopeFor, RegionChars and RegionsBlocks all
	// use, so that a document printing CN, JA and ZH-HK counts one section and not
	// three. A region whose language was never established is skipped outright: it
	// has no section for a share to be a share of, and the two-page spread the
	// column manual leaves unnamed is exactly the accident furnitureMinPages exists
	// to refuse.
	byLang := make(map[string][]int, 8)
	for i := range regions {
		base := BaseLanguage(regions[i].Lang)
		if base == "" {
			continue
		}
		if inScope != nil && !inScope[base] {
			continue
		}
		byLang[base] = append(byLang[base], i)
	}

	for _, langs := range sortedKeysOfSlices(byLang) {
		f.findInSection(byPage, regions, byLang[langs], folios)
	}
	return f
}

// findInSection applies both clauses to one language's section.
func (f *Furniture) findInSection(byPage map[int]*PageRuns, regions []Region,
	idx []int, folios map[int]int) {
	// Every usable run inside this language's regions, by page. A page is counted
	// once however many regions of the language it holds, and a run once however
	// many of them contain it — regions.md rule 3 stores a whole page of one
	// language as one region, but nothing here may depend on that.
	runsOn := make(map[int][]TextRun, len(idx))
	for _, i := range idx {
		r := &regions[i]
		p := byPage[r.Page]
		if p == nil {
			continue
		}
		var dropped DroppedRuns
		inside := runsInBox(usableRuns(p.Runs, p.Width, p.Height, &dropped), r.X0, r.X1)
		seen := make(map[furnitureKey]bool, len(inside))
		for j := range runsOn[r.Page] {
			seen[keyOf(&runsOn[r.Page][j])] = true
		}
		for j := range inside {
			if k := keyOf(&inside[j]); !seen[k] {
				seen[k] = true
				runsOn[r.Page] = append(runsOn[r.Page], inside[j])
			}
		}
	}
	total := len(runsOn)
	if total < furnitureMinPages {
		return
	}

	// Clause 1: the same text at the same height. Counted in pages and not in runs,
	// so that a page printing its tab twice is one page's worth of evidence.
	repeats := make(map[furnitureKey]map[int]bool, 64)
	// Clause 2: the heights at which a run agreeing with the page's printed folio
	// was seen, and on which pages.
	folioLines := make(map[int]map[int]bool, 4)

	for page, runs := range runsOn {
		want, hasFolio := folios[page]
		text := strconv.Itoa(want)
		for i := range runs {
			k := keyOf(&runs[i])
			if repeats[k] == nil {
				repeats[k] = make(map[int]bool, total)
			}
			repeats[k][page] = true
			if hasFolio && len([]rune(k.text)) <= maxFolioRunes && k.text == text {
				if folioLines[k.line] == nil {
					folioLines[k.line] = make(map[int]bool, total)
				}
				folioLines[k.line][page] = true
			}
		}
	}

	need := int(math.Ceil(furnitureMinShare * float64(total)))
	if need < furnitureMinPages {
		need = furnitureMinPages
	}
	for k, pgs := range repeats {
		if len(pgs) < need {
			continue
		}
		note := fmt.Sprintf("page furniture: %q is printed at y=%.0f on %d of this "+
			"language's %d pages", k.text, float64(k.line)*furnitureYTolerance, len(pgs), total)
		for page := range pgs {
			if f.mark(page, k, note) {
				f.Tabs++
			}
		}
	}

	for line, pgs := range folioLines {
		if len(pgs) < furnitureMinPages {
			continue
		}
		for page := range pgs {
			k := furnitureKey{line: line, text: strconv.Itoa(folios[page])}
			note := fmt.Sprintf("page furniture: the printed folio %q, at the y=%.0f where "+
				"this language prints one on %d of its %d pages", k.text,
				float64(line)*furnitureYTolerance, len(pgs), total)
			if f.mark(page, k, note) {
				f.Folios++
			}
		}
	}
}

// splitFurniture divides a region's runs into content and furniture.
//
// Both slices keep the order they arrived in. The furniture runs are taken out
// BEFORE anything is measured or grouped, and that is the point of doing this at
// run level rather than on the finished blocks: the tab is not always a block of
// its own. The column manual sets it on the same baseline as the chapter head, so
// page 14 arrives as one heading reading "D Trockensaugen" and page 57 as
// "D Fehlerbehebung"; the sequential manual sets it under the running head close
// enough to join it, so pages 34 and 35 arrive as "Fehlersuche DE" with the tab
// at the END. Removing a whole block is wrong on all four, and taking two letters
// off the front of a block's text is wrong on two of them and unsafe on the
// others — the sequential manual heads 28 pages with a bare "A" and 22 with a
// bare "D", so a rule that eats a leading capital eats a real section title. Taken
// out as a run, the tab is simply not there when the line is assembled, and the
// heading that remains is the heading that was printed.
func splitFurniture(runs []TextRun, page int, f *Furniture) (content, furniture []TextRun) {
	if f == nil {
		return runs, nil
	}
	if _, any := f.notes[page]; !any {
		return runs, nil
	}
	content = make([]TextRun, 0, len(runs))
	for i := range runs {
		if f.Note(page, &runs[i]) != "" {
			furniture = append(furniture, runs[i])
			continue
		}
		content = append(content, runs[i])
	}
	return content, furniture
}

// furnitureBlocks turns a region's furniture runs into blocks.
//
// One block per printed line, which is what furniture is: runs on one baseline
// carrying the same note are one thing the page prints. Two furniture items that
// share a baseline stay apart — the column manual would otherwise join a tab to a
// chapter head that is not furniture, and nothing here may reunite what
// [splitFurniture] separated — so the note is part of the grouping and not only
// of the result.
//
// Every block is a [BlockParagraph] whatever the type it is set in, and that is
// deliberate. A kind is a reading decision — "this line titles what follows" —
// and a line that is on the page because of where the page is titles nothing. The
// tab classified as a level-2 heading on 110 pages is the defect, not a fact
// worth carrying forward.
func furnitureBlocks(runs []TextRun, r *Region, f *Furniture, from int) []Block {
	if len(runs) == 0 {
		return nil
	}
	ordered := make([]TextRun, len(runs))
	copy(ordered, runs)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Y != ordered[j].Y {
			return ordered[i].Y < ordered[j].Y
		}
		return ordered[i].X < ordered[j].X
	})

	tol := baselineToleranceFraction * medianHeight(ordered)
	var out []Block
	var cur []TextRun
	var curNote string

	flush := func() {
		if len(cur) == 0 {
			return
		}
		b := Block{
			Page: r.Page, RegionX0: r.X0, Index: from + len(out),
			Kind: BlockParagraph, Lang: r.Lang, Furniture: true, Note: curNote,
			Text: collapseSpaces(joinRuns(cur)), Lines: 1,
			X0: math.Inf(1), X1: math.Inf(-1), Y0: math.Inf(1), Y1: math.Inf(-1),
		}
		for i := range cur {
			b.X0 = math.Min(b.X0, cur[i].X)
			b.X1 = math.Max(b.X1, cur[i].right())
			b.Y0 = math.Min(b.Y0, cur[i].Y)
			b.Y1 = math.Max(b.Y1, cur[i].bottom())
		}
		b.Chars = len([]rune(b.Text))
		if b.Chars > 0 {
			out = append(out, b)
		}
		cur, curNote = nil, ""
	}

	for i := range ordered {
		note := f.Note(r.Page, &ordered[i])
		if len(cur) > 0 && (note != curNote || !sameBaseline(cur[0].Y, ordered[i].Y, tol)) {
			flush()
		}
		cur, curNote = append(cur, ordered[i]), note
	}
	flush()
	return out
}

// sortedKeysOfSlices returns a map's keys in order, so that a document converts
// identically twice. Same reason [sortedKeys] exists; a separate function because
// Go 1.25 will not let one body serve two map value types without generics that
// buy nothing here.
func sortedKeysOfSlices(m map[string][]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FoliosOf collects the page numbers a document's pages print, in the shape
// [FindFurniture] wants them.
//
// Separate from [FindFurniture] so that a test can state the folios it means
// without building a [Result], and so that the one place that knows where they
// come from is here rather than in [Convert].
func FoliosOf(pages []Page) map[int]int {
	if len(pages) == 0 {
		return nil
	}
	out := make(map[int]int, len(pages))
	for i := range pages {
		if pages[i].Folio != nil {
			out[pages[i].No] = *pages[i].Folio
		}
	}
	return out
}
