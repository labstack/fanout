package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/env"
)

func registerTestUserRoutes(s *testAuthServer) {
	RegisterUserRoutes(s.e, s.users, auth.SMTPConfig{}, env.Config{AuthMode: "oidc"})
}

func TestUserMutationsProtectLastActiveAdmin(t *testing.T) {
	for _, tc := range []struct{ name, method, body string }{
		{"demote", http.MethodPut, `{"role":"viewer"}`},
		{"deactivate", http.MethodPut, `{"active":false}`},
		{"delete", http.MethodDelete, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestAuthServer(t)
			registerTestUserRoutes(s)
			admin, _ := s.users.Create("admin@example.com", "", "admin")
			cookie := s.login(t, admin)
			var body *strings.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := sessionRequest(tc.method, "/api/users/"+admin.ID, body, cookie)
			if body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			s.e.ServeHTTP(rec, req)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409", rec.Code)
			}
		})
	}
}

func TestDeleteUser_AllowsDeletingAdminWhenAnotherActiveAdminExists(t *testing.T) {
	s := newTestAuthServer(t)
	registerTestUserRoutes(s)
	admin, _ := s.users.Create("admin@example.com", "", "admin")
	other, _ := s.users.Create("other@example.com", "", "admin")
	cookie := s.login(t, admin)
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, sessionRequest(http.MethodDelete, "/api/users/"+other.ID, nil, cookie))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestLogoutAllRevokesTargetSessions(t *testing.T) {
	s := newTestAuthServer(t)
	registerTestUserRoutes(s)
	admin, _ := s.users.Create("admin@example.com", "", "admin")
	target, _ := s.users.Create("target@example.com", "", "operator")
	adminCookie := s.login(t, admin)
	targetCookie := s.login(t, target)
	revoke := httptest.NewRecorder()
	s.e.ServeHTTP(revoke, sessionRequest(http.MethodPost, "/api/users/"+target.ID+"/logout-all", nil, adminCookie))
	if revoke.Code != http.StatusOK {
		t.Fatalf("logout-all = %d %s", revoke.Code, revoke.Body.String())
	}
	me := httptest.NewRecorder()
	s.e.ServeHTTP(me, sessionRequest(http.MethodGet, "/api/auth/me", nil, targetCookie))
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("target reused session = %d", me.Code)
	}
}
