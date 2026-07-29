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
	rtlShare = 0.5
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
// visual order, so every Hebrew and Arabic page would otherwise report hundreds
// of invented words. A page more than [rtlShare] right-to-left by token gets one
// [KindRightToLeft] finding instead, and that finding carries the confirmation:
// how many of the absent tokens are present in `pdftotext` when reversed rune for
// rune. The day the extraction is fixed, this stops firing in one place.
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
}

var defaultTextGuards = textGuards{
	minToken: minTokenRunes, maxInvented: maxInventedShare,
	minAbsent: minInventedTokens, rtl: rtlShare,
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
		sample                          string
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
			}
		}
		if len(absent) == 0 {
			continue
		}
		if st.sample == "" {
			st.sample = strings.Join(absent, " ")
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
		if st.absent == 0 {
			continue
		}
		out = append(out, Finding{
			Kind: KindRightToLeft, Page: p,
			Count: st.absent, Total: st.tokens,
			Got:  float64(st.reversible),
			Want: float64(st.absent),
			// The excerpt is the absent words themselves, which read as the printed
			// words backwards and are the readable proof of the cause.
			Sample: excerpt(st.sample),
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
