// Package fixture loads test documents that are described in the repository but
// not stored in it.
//
// The ingest pipeline can only be tested honestly against real manuals, and real
// manuals are large and copyrighted. A 15 MB PDF committed to git is downloaded
// by every clone forever, and redistributing a manufacturer's manual would break
// manualbox's own rule against exactly that.
//
// So testdata/fixtures/*.json describes each document — where to get it, its
// checksum, and what the pipeline should find in it — and the bytes are fetched
// on demand into a local cache. Tests that need a fixture skip when it is not
// available, so the default suite stays hermetic and works offline.
package fixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// EnableEnv gates fetching. Downloading tens of megabytes must be something a
// developer opts into, never a surprise in a routine `go test ./...`.
const EnableEnv = "MANUALBOX_TEST_FIXTURES"

// Section is one language run within a multi-language document.
type Section struct {
	Code           string `json:"code"`
	Title          string `json:"title"`
	PrintedPage    int    `json:"printed_page"`
	PDFStart       int    `json:"pdf_start"`
	PDFEnd         int    `json:"pdf_end"`
	Pages          int    `json:"pages"`
	BoundarySource string `json:"boundary_source"`
	Note           string `json:"note,omitempty"`
}

// Manifest describes a fixture document and what the pipeline should find in it.
type Manifest struct {
	Name                   string    `json:"name"`
	URL                    string    `json:"url"`
	SHA256                 string    `json:"sha256"`
	Bytes                  int64     `json:"bytes"`
	Pages                  int       `json:"pages"`
	HasTextLayer           bool      `json:"has_text_layer"`
	MedianCharsPerPage     int       `json:"median_chars_per_page"`
	ContentStartsOnPDFPage int       `json:"content_starts_on_pdf_page"`
	IndexPages             []int     `json:"index_pages"`
	Sections               []Section `json:"sections"`
}

// Section returns the section for a language code.
func (m *Manifest) Section(code string) (Section, bool) {
	for _, s := range m.Sections {
		if s.Code == code {
			return s, true
		}
	}
	return Section{}, false
}

// Load reads a manifest by name from testdata/fixtures.
//
// dir is the repository-relative path to the fixtures directory; callers pass a
// path relative to their own package, which is why it is a parameter rather than
// resolved here.
func Load(dir, name string) (*Manifest, error) {
	path := filepath.Join(dir, name+".json")
	data, err := os.ReadFile(path) //nolint:gosec // test fixture path from the repository
	if err != nil {
		return nil, fmt.Errorf("fixture: read %s: %w", path, err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("fixture: parse %s: %w", path, err)
	}
	if m.Name == "" || m.URL == "" || m.SHA256 == "" {
		return nil, fmt.Errorf("fixture %s: name, url, and sha256 are all required", path)
	}
	return &m, nil
}

// CacheDir is where fetched fixtures live between runs.
func CacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "manualbox", "test-fixtures")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("fixture: create cache dir: %w", err)
	}
	return dir, nil
}

// Fetch returns a path to the fixture's bytes, downloading them once and reusing
// the cached copy afterwards.
//
// The checksum is verified on both paths: on download because the file comes from
// a CDN that could serve anything, and on cache hit because a truncated earlier
// download would otherwise poison every later run.
func (m *Manifest) Fetch(ctx context.Context) (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, m.SHA256+filepath.Ext(m.Name)+".pdf")

	if sum, err := fileSHA256(path); err == nil {
		if sum == m.SHA256 {
			return path, nil
		}
		// Corrupt or superseded; drop it and fetch again.
		_ = os.Remove(path)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("fixture %s: build request: %w", m.Name, err)
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fixture %s: download: %w", m.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fixture %s: download returned %s", m.Name, resp.Status)
	}

	// Write to a temporary file and rename, so an interrupted download cannot
	// leave a half-file that looks cached.
	tmp, err := os.CreateTemp(dir, "download-*")
	if err != nil {
		return "", fmt.Errorf("fixture %s: create temp file: %w", m.Name, err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), resp.Body); err != nil {
		return "", fmt.Errorf("fixture %s: write: %w", m.Name, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("fixture %s: close: %w", m.Name, err)
	}

	if got := hex.EncodeToString(hasher.Sum(nil)); got != m.SHA256 {
		return "", fmt.Errorf("fixture %s: checksum mismatch — the document at %s has changed\n  want %s\n  got  %s",
			m.Name, m.URL, m.SHA256, got)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", fmt.Errorf("fixture %s: commit to cache: %w", m.Name, err)
	}
	return path, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // path is derived from a checksum in the cache dir
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
