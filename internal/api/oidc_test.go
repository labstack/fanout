package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v5"
	appauth "github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
	appstore "github.com/labstack/fanout/internal/store"
)

func TestOIDCProvisionPolicy(t *testing.T) {
	h := &OIDCHandler{cfg: config.Config{
		OIDCAllowedGroups:  "fanout-users, sre",
		OIDCAllowedDomains: "example.com",
		OIDCDefaultRole:    "viewer",
		OIDCOperatorGroups: "operators",
		OIDCAdminGroups:    "fanout-admins",
	}}
	if !h.provisionAllowed("person@other.test", []string{"SRE"}) {
		t.Fatal("allowed group was rejected")
	}
	if !h.provisionAllowed("person@example.com", nil) {
		t.Fatal("allowed domain was rejected")
	}
	if h.provisionAllowed("person@other.test", []string{"unrelated"}) {
		t.Fatal("unknown identity was provisioned")
	}
	if role := h.provisionRole([]string{"fanout-admins"}); role != "admin" {
		t.Fatalf("admin group role = %q", role)
	}
	if role := h.provisionRole([]string{"operators"}); role != "operator" {
		t.Fatalf("operator group role = %q", role)
	}
}

func TestOIDCWildcardDomainAllowsVerifiedViewerProvisioning(t *testing.T) {
	db, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	users := appauth.NewUserStore(db.DB)
	if _, err := users.Create("seed-admin@example.com", "", "admin"); err != nil {
		t.Fatal(err)
	}
	handler := &OIDCHandler{
		cfg: config.Config{
			OIDCEmailVerification: "required",
			OIDCAutoProvision:     true,
			OIDCAllowedDomains:    "*",
			OIDCDefaultRole:       "viewer",
		},
		users: users, identities: appauth.NewIdentityStore(db.DB), audit: appauth.NewAuditStore(db.DB),
	}
	if handler.provisionAllowed("not-an-email", nil) {
		t.Fatal("wildcard domain accepted an unusable email")
	}
	handler.cfg.OIDCEmailVerification = "issuer"
	if handler.provisionAllowed("visitor@anywhere.example", nil) {
		t.Fatal("wildcard domain accepted an issuer-trusted email")
	}
	handler.cfg.OIDCEmailVerification = "required"
	verified := true
	claims := oidcClaims{Subject: "public-visitor", Email: "visitor@anywhere.example", EmailVerified: &verified}
	user, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{})
	if err != nil {
		t.Fatalf("verified visitor was not provisioned: %v", err)
	}
	if user.Role != appauth.RoleViewer {
		t.Fatalf("provisioned role = %q, want viewer", user.Role)
	}
	claims.EmailVerified = new(bool)
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{}); err == nil {
		t.Fatal("linked visitor authenticated after its email became unverified")
	}
}

func TestOIDCWildcardDomainRejectsUnverifiedEmail(t *testing.T) {
	db, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	users := appauth.NewUserStore(db.DB)
	if _, err := users.Create("seed-admin@example.com", "", "admin"); err != nil {
		t.Fatal(err)
	}
	handler := &OIDCHandler{
		cfg: config.Config{
			OIDCEmailVerification: "required",
			OIDCAutoProvision:     true,
			OIDCAllowedDomains:    "*",
			OIDCDefaultRole:       "viewer",
		},
		users: users, identities: appauth.NewIdentityStore(db.DB), audit: appauth.NewAuditStore(db.DB),
	}
	verified := false
	claims := oidcClaims{Subject: "unverified-visitor", Email: "visitor@anywhere.example", EmailVerified: &verified}
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{}); err == nil {
		t.Fatal("wildcard policy provisioned an unverified email")
	}
	if _, err := users.GetByEmail("visitor@anywhere.example"); err == nil {
		t.Fatal("denied visitor left a persisted user behind")
	}
}

func TestSafeReturnTo(t *testing.T) {
	for _, accepted := range []string{"/", "/dashboards", "/api/auth/oauth/authorize?client_id=x"} {
		if got := safeReturnTo(accepted); got != accepted {
			t.Errorf("safeReturnTo(%q) = %q", accepted, got)
		}
	}
	for _, rejected := range []string{"", "https://evil.test/", "//evil.test/", "dashboard"} {
		if got := safeReturnTo(rejected); got != "" {
			t.Errorf("safeReturnTo(%q) = %q, want empty", rejected, got)
		}
	}
}

