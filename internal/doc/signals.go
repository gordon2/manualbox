package doc

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// minTagRunPages is how many consecutive pages must carry the same printed tag
// before it is believed.
//
// This guard is not defensive programming, it is a measured necessity. A manual's
// contents pages list every language code in the same corner the per-page tab
// occupies, so they produce spurious single-page runs — on the measured fixture,
// three of them (EN, MS and RO on pages 2, 3 and 4). Requiring two consecutive
// pages removed all three false positives and left exactly the 34 real sections.
const minTagRunPages = 2

// tagScriptAgreementFraction is the share of a tag run's pages whose script must
// be compatible with the tagged language. Below this the tag is disbelieved: a
// run tagged EL whose pages are not Greek is not Greek, whatever the tab says.
const tagScriptAgreementFraction = 0.5

// TagRuns builds language runs from the code each page prints on itself.
//
// This is the cheapest accurate signal available, and on a manual that prints
// tags it is also the most accurate: measured at 553 of 553 content pages, it
// labelled correctly two sections that statistical detection cannot — Uzbek,
// which lingua-go does not support at all, and Latin-script Serbian, which reads
// as Croatian. See docs/design/language-detection.md.
//
// Not every manual prints tags. An empty result is a normal outcome, not a
// failure.
// EffectiveTags decides each page's printed language tag, given the set of codes
// the document's own contents table declares.
//
// The conservative reading — a code among the first lines of the page — is
// trusted outright. Beyond that, a candidate found anywhere on the page is
// accepted only if the printed index also lists that code, which is what makes
// searching the whole page safe: NO, IT, IS and BE are valid language codes and
// ordinary words, but a manual that does not contain a Norwegian section does not
// list NO in its contents.
//
// This exists because right-to-left pages put their tab well after the heading in
// reading order. Without it the Hebrew and Arabic sections of the measured
// fixture went partly unlabelled, and the gaps were filled by an erroneous claim
// from the index.
//
// It returns a copy of the tags rather than mutating pages, so the raw extraction
// stays separable from the interpretation of it.
func EffectiveTags(pages []Page, knownCodes map[string]bool) []string {
	tags := make([]string, len(pages))
	for i := range pages {
		p := &pages[i]
		// A contents table is not a page of any language section, whatever code it
		// happens to print first. The run-length guard alone does not catch this:
		// it only helps when the contents page is separated from the section it
		// lists, and a contents page sitting immediately before the first section
		// is otherwise absorbed straight into it.
		if IsContentsPage(p) {
			continue
		}
		if p.Tag != "" {
			tags[i] = p.Tag
			continue
		}
		for _, c := range p.TagCandidates {
			// The vocabulary check is what makes a single letter usable at all:
			// "D" is German on a manual whose contents table lists D, and a list
			// marker everywhere else.
			if knownCodes[c] {
				tags[i] = c
				break
			}
		}
	}
	return tags
}

// IsContentsPage reports whether a page is a printed contents table rather than a
// page of content.
//
// The test is structural: several code/title/page triples in reading order. Three
// is enough to tell a real index from a page that merely mentions a language code
// once.
func IsContentsPage(p *Page) bool {
	return len(parseIndexPage(p.Text)) >= minIndexEntriesPerPage
}

// IndexCodes returns the set of language codes the printed index declares. It is
// the vocabulary against which looser tag candidates are checked.
func IndexCodes(indexRuns []Run) map[string]bool {
	codes := make(map[string]bool, len(indexRuns))
	for _, r := range indexRuns {
		codes[strings.ToUpper(r.Code)] = true
	}
	return codes
}

