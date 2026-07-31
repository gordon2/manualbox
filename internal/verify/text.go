package verify

import (
	"fmt"
	"strings"
	"unicode"
)

// Bounds on what counts as dropped content and as an invented word.
//
// Every one is measured against both fixtures — the 68-page parallel-columns
// manual (internal/doc/testdata/fixtures/thomas-drybox-amfibia.json) and the
// 560-page sequential one (dreame-l40-ultra.json) — converted for EVERY language
// each holds, so that a page carrying five languages is compared against all five.
// The measurements are quoted at each constant, in the style columns.go set.
const (
	// minCoverage is how little of `pdftotext`'s text a page's blocks may hold
	// before the page is reported as having dropped content.
	//
	// The honest baseline is below 1 and that is not a defect. Blocks are built from
	// doc's usableRuns, which drops sub-legible production artifacts — the column
	// manual's text layer carries an InDesign filename slug and an export timestamp
	// 260 times each, 8% of its runs — and `pdftotext` reports every one of them. It
	// also reports rotated text, which that filter drops, and furniture outside any
	// region. So the question is not "is it 1" but "is it what a correct conversion
	// scores".
	//
	// Measured per page over every language of both manuals:
	//
	//	column manual   66 pages judged: median 0.974, min 0.801 (page 5), then
	//	                0.802, 0.858, 0.859, 0.871; 8 pages under 0.90, 0 under 0.80
	//	sequential     552 pages judged: median 1.000, min 0.952 (page 189),
	//	                0 pages under 0.95
	//
	// So a correct conversion of these two documents floors at 0.80, and the pages
	// that get there are the artifact-heavy front matter the filter is for. 0.75
	// leaves that floor about six points of headroom while still reporting a page
	// that lost a quarter of itself. Set at 0.80 it would report page 5 of the column
	// manual on a thousandth of a point, which is a threshold pinned to one page.
	//
	// A ratio slightly ABOVE 1 is also normal — the sequential manual's maximum is
	// 1.003 — because a block joins a hyphenated word that `pdftotext` leaves broken
	// across two lines, and the join is one character shorter than the break.
	minCoverage = 0.75

	// minCoverageText is how much text a page needs before its ratio is judged.
	// A page holding a folio and a language badge scores whatever those two runs
	// happen to do; one page of the sequential manual is that page. The same floor
	// [doc.MinTextChars] sets, and for the same reason.
	minCoverageText = 50

	// minTokenRunes is how long a word must be to enter the comparison.
	//
	// It barely moves the numbers and it is still worth having. Measured over the
	// left-to-right pages of both manuals, the share of block words absent from
	// `pdftotext` runs 1.84% / 1.95% / 1.98% on the column manual at a floor of 1, 2
	// and 3 runes, and 0.47% / 0.45% / 0.48% on the sequential one. What the floor
	// removes is 1,756 of the column manual's 31,450 tokens, and they are bullets,
	// folios, list markers and unit letters — tokens on which the two tools
	// legitimately disagree (a printed bullet arrives as U+2022 from one and a hyphen
	// from the other) and which carry no evidence either way. 2 keeps every word.
	minTokenRunes = 2

	// maxInventedShare and minInventedTokens are how much of a block may be absent
	// from `pdftotext` before the block is reported.
	//
	// Not zero, and the measurement says why: 1.95% of the column manual's words and
	// 0.45% of the sequential one's are absent from a CORRECT conversion, because the
	// two tools break lines and normalise combining marks differently. Reporting
	// every one of them is 280 and 322 blocks of noise.
	//
	// Swept together over both manuals, as blocks reported:
	//
	//	share > 0.00   231 column / 190 sequential
	//	share > 0.10   113 / 179
	//	share > 0.20    17 / 169
	//	share > 0.34     4 / 153
	//	share > 0.50     0 / 118
	//
	// 0.34 is where the column manual stops reporting anything but real faults: its
	// remaining 4 are table cells where the two tools disagree about where a Cyrillic
	// or Kazakh word divides. It is deliberately not pushed to 0.50, because the
	// sequential manual's 153 findings at 0.34 are real — its Thai section arrives
	// with words broken at a vowel — and a threshold chosen to silence one document
	// would hide a defect in the other.
	//
	// The floor of 2 absent words is what keeps a one-word block from reporting
	// itself at 100%: a unit symbol or a bullet is a block, and one absent word out
	// of one is not evidence.
	maxInventedShare  = 0.34
	minInventedTokens = 2

	// rtlShare is how much of a page's words must be right-to-left before the page is
	// reported as [KindRightToLeft] rather than block by block.
	//
	// Measured: 32 pages of the sequential manual are 0.65 to 1.00 right-to-left by
	// word — its Hebrew and Arabic sections, the rest of each page being Latin part
	// numbers — and every other page of either manual is exactly 0.000. 0.5 sits in
	// the middle of that, and nothing between 0.05 and 0.6 changes the answer.
	//
	// Still 32 after doc/bidi.go, because reversing a line does not change which
	// script its characters are in. What changed is that 7 of the 32 now hold no
	// absent word at all, so only 25 reach [minReversibleWords] to be judged, and
	// none of those 25 is judged against it any more: see that constant.
	rtlShare = 0.5

	// minReversibleWords is how many of a right-to-left page's words must be absent
	// from `pdftotext` AND present in it reversed before the page is reported as
	// [KindRightToLeft] rather than block by block.
	//
	// # Why the check needed this at all
	//
	// It used to fire on a right-to-left page with any absent word whatsoever, which
	// was the same question as "is this page Hebrew or Arabic" for as long as every
	// such page arrived backwards. Once doc/bidi.go put the order right, the two
	// questions came apart and the check went on answering the first while its name
	// and its Detail string claimed the second: 25 pages of the sequential manual
	// still fired, on 220 absent words in 6,834, three of them on one page of 510.
	// A finding that reports pages which are not reversed cannot reach zero, and
	// says nothing on the way there.
	//
	// The evidence for reversal was already being counted and not used: a word that
	// is absent from the reference and present in it BACKWARDS was not extracted
	// wrong in some general way, it was extracted in visual order. That is the
	// signature, and nothing else this pipeline does produces it.
	//
	// # Why a count and not a share, which is the part worth keeping
	//
	// Three measurements over the sequential manual, absent words present reversed:
	//
	//	before bidi.go        32 pages, 8,120 absent, 7,938 reversible; per-page
	//	                      share 0.913 (page 188) to 1.000, 8 pages at 1.000
	//	majority direction    25 pages,   220 absent,    18 reversible; per-page
	//	                      share .600 .538 .125 .100 .091 .059, nineteen 0.000
	//	region direction      25 pages,   202 absent,     0 reversible; ALL 0.000
	//
	// A share of the absent words is the obvious rule, it had a real gap to sit in at
	// the middle measurement — nothing between 0.600 and 0.913 — and it was the wrong
	// rule, because those 18 words were not noise. Every one was a genuine word still
	// reversed: `תבותכב` where the page prints `בכתובת`, `ليلد` for `دليل`. A share of
	// 0.65 would have reported zero while six pages were reversed, and measured, it
	// would not merely have renamed them: pages 188, 204 and 207 fell through to a
	// [KindInvented] block, but 189, 191 and 205 held one reversed word in a block
	// that was otherwise right — under both [maxInventedShare] and
	// [minInventedTokens] — and vanished entirely.
	//
	// That is why the rule is a count, and it is worth keeping the argument even
	// though the corpus can no longer make it: the third row is zero, so a sweep over
	// this document would now choose anything. The argument is kept where it stays
	// falsifiable instead — TestAShareOfAbsentWordsWouldHideAReversal builds the
	// measured page-191 shape by hand and fails if the rule is ever changed back.
	//
	// # There is nothing under the floor, and 1 is still the only value
	//
	// The sweep chose the RULE, never the VALUE. 1 is not fitted to anything: it is
	// the statement that one word of evidence is evidence. 0 restores the defect this
	// constant was added to remove, since it requires no evidence at all, and any
	// value above 1 asserts that some quantity of reversed text is acceptable, which
	// nothing here would defend and which the corpus gives no basis for. So it stays
	// at 1 with its measurements recorded as history, and the load-bearing test moved
	// from "which threshold" to "is it zero" — see [TestNoTextIsStoredReversed].
	//
	// The one number that did fit the data is gone with it: the floor was 1 rather
	// than 2 because over the nineteen right-to-left pages holding no reversal, 140
	// absent words produced not one coincidental match. `שי` for `יש` was the only
	// two-rune match in the whole corpus and it sat among five unambiguous ones.
	//
	// # What this can and cannot see
	//
	// It sees a page holding at least one word that this pipeline read backwards and
	// `pdftotext` did not. It is named per page, which overstated the extent while
	// there was anything to overstate: the residual was one LINE on each page.
	//
	// It cannot see a reversal both tools make — they share no code, so this has no
	// example, but it is not ruled out. It cannot see a reversed word whose reverse is
	// missing from the reference for a second reason, which is what Arabic costs it:
	// `pdftohtml` returns unshaped letter forms, so a word can be both reversed and
	// unshaped and then only the shaping shows. It cannot see a reversed PALINDROME.
	//
	// AND IT CANNOT SEE A REORDERING THAT PRESERVES THE WORD SET, which is the
	// limitation worth knowing about, because it is the one that has actually cost
	// something twice. A zero from [KindRightToLeft] means no word is stored as its own
	// reverse. It does not mean the words are in the right order.
	//
	// Set membership per page is what [checkText] compares, for the reasons given
	// there, so word ORDER is outside it by construction. Both of doc/bidi.go's
	// run-order defects were invisible here: page 204's support URL arrived with its
	// seventeen runs reversed, and page 211's list marker `1.` arrived as `. 1`, and in
	// both cases every token was still present in the reference. The first surfaced
	// sideways as a [KindJoinHyphen] and the SECOND DID NOT SURFACE AT ALL — it was
	// caught only because a pinned block count moved by 43.
	//
	// [checkOrder] asks the order question of blocks and nothing asks it of words. That
	// gap is deliberately open, and conversion.md carries the design: what the
	// comparison would be against — `pdftotext`'s byte order already IS reading order
	// once the bidi controls are stripped, which the tokeniser does anyway — and what
	// makes it real work, which is matching lines to a per-page reference and not
	// reporting the reflow and column interleaving that are already reported elsewhere.
	minReversibleWords = 1
)

