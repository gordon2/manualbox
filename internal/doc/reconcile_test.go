package doc_test

import (
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
)

// Regression tests for reconciliation defects found in review. Each was
// reproduced before being fixed.

func TestTwoRegionalVariantsStayTwoSections(t *testing.T) {
	// Grouping continued a run whenever two labels shared a base language, then
	// kept whichever tag string was longer. That could not tell "a vaguer signal
	// filled a gap in one section" from "the document named two different
	// variants", so a Portuguese section and a Brazilian Portuguese section merged
	// into one — and half its pages were then relabelled with a variant their own
	// printed tag contradicts. A household reading one variant would be scoped
	// onto both and pay to translate the wrong sixteen pages.
	pages := []doc.Page{
		page(1, "PT", doc.ScriptLatin, 1, "texto em idioma iberico"),
		page(2, "PT", doc.ScriptLatin, 2, "texto em idioma iberico"),
		page(3, "BR", doc.ScriptLatin, 3, "texto em idioma brasileiro"),
		page(4, "BR", doc.ScriptLatin, 4, "texto em idioma brasileiro"),
	}
	bySource := map[doc.Source][]doc.Run{
		doc.SourcePageTag: {
			{Source: doc.SourcePageTag, Code: "PT", Lang: "pt", Start: 1, End: 2, Confidence: 1},
			{Source: doc.SourcePageTag, Code: "BR", Lang: "pt-BR", Start: 3, End: 4, Confidence: 1},
		},
	}

	runs := doc.Reconcile(pages, bySource)
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d — the document names two variants: %+v", len(runs), runs)
	}
	if runs[0].Lang != "pt" || runs[0].End != 2 {
		t.Errorf("first run = %s %d-%d, want pt 1-2", runs[0].Lang, runs[0].Start, runs[0].End)
	}
	if runs[1].Lang != "pt-BR" || runs[1].Start != 3 {
		t.Errorf("second run = %s %d-%d, want pt-BR 3-4", runs[1].Lang, runs[1].Start, runs[1].End)
	}
}

func TestAVaguerScriptSignalStillContinuesASection(t *testing.T) {
	// The converse must keep working: the script signal cannot express a region,
	// so when it fills a page inside a ZH-HK section that is still one section.
	pages := []doc.Page{
		page(1, "ZH-HK", doc.ScriptHan, 1, "用戶手冊"),
		page(2, "", doc.ScriptHan, 2, "用戶手冊"),
		page(3, "ZH-HK", doc.ScriptHan, 3, "用戶手冊"),
	}
	bySource := map[doc.Source][]doc.Run{
		doc.SourcePageTag: {
			{Source: doc.SourcePageTag, Code: "ZH-HK", Lang: "zh-HK", Start: 1, End: 1, Confidence: 1},
			{Source: doc.SourcePageTag, Code: "ZH-HK", Lang: "zh-HK", Start: 3, End: 3, Confidence: 1},
		},
		doc.SourceScript: {
			{Source: doc.SourceScript, Code: "ZH", Lang: "zh", Start: 1, End: 3, Confidence: 0.7},
		},
	}

	runs := doc.Reconcile(pages, bySource)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d: %+v", len(runs), runs)
	}
	if runs[0].Lang != "zh-HK" || runs[0].Pages() != 3 {
		t.Errorf("run = %s covering %d pages, want zh-HK covering 3", runs[0].Lang, runs[0].Pages())
	}
}

func TestAWhollyDisputedShortRunIsFlagged(t *testing.T) {
	// Interior-only flagging used strict inequality, so a run of one or two pages
	// had no interior and a flat contradiction about the entire section was
	// dropped — the silent resolution the design forbids. Short sections are real.
	pages := []doc.Page{
		page(1, "DA", doc.ScriptLatin, 1, "dansk"),
		page(2, "DA", doc.ScriptLatin, 2, "dansk"),
	}
	bySource := map[doc.Source][]doc.Run{
		doc.SourcePageTag: {{Source: doc.SourcePageTag, Code: "DA", Lang: "da", Start: 1, End: 2, Confidence: 1}},
		doc.SourceIndex:   {{Source: doc.SourceIndex, Code: "FI", Lang: "fi", Start: 1, End: 2, Confidence: 0.6}},
	}

	runs := doc.Reconcile(pages, bySource)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if !runs[0].Conflict {
		t.Errorf("a two-page run contradicted on every page was not flagged: %q", runs[0].Note)
	}
	if !strings.Contains(runs[0].Note, "FI") {
		t.Errorf("the note should name the disagreeing claim, got %q", runs[0].Note)
	}
}

