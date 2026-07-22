package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/settings"
	appstore "github.com/labstack/fanout/internal/store"
)

type testAuthServer struct {
	e          *echo.Echo
	db         *appstore.SQLite
	users      *auth.UserStore
	codes      *auth.CodeStore
	setup      *auth.Setup
	setupToken string
	sessions   *auth.BrowserSessions
	audit      *auth.AuditStore
	cfg        env.Config
}

func newTestAuthServer(t *testing.T) *testAuthServer {
	return newTestAuthServerWith(t, env.Config{AuthMode: "local"}, auth.SMTPConfig{})
}

func newTestAuthServerWith(t *testing.T, cfg env.Config, smtp auth.SMTPConfig) *testAuthServer {
	t.Helper()
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	if cfg.AuthMode == "" {
		cfg.AuthMode = "local"
	}
	if cfg.SessionIdleTTL == 0 {
		cfg.SessionIdleTTL = 12 * time.Hour
	}
	if cfg.SessionAbsoluteTTL == 0 {
		cfg.SessionAbsoluteTTL = 7 * 24 * time.Hour
	}
	secret := "0123456789abcdef0123456789abcdef"
	users := auth.NewUserStore(sqlite.DB)
	codes := auth.NewCodeStore(sqlite.DB, secret)
	setup := auth.NewSetup()
	setupToken, _, err := setup.Rotate()
	if err != nil {
		t.Fatalf("Rotate setup: %v", err)
	}
	sessions := auth.NewBrowserSessions(sqlite.DB, cfg.SessionIdleTTL, cfg.SessionAbsoluteTTL, false)
	audit := auth.NewAuditStore(sqlite.DB)
	e := echo.New()
	RegisterAuthMiddleware(e, users, sessions, audit, cfg)
	RegisterAuthRoutes(e, users, codes, setup, settings.NewStore(sqlite.DB), sessions, audit, smtp, cfg)
	return &testAuthServer{e: e, db: sqlite, users: users, codes: codes, setup: setup, setupToken: setupToken, sessions: sessions, audit: audit, cfg: cfg}
}

func (s *testAuthServer) login(t *testing.T, user auth.User) *http.Cookie {
	t.Helper()
	code, err := s.codes.Create(user.Email)
	if err != nil {
		t.Fatalf("Create code: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/verify", strings.NewReader(`{"email":"`+user.Email+`","code":"`+code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify = %d %s", rec.Code, rec.Body.String())
	}
	return firstCookie(t, rec, "fanout_session")
}

func sessionRequest(method, target string, body *strings.Reader, cookie *http.Cookie) *http.Request {
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, body)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if isUnsafeMethod(method) {
		req.Header.Set("X-Fanout-Request", "1")
	}
	return req
}

func firstCookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response did not set a %q cookie", name)
	return nil
}

