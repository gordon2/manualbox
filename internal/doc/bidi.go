package doc

import (
	"strings"

	"golang.org/x/text/unicode/bidi"
)

// Right-to-left text arrives from `pdftohtml -xml` in VISUAL order, and this file
// puts it back into the order the page is written in.
//
// It is a defect in the pipeline rather than a limitation of it, and it bit twice:
// a Hebrew section read backwards on screen, and — because the same text is what
// the search index holds — a Hebrew word was findable only if it was typed
// backwards. One fix, at the one place a line's order is decided, repairs both.
//
// # What the tool actually returns
//
// Two separate reversals, and missing either one leaves the line wrong. Page 185 of
// the sequential manual, its Hebrew safety section, is the worked example.
//
// The runes inside a run are reversed: the run reads `שומיש תולבגה` where the page
// prints `הגבלות שימוש`, "usage restrictions".
//
// And the RUNS THEMSELVES are in visual order along the line. That page's second
// paragraph is three runs at x=89, x=643 and x=653 — a chunk of Hebrew, the digit 8,
// and another chunk of Hebrew — and the line begins at the RIGHT, so the run at 653
// is the first thing read and the run at 89 the last. Joining them left to right,
// which is what every other line in these documents wants, interleaves the sentence.
//
// # Why not simply reverse the string
//
// Because a right-to-left line carries left-to-right islands, and they are not
// reversed on the page: "8" is printed "8" inside Hebrew prose, not "8" backwards,
// and a Latin product name reads forwards. Reversing the whole line would turn `8`
// into `8` harmlessly and `MopExtend` into `dnetxEpoM` — which is why the reversal
// is followed by putting each left-to-right island back the way it was. That is the
// standard visual-to-logical reading, and it is checked rather than assumed: see
// [visualToLogical]'s own note for what it reproduces.
//
// # The reference this was measured against
//
// `pdftotext` reads the same bytes with different code and gets the logical order
// right, wrapping it in the bidi controls U+202B and U+202C. So every line here has
// a free second opinion, which is the same stance internal/verify takes, and the
// check that measures this defect — 32 pages and 8,120 words reported reversed on
// the sequential manual — is the one that says whether the fix worked. It is also
// what found the two defects in the first version of this file: see
// [lineIsRightToLeft] and [leftToRightIsland].
//
// # What this does NOT fix, measured
//
// **Arabic is unshaped**, in both tools. It arrives in isolated letter forms rather
// than the presentation forms the page prints, and `pdftotext` does no better —
// `السالمة` where the page prints `السلامة`, in both readings. That is a property of
// how the font maps its glyphs and is not an ordering question, so putting the order
// right is all this can do for Arabic. It is still worth doing: the words are now in
// the order they are read in, so search finds them.
//
// **Brackets come out right, and that is measured rather than assumed.** A bracket is
// bidi class ON, so it is not an island and reverses with the text around it — which
// is correct here, because poppler emits the glyph the page DRAWS. On the sequential
// manual's page 190 a parenthesised aside arrives as `)םייפיצפס םירוזאב קר ןימז(`,
// closing glyph first, and reversing puts the opening one back at the start. Five
// such runs on pages 189 and 190; none needs mirroring.
//
// **The language signals were never affected and are not changed here.** They count
// characters, and a reversed string has the same characters; the printed page tag
// already strips bidi controls for the reason [stripFormatting] gives. What was
// wrong was the readable text, and therefore search and, later, translation.

// IsRightToLeftLanguage reports whether a language is written right to left.
//
// The scripts these documents actually contain, and no more: a list is honest about
// what has been seen, where a table of every RTL script would be a claim about
// documents this project has never read. Hebrew and Arabic are the sequential
// manual's; the others are here because they are the rest of the right-to-left world
// a household might plausibly configure, and because getting one wrong costs a
// section rather than a line.
func IsRightToLeftLanguage(lang string) bool {
	switch BaseLanguage(lang) {
	case "he", "ar", "fa", "ur", "ps", "sd", "ug", "yi", "dv", "ku", "arc":
		return true
	}
	return false
}

