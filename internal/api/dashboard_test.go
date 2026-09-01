package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/fanout/internal/dashboard"
)

func TestAnonymousCannotOwnDashboards(t *testing.T) {
	s := newTestAuthServer(t)
	if _, err := s.users.Create("admin@example.com", "", "admin"); err != nil {
		t.Fatalf("Create admin: %v", err)
	}
	RegisterDashboardRoutes(s.e, dashboard.New(s.db.DB, 30))
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dashboards", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous dashboard list = %d, want 401", rec.Code)
	}
}

func TestDashboardRoutesHideOtherOwnersResources(t *testing.T) {
	s := newTestAuthServer(t)
	ownerA, err := s.users.Create("owner-a@example.com", "", "operator")
	if err != nil {
		t.Fatalf("Create owner A: %v", err)
	}
	ownerB, err := s.users.Create("owner-b@example.com", "", "operator")
	if err != nil {
		t.Fatalf("Create owner B: %v", err)
	}
	dashboards := dashboard.New(s.db.DB, 30)
	item, err := dashboards.Default(t.Context(), ownerA.ID)
	if err != nil {
		t.Fatalf("create dashboard: %v", err)
	}
	RegisterDashboardRoutes(s.e, dashboards)

	recorder := httptest.NewRecorder()
	s.e.ServeHTTP(recorder, sessionRequest(http.MethodGet, "/api/dashboards/"+item.ID, nil, s.login(t, ownerB)))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("cross-owner dashboard read = %d, want 404", recorder.Code)
	}
}
