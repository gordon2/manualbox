// Package doc turns an uploaded document into facts about itself, cheaply,
// before anything expensive happens to it.
//
// The pipeline this package implements is the funnel described in
// docs/design/ingest.md. Its purpose is not to convert anything: it is to find
// out what is being held, for free, so that the expensive work can be aimed at
// the small part of the document that matters. On a measured 560-page,
// 34-language manual, 98% of a naive conversion spend buys nothing.
//
// Three stages run here, and all three are free:
//
//	Stage 0  pdfinfo      page count, encryption, structure tags   ~0.06 s
//	Stage 1  pdftotext    per-page text, is there a text layer?    ~1.8 s
//	Stage 2  (local)      the language map, from several signals    ~0 s
//
// Nothing in this package calls a model, sends anything over a network, or
// costs money. Stage 3 — asking the user what to process — and stage 4 — actually
// processing it — happen elsewhere, after this package has reported what it found.
package doc

import (
	"context"
	"fmt"
	"slices"
	"sort"
)

// Source identifies which signal produced a language run. Every run records its
// own source so that a disagreement between signals stays inspectable instead of
// being averaged into a single unattributable answer.
type Source string

// The language signals, cheapest first. See docs/design/language-detection.md.
const (
	// SourcePageTag is the language code a manual prints on each page.
	SourcePageTag Source = "page-tag"
	// SourceIndex is the manual's own printed contents table.
	SourceIndex Source = "index"
	// SourceScript is Unicode script analysis.
	SourceScript Source = "script"
	// SourceDetector is statistical language detection. No implementation is
	// wired up yet; the constant exists so stored rows and the reconciliation
	// order do not change when one is added.
	SourceDetector Source = "detector"
	// SourceReconciled is the resolved view built from the others.
	SourceReconciled Source = "reconciled"
)

// Run is a contiguous span of pages in one language, according to one signal.
type Run struct {
	Source Source `json:"source"`
	// Code is the language as the document expresses it, which may not be a valid
	// tag: real manuals print UA, CZ and ZH-HK.
	Code string `json:"code"`
	// Lang is Code normalised to BCP-47, empty when it could not be normalised.
	Lang string `json:"lang"`
	// Start and End are inclusive 1-based PDF page numbers.
	Start int `json:"start"`
	End   int `json:"end"`
	// Title is the section title as printed in the manual's contents table, in
	// that language. Only the index signal can supply it.
	Title string `json:"title,omitempty"`
	// PrintedPage is the start page the printed index claims. Only the index
	// signal sets it, and it is frequently 1-2 off from reality.
	PrintedPage *int `json:"printedPage,omitempty"`
	// Confidence is this signal's confidence in this run, 0 to 1.
	Confidence float64 `json:"confidence"`
	// Conflict marks a reconciled run the signals disagreed about.
	Conflict bool `json:"conflict"`
	// Note explains a conflict, or records how the run was established.
	Note string `json:"note,omitempty"`
}

// Pages is how many pages the run covers.
//
// A run that named a language but could not place it covers none. Start 0 means
// "unplaceable", not page zero — the arithmetic span reported the printed index's
// unplaceable HE, AR and CZ entries as one-page sections at 0-0.
func (r Run) Pages() int {
	if r.Start == 0 {
		return 0
	}
	return r.End - r.Start + 1
}

// Contains reports whether a page falls inside the run.
func (r Run) Contains(page int) bool { return page >= r.Start && page <= r.End }

