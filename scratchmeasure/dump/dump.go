// Command dump is a scratch measurement harness: it converts a real manual and
// writes its blocks as JSON, so the tokeniser measurement runs on real text.
// Deleted before commit.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/gordon2/manualbox/internal/doc"
)

type row struct {
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
	path := os.Args[1]
	out := os.Args[2]
	langs := strings.Split(os.Args[3], ",")

	ctx := context.Background()
	res, err := doc.Analyze(ctx, path)
	if err != nil {
		panic(err)
	}
	fmt.Fprintf(os.Stderr, "%d pages, regionNote=%q\n", res.Info.Pages, res.RegionNote)
	conv, err := doc.Convert(ctx, path, res, langs)
	if err != nil {
		panic(err)
	}
	fmt.Fprintf(os.Stderr, "%s\n", conv.Summary())
	rows := make([]row, 0, len(conv.Blocks))
	for i := range conv.Blocks {
		b := &conv.Blocks[i]
		rows = append(rows, row{b.Page, b.RegionX0, b.Index, string(b.Kind), b.Level, b.Text, b.Lang, b.Chars})
	}
	f, err := os.Create(out)
	if err != nil {
		panic(err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rows); err != nil {
		panic(err)
	}
	fmt.Fprintf(os.Stderr, "wrote %d blocks to %s\n", len(rows), out)
}
