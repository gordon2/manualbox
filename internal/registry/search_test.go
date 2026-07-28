package registry_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/registry"
	"github.com/gordon2/manualbox/internal/store"
)

// These tests are about the index, not about conversion: they store blocks
// directly and then ask the questions a household asks. The text is real text from
// the two measured manuals wherever the script matters, because the whole tokeniser
// decision turns on scripts that a made-up ASCII fixture cannot represent.

// newDocumentOnDevice is newProbedDocument with the device named, because a search
// hit has to say WHICH manual and the device's name is half of that.
func newDocumentOnDevice(t *testing.T, s *registry.Service, deviceName, filename, digest string) string {
	t.Helper()
	ctx := context.Background()

	device, err := s.CreateDevice(ctx, registry.NewDevice{Name: deviceName})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	ref := store.Ref{SHA256: strings.Repeat(digest, 32), Size: 10}
	if err := s.RecordBlob(ctx, ref, "application/pdf"); err != nil {
		t.Fatalf("record blob: %v", err)
	}
	document, _, err := s.CreateDocument(ctx, registry.NewDocument{
		DeviceID: device.ID, BlobSHA256: ref.SHA256, Filename: filename,
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	return document.ID
}

// block is one stored block with only the fields search reads set.
func block(page int, x0 float64, index int, kind doc.BlockKind, lang, text string) doc.Block {
	return doc.Block{
		Page: page, RegionX0: x0, Index: index,
		Kind: kind, Text: text, Lang: lang,
		X0: x0, X1: x0 + 200, Y0: 100, Y1: 118,
		Lines: 1, Chars: len([]rune(text)),
	}
}

func heading(page int, x0 float64, index int, lang, text string) doc.Block {
	b := block(page, x0, index, doc.BlockHeading, lang, text)
	b.Level = 2
	return b
}

// save stores blocks as a conversion, which is the only way they ever arrive.
func save(t *testing.T, s *registry.Service, docID string, blocks ...doc.Block) {
	t.Helper()
	if err := s.SaveConversion(context.Background(), docID, blocks, nil, nil,
		registry.StateReady); err != nil {
		t.Fatalf("save conversion: %v", err)
	}
}

func search(t *testing.T, s *registry.Service, q registry.SearchQuery) *registry.SearchResults {
	t.Helper()
	res, err := s.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("search %q: %v", q.Text, err)
	}
	return res
}

// TestASearchHitSaysWhichManualAndWhere is the acceptance criterion, in the words
// README uses: the paper pile is unsearchable, and knowing that something matched
// is not the answer. A hit must name the document, the device, the page and the
// language, and carry enough text to recognise.
func TestASearchHitSaysWhichManualAndWhere(t *testing.T) {
	s := newService(t)
	docID := newDocumentOnDevice(t, s, "Vacuum cleaner", "thomas-drybox.pdf", "a")

	save(t, s, docID,
		heading(48, 43, 0, "de", "Ausblasfilter austauschen"),
		block(48, 43, 1, doc.BlockParagraph, "de",
			"Tauschen Sie den Spezial-Hygiene-Filter alle zwei Jahre aus."),
	)

	res := search(t, s, registry.SearchQuery{Text: "Ausblasfilter"})
	if res.Mode != registry.SearchIndex {
		t.Errorf("mode = %q, want %q", res.Mode, registry.SearchIndex)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("got %d hits, want 1: %+v", len(res.Hits), res.Hits)
	}
	got := res.Hits[0]
	if got.DocumentID != docID {
		t.Errorf("documentId = %q, want %q", got.DocumentID, docID)
	}
	if got.Filename != "thomas-drybox.pdf" || got.DeviceName != "Vacuum cleaner" {
		t.Errorf("hit names %q on %q; a household recognises the file and the device",
			got.Filename, got.DeviceName)
	}
	if got.Page != 48 {
		t.Errorf("page = %d, want 48", got.Page)
	}
	// The citation conversion.md specifies: the page, the region's left edge and the
	// index within it. Without these a hit cannot be deep-linked to the paragraph.
	if got.RegionX0 != 43 || got.Index != 0 {
		t.Errorf("regionX0/index = %d/%d, want 43/0", got.RegionX0, got.Index)
	}
	if got.Lang != "de" || got.Name != "German" {
		t.Errorf("lang/name = %q/%q, want de/German", got.Lang, got.Name)
	}
	if got.Kind != "heading" || got.Level != 2 {
		t.Errorf("kind/level = %q/%d, want heading/2", got.Kind, got.Level)
	}
	if !strings.Contains(got.Snippet, "Ausblasfilter") {
		t.Errorf("snippet %q does not contain the word searched for", got.Snippet)
	}
	if got.State != registry.StateReady {
		t.Errorf("state = %q, want %q", got.State, registry.StateReady)
	}
	if res.Indexed != nil {
		t.Errorf("indexed = %d on a search that matched; it is only for an empty result",
			*res.Indexed)
	}
}

// TestSearchSpansDocumentsAndCanBeNarrowedToOne is the scope decision. "Which
// manual says X" is a question about the household, so the default is every
// document; a reader already inside one asks the narrower question.
func TestSearchSpansDocumentsAndCanBeNarrowedToOne(t *testing.T) {
	s := newService(t)
	vacuum := newDocumentOnDevice(t, s, "Vacuum cleaner", "vacuum.pdf", "a")
	washer := newDocumentOnDevice(t, s, "Washing machine", "washer.pdf", "b")

	save(t, s, vacuum, block(12, 43, 0, doc.BlockParagraph, "de",
		"Den Filter alle drei Monate reinigen."))
	save(t, s, washer, block(7, 0, 0, doc.BlockParagraph, "de",
		"Den Flusenfilter nach jedem Waschgang reinigen."))

	all := search(t, s, registry.SearchQuery{Text: "Filter"})
	if len(all.Hits) != 2 {
		t.Fatalf("searching every manual got %d hits, want 2: %+v", len(all.Hits), all.Hits)
	}
	seen := map[string]bool{}
	for i := range all.Hits {
		seen[all.Hits[i].DeviceName] = true
	}
	if !seen["Vacuum cleaner"] || !seen["Washing machine"] {
		t.Errorf("hits came from %v, want both devices", seen)
	}

	one := search(t, s, registry.SearchQuery{Text: "Filter", DocumentID: washer})
	if len(one.Hits) != 1 || one.Hits[0].DocumentID != washer {
		t.Fatalf("narrowed search got %+v, want the one washer hit", one.Hits)
	}

	// An unknown document is a search of nothing rather than an error: this
	// parameter scopes a search, and turning it into an existence check would make
	// it a way to probe for ids.
	none := search(t, s, registry.SearchQuery{Text: "Filter", DocumentID: "doc_nope"})
	if len(none.Hits) != 0 {
		t.Errorf("unknown document returned %d hits", len(none.Hits))
	}
}

// TestAWordWithNoSpacesAroundItIsFound is why the tokeniser is trigram and not
// unicode61, and it is the test that would fail on the obvious choice.
//
// Japanese and Thai do not separate words with spaces, so unicode61 indexes a whole
// run as one token and finds a real word in neither: measured on the sequential
// manual, 0 hits for the Japanese "instruction manual" and 0 for the Thai "manual"
// against 6 stored blocks each. Every string here is real text from that manual.
func TestAWordWithNoSpacesAroundItIsFound(t *testing.T) {
	s := newService(t)
	docID := newDocumentOnDevice(t, s, "Robot vacuum", "dreame-l40.pdf", "a")

	save(t, s, docID,
		block(539, 0, 0, doc.BlockParagraph, "ja",
			"本製品の不適切な使用による感電、火災、またはケガを回避するために、"+
				"本製品を使用する前に取扱説明書をよくお読みになり、大切に保管してください。"),
		block(473, 0, 0, doc.BlockParagraph, "th",
			"เพื่อหลีกเลี่ยงการเกิดไฟฟ้าช็อต ไฟไหม้ "+
				"หรือการบาดเจ็บที่เกิดจากการใช้เครื่องอย่างไม่เหมาะสม โปรดอ่านคู่มือการใช้งาน"),
		block(517, 0, 0, doc.BlockParagraph, "ru",
			"Во избежание поражения электрическим током перед использованием "+
				"устройства прочитайте руководство по эксплуатации."),
	)

	for _, tc := range []struct {
		script, query, lang string
	}{
		// "Instruction manual", inside a Japanese sentence with no spaces anywhere.
		{"Japanese", "取扱説明書", "ja"},
		// "Manual", inside a Thai sentence whose words are not separated either.
		{"Thai", "คู่มือ", "th"},
		// Cyrillic, which unicode61 would also have found -- here to show the one
		// index serves both kinds of script rather than trading one for the other.
		{"Cyrillic", "устройства", "ru"},
	} {
		res := search(t, s, registry.SearchQuery{Text: tc.query})
		if len(res.Hits) != 1 {
			t.Errorf("%s %q: got %d hits, want 1. A word-boundary tokeniser finds "+
				"none of these", tc.script, tc.query, len(res.Hits))
			continue
		}
		if res.Hits[0].Lang != tc.lang {
			t.Errorf("%s %q matched the %s block", tc.script, tc.query, res.Hits[0].Lang)
		}
		if res.Mode != registry.SearchIndex {
			t.Errorf("%s %q was answered by %s, not the index", tc.script, tc.query, res.Mode)
		}
	}
}

// TestDiacriticsAreFoldedForLatinOnly pins both halves of that decision, and the
// second half is the one that was measured rather than assumed: FTS5's folding
// table reaches precomposed Latin and does not touch Cyrillic or Greek, so turning
// it on costs those scripts nothing.
func TestDiacriticsAreFoldedForLatinOnly(t *testing.T) {
	s := newService(t)
	docID := newDocumentOnDevice(t, s, "Coffee machine", "manual.pdf", "a")

	save(t, s, docID,
		block(3, 0, 0, doc.BlockParagraph, "de", "Entkalken Sie das Gerät alle drei Monate."),
		block(4, 0, 0, doc.BlockParagraph, "ru", "Ещё раз проверьте фильтр."),
		block(5, 0, 0, doc.BlockParagraph, "el", "Διαβάστε τις οδηγίες χρήσης."),
	)

	// A German household on any keyboard types Gerat and must find Gerät. Without
	// remove_diacritics -- which trigram, unlike unicode61, leaves OFF by default --
	// this is 0 hits.
	if res := search(t, s, registry.SearchQuery{Text: "Gerat"}); len(res.Hits) != 1 {
		t.Errorf("Gerat found %d blocks, want the one holding Geraet", len(res.Hits))
	}
	if res := search(t, s, registry.SearchQuery{Text: "Gerät"}); len(res.Hits) != 1 {
		t.Errorf("Geraet as written found %d blocks, want 1", len(res.Hits))
	}

	// Cyrillic and Greek are NOT folded, so a query must be written as the text is.
	// That is the measured behaviour and the reason the fold was free: it never had
	// the chance to merge two Russian or Greek words.
	if res := search(t, s, registry.SearchQuery{Text: "Ещё"}); len(res.Hits) != 1 {
		t.Errorf("the Russian word as printed found %d blocks, want 1", len(res.Hits))
	}
	if res := search(t, s, registry.SearchQuery{Text: "Еще"}); len(res.Hits) != 0 {
		t.Errorf("the Russian word with its diaeresis dropped found %d blocks; "+
			"FTS5 was measured not to fold Cyrillic, so this must be 0", len(res.Hits))
	}
	if res := search(t, s, registry.SearchQuery{Text: "οδηγίες"}); len(res.Hits) != 1 {
		t.Errorf("the Greek word as printed found %d blocks, want 1", len(res.Hits))
	}
	if res := search(t, s, registry.SearchQuery{Text: "οδηγιες"}); len(res.Hits) != 0 {
		t.Errorf("the Greek word without its tonos found %d blocks; FTS5 was measured "+
			"not to fold Greek, so this must be 0", len(res.Hits))
	}
}

// TestAHeadingOutranksAParagraphOfEqualStanding is the ranking judgement, held to
// what it claims. A heading names a section, so it is a better answer to "where
// does it say this" than a sentence mentioning the word in passing -- and the bonus
// is visible in the response, as the gap between bm25 and score, so it can be
// argued with.
func TestAHeadingOutranksAParagraphOfEqualStanding(t *testing.T) {
	s := newService(t)
	docID := newDocumentOnDevice(t, s, "Vacuum cleaner", "manual.pdf", "a")

	// Same length, same one occurrence, so bm25 alone would rank them together and
	// the tie would break on page order -- which would put the paragraph first.
	save(t, s, docID,
		block(12, 0, 0, doc.BlockParagraph, "de", "Der Wasserfilter sitzt hinten."),
		heading(48, 0, 0, "de", "Der Wasserfilter wird getauscht"),
	)

	res := search(t, s, registry.SearchQuery{Text: "Wasserfilter"})
	if len(res.Hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(res.Hits))
	}
	if res.Hits[0].Kind != "heading" {
		t.Errorf("first hit is a %s from page %d; a heading of equal standing should "+
			"lead", res.Hits[0].Kind, res.Hits[0].Page)
	}
	// The bonus is 1.0 and it is applied to the heading only.
	h := res.Hits[0]
	if h.Score >= h.BM25 {
		t.Errorf("heading score %v is not better than its bm25 %v; bm25 is negative "+
			"and the bonus is subtracted", h.Score, h.BM25)
	}
	if got := h.BM25 - h.Score; got < 0.99 || got > 1.01 {
		t.Errorf("heading bonus = %v, want 1.0", got)
	}
	if p := res.Hits[1]; p.Score != p.BM25 {
		t.Errorf("a %s got a bonus of %v; only headings do", p.Kind, p.BM25-p.Score)
	}
}

