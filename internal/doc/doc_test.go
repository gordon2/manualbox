package doc_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
)

// Hermetic tests for the language signals and their reconciliation, built from
// synthetic pages. No PDF and no poppler: these state the rules, and
// fixture_test.go checks them against a real 34-language manual.

// page builds a synthetic page. body is repeated so the page clears the
// text-layer floor, since a page with almost no text is deliberately ignored.
func page(no int, tag, script string, folio int, body string) doc.Page {
	text := strings.Repeat(body+" ", 30)
	p := doc.Page{
		No: no, Text: text, Chars: len([]rune(text)),
		Script: script, Tag: tag,
	}
	if tag != "" {
		p.TagCandidates = []string{tag}
	}
	if folio > 0 {
		p.Folio = &folio
	}
	return p
}

// thinPage is a page with too little text to classify: a full-page illustration.
func thinPage(no int) doc.Page {
	return doc.Page{No: no, Text: "12", Chars: 2}
}

func TestTagRunsNeedsTwoConsecutivePages(t *testing.T) {
	// A contents page lists every language code in the same position the per-page
	// tab occupies, producing one-page runs. Requiring two consecutive pages is
	// what keeps a contents page from becoming a section — measured on a real
	// manual, where it produced three bogus sections.
	pages := []doc.Page{
		page(1, "EN", doc.ScriptLatin, 0, "contents"), // a contents page
		page(2, "DE", doc.ScriptLatin, 1, "guten tag"),
		page(3, "DE", doc.ScriptLatin, 2, "guten tag"),
	}

	runs := doc.TagRuns(pages, nil)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d: %+v", len(runs), runs)
	}
	if runs[0].Code != "DE" || runs[0].Start != 2 || runs[0].End != 3 {
		t.Errorf("run = %s %d-%d, want DE 2-3", runs[0].Code, runs[0].Start, runs[0].End)
	}
}

func TestContentsPageAdjacentToItsFirstSectionIsExcluded(t *testing.T) {
	// The run-length guard is not enough on its own. A contents page listing EN
	// first, sitting immediately before the EN section, is contiguous with it and
	// gets absorbed — inflating that section by one page. A contents table is not a
	// page of any language, so it is excluded outright.
	contents := doc.Page{No: 1}
	contents.Text = "Contents\nEN\nUser Manual\n1\nDE\nBenutzerhandbuch\n4\nFR\nManuel\n7\n"
	contents.Chars = len([]rune(contents.Text))
	contents.Tag = "EN" // what the naive reading of the first lines produces
	contents.TagCandidates = []string{"EN", "DE", "FR"}

	pages := []doc.Page{
		contents,
		page(2, "EN", doc.ScriptLatin, 1, "english"),
		page(3, "EN", doc.ScriptLatin, 2, "english"),
		page(4, "DE", doc.ScriptLatin, 3, "german"),
		page(5, "DE", doc.ScriptLatin, 4, "german"),
	}

	if !doc.IsContentsPage(&contents) {
		t.Fatal("the contents page was not recognised as one")
	}

	tags := doc.EffectiveTags(pages, doc.IndexCodes(doc.IndexRuns(pages)))
	if tags[0] != "" {
		t.Errorf("the contents page kept the tag %q", tags[0])
	}

	for _, r := range doc.TagRuns(pages, tags) {
		if r.Contains(1) {
			t.Errorf("run %s %d-%d absorbed the contents page", r.Code, r.Start, r.End)
		}
		if r.Code == "EN" && r.Pages() != 2 {
			t.Errorf("EN covers %d pages, want 2", r.Pages())
		}
	}
}

func TestTagRunsCorroboratedByScriptRankHigher(t *testing.T) {
	greek := []doc.Page{
		page(1, "EL", doc.ScriptGreek, 1, "οδηγίες"),
		page(2, "EL", doc.ScriptGreek, 2, "οδηγίες"),
	}
	runs := doc.TagRuns(greek, nil)
	if len(runs) != 1 || runs[0].Confidence != 1.0 {
		t.Fatalf("script-corroborated tag should have confidence 1.0, got %+v", runs)
	}

	// A tag the script contradicts is disbelieved rather than trusted: a run
	// tagged EL whose pages are Cyrillic is not Greek.
	mismatched := []doc.Page{
		page(1, "EL", doc.ScriptCyrillic, 1, "инструкция"),
		page(2, "EL", doc.ScriptCyrillic, 2, "инструкция"),
	}
	runs = doc.TagRuns(mismatched, nil)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].Confidence > 0.3 {
		t.Errorf("tag contradicted by script should be low confidence, got %.1f: %s",
			runs[0].Confidence, runs[0].Note)
	}
}

