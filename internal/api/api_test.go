package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gordon2/manualbox/internal/auth"
	"github.com/gordon2/manualbox/internal/config"
	"github.com/gordon2/manualbox/internal/db"
	"github.com/gordon2/manualbox/internal/ingest"
	"github.com/gordon2/manualbox/internal/jobs"
	"github.com/gordon2/manualbox/internal/logging"
	"github.com/gordon2/manualbox/internal/registry"
	"github.com/gordon2/manualbox/internal/store"
)

const testPassword = "a perfectly fine passphrase"

type harness struct {
	server   *httptest.Server
	queue    *jobs.Queue
	auth     *auth.Service
	registry *registry.Service
	client   *http.Client
}

// newHarness starts a real server over a real database, so these tests exercise
// routing, middleware, and handlers together rather than mocking the layer under
// test.
func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()

	database, err := db.Open(ctx, db.Options{Path: filepath.Join(t.TempDir(), "api.db")})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	blobs, err := store.New(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	authService := auth.New(database, auth.Options{Logger: logging.Discard()})
	queue := jobs.NewQueue(database, logging.Discard())
	t.Cleanup(queue.Broker().Close)

	cfg := config.Default()
	cfg.Content.Languages = []string{"de", "en"}

	registryService := registry.New(database, registry.Options{})
	ingestService := ingest.New(ingest.Deps{
		Config: cfg, Registry: registryService, Store: blobs, Jobs: queue,
	})

	srv := New(Deps{
		Config: cfg, DB: database, Store: blobs, Auth: authService,
		Jobs: queue, Registry: registryService, Ingest: ingestService,
		Logger: logging.Discard(), Version: "test",
	})

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// A cookie jar so the session behaves as it would in a browser.
	jar := &cookieJar{}
	return &harness{
		server:   ts,
		queue:    queue,
		auth:     authService,
		registry: registryService,
		client:   &http.Client{Jar: jar, Timeout: 10 * time.Second},
	}
}

func (h *harness) do(t *testing.T, method, path string, body any, headers ...[2]string) *http.Response {
	t.Helper()

	var reader *strings.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
		reader = strings.NewReader(string(encoded))
	} else {
		reader = strings.NewReader("")
	}

	req, err := http.NewRequestWithContext(context.Background(), method, h.server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, kv := range headers {
		req.Header.Set(kv[0], kv[1])
	}

	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// status performs a request, closes the body, and returns only the status code.
// Most assertions care about nothing else, and an inline h.do() in an if-condition
// leaks the response.
func (h *harness) status(t *testing.T, method, path string, body any, headers ...[2]string) int {
	t.Helper()
	resp := h.do(t, method, path, body, headers...)
	defer resp.Body.Close()
	return resp.StatusCode
}

// decode reads a JSON body into a map.
func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return out
}

