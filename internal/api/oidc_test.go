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
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v5"
	appauth "github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/env"
	appstore "github.com/labstack/fanout/internal/store"
)

func TestOIDCProvisionPolicy(t *testing.T) {
	h := &OIDCHandler{cfg: env.Config{
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
	cfg := env.Config{
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
		cfg:   env.Config{OIDCEmailVerification: "issuer", OIDCAllowedGroups: "trusted", OIDCDefaultRole: "viewer"},
		users: users, identities: identities, audit: appauth.NewAuditStore(db.DB),
	}
	verified := true
	claims := oidcClaims{Subject: "subject-1", Email: user.Email, EmailVerified: &verified, Groups: []string{"untrusted"}}
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims); err == nil {
		t.Fatal("issuer-mode existing user bypassed allow policy")
	}
	if count, err := identities.CountForUser(t.Context(), user.ID); err != nil || count != 0 {
		t.Fatalf("denied identity links = %d, err %v", count, err)
	}

	claims.Groups = []string{"trusted"}
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims); err != nil {
		t.Fatalf("allowed existing user: %v", err)
	}
	claims.Subject = "subject-2"
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", claims); err == nil {
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
	if _, _, err := handler.resolveUser(t.Context(), "https://issuer.example", inactiveClaims); err == nil {
		t.Fatal("inactive user was linked")
	}
}