func TestEffectiveTagsNarrowsByIndexVocabulary(t *testing.T) {
	// Searching a whole page for a code is necessary for right-to-left layouts but
	// unsafe on its own: NO, IT, IS and BE are all valid language codes and
	// ordinary English words. A candidate is adopted only if the document's own
	// contents table lists that code.
	pages := []doc.Page{
		{No: 1, Chars: 500, TagCandidates: []string{"NO"}}, // stray word in a table
		{No: 2, Chars: 500, TagCandidates: []string{"AR"}},
	}

	tags := doc.EffectiveTags(pages, map[string]bool{"AR": true})
	if tags[0] != "" {
		t.Errorf("page 1 adopted %q, but NO is not in the index vocabulary", tags[0])
	}
	if tags[1] != "AR" {
		t.Errorf("page 2 tag = %q, want AR", tags[1])
	}
}

func TestEffectiveTagsPrefersTheConservativeReading(t *testing.T) {
	// A code found at the top of the page is trusted without needing the index's
	// blessing, so a manual with no parseable contents table still works.
	pages := []doc.Page{{No: 1, Chars: 500, Tag: "SV", TagCandidates: []string{"SV"}}}
	if got := doc.EffectiveTags(pages, nil); got[0] != "SV" {
		t.Errorf("tag = %q, want SV even with an empty vocabulary", got[0])
	}
}

func TestIndexRunsResolveClaimsThroughFolios(t *testing.T) {
	// The index claims printed page numbers; folios printed on the pages
	// themselves are what convert a claim into a PDF page, with no global offset
	// assumed. Here the front matter is 2 pages, so folio n is PDF page n+2.
	contents := page(1, "", doc.ScriptLatin, 0,
		"") // replaced below
	contents.Text = "Contents\nEN\nUser Manual\n1\nDE\nBenutzerhandbuch\n3\nFR\nManuel\n5\n"
	contents.Chars = len([]rune(contents.Text))

	pages := []doc.Page{
		contents,
		page(2, "", doc.ScriptLatin, 0, "cover"),
		page(3, "EN", doc.ScriptLatin, 1, "english"),
		page(4, "EN", doc.ScriptLatin, 2, "english"),
		page(5, "DE", doc.ScriptLatin, 3, "german"),
		page(6, "DE", doc.ScriptLatin, 4, "german"),
		page(7, "FR", doc.ScriptLatin, 5, "french"),
		page(8, "FR", doc.ScriptLatin, 6, "french"),
	}

	runs := doc.IndexRuns(pages)
	if len(runs) != 3 {
		t.Fatalf("expected 3 index entries, got %d: %+v", len(runs), runs)
	}
	want := map[string]int{"EN": 3, "DE": 5, "FR": 7}
	for _, r := range runs {
		if got := want[r.Code]; r.Start != got {
			t.Errorf("%s resolved to page %d, want %d", r.Code, r.Start, got)
		}
		if r.Title == "" {
			t.Errorf("%s carries no title; titles are the index's unique contribution", r.Code)
		}
	}
}

func TestContentsPageFoliosDoNotResolveIndexClaims(t *testing.T) {
	// A contents page's trailing number is an index entry's page reference, not a
	// folio — the page is listing "FR ... 5", not declaring itself page 5. Treating
	// it as one maps a claimed page onto the contents page itself: on a real
	// document, page 2 ends with "194", so the Arabic section's claimed start of
	// 194 resolved to page 2 and produced a one-page Arabic section at the front.
	//
	// The contents page therefore carries a folio here, exactly as the extractor
	// would derive one, because that is the condition that triggers the bug.
	contentsFolio := 5
	contents := doc.Page{No: 1, Folio: &contentsFolio}
	contents.Text = "Contents\nEN\nUser Manual\n1\nDE\nBenutzerhandbuch\n3\nFR\nManuel\n5\n"
	contents.Chars = len([]rune(contents.Text))

	pages := []doc.Page{
		contents,
		page(2, "", doc.ScriptLatin, 0, "cover"),
		page(3, "EN", doc.ScriptLatin, 1, "english"),
		page(4, "EN", doc.ScriptLatin, 2, "english"),
		page(5, "DE", doc.ScriptLatin, 3, "german"),
		page(6, "DE", doc.ScriptLatin, 4, "german"),
		page(7, "FR", doc.ScriptLatin, 5, "french"),
		page(8, "FR", doc.ScriptLatin, 6, "french"),
	}

	for _, r := range doc.IndexRuns(pages) {
		if r.Code != "FR" {
			continue
		}
		if r.Start == 1 {
			t.Fatal("the French claim resolved onto the contents page, whose trailing " +
				"number is an index reference rather than a folio")
		}
		if r.Start != 7 {
			t.Errorf("French resolved to page %d, want 7", r.Start)
		}
		return
	}
	t.Fatal("no French index entry was parsed")
}

