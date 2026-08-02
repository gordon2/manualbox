// Package verify checks a conversion against a second, independent extraction of
// the same bytes, and reports what it finds as data rather than as log lines.
//
// # Why this can be free
//
// docs/design/conversion.md records five defects, and every one of them is
// arithmetic rather than judgement. The reason arithmetic is enough is that the
// document has already been extracted twice by different code: every block,
// column and region in this project comes from `pdftohtml -xml` through
// [doc.ExtractRuns], while [doc.ExtractText] reads the same file with
// `pdftotext`. So for every page there is a second opinion that cost nothing to
// obtain and that shares no code with the first. Comparing them is a diff.
//
// That is the whole design. No model is called, nothing is sampled, and the
// checks run in CI so a regression cannot come back. A later tier can spend
// tokens on the pages this one flags.
//
// # What it does not claim
//
// A finding is evidence, not a verdict. Two of the five checks fire on defects
// this project has deliberately accepted — a hyphen followed by a space is
// recorded in conversion.md as the smaller error, and right-to-left text was a
// known extraction defect with its own named finding so that fixing it later
// would turn off one [KindRightToLeft] rather than thousands of [KindInvented].
// It was fixed, that is exactly what happened, and the finding then had to be
// sharpened before it would go quiet on the pages that were no longer wrong: see
// [minReversibleWords], which is the clearest example here of a check outliving
// the shape of the defect it was written for. A report with no findings would mean
// the checks are broken, not that the conversion is perfect.
package verify

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/extern"
)

// Kind is what a finding is. A string for the same reason [doc.BlockKind] is one:
// it reaches a report a person reads and a test that asserts on it, where
// "coverage" survives a reordering of this list and 0 does not.
type Kind string

const (
	// KindCoverage says a page's blocks hold materially less text than
	// `pdftotext` found on the same page, so content was dropped.
	KindCoverage Kind = "coverage"
	// KindInvented says a converted block holds words that do not appear anywhere
	// in `pdftotext`'s text for that page. Characters in the wrong order produce
	// this and not [KindCoverage], which is why both checks exist: interleaved
	// columns preserve every character and destroy every word.
	KindInvented Kind = "invented-text"
	// KindRightToLeft says a page reads right to left AND still holds text this
	// pipeline read backwards: words absent from `pdftotext` that are present in it
	// reversed. Named apart from [KindInvented] because the cause is known and
	// recorded — see [minReversibleWords] and conversion.md — and reported once per
	// page rather than once per word, so a Hebrew section costs the report a line
	// instead of a thousand. Being right to left is not enough on its own: that made
	// this fire on pages that were correct.
	//
	// It reports nothing on either manual now, and that is the one finding here of
	// which a zero is the goal rather than a suspicion — the defect it names was
	// fixed in doc/bidi.go. verify.TestNoTextIsStoredReversed is what holds it there.
	KindRightToLeft Kind = "right-to-left-reversed"
	// KindJoinHyphen, KindJoinGlued and KindJoinSpace are the three shapes of a
	// suspicious join: a hyphen followed by a space mid-word, two words glued with
	// no space between them, a doubled space. Three kinds and not one because they
	// have three different causes and only one of them is deliberate — see
	// [checkJoins]. Reported, never fixed.
	KindJoinHyphen Kind = "join-hyphen-space"
	KindJoinGlued  Kind = "join-glued-words"
	KindJoinSpace  Kind = "join-double-space"
	// KindFigureBand says a figure's box is materially bigger than the shapes
	// drawn inside it, so the picture arrives with an empty band around it.
	KindFigureBand Kind = "figure-blank-band"
	// KindFigureClipped says shapes crossing the figure's box are drawn from
	// inside it, so part of the picture is cut off.
	KindFigureClipped Kind = "figure-clipped"
	// KindReadingOrder says two consecutive blocks of one region switch column
	// without going back up the page, which is what interleaving looks like from
	// the outside.
	KindReadingOrder Kind = "reading-order"
)

