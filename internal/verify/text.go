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
	// The honest baseline is well below 1 and that is not a defect. Blocks are
	// built from [doc.usableRuns], which drops sub-legible production artifacts —
	// the column manual's text layer carries an InDesign filename slug and an
	// export timestamp 260 times each — and `pdftotext` reports every one of them.
	// It also reports rotated text, which that filter drops, and page furniture
	// living outside any region.
	//
	// Measured, per page, over every language of both manuals:
	//
	//	column manual   median 0.96, min 0.34 (page 1, the cover), 4 of 68 below 0.80
	//	sequential      median 0.98, min 0.00 (blank pages), 21 of 560 below 0.80
	//
	// 0.80 is chosen because the distribution has a gap there: on the column
	// manual the pages below it are the cover and three pages of framed
	// illustrations whose captions are rotated, and every page of prose scores
	// above 0.90. A tighter bound would report the cover of every manual, which is
	// a page nobody reads; a looser one would admit losing a fifth of a page of
	// prose, which is the defect this check exists for.
	minCoverage = 0.80

	// minCoverageText is how much text a page needs before its ratio is judged.
	// A page holding a folio and a language badge scores whatever those two runs
	// happen to do, and 34 of the sequential manual's pages are that page. The
	// same floor [doc.MinTextChars] sets, and for the same reason.
	minCoverageText = 50

	// minTokenRunes is how long a word must be to be compared.
	//
	// One-rune tokens are bullets, folios and list markers, and the two extractions
	// legitimately disagree about them: `pdftohtml` reports a printed bullet as
	// U+2022 where `pdftotext` writes it as a hyphen or drops it. Measured on the
	// column manual, comparing single runes as well raises the miss rate from 0.4%
	// to 3.1% and every added miss is punctuation.
	minTokenRunes = 2

	// maxInventedShare is how many of a block's words may be absent from
	// `pdftotext`'s reading of the same page before the block is reported.
	//
	// Not zero, and the measurement says why. A block legitimately misses a word
	// when the two tools break a line differently: `pdftohtml` reports a run
	// ending in a soft hyphen that `pdftotext` joins, and a ligature or a
	// combining mark can normalise differently between them. Measured over both
	// manuals with every language converted, on pages whose script reads left to
	// right:
	//
	//	column manual   14,061 tokens, 47 absent (0.33%), worst block 1 of 3
	//	sequential      99,927 tokens, 288 absent (0.29%), worst block 2 of 4
	//
	// The absences are concentrated in short blocks, which is why this is a share
	// with a floor rather than a share alone: 0.34 admits one word missing from a
	// three-word block and reports two missing from six.
	maxInventedShare = 0.34

	// minInventedTokens is how many words must be absent before a block is
	// reported at all, whatever the share. A one-word block whose one word is
	// absent is 100% invented and is almost always a bullet or a unit symbol.
	minInventedTokens = 2

	// rtlShare is how much of a page's text must be right-to-left before the page
	// is reported as [KindRightToLeft] instead of block by block.
	//
	// Measured on the sequential manual's Hebrew and Arabic sections: their pages
	// run 0.62 to 0.94 right-to-left by token, the rest being Latin part numbers
	// and digits, while no left-to-right page of either manual exceeds 0.02. 0.5
	// sits in the middle of a gap two orders wide.
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
// is 0.80 and not 1: see its measurement.
func checkCoverage(in Input, scope []int) ([]PageCoverage, []Finding) {
	blocks := make(map[int]int, len(scope))
	for i := range in.Blocks {
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