// TestAQueryTooShortForTheIndexIsAnsweredByAScan is the named limitation and its
// mitigation. A trigram index holds no token under three characters, so a
// two-character query -- an ordinary word in Chinese and Japanese -- matches
// nothing in it. Measured on the sequential manual: the two characters for "power"
// occur in 27 stored blocks and the index finds 0.
func TestAQueryTooShortForTheIndexIsAnsweredByAScan(t *testing.T) {
	s := newService(t)
	docID := newDocumentOnDevice(t, s, "Robot vacuum", "dreame-l40.pdf", "a")

	save(t, s, docID,
		block(541, 0, 0, doc.BlockParagraph, "ja", "電源を入れる前に取扱説明書をお読みください。"),
		heading(542, 0, 0, "ja", "電源について"),
	)

	res := search(t, s, registry.SearchQuery{Text: "電源"})
	if res.Mode != registry.SearchSubstring {
		t.Fatalf("mode = %q, want %q: two characters cannot be a trigram",
			res.Mode, registry.SearchSubstring)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("got %d hits, want 2; the scan is what makes a two-character word "+
			"findable at all", len(res.Hits))
	}
	// The same heading judgement applies, since it is a judgement about answers and
	// not about scoring.
	if res.Hits[0].Kind != "heading" {
		t.Errorf("first hit is a %s; the heading should lead here too", res.Hits[0].Kind)
	}
	// No bm25 exists on this path, and saying 0 is honest where inventing a number
	// would not be.
	for i := range res.Hits {
		if res.Hits[i].BM25 != 0 || res.Hits[i].Score != 0 {
			t.Errorf("hit %d carries bm25 %v score %v; there is no index term to weigh",
				i, res.Hits[i].BM25, res.Hits[i].Score)
		}
	}
	if !strings.Contains(res.Hits[0].Snippet, "電源") {
		t.Errorf("snippet %q does not show the match", res.Hits[0].Snippet)
	}

	// One short word sends the WHOLE query to the scan, rather than being dropped
	// from it: a search for two words that quietly became a search for one is
	// indistinguishable from a correct answer.
	mixed := search(t, s, registry.SearchQuery{Text: "取扱説明書 を"})
	if mixed.Mode != registry.SearchSubstring {
		t.Errorf("a query mixing a long word with a short one ran as %q", mixed.Mode)
	}
}

