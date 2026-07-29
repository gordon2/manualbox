package doc_test

// Scratch measurement harness. Dumps every block of both manuals to JSON so the
// furniture rule can be worked out without re-running a 20-second conversion for
// each experiment. Deleted before the branch lands.

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
)

type dumpBlock struct {
	Page     int     `json:"page"`
	RegionX0 float64 `json:"region_x0"`
	Index    int     `json:"index"`
	Kind     string  `json:"kind"`
	Level    int     `json:"level"`
	Text     string  `json:"text"`
	Lang     string  `json:"lang"`
	X0       float64 `json:"x0"`
	X1       float64 `json:"x1"`
	Y0       float64 `json:"y0"`
	Y1       float64 `json:"y1"`
	Lines    int     `json:"lines"`
	Chars    int     `json:"chars"`
	Note     string  `json:"note"`
}

func TestScratchDumpRuns(t *testing.T) {
	out := os.Getenv("MANUALBOX_SCRATCH_DIR")
	if out == "" {
		t.Skip("set MANUALBOX_SCRATCH_DIR")
	}
	for _, c := range []struct{ file, fix string }{
		{"column-runs.json", "thomas-drybox-amfibia"},
		{"seq-runs.json", "dreame-l40-ultra"},
	} {
		var path string
		if c.fix == "thomas-drybox-amfibia" {
			_, path = columnFixture(t)
		} else {
			_, path = loadFixture(t)
		}
		pages, err := doc.ExtractRuns(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(out + "/" + c.file)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(f).Encode(pages); err != nil {
			t.Fatal(err)
		}
		f.Close()
		t.Logf("%s: %d pages", c.file, len(pages))

		res, err := doc.Analyze(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		g, err := os.Create(out + "/" + c.file + ".regions.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(g).Encode(res.Regions); err != nil {
			t.Fatal(err)
		}
		g.Close()
		t.Logf("  %d regions", len(res.Regions))

		folios := map[int]*int{}
		for i := range res.Pages {
			folios[res.Pages[i].No] = res.Pages[i].Folio
		}
		h, err := os.Create(out + "/" + c.file + ".folios.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(h).Encode(folios); err != nil {
			t.Fatal(err)
		}
		h.Close()
	}
}

func TestScratchDumpBlocks(t *testing.T) {
	out := os.Getenv("MANUALBOX_SCRATCH_DIR")
	if out == "" {
		t.Skip("set MANUALBOX_SCRATCH_DIR")
	}
	cases := []struct {
		file  string
		fix   string
		langs []string
	}{
		{"column-de.json", "thomas-drybox-amfibia", []string{"de"}},
		{"column-de-uk.json", "thomas-drybox-amfibia", []string{"de", "uk"}},
		{"seq-de.json", "dreame-l40-ultra", []string{"de"}},
		{"seq-ru.json", "dreame-l40-ultra", []string{"ru"}},
		{"seq-de-ru-ja.json", "dreame-l40-ultra", []string{"de", "ru", "ja"}},
	}
	for _, c := range cases {
		conv := convertFixture(t, c.fix, c.langs...)
		blocks := make([]dumpBlock, 0, len(conv.Blocks))
		for i := range conv.Blocks {
			b := &conv.Blocks[i]
			blocks = append(blocks, dumpBlock{
				Page: b.Page, RegionX0: b.RegionX0, Index: b.Index, Kind: string(b.Kind),
				Level: b.Level, Text: b.Text, Lang: b.Lang,
				X0: b.X0, X1: b.X1, Y0: b.Y0, Y1: b.Y1,
				Lines: b.Lines, Chars: b.Chars, Note: b.Note,
			})
		}
		payload := map[string]any{"pages": conv.Pages, "blocks": blocks}
		f, err := os.Create(out + "/" + c.file)
		if err != nil {
			t.Fatal(err)
		}
		enc := json.NewEncoder(f)
		enc.SetIndent("", " ")
		if err := enc.Encode(payload); err != nil {
			t.Fatal(err)
		}
		f.Close()
		t.Logf("%s: %d blocks over %d pages", c.file, len(blocks), len(conv.Pages))
	}
	_ = doc.BlockHeading
}