func TestOIDCFlowValidatesPKCENonceAndCreatesSession(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	var issuer, issuedNonce, expectedChallenge string
	var pkceVerified bool
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token",
				"jwks_uri": issuer + "/keys", "response_types_supported": []string{"code"},
				"subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"},
				"token_endpoint_auth_methods_supported": []string{"client_secret_basic"},
			})
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
				"kty": "RSA", "kid": "test-key", "use": "sig", "alg": "RS256",
				"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
			}}})
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse token form: %v", err)
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			verifier := r.Form.Get("code_verifier")
			digest := sha256.Sum256([]byte(verifier))
			pkceVerified = verifier != "" && base64.RawURLEncoding.EncodeToString(digest[:]) == expectedChallenge
			claims := jwt.MapClaims{
				"iss": issuer, "sub": "subject-1", "aud": "fanout-client",
				"exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Add(-time.Second).Unix(),
				"nonce": issuedNonce, "email": "admin@example.com", "email_verified": true,
			}
			idToken := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			idToken.Header["kid"] = "test-key"
			signed, signErr := idToken.SignedString(key)
			if signErr != nil {
				t.Errorf("sign ID token: %v", signErr)
				http.Error(w, "signing failed", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "upstream-access", "token_type": "Bearer", "expires_in": 60, "id_token": signed,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer idp.Close()
	issuer = idp.URL

	db, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer db.Close()
	users := appauth.NewUserStore(db.DB)
	user, err := users.Create("admin@example.com", "", "admin")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	identities := appauth.NewIdentityStore(db.DB)
	sessions := appauth.NewBrowserSessions(db.DB, 12*time.Hour, 7*24*time.Hour, false)
	e := echo.New()
	e.Use(echo.WrapMiddleware(sessions.Middleware))
	cfg := config.Config{
		AuthMode: "oidc", OIDCIssuerURL: issuer, OIDCClientID: "fanout-client", OIDCClientSecret: "client-secret",
		PublicURL: "https://fanout.example.com", OIDCEmailVerification: "required", OIDCDefaultRole: "viewer",
	}
	if err := RegisterOIDCRoutes(context.Background(), e, cfg, users, identities, sessions, appauth.NewAuditStore(db.DB)); err != nil {
		t.Fatalf("RegisterOIDCRoutes: %v", err)
	}

	startRecorder := httptest.NewRecorder()
	e.ServeHTTP(startRecorder, httptest.NewRequest(http.MethodGet, "/api/auth/oidc/start?return_to=/dashboards", nil))
	if startRecorder.Code != http.StatusFound {
		t.Fatalf("OIDC start status = %d, want 302: %s", startRecorder.Code, startRecorder.Body.String())
	}
	authorizeURL, err := url.Parse(startRecorder.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse authorization redirect: %v", err)
	}
	state := authorizeURL.Query().Get("state")
	issuedNonce = authorizeURL.Query().Get("nonce")
	expectedChallenge = authorizeURL.Query().Get("code_challenge")
	if state == "" || issuedNonce == "" || expectedChallenge == "" || authorizeURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("incomplete authorization redirect: %s", authorizeURL.String())
	}
	cookies := startRecorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("pre-auth cookies = %d, want 1", len(cookies))
	}

	callback := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=authorization-code&state="+url.QueryEscape(state), nil)
	callback.AddCookie(cookies[0])
	callbackRecorder := httptest.NewRecorder()
	e.ServeHTTP(callbackRecorder, callback)
	if callbackRecorder.Code != http.StatusFound || callbackRecorder.Header().Get("Location") != "/dashboards" {
		t.Fatalf("OIDC callback = %d location %q: %s", callbackRecorder.Code, callbackRecorder.Header().Get("Location"), callbackRecorder.Body.String())
	}
	if !pkceVerified {
		t.Fatal("token exchange did not present the PKCE verifier from the pre-auth session")
	}
	identity, err := identities.Find(t.Context(), issuer, "subject-1")
	if err != nil || identity.UserID != user.ID {
		t.Fatalf("linked identity = %+v, err %v", identity, err)
	}
	var sessionCount int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, user.ID).Scan(&sessionCount); err != nil {
		t.Fatalf("count authenticated sessions: %v", err)
	}
	if sessionCount != 1 {
		t.Fatalf("authenticated sessions = %d, want 1", sessionCount)
	}
}