// TestTwoWordsMeanBothOfThem: the phrases are ANDed, so a query is a conjunction
// rather than a phrase that must appear verbatim.
func TestTwoWordsMeanBothOfThem(t *testing.T) {
	s := newService(t)
	docID := newDocumentOnDevice(t, s, "Vacuum cleaner", "manual.pdf", "a")

	save(t, s, docID,
		block(1, 0, 0, doc.BlockParagraph, "de", "Den Filter regelmaessig reinigen."),
		block(2, 0, 0, doc.BlockParagraph, "de", "Reinigen Sie das Gehaeuse feucht."),
		block(3, 0, 0, doc.BlockParagraph, "de", "Der Filter sitzt hinten."),
	)

	res := search(t, s, registry.SearchQuery{Text: "Filter reinigen"})
	if len(res.Hits) != 1 || res.Hits[0].Page != 1 {
		t.Fatalf("got %+v, want only the block holding both words", res.Hits)
	}
}

// TestAQueryIsNeverFTS5Syntax. FTS5 has an expression language, so an ordinary
// query containing a colon, a quote, a star or the word NOT would otherwise be a
// syntax error or, worse, silently mean something else.
func TestAQueryIsNeverFTS5Syntax(t *testing.T) {
	s := newService(t)
	docID := newDocumentOnDevice(t, s, "Vacuum cleaner", "manual.pdf", "a")
	save(t, s, docID, block(1, 0, 0, doc.BlockParagraph, "de",
		`Fehler: "Motor laeuft NICHT" - Filter pruefen.`))

	for _, q := range []string{
		`Fehler:`,
		`"Motor`,
		`Motor NOT laeuft`,
		`Filter*`,
		`Motor OR Filter`,
		`NEAR(Motor Filter)`,
		`^Fehler`,
		`{Motor}`,
	} {
		res, err := s.Search(context.Background(), registry.SearchQuery{Text: q})
		if err != nil {
			t.Errorf("searching %q failed: %v. A search box has no query language", q, err)
			continue
		}
		_ = res
	}

	// And a quote inside a word is escaped rather than opening a phrase.
	if res := search(t, s, registry.SearchQuery{Text: `"Motor laeuft NICHT"`}); len(res.Hits) != 1 {
		t.Errorf("quoted text found %d hits, want the one block holding it", len(res.Hits))
	}
}

