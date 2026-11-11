package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/pitot/internal/auth"
	"github.com/labstack/pitot/internal/config"
)

const (
	ownershipAdminGroup    = "pitot-admins-group-name"
	ownershipOperatorGroup = "pitot-operators-group-name"
)

func roleMappedConfig() config.Config {
	cfg := scimTestConfig()
	cfg.SCIMToken = ""
	cfg.OIDCAdminGroups = ownershipAdminGroup
	return cfg
}

func directoryConfig() config.Config {
	cfg := roleMappedConfig()
	cfg.SCIMToken = testSCIMToken
	return cfg
}

// newOwnershipServer serves the browser and SCIM routes together under cfg, as
// production does.
func newOwnershipServer(t *testing.T, cfg config.Config) *scimServer {
	t.Helper()
	e, users, _, db := newOAuthTestServerAndDB(t, cfg)
	identities := auth.NewIdentityStore(db.DB)
	RegisterUserRoutes(e, users, auth.SMTPConfig{}, cfg)
	RegisterSCIMRoutes(e, cfg, users, identities)
	return &scimServer{e: e, users: users, identities: identities, db: db}
}

func createOwnershipUser(t *testing.T, s *scimServer, email string, role auth.Role) auth.User {
	t.Helper()
	user, err := s.users.CreateWithAudit(email, "", role, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("create %s: %v", email, err)
	}
	return user
}

func browserHeaders() map[string]string {
	return map[string]string{"Content-Type": "application/json", "Pitot-Request": "1"}
}

type browserUserOperation struct {
	name, method, suffix, body string
	allowedStatus              int
}

var browserUserOperations = []browserUserOperation{
	{"create", http.MethodPost, "", `{"email":"created@example.com","role":"viewer"}`, http.StatusCreated},
	{"role", http.MethodPatch, "/role", `{"role":"operator"}`, http.StatusOK},
	{"suspend", http.MethodPatch, "/status", `{"status":"suspended"}`, http.StatusOK},
	{"reactivate", http.MethodPatch, "/status", `{"status":"active"}`, http.StatusOK},
	{"email", http.MethodPatch, "", `{"email":"changed@example.com"}`, http.StatusOK},
	{"email and display name", http.MethodPatch, "", `{"email":"changed@example.com","display_name":"Changed"}`, http.StatusOK},
	{"display name", http.MethodPatch, "", `{"display_name":"Changed"}`, http.StatusOK},
	{"delete", http.MethodDelete, "", "", http.StatusNoContent},
	{"revoke access", http.MethodPost, "/access/revoke", "", http.StatusOK},
}

func (op browserUserOperation) target(id string) string {
	if op.name == "create" {
		return "/api/users"
	}
	return "/api/users/" + id + op.suffix
}

// assertUserUntouched fails unless a refused request left the target as it was.
func assertUserUntouched(t *testing.T, s *scimServer, want auth.User, wantUsers int64) {
	t.Helper()
	got, err := s.users.GetByID(want.ID)
	if err != nil {
		t.Fatalf("target after refusal: %v", err)
	}
	if got.Email != want.Email || got.DisplayName != want.DisplayName || got.Role != want.Role || got.Status != want.Status {
		t.Fatalf("target changed: got %+v, want %+v", got, want)
	}
	if count, err := s.users.CountUsers(); err != nil || count != wantUsers {
		t.Fatalf("users = %d err=%v, want %d", count, err, wantUsers)
	}
}

func TestBrowserUserRoutesFollowAccountOwnership(t *testing.T) {
	local := config.Config{AuthMode: "local", OIDCAdminGroups: ownershipAdminGroup}
	noMapping := scimTestConfig()
	noMapping.SCIMToken = ""
	blankMapping := noMapping
	blankMapping.OIDCAdminGroups, blankMapping.OIDCOperatorGroups = " , ", ","
	operatorOnly := noMapping
	operatorOnly.OIDCOperatorGroups = ownershipOperatorGroup
	withProvisioning := roleMappedConfig()
	withProvisioning.OIDCAutoProvision = true

	roleRefused := map[string]bool{"create": true, "role": true}
	directoryRefused := map[string]bool{
		"create": true, "role": true, "suspend": true, "reactivate": true,
		"email": true, "email and display name": true, "delete": true,
	}
	for _, tc := range []struct {
		name    string
		cfg     config.Config
		refused map[string]bool
	}{
		{"local mode ignores stray groups", local, nil},
		{"oidc without role mapping", noMapping, nil},
		{"oidc with blank group lists", blankMapping, nil},
		{"scim token without role mapping", scimTestConfig(), nil},
		{"admin group mapping", roleMappedConfig(), roleRefused},
		{"operator group mapping", operatorOnly, roleRefused},
		{"role mapping with provisioning", withProvisioning, roleRefused},
		{"role mapping with scim token", directoryConfig(), directoryRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, op := range browserUserOperations {
				t.Run(op.name, func(t *testing.T) {
					s := newOwnershipServer(t, tc.cfg)
					admin := createOwnershipUser(t, s, "admin@example.com", auth.RoleAdmin)
					target := createOwnershipUser(t, s, "target@example.com", auth.RoleViewer)
					cookie := oauthCookieForUser(t, s.e, admin)
					if op.name == "reactivate" {
						if _, err := s.db.DB.Exec(`UPDATE users SET status = 'suspended' WHERE id = ?`, target.ID); err != nil {
							t.Fatalf("suspend fixture: %v", err)
						}
						target.Status = auth.UserStatusSuspended
					}

					rec := serve(t, s.e, op.method, op.target(target.ID), op.body, browserHeaders(), cookie)
					if !tc.refused[op.name] {
						if rec.Code != op.allowedStatus {
							t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), op.allowedStatus)
						}
						return
					}
					if rec.Code != http.StatusForbidden {
						t.Fatalf("status = %d %s, want 403", rec.Code, rec.Body.String())
					}
					var failure struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					}
					if err := json.Unmarshal(rec.Body.Bytes(), &failure); err != nil {
						t.Fatalf("decode: %v in %s", err, rec.Body.String())
					}
					if failure.Code != "managed_by_identity_provider" || !strings.Contains(failure.Message, "identity provider") {
						t.Fatalf("refusal = %+v", failure)
					}
					for _, secret := range []string{ownershipAdminGroup, ownershipOperatorGroup, testSCIMToken} {
						if strings.Contains(rec.Body.String(), secret) {
							t.Fatalf("refusal leaks configuration: %s", rec.Body.String())
						}
					}
					assertUserUntouched(t, s, target, 2)
				})
			}
		})
	}
}

