package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/env"
	appstore "github.com/labstack/fanout/internal/store"
	mcpgoauth "github.com/modelcontextprotocol/go-sdk/auth"
)

const testMCPResource = "https://demo.fanout.test/mcp"

func newOAuthTestServer(t *testing.T) (*echo.Echo, *auth.UserStore, *auth.BrowserSessions) {
	return newOAuthTestServerWithConfig(t, env.Config{})
}

func newOAuthTestServerWithConfig(t *testing.T, cfg env.Config) (*echo.Echo, *auth.UserStore, *auth.BrowserSessions) {
	t.Helper()
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	users := auth.NewUserStore(sqlite.DB)
	sessions := auth.NewBrowserSessions(sqlite.DB, 12*time.Hour, 7*24*time.Hour, false)
	audit := auth.NewAuditStore(sqlite.DB)
	handler, err := NewMCPAuthorization(auth.NewOAuthStore(sqlite.DB), users, testMCPResource)
	if err != nil {
		t.Fatalf("NewMCPAuthorization: %v", err)
	}
	e := echo.New()
	RegisterAuthMiddleware(e, users, sessions, audit, cfg)
	// Test-only login hook: it still creates the cookie through the production
	// BrowserSessions API and middleware commit path.
	e.POST("/api/auth/setup", func(c *echo.Context) error {
		user, err := users.GetByID(c.Request().Header.Get("X-Test-User"))
		if err != nil {
			return err
		}
		if err := sessions.EstablishAuthenticatedSession(c.Request().Context(), user); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	})
	handler.Register(e)
	e.Any("/mcp", echo.WrapHandler(handler.ProtectMCP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))))
	e.Any("/api/mcp", ProtectBrowserMCP(sessions, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := mcpgoauth.TokenInfoFromContext(r.Context())
		if info == nil {
			http.Error(w, "missing MCP token info", http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Test-MCP-User", info.UserID)
		w.Header().Set("X-Test-MCP-Scopes", strings.Join(info.Scopes, " "))
		w.WriteHeader(http.StatusNoContent)
	})))
	e.GET("/api/auth/me", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	return e, users, sessions
}

func TestMCPOAuthDiscoveryAndAuthorizationCodeFlow(t *testing.T) {
	e, users, _ := newOAuthTestServer(t)
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
	cookie := oauthCookieForUser(t, e, user)
	consent := serve(t, e, http.MethodGet, "/api/auth/oauth/authorize?"+params.Encode(), "", nil, cookie)
	if consent.Code != http.StatusOK || !strings.Contains(consent.Body.String(), "Allow Codex to access Fanout?") {
		t.Fatalf("consent = %d %s", consent.Code, consent.Body.String())
	}
	csp := consent.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "form-action 'self' http://localhost:4321") {
		t.Fatalf("consent CSP does not allow only the registered loopback origin: %q", csp)
	}
	for _, directive := range []string{"default-src 'none'", "base-uri 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, directive) {
			t.Fatalf("consent CSP = %q, missing %q", csp, directive)
		}
	}
	if !strings.Contains(consent.Body.String(), `action="/api/auth/oauth/authorize"`) {
		t.Fatalf("consent form target is not the fixed same-origin endpoint: %s", consent.Body.String())
	}
	for _, brandElement := range []string{
		`class="brand-mark"`,
		`class="brand-name">Fanout`,
	} {
		if !strings.Contains(consent.Body.String(), brandElement) {
			t.Fatalf("consent page is missing branded element %q: %s", brandElement, consent.Body.String())
		}
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
	if err := users.RevokeAllSessions(user.ID); err != nil {
		t.Fatalf("logout everywhere: %v", err)
	}
	replayed := serve(t, e, http.MethodPost, "/mcp", "", map[string]string{"Authorization": "Bearer " + access})
	if replayed.Code != http.StatusUnauthorized {
		t.Fatalf("MCP token survived logout everywhere: %d %s", replayed.Code, replayed.Body.String())
	}
	web := serve(t, e, http.MethodGet, "/api/auth/me", "", map[string]string{"Authorization": "Bearer " + access})
	if web.Code != http.StatusUnauthorized {
		t.Fatalf("web API accepted MCP token: %d", web.Code)
	}
}