func TestOIDCCallbackRejectsInvalidIdentityTokensAndProvisioning(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(jwt.MapClaims)
		badKey     bool
		knownEmail bool
	}{
		{name: "nonce mismatch", knownEmail: true, mutate: func(c jwt.MapClaims) { c["nonce"] = "wrong" }},
		{name: "wrong audience", knownEmail: true, mutate: func(c jwt.MapClaims) { c["aud"] = "other-client" }},
		{name: "expired", knownEmail: true, mutate: func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Hour).Unix() }},
		{name: "bad signature", knownEmail: true, badKey: true},
		{name: "email unverified", knownEmail: true, mutate: func(c jwt.MapClaims) { c["email_verified"] = false }},
		{name: "email verification absent", knownEmail: true, mutate: func(c jwt.MapClaims) { delete(c, "email_verified") }},
		{name: "auto provision disabled", knownEmail: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runRejectedOIDCCallback(t, tc.mutate, tc.badKey, tc.knownEmail); got != http.StatusUnauthorized {
				t.Fatalf("callback status = %d, want 401", got)
			}
		})
	}
}

func runRejectedOIDCCallback(t *testing.T, mutate func(jwt.MapClaims), badKey, knownEmail bool) int {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer, issuedNonce string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token",
				"jwks_uri": issuer + "/keys", "response_types_supported": []string{"code"},
				"subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"},
				"token_endpoint_auth_methods_supported": []string{"client_secret_basic"},
			})
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
				"kty": "RSA", "kid": "test-key", "use": "sig", "alg": "RS256",
				"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
			}}})
		case "/token":
			claims := jwt.MapClaims{
				"iss": issuer, "sub": "rejected-subject", "aud": "fanout-client",
				"exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Add(-time.Second).Unix(),
				"nonce": issuedNonce, "email": "candidate@example.com", "email_verified": true,
			}
			if mutate != nil {
				mutate(claims)
			}
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			token.Header["kid"] = "test-key"
			signingKey := key
			if badKey {
				signingKey = wrongKey
			}
			signed, signErr := token.SignedString(signingKey)
			if signErr != nil {
				t.Error(signErr)
				http.Error(w, "signing failed", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "upstream-access", "token_type": "Bearer", "expires_in": 60, "id_token": signed,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer idp.Close()
	issuer = idp.URL

	db, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	users := appauth.NewUserStore(db.DB)
	seedEmail := "admin@example.com"
	if knownEmail {
		seedEmail = "candidate@example.com"
	}
	if _, err := users.Create(seedEmail, "", "admin"); err != nil {
		t.Fatal(err)
	}
	sessions := appauth.NewBrowserSessions(db.DB, 12*time.Hour, 7*24*time.Hour, false)
	e := echo.New()
	e.Use(echo.WrapMiddleware(sessions.Middleware))
	cfg := config.Config{
		AuthMode: "oidc", OIDCIssuerURL: issuer, OIDCClientID: "fanout-client", OIDCClientSecret: "client-secret",
		PublicURL: "https://fanout.example.com", OIDCEmailVerification: "required", OIDCDefaultRole: "viewer",
		OIDCAutoProvision: false,
	}
	if err := RegisterOIDCRoutes(context.Background(), e, cfg, users, appauth.NewIdentityStore(db.DB), sessions, appauth.NewAuditStore(db.DB)); err != nil {
		t.Fatal(err)
	}
	start := httptest.NewRecorder()
	e.ServeHTTP(start, httptest.NewRequest(http.MethodGet, "/api/auth/oidc/start", nil))
	if start.Code != http.StatusFound {
		t.Fatalf("start = %d: %s", start.Code, start.Body.String())
	}
	location, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	issuedNonce = location.Query().Get("nonce")
	callback := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=code&state="+url.QueryEscape(location.Query().Get("state")), nil)
	callback.AddCookie(start.Result().Cookies()[0])
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, callback)
	return recorder.Code
}

