package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/registry"
	"github.com/gordon2/manualbox/internal/store"
)

// --- locations ---

func (s *Server) handleListLocations(w http.ResponseWriter, r *http.Request) {
	locations, err := s.deps.Registry.ListLocations(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"locations": locations})
}

func (s *Server) handleCreateLocation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		ParentID string `json:"parentId"`
		Notes    string `json:"notes"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}

	location, err := s.deps.Registry.CreateLocation(r.Context(), strings.TrimSpace(body.Name), body.ParentID, body.Notes)
	if err != nil {
		s.writeRegistryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, location)
}

// --- devices ---

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := s.deps.Registry.ListDevices(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

// deviceBody is the shared shape for creating and updating a device.
//
// Serial number and purchase price are deliberately absent. They are the
// highest-harm fields manualbox would hold and must be encrypted with a key kept
// outside the data directory; accepting them before the keyring is wired in would
// store them in the clear. See docs/design/privacy.md.
type deviceBody struct {
	Name        string `json:"name"`
	Brand       string `json:"brand"`
	Model       string `json:"model"`
	Category    string `json:"category"`
	LocationID  string `json:"locationId"`
	Notes       string `json:"notes"`
	PurchasedAt string `json:"purchasedAt"`
}

func (b deviceBody) toNewDevice() (registry.NewDevice, error) {
	in := registry.NewDevice{
		Name:       strings.TrimSpace(b.Name),
		Brand:      strings.TrimSpace(b.Brand),
		Model:      strings.TrimSpace(b.Model),
		Category:   strings.TrimSpace(b.Category),
		LocationID: b.LocationID,
		Notes:      b.Notes,
	}
	if b.PurchasedAt != "" {
		// Date only: a purchase has a date, not a time of day, and accepting a
		// timestamp would invite a timezone shifting it to the previous day.
		t, err := time.Parse(time.DateOnly, b.PurchasedAt)
		if err != nil {
			return in, fmt.Errorf("purchasedAt must be a date like 2026-07-25")
		}
		in.PurchasedAt = &t
	}
	return in, nil
}

func (s *Server) handleCreateDevice(w http.ResponseWriter, r *http.Request) {
	var body deviceBody
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	in, err := body.toNewDevice()
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}

	device, err := s.deps.Registry.CreateDevice(r.Context(), in)
	if err != nil {
		s.writeRegistryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, device)
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	device, err := s.deps.Registry.GetDevice(r.Context(), chi.URLParam(r, "deviceID"))
	if err != nil {
		s.writeRegistryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, device)
}

func (s *Server) handleUpdateDevice(w http.ResponseWriter, r *http.Request) {
	var body deviceBody
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	in, err := body.toNewDevice()
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}

	device, err := s.deps.Registry.UpdateDevice(r.Context(), chi.URLParam(r, "deviceID"), in)
	if err != nil {
		s.writeRegistryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, device)
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Registry.DeleteDevice(r.Context(), chi.URLParam(r, "deviceID")); err != nil {
		s.writeRegistryError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- documents ---

func (s *Server) handleListDocuments(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "deviceID")
	if _, err := s.deps.Registry.GetDevice(r.Context(), deviceID); err != nil {
		s.writeRegistryError(w, r, err)
		return
	}
	documents, err := s.deps.Registry.ListDocumentsForDevice(r.Context(), deviceID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": documents})
}

// uploadFormField is the multipart field name for the file itself.
const uploadFormField = "file"

// handleUploadDocument stores an uploaded file and queues the free probe.
//
// The response is deliberately returned before the document has been read: the
// probe takes a couple of seconds on a large manual, and blocking an HTTP request
// on it would mean a user who closes the tab loses the upload. Progress arrives
// over the existing job event stream.
func (s *Server) handleUploadDocument(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "deviceID")
	if _, err := s.deps.Registry.GetDevice(r.Context(), deviceID); err != nil {
		s.writeRegistryError(w, r, err)
		return
	}

	// Cap the request body before reading any of it, so an oversized upload is
	// refused rather than filling the disk on the way to being rejected.
	r.Body = http.MaxBytesReader(w, r.Body, s.deps.Config.Server.MaxUploadBytes)

	file, header, err := r.FormFile(uploadFormField)
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			s.writeError(w, r, http.StatusBadRequest, "missing_file",
				fmt.Sprintf("Attach the document as a multipart field named %q.", uploadFormField))
			return
		}
		// A body that exceeded the cap surfaces here, as does malformed multipart.
		s.writeError(w, r, http.StatusRequestEntityTooLarge, "upload_failed",
			"The upload could not be read. It may be larger than this instance allows.")
		return
	}
	defer func() { _ = file.Close() }()

	kind := r.FormValue("kind")
	if kind == "" {
		kind = registry.KindManual
	}
	if !validDocumentKind(kind) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_kind",
			"kind must be one of manual, receipt, warranty, photo, other.")
		return
	}

	ref, err := s.deps.Store.Put(r.Context(), file)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	// Determine the media type from the bytes, never from the upload.
	//
	// The client's Content-Type is attacker-controlled — any HTTP client sets it
	// freely, and a browser derives it from the file extension. Storing it and
	// later echoing it back turns "upload a manual you found on the web" into
	// script execution at this instance's own origin, with the session cookie
	// riding along. Sniffing is the only version of this that cannot be lied to.
	mediaType, err := s.sniffMediaType(ref.SHA256)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.deps.Registry.RecordBlob(r.Context(), ref, mediaType); err != nil {
		s.internalError(w, r, err)
		return
	}

	document, created, err := s.deps.Registry.CreateDocument(r.Context(), registry.NewDocument{
		DeviceID:   deviceID,
		BlobSHA256: ref.SHA256,
		Filename:   baseFilename(header.Filename),
		MediaType:  mediaType,
		Kind:       kind,
	})
	if err != nil {
		s.writeRegistryError(w, r, err)
		return
	}

	// Queue the probe whether or not the row is new: an earlier attempt may have
	// been interrupted before the job was created, and the probe is idempotent.
	job, err := s.deps.Ingest.EnqueueProbe(r.Context(), document.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	status := http.StatusCreated
	if !created {
		// The same bytes against the same device is the same document, not a
		// conflict: say so with 200 rather than inventing a duplicate.
		status = http.StatusOK
	}
	body := map[string]any{"document": document, "duplicate": !created}
	if job != nil {
		body["jobId"] = job.ID
	}
	writeJSON(w, status, body)
}

func validDocumentKind(kind string) bool {
	switch kind {
	case registry.KindManual, registry.KindReceipt, registry.KindWarranty,
		registry.KindPhoto, registry.KindOther:
		return true
	default:
		return false
	}
}

func (s *Server) handleGetDocument(w http.ResponseWriter, r *http.Request) {
	document, err := s.deps.Registry.GetDocument(r.Context(), chi.URLParam(r, "documentID"))
	if err != nil {
		s.writeRegistryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, document)
}

// handleDocumentGate answers the pre-flight question: what is in this document,
// what would be processed, and what that would cost.
func (s *Server) handleDocumentGate(w http.ResponseWriter, r *http.Request) {
	gate, err := s.deps.Ingest.Gate(r.Context(), chi.URLParam(r, "documentID"))
	if err != nil {
		s.writeRegistryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gate)
}

// handleDocumentLanguages returns one signal's view of the language map.
//
// The default is the reconciled view. Asking for a specific signal is what makes
// a disagreement inspectable rather than merely flagged — "the tag says DA, the
// index says FI" is answerable after the fact.
func (s *Server) handleDocumentLanguages(w http.ResponseWriter, r *http.Request) {
	source := doc.Source(r.URL.Query().Get("source"))
	if source == "" {
		source = doc.SourceReconciled
	}
	switch source {
	case doc.SourcePageTag, doc.SourceIndex, doc.SourceScript, doc.SourceDetector, doc.SourceReconciled:
	default:
		s.writeError(w, r, http.StatusBadRequest, "invalid_source",
			"source must be one of page-tag, index, script, detector, reconciled.")
		return
	}

	runs, err := s.deps.Registry.LanguageRuns(r.Context(), chi.URLParam(r, "documentID"), source)
	if err != nil {
		s.writeRegistryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": source, "runs": runs})
}

// handleDeclineDocument records that the user does not want the document
// processed. The original is kept regardless.
func (s *Server) handleDeclineDocument(w http.ResponseWriter, r *http.Request) {
	documentID := chi.URLParam(r, "documentID")
	if _, err := s.deps.Registry.GetDocument(r.Context(), documentID); err != nil {
		s.writeRegistryError(w, r, err)
		return
	}
	if err := s.deps.Ingest.Decline(r.Context(), documentID); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDocumentContent serves the stored original, byte for byte.
//
// This is the "own your data" promise at its most literal: whatever manualbox
// derives, the file you uploaded is retrievable unchanged. Served with
// ServeContent so range requests work, which is what lets a PDF viewer fetch one
// page at a time instead of a 15 MB download.
func (s *Server) handleDocumentContent(w http.ResponseWriter, r *http.Request) {
	document, err := s.deps.Registry.GetDocument(r.Context(), chi.URLParam(r, "documentID"))
	if err != nil {
		s.writeRegistryError(w, r, err)
		return
	}

	content, err := s.deps.Store.Open(document.BlobSHA256)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, r, http.StatusNotFound, "content_missing",
				"The stored content for this document is missing.")
			return
		}
		s.internalError(w, r, err)
		return
	}
	defer func() { _ = content.Close() }()

	// Only a short list of types may render in the browser. Anything else is
	// downloaded as opaque bytes, because rendering it would run it at this
	// instance's origin. SVG is deliberately absent from the safe list: it is an
	// XML document that can carry script.
	served, inline := safeInlineType(document.MediaType)
	w.Header().Set("Content-Type", served)
	// Without this a browser may sniff the body and render it as HTML regardless
	// of the type we sent, which would undo the allowlist above.
	w.Header().Set("X-Content-Type-Options", "nosniff")

	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	if name := document.Filename; name != "" {
		w.Header().Set("Content-Disposition", disposition+"; filename*=UTF-8''"+urlPathEscape(name))
	} else {
		w.Header().Set("Content-Disposition", disposition)
	}

	// The digest is the content, so the ETag is exact and the content immutable.
	w.Header().Set("ETag", `"`+document.BlobSHA256+`"`)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")

	seeker, ok := content.(io.ReadSeeker)
	if !ok {
		s.internalError(w, r, errors.New("api: stored content is not seekable"))
		return
	}
	http.ServeContent(w, r, document.Filename, document.UpdatedAt, seeker)
}

// urlPathEscape escapes a filename for a Content-Disposition header, so a name
// with a space or a non-ASCII character does not corrupt the header.
func urlPathEscape(name string) string { return url.PathEscape(name) }

// sniffBytes is how much of a file [Server.sniffMediaType] inspects.
// http.DetectContentType never looks at more than this.
const sniffBytes = 512

// sniffMediaType determines a stored blob's type from its own bytes.
func (s *Server) sniffMediaType(digest string) (string, error) {
	content, err := s.deps.Store.Open(digest)
	if err != nil {
		return "", err
	}
	defer func() { _ = content.Close() }()

	head := make([]byte, sniffBytes)
	n, err := io.ReadFull(content, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("api: read blob head: %w", err)
	}
	return http.DetectContentType(head[:n]), nil
}

// inlineSafeTypes are the media types a browser may render directly from this
// origin. Everything else is served as an attachment.
//
// The list is short on purpose. A document served inline shares the origin of
// the app and its session cookie, so anything that can execute — HTML, SVG, XML
// with a stylesheet — must not be on it, whatever the uploader called the file.
//
// text/plain is deliberately absent, and that is not an oversight.
// http.DetectContentType has no SVG signature and returns text/plain for any
// textual content it does not recognise, so the bucket contains SVG, XML and
// anything else script-bearing that dodges the HTML signatures. A current
// browser honouring nosniff renders it as text and nothing executes — but that
// would make one response header the only thing preventing it. The cost of
// leaving it out is that a genuine .txt downloads instead of displaying, which
// is a poor trade to reverse.
var inlineSafeTypes = map[string]bool{
	"application/pdf": true,
	"image/png":       true,
	"image/jpeg":      true,
	"image/gif":       true,
	"image/webp":      true,
}

// safeInlineType maps a stored media type onto what will actually be sent, and
// whether it may be displayed rather than downloaded.
func safeInlineType(stored string) (served string, inline bool) {
	// DetectContentType returns parameters such as "; charset=utf-8"; match on
	// the bare type.
	base := stored
	if i := strings.IndexByte(base, ';'); i >= 0 {
		base = base[:i]
	}
	base = strings.ToLower(strings.TrimSpace(base))

	if inlineSafeTypes[base] {
		// Re-send the bare type rather than the stored string, so no parameter
		// from the stored value is echoed back into the header.
		return base, true
	}
	return "application/octet-stream", false
}

// baseFilename reduces an uploaded name to its last path component.
//
// filepath.Base alone is not enough: it is platform-specific, so on the Linux
// server this targets it leaves a Windows path such as
// `C:\Users\alice\Downloads\manual.pdf` entirely intact — carrying the
// uploader's directory layout, and their username, into the database. Cut on
// both separators.
func baseFilename(name string) string {
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:]
	}
	return filepath.Base(name)
}

// writeRegistryError maps registry errors onto status codes.
func (s *Server) writeRegistryError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, registry.ErrNotFound):
		s.writeError(w, r, http.StatusNotFound, "not_found", "No such item.")
	case errors.Is(err, registry.ErrInvalid):
		s.writeError(w, r, http.StatusBadRequest, "invalid", err.Error())
	default:
		s.internalError(w, r, err)
	}
}