// completeSetup runs first-run setup, leaving the harness authenticated.
func (h *harness) completeSetup(t *testing.T) {
	t.Helper()
	resp := h.do(t, http.MethodPost, "/api/v1/setup", map[string]string{
		"email": "owner@example.com", "password": testPassword, "displayName": "Test Owner",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("setup returned %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()
}

// --- health and setup ---

func TestHealth(t *testing.T) {
	h := newHarness(t)

	resp := h.do(t, http.MethodGet, "/api/v1/health", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decode(t, resp)
	if body["status"] != "ok" {
		t.Errorf("status = %v", body["status"])
	}
	// Health touches the database, so a non-zero schema version proves it is
	// reporting real readiness and not just that the process is alive.
	if v, ok := body["schemaVersion"].(float64); !ok || v < 1 {
		t.Errorf("schemaVersion = %v, want at least 1", body["schemaVersion"])
	}
}

func TestSetupFlow(t *testing.T) {
	h := newHarness(t)

	if body := decode(t, h.do(t, http.MethodGet, "/api/v1/setup", nil)); body["needsSetup"] != true { //nolint:bodyclose // decode closes it
		t.Fatal("a fresh instance should report needsSetup")
	}

	h.completeSetup(t)

	if body := decode(t, h.do(t, http.MethodGet, "/api/v1/setup", nil)); body["needsSetup"] != false { //nolint:bodyclose // decode closes it
		t.Error("setup should be complete")
	}

	// Setup logs the new admin straight in, so the session works immediately.
	resp := h.do(t, http.MethodGet, "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me after setup returned %d, want 200 — setup should sign the user in", resp.StatusCode)
	}
	body := decode(t, resp)
	user, _ := body["user"].(map[string]any)
	if user["email"] != "owner@example.com" {
		t.Errorf("user = %v", user)
	}
	if user["role"] != "admin" {
		t.Errorf("role = %v, want admin", user["role"])
	}
}

func TestSetupTwiceIsConflict(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)

	resp := h.do(t, http.MethodPost, "/api/v1/setup", map[string]string{
		"email": "attacker@example.com", "password": testPassword,
	})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409 — the setup endpoint stays routable and must stay guarded", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "setup_complete" {
		t.Errorf("code = %q", code)
	}
}

func TestSetupValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     map[string]string
		wantCode string
	}{
		{"weak password", map[string]string{"email": "a@example.com", "password": "short"}, "weak_password"},
		{"bad email", map[string]string{"email": "nope", "password": testPassword}, "invalid_email"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			resp := h.do(t, http.MethodPost, "/api/v1/setup", tc.body)
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422", resp.StatusCode)
			}
			if code := errorCode(t, resp); code != tc.wantCode {
				t.Errorf("code = %q, want %q", code, tc.wantCode)
			}
		})
	}
}

// --- authentication ---

func TestProtectedRoutesRequireASession(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)
	h.client.Jar = &cookieJar{} // forget the session

	// Every route behind requireUser must refuse an anonymous caller. Listing
	// them explicitly means a new route added outside the guarded group shows up
	// here as a failure.
	for _, path := range []string{
		"/api/v1/auth/me",
		"/api/v1/instance",
		"/api/v1/jobs",
		"/api/v1/jobs/job_123",
		"/api/v1/jobs/events",
		// The registry and document routes. This list went stale once already —
		// eleven routes were added without being added here — so the guarantee in
		// the comment above was not actually being enforced for any of them.
		"/api/v1/locations",
		"/api/v1/devices",
		"/api/v1/devices/dev_123",
		"/api/v1/devices/dev_123/documents",
		"/api/v1/documents/doc_123",
		"/api/v1/documents/doc_123/gate",
		"/api/v1/documents/doc_123/languages",
		"/api/v1/documents/doc_123/content",
		"/api/v1/documents/doc_123/conversion",
		// Search reads every converted manual in the household, so it is the last
		// route that may answer an anonymous caller.
		"/api/v1/search",
	} {
		resp := h.do(t, http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s returned %d, want 401", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestLogin(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)
	h.client.Jar = &cookieJar{}

	resp := h.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "owner@example.com", "password": testPassword,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login returned %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	me := h.do(t, http.MethodGet, "/api/v1/auth/me", nil)
	defer me.Body.Close()
	if me.StatusCode != http.StatusOK {
		t.Errorf("me after login returned %d", me.StatusCode)
	}
}

func TestLoginFailureIsIndistinguishable(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)

	unknown := h.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "nobody@example.com", "password": testPassword,
	})
	wrong := h.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "owner@example.com", "password": "definitely wrong",
	})

	if unknown.StatusCode != http.StatusUnauthorized || wrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("statuses = %d and %d, want 401 for both", unknown.StatusCode, wrong.StatusCode)
	}
	// An attacker must not be able to enumerate registered addresses.
	u, w := decode(t, unknown), decode(t, wrong)
	if !equalJSON(u, w) {
		t.Errorf("responses differ and leak account existence:\n unknown: %v\n   wrong: %v", u, w)
	}
}

