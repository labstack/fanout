package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
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
	assertHistoryWire(t, rec.Body.Bytes(), false)
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
	assertHistoryWire(t, rec.Body.Bytes(), true)
	for _, tc := range []struct {
		cookie        *http.Cookie
		message, code string
	}{
		{cookie, "version 99 of dashboard " + first.ID + " does not exist", "dashboard_version_not_found"},
		{s.login(t, other), "dashboard not found", "not_found"},
	} {
		var wire map[string]any
		missing := call("99", tc.cookie)
		if err := json.Unmarshal(missing.Body.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		if missing.Code != 404 || wire["message"] != tc.message || (tc.code != "" && wire["code"] != tc.code) || (tc.code == "" && wire["code"] != nil) {
			t.Fatalf("missing = %s", missing.Body)
		}
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
	current := httptest.NewRecorder()
	s.e.ServeHTTP(current, sessionRequest(http.MethodPost, "/api/dashboards/"+first.ID+"/versions/3/restore", nil, cookie))
	var conflict map[string]any
	if err := json.Unmarshal(current.Body.Bytes(), &conflict); err != nil {
		t.Fatal(err)
	}
	if current.Code != 409 || conflict["code"] != "already_current" {
		t.Fatalf("current restore=%d %s", current.Code, current.Body)
	}
	versions, err := service.Versions(t.Context(), owner.ID, first.ID)
	if err != nil || len(versions) != 3 {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
	if _, err := s.db.DB.Exec("DELETE FROM dashboard_versions WHERE dashboard_id=? AND version=1", first.ID); err != nil {
		t.Fatal(err)
	}
	pruned := call("2", cookie)
	assertHistoryWire(t, pruned.Body.Bytes(), true)
	var unavailable map[string]any
	if err := json.Unmarshal(pruned.Body.Bytes(), &unavailable); err != nil {
		t.Fatal(err)
	}
	if pruned.Code != 200 || unavailable["changes_available"] != false || len(unavailable["changes"].([]any)) != 0 {
		t.Fatalf("pruned wire=%s", pruned.Body)
	}
}

func assertHistoryWire(t *testing.T, raw []byte, emptyDashboardFields bool) {
	t.Helper()
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(wire))
	for key := range wire {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	want := []string{"author_id", "author_kind", "changes", "changes_available", "created_at", "dashboard", "dashboard_fields", "layout_changed", "message"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("wire keys=%v", keys)
	}
	changes, ok := wire["changes"].([]any)
	if !ok {
		t.Fatalf("changes must be an array: %s", raw)
	}
	fields, ok := wire["dashboard_fields"].([]any)
	if !ok || (emptyDashboardFields && len(fields) != 0) {
		t.Fatalf("dashboard_fields=%v", wire["dashboard_fields"])
	}
	if _, ok := wire["changes_available"].(bool); !ok {
		t.Fatal("missing boolean changes_available")
	}
	if _, ok := wire["layout_changed"].(bool); !ok {
		t.Fatal("missing boolean layout_changed")
	}
	if wire["changes_available"] == false && len(changes) != 0 {
		t.Fatal("unavailable changes must be []")
	}
	if wire["dashboard"].(map[string]any)["version"] == float64(1) {
		if len(changes) != 1 || changes[0].(map[string]any)["kind"] != "added" || wire["layout_changed"] != false {
			t.Fatalf("create diff=%v", changes)
		}
	}
}