func TestMCPOAuthRejectsUnknownBearerAndAdvertisesDiscovery(t *testing.T) {
	e, _, _ := newOAuthTestServer(t)
	rec := serve(t, e, http.MethodPost, "/mcp", "", map[string]string{"Authorization": "Bearer not-an-mcp-token"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("MCP accepted unknown bearer: %d", rec.Code)
	}
	want := `resource_metadata="https://demo.fanout.test/.well-known/oauth-protected-resource/mcp"`
	if !strings.Contains(rec.Header().Get("WWW-Authenticate"), want) {
		t.Fatalf("WWW-Authenticate = %q, want %s", rec.Header().Get("WWW-Authenticate"), want)
	}
}

func TestBrowserMCPUsesSessionWithoutWeakeningRemoteMCP(t *testing.T) {
	e, users, _ := newOAuthTestServer(t)
	user, err := users.Create("browser-mcp@example.com", "Browser MCP", "viewer")
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}

	anonymous := serve(t, e, http.MethodPost, "/api/mcp", "", map[string]string{"X-Fanout-Request": "1"})
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous browser MCP = %d, want 401", anonymous.Code)
	}

	cookie := oauthCookieForUser(t, e, user)
	csrfRequest := httptest.NewRequest(http.MethodPost, "/api/mcp", nil)
	csrfRequest.AddCookie(cookie)
	csrfResponse := httptest.NewRecorder()
	e.ServeHTTP(csrfResponse, csrfRequest)
	if csrfResponse.Code != http.StatusForbidden {
		t.Fatalf("browser MCP without CSRF proof = %d %s, want 403", csrfResponse.Code, csrfResponse.Body.String())
	}

	browser := serve(t, e, http.MethodPost, "/api/mcp", "", map[string]string{
		"Authorization":    "Bearer attacker-controlled",
		"X-Fanout-Request": "1",
	}, cookie)
	if browser.Code != http.StatusNoContent {
		t.Fatalf("session browser MCP = %d %s", browser.Code, browser.Body.String())
	}
	if got := browser.Header().Get("X-Test-MCP-User"); got != user.ID {
		t.Fatalf("browser MCP user = %q, want %q", got, user.ID)
	}
	scopes := strings.Fields(browser.Header().Get("X-Test-MCP-Scopes"))
	if !slices.Contains(scopes, mcpReadScope) || !slices.Contains(scopes, "fanout:dashboard") {
		t.Fatalf("browser MCP scopes = %v, want read and dashboard access", scopes)
	}

	remoteWithSessionOnly := serve(t, e, http.MethodPost, "/mcp", "", map[string]string{"X-Fanout-Request": "1"}, cookie)
	if remoteWithSessionOnly.Code != http.StatusUnauthorized {
		t.Fatalf("remote MCP accepted browser session: %d", remoteWithSessionOnly.Code)
	}

	public, _, _ := newOAuthTestServerWithConfig(t, env.Config{PublicRead: true})
	publicBrowserMCP := serve(t, public, http.MethodGet, "/api/mcp", "", nil)
	if publicBrowserMCP.Code != http.StatusUnauthorized {
		t.Fatalf("PUBLIC_READ viewer reached browser MCP: %d %s", publicBrowserMCP.Code, publicBrowserMCP.Body.String())
	}
}

