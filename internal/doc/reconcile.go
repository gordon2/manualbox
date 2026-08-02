package doc

import (
	"fmt"
	"strings"
)

// signalPriority is the order in which signals are believed when they disagree
// about a page, most trusted first.
//
// The ordering is evidential, not arbitrary:
//
//   - The printed page tag is the document asserting its own language, per page.
//     It gives a label and a boundary simultaneously, which no other signal does.
//   - The index gives a real language label, including for languages no detector
//     supports, but its page claims are known to be 1-2 pages off.
//   - Script is certain about what it can see and silent about the rest; it
//     resolves the non-Latin sections and cannot separate the Latin ones.
//   - A statistical detector would sit last, as the fallback for pages the free
//     signals could not name. None is wired up; see
//     docs/design/language-detection.md for why that decision is still open.
var signalPriority = []Source{SourcePageTag, SourceIndex, SourceScript, SourceDetector}

// Reconcile combines the signals into the language map manualbox believes.
//
// The rule from docs/design/ingest.md, generalised past two signals: prefer the
// cheapest signal present, corroborate with the next, and where they conflict
// record the conflict rather than resolving it silently. A page nobody could name
// stays unnamed — that is a reportable state, not something to guess at.
func Reconcile(pages []Page, bySource map[Source][]Run) []Run {
	if len(pages) == 0 {
		return []Run{}
	}

	// Index each signal's runs by page for O(1) lookup during resolution.
	type claim struct {
		code, lang string
		confidence float64
		note       string
	}
	claims := make(map[Source]map[int]claim, len(bySource))
	for source, runs := range bySource {
		byPage := make(map[int]claim, len(pages))
		for _, r := range runs {
			// A run that named no language contributes nothing to resolution,
			// though it is still stored and reportable on its own terms.
			if r.Lang == "" {
				continue
			}
			for p := r.Start; p <= r.End; p++ {
				byPage[p] = claim{r.Code, r.Lang, r.Confidence, r.Note}
			}
		}
		claims[source] = byPage
	}

	// Resolve each page independently, then group. Resolving per page and grouping
	// afterwards is what lets a boundary fall wherever the evidence puts it,
	// rather than inheriting a boundary from whichever signal was consulted first.
	type resolution struct {
		code, lang string
		source     Source
		confidence float64
		note       string
		disputedBy []string
	}
	resolved := make(map[int]resolution, len(pages))

	for i := range pages {
		p := &pages[i]
		if p.Chars < MinTextChars {
			continue
		}
		var winner *resolution
		for _, source := range signalPriority {
			c, ok := claims[source][p.No]
			if !ok {
				continue
			}
			// A claim that contradicts the page's own script is not evidence. This
			// is what stops the printed index's final entry — which claims every
			// page to the end of the document — from labelling a Latin-script back
			// cover as Japanese.
			if !ScriptCompatible(p.Script, c.lang) {
				continue
			}
			if winner == nil {
				winner = &resolution{
					code: c.code, lang: c.lang, source: source,
					confidence: c.confidence, note: c.note,
				}
				continue
			}
			// A lower-priority signal that names a different language is a
			// disagreement worth recording, even though it does not win.
			if !SameLanguage(winner.lang, c.lang) {
				winner.disputedBy = append(winner.disputedBy,
					fmt.Sprintf("%s says %s", source, c.code))
			}
		}
		if winner != nil {
			resolved[p.No] = *winner
		}
	}

	// Group consecutive pages that resolved to the same language.
	titles := indexTitles(bySource[SourceIndex])
	var runs []Run
	// lastSource records which signal resolved the most recent page of each run,
	// which is what decides whether a differently-specific label continues it.
	var lastSource []Source
	// disputes records, per run index, which pages were disputed and by what.
	disputes := make(map[int]map[int][]string)

	for i := range pages {
		p := &pages[i]
		r, ok := resolved[p.No]
		if !ok {
			continue
		}
		n := len(runs)
		if n > 0 && runs[n-1].End == p.No-1 &&
			continuesRun(runs[n-1].Lang, lastSource[n-1], r.lang, r.source) {
			runs[n-1].End = p.No
			if len(r.lang) > len(runs[n-1].Lang) {
				runs[n-1].Lang, runs[n-1].Code = r.lang, r.code
			}
			lastSource[n-1] = r.source
		} else {
			runs = append(runs, Run{
				Source: SourceReconciled, Code: r.code, Lang: r.lang,
				Start: p.No, End: p.No, Confidence: r.confidence,
				Title: titles[r.code],
				Note:  fmt.Sprintf("%s: %s", r.source, r.note),
			})
			lastSource = append(lastSource, r.source)
		}
		if len(r.disputedBy) > 0 {
			idx := len(runs) - 1
			if disputes[idx] == nil {
				disputes[idx] = make(map[int][]string, 2)
			}
			disputes[idx][p.No] = r.disputedBy
		}
	}

	// Flag disputes against the runs as the evidence produced them, BEFORE any
	// bridging. Bridging extends a run past a page that used to be its edge, and
	// an edge dispute — the ordinary one-page-off index claim — would then look
	// interior. Identical evidence must not produce a different verdict because a
	// photograph happened to sit inside the section.
	flagDisputes(runs, disputes)
	runs = bridgeLowTextGaps(runs, pages)
	annotateIndexDisagreements(runs, bySource[SourceIndex])

	if runs == nil {
		return []Run{}
	}
	return runs
}

