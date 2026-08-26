package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
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
	cfg        config.Config
}

type expiredSetupCredential struct{}

func (expiredSetupCredential) Verify(string) auth.SetupStatus { return auth.SetupStatusExpired }
func (expiredSetupCredential) Clear()                         {}

func newTestAuthServer(t *testing.T) *testAuthServer {
	return newTestAuthServerWith(t, config.Config{AuthMode: "local"}, auth.SMTPConfig{})
}

func newTestAuthServerWith(t *testing.T, cfg config.Config, smtp auth.SMTPConfig) *testAuthServer {
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
	cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom = smtp.Host, smtp.Port, smtp.User, smtp.Pass, smtp.From
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
		req.Header.Set("Fanout-Request", "1")
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
		{http.MethodPost, "/api/auth/login-link", routePolicyPublic, ""},
		{http.MethodGet, "/api/auth/oidc/start", routePolicyPublic, ""},
		{http.MethodGet, "/api/auth/oidc/callback", routePolicyPublic, ""},
		{http.MethodGet, "/api/auth/me", routePolicyAuthenticated, ""},
		{http.MethodPost, "/api/auth/logout", routePolicyAuthenticated, ""},
		{http.MethodGet, "/api/auth/oauth/authorize", routePolicyAuthenticated, ""},
		{http.MethodPost, "/api/auth/oauth/authorize", routePolicyAuthenticated, ""},
		{http.MethodGet, "/api/observability/overview", routePolicyCapability, ReadTelemetry},
		{http.MethodGet, "/api/intelligence", routePolicyCapability, ReadTelemetry},
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
		{http.MethodPost, "/api/mcp", routePolicyCapability, ReadTelemetry},
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

func TestTelemetryReadsRequireAuthenticatedAccount(t *testing.T) {
	s := newTestAuthServer(t)
	viewer, err := s.users.Create("viewer@example.com", "", "viewer")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s.e.GET("/api/observability/overview", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })

	unauthenticated := httptest.NewRecorder()
	s.e.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/observability/overview", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated telemetry read = %d, want 401", unauthenticated.Code)
	}

	authenticated := httptest.NewRecorder()
	s.e.ServeHTTP(authenticated, sessionRequest(http.MethodGet, "/api/observability/overview", nil, s.login(t, viewer)))
	if authenticated.Code != http.StatusNoContent {
		t.Fatalf("viewer telemetry read = %d, want 204", authenticated.Code)
	}
}

