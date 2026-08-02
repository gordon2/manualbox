package ingest

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/jobs"
	"github.com/gordon2/manualbox/internal/registry"
)

// Gate is the pre-flight question: what manualbox is holding, what it would
// process, and what that would cost — asked before anything is spent.
//
// It is built entirely from stored probe results, so it survives a restart and
// costs nothing to render. Re-probing a document to answer "what is in this?"
// would defeat the purpose of having probed it. Every field below is therefore
// derived from doc_pages, doc_langs and doc_regions and from nothing else.
type Gate struct {
	DocumentID string `json:"documentId"`
	DeviceID   string `json:"deviceId"`
	Filename   string `json:"filename,omitempty"`
	Kind       string `json:"kind"`
	State      string `json:"state"`
	Probed     bool   `json:"probed"`

	Pages        int  `json:"pages"`
	Encrypted    bool `json:"encrypted"`
	HasTextLayer bool `json:"hasTextLayer"`
	MedianChars  int  `json:"medianChars"`

	// Chars is the document's named text: the characters of every language
	// something could name, and the denominator [GateLanguage.Share] is taken
	// against. Text nothing could name is excluded, because a share of it would
	// silently shrink every language by however much the signals failed to read.
	Chars int `json:"chars"`

	// Household is the configured reading languages, echoed back so the UI can
	// explain why a section is in or out of scope.
	Household []string `json:"household"`

	// InScope are the document's languages the household reads.
	InScope []GateLanguage `json:"inScope"`
	// Other are the languages present that the household does not read. They are
	// listed, never discarded: the original is kept whole, so importing one later
	// is a button rather than a re-upload.
	Other []GateLanguage `json:"other"`

	// ScopePages counts the pages carrying an in-scope language, DISTINCT pages
	// rather than a sum over languages. On a parallel-columns manual a page holds
	// several, so summing per-language page counts reports 133 pages of a 68-page
	// document. Where languages do not share pages the two agree, which is why the
	// sequential manual's 16 is unaffected.
	ScopePages    int     `json:"scopePages"`
	ScopeFraction float64 `json:"scopeFraction"`

	// ScopeChars is the characters of the in-scope languages, and
	// ScopeCharFraction its share of [Gate.Chars]. This is the honest measure of
	// how much of a document a household actually reads: on the measured
	// parallel-columns manual German is 26 of 68 pages, 38% by pages, and 20% by
	// characters — because it occupies one column of each of those pages.
	ScopeChars        int     `json:"scopeChars"`
	ScopeCharFraction float64 `json:"scopeCharFraction"`

	// Conflicts is how many runs the signals disagreed about. Surfaced rather than
	// resolved silently.
	//
	// Runs, deliberately, and not regions: the UI explains this number as the
	// document's own contents table disagreeing with its pages, which is what a
	// conflicting run means. A conflicting region is a different disagreement — a
	// column's alphabet against the page's printed tab — and the sequential manual
	// has 1 of the first and 32 of the second. Reporting 32 under the first
	// sentence would be a lie. A region's dispute reaches the user through
	// [GateLanguage.Conflict] on the languages regions named.
	Conflicts int `json:"conflicts"`
	// UnlabelledPages is how many content pages carry text that no signal could
	// name. Front matter and a back cover are excluded: they carry text and belong
	// to no section legitimately, so counting them would report a fault on every
	// document. On the measured manuals it is 2 and 0.
	UnlabelledPages int `json:"unlabelledPages"`

	// RequiresApproval reports whether the document exceeds ingest.max_pages_auto
	// and so may not be processed without the user saying yes.
	RequiresApproval bool `json:"requiresApproval"`
	MaxPagesAuto     int  `json:"maxPagesAuto"`

	// Cost is what processing the scope would cost. It is deliberately not a
	// guess: see [CostEstimate].
	Cost CostEstimate `json:"cost"`

	// Summary is a one-line human description of the situation.
	Summary string `json:"summary"`
}

// GateLanguage is one of a document's languages as the gate reports it.
//
// It embeds the stored run so that every field the run carried stays present and
// keeps its meaning — title, printed page, span, confidence — and adds what only
// the region map can say. A language the per-page signals never named has no run
// at all, and then the embedded fields carry what the regions know: the printed
// code, the language, its page span, and no title or confidence, because regions
// store neither and inventing them would be an estimate.
type GateLanguage struct {
	registry.LanguageRun

	// Chars is how much of this language the document holds, in runes.
	//
	// Characters lead and pages are context. A language occupying one of three
	// columns on 26 of 68 pages is not 26 pages of reading, and the page count on
	// its own says it is — see SharesPages.
	Chars int `json:"chars"`
	// Share is Chars as a fraction of [Gate.Chars], 0 to 1.
	Share float64 `json:"share"`
	// SharesPages reports that this language does not have its pages to itself:
	// somewhere it occupies a box on a page another language also occupies.
	//
	// This is what stops the page count misleading, and it is worked out from a
	// page carrying more than one region rather than from a region's x0 — a
	// leftmost column legitimately begins at 0, so testing x0 would call every
	// left-hand column whole-page.
	SharesPages bool `json:"sharesPages"`
}