func TestMCPOAuthRegistrationRejectsUnsafeRedirect(t *testing.T) {
	e, _, _ := newOAuthTestServer(t)
	rec := serve(t, e, http.MethodPost, "/oauth/register", `{"client_name":"bad","redirect_uris":["http://example.com/callback"]}`, map[string]string{"Content-Type": "application/json"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_redirect_uri") {
		t.Fatalf("unsafe redirect registration = %d %s", rec.Code, rec.Body.String())
	}
}

func TestMCPOAuthRegistrationRejectsCSPUnsafeRedirectHost(t *testing.T) {
	e, _, _ := newOAuthTestServer(t)
	rec := serve(t, e, http.MethodPost, "/oauth/register", `{"client_name":"bad","redirect_uris":["https://example.com;form-action=*/callback"]}`, map[string]string{"Content-Type": "application/json"})
	if rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "invalid_redirect_uri") ||
		!strings.Contains(rec.Body.String(), "ASCII hostname") {
		t.Fatalf("CSP-unsafe redirect registration = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRedirectURIOriginPreservesIPv4MappedIPv6Host(t *testing.T) {
	const redirect = "http://[::ffff:127.0.0.1]:5000/callback"
	origin, err := redirectURIOrigin(redirect)
	if err != nil {
		t.Fatalf("redirectURIOrigin(%q): %v", redirect, err)
	}
	if want := "http://[::ffff:127.0.0.1]:5000"; origin != want {
		t.Fatalf("redirectURIOrigin(%q) = %q, want %q", redirect, origin, want)
	}
	if source := redirectFormActionSource(redirect, origin); source != "http:" {
		t.Fatalf("redirectFormActionSource(%q) = %q, want %q", redirect, source, "http:")
	}
}

func TestRedirectFormActionSourceUsesSchemeForIPv6(t *testing.T) {
	const redirect = "http://[::1]:5000/callback"
	origin, err := redirectURIOrigin(redirect)
	if err != nil {
		t.Fatalf("redirectURIOrigin(%q): %v", redirect, err)
	}
	if want := "http://[::1]:5000"; origin != want {
		t.Fatalf("redirectURIOrigin(%q) = %q, want %q", redirect, origin, want)
	}
	if source := redirectFormActionSource(redirect, origin); source != "http:" {
		t.Fatalf("redirectFormActionSource(%q) = %q, want %q", redirect, source, "http:")
	}
}

// --- HTTP-layer flow helpers -------------------------------------------------

const testRedirectURI = "http://localhost:4321/callback"

var formHeaders = map[string]string{"Content-Type": "application/x-www-form-urlencoded"}

func oauthCookieForUser(t *testing.T, e *echo.Echo, user auth.User) *http.Cookie {
	t.Helper()
	rec := serve(t, e, http.MethodPost, "/api/auth/setup", "", map[string]string{"X-Test-User": user.ID, "X-Fanout-Request": "1"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("test login = %d %s", rec.Code, rec.Body.String())
	}
	return firstCookie(t, rec, "fanout_session")
}

func oauthSessionCookie(t *testing.T, e *echo.Echo, users *auth.UserStore, email string) *http.Cookie {
	t.Helper()
	user, err := users.Create(email, "", "admin")
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	return oauthCookieForUser(t, e, user)
}

func registerOAuthClient(t *testing.T, e *echo.Echo) auth.OAuthClient {
	t.Helper()
	rec := serve(t, e, http.MethodPost, "/oauth/register",
		`{"client_name":"Test Client","redirect_uris":["`+testRedirectURI+`"]}`,
		map[string]string{"Content-Type": "application/json"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("registration = %d %s", rec.Code, rec.Body.String())
	}
	var client auth.OAuthClient
	if err := json.Unmarshal(rec.Body.Bytes(), &client); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	return client
}

func pkcePair() (verifier, challenge string) {
	verifier = strings.Repeat("k", 64)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// authorizeParams builds a valid authorization request; scope "" omits the
// scope parameter entirely.
func authorizeParams(clientID, scope, challenge string) url.Values {
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {testRedirectURI},
		"state":                 {"state-xyz"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"resource":              {testMCPResource},
	}
	if scope != "" {
		params.Set("scope", scope)
	}
	return params
}

func approveAndGetCode(t *testing.T, e *echo.Echo, params url.Values, cookie *http.Cookie) string {
	t.Helper()
	params.Set("decision", "approve")
	rec := serve(t, e, http.MethodPost, "/api/auth/oauth/authorize", params.Encode(), formHeaders, cookie)
	if rec.Code != http.StatusFound {
		t.Fatalf("approve = %d %s", rec.Code, rec.Body.String())
	}
	callback, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse callback: %v", err)
	}
	code := callback.Query().Get("code")
	if code == "" {
		t.Fatalf("callback without code: %s", callback)
	}
	return code
}

func exchangeCode(t *testing.T, e *echo.Echo, clientID, code, redirectURI, verifier string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
		"resource":      {testMCPResource},
	}
	return serve(t, e, http.MethodPost, "/oauth/token", form.Encode(), formHeaders)
}

func decodeTokens(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("token exchange = %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode tokens: %v", err)
	}
	return body
}

// --- HTTP-layer negative-path and scope tests ---------------------------------

func TestMCPOAuthTokenExchangeRejectsWrongPKCEVerifier(t *testing.T) {
	e, users, _ := newOAuthTestServer(t)
	cookie := oauthSessionCookie(t, e, users, "pkce@example.com")
	client := registerOAuthClient(t, e)
	_, challenge := pkcePair()
	code := approveAndGetCode(t, e, authorizeParams(client.ClientID, mcpReadScope, challenge), cookie)

	rec := exchangeCode(t, e, client.ClientID, code, testRedirectURI, strings.Repeat("x", 64))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_grant") {
		t.Fatalf("wrong PKCE verifier = %d %s, want 400 invalid_grant", rec.Code, rec.Body.String())
	}
}

func TestMCPOAuthConsentDenyRedirectsAccessDenied(t *testing.T) {
	e, users, _ := newOAuthTestServer(t)
	cookie := oauthSessionCookie(t, e, users, "deny@example.com")
	client := registerOAuthClient(t, e)
	_, challenge := pkcePair()
	params := authorizeParams(client.ClientID, mcpReadScope, challenge)
	params.Set("decision", "deny")

	rec := serve(t, e, http.MethodPost, "/api/auth/oauth/authorize", params.Encode(), formHeaders, cookie)
	if rec.Code != http.StatusFound {
		t.Fatalf("deny = %d %s, want redirect", rec.Code, rec.Body.String())
	}
	callback, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse callback: %v", err)
	}
	if callback.Query().Get("error") != "access_denied" || callback.Query().Get("code") != "" {
		t.Fatalf("deny callback = %s, want error=access_denied and no code", callback)
	}
	if callback.Query().Get("state") != "state-xyz" {
		t.Fatalf("deny callback dropped state: %s", callback)
	}
}