func TestAuthStatusAndMeRequirePersistedAccount(t *testing.T) {
	s := newTestAuthServer(t)
	admin, err := s.users.Create("admin@example.com", "", "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	statusRec := httptest.NewRecorder()
	s.e.ServeHTTP(statusRec, httptest.NewRequest(http.MethodGet, "/api/auth/status", nil))
	var status map[string]any
	if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil {
		t.Fatalf("status = %s err=%v", statusRec.Body.String(), err)
	}
	if len(status) != 6 || status["agent_available"] != false || status["smtp_configured"] != false || status["self_signup"] != false {
		t.Fatalf("unexpected auth status fields: %s", statusRec.Body.String())
	}

	meRec := httptest.NewRecorder()
	s.e.ServeHTTP(meRec, httptest.NewRequest(http.MethodGet, "/api/auth/me", nil))
	if meRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated me = %d, want 401", meRec.Code)
	}

	authenticatedMe := httptest.NewRecorder()
	s.e.ServeHTTP(authenticatedMe, sessionRequest(http.MethodGet, "/api/auth/me", nil, s.login(t, admin)))
	var persistedMe map[string]any
	if err := json.Unmarshal(authenticatedMe.Body.Bytes(), &persistedMe); err != nil {
		t.Fatalf("decode authenticated me: %v", err)
	}
	if authenticatedMe.Code != http.StatusOK || persistedMe["id"] != admin.ID || persistedMe["role"] != string(auth.RoleAdmin) {
		t.Fatalf("authenticated me = %d %s", authenticatedMe.Code, authenticatedMe.Body.String())
	}
	if _, present := persistedMe["anonymous"]; present {
		t.Fatalf("authenticated me still contains removed anonymous marker: %s", authenticatedMe.Body.String())
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

func TestViewerCanRunAgentWithoutOperatorPrivileges(t *testing.T) {
	s := newTestAuthServer(t)
	viewer, _ := s.users.Create("viewer-agent@example.com", "", "viewer")
	cookie := s.login(t, viewer)
	s.e.POST("/api/agent", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	s.e.POST("/api/rules", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })

	agentRec := httptest.NewRecorder()
	s.e.ServeHTTP(agentRec, sessionRequest(http.MethodPost, "/api/agent", nil, cookie))
	if agentRec.Code != http.StatusNoContent {
		t.Fatalf("viewer agent request = %d, want 204", agentRec.Code)
	}

	alertRec := httptest.NewRecorder()
	s.e.ServeHTTP(alertRec, sessionRequest(http.MethodPost, "/api/rules", nil, cookie))
	if alertRec.Code != http.StatusForbidden {
		t.Fatalf("viewer alert mutation = %d, want 403", alertRec.Code)
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
		{name: "custom header", head: map[string]string{"Fanout-Request": "1"}, want: http.StatusNoContent},
		{name: "same origin", head: map[string]string{"Origin": "http://example.com"}, want: http.StatusNoContent},
		{name: "referer", head: map[string]string{"Referer": "http://example.com/page"}, want: http.StatusNoContent},
		{name: "malformed referer", head: map[string]string{"Referer": "://bad"}, want: http.StatusForbidden},
		{name: "evil origin", head: map[string]string{"Origin": "https://evil.test"}, want: http.StatusForbidden},
		{name: "cross site overrides header", head: map[string]string{"Fanout-Request": "1", "Sec-Fetch-Site": "cross-site"}, want: http.StatusForbidden},
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
	smtp := auth.SMTPConfig{Host: "smtp.example.com", Port: 587, User: "user", Pass: "pass", From: "Fanout <noreply@example.com>"}
	s := newTestAuthServerWith(t, config.Config{AuthMode: "local"}, smtp)
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

func TestStartExplainsWhenSMTPIsNotConfigured(t *testing.T) {
	s := newTestAuthServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/start", strings.NewReader(`{"email":"active@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "login link") {
		t.Fatalf("status = %d body=%s, want 503 with login-link guidance", rec.Code, rec.Body.String())
	}
}

func TestLoginLinkAuthenticatesExactlyOnce(t *testing.T) {
	s := newTestAuthServer(t)
	user, err := s.users.Create("link@example.com", "", "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, err := s.codes.CreateLoginLink(user.Email)
	if err != nil {
		t.Fatalf("CreateLoginLink: %v", err)
	}
	body := `{"token":"` + token + `"}`
	first := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login-link", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.e.ServeHTTP(first, req)
	if first.Code != http.StatusOK || firstCookie(t, first, "fanout_session") == nil {
		t.Fatalf("first redemption = %d %s", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login-link", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.e.ServeHTTP(second, req)
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("second redemption = %d %s, want 401", second.Code, second.Body.String())
	}
	var events int
	if err := s.db.DB.QueryRow(`SELECT COUNT(*) FROM auth_audit_events WHERE event_type = 'login_link.redeemed' AND actor_user_id = ?`, user.ID).Scan(&events); err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if events != 1 {
		t.Fatalf("redeemed audit events = %d, want 1", events)
	}
}

func TestStartReturnsServiceUnavailableWhenEmailDeliveryFails(t *testing.T) {
	smtp := auth.SMTPConfig{Host: "127.0.0.1", Port: 1, User: "user", Pass: "pass", From: "Fanout <noreply@example.com>"}
	s := newTestAuthServerWith(t, config.Config{AuthMode: "local", SelfSignup: true}, smtp)
	if _, err := s.users.Create("admin@example.com", "", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/start", strings.NewReader(`{"email":"new-viewer@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if _, err := s.users.GetByEmail("new-viewer@example.com"); !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("unverified address created a user: %v", err)
	}
}

func TestLocalSelfSignupCreatesVerifiedViewer(t *testing.T) {
	s := newTestAuthServerWith(t, config.Config{AuthMode: "local", SelfSignup: true}, auth.SMTPConfig{})
	if _, err := s.users.Create("admin@example.com", "", auth.RoleAdmin); err != nil {
		t.Fatalf("Create admin: %v", err)
	}
	code, err := s.codes.Create("new-viewer@example.com")
	if err != nil {
		t.Fatalf("Create code: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/verify", strings.NewReader(`{"email":"NEW-viewer@example.com","code":"`+code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "self-signup-test")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify = %d %s", rec.Code, rec.Body.String())
	}
	user, err := s.users.GetByEmail("new-viewer@example.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if user.Role != auth.RoleViewer || !user.Active {
		t.Fatalf("self-signup user = %#v, want active viewer", user)
	}
	firstCookie(t, rec, "fanout_session")
	var provisioned int
	if err := s.db.DB.QueryRow(`SELECT COUNT(*) FROM auth_audit_events WHERE event_type = 'user.provisioned' AND target_id = ?`, user.ID).Scan(&provisioned); err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if provisioned != 1 {
		t.Fatalf("provision audit events = %d, want 1", provisioned)
	}
}

func TestLocalSelfSignupCannotPreemptFirstAdminOrReactivateUser(t *testing.T) {
	t.Run("first admin", func(t *testing.T) {
		s := newTestAuthServerWith(t, config.Config{AuthMode: "local", SelfSignup: true}, auth.SMTPConfig{})
		code, err := s.codes.Create("visitor@example.com")
		if err != nil {
			t.Fatalf("Create code: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/auth/verify", strings.NewReader(`{"email":"visitor@example.com","code":"`+code+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("verify before setup = %d %s, want 401", rec.Code, rec.Body.String())
		}
		if _, err := s.users.GetByEmail("visitor@example.com"); !errors.Is(err, auth.ErrUserNotFound) {
			t.Fatalf("visitor preempted first admin: %v", err)
		}
	})

	t.Run("inactive user", func(t *testing.T) {
		s := newTestAuthServerWith(t, config.Config{AuthMode: "local", SelfSignup: true}, auth.SMTPConfig{})
		if _, err := s.users.Create("admin@example.com", "", auth.RoleAdmin); err != nil {
			t.Fatalf("Create admin: %v", err)
		}
		viewer, err := s.users.Create("inactive@example.com", "", auth.RoleViewer)
		if err != nil {
			t.Fatalf("Create viewer: %v", err)
		}
		active := false
		if _, err := s.users.Update(viewer.ID, nil, nil, nil, &active); err != nil {
			t.Fatalf("deactivate: %v", err)
		}
		code, err := s.codes.Create(viewer.Email)
		if err != nil {
			t.Fatalf("Create code: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/auth/verify", strings.NewReader(`{"email":"inactive@example.com","code":"`+code+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("inactive verify = %d %s, want 401", rec.Code, rec.Body.String())
		}
		got, err := s.users.GetByEmail(viewer.Email)
		if err != nil || got.Active {
			t.Fatalf("inactive user was reactivated: user=%#v err=%v", got, err)
		}
	})
}

func TestLocalSelfSignupDisabledRejectsUnknownVerifiedAddress(t *testing.T) {
	s := newTestAuthServer(t)
	if _, err := s.users.Create("admin@example.com", "", auth.RoleAdmin); err != nil {
		t.Fatalf("Create admin: %v", err)
	}
	code, err := s.codes.Create("visitor@example.com")
	if err != nil {
		t.Fatalf("Create code: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/verify", strings.NewReader(`{"email":"visitor@example.com","code":"`+code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("verify = %d %s, want 401", rec.Code, rec.Body.String())
	}
	if _, err := s.users.GetByEmail("visitor@example.com"); !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("disabled self-signup created user: %v", err)
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
	if body["ingest_header_name"] != "Authorization" {
		t.Fatalf("ingest header name = %q", body["ingest_header_name"])
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
		e := echo.New()
		h := &AuthHandler{setup: expiredSetupCredential{}, setupLimiter: auth.NewKeyedLimiter(10, 15*time.Minute)}
		e.POST("/api/auth/setup", h.Setup)
		req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"email":"admin@example.com","setup_token":"expired"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
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
		if rec.Code != http.StatusForbidden {
			t.Fatalf("retry = %d %s, want 403: one setup credential must not establish a second session", rec.Code, rec.Body.String())
		}
		for _, cookie := range rec.Result().Cookies() {
			if cookie.Name == "fanout_session" && cookie.Value != "" {
				t.Fatal("denied setup retry established a browser session")
			}
		}
	})
}

func TestSetupReturnsAdvertisedIngestEndpoint(t *testing.T) {
	const endpoint = "https://ingest.example.com"
	s := newTestAuthServerWith(t, config.Config{AuthMode: "local", IngestAdvertisedEndpoint: endpoint}, auth.SMTPConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"email":"admin@example.com","setup_token":"`+s.setupToken+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), endpoint) {
		t.Fatalf("setup endpoint = %d %s", rec.Code, rec.Body.String())
	}
}

func TestSetupConsumesTokenBeforeFalliblePostSetupWork(t *testing.T) {
	s := newTestAuthServer(t)
	// Break the post-admin settings work so the request fails after the first
	// administrator is committed.
	if _, err := s.db.DB.Exec(`DROP TABLE settings`); err != nil {
		t.Fatalf("drop settings: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"email":"admin@example.com","setup_token":"`+s.setupToken+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("setup unexpectedly succeeded: %s", rec.Body.String())
	}
	if _, err := s.users.GetByEmail("admin@example.com"); err != nil {
		t.Fatalf("first administrator was not committed: %v", err)
	}
	if got := s.setup.Verify(s.setupToken); got != auth.SetupStatusUnset {
		t.Fatalf("setup token state = %v, want unset: a committed administrator must not leave a live setup credential", got)
	}
}
