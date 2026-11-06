package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/alert"
	"github.com/labstack/fanout/internal/auth"
)

func TestAlertingReadRoutesRequireAuthenticationAndPreserveBodies(t *testing.T) {
	s := newTestAuthServer(t)
	RegisterAlertRoutes(s.e, nil, nil)
	viewer, err := s.users.CreateWithAudit("alert-reader@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := s.login(t, viewer)
	for _, tc := range []struct{ path, body string }{
		{"/api/alerting/alerts", `{"alerts":[]}`},
		{"/api/alerting/summary", `{"firing":0,"pending":0,"resolved":0}`},
		{"/api/alerting/rules", `{"rules":[]}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			anonymous := httptest.NewRecorder()
			s.e.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if anonymous.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous read = %d, want 401: %s", anonymous.Code, anonymous.Body.String())
			}
			authenticated := httptest.NewRecorder()
			s.e.ServeHTTP(authenticated, sessionRequest(http.MethodGet, tc.path, nil, cookie))
			if authenticated.Code != http.StatusOK || strings.TrimSpace(authenticated.Body.String()) != tc.body {
				t.Fatalf("viewer read = %d %s, want 200 %s", authenticated.Code, authenticated.Body.String(), tc.body)
			}
		})
	}
}

func TestRetiredAlertRoutesReturn404WithSPACatchAll(t *testing.T) {
	s := newTestAuthServer(t)
	RegisterAlertRoutes(s.e, nil, nil)
	s.e.GET("/*", func(c *echo.Context) error { return c.String(http.StatusOK, "SPA") })
	viewer, err := s.users.CreateWithAudit("retired-alert-reader@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := s.login(t, viewer)
	retiredPath := "/api/" + "alerts"
	for _, path := range []string{retiredPath, retiredPath + "/summary"} {
		for _, session := range []struct {
			name   string
			cookie *http.Cookie
		}{
			{"anonymous", nil},
			{"authenticated", cookie},
		} {
			t.Run(session.name+" "+path, func(t *testing.T) {
				rec := httptest.NewRecorder()
				s.e.ServeHTTP(rec, sessionRequest(http.MethodGet, path, nil, session.cookie))
				if rec.Code != http.StatusNotFound {
					t.Fatalf("retired route = %d, want 404: %s", rec.Code, rec.Body.String())
				}
			})
		}
	}
}

func TestAlertRulesRejectNonStringWebhookHeaders(t *testing.T) {
	s := newTestAuthServer(t)
	RegisterAlertRoutes(s.e, alert.NewStore(s.db.DB), nil)
	admin, err := s.users.CreateWithAudit("alert-admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := s.login(t, admin)
	create := func(headers string) *httptest.ResponseRecorder {
		body := `{"name":"Errors","enabled":true,"expression":"error_rate > 0.05","webhook_headers":` + strconv.Quote(headers) + `}`
		req := sessionRequest(http.MethodPost, "/api/alerting/rules", strings.NewReader(body), cookie)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		return rec
	}
	if rec := create(`{"X-Test": 5}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "X-Test") {
		t.Fatalf("numeric header = %d %s, want 400 naming X-Test", rec.Code, rec.Body)
	}
	if rec := create(`{"X-Test": "5"}`); rec.Code != http.StatusCreated {
		t.Fatalf("string header = %d %s, want 201", rec.Code, rec.Body)
	}
}

// A rule saved before the header check tightened can still be toggled: a
// replace checks headers only when it changes them.
func TestAlertRuleReplaceChecksOnlyChangedHeaders(t *testing.T) {
	s := newTestAuthServer(t)
	store := alert.NewStore(s.db.DB)
	RegisterAlertRoutes(s.e, store, nil)
	admin, err := s.users.CreateWithAudit("alert-admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := s.login(t, admin)
	stored, err := store.CreateRule(alert.Rule{Name: "Errors", Enabled: true, Expression: "error_rate > 0.05", WebhookHeaders: `{"Bad Name":"x"}`})
	if err != nil {
		t.Fatal(err)
	}
	replace := func(headers string) *httptest.ResponseRecorder {
		body := `{"name":"Errors","enabled":false,"expression":"error_rate > 0.05","webhook_headers":` + strconv.Quote(headers) + `}`
		req := sessionRequest(http.MethodPut, "/api/alerting/rules/"+stored.ID, strings.NewReader(body), cookie)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		return rec
	}
	if rec := replace(stored.WebhookHeaders); rec.Code != http.StatusOK {
		t.Fatalf("toggle with stored headers = %d %s, want 200", rec.Code, rec.Body)
	}
	if rec := replace(`{"Other Bad":"x"}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Other Bad") {
		t.Fatalf("changed invalid headers = %d %s, want 400", rec.Code, rec.Body)
	}
}

// Disabling a rule resolves its firing alerts instead of deleting them.
func TestAlertRuleDisableResolvesFiringAlerts(t *testing.T) {
	s := newTestAuthServer(t)
	store := alert.NewStore(s.db.DB)
	RegisterAlertRoutes(s.e, store, alert.NewEngine(store, nil, nil, 30*time.Second, 7))
	admin, err := s.users.CreateWithAudit("alert-admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := s.login(t, admin)
	rule, err := store.CreateRule(alert.Rule{Name: "Errors", Enabled: true, Expression: "error_rate > 0.05"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAlert(alert.Alert{RuleID: rule.ID, Service: "checkout", State: "firing", FiredAt: time.Now().Format(time.RFC3339), CreatedAt: time.Now().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	req := sessionRequest(http.MethodPut, "/api/alerting/rules/"+rule.ID, strings.NewReader(`{"name":"Errors","enabled":false,"expression":"error_rate > 0.05"}`), cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable = %d %s", rec.Code, rec.Body)
	}
	a, err := store.GetAlert(rule.ID, "checkout")
	if err != nil {
		t.Fatalf("firing alert was deleted: %v", err)
	}
	if a.State != "resolved" || a.ResolvedAt == "" {
		t.Fatalf("alert = %+v, want resolved", a)
	}
}

func TestAlertCollectionsPreserveItemsAndFilters(t *testing.T) {
	s := newTestAuthServer(t)
	store := alert.NewStore(s.db.DB)
	RegisterAlertRoutes(s.e, store, nil)
	viewer, err := s.users.CreateWithAudit("collections@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := s.login(t, viewer)
	read := func(path, key string, want any) {
		t.Helper()
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, sessionRequest(http.MethodGet, path, nil, cookie))
		var got map[string]json.RawMessage
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, rec.Code, rec.Body)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || !reflect.DeepEqual(got[key], json.RawMessage(encoded)) {
			t.Fatalf("%s = %s, want %s", path, rec.Body, encoded)
		}
	}
	read("/api/alerting/alerts", "alerts", []alert.Alert{})
	read("/api/alerting/rules", "rules", []alert.Rule{})
	rule, err := store.CreateRule(alert.Rule{Name: "Errors", Enabled: true, Expression: "error_rate > 0.05"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.UpsertAlert(alert.Alert{RuleID: rule.ID, Service: "checkout", State: "firing", CreatedAt: "2026-10-01T12:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	read("/api/alerting/rules", "rules", []alert.Rule{rule})
	read("/api/alerting/alerts?state=firing&service=checkout&rule_id="+rule.ID, "alerts", []alert.Alert{item})
	read("/api/alerting/alerts?service=missing", "alerts", []alert.Alert{})
}