func TestRoutePolicyClassification(t *testing.T) {
	tests := []struct {
		method, path string
		kind         routePolicyKind
		capability   Capability
	}{
		{http.MethodGet, "/healthz", routePolicyPublic, ""},
		{http.MethodGet, "/readyz", routePolicyPublic, ""},
		{http.MethodGet, "/api/health", routePolicyPublic, ""},
		{http.MethodPost, "/api/auth/setup", routePolicyPublic, ""},
		{http.MethodPost, "/api/auth/start", routePolicyPublic, ""},
		{http.MethodPost, "/api/auth/verify", routePolicyPublic, ""},
		{http.MethodGet, "/api/auth/oidc/start", routePolicyPublic, ""},
		{http.MethodGet, "/api/auth/oidc/callback", routePolicyPublic, ""},
		{http.MethodGet, "/api/auth/me", routePolicyAuthenticated, ""},
		{http.MethodPost, "/api/auth/logout", routePolicyAuthenticated, ""},
		{http.MethodGet, "/api/auth/oauth/authorize", routePolicyAuthenticated, ""},
		{http.MethodPost, "/api/auth/oauth/authorize", routePolicyAuthenticated, ""},
		{http.MethodGet, "/api/observability/overview", routePolicyCapability, ReadTelemetry},
		{http.MethodGet, "/api/alerts", routePolicyCapability, ReadTelemetry},
		{http.MethodPost, "/api/rules", routePolicyCapability, ManageAlerts},
		{http.MethodPut, "/api/rules/rule-1", routePolicyCapability, ManageAlerts},
		{http.MethodGet, "/api/dashboards/dashboard-1", routePolicyCapability, ManageOwnDashboards},
		{http.MethodPost, "/api/agent", routePolicyCapability, RunAgent},
		{http.MethodGet, "/api/settings/ingest", routePolicyCapability, ReadIngestMetadata},
		{http.MethodPost, "/api/settings/ingest/rotate-token", routePolicyCapability, ManageIngest},
		{http.MethodPost, "/api/users/user-1/logout-all", routePolicyCapability, ManageUsers},
		{http.MethodGet, "/debug/pprof/heap", routePolicyCapability, ReadOperations},
		{http.MethodGet, "/-/metrics", routePolicyServiceCredential, ReadOperations},
		{http.MethodPost, "/oauth/token", routePolicyProtocol, ""},
		{http.MethodPost, "/mcp", routePolicyProtocol, ""},
		{http.MethodGet, "/assets/app.js", routePolicyPublic, ""},
	}
	for _, tc := range tests {
		policy, ok := classifyRoute(tc.method, tc.path)
		if !ok {
			t.Errorf("%s %s is unclassified", tc.method, tc.path)
			continue
		}
		if policy.kind != tc.kind || policy.capability != tc.capability {
			t.Errorf("%s %s policy = {%v %q}, want {%v %q}", tc.method, tc.path, policy.kind, policy.capability, tc.kind, tc.capability)
		}
	}
	if _, ok := classifyRoute(http.MethodPost, "/api/new-unreviewed-route"); ok {
		t.Fatal("unreviewed API route must be unclassified")
	}
}

func TestUnknownProtectedPathsAuthenticateThenReturn404(t *testing.T) {
	s := newTestAuthServer(t)
	user, _ := s.users.Create("unknown-path@example.com", "", "admin")
	anonymous := httptest.NewRecorder()
	s.e.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/api/not-registered", nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous unknown API path = %d, want 401", anonymous.Code)
	}
	cookie := s.login(t, user)
	authenticated := httptest.NewRecorder()
	s.e.ServeHTTP(authenticated, sessionRequest(http.MethodGet, "/api/not-registered", nil, cookie))
	if authenticated.Code != http.StatusNotFound {
		t.Fatalf("authenticated unknown API path = %d, want 404", authenticated.Code)
	}
	s.e.GET("/api/new-unreviewed-route", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	unclassified := httptest.NewRecorder()
	s.e.ServeHTTP(unclassified, sessionRequest(http.MethodGet, "/api/new-unreviewed-route", nil, cookie))
	if unclassified.Code != http.StatusInternalServerError {
		t.Fatalf("registered unclassified API route = %d, want 500", unclassified.Code)
	}
}

func TestKnownRouteWrongMethodReturns405(t *testing.T) {
	s := newTestAuthServer(t)
	s.e.GET("/api/alerts", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/alerts", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/alerts = %d, want 405", rec.Code)
	}
}

func TestPublicReadServesOnlyTelemetryReads(t *testing.T) {
	s := newTestAuthServerWith(t, env.Config{AuthMode: "local", PublicRead: true}, auth.SMTPConfig{})
	if _, err := s.users.Create("admin@example.com", "", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	s.e.GET("/api/observability/overview", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	s.e.POST("/api/rules", func(c *echo.Context) error { return c.NoContent(http.StatusCreated) })
	s.e.GET("/api/users", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) }, RequireCapability(ManageUsers))
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/observability/overview", http.StatusNoContent},
		{http.MethodPost, "/api/rules", http.StatusUnauthorized},
		{http.MethodGet, "/api/users", http.StatusUnauthorized},
	} {
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Fatalf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}