// continuesRun decides whether a page extends the current run or starts a new one.
//
// Identical languages always continue. Two labels sharing a base language —
// "zh" and "zh-HK", "pt" and "pt-BR" — are the interesting case, and the answer
// depends on *which signal* was less specific:
//
//   - The script signal cannot express a region at all; the most it can say for
//     Han glyphs is "zh". When it fills a gap inside a section the page tag called
//     ZH-HK, that is one section and the specific label is right for all of it.
//   - A page tag or a printed index naming CN on one page and ZH-HK on another is
//     the document distinguishing two sections. Merging them loses a real
//     boundary and then relabels half the pages with a variant their own printed
//     tag contradicts — so a household reading one variant is scoped onto both
//     and pays to translate the wrong one.
func continuesRun(prevLang string, prevSource Source, curLang string, curSource Source) bool {
	if prevLang == curLang {
		return true
	}
	if !SameLanguage(prevLang, curLang) {
		return false
	}
	// Same language, different specificity: allowed only when the vaguer of the
	// two came from a signal incapable of being more precise.
	vaguerSource := curSource
	if len(curLang) > len(prevLang) {
		vaguerSource = prevSource
	}
	return vaguerSource == SourceScript
}

// bridgeLowTextGaps joins runs of the same language separated only by pages with
// too little text to classify.
//
// A page carrying nothing but a photograph sits between two pages of Japanese; it
// is Japanese, and leaving it out splits one section into two. On the measured
// fixture exactly this happened, and because the two fragments' page counts still
// summed correctly it was invisible to a test that checked only totals.
func bridgeLowTextGaps(runs []Run, pages []Page) []Run {
	if len(runs) < 2 {
		return runs
	}
	byNo := make(map[int]Page, len(pages))
	for i := range pages {
		byNo[pages[i].No] = pages[i]
	}

	out := []Run{runs[0]}
	for i := 1; i < len(runs); i++ {
		prev := &out[len(out)-1]
		cur := runs[i]

		// Two conditions, both learned the hard way.
		//
		// There must be an actual gap: `cur.Start > prev.End` is satisfied by
		// merely adjacent runs, so bridging silently re-joined two sections that
		// grouping had just decided to keep apart.
		//
		// And the languages must match exactly, not merely share a base. Grouping
		// separates a Portuguese section from a Brazilian Portuguese one on
		// purpose; bridging must not put them back together.
		bridgeable := prev.Lang == cur.Lang && cur.Start > prev.End+1
		for p := prev.End + 1; bridgeable && p < cur.Start; p++ {
			page, known := byNo[p]
			if !known || page.Chars >= MinTextChars {
				bridgeable = false
			}
		}
		if bridgeable {
			prev.End = cur.End
			if len(cur.Lang) > len(prev.Lang) {
				prev.Lang, prev.Code = cur.Lang, cur.Code
			}
			// A conflict already established on either fragment survives the merge.
			if cur.Conflict && !prev.Conflict {
				prev.Conflict, prev.Note = true, cur.Note
			}
			continue
		}
		out = append(out, cur)
	}
	return out
}

