package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/gordon2/manualbox/internal/id"
)

// openAtVersion opens a real file-backed database and migrates it to exactly
// version v, so a migration can be exercised against data written by the schema
// that preceded it. Open() always migrates to head, which is the one thing a
// migration test must not do.
func openAtVersion(t *testing.T, path string, v int64) (*sql.DB, *goose.Provider) {
	t.Helper()

	pool, err := openPool(Options{Path: path, BusyTimeout: 5 * time.Second}, false, true)
	if err != nil {
		t.Fatalf("openPool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, pool, sub)
	if err != nil {
		t.Fatalf("goose.NewProvider: %v", err)
	}
	if _, err := provider.UpTo(context.Background(), v); err != nil {
		t.Fatalf("UpTo(%d): %v", v, err)
	}

	got, err := provider.GetDBVersion(context.Background())
	if err != nil {
		t.Fatalf("GetDBVersion: %v", err)
	}
	if got != v {
		t.Fatalf("schema version = %d, want %d", got, v)
	}
	return pool, provider
}

// snapshot renders every column of every row as text, ordered deterministically,
// so "the rows survived" can be asserted value by value rather than by counting.
// NULL is rendered distinctly from the empty string and from 0, which is the
// distinction a rebuild is most likely to lose.
func snapshot(t *testing.T, pool *sql.DB, query string) []string {
	t.Helper()

	rows, err := pool.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("snapshot query: %v", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns: %v", err)
	}

	var out []string
	for rows.Next() {
		cells := make([]any, len(cols))
		for i := range cells {
			cells[i] = new(any)
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatalf("scan: %v", err)
		}
		var b strings.Builder
		for i, c := range cells {
			v := *(c.(*any))
			if i > 0 {
				b.WriteString(" | ")
			}
			if v == nil {
				fmt.Fprintf(&b, "%s=<NULL>", cols[i])
			} else {
				fmt.Fprintf(&b, "%s=%T(%v)", cols[i], v, v)
			}
		}
		out = append(out, b.String())
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

const (
	pagesSnapshot = `SELECT document_id, page_no, chars, script, page_tag, printed_folio, lang, lang_source
	                 FROM doc_pages ORDER BY document_id, page_no`
	langsSnapshot = `SELECT document_id, source, pdf_start, pdf_end, code, lang, title, printed_page,
	                        confidence, conflict, note, created_at
	                 FROM doc_langs ORDER BY document_id, source, code, pdf_start`
)

// TestMigration3WidensLangSourceChecks is the guard on a schema change to other
// people's data: 00003 rebuilds two shipped tables to widen a CHECK, and a
// rebuild that loses a row, a NULL, or the constraint itself is silent damage.
func TestMigration3WidensLangSourceChecks(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "migrate.db")

	pool, provider := openAtVersion(t, path, 2)

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("exec %s: %v", query, err)
		}
	}
	mustFail := func(what, query string, args ...any) {
		t.Helper()
		if _, err := pool.ExecContext(ctx, query, args...); err == nil {
			t.Errorf("%s: expected the CHECK constraint to reject this, it was accepted", what)
		}
	}

	// Parents first: doc_pages and doc_langs both cascade from documents.
	docID := id.New(id.Document)
	deviceID := id.New(id.Device)
	sha := strings.Repeat("a", 64)
	exec(`INSERT INTO blobs (sha256, size_bytes, media_type, created_at) VALUES (?, 1, 'application/pdf', ?)`, sha, Now())
	exec(`INSERT INTO devices (id, name, created_at, updated_at) VALUES (?, 'Dishwasher', ?, ?)`, deviceID, Now(), Now())
	exec(`INSERT INTO documents (id, device_id, blob_sha256, filename, media_type, created_at, updated_at)
	      VALUES (?, ?, ?, 'manual.pdf', 'application/pdf', ?, ?)`, docID, deviceID, sha, Now(), Now())

	// doc_pages: every column exercised, including printed_folio NULL and a page
	// with no text layer at all.
	exec(`INSERT INTO doc_pages (document_id, page_no, chars, script, page_tag, printed_folio, lang, lang_source)
	      VALUES (?, 1, 1840, 'Latin', 'DE', 3, 'de', 'page-tag')`, docID)
	exec(`INSERT INTO doc_pages (document_id, page_no, chars, script, page_tag, printed_folio, lang, lang_source)
	      VALUES (?, 2, 0, '', '', NULL, '', '')`, docID)
	exec(`INSERT INTO doc_pages (document_id, page_no, chars, script, page_tag, printed_folio, lang, lang_source)
	      VALUES (?, 3, 920, 'Cyrillic', 'УКР', NULL, 'uk', 'reconciled')`, docID)

	// doc_langs: one placed run per remaining signal, plus the pdf_start = 0 row —
	// "named a language but could not place it", the state 00002 documents at
	// length and the one a naive rebuild is most likely to reject.
	exec(`INSERT INTO doc_langs (document_id, source, pdf_start, pdf_end, code, lang, title, printed_page,
	                             confidence, conflict, note, created_at)
	      VALUES (?, 'index', 5, 12, 'CZ', 'cs', 'Návod k použití', 4, 0.75, 1, 'index claims an Arabic page', ?)`, docID, Now())
	exec(`INSERT INTO doc_langs (document_id, source, pdf_start, pdf_end, code, lang, title, printed_page,
	                             confidence, conflict, note, created_at)
	      VALUES (?, 'index', 0, 0, 'ZH-HK', '', '', NULL, 0, 0, 'could not be placed', ?)`, docID, Now())
	exec(`INSERT INTO doc_langs (document_id, source, pdf_start, pdf_end, code, lang, title, printed_page,
	                             confidence, conflict, note, created_at)
	      VALUES (?, 'script', 1, 4, 'de', 'de', '', NULL, 1.0, 0, '', ?)`, docID, Now())
	exec(`INSERT INTO doc_langs (document_id, source, pdf_start, pdf_end, code, lang, title, printed_page,
	                             confidence, conflict, note, created_at)
	      VALUES (?, 'reconciled', 1, 4, 'de', 'de', '', NULL, 0.9, 1, 'page-tag disagreed', ?)`, docID, Now())

	// 00002's shape: repertoire is not yet a legal value in either column.
	mustFail("doc_pages.lang_source = repertoire at version 2",
		`INSERT INTO doc_pages (document_id, page_no, lang, lang_source) VALUES (?, 90, 'el', 'repertoire')`, docID)
	mustFail("doc_langs.source = repertoire at version 2",
		`INSERT INTO doc_langs (document_id, source, pdf_start, pdf_end, code, created_at)
		 VALUES (?, 'repertoire', 20, 24, 'EL', ?)`, docID, Now())

	pagesBefore := snapshot(t, pool, pagesSnapshot)
	langsBefore := snapshot(t, pool, langsSnapshot)
	if len(pagesBefore) != 3 || len(langsBefore) != 4 {
		t.Fatalf("fixture rows: %d doc_pages, %d doc_langs; want 3 and 4", len(pagesBefore), len(langsBefore))
	}

	// The rebuild. goose wraps a migration in a transaction by default and the
	// connection carries _pragma foreign_keys(1); if create/copy/drop/rename could
	// not run under both, this is where it would fail.
	if _, err := provider.UpTo(ctx, 3); err != nil {
		t.Fatalf("UpTo(3): %v", err)
	}

	assertSnapshotsEqual(t, "after Up", pagesBefore, snapshot(t, pool, pagesSnapshot), langsBefore, snapshot(t, pool, langsSnapshot))

	// The indexes belonged to the dropped tables and had to be recreated.
	for _, idx := range []string{"doc_pages_lang_idx", "doc_langs_source_idx", "doc_langs_lang_idx"} {
		var name string
		err := pool.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&name)
		if err != nil {
			t.Errorf("index %q missing after the rebuild: %v", idx, err)
		}
	}

	// Leftover scratch tables would mean the rename did not happen.
	var leftovers int
	if err := pool.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name LIKE 'doc_%_new'`).Scan(&leftovers); err != nil {
		t.Fatalf("count scratch tables: %v", err)
	}
	if leftovers != 0 {
		t.Errorf("%d scratch table(s) survived the migration", leftovers)
	}

	// What the migration exists for.
	exec(`INSERT INTO doc_pages (document_id, page_no, chars, script, page_tag, printed_folio, lang, lang_source)
	      VALUES (?, 4, 300, 'Greek', '', NULL, 'el', 'repertoire')`, docID)
	exec(`INSERT INTO doc_langs (document_id, source, pdf_start, pdf_end, code, lang, title, printed_page,
	                             confidence, conflict, note, created_at)
	      VALUES (?, 'repertoire', 20, 24, 'EL', 'el', '', NULL, 0.6, 0, '', ?)`, docID, Now())

	// Widened, not dropped. A rebuild that lost the constraint would pass every
	// assertion above.
	mustFail("doc_pages.lang_source = bogus at version 3",
		`INSERT INTO doc_pages (document_id, page_no, lang, lang_source) VALUES (?, 91, 'el', 'bogus')`, docID)
	mustFail("doc_langs.source = bogus at version 3",
		`INSERT INTO doc_langs (document_id, source, pdf_start, pdf_end, code, created_at)
		 VALUES (?, 'bogus', 30, 34, 'EL', ?)`, docID, Now())

	// Everything else 00002 constrained must still be constrained.
	mustFail("doc_pages.page_no = 0 at version 3",
		`INSERT INTO doc_pages (document_id, page_no) VALUES (?, 0)`, docID)
	mustFail("doc_langs.pdf_end < pdf_start at version 3",
		`INSERT INTO doc_langs (document_id, source, pdf_start, pdf_end, code, created_at)
		 VALUES (?, 'index', 40, 30, 'EL', ?)`, docID, Now())
	mustFail("doc_langs.confidence out of range at version 3",
		`INSERT INTO doc_langs (document_id, source, pdf_start, pdf_end, code, confidence, created_at)
		 VALUES (?, 'index', 50, 54, 'EL', 1.5, ?)`, docID, Now())
	mustFail("doc_pages row for a nonexistent document at version 3",
		`INSERT INTO doc_pages (document_id, page_no) VALUES ('doc_nonexistent', 1)`)

	// STRICT must survive the rebuild too.
	mustFail("text in doc_pages.chars at version 3",
		`INSERT INTO doc_pages (document_id, page_no, chars) VALUES (?, 92, 'lots')`, docID)

	// The cascade from documents must survive the rebuild: the FK clause is easy
	// to copy without ON DELETE CASCADE and nothing else would notice.
	for _, table := range []string{"doc_pages", "doc_langs"} {
		var onDelete string
		if err := pool.QueryRowContext(ctx,
			`SELECT "on_delete" FROM pragma_foreign_key_list(?) WHERE "table" = 'documents'`, table).Scan(&onDelete); err != nil {
			t.Fatalf("%s: read foreign key to documents: %v", table, err)
		}
		if onDelete != "CASCADE" {
			t.Errorf("%s: ON DELETE to documents = %q, want CASCADE", table, onDelete)
		}
	}

	// Down must restore 00002's shape, not drop the tables. Remove the rows that
	// only version 3 can hold first — there is nowhere for them to go, and the
	// migration failing on them is the honest behaviour, not the one under test.
	exec(`DELETE FROM doc_pages WHERE lang_source = 'repertoire'`)
	exec(`DELETE FROM doc_langs WHERE source = 'repertoire'`)

	if _, err := provider.DownTo(ctx, 2); err != nil {
		t.Fatalf("DownTo(2): %v", err)
	}

	assertSnapshotsEqual(t, "after Down", pagesBefore, snapshot(t, pool, pagesSnapshot), langsBefore, snapshot(t, pool, langsSnapshot))

	mustFail("doc_pages.lang_source = repertoire after Down",
		`INSERT INTO doc_pages (document_id, page_no, lang, lang_source) VALUES (?, 93, 'el', 'repertoire')`, docID)
	mustFail("doc_langs.source = repertoire after Down",
		`INSERT INTO doc_langs (document_id, source, pdf_start, pdf_end, code, created_at)
		 VALUES (?, 'repertoire', 60, 64, 'EL', ?)`, docID, Now())
}

func assertSnapshotsEqual(t *testing.T, when string, pagesWant, pagesGot, langsWant, langsGot []string) {
	t.Helper()
	for _, c := range []struct {
		table     string
		want, got []string
	}{
		{"doc_pages", pagesWant, pagesGot},
		{"doc_langs", langsWant, langsGot},
	} {
		if len(c.got) != len(c.want) {
			t.Errorf("%s: %s has %d rows %s, want %d\n got: %v\nwant: %v",
				when, c.table, len(c.got), when, len(c.want), c.got, c.want)
			continue
		}
		for i := range c.want {
			if c.got[i] != c.want[i] {
				t.Errorf("%s: %s row %d changed\n got: %s\nwant: %s", when, c.table, i, c.got[i], c.want[i])
			}
		}
	}
}