func TestSearchRejectsAnEmptyQuery(t *testing.T) {
	s := newService(t)
	for _, q := range []string{"", "   ", "\t\n"} {
		if _, err := s.Search(context.Background(), registry.SearchQuery{Text: q}); !errors.Is(err, registry.ErrInvalid) {
			t.Errorf("searching %q gave %v, want ErrInvalid", q, err)
		}
	}
}

// TestNothingMatchedSaysHowMuchWasIndexed. "No manual says that" and "no manual has
// been converted yet" are the same empty list, and the second is not a search
// failure -- it is the same distinction Blocks makes between empty and absent.
func TestNothingMatchedSaysHowMuchWasIndexed(t *testing.T) {
	s := newService(t)
	docID := newDocumentOnDevice(t, s, "Vacuum cleaner", "manual.pdf", "a")

	fresh := search(t, s, registry.SearchQuery{Text: "Saugkraft"})
	if fresh.Indexed == nil || *fresh.Indexed != 0 {
		t.Errorf("indexed = %v before any conversion, want 0", fresh.Indexed)
	}

	save(t, s, docID, block(1, 0, 0, doc.BlockParagraph, "de", "Den Filter reinigen."))
	after := search(t, s, registry.SearchQuery{Text: "Saugkraft"})
	if after.Indexed == nil || *after.Indexed != 1 {
		t.Errorf("indexed = %v with one block stored, want 1", after.Indexed)
	}
}