func TestIndexRunsRejectClaimOnTheWrongScript(t *testing.T) {
	// A real manual's contents table lists Czech at a page that is actually
	// Arabic — a typo the manufacturer ships. Resolving it faithfully produces a
	// Czech claim over Arabic pages, so script has to veto it.
	contents := doc.Page{No: 1}
	contents.Text = "Contents\nCZ\nUzivatelska prirucka\n5\nAR\nArabic manual\n7\nEN\nUser Manual\n9\n"
	contents.Chars = len([]rune(contents.Text))

	pages := []doc.Page{
		contents,
		page(2, "", doc.ScriptLatin, 0, "cover"),
		page(3, "AR", doc.ScriptArabic, 5, "عربي"),
		page(4, "AR", doc.ScriptArabic, 6, "عربي"),
		page(5, "AR", doc.ScriptArabic, 7, "عربي"),
		page(6, "EN", doc.ScriptLatin, 9, "english"),
	}

	for _, r := range doc.IndexRuns(pages) {
		if r.Code != "CZ" {
			continue
		}
		if r.Start != 0 {
			t.Errorf("Czech claim resolved to page %d, which is Arabic script; it should contribute no boundary", r.Start)
		}
		// The note must state what was observed, so a user can judge it themselves:
		// which language was claimed, what is actually on that page, and the
		// consequence.
		for _, want := range []string{"CZ", "Arabic", "no boundary"} {
			if !strings.Contains(r.Note, want) {
				t.Errorf("the note should mention %q to explain the rejection, got %q", want, r.Note)
			}
		}
		return
	}
	t.Fatal("the Czech entry was dropped entirely; it should be kept for its label and title")
}

func TestReconcilePrefersTheTagOverTheIndex(t *testing.T) {
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
	if runs[0].Lang != "da" {
		t.Errorf("lang = %q, want da: the printed tag outranks the index", runs[0].Lang)
	}
}

func TestReconcileRejectsALanguageItsScriptForbids(t *testing.T) {
	// A printed index's final entry claims every remaining page, which on a real
	// manual swallowed an English back cover into the Japanese section. Japanese
	// cannot be written in the Latin alphabet, so the claim must not win.
	pages := []doc.Page{
		page(1, "JA", doc.ScriptKana, 1, "説明書です"),
		page(2, "JA", doc.ScriptKana, 2, "説明書です"),
		page(3, "", doc.ScriptLatin, 0, "Made in China. For support contact us."),
	}
	bySource := map[doc.Source][]doc.Run{
		doc.SourcePageTag: {{Source: doc.SourcePageTag, Code: "JA", Lang: "ja", Start: 1, End: 2, Confidence: 1}},
		doc.SourceIndex:   {{Source: doc.SourceIndex, Code: "JA", Lang: "ja", Start: 1, End: 3, Confidence: 0.6}},
	}

	runs := doc.Reconcile(pages, bySource)

	// The negative assertion alone is vacuous: it passes if Reconcile returns
	// nothing at all, which a total failure would. Verified by gutting Reconcile —
	// this test went green. So assert what must still be true as well.
	labelled := 0
	for _, r := range runs {
		if r.Contains(3) {
			t.Errorf("Latin-script page 3 was labelled %s (%s)", r.Code, r.Lang)
		}
		for p := r.Start; p <= r.End; p++ {
			if p == 1 || p == 2 {
				labelled++
			}
		}
		if r.Lang != "ja" {
			t.Errorf("run %s has lang %q, want ja", r.Code, r.Lang)
		}
	}
	if labelled != 2 {
		t.Errorf("%d of pages 1-2 were labelled Japanese, want 2 — rejecting page 3 must not cost the real section", labelled)
	}
}

func TestReconcileBridgesALowTextPage(t *testing.T) {
	// A full-page illustration between two pages of the same language belongs to
	// that language. Splitting the section there produced two spans whose page
	// totals still summed correctly, which is how it escaped notice.
	pages := []doc.Page{
		page(1, "JA", doc.ScriptKana, 1, "説明書"),
		page(2, "JA", doc.ScriptKana, 2, "説明書"),
		thinPage(3),
		page(4, "JA", doc.ScriptKana, 4, "説明書"),
	}
	bySource := map[doc.Source][]doc.Run{
		doc.SourcePageTag: {
			{Source: doc.SourcePageTag, Code: "JA", Lang: "ja", Start: 1, End: 2, Confidence: 1},
			{Source: doc.SourcePageTag, Code: "JA", Lang: "ja", Start: 4, End: 4, Confidence: 1},
		},
	}

	runs := doc.Reconcile(pages, bySource)
	if len(runs) != 1 {
		t.Fatalf("expected the illustration page to be bridged into 1 run, got %d: %+v", len(runs), runs)
	}
	if runs[0].Start != 1 || runs[0].End != 4 {
		t.Errorf("run = %d-%d, want 1-4", runs[0].Start, runs[0].End)
	}
}