// Result is everything the free stages discovered about a document.
type Result struct {
	Info Info `json:"info"`

	// Pages holds the per-page facts, one entry per page of the original.
	Pages []Page `json:"-"`

	// BySource holds each signal's own view of the language map, unreconciled.
	BySource map[Source][]Run `json:"bySource"`
	// Runs is the reconciled language map: what manualbox actually believes.
	Runs []Run `json:"runs"`

	// MedianChars is the median rune count across all pages. A scan yields ~0,
	// which is the number that selects between the free extraction path and one
	// costing a vision call per page.
	MedianChars int `json:"medianChars"`
	// HasTextLayer reports whether text extraction is viable at all.
	HasTextLayer bool `json:"hasTextLayer"`
	// PagesWithText counts pages that yielded meaningful text.
	PagesWithText int `json:"pagesWithText"`

	// ContentStart and ContentEnd bound the pages holding actual content,
	// excluding front matter and back cover.
	ContentStart int `json:"contentStart"`
	ContentEnd   int `json:"contentEnd"`

	// Unlabelled counts pages that carry text, sit in no language run, and are
	// not a contents table. It is the honest measure of how much a statistical
	// detector would add for this document.
	//
	// A small non-zero value is normal and not a fault: a cover and a colophon
	// carry text and belong to no section. On the measured 560-page fixture it is
	// 4. What matters is the magnitude — 4 says the free signals covered the
	// document, 100 says they did not.
	Unlabelled int `json:"unlabelled"`
}

// minTextChars is how many runes a page needs before it counts as carrying text.
// Page furniture alone — a folio, a language tab, a header — is a few dozen runes
// on an otherwise scanned page, so the floor has to sit above that.
const minTextChars = 50

// textLayerPageFraction is the share of pages that must carry text before
// extraction is considered viable. A median alone misjudges a document that is
// half scanned, which is common when someone photographs the pages they need.
const textLayerPageFraction = 0.5

// Analyze runs stages 0 through 2 against a document on disk.
//
// It never mutates the file and never calls anything remote, so it is safe to run
// on upload and safe to re-run: it is a pure function of the bytes, which is what
// lets the probe job be idempotent.
func Analyze(ctx context.Context, path string) (*Result, error) {
	info, err := ProbeInfo(ctx, path)
	if err != nil {
		return nil, err
	}

	res := &Result{Info: info, BySource: make(map[Source][]Run, 4)}

	// An encrypted PDF cannot be extracted from. Report what stage 0 found and
	// stop rather than failing: the original is still stored, and the user can be
	// told precisely why nothing else happened.
	if info.Encrypted {
		res.Runs = []Run{}
		return res, nil
	}

	pages, err := ExtractText(ctx, path, info.Pages)
	if err != nil {
		return nil, err
	}
	res.Pages = pages

	res.MedianChars = medianChars(pages)
	res.PagesWithText = countWithText(pages)
	if len(pages) > 0 {
		res.HasTextLayer = float64(res.PagesWithText)/float64(len(pages)) >= textLayerPageFraction
	}

	// With no text there is nothing for the free language signals to read. The
	// OCR path handles this, and it is not part of this package's job.
	if !res.HasTextLayer {
		res.Runs = []Run{}
		res.ContentStart, res.ContentEnd = firstLastWithText(pages)
		return res, nil
	}

	// The index is parsed first even though the page tag outranks it, because the
	// index supplies the vocabulary of codes that makes a loose tag reading safe.
	// The ordering here is about evidence availability; the ordering that decides
	// disagreements lives in reconcile.go.
	indexRuns := IndexRuns(pages)
	res.BySource[SourceIndex] = indexRuns

	tags := EffectiveTags(pages, IndexCodes(indexRuns))
	for i := range res.Pages {
		res.Pages[i].Tag = tags[i]
	}
	res.BySource[SourcePageTag] = TagRuns(res.Pages, tags)
	res.BySource[SourceScript] = ScriptRuns(res.Pages)

	res.Runs = Reconcile(res.Pages, res.BySource)

	res.ContentStart, res.ContentEnd = ContentRange(pages, res.Runs)
	res.Unlabelled = CountUnlabelled(pages, res.Runs)

	return res, nil
}