// conflictNote renders a note that keeps both the winning evidence and what
// disagreed with it, because a conflict the user cannot see is a conflict that
// was resolved silently.
func conflictNote(base string, disputedBy []string) string {
	seen := make(map[string]bool, len(disputedBy))
	unique := make([]string, 0, len(disputedBy))
	for _, d := range disputedBy {
		if seen[d] {
			continue
		}
		seen[d] = true
		unique = append(unique, d)
	}
	return fmt.Sprintf("%s; disagreed with by %s", base, strings.Join(unique, ", "))
}

// indexTitles maps a language code to the section title the printed index gave
// it. Titles are the one thing only the index can supply.
func indexTitles(indexRuns []Run) map[string]string {
	titles := make(map[string]string, len(indexRuns))
	for i := range indexRuns {
		if indexRuns[i].Title != "" {
			titles[indexRuns[i].Code] = indexRuns[i].Title
		}
	}
	return titles
}

// flagDisputes marks a run as conflicting when the disagreement is about the run
// itself rather than about where its edge falls.
//
// Two cases count. A disagreement strictly inside a run is a real conflict: the
// signals name different languages for a page the run claims. A disagreement
// covering *every* page of a run is the same thing — and it was previously
// missed entirely, because a run of one or two pages has no strict interior, so
// a flat contradiction about a whole short section was silently dropped. Short
// sections exist in real manuals.
//
// What is deliberately not flagged is a single dispute at an edge of a longer
// run. A printed index whose claimed start is one page off disagrees about
// exactly one page — the last of the preceding section — and that is a boundary
// claim being slightly wrong, reported once per section by
// annotateIndexDisagreements. Flagging it again per run turned 8 of 35 runs on
// the measured fixture into "conflicts" and made the flag worthless.
func flagDisputes(runs []Run, disputes map[int]map[int][]string) {
	for i := range runs {
		byPage := disputes[i]
		if len(byPage) == 0 {
			continue
		}

		var reasons []string
		for page, by := range byPage {
			if page > runs[i].Start && page < runs[i].End {
				reasons = append(reasons, by...)
			}
		}
		// Every page disputed: the signals disagree about the whole section, at
		// any length.
		if len(reasons) == 0 && len(byPage) == runs[i].Pages() {
			for _, by := range byPage {
				reasons = append(reasons, by...)
			}
		}
		if len(reasons) == 0 {
			continue
		}
		runs[i].Conflict = true
		runs[i].Note = conflictNote(runs[i].Note, reasons)
	}
}

// indexStartTolerance is how far a printed index's claimed start may sit from the
// reconciled start before it is reported as a disagreement.
//
// Zero would be noise: the claim resolves through a printed folio, and a section
// beginning on a spread's verso legitimately shifts a page. Two pages is what the
// measured drift reached on a document whose index was otherwise correct, so
// beyond that the claim is wrong about something real.
const indexStartTolerance = 2

// annotateIndexDisagreements records where the printed index's claimed start
// disagrees with where the section actually begins.
//
// This is deliberately a note rather than a correction. The index being wrong is
// information about the document — it is exactly the failure the design says must
// be surfaced instead of resolved — and it is also how a user recognises that a
// manual's contents table cannot be trusted for navigation.
func annotateIndexDisagreements(runs, indexRuns []Run) {
	claimed := make(map[string]Run, len(indexRuns))
	for _, r := range indexRuns {
		if r.Start > 0 {
			claimed[r.Code] = r
		}
	}

	firstSeen := make(map[string]int, len(runs))
	for i, run := range runs {
		if _, ok := firstSeen[run.Code]; !ok {
			firstSeen[run.Code] = i
		}
	}

	for code, i := range firstSeen {
		claim, ok := claimed[code]
		if !ok {
			continue
		}
		delta := runs[i].Start - claim.Start
		if delta < 0 {
			delta = -delta
		}
		if delta <= indexStartTolerance {
			continue
		}
		runs[i].Conflict = true
		// Quote the page the index actually printed. claim.Start is the
		// folio-resolved PDF page, which is a number the index never showed and
		// which the reader cannot check against their copy.
		printed := claim.Start
		if claim.PrintedPage != nil {
			printed = *claim.PrintedPage
		}
		runs[i].Note = fmt.Sprintf(
			"%s; the printed index lists %s as starting on page %d, but it begins on PDF page %d",
			runs[i].Note, code, printed, runs[i].Start)
	}
}