func TestBridgingDoesNotInventAConflict(t *testing.T) {
	// Bridging extends a run past a page that used to be its edge, and interior
	// flagging then saw that page as interior. Identical evidence produced
	// opposite verdicts depending on whether a photograph happened to sit inside
	// the section, which is exactly the false positive the flag exists to avoid.
	bySource := func() map[doc.Source][]doc.Run {
		return map[doc.Source][]doc.Run{
			doc.SourcePageTag: {
				{Source: doc.SourcePageTag, Code: "IT", Lang: "it", Start: 1, End: 3, Confidence: 1},
				{Source: doc.SourcePageTag, Code: "IT", Lang: "it", Start: 5, End: 6, Confidence: 1},
			},
			// The ordinary one-page-early index claim, landing on page 3.
			doc.SourceIndex: {{Source: doc.SourceIndex, Code: "ES", Lang: "es", Start: 3, End: 3, Confidence: 0.6}},
		}
	}

	// Control: the same evidence with no gap to bridge.
	control := []doc.Page{
		page(1, "IT", doc.ScriptLatin, 1, "italiano"),
		page(2, "IT", doc.ScriptLatin, 2, "italiano"),
		page(3, "IT", doc.ScriptLatin, 3, "italiano"),
	}
	controlRuns := doc.Reconcile(control, map[doc.Source][]doc.Run{
		doc.SourcePageTag: {{Source: doc.SourcePageTag, Code: "IT", Lang: "it", Start: 1, End: 3, Confidence: 1}},
		doc.SourceIndex:   {{Source: doc.SourceIndex, Code: "ES", Lang: "es", Start: 3, End: 3, Confidence: 0.6}},
	})
	if len(controlRuns) != 1 || controlRuns[0].Conflict {
		t.Fatalf("control: an edge dispute should not flag a conflict, got %+v", controlRuns)
	}

	// Same evidence, plus an illustration page inside the section.
	withGap := []doc.Page{
		page(1, "IT", doc.ScriptLatin, 1, "italiano"),
		page(2, "IT", doc.ScriptLatin, 2, "italiano"),
		page(3, "IT", doc.ScriptLatin, 3, "italiano"),
		thinPage(4),
		page(5, "IT", doc.ScriptLatin, 5, "italiano"),
		page(6, "IT", doc.ScriptLatin, 6, "italiano"),
	}
	runs := doc.Reconcile(withGap, bySource())
	if len(runs) != 1 {
		t.Fatalf("expected the illustration page to be bridged, got %d runs", len(runs))
	}
	if runs[0].Conflict {
		t.Errorf("bridging turned an edge dispute into a conflict: %q", runs[0].Note)
	}
}

func TestIndexDisagreementNoteQuotesThePrintedPage(t *testing.T) {
	// The note said "the printed index places X at page N" but passed the
	// folio-resolved PDF page — a number the index never showed, which the reader
	// cannot check against their own copy.
	pages := []doc.Page{
		page(1, "", doc.ScriptLatin, 0, "cover"),
		page(2, "", doc.ScriptLatin, 0, "cover"),
		page(3, "NL", doc.ScriptLatin, 1, "nederlands"),
		page(4, "NL", doc.ScriptLatin, 2, "nederlands"),
		page(5, "NL", doc.ScriptLatin, 3, "nederlands"),
		page(6, "NL", doc.ScriptLatin, 4, "nederlands"),
		page(7, "NL", doc.ScriptLatin, 5, "nederlands"),
		page(8, "NL", doc.ScriptLatin, 6, "nederlands"),
	}
	printed := 1
	bySource := map[doc.Source][]doc.Run{
		doc.SourcePageTag: {{Source: doc.SourcePageTag, Code: "NL", Lang: "nl", Start: 3, End: 8, Confidence: 1}},
		// The index claims printed page 1, which resolves to PDF page 3 — but it is
		// recorded here as starting far away, so the tolerance is exceeded.
		doc.SourceIndex: {{
			Source: doc.SourceIndex, Code: "NL", Lang: "nl",
			Start: 7, End: 8, PrintedPage: &printed, Confidence: 0.6,
		}},
	}

	for _, r := range doc.Reconcile(pages, bySource) {
		if r.Code != "NL" || !r.Conflict {
			continue
		}
		if !strings.Contains(r.Note, "page 1") {
			t.Errorf("note should quote the printed page 1, got %q", r.Note)
		}
		return
	}
	t.Fatal("expected the NL run to be flagged as disagreeing with the index")
}
