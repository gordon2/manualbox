package doc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Temporary harness that writes one page's figure crops out to look at.
// Not committed.
func TestZZDumpFigures(t *testing.T) {
	path := os.Getenv("MB_PDF")
	out := os.Getenv("MB_OUT")
	pages := os.Getenv("MB_PAGES")
	if path == "" || out == "" || pages == "" {
		t.Skip("no MB_PDF/MB_OUT/MB_PAGES")
	}
	ctx := context.Background()
	runs, err := ExtractRuns(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range strings.Split(pages, ",") {
		var no int
		if _, err := fmt.Sscan(s, &no); err != nil {
			t.Fatal(err)
		}
		var page *PageRuns
		for i := range runs {
			if runs[i].No == no {
				page = &runs[i]
			}
		}
		if page == nil {
			t.Fatalf("page %d has no runs", no)
		}
		figs, err := PageFigures(ctx, path, page)
		if err != nil {
			t.Fatal(err)
		}
		for i := range figs {
			f := &figs[i]
			name := filepath.Join(out, fmt.Sprintf("p%d-f%d.png", no, f.Index))
			if err := os.WriteFile(name, f.PNG, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Logf("page %d figure %d %.1f,%.1f-%.1f,%.1f ink=%d text=%.1f%% -> %s",
				no, f.Index, f.Rect.X0, f.Rect.Y0, f.Rect.X1, f.Rect.Y1,
				f.Ink, f.TextFraction*100, name)
		}
	}
}
