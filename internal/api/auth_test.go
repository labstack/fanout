package api

import (
	"context"
	"database/sql"
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
	e.HTTPErrorHandler = HTTPErrorHandler
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
	req := httptest.NewRequest(http.MethodPost, "/api/auth/code/verify", strings.NewReader(`{"email":"`+user.Email+`","code":"`+code+`"}`))
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

func TestRetiredRoutesDoNotReachSPA(t *testing.T) {
	s := newTestAuthServer(t)
	registerTestUserRoutes(s)
	RegisterIntelligenceRoutes(s.e, fakeIntelligenceSnapshots{})
	RegisterAnnotationRoutes(s.e, annotationFake{})
	admin, err := s.users.CreateWithAudit("retired-routes@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := s.login(t, admin)
	s.e.GET("/*", func(c *echo.Context) error { return c.String(http.StatusOK, "SPA") })
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/auth/start"},
		{http.MethodPost, "/api/auth/verify"},
		{http.MethodPost, "/api/auth/login-link"},
		{http.MethodGet, "/api/dashboard"},
		{http.MethodGet, "/api/health"},
		{http.MethodGet, "/api/intelligence"},
		{http.MethodPost, "/api/annotations"},
		{http.MethodGet, "/api/rules"},
		{http.MethodPost, "/api/agent"},
		{http.MethodPost, "/api/users/" + admin.ID + "/logout-all"},
		{http.MethodPost, "/api/settings/ingest/rotate-token"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.e.ServeHTTP(rec, sessionRequest(route.method, route.path, nil, cookie))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("retired route returned %d, want 404: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRoutePolicyClassification(t *testing.T) {
	tests := []struct {
		method, path string
		kind         routePolicyKind
		capability   Capability
	}{
		{http.MethodGet, "/healthz", routePolicyPublic, ""},
		{http.MethodGet, "/readyz", routePolicyPublic, ""},
		{http.MethodPost, "/api/auth/setup", routePolicyPublic, ""},
		{http.MethodPost, "/api/auth/code/send", routePolicyPublic, ""},
		{http.MethodPost, "/api/auth/code/verify", routePolicyPublic, ""},
		{http.MethodPost, "/api/auth/link/verify", routePolicyPublic, ""},
		{http.MethodGet, "/api/auth/oidc/start", routePolicyPublic, ""},
		{http.MethodGet, "/api/auth/oidc/callback", routePolicyPublic, ""},
		{http.MethodGet, "/api/auth/me", routePolicyAuthenticated, ""},
		{http.MethodPost, "/api/auth/logout", routePolicyAuthenticated, ""},
		{http.MethodGet, "/api/auth/oauth/authorize", routePolicyAuthenticated, ""},
		{http.MethodPost, "/api/auth/oauth/authorize", routePolicyAuthenticated, ""},
		{http.MethodGet, "/api/auth/oauth/connections", routePolicyAuthenticated, ""},
		{http.MethodPost, "/api/auth/oauth/connections/client-1/revoke", routePolicyAuthenticated, ""},
		{http.MethodGet, "/api/traces/abc", routePolicyCapability, ReadTelemetry},
		{http.MethodHead, "/api/traces/abc", routePolicyCapability, ReadTelemetry},
		{http.MethodGet, "/api/insights", routePolicyCapability, ReadTelemetry},
		{http.MethodGet, "/api/alerting/alerts", routePolicyCapability, ReadTelemetry},
		{http.MethodHead, "/api/alerting/alerts", routePolicyCapability, ReadTelemetry},
		{http.MethodGet, "/api/alerting/summary", routePolicyCapability, ReadTelemetry},
		{http.MethodHead, "/api/alerting/summary", routePolicyCapability, ReadTelemetry},
		{http.MethodGet, "/api/alerting/rules", routePolicyCapability, ReadTelemetry},
		{http.MethodPost, "/api/alerting/rules", routePolicyCapability, ManageAlerts},
		{http.MethodPut, "/api/alerting/rules/rule-1", routePolicyCapability, ManageAlerts},
		{http.MethodGet, "/api/dashboards/dashboard-1", routePolicyCapability, ManageOwnDashboards},
		{http.MethodPatch, "/api/dashboards/dashboard-1", routePolicyCapability, ManageOwnDashboards},
		{http.MethodGet, "/api/dashboards/dashboard-1/versions", routePolicyCapability, ManageOwnDashboards},
		{http.MethodGet, "/api/dashboards/dashboard-1/versions/1", routePolicyCapability, ManageOwnDashboards},
		{http.MethodPost, "/api/dashboards/dashboard-1/versions/1/restore", routePolicyCapability, ManageOwnDashboards},
		{http.MethodPost, "/api/agent/runs", routePolicyCapability, RunAgent},
		{http.MethodPost, "/api/queries/variables/resolve", routePolicyCapability, ReadTelemetry},
		{http.MethodPost, "/api/fanql/parse", routePolicyCapability, ReadTelemetry},
		{http.MethodPost, "/api/fanql/format", routePolicyCapability, ReadTelemetry},
		{http.MethodPost, "/api/fanql/complete", routePolicyCapability, ReadTelemetry},
		{http.MethodGet, "/api/fanql/reference", routePolicyCapability, ReadTelemetry},
		{http.MethodGet, "/api/settings/ingest", routePolicyCapability, ReadIngestMetadata},
		{http.MethodPost, "/api/settings/ingest/token/rotate", routePolicyCapability, ManageIngest},
		{http.MethodPost, "/api/users/user-1/access/revoke", routePolicyCapability, ManageUsers},
		{http.MethodPatch, "/api/users/user-1", routePolicyCapability, ManageUsers},
		{http.MethodPatch, "/api/users/user-1/role", routePolicyCapability, ManageUsers},
		{http.MethodPatch, "/api/users/user-1/status", routePolicyCapability, ManageUsers},
		{http.MethodGet, "/debug/pprof/heap", routePolicyCapability, ReadOperations},
		{http.MethodGet, "/metrics", routePolicyServiceCredential, ReadOperations},
		{http.MethodPost, "/oauth/token", routePolicyProtocol, ""},
		{http.MethodPost, "/mcp", routePolicyProtocol, ""},
		{http.MethodPost, "/api/mcp", routePolicyCapability, ReadTelemetry},
		{http.MethodGet, "/assets/app.js", routePolicyPublic, ""},
		{http.MethodGet, "/scim/v2/Users", routePolicySCIM, ""},
		{http.MethodPost, "/scim/v2/Users", routePolicySCIM, ""},
		{http.MethodGet, "/scim/v2/Users/user-1", routePolicySCIM, ""},
		{http.MethodPatch, "/scim/v2/Users/user-1", routePolicySCIM, ""},
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
	for _, tc := range []struct{ method, path string }{
		{http.MethodDelete, "/scim/v2/Users/user-1"},
		{http.MethodPut, "/scim/v2/Users/user-1"},
		{http.MethodPost, "/scim/v2/Users/user-1"},
		{http.MethodGet, "/scim/v2/Users/"},
		{http.MethodGet, scimUsersPath + "/user-1/extra"},
		{http.MethodGet, "/scim/v2/Groups"},
	} {
		if _, ok := classifyRoute(tc.method, tc.path); ok {
			t.Errorf("%s %s must be unclassified", tc.method, tc.path)
		}
	}
	if _, ok := classifyRoute(http.MethodPost, "/api/new-unreviewed-route"); ok {
		t.Fatal("unreviewed API route must be unclassified")
	}
}

func TestUnknownProtectedPathsReturn404BeforeAuthentication(t *testing.T) {
	s := newTestAuthServer(t)
	user, _ := s.users.CreateWithAudit("unknown-path@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	anonymous := httptest.NewRecorder()
	s.e.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/api/not-registered", nil))
	if anonymous.Code != http.StatusNotFound {
		t.Fatalf("anonymous unknown API path = %d, want 404", anonymous.Code)
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

func TestMetricsRequiresValidScraperToken(t *testing.T) {
	s := newTestAuthServerWith(t, config.Config{AuthMode: "local", MetricsToken: "metrics-test-token"}, auth.SMTPConfig{})
	s.e.GET("/metrics", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	s.e.GET("/*", func(c *echo.Context) error { return c.String(http.StatusOK, "SPA") })
	for _, token := range []string{"", "wrong"} {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("metrics with invalid token = %d, want 401", rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer metrics-test-token")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("current metrics with valid token = %d, want 204", rec.Code)
	}
}

func TestKnownRouteWrongMethodReturns405(t *testing.T) {
	s := newTestAuthServer(t)
	s.e.GET("/api/alerting/alerts", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/alerting/alerts", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/alerting/alerts = %d, want 405", rec.Code)
	}
}

func TestTelemetryReadsRequireAuthenticatedAccount(t *testing.T) {
	s := newTestAuthServer(t)
	viewer, err := s.users.CreateWithAudit("viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s.e.GET("/api/traces/abc", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })

	unauthenticated := httptest.NewRecorder()
	s.e.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/traces/abc", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated telemetry read = %d, want 401", unauthenticated.Code)
	}

	authenticated := httptest.NewRecorder()
	s.e.ServeHTTP(authenticated, sessionRequest(http.MethodGet, "/api/traces/abc", nil, s.login(t, viewer)))
	if authenticated.Code != http.StatusNoContent {
		t.Fatalf("viewer telemetry read = %d, want 204", authenticated.Code)
	}
}

func TestAuthStatusAndMeRequirePersistedAccount(t *testing.T) {
	s := newTestAuthServer(t)
	admin, err := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
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
	user, _ := s.users.CreateWithAudit("db-outage@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
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

func TestRoleChangeAndSuspensionRevokeSessions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*auth.UserStore, auth.User) error
	}{
		{name: "role", change: func(users *auth.UserStore, user auth.User) error {
			role := auth.RoleViewer
			_, err := users.UpdateWithAudit(user.ID, nil, nil, &role, nil, auth.AuditEvent{EventType: "user.updated", Outcome: "success"})
			return err
		}},
		{name: "suspended", change: func(users *auth.UserStore, user auth.User) error {
			status := auth.UserStatusSuspended
			_, err := users.UpdateWithAudit(user.ID, nil, nil, nil, &status, auth.AuditEvent{EventType: "user.updated", Outcome: "success"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestAuthServer(t)
			user, _ := s.users.CreateWithAudit(tc.name+"@example.com", "", "operator", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
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
	viewer, _ := s.users.CreateWithAudit("viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	cookie := s.login(t, viewer)
	// Deliberately omit route-level RequireCapability. The global policy remains authoritative.
	s.e.POST("/api/settings/ingest/token/rotate", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, sessionRequest(http.MethodPost, "/api/settings/ingest/token/rotate", nil, cookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer mutation = %d, want 403", rec.Code)
	}
}

func TestViewerCanRunAgentWithoutOperatorPrivileges(t *testing.T) {
	s := newTestAuthServer(t)
	viewer, _ := s.users.CreateWithAudit("viewer-agent@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	cookie := s.login(t, viewer)
	s.e.POST("/api/agent/runs", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	s.e.POST("/api/alerting/rules", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })

	agentRec := httptest.NewRecorder()
	s.e.ServeHTTP(agentRec, sessionRequest(http.MethodPost, "/api/agent/runs", nil, cookie))
	if agentRec.Code != http.StatusNoContent {
		t.Fatalf("viewer agent request = %d, want 204", agentRec.Code)
	}

	alertRec := httptest.NewRecorder()
	s.e.ServeHTTP(alertRec, sessionRequest(http.MethodPost, "/api/alerting/rules", nil, cookie))
	if alertRec.Code != http.StatusForbidden {
		t.Fatalf("viewer alert mutation = %d, want 403", alertRec.Code)
	}
}

func TestBrowserMutationValidation(t *testing.T) {
	s := newTestAuthServer(t)
	user, _ := s.users.CreateWithAudit("csrf@example.com", "", "operator", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	cookie := s.login(t, user)
	s.e.POST("/api/alerting/rules", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
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
			req := httptest.NewRequest(http.MethodPost, "/api/alerting/rules", nil)
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
	user, _ := s.users.CreateWithAudit("logout@example.com", "", "operator", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
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
	suspended, _ := s.users.CreateWithAudit("suspended@example.com", "", "operator", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	status := auth.UserStatusSuspended
	if _, err := s.users.UpdateWithAudit(suspended.ID, nil, nil, nil, &status, auth.AuditEvent{EventType: "user.updated", Outcome: "success"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	for _, email := range []string{"missing@example.com", "suspended@example.com"} {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/code/send", strings.NewReader(`{"email":"`+email+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"code_sent":true`) {
			t.Fatalf("%s = %d %s", email, rec.Code, rec.Body.String())
		}
	}
}

func TestLoginWithoutSMTPReturnsUnavailable(t *testing.T) {
	s := newTestAuthServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/code/send", strings.NewReader(`{"email":"active@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"unavailable"`) {
		t.Fatalf("status = %d body=%s, want 503 with unavailable code", rec.Code, rec.Body.String())
	}
}

func TestLoginLinkAuthenticatesExactlyOnce(t *testing.T) {
	s := newTestAuthServer(t)
	user, err := s.users.CreateWithAudit("link@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, err := s.codes.CreateLoginLink(user.Email)
	if err != nil {
		t.Fatalf("CreateLoginLink: %v", err)
	}
	body := `{"token":"` + token + `"}`
	first := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/link/verify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.e.ServeHTTP(first, req)
	if first.Code != http.StatusOK || firstCookie(t, first, "fanout_session") == nil {
		t.Fatalf("first redemption = %d %s", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/link/verify", strings.NewReader(body))
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
	if _, err := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/code/send", strings.NewReader(`{"email":"new-viewer@example.com"}`))
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
	if _, err := s.users.CreateWithAudit("admin@example.com", "", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"}); err != nil {
		t.Fatalf("Create admin: %v", err)
	}
	code, err := s.codes.Create("new-viewer@example.com")
	if err != nil {
		t.Fatalf("Create code: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/code/verify", strings.NewReader(`{"email":"NEW-viewer@example.com","code":"`+code+`"}`))
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
	if user.Role != auth.RoleViewer || user.Status != auth.UserStatusActive {
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
		req := httptest.NewRequest(http.MethodPost, "/api/auth/code/verify", strings.NewReader(`{"email":"visitor@example.com","code":"`+code+`"}`))
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

	t.Run("suspended user", func(t *testing.T) {
		s := newTestAuthServerWith(t, config.Config{AuthMode: "local", SelfSignup: true}, auth.SMTPConfig{})
		if _, err := s.users.CreateWithAudit("admin@example.com", "", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"}); err != nil {
			t.Fatalf("Create admin: %v", err)
		}
		viewer, err := s.users.CreateWithAudit("suspended@example.com", "", auth.RoleViewer, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
		if err != nil {
			t.Fatalf("Create viewer: %v", err)
		}
		status := auth.UserStatusSuspended
		if _, err := s.users.UpdateWithAudit(viewer.ID, nil, nil, nil, &status, auth.AuditEvent{EventType: "user.updated", Outcome: "success"}); err != nil {
			t.Fatalf("suspend: %v", err)
		}
		code, err := s.codes.Create(viewer.Email)
		if err != nil {
			t.Fatalf("Create code: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/auth/code/verify", strings.NewReader(`{"email":"suspended@example.com","code":"`+code+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("suspended verify = %d %s, want 401", rec.Code, rec.Body.String())
		}
		got, err := s.users.GetByEmail(viewer.Email)
		if err != nil || got.Status == auth.UserStatusActive {
			t.Fatalf("suspended user was reactivated: user=%#v err=%v", got, err)
		}
	})
}

func TestLocalSelfSignupDisabledRejectsUnknownVerifiedAddress(t *testing.T) {
	s := newTestAuthServer(t)
	if _, err := s.users.CreateWithAudit("admin@example.com", "", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"}); err != nil {
		t.Fatalf("Create admin: %v", err)
	}
	code, err := s.codes.Create("visitor@example.com")
	if err != nil {
		t.Fatalf("Create code: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/code/verify", strings.NewReader(`{"email":"visitor@example.com","code":"`+code+`"}`))
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

	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"email":"admin@example.com","display_name":"First Admin","setup_token":"`+s.setupToken+`"}`))
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
	if !strings.HasPrefix(body["ingest_token"], "foi_") {
		t.Fatalf("ingest token = %q", body["ingest_token"])
	}
	if body["ingest_header_name"] != "Authorization" {
		t.Fatalf("ingest header name = %q", body["ingest_header_name"])
	}
	if user, err := s.users.GetByEmail("admin@example.com"); err != nil || user.Role != auth.RoleAdmin || user.DisplayName != "First Admin" || user.Status != auth.UserStatusActive {
		t.Fatalf("admin = %+v err=%v", user, err)
	}
	if got := s.setup.Verify(s.setupToken); got != auth.SetupStatusUnset {
		t.Fatalf("setup state = %v", got)
	}
}

func TestSetupExpiryAndExistingAdminRetry(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		e := echo.New()
		e.HTTPErrorHandler = HTTPErrorHandler
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
		if _, err := s.users.CreateFirstAdminWithAudit("admin@example.com", "Admin", auth.AuditEvent{EventType: "setup.completed", Outcome: "success"}); err != nil {
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

func TestSetupReturnsPublicIngestEndpoint(t *testing.T) {
	const endpoint = "https://fanout.example.com"
	s := newTestAuthServerWith(t, config.Config{AuthMode: "local", PublicURL: endpoint}, auth.SMTPConfig{})
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

const breakGlassTestSecret = "0123456789abcdef0123456789abcdef"

func newBreakGlassTestServer(t *testing.T, secret string) *testAuthServer {
	t.Helper()
	return newTestAuthServerWith(t, config.Config{AuthMode: "oidc", AuthCodeSecret: secret}, auth.SMTPConfig{})
}

func (s *testAuthServer) redeemLink(token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/link/verify", strings.NewReader(`{"token":"`+token+`"}`))
	req.Header.Set("Content-Type", "application/json")
	s.e.ServeHTTP(rec, req)
	return rec
}

func (s *testAuthServer) countAudit(t *testing.T, eventType string) int {
	t.Helper()
	var events int
	if err := s.db.DB.QueryRow(`SELECT COUNT(*) FROM auth_audit_events WHERE event_type = ?`, eventType).Scan(&events); err != nil {
		t.Fatalf("query audit: %v", err)
	}
	return events
}

func (s *testAuthServer) countFailedReason(t *testing.T, reason string) int {
	t.Helper()
	var events int
	if err := s.db.DB.QueryRow(`SELECT COUNT(*) FROM auth_audit_events WHERE event_type = 'login.failed' AND json_extract(metadata_json, '$.reason') = ?`, reason).Scan(&events); err != nil {
		t.Fatalf("query audit reason: %v", err)
	}
	return events
}

func hasSessionCookie(rec *httptest.ResponseRecorder) bool {
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "fanout_session" && cookie.Value != "" {
			return true
		}
	}
	return false
}

func TestBreakGlassLinkAuthenticatesActiveAdministratorOnceInOIDCMode(t *testing.T) {
	s := newBreakGlassTestServer(t, breakGlassTestSecret)
	user, err := s.users.CreateWithAudit("admin@example.com", "", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, err := s.codes.CreateBreakGlassLoginLink(user.Email)
	if err != nil {
		t.Fatalf("CreateBreakGlassLoginLink: %v", err)
	}
	first := s.redeemLink(token)
	if first.Code != http.StatusOK || !hasSessionCookie(first) {
		t.Fatalf("first redemption = %d %s", first.Code, first.Body.String())
	}
	if second := s.redeemLink(token); second.Code != http.StatusUnauthorized || hasSessionCookie(second) {
		t.Fatalf("second redemption = %d %s, want 401", second.Code, second.Body.String())
	}
	if got := s.countAudit(t, "login_link.break_glass_redeemed"); got != 1 {
		t.Fatalf("break-glass redeemed events = %d, want 1", got)
	}
	if got := s.countAudit(t, "login_link.redeemed"); got != 0 {
		t.Fatalf("ordinary redeemed events = %d, want 0", got)
	}
}

func TestOIDCModeRefusesOrdinaryLoginLinkWithoutConsumingIt(t *testing.T) {
	s := newBreakGlassTestServer(t, breakGlassTestSecret)
	user, err := s.users.CreateWithAudit("admin@example.com", "", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, err := s.codes.CreateLoginLink(user.Email)
	if err != nil {
		t.Fatalf("CreateLoginLink: %v", err)
	}
	if rec := s.redeemLink(token); rec.Code != http.StatusUnauthorized || hasSessionCookie(rec) {
		t.Fatalf("ordinary link in OIDC mode = %d %s, want 401 without a session", rec.Code, rec.Body.String())
	}
	if got := s.countAudit(t, "login.failed"); got != 1 {
		t.Fatalf("login.failed events = %d, want 1", got)
	}
	if got := s.countAudit(t, "login_link.redeemed") + s.countAudit(t, "login_link.break_glass_redeemed"); got != 0 {
		t.Fatalf("redeemed events = %d, want 0", got)
	}
	if _, ok, err := s.codes.VerifyLoginLink(token); err != nil || !ok {
		t.Fatalf("ordinary link was consumed: (%v, %v)", ok, err)
	}
}

func TestBreakGlassLinkRefusesIneligibleUsersAtRedemption(t *testing.T) {
	for _, tc := range []struct {
		name   string
		role   auth.Role
		status auth.UserStatus
		want   int
		reason string
	}{
		{"viewer", auth.RoleViewer, auth.UserStatusActive, http.StatusForbidden, "break_glass_requires_admin"},
		{"operator", auth.RoleOperator, auth.UserStatusActive, http.StatusForbidden, "break_glass_requires_admin"},
		{"suspended administrator", auth.RoleAdmin, auth.UserStatusSuspended, http.StatusUnauthorized, "suspended_or_missing_break_glass_user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newBreakGlassTestServer(t, breakGlassTestSecret)
			// A second administrator keeps the last-admin guard out of the way.
			if _, err := s.users.CreateWithAudit("keeper@example.com", "", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"}); err != nil {
				t.Fatalf("Create keeper: %v", err)
			}
			user, err := s.users.CreateWithAudit("target@example.com", "", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			// Issue while eligible: redemption must re-check the account's
			// current state rather than trust the state at issue time.
			token, err := s.codes.CreateBreakGlassLoginLink(user.Email)
			if err != nil {
				t.Fatalf("CreateBreakGlassLoginLink: %v", err)
			}
			role, status := tc.role, tc.status
			if _, err := s.users.UpdateWithAudit(user.ID, nil, nil, &role, &status, auth.AuditEvent{EventType: "user.updated", Outcome: "success"}); err != nil {
				t.Fatalf("Update: %v", err)
			}
			rec := s.redeemLink(token)
			if rec.Code != tc.want || hasSessionCookie(rec) {
				t.Fatalf("redemption = %d %s, want %d without a session", rec.Code, rec.Body.String(), tc.want)
			}
			if got := s.countAudit(t, "login_link.break_glass_redeemed"); got != 0 {
				t.Fatalf("redeemed events = %d, want 0", got)
			}
			if got := s.countFailedReason(t, tc.reason); got != 1 {
				t.Fatalf("login.failed events with reason %q = %d, want 1", tc.reason, got)
			}
		})
	}
}

func TestBreakGlassLinkForMissingUserHasDistinctAuditReason(t *testing.T) {
	s := newBreakGlassTestServer(t, breakGlassTestSecret)
	token, err := s.codes.CreateBreakGlassLoginLink("gone@example.com")
	if err != nil {
		t.Fatalf("CreateBreakGlassLoginLink: %v", err)
	}
	if rec := s.redeemLink(token); rec.Code != http.StatusUnauthorized || hasSessionCookie(rec) {
		t.Fatalf("redemption = %d %s, want 401 without a session", rec.Code, rec.Body.String())
	}
	if got := s.countFailedReason(t, "suspended_or_missing_break_glass_user"); got != 1 {
		t.Fatalf("break-glass reason events = %d, want 1", got)
	}
	if got := s.countFailedReason(t, "suspended_or_missing_login_link_user"); got != 0 {
		t.Fatalf("ordinary reason events = %d, want 0", got)
	}
}

func TestOrdinaryLoginLinkKeepsItsSuspendedAuditReason(t *testing.T) {
	s := newTestAuthServer(t)
	token, err := s.codes.CreateLoginLink("gone@example.com")
	if err != nil {
		t.Fatalf("CreateLoginLink: %v", err)
	}
	if rec := s.redeemLink(token); rec.Code != http.StatusUnauthorized {
		t.Fatalf("redemption = %d %s, want 401", rec.Code, rec.Body.String())
	}
	if got := s.countFailedReason(t, "suspended_or_missing_login_link_user"); got != 1 {
		t.Fatalf("ordinary reason events = %d, want 1", got)
	}
}

func (s *testAuthServer) failAuditWrites(t *testing.T, eventType string) {
	t.Helper()
	if _, err := s.db.DB.Exec(`CREATE TRIGGER fail_audit BEFORE INSERT ON auth_audit_events WHEN NEW.event_type = '` + eventType + `' BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END`); err != nil {
		t.Fatalf("create audit failure trigger: %v", err)
	}
}

func (s *testAuthServer) countSessions(t *testing.T) int {
	t.Helper()
	var sessions int
	if err := s.db.DB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return sessions
}

func TestBreakGlassRedemptionFailsClosedWhenAuditWriteFails(t *testing.T) {
	s := newBreakGlassTestServer(t, breakGlassTestSecret)
	user, err := s.users.CreateWithAudit("admin@example.com", "", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, err := s.codes.CreateBreakGlassLoginLink(user.Email)
	if err != nil {
		t.Fatalf("CreateBreakGlassLoginLink: %v", err)
	}
	s.failAuditWrites(t, "login_link.break_glass_redeemed")
	rec := s.redeemLink(token)
	if rec.Code != http.StatusInternalServerError || hasSessionCookie(rec) {
		t.Fatalf("redemption = %d %s, want 500 without a session", rec.Code, rec.Body.String())
	}
	if got := s.countSessions(t); got != 0 {
		t.Fatalf("stored sessions = %d, want 0", got)
	}
	// establishSession records the login first, so an untouched timestamp shows
	// the audit failure stopped the request before any session was started.
	var lastLogin sql.NullString
	if err := s.db.DB.QueryRow(`SELECT logged_in_at FROM users WHERE id = ?`, user.ID).Scan(&lastLogin); err != nil {
		t.Fatalf("query last login: %v", err)
	}
	if lastLogin.String != "" {
		t.Fatalf("logged_in_at = %q, want no session establishment", lastLogin.String)
	}
}

func TestOrdinaryLoginLinkStillSucceedsWhenAuditWriteFails(t *testing.T) {
	s := newTestAuthServer(t)
	user, err := s.users.CreateWithAudit("user@example.com", "", auth.RoleViewer, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, err := s.codes.CreateLoginLink(user.Email)
	if err != nil {
		t.Fatalf("CreateLoginLink: %v", err)
	}
	s.failAuditWrites(t, "login_link.redeemed")
	if rec := s.redeemLink(token); rec.Code != http.StatusOK || !hasSessionCookie(rec) {
		t.Fatalf("redemption = %d %s, want 200 with a session", rec.Code, rec.Body.String())
	}
}

func TestOIDCModeWithoutUsableSecretKeepsLinkRouteDisabled(t *testing.T) {
	for _, secret := range []string{"", "too-short"} {
		s := newBreakGlassTestServer(t, secret)
		user, err := s.users.CreateWithAudit("admin@example.com", "", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		token, err := s.codes.CreateBreakGlassLoginLink(user.Email)
		if err != nil {
			t.Fatalf("CreateBreakGlassLoginLink: %v", err)
		}
		if rec := s.redeemLink(token); rec.Code != http.StatusNotFound || hasSessionCookie(rec) {
			t.Fatalf("secret %q: redemption = %d %s, want 404", secret, rec.Code, rec.Body.String())
		}
		if got := s.countAudit(t, "login.failed"); got != 0 {
			t.Fatalf("secret %q: login.failed events = %d, want 0", secret, got)
		}
		if _, ok, err := s.codes.VerifyBreakGlassLoginLink(token); err != nil || !ok {
			t.Fatalf("secret %q: link was consumed: (%v, %v)", secret, ok, err)
		}
	}
}

func TestLocalModeRefusesBreakGlassLink(t *testing.T) {
	s := newTestAuthServer(t)
	user, err := s.users.CreateWithAudit("admin@example.com", "", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, err := s.codes.CreateBreakGlassLoginLink(user.Email)
	if err != nil {
		t.Fatalf("CreateBreakGlassLoginLink: %v", err)
	}
	if rec := s.redeemLink(token); rec.Code != http.StatusUnauthorized || hasSessionCookie(rec) {
		t.Fatalf("break-glass link in local mode = %d %s, want 401", rec.Code, rec.Body.String())
	}
}

func TestSetupKeepsTokenAppliedFromConfiguration(t *testing.T) {
	s := newTestAuthServer(t)
	store := settings.NewStore(s.db.DB)
	token := "foi_" + strings.Repeat("A", 43)
	if applied, err := store.ApplyConfiguredIngestToken(context.Background(), token, s.audit); err != nil || !applied {
		t.Fatalf("applied = %v err = %v", applied, err)
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
	if body["status"] != "authenticated" {
		t.Fatalf("status = %q", body["status"])
	}
	if _, issued := body["ingest_token"]; issued {
		t.Fatal("setup must not issue a token when configuration already supplied one")
	}
	if strings.Contains(rec.Body.String(), token) {
		t.Fatal("setup response exposed the configured token")
	}
	got, err := store.GetIngest(context.Background())
	if err != nil {
		t.Fatalf("GetIngest: %v", err)
	}
	if !settings.CheckIngestToken(token, got.TokenHash) {
		t.Fatal("setup replaced the configured token")
	}
}
