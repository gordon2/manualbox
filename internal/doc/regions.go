package doc

import (
	"fmt"
	"strings"
)

// A region is a language's territory on a page: the whole page where a manual
// runs its languages in sequence, a box where it runs them in parallel columns.
// See docs/design/regions.md for the contract this implements and what it
// deliberately leaves unsolved.
//
// The rule for when a page splits is the one decision here that could not be
// taken from the contract, because it needed both fixtures to settle: a page
// splits into boxes only when its columns name MORE THAN ONE language. Column
// count is not language count, in both directions, and both were measured:
//
//   - The column manual sets two columns of one language on pages 6 to 10, and
//     three of one language on 52 to 56. Its manifest calls this out precisely
//     because column count identifies nothing on its own.
//   - The sectioned manual reads as three columns on 199 of its 560 pages and
//     four on 71, and every one of those is a side-by-side troubleshooting table.
//     Pages 20 and 100 were rendered at 108 dpi and checked: the regions are the
//     table's cells, correctly located. A table cell is not a language boundary —
//     regions.md records that geometry cannot tell them apart and that the call
//     belongs above this layer. This is where that call is made.
//
// Splitting on geometry alone would therefore store four regions for a page in
// one language, on hundreds of pages of a manual that has no parallel columns at
// all, and would make the sectioned document's storage depend on its table
// layout. Splitting on language keeps a whole-page region for every one of them,
// which is the compatibility stance regions.md asks for.

// Region is one language's territory on a page.
type Region struct {
	// Page is the 1-based page number in the original PDF.
	Page int
	// X0 and X1 bound the region horizontally, in the coordinate space
	// [ExtractRuns] reports — 1.5 times the PDF's own points.
	//
	// A region covering the whole page runs from 0 to the page width. That is the
	// compatibility stance rather than a convenience: a caller clipping text to
	// the box gets the whole page, so a page-at-a-time reader needs no special
	// case and no null check for "this one has no box".
	X0, X1 float64
	// Code is the label as the document expresses it, which need not be a valid
	// tag: real manuals print D, RUS, UA and KAZ.
	Code string
	// Lang is Code normalised to BCP-47, empty when nothing was established.
	Lang string
	// Source is the signal that named it, empty when none could. Empty is a real
	// state and not a defect: a page of service addresses in six languages is
	// genuinely unnameable, and saying so beats guessing.
	Source Source
	// Chars is the rune count of the text inside the box — the unit of size that
	// replaces pages, since a page holding three languages cannot be a unit of
	// anything. Runes, not bytes: half a real manual is Cyrillic, Greek or CJK,
	// where the same amount of writing runs a third more bytes.
	Chars int
	// Runs is how many text runs the region holds. It is the density evidence, the
	// same as [Column.Runs]: a region of five runs is page furniture.
	Runs int
	// Conflict marks a region whose printed tag and whose alphabet disagreed.
	// Recorded, never resolved silently.
	Conflict bool
	// Note says in checkable terms how the region was read.
	Note string
}

// Width is the region's horizontal extent.
func (r *Region) Width() float64 { return r.X1 - r.X0 }

// PageResolution is what the per-page pass already concluded about a page. The
// region reader defers to it, and the order of precedence is the whole design:
// see [PageRegions].
type PageResolution struct {
	// Code, Lang and Source are the reconciled per-page answer, empty when the
	// per-page signals could not name the page.
	Code   string
	Lang   string
	Source Source
	// Contents marks a page the printed-index parser recognises as a contents
	// table. Such a page is furniture: it lists other sections' languages and its
	// own letters are a poor guide to anything, so a column's guess about it must
	// not become the page's language. Measured — the sectioned manual's pages 2 to
	// 5 are contents pages whose alphabet reads as Swedish and Turkish, and the
	// column manual's page 68 of service addresses reads as Turkish. All three are
	// wrong and all three are suppressed by this.
	Contents bool
}