// CostEstimate is what the scope would cost to process.
//
// When no AI provider is configured there is no honest number to show. A token
// count depends on a specific model's tokeniser, and a currency figure depends on
// a billing mode — a metered key bills money, a subscription draws down a rolling
// window, a local model costs nothing at all. Inventing a figure from a character
// count would be the kind of estimate that turns out wrong by a factor of two, so
// Available stays false and Reason says why. See docs/design/providers.md.
type CostEstimate struct {
	Available bool `json:"available"`
	// Chars is measured, free, and always present: the extracted character count
	// of the text in scope. It is a real quantity rather than a prediction, and it
	// is the same number as [Gate.ScopeChars] — repeated here because this is the
	// struct a caller asks about spending.
	Chars  int    `json:"chars"`
	Reason string `json:"reason,omitempty"`
}

// Gate assembles the pre-flight answer for a document.
//
// The language map is read from the regions where there are regions, and from the
// per-page runs otherwise. That is not a preference between two equivalent
// sources. On a parallel-columns manual the per-page map names nothing — a page
// there holds three languages and no per-page answer about it can be right — so
// summarised from runs alone that document reported "68 pages, but no language
// could be identified" while its regions held five languages and 240,622
// characters. Regions are the finer-grained record of the same reconciliation, so
// on a sequential manual the two agree and nothing it reports changes.
//
// An empty region set is not the claim that a manual has one language: a document
// probed on a host without pdftohtml has no regions and a complete per-page map,
// which is exactly what the fallback is for.
func (s *Service) Gate(ctx context.Context, documentID string) (*Gate, error) {
	document, err := s.registry.GetDocument(ctx, documentID)
	if err != nil {
		return nil, err
	}

	g := &Gate{
		DocumentID:   document.ID,
		DeviceID:     document.DeviceID,
		Filename:     document.Filename,
		Kind:         document.Kind,
		State:        document.State,
		Probed:       document.Probed(),
		Household:    s.cfg.Content.Languages,
		MaxPagesAuto: s.cfg.Ingest.MaxPagesAuto,
		InScope:      []GateLanguage{},
		Other:        []GateLanguage{},
	}

	if document.PageCount != nil {
		g.Pages = *document.PageCount
	}
	if document.Encrypted != nil {
		g.Encrypted = *document.Encrypted
	}
	if document.HasTextLayer != nil {
		g.HasTextLayer = *document.HasTextLayer
	}
	if document.MedianCharsPerPage != nil {
		g.MedianChars = *document.MedianCharsPerPage
	}
	g.RequiresApproval = g.Pages > s.cfg.Ingest.MaxPagesAuto

	if !g.Probed {
		g.Summary = "Not yet read."
		g.Cost.Reason = "the document has not been read yet"
		return g, nil
	}

	runs, err := s.registry.LanguageRuns(ctx, document.ID, doc.SourceReconciled)
	if err != nil {
		return nil, err
	}
	regions, err := s.registry.Regions(ctx, document.ID)
	if err != nil {
		return nil, err
	}
	pages, err := s.registry.Pages(ctx, document.ID)
	if err != nil {
		return nil, err
	}

	langs := collapseRuns(runs)
	g.Conflicts = countConflicts(runs)
	if len(regions) > 0 {
		langs.addRegions(regions)
	} else {
		langs.sizeFromPages(pages)
	}
	langs.finish(g, s.cfg.Content.Languages)

	first, last := contentRange(document)
	g.UnlabelledPages = unlabelledPages(regions, pages, first, last)

	g.Cost = s.costEstimate()
	g.Cost.Chars = g.ScopeChars
	g.Summary = g.summarize()
	return g, nil
}

// languageMap accumulates one entry per language while both stored sources are
// read, keyed on base language so that a document printing ZH-HK and zh is one
// language rather than two.
type languageMap struct {
	order  []string
	byLang map[string]*langEntry
}