func TestSearchTruncatesAtTheLimitAndSaysSo(t *testing.T) {
	s := newService(t)
	docID := newDocumentOnDevice(t, s, "Vacuum cleaner", "manual.pdf", "a")

	blocks := make([]doc.Block, 0, 5)
	for i := range 5 {
		blocks = append(blocks, block(1+i, 0, 0, doc.BlockParagraph, "de", "Den Filter reinigen."))
	}
	save(t, s, docID, blocks...)

	res := search(t, s, registry.SearchQuery{Text: "Filter", Limit: 3})
	if len(res.Hits) != 3 || !res.Truncated {
		t.Errorf("got %d hits truncated=%v, want 3 and true", len(res.Hits), res.Truncated)
	}
	full := search(t, s, registry.SearchQuery{Text: "Filter", Limit: 5})
	if len(full.Hits) != 5 || full.Truncated {
		t.Errorf("got %d hits truncated=%v, want 5 and false", len(full.Hits), full.Truncated)
	}
	// Above the cap is clamped rather than refused, because a client asking for too
	// much wants as much as it can have.
	if capped := search(t, s, registry.SearchQuery{
		Text: "Filter", Limit: registry.MaxSearchLimit + 1000,
	}); capped.Limit != registry.MaxSearchLimit {
		t.Errorf("limit = %d, want it clamped to %d", capped.Limit, registry.MaxSearchLimit)
	}
}

