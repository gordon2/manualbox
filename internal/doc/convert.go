package doc

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/gordon2/manualbox/internal/extern"
)

// Conversion is everything a reader needs for one household's languages: the
// ordered blocks and the pictures, and nothing belonging to a language nobody
// asked for.
//
// It is the assembly of four pieces that were deliberately built apart —
// [PageRegions] for whose territory a piece of a page is, [RegionBlocks] for the
// reading order inside it, [PageTables] for the cells, [PageFigures] for the
// pictures. Nothing here stores anything: this file is a pure function of the
// document's bytes and the household's languages, the same stance the rest of
// this package takes, and it is what lets the job that calls it run twice.
type Conversion struct {
	// Blocks are the readable content in document order, only for the languages in
	// scope. Their natural key — page, region left edge, index within the region —
	// is assigned by [RegionBlocks] and is unchanged by being collected here.
	//
	// Page furniture is IN here, flagged rather than removed: a block with
	// [Block.Furniture] set is a printed tab, a folio or a running head, and it
	// comes last within its region. [Conversion.ContentBlocks] is what a reader and
	// an index want; this slice is what a check that must account for every
	// character on the page wants. See [Furniture] for why the difference matters.
	Blocks []Block
	// Figures are the pictures of the pages in scope, in page then reading order.
	Figures []ConvertedFigure
	// Scope is the intersection of the document's languages with the household's,
	// as [Result.ScopeFor] computed it. Carried so that a caller can see what was
	// converted, and what else the document holds, without asking twice.
	Scope Scope
	// Pages are the PDF pages converted, ascending. This is the funnel made
	// countable: measured at 26 of the column manual's 68 pages for German, and 22
	// of the sequential manual's 560 for Russian.
	Pages []int
	// Furniture is what the document repeats in the same place page after page, and
	// why each piece was judged so. Carried rather than only applied so that the two
	// clauses can be counted apart by a report and by a test — nil when nothing was
	// converted. See [Furniture].
	Furniture *Furniture

	// Notes say what could not be done, in the caller's terms. A missing pdftocairo
	// costs the cells and the pictures and not the text, so it is reported here
	// rather than returned as an error — the same stance [ExtractRules] takes one
	// level down, and the reason a document is never failed for want of an optional
	// tool.
	Notes []string
}

// ConvertedFigure is one picture together with the languages it belongs to.
//
// The languages are plural because of the rule docs/design/conversion.md settles:
// a picture that belongs to no language belongs to every language. A diagram
// spanning the full measure of a parallel-columns page is nobody's column and
// everybody's picture, and a reader of one language must not lose it for having
// no language of its own.
type ConvertedFigure struct {
	Figure

	// Langs are the household languages this figure is part of, as base tags,
	// sorted. One entry for a figure sitting inside a language's region; every
	// language in scope for one sitting in none.
	Langs []string
	// RegionX0 is the left edge of the region the figure sits in, matching
	// [Block.RegionX0] so that a figure can be placed among the blocks of the same
	// region. Meaningless when Neutral.
	RegionX0 float64
	// Neutral reports that the figure sits inside no region of its page. That is a
	// property of the picture and not of the household, so it is recorded rather
	// than inferred from len(Langs) — a household reading one language cannot
	// otherwise tell a neutral figure from one of its own.
	Neutral bool
}

// figureRegionSlack is how far a figure may reach past a region's edge and still
// count as inside it, in the 1.5-scaled space. The same one unit [RegionBlocks]
// allows a block, and for the same reason: a region's box comes from the extent
// of the runs assigned to it, so an exact comparison is comparing a drawing
// against a measurement of the text beside it.
const figureRegionSlack = 1.0