// checkCoverage answers "did we drop content", by comparing the blocks of a page
// against `pdftotext`'s reading of the same page.
//
// The comparison is non-space runes on both sides. Runes for the reason the whole
// project counts runes; non-space because `pdftotext` preserves the printed line
// breaks and column padding a block deliberately removes, so counting whitespace
// would compare a layout against a reflow.
//
// The ratio is expected to be below 1 for real reasons, which is why [minCoverage]
// is 0.75 and not 1: see its measurement.
//
// # Page furniture is NOT counted, on purpose, and it lowers every ratio
//
// A block [doc.Furniture] claimed is a language tab, a folio or a running head. It
// is really printed on the page, so `pdftotext` reports it and counting it would
// leave this ratio exactly where it was before that pass existed. It is skipped
// anyway, and the reason is that this check is the only thing that can refute the
// furniture rule. Count furniture and a rule that wrongly claims a paragraph is
// invisible here, because the paragraph is still in the sum. Skip it and the same
// mistake reads as a page that dropped a paragraph, which is what a coverage
// finding is for. The cost is a permanently lower floor, measured at
// [minCoverage].
func checkCoverage(in Input, scope []int) ([]PageCoverage, []Finding) {
	blocks := make(map[int]int, len(scope))
	for i := range in.Blocks {
		if in.Blocks[i].Furniture {
			continue
		}
		blocks[in.Blocks[i].Page] += countGraphemes(in.Blocks[i].Text)
	}
	text := make(map[int]int, len(in.Text))
	for i := range in.Text {
		text[in.Text[i].No] = countGraphemes(in.Text[i].Text)
	}

	cov := make([]PageCoverage, 0, len(scope))
	var out []Finding
	for _, p := range scope {
		c := PageCoverage{Page: p, Blocks: blocks[p], Text: text[p]}
		if c.Text > 0 {
			c.Ratio = float64(c.Blocks) / float64(c.Text)
		}
		cov = append(cov, c)
		if c.Text < minCoverageText || c.Ratio >= minCoverage {
			continue
		}
		out = append(out, Finding{
			Kind: KindCoverage, Page: p,
			Got: c.Ratio, Want: minCoverage,
			Count: c.Blocks, Total: c.Text,
			Detail: fmt.Sprintf("page %d: blocks hold %d of pdftotext's %d characters "+
				"(%.2f, want at least %.2f)", p, c.Blocks, c.Text, c.Ratio, minCoverage),
		})
	}
	return cov, out
}