// TestReconvertingADocumentDoesNotDuplicateItsHits is the idempotency requirement
// every derived table here carries: a worker can die after doing the work and
// before recording success, so the same conversion runs twice.
func TestReconvertingADocumentDoesNotDuplicateItsHits(t *testing.T) {
	s := newService(t)
	docID := newDocumentOnDevice(t, s, "Vacuum cleaner", "manual.pdf", "a")

	blocks := []doc.Block{
		heading(48, 43, 0, "de", "Ausblasfilter austauschen"),
		block(48, 43, 1, doc.BlockParagraph, "de", "Den Filter alle zwei Jahre tauschen."),
	}
	save(t, s, docID, blocks...)
	first := search(t, s, registry.SearchQuery{Text: "Filter"})

	for range 3 {
		save(t, s, docID, blocks...)
	}
	again := search(t, s, registry.SearchQuery{Text: "Filter"})

	if len(again.Hits) != len(first.Hits) {
		t.Fatalf("after three more conversions the index returns %d hits, first "+
			"returned %d", len(again.Hits), len(first.Hits))
	}
	if len(again.Hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(again.Hits))
	}
	for i := range again.Hits {
		if again.Hits[i] != first.Hits[i] {
			t.Errorf("hit %d changed:\n first %+v\n again %+v", i, first.Hits[i], again.Hits[i])
		}
	}
}