// Convert reads a document for one household's languages.
//
// path is the document, res the probe's result — which already carries the
// regions, so this does not divide any page again — and household the languages
// the user reads. The result is the blocks in document order plus the figures,
// for those languages only.
//
// Three rules govern it, and all three are decisions rather than mechanics:
//
//  1. Only the languages in scope are converted, which is the funnel. [ScopeFor]
//     decides which those are; nothing here re-implements the matching, so a
//     household reading pt gets a pt-BR section here exactly as the gate promised
//     it would.
//  2. A picture inside a language's region belongs to that language. A picture
//     inside none belongs to every language in scope. See [ConvertedFigure].
//  3. Nothing assumes one language's section looks like another's. The pages
//     converted are the pages the household's own regions occupy, whatever is on
//     them: measured on the sequential manual, Russian is 22 pages carrying 81
//     figures where 32 other languages are 16 pages carrying none, and any
//     per-section shape would have hidden that section's illustrated half.
//
// Cost is bounded to the pages in scope. Reading positioned text is one
// pdftohtml over the document, which the probe has already paid for once and
// this pays again because [Result] deliberately does not carry 3.8 MB of
// coordinates. Everything after that is per page and only for a page in scope:
// the cells and the pictures are two pdftocairo spawns each, which
// docs/design/conversion.md records as the accepted price of reading a page
// twice.
//
// A document is not failed for want of an optional tool. Losing pdftocairo loses
// the cells and the pictures, is written into Notes, and leaves the text intact.
func Convert(ctx context.Context, path string, res *Result, household []string) (*Conversion, error) {
	if res == nil {
		return nil, errors.New("doc: Convert needs the probe's result")
	}

	conv := &Conversion{Scope: res.ScopeFor(household)}

	// Keyed on base language, the key ScopeFor, RegionChars and RegionsBlocks all
	// use. Building it from the scope rather than from the household is what makes
	// the matching single-sourced: the scope has already resolved pt-BR against pt.
	inScope := make(map[string]bool, len(conv.Scope.Languages))
	for _, l := range conv.Scope.Languages {
		if base := BaseLanguage(l.Lang); base != "" {
			inScope[base] = true
		}
	}
	if len(inScope) == 0 {
		conv.note("none of the household's languages appear in this document")
		return conv, nil
	}

	if len(res.Regions) == 0 {
		reason := res.RegionNote
		if reason == "" {
			reason = "the probe established no language regions"
		}
		conv.note("nothing could be converted: " + reason)
		return conv, nil
	}

	// The pages the household's own regions sit on, and no others.
	want := make(map[int]bool, len(res.Regions))
	for i := range res.Regions {
		if inScope[BaseLanguage(res.Regions[i].Lang)] {
			want[res.Regions[i].Page] = true
		}
	}

	pages, err := ExtractRuns(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("doc: convert: %w", err)
	}

	// Every region of a page in scope, including the ones out of it. A figure in
	// the Polish column of a German page belongs to Polish and must be dropped, and
	// that can only be seen by holding the Polish region against it — filtering the
	// regions first would make every neighbour's picture look language-neutral and
	// hand all of them to the reader.
	byPage := make(map[int][]int, len(want))
	for i := range res.Regions {
		if want[res.Regions[i].Page] {
			byPage[res.Regions[i].Page] = append(byPage[res.Regions[i].Page], i)
		}
	}

	scopeLangs := sortedKeys(inScope)
	tables := make(map[int][]RuledTable, len(want))
	var ruleFailures, inkFailures []int
	rules, ink := true, true

	for i := range pages {
		p := &pages[i]
		if !want[p.No] {
			continue
		}
		// A cancelled job stops here rather than in poppler. Without this every
		// remaining page still spawns, fails on the dead context, and comes back as a
		// note saying its drawings could not be read — which reports a cancellation
		// as a defect in the document.
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("doc: convert: %w", err)
		}
		conv.Pages = append(conv.Pages, p.No)

		if rules {
			found, err := PageTables(ctx, path, p)
			switch {
			case err == nil:
				if len(found) > 0 {
					tables[p.No] = found
				}
			case errors.Is(err, extern.ErrNotFound):
				// The tool is absent, so every remaining page would fail the same way.
				// Stop spawning and say so once.
				rules = false
				conv.note("no table cells: " + err.Error())
			default:
				ruleFailures = append(ruleFailures, p.No)
			}
		}

		if ink {
			found, err := PageFigures(ctx, path, p)
			switch {
			case err == nil:
				for j := range found {
					if fig, ok := attribute(&found[j], res.Regions, byPage[p.No], inScope, scopeLangs); ok {
						conv.Figures = append(conv.Figures, fig)
					}
				}
			case errors.Is(err, extern.ErrNotFound):
				ink = false
				conv.note("no pictures: " + err.Error())
			default:
				inkFailures = append(inkFailures, p.No)
			}
		}
	}

	// The furniture pass, and this is the only place in the pipeline that can run
	// it: it needs every page of a language's section at once, which is what this
	// function holds and what [RegionBlocks] by construction never does. Free — one
	// walk over runs already in memory, no tool spawned. See [FindFurniture].
	fur := FindFurniture(pages, res.Regions, inScope, FoliosOf(res.Pages))
	conv.Furniture = fur
	conv.Blocks = RegionsBlocks(pages, res.Regions, inScope, tables, fur)

	// One note per kind rather than one per page: a document whose pdftocairo dies
	// on forty pages should say so in a line a user can read.
	if len(ruleFailures) > 0 {
		conv.note(fmt.Sprintf("the ruled lines of %d page(s) could not be read, so their "+
			"tables read as lines of text: %s", len(ruleFailures), pageList(ruleFailures)))
	}
	if len(inkFailures) > 0 {
		conv.note(fmt.Sprintf("the drawings of %d page(s) could not be read, so their "+
			"pictures are missing: %s", len(inkFailures), pageList(inkFailures)))
	}
	return conv, nil
}

