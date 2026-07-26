package ingest_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/extern"
	"github.com/gordon2/manualbox/internal/ingest"
	"github.com/gordon2/manualbox/internal/testpdf"
)

// The gate's language map comes from the stored regions where there are regions
// and from the per-page runs otherwise, so these tests drive both paths through
// the real pipeline. They generate their own PDFs: the shapes below are the two
// layouts docs/design/layouts.md describes, in miniature.

// requirePDFToHTML skips a test that needs positioned text. Regions cannot be
// invented without coordinates, so without this tool the pipeline legitimately
// stores none and there is nothing to assert.
func requirePDFToHTML(t *testing.T) {
	t.Helper()
	if !extern.Available(extern.PDFToHTML) {
		t.Skip("pdftohtml is not installed, so no regions are stored")
	}
}

// columnManual builds a manual that runs its languages in parallel columns: every
// page carries all of them side by side, each column headed by its own printed
// code.
//
// Two details are what make it the real shape rather than a convenient one. There
// is no contents table, because the measured manual's contents page is laid out in
// columns and does not parse — so no vocabulary of codes exists and the per-page
// tag reader cannot narrow anything. And each page opens with a heading set across
// the whole measure, so the first lines of the page are not a code either. The
// result is the case that matters: the per-page signals name nothing at all, and
// only the columns know what the document contains.
func columnManual(codes []string, pages int) testpdf.Doc {
	return columnManualFrom(codes, pages, 40)
}

// columnManualFrom is the same, with the left column's offset given, because
// whether a language shares its pages must not depend on where the leftmost
// column happens to start.
func columnManualFrom(codes []string, pages, leftEdge int) testpdf.Doc {
	var d testpdf.Doc
	for p := range pages {
		page := testpdf.Page{Lines: []string{
			"Installation and maintenance of the appliance, page " + fmt.Sprint(p+1),
			"Read the whole of this section before starting any work at all",
			"Keep this booklet for later reference and for the next owner",
		}}
		for i, code := range codes {
			lines := []string{code}
			for range 10 {
				lines = append(lines, "Maintenance information.")
			}
			page.Columns = append(page.Columns, testpdf.Column{
				X:     leftEdge + i*190,
				Lines: lines,
			})
		}
		d.Pages = append(d.Pages, page)
	}
	return d
}

func TestGateReadsAColumnManualFromItsRegions(t *testing.T) {
	// The bug this fixes, in miniature: a page holding three languages has no
	// honest per-page answer, so the per-page map names nothing and the gate said
	// "no language could be identified" about a document whose regions held every
	// one of them.
	requirePDFToHTML(t)
	h := newHarness(t, []string{"de"})
	ctx := context.Background()

	document := h.upload(t, "columns.pdf", columnManual([]string{"DE", "PL", "NL"}, 4))
	h.runProbe(t, document.ID)

	// The premise, asserted rather than assumed: there is no per-page answer to
	// read. If this ever stops being true the test below stops testing anything.
	runs, err := h.registry.LanguageRuns(ctx, document.ID, doc.SourceReconciled)
	if err != nil {
		t.Fatalf("language runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("the per-page map named %d runs, so this document no longer exercises "+
			"the regions path: %+v", len(runs), runs)
	}

	gate, err := h.ingest.Gate(ctx, document.ID)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}

	if len(gate.InScope) != 1 {
		t.Fatalf("in scope = %d languages, want 1 (de): %+v", len(gate.InScope), gate.InScope)
	}
	if len(gate.Other) != 2 {
		t.Errorf("other = %d languages, want 2 (pl, nl): %+v", len(gate.Other), gate.Other)
	}

	german := gate.InScope[0]
	if german.Lang != "de" {
		t.Errorf("in-scope language = %q, want de", german.Lang)
	}
	if german.Chars == 0 {
		t.Error("German reached the gate with no characters, which is the whole point of reading regions")
	}
	if german.Pages != 4 {
		t.Errorf("German is on %d pages, want 4", german.Pages)
	}
	// The page count on its own would read as four pages of German reading. It is
	// one column of each of four pages, and this is the field that says so.
	if !german.SharesPages {
		t.Error("German does not report sharing its pages, though every page holds three languages")
	}

	// Characters lead: a third of the columns is roughly a third of the text, while
	// the page count is all of them.
	if gate.Chars <= german.Chars {
		t.Errorf("document chars = %d, not more than German's %d", gate.Chars, german.Chars)
	}
	if german.Share <= 0 || german.Share >= 0.5 {
		t.Errorf("German's share = %.3f, want a third-ish of a three-language document", german.Share)
	}
	if gate.ScopeChars != german.Chars {
		t.Errorf("scope chars = %d, want German's %d", gate.ScopeChars, german.Chars)
	}
	if gate.Cost.Chars != gate.ScopeChars {
		t.Errorf("cost.chars = %d, want the %d characters in scope — the field is documented "+
			"as measured and always present", gate.Cost.Chars, gate.ScopeChars)
	}

	// Distinct pages, not a sum: three languages on four pages is four pages.
	if gate.ScopePages != 4 {
		t.Errorf("scope pages = %d, want 4", gate.ScopePages)
	}
	if !strings.Contains(gate.Summary, "3 languages") {
		t.Errorf("summary does not name the languages found: %q", gate.Summary)
	}
	if strings.Contains(gate.Summary, "no language could be identified") {
		t.Errorf("the gate still claims it read nothing: %q", gate.Summary)
	}
}