// AllKinds is every kind, in report order — coverage first because it is the
// question a reader asks first, the deliberate defects last.
var AllKinds = []Kind{KindCoverage, KindInvented, KindRightToLeft,
	KindJoinGlued, KindJoinHyphen, KindJoinSpace,
	KindFigureBand, KindFigureClipped, KindReadingOrder}

// Finding is one thing that is wrong, with the numbers behind it.
//
// Every field is a number or a short string, and nothing here is a formatted
// sentence except Detail: a test asserts on Kind, Page and the counts, and a
// person reads Detail. That split is deliberate — a check whose only output is
// prose cannot be regression-tested.
type Finding struct {
	// Kind is what is wrong.
	Kind Kind
	// Page is the 1-based PDF page, 0 for a finding about the document.
	Page int
	// RegionX0 and Index locate the block, matching [doc.Block]'s natural key.
	// Both are 0 for a finding about a page rather than a block.
	RegionX0 float64
	Index    int

	// Got and Want are the measurement and the bound it failed, in the check's own
	// units: runes for [KindCoverage], units of the 1.5-scaled page space for the
	// figure checks. Both 0 for a check that counts rather than measures.
	Got, Want float64
	// Count is how many things are wrong, and Total how many were examined —
	// tokens for [KindInvented], shapes for [KindFigureClipped].
	Count, Total int
	// Sample is a short excerpt of the offending text, at most [sampleRunes]
	// runes. Present so a person can find the page; never the whole block, because
	// a report is not a copy of the manual.
	Sample string
	// Detail is the finding in one sentence, numbers included.
	Detail string
}

// sampleRunes bounds an excerpt. Long enough to recognise a line on the page,
// short enough that a report naming 400 findings is still a page of text.
const sampleRunes = 60

// Report is everything one pass found, plus what it measured on the way.
type Report struct {
	// Findings are the problems, sorted by page then kind.
	Findings []Finding
	// Coverage is the per-page measurement behind [KindCoverage], kept for every
	// page examined and not only for the ones that failed. This is what a
	// threshold is chosen against, so a report that dropped it would make the
	// next threshold a guess.
	Coverage []PageCoverage
	// Pages is how many pages were examined, Figures how many figures.
	Pages, Figures int
	// Notes say what could not be checked, in the caller's terms — a missing
	// optional tool costs a check and does not fail a document, the stance
	// [doc.Convert] takes one level down.
	Notes []string
}

// PageCoverage is one page's text accounting.
type PageCoverage struct {
	// Page is the 1-based PDF page.
	Page int
	// Blocks is the non-space rune count summed over the page's converted blocks,
	// Text the same count from `pdftotext`.
	Blocks, Text int
	// Ratio is Blocks over Text, 0 when the page has no `pdftotext` text.
	Ratio float64
}

// Input is everything a check needs, already gathered.
//
// Taking it as data rather than as a path is what makes every check testable
// without poppler and without a fixture: a hermetic test hands it three blocks
// and one page of text. [Check] is the only thing here that spawns a process, and
// all it does is fill this in.
type Input struct {
	// Blocks are the converted blocks under test.
	Blocks []doc.Block
	// Figures are the converted figures under test.
	Figures []doc.ConvertedFigure
	// Text is `pdftotext`'s reading of the same document, by page. The second
	// opinion; without it the text checks are skipped and said to be skipped.
	Text []doc.Page
	// Ink is every shape each page draws, keyed by page, as [doc.ExtractInk]
	// reports it. Needed only for the pages carrying figures.
	Ink map[int][]doc.Ink
	// Pages bounds which pages are examined at all. Empty means every page a
	// block or a figure appears on. This is what keeps a conversion of 22 pages
	// from being judged against a 560-page document's other 538 blank ones.
	Pages []int
}

