package fixture

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fixturesDir is relative to this package.
const fixturesDir = "../../testdata/fixtures"

// TestManifestLoads runs always and offline. It guards the manifest itself: a
// hand-edited fixture with a broken range or a duplicate language is a test that
// would fail later for a confusing reason.
func TestManifestLoads(t *testing.T) {
	m, err := Load(fixturesDir, "dreame-l40-ultra")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if m.Pages != 560 {
		t.Errorf("Pages = %d, want 560", m.Pages)
	}
	if len(m.Sections) != 34 {
		t.Errorf("%d sections, want 34", len(m.Sections))
	}

	seen := map[string]bool{}
	prevEnd := 0
	for _, s := range m.Sections {
		if seen[s.Code] {
			t.Errorf("language %s appears twice", s.Code)
		}
		seen[s.Code] = true

		if s.PDFStart < 1 || s.PDFEnd > m.Pages || s.PDFStart > s.PDFEnd {
			t.Errorf("%s: range %d-%d is not inside 1-%d", s.Code, s.PDFStart, s.PDFEnd, m.Pages)
		}
		if s.Pages != s.PDFEnd-s.PDFStart+1 {
			t.Errorf("%s: Pages = %d but range %d-%d spans %d", s.Code, s.Pages, s.PDFStart, s.PDFEnd, s.PDFEnd-s.PDFStart+1)
		}
		// Sections are contiguous and ordered; a gap or overlap means the map is wrong.
		if prevEnd != 0 && s.PDFStart != prevEnd+1 {
			t.Errorf("%s starts at %d but the previous section ended at %d", s.Code, s.PDFStart, prevEnd)
		}
		prevEnd = s.PDFEnd
	}

	// The whole point of the fixture: English is a tiny slice of the document.
	en, ok := m.Section("EN")
	if !ok {
		t.Fatal("no EN section")
	}
	if share := float64(en.Pages) / float64(m.Pages); share > 0.05 {
		t.Errorf("EN is %.1f%% of the document; expected a few percent", share*100)
	}
}

// TestFixtureMatchesReality downloads the real PDF and checks the manifest's
// claims against it. Opt-in, because it fetches 15 MB.
//
// This is the test that catches the manifest going stale: the manufacturer
// replacing the file, the URL rotting, or a measurement I recorded being wrong.
func TestFixtureMatchesReality(t *testing.T) {
	if os.Getenv(EnableEnv) == "" {
		t.Skipf("set %s=1 to download fixtures and verify them against the real document", EnableEnv)
	}
	requireTool(t, "pdfinfo")
	requireTool(t, "pdftotext")

	m, err := Load(fixturesDir, "dreame-l40-ultra")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	path, err := m.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	// Size and page count.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != m.Bytes {
		t.Errorf("size = %d, manifest says %d", info.Size(), m.Bytes)
	}

	out, err := exec.CommandContext(ctx, "pdfinfo", path).Output()
	if err != nil {
		t.Fatalf("pdfinfo: %v", err)
	}
	if got := pdfInfoInt(string(out), "Pages"); got != m.Pages {
		t.Errorf("pdfinfo reports %d pages, manifest says %d", got, m.Pages)
	}

	// Text layer. This is the number that decides whether conversion is free or
	// costs a vision call per page, so it is worth asserting rather than assuming.
	text, err := exec.CommandContext(ctx, "pdftotext", "-f", "20", "-l", "20", path, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext: %v", err)
	}
	if m.HasTextLayer && len(strings.TrimSpace(string(text))) < 200 {
		t.Errorf("manifest claims a text layer but page 20 yielded %d chars", len(text))
	}

	// The printed index must still parse to the expected language codes.
	idx, err := exec.CommandContext(ctx, "pdftotext",
		"-f", strconv.Itoa(m.IndexPages[0]),
		"-l", strconv.Itoa(m.IndexPages[len(m.IndexPages)-1]+1),
		path, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext (index): %v", err)
	}
	codes := parseIndexCodes(string(idx))
	for _, s := range m.Sections {
		if !codes[s.Code] {
			t.Errorf("index no longer lists %s; the document may have been revised", s.Code)
		}
	}
}

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is not installed", name)
	}
}

var pagesRe = regexp.MustCompile(`(?m)^Pages:\s+(\d+)`)

func pdfInfoInt(out, key string) int {
	if key != "Pages" {
		return 0
	}
	if match := pagesRe.FindStringSubmatch(out); match != nil {
		n, _ := strconv.Atoi(match[1])
		return n
	}
	return 0
}

var codeRe = regexp.MustCompile(`^[A-Z]{2}(-[A-Z]{2})?$`)

// parseIndexCodes pulls language codes out of the contents page.
func parseIndexCodes(text string) map[string]bool {
	codes := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); codeRe.MatchString(trimmed) {
			codes[trimmed] = true
		}
	}
	return codes
}
