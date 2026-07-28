// Command snip measures what snippet() and highlight() produce over a trigram
// index, since a trigram token is three characters and not a word.
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
		for _, tokens := range []int{8, 16, 32, 64} {
			var s string
			err := db.QueryRowContext(ctx,
				fmt.Sprintf(`SELECT snippet(doc_blocks_fts, 0, '[', ']', '...', %d)
				 FROM doc_blocks_fts WHERE doc_blocks_fts MATCH ?
				 ORDER BY bm25(doc_blocks_fts) LIMIT 1`, tokens), `"`+term+`"`).Scan(&s)
			if err != nil {
				fmt.Printf("   %2d tokens ERROR %v\n", tokens, err)
				continue
			}
			fmt.Printf("   %2d tokens (%d runes): %s\n", tokens, len([]rune(s)), s)
		}
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
