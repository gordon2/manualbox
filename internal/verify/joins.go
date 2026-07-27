package verify

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/gordon2/manualbox/internal/doc"
)

// Bounds on what reads as a bad join. Measured on both fixtures, quoted below.
const (
	// minGluedPart is how long each half of a suspected glued word must be before
	// the split is believed.
	//
	// Without a floor the check reads any two-letter prefix as a word. Swept over
	// both manuals, as words reported:
	//
	//	floor 2   5 column / 3 sequential
	//	floor 3   0 / 3
	//	floor 4   0 / 1
	//
	// The five the column manual reports at 2 are the check running backwards: the
	// block holds "трубка" as one word and it is `pdftotext` that split it, into
	// "труб" and "ка", so the split it finds is the other tool's rather than ours. 3
	// removes all five and keeps both of the sequential manual's Thai cases; 4 loses
	// two of the three.
	minGluedPart = 3
)

// checkJoins reports text that reads as a typo, and fixes nothing.
//
// Three shapes, all mechanical:
//
//	a hyphen followed by a space mid-word — "Gehäusede- ckel"
//	two words glued with no space between them — "imGerät"
//	a doubled space inside a block
//
// The first is deliberate in `doc` and stays deliberate. conversion.md records
// that hyphenation is not undone because German legitimately ends a line with a
// hyphen, and this check does not argue with it: it counts the cost, so a later
// tier that can afford a judgement knows which pages to read.
//
// # What the hyphen sub-check cannot separate, measured
//
// German elides a shared stem with exactly the same characters: "Ein- und
// Ausschalten" is correct prose and "Gehäusede- ckel" is a broken word, and both
// are a letter, a hyphen, a space and a lowercase letter. Nothing on the page
// separates them without a lexicon — a line-break hyphen is recognisable only from
// where the line broke, and a block has deliberately removed that. So this fires on
// both, the excerpt says which, and no filter pretends otherwise.
//
// Measured: 276 blocks of the column manual carrying 313 such hyphens, and 72 blocks
// of the sequential one carrying 79. Of the first 25 read by eye, 22 are line-break
// hyphenation ("Polster- möbel", "эксклю- зивное") and 3 are elision ("Vor- oder",
// "Elektro- und"), so the shape is mostly right and cannot be made entirely right.
// Requiring a lowercase letter after the space is what keeps the punctuation dash out:
// without it the same pass reports "230 V - 50 Hz" on every specification page.
//
// The glued sub-check needs the second opinion and is skipped without it: a word
// is believed to be two words only when the page prints both of them separately.
func checkJoins(in Input) []Finding { return checkJoinsWith(in, minGluedPart) }

func checkJoinsWith(in Input, glueFloor int) []Finding {
	printed := make(map[int]map[string]bool, len(in.Text))
	for i := range in.Text {
		printed[in.Text[i].No] = tokenSet(in.Text[i].Text)
	}

	var out []Finding
	for i := range in.Blocks {
		b := &in.Blocks[i]
		out = append(out, hyphenJoins(b)...)
		out = append(out, doubleSpaces(b)...)
		if have := printed[b.Page]; have != nil {
			out = append(out, gluedWords(b, have, glueFloor)...)
		}
	}
	return out
}

// hyphenJoins finds a hyphen followed by a space and a lowercase letter.
//
// Lowercase and letter on purpose: "230 V - 50 Hz" and "Amfibia 788/M - Modell"
// are a dash used as punctuation, and a capital, a digit or another dash after the
// space says so. The rune before the hyphen must be a letter for the same reason.
func hyphenJoins(b *doc.Block) []Finding {
	r := []rune(b.Text)
	var hits []string
	for i := 1; i+2 < len(r); i++ {
		if r[i] != '-' || !unicode.IsLetter(r[i-1]) {
			continue
		}
		if r[i+1] != ' ' || !unicode.IsLower(r[i+2]) {
			continue
		}
		hits = append(hits, excerpt(window(r, i, 12)))
	}
	if len(hits) == 0 {
		return nil
	}
	return []Finding{{
		Kind: KindJoinHyphen, Page: b.Page, RegionX0: b.RegionX0, Index: b.Index,
		Count: len(hits), Total: b.Chars,
		Sample: excerpt(strings.Join(hits, " | ")),
		Detail: fmt.Sprintf("page %d block %d at x=%.0f: %d hyphen(s) followed by a "+
			"space mid-word", b.Page, b.Index, b.RegionX0, len(hits)),
	}}
}

// doubleSpaces finds two or more spaces in a row.
func doubleSpaces(b *doc.Block) []Finding {
	n := 0
	r := []rune(b.Text)
	for i := 1; i < len(r); i++ {
		if r[i] == ' ' && r[i-1] == ' ' {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return []Finding{{
		Kind: KindJoinSpace, Page: b.Page, RegionX0: b.RegionX0, Index: b.Index,
		Count: n, Total: b.Chars,
		Sample: excerpt(b.Text),
		Detail: fmt.Sprintf("page %d block %d at x=%.0f: %d doubled space(s)",
			b.Page, b.Index, b.RegionX0, n),
	}}
}

// gluedWords finds a word the page never printed whose two halves it did.
//
// This is the one join sub-check with evidence behind it rather than a shape: the
// word is absent from `pdftotext`'s reading of the page, and a split point exists
// where both halves are words that page printed. A word absent for any other
// reason — a ligature, a reversed right-to-left line — has no such split and is
// not reported here.
func gluedWords(b *doc.Block, printed map[string]bool, floor int) []Finding {
	var hits []string
	for _, tok := range tokens(b.Text) {
		if printed[tok] {
			continue
		}
		r := []rune(tok)
		if len(r) < 2*floor {
			continue
		}
		for i := floor; i <= len(r)-floor; i++ {
			if printed[string(r[:i])] && printed[string(r[i:])] {
				hits = append(hits, string(r[:i])+"|"+string(r[i:]))
				break
			}
		}
	}
	if len(hits) == 0 {
		return nil
	}
	return []Finding{{
		Kind: KindJoinGlued, Page: b.Page, RegionX0: b.RegionX0, Index: b.Index,
		Count: len(hits), Total: b.Chars,
		Sample: excerpt(strings.Join(hits, " ")),
		Detail: fmt.Sprintf("page %d block %d at x=%.0f: %d word(s) glued from two the "+
			"page prints separately", b.Page, b.Index, b.RegionX0, len(hits)),
	}}
}

// window is the runes around an index, for an excerpt a person can find on the
// page.
func window(r []rune, at, radius int) string {
	lo, hi := at-radius, at+radius
	if lo < 0 {
		lo = 0
	}
	if hi > len(r) {
		hi = len(r)
	}
	return string(r[lo:hi])
}