// checkText answers "did we invent text", which coverage cannot.
//
// Interleaved columns keep every character of a page and destroy every word, so
// the count matches and the reading does not. Comparing words catches it: a word
// in a converted block that appears nowhere in `pdftotext`'s reading of the same
// page was assembled by this pipeline rather than printed on the paper.
//
// # The normalisation, which is the whole of the check's precision
//
// A token is a maximal run of letters, digits and combining marks, lowercased,
// with Unicode format characters stripped first — the bidi controls `pdftotext`
// wraps a right-to-left line in, which CONTRIBUTING.md records as having silently
// lost whole sections once already. Everything else is a separator, so punctuation,
// the soft hyphen and the printed bullet never enter the comparison, and neither
// does a difference of opinion about them. Tokens shorter than [minTokenRunes] are
// dropped, measured.
//
// Set membership per page, not a multiset and not a sequence. A multiset would
// report a legitimate difference of one occurrence, and a sequence would report
// the reading order this check is not about — [checkOrder] is.
//
// # Right-to-left is a known defect and gets its own finding
//
// conversion.md records that `pdftohtml -xml` returns a right-to-left line in
// visual order, and doc/bidi.go now repairs it. Where the repair does not reach,
// a page would report hundreds of invented words; a page that is more than
// [rtlShare] right-to-left by token AND carries at least [minReversibleWords]
// words absent from `pdftotext` but present in it reversed gets one
// [KindRightToLeft] finding instead of one per block.
//
// Both halves of that are needed and the second is the one measured hardest: being
// Hebrew is not being backwards, so a right-to-left page whose absent words are
// ordinary disagreement is judged block by block like any other. The reversal
// itself is the evidence, it is what the finding's name claims, and it is what
// makes the count able to reach zero. See [minReversibleWords].
func checkText(in Input, scope []int) []Finding {
	return checkTextWith(in, scope, defaultTextGuards)
}

