package verify

import (
	"context"
	"os"
	"sort"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/extern"
	"github.com/gordon2/manualbox/internal/fixture"
)

// TestNoTextIsStoredReversed is the acceptance criterion for doc/bidi.go, asserted
// from the outside: over the only document either fixture holds that reads right to
// left, not one word is absent from `pdftotext` and present in it backwards.
//
// It began as a threshold sweep for [minReversibleWords], on the model of
// doc/figures_internal_test.go's TestGuardSweep, and it is not one any more because
// there is nothing left to sweep. Three measurements, the first two recorded at that
// constant and the third printed by this test every time it runs:
//
//	before bidi.go        32 pages, 8,120 absent, 7,938 reversible
//	majority direction    25 pages,   220 absent,    18 reversible on 6 pages
//	region direction      25 pages,   202 absent,     0 reversible
//
// A sweep over an empty population would accept any value and prove nothing, so the
// assertion moved from "which threshold" to "is it zero" — which is stronger, and is
// what would actually break if direction handling regressed. What the sweep decided,
// a count of reversed words rather than a share of the absent ones, is pinned by
// [TestAShareOfAbsentWordsWouldHideAReversal] instead, hermetically, because this
// corpus can no longer demonstrate it.
//
// The distribution is still printed. Someone changing doc's direction handling wants
// to see what this document does, not to be told that it is fine.
func TestNoTextIsStoredReversed(t *testing.T) {
	in := sequentialInput(t)
	scope := pageScope(in)
	rows := rightToLeftPages(t, in, scope)

	t.Logf("%d right-to-left page(s), by how much of their absent text is present reversed:",
		len(rows))
	var absent, reversible int
	var flagged []int
	for _, r := range rows {
		absent += r.absent
		reversible += r.reversible
		if r.reversible > 0 {
			flagged = append(flagged, r.page)
		}
		t.Logf("  page %3d: %3d of %4d words absent, %3d present reversed (%.3f)  %s",
			r.page, r.absent, r.tokens, r.reversible, r.share, r.sample)
		for _, w := range reversibleWords(in, r.page) {
			// Every word behind a non-zero verdict, printed, because the whole
			// question is whether such a word is a real reversal. Last time all 18
			// were, and naming them is what found the cause.
			t.Logf("        %s", w)
		}
	}
	t.Logf("  %d page(s), %d absent word(s), %d present reversed", len(rows), absent, reversible)

	// The assertion. 202 absent words remain and none is backwards: what is left is
	// Arabic shaping and combining-mark disagreement, which is [KindInvented]'s
	// business and which conversion.md records as neither tool's to fix.
	if reversible != 0 {
		t.Errorf("%d word(s) on page(s) %v are absent from pdftotext and present in it "+
			"reversed; doc/bidi.go is meant to leave none, and each word is logged "+
			"above with the block it sits in", reversible, flagged)
	}

	// With the population empty the constant decides nothing, and that is worth
	// showing rather than asserting: every value gives the same report.
	for _, v := range []int{1, 2, 5, 50} {
		g := defaultTextGuards
		g.reversible = v
		pages, blocks := countKinds(checkTextWith(in, scope, g))
		t.Logf("  minReversibleWords=%-2d -> %d right-to-left page(s), %d invented-text block(s)",
			v, pages, blocks)
	}
}