type langEntry struct {
	lang GateLanguage
	// pages is the distinct pages this language occupies according to the regions,
	// which is how a page holding several languages is counted once.
	pages map[int]bool
	// runPages is the same count according to the runs, summed across them as it
	// always was. Kept apart from the map because the two are different
	// measurements and mixing them would double-count a page.
	runPages int
	// fromRun records that a stored run described this language. Where one did,
	// the run's own span and page count stand, so a sequential manual reports
	// exactly what it reported before regions were read here.
	fromRun bool
}

func newLanguageMap(size int) *languageMap {
	return &languageMap{order: make([]string, 0, size), byLang: make(map[string]*langEntry, size)}
}

// langKey is the language a label belongs to, falling back to the label itself so
// that a manual printing an unrecognised code still gets an entry. Storing the
// unrecognised is deliberate — see doc.KnownLanguage.
func langKey(lang, code string) string {
	if k := doc.BaseLanguage(lang); k != "" {
		return k
	}
	return code
}

func (m *languageMap) at(k string) (*langEntry, bool) {
	e, ok := m.byLang[k]
	if ok {
		return e, false
	}
	e = &langEntry{pages: make(map[int]bool, 16)}
	m.byLang[k] = e
	m.order = append(m.order, k)
	return e, true
}

// collapseRuns reduces the stored per-page runs to one entry per language,
// keeping the most specific label and the widest span. This is what the gate did
// before it read regions, unchanged, because a sequential manual must go on
// reporting exactly what it reported.
func collapseRuns(runs []registry.LanguageRun) *languageMap {
	m := newLanguageMap(len(runs))
	for i := range runs {
		r := &runs[i]
		e, fresh := m.at(langKey(r.Lang, r.Code))
		e.fromRun = true
		e.runPages += r.Pages
		if fresh {
			e.lang.LanguageRun = *r
			continue
		}
		run := &e.lang.LanguageRun
		if len(r.Lang) > len(run.Lang) {
			*run = *r
		}
		if r.Start < run.Start {
			run.Start = r.Start
		}
		if r.End > run.End {
			run.End = r.End
		}
	}
	for _, k := range m.order {
		e := m.byLang[k]
		e.lang.Pages = e.runPages
	}
	return m
}

// countConflicts counts the runs the signals disagreed about. See
// [Gate.Conflicts] for why this is counted over runs and never over regions.
func countConflicts(runs []registry.LanguageRun) int {
	n := 0
	for i := range runs {
		if runs[i].Conflict {
			n++
		}
	}
	return n
}

// addRegions folds the region map in: characters and shared pages for every
// language, and a whole entry for a language only the regions named.
func (m *languageMap) addRegions(regions []registry.Region) {
	perPage := make(map[int]int, len(regions))
	for i := range regions {
		perPage[regions[i].Page]++
	}

	for i := range regions {
		r := &regions[i]
		if r.Lang == "" && r.Code == "" {
			// Nothing named it, so it belongs to no language's total. Its characters
			// are still real, which is what UnlabelledPages reports.
			continue
		}
		e, fresh := m.at(langKey(r.Lang, r.Code))
		e.lang.Chars += r.Chars
		e.pages[r.Page] = true
		if perPage[r.Page] > 1 {
			e.lang.SharesPages = true
		}

		if e.fromRun {
			// A run already described this language and its record stands: the run
			// carries a title, a confidence and a span the regions do not have.
			continue
		}
		run := &e.lang.LanguageRun
		if fresh {
			run.Source, run.Code, run.Lang, run.Name = r.Source, r.Code, r.Lang, r.Name
			run.Note, run.Conflict = r.Note, r.Conflict
			run.Start, run.End = r.Page, r.Page
		}
		if len(r.Lang) > len(run.Lang) {
			run.Code, run.Lang, run.Name = r.Code, r.Lang, r.Name
		}
		if r.Page < run.Start {
			run.Start = r.Page
		}
		if r.Page > run.End {
			run.End = r.Page
		}
		if r.Conflict {
			run.Conflict, run.Note = true, r.Note
		}
	}

	for _, k := range m.order {
		e := m.byLang[k]
		if !e.fromRun {
			e.lang.Pages = len(e.pages)
		}
	}
}

// sizeFromPages measures each language from the per-page character counts, for a
// document that has no regions stored.
//
// The two measurements are not identical and the difference is known: whole-page
// counts come from pdftotext and a region's from positioned runs, and on the
// fixtures they disagree by 3.3% and 2.5% on a document's total. Regions are
// preferred where they exist for that reason; where they do not, a 3% difference
// beats reporting nothing.
func (m *languageMap) sizeFromPages(pages []registry.PageFact) {
	chars := make(map[int]int, len(pages))
	for i := range pages {
		chars[pages[i].Page] = pages[i].Chars
	}
	for _, k := range m.order {
		e := m.byLang[k]
		run := &e.lang.LanguageRun
		// A run that named a language but could not place it starts at 0 and covers
		// no pages, so it has no characters to find either.
		if run.Start == 0 {
			continue
		}
		for p := run.Start; p <= run.End; p++ {
			e.lang.Chars += chars[p]
		}
	}
}