// TagRuns builds language runs from per-page printed tags. tags is parallel to
// pages and normally comes from [EffectiveTags].
func TagRuns(pages []Page, tags []string) []Run {
	// Work on a copy carrying the effective tags, so grouping sees the narrowed
	// interpretation rather than the raw first-lines reading.
	if len(tags) == len(pages) {
		tagged := make([]Page, len(pages))
		copy(tagged, pages)
		for i := range tagged {
			tagged[i].Tag = tags[i]
		}
		pages = tagged
	}

	var runs []Run
	for _, group := range groupBy(pages, func(p *Page) string { return p.Tag }) {
		if group.key == "" || group.pages() < minTagRunPages {
			continue
		}
		lang, ok := NormalizeCode(group.key)
		if !ok {
			// A tag that is not a language code at all: keep the raw code so the
			// run is still reportable, but claim no language for it.
			runs = append(runs, Run{
				Source: SourcePageTag, Code: group.key, Start: group.start, End: group.end,
				Confidence: 0.3,
				Note:       fmt.Sprintf("printed tag %q is not a recognised language code", group.key),
			})
			continue
		}

		agree, judged := 0, 0
		for i := group.startIdx; i <= group.endIdx; i++ {
			if pages[i].Script == "" {
				continue
			}
			judged++
			// Agreement is checked in both directions, as reconciliation checks it.
			// ScriptAllows alone treats a Latin page as corroborating anything, so two
			// pages tagged JA whose CJK glyphs failed to extract — leaving only Latin
			// furniture — scored confidence 1.0 "corroborated by script" while
			// Reconcile discarded the run outright. The stored row then asserted
			// maximum confidence for a section that ended up unlabelled.
			if ScriptCompatible(pages[i].Script, lang) {
				agree++
			}
		}

		run := Run{
			Source: SourcePageTag, Code: group.key, Lang: lang,
			Start: group.start, End: group.end, Confidence: 0.9,
			Note: "language printed on every page of the run",
		}
		switch {
		case judged == 0:
			// No script evidence either way; the tag stands on its own.
		case float64(agree)/float64(judged) < tagScriptAgreementFraction:
			run.Confidence = 0.2
			run.Note = fmt.Sprintf("printed tag %s disagrees with the page script on %d of %d pages",
				group.key, judged-agree, judged)
		case agree == judged:
			// Script corroborates the tag: the strongest evidence available here.
			run.Confidence = 1.0
			run.Note = "language printed on every page, corroborated by script"
		}
		runs = append(runs, run)
	}
	return runs
}

// ScriptRuns builds runs from Unicode script analysis.
//
// A script names a language only when just one language uses it in practice —
// Greek, Hebrew, Thai, Japanese and so on. For Latin, and for Cyrillic with its
// several candidates, the run carries the script as its code and no language,
// because claiming one would invent information this signal does not have.
func ScriptRuns(pages []Page) []Run {
	var runs []Run
	for _, group := range groupBy(pages, func(p *Page) string { return p.Script }) {
		if group.key == "" {
			continue
		}
		run := Run{
			Source: SourceScript, Code: group.key,
			Start: group.start, End: group.end, Confidence: 0.2,
			Note: fmt.Sprintf("%s script", group.key),
		}
		if candidates := ScriptLanguages(group.key); len(candidates) == 1 {
			if lang, ok := NormalizeCode(candidates[0]); ok {
				run.Lang = lang
				// Code carries the language, not the script name. A reconciled run
				// that a script signal won must still be identified by the language
				// it names, or a Hebrew section ends up labelled "Hebrew" and no
				// longer matches the "HE" the document itself uses.
				run.Code = strings.ToUpper(lang)
				run.Confidence = 0.7
				run.Note = fmt.Sprintf("%s script is used by only one language here", group.key)
			}
		}
		runs = append(runs, run)
	}
	return runs
}

// minIndexEntriesPerPage is how many code/title/page triples a page needs before
// it is treated as a contents page. Three is enough to distinguish a real index
// from a page that merely happens to mention a language code.
const minIndexEntriesPerPage = 3

// indexLookaheadLines bounds how far after a code line the parser will look for
// that entry's page number. Titles run to one or two lines in practice.
const indexLookaheadLines = 4