// Check gathers the second opinion and runs every check.
//
// It calls `pdftotext` once over the whole document and `pdftocairo` once per
// page that carries a figure — the same costs [doc.Analyze] and [doc.Convert]
// already pay, paid again because neither hands back what it read. Losing either
// tool costs the checks that need it and is written into Notes rather than
// returned as an error.
//
// conv must be a conversion of EVERY language the document holds, not one
// household's. Coverage compares a page's blocks against all the text on that
// page, and a page of the column manual holds five languages, so judging one
// language's conversion against it would report a correct conversion as having
// dropped four fifths of the page. See [ConvertAll].
func Check(ctx context.Context, path string, conv *doc.Conversion) (*Report, error) {
	if conv == nil {
		return nil, errors.New("verify: Check needs a conversion")
	}
	in := Input{Blocks: conv.Blocks, Figures: conv.Figures, Pages: conv.Pages}
	rep := &Report{}

	pageCount := conv.Scope.TotalPages
	if pageCount <= 0 {
		pageCount = maxPage(in)
	}
	text, err := doc.ExtractText(ctx, path, pageCount)
	switch {
	case err == nil:
		in.Text = text
	case errors.Is(err, extern.ErrNotFound):
		rep.note("no text comparison: " + err.Error())
	default:
		return nil, fmt.Errorf("verify: %w", err)
	}

	withFigures := make(map[int]bool, len(in.Figures))
	for i := range in.Figures {
		withFigures[in.Figures[i].Page] = true
	}
	if len(withFigures) > 0 {
		in.Ink = make(map[int][]doc.Ink, len(withFigures))
		for _, p := range sortedPages(withFigures) {
			ink, err := doc.ExtractInk(ctx, path, p)
			switch {
			case err == nil:
				in.Ink[p] = ink
			case errors.Is(err, extern.ErrNotFound):
				rep.note("no figure geometry: " + err.Error())
				in.Ink = nil
			default:
				return nil, fmt.Errorf("verify: %w", err)
			}
			if in.Ink == nil {
				break
			}
		}
	}

	got := Inspect(in)
	got.Notes = append(rep.Notes, got.Notes...)
	return got, nil
}

// ConvertAll converts a document for every language it holds, which is the input
// coverage has to be measured against.
//
// One [doc.Convert] call with every language as the household, not one call per
// language: the union is what is wanted and one call produces it, where 34 calls
// would re-read the document 34 times. Measured on the sequential manual, a
// per-language loop is about eight minutes against 25 s for this.
func ConvertAll(ctx context.Context, path string, res *doc.Result) (*doc.Conversion, error) {
	if res == nil {
		return nil, errors.New("verify: ConvertAll needs the probe's result")
	}
	summaries := res.Languages()
	langs := make([]string, 0, len(summaries))
	for i := range summaries {
		if summaries[i].Lang != "" {
			langs = append(langs, summaries[i].Lang)
		}
	}
	return doc.Convert(ctx, path, res, langs)
}

// Inspect runs every check over gathered input. Pure: it spawns nothing, reads no
// file, and is the entry point every hermetic test uses.
func Inspect(in Input) *Report {
	rep := &Report{}
	scope := pageScope(in)
	rep.Pages = len(scope)
	rep.Figures = len(in.Figures)

	if len(in.Text) == 0 {
		rep.note("coverage and word checks were skipped: no pdftotext reading was supplied")
	} else {
		cov, findings := checkCoverage(in, scope)
		rep.Coverage = cov
		rep.Findings = append(rep.Findings, findings...)
		rep.Findings = append(rep.Findings, checkText(in, scope)...)
	}
	rep.Findings = append(rep.Findings, checkJoins(in)...)
	if len(in.Figures) > 0 {
		// The two figure faults need different evidence, so they are skipped
		// separately: a blank band is read off the rendered bytes, a clipped picture
		// off the page's shapes.
		if len(in.Ink) == 0 {
			rep.note("clipped figures were not checked: no page ink was supplied")
		}
		if !anyRendered(in.Figures) {
			rep.note("blank bands were not checked: the figures carry no rendered bytes")
		}
	}
	rep.Findings = append(rep.Findings, checkFigures(in)...)
	rep.Findings = append(rep.Findings, checkOrder(in.Blocks)...)

	sort.SliceStable(rep.Findings, func(a, b int) bool {
		if rep.Findings[a].Page != rep.Findings[b].Page {
			return rep.Findings[a].Page < rep.Findings[b].Page
		}
		if rep.Findings[a].Kind != rep.Findings[b].Kind {
			return rep.Findings[a].Kind < rep.Findings[b].Kind
		}
		return rep.Findings[a].Index < rep.Findings[b].Index
	})
	return rep
}

