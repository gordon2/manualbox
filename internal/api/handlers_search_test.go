package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/registry"
	"github.com/gordon2/manualbox/internal/store"
)

// searchable stores a converted manual through the real registry, and returns the
// document's id. The API's job is to shape the answer; the index is internal/db's
// and internal/registry's business and is tested there.
func (h *harness) searchable(t *testing.T, deviceName, filename, digest string, blocks ...doc.Block) string {
	t.Helper()
	ctx := context.Background()

	device, err := h.registry.CreateDevice(ctx, registry.NewDevice{Name: deviceName})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	ref := store.Ref{SHA256: strings.Repeat(digest, 32), Size: 10}
	if err := h.registry.RecordBlob(ctx, ref, "application/pdf"); err != nil {
		t.Fatalf("record blob: %v", err)
	}
	document, _, err := h.registry.CreateDocument(ctx, registry.NewDocument{
		DeviceID: device.ID, BlobSHA256: ref.SHA256, Filename: filename,
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	if err := h.registry.SaveConversion(ctx, document.ID, blocks, nil, nil,
		registry.StateReady); err != nil {
		t.Fatalf("save conversion: %v", err)
	}
	return document.ID
}

func para(page int, lang, text string) doc.Block {
	return doc.Block{
		Page: page, RegionX0: 43, Index: 0, Kind: doc.BlockParagraph,
		Text: text, Lang: lang, X0: 43, X1: 300, Y0: 100, Y1: 118,
		Lines: 1, Chars: len([]rune(text)),
	}
}

// TestSearchEndpointSaysWhichManualAndWhere is the endpoint's whole job: a GET with
// a query string, answering across the household.
func TestSearchEndpointSaysWhichManualAndWhere(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)

	vacuum := h.searchable(t, "Vacuum cleaner", "thomas-drybox.pdf", "a",
		para(48, "de", "Den Ausblasfilter alle zwei Jahre tauschen."))
	h.searchable(t, "Washing machine", "washer.pdf", "b",
		para(7, "de", "Den Flusenfilter nach jedem Waschgang reinigen."))

	body := decode(t, h.do(t, http.MethodGet, "/api/v1/search?q=Filter", nil)) //nolint:bodyclose // decode closes it
	if body["mode"] != "index" {
		t.Errorf("mode = %v, want index", body["mode"])
	}
	if body["query"] != "Filter" {
		t.Errorf("query = %v, want it echoed", body["query"])
	}
	if body["truncated"] != false {
		t.Errorf("truncated = %v, want false", body["truncated"])
	}
	if _, ok := body["indexed"]; ok {
		t.Errorf("indexed = %v on a search that matched; it is only for an empty result",
			body["indexed"])
	}
	hits, ok := body["hits"].([]any)
	if !ok || len(hits) != 2 {
		t.Fatalf("hits = %v, want 2 across both manuals", body["hits"])
	}
	first, ok := hits[0].(map[string]any)
	if !ok {
		t.Fatalf("hit is not an object: %v", hits[0])
	}
	for _, key := range []string{
		"documentId", "filename", "deviceId", "deviceName", "state",
		"page", "regionX0", "index", "kind", "lang", "name", "snippet", "chars",
		"bm25", "score",
	} {
		if _, ok := first[key]; !ok {
			t.Errorf("hit is missing %q: %v", key, first)
		}
	}

	// Narrowed to one manual.
	one := decode(t, h.do(t, http.MethodGet, //nolint:bodyclose // decode closes it
		"/api/v1/search?q=Filter&documentId="+vacuum, nil))
	narrowed, ok := one["hits"].([]any)
	if !ok || len(narrowed) != 1 {
		t.Fatalf("narrowed hits = %v, want 1", one["hits"])
	}
	if got := narrowed[0].(map[string]any)["documentId"]; got != vacuum {
		t.Errorf("narrowed hit came from %v, want %s", got, vacuum)
	}
}

// TestSearchEndpointTakesTheQueryLiterally. A search box has no query language, so
// FTS5 syntax in a URL parameter must be text rather than an expression -- and a
// 500 from a background parser is the worst possible answer to a typo.
func TestSearchEndpointTakesTheQueryLiterally(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)
	h.searchable(t, "Vacuum cleaner", "manual.pdf", "a",
		para(1, "de", `Fehler: "Motor laeuft NICHT" - Filter pruefen.`))

	for _, q := range []string{
		`Fehler:`, `"Motor`, `Motor NOT laeuft`, `Filter*`, `NEAR(a b)`, `{x}`, `^a`,
		// Percent and underscore would be wildcards on the scan path, which is the
		// path a two-character query takes.
		`%`, `_`, `%%`,
	} {
		code := h.status(t, http.MethodGet, "/api/v1/search?q="+url.QueryEscape(q), nil)
		if code != http.StatusOK {
			t.Errorf("GET /search?q=%q returned %d, want 200", q, code)
		}
	}
}