// lineIsRightToLeft reports whether a line's base direction is right to left.
//
// The REGION's language decides it, and counting the line's own characters is the
// fallback. That order is the correction to what shipped first, and six lines of the
// sequential manual are why.
//
// The first version counted strong characters and took the majority — which cannot
// be the Unicode algorithm's P2 rule, since P2 wants the first character in LOGICAL
// order and logical order is precisely what has been lost. Its header claimed the
// majority "agrees with P2 on every line of both documents". That was wrong, and it
// was wrong in the way that matters: a Hebrew line carrying a URL has more Latin than
// Hebrew, so it was read left to right and never repaired. Page 188 prints
//
//	https://global.dreametech.com/pages/user-manuals-and-faqs :האבה תבותכב ןייעל שי
//
// — about 55 Latin letters against 30 Hebrew. The same shape appears on 204 (its
// Arabic twin), `Dreamehome תייצקלפא` on 191, `Dreamehome App قيبطت ليزنت` on 207,
// and a Wi-Fi label on 189 and 205. Those six lines were every word the verifier
// still reported as reversed, and the one Hebrew query that still had to be typed
// backwards to find anything.
//
// The region's language is the right authority because it is a document-wide answer
// to a question one line cannot settle, and because the whole probe exists to
// establish it. A line only ever gets this treatment inside a region a language was
// named for.
//
// A line with NO right-to-left character is left alone whatever its region says, and
// that guard is load-bearing rather than defensive: a pure-Latin line is entirely one
// island, so reversing its runes is a no-op, but reversing the ORDER OF ITS RUNS is
// not — a two-run Latin line in a Hebrew region would come out backwards. That is the
// case the mutation testing on this file found by accident, when a one-run control
// line read identically in both directions and proved nothing.
func lineIsRightToLeft(runs []TextRun, rtlRegion bool) bool {
	var rtl, ltr int
	for i := range runs {
		for _, r := range runs[i].Text {
			switch p, _ := bidi.LookupRune(r); p.Class() {
			case bidi.R, bidi.AL:
				rtl++
			case bidi.L:
				ltr++
			}
		}
	}
	if rtl == 0 {
		return false
	}
	if rtlRegion {
		return true
	}
	return rtl >= ltr
}

// visualToLogical turns one visually-ordered right-to-left string into the order it
// is written in: reverse it, then put every left-to-right island back.
//
// An island is a stretch of characters that runs left to right inside
// right-to-left text — Latin letters, European and Arabic-Indic digits, and the
// separators that belong to a number — plus a space between two of them, so that
// `Dreame L40 Ultra` survives as one island instead of three.
//
// Checked against `pdftotext` on the sequential manual's Hebrew page 185 and Arabic
// page 201: every line matches the reference's reading, including the `8` in
// `אין לתת לילדים מתחת לגיל 8`, where a naive whole-string reversal is
// indistinguishable on one digit and wrong on two.
func visualToLogical(s string) string {
	rs := []rune(s)
	for i, j := 0, len(rs)-1; i < j; i, j = i+1, j-1 {
		rs[i], rs[j] = rs[j], rs[i]
	}
	for i := 0; i < len(rs); {
		if !leftToRightIsland(rs, i) {
			i++
			continue
		}
		j := i
		for j < len(rs) && leftToRightIsland(rs, j) {
			j++
		}
		for a, b := i, j-1; a < b; a, b = a+1, b-1 {
			rs[a], rs[b] = rs[b], rs[a]
		}
		i = j
	}
	return string(rs)
}