// textGuards are the bounds, taken as a value so a test can sweep them over both
// whole documents. Every threshold in this project is set that way; see
// doc/figures.go's figureGuards.
type textGuards struct {
	minToken    int
	maxInvented float64
	minAbsent   int
	rtl         float64
	reversible  int
}

var defaultTextGuards = textGuards{
	minToken: minTokenRunes, maxInvented: maxInventedShare,
	minAbsent: minInventedTokens, rtl: rtlShare, reversible: minReversibleWords,
}

func checkTextWith(in Input, scope []int, g textGuards) []Finding {
	inScope := make(map[int]bool, len(scope))
	for _, p := range scope {
		inScope[p] = true
	}
	printed := make(map[int]map[string]bool, len(in.Text))
	for i := range in.Text {
		if inScope[in.Text[i].No] {
			printed[in.Text[i].No] = tokenSetMin(in.Text[i].Text, g.minToken)
		}
	}

	type pageState struct {
		tokens, rtl, absent, reversible int
		reversed                        string
	}
	state := make(map[int]*pageState, len(scope))
	byPage := make(map[int][]Finding, len(scope))

	for i := range in.Blocks {
		b := &in.Blocks[i]
		if !inScope[b.Page] {
			continue
		}
		have := printed[b.Page]
		st := state[b.Page]
		if st == nil {
			st = &pageState{}
			state[b.Page] = st
		}

		var absent []string
		toks := tokensMin(b.Text, g.minToken)
		for _, t := range toks {
			st.tokens++
			if isRightToLeft(t) {
				st.rtl++
			}
			if have[t] {
				continue
			}
			absent = append(absent, t)
			st.absent++
			if have[reverse(t)] {
				st.reversible++
				// The word as stored and as the page prints it, side by side. This is
				// the readable proof and it is why the finding exists at all, so it is
				// collected here rather than reconstructed from the page later.
				if len([]rune(st.reversed)) < sampleRunes {
					st.reversed += t + " for " + reverse(t) + "; "
				}
			}
		}
		if len(absent) == 0 {
			continue
		}
		if len(absent) < g.minAbsent ||
			float64(len(absent))/float64(len(toks)) <= g.maxInvented {
			continue
		}
		byPage[b.Page] = append(byPage[b.Page], Finding{
			Kind: KindInvented, Page: b.Page, RegionX0: b.RegionX0, Index: b.Index,
			Count: len(absent), Total: len(toks),
			Got:    float64(len(absent)) / float64(len(toks)),
			Want:   g.maxInvented,
			Sample: excerpt(strings.Join(absent, " ")),
			Detail: fmt.Sprintf("page %d block %d at x=%.0f: %d of %d words appear "+
				"nowhere in pdftotext's reading of the page",
				b.Page, b.Index, b.RegionX0, len(absent), len(toks)),
		})
	}

	var out []Finding
	for _, p := range scope {
		st := state[p]
		if st == nil || st.tokens == 0 {
			continue
		}
		if float64(st.rtl)/float64(st.tokens) <= g.rtl {
			out = append(out, byPage[p]...)
			continue
		}
		if st.absent == 0 || st.reversible < g.reversible {
			// Absent words with no reversal behind them are ordinary disagreement
			// between the two extractions, whatever direction the page reads in, so
			// they are judged block by block like every other page. See
			// [minReversibleWords] for what happens to a page that is judged the
			// other way round.
			out = append(out, byPage[p]...)
			continue
		}
		out = append(out, Finding{
			Kind: KindRightToLeft, Page: p,
			Count: st.absent, Total: st.tokens,
			// Got is the reversed words counted, Want the fewest that raise this at
			// all — the same "measurement and the bound it failed" every other finding
			// carries, where this one used to put the absent count in Want and so read
			// as though every absent word were expected to reverse.
			Got:  float64(st.reversible),
			Want: float64(g.reversible),
			// The excerpt is the reversed words with the printed spelling beside each,
			// which is the readable proof of the cause. It was the absent words, which
			// was the same list while whole pages were reversed and is mostly ordinary
			// disagreement now.
			Sample: excerpt(strings.TrimSuffix(st.reversed, "; ")),
			Detail: fmt.Sprintf("page %d reads right to left: %d of %d words are absent "+
				"from pdftotext, %d of them present when reversed — the known "+
				"pdftohtml visual-order defect, see docs/design/conversion.md",
				p, st.absent, st.tokens, st.reversible),
		})
	}
	return out
}

