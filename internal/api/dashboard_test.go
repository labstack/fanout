package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/dashboard"
	appstore "github.com/labstack/fanout/internal/store"
)

func TestPublicViewerCannotOwnDashboards(t *testing.T) {
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })
	users := auth.NewUserStore(sqlite.DB)
	if _, err := users.Create("admin@example.com", "", "admin"); err != nil {
		t.Fatalf("Create admin: %v", err)
	}

	e := echo.New()
	RegisterAuthMiddleware(e, users, "0123456789abcdef0123456789abcdef", true)
	RegisterDashboardRoutes(e, dashboard.New(sqlite.DB))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dashboards", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous dashboard list = %d, want 401", rec.Code)
	}
}
