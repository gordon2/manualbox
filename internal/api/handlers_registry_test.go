package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/testpdf"
)

// upload posts a file to a device the way a browser would, including a
// client-supplied Content-Type for the file part — which is exactly the value
// that must not be trusted.
func (h *harness) upload(t *testing.T, deviceID, filename, clientType string, content []byte) *http.Response {
	t.Helper()

	var body bytes.Buffer
	form := multipart.NewWriter(&body)

	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition",
		`form-data; name="file"; filename="`+filename+`"`)
	if clientType != "" {
		partHeader.Set("Content-Type", clientType)
	}
	part, err := form.CreatePart(partHeader)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := form.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		h.server.URL+"/api/v1/devices/"+deviceID+"/documents", &body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Origin", h.server.URL)

	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	return resp
}

func (h *harness) createDevice(t *testing.T) string {
	t.Helper()
	resp := h.do(t, http.MethodPost, "/api/v1/devices",
		map[string]string{"name": "Kettle"}, [2]string{"Origin", h.server.URL})
	if resp.StatusCode != http.StatusCreated {
		defer resp.Body.Close()
		t.Fatalf("create device returned %d, want 201", resp.StatusCode)
	}
	body := decode(t, resp)
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatal("device has no id")
	}
	return id
}

func uploadedDocumentID(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var body struct {
		Document struct {
			ID        string `json:"id"`
			MediaType string `json:"mediaType"`
			Filename  string `json:"filename"`
		} `json:"document"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	return body.Document.ID
}

// TestUploadedHTMLIsNeverServedAsHTML is the regression test for a stored XSS.
//
// The client sets the file part's Content-Type, and it used to be stored verbatim
// and echoed back with `Content-Disposition: inline`. Fetching the "manual" then
// executed its script at this instance's own origin, next to the session cookie —
// reachable by uploading an .html file found on the web, with no deliberate
// attacker. Same-origin also means checkOrigin cannot help.
func TestUploadedHTMLIsNeverServedAsHTML(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)
	deviceID := h.createDevice(t)

	const payload = `<html><body><script>alert(document.cookie)</script></body></html>`
	documentID := uploadedDocumentID(t, h.upload(t, deviceID, "manual.html", "text/html", []byte(payload))) //nolint:bodyclose // uploadedDocumentID closes it

	resp := h.do(t, http.MethodGet, "/api/v1/documents/"+documentID+"/content", nil)
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Type"); strings.Contains(strings.ToLower(got), "html") {
		t.Errorf("Content-Type = %q — HTML must never be served back from this origin", got)
	}
	if got := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff — without it the browser may sniff the body as HTML anyway", got)
	}
}

// TestUploadedSVGIsNotInline covers the other executable format. SVG is an XML
// document that can carry script, so it must not be rendered inline even though
// it is nominally an image.
func TestUploadedSVGIsNotInline(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)
	deviceID := h.createDevice(t)

	const payload = `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`
	documentID := uploadedDocumentID(t, h.upload(t, deviceID, "diagram.svg", "image/svg+xml", []byte(payload))) //nolint:bodyclose // uploadedDocumentID closes it

	resp := h.do(t, http.MethodGet, "/api/v1/documents/"+documentID+"/content", nil)
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment for SVG", got)
	}
	if got := resp.Header.Get("Content-Type"); strings.Contains(got, "svg") {
		t.Errorf("Content-Type = %q, want the type not to be honoured", got)
	}
}

// TestAGenuinePDFIsStillServedInline guards against over-correcting: the whole
// point of the route is to let a browser display a manual.
func TestAGenuinePDFIsStillServedInline(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)
	deviceID := h.createDevice(t)

	pdf := testpdf.TaggedSections([]string{"EN"}, 2, false).Build()
	// Note the lie: the client claims plain text, the bytes are a PDF. Sniffing
	// must win in both directions.
	documentID := uploadedDocumentID(t, h.upload(t, deviceID, "manual.pdf", "text/plain", pdf)) //nolint:bodyclose // uploadedDocumentID closes it

	resp := h.do(t, http.MethodGet, "/api/v1/documents/"+documentID+"/content", nil)
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Type"); got != "application/pdf" {
		t.Errorf("Content-Type = %q, want application/pdf — the bytes decide, not the upload", got)
	}
	if got := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "inline") {
		t.Errorf("Content-Disposition = %q, want inline for a real PDF", got)
	}
}

func TestSafeInlineType(t *testing.T) {
	tests := []struct {
		stored     string
		wantServed string
		wantInline bool
	}{
		{"application/pdf", "application/pdf", true},
		{"image/png", "image/png", true},
		{"image/jpeg", "image/jpeg", true},
		{"text/html; charset=utf-8", "application/octet-stream", false},
		// text/plain is the sniffer's catch-all for unrecognised text, which
		// includes SVG and anything else script-bearing, so it is not inline-safe.
		{"text/plain; charset=utf-8", "application/octet-stream", false},
		{"image/svg+xml", "application/octet-stream", false},
		{"application/xhtml+xml", "application/octet-stream", false},
		{"", "application/octet-stream", false},
		// Parameters on a safe type are dropped rather than echoed back.
		{"application/pdf; charset=binary", "application/pdf", true},
		{"APPLICATION/PDF", "application/pdf", true},
	}
	for _, tc := range tests {
		served, inline := safeInlineType(tc.stored)
		if served != tc.wantServed || inline != tc.wantInline {
			t.Errorf("safeInlineType(%q) = %q,%t; want %q,%t",
				tc.stored, served, inline, tc.wantServed, tc.wantInline)
		}
	}
}

func TestBaseFilenameStripsWindowsPaths(t *testing.T) {
	// filepath.Base is platform-specific, so on the Linux server this targets it
	// leaves a Windows path completely intact — storing the uploader's directory
	// layout, and their username, in the database.
	tests := map[string]string{
		`C:\Users\alice\Downloads\manual.pdf`: "manual.pdf",
		// Not a /home/... path: CI's hygiene job greps for those to catch a real
		// developer's directory leaking into the repo, and it cannot tell a
		// fictional name from a real one. Any absolute POSIX path tests the same
		// thing.
		`/srv/uploads/manual.pdf`: "manual.pdf",
		`manual.pdf`:              "manual.pdf",
		`../../etc/passwd`:        "passwd",
		``:                        ".",
	}
	for in, want := range tests {
		if got := baseFilename(in); got != want {
			t.Errorf("baseFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
