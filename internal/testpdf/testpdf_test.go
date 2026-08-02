package testpdf_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/extern"
	"github.com/gordon2/manualbox/internal/testpdf"
)

// The generated PDFs are only useful if real poppler can read them, so that is
// what this asserts. Without poppler the check cannot be made and skips.
func TestGeneratedPDFIsValid(t *testing.T) {
	if !extern.Available(extern.PDFInfo) || !extern.Available(extern.PDFToText) {
		t.Skip("poppler is not installed")
	}

	d := testpdf.TaggedSections([]string{"EN", "DE", "FR"}, 2, true)
	path := filepath.Join(t.TempDir(), "gen.pdf")
	if err := os.WriteFile(path, d.Build(), 0o600); err != nil {
		t.Fatal(err)
	}

	info, err := exec.CommandContext(t.Context(), "pdfinfo", path).CombinedOutput()
	if err != nil {
		t.Fatalf("pdfinfo rejected the generated file: %v\n%s", err, info)
	}
	// 1 contents page + 3 sections x 2 pages.
	if !strings.Contains(string(info), "Pages:           7") {
		t.Errorf("expected 7 pages, pdfinfo said:\n%s", info)
	}

	text, err := exec.CommandContext(t.Context(), "pdftotext", "-enc", "UTF-8", path, "-").CombinedOutput()
	if err != nil {
		t.Fatalf("pdftotext rejected the generated file: %v\n%s", err, text)
	}
	for _, want := range []string{"Contents", "EN User Manual", "Section DE page 1"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("extracted text is missing %q", want)
		}
	}
}

func TestBlankHasNoText(t *testing.T) {
	if !extern.Available(extern.PDFToText) {
		t.Skip("poppler is not installed")
	}
	path := filepath.Join(t.TempDir(), "blank.pdf")
	if err := os.WriteFile(path, testpdf.Blank(3).Build(), 0o600); err != nil {
		t.Fatal(err)
	}
	text, err := exec.CommandContext(t.Context(), "pdftotext", "-enc", "UTF-8", path, "-").CombinedOutput()
	if err != nil {
		t.Fatalf("pdftotext failed: %v\n%s", err, text)
	}
	if strings.TrimSpace(strings.ReplaceAll(string(text), "\f", "")) != "" {
		t.Errorf("a blank document yielded text: %q", text)
	}
}