// TestAReconversionThatDropsABlockDropsItFromTheIndex. The wholesale replace exists
// because a re-conversion can produce FEWER blocks, and an index that kept the old
// text would answer with a paragraph that no longer exists -- worse than a stale
// row in a reader, because search is how it would be found.
func TestAReconversionThatDropsABlockDropsItFromTheIndex(t *testing.T) {
	s := newService(t)
	docID := newDocumentOnDevice(t, s, "Vacuum cleaner", "manual.pdf", "a")

	save(t, s, docID,
		block(48, 43, 0, doc.BlockParagraph, "de", "Den Ausblasfilter tauschen."),
		block(48, 43, 1, doc.BlockParagraph, "de", "Den Motorschutzfilter waschen."),
		block(48, 43, 2, doc.BlockParagraph, "de", "Den Hygienefilter entsorgen."),
	)
	if res := search(t, s, registry.SearchQuery{Text: "Hygienefilter"}); len(res.Hits) != 1 {
		t.Fatalf("setup: got %d hits for the third block", len(res.Hits))
	}

	// A better paragraph rule merges the first two and drops the third: two blocks
	// where there were three, and the surviving indices are 0 and 1.
	save(t, s, docID,
		block(48, 43, 0, doc.BlockParagraph, "de",
			"Den Ausblasfilter tauschen. Den Motorschutzfilter waschen."),
		block(48, 43, 1, doc.BlockParagraph, "de", "Den Staubbehaelter leeren."),
	)

	if res := search(t, s, registry.SearchQuery{Text: "Hygienefilter"}); len(res.Hits) != 0 {
		t.Errorf("the dropped block is still findable: %+v", res.Hits)
	}
	// And the block that was updated in place -- same key, new text -- is findable by
	// its new text and not by its old.
	if res := search(t, s, registry.SearchQuery{Text: "Staubbehaelter"}); len(res.Hits) != 1 {
		t.Errorf("the replacement block at index 1 is not findable: %+v", res.Hits)
	}
	if res := search(t, s, registry.SearchQuery{Text: "Motorschutzfilter"}); len(res.Hits) != 1 {
		t.Errorf("the merged text found %d hits, want the one block it merged into",
			len(res.Hits))
	}
}

// TestDeletingADeviceRemovesItsManualsFromTheIndex is the path NO GO CODE OBSERVES,
// and it is why the index is maintained by triggers rather than by statements next
// to each write. Deleting a device cascades twice -- device to documents to blocks
// -- and nothing in Go touches doc_blocks on the way. Without the delete trigger,
// search hands a household a manual it deleted; TestBlockSearchIndexSurvivesTheCascade
// in internal/db is the same claim held against FTS5's own integrity check.
//
// There is no Service.DeleteDocument and no DELETE route for a document: a document
// is removed today only with its device. Declining one keeps it deliberately. So
// this is the whole of the delete surface, and the document-level cascade is pinned
// in internal/db where a raw handle can exercise it.
func TestDeletingADeviceRemovesItsManualsFromTheIndex(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	kept := newDocumentOnDevice(t, s, "Washing machine", "washer.pdf", "b")
	doomed := newDocumentOnDevice(t, s, "Vacuum cleaner", "vacuum.pdf", "a")

	save(t, s, kept, block(7, 0, 0, doc.BlockParagraph, "de", "Den Flusenfilter reinigen."))
	save(t, s, doomed, block(12, 0, 0, doc.BlockParagraph, "de", "Den Saugfilter reinigen."))

	if res := search(t, s, registry.SearchQuery{Text: "Saugfilter"}); len(res.Hits) != 1 {
		t.Fatalf("setup: got %d hits", len(res.Hits))
	}

	if err := s.DeleteDevice(ctx, mustDeviceOf(t, s, doomed)); err != nil {
		t.Fatalf("delete device: %v", err)
	}

	if res := search(t, s, registry.SearchQuery{Text: "Saugfilter"}); len(res.Hits) != 0 {
		t.Errorf("the deleted device's manual is still findable: %+v", res.Hits)
	}
	if res := search(t, s, registry.SearchQuery{Text: "Flusenfilter"}); len(res.Hits) != 1 {
		t.Errorf("the other device's manual lost its hit: %+v", res.Hits)
	}
}

func mustDeviceOf(t *testing.T, s *registry.Service, documentID string) string {
	t.Helper()
	document, err := s.GetDocument(context.Background(), documentID)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	return document.DeviceID
}