func TestConfiguredOIDCEmailRejectsFallbackValues(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]json.RawMessage
		want string
		ok   bool
	}{
		{name: "string", raw: map[string]json.RawMessage{"trusted_email": json.RawMessage(`"trusted@example.com"`)}, want: "trusted@example.com", ok: true},
		{name: "missing", raw: map[string]json.RawMessage{"email": json.RawMessage(`"attacker@example.com"`)}},
		{name: "array", raw: map[string]json.RawMessage{"trusted_email": json.RawMessage(`["attacker@example.com"]`)}},
		{name: "null", raw: map[string]json.RawMessage{"trusted_email": json.RawMessage(`null`)}},
		{name: "empty", raw: map[string]json.RawMessage{"trusted_email": json.RawMessage(`""`)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := configuredOIDCEmail(tc.raw, "trusted_email")
			if tc.ok && (err != nil || got != tc.want) {
				t.Fatalf("configuredOIDCEmail = %q, %v", got, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("configuredOIDCEmail accepted %q", got)
			}
		})
	}
}

func TestResolveUserRequiresActiveUnlinkedIssuerAllowedUser(t *testing.T) {
	db, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	users := appauth.NewUserStore(db.DB)
	identities := appauth.NewIdentityStore(db.DB)
	user, err := users.Create("existing@example.com", "", "admin")
	if err != nil {
		t.Fatal(err)
	}
	handler := &OIDCHandler{
		cfg:   config.Config{OIDCEmailVerification: "issuer", OIDCAllowedGroups: "trusted", OIDCDefaultRole: "viewer"},
		users: users, identities: identities, audit: appauth.NewAuditStore(db.DB),
	}
	verified := true
	claims := oidcClaims{Subject: "subject-1", Email: user.Email, EmailVerified: &verified, Groups: []string{"untrusted"}}
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{}); err == nil {
		t.Fatal("issuer-mode existing user bypassed allow policy")
	}
	if count, err := identities.CountForUser(t.Context(), user.ID); err != nil || count != 0 {
		t.Fatalf("denied identity links = %d, err %v", count, err)
	}

	claims.Groups = []string{"trusted"}
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{}); err != nil {
		t.Fatalf("allowed existing user: %v", err)
	}
	claims.Subject = "subject-2"
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{}); err == nil {
		t.Fatal("second identity linked to an already-linked user")
	}

	inactive, err := users.Create("inactive-oidc@example.com", "", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	active := false
	if _, err := users.Update(inactive.ID, nil, nil, nil, &active); err != nil {
		t.Fatal(err)
	}
	inactiveClaims := oidcClaims{Subject: "inactive-subject", Email: inactive.Email, EmailVerified: &verified, Groups: []string{"trusted"}}
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", inactiveClaims, requestSource{}); err == nil {
		t.Fatal("inactive user was linked")
	}
}

// linkedOIDCFixture creates a user with one linked identity and returns the
// handler, stores, and user so reconciliation tests can vary later logins.
func linkedOIDCFixture(t *testing.T, cfg config.Config, role appauth.Role, linkGroups []string) (*OIDCHandler, *appauth.UserStore, appauth.User) {
	t.Helper()
	db, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	users := appauth.NewUserStore(db.DB)
	identities := appauth.NewIdentityStore(db.DB)
	user, err := users.Create("person@example.com", "", role)
	if err != nil {
		t.Fatal(err)
	}
	handler := &OIDCHandler{cfg: cfg, users: users, identities: identities, audit: appauth.NewAuditStore(db.DB)}
	verified := true
	claims := oidcClaims{Subject: "subject-1", Email: user.Email, EmailVerified: &verified, Groups: linkGroups}
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{}); err != nil {
		t.Fatalf("link identity: %v", err)
	}
	return handler, users, user
}

