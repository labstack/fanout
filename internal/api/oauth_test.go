package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
	appstore "github.com/labstack/fanout/internal/store"
	mcpgoauth "github.com/modelcontextprotocol/go-sdk/auth"
)

const testMCPResource = "https://fanout.example.com/mcp"

const testReadMCPCall = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"observability_overview","arguments":{}}}`

func newOAuthTestServer(t *testing.T) (*echo.Echo, *auth.UserStore, *auth.BrowserSessions) {
	return newOAuthTestServerWithConfig(t, config.Config{})
}

func newOAuthTestServerWithConfig(t *testing.T, cfg config.Config) (*echo.Echo, *auth.UserStore, *auth.BrowserSessions) {
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
	e.Any("/mcp", echo.WrapHandler(handler.ProtectMCP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if info := mcpgoauth.TokenInfoFromContext(r.Context()); info != nil {
			if role, ok := info.Extra["role"]; ok {
				w.Header().Set("X-Test-MCP-Role", fmt.Sprint(role))
			}
		}
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
		if role, ok := info.Extra["role"]; ok {
			w.Header().Set("X-Test-MCP-Role", fmt.Sprint(role))
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	e.GET("/api/auth/me", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	return e, users, sessions
}

func TestMCPRejectsUnexpectedHostBeforeAuthentication(t *testing.T) {
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	handler, err := NewMCPAuthorization(auth.NewOAuthStore(sqlite.DB), auth.NewUserStore(sqlite.DB), testMCPResource)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://attacker.example/mcp", nil)
	req.Host = "attacker.example"
	rec := httptest.NewRecorder()
	handler.ProtectMCP(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unexpected host reached MCP protocol handler")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("unexpected host = %d, want 421", rec.Code)
	}
}

func TestMCPOAuthDiscoveryAndAuthorizationCodeFlow(t *testing.T) {
	e, users, _ := newOAuthTestServer(t)
	user, err := users.Create("owner@example.com", "Owner", "admin")
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}

	metadata := serve(t, e, http.MethodGet, "/.well-known/oauth-protected-resource/mcp", "", nil)
	if metadata.Code != http.StatusOK ||
		!strings.Contains(metadata.Body.String(), testMCPResource) ||
		!strings.Contains(metadata.Body.String(), `"resource_name":"Fanout Observability"`) ||
		!strings.Contains(metadata.Body.String(), `"scopes_supported":["telemetry:read","dashboard:manage"]`) {
		t.Fatalf("protected resource metadata = %d %s", metadata.Code, metadata.Body.String())
	}
	authorizationMetadata := serve(t, e, http.MethodGet, "/.well-known/oauth-authorization-server", "", nil)
	if authorizationMetadata.Code != http.StatusOK ||
		!strings.Contains(authorizationMetadata.Body.String(), `"code_challenge_methods_supported":["S256"]`) ||
		!strings.Contains(authorizationMetadata.Body.String(), `"scopes_supported":["telemetry:read","dashboard:manage"]`) ||
		!strings.Contains(authorizationMetadata.Body.String(), `"authorization_response_iss_parameter_supported":true`) {
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
	if callback.Query().Get("state") != "state-123" || callback.Query().Get("code") == "" || callback.Query().Get("iss") != "https://fanout.example.com" {
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

	mcp := serve(t, e, http.MethodPost, "/mcp", testReadMCPCall, map[string]string{"Authorization": "Bearer " + access})
	if mcp.Code != http.StatusNoContent {
		t.Fatalf("MCP with OAuth token = %d %s", mcp.Code, mcp.Body.String())
	}
	if role := mcp.Header().Get("X-Test-MCP-Role"); role != "" {
		t.Fatalf("delegated MCP context exposed account role %q", role)
	}
	if err := users.RevokeAllSessions(user.ID); err != nil {
		t.Fatalf("logout everywhere: %v", err)
	}
	replayed := serve(t, e, http.MethodPost, "/mcp", testReadMCPCall, map[string]string{"Authorization": "Bearer " + access})
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
	want := `resource_metadata="https://fanout.example.com/.well-known/oauth-protected-resource/mcp"`
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

	anonymous := serve(t, e, http.MethodPost, "/api/mcp", "", map[string]string{"Fanout-Request": "1"})
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
		"Authorization":  "Bearer attacker-controlled",
		"Fanout-Request": "1",
	}, cookie)
	if browser.Code != http.StatusNoContent {
		t.Fatalf("session browser MCP = %d %s", browser.Code, browser.Body.String())
	}
	if got := browser.Header().Get("X-Test-MCP-User"); got != user.ID {
		t.Fatalf("browser MCP user = %q, want %q", got, user.ID)
	}
	scopes := strings.Fields(browser.Header().Get("X-Test-MCP-Scopes"))
	if !slices.Contains(scopes, mcpReadScope) || !slices.Contains(scopes, auth.MCPScopeDashboardManage) {
		t.Fatalf("browser MCP scopes = %v, want read and dashboard access", scopes)
	}
	if role := browser.Header().Get("X-Test-MCP-Role"); role != "" {
		t.Fatalf("browser MCP context exposed account role %q", role)
	}

	remoteWithSessionOnly := serve(t, e, http.MethodPost, "/mcp", "", map[string]string{"Fanout-Request": "1"}, cookie)
	if remoteWithSessionOnly.Code != http.StatusUnauthorized {
		t.Fatalf("remote MCP accepted browser session: %d", remoteWithSessionOnly.Code)
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
	origin, source, err := redirectURIOrigin(redirect)
	if err != nil {
		t.Fatalf("redirectURIOrigin(%q): %v", redirect, err)
	}
	if want := "http://[::ffff:127.0.0.1]:5000"; origin != want {
		t.Fatalf("redirectURIOrigin(%q) = %q, want %q", redirect, origin, want)
	}
	if source != "http:" {
		t.Fatalf("redirectURIOrigin(%q) form action = %q, want %q", redirect, source, "http:")
	}
}

func TestRedirectFormActionSourceUsesSchemeForIPv6(t *testing.T) {
	const redirect = "http://[::1]:5000/callback"
	origin, source, err := redirectURIOrigin(redirect)
	if err != nil {
		t.Fatalf("redirectURIOrigin(%q): %v", redirect, err)
	}
	if want := "http://[::1]:5000"; origin != want {
		t.Fatalf("redirectURIOrigin(%q) = %q, want %q", redirect, origin, want)
	}
	if source != "http:" {
		t.Fatalf("redirectURIOrigin(%q) form action = %q, want %q", redirect, source, "http:")
	}
}

func TestMCPOAuthConsentUsesSchemeSourceForIPv6Redirect(t *testing.T) {
	const redirect = "http://[::1]:5000/callback"

	e, users, _ := newOAuthTestServer(t)
	user, err := users.Create("ipv6-owner@example.com", "IPv6 Owner", "admin")
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}

	registration := serve(t, e, http.MethodPost, "/oauth/register",
		`{"client_name":"IPv6 Client","redirect_uris":["`+redirect+`"]}`,
		map[string]string{"Content-Type": "application/json"})
	if registration.Code != http.StatusCreated {
		t.Fatalf("registration = %d %s", registration.Code, registration.Body.String())
	}
	var client auth.OAuthClient
	if err := json.Unmarshal(registration.Body.Bytes(), &client); err != nil {
		t.Fatalf("decode registration: %v", err)
	}

	challenge := base64.RawURLEncoding.EncodeToString(make([]byte, sha256.Size))
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ClientID},
		"redirect_uri":          {redirect},
		"scope":                 {mcpReadScope},
		"state":                 {"state-ipv6"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"resource":              {testMCPResource},
	}
	consent := serve(t, e, http.MethodGet, "/api/auth/oauth/authorize?"+params.Encode(), "", nil, oauthCookieForUser(t, e, user))
	if consent.Code != http.StatusOK {
		t.Fatalf("consent = %d %s", consent.Code, consent.Body.String())
	}
	csp := consent.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "form-action 'self' http:;") {
		t.Fatalf("IPv6 consent CSP = %q, want scheme-source fallback", csp)
	}
	if strings.Contains(csp, "http://[::1]") {
		t.Fatalf("IPv6 consent CSP contains an inexpressible bracketed host-source: %q", csp)
	}
}

// --- HTTP-layer flow helpers -------------------------------------------------

const testRedirectURI = "http://localhost:4321/callback"

var formHeaders = map[string]string{"Content-Type": "application/x-www-form-urlencoded"}

func oauthCookieForUser(t *testing.T, e *echo.Echo, user auth.User) *http.Cookie {
	t.Helper()
	rec := serve(t, e, http.MethodPost, "/api/auth/setup", "", map[string]string{"X-Test-User": user.ID, "Fanout-Request": "1"})
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

func TestMCPOAuthScopePolicyCanonicalizesLegacyNames(t *testing.T) {
	legacy := "fanout:dashboard fanout:read fanout:dashboard"
	if !validMCPScopes(strings.Fields(legacy)) {
		t.Fatal("legacy scope aliases were rejected")
	}
	want := mcpReadScope + " " + auth.MCPScopeDashboardManage
	if got := authorizationScope(legacy); got != want {
		t.Fatalf("canonical scope = %q, want %q", got, want)
	}
	if !userCanUseMCPScopes(auth.User{Role: auth.RoleViewer}, want) {
		t.Fatal("viewer lost its configured MCP capabilities")
	}
	if userCanUseMCPScopes(auth.User{Role: auth.Role("retired")}, want) {
		t.Fatal("unknown role received MCP capabilities")
	}
}

func TestRequiredMCPToolScopeUsesPayloadAndReplaysBody(t *testing.T) {
	body := `[{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"observability_overview"}},{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"dashboard_get"}}]`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "observability_overview")

	got, err := requiredMCPToolScope(req)
	if err != nil {
		t.Fatal(err)
	}
	if got != auth.MCPScopeDashboardManage {
		t.Fatalf("required scope = %q, want %q", got, auth.MCPScopeDashboardManage)
	}
	replayed, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(replayed) != body {
		t.Fatalf("replayed body = %q, want original payload", replayed)
	}
}

func TestMCPOAuthTokenEndpointEnforcesScopeByGrantType(t *testing.T) {
	e, users, _ := newOAuthTestServer(t)
	cookie := oauthSessionCookie(t, e, users, "token-scope@example.com")
	client := registerOAuthClient(t, e)
	verifier, challenge := pkcePair()
	fullScope := mcpReadScope + " " + auth.MCPScopeDashboardManage
	params := authorizeParams(client.ClientID, fullScope, challenge)
	code := approveAndGetCode(t, e, params, cookie)

	codeExchange := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {client.ClientID},
		"code":          {code},
		"redirect_uri":  {testRedirectURI},
		"code_verifier": {verifier},
		"resource":      {testMCPResource},
		"scope":         {mcpReadScope},
	}
	rejectedCodeExchange := serve(t, e, http.MethodPost, "/oauth/token", codeExchange.Encode(), formHeaders)
	if rejectedCodeExchange.Code != http.StatusBadRequest || !strings.Contains(rejectedCodeExchange.Body.String(), `"error":"invalid_request"`) {
		t.Fatalf("code exchange scope = %d %s, want 400 invalid_request", rejectedCodeExchange.Code, rejectedCodeExchange.Body.String())
	}

	// Rejecting the extra parameter must not consume the authorization code.
	tokens := decodeTokens(t, exchangeCode(t, e, client.ClientID, code, testRedirectURI, verifier))
	refresh := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {client.ClientID},
		"refresh_token": {tokens["refresh_token"].(string)},
		"scope":         {mcpReadScope},
	}
	narrowed := decodeTokens(t, serve(t, e, http.MethodPost, "/oauth/token", refresh.Encode(), formHeaders))
	if narrowed["scope"] != mcpReadScope {
		t.Fatalf("narrowed refresh scope = %v, want %q", narrowed["scope"], mcpReadScope)
	}

	refresh.Set("refresh_token", narrowed["refresh_token"].(string))
	refresh.Set("scope", fullScope)
	expanded := serve(t, e, http.MethodPost, "/oauth/token", refresh.Encode(), formHeaders)
	if expanded.Code != http.StatusBadRequest || !strings.Contains(expanded.Body.String(), `"error":"invalid_scope"`) {
		t.Fatalf("expanded refresh scope = %d %s, want 400 invalid_scope", expanded.Code, expanded.Body.String())
	}

	// An invalid scope request must not consume the refresh token.
	refresh.Set("scope", mcpReadScope)
	decodeTokens(t, serve(t, e, http.MethodPost, "/oauth/token", refresh.Encode(), formHeaders))
}

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
	if callback.Query().Get("iss") != "https://fanout.example.com" {
		t.Fatalf("deny callback issuer = %q", callback.Query().Get("iss"))
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
	mcp := serve(t, e, http.MethodPost, "/mcp", testReadMCPCall, map[string]string{"Authorization": "Bearer " + rotated["access_token"].(string)})
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
	if strings.Contains(body, auth.MCPScopeDashboardManage) || strings.Contains(body, "dashboards and overwrite") {
		t.Fatalf("consent for omitted scope leaked dashboard write access: %s", body)
	}

	code := approveAndGetCode(t, e, params, cookie)
	tokens := decodeTokens(t, exchangeCode(t, e, client.ClientID, code, testRedirectURI, verifier))
	if tokens["scope"] != mcpReadScope {
		t.Fatalf("granted scope = %v, want %q", tokens["scope"], mcpReadScope)
	}
	dashboardCall := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dashboard_list","arguments":{}}}`
	challenged := serve(t, e, http.MethodPost, "/mcp", dashboardCall, map[string]string{
		"Authorization": "Bearer " + tokens["access_token"].(string),
		"Content-Type":  "application/json",
	})
	if challenged.Code != http.StatusForbidden {
		t.Fatalf("dashboard step-up = %d %s, want 403", challenged.Code, challenged.Body.String())
	}
	wwwAuthenticate := challenged.Header().Get("WWW-Authenticate")
	if !strings.Contains(wwwAuthenticate, `error="insufficient_scope"`) ||
		!strings.Contains(wwwAuthenticate, `scope="dashboard:manage"`) ||
		!strings.Contains(wwwAuthenticate, `resource_metadata="https://fanout.example.com/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("dashboard step-up challenge = %q", wwwAuthenticate)
	}
	spoofed := serve(t, e, http.MethodPost, "/mcp", dashboardCall, map[string]string{
		"Authorization": "Bearer " + tokens["access_token"].(string),
		"Content-Type":  "application/json",
		"Mcp-Method":    "tools/call",
		"Mcp-Name":      "observability_overview",
	})
	if spoofed.Code != http.StatusForbidden {
		t.Fatalf("spoofed dashboard step-up = %d %s, want 403", spoofed.Code, spoofed.Body.String())
	}
	malformed := serve(t, e, http.MethodPost, "/mcp", `{`, map[string]string{
		"Authorization": "Bearer " + tokens["access_token"].(string),
		"Content-Type":  "application/json",
	})
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed MCP body = %d %s, want 400", malformed.Code, malformed.Body.String())
	}
	oversized := serve(t, e, http.MethodPost, "/mcp", strings.Repeat(" ", maxMCPAuthorizationBodyBytes+1), map[string]string{
		"Authorization": "Bearer " + tokens["access_token"].(string),
		"Content-Type":  "application/json",
	})
	if oversized.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized MCP body = %d %s, want 413", oversized.Code, oversized.Body.String())
	}
}

// When dashboard write access is requested, the consent card must say so and
// must not claim the grant is read-only.
func TestMCPOAuthConsentShowsDashboardWriteGrant(t *testing.T) {
	e, users, _ := newOAuthTestServer(t)
	cookie := oauthSessionCookie(t, e, users, "dashboard-scope@example.com")
	client := registerOAuthClient(t, e)
	verifier, challenge := pkcePair()
	scope := mcpReadScope + " " + auth.MCPScopeDashboardManage
	params := authorizeParams(client.ClientID, scope, challenge)

	consent := serve(t, e, http.MethodGet, "/api/auth/oauth/authorize?"+params.Encode(), "", nil, cookie)
	if consent.Code != http.StatusOK {
		t.Fatalf("consent = %d %s", consent.Code, consent.Body.String())
	}
	body := consent.Body.String()
	if !strings.Contains(body, "Create and replace dashboards") || !strings.Contains(body, auth.MCPScopeDashboardManage) {
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
	allowed := serve(t, e, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dashboard_list","arguments":{}}}`, map[string]string{
		"Authorization": "Bearer " + tokens["access_token"].(string),
		"Content-Type":  "application/json",
	})
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("dashboard scope was rejected: %d %s", allowed.Code, allowed.Body.String())
	}
	// Fully scoped tokens bypass authorization-body buffering; payload validity
	// remains the MCP protocol handler's responsibility.
	unparsed := serve(t, e, http.MethodPost, "/mcp", `{`, map[string]string{
		"Authorization": "Bearer " + tokens["access_token"].(string),
		"Content-Type":  "application/json",
	})
	if unparsed.Code != http.StatusNoContent {
		t.Fatalf("fully scoped request was parsed by authorization gate: %d %s", unparsed.Code, unparsed.Body.String())
	}
}