func TestGateSeesASharedPageWhoseLeftColumnBeginsAtTheEdge(t *testing.T) {
	// Testing a region's x0 against zero looks like a way to tell a box from a whole
	// page, and it is not: a leftmost column can legitimately begin at the page's
	// left edge, and then the language filling it would report the page as its own.
	// A page carrying more than one region is the robust test, and this document is
	// where the two rules disagree.
	requirePDFToHTML(t)
	h := newHarness(t, []string{"de"})
	ctx := context.Background()

	document := h.upload(t, "flush.pdf", columnManualFrom([]string{"DE", "PL", "NL"}, 3, 0))
	h.runProbe(t, document.ID)

	regions, err := h.registry.Regions(ctx, document.ID)
	if err != nil {
		t.Fatalf("regions: %v", err)
	}
	// The premise: German's territory really does start at the page edge.
	flush := false
	for i := range regions {
		if regions[i].Lang == "de" && regions[i].X0 == 0 {
			flush = true
		}
	}
	if !flush {
		t.Skipf("no German region begins at x0 = 0, so this document does not "+
			"separate the two rules: %+v", regions)
	}

	gate, err := h.ingest.Gate(ctx, document.ID)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if len(gate.InScope) != 1 {
		t.Fatalf("in scope = %d languages, want 1: %+v", len(gate.InScope), gate.InScope)
	}
	if !gate.InScope[0].SharesPages {
		t.Error("German claims its pages as its own, though it fills the left column of " +
			"pages that hold three languages")
	}
}

func TestGateCountsPagesSharedByTwoInScopeLanguagesOnce(t *testing.T) {
	// Summing per-language page counts is what makes a columns manual report more
	// pages in scope than it has: on the measured 68-page manual, five languages of
	// 26 to 27 pages each sum to 133. A household reading two of them is still
	// reading the same pages.
	requirePDFToHTML(t)
	h := newHarness(t, []string{"de", "nl"})
	ctx := context.Background()

	document := h.upload(t, "columns.pdf", columnManual([]string{"DE", "PL", "NL"}, 4))
	h.runProbe(t, document.ID)

	gate, err := h.ingest.Gate(ctx, document.ID)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if len(gate.InScope) != 2 {
		t.Fatalf("in scope = %d languages, want 2 (de, nl): %+v", len(gate.InScope), gate.InScope)
	}
	if gate.ScopePages != 4 {
		t.Errorf("scope pages = %d, want 4 — two languages sharing the same four pages, "+
			"not 8", gate.ScopePages)
	}
	if gate.ScopeFraction > 1 {
		t.Errorf("scope fraction = %.2f, more than the whole document", gate.ScopeFraction)
	}
	// Characters do add up, because two columns of a page are twice the reading.
	if want := gate.InScope[0].Chars + gate.InScope[1].Chars; gate.ScopeChars != want {
		t.Errorf("scope chars = %d, want %d — characters are the thing that sums",
			gate.ScopeChars, want)
	}
}