// Languages returns the reconciled map collapsed to one entry per language, in
// document order. This is what the pre-flight gate shows.
func (r *Result) Languages() []LanguageSummary {
	type acc struct {
		code, lang, title  string
		pages, first, last int
		runs               int
		disputed           bool
	}
	order := make([]string, 0, 8)
	seen := make(map[string]*acc, 8)

	for _, run := range r.Runs {
		// Keyed by language, not by printed label, so a section the document calls
		// UA and a signal calls uk are one language rather than two.
		key := BaseLanguage(run.Lang)
		if key == "" {
			key = run.Code
		}
		a, ok := seen[key]
		if !ok {
			a = &acc{code: run.Code, lang: run.Lang, title: run.Title, first: run.Start, last: run.End}
			seen[key] = a
			order = append(order, key)
		}
		a.pages += run.Pages()
		a.runs++
		if run.End > a.last {
			a.last = run.End
		}
		if run.Conflict {
			a.disputed = true
		}
		if a.title == "" {
			a.title = run.Title
		}
		// Keep the most specific tag seen for this language: zh-HK beats zh.
		if len(run.Lang) > len(a.lang) {
			a.lang, a.code = run.Lang, run.Code
		}
		if run.Start < a.first {
			a.first = run.Start
		}
	}

	out := make([]LanguageSummary, 0, len(order))
	for _, key := range order {
		a := seen[key]
		out = append(out, LanguageSummary{
			Code: a.code, Lang: a.lang, Title: a.title,
			Name: DisplayName(a.lang), Pages: a.pages,
			FirstPage: a.first, LastPage: a.last, Runs: a.runs,
			Disputed: a.disputed,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FirstPage < out[j].FirstPage })
	return out
}

// LanguageSummary is one language's total presence in a document.
type LanguageSummary struct {
	// Code is the label as the document printed it, e.g. "UA".
	Code string `json:"code"`
	// Lang is the BCP-47 tag, e.g. "uk".
	Lang string `json:"lang"`
	// Name is the English display name, e.g. "Ukrainian".
	Name string `json:"name"`
	// Title is the section title in that language, when the index supplied one.
	Title string `json:"title,omitempty"`
	// Pages is how many pages of the document are in this language.
	Pages int `json:"pages"`
	// FirstPage is where it starts.
	FirstPage int `json:"firstPage"`
	// LastPage is where it ends.
	LastPage int `json:"lastPage"`
	// Runs is how many separate spans this language occupies. More than one is
	// legitimate — a manual may return to a language — but it is also how a
	// wrongly split section shows itself, since page totals alone cannot.
	Runs int `json:"runs"`
	// Disputed reports that the signals disagreed somewhere in this language.
	Disputed bool `json:"disputed"`
}

// Scope is what would actually be processed for a given set of household
// languages: the answer the pre-flight gate needs.
type Scope struct {
	// Languages are the household languages present in this document.
	Languages []LanguageSummary `json:"languages"`
	// Pages is how many pages those languages occupy.
	Pages int `json:"pages"`
	// TotalPages is the document's page count, for the comparison that makes the
	// saving visible.
	TotalPages int `json:"totalPages"`
	// Chars is the extracted character count of those pages, which is the honest
	// free proxy for size. A token count needs a provider and is not invented
	// here; see docs/design/providers.md.
	Chars int `json:"chars"`
	// OtherLanguages are the languages present that the household does not read.
	// They are never discarded — the original is kept whole, so importing them
	// later is a button rather than a re-upload.
	OtherLanguages []LanguageSummary `json:"otherLanguages"`
}

// Fraction is the share of the document the scope covers, 0 to 1.
func (s Scope) Fraction() float64 {
	if s.TotalPages == 0 {
		return 0
	}
	return float64(s.Pages) / float64(s.TotalPages)
}

// ScopeFor intersects the document's languages with the household's.
func (r *Result) ScopeFor(household []string) Scope {
	scope := Scope{TotalPages: r.Info.Pages}

	// Keyed by base language, not by printed code. A summary carries one code per
	// language — the most specific tag seen wins a contest between them — while the
	// runs each carry their own. Keying on the code counted the pages of every run
	// but the characters of only those whose label happened to win: a document
	// printing CN, JA and ZH-HK reported 4 pages and 2000 characters where the same
	// pages hold 4000.
	inScope := make(map[string]bool, len(household))
	for _, summary := range r.Languages() {
		if _, ok := MatchesAny(summary.Lang, household); ok {
			scope.Languages = append(scope.Languages, summary)
			scope.Pages += summary.Pages
			inScope[BaseLanguage(summary.Lang)] = true
		} else {
			scope.OtherLanguages = append(scope.OtherLanguages, summary)
		}
	}

	byPage := make(map[int]bool, 64)
	for i := range r.Runs {
		if inScope[BaseLanguage(r.Runs[i].Lang)] {
			for p := r.Runs[i].Start; p <= r.Runs[i].End; p++ {
				byPage[p] = true
			}
		}
	}
	for i := range r.Pages {
		if byPage[r.Pages[i].No] {
			scope.Chars += r.Pages[i].Chars
		}
	}
	return scope
}

// PageLang returns the reconciled language for a page, or "" if none was
// established.
func (r *Result) PageLang(page int) (string, Source) {
	for _, run := range r.Runs {
		if run.Contains(page) {
			return run.Lang, run.Source
		}
	}
	return "", ""
}

// String renders a one-line summary for logs. It deliberately carries no
// filename or path, only shape, so it is safe in a log line.
func (r *Result) String() string {
	return fmt.Sprintf("%d pages, text=%t, median %d chars, %d languages, %d unlabelled",
		r.Info.Pages, r.HasTextLayer, r.MedianChars, len(r.Languages()), r.Unlabelled)
}

func medianChars(pages []Page) int {
	if len(pages) == 0 {
		return 0
	}
	counts := make([]int, len(pages))
	for i := range pages {
		counts[i] = pages[i].Chars
	}
	slices.Sort(counts)
	mid := len(counts) / 2
	if len(counts)%2 == 1 {
		return counts[mid]
	}
	return (counts[mid-1] + counts[mid]) / 2
}

func countWithText(pages []Page) int {
	n := 0
	for i := range pages {
		if pages[i].Chars >= minTextChars {
			n++
		}
	}
	return n
}

func firstLastWithText(pages []Page) (first, last int) {
	for i := range pages {
		if pages[i].Chars >= minTextChars {
			if first == 0 {
				first = pages[i].No
			}
			last = pages[i].No
		}
	}
	return first, last
}

// ContentRange is the span the language map covers: the first and last page
// belonging to an identified language section.
//
// Deliberately not "where the body of the document is". Those two readings pull
// in opposite directions and no evidence here separates them — six unnameable
// pages at the front are furniture to be excluded, fifty are a body the signals
// failed on, and the only difference is how many. An earlier attempt to serve
// both needed a page-count threshold tuned to one document, which is the kind of
// constant that silently misbehaves on the next one.
//
// So this answers the narrow question, which the runs answer exactly: on the
// measured fixture, 7-559 of 560, correctly excluding six front-matter pages and
// an English colophon.
//
// It must NOT be used to decide which pages count as unlabelled. That was the
// original defect — the range comes from the runs, so a page no signal could name
// fell outside it by construction and could never be counted. [CountUnlabelled]
// no longer consults it.
func ContentRange(pages []Page, runs []Run) (start, end int) {
	for i := range runs {
		// A run that fixed no boundary says nothing about where content lies.
		if runs[i].Start == 0 {
			continue
		}
		if start == 0 || runs[i].Start < start {
			start = runs[i].Start
		}
		end = max(end, runs[i].End)
	}
	if start == 0 {
		// Nothing was labelled, so the text itself is the only evidence.
		return firstLastWithText(pages)
	}
	return start, end
}

func CountUnlabelled(pages []Page, runs []Run) int {
	labelled := make(map[int]bool, len(pages))
	for i := range runs {
		for p := runs[i].Start; p <= runs[i].End; p++ {
			labelled[p] = true
		}
	}

	n := 0
	for i := range pages {
		p := &pages[i]
		switch {
		case p.Chars < minTextChars:
			// Nothing to name.
		case labelled[p.No]:
		case IsContentsPage(p):
			// A contents table is furniture, and it is the one kind this code can
			// identify structurally rather than by guessing.
		default:
			n++
		}
	}
	return n
}