func serve(t *testing.T, e *echo.Echo, method, target, body string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = "fanout.example.com"
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	if len(cookies) > 0 && isUnsafeMethod(method) && req.Header.Get("Fanout-Request") == "" {
		req.Header.Set("Fanout-Request", "1")
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestMCPOAuthRegistrationIsRateLimited(t *testing.T) {
	e, _, _ := newOAuthTestServer(t)
	body := `{"client_name":"burst","redirect_uris":["https://client.example.com/callback"]}`
	limited := false
	for i := 0; i < 200 && !limited; i++ {
		rec := serve(t, e, http.MethodPost, "/oauth/register", body, map[string]string{"Content-Type": "application/json"})
		switch rec.Code {
		case http.StatusCreated:
		case http.StatusTooManyRequests:
			limited = true
		default:
			t.Fatalf("registration %d = %d %s", i, rec.Code, rec.Body.String())
		}
	}
	if !limited {
		t.Fatal("200 unauthenticated registrations from one source were all accepted")
	}
}

func TestMCPOAuthConsentMarksDynamicClientUnverified(t *testing.T) {
	e, users, _ := newOAuthTestServer(t)
	cookie := oauthSessionCookie(t, e, users, "consent-provenance@example.com")
	client := registerOAuthClient(t, e)
	_, challenge := pkcePair()
	params := authorizeParams(client.ClientID, mcpReadScope, challenge)

	consent := serve(t, e, http.MethodGet, "/api/auth/oauth/authorize?"+params.Encode(), "", nil, cookie)
	if consent.Code != http.StatusOK {
		t.Fatalf("consent = %d %s", consent.Code, consent.Body.String())
	}
	page := consent.Body.String()
	// Registration is unauthenticated, so the client name is attacker-chosen.
	// The screen must show provenance the name cannot forge.
	if !strings.Contains(strings.ToLower(page), "unverified") {
		t.Fatalf("consent screen presents an attacker-chosen client name with no provenance warning: %s", page)
	}
}