func TestSearchEndpointValidation(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)

	// No q at all, and whitespace-only q: both are 400 rather than an empty result
	// that reads as "nothing in the house says that".
	for _, path := range []string{
		"/api/v1/search",
		"/api/v1/search?q=",
		"/api/v1/search?q=%20%20",
	} {
		if code := h.status(t, http.MethodGet, path, nil); code != http.StatusBadRequest {
			t.Errorf("GET %s returned %d, want 400", path, code)
		}
	}

	for _, path := range []string{
		"/api/v1/search?q=Filter&limit=0",
		"/api/v1/search?q=Filter&limit=-1",
		"/api/v1/search?q=Filter&limit=nope",
		"/api/v1/search?q=Filter&limit=101",
	} {
		if code := h.status(t, http.MethodGet, path, nil); code != http.StatusBadRequest {
			t.Errorf("GET %s returned %d, want 400", path, code)
		}
	}
	if code := h.status(t, http.MethodGet, "/api/v1/search?q=Filter&limit=100", nil); code != http.StatusOK {
		t.Errorf("the maximum limit was refused, %d", code)
	}
}

// TestSearchEndpointOnAnEmptyLibrarySaysSo. Nothing converted yet is not a search
// failure, and it is not the same answer as "no manual says that".
func TestSearchEndpointOnAnEmptyLibrarySaysSo(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)

	body := decode(t, h.do(t, http.MethodGet, "/api/v1/search?q=Saugkraft", nil)) //nolint:bodyclose // decode closes it
	hits, ok := body["hits"].([]any)
	if !ok || len(hits) != 0 {
		t.Errorf("hits = %v, want an empty array rather than null", body["hits"])
	}
	indexed, ok := body["indexed"].(float64)
	if !ok || indexed != 0 {
		t.Errorf("indexed = %v, want 0", body["indexed"])
	}
}

// TestSearchEndpointReportsTheScanPath. A two-character query cannot be in a
// trigram index, so the response says which path answered it rather than leaving a
// client to wonder why the ranking is flat.
func TestSearchEndpointReportsTheScanPath(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)
	h.searchable(t, "Robot vacuum", "dreame-l40.pdf", "a",
		para(541, "ja", "電源を入れる前に取扱説明書をお読みください。"))

	body := decode(t, h.do(t, http.MethodGet, //nolint:bodyclose // decode closes it
		"/api/v1/search?q="+url.QueryEscape("電源"), nil))
	if body["mode"] != "substring" {
		t.Errorf("mode = %v, want substring", body["mode"])
	}
	hits, ok := body["hits"].([]any)
	if !ok || len(hits) != 1 {
		t.Fatalf("hits = %v, want the one Japanese block", body["hits"])
	}

	indexed := decode(t, h.do(t, http.MethodGet, //nolint:bodyclose // decode closes it
		"/api/v1/search?q="+url.QueryEscape("取扱説明書"), nil))
	if indexed["mode"] != "index" {
		t.Errorf("a five-character Japanese query ran as %v, want index", indexed["mode"])
	}
}