func TestReconcileFlagsInteriorDisagreementsOnly(t *testing.T) {
	// An index start that is one page off disagrees about exactly one page: the
	// last of the previous section. That is boundary noise, reported once per
	// section elsewhere. A disagreement in the middle of a run is a real conflict.
	pages := []doc.Page{
		page(1, "IT", doc.ScriptLatin, 1, "italiano"),
		page(2, "IT", doc.ScriptLatin, 2, "italiano"),
		page(3, "IT", doc.ScriptLatin, 3, "italiano"),
	}

	boundary := map[doc.Source][]doc.Run{
		doc.SourcePageTag: {{Source: doc.SourcePageTag, Code: "IT", Lang: "it", Start: 1, End: 3, Confidence: 1}},
		doc.SourceIndex:   {{Source: doc.SourceIndex, Code: "ES", Lang: "es", Start: 3, End: 3, Confidence: 0.6}},
	}
	for _, r := range doc.Reconcile(pages, boundary) {
		if r.Conflict {
			t.Errorf("a one-page disagreement at the run's edge should not flag a conflict: %s", r.Note)
		}
	}

	interior := map[doc.Source][]doc.Run{
		doc.SourcePageTag: {{Source: doc.SourcePageTag, Code: "IT", Lang: "it", Start: 1, End: 3, Confidence: 1}},
		doc.SourceIndex:   {{Source: doc.SourceIndex, Code: "ES", Lang: "es", Start: 2, End: 2, Confidence: 0.6}},
	}
	runs := doc.Reconcile(pages, interior)
	if len(runs) != 1 || !runs[0].Conflict {
		t.Errorf("a disagreement inside the run must be flagged: %+v", runs)
	}
	if !strings.Contains(runs[0].Note, "index") {
		t.Errorf("the note should name the disagreeing signal, got %q", runs[0].Note)
	}
}

func TestReconcileLeavesUnknowableLanguagesUnlabelled(t *testing.T) {
	// A page nobody can name stays unnamed. Guessing would be worse than
	// reporting that a statistical detector is needed.
	pages := []doc.Page{page(1, "", doc.ScriptLatin, 1, "some latin prose")}
	if runs := doc.Reconcile(pages, map[doc.Source][]doc.Run{}); len(runs) != 0 {
		t.Errorf("expected no runs, got %+v", runs)
	}

	// The control matters: an empty result also occurs when reconciliation is
	// broken outright, so prove the same page IS labelled once a signal names it.
	// Without this the assertion above passes against a Reconcile that returns
	// nothing for every input.
	withSignal := doc.Reconcile(pages, map[doc.Source][]doc.Run{
		doc.SourcePageTag: {{Source: doc.SourcePageTag, Code: "EN", Lang: "en", Start: 1, End: 1, Confidence: 1}},
	})
	if len(withSignal) != 1 || withSignal[0].Lang != "en" {
		t.Fatalf("the same page with a signal should be labelled en, got %+v", withSignal)
	}
}

func TestNormalizeCodeHandlesLabelsManualsActuallyPrint(t *testing.T) {
	tests := []struct{ in, want string }{
		{"EN", "en"},
		{"UA", "uk"}, // country code used for Ukrainian
		{"CZ", "cs"}, // country code used for Czech
		{"ZH-HK", "zh-HK"},
		{"DK", "da"},
		{"JP", "ja"},
		{"de", "de"},
	}
	for _, tc := range tests {
		got, ok := doc.NormalizeCode(tc.in)
		if !ok || got != tc.want {
			t.Errorf("NormalizeCode(%q) = %q, %t; want %q, true", tc.in, got, ok, tc.want)
		}
	}
	if _, ok := doc.NormalizeCode("QQ"); ok {
		t.Error("NormalizeCode accepted QQ, which is not a language")
	}
}

func TestDominantScriptSeparatesJapaneseFromChinese(t *testing.T) {
	// Japanese mixes kanji with kana and the kanji usually outnumber the kana, so
	// a plain maximum reports Han and loses the distinction.
	if got := doc.DominantScript("取扱説明書をお読みください"); got != doc.ScriptKana {
		t.Errorf("Japanese text = %q, want %q", got, doc.ScriptKana)
	}
	if got := doc.DominantScript("用戶手冊請仔細閱讀本手冊"); got != doc.ScriptHan {
		t.Errorf("Chinese text = %q, want %q", got, doc.ScriptHan)
	}
	if got := doc.DominantScript("123 456 !!!"); got != "" {
		t.Errorf("text with no letters = %q, want empty", got)
	}
}

func TestScriptCompatibleChecksBothDirections(t *testing.T) {
	tests := []struct {
		script, lang string
		want         bool
	}{
		{doc.ScriptGreek, "el", true},
		{doc.ScriptGreek, "de", false}, // script rules the language out
		{doc.ScriptLatin, "ja", false}, // language rules the script out
		{doc.ScriptLatin, "de", true},
		{doc.ScriptLatin, "sr", true}, // Serbian is written in both alphabets
		{doc.ScriptCyrillic, "sr", true},
		{doc.ScriptKana, "ja", true},
		{"", "ja", true}, // no script evidence rules nothing out
	}
	for _, tc := range tests {
		if got := doc.ScriptCompatible(tc.script, tc.lang); got != tc.want {
			t.Errorf("ScriptCompatible(%q, %q) = %t, want %t", tc.script, tc.lang, got, tc.want)
		}
	}
}