// IndexRuns parses the manual's own printed contents table.
//
// The index is the only signal that supplies section *titles*, and the only one
// that can name a language no detector supports. What it cannot be trusted about
// is page numbers: on the measured fixture, 10 of 34 sections claim a printed page
// 1-2 away from the folio actually printed, because two sections run 17 pages
// rather than 16.
//
// So a claimed page is resolved through the folios actually printed on the pages,
// not through a global offset. That converts the claim into a PDF page faithfully,
// including its error — which is the point. The claim stays a hypothesis for
// reconciliation to test, rather than being silently corrected or silently
// trusted.
func IndexRuns(pages []Page) []Run {
	type entry struct {
		code, title string
		printed     int
	}
	var entries []entry
	seen := make(map[string]bool, 32)
	isContentsPage := make(map[int]bool, 4)

	for i := range pages {
		p := &pages[i]
		if !IsContentsPage(p) {
			continue
		}
		isContentsPage[p.No] = true
		for _, e := range parseIndexPage(p.Text) {
			if seen[e.code] {
				continue
			}
			seen[e.code] = true
			entries = append(entries, entry{e.code, e.title, e.printed})
		}
	}
	if len(entries) == 0 {
		return nil
	}

	// A contents page's own trailing number is an index entry's page reference,
	// not a folio. Including it maps a claimed page onto the contents page itself:
	// on the measured fixture, page 2 ends with "194", so Arabic's claimed start
	// of 194 resolved to page 2 and produced a one-page Arabic section at the front
	// of the document. Contents pages therefore contribute no folios.
	folioToPage := make(map[int]int, len(pages))
	for i := range pages {
		p := &pages[i]
		if p.Folio == nil || isContentsPage[p.No] {
			continue
		}
		if _, dup := folioToPage[*p.Folio]; !dup {
			folioToPage[*p.Folio] = p.No
		}
	}

	// Stable, so that two entries claiming the same printed page stay in the order
	// the contents table printed them. An unstable sort ordered them arbitrarily,
	// and reconciliation keeps whichever claim it sees last — which made the
	// language of those pages depend on the sort's internals rather than on the
	// document.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].printed < entries[j].printed })

	// Every claim is resolved before any boundary is derived from it. A rejected
	// claim is not evidence of where the previous section ends: on an index listing
	// AR at folio 5, CZ at 7 and EN at 9, where the CZ claim lands inside the Arabic
	// section and is vetoed by script, ending Arabic at the page before a claim
	// nobody believes cut the section in half.
	runs := make([]Run, 0, len(entries))
	// placed indexes the runs that fixed a start, in the same order.
	placed := make([]int, 0, len(entries))

	for _, e := range entries {
		printed := e.printed
		run := Run{
			Source: SourceIndex, Code: e.code, Title: e.title,
			PrintedPage: &printed, Confidence: 0.6,
			Note: fmt.Sprintf("listed in the printed index at page %d", printed),
		}
		if lang, ok := NormalizeCode(e.code); ok {
			run.Lang = lang
		}

		start, resolved := folioToPage[e.printed]
		if !resolved {
			// The claimed page has no matching folio anywhere in the document,
			// which is itself evidence the claim is wrong. Keep the entry for its
			// label and title; it contributes no boundary.
			run.Confidence = 0.3
			run.Note = fmt.Sprintf("printed index claims page %d, which no page in this document prints", printed)
			runs = append(runs, run)
			continue
		}

		// Script vetoes a claim that lands on the wrong alphabet. The measured
		// fixture's index lists Czech at printed page 207, which is inside the
		// Arabic section — a typo the manual actually ships. Resolving it
		// faithfully produces a Czech claim over Arabic pages, and without this
		// check that claim fills gaps in stronger signals with a language that is
		// nowhere near those pages. A cheap signal correcting a more informative
		// one is the whole point of keeping several.
		if run.Lang != "" && !ScriptAllows(scriptAt(pages, start), BaseLanguage(run.Lang)) {
			run.Confidence = 0.1
			// Deliberately not called a typo. Both causes look identical here and
			// the distinction matters to a reader: the claim may be wrong outright
			// (a real manual lists Czech at a page deep inside the Arabic section)
			// or merely off by a page, landing on the tail of the previous
			// language. Either way it cannot be a boundary, and saying only what
			// was observed avoids asserting which.
			run.Note = fmt.Sprintf(
				"printed index claims %s starts at page %d, but that page is %s script, so it fixes no boundary",
				e.code, printed, scriptAt(pages, start))
			runs = append(runs, run)
			continue
		}
		run.Start = start
		runs = append(runs, run)
		placed = append(placed, len(runs)-1)
	}

	// A section ends where the next accepted claim begins, and runs to the end of
	// the document when there is none. A later claim that resolves to an earlier
	// page is the index contradicting itself rather than a boundary, so only a
	// start beyond this one closes the run.
	for i, idx := range placed {
		runs[idx].End = lastPageNo(pages)
		for _, next := range placed[i+1:] {
			if runs[next].Start > runs[idx].Start {
				runs[idx].End = runs[next].Start - 1
				break
			}
		}
	}
	return runs
}