func TestOwnershipRefusalPrecedesBodyValidation(t *testing.T) {
	s := newOwnershipServer(t, directoryConfig())
	admin := createOwnershipUser(t, s, "admin@example.com", auth.RoleAdmin)
	target := createOwnershipUser(t, s, "target@example.com", auth.RoleViewer)
	cookie := oauthCookieForUser(t, s.e, admin)
	for _, tc := range []struct{ name, target, body string }{
		{"create with invalid role", "/api/users", `{"email":"a@example.com","role":"owner"}`},
		{"role with invalid value", "/api/users/" + target.ID + "/role", `{"role":"owner"}`},
		{"status with invalid value", "/api/users/" + target.ID + "/status", `{"status":"gone"}`},
		{"email that does not parse", "/api/users/" + target.ID, `{"email":"not an address"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method := http.MethodPatch
			if tc.target == "/api/users" {
				method = http.MethodPost
			}
			if rec := serve(t, s.e, method, tc.target, tc.body, browserHeaders(), cookie); rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d %s, want 403", rec.Code, rec.Body.String())
			}
		})
	}
}

// A non-administrator is stopped by the capability check and learns nothing
// about how accounts are owned.
func TestOwnershipRefusalDoesNotReachNonAdministrators(t *testing.T) {
	s := newOwnershipServer(t, directoryConfig())
	member := createOwnershipUser(t, s, "member@example.com", auth.RoleOperator)
	rec := serve(t, s.e, http.MethodPost, "/api/users", `{"email":"a@example.com"}`, browserHeaders(), oauthCookieForUser(t, s.e, member))
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "managed_by_identity_provider") {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
}

func TestSCIMManagesAccountsWhileBrowserRoutesRefuse(t *testing.T) {
	s := newOwnershipServer(t, directoryConfig())
	admin := createOwnershipUser(t, s, "admin@example.com", auth.RoleAdmin)
	cookie := oauthCookieForUser(t, s.e, admin)

	created := s.seed(t, "subject-1", "person@example.com", "viewer")
	path := scimUsersPath + "/" + created.ID
	apply := func(operations string) scimUserResource {
		t.Helper()
		rec := s.do(t, http.MethodPatch, path, scimPatchBody(operations))
		if rec.Code != http.StatusOK {
			t.Fatalf("patch %s = %d %s", operations, rec.Code, rec.Body.String())
		}
		return decodeSCIMResource(t, rec)
	}
	apply(`{"op":"replace","path":"roles","value":[{"value":"operator","primary":true}]}`)
	apply(`{"op":"replace","path":"active","value":false}`)
	apply(`{"op":"replace","path":"active","value":true}`)
	apply(`{"op":"replace","path":"userName","value":"renamed@example.com"}`)

	user, err := s.users.GetByID(created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if user.Role != auth.RoleOperator || user.Status != auth.UserStatusActive || user.Email != "renamed@example.com" {
		t.Fatalf("user after SCIM updates = %+v", user)
	}
	if rec := serve(t, s.e, http.MethodPatch, "/api/users/"+created.ID+"/role", `{"role":"admin"}`, browserHeaders(), cookie); rec.Code != http.StatusForbidden {
		t.Fatalf("browser role change = %d %s, want 403", rec.Code, rec.Body.String())
	}
}

func TestLoginManagesAccountsWhileBrowserRoutesRefuse(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config.Config
	}{
		{"role mapping", roleMappedConfig()},
		{"role mapping with scim token", directoryConfig()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.OIDCEmailVerification = "required"
			cfg.OIDCDefaultRole = "viewer"
			cfg.OIDCAutoProvision = true
			cfg.OIDCAllowedDomains = "example.com"
			handler, users, user := linkedOIDCFixture(t, cfg, auth.RoleViewer, nil)

			verified := true
			promoted := oidcClaims{Subject: "subject-1", Email: user.Email, EmailVerified: &verified, Groups: []string{ownershipAdminGroup}}
			got, _, err := handler.resolveUser(t.Context(), testSCIMIssuer, promoted, requestSource{})
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if got.Role != auth.RoleAdmin {
				t.Fatalf("role after login = %q, want admin", got.Role)
			}

			newcomer := oidcClaims{Subject: "subject-2", Email: "newcomer@example.com", EmailVerified: &verified}
			created, _, err := handler.resolveUser(t.Context(), testSCIMIssuer, newcomer, requestSource{})
			if err != nil {
				t.Fatalf("provision at login: %v", err)
			}
			if stored, err := users.GetByID(created.ID); err != nil || stored.Email != "newcomer@example.com" {
				t.Fatalf("provisioned user = %+v err=%v", stored, err)
			}
		})
	}
}

func TestAuthStatusPublishesAccountOwnership(t *testing.T) {
	noMapping := scimTestConfig()
	noMapping.SCIMToken = ""
	operatorOnly := roleMappedConfig()
	operatorOnly.OIDCAdminGroups, operatorOnly.OIDCOperatorGroups = "", ownershipOperatorGroup
	provisioning := roleMappedConfig()
	provisioning.OIDCAutoProvision = true
	for _, tc := range []struct {
		name string
		cfg  config.Config
		want string
	}{
		{"local", config.Config{AuthMode: "local", OIDCAdminGroups: ownershipAdminGroup}, "local"},
		{"oidc without role mapping", noMapping, "local"},
		{"scim token without role mapping", scimTestConfig(), "local"},
		{"admin group mapping without provisioning", roleMappedConfig(), "identity_provider_roles_no_provisioning"},
		{"operator group mapping without provisioning", operatorOnly, "identity_provider_roles_no_provisioning"},
		{"role mapping with provisioning", provisioning, "identity_provider_roles"},
		{"role mapping with scim token", directoryConfig(), "identity_provider_directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestAuthServerWith(t, tc.cfg, auth.SMTPConfig{})
			rec := httptest.NewRecorder()
			s.e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/status", nil))
			var status struct {
				AccountOwnership string `json:"account_ownership"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil || status.AccountOwnership != tc.want {
				t.Fatalf("status = %s err=%v, want account_ownership %q", rec.Body.String(), err, tc.want)
			}
			for _, secret := range []string{ownershipAdminGroup, ownershipOperatorGroup, testSCIMToken} {
				if strings.Contains(rec.Body.String(), secret) {
					t.Fatalf("status leaks configuration: %s", rec.Body.String())
				}
			}
		})
	}
}

