package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/db/gen"
	"github.com/gordon2/manualbox/internal/id"
)

// TestDocBlockQueriesExecute is a SMOKE TEST FOR THE GENERATOR, not a behaviour
// test, and it is the twin of TestDocRegionQueriesExecute. It asserts only that
// every generated doc_blocks and doc_figures statement prepares and runs against a
// real migrated database and returns something coherent; what the pipeline does
// with them is internal/registry's business.
//
// It exists for the reason set out at length above TestDocRegionQueriesExecute and
// in the header of queries/docblocks.sql: sqlc v1.31.1 silently truncates the tail
// of a generated statement when a query file contains a non-ASCII character. `make
// sqlc` exits 0, the Go compiles, the linter passes, and the statement fails at
// PREPARE time inside a background job against a user's database. Executing each
// statement once is the cheapest thing that turns that into a build failure.
//
// The ORDER BY clauses are asserted rather than assumed, because a truncated tail
// is exactly what the bug eats and an unordered result is what a reader would see
// as scrambled prose.
func TestDocBlockQueriesExecute(t *testing.T) {
	ctx := context.Background()

	database, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "blocks.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	// Parents first: both tables cascade from documents, and doc_figures also
	// references blobs.
	w := gen.New(database.Write())
	docID, deviceID := id.New(id.Document), id.New(id.Device)
	sha := strings.Repeat("a", 64)
	figureSHA := strings.Repeat("b", 64)
	for _, digest := range []string{sha, figureSHA} {
		if err := w.UpsertBlob(ctx, gen.UpsertBlobParams{
			Sha256: digest, SizeBytes: 1, MediaType: "application/pdf", CreatedAt: Now(),
		}); err != nil {
			t.Fatalf("blob %s: %v", digest[:4], err)
		}
	}
	if _, err := database.Write().ExecContext(ctx,
		`INSERT INTO devices (id, name, created_at, updated_at) VALUES (?, 'Dryer', ?, ?)`,
		deviceID, Now(), Now()); err != nil {
		t.Fatalf("device: %v", err)
	}
	if _, err := w.CreateDocument(ctx, gen.CreateDocumentParams{
		ID: docID, DeviceID: deviceID, BlobSha256: sha, Filename: "manual.pdf",
		Kind: "manual", State: "uploaded", CreatedAt: Now(), UpdatedAt: Now(),
	}); err != nil {
		t.Fatalf("document: %v", err)
	}

	// Two regions of different languages on one page, plus a whole-page region on
	// another, plus a block nothing could name a language for. Between them these
	// exercise every column, every kind that is produced today, and the two states
	// the schema calls out: region_x0 = 0 for a whole page, and lang = ''.
	blocks := []gen.UpsertDocBlockParams{
		{Page: 2, RegionX0: 43, Idx: 0, Kind: "heading", Level: 1, Text: "Sicherheitshinweise",
			Lang: "de", X0: 43.2, X1: 300.8, Y0: 100.5, Y1: 118.5, Lines: 1, Chars: 19,
			Note: "18pt bold at 19 characters"},
		{Page: 2, RegionX0: 43, Idx: 1, Kind: "paragraph", Text: "Lesen Sie diese Anleitung.",
			Lang: "de", X0: 43.2, X1: 304.9, Y0: 122.0, Y1: 170.0, Lines: 3, Chars: 26},
		{Page: 2, RegionX0: 323, Idx: 0, Kind: "list-item", Text: "Przeczytaj instrukcje.",
			Lang: "pl", X0: 323.4, X1: 585.1, Y0: 122.0, Y1: 138.0, Lines: 1, Chars: 22},
		{Page: 3, RegionX0: 0, Idx: 0, Kind: "table", Text: "230 V, 50 Hz", Lang: "uk",
			X0: 30.0, X1: 400.0, Y0: 90.0, Y1: 106.0, Lines: 1, Chars: 12,
			Note: "row 2, column 1 of 2"},
		{Page: 3, RegionX0: 0, Idx: 1, Kind: "paragraph", Text: "62", Lang: "",
			X0: 430.0, X1: 445.0, Y0: 1150.0, Y1: 1166.0, Lines: 1, Chars: 2,
			Note: "no language established for this region"},
	}
	for i := range blocks {
		blocks[i].DocumentID = docID
		blocks[i].CreatedAt = Now()
		if err := w.UpsertDocBlock(ctx, blocks[i]); err != nil {
			t.Fatalf("UpsertDocBlock %d: %v", i, err)
		}
	}

	figures := []gen.UpsertDocFigureParams{
		{Page: 2, Idx: 0, X0: 43, Y0: 200, X1: 300, Y1: 460, Ink: 42, TextFraction: 0.02,
			Dpi: 216, PixelWidth: 514, PixelHeight: 520, BlobSha256: figureSHA},
		{Page: 3, Idx: 0, X0: 60, Y0: 300, X1: 500, Y1: 700, Ink: 17, TextFraction: 0.11,
			Dpi: 216, PixelWidth: 880, PixelHeight: 800, BlobSha256: figureSHA},
	}
	for i := range figures {
		figures[i].DocumentID = docID
		figures[i].CreatedAt = Now()
		if err := w.UpsertDocFigure(ctx, figures[i]); err != nil {
			t.Fatalf("UpsertDocFigure %d: %v", i, err)
		}
	}

	// THE UPSERT ITSELF, which nothing else covers. registry.SaveConversion deletes
	// before it inserts, so its idempotency test passes with the ON CONFLICT clause
	// removed entirely -- measured, not assumed. What the clause is actually for is a
	// retry WITHIN one conversion, where the delete has already happened and the same
	// key is written twice, and this is the only place that case is exercised.
	//
	// The re-write changes kind, which is deliberately NOT in the key: a paragraph
	// that a better heading rule promotes to a heading must update in place rather
	// than become a second row at the same index.
	reWrite := blocks[1]
	reWrite.Kind = "heading"
	reWrite.Level = 2
	reWrite.Text = "Lesen Sie diese Anleitung"
	reWrite.Chars = 25
	if err := w.UpsertDocBlock(ctx, reWrite); err != nil {
		t.Fatalf("UpsertDocBlock over an existing key: %v", err)
	}
	if err := w.UpsertDocFigure(ctx, figures[0]); err != nil {
		t.Fatalf("UpsertDocFigure over an existing key: %v", err)
	}

	r := gen.New(database.Read())

	rewritten, err := r.ListDocBlocksForPage(ctx, gen.ListDocBlocksForPageParams{
		DocumentID: docID, Page: 2,
	})
	if err != nil {
		t.Fatalf("ListDocBlocksForPage after re-upsert: %v", err)
	}
	if len(rewritten) != 3 {
		t.Errorf("re-upserting one block left %d rows on page 2, want 3: the ON CONFLICT "+
			"clause did not match and the block was duplicated", len(rewritten))
	}
	for i := range rewritten {
		b := &rewritten[i]
		if b.RegionX0 == 43 && b.Idx == 1 && (b.Kind != "heading" || b.Level != 2 || b.Chars != 25) {
			t.Errorf("the re-upserted block did not take the new values: %+v", b)
		}
	}
	if figs, err := r.ListDocFigures(ctx, docID); err != nil || len(figs) != 2 {
		t.Errorf("re-upserting a figure left %d rows, want 2 (err %v)", len(figs), err)
	}

	all, err := r.ListDocBlocks(ctx, docID)
	if err != nil {
		t.Fatalf("ListDocBlocks: %v", err)
	}
	if len(all) != len(blocks) {
		t.Errorf("ListDocBlocks returned %d rows, want %d", len(all), len(blocks))
	}
	// ORDER BY page, region_x0, idx: three sort keys, and the tail of that clause is
	// precisely what the truncation bug eats.
	for i := 1; i < len(all); i++ {
		prev, cur := &all[i-1], &all[i]
		before := prev.Page < cur.Page ||
			(prev.Page == cur.Page && prev.RegionX0 < cur.RegionX0) ||
			(prev.Page == cur.Page && prev.RegionX0 == cur.RegionX0 && prev.Idx < cur.Idx)
		if !before {
			t.Errorf("ListDocBlocks is not ordered by page, region_x0, idx: row %d is "+
				"(%d, %d, %d), row %d is (%d, %d, %d)",
				i-1, prev.Page, prev.RegionX0, prev.Idx,
				i, cur.Page, cur.RegionX0, cur.Idx)
		}
	}

	byLang, err := r.ListDocBlocksByLang(ctx, gen.ListDocBlocksByLangParams{
		DocumentID: docID, Lang: "de",
	})
	if err != nil {
		t.Fatalf("ListDocBlocksByLang: %v", err)
	}
	if len(byLang) != 2 {
		t.Errorf("de has %d blocks, want 2", len(byLang))
	}
	// And the unnamed content stays reachable by asking for it.
	unnamed, err := r.ListDocBlocksByLang(ctx, gen.ListDocBlocksByLangParams{
		DocumentID: docID, Lang: "",
	})
	if err != nil {
		t.Fatalf("ListDocBlocksByLang(''): %v", err)
	}
	if len(unnamed) != 1 {
		t.Errorf("%d blocks have no language, want 1", len(unnamed))
	}

	onPage, err := r.ListDocBlocksForPage(ctx, gen.ListDocBlocksForPageParams{
		DocumentID: docID, Page: 2,
	})
	if err != nil {
		t.Fatalf("ListDocBlocksForPage: %v", err)
	}
	if len(onPage) != 3 {
		t.Errorf("page 2 has %d blocks, want 3", len(onPage))
	}

	summary, err := r.SummarizeDocBlocks(ctx, docID)
	if err != nil {
		t.Fatalf("SummarizeDocBlocks: %v", err)
	}
	// Four labels: de, pl, uk and the unnamed one. The aggregates must be int64 and
	// not interface{} -- that is what the CASTs buy, and it is a compile-time
	// assertion as much as a runtime one.
	if len(summary) != 4 {
		t.Errorf("SummarizeDocBlocks returned %d rows, want 4: %+v", len(summary), summary)
	}
	for i := range summary {
		s := &summary[i]
		if s.Lang != "de" {
			continue
		}
		if s.Blocks != 2 || s.Chars != 44 || s.Lines != 4 || s.Pages != 1 || s.FirstPage != 2 {
			t.Errorf("de summary = %+v; want blocks 2, chars 44, lines 4, pages 1, first_page 2", s)
		}
	}
	for i := 1; i < len(summary); i++ {
		if summary[i-1].FirstPage > summary[i].FirstPage {
			t.Errorf("SummarizeDocBlocks is not ordered by first_page: %+v", summary)
		}
	}

	figs, err := r.ListDocFigures(ctx, docID)
	if err != nil {
		t.Fatalf("ListDocFigures: %v", err)
	}
	if len(figs) != 2 {
		t.Errorf("ListDocFigures returned %d rows, want 2", len(figs))
	}
	for i := 1; i < len(figs); i++ {
		if figs[i-1].Page > figs[i].Page {
			t.Errorf("ListDocFigures is not ordered by page: %+v", figs)
		}
	}
	figsOnPage, err := r.ListDocFiguresForPage(ctx, gen.ListDocFiguresForPageParams{
		DocumentID: docID, Page: 3,
	})
	if err != nil {
		t.Fatalf("ListDocFiguresForPage: %v", err)
	}
	if len(figsOnPage) != 1 {
		t.Errorf("page 3 has %d figures, want 1", len(figsOnPage))
	}

	if err := r.DeleteDocBlocks(ctx, docID); err != nil {
		t.Fatalf("DeleteDocBlocks: %v", err)
	}
	if left, err := r.ListDocBlocks(ctx, docID); err != nil || len(left) != 0 {
		t.Errorf("%d blocks survived DeleteDocBlocks (err %v)", len(left), err)
	}
	if err := r.DeleteDocFigures(ctx, docID); err != nil {
		t.Fatalf("DeleteDocFigures: %v", err)
	}
	if left, err := r.ListDocFigures(ctx, docID); err != nil || len(left) != 0 {
		t.Errorf("%d figures survived DeleteDocFigures (err %v)", len(left), err)
	}
}