// parseIndexPage extracts code/title/page triples from a contents page.
//
// The shape a printed index takes is a language code, a title in that language,
// then the page it starts on. pdftotext emits them in that reading order.
func parseIndexPage(text string) []struct {
	code, title string
	printed     int
} {
	type triple struct {
		code, title string
		printed     int
	}
	var out []triple

	// Formatting characters are stripped for the same reason as in pageTag: a
	// contents table that lists right-to-left languages wraps their codes in bidi
	// embedding marks.
	lines := make([]string, 0, 64)
	for line := range strings.Lines(text) {
		if line = strings.TrimSpace(stripFormatting(line)); line != "" {
			lines = append(lines, line)
		}
	}

	for i, line := range lines {
		if len([]rune(line)) > maxRunesInCodeLine || !looksLikeLanguageCode(line) {
			continue
		}
		// Walk forward for this entry's page number, collecting the title on the
		// way. Stop at the next code line: a missing page number means a
		// malformed entry, not a licence to consume the following one.
		var title []string
		for j := i + 1; j < len(lines) && j <= i+indexLookaheadLines; j++ {
			next := lines[j]
			if looksLikeIndexLabel(next) {
				break
			}
			if n, err := strconv.Atoi(next); err == nil && n > 0 {
				out = append(out, triple{
					code:    strings.ToUpper(line),
					title:   strings.Join(title, " "),
					printed: n,
				})
				break
			}
			title = append(title, next)
		}
	}

	result := make([]struct {
		code, title string
		printed     int
	}, len(out))
	for i, t := range out {
		result[i] = struct {
			code, title string
			printed     int
		}{t.code, t.title, t.printed}
	}
	return result
}

// maxRunesInIndexLabel bounds how long a line can be and still be the next
// contents entry's label. Four covers the three-letter codes manufacturers print
// and leaves ZH-HK to [looksLikeLanguageCode].
const maxRunesInIndexLabel = 4

// looksLikeIndexLabel reports whether a contents-table line is the next entry's
// language label rather than part of this entry's title.
//
// [looksLikeLanguageCode] is too narrow for this: it matches only XX and XX-XX,
// while real manufacturers print POR, SPA, CHI and SRB. Such a line was taken for
// title text and the walk continued into the *following* entry's page number, so
// one entry claimed its neighbour's start page and carried its neighbour's title.
//
// Case is what keeps this from eating titles: a contents table prints its codes in
// capitals, and requiring all-uppercase ASCII leaves ordinary short title lines
// alone.
func looksLikeIndexLabel(s string) bool {
	if looksLikeLanguageCode(s) {
		return true
	}
	r := []rune(s)
	if len(r) < 2 || len(r) > maxRunesInIndexLabel {
		return false
	}
	for _, c := range r {
		if c > unicode.MaxASCII || !unicode.IsUpper(c) {
			return false
		}
	}
	return true
}

// group is a maximal span of consecutive pages sharing a key.
type group struct {
	key              string
	start, end       int
	startIdx, endIdx int
}

func (g group) pages() int { return g.end - g.start + 1 }

// groupBy splits pages into maximal runs of consecutive page numbers sharing a
// key. Consecutiveness matters: a document whose page 10 and page 40 share a tag
// has two runs, not one 31-page run.
func groupBy(pages []Page, key func(*Page) string) []group {
	var groups []group
	for i := range pages {
		p := &pages[i]
		k := key(p)
		if n := len(groups); n > 0 && groups[n-1].key == k && groups[n-1].end == p.No-1 {
			groups[n-1].end = p.No
			groups[n-1].endIdx = i
			continue
		}
		groups = append(groups, group{key: k, start: p.No, end: p.No, startIdx: i, endIdx: i})
	}
	return groups
}

func lastPageNo(pages []Page) int {
	if len(pages) == 0 {
		return 0
	}
	return pages[len(pages)-1].No
}

// scriptAt returns the dominant script of a page by its 1-based number.
func scriptAt(pages []Page, no int) string {
	for i := range pages {
		if pages[i].No == no {
			return pages[i].Script
		}
	}
	return ""
}