func TestResolveUserDeniesLinkedIdentityRemovedFromAllowedGroups(t *testing.T) {
	cfg := config.Config{OIDCEmailVerification: "required", OIDCAllowedGroups: "trusted", OIDCDefaultRole: "viewer"}
	handler, _, user := linkedOIDCFixture(t, cfg, "viewer", []string{"trusted"})

	verified := true
	claims := oidcClaims{Subject: "subject-1", Email: user.Email, EmailVerified: &verified, Groups: []string{"unrelated"}}
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{}); err == nil {
		t.Fatal("linked identity removed from the allowed group was still admitted")
	}
}

func TestResolveUserPreservesLinkedIdentityWhenNoAllowPolicyConfigured(t *testing.T) {
	cfg := config.Config{OIDCEmailVerification: "required", OIDCDefaultRole: "viewer"}
	handler, _, user := linkedOIDCFixture(t, cfg, "viewer", nil)

	verified := true
	claims := oidcClaims{Subject: "subject-1", Email: user.Email, EmailVerified: &verified, Groups: []string{"anything"}}
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{}); err != nil {
		t.Fatalf("locally managed user denied by an unconfigured policy: %v", err)
	}
}

func TestResolveUserDowngradesRoleWhenIdPAdminGroupRemoved(t *testing.T) {
	cfg := config.Config{
		OIDCEmailVerification: "required",
		OIDCAllowedGroups:     "trusted",
		OIDCAdminGroups:       "fanout-admins",
		OIDCDefaultRole:       "viewer",
	}
	handler, users, user := linkedOIDCFixture(t, cfg, "admin", []string{"trusted", "fanout-admins"})
	// A second admin keeps the last-active-admin invariant from blocking the downgrade.
	if _, err := users.Create("other-admin@example.com", "", "admin"); err != nil {
		t.Fatal(err)
	}

	verified := true
	claims := oidcClaims{Subject: "subject-1", Email: user.Email, EmailVerified: &verified, Groups: []string{"trusted"}}
	got, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{})
	if err != nil {
		t.Fatalf("resolve after admin group removal: %v", err)
	}
	if got.Role != appauth.RoleViewer {
		t.Fatalf("role after admin group removal = %q, want viewer", got.Role)
	}
	stored, err := users.GetByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Role != appauth.RoleViewer {
		t.Fatalf("persisted role = %q, want viewer", stored.Role)
	}
	if stored.AuthVersion <= user.AuthVersion {
		t.Fatalf("auth version = %d, want greater than %d so existing sessions are revoked", stored.AuthVersion, user.AuthVersion)
	}
}

func TestResolveUserDeniesGroupOverageWhenGroupPolicyConfigured(t *testing.T) {
	cfg := config.Config{
		OIDCEmailVerification: "required",
		OIDCAllowedGroups:     "trusted",
		OIDCAdminGroups:       "fanout-admins",
		OIDCDefaultRole:       "viewer",
	}
	handler, users, user := linkedOIDCFixture(t, cfg, "admin", []string{"trusted", "fanout-admins"})
	if _, err := users.Create("other-admin@example.com", "", "admin"); err != nil {
		t.Fatal(err)
	}

	verified := true
	claims := oidcClaims{
		Subject:       "subject-1",
		Email:         user.Email,
		EmailVerified: &verified,
		ClaimNames:    map[string]json.RawMessage{"groups": json.RawMessage(`"src1"`)},
	}
	_, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{})
	if err == nil {
		t.Fatal("unreadable group membership was treated as satisfying the allow policy")
	}
	if !strings.Contains(err.Error(), "group") {
		t.Fatalf("denial reason = %q, want it to name the group claim so operators can fix the IdP", err)
	}
	stored, err := users.GetByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Role != appauth.RoleAdmin {
		t.Fatalf("role = %q, want admin: unreadable groups must never be read as an empty group set", stored.Role)
	}
}

