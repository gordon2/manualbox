package doc

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// Temporary measurement harness. Not committed.
func TestZZMeasurePage38(t *testing.T) {
	path := os.Getenv("MB_PDF")
	if path == "" {
		t.Skip("no MB_PDF")
	}
	for _, no := range []int{22, 38, 44, 57} {
		rules, err := ExtractRules(context.Background(), path, no)
		if err != nil {
			t.Fatal(err)
		}
		tabs := FindRuledTables(rules, nil)
		cells := 0
		for i := range tabs {
			cells += len(tabs[i].Cells)
		}
		t.Logf("page %d: %d rules, shape-guard tables %d, cells %d", no, len(rules), len(tabs), cells)
	}
}

// TestZZDumpRules prints one page's rules so two builds can be diffed.
func TestZZDumpRules(t *testing.T) {
	path := os.Getenv("MB_PDF")
	page := os.Getenv("MB_PAGE")
	if path == "" || page == "" {
		t.Skip("no MB_PDF/MB_PAGE")
	}
	var no int
	if _, err := fmt.Sscan(page, &no); err != nil {
		t.Fatal(err)
	}
	rules, err := ExtractRules(context.Background(), path, no)
	if err != nil {
		t.Fatal(err)
	}
	for i := range rules {
		r := &rules[i]
		t.Logf("%s at=%.2f %.2f-%.2f th=%.2f filled=%v", r.Dir, r.At, r.Start, r.End, r.Thickness, r.Filled)
	}
}