func TestScopeIntersectsWithTheHousehold(t *testing.T) {
	res := &doc.Result{
		Info: doc.Info{Pages: 100},
		Pages: []doc.Page{
			{No: 1, Chars: 1000}, {No: 2, Chars: 1000},
			{No: 3, Chars: 1000}, {No: 4, Chars: 1000},
		},
		Runs: []doc.Run{
			{Source: doc.SourceReconciled, Code: "EN", Lang: "en", Start: 1, End: 2},
			{Source: doc.SourceReconciled, Code: "ZH-HK", Lang: "zh-HK", Start: 3, End: 4},
		},
	}

	scope := res.ScopeFor([]string{"en"})
	if len(scope.Languages) != 1 || scope.Languages[0].Lang != "en" {
		t.Fatalf("in scope = %+v, want just en", scope.Languages)
	}
	if scope.Pages != 2 {
		t.Errorf("scope pages = %d, want 2", scope.Pages)
	}
	if scope.Chars != 2000 {
		t.Errorf("scope chars = %d, want 2000", scope.Chars)
	}
	if len(scope.OtherLanguages) != 1 {
		t.Errorf("other languages = %+v, want zh-HK reported so it can be imported later", scope.OtherLanguages)
	}

	// A regional variant satisfies a household that reads the base language.
	if scope := res.ScopeFor([]string{"zh"}); scope.Pages != 2 {
		t.Errorf("zh should match zh-HK, got %d pages", scope.Pages)
	}
}

// --- edge cases in run construction, pinned deliberately ---

func TestReconcileBridgesAChainOfLowTextPages(t *testing.T) {
	// Three runs of the same language separated by thin pages must collapse to
	// one, not two. Merging keeps a pointer into the output slice and remaps a
	// dispute map keyed by run index, so a chain is where that bookkeeping would
	// go wrong.
	pages := []doc.Page{
		page(1, "JA", doc.ScriptKana, 1, "説明書"),
		thinPage(2),
		page(3, "JA", doc.ScriptKana, 3, "説明書"),
		thinPage(4),
		page(5, "JA", doc.ScriptKana, 5, "説明書"),
	}
	bySource := map[doc.Source][]doc.Run{
		doc.SourcePageTag: {
			{Source: doc.SourcePageTag, Code: "JA", Lang: "ja", Start: 1, End: 1, Confidence: 1},
			{Source: doc.SourcePageTag, Code: "JA", Lang: "ja", Start: 3, End: 3, Confidence: 1},
			{Source: doc.SourcePageTag, Code: "JA", Lang: "ja", Start: 5, End: 5, Confidence: 1},
		},
	}

	runs := doc.Reconcile(pages, bySource)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run after bridging a chain, got %d: %+v", len(runs), runs)
	}
	if runs[0].Start != 1 || runs[0].End != 5 {
		t.Errorf("run = %d-%d, want 1-5", runs[0].Start, runs[0].End)
	}
}

func TestTwoPageTagVariantsAreTwoSectionsInEitherOrder(t *testing.T) {
	// This test previously asserted the opposite, and was wrong. It gave both
	// pages a *page tag* — one ZH, one ZH-HK — and required them to merge into a
	// single section, which encoded the very defect review later found: the
	// document naming two variants is the document distinguishing two sections,
	// and merging them loses a real boundary.
	//
	// The case the merge rule genuinely exists for is a vaguer *script* signal
	// filling a gap, which cannot express a region at all. That lives in
	// reconcile_test.go as TestAVaguerScriptSignalStillContinuesASection.
	for _, name := range []string{"specific first", "base first"} {
		t.Run(name, func(t *testing.T) {
			pages := []doc.Page{
				page(1, "", doc.ScriptHan, 1, "用戶手冊"),
				page(2, "", doc.ScriptHan, 2, "用戶手冊"),
			}
			specific := doc.Run{Source: doc.SourcePageTag, Code: "ZH-HK", Lang: "zh-HK", Start: 1, End: 1, Confidence: 1}
			base := doc.Run{Source: doc.SourcePageTag, Code: "CN", Lang: "zh", Start: 2, End: 2, Confidence: 1}
			if name == "base first" {
				specific.Start, specific.End = 2, 2
				base.Start, base.End = 1, 1
			}

			runs := doc.Reconcile(pages, map[doc.Source][]doc.Run{
				doc.SourcePageTag: {base, specific},
			})
			if len(runs) != 2 {
				t.Fatalf("expected 2 runs — two printed tags name two variants, got %d: %+v", len(runs), runs)
			}
			for _, r := range runs {
				if r.Pages() != 1 {
					t.Errorf("run %s covers %d pages, want 1", r.Code, r.Pages())
				}
			}
		})
	}
}

func TestIndexRunsNeverProduceAnInvertedRange(t *testing.T) {
	// The schema requires pdf_end >= pdf_start, and a violation fails the whole
	// probe — that already happened once. An index whose entries resolve out of
	// order relative to their claimed pages is the way to provoke it.
	contents := doc.Page{No: 1}
	contents.Text = "Contents\nEN\nEnglish\n9\nDE\nGerman\n1\nFR\nFrench\n5\n"
	contents.Chars = len([]rune(contents.Text))

	pages := []doc.Page{contents}
	for i, folio := range []int{1, 3, 5, 7, 9} {
		pages = append(pages, page(i+2, "", doc.ScriptLatin, folio, "body text here"))
	}

	for _, r := range doc.IndexRuns(pages) {
		if r.Start == 0 {
			continue // a claim that fixed no boundary is allowed
		}
		if r.End < r.Start {
			t.Errorf("%s produced an inverted range %d-%d", r.Code, r.Start, r.End)
		}
	}
}

