// Command trig measures whether an FTS5 index kept by triggers survives the
// three paths that change doc_blocks: the wholesale replace, an upsert, and the
// ON DELETE CASCADE from documents. Scratch; deleted before commit.
package main

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE documents (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE doc_blocks (
  document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  page INTEGER NOT NULL, region_x0 INTEGER NOT NULL, idx INTEGER NOT NULL,
  kind TEXT NOT NULL, text TEXT NOT NULL, lang TEXT NOT NULL,
  PRIMARY KEY (document_id, page, region_x0, idx)) STRICT;
CREATE VIRTUAL TABLE doc_blocks_fts USING fts5(
  text, content='doc_blocks', content_rowid='rowid',
  tokenize='trigram remove_diacritics 1');
CREATE TRIGGER doc_blocks_fts_insert AFTER INSERT ON doc_blocks BEGIN
  INSERT INTO doc_blocks_fts(rowid, text) VALUES (new.rowid, new.text);
END;
CREATE TRIGGER doc_blocks_fts_delete AFTER DELETE ON doc_blocks BEGIN
  INSERT INTO doc_blocks_fts(doc_blocks_fts, rowid, text)
  VALUES ('delete', old.rowid, old.text);
END;
CREATE TRIGGER doc_blocks_fts_update AFTER UPDATE ON doc_blocks BEGIN
  INSERT INTO doc_blocks_fts(doc_blocks_fts, rowid, text)
  VALUES ('delete', old.rowid, old.text);
  INSERT INTO doc_blocks_fts(rowid, text) VALUES (new.rowid, new.text);
END;
`

func main() {
	for _, recursive := range []bool{false, true} {
		dsn := "file::memory:?_pragma=foreign_keys(1)"
		if recursive {
			dsn += "&_pragma=recursive_triggers(1)"
		}
		fmt.Printf("== foreign_keys=on recursive_triggers=%v\n", recursive)
		run(dsn)
	}
}

func run(dsn string) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", dsn)
	must(err)
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(ctx, schema)
	must(err)

	ins := func(doc string, idx int, text string) {
		_, err := db.ExecContext(ctx,
			`INSERT INTO doc_blocks (document_id, page, region_x0, idx, kind, text, lang)
			 VALUES (?, 1, 0, ?, 'paragraph', ?, 'de')
			 ON CONFLICT(document_id, page, region_x0, idx) DO UPDATE SET text = excluded.text`,
			doc, idx, text)
		must(err)
	}
	hits := func(term string) int {
		var n int
		must(db.QueryRowContext(ctx,
			`SELECT count(*) FROM doc_blocks_fts WHERE doc_blocks_fts MATCH ?`,
			`"`+term+`"`).Scan(&n))
		return n
	}
	rows := func() int {
		var n int
		must(db.QueryRowContext(ctx, `SELECT count(*) FROM doc_blocks`).Scan(&n))
		return n
	}
	integrity := func() string {
		_, err := db.ExecContext(ctx,
			`INSERT INTO doc_blocks_fts(doc_blocks_fts, rank) VALUES ('integrity-check', 1)`)
		if err != nil {
			return "FAILED: " + err.Error()
		}
		return "ok"
	}

	_, err = db.ExecContext(ctx, `INSERT INTO documents (id) VALUES ('d1'), ('d2')`)
	must(err)

	ins("d1", 0, "Saugkraft ist zu gering")
	ins("d1", 1, "Filter reinigen")
	ins("d2", 0, "Saugkraft anderer Manual")
	fmt.Printf("   after insert: rows=%d Saugkraft=%d integrity=%s\n",
		rows(), hits("Saugkraft"), integrity())

	// The upsert path: same key, new text.
	ins("d1", 0, "Saugleistung ist zu gering")
	fmt.Printf("   after upsert: Saugkraft=%d Saugleistung=%d integrity=%s\n",
		hits("Saugkraft"), hits("Saugleistung"), integrity())

	// The wholesale replace SaveConversion does, one document only.
	_, err = db.ExecContext(ctx, `DELETE FROM doc_blocks WHERE document_id = 'd1'`)
	must(err)
	fmt.Printf("   after replace-delete: rows=%d Saugkraft=%d Filter=%d integrity=%s\n",
		rows(), hits("Saugkraft"), hits("Filter"), integrity())

	// The cascade: deleting the document.
	_, err = db.ExecContext(ctx, `DELETE FROM documents WHERE id = 'd2'`)
	must(err)
	fmt.Printf("   after document delete (cascade): rows=%d Saugkraft=%d integrity=%s\n",
		rows(), hits("Saugkraft"), integrity())

	// VACUUM renumbers the rowids of a table whose rowid is not an INTEGER PRIMARY
	// KEY, and doc_blocks' key is composite. If it does that here, an external
	// content index points at the wrong rows after any VACUUM.
	_, err = db.ExecContext(ctx, `INSERT INTO documents (id) VALUES ('d4'), ('d5')`)
	must(err)
	for i := 0; i < 40; i++ {
		ins("d4", i, fmt.Sprintf("Saugkraft Absatz %d", i))
		ins("d5", i, fmt.Sprintf("Filter Absatz %d", i))
	}
	_, err = db.ExecContext(ctx, `DELETE FROM doc_blocks WHERE document_id = 'd4' AND idx < 20`)
	must(err)
	var before, after int64
	must(db.QueryRowContext(ctx, `SELECT max(rowid) FROM doc_blocks`).Scan(&before))
	fmt.Printf("   before vacuum: rows=%d maxrowid=%d Filter=%d integrity=%s\n",
		rows(), before, hits("Filter"), integrity())
	_, err = db.ExecContext(ctx, `VACUUM`)
	if err != nil {
		fmt.Printf("   VACUUM: %v\n", err)
	} else {
		must(db.QueryRowContext(ctx, `SELECT max(rowid) FROM doc_blocks`).Scan(&after))
		fmt.Printf("   after vacuum:  rows=%d maxrowid=%d Filter=%d integrity=%s\n",
			rows(), after, hits("Filter"), integrity())
	}

	// Negative control: without the delete trigger, does the same cascade leave a
	// detectably broken index? If it does not, the check above proves nothing.
	_, err = db.ExecContext(ctx, `INSERT INTO documents (id) VALUES ('d3')`)
	must(err)
	ins("d3", 0, "Saugkraft ohne Trigger")
	_, err = db.ExecContext(ctx, `DROP TRIGGER doc_blocks_fts_delete`)
	must(err)
	_, err = db.ExecContext(ctx, `DELETE FROM documents WHERE id = 'd3'`)
	must(err)
	fmt.Printf("   control, delete trigger dropped: rows=%d Saugkraft=%d integrity=%s\n",
		rows(), hits("Saugkraft"), integrity())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