func TestResolveUserIgnoresGroupOverageWhenNoGroupPolicyConfigured(t *testing.T) {
	cfg := config.Config{OIDCEmailVerification: "required", OIDCAllowedDomains: "example.com", OIDCDefaultRole: "viewer"}
	handler, _, user := linkedOIDCFixture(t, cfg, "viewer", nil)

	verified := true
	claims := oidcClaims{
		Subject:       "subject-1",
		Email:         user.Email,
		EmailVerified: &verified,
		ClaimNames:    map[string]json.RawMessage{"groups": json.RawMessage(`"src1"`)},
	}
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{}); err != nil {
		t.Fatalf("domain-only policy denied a login over an irrelevant group claim: %v", err)
	}
}

func TestResolveUserAppliesAllowPolicyWhenLinkingExistingUser(t *testing.T) {
	db, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	users := appauth.NewUserStore(db.DB)
	identities := appauth.NewIdentityStore(db.DB)
	user, err := users.Create("contractor@other.test", "", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	handler := &OIDCHandler{
		cfg:   config.Config{OIDCEmailVerification: "required", OIDCAllowedDomains: "example.com", OIDCDefaultRole: "viewer"},
		users: users, identities: identities, audit: appauth.NewAuditStore(db.DB),
	}
	verified := true
	claims := oidcClaims{Subject: "subject-1", Email: user.Email, EmailVerified: &verified}
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{}); err == nil {
		t.Fatal("linking login bypassed the allow policy that every later login enforces")
	}
	if count, err := identities.CountForUser(t.Context(), user.ID); err != nil || count != 0 {
		t.Fatalf("ineligible identity links = %d, err %v", count, err)
	}
}

func TestResolveUserReportsUnusableEmailClaimDistinctly(t *testing.T) {
	cfg := config.Config{OIDCEmailVerification: "required", OIDCAllowedDomains: "example.com", OIDCDefaultRole: "viewer"}
	handler, _, user := linkedOIDCFixture(t, cfg, "viewer", nil)

	verified := true
	claims := oidcClaims{Subject: "subject-1", Email: "", EmailVerified: &verified}
	_, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{})
	if err == nil {
		t.Fatalf("login for %s proceeded with no evaluable email claim", user.Email)
	}
	if !strings.Contains(err.Error(), "email") {
		t.Fatalf("denial reason = %q, want it to name the missing email claim rather than blame the allow policy", err)
	}
}

func TestResolveUserSkipsRoleReconciliationForInactiveUser(t *testing.T) {
	cfg := config.Config{
		OIDCEmailVerification: "required",
		OIDCAllowedGroups:     "trusted",
		OIDCAdminGroups:       "fanout-admins",
		OIDCDefaultRole:       "viewer",
	}
	handler, users, user := linkedOIDCFixture(t, cfg, "admin", []string{"trusted", "fanout-admins"})
	if _, err := users.Create("other-admin@example.com", "", "admin"); err != nil {
		t.Fatal(err)
	}
	inactive := false
	if _, err := users.Update(user.ID, nil, nil, nil, &inactive); err != nil {
		t.Fatal(err)
	}

	verified := true
	claims := oidcClaims{Subject: "subject-1", Email: user.Email, EmailVerified: &verified, Groups: []string{"trusted"}}
	_, _, _ = handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{})

	stored, err := users.GetByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Role != appauth.RoleAdmin {
		t.Fatalf("role = %q, want admin: a denied login by a deactivated account must not rewrite its role", stored.Role)
	}
}