// TestAShareOfAbsentWordsWouldHideAReversal keeps the one design decision the
// fixture measurement can no longer defend, now that the residual it was measured on
// is zero.
//
// The obvious rule for [minReversibleWords] is a share of the page's absent words
// rather than a count of the reversed ones, and when it was chosen that share had a
// real gap to sit in: nothing between 0.600 and 0.913. This is the shape that rules
// it out, taken from the measured page 191 — one line reversed on a page that is
// otherwise right, so one reversed word among ordinary disagreement. A count reports
// it. A share buries it under the noise on its own page, and it does not even fall
// through to [KindInvented], because one absent word in a block is under
// [minInventedTokens].
//
// So a share would have called page 191 clean while `אפלקציית` was stored as
// `תייצקלפא`, and the check would have read zero for the wrong reason.
func TestAShareOfAbsentWordsWouldHideAReversal(t *testing.T) {
	// The page as pdftotext reads it, wrapped in the bidi controls it uses. Written
	// as escapes because they are invisible, the reason verify_test.go's rtlEmbed
	// gives — a test whose input cannot be seen in the source is one nobody can check.
	const printed = "\u202b" + "אפלקציית Dreamehome תואמתמ הוראות בטמפרטורה גבוהה " +
		"ובלחות רבה יש להימנע משימוש" + "\u202c"
	in := Input{
		Blocks: []doc.Block{
			// One reversed word, in a short block of its own, as page 191 has it.
			{Page: 191, Index: 0, Text: "Dreamehome תייצקלפא", Chars: 19, Lines: 1,
				X0: 700, X1: 860, Y0: 100, Y1: 118},
			// The rest of the page, correctly ordered but with one word the
			// reference spells differently — the ordinary disagreement that raises
			// the absent count a share would be divided by.
			{Page: 191, Index: 1, Text: "תואמתמ הוראות בטמפרטורת גבוהה ובלחות רבה " +
				"יש להימנע משימוש", Chars: 57, Lines: 1,
				X0: 500, X1: 860, Y0: 130, Y1: 148},
		},
		Text: []doc.Page{{No: 191, Text: printed, Chars: len([]rune(printed))}},
	}

	// The rule as it stands: the reversed word is found and the page is named.
	rep := Inspect(in)
	if got := rep.Count(KindRightToLeft); got != 1 {
		t.Fatalf("the count rule missed a page with a reversed word on it: %+v",
			rep.Findings)
	}
	if got := int(rep.Findings[0].Got); got != 1 {
		t.Errorf("the finding claims %d reversed word(s), want 1", got)
	}

	// The rejected rule, standing in for any share a single reversal cannot reach.
	// Nothing is reported at all — not renamed, gone — which is the whole argument.
	g := defaultTextGuards
	g.reversible = 1 << 30
	if found := checkTextWith(in, pageScope(in), g); len(found) != 0 {
		t.Fatalf("this fixture no longer demonstrates the trap: judged block by "+
			"block it reports %+v, so a share rule would have renamed the reversal "+
			"rather than hidden it, and the test needs rebuilding", found)
	}
}

func countKinds(found []Finding) (pages, blocks int) {
	for i := range found {
		switch found[i].Kind {
		case KindRightToLeft:
			pages++
		case KindInvented:
			blocks++
		}
	}
	return pages, blocks
}

type rtlPage struct {
	page                       int
	absent, reversible, tokens int
	share                      float64
	sample                     string
}

// rightToLeftPages is every page the direction test claims, whatever its evidence,
// which is the population a threshold would be chosen over.
func rightToLeftPages(t *testing.T, in Input, scope []int) []rtlPage {
	t.Helper()
	g := defaultTextGuards
	g.reversible = 0
	var out []rtlPage
	for _, f := range checkTextWith(in, scope, g) {
		if f.Kind != KindRightToLeft {
			continue
		}
		r := rtlPage{page: f.Page, absent: f.Count, reversible: int(f.Got),
			tokens: f.Total, sample: f.Sample}
		if r.absent > 0 {
			r.share = float64(r.reversible) / float64(r.absent)
		}
		out = append(out, r)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].share != out[b].share {
			return out[a].share > out[b].share
		}
		return out[a].page < out[b].page
	})
	return out
}

// reversibleWords is the words of one page that are absent from `pdftotext` and
// present in it reversed, with the block they sit in. Exactly what [checkTextWith]
// counts, printed so a reader can judge whether it is a reversal or a coincidence.
func reversibleWords(in Input, page int) []string {
	var have map[string]bool
	for i := range in.Text {
		if in.Text[i].No == page {
			have = tokenSet(in.Text[i].Text)
		}
	}
	var out []string
	seen := make(map[string]bool)
	for i := range in.Blocks {
		if in.Blocks[i].Page != page {
			continue
		}
		for _, t := range tokens(in.Blocks[i].Text) {
			if !have[t] && have[reverse(t)] && !seen[t] {
				seen[t] = true
				out = append(out, t+" for "+reverse(t)+", in ["+excerpt(in.Blocks[i].Text)+"]")
			}
		}
	}
	return out
}

// sequentialInput converts the 560-page fixture for every language and reads it a
// second time with `pdftotext`, which is what the text checks compare.
func sequentialInput(t *testing.T) Input {
	t.Helper()
	if os.Getenv(fixture.EnableEnv) == "" {
		t.Skipf("set %s=1 to download the fixture and run this", fixture.EnableEnv)
	}
	for _, tool := range []extern.Tool{extern.PDFInfo, extern.PDFToText, extern.PDFToHTML} {
		if !extern.Available(tool) {
			t.Skipf("%s is not installed", tool.Name)
		}
	}
	m, err := fixture.Load("../../testdata/fixtures", "dreame-l40-ultra")
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	ctx := context.Background()
	path, err := m.Fetch(ctx)
	if err != nil {
		t.Fatalf("fetch fixture: %v", err)
	}
	res, err := doc.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	conv, err := ConvertAll(ctx, path, res)
	if err != nil {
		t.Fatalf("ConvertAll: %v", err)
	}
	text, err := doc.ExtractText(ctx, path, conv.Scope.TotalPages)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	return Input{Blocks: conv.Blocks, Text: text, Pages: conv.Pages}
}