// leftToRightIsland reports whether the rune at i runs left to right inside
// right-to-left text.
//
// A space counts only when the runes on BOTH sides of it do, and the second half of
// that was a real defect rather than a refinement. Requiring only the next rune —
// which is what keeps `Dreame L40 Ultra` one island — also swallows the space that
// SEPARATES the island from the right-to-left word before it, so the space came out
// on the wrong side of the island and, once [collapseSpaces] had run, the word and
// the island were one token: `מכשירMopExtend` for a page printing
// `מכשיר MopExtend`. A lost word boundary is a lost search hit, which is most of
// what this file exists to repair.
//
// It bites on real pages and only where poppler emits both scripts in ONE run, which
// is why page 185 never showed it and the pdftotext comparison could not see it: the
// digit there is a run of its own. Measured over the sequential manual's
// right-to-left pages, the runs that mix scripts are page 188's three
// (`Class 1 רזייל`, `IEC 60825-1:2014`), page 189's `Wi-Fi ןווחמ`, and one each on
// the Arabic pages 201 and 202. Found by the agent writing this file's tests, from a
// case the brief did not list.
func leftToRightIsland(rs []rune, i int) bool {
	switch p, _ := bidi.LookupRune(rs[i]); p.Class() {
	case bidi.L, bidi.EN, bidi.AN, bidi.ES, bidi.ET, bidi.CS:
		return true
	}
	if rs[i] == ' ' && i > 0 && i+1 < len(rs) {
		return strongLeftToRight(rs[i-1]) && strongLeftToRight(rs[i+1])
	}
	return false
}

// strongLeftToRight reports whether a rune is a letter or digit that reads left to
// right — what a space has to sit between to belong to an island.
func strongLeftToRight(r rune) bool {
	switch p, _ := bidi.LookupRune(r); p.Class() {
	case bidi.L, bidi.EN, bidi.AN:
		return true
	}
	return false
}

// joinRunsRightToLeft is [joinRuns] for a line that reads right to left: the runs are
// taken from the rightmost, EXCEPT that a stretch of runs which is entirely
// left-to-right keeps its own order.
//
// That exception is [leftToRightIsland] one level up, and it is not hypothetical — it
// was a regression this file shipped and the verifier caught. A left-to-right island
// can span many runs, because poppler splits a run at a font change: page 204 of the
// sequential manual sets the punctuation of
//
//	https://global.dreametech.com/pages/user-manuals-and-faqs
//
// in one font and the words in another, so that URL arrives as SEVENTEEN runs, broken
// at every `:`, `/`, `.` and `-`. Reversing the order of a line's runs is right for
// the Arabic prose around it and wrong for those seventeen, whose visual order already
// IS their logical order: the line came out reading
// `faqs-and- manuals-user/pages/com.dreametech.global://https`.
//
// Page 188's Hebrew twin never showed it, because there the same URL is a single run —
// the same reason the character-level version of this bug hid from the pdftotext
// comparison. A line is not a reliable witness to how poppler will cut it up.
//
// The runs slice is not reordered — the caller's geometry is computed from it and
// every other reader of a line wants it left to right. Only the text is built this
// way round.
func joinRunsRightToLeft(runs []TextRun) string {
	order := make([]int, 0, len(runs))
	for i := len(runs) - 1; i >= 0; {
		if hasRightToLeft(runs[i].Text) {
			order = append(order, i)
			i--
			continue
		}
		// A maximal stretch of runs with no right-to-left character in them: one
		// island, emitted in the order it is printed.
		j := i
		for j >= 0 && !hasRightToLeft(runs[j].Text) {
			j--
		}
		for k := j + 1; k <= i; k++ {
			order = append(order, k)
		}
		i = j
	}

	var b strings.Builder
	for n, i := range order {
		if n > 0 {
			prev := &runs[order[n-1]]
			cur := &runs[i]
			// Whichever way round the two sit on the page, the gap is between their
			// facing edges: reading order and left-to-right order are not the same
			// thing here, so the subtraction cannot assume one of them.
			gap := cur.X - prev.right()
			if cur.X < prev.X {
				gap = prev.X - cur.right()
			}
			if gap > 0 && !endsWithSpace(prev.Text) && !startsWithSpace(cur.Text) {
				b.WriteByte(' ')
			}
		}
		b.WriteString(visualToLogical(runs[i].Text))
	}
	return collapseSpaces(b.String())
}

// hasRightToLeft reports whether a string carries any right-to-left letter.
func hasRightToLeft(s string) bool {
	for _, r := range s {
		switch p, _ := bidi.LookupRune(r); p.Class() {
		case bidi.R, bidi.AL:
			return true
		}
	}
	return false
}
