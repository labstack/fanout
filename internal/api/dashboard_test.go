package api

import (
	"context"
	"encoding/json"
	"github.com/labstack/fanout/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
)

type structuralValidator struct{}

func (structuralValidator) Validate(_ context.Context, d *panel.Dashboard) error {
	panel.Normalize(d)
	if problems := panel.Validate(d); len(problems) > 0 {
		return problems
	}
	return nil
}

func TestAnonymousCannotOwnDashboards(t *testing.T) {
	s := newTestAuthServer(t)
	if _, err := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"}); err != nil {
		t.Fatalf("Create admin: %v", err)
	}
	RegisterDashboardRoutes(s.e, dashboard.New(s.db.DB, structuralValidator{}))
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dashboards", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous dashboard list = %d, want 401", rec.Code)
	}
}

func TestDashboardLifecycleOverHTTP(t *testing.T) {
	s := newTestAuthServer(t)
	if _, err := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.users.CreateWithAudit("owner@example.com", "", "operator", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	other, _ := s.users.CreateWithAudit("other@example.com", "", "operator", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	RegisterDashboardRoutes(s.e, dashboard.New(s.db.DB, structuralValidator{}))
	ownerCookie, otherCookie := s.login(t, owner), s.login(t, other)
	call := func(method, path, body string, cookie *http.Cookie, headers ...string) *httptest.ResponseRecorder {
		var reader *strings.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := sessionRequest(method, path, reader, cookie)
		req.Header.Set("Content-Type", "application/json")
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		return rec
	}

	rec := call(http.MethodPost, "/api/dashboards", `{"spec":{"name":"Ops","panels":[{"id":"notes","title":"Notes","viz":"text","content":"hi"}]}}`, ownerCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	var created dashboard.Record
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	if rec := call(http.MethodGet, "/api/dashboards/"+created.ID, "", otherCookie); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-owner read = %d", rec.Code)
	}
	rec = call(http.MethodPatch, "/api/dashboards/"+created.ID, `{"operations":[{"op":"update_panel","id":"notes","set":{"content":"changed"}}],"base_version":1,"message":"Reword"}`, ownerCookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"version":2`) {
		t.Fatalf("patch %d %s", rec.Code, rec.Body)
	}
	rec = call(http.MethodPut, "/api/dashboards/"+created.ID, `{"spec":{"name":"Ops","panels":[{"id":"notes","title":"Notes","viz":"text","content":"x"}]},"base_version":1}`, ownerCookie)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "changed since") {
		t.Fatalf("stale put %d %s", rec.Code, rec.Body)
	}
	rec = call(http.MethodPut, "/api/dashboards/"+created.ID, `{"spec":{"name":"Ops","panels":[{"id":"notes","title":"Notes","viz":"piechart"}]}}`, ownerCookie)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"problems"`) {
		t.Fatalf("invalid put %d %s", rec.Code, rec.Body)
	}
	rec = call(http.MethodGet, "/api/dashboards/"+created.ID+"/versions", "", ownerCookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"Reword"`) {
		t.Fatalf("versions %d %s", rec.Code, rec.Body)
	}
	rec = call(http.MethodPost, "/api/dashboards/"+created.ID+"/versions/1/restore", "", ownerCookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"version":3`) {
		t.Fatalf("restore %d %s", rec.Code, rec.Body)
	}
	rec = call(http.MethodPost, "/api/dashboards/"+created.ID+"/versions/9/restore", "", ownerCookie)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "version 9 of dashboard "+created.ID+" does not exist") || !strings.Contains(rec.Body.String(), `"error_code":"dashboard_version_not_found"`) {
		t.Fatalf("missing version %d %s", rec.Code, rec.Body)
	}
	if rec := call(http.MethodDelete, "/api/dashboards/"+created.ID, "", ownerCookie); rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("unconfirmed delete = %d", rec.Code)
	}
	if rec := call(http.MethodDelete, "/api/dashboards/"+created.ID, "", ownerCookie, "Fanout-Confirm-Delete", created.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
}

type failingValidator struct{ err error }

func (v failingValidator) Validate(context.Context, *panel.Dashboard) error { return v.err }

func TestDashboardValidationTimeoutIs504(t *testing.T) {
	s := newTestAuthServer(t)
	if _, err := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.users.CreateWithAudit("owner@example.com", "", "operator", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	RegisterDashboardRoutes(s.e, dashboard.New(s.db.DB, failingValidator{err: context.DeadlineExceeded}))
	cookie := s.login(t, owner)
	req := sessionRequest(http.MethodPost, "/api/dashboards", strings.NewReader(`{"spec":{"name":"Ops","panels":[{"id":"notes","title":"Notes","viz":"text","content":"hi"}]}}`), cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout create = %d %s", rec.Code, rec.Body)
	}
}

func TestDashboardBrowserSummaryKeepsOwnerScopedBuildProvenance(t *testing.T) {
	s := newTestAuthServer(t)
	owner, err := s.users.CreateWithAudit("origin@example.test", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.users.CreateWithAudit("other-origin@example.test", "", "operator", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.DB.Exec(`INSERT INTO agui_threads(thread_id,owner_id) VALUES ('source',?)`, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	service := dashboard.New(s.db.DB, structuralValidator{})
	origin := dashboard.BuildOrigin{ThreadID: "source", MessageID: "request", RequestExcerpt: "Private request"}
	_, err = service.Create(dashboard.WithBuildOrigin(t.Context(), origin), owner.ID, panel.Dashboard{Name: "Origin", Panels: []panel.Panel{{ID: "notes", Title: "Notes", Viz: "text", Content: "hi"}}}, dashboard.Author{Kind: "agent", ID: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	RegisterDashboardRoutes(s.e, service)
	for _, user := range []auth.User{owner, other} {
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, sessionRequest(http.MethodGet, "/api/dashboards", nil, s.login(t, user)))
		if rec.Code != http.StatusOK {
			t.Fatal(rec.Code, rec.Body)
		}
		has := strings.Contains(rec.Body.String(), `"request_excerpt":"Private request"`)
		if has != (user.ID == owner.ID) {
			t.Fatal("browser origin scope", rec.Body)
		}
	}
}
