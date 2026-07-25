package frontend

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestEmbedPatternIsSatisfied is the regression guard for a breakage that only
// appears in a clean checkout: Vite's emptyOutDir once deleted the committed
// placeholder that keeps the go:embed pattern satisfied, so `go build ./...`
// failed for anyone who had not run npm. If the placeholder goes missing again,
// compilation fails outright — this test additionally proves the embedded tree is
// reachable and self-consistent.
func TestEmbedPatternIsSatisfied(t *testing.T) {
	entries, err := fs.ReadDir(embedded, "dist")
	if err != nil {
		t.Fatalf("the embedded dist directory is unreadable: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("dist is empty; the go:embed pattern would not compile")
	}
}

// TestFSReportsBuildStateHonestly must pass in both states — a developer machine
// after `make build`, and a fresh clone that has never run npm.
func TestFSReportsBuildStateHonestly(t *testing.T) {
	files, built := FS()

	if !built {
		if files != nil {
			if _, err := fs.Stat(files, "index.html"); err == nil {
				t.Error("FS reported not-built but index.html is present")
			}
		}
		t.Log("no SPA compiled in; exercising the placeholder path")
		return
	}

	if files == nil {
		t.Fatal("FS reported built but returned no filesystem")
	}
	if _, err := fs.Stat(files, "index.html"); err != nil {
		t.Errorf("FS reported built but index.html is missing: %v", err)
	}
}

func TestHandlerServesSomethingForTheRootPath(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want HTML", ct)
	}
	if rec.Body.Len() == 0 {
		t.Error("empty body")
	}
}

// TestClientSideRoutesFallBackToTheApp keeps a hard refresh on a deep link
// working.
func TestClientSideRoutesFallBackToTheApp(t *testing.T) {
	for _, path := range []string{"/devices", "/devices/dev_01JQ8ZK3M4N5P6R7S8T9V0W1X2", "/settings"} {
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s returned %d, want 200", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s Content-Type = %q, want HTML", path, ct)
		}
	}
}

// TestMissingAssetIsNotAnsweredWithHTML holds in both build states: a request for
// a bundle that does not exist must fail loudly, because answering 200 with a page
// turns a broken build into a baffling runtime error.
func TestMissingAssetIsNotAnsweredWithHTML(t *testing.T) {
	for _, path := range []string{
		"/assets/does-not-exist.js",
		"/assets/nope.css",
		"/favicon.ico",
	} {
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))

		if rec.Code == http.StatusOK && strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
			t.Errorf("GET %s returned HTML with 200; a missing asset must 404", path)
		}
	}
}

func TestBuiltAssetsAreCachedAndIndexIsNot(t *testing.T) {
	files, built := FS()
	if !built {
		t.Skip("no SPA compiled in")
	}

	// index.html must never be cached, or a deploy never reaches the browser.
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-cache") {
		t.Errorf("index.html Cache-Control = %q, want no-cache", got)
	}

	// Hashed asset filenames are immutable and should be cached hard.
	var asset string
	_ = fs.WalkDir(files, "assets", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && asset == "" {
			asset = path
		}
		return nil
	})
	if asset == "" {
		t.Skip("no hashed assets in this build")
	}

	rec = httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/"+asset, http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /%s returned %d", asset, rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("asset Cache-Control = %q, want immutable", got)
	}
}