func TestDegenerateInputsDoNotPanic(t *testing.T) {
	// Empty, single-page, and all-thin documents must return an empty map rather
	// than panicking or inventing a run.
	cases := map[string][]doc.Page{
		"no pages":       {},
		"one thin page":  {thinPage(1)},
		"all thin":       {thinPage(1), thinPage(2), thinPage(3)},
		"one good page":  {page(1, "EN", doc.ScriptLatin, 1, "english text")},
		"non-contiguous": {page(1, "EN", doc.ScriptLatin, 1, "english"), page(9, "EN", doc.ScriptLatin, 9, "english")},
	}
	for name, pages := range cases {
		t.Run(name, func(t *testing.T) {
			runs := doc.Reconcile(pages, map[doc.Source][]doc.Run{})
			for _, r := range runs {
				if r.End < r.Start || r.Start < 1 {
					t.Errorf("invalid run %+v", r)
				}
			}
			if got := doc.IndexRuns(pages); got != nil {
				for _, r := range got {
					if r.Start != 0 && r.End < r.Start {
						t.Errorf("invalid index run %+v", r)
					}
				}
			}
			_ = doc.TagRuns(pages, doc.EffectiveTags(pages, nil))
			_ = doc.ScriptRuns(pages)
		})
	}
}

func TestNonContiguousPagesAreTwoRuns(t *testing.T) {
	// A document whose page 1 and page 9 share a tag has two runs, not one
	// nine-page run — bridging only applies across pages that exist and are thin.
	pages := []doc.Page{
		page(1, "EN", doc.ScriptLatin, 1, "english"),
		page(9, "EN", doc.ScriptLatin, 9, "english"),
	}
	bySource := map[doc.Source][]doc.Run{
		doc.SourcePageTag: {
			{Source: doc.SourcePageTag, Code: "EN", Lang: "en", Start: 1, End: 1, Confidence: 1},
			{Source: doc.SourcePageTag, Code: "EN", Lang: "en", Start: 9, End: 9, Confidence: 1},
		},
	}
	runs := doc.Reconcile(pages, bySource)
	if len(runs) != 2 {
		t.Errorf("expected 2 runs across a real gap, got %d: %+v", len(runs), runs)
	}
}

func TestARejectedIndexClaimEndsNothing(t *testing.T) {
	// A claim the parser refused is not a boundary. This index lists AR at printed
	// page 5, CZ at 7 and EN at 9; the Czech claim resolves onto a page that is
	// Arabic script and is vetoed, exactly as a real manual's Czech-inside-Arabic
	// typo is. Ending the Arabic section one page before that rejected claim cut the
	// section in half: 3-4 instead of 3-6.
	contents := doc.Page{No: 1}
	contents.Text = "Contents\nAR\nArabic manual\n5\nCZ\nUzivatelska prirucka\n7\nEN\nUser Manual\n9\n"
	contents.Chars = len([]rune(contents.Text))

	pages := []doc.Page{
		contents,
		page(2, "", doc.ScriptLatin, 0, "cover"),
		page(3, "AR", doc.ScriptArabic, 5, "عربي"),
		page(4, "AR", doc.ScriptArabic, 6, "عربي"),
		page(5, "AR", doc.ScriptArabic, 7, "عربي"),
		page(6, "AR", doc.ScriptArabic, 8, "عربي"),
		page(7, "EN", doc.ScriptLatin, 9, "english"),
		page(8, "EN", doc.ScriptLatin, 10, "english"),
	}

	byCode := make(map[string]doc.Run, 3)
	for _, r := range doc.IndexRuns(pages) {
		byCode[r.Code] = r
	}
	ar, ok := byCode["AR"]
	if !ok {
		t.Fatal("no Arabic index entry was parsed")
	}
	if ar.Start != 3 || ar.End != 6 {
		t.Errorf("Arabic = %d-%d, want 3-6: the section ends where the next *accepted* claim begins",
			ar.Start, ar.End)
	}
	if cz := byCode["CZ"]; cz.Start != 0 {
		t.Errorf("the Czech claim resolved to page %d; that page is Arabic, so it fixes no boundary", cz.Start)
	}
}

