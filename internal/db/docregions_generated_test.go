package db

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gordon2/manualbox/internal/db/gen"
	"github.com/gordon2/manualbox/internal/id"
)

// TestDocRegionQueriesExecute is a SMOKE TEST FOR THE GENERATOR, not a behaviour
// test. It asserts only that every generated doc_regions query prepares and runs
// against a real migrated database and returns something coherent; what the
// pipeline does with them is internal/registry's business.
//
// It exists because sqlc v1.31.1 (pinned in tools/go.mod, built to ./bin/sqlc)
// silently TRUNCATES the tail of a generated statement. It tracks each statement's
// end offset in bytes but slices the text in characters, so every non-ASCII byte
// earlier in a queries/*.sql file shortens every statement after it by one
// character. Measured while writing docregions.sql: one em-dash in a comment turned
// "ORDER BY first_page, code" into "ORDER BY first_page, co", and four em-dashes
// turned it into "ORDER BY first_pa".
//
// Nothing upstream catches that. `make sqlc` exits 0, the generated Go compiles,
// the linter passes, and the statement fails at PREPARE time inside a background
// job against a user's database. Executing each statement once is the cheapest
// thing that turns it into a build failure, and a mangled statement names itself in
// the error.
func TestDocRegionQueriesExecute(t *testing.T) {
	ctx := context.Background()

	database, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "regions.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	// Parents first: doc_regions cascades from documents.
	w := gen.New(database.Write())
	docID, deviceID := id.New(id.Document), id.New(id.Device)
	sha := strings.Repeat("a", 64)
	if err := w.UpsertBlob(ctx, gen.UpsertBlobParams{
		Sha256: sha, SizeBytes: 1, MediaType: "application/pdf", CreatedAt: Now(),
	}); err != nil {
		t.Fatalf("blob: %v", err)
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

	// Two boxed regions of one language on one page, plus a whole-page region on
	// another, plus one region no signal could name. Between them these exercise
	// every column and both of the states the schema calls out: source = '' and a
	// page holding more than one region.
	regions := []gen.UpsertDocRegionParams{
		{Source: "repertoire", Page: 2, X0: 43, X1: 305, Code: "D", Lang: "de", Chars: 900, Runs: 50},
		{Source: "repertoire", Page: 2, X0: 323, X1: 585, Code: "D", Lang: "de", Chars: 880, Runs: 47},
		{Source: "page-tag", Page: 3, X0: 0, X1: 892, Code: "UA", Lang: "uk", Chars: 1700, Runs: 96,
			Conflict: 1, Note: "the page reads as Ukrainian, but 1 of its 2 columns read as Kazakh"},
		{Source: "", Page: 4, X0: 0, X1: 892, Chars: 120, Runs: 12,
			Note: "no language established for this page"},
	}
	for i := range regions {
		regions[i].DocumentID = docID
		regions[i].CreatedAt = Now()
		if err := w.UpsertDocRegion(ctx, regions[i]); err != nil {
			t.Fatalf("UpsertDocRegion %d: %v", i, err)
		}
	}

	r := gen.New(database.Read())

	all, err := r.ListDocRegions(ctx, docID)
	if err != nil {
		t.Fatalf("ListDocRegions: %v", err)
	}
	if len(all) != len(regions) {
		t.Errorf("ListDocRegions returned %d rows, want %d", len(all), len(regions))
	}
	// The ORDER BY is the clause the truncation bug ate, so it is asserted rather
	// than assumed: page ascending, then x0 ascending within a page.
	for i := 1; i < len(all); i++ {
		prev, cur := &all[i-1], &all[i]
		if prev.Page > cur.Page || (prev.Page == cur.Page && prev.X0 >= cur.X0) {
			t.Errorf("ListDocRegions is not ordered by page, x0: row %d is (%d, %d), row %d is (%d, %d)",
				i-1, prev.Page, prev.X0, i, cur.Page, cur.X0)
		}
	}

	onPage, err := r.ListDocRegionsForPage(ctx, gen.ListDocRegionsForPageParams{
		DocumentID: docID, Page: 2,
	})
	if err != nil {
		t.Fatalf("ListDocRegionsForPage: %v", err)
	}
	if len(onPage) != 2 {
		t.Errorf("page 2 has %d regions, want 2", len(onPage))
	}

	summary, err := r.SummarizeDocRegions(ctx, docID)
	if err != nil {
		t.Fatalf("SummarizeDocRegions: %v", err)
	}
	// Three labels: D/de, UA/uk, and the unnamed one.
	if len(summary) != 3 {
		t.Errorf("SummarizeDocRegions returned %d rows, want 3: %+v", len(summary), summary)
	}
	// The aggregates must be int64, not interface{} — that is what the CASTs buy,
	// and it is a compile-time assertion as much as a runtime one. Also confirms the
	// two same-language columns were summed rather than one of them being lost.
	for i := range summary {
		s := &summary[i]
		if s.Lang != "de" {
			continue
		}
		if s.Chars != 1780 || s.Runs != 97 || s.Pages != 1 || s.FirstPage != 2 {
			t.Errorf("de summary = %+v; want chars 1780, runs 97, pages 1, first_page 2", s)
		}
	}
	// ORDER BY first_page, code: the second sort key is the other half of the clause
	// the bug truncated.
	for i := 1; i < len(summary); i++ {
		if summary[i-1].FirstPage > summary[i].FirstPage {
			t.Errorf("SummarizeDocRegions is not ordered by first_page: %+v", summary)
		}
	}

	if err := r.DeleteDocRegions(ctx, docID); err != nil {
		t.Fatalf("DeleteDocRegions: %v", err)
	}
	left, err := r.ListDocRegions(ctx, docID)
	if err != nil {
		t.Fatalf("ListDocRegions after delete: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("%d regions survived DeleteDocRegions", len(left))
	}
}

// TestQueryFilesAreASCII is the cause-side guard for the generator bug that
// TestDocRegionQueriesExecute catches symptomatically.
//
// sqlc v1.31.1 truncates a generated statement by one character for every non-ASCII
// byte that appears earlier in the same queries/*.sql file, because it mixes byte
// offsets with character slicing. All the query files were pure ASCII when this was
// written, which is the only reason the bug had never fired here; the codebase's
// prose comments elsewhere use em-dashes freely, so the first person to write one in
// a query comment would have shipped invalid SQL that generates and compiles
// cleanly.
//
// Restricting these files to ASCII costs nothing — they are SQL and identifiers —
// and it removes the whole failure mode rather than one instance of it.
func TestQueryFilesAreASCII(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("queries", "*.sql"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("found no query files; this guard would pass vacuously")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		body := string(raw)
		for i, r := range body {
			if r >= utf8.RuneSelf {
				line := 1 + strings.Count(body[:i], "\n")
				t.Errorf("%s:%d contains the non-ASCII character %q. sqlc v1.31.1 will "+
					"silently truncate the tail of every statement after it; use plain "+
					"ASCII in query files.", f, line, r)
				break
			}
		}
	}
}
