package api

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/gordon2/manualbox/internal/registry"
	"github.com/gordon2/manualbox/internal/store"
)

// handleApproveDocument authorises the work the gate reported and queues it.
//
// The body is empty, and that is the point: the scope is the household's
// configured languages, which is what the gate showed. Accepting a language list
// here would let a caller approve something other than what the user was told
// about — see [ingest.Service.Approve].
func (s *Server) handleApproveDocument(w http.ResponseWriter, r *http.Request) {
	documentID := chi.URLParam(r, "documentID")
	job, err := s.deps.Ingest.Approve(r.Context(), documentID)
	if err != nil {
		s.writeRegistryError(w, r, err)
		return
	}

	document, err := s.deps.Registry.GetDocument(r.Context(), documentID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	body := map[string]any{"document": document}
	if job != nil {
		body["jobId"] = job.ID
	}
	// 202: the conversion is queued, not done. Progress arrives on the existing job
	// event stream, the same way an upload's probe does.
	writeJSON(w, http.StatusAccepted, body)
}

// handleDocumentConversion serves what the conversion produced: the readable
// blocks and the pictures.
//
// # Why this is not /content
//
// /documents/{id}/content is the stored original, byte for byte, and
// docs/design/privacy.md is explicit that the original is kept whole. That path is
// the "own your data" promise at its most literal, so it keeps serving the PDF and
// nothing else. Overloading it would mean either content negotiation — which is
// invisible in a URL, so the derived view could not be linked or bookmarked — or a
// query parameter that silently changes the response from bytes to JSON. A
// separate path costs one route and keeps both answers unambiguous.
//
// `?lang=de` is the funnel's own query and returns one language's blocks together
// with the pictures belonging to it, which includes every picture belonging to no
// language. Omitting the parameter returns everything stored, which is already only
// what the household's scope charged for. `?lang=` with an empty value is a third,
// real question: the blocks and pictures nothing could name, which no other
// language's answer contains.
func (s *Server) handleDocumentConversion(w http.ResponseWriter, r *http.Request) {
	documentID := chi.URLParam(r, "documentID")
	document, err := s.deps.Registry.GetDocument(r.Context(), documentID)
	if err != nil {
		s.writeRegistryError(w, r, err)
		return
	}

	var (
		blocks  []registry.Block
		figures []registry.Figure
	)
	lang := r.URL.Query().Get("lang")
	// Has, not a non-empty value: "" is a question about the unnamed content rather
	// than the absence of a filter.
	if r.URL.Query().Has("lang") {
		blocks, err = s.deps.Registry.BlocksByLang(r.Context(), documentID, lang)
		if err == nil {
			figures, err = s.deps.Registry.FiguresByLang(r.Context(), documentID, lang)
		}
	} else {
		blocks, err = s.deps.Registry.Blocks(r.Context(), documentID)
		if err == nil {
			figures, err = s.deps.Registry.Figures(r.Context(), documentID)
		}
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	body := map[string]any{
		"documentId": document.ID,
		// The state is what distinguishes "converted and empty" from "not converted",
		// which no count can: a document that has not been through the gate has no
		// blocks, and that is not the claim that it has no content.
		"state":   document.State,
		"blocks":  blocks,
		"figures": figures,
	}
	if r.URL.Query().Has("lang") {
		body["lang"] = lang
	}
	if document.LastError != "" {
		body["lastError"] = document.LastError
	}
	writeJSON(w, http.StatusOK, body)
}

// handleDocumentFigure serves one rendered figure's PNG.
//
// Addressed by digest rather than by page and index, because the digest is what
// the conversion response already carries, and because the content is the name: the
// ETag is exact and the bytes are immutable for ever.
//
// The digest is checked against this document's own figures rather than being
// handed to the blob store directly. The store is content addressed and holds every
// original anyone has uploaded, so a route that opened any digest a caller named
// would serve another household's manual to whoever could guess a hash.
func (s *Server) handleDocumentFigure(w http.ResponseWriter, r *http.Request) {
	documentID := chi.URLParam(r, "documentID")
	digest := chi.URLParam(r, "sha256")
	if err := store.ValidDigest(digest); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_digest",
			"A figure is addressed by its SHA-256.")
		return
	}

	figures, err := s.deps.Registry.Figures(r.Context(), documentID)
	if err != nil {
		s.writeRegistryError(w, r, err)
		return
	}
	found := false
	for i := range figures {
		if figures[i].SHA256 == digest {
			found = true
			break
		}
	}
	if !found {
		s.writeError(w, r, http.StatusNotFound, "not_found",
			"This document has no such figure.")
		return
	}

	content, err := s.deps.Store.Open(digest)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, r, http.StatusNotFound, "content_missing",
				"The stored bytes for this figure are missing.")
			return
		}
		s.internalError(w, r, err)
		return
	}
	defer func() { _ = content.Close() }()

	// image/png is checked rather than assumed: internal/doc renders with pdftoppm
	// -png and verifies the signature and IHDR before returning the bytes, and
	// registry.SaveConversion records that type against the blob.
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", `"`+digest+`"`)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")

	seeker, ok := content.(io.ReadSeeker)
	if !ok {
		s.internalError(w, r, errors.New("api: stored figure is not seekable"))
		return
	}
	http.ServeContent(w, r, digest+".png", time.Time{}, seeker)
}
