// Package api is the HTTP layer: routing, middleware, and JSON handlers.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/gordon2/manualbox/internal/auth"
	"github.com/gordon2/manualbox/internal/config"
	"github.com/gordon2/manualbox/internal/db"
	"github.com/gordon2/manualbox/internal/frontend"
	"github.com/gordon2/manualbox/internal/ingest"
	"github.com/gordon2/manualbox/internal/jobs"
	"github.com/gordon2/manualbox/internal/registry"
	"github.com/gordon2/manualbox/internal/store"
)

// sessionCookie is the name of the session cookie.
const sessionCookie = "manualbox_session"

// Deps are the collaborators the API needs.
type Deps struct {
	Config   config.Config
	DB       *db.DB
	Store    *store.Store
	Auth     *auth.Service
	Jobs     *jobs.Queue
	Registry *registry.Service
	Ingest   *ingest.Service
	Logger   *slog.Logger
	Version  string
}

// Server serves the API and the embedded frontend.
type Server struct {
	deps   Deps
	log    *slog.Logger
	router chi.Router
}

// New builds a Server with all routes mounted.
func New(deps Deps) *Server {
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.DiscardHandler)
	}
	s := &Server{deps: deps, log: deps.Logger}
	s.routes()
	return s
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) routes() {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	// Deliberately not chi's middleware.RealIP: it rewrites RemoteAddr from
	// X-Forwarded-For / X-Real-IP / True-Client-IP whether or not a proxy set
	// them, so any client can forge its own address (GHSA-3fxj-6jh8-hvhx).
	// Since that address is logged and stored on sessions, it honours forwarded
	// headers only when the operator has declared a trusted proxy.
	r.Use(s.realIP)
	r.Use(s.logRequests)
	r.Use(s.recoverPanics)
	r.Use(middleware.Compress(5))
	r.Use(middleware.Timeout(60 * time.Second))
	// Defence in depth against content sniffing. The document route sets this
	// itself and serves anything unrecognised as an attachment, but a browser that
	// second-guesses a Content-Type anywhere on this origin can turn stored bytes
	// into script running beside the session cookie.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			next.ServeHTTP(w, r)
		})
	})

	r.Route("/api/v1", func(r chi.Router) {
		// Reject cross-site state changes before anything else looks at the body.
		r.Use(s.checkOrigin)

		// Unmatched paths under /api must answer JSON. Without these the router
		// falls through to the SPA handler below and a mistyped endpoint returns
		// 200 with an HTML page — which looks like a working server returning
		// nonsense, and is thoroughly confusing from a client.
		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			s.writeError(w, r, http.StatusNotFound, "not_found", "No such endpoint.")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			s.writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed",
				"That method is not allowed on this endpoint.")
		})

		r.Get("/health", s.handleHealth)

		// Setup is open by necessity — there is no user to authenticate as yet —
		// and is guarded by the service refusing to run twice.
		r.Get("/setup", s.handleSetupStatus)
		r.Post("/setup", s.handleSetup)

		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/logout", s.handleLogout)

		// Everything below requires a session.
		r.Group(func(r chi.Router) {
			r.Use(s.requireUser)

			r.Get("/auth/me", s.handleMe)
			r.Post("/auth/password", s.handleChangePassword)

			r.Get("/instance", s.handleInstance)

			r.Get("/jobs", s.handleListJobs)
			r.Get("/jobs/events", s.handleJobEvents)
			r.Get("/jobs/{jobID}", s.handleGetJob)
			r.Post("/jobs/{jobID}/cancel", s.handleCancelJob)

			r.Get("/locations", s.handleListLocations)
			r.Post("/locations", s.handleCreateLocation)

			r.Get("/devices", s.handleListDevices)
			r.Post("/devices", s.handleCreateDevice)
			r.Get("/devices/{deviceID}", s.handleGetDevice)
			r.Patch("/devices/{deviceID}", s.handleUpdateDevice)
			r.Delete("/devices/{deviceID}", s.handleDeleteDevice)

			r.Get("/devices/{deviceID}/documents", s.handleListDocuments)
			r.Post("/devices/{deviceID}/documents", s.handleUploadDocument)

			r.Get("/documents/{documentID}", s.handleGetDocument)
			// The gate is the decision point: what is in the document, what would
			// be processed, and what that would cost.
			r.Get("/documents/{documentID}/gate", s.handleDocumentGate)
			r.Get("/documents/{documentID}/languages", s.handleDocumentLanguages)
			r.Get("/documents/{documentID}/content", s.handleDocumentContent)
			r.Post("/documents/{documentID}/decline", s.handleDeclineDocument)
			// The other half of the decision. Approving is what authorises the
			// first work in the pipeline that is not free.
			r.Post("/documents/{documentID}/approve", s.handleApproveDocument)

			// What the conversion produced. Deliberately not served from
			// /content, which is the original bytes and stays that way.
			r.Get("/documents/{documentID}/conversion", s.handleDocumentConversion)
			r.Get("/documents/{documentID}/figures/{sha256}", s.handleDocumentFigure)
		})
	})

	// Anything not under /api is the single-page app.
	r.NotFound(frontend.Handler().ServeHTTP)

	s.router = r
}

// --- responses ---

// errorBody is the single error shape the API returns, so a client has exactly
// one thing to parse on failure.
type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already sent, so there is nothing to correct — but a
		// silent truncated body would be baffling, so leave a trace.
		slog.Default().Error("encoding a response failed", "error", err)
	}
}

// writeError sends a structured error. code is a stable machine-readable string
// the frontend can branch on; message is for humans.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	var body errorBody
	body.Error.Code = code
	body.Error.Message = message

	// 5xx means we broke something, so it belongs in the log with the request ID.
	if status >= http.StatusInternalServerError {
		s.log.Error("request failed",
			"status", status, "code", code, "message", message,
			"method", r.Method, "path", r.URL.Path,
			"request_id", middleware.GetReqID(r.Context()))
	}
	writeJSON(w, status, body)
}

// internalError reports an unexpected failure without leaking its detail to the
// client, while keeping the detail in the log.
func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("internal error",
		"error", err, "method", r.Method, "path", r.URL.Path,
		"request_id", middleware.GetReqID(r.Context()))
	s.writeError(w, r, http.StatusInternalServerError, "internal_error", "Something went wrong on the server.")
}

// decodeJSON reads a JSON body with a size limit, rejecting unknown fields so a
// typo'd client field is reported rather than silently ignored.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	const maxBody = 1 << 20 // 1 MiB is generous for any JSON this API accepts
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	// Reject trailing content so "{}{}" is not silently accepted.
	if dec.More() {
		return errors.New("unexpected trailing content after the JSON body")
	}
	return nil
}
