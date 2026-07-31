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

// TestRightToLeftSweep prints how [minReversibleWords] behaves over the whole
// sequential manual, which is the only document either fixture holds that reads
// right to left at all. It is the measurement that constant is set from, and it is
// a test rather than a script so that a later change re-runs it instead of trusting
// the numbers written down. The shape is doc/figures_internal_test.go's
// TestGuardSweep.
//
// It sweeps the rejected alternative too. A share of the absent words is the
// obvious rule and it has a gap to sit in, and it is wrong: see the constant. The
// sweep prints what each rule reports so the argument can be re-checked rather than
// re-read.
func TestRightToLeftSweep(t *testing.T) {
	in := sequentialInput(t)
	scope := pageScope(in)
	rows := rightToLeftPages(t, in, scope)

	t.Logf("%d right-to-left page(s), by how much of their absent text is present reversed:",
		len(rows))
	var absent, reversible, withEvidence int
	for _, r := range rows {
		absent += r.absent
		reversible += r.reversible
		if r.reversible > 0 {
			withEvidence++
		}
		t.Logf("  page %3d: %3d of %4d words absent, %3d present reversed (%.3f)  %s",
			r.page, r.absent, r.tokens, r.reversible, r.share, r.sample)
		for _, w := range reversibleWords(in, r.page) {
			// The words that carry the verdict, printed because the whole choice of
			// rule turns on whether they are real. They are.
			t.Logf("        %s", w)
		}
	}
	t.Logf("  %d page(s), %d absent word(s), %d present reversed on %d page(s)",
		len(rows), absent, reversible, withEvidence)

	for _, v := range []int{0, 1, 2, 3, 5, 10, 50} {
		g := defaultTextGuards
		g.reversible = v
		pages, blocks := countKinds(checkTextWith(in, scope, g))
		t.Logf("  minReversibleWords=%-2d -> %d right-to-left page(s), %d invented-text block(s)",
			v, pages, blocks)
	}
	// The rejected rule, at every threshold a gap would allow.
	for _, v := range []float64{0.1, 0.3, 0.5, 0.65, 0.8, 0.95} {
		pages := 0
		for _, r := range rows {
			if r.share >= v {
				pages++
			}
		}
		t.Logf("  as a share of absent >= %-4.2f -> %d page(s), and %d page(s) holding a "+
			"real reversal report nothing at all", v, pages, hidden(t, in, scope, rows, v))
	}

	// The one property the rule has to have, and the one a share does not: the
	// pages it reports are exactly the pages that carry evidence of a reversal.
	// Asserted as a partition rather than as a count, because the count is the
	// document's business and this is the check's.
	reported := make(map[int]bool)
	for _, f := range checkTextWith(in, scope, defaultTextGuards) {
		if f.Kind == KindRightToLeft {
			reported[f.Page] = true
		}
	}
	for _, r := range rows {
		switch {
		case r.reversible > 0 && !reported[r.page]:
			t.Errorf("page %d holds %d word(s) that are absent forwards and present "+
				"reversed, and the check says nothing about it", r.page, r.reversible)
		case r.reversible == 0 && reported[r.page]:
			t.Errorf("page %d is reported as right-to-left-reversed with no reversed "+
				"word on it, which is the thing this guard was added to stop", r.page)
		}
	}
}

// hidden is how many pages carrying a real reversal a share threshold would leave
// with no finding of any kind — not renamed to [KindInvented], gone.
func hidden(t *testing.T, in Input, scope []int, rows []rtlPage, share float64) int {
	t.Helper()
	g := defaultTextGuards
	g.reversible = 1 << 30 // never name a page; judge every one block by block
	seen := make(map[int]bool)
	for _, f := range checkTextWith(in, scope, g) {
		seen[f.Page] = true
	}
	n := 0
	for _, r := range rows {
		if r.reversible > 0 && r.share < share && !seen[r.page] {
			n++
		}
	}
	return n
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
// which is the population a threshold is chosen over.
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
	sort.Slice(out, func(a, b int) bool { return out[a].share > out[b].share })
	return out
}

// reversibleWords is the words of one page that are absent from `pdftotext` and
// present in it reversed, with the block they sit in. Exactly what
// [checkTextWith] counts, printed so a reader can judge whether it is a reversal
// or a coincidence.
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