// attribute decides which languages a figure belongs to.
//
// A figure is inside a region when its horizontal extent lies within the
// region's box; a region spans the page's full height, so there is no vertical
// question to ask. A figure that lies inside none of its page's regions — which
// includes one straddling two of them — belongs to every language in scope,
// which is rule 2 of [Convert].
//
// The extent asked about is [Figure.DrawnExtent], never the rendered crop. Those differ once [doc.growToLabels] has taken a label in, and using the
// crop would let a drawing grown sideways onto its label reach out of its own
// column and be served to every household — the one failure the funnel may not
// have. A picture's language is a property of the picture, not of how much of the
// page around it was rendered.
//
// The false return is the third case, and it is the funnel: a figure inside a
// region in a language the household does not read is that language's picture,
// and is dropped exactly as its text is.
func attribute(f *Figure, regions []Region, onPage []int, inScope map[string]bool,
	scopeLangs []string) (ConvertedFigure, bool) {
	for _, i := range onPage {
		r := &regions[i]
		drawn := f.DrawnExtent()
		if drawn.X0 < r.X0-figureRegionSlack || drawn.X1 > r.X1+figureRegionSlack {
			continue
		}
		base := BaseLanguage(r.Lang)
		if base == "" {
			// A region no signal could name is not a language, so a figure inside it
			// has none either and falls through to the neutral rule. That is the
			// stance everywhere else here: an unnamed region is a reportable state,
			// never a sixth language.
			continue
		}
		if !inScope[base] {
			return ConvertedFigure{}, false
		}
		return ConvertedFigure{Figure: *f, Langs: []string{base}, RegionX0: r.X0}, true
	}
	// A copy per figure rather than the one slice shared by all of them. Langs is an
	// exported field on a value type, and handing every neutral figure the same
	// backing array makes one caller's append visible in the others.
	langs := make([]string, len(scopeLangs))
	copy(langs, scopeLangs)
	return ConvertedFigure{Figure: *f, Langs: langs, Neutral: true}, true
}

// FiguresFor returns the figures belonging to one language, which is every
// figure of its own regions plus every language-neutral one.
func (c *Conversion) FiguresFor(lang string) []ConvertedFigure {
	base := BaseLanguage(lang)
	var out []ConvertedFigure
	for i := range c.Figures {
		for _, l := range c.Figures[i].Langs {
			if l == base {
				out = append(out, c.Figures[i])
				break
			}
		}
	}
	return out
}

// BlocksFor returns the blocks of one language, in document order, furniture
// included. A caller wanting what a person reads intersects it with
// [Conversion.ContentBlocks] — or, more simply, skips the blocks whose
// [Block.Furniture] is set, which is what that method does.
func (c *Conversion) BlocksFor(lang string) []Block {
	base := BaseLanguage(lang)
	var out []Block
	for i := range c.Blocks {
		if BaseLanguage(c.Blocks[i].Lang) == base {
			out = append(out, c.Blocks[i])
		}
	}
	return out
}

// ContentBlocks returns the blocks a person reads: everything except the page
// furniture.
//
// This is the slice a reader renders and an index indexes, and it exists as a
// method rather than as a filter each caller writes so that "what is content" has
// one answer. [Conversion.Blocks] keeps the furniture, because a check comparing a
// conversion against a second extraction of the same page must be able to account
// for every character the page prints.
func (c *Conversion) ContentBlocks() []Block {
	out := make([]Block, 0, len(c.Blocks))
	for i := range c.Blocks {
		if !c.Blocks[i].Furniture {
			out = append(out, c.Blocks[i])
		}
	}
	return out
}

// FurnitureBlocks returns only the page furniture, in document order. The
// complement of [Conversion.ContentBlocks], and what a report counts.
func (c *Conversion) FurnitureBlocks() []Block {
	var out []Block
	for i := range c.Blocks {
		if c.Blocks[i].Furniture {
			out = append(out, c.Blocks[i])
		}
	}
	return out
}

// Summary describes a conversion in one line, for logs and for a test that wants
// the shape rather than every row. It carries no filename and no text, only
// counts, so it is safe in a log line — the same stance [Result.String] takes.
func (c *Conversion) Summary() string {
	langs := make([]string, 0, len(c.Scope.Languages))
	for _, l := range c.Scope.Languages {
		langs = append(langs, l.Lang)
	}
	neutral := 0
	for i := range c.Figures {
		if c.Figures[i].Neutral {
			neutral++
		}
	}
	// Content first and furniture named separately, because the same document
	// converted before and after the furniture pass reports the same total and a
	// summary that only totalled would hide the whole change.
	fur := 0
	for i := range c.Blocks {
		if c.Blocks[i].Furniture {
			fur++
		}
	}
	s := fmt.Sprintf("%d blocks (%d furniture), %d figures (%d language-neutral) over %d of %d pages, %s",
		len(c.Blocks)-fur, fur, len(c.Figures), neutral, len(c.Pages), c.Scope.TotalPages,
		strings.Join(langs, "+"))
	if len(c.Notes) > 0 {
		s += fmt.Sprintf(", %d note(s)", len(c.Notes))
	}
	return s
}

func (c *Conversion) note(s string) { c.Notes = append(c.Notes, s) }

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// pageList renders a handful of page numbers, because a note naming forty pages
// is not a note anybody reads.
func pageList(pages []int) string {
	const show = 8
	parts := make([]string, 0, show+1)
	for i, p := range pages {
		if i == show {
			parts = append(parts, fmt.Sprintf("and %d more", len(pages)-show))
			break
		}
		parts = append(parts, fmt.Sprint(p))
	}
	return strings.Join(parts, ", ")
}