// PageRegions divides one page into language regions.
//
// knownCodes is the vocabulary the document's own contents table declares, passed
// through to [ColumnLanguages]. resolved is what the per-page pass concluded.
//
// The precedence, in order, and every branch of it is measured against both
// fixtures:
//
//  1. A page the per-page signals named is one whole-page region in that language.
//     Those signals are reconciled from the printed tab, the printed index and the
//     page's script, and on the sectioned manual the tab alone is right on all 553
//     content pages. A column's alphabet reading must not overturn that: doing so
//     split 31 of its pages and contradicted the tab on 46 regions, every one of
//     them a short table cell — "de" read as Finnish, Spanish, Portuguese. Where
//     the columns disagree the region records a conflict, because the
//     disagreement is real information; it is not allowed to change the answer.
//  2. Otherwise, if the columns name more than one language, the page divides into
//     one region per column. This is the parallel-columns manual, where the
//     per-page signals name nothing at all on any of its eight verified pages —
//     measured, and the reason the columns are trusted here rather than there.
//  3. Otherwise, one whole-page region taking the columns' single language, unless
//     the page is a contents table, whose letters name nothing trustworthy.
//  4. Otherwise, one whole-page region with no language, which is a reportable
//     state and not a failure.
//
// What this deliberately cannot do is divide a page that carries BOTH a whole-page
// printed tab and parallel columns of different languages: rule 1 would call it one
// language and record a conflict. Neither measured manual is that document — one
// prints per-page tabs and sets one language per page, the other prints per-column
// tabs and names no page — so the mechanism for it would be invented rather than
// designed. If a third manual does it, that is the stop condition, in the sense
// docs/design/regions.md uses the term.
func PageRegions(p *PageRuns, knownCodes map[string]bool, resolved PageResolution) []Region {
	// Rule 1 defers to the per-page answer because it is stronger evidence — but
	// only where it actually names a language. A page-level answer of "fax", which
	// this document really did produce for two of its pages, is a broken index
	// parse wearing the shape of a language tag, and it must not outrank two columns
	// that read correctly as German and Polish. See [KnownLanguage] for how that
	// arises. The junk is still stored as what it is, an index run, where it stays
	// inspectable; it simply stops being evidence about the page.
	if !KnownLanguage(resolved.Lang) {
		resolved.Code, resolved.Lang, resolved.Source = "", "", ""
	}

	layout := DetectColumns(p.Runs, p.Width, p.Height)
	cols := ColumnLanguages(p.Runs, layout.Columns, knownCodes)

	// Size is counted over the runs the detector considers text, not every run in
	// the file. The column manual's text layer carries 522 sub-legible InDesign
	// slugs and parks 218 runs of a superseded address list above the top edge of
	// one page; counting those overstates that page's size by half, measured. The
	// filter is the same one DetectColumns applies internally, so a region's
	// characters and its columns are drawn from the same set of runs.
	//
	// Naming deliberately still reads every run inside the box, which is what
	// ColumnLanguages was measured against — a language is read from a sample of
	// text, while size is a measurement of it, and the right set differs. A boxed
	// region cannot come out with no characters despite a language, because a
	// column exists only where at least minColumnRuns kept runs do.
	var dropped DroppedRuns
	kept := usableRuns(p.Runs, p.Width, p.Height, &dropped)

	// Rule 2, and only where rule 1 does not apply: the columns divide the page
	// solely when the per-page pass had no answer of its own.
	if named := distinctLanguages(cols); len(named) > 1 && resolved.Lang == "" {
		out := make([]Region, 0, len(cols))
		for i := range cols {
			out = append(out, boxedRegion(p, &cols[i], kept))
		}
		return out
	}

	region, ok := wholePageRegion(p, cols, kept, resolved)
	if !ok {
		return nil
	}
	return []Region{region}
}