func TestLogout(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)

	resp := h.do(t, http.MethodPost, "/api/v1/auth/logout", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout returned %d, want 204", resp.StatusCode)
	}
	resp.Body.Close()

	if got := h.status(t, http.MethodGet, "/api/v1/auth/me", nil); got != http.StatusUnauthorized {
		t.Errorf("me after logout returned %d, want 401", got)
	}
}

func TestSessionCookieIsHardened(t *testing.T) {
	h := newHarness(t)

	resp := h.do(t, http.MethodPost, "/api/v1/setup", map[string]string{
		"email": "owner@example.com", "password": testPassword,
	})
	defer resp.Body.Close()

	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("setup did not issue a session cookie")
	}
	// HttpOnly keeps the token away from page scripts; SameSite=Lax is the CSRF
	// baseline. Both are load-bearing, so assert them rather than assume.
	if !cookie.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("Path = %q, want /", cookie.Path)
	}
}

func TestBearerTokenAuthWorks(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)

	// A script or the CLI should be able to authenticate without cookie handling.
	token, _, err := h.auth.Login(context.Background(), "owner@example.com", testPassword, "cli", "")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	h.client.Jar = &cookieJar{}

	resp := h.do(t, http.MethodGet, "/api/v1/auth/me", nil, [2]string{"Authorization", "Bearer " + token})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("bearer auth returned %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}

// --- CSRF ---

func TestCrossOriginStateChangeIsBlocked(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)

	resp := h.do(t, http.MethodPost, "/api/v1/auth/logout", nil,
		[2]string{"Origin", "https://evil.example.com"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "cross_origin_blocked" {
		t.Errorf("code = %q", code)
	}

	// The session must still work — the request was refused, not the session.
	if got := h.status(t, http.MethodGet, "/api/v1/auth/me", nil); got != http.StatusOK {
		t.Errorf("session was damaged by a blocked request: me returned %d", got)
	}
}

func TestSameOriginStateChangeIsAllowed(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)

	resp := h.do(t, http.MethodPost, "/api/v1/auth/logout", nil,
		[2]string{"Origin", h.server.URL})
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204 for a same-origin request", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCrossOriginReadIsAllowed(t *testing.T) {
	h := newHarness(t)

	// Safe methods carry no CSRF risk and must not be blocked.
	resp := h.do(t, http.MethodGet, "/api/v1/health", nil,
		[2]string{"Origin", "https://elsewhere.example.com"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 — GET is safe", resp.StatusCode)
	}
	resp.Body.Close()
}

// --- request bodies ---

func TestUnknownJSONFieldIsRejected(t *testing.T) {
	h := newHarness(t)

	// Silently ignoring an unrecognised field hides client bugs, and would hide a
	// privilege-escalation attempt like {"role":"admin"}.
	resp := h.do(t, http.MethodPost, "/api/v1/setup", map[string]any{
		"email": "a@example.com", "password": testPassword, "role": "admin",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "invalid_body" {
		t.Errorf("code = %q", code)
	}
}

func TestMalformedJSONIsRejected(t *testing.T) {
	h := newHarness(t)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		h.server.URL+"/api/v1/setup", strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// --- jobs ---

func TestJobsEndpoints(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)
	ctx := context.Background()

	job, err := h.queue.Enqueue(ctx, "convert", map[string]string{"documentId": "doc_1"}, jobs.EnqueueOptions{})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	list := decode(t, h.do(t, http.MethodGet, "/api/v1/jobs", nil)) //nolint:bodyclose // decode closes it
	items, _ := list["jobs"].([]any)
	if len(items) != 1 {
		t.Fatalf("listed %d jobs, want 1", len(items))
	}

	single := decode(t, h.do(t, http.MethodGet, "/api/v1/jobs/"+job.ID, nil)) //nolint:bodyclose // decode closes it
	if single["id"] != job.ID {
		t.Errorf("id = %v, want %v", single["id"], job.ID)
	}

	resp := h.do(t, http.MethodGet, "/api/v1/jobs/job_01MISSING0000000000000000", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing job returned %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestJobQueryValidation(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)

	for _, tc := range []struct{ query, wantCode string }{
		{"?state=nonsense", "invalid_state"},
		{"?limit=0", "invalid_limit"},
		{"?limit=99999", "invalid_limit"},
		{"?limit=abc", "invalid_limit"},
	} {
		resp := h.do(t, http.MethodGet, "/api/v1/jobs"+tc.query, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET /jobs%s returned %d, want 400", tc.query, resp.StatusCode)
			resp.Body.Close()
			continue
		}
		if code := errorCode(t, resp); code != tc.wantCode {
			t.Errorf("GET /jobs%s code = %q, want %q", tc.query, code, tc.wantCode)
		}
	}
}

func TestCancelJob(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)

	job, err := h.queue.Enqueue(context.Background(), "convert", nil, jobs.EnqueueOptions{})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	resp := h.do(t, http.MethodPost, "/api/v1/jobs/"+job.ID+"/cancel", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel returned %d, want 204", resp.StatusCode)
	}
	resp.Body.Close()

	// Cancelling twice is a conflict, not a 500 and not a silent success.
	resp = h.do(t, http.MethodPost, "/api/v1/jobs/"+job.ID+"/cancel", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second cancel returned %d, want 409", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestJobEventStream checks the SSE contract the UI depends on.
func TestJobEventStream(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.server.URL+"/api/v1/jobs/events", http.NoBody)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	// Copy the session cookie onto the streaming request.
	for _, c := range h.client.Jar.Cookies(mustParseURL(t, h.server.URL)) {
		req.AddCookie(c)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	// Without this, nginx buffers the stream and events arrive minutes late.
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", got)
	}

	// Enqueue after the stream is open; the event must arrive over the wire.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = h.queue.Enqueue(context.Background(), "translate", nil, jobs.EnqueueOptions{})
	}()

	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(5 * time.Second)
	var sawEvent, sawData bool
	for time.Now().Before(deadline) && !sawData {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read stream: %v", err)
		}
		switch {
		case strings.HasPrefix(line, "event: job"):
			sawEvent = true
		case sawEvent && strings.HasPrefix(line, "data: "):
			var ev jobs.Event
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
				t.Fatalf("decode SSE payload: %v", err)
			}
			if ev.Kind != "translate" {
				t.Errorf("event kind = %q, want translate", ev.Kind)
			}
			if ev.JobID == "" {
				t.Error("event carries no job id")
			}
			sawData = true
		}
	}
	if !sawData {
		t.Error("no job event arrived on the stream within the deadline")
	}
}

// --- instance ---

func TestInstanceNeverLeaksAnAPIKey(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)

	// Reconfigure with a key present, then confirm the endpoint reports only that
	// a key exists.
	const secret = "sk-ant-must-not-appear"
	cfg := config.Default()
	cfg.Providers.Translate = config.Provider{Kind: "claude", Model: "claude-sonnet-5", APIKey: config.Secret(secret)}

	srv := New(Deps{
		Config: cfg, DB: nil, Auth: h.auth, Jobs: h.queue,
		Logger: logging.Discard(), Version: "test",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance", http.NoBody)
	srv.handleInstance(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, secret) {
		t.Fatalf("the instance endpoint leaked the API key:\n%s", body)
	}
	if !strings.Contains(body, `"hasAPIKey":true`) {
		t.Errorf("expected hasAPIKey to be reported, got:\n%s", body)
	}
	if !strings.Contains(body, `"translate":true`) {
		t.Errorf("expected translate to be reported as enabled, got:\n%s", body)
	}
}

// --- client address ---

// TestForwardedIPIsIgnoredUnlessProxyIsTrusted guards against a client forging
// its own address. The address is logged and stored on sessions, so a spoofable
// value would poison the audit trail and any future IP-based throttling.
func TestForwardedIPIsIgnoredUnlessProxyIsTrusted(t *testing.T) {
	for _, tc := range []struct {
		name       string
		trustProxy bool
		headers    map[string]string
		wantIP     string
	}{
		{
			name:       "untrusted proxy: headers ignored",
			trustProxy: false,
			headers:    map[string]string{"X-Forwarded-For": "1.2.3.4", "X-Real-IP": "5.6.7.8"},
			wantIP:     "192.0.2.1:1234",
		},
		{
			name:       "trusted proxy: single value honoured",
			trustProxy: true,
			headers:    map[string]string{"X-Forwarded-For": "1.2.3.4"},
			wantIP:     "1.2.3.4",
		},
		{
			// A client can prepend entries; only the rightmost was added by the
			// proxy we trust, so that is the one to believe.
			name:       "trusted proxy: rightmost entry wins over client-injected ones",
			trustProxy: true,
			headers:    map[string]string{"X-Forwarded-For": "9.9.9.9, 8.8.8.8, 1.2.3.4"},
			wantIP:     "1.2.3.4",
		},
		{
			name:       "trusted proxy: falls back to X-Real-IP",
			trustProxy: true,
			headers:    map[string]string{"X-Real-IP": "5.6.7.8"},
			wantIP:     "5.6.7.8",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Server.TrustProxy = tc.trustProxy
			srv := New(Deps{Config: cfg, Logger: logging.Discard()})

			var got string
			handler := srv.realIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = r.RemoteAddr
			}))

			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			req.RemoteAddr = "192.0.2.1:1234"
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			handler.ServeHTTP(httptest.NewRecorder(), req)

			if got != tc.wantIP {
				t.Errorf("RemoteAddr = %q, want %q", got, tc.wantIP)
			}
		})
	}
}