func TestIndexLookaheadStopsAtAThreeLetterLabel(t *testing.T) {
	// Manufacturers print three-letter labels — POR, SPA, CHI, SRB — and a code line
	// is otherwise recognised only as XX or XX-XX. Such a line was read as title text
	// and the walk continued into the *following* entry's page number, so EN claimed
	// the Portuguese section's start page and carried its title along with it.
	contents := doc.Page{No: 1}
	contents.Text = "Contents\n" +
		"EN\nUser Manual\nPOR\nManual do utilizador\n17\n" +
		"FR\nManuel d'utilisation\n33\n" +
		"DE\nBenutzerhandbuch\n49\n" +
		"ES\nManual de usuario\n65\n"
	contents.Chars = len([]rune(contents.Text))

	pages := []doc.Page{
		contents,
		page(2, "", doc.ScriptLatin, 0, "body"),
		page(3, "", doc.ScriptLatin, 0, "body"),
	}

	byCode := make(map[string]doc.Run, 4)
	for _, r := range doc.IndexRuns(pages) {
		byCode[r.Code] = r
		if strings.Contains(r.Title, "utilizador") {
			t.Errorf("%s absorbed the Portuguese title: %q", r.Code, r.Title)
		}
	}
	if en, ok := byCode["EN"]; ok && en.PrintedPage != nil && *en.PrintedPage == 17 {
		t.Error("EN claims printed page 17, which belongs to the entry listed between them")
	}
	// The entries after the three-letter label must still parse; breaking the walk
	// must not cost the rest of the table.
	for code, want := range map[string]int{"FR": 33, "DE": 49, "ES": 65} {
		r, ok := byCode[code]
		if !ok {
			t.Errorf("%s was not parsed at all", code)
			continue
		}
		if r.PrintedPage == nil || *r.PrintedPage != want {
			t.Errorf("%s claims %v, want printed page %d", code, r.PrintedPage, want)
		}
	}
}

func TestATagOnALatinPageIsNotCorroboratedByIt(t *testing.T) {
	// Two pages tagged JA whose CJK glyphs failed to extract leave nothing but Latin
	// furniture behind. A Latin page permits any language — that is what the script
	// signal means by "no information" — so a one-directional check read it as
	// corroboration and stored confidence 1.0, while reconciliation, which checks
	// both directions, discarded the run and left the section unlabelled. Maximum
	// confidence for a section nothing believes in is the worst of both answers.
	pages := []doc.Page{
		page(1, "JA", doc.ScriptLatin, 1, "L40 Ultra"),
		page(2, "JA", doc.ScriptLatin, 2, "L40 Ultra"),
	}

	runs := doc.TagRuns(pages, nil)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d: %+v", len(runs), runs)
	}
	if runs[0].Confidence > 0.3 {
		t.Errorf("confidence = %.1f (%s); Japanese is not written in the Latin alphabet",
			runs[0].Confidence, runs[0].Note)
	}
}

func TestIndexEntriesClaimingOnePageKeepPrintedOrder(t *testing.T) {
	// Two entries claiming the same printed page must stay in the order the contents
	// table printed them. Sorting them unstably left the order to the sort's
	// internals, and reconciliation keeps whichever claim it sees last — so which
	// language those pages were said to be in depended on nothing in the document.
	//
	// Thirteen entries, because Go's sort is stable in effect on very short slices;
	// the entry with a wildly out-of-order claim is the typo shape a real contents
	// table has.
	type listed struct {
		code, title string
		printed     int
	}
	table := []listed{
		{"EN", "User Manual", 1},
		{"DE", "Benutzerhandbuch", 17},
		{"FR", "Manuel d'utilisation", 33},
		{"IT", "Manuale utente", 49},
		{"ES", "Manual de usuario", 65},
		{"PL", "Instrukcja obslugi", 65}, // the same page as ES
		{"NL", "Handleiding", 97},
		{"NO", "Brukerhandbok", 113},
		{"SV", "Bruksanvisning", 129},
		{"EL", "Odigies chrisis", 7}, // a typo: page 7 is inside the English section
		{"PT", "Manual do utilizador", 161},
		{"HE", "Hebrew manual", 177},
		{"AR", "Arabic manual", 193},
	}

	var b strings.Builder
	b.WriteString("Contents\n")
	for _, e := range table {
		fmt.Fprintf(&b, "%s\n%s\n%d\n", e.code, e.title, e.printed)
	}
	contents := doc.Page{No: 1, Text: b.String()}
	contents.Chars = len([]rune(contents.Text))

	pages := []doc.Page{
		contents,
		page(2, "", doc.ScriptLatin, 0, "body"),
		page(3, "", doc.ScriptLatin, 0, "body"),
	}

	order := make(map[string]int, len(table))
	runs := doc.IndexRuns(pages)
	for i, r := range runs {
		order[r.Code] = i
	}
	es, haveES := order["ES"]
	pl, havePL := order["PL"]
	if !haveES || !havePL {
		t.Fatalf("expected both ES and PL entries, got %d runs", len(runs))
	}
	if es > pl {
		t.Errorf("ES and PL both claim page 65 and the table lists ES first, "+
			"but they came out in the order PL (%d) then ES (%d)", pl, es)
	}
}

