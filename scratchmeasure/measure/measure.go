// Command measure is the scratch tokeniser measurement: it loads the real
// corpus dumped by dump.go into a fresh database per FTS5 variant, and reports
// the index cost and whether a real word of each script is findable.
// Deleted before commit.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type variant struct {
	name     string
	external bool
	tokenize string
}

type probe struct {
	script string
	term   string
}

type row struct {
	Doc   string
	Page  int     `json:"page"`
	X0    float64 `json:"regionX0"`
	Index int     `json:"index"`
	Kind  string  `json:"kind"`
	Level int     `json:"level"`
	Text  string  `json:"text"`
	Lang  string  `json:"lang"`
	Chars int     `json:"chars"`
}

func main() {
	dir := os.Args[1]
	measureMain(readCorpus(os.Args[2:]), dir)
}

func measureMain(corpus []row, dir string) {
	variants := []variant{
		{"standalone-unicode61", false, "unicode61"},
		{"external-unicode61", true, "unicode61"},
		{"external-unicode61-rd0", true, "unicode61 remove_diacritics 0"},
		{"external-unicode61-nodia", true, "unicode61 remove_diacritics 2"},
		{"standalone-trigram", false, "trigram"},
		{"external-trigram", true, "trigram"},
		{"external-trigram-nodia", true, "trigram remove_diacritics 1"},
	}
	probes := []probe{
		{"Latin (de)", "Filter"},
		{"Latin (de)", "Saugkraft"},
		{"Latin (de) umlaut", "Gerät"},
		{"Latin (de) folded", "Gerat"},
		{"Cyrillic (ru)", "фильтр"},
		{"Cyrillic (ru) accented", "устройства"},
		{"Japanese (ja)", "取扱説明書"},
		{"Thai (th)", "คู่มือ"},
		{"Hebrew (he) visual", "ךירדמ"},
		{"Hebrew (he) logical", "מדריך"},
		{"Cyrillic fold probe", "устроиства"},
		{"ja 2 chars", "電源"},
		{"ja 2 chars", "製品"},
		{"de 2 chars", "Sie"},
		{"th 3 chars", "น้ำ"},
	}

	baseline := load(filepath.Join(dir, "baseline.db"), corpus, variant{}, false)
	fmt.Printf("corpus: %d blocks, baseline db (doc_blocks only) %d bytes\n\n", len(corpus), baseline)

	for _, v := range variants {
		path := filepath.Join(dir, v.name+".db")
		total := load(path, corpus, v, true)
		fmt.Printf("== %s (content=%v)\n", v.name, v.external)
		fmt.Printf("   db %d bytes, index %+d bytes (%.2fx baseline)\n",
			total, total-baseline, float64(total)/float64(baseline))
		db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
		must(err)
		for _, p := range probes {
			n, err := count(db, p.term)
			if err != nil {
				fmt.Printf("   %-24s %-12q ERROR %v\n", p.script, p.term, err)
				continue
			}
			fmt.Printf("   %-24s %-12q %d hits\n", p.script, p.term, n)
		}
		_ = db.Close()
		fmt.Println()
	}

	// The substring fallback for a query too short for trigram, and what it costs
	// as a full scan over the same corpus.
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "external-trigram.db")+"?mode=ro")
	must(err)
	defer func() { _ = db.Close() }()
	for _, term := range []string{"電源", "製品", "Sie"} {
		start := time.Now()
		var n int
		must(db.QueryRowContext(context.Background(),
			`SELECT count(*) FROM doc_blocks WHERE text LIKE '%' || ? || '%'`, term).Scan(&n))
		fmt.Printf("LIKE fallback %-8q %d hits in %v\n", term, n, time.Since(start).Round(time.Microsecond))
	}

	// What bm25 does to a heading against a paragraph, before any boost.
	rows, err := db.QueryContext(context.Background(),
		`SELECT b.kind, bm25(doc_blocks_fts) AS r, substr(b.text, 1, 60)
		 FROM doc_blocks_fts JOIN doc_blocks b ON b.rowid = doc_blocks_fts.rowid
		 WHERE doc_blocks_fts MATCH '"Saugkraft"' ORDER BY r LIMIT 10`)
	must(err)
	defer func() { _ = rows.Close() }()
	fmt.Println("\nbm25 order for \"Saugkraft\" (trigram, no boost):")
	for rows.Next() {
		var kind, text string
		var r float64
		must(rows.Scan(&kind, &r, &text))
		fmt.Printf("   %-10s %8.3f %s\n", kind, r, text)
	}
	must(rows.Err())
}

func count(db *sql.DB, term string) (int, error) {
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM doc_blocks_fts WHERE doc_blocks_fts MATCH ?`,
		`"`+term+`"`).Scan(&n)
	return n, err
}

func load(path string, corpus []row, v variant, withFTS bool) int64 {
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	db, err := sql.Open("sqlite", "file:"+path)
	must(err)
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	_, err = db.ExecContext(ctx, `CREATE TABLE doc_blocks (
		document_id TEXT NOT NULL, page INTEGER NOT NULL, region_x0 INTEGER NOT NULL,
		idx INTEGER NOT NULL, kind TEXT NOT NULL, level INTEGER NOT NULL,
		text TEXT NOT NULL, lang TEXT NOT NULL,
		PRIMARY KEY (document_id, page, region_x0, idx)) STRICT`)
	must(err)

	if withFTS {
		content := ""
		if v.external {
			content = ", content='doc_blocks'"
		}
		stmt := fmt.Sprintf(
			`CREATE VIRTUAL TABLE doc_blocks_fts USING fts5(text, lang UNINDEXED, kind UNINDEXED%s, tokenize='%s')`,
			content, v.tokenize)
		_, err = db.ExecContext(ctx, stmt)
		must(err)
	}

	tx, err := db.Begin()
	must(err)
	ins, err := tx.PrepareContext(ctx,
		`INSERT INTO doc_blocks (document_id, page, region_x0, idx, kind, level, text, lang)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	must(err)
	for i := range corpus {
		r := &corpus[i]
		_, err = ins.ExecContext(ctx, r.Doc, r.Page, int64(r.X0+0.5), r.Index, r.Kind, r.Level, r.Text, r.Lang)
		must(err)
	}
	must(tx.Commit())

	if withFTS {
		if v.external {
			_, err = db.ExecContext(ctx,
				`INSERT INTO doc_blocks_fts(doc_blocks_fts) VALUES ('rebuild')`)
			must(err)
		} else {
			_, err = db.ExecContext(ctx,
				`INSERT INTO doc_blocks_fts(rowid, text, lang, kind)
				 SELECT rowid, text, lang, kind FROM doc_blocks`)
			must(err)
		}
		_, err = db.ExecContext(ctx, `INSERT INTO doc_blocks_fts(doc_blocks_fts) VALUES ('optimize')`)
		must(err)
	}
	_, err = db.ExecContext(ctx, `VACUUM`)
	must(err)
	must(db.Close())

	st, err := os.Stat(path)
	must(err)
	return st.Size()
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func readCorpus(paths []string) []row {
	var all []row
	for _, p := range paths {
		b, err := os.ReadFile(p)
		must(err)
		var rows []row
		must(json.Unmarshal(b, &rows))
		for i := range rows {
			rows[i].Doc = filepath.Base(p)
		}
		all = append(all, rows...)
	}
	return all
}
