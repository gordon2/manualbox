// Command rank measures what bm25 does to a heading against a paragraph on the
// real corpus, so the heading bonus is a number with evidence behind it.
// Scratch; deleted before commit.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "file:"+os.Args[1]+"?mode=ro")
	must(err)
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	for _, term := range os.Args[2:] {
		fmt.Printf("\n== %q\n", term)
		rows, err := db.QueryContext(ctx,
			`SELECT b.kind, b.level, b.lang, b.page, bm25(doc_blocks_fts) AS r,
			        substr(b.text, 1, 64)
			 FROM doc_blocks_fts JOIN doc_blocks b ON b.rowid = doc_blocks_fts.rowid
			 WHERE doc_blocks_fts MATCH ? ORDER BY r LIMIT 12`, `"`+term+`"`)
		must(err)
		for rows.Next() {
			var kind, lang, text string
			var level, page int
			var r float64
			must(rows.Scan(&kind, &level, &lang, &page, &r, &text))
			fmt.Printf("   %8.3f %-10s L%d %-3s p%-4d %s\n", r, kind, level, lang, page, text)
		}
		must(rows.Err())
		_ = rows.Close()

	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