func TestMCPOAuthTokenExchangeRejectsRedirectURIMismatch(t *testing.T) {
	e, users, _ := newOAuthTestServer(t)
	cookie := oauthSessionCookie(t, e, users, "redirect-mismatch@example.com")
	client := registerOAuthClient(t, e)
	verifier, challenge := pkcePair()
	code := approveAndGetCode(t, e, authorizeParams(client.ClientID, mcpReadScope, challenge), cookie)

	rec := exchangeCode(t, e, client.ClientID, code, "http://localhost:4321/other", verifier)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_grant") {
		t.Fatalf("redirect_uri mismatch = %d %s, want 400 invalid_grant", rec.Code, rec.Body.String())
	}
}

func TestMCPOAuthRefreshGrantOverHTTP(t *testing.T) {
	e, users, _ := newOAuthTestServer(t)
	cookie := oauthSessionCookie(t, e, users, "refresh-http@example.com")
	client := registerOAuthClient(t, e)
	verifier, challenge := pkcePair()
	code := approveAndGetCode(t, e, authorizeParams(client.ClientID, mcpReadScope, challenge), cookie)
	tokens := decodeTokens(t, exchangeCode(t, e, client.ClientID, code, testRedirectURI, verifier))
	firstRefresh := tokens["refresh_token"].(string)

	// A resource that is not this MCP server is rejected before rotation.
	mismatch := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {client.ClientID},
		"refresh_token": {firstRefresh},
		"resource":      {"https://other.example/mcp"},
	}
	rec := serve(t, e, http.MethodPost, "/oauth/token", mismatch.Encode(), formHeaders)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_target") {
		t.Fatalf("resource mismatch = %d %s, want 400 invalid_target", rec.Code, rec.Body.String())
	}

	// Omitted resource defaults to this MCP server and rotates successfully.
	rotate := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {client.ClientID},
		"refresh_token": {firstRefresh},
	}
	rotated := decodeTokens(t, serve(t, e, http.MethodPost, "/oauth/token", rotate.Encode(), formHeaders))
	if rotated["access_token"] == tokens["access_token"] || rotated["refresh_token"] == firstRefresh {
		t.Fatalf("refresh grant did not rotate credentials: %v", rotated)
	}
	if rotated["scope"] != mcpReadScope {
		t.Fatalf("rotated scope = %v, want %q", rotated["scope"], mcpReadScope)
	}
	mcp := serve(t, e, http.MethodPost, "/mcp", "", map[string]string{"Authorization": "Bearer " + rotated["access_token"].(string)})
	if mcp.Code != http.StatusNoContent {
		t.Fatalf("MCP with rotated token = %d %s", mcp.Code, mcp.Body.String())
	}

	// Replaying the consumed refresh token is invalid_grant at the HTTP layer.
	rec = serve(t, e, http.MethodPost, "/oauth/token", rotate.Encode(), formHeaders)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_grant") {
		t.Fatalf("refresh replay = %d %s, want 400 invalid_grant", rec.Code, rec.Body.String())
	}
}

