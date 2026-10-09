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
	for _, tc := range []struct{ name, method, path, body string }{
		{"demote", http.MethodPatch, "/role", `{"role":"viewer"}`},
		{"suspend", http.MethodPatch, "/status", `{"status":"suspended"}`},
		{"delete", http.MethodDelete, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestAuthServer(t)
			registerTestUserRoutes(s)
			admin, _ := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
			other, err := s.users.CreateWithAudit("suspended-admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
			if err != nil {
				t.Fatal(err)
			}
			status := auth.UserStatusSuspended
			if _, err := s.users.UpdateWithAudit(other.ID, nil, nil, nil, &status, auth.AuditEvent{EventType: "user.suspended", Outcome: "success"}); err != nil {
				t.Fatal(err)
			}
			cookie := s.login(t, admin)
			var body *strings.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := sessionRequest(tc.method, "/api/users/"+admin.ID+tc.path, body, cookie)
			if body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			s.e.ServeHTTP(rec, req)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"last_active_administrator"`) {
				t.Fatalf("status = %d, want 409", rec.Code)
			}
		})
	}
}

func TestUserSecurityRoutesAllowChangesWithAnotherActiveAdmin(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/role", `{"role":"viewer"}`},
		{"/status", `{"status":"suspended"}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			s := newTestAuthServer(t)
			registerTestUserRoutes(s)
			admin, err := s.users.CreateWithAudit("admin@example.com", "Admin", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
			if err != nil {
				t.Fatal(err)
			}
			target, err := s.users.CreateWithAudit("target@example.com", "Target", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
			if err != nil {
				t.Fatal(err)
			}
			cookie := s.login(t, admin)
			targetCookie := s.login(t, target)
			rec := userRequest(t, s, http.MethodPatch, "/api/users/"+target.ID+tc.path, tc.body, cookie)
			if rec.Code != http.StatusOK {
				t.Fatalf("security change = %d: %s", rec.Code, rec.Body.String())
			}
			updated, err := s.users.GetByID(target.ID)
			if err != nil || updated.AuthVersion != target.AuthVersion+1 {
				t.Fatalf("security change = %+v %v", updated, err)
			}
			me := httptest.NewRecorder()
			s.e.ServeHTTP(me, sessionRequest(http.MethodGet, "/api/auth/me", nil, targetCookie))
			if me.Code != http.StatusUnauthorized {
				t.Fatalf("revoked session = %d", me.Code)
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

func TestPatchUserPreservesOmittedProfileFields(t *testing.T) {
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
	for _, body := range []string{`{"display_name":"Renamed"}`, `{}`} {
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
		if updated.Email != target.Email || updated.Role != target.Role || updated.DisplayName != "Renamed" || updated.Status != auth.UserStatusActive {
			t.Fatalf("PATCH %s changed omitted fields: %+v", body, updated)
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

func userRequest(t *testing.T, s *testAuthServer, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := sessionRequest(method, path, strings.NewReader(body), cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	return rec
}

func TestUserPatchRoutes(t *testing.T) {
	s := newTestAuthServer(t)
	registerTestUserRoutes(s)
	admin, err := s.users.CreateWithAudit("admin@example.com", "Admin", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.users.CreateWithAudit("target@example.com", "Original", auth.RoleOperator, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := s.users.CreateWithAudit("viewer@example.com", "Viewer", auth.RoleViewer, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	adminCookie := s.login(t, admin)
	targetCookie := s.login(t, target)
	viewerCookie := s.login(t, viewer)
	for _, tc := range []struct{ path, body string }{
		{"", `{"email":"changed@example.com","display_name":"Changed"}`},
		{"/role", `{"role":"viewer"}`},
		{"/status", `{"status":"suspended"}`},
		{"/status", `{"status":"active"}`},
	} {
		rec := userRequest(t, s, http.MethodPatch, "/api/users/"+target.ID+tc.path, tc.body, adminCookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH %s %s = %d: %s", tc.path, tc.body, rec.Code, rec.Body.String())
		}
	}
	updated, err := s.users.GetByID(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "Changed" || updated.Email != "changed@example.com" || updated.Role != auth.RoleViewer || updated.Status != auth.UserStatusActive || updated.AuthVersion != target.AuthVersion+4 {
		t.Fatalf("updated user = %+v", updated)
	}
	// Reactivation must not restore a session revoked by a security change.
	me := httptest.NewRecorder()
	s.e.ServeHTTP(me, sessionRequest(http.MethodGet, "/api/auth/me", nil, targetCookie))
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session = %d", me.Code)
	}
	for _, tc := range []struct{ path, body string }{
		{"", `{"display_name":"Denied"}`},
		{"/role", `{"role":"admin"}`},
		{"/status", `{"status":"suspended"}`},
	} {
		for _, cookie := range []*http.Cookie{viewerCookie, nil} {
			want := http.StatusForbidden
			if cookie == nil {
				want = http.StatusUnauthorized
			}
			rec := userRequest(t, s, http.MethodPatch, "/api/users/"+target.ID+tc.path, tc.body, cookie)
			if rec.Code != want {
				t.Fatalf("non-admin PATCH %s = %d, want %d", tc.path, rec.Code, want)
			}
		}
		rec := userRequest(t, s, http.MethodPatch, "/api/users/missing"+tc.path, tc.body, adminCookie)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("missing PATCH %s = %d", tc.path, rec.Code)
		}
	}
}

func TestUserPatchRoutesRejectWrongFields(t *testing.T) {
	s := newTestAuthServer(t)
	registerTestUserRoutes(s)
	admin, err := s.users.CreateWithAudit("admin@example.com", "Admin", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := s.login(t, admin)
	for _, tc := range []struct{ path, body string }{
		{"", `{"role":"viewer"}`}, {"", `{"status":"suspended"}`},
		{"", `{"active":false}`}, {"", `{"name":"Old"}`},
		{"", `{"display_name":"Changed","unexpected":true}`},
		{"", `{"email":"invalid"}`},
		{"", `{"display_name":12}`}, {"", `{} {}`}, {"", `[]`},
		{"/role", `{"role":"viewer","display_name":"Changed"}`},
		{"/role", `{"role":"viewer","status":"active"}`},
		{"/role", `{"role":"invalid"}`}, {"/role", `{}`}, {"/role", `{"role":null}`},
		{"/status", `{"status":"suspended","role":"viewer"}`},
		{"/status", `{"status":"active","email":"other@example.com"}`},
		{"/status", `{"status":"disabled"}`}, {"/status", `{"status":"ACTIVE"}`},
		{"/status", `{"status":false}`}, {"/status", `{}`}, {"/status", `{"status":null}`},
	} {
		rec := userRequest(t, s, http.MethodPatch, "/api/users/"+admin.ID+tc.path, tc.body, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("PATCH %s %s = %d: %s", tc.path, tc.body, rec.Code, rec.Body.String())
		}
	}
	unchanged, err := s.users.GetByID(admin.ID)
	if err != nil || unchanged.Email != admin.Email || unchanged.DisplayName != admin.DisplayName || unchanged.Role != admin.Role || unchanged.Status != admin.Status || unchanged.AuthVersion != admin.AuthVersion {
		t.Fatalf("invalid requests changed user: %+v %v", unchanged, err)
	}
}

func TestUserProjectionsAndProvisioning(t *testing.T) {
	s := newTestAuthServer(t)
	registerTestUserRoutes(s)
	admin, err := s.users.CreateWithAudit("admin@example.com", "Admin", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := s.login(t, admin)
	rec := userRequest(t, s, http.MethodPost, "/api/users", `{"email":"new@example.com","display_name":"New User","role":"operator"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("provision = %d: %s", rec.Code, rec.Body.String())
	}
	assertUserProjection := func(body []byte) {
		t.Helper()
		var projection map[string]any
		if err := json.Unmarshal(body, &projection); err != nil {
			t.Fatal(err)
		}
		if _, ok := projection["display_name"].(string); !ok {
			t.Fatalf("missing display_name: %s", body)
		}
		if projection["status"] != "active" {
			t.Fatalf("missing status: %s", body)
		}
		for _, key := range []string{"name", "active"} {
			if _, ok := projection[key]; ok {
				t.Fatalf("superseded %s in %s", key, body)
			}
		}
	}
	assertUserProjection(rec.Body.Bytes())
	created, err := s.users.GetByEmail("new@example.com")
	if err != nil || created.DisplayName != "New User" || created.Role != auth.RoleOperator {
		t.Fatalf("created = %+v %v", created, err)
	}
	me := httptest.NewRecorder()
	s.e.ServeHTTP(me, sessionRequest(http.MethodGet, "/api/auth/me", nil, cookie))
	if me.Code != http.StatusOK {
		t.Fatalf("me = %d", me.Code)
	}
	assertUserProjection(me.Body.Bytes())
	list := httptest.NewRecorder()
	s.e.ServeHTTP(list, sessionRequest(http.MethodGet, "/api/users", nil, cookie))
	if list.Code != http.StatusOK {
		t.Fatalf("list = %d", list.Code)
	}
	var users []json.RawMessage
	if err := json.Unmarshal(list.Body.Bytes(), &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("users = %s", list.Body.String())
	}
	for _, user := range users {
		assertUserProjection(user)
	}
	for _, body := range []string{
		`{"email":"rejected@example.com","name":"Old"}`,
		`{"email":"rejected@example.com","active":true}`,
		`{"email":"rejected@example.com","status":"active"}`,
	} {
		if rec := userRequest(t, s, http.MethodPost, "/api/users", body, cookie); rec.Code != http.StatusBadRequest {
			t.Fatalf("provision %s = %d", body, rec.Code)
		}
	}
}

func TestStatusRouteSuspensionBlocksSessionsAndSignIn(t *testing.T) {
	s := newTestAuthServer(t)
	registerTestUserRoutes(s)
	admin, err := s.users.CreateWithAudit("admin@example.com", "Admin", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.users.CreateWithAudit("target@example.com", "Target", auth.RoleOperator, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	adminCookie := s.login(t, admin)
	targetCookie := s.login(t, target)
	code, err := s.codes.Create(target.Email)
	if err != nil {
		t.Fatal(err)
	}
	link, err := s.codes.CreateLoginLink(target.Email)
	if err != nil {
		t.Fatal(err)
	}
	rec := userRequest(t, s, http.MethodPatch, "/api/users/"+target.ID+"/status", `{"status":"suspended"}`, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("suspend = %d: %s", rec.Code, rec.Body.String())
	}
	updated, err := s.users.GetByID(target.ID)
	if err != nil || updated.Status != auth.UserStatusSuspended || updated.AuthVersion != target.AuthVersion+1 {
		t.Fatalf("suspended = %+v %v", updated, err)
	}
	me := httptest.NewRecorder()
	s.e.ServeHTTP(me, sessionRequest(http.MethodGet, "/api/auth/me", nil, targetCookie))
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("suspended session = %d", me.Code)
	}
	for _, tc := range []struct{ path, body string }{
		{"/api/auth/code/verify", `{"email":"` + target.Email + `","code":"` + code + `"}`},
		{"/api/auth/link/verify", `{"token":"` + link + `"}`},
	} {
		rec := userRequest(t, s, http.MethodPost, tc.path, tc.body, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("suspended sign-in %s = %d: %s", tc.path, rec.Code, rec.Body.String())
		}
		for _, cookie := range rec.Result().Cookies() {
			if cookie.Name == "fanout_session" && cookie.Value != "" {
				t.Fatal("suspended sign-in issued a session")
			}
		}
	}
	var audits int
	if err := s.db.DB.QueryRow(`SELECT COUNT(*) FROM auth_audit_events WHERE event_type = 'user.suspended' AND target_id = ?`, target.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("suspension audits = %d, %v", audits, err)
	}
}
