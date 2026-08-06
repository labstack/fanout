package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
	appstore "github.com/labstack/fanout/internal/store"
)

func authenticatedSessionCookie(t *testing.T, sessions *auth.BrowserSessions, user auth.User) *http.Cookie {
	t.Helper()
	handler := sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := sessions.EstablishAuthenticatedSession(r.Context(), user); err != nil {
			t.Fatalf("EstablishAuthenticatedSession: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/login", nil))
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("session cookies = %d, want 1", len(cookies))
	}
	return cookies[0]
}

func TestSessionMiddlewareCSRFAndMetricsCredential(t *testing.T) {
	db, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	users := auth.NewUserStore(db.DB)
	user, err := users.Create("admin@example.com", "", "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sessions := auth.NewBrowserSessions(db.DB, 12*time.Hour, 7*24*time.Hour, false)
	cfg := config.Config{MetricsToken: "metrics-secret"}
	e := echo.New()
	RegisterAuthMiddleware(e, users, sessions, auth.NewAuditStore(db.DB), cfg)
	e.POST("/api/rules", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	e.GET("/-/metrics", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })

	cookie := authenticatedSessionCookie(t, sessions, user)
	withoutHeader := httptest.NewRequest(http.MethodPost, "/api/rules", nil)
	withoutHeader.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, withoutHeader)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("session mutation without Fanout header = %d, want 403", recorder.Code)
	}

	withHeader := httptest.NewRequest(http.MethodPost, "/api/rules", nil)
	withHeader.AddCookie(cookie)
	withHeader.Header.Set("X-Fanout-Request", "1")
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, withHeader)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("session mutation with Fanout header = %d, want 204", recorder.Code)
	}

	metrics := httptest.NewRequest(http.MethodGet, "/-/metrics", nil)
	metrics.Header.Set("Authorization", "Bearer metrics-secret")
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, metrics)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("metrics service credential = %d, want 204", recorder.Code)
	}

	badMetrics := httptest.NewRequest(http.MethodGet, "/-/metrics", nil)
	badMetrics.Header.Set("Authorization", "Bearer not-a-jwt")
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, badMetrics)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid metrics credential = %d, want 401", recorder.Code)
	}

	viewer, err := users.Create("viewer@example.com", "", "viewer")
	if err != nil {
		t.Fatalf("Create viewer: %v", err)
	}
	viewerMetrics := httptest.NewRequest(http.MethodGet, "/-/metrics", nil)
	viewerMetrics.AddCookie(authenticatedSessionCookie(t, sessions, viewer))
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, viewerMetrics)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("viewer metrics request = %d, want 403", recorder.Code)
	}

	noToken := echo.New()
	RegisterAuthMiddleware(noToken, users, sessions, auth.NewAuditStore(db.DB), config.Config{})
	noToken.GET("/-/metrics", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	recorder = httptest.NewRecorder()
	noToken.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/-/metrics", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("empty configured token accepted missing bearer: %d", recorder.Code)
	}

	publicMetrics := echo.New()
	RegisterAuthMiddleware(publicMetrics, users, sessions, auth.NewAuditStore(db.DB), config.Config{MetricsPublic: true})
	publicMetrics.GET("/-/metrics", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	recorder = httptest.NewRecorder()
	publicMetrics.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/-/metrics", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("public metrics request = %d, want 204", recorder.Code)
	}
}
