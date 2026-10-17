package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

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
		{"/api/alerting/alerts", "[]"},
		{"/api/alerting/summary", `{"firing":0,"pending":0,"resolved":0}`},
		{"/api/alerting/rules", "[]"},
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
