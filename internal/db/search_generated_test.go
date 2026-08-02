package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/db/gen"
	"github.com/gordon2/manualbox/internal/id"
)

// searchFixture is a migrated database holding one device, one document and a few
// blocks in the scripts that decided the tokeniser.
func searchFixture(t *testing.T) (database *DB, documentID string) {
	t.Helper()
	ctx := context.Background()

	database, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "search.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	w := gen.New(database.Write())
	docID, deviceID := id.New(id.Document), id.New(id.Device)
	sha := strings.Repeat("a", 64)
	if err := w.UpsertBlob(ctx, gen.UpsertBlobParams{
		Sha256: sha, SizeBytes: 1, MediaType: "application/pdf", CreatedAt: Now(),
	}); err != nil {
		t.Fatalf("blob: %v", err)
	}
	if _, err := database.Write().ExecContext(ctx,
		`INSERT INTO devices (id, name, created_at, updated_at) VALUES (?, 'Robot vacuum', ?, ?)`,
		deviceID, Now(), Now()); err != nil {
		t.Fatalf("device: %v", err)
	}
	if _, err := w.CreateDocument(ctx, gen.CreateDocumentParams{
		ID: docID, DeviceID: deviceID, BlobSha256: sha, Filename: "manual.pdf",
		Kind: "manual", State: "ready", CreatedAt: Now(), UpdatedAt: Now(),
	}); err != nil {
		t.Fatalf("document: %v", err)
	}

	blocks := []gen.UpsertDocBlockParams{
		{Page: 48, RegionX0: 43, Idx: 0, Kind: "heading", Level: 2, Lang: "de",
			Text: "Ausblasfilter austauschen", X1: 300, Y1: 118, Lines: 1, Chars: 25},
		{Page: 48, RegionX0: 43, Idx: 1, Kind: "paragraph", Lang: "de",
			Text: "Zubehör und Düsen alle drei Monate reinigen.", X1: 300, Y1: 170, Lines: 2, Chars: 43},
		{Page: 539, RegionX0: 0, Idx: 0, Kind: "paragraph", Lang: "ja",
			Text: "本製品を使用する前に取扱説明書をお読みください。",
			X1:   800, Y1: 200, Lines: 2, Chars: 27},
	}
	for i := range blocks {
		blocks[i].DocumentID = docID
		blocks[i].CreatedAt = Now()
		if err := w.UpsertDocBlock(ctx, blocks[i]); err != nil {
			t.Fatalf("UpsertDocBlock %d: %v", i, err)
		}
	}
	return database, docID
}