// tokens splits text the way both extractions can agree on. See [checkText] for
// why this is the normalisation and not another.
func tokens(s string) []string { return tokensMin(s, minTokenRunes) }

func tokensMin(s string, minRunes int) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) >= minRunes {
			out = append(out, string(cur))
		}
		cur = cur[:0]
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.Is(unicode.Cf, r):
			// A bidi control is not a separator: dropping it joins the runes either
			// side, which is what they are on the page.
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) ||
			unicode.Is(unicode.Mc, r):
			cur = append(cur, r)
		default:
			flush()
		}
	}
	flush()
	return out
}

func tokenSet(s string) map[string]bool { return tokenSetMin(s, minTokenRunes) }

func tokenSetMin(s string, minRunes int) map[string]bool {
	toks := tokensMin(s, minRunes)
	out := make(map[string]bool, len(toks))
	for _, t := range toks {
		out[t] = true
	}
	return out
}

// countGraphemes counts non-space runes. Runes, not bytes — half of a real manual
// is Cyrillic, Greek, Hebrew, Arabic or CJK.
func countGraphemes(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) && !unicode.Is(unicode.Cf, r) {
			n++
		}
	}
	return n
}

// rightToLeftScripts are the scripts these manuals actually print in that
// direction. Hebrew and Arabic are the two the sequential manual has; the other
// three cost nothing and stop the check being wrong about a document nobody here
// has seen.
var rightToLeftScripts = []*unicode.RangeTable{
	unicode.Hebrew, unicode.Arabic, unicode.Syriac, unicode.Thaana, unicode.Nko,
}

// isRightToLeft reports whether a token is written in a right-to-left script,
// decided by its first letter. First letter and not a majority vote: a Hebrew word
// with a Latin unit suffix is still a Hebrew word, and it is the line's direction
// this stands in for.
func isRightToLeft(tok string) bool {
	for _, r := range tok {
		if !unicode.IsLetter(r) {
			continue
		}
		for _, tab := range rightToLeftScripts {
			if unicode.Is(tab, r) {
				return true
			}
		}
		return false
	}
	return false
}

// reverse reverses a string's runes. Used only as evidence for the right-to-left
// finding — conversion.md is explicit that reversing in the view would be wrong
// twice over, and nothing here repairs anything.
func reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}
