// Command stale finds out what an unmaintained external content index actually
// does wrong, since an inner join hides the obvious answer.
// Scratch; deleted before commit.
package main

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE doc_blocks (
  document_id TEXT NOT NULL, page INTEGER NOT NULL, region_x0 INTEGER NOT NULL,
  idx INTEGER NOT NULL, kind TEXT NOT NULL, text TEXT NOT NULL,
  PRIMARY KEY (document_id, page, region_x0, idx)) STRICT;
CREATE VIRTUAL TABLE doc_blocks_fts USING fts5(
  text, content='doc_blocks', content_rowid='rowid', tokenize='trigram');
CREATE TRIGGER ins AFTER INSERT ON doc_blocks BEGIN
  INSERT INTO doc_blocks_fts(rowid, text) VALUES (new.rowid, new.text);
END;
CREATE TRIGGER del AFTER DELETE ON doc_blocks BEGIN
  INSERT INTO doc_blocks_fts(doc_blocks_fts, rowid, text)
  VALUES ('delete', old.rowid, old.text);
END;
`

func main() {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:")
	must(err)
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(ctx, schema)
	must(err)

	ins := func(doc string, idx int, text string) {
		_, err := db.ExecContext(ctx,
			`INSERT INTO doc_blocks (document_id, page, region_x0, idx, kind, text)
			 VALUES (?, 1, 0, ?, 'paragraph', ?)`, doc, idx, text)
		must(err)
	}
	report := func(label, term string) {
		rows, err := db.QueryContext(ctx,
			`SELECT b.document_id, b.rowid, b.text FROM doc_blocks_fts
			 JOIN doc_blocks b ON b.rowid = doc_blocks_fts.rowid
			 WHERE doc_blocks_fts.text MATCH ?`, `"`+term+`"`)
		must(err)
		fmt.Printf("   %s search %q:\n", label, term)
		n := 0
		for rows.Next() {
			var d, text string
			var rid int64
			must(rows.Scan(&d, &rid, &text))
			fmt.Printf("      -> %s rowid=%d text=%q\n", d, rid, text)
			n++
		}
		must(rows.Err())
		_ = rows.Close()
		if n == 0 {
			fmt.Println("      -> no hits")
		}
	}

	ins("d1", 0, "Der Hygienefilter ist gewaschen.")
	report("with the trigger", "Hygienefilter")

	// Drop the delete trigger, then delete the highest rowid and insert a new block.
	// SQLite hands out max(rowid)+1, so the new block takes the rowid the stale
	// index entry still points at.
	_, err = db.ExecContext(ctx, `DROP TRIGGER del`)
	must(err)
	_, err = db.ExecContext(ctx, `DELETE FROM doc_blocks WHERE document_id = 'd1'`)
	must(err)
	fmt.Println("   trigger dropped, block deleted")
	report("after the delete", "Hygienefilter")

	ins("d2", 0, "Ganz andere Anleitung, anderes Geraet.")
	var rid int64
	must(db.QueryRowContext(ctx, `SELECT rowid FROM doc_blocks`).Scan(&rid))
	fmt.Printf("   inserted a block of another document at rowid=%d\n", rid)
	report("after the reuse", "Hygienefilter")
	report("after the reuse", "Anleitung")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