func TestOIDCClaimsDetectGroupOverageWithoutBreakingOnProviderShapes(t *testing.T) {
	for name, raw := range map[string]string{
		"claim names":        `{"sub":"s","_claim_names":{"groups":"src1"}}`,
		"claim names object": `{"sub":"s","_claim_names":{"groups":{"endpoint":"https://graph"}}}`,
		"has groups bool":    `{"sub":"s","hasgroups":true}`,
		"has groups string":  `{"sub":"s","hasgroups":"true"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var claims oidcClaims
			if err := json.Unmarshal([]byte(raw), &claims); err != nil {
				t.Fatalf("provider claim shape broke every login: %v", err)
			}
			if !claims.groupsUnreadable() {
				t.Fatal("group overage was read as an empty group set")
			}
		})
	}
	var plain oidcClaims
	if err := json.Unmarshal([]byte(`{"sub":"s","groups":["a"]}`), &plain); err != nil {
		t.Fatal(err)
	}
	if plain.groupsUnreadable() {
		t.Fatal("an enumerable group set was treated as unreadable")
	}
}

func TestResolveUserReconcilesRoleOnTheLinkingLogin(t *testing.T) {
	db, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	users := appauth.NewUserStore(db.DB)
	identities := appauth.NewIdentityStore(db.DB)
	user, err := users.Create("promoted@example.com", "", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.Create("other-admin@example.com", "", "admin"); err != nil {
		t.Fatal(err)
	}
	handler := &OIDCHandler{
		cfg: config.Config{
			OIDCEmailVerification: "required",
			OIDCAllowedDomains:    "example.com",
			OIDCAdminGroups:       "fanout-admins",
			OIDCDefaultRole:       "viewer",
		},
		users: users, identities: identities, audit: appauth.NewAuditStore(db.DB),
	}
	verified := true
	claims := oidcClaims{Subject: "subject-1", Email: user.Email, EmailVerified: &verified}
	got, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{})
	if err != nil {
		t.Fatalf("linking login: %v", err)
	}
	if got.Role != appauth.RoleViewer {
		t.Fatalf("role on the linking login = %q, want viewer: the IdP owns the role from the first login, not the second", got.Role)
	}
}

func TestResolveUserDoesNotProvisionWhenGroupsAreUnreadable(t *testing.T) {
	db, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	users := appauth.NewUserStore(db.DB)
	if _, err := users.Create("seed-admin@example.com", "", "admin"); err != nil {
		t.Fatal(err)
	}
	handler := &OIDCHandler{
		cfg: config.Config{
			OIDCEmailVerification: "required",
			OIDCAutoProvision:     true,
			OIDCAllowedDomains:    "example.com",
			OIDCAllowedGroups:     "trusted",
			OIDCDefaultRole:       "viewer",
		},
		users: users, identities: appauth.NewIdentityStore(db.DB), audit: appauth.NewAuditStore(db.DB),
	}
	verified := true
	claims := oidcClaims{
		Subject:       "subject-new",
		Email:         "newcomer@example.com",
		EmailVerified: &verified,
		ClaimNames:    map[string]json.RawMessage{"groups": json.RawMessage(`"src1"`)},
	}
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims, requestSource{}); err == nil {
		t.Fatal("unreadable groups provisioned a new user")
	}
	if _, err := users.GetByEmail("newcomer@example.com"); err == nil {
		t.Fatal("a denied login left a persisted user behind")
	}
}

func TestReconciledRoleChangeIsAttributableToItsSource(t *testing.T) {
	db, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	users := appauth.NewUserStore(db.DB)
	identities := appauth.NewIdentityStore(db.DB)
	user, err := users.Create("attributed@example.com", "", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.Create("other-admin@example.com", "", "admin"); err != nil {
		t.Fatal(err)
	}
	handler := &OIDCHandler{
		cfg: config.Config{
			OIDCEmailVerification: "required",
			OIDCAllowedGroups:     "trusted",
			OIDCAdminGroups:       "fanout-admins",
			OIDCDefaultRole:       "viewer",
		},
		users: users, identities: identities, audit: appauth.NewAuditStore(db.DB),
	}
	verified := true
	source := requestSource{IP: "203.0.113.7", UserAgent: "smoke/1.0"}
	linking := oidcClaims{Subject: "subject-1", Email: user.Email, EmailVerified: &verified, Groups: []string{"trusted"}}
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", linking, source); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var ip, agent string
	row := db.DB.QueryRow(`SELECT COALESCE(remote_ip, ''), COALESCE(user_agent, '') FROM auth_audit_events WHERE event_type = 'role.changed'`)
	if err := row.Scan(&ip, &agent); err != nil {
		t.Fatalf("read role.changed audit event: %v", err)
	}
	if ip != source.IP || agent != source.UserAgent {
		t.Fatalf("role.changed recorded ip=%q agent=%q, want %q and %q: the handler's most security-relevant event must be correlatable", ip, agent, source.IP, source.UserAgent)
	}
}
