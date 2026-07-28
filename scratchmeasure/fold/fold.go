// Command fold measures exactly what remove_diacritics does to each script,
// against both tokenisers. Scratch; deleted before commit.
package main

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Each pair is (stored, queried): the query is the same word with its marks
// stripped, which a household on a Latin keyboard would type.
var pairs = [][3]string{
	{"German", "Entkalken Sie das Gerat alle drei Monate", "Gerat"},
	{"German", "Entkalken Sie das Gerat alle drei Monate", "Gerät"},
	{"German umlaut stored", "Entkalken Sie das Gerät alle drei Monate", "Gerat"},
	{"German sharp s", "Bestimmungsgemäße Verwendung", "Verwendung"},
	{"Russian yo stored", "ещё раз", "еще"},
	{"Russian yo stored", "ещё раз", "ещё"},
	{"Russian yo plain", "еще раз", "ещё"},
	{"Russian short i", "устройства работают", "устроиства"},
	{"Ukrainian yi", "Київ інструкція", "Киiв"},
	{"Ukrainian yi", "Київ інструкція", "Київ"},
	{"Greek tonos", "Ελληνικά οδηγίες χρήσης", "οδηγιες"},
	{"Greek tonos", "Ελληνικά οδηγίες χρήσης", "οδηγίες"},
	{"Hebrew niqqud", "מַדְרִיךְ לַמִשְׁתַמֵש", "מדריך"},
	{"Thai", "คู่มือการใช้งาน", "คู่มือ"},
	{"Thai stripped", "คู่มือการใช้งาน", "คูมือ"},
	{"Japanese", "本製品の取扱説明書", "取扱説明書"},
}

func main() {
	tokenizers := []string{
		"unicode61",
		"unicode61 remove_diacritics 0",
		"unicode61 remove_diacritics 2",
		"trigram",
		"trigram remove_diacritics 1",
	}
	ctx := context.Background()
	fmt.Printf("%-24s %-42s %-14s", "case", "query", "")
	fmt.Println()
	for _, tk := range tokenizers {
		db, err := sql.Open("sqlite", "file::memory:")
		must(err)
		_, err = db.ExecContext(ctx,
			fmt.Sprintf(`CREATE VIRTUAL TABLE t USING fts5(body, tokenize='%s')`, tk))
		must(err)
		fmt.Printf("\n== %s\n", tk)
		for _, p := range pairs {
			_, err = db.ExecContext(ctx, `DELETE FROM t`)
			must(err)
			_, err = db.ExecContext(ctx, `INSERT INTO t(body) VALUES (?)`, p[1])
			must(err)
			var n int
			err = db.QueryRowContext(ctx,
				`SELECT count(*) FROM t WHERE t MATCH ?`, `"`+p[2]+`"`).Scan(&n)
			state := "MISS"
			if err != nil {
				state = "ERR " + err.Error()
			} else if n > 0 {
				state = "hit"
			}
			fmt.Printf("   %-22s %-16q -> %s\n", p[0], p[2], state)
		}
		_ = db.Close()
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