// An omitted scope must grant read-only access, and the consent card must say
// exactly that — never the full supported-scope set.
func TestMCPOAuthOmittedScopeGrantsReadOnly(t *testing.T) {
	e, users, _ := newOAuthTestServer(t)
	cookie := oauthSessionCookie(t, e, users, "default-scope@example.com")
	client := registerOAuthClient(t, e)
	verifier, challenge := pkcePair()
	params := authorizeParams(client.ClientID, "", challenge) // no scope

	consent := serve(t, e, http.MethodGet, "/api/auth/oauth/authorize?"+params.Encode(), "", nil, cookie)
	if consent.Code != http.StatusOK {
		t.Fatalf("consent = %d %s", consent.Code, consent.Body.String())
	}
	body := consent.Body.String()
	if !strings.Contains(body, "read-only") || !strings.Contains(body, mcpReadScope) {
		t.Fatalf("consent for omitted scope must advertise read-only %s, got: %s", mcpReadScope, body)
	}
	if strings.Contains(body, "fanout:dashboard") || strings.Contains(body, "dashboards and overwrite") {
		t.Fatalf("consent for omitted scope leaked dashboard write access: %s", body)
	}

	code := approveAndGetCode(t, e, params, cookie)
	tokens := decodeTokens(t, exchangeCode(t, e, client.ClientID, code, testRedirectURI, verifier))
	if tokens["scope"] != mcpReadScope {
		t.Fatalf("granted scope = %v, want %q", tokens["scope"], mcpReadScope)
	}
}

// When dashboard write access is requested, the consent card must say so and
// must not claim the grant is read-only.
func TestMCPOAuthConsentShowsDashboardWriteGrant(t *testing.T) {
	e, users, _ := newOAuthTestServer(t)
	cookie := oauthSessionCookie(t, e, users, "dashboard-scope@example.com")
	client := registerOAuthClient(t, e)
	verifier, challenge := pkcePair()
	scope := mcpReadScope + " fanout:dashboard"
	params := authorizeParams(client.ClientID, scope, challenge)

	consent := serve(t, e, http.MethodGet, "/api/auth/oauth/authorize?"+params.Encode(), "", nil, cookie)
	if consent.Code != http.StatusOK {
		t.Fatalf("consent = %d %s", consent.Code, consent.Body.String())
	}
	body := consent.Body.String()
	if !strings.Contains(body, "Create and replace dashboards") || !strings.Contains(body, "fanout:dashboard") {
		t.Fatalf("consent must disclose dashboard write access, got: %s", body)
	}
	if strings.Contains(body, "read-only") {
		t.Fatalf("consent claims read-only for a write-capable grant: %s", body)
	}

	code := approveAndGetCode(t, e, params, cookie)
	tokens := decodeTokens(t, exchangeCode(t, e, client.ClientID, code, testRedirectURI, verifier))
	if tokens["scope"] != scope {
		t.Fatalf("granted scope = %v, want %q", tokens["scope"], scope)
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
	if len(cookies) > 0 && isUnsafeMethod(method) && req.Header.Get("X-Fanout-Request") == "" {
		req.Header.Set("X-Fanout-Request", "1")
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}
