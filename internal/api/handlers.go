package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/gordon2/manualbox/internal/auth"
	"github.com/gordon2/manualbox/internal/config"
	"github.com/gordon2/manualbox/internal/extern"
	"github.com/gordon2/manualbox/internal/jobs"
)

// --- health ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Touch the database so this reports real readiness rather than merely that
	// the process is up — a health check that cannot fail is worthless.
	version, err := s.deps.DB.Version(r.Context())
	if err != nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "database_unavailable", "The database is not reachable.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"version":       s.deps.Version,
		"schemaVersion": version,
	})
}

// --- setup ---

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	needs, err := s.deps.Auth.NeedsSetup(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"needsSetup": needs})
}

type setupRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
}

// handleSetup creates the first administrator and signs them straight in, so the
// first-run flow does not ask for the same password twice in a row.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}

	user, err := s.deps.Auth.Setup(r.Context(), req.Email, req.Password, req.DisplayName)
	switch {
	case errors.Is(err, auth.ErrSetupComplete):
		s.writeError(w, r, http.StatusConflict, "setup_complete", "This instance has already been set up.")
		return
	case errors.Is(err, auth.ErrWeakPassword):
		s.writeError(w, r, http.StatusUnprocessableEntity, "weak_password", err.Error())
		return
	case errors.Is(err, auth.ErrInvalidEmail):
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_email", "That email address is not valid.")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}

	token, _, err := s.deps.Auth.Login(r.Context(), req.Email, req.Password, r.UserAgent(), r.RemoteAddr)
	if err != nil {
		// The account exists, so report success and let them sign in normally
		// rather than implying setup failed.
		s.log.Error("signing in immediately after setup failed", "error", err)
		writeJSON(w, http.StatusCreated, map[string]any{"user": user})
		return
	}

	s.setSessionCookie(w, r, token, time.Now().Add(auth.DefaultSessionTTL))
	writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

// --- auth ---

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}

	token, user, err := s.deps.Auth.Login(r.Context(), req.Email, req.Password, r.UserAgent(), r.RemoteAddr)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			// Deliberately the same response whether the address is unknown or the
			// password is wrong.
			s.writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "Email or password is incorrect.")
			return
		}
		s.internalError(w, r, err)
		return
	}

	s.setSessionCookie(w, r, token, time.Now().Add(auth.DefaultSessionTTL))
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if token := sessionTokenFrom(r); token != "" {
		if err := s.deps.Auth.Logout(r.Context(), token); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	s.clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"user":    UserFrom(r.Context()),
		"session": SessionFrom(r.Context()),
	})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}

	user := UserFrom(r.Context())
	err := s.deps.Auth.ChangePassword(r.Context(), user.ID, req.CurrentPassword, req.NewPassword)
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		s.writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "Your current password is incorrect.")
		return
	case errors.Is(err, auth.ErrWeakPassword):
		s.writeError(w, r, http.StatusUnprocessableEntity, "weak_password", err.Error())
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}

	// Every session was revoked, including this one, so drop the cookie too.
	s.clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// --- instance ---

// handleInstance reports what this deployment can currently do, so the UI can
// hide features that have no provider configured instead of offering a button
// that always fails.
func (s *Server) handleInstance(w http.ResponseWriter, r *http.Request) {
	cfg := s.deps.Config

	tools := make(map[string]any, len(extern.All()))
	for _, st := range extern.ProbeAll(r.Context()) {
		tools[st.Name] = map[string]any{
			"available": st.Found,
			"version":   st.Version,
			"purpose":   st.Purpose,
			"install":   st.InstallHint(),
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"version":         s.deps.Version,
		"languages":       cfg.Content.Languages,
		"primaryLanguage": cfg.PrimaryLanguage(),
		"capabilities": map[string]bool{
			"convert":   cfg.Providers.Convert.Enabled(),
			"ocr":       cfg.Providers.OCR.Enabled(),
			"translate": cfg.Providers.Translate.Enabled(),
			"extract":   cfg.Providers.Extract.Enabled(),
		},
		"providers": map[string]any{
			"convert":   providerInfo(cfg.Providers.Convert),
			"ocr":       providerInfo(cfg.Providers.OCR),
			"translate": providerInfo(cfg.Providers.Translate),
			"extract":   providerInfo(cfg.Providers.Extract),
		},
		"externalTools": tools,
	})
}

// providerInfo exposes which adapter is configured without ever exposing its key.
func providerInfo(p config.Provider) map[string]any {
	return map[string]any{
		"kind":      p.Kind,
		"model":     p.Model,
		"enabled":   p.Enabled(),
		"hasAPIKey": p.APIKey.Set(),
	}
}

// --- jobs ---

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	state := jobs.State(r.URL.Query().Get("state"))
	switch state {
	case "", jobs.StateQueued, jobs.StateRunning, jobs.StateSucceeded, jobs.StateFailed, jobs.StateCancelled:
	default:
		s.writeError(w, r, http.StatusBadRequest, "invalid_state", fmt.Sprintf("%q is not a job state.", state))
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 500 {
			s.writeError(w, r, http.StatusBadRequest, "invalid_limit", "limit must be a number between 1 and 500.")
			return
		}
		limit = n
	}

	list, err := s.deps.Jobs.List(r.Context(), state, limit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": list})
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.deps.Jobs.Get(r.Context(), chi.URLParam(r, "jobID"))
	if err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			s.writeError(w, r, http.StatusNotFound, "not_found", "No such job.")
			return
		}
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	err := s.deps.Jobs.Cancel(r.Context(), chi.URLParam(r, "jobID"))
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		s.writeError(w, r, http.StatusNotFound, "not_found", "No such job.")
		return
	case err != nil:
		// Cancel reports a plain error when the job already finished, which is a
		// conflict rather than a server fault.
		s.writeError(w, r, http.StatusConflict, "not_cancellable", "That job has already finished.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleJobEvents streams job progress as Server-Sent Events.
//
// SSE rather than WebSockets: the traffic is one-directional, it survives proxies
// that mangle upgrades, and browsers reconnect on their own. The stream opens with
// the current active jobs so a client that connects mid-conversion sees state
// immediately instead of waiting for the next change.
func (s *Server) handleJobEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, r, http.StatusInternalServerError, "streaming_unsupported", "This server cannot stream events.")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// Tell nginx not to buffer, or events arrive in batches minutes late.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Subscribe before the snapshot, so a change happening between the two is
	// buffered rather than missed.
	events, unsubscribe := s.deps.Jobs.Broker().Subscribe()
	defer unsubscribe()

	if active, err := s.deps.Jobs.Active(r.Context()); err == nil {
		for _, job := range active {
			s.writeSSE(w, "job", jobs.Event{
				JobID: job.ID, Kind: job.Kind, State: job.State,
				Progress: job.Progress, Note: job.ProgressNote,
				Error: job.LastError, At: job.UpdatedAt,
			})
		}
		flusher.Flush()
	}

	// Comment-only keepalives stop idle proxies and load balancers from closing a
	// stream that is simply quiet.
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-events:
			if !open {
				return
			}
			s.writeSSE(w, "job", ev)
			flusher.Flush()
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) writeSSE(w http.ResponseWriter, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		s.log.Error("encoding an SSE payload failed", "error", err)
		return
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

// slogLevelForStatus keeps ordinary traffic at debug so the log is readable, and
// promotes failures.
func slogLevelForStatus(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelDebug
	}
}
