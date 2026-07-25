package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/gordon2/manualbox/internal/auth"
)

// contextKey is unexported so nothing outside this package can collide with it.
type contextKey int

const (
	userKey contextKey = iota
	sessionKey
)

// UserFrom returns the authenticated user, if the request went through
// requireUser.
func UserFrom(ctx context.Context) *auth.User {
	u, _ := ctx.Value(userKey).(*auth.User)
	return u
}

// SessionFrom returns the authenticated session.
func SessionFrom(ctx context.Context) *auth.Session {
	s, _ := ctx.Value(sessionKey).(*auth.Session)
	return s
}

// realIP resolves the client address, honouring proxy headers only when the
// operator has opted in via TrustProxy.
//
// The rightmost X-Forwarded-For entry is used rather than the leftmost. A client
// can put anything it likes in the header, and a proxy appends the address it
// actually observed — so the last entry is the only one not under the client's
// control. With a proxy that overwrites the header instead of appending, there is
// a single value and the two are the same.
func (s *Server) realIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.deps.Config.Server.TrustProxy {
			if ip := forwardedClientIP(r); ip != "" {
				r.RemoteAddr = ip
			}
		}
		next.ServeHTTP(w, r)
	})
}

// forwardedClientIP extracts the proxy-reported client address.
func forwardedClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if candidate := strings.TrimSpace(parts[len(parts)-1]); candidate != "" {
			return candidate
		}
	}
	// X-Real-IP is set by a single proxy hop and has no list semantics.
	return strings.TrimSpace(r.Header.Get("X-Real-IP"))
}

// logRequests logs one line per request at completion, with the status, duration,
// and request ID so a report of "it failed at 14:03" is traceable.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		// The SSE stream is long-lived; logging it on completion would report a
		// misleading multi-minute "request duration".
		if r.URL.Path == "/api/v1/jobs/events" {
			return
		}

		level := slogLevelForStatus(ww.Status())
		s.log.Log(r.Context(), level, "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"duration", time.Since(start).Round(time.Millisecond),
			"ip", r.RemoteAddr,
			"request_id", middleware.GetReqID(r.Context()),
		)
	})
}

// recoverPanics turns a panicking handler into a 500 instead of a dropped
// connection, and keeps the server alive.
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				// http.ErrAbortHandler is the documented way to abort a response on
				// purpose; it is not an error.
				if rec == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http documents
					panic(rec)
				}
				s.log.Error("handler panicked",
					"panic", rec,
					"method", r.Method, "path", r.URL.Path,
					"request_id", middleware.GetReqID(r.Context()))
				s.writeError(w, r, http.StatusInternalServerError, "internal_error", "Something went wrong on the server.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// checkOrigin blocks cross-site state-changing requests.
//
// Session auth rides a cookie, so CSRF is a real concern. SameSite=Lax already
// stops the cookie being attached to a cross-site POST in current browsers; this
// is the second layer, for older browsers and for anything that sends an Origin
// we do not recognise. Safe methods are untouched, and a request with no Origin
// at all (curl, a native client) is allowed because there is no ambient cookie
// for an attacker to exploit in that case.
func (s *Server) checkOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if s.originAllowed(origin, r) {
			next.ServeHTTP(w, r)
			return
		}

		s.log.Warn("blocked a cross-origin state-changing request",
			"origin", origin, "method", r.Method, "path", r.URL.Path, "ip", r.RemoteAddr)
		s.writeError(w, r, http.StatusForbidden, "cross_origin_blocked",
			"This request came from another origin and was blocked.")
	})
}

// originAllowed reports whether origin matches the configured base URL or the
// request's own host.
func (s *Server) originAllowed(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if base, err := url.Parse(s.deps.Config.Server.BaseURL); err == nil && base.Host != "" {
		if strings.EqualFold(u.Host, base.Host) {
			return true
		}
	}
	return false
}

// requireUser rejects unauthenticated requests and attaches the user to the
// context for downstream handlers.
func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := sessionTokenFrom(r)

		user, session, err := s.deps.Auth.Authenticate(r.Context(), token)
		if err != nil {
			if errors.Is(err, auth.ErrNoSession) {
				// Clear a cookie that no longer works, so a stale token does not
				// keep producing 401s on every page load.
				if token != "" {
					s.clearSessionCookie(w, r)
				}
				s.writeError(w, r, http.StatusUnauthorized, "unauthenticated", "You need to sign in.")
				return
			}
			s.internalError(w, r, err)
			return
		}

		ctx := context.WithValue(r.Context(), userKey, user)
		ctx = context.WithValue(ctx, sessionKey, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// sessionTokenFrom reads the session token from the cookie, falling back to a
// bearer header so scripts and the CLI can authenticate without cookie handling.
func sessionTokenFrom(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		return c.Value
	}
	if h := r.Header.Get("Authorization"); h != "" {
		if token, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(token)
		}
	}
	return ""
}

// setSessionCookie issues the session cookie.
func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:  sessionCookie,
		Value: token,
		Path:  "/",
		// HttpOnly keeps the token away from any script on the page, so an XSS bug
		// in a dependency cannot read it.
		HttpOnly: true,
		// Lax is the CSRF baseline: the cookie is not sent on cross-site POSTs but
		// survives following an ordinary link into the app.
		SameSite: http.SameSiteLaxMode,
		Secure:   s.requestIsHTTPS(r),
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.requestIsHTTPS(r),
		MaxAge:   -1,
	})
}

// requestIsHTTPS reports whether the client's connection is encrypted.
//
// X-Forwarded-Proto is only believed when the operator has said they run behind a
// proxy. Trusting it unconditionally would let any client set the header and
// influence the Secure flag.
func (s *Server) requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if s.deps.Config.Server.TrustProxy {
		if proto := r.Header.Get("X-Forwarded-Proto"); strings.EqualFold(proto, "https") {
			return true
		}
	}
	return strings.HasPrefix(s.deps.Config.Server.BaseURL, "https://")
}