func TestUnlabelledSeesPagesOutsideTheLabelledSpan(t *testing.T) {
	// A 120-page manual printing no tags and carrying no parseable index: script
	// names the Greek section and nothing else, because 25 languages share the Latin
	// alphabet. Deriving the content range from the runs made the count circular —
	// the range became 51-70 and the count ran only inside it, so a document of which
	// 100 pages are unnameable reported none. Unlabelled is the number that says
	// whether a statistical detector would earn its 118 MB, so a structural zero is
	// the one answer it must never give.
	pages := make([]doc.Page, 0, 120)
	for no := 1; no <= 120; no++ {
		if no >= 51 && no <= 70 {
			pages = append(pages, page(no, "", doc.ScriptGreek, no, "οδηγίες χρήσης"))
			continue
		}
		pages = append(pages, page(no, "", doc.ScriptLatin, no, "latin prose nobody can name"))
	}

	runs := doc.Reconcile(pages, map[doc.Source][]doc.Run{doc.SourceScript: doc.ScriptRuns(pages)})

	// The count is the point, and it must no longer be bounded by the range.
	if got := doc.CountUnlabelled(pages, runs); got != 100 {
		t.Errorf("unlabelled = %d, want 100", got)
	}
	// The range reports what the language map covers, which here really is only
	// the Greek section — the other 100 pages are content nobody could name, and
	// CountUnlabelled above is what says so.
	if start, end := doc.ContentRange(pages, runs); start != 51 || end != 70 {
		t.Errorf("content range = %d-%d, want 51-70", start, end)
	}
}

func TestContentRangeStillExcludesFrontMatterAndABackCover(t *testing.T) {
	// The runs stay the better evidence at both ends when what they exclude is
	// plausibly furniture: on the measured fixture six pages of front matter and an
	// English colophon carry text without being content. This is what counting
	// unlabelled pages honestly must not trade away.
	pages := make([]doc.Page, 0, 30)
	for no := 1; no <= 6; no++ {
		pages = append(pages, page(no, "", doc.ScriptLatin, 0, "front matter"))
	}
	for no := 7; no <= 29; no++ {
		pages = append(pages, page(no, "EL", doc.ScriptGreek, no-6, "οδηγίες"))
	}
	pages = append(pages, page(30, "", doc.ScriptLatin, 0, "Made in China"))

	runs := doc.Reconcile(pages, map[doc.Source][]doc.Run{
		doc.SourcePageTag: {{Source: doc.SourcePageTag, Code: "EL", Lang: "el", Start: 7, End: 29, Confidence: 1}},
	})
	start, end := doc.ContentRange(pages, runs)
	if start != 7 || end != 29 {
		t.Errorf("content range = %d-%d, want 7-29", start, end)
	}
	// Front matter and a colophon carry text and belong to no section, so they
	// are counted — deliberately. A small non-zero count is the honest answer;
	// suppressing it is what made the number structurally unable to report a
	// problem. Six front pages plus one back cover.
	if got := doc.CountUnlabelled(pages, runs); got != 7 {
		t.Errorf("unlabelled = %d, want 7 (six front-matter pages and a back cover)", got)
	}
}

func TestScopeCountsCharsOfEveryRunOfAHouseholdLanguage(t *testing.T) {
	// A language's summary carries one printed code — the most specific tag wins a
	// contest between them — while each run carries its own. Counting characters by
	// the summary's code therefore counted only the runs whose label happened to win:
	// the CN and ZH-HK sections both put their pages in scope, but only one of them
	// contributed any characters, and the character count is what the pre-flight gate
	// turns into a price.
	res := &doc.Result{
		Info: doc.Info{Pages: 6},
		Pages: []doc.Page{
			{No: 1, Chars: 1000}, {No: 2, Chars: 1000}, {No: 3, Chars: 1000},
			{No: 4, Chars: 1000}, {No: 5, Chars: 1000}, {No: 6, Chars: 1000},
		},
		Runs: []doc.Run{
			{Source: doc.SourceReconciled, Code: "CN", Lang: "zh", Start: 1, End: 2},
			{Source: doc.SourceReconciled, Code: "JA", Lang: "ja", Start: 3, End: 4},
			{Source: doc.SourceReconciled, Code: "HK", Lang: "zh-HK", Start: 5, End: 6},
		},
	}

	scope := res.ScopeFor([]string{"zh"})
	if scope.Pages != 4 {
		t.Errorf("scope pages = %d, want 4", scope.Pages)
	}
	if scope.Chars != 4000 {
		t.Errorf("scope chars = %d, want 4000: every Chinese run's pages are in scope", scope.Chars)
	}
}

func TestAnUnplaceableRunCoversNoPages(t *testing.T) {
	// The printed index names languages it cannot place — the measured fixture's HE,
	// AR and CZ entries all resolve to nothing. A start of 0 means unplaceable, not
	// page zero, and the arithmetic span turned each of them into a one-page section
	// spanning 0-0.
	unplaceable := doc.Run{Source: doc.SourceIndex, Code: "AR", Lang: "ar"}
	if got := unplaceable.Pages(); got != 0 {
		t.Errorf("an unplaceable run covers %d pages, want 0", got)
	}
	placed := doc.Run{Source: doc.SourceIndex, Code: "EN", Lang: "en", Start: 7, End: 22}
	if got := placed.Pages(); got != 16 {
		t.Errorf("run 7-22 covers %d pages, want 16", got)
	}
}