// Count is how many findings of one kind there are.
func (r *Report) Count(k Kind) int {
	n := 0
	for i := range r.Findings {
		if r.Findings[i].Kind == k {
			n++
		}
	}
	return n
}

// Kinds is the count of every kind present, for a report a person skims.
func (r *Report) Kinds() map[Kind]int {
	out := make(map[Kind]int, 4)
	for i := range r.Findings {
		out[r.Findings[i].Kind]++
	}
	return out
}

// PagesFlagged is how many distinct pages carry a finding of one kind.
func (r *Report) PagesFlagged(k Kind) int {
	seen := make(map[int]bool)
	for i := range r.Findings {
		if r.Findings[i].Kind == k {
			seen[r.Findings[i].Page] = true
		}
	}
	return len(seen)
}

// MedianCoverage is the middle page's block-to-text ratio, over the pages that
// carry text. The median and not the mean, for the reason
// [doc.Result.MedianChars] gives: one page of front matter with two words on it
// would otherwise move the number more than a whole section.
func (r *Report) MedianCoverage() float64 {
	ratios := make([]float64, 0, len(r.Coverage))
	for i := range r.Coverage {
		if r.Coverage[i].Text > 0 {
			ratios = append(ratios, r.Coverage[i].Ratio)
		}
	}
	if len(ratios) == 0 {
		return 0
	}
	sort.Float64s(ratios)
	return ratios[len(ratios)/2]
}

// Summary describes a report in one line. Counts only — no text and no filename —
// so it is safe in a log line, the stance [doc.Conversion.Summary] takes.
func (r *Report) Summary() string {
	kinds := r.Kinds()
	parts := make([]string, 0, len(kinds))
	for _, k := range AllKinds {
		if n := kinds[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, k))
		}
	}
	s := fmt.Sprintf("%d finding(s) over %d page(s) and %d figure(s)",
		len(r.Findings), r.Pages, r.Figures)
	if len(parts) > 0 {
		s += ": " + strings.Join(parts, ", ")
	}
	if len(r.Coverage) > 0 {
		s += fmt.Sprintf("; median coverage %.2f", r.MedianCoverage())
	}
	if len(r.Notes) > 0 {
		s += fmt.Sprintf("; %d note(s)", len(r.Notes))
	}
	return s
}

func (r *Report) note(s string) { r.Notes = append(r.Notes, s) }

// anyRendered reports whether any figure carries its PNG. A conversion read back
// out of the database does not — the bytes live in the blob store — and the band
// check has to say so rather than reporting every figure as clean.
func anyRendered(figs []doc.ConvertedFigure) bool {
	for i := range figs {
		if len(figs[i].PNG) > 0 {
			return true
		}
	}
	return false
}

// pageScope is the pages to examine, ascending.
func pageScope(in Input) []int {
	if len(in.Pages) > 0 {
		seen := make(map[int]bool, len(in.Pages))
		for _, p := range in.Pages {
			seen[p] = true
		}
		return sortedPages(seen)
	}
	seen := make(map[int]bool)
	for i := range in.Blocks {
		seen[in.Blocks[i].Page] = true
	}
	for i := range in.Figures {
		seen[in.Figures[i].Page] = true
	}
	return sortedPages(seen)
}

func maxPage(in Input) int {
	last := 0
	for i := range in.Blocks {
		if in.Blocks[i].Page > last {
			last = in.Blocks[i].Page
		}
	}
	return last
}

func sortedPages(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

// excerpt trims text to something a report can print.
func excerpt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= sampleRunes {
		return s
	}
	return string(r[:sampleRunes]) + "…"
}
