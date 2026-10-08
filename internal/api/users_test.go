package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
)

func registerTestUserRoutes(s *testAuthServer) {
	RegisterUserRoutes(s.e, s.users, auth.SMTPConfig{}, config.Config{AuthMode: "oidc"})
}

func TestUserMutationsProtectLastActiveAdmin(t *testing.T) {
	for _, tc := range []struct{ name, method, body string }{
		{"demote", http.MethodPatch, `{"role":"viewer"}`},
		{"deactivate", http.MethodPatch, `{"active":false}`},
		{"delete", http.MethodDelete, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestAuthServer(t)
			registerTestUserRoutes(s)
			admin, _ := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
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
	admin, _ := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	other, _ := s.users.CreateWithAudit("other@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	cookie := s.login(t, admin)
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, sessionRequest(http.MethodDelete, "/api/users/"+other.ID, nil, cookie))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRevokeAccessRevokesTargetSessions(t *testing.T) {
	s := newTestAuthServer(t)
	registerTestUserRoutes(s)
	admin, _ := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	target, _ := s.users.CreateWithAudit("target@example.com", "", "operator", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	adminCookie := s.login(t, admin)
	targetCookie := s.login(t, target)
	revoke := httptest.NewRecorder()
	s.e.ServeHTTP(revoke, sessionRequest(http.MethodPost, "/api/users/"+target.ID+"/access/revoke", nil, adminCookie))
	if revoke.Code != http.StatusOK {
		t.Fatalf("revoke-access = %d %s", revoke.Code, revoke.Body.String())
	}
	me := httptest.NewRecorder()
	s.e.ServeHTTP(me, sessionRequest(http.MethodGet, "/api/auth/me", nil, targetCookie))
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("target reused session = %d", me.Code)
	}
}

func TestPatchUserPreservesOmittedFieldsAndAppliesFalse(t *testing.T) {
	s := newTestAuthServer(t)
	registerTestUserRoutes(s)
	admin, err := s.users.CreateWithAudit("admin@example.com", "Administrator", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.users.CreateWithAudit("target@example.com", "Original", "operator", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := s.login(t, admin)
	path := "/api/users/" + target.ID
	for _, body := range []string{`{"name":"Renamed"}`, `{"active":false}`} {
		req := sessionRequest(http.MethodPatch, path, strings.NewReader(body), cookie)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH %s = %d: %s", body, rec.Code, rec.Body.String())
		}
		var updated auth.User
		if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
			t.Fatal(err)
		}
		wantActive := body != `{"active":false}`
		if updated.Email != target.Email || updated.Role != target.Role || updated.Name != "Renamed" || updated.Active != wantActive {
			t.Fatalf("PATCH %s changed omitted fields or lost false: %+v", body, updated)
		}
	}
	if _, ok := classifyRoute(http.MethodPut, path); ok {
		t.Fatal("PUT still classified as a supported user update")
	}
}

func TestCreateUserWithoutSMTPReturnsLoginLinkInstruction(t *testing.T) {
	s := newTestAuthServer(t)
	RegisterUserRoutes(s.e, s.users, auth.SMTPConfig{}, config.Config{AuthMode: "local"})
	admin, _ := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	cookie := s.login(t, admin)
	req := sessionRequest(http.MethodPost, "/api/users", strings.NewReader(`{"email":"new@example.com","role":"viewer"}`), cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Email             string `json:"email"`
		InviteDelivery    string `json:"invite_delivery"`
		LoginLinkRequired bool   `json:"login_link_required"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Email != "new@example.com" || body.InviteDelivery != "not_configured" || !body.LoginLinkRequired {
		t.Fatalf("response = %+v", body)
	}
}