// TestSearchQueriesExecute is a SMOKE TEST FOR THE GENERATOR, the twin of
// TestDocBlockQueriesExecute and TestDocRegionQueriesExecute, and it earns its keep
// twice over here.
//
// The first reason is theirs: sqlc v1.31.1 silently truncates the tail of a
// generated statement when a query file holds a non-ASCII character, so `make sqlc`
// exits 0, the Go compiles, the linter passes and the statement fails at PREPARE
// time. Executing each statement once turns that into a build failure.
//
// The second is specific to search. queries/search.sql cannot use the documented
// FTS5 form `WHERE doc_blocks_fts MATCH ?`, because sqlc models a virtual table as
// its declared columns and rejects the table's own hidden column as unknown. The
// form that generates is `doc_blocks_fts.text MATCH ?`, a column-scoped match, and
// nothing but running it against a real FTS5 table proves the two are the same
// query. So this test is also the evidence for that workaround.
func TestSearchQueriesExecute(t *testing.T) {
	ctx := context.Background()
	database, docID := searchFixture(t)
	r := gen.New(database.Read())

	hits, err := r.SearchBlocks(ctx, gen.SearchBlocksParams{Match: `"Ausblasfilter"`, Limit: 10})
	if err != nil {
		t.Fatalf("SearchBlocks: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("SearchBlocks returned %d rows, want 1", len(hits))
	}
	got := hits[0]
	// Every joined column, because a truncated statement is exactly what loses the
	// tail of a SELECT list and a hit without a device name answers nothing.
	if got.DocumentID != docID || got.Filename != "manual.pdf" || got.DeviceName != "Robot vacuum" {
		t.Errorf("hit = %+v; want it to name the document, its file and its device", got)
	}
	if got.Page != 48 || got.RegionX0 != 43 || got.Idx != 0 {
		t.Errorf("hit is at %d/%d/%d, want page 48, region 43, index 0",
			got.Page, got.RegionX0, got.Idx)
	}
	if got.Kind != "heading" || got.Level != 2 || got.Lang != "de" || got.State != "ready" {
		t.Errorf("hit = %+v; want the heading's own columns", got)
	}
	if !strings.Contains(got.Snippet, "Ausblasfilter") {
		t.Errorf("snippet %q does not hold the term", got.Snippet)
	}
	// bm25 is negative and the heading bonus is subtracted, so score is lower still.
	if got.Bm25 >= 0 {
		t.Errorf("bm25 = %v, want a negative score", got.Bm25)
	}
	if diff := got.Bm25 - got.Score; diff < 0.99 || diff > 1.01 {
		t.Errorf("heading bonus = %v, want 1.0", diff)
	}

	narrowed, err := r.SearchBlocksInDocument(ctx, gen.SearchBlocksInDocumentParams{
		Match: `"Filter"`, DocumentID: docID, Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchBlocksInDocument: %v", err)
	}
	if len(narrowed) != 1 {
		t.Errorf("SearchBlocksInDocument returned %d rows, want 1", len(narrowed))
	}
	if elsewhere, err := r.SearchBlocksInDocument(ctx, gen.SearchBlocksInDocumentParams{
		Match: `"Filter"`, DocumentID: "doc_nope", Limit: 10,
	}); err != nil || len(elsewhere) != 0 {
		t.Errorf("narrowing to another document returned %d rows (err %v)", len(elsewhere), err)
	}

	// The scan, whose ORDER BY puts a heading first and then reads in page order.
	scanned, err := r.SearchBlocksSubstring(ctx, gen.SearchBlocksSubstringParams{
		Needle: "Filter", Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchBlocksSubstring: %v", err)
	}
	if len(scanned) != 1 || scanned[0].Kind != "heading" {
		t.Errorf("SearchBlocksSubstring returned %+v, want the one heading", scanned)
	}
	if scanned[0].Bm25 != 0 || scanned[0].Score != 0 {
		t.Errorf("the scan reported bm25 %v score %v; there is no term to weigh",
			scanned[0].Bm25, scanned[0].Score)
	}
	// Lowercased on both sides, because the scan's own matching is SQLite's lower()
	// and the snippet is a slice of the block's text exactly as stored.
	if !strings.Contains(strings.ToLower(scanned[0].Snippet), "filter") {
		t.Errorf("scan snippet %q does not hold the needle", scanned[0].Snippet)
	}
	if inDoc, err := r.SearchBlocksSubstringInDocument(ctx,
		gen.SearchBlocksSubstringInDocumentParams{
			Needle: "Filter", DocumentID: docID, Limit: 10,
		}); err != nil || len(inDoc) != 1 {
		t.Errorf("SearchBlocksSubstringInDocument returned %d rows (err %v)", len(inDoc), err)
	}

	if n, err := r.CountSearchableBlocks(ctx); err != nil || n != 3 {
		t.Errorf("CountSearchableBlocks = %d (err %v), want 3", n, err)
	}
}

// TestTheIndexHoldsWhatTheTokeniserWasChosenFor: a word inside a run with no spaces
// in it. This is the whole reason the tokeniser is trigram, and it is the assertion
// that fails on FTS5's default unicode61, which indexes the entire Japanese
// sentence as one token and finds nothing inside it.
func TestTheIndexHoldsWhatTheTokeniserWasChosenFor(t *testing.T) {
	ctx := context.Background()
	database, _ := searchFixture(t)
	r := gen.New(database.Read())

	// "Instruction manual", in the middle of a Japanese sentence.
	hits, err := r.SearchBlocks(ctx, gen.SearchBlocksParams{
		Match: `"取扱説明書"`, Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchBlocks: %v", err)
	}
	if len(hits) != 1 || hits[0].Lang != "ja" {
		t.Errorf("a Japanese word inside a spaceless run found %d hits, want the ja "+
			"block: %+v", len(hits), hits)
	}

	// And the Latin fold, which trigram does not do unless it is asked to: without
	// `remove_diacritics 1` in the migration this is 0 hits.
	folded, err := r.SearchBlocks(ctx, gen.SearchBlocksParams{Match: `"Zubehor"`, Limit: 10})
	if err != nil {
		t.Fatalf("SearchBlocks folded: %v", err)
	}
	if len(folded) != 1 {
		t.Errorf("searching without the umlaut found %d hits, want 1", len(folded))
	}
}

// TestBlockSearchIndexSurvivesEveryWriteToDocBlocks holds the index against FTS5's
// own integrity check after each of the three paths that change doc_blocks, and the
// third is the one no Go code observes.
//
// 'integrity-check' compares the index against the content table and fails if they
// disagree, which is exactly the failure an unmaintained external content index
// produces: a hit whose text no longer exists. The control at the end proves the
// check is not vacuous.
func TestBlockSearchIndexSurvivesEveryWriteToDocBlocks(t *testing.T) {
	ctx := context.Background()
	database, docID := searchFixture(t)
	w := gen.New(database.Write())
	r := gen.New(database.Read())

	integrity := func(stage string) error {
		_, err := database.Write().ExecContext(ctx,
			`INSERT INTO doc_blocks_fts(doc_blocks_fts, rank) VALUES ('integrity-check', 1)`)
		if err != nil {
			t.Errorf("the index is corrupt after %s: %v", stage, err)
		}
		return err
	}
	hits := func(term string) int {
		rows, err := r.SearchBlocks(ctx, gen.SearchBlocksParams{Match: `"` + term + `"`, Limit: 50})
		if err != nil {
			t.Fatalf("search %q: %v", term, err)
		}
		return len(rows)
	}

	_ = integrity("the initial inserts")
	if hits("Ausblasfilter") != 1 {
		t.Fatalf("setup: the heading is not in the index")
	}

	// 1. The upsert path: same key, new text. The old term must go and the new one
	// must arrive, which is what the UPDATE trigger's delete-then-insert is for.
	if err := w.UpsertDocBlock(ctx, gen.UpsertDocBlockParams{
		DocumentID: docID, Page: 48, RegionX0: 43, Idx: 0, Kind: "heading", Level: 2,
		Lang: "de", Text: "Motorschutzfilter waschen", X1: 300, Y1: 118,
		Lines: 1, Chars: 25, CreatedAt: Now(),
	}); err != nil {
		t.Fatalf("upsert over an existing key: %v", err)
	}
	_ = integrity("an upsert in place")
	if n := hits("Ausblasfilter"); n != 0 {
		t.Errorf("the replaced text is still findable %d times", n)
	}
	if n := hits("Motorschutzfilter"); n != 1 {
		t.Errorf("the new text is findable %d times, want 1", n)
	}

	// 2. The wholesale replace registry.saveBlocks does.
	if err := w.DeleteDocBlocks(ctx, docID); err != nil {
		t.Fatalf("delete blocks: %v", err)
	}
	_ = integrity("the wholesale delete")
	if n := hits("Motorschutzfilter"); n != 0 {
		t.Errorf("a deleted block is findable %d times", n)
	}

	// 3. The ON DELETE CASCADE from documents, which runs no Go at all. SQLite's own
	// documentation makes trigger firing on a foreign key action conditional on
	// recursive_triggers, which internal/db does not set -- so this is measured here
	// rather than assumed anywhere.
	if err := w.UpsertDocBlock(ctx, gen.UpsertDocBlockParams{
		DocumentID: docID, Page: 1, RegionX0: 0, Idx: 0, Kind: "paragraph", Lang: "de",
		Text: "Der Wasserfilter sitzt hinten.", X1: 300, Y1: 118, Lines: 1, Chars: 30,
		CreatedAt: Now(),
	}); err != nil {
		t.Fatalf("re-insert: %v", err)
	}
	if hits("Wasserfilter") != 1 {
		t.Fatalf("setup for the cascade: the block is not in the index")
	}
	if _, err := database.Write().ExecContext(ctx,
		`DELETE FROM documents WHERE id = ?`, docID); err != nil {
		t.Fatalf("delete document: %v", err)
	}
	_ = integrity("the cascade from documents")
	if n := hits("Wasserfilter"); n != 0 {
		t.Errorf("a document deleted by cascade is still findable %d times", n)
	}

	revertCheckTheDeleteTrigger(t, database)
}

// revertCheckTheDeleteTrigger drops the delete trigger and shows what goes wrong,
// because everything above would otherwise pass for the wrong reason.
//
// AND THE FAILURE IS NOT WHAT IT LOOKS LIKE, which is why this is worth its own
// function. Removing the trigger does NOT leave a deleted manual findable: every
// search joins the index to doc_blocks, so an index entry whose row is gone joins to
// nothing and silently disappears from the results. Measured that way round first,
// and it made the obvious control assertion pass while the index was corrupt.
//
// What actually goes wrong is worse. SQLite hands a new row max(rowid)+1, so
// deleting the highest block frees a rowid that the next insert takes. The stale
// index entry then points at a REAL row belonging to a DIFFERENT document, and a
// search for a word from the deleted manual returns a confident hit naming another
// manual, another page and text that does not contain the word. A wrong citation,
// not a missing one.
func revertCheckTheDeleteTrigger(t *testing.T, database *DB) {
	t.Helper()
	ctx := context.Background()
	w := gen.New(database.Write())

	doomed := reinsertDocument(t, database, "doomed.pdf")
	if err := w.UpsertDocBlock(ctx, gen.UpsertDocBlockParams{
		DocumentID: doomed, Page: 1, RegionX0: 0, Idx: 0, Kind: "paragraph", Lang: "de",
		Text: "Der Hygienefilter ist gewaschen.", X1: 300, Y1: 118, Lines: 1, Chars: 32,
		CreatedAt: Now(),
	}); err != nil {
		t.Fatalf("control insert: %v", err)
	}

	if _, err := database.Write().ExecContext(ctx, `DROP TRIGGER doc_blocks_fts_delete`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	if _, err := database.Write().ExecContext(ctx,
		`DELETE FROM documents WHERE id = ?`, doomed); err != nil {
		t.Fatalf("control delete: %v", err)
	}

	// FTS5's own check sees the damage even though a search does not.
	if _, err := database.Write().ExecContext(ctx,
		`INSERT INTO doc_blocks_fts(doc_blocks_fts, rank) VALUES ('integrity-check', 1)`); err == nil {
		t.Error("with the delete trigger dropped, the cascade left a consistent index. " +
			"Either the trigger is not what keeps it correct, or integrity-check does " +
			"not detect this -- and then every assertion above is worthless.")
	}

	// Now the consequence. A block of an unrelated document takes the freed rowid.
	other := reinsertDocument(t, database, "unrelated.pdf")
	if err := w.UpsertDocBlock(ctx, gen.UpsertDocBlockParams{
		DocumentID: other, Page: 9, RegionX0: 0, Idx: 0, Kind: "paragraph", Lang: "de",
		Text: "Ganz andere Anleitung, anderes Gerät.", X1: 300, Y1: 118, Lines: 1, Chars: 37,
		CreatedAt: Now(),
	}); err != nil {
		t.Fatalf("control re-insert: %v", err)
	}

	rows, err := gen.New(database.Read()).SearchBlocks(ctx, gen.SearchBlocksParams{
		Match: `"Hygienefilter"`, Limit: 10,
	})
	if err != nil {
		t.Fatalf("control search: %v", err)
	}
	if len(rows) == 0 {
		t.Error("without the delete trigger, the freed rowid was not reused and the " +
			"stale entry stayed invisible. The trigger still has to exist -- FTS5's " +
			"integrity check above says the index is corrupt -- but this assertion no " +
			"longer demonstrates the harm, so find the shape that does before " +
			"trusting it.")
		return
	}
	if rows[0].DocumentID != other {
		t.Errorf("control: expected the stale entry to resolve to the unrelated "+
			"document, got %+v", rows[0])
	}
	t.Logf("without the delete trigger, searching for a word from the deleted manual "+
		"returns %q on page %d of %s, which does not contain it",
		rows[0].Snippet, rows[0].Page, rows[0].Filename)
}

// reinsertDocument adds a second document on the existing device, for the control
// run that needs something to delete after the first document is gone.
func reinsertDocument(t *testing.T, database *DB, filename string) string {
	t.Helper()
	ctx := context.Background()
	var deviceID string
	if err := database.Read().QueryRowContext(ctx,
		`SELECT id FROM devices LIMIT 1`).Scan(&deviceID); err != nil {
		t.Fatalf("read device: %v", err)
	}
	docID := id.New(id.Document)
	if _, err := gen.New(database.Write()).CreateDocument(ctx, gen.CreateDocumentParams{
		ID: docID, DeviceID: deviceID, BlobSha256: strings.Repeat("a", 64),
		Filename: filename, Kind: "manual", State: "ready",
		CreatedAt: Now(), UpdatedAt: Now(),
	}); err != nil {
		t.Fatalf("second document: %v", err)
	}
	return docID
}
