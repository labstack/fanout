package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
)

func TestHistoricalDashboardHTTPReadRequiresOwnerAndReturnsSaveChanges(t *testing.T) {
	s := newTestAuthServer(t)
	owner, err := s.users.CreateWithAudit("history@example.test", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.users.CreateWithAudit("other-history@example.test", "", "operator", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := s.users.CreateWithAudit("viewer-history@example.test", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	service := dashboard.New(s.db.DB, structuralValidator{})
	first, err := service.Create(t.Context(), owner.ID, panel.Dashboard{Name: "History", Panels: []panel.Panel{{ID: "notes", Title: "Notes", Viz: "text", Content: "hello"}}}, dashboard.Author{Kind: "agent", ID: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := service.EditWithChanges(t.Context(), owner.ID, first.ID, []dashboard.Operation{{Op: "update_panel", ID: "notes", Set: map[string]any{"title": "New title"}}}, 1, dashboard.Author{Kind: "user", ID: owner.ID}, "Updated title")
	if err != nil {
		t.Fatal(err)
	}
	RegisterDashboardRoutes(s.e, service)
	cookie := s.login(t, owner)
	call := func(version string, cookie *http.Cookie) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, sessionRequest(http.MethodGet, "/api/dashboards/"+first.ID+"/versions/"+version, nil, cookie))
		return rec
	}
	for _, tc := range []struct {
		version string
		cookie  *http.Cookie
		status  int
	}{{"1", nil, 401}, {"1", s.login(t, viewer), 404}, {"1", s.login(t, other), 404}, {"0", cookie, 400}, {"-1", cookie, 400}, {"bad", cookie, 400}, {"99", cookie, 404}} {
		if rec := call(tc.version, tc.cookie); rec.Code != tc.status {
			t.Fatalf("version %s: %d %s", tc.version, rec.Code, rec.Body)
		}
	}
	rec := call("2", cookie)
	if rec.Code != 200 {
		t.Fatalf("read: %d %s", rec.Code, rec.Body)
	}
	var got dashboard.VersionRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	diff := dashboard.Changes(saved.Before, saved.Record.Spec)
	if !got.ChangesAvailable || !reflect.DeepEqual(got.Changes, diff.Panels) || got.LayoutChanged != diff.LayoutChanged || !reflect.DeepEqual(got.DashboardFields, diff.DashboardFields) || got.Message != "Updated title" || got.AuthorKind != "user" || got.AuthorID != owner.ID {
		t.Fatal(got)
	}
	rec = call("1", cookie)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || got.Dashboard.Version != 1 || got.Dashboard.Spec.Panels[0].Title != "Notes" || got.CreatedAt != first.UpdatedAt || got.Dashboard.UpdatedAt != first.UpdatedAt || got.AuthorKind != "agent" || got.Message != "Created" {
		t.Fatal(got)
	}
	restore := httptest.NewRecorder()
	s.e.ServeHTTP(restore, sessionRequest(http.MethodPost, "/api/dashboards/"+first.ID+"/versions/1/restore", nil, cookie))
	if restore.Code != 200 {
		t.Fatal(restore.Code, restore.Body)
	}
	rec = call("3", cookie)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || got.AuthorID != owner.ID || got.AuthorKind != "user" || got.Message != "Restored version 1" || got.Dashboard.Version != 3 {
		t.Fatal(got)
	}
}