func TestGateOnASequentialManualIsUnchangedByRegions(t *testing.T) {
	// The other half of the acceptance in docs/design/regions.md: a change that
	// improves the columns manual by altering the sequential one has broken
	// something. Every field here is what the runs said before regions were read,
	// and the new ones are additions to it.
	requirePDFToHTML(t)
	h := newHarness(t, []string{"en", "de"})
	ctx := context.Background()

	document := h.upload(t, "manual.pdf",
		testpdf.TaggedSections([]string{"EN", "DE", "FR", "IT", "ES"}, 3, true))
	h.runProbe(t, document.ID)

	gate, err := h.ingest.Gate(ctx, document.ID)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}

	if len(gate.InScope) != 2 || len(gate.Other) != 3 {
		t.Fatalf("language map = %d in scope and %d other, want 2 and 3: %+v %+v",
			len(gate.InScope), len(gate.Other), gate.InScope, gate.Other)
	}
	if gate.ScopePages != 6 {
		t.Errorf("scope pages = %d, want 6 — two sections of three pages", gate.ScopePages)
	}

	for i := range gate.InScope {
		e := &gate.InScope[i]
		// The run's own record survives: a language named per page keeps its
		// section title, its source and its confidence, none of which a region
		// stores.
		if e.Source != string(doc.SourceReconciled) {
			t.Errorf("%s reports source %q, want the reconciled run's own", e.Lang, e.Source)
		}
		if e.Title == "" {
			t.Errorf("%s lost the section title the contents table printed", e.Lang)
		}
		if e.Pages != 3 {
			t.Errorf("%s covers %d pages, want 3", e.Lang, e.Pages)
		}
		// This manual runs its languages in sequence, so no language shares a page
		// with another. The field that stops a page count misleading must not fire
		// where the page count is honest.
		if e.SharesPages {
			t.Errorf("%s reports sharing its pages, but this manual sets one language per page", e.Lang)
		}
		if e.Chars == 0 {
			t.Errorf("%s reached the gate with no characters", e.Lang)
		}
	}

	if gate.ScopeChars == 0 || gate.Cost.Chars != gate.ScopeChars {
		t.Errorf("scope chars = %d and cost.chars = %d, want both the same non-zero count",
			gate.ScopeChars, gate.Cost.Chars)
	}
	if gate.ScopeCharFraction <= 0 || gate.ScopeCharFraction >= 1 {
		t.Errorf("scope char fraction = %.3f for 2 of 5 languages", gate.ScopeCharFraction)
	}
}

func TestGateFallsBackToPerPageRunsWithNoRegions(t *testing.T) {
	// An empty region set is not the claim that a manual has one language. A
	// document probed on a host without pdftohtml has none stored and a complete
	// per-page map, and it must go on reporting that map.
	h := newHarness(t, []string{"de"})
	ctx := context.Background()

	document := h.upload(t, "manual.pdf",
		testpdf.TaggedSections([]string{"EN", "DE", "FR"}, 3, true))
	h.runProbe(t, document.ID)

	if _, err := h.db.Write().ExecContext(ctx,
		"DELETE FROM doc_regions WHERE document_id = ?", document.ID); err != nil {
		t.Fatalf("delete regions: %v", err)
	}

	gate, err := h.ingest.Gate(ctx, document.ID)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}

	if len(gate.InScope) != 1 || len(gate.Other) != 2 {
		t.Fatalf("with no regions the gate reports %d in scope and %d other, want 1 and 2",
			len(gate.InScope), len(gate.Other))
	}
	if got := gate.InScope[0].Lang; got != "de" {
		t.Errorf("in-scope language = %q, want de", got)
	}
	if gate.ScopePages != 3 {
		t.Errorf("scope pages = %d, want 3", gate.ScopePages)
	}
	// Characters are still measured, from the per-page counts rather than from
	// boxes. The two differ by a few percent on a real document; reporting nothing
	// would be worse.
	if gate.InScope[0].Chars == 0 || gate.Cost.Chars != gate.InScope[0].Chars {
		t.Errorf("chars = %d and cost.chars = %d with no regions, want both the German "+
			"section's per-page count", gate.InScope[0].Chars, gate.Cost.Chars)
	}
	if gate.InScope[0].SharesPages {
		t.Error("a language shares pages according to a document with no regions stored")
	}
}

func TestGateCountsContentPagesNothingCouldName(t *testing.T) {
	// UnlabelledPages is the honest measure of how much a statistical detector
	// would add for this document, and it was declared and never assigned: always
	// 0, on every document, however much of it went unread.
	requirePDFToHTML(t)
	h := newHarness(t, []string{"de"})
	ctx := context.Background()

	// Three pages of columns that name themselves, then a page of the same body
	// text with no code anywhere on it. Nothing can name the last page: no printed
	// tag, and its letters are the Latin the other pages use.
	d := columnManual([]string{"DE", "PL", "NL"}, 3)
	unnamed := testpdf.Page{Lines: []string{
		"Service addresses and contact details for every country listed",
	}}
	for range 12 {
		unnamed.Lines = append(unnamed.Lines,
			"Ordinary prose with no language code printed anywhere upon it.")
	}
	d.Pages = append(d.Pages, unnamed)

	document := h.upload(t, "columns.pdf", d)
	h.runProbe(t, document.ID)

	gate, err := h.ingest.Gate(ctx, document.ID)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if gate.UnlabelledPages != 1 {
		t.Errorf("unlabelled pages = %d, want 1 — the page carrying text that nothing named",
			gate.UnlabelledPages)
	}
	// It is not counted as anybody's, either.
	for _, e := range append(append([]ingest.GateLanguage{}, gate.InScope...), gate.Other...) {
		if e.Pages > 3 {
			t.Errorf("%s claims %d pages, more than the 3 that name it", e.Lang, e.Pages)
		}
	}
}