// finish splits the languages into scope and the rest, and totals the document.
func (m *languageMap) finish(g *Gate, household []string) {
	scopePages := make(map[int]bool, 64)
	for _, k := range m.order {
		e := m.byLang[k]
		entry := e.lang
		g.Chars += entry.Chars

		if _, reads := doc.MatchesAny(entry.Lang, household); reads {
			g.InScope = append(g.InScope, entry)
			g.ScopeChars += entry.Chars
			if len(e.pages) == 0 {
				// No regions named this language, so the runs are the only source and
				// their page counts sum as they always did.
				g.ScopePages += entry.Pages
			}
			for p := range e.pages {
				scopePages[p] = true
			}
		} else {
			g.Other = append(g.Other, entry)
		}
	}
	g.ScopePages += len(scopePages)

	if g.Chars > 0 {
		for i := range g.InScope {
			g.InScope[i].Share = float64(g.InScope[i].Chars) / float64(g.Chars)
		}
		for i := range g.Other {
			g.Other[i].Share = float64(g.Other[i].Chars) / float64(g.Chars)
		}
		g.ScopeCharFraction = float64(g.ScopeChars) / float64(g.Chars)
	}
	if g.Pages > 0 {
		g.ScopeFraction = float64(g.ScopePages) / float64(g.Pages)
	}
}

// contentRange is the pages holding actual content, excluding front matter and
// back cover. A document probed before those were recorded has no range, and 0, 0
// means every page counts.
func contentRange(document *registry.Document) (first, last int) {
	if document.ContentStartPage != nil {
		first = *document.ContentStartPage
	}
	if document.ContentEndPage != nil {
		last = *document.ContentEndPage
	}
	return first, last
}

// unlabelledPages counts the content pages carrying text that nothing could name.
//
// It is the honest measure of how much a statistical detector would add for this
// document, and it must be read from the regions where there are regions: the
// parallel-columns manual has no per-page language at all, so counted from
// doc_pages it reports all 68 of its pages as unnamed when 2 of them are — a
// number that would send the reader looking for a detector this document does not
// need. Counted from its regions it is 2, both of them the service-address pages
// at the back that genuinely name nothing.
func unlabelledPages(regions []registry.Region, pages []registry.PageFact, first, last int) int {
	inRange := func(page int) bool {
		return (first == 0 || page >= first) && (last == 0 || page <= last)
	}

	if len(regions) > 0 {
		named := make(map[int]bool, len(regions))
		chars := make(map[int]int, len(regions))
		for i := range regions {
			r := &regions[i]
			chars[r.Page] += r.Chars
			if r.Lang != "" || r.Code != "" {
				named[r.Page] = true
			}
		}
		n := 0
		for page, c := range chars {
			if !named[page] && c >= doc.MinTextChars && inRange(page) {
				n++
			}
		}
		return n
	}

	n := 0
	for i := range pages {
		p := &pages[i]
		if p.Lang == "" && p.Chars >= doc.MinTextChars && inRange(p.Page) {
			n++
		}
	}
	return n
}

// costEstimate reports what is known about cost, and admits what is not.
func (s *Service) costEstimate() CostEstimate {
	est := CostEstimate{}
	switch {
	case !s.cfg.Providers.Translate.Enabled() && !s.cfg.Providers.Extract.Enabled():
		est.Reason = "no AI provider is configured, so nothing would be sent anywhere"
	default:
		// A provider exists but no adapter is implemented yet, so there is still no
		// tokeniser to count with. Saying so beats printing a character-derived
		// guess that a real count would contradict.
		est.Reason = "a token estimate needs the configured provider's own tokeniser, which is not wired up yet"
	}
	return est
}

