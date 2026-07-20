package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	appstore "github.com/labstack/fanout/internal/store"
)

const testMCPResource = "https://demo.fanout.test/mcp"

func newOAuthTestServer(t *testing.T) (*echo.Echo, *auth.UserStore, string) {
	t.Helper()
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	users := auth.NewUserStore(sqlite.DB)
	refreshSecret := "abcdef0123456789abcdef0123456789"
	handler, err := NewMCPAuthorization(auth.NewOAuthStore(sqlite.DB), users, refreshSecret, testMCPResource)
	if err != nil {
		t.Fatalf("NewMCPAuthorization: %v", err)
	}
	e := echo.New()
	RegisterAuthMiddleware(e, users, "0123456789abcdef0123456789abcdef", false)
	handler.Register(e)
	e.Any("/mcp", echo.WrapHandler(handler.ProtectMCP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))))
	e.GET("/api/auth/me", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	return e, users, refreshSecret
}

func TestMCPOAuthDiscoveryAndAuthorizationCodeFlow(t *testing.T) {
	e, users, refreshSecret := newOAuthTestServer(t)
	user, err := users.Create("owner@example.com", "Owner", "admin")
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}

	metadata := serve(t, e, http.MethodGet, "/.well-known/oauth-protected-resource/mcp", "", nil)
	if metadata.Code != http.StatusOK || !strings.Contains(metadata.Body.String(), testMCPResource) {
		t.Fatalf("protected resource metadata = %d %s", metadata.Code, metadata.Body.String())
	}
	authorizationMetadata := serve(t, e, http.MethodGet, "/.well-known/oauth-authorization-server", "", nil)
	if authorizationMetadata.Code != http.StatusOK || !strings.Contains(authorizationMetadata.Body.String(), `"code_challenge_methods_supported":["S256"]`) {
		t.Fatalf("authorization metadata = %d %s", authorizationMetadata.Code, authorizationMetadata.Body.String())
	}

	registration := serve(t, e, http.MethodPost, "/oauth/register", `{
		"client_name":"Codex",
		"redirect_uris":["http://localhost:4321/callback"],
		"grant_types":["authorization_code","refresh_token"],
		"response_types":["code"],
		"token_endpoint_auth_method":"none"
	}`, map[string]string{"Content-Type": "application/json"})
	if registration.Code != http.StatusCreated {
		t.Fatalf("registration = %d %s", registration.Code, registration.Body.String())
	}
	var client auth.OAuthClient
	if err := json.Unmarshal(registration.Body.Bytes(), &client); err != nil {
		t.Fatalf("decode registration: %v", err)
	}

	verifier := strings.Repeat("v", 64)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ClientID},
		"redirect_uri":          {"http://localhost:4321/callback"},
		"scope":                 {mcpReadScope},
		"state":                 {"state-123"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"resource":              {testMCPResource},
	}
	refreshToken, err := auth.SignRefresh(refreshSecret, user.ID, time.Now())
	if err != nil {
		t.Fatalf("SignRefresh: %v", err)
	}
	cookie := &http.Cookie{Name: "refresh_token", Value: refreshToken}
	consent := serve(t, e, http.MethodGet, "/api/auth/oauth/authorize?"+params.Encode(), "", nil, cookie)
	if consent.Code != http.StatusOK || !strings.Contains(consent.Body.String(), "Allow Codex to access Fanout?") {
		t.Fatalf("consent = %d %s", consent.Code, consent.Body.String())
	}

	params.Set("decision", "approve")
	approved := serve(t, e, http.MethodPost, "/api/auth/oauth/authorize", params.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, cookie)
	if approved.Code != http.StatusFound {
		t.Fatalf("approve = %d %s", approved.Code, approved.Body.String())
	}
	callback, err := url.Parse(approved.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse callback: %v", err)
	}
	if callback.Query().Get("state") != "state-123" || callback.Query().Get("code") == "" {
		t.Fatalf("callback = %s", callback)
	}

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {client.ClientID},
		"code":          {callback.Query().Get("code")},
		"redirect_uri":  {"http://localhost:4321/callback"},
		"code_verifier": {verifier},
		"resource":      {testMCPResource},
	}
	tokens := serve(t, e, http.MethodPost, "/oauth/token", tokenForm.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if tokens.Code != http.StatusOK {
		t.Fatalf("token exchange = %d %s", tokens.Code, tokens.Body.String())
	}
	var tokenBody map[string]any
	if err := json.Unmarshal(tokens.Body.Bytes(), &tokenBody); err != nil {
		t.Fatalf("decode tokens: %v", err)
	}
	access := tokenBody["access_token"].(string)
	if !strings.HasPrefix(access, "foa_") || tokenBody["refresh_token"] == "" {
		t.Fatalf("unexpected token response: %#v", tokenBody)
	}

	mcp := serve(t, e, http.MethodPost, "/mcp", "", map[string]string{"Authorization": "Bearer " + access})
	if mcp.Code != http.StatusNoContent {
		t.Fatalf("MCP with OAuth token = %d %s", mcp.Code, mcp.Body.String())
	}
	web := serve(t, e, http.MethodGet, "/api/auth/me", "", map[string]string{"Authorization": "Bearer " + access})
	if web.Code != http.StatusUnauthorized {
		t.Fatalf("web API accepted MCP token: %d", web.Code)
	}
}

func TestMCPOAuthRejectsBrowserJWTAndAdvertisesDiscovery(t *testing.T) {
	e, users, _ := newOAuthTestServer(t)
	user, _ := users.Create("jwt@example.com", "", "admin")
	webJWT, _ := auth.SignAccess("0123456789abcdef0123456789abcdef", user.ID)
	rec := serve(t, e, http.MethodPost, "/mcp", "", map[string]string{"Authorization": "Bearer " + webJWT})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("MCP accepted browser JWT: %d", rec.Code)
	}
	want := `resource_metadata="https://demo.fanout.test/.well-known/oauth-protected-resource/mcp"`
	if !strings.Contains(rec.Header().Get("WWW-Authenticate"), want) {
		t.Fatalf("WWW-Authenticate = %q, want %s", rec.Header().Get("WWW-Authenticate"), want)
	}
}

func TestMCPOAuthRegistrationRejectsUnsafeRedirect(t *testing.T) {
	e, _, _ := newOAuthTestServer(t)
	rec := serve(t, e, http.MethodPost, "/oauth/register", `{"client_name":"bad","redirect_uris":["http://example.com/callback"]}`, map[string]string{"Content-Type": "application/json"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_redirect_uri") {
		t.Fatalf("unsafe redirect registration = %d %s", rec.Code, rec.Body.String())
	}
}

func serve(t *testing.T, e *echo.Echo, method, target, body string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}