// distinctLanguages returns the base languages the columns named, deduplicated.
//
// Base languages, not labels: a page whose columns are tagged ZH-HK and zh is one
// language in two notations, and splitting it would invent a boundary. A column
// that named nothing is not evidence of a second language and is not counted.
func distinctLanguages(cols []ColumnLanguage) []string {
	seen := make(map[string]bool, len(cols))
	out := make([]string, 0, len(cols))
	for i := range cols {
		if cols[i].Lang == "" {
			continue
		}
		key := BaseLanguage(cols[i].Lang)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}

// boxedRegion records one column of a page that holds several languages.
func boxedRegion(p *PageRuns, col *ColumnLanguage, kept []TextRun) Region {
	inside := runsInBox(kept, col.Column.Min, col.Column.Max)
	return Region{
		Page:     p.No,
		X0:       col.Column.Min,
		X1:       col.Column.Max,
		Code:     col.Code,
		Lang:     col.Lang,
		Source:   col.Source,
		Chars:    countRunes(inside),
		Runs:     len(inside),
		Conflict: col.Conflict,
		Note:     col.Note,
	}
}

// wholePageRegion records a page that is one region: rules 1, 3 and 4 of
// [PageRegions]. ok is false for a page with neither text nor a language, which is
// nothing to record.
func wholePageRegion(p *PageRuns, cols []ColumnLanguage, kept []TextRun, resolved PageResolution) (region Region, ok bool) {
	if len(kept) == 0 && resolved.Lang == "" {
		return Region{}, false
	}

	region = Region{
		Page:   p.No,
		X0:     0,
		X1:     p.Width,
		Code:   resolved.Code,
		Lang:   resolved.Lang,
		Source: resolved.Source,
		Chars:  countRunes(kept),
		Runs:   len(kept),
	}

	columnLangs := distinctLanguages(cols)

	switch {
	case region.Lang != "":
		// Rule 1. The per-page answer stands. A column naming a different language
		// is recorded as a conflict and changes nothing — see [PageRegions] for the
		// measurement that settled this direction.
		var disputes []string
		for _, lang := range columnLangs {
			if !SameLanguage(lang, region.Lang) {
				disputes = append(disputes, DisplayName(lang))
			}
		}
		if len(disputes) > 0 {
			region.Conflict = true
			region.Note = fmt.Sprintf("the page reads as %s, but %d of its %d columns read as %s",
				DisplayName(region.Lang), len(disputes), len(cols), strings.Join(disputes, " and "))
		}

	case len(columnLangs) == 1 && !resolved.Contents:
		// Rule 3. The columns agree on one language the page-level pass could not
		// find, which on the column manual is how a single-column page and a page of
		// two same-language columns are named at all.
		for i := range cols {
			if cols[i].Lang == "" {
				continue
			}
			region.Code, region.Lang, region.Source = cols[i].Code, cols[i].Lang, cols[i].Source
			region.Conflict = cols[i].Conflict
			region.Note = cols[i].Note
			break
		}
	}

	if region.Note == "" {
		region.Note = wholePageNote(len(cols), region.Lang, columnLangs, resolved.Contents)
	}
	return region, true
}

func wholePageNote(columns int, lang string, columnLangs []string, contents bool) string {
	switch {
	case lang == "" && contents && len(columnLangs) > 0:
		// Say that something was read and refused, or this looks like a page nothing
		// could be made of.
		return fmt.Sprintf("a contents page, whose letters read as %s; too weak a "+
			"guide to name the page by", strings.Join(columnLangs, " and "))
	case lang == "":
		return "no language established for this page"
	case columns > 1:
		// Worth saying, because it is the case that looks like a mistake and is not:
		// several columns, one language, so the page is not divided.
		return fmt.Sprintf("%d columns, all of them %s, so the whole page is one region",
			columns, DisplayName(lang))
	default:
		return fmt.Sprintf("the whole page is %s", DisplayName(lang))
	}
}

// runsInBox returns the runs lying within a horizontal range.
//
// A run belongs to the box its left edge sits in, and a run crossing the boundary
// spans boxes and belongs to neither. The tolerance absorbs the rounding between
// a column's reported extent and the runs that produced it.
//
// This is the single definition of that membership, shared with the column
// language reader: a region's characters must be counted over the same runs its
// language was read from, or the two would describe different text.
func runsInBox(runs []TextRun, x0, x1 float64) []TextRun {
	var inside []TextRun
	for i := range runs {
		r := &runs[i]
		if r.X >= x0-1 && r.right() <= x1+1 {
			inside = append(inside, *r)
		}
	}
	return inside
}

func countRunes(runs []TextRun) int {
	n := 0
	for i := range runs {
		n += len([]rune(runs[i].Text))
	}
	return n
}

// RegionChars totals the characters of the regions in the given languages.
//
// Keyed on base language for the reason ScopeFor was: a summary carries one label
// per language while the regions each carry their own, so keying on the label
// counted a document printing CN, JA and ZH-HK as three languages in pages and one
// in characters.
func RegionChars(regions []Region, inScope map[string]bool) int {
	chars := 0
	for i := range regions {
		if inScope[BaseLanguage(regions[i].Lang)] {
			chars += regions[i].Chars
		}
	}
	return chars
}

// RegionSummary describes the regions of a document in one line, for logs and for
// a test that wants the shape rather than every row.
func RegionSummary(regions []Region) string {
	if len(regions) == 0 {
		return "no regions"
	}
	chars := 0
	perPage := make(map[int]int, len(regions))
	langs := make(map[string]bool, 8)
	for i := range regions {
		r := &regions[i]
		perPage[r.Page]++
		chars += r.Chars
		if r.Lang != "" {
			langs[BaseLanguage(r.Lang)] = true
		}
	}
	// A region is boxed when its page carries more than one, which is robust where
	// testing x0 against zero is not: a leftmost column may legitimately begin at
	// the page's left edge.
	boxed := 0
	for _, n := range perPage {
		if n > 1 {
			boxed += n
		}
	}
	pages := perPage
	var b strings.Builder
	fmt.Fprintf(&b, "%d regions over %d pages, %d of them boxed, %d languages, %d chars",
		len(regions), len(pages), boxed, len(langs), chars)
	return b.String()
}