func TestPublicReadStatusAndAnonymousMe(t *testing.T) {
	s := newTestAuthServerWith(t, env.Config{AuthMode: "local", PublicRead: true}, auth.SMTPConfig{})
	if _, err := s.users.Create("admin@example.com", "", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	statusRec := httptest.NewRecorder()
	s.e.ServeHTTP(statusRec, httptest.NewRequest(http.MethodGet, "/api/auth/status", nil))
	var status map[string]any
	if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil || status["public_read"] != true {
		t.Fatalf("status = %s err=%v", statusRec.Body.String(), err)
	}
	meRec := httptest.NewRecorder()
	s.e.ServeHTTP(meRec, httptest.NewRequest(http.MethodGet, "/api/auth/me", nil))
	if meRec.Code != http.StatusOK || !strings.Contains(meRec.Body.String(), `"role":"viewer"`) {
		t.Fatalf("anonymous me = %d %s", meRec.Code, meRec.Body.String())
	}

	admin, err := s.users.GetByEmail("admin@example.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	authenticatedMe := httptest.NewRecorder()
	s.e.ServeHTTP(authenticatedMe, sessionRequest(http.MethodGet, "/api/auth/me", nil, s.login(t, admin)))
	if authenticatedMe.Code != http.StatusOK || !strings.Contains(authenticatedMe.Body.String(), `"role":"admin"`) {
		t.Fatalf("authenticated public-mode me = %d %s", authenticatedMe.Code, authenticatedMe.Body.String())
	}
}

func TestSessionAuthDBFailureReturns500(t *testing.T) {
	s := newTestAuthServer(t)
	user, _ := s.users.Create("db-outage@example.com", "", "admin")
	cookie := s.login(t, user)
	if _, err := s.db.DB.Exec(`ALTER TABLE users RENAME TO users_offline`); err != nil {
		t.Fatalf("rename users: %v", err)
	}
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, sessionRequest(http.MethodGet, "/api/auth/me", nil, cookie))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("session during DB failure = %d, want 500", rec.Code)
	}
}

func TestRoleChangeAndDeactivationRevokeSessions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*auth.UserStore, auth.User) error
	}{
		{name: "role", change: func(users *auth.UserStore, user auth.User) error {
			role := auth.RoleViewer
			_, err := users.Update(user.ID, nil, nil, &role, nil)
			return err
		}},
		{name: "inactive", change: func(users *auth.UserStore, user auth.User) error {
			active := false
			_, err := users.Update(user.ID, nil, nil, nil, &active)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestAuthServer(t)
			user, _ := s.users.Create(tc.name+"@example.com", "", "operator")
			cookie := s.login(t, user)
			if err := tc.change(s.users, user); err != nil {
				t.Fatalf("change: %v", err)
			}
			rec := httptest.NewRecorder()
			s.e.ServeHTTP(rec, sessionRequest(http.MethodGet, "/api/auth/me", nil, cookie))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("revoked session = %d, want 401", rec.Code)
			}
		})
	}
}