// --- SPA ---

func TestSPARoutesFallThroughToTheApp(t *testing.T) {
	h := newHarness(t)

	// A client-side route must return the app, not a 404, so a hard refresh on a
	// deep link works.
	resp := h.do(t, http.MethodGet, "/devices/dev_123", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q, want HTML", got)
	}
}

func TestMissingAssetIs404(t *testing.T) {
	h := newHarness(t)

	// A request for a bundle that does not exist must fail loudly rather than
	// receiving an HTML page, which would turn a broken build into a confusing
	// runtime error.
	resp := h.do(t, http.MethodGet, "/assets/does-not-exist.js", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestUnknownAPIRouteIsNotSwallowedByTheSPA(t *testing.T) {
	h := newHarness(t)

	// An API typo must not silently return the HTML app, which is maddening to
	// debug from a client.
	resp := h.do(t, http.MethodGet, "/api/v1/nope", nil)
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
		t.Errorf("an unknown /api route returned HTML (%s); it should not fall through to the SPA", ct)
	}
}

// --- helpers ---

func errorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var body errorBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return body.Error.Code
}

func equalJSON(a, b map[string]any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

// cookieJar is a minimal in-memory jar. net/http/cookiejar refuses to store
// cookies for the bare "127.0.0.1" host that httptest uses, which would make
// every session test fail for the wrong reason.
type cookieJar struct {
	cookies []*http.Cookie
}

func (j *cookieJar) SetCookies(_ *url.URL, cookies []*http.Cookie) {
	for _, incoming := range cookies {
		replaced := false
		for i, existing := range j.cookies {
			if existing.Name == incoming.Name {
				j.cookies[i] = incoming
				replaced = true
				break
			}
		}
		if !replaced {
			j.cookies = append(j.cookies, incoming)
		}
	}
	// Honour deletions so logout actually clears the session client-side.
	kept := j.cookies[:0]
	for _, c := range j.cookies {
		if c.MaxAge < 0 {
			continue
		}
		kept = append(kept, c)
	}
	j.cookies = kept
}

func (j *cookieJar) Cookies(_ *url.URL) []*http.Cookie { return j.cookies }