// summarize renders the sentence the gate leads with.
//
// Characters lead and pages are context, which is the decision docs/design/
// regions.md records: "48 of 560 pages" was always a proxy, and on a manual
// running its languages in parallel columns it is a wrong one, because a language
// filling one column of 26 pages is not 26 pages of reading.
func (g *Gate) summarize() string {
	switch {
	case g.Encrypted:
		return "This document is password-protected, so it cannot be read. The original is stored unchanged."
	case !g.HasTextLayer:
		return fmt.Sprintf("%d pages with no text layer — this is a scan, and reading it needs OCR.", g.Pages)
	case len(g.InScope) == 0 && len(g.Other) > 0:
		return fmt.Sprintf("%d pages in %d languages, none of them yours. %s",
			g.Pages, len(g.Other)+len(g.InScope), listLanguages(g.Other, 4))
	case len(g.InScope) == 0:
		return fmt.Sprintf("%d pages, but no language could be identified.", g.Pages)
	}

	total := len(g.InScope) + len(g.Other)
	if total == 1 {
		return fmt.Sprintf("%d pages in %s.", g.Pages, g.InScope[0].Name)
	}

	yours := "Yours is 1 of them"
	if len(g.InScope) > 1 {
		yours = fmt.Sprintf("Yours are %d of them", len(g.InScope))
	}
	return fmt.Sprintf("This manual contains %d languages across %d pages. %s — %s characters, %.0f%% of the text.",
		total, g.Pages, yours, groupThousands(g.ScopeChars), 100*g.ScopeCharFraction)
}

// groupThousands renders a count with thousands separators, because 47641 in a
// sentence a person reads is worse than 47,641.
func groupThousands(n int) string {
	digits := strconv.Itoa(n)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return sign + b.String()
}

// listLanguages names up to limit languages for a human-readable sentence.
func listLanguages(langs []GateLanguage, limit int) string {
	if len(langs) == 0 {
		return ""
	}
	names := make([]string, 0, limit)
	for i := range langs {
		if i == limit {
			return fmt.Sprintf("It has %s and %d more.", joinWords(names), len(langs)-limit)
		}
		names = append(names, langs[i].Name)
	}
	return fmt.Sprintf("It has %s.", joinWords(names))
}

func joinWords(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	case 2:
		return words[0] + " and " + words[1]
	}
	out := ""
	for i, w := range words[:len(words)-1] {
		if i > 0 {
			out += ", "
		}
		out += w
	}
	return out + " and " + words[len(words)-1]
}

// Decline records that the user does not want this document processed. The
// original is kept: declining is a decision about processing, never about storage.
func (s *Service) Decline(ctx context.Context, documentID string) error {
	return s.registry.SetDocumentState(ctx, documentID, registry.StateDeclined, "")
}

// Approve is Decline's opposite: the user has seen what the gate reported and
// authorises the work. It moves the document to converting and queues the job.
//
// # What is approved is the scope the gate showed, not a scope the caller sends
//
// There is no language argument, and there must not be one. The gate rendered
// this household's languages out of configuration and told the user what
// converting them would involve; taking a different set from the request body
// would let the thing approved differ from the thing shown, which is the one
// promise the funnel makes. [Service.handleConvert] reads the same configuration
// again for the same reason.
//
// Refusals are up front rather than left to produce an empty conversion. A
// document that says "ready" with no blocks reads as an empty manual, and a user
// who authorised spending on a scan deserves to be told it needs OCR rather than
// shown nothing.
func (s *Service) Approve(ctx context.Context, documentID string) (*jobs.Job, error) {
	g, err := s.Gate(ctx, documentID)
	if err != nil {
		return nil, err
	}

	switch {
	case !g.Probed:
		return nil, fmt.Errorf("%w: this document has not been read yet, so there is "+
			"nothing to approve", registry.ErrInvalid)
	case g.Encrypted:
		return nil, fmt.Errorf("%w: this document is password-protected, so its pages "+
			"cannot be read", registry.ErrInvalid)
	case !g.HasTextLayer:
		return nil, fmt.Errorf("%w: this document has no text layer — it is a scan, and "+
			"reading it needs OCR", registry.ErrInvalid)
	case len(g.InScope) == 0:
		return nil, fmt.Errorf("%w: none of this document's languages are ones this "+
			"household reads, so there is nothing in scope to convert", registry.ErrInvalid)
	}

	// The state moves before the job is queued, so a user who has just approved
	// never sees the gate offer them the decision again while the queue picks the
	// job up. The other order would race: a worker that started before this write
	// would have its "ready" overwritten with "converting" and the document would
	// sit converting for ever.
	if err := s.registry.SetDocumentState(ctx, documentID, registry.StateConverting, ""); err != nil {
		return nil, err
	}
	job, err := s.EnqueueConvert(ctx, documentID)
	if err != nil {
		// Put it back where it was, or the document is stuck in a state no job will
		// ever leave.
		if back := s.registry.SetDocumentState(ctx, documentID, g.State, ""); back != nil {
			s.log.Error("restoring the document state after a failed enqueue failed",
				"document", documentID, "state", g.State, "error", back)
		}
		return nil, err
	}
	return job, nil
}