func TestCapabilityIsEnforcedCentrally(t *testing.T) {
	s := newTestAuthServer(t)
	viewer, _ := s.users.Create("viewer@example.com", "", "viewer")
	cookie := s.login(t, viewer)
	// Deliberately omit route-level RequireCapability. The global policy remains authoritative.
	s.e.POST("/api/settings/ingest/rotate-token", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, sessionRequest(http.MethodPost, "/api/settings/ingest/rotate-token", nil, cookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer mutation = %d, want 403", rec.Code)
	}
}

func TestBrowserMutationValidation(t *testing.T) {
	s := newTestAuthServer(t)
	user, _ := s.users.Create("csrf@example.com", "", "operator")
	cookie := s.login(t, user)
	s.e.POST("/api/rules", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	for _, tc := range []struct {
		name string
		head map[string]string
		want int
	}{
		{name: "missing", want: http.StatusForbidden},
		{name: "custom header", head: map[string]string{"X-Fanout-Request": "1"}, want: http.StatusNoContent},
		{name: "same origin", head: map[string]string{"Origin": "http://example.com"}, want: http.StatusNoContent},
		{name: "referer", head: map[string]string{"Referer": "http://example.com/page"}, want: http.StatusNoContent},
		{name: "malformed referer", head: map[string]string{"Referer": "://bad"}, want: http.StatusForbidden},
		{name: "evil origin", head: map[string]string{"Origin": "https://evil.test"}, want: http.StatusForbidden},
		{name: "cross site overrides header", head: map[string]string{"X-Fanout-Request": "1", "Sec-Fetch-Site": "cross-site"}, want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/rules", nil)
			req.Host = "example.com"
			req.AddCookie(cookie)
			for key, value := range tc.head {
				req.Header.Set(key, value)
			}
			rec := httptest.NewRecorder()
			s.e.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestLogoutDeletesServerSession(t *testing.T) {
	s := newTestAuthServer(t)
	user, _ := s.users.Create("logout@example.com", "", "operator")
	cookie := s.login(t, user)
	logoutRec := httptest.NewRecorder()
	s.e.ServeHTTP(logoutRec, sessionRequest(http.MethodPost, "/api/auth/logout", nil, cookie))
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout = %d %s", logoutRec.Code, logoutRec.Body.String())
	}
	meRec := httptest.NewRecorder()
	s.e.ServeHTTP(meRec, sessionRequest(http.MethodGet, "/api/auth/me", nil, cookie))
	if meRec.Code != http.StatusUnauthorized {
		t.Fatalf("reused cookie = %d, want 401", meRec.Code)
	}
}

func TestStartDoesNotRevealAccountState(t *testing.T) {
	s := newTestAuthServer(t)
	inactive, _ := s.users.Create("inactive@example.com", "", "operator")
	active := false
	if _, err := s.users.Update(inactive.ID, nil, nil, nil, &active); err != nil {
		t.Fatalf("Update: %v", err)
	}
	for _, email := range []string{"missing@example.com", "inactive@example.com"} {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/start", strings.NewReader(`{"email":"`+email+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"code_sent":true`) {
			t.Fatalf("%s = %d %s", email, rec.Code, rec.Body.String())
		}
	}
}

func TestStartReturnsServiceUnavailableWhenEmailDeliveryFails(t *testing.T) {
	smtp := auth.SMTPConfig{Host: "127.0.0.1", Port: 1, User: "user", Pass: "pass", From: "Fanout <noreply@example.com>"}
	s := newTestAuthServerWith(t, env.Config{AuthMode: "local"}, smtp)
	if _, err := s.users.Create("active@example.com", "", "operator"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/start", strings.NewReader(`{"email":"active@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestSetupLifecycleAndIngestToken(t *testing.T) {
	s := newTestAuthServer(t)
	bad := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"email":"admin@example.com","setup_token":"wrong"}`))
	bad.Header.Set("Content-Type", "application/json")
	badRec := httptest.NewRecorder()
	s.e.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusForbidden {
		t.Fatalf("bad setup = %d", badRec.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"email":"admin@example.com","setup_token":"`+s.setupToken+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["access_token"] != "" {
		t.Fatal("legacy access token must not be returned")
	}
	if !strings.HasPrefix(body["ingest_token"], "fo_") {
		t.Fatalf("ingest token = %q", body["ingest_token"])
	}
	if user, err := s.users.GetByEmail("admin@example.com"); err != nil || user.Role != auth.RoleAdmin {
		t.Fatalf("admin = %+v err=%v", user, err)
	}
	if got := s.setup.Verify(s.setupToken); got != auth.SetupStatusUnset {
		t.Fatalf("setup state = %v", got)
	}
}

func TestSetupExpiryAndExistingAdminRetry(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		s := newTestAuthServer(t)
		s.setup.SetExpiresForTest(time.Now().Add(-time.Minute))
		req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"email":"admin@example.com","setup_token":"`+s.setupToken+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		if rec.Code != http.StatusGone {
			t.Fatalf("expired = %d", rec.Code)
		}
	})
	t.Run("existing admin", func(t *testing.T) {
		s := newTestAuthServer(t)
		if _, err := s.users.CreateFirstAdmin("admin@example.com", "Admin"); err != nil {
			t.Fatalf("CreateFirstAdmin: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"email":"admin@example.com","setup_token":"`+s.setupToken+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("retry = %d %s", rec.Code, rec.Body.String())
		}
	})
}

func TestSetupReturnsConfiguredIngestEndpoint(t *testing.T) {
	const endpoint = "https://ingest.fanout.labstack.com"
	s := newTestAuthServerWith(t, env.Config{AuthMode: "local", IngestEndpoint: endpoint}, auth.SMTPConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"email":"admin@example.com","setup_token":"`+s.setupToken+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), endpoint) {
		t.Fatalf("setup endpoint = %d %s", rec.Code, rec.Body.String())
	}
}