func TestEmptyDatabaseWordingMatchesHowTheFirstAdminArrives(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config.Config
		want string
	}{
		{"browser setup", config.Config{AuthMode: "oidc", OIDCIssuerURL: testSCIMIssuer}, "setup required"},
		{"scim token", scimTestConfig(), "no accounts have been provisioned yet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _, _ := newOAuthTestServerAndDB(t, tc.cfg)
			rec := serve(t, e, http.MethodGet, "/api/users", "", nil)
			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("response = %d %s, want 401 with %q", rec.Code, rec.Body.String(), tc.want)
			}
		})
	}
}

func TestOIDCEmptyDatabaseDenialMatchesHowTheFirstAdminArrives(t *testing.T) {
	for _, tc := range []struct {
		name  string
		token string
		want  string
	}{
		{"browser setup", "", "first administrator setup is required"},
		{"scim token", testSCIMToken, "provisioned through SCIM"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newOwnershipServer(t, scimTestConfig())
			handler := &OIDCHandler{
				cfg:   config.Config{OIDCEmailVerification: "required", OIDCAutoProvision: true, OIDCAllowedDomains: "example.com", OIDCDefaultRole: "viewer", SCIMToken: tc.token},
				users: s.users, identities: s.identities, audit: auth.NewAuditStore(s.db.DB),
			}
			verified := true
			claims := oidcClaims{Subject: "subject-1", Email: "first@example.com", EmailVerified: &verified}
			_, _, err := handler.resolveUser(t.Context(), testSCIMIssuer, claims, requestSource{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestBreakGlassRedemptionWhenIdentityProviderOwnsAccounts(t *testing.T) {
	cfg := directoryConfig()
	cfg.AuthCodeSecret = breakGlassTestSecret
	s := newTestAuthServerWith(t, cfg, auth.SMTPConfig{})
	user, err := s.users.CreateWithAudit("admin@example.com", "", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, err := s.codes.CreateBreakGlassLoginLink(user.Email)
	if err != nil {
		t.Fatalf("CreateBreakGlassLoginLink: %v", err)
	}
	if rec := s.redeemLink(token); rec.Code != http.StatusOK || !hasSessionCookie(rec) {
		t.Fatalf("redemption = %d %s", rec.Code, rec.Body.String())
	}
}
