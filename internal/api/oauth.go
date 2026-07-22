package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/dashboard"

	appauth "github.com/labstack/fanout/internal/auth"
	mcpgoauth "github.com/modelcontextprotocol/go-sdk/auth"
)

const mcpReadScope = "fanout:read"

var mcpSupportedScopes = []string{mcpReadScope, dashboard.OAuthScope}

type MCPAuthorization struct {
	store       *appauth.OAuthStore
	users       *appauth.UserStore
	resource    string
	issuer      string
	metadataURL string
}

func NewMCPAuthorization(store *appauth.OAuthStore, users *appauth.UserStore, publicMCPURL string) (*MCPAuthorization, error) {
	if store == nil || users == nil {
		return nil, fmt.Errorf("MCP authorization dependencies are required")
	}
	resource, err := url.Parse(publicMCPURL)
	if err != nil || resource.Scheme != "https" || resource.Host == "" || resource.Path != "/mcp" {
		return nil, fmt.Errorf("invalid public MCP URL")
	}
	issuer := resource.Scheme + "://" + resource.Host
	return &MCPAuthorization{
		store:       store,
		users:       users,
		resource:    resource.String(),
		issuer:      issuer,
		metadataURL: issuer + "/.well-known/oauth-protected-resource/mcp",
	}, nil
}

func (h *MCPAuthorization) Register(e *echo.Echo) {
	e.GET("/.well-known/oauth-protected-resource", h.ProtectedResourceMetadata)
	e.GET("/.well-known/oauth-protected-resource/mcp", h.ProtectedResourceMetadata)
	e.GET("/.well-known/oauth-authorization-server", h.AuthorizationServerMetadata)
	e.POST("/oauth/register", h.RegisterClient)
	e.POST("/oauth/token", h.Token)
	e.GET("/api/auth/oauth/authorize", h.Authorize)
	e.POST("/api/auth/oauth/authorize", h.Authorize)
}

func (h *MCPAuthorization) ProtectMCP(next http.Handler) http.Handler {
	return mcpgoauth.RequireBearerToken(h.verifyMCPToken, &mcpgoauth.RequireBearerTokenOptions{
		ResourceMetadataURL: h.metadataURL,
		Scopes:              []string{mcpReadScope},
	})(next)
}

func (h *MCPAuthorization) verifyMCPToken(ctx context.Context, raw string, _ *http.Request) (*mcpgoauth.TokenInfo, error) {
	record, err := h.store.VerifyAccessToken(ctx, raw, h.resource)
	if errors.Is(err, appauth.ErrInvalidOAuthToken) {
		return nil, mcpgoauth.ErrInvalidToken
	}
	if err != nil {
		// Infrastructure failure, not a bad credential: log it and return a
		// non-ErrInvalidToken error so the middleware renders 500, not 401.
		slog.Error("mcp oauth token verification failed", "err", err)
		return nil, fmt.Errorf("verify mcp access token: %w", err)
	}
	user, err := h.users.GetByID(record.UserID)
	if errors.Is(err, appauth.ErrUserNotFound) {
		return nil, mcpgoauth.ErrInvalidToken
	}
	if err != nil {
		slog.Error("mcp oauth user lookup failed", "user_id", record.UserID, "err", err)
		return nil, fmt.Errorf("load mcp token user: %w", err)
	}
	if !user.Active {
		return nil, mcpgoauth.ErrInvalidToken
	}
	return &mcpgoauth.TokenInfo{
		Scopes:     strings.Fields(record.Scope),
		Expiration: record.ExpiresAt,
		UserID:     record.UserID,
		Extra: map[string]any{
			"client_id": record.ClientID,
			"role":      user.Role,
		},
	}, nil
}

func (h *MCPAuthorization) ProtectedResourceMetadata(c *echo.Context) error {
	setDiscoveryHeaders(c)
	return c.JSON(http.StatusOK, map[string]any{
		"resource":                 h.resource,
		"authorization_servers":    []string{h.issuer},
		"scopes_supported":         mcpSupportedScopes,
		"bearer_methods_supported": []string{"header"},
	})
}

func (h *MCPAuthorization) AuthorizationServerMetadata(c *echo.Context) error {
	setDiscoveryHeaders(c)
	return c.JSON(http.StatusOK, map[string]any{
		"issuer":                                h.issuer,
		"authorization_endpoint":                h.issuer + "/api/auth/oauth/authorize",
		"token_endpoint":                        h.issuer + "/oauth/token",
		"registration_endpoint":                 h.issuer + "/oauth/register",
		"scopes_supported":                      mcpSupportedScopes,
		"response_types_supported":              []string{"code"},
		"response_modes_supported":              []string{"query"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"code_challenge_methods_supported":      []string{"S256"},
		"client_id_metadata_document_supported": false,
	})
}

func setDiscoveryHeaders(c *echo.Context) {
	c.Response().Header().Set("Access-Control-Allow-Origin", "*")
	c.Response().Header().Set("Cache-Control", "public, max-age=300")
}

type oauthRegistrationRequest struct {
	ClientName              string   `json:"client_name"`
	ClientURI               string   `json:"client_uri"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

func (h *MCPAuthorization) RegisterClient(c *echo.Context) error {
	var req oauthRegistrationRequest
	decoder := json.NewDecoder(io.LimitReader(c.Request().Body, 64<<10))
	if err := decoder.Decode(&req); err != nil {
		return oauthJSONError(c, http.StatusBadRequest, "invalid_client_metadata", "invalid registration document")
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 10 {
		return oauthJSONError(c, http.StatusBadRequest, "invalid_redirect_uri", "at least one redirect URI is required")
	}
	seen := make(map[string]struct{}, len(req.RedirectURIs))
	for _, redirect := range req.RedirectURIs {
		if err := validateRedirectURI(redirect); err != nil {
			return oauthJSONError(c, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
		}
		if _, exists := seen[redirect]; exists {
			return oauthJSONError(c, http.StatusBadRequest, "invalid_client_metadata", "redirect URIs must be unique")
		}
		seen[redirect] = struct{}{}
	}
	if req.ClientURI != "" {
		u, err := url.Parse(req.ClientURI)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.Fragment != "" {
			return oauthJSONError(c, http.StatusBadRequest, "invalid_client_metadata", "client_uri must be HTTPS")
		}
	}
	if len(req.GrantTypes) > 0 && !sameStringSet(req.GrantTypes, []string{"authorization_code", "refresh_token"}) {
		return oauthJSONError(c, http.StatusBadRequest, "invalid_client_metadata", "only authorization_code and refresh_token are supported")
	}
	if len(req.ResponseTypes) > 0 && !sameStringSet(req.ResponseTypes, []string{"code"}) {
		return oauthJSONError(c, http.StatusBadRequest, "invalid_client_metadata", "only the code response type is supported")
	}
	if req.TokenEndpointAuthMethod != "" && req.TokenEndpointAuthMethod != "none" {
		return oauthJSONError(c, http.StatusBadRequest, "invalid_client_metadata", "only public PKCE clients are supported")
	}
	name := strings.TrimSpace(req.ClientName)
	if name == "" {
		name = "MCP client"
	}
	if len(name) > 120 {
		return oauthJSONError(c, http.StatusBadRequest, "invalid_client_metadata", "client_name is too long")
	}
	client, err := h.store.RegisterClient(c.Request().Context(), name, req.ClientURI, req.RedirectURIs)
	if err != nil {
		slog.Error("oauth client registration failed", "err", err)
		return oauthJSONError(c, http.StatusInternalServerError, "server_error", "client registration failed")
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusCreated, client)
}

type authorizationRequest struct {
	ResponseType  string
	ClientID      string
	RedirectURI   string
	Scope         string
	State         string
	CodeChallenge string
	CodeMethod    string
	Resource      string
}

func (h *MCPAuthorization) Authorize(c *echo.Context) error {
	if err := c.Request().ParseForm(); err != nil {
		return oauthJSONError(c, http.StatusBadRequest, "invalid_request", "invalid authorization request")
	}
	req := authorizationRequest{
		ResponseType:  c.Request().Form.Get("response_type"),
		ClientID:      c.Request().Form.Get("client_id"),
		RedirectURI:   c.Request().Form.Get("redirect_uri"),
		Scope:         c.Request().Form.Get("scope"),
		State:         c.Request().Form.Get("state"),
		CodeChallenge: c.Request().Form.Get("code_challenge"),
		CodeMethod:    c.Request().Form.Get("code_challenge_method"),
		Resource:      c.Request().Form.Get("resource"),
	}
	client, errorCode, description := h.validateAuthorizationRequest(c.Request().Context(), req)
	if errorCode != "" {
		if client.ClientID != "" {
			return redirectOAuthError(c, req.RedirectURI, req.State, errorCode, description)
		}
		status := http.StatusBadRequest
		if errorCode == "server_error" {
			status = http.StatusInternalServerError
		}
		return oauthJSONError(c, status, errorCode, description)
	}

	user, ok := h.browserUser(c)
	if !ok {
		slog.Error("oauth consent reached handler without an authenticated browser user")
		return oauthJSONError(c, http.StatusUnauthorized, "access_denied", "browser authentication is required")
	}
	if c.Request().Method == http.MethodGet {
		redirectURL, _ := url.Parse(req.RedirectURI)
		grantedScope := authorizationScope(req.Scope)
		c.Response().Header().Set("Cache-Control", "no-store")
		c.Response().Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		c.Response().Header().Set("Content-Type", "text/html; charset=utf-8")
		return oauthConsentTemplate.Execute(c.Response(), map[string]any{
			"ClientName": client.ClientName,
			"Redirect":   redirectURL.Scheme + "://" + redirectURL.Host,
			"Email":      user.Email,
			"Request":    req,
			"Scope":      grantedScope,
			"ReadOnly":   grantedScope == mcpReadScope,
			"Grants":     consentGrants(grantedScope),
		})
	}
	if !validBrowserMutation(c.Request()) {
		return oauthJSONError(c, http.StatusForbidden, "access_denied", "browser request validation failed")
	}

	if c.Request().Form.Get("decision") != "approve" {
		return redirectOAuthError(c, req.RedirectURI, req.State, "access_denied", "authorization was denied")
	}
	code, err := h.store.CreateAuthorizationCode(c.Request().Context(), appauth.OAuthAuthorizationCode{
		ClientID:      req.ClientID,
		UserID:        user.ID,
		RedirectURI:   req.RedirectURI,
		Scope:         authorizationScope(req.Scope),
		Resource:      h.resource,
		CodeChallenge: req.CodeChallenge,
	})
	if err != nil {
		slog.Error("oauth authorization code creation failed", "client_id", req.ClientID, "user_id", user.ID, "err", err)
		return oauthJSONError(c, http.StatusInternalServerError, "server_error", "authorization failed")
	}
	slog.Info("oauth authorization approved", "client_id", req.ClientID, "user_id", user.ID, "scope", authorizationScope(req.Scope))
	return redirectOAuthSuccess(c, req.RedirectURI, req.State, code)
}

func (h *MCPAuthorization) validateAuthorizationRequest(ctx context.Context, req authorizationRequest) (appauth.OAuthClient, string, string) {
	client, err := h.store.GetClient(ctx, req.ClientID)
	if err != nil {
		if !errors.Is(err, appauth.ErrOAuthClientNotFound) {
			// Infrastructure failure, not a bad client — do not report it as one.
			slog.Error("oauth client lookup failed", "client_id", req.ClientID, "err", err)
			return appauth.OAuthClient{}, "server_error", "authorization failed"
		}
		return appauth.OAuthClient{}, "invalid_request", "unknown client"
	}
	if !slices.Contains(client.RedirectURIs, req.RedirectURI) {
		return appauth.OAuthClient{}, "invalid_request", "redirect URI does not match the registered client"
	}
	if req.ResponseType != "code" {
		return client, "unsupported_response_type", "only authorization code is supported"
	}
	if req.Resource != h.resource {
		return client, "invalid_target", "resource must identify this MCP server"
	}
	if req.Scope != "" && !validMCPScopes(strings.Fields(req.Scope)) {
		return client, "invalid_scope", "unsupported scope"
	}
	if req.CodeMethod != "S256" || !validS256Challenge(req.CodeChallenge) {
		return client, "invalid_request", "PKCE with S256 is required"
	}
	return client, "", ""
}

// authorizationScope resolves the scope actually granted. An omitted scope
// defaults to read-only — never to every supported scope, which would grant
// dashboard write access the consent screen never asked about.
func authorizationScope(requested string) string {
	if strings.TrimSpace(requested) == "" {
		return mcpReadScope
	}
	return strings.Join(strings.Fields(requested), " ")
}

type consentGrant struct {
	Title  string
	Detail string
}

// consentGrants translates the scope string that will actually be granted
// into the human-readable entries shown on the consent card, so the screen
// never understates (or overstates) the authority being handed out.
func consentGrants(scope string) []consentGrant {
	grants := []consentGrant{{
		Title:  "Read observability data",
		Detail: "View service health, topology, performance, traces, and logs.",
	}}
	if slices.Contains(strings.Fields(scope), dashboard.OAuthScope) {
		grants = append(grants, consentGrant{
			Title:  "Create and replace dashboards",
			Detail: "Write access: this application can add new dashboards and overwrite existing ones.",
		})
	}
	return grants
}

func validMCPScopes(scopes []string) bool {
	if !slices.Contains(scopes, mcpReadScope) {
		return false
	}
	for _, scope := range scopes {
		if !slices.Contains(mcpSupportedScopes, scope) {
			return false
		}
	}
	return true
}

func (h *MCPAuthorization) browserUser(c *echo.Context) (appauth.User, bool) {
	user := GetCurrentUser(c)
	if user == nil || user.ID == publicViewerID || !user.Active {
		return appauth.User{}, false
	}
	return *user, true
}

func (h *MCPAuthorization) Token(c *echo.Context) error {
	if err := c.Request().ParseForm(); err != nil {
		return oauthJSONError(c, http.StatusBadRequest, "invalid_request", "invalid token request")
	}
	grantType := c.Request().PostForm.Get("grant_type")
	clientID := c.Request().PostForm.Get("client_id")
	if clientID == "" {
		return oauthJSONError(c, http.StatusUnauthorized, "invalid_client", "client_id is required")
	}
	client, err := h.store.GetClient(c.Request().Context(), clientID)
	if err != nil {
		if !errors.Is(err, appauth.ErrOAuthClientNotFound) {
			slog.Error("oauth client lookup failed", "client_id", clientID, "err", err)
			return oauthJSONError(c, http.StatusInternalServerError, "server_error", "token exchange failed")
		}
		return oauthJSONError(c, http.StatusUnauthorized, "invalid_client", "unknown client")
	}
	if client.TokenEndpointAuthMethod != "none" {
		return oauthJSONError(c, http.StatusUnauthorized, "invalid_client", "unknown client")
	}

	var pair appauth.OAuthTokenPair
	switch grantType {
	case "authorization_code":
		pair, err = h.exchangeAuthorizationCode(c, clientID)
	case "refresh_token":
		resource := c.Request().PostForm.Get("resource")
		if resource == "" {
			resource = h.resource
		}
		if resource != h.resource {
			return oauthJSONError(c, http.StatusBadRequest, "invalid_target", "resource must identify this MCP server")
		}
		pair, err = h.store.RotateRefreshToken(c.Request().Context(), clientID, c.Request().PostForm.Get("refresh_token"), resource)
	default:
		return oauthJSONError(c, http.StatusBadRequest, "unsupported_grant_type", "unsupported grant type")
	}
	if err != nil {
		if errors.Is(err, appauth.ErrInvalidOAuthGrant) || errors.Is(err, appauth.ErrOAuthRefreshReuse) {
			return oauthJSONError(c, http.StatusBadRequest, "invalid_grant", "grant is invalid or expired")
		}
		slog.Error("oauth token exchange failed", "client_id", clientID, "grant_type", grantType, "err", err)
		return oauthJSONError(c, http.StatusInternalServerError, "server_error", "token exchange failed")
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("Pragma", "no-cache")
	return c.JSON(http.StatusOK, map[string]any{
		"access_token":  pair.AccessToken,
		"token_type":    "Bearer",
		"expires_in":    pair.ExpiresIn,
		"refresh_token": pair.RefreshToken,
		"scope":         pair.Scope,
	})
}

func (h *MCPAuthorization) exchangeAuthorizationCode(c *echo.Context, clientID string) (appauth.OAuthTokenPair, error) {
	code, err := h.store.ConsumeAuthorizationCode(c.Request().Context(), c.Request().PostForm.Get("code"))
	if err != nil {
		return appauth.OAuthTokenPair{}, err
	}
	if code.ClientID != clientID || code.RedirectURI != c.Request().PostForm.Get("redirect_uri") || code.Resource != c.Request().PostForm.Get("resource") {
		return appauth.OAuthTokenPair{}, appauth.ErrInvalidOAuthGrant
	}
	verifier := c.Request().PostForm.Get("code_verifier")
	challenge := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(challenge[:])
	if subtle.ConstantTimeCompare([]byte(want), []byte(code.CodeChallenge)) != 1 {
		return appauth.OAuthTokenPair{}, appauth.ErrInvalidOAuthGrant
	}
	user, err := h.users.GetByID(code.UserID)
	if errors.Is(err, appauth.ErrUserNotFound) {
		return appauth.OAuthTokenPair{}, appauth.ErrInvalidOAuthGrant
	}
	if err != nil {
		// DB failure is an infrastructure error, not an invalid grant.
		return appauth.OAuthTokenPair{}, fmt.Errorf("load code user: %w", err)
	}
	if !user.Active {
		return appauth.OAuthTokenPair{}, appauth.ErrInvalidOAuthGrant
	}
	return h.store.IssueTokenPair(c.Request().Context(), code.ClientID, code.UserID, code.Scope, code.Resource)
}

func validateRedirectURI(raw string) error {
	if len(raw) > 2048 {
		return fmt.Errorf("redirect URI is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("redirect URI must be an absolute URL without a fragment")
	}
	host := u.Hostname()
	loopback := strings.EqualFold(host, "localhost")
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		loopback = true
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return fmt.Errorf("redirect URI must use HTTPS or loopback HTTP")
	}
	return nil
}

func validS256Challenge(challenge string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(challenge)
	return err == nil && len(decoded) == sha256.Size
}

func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, value := range want {
		if !slices.Contains(got, value) {
			return false
		}
	}
	return true
}

func redirectOAuthSuccess(c *echo.Context, redirectURI, state, code string) error {
	u, _ := url.Parse(redirectURI)
	query := u.Query()
	query.Set("code", code)
	if state != "" {
		query.Set("state", state)
	}
	u.RawQuery = query.Encode()
	return c.Redirect(http.StatusFound, u.String())
}

func redirectOAuthError(c *echo.Context, redirectURI, state, code, description string) error {
	u, _ := url.Parse(redirectURI)
	query := u.Query()
	query.Set("error", code)
	query.Set("error_description", description)
	if state != "" {
		query.Set("state", state)
	}
	u.RawQuery = query.Encode()
	return c.Redirect(http.StatusFound, u.String())
}

func oauthJSONError(c *echo.Context, status int, code, description string) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("Pragma", "no-cache")
	return c.JSON(status, map[string]string{"error": code, "error_description": description})
}

var oauthConsentTemplate = template.Must(template.New("oauth-consent").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Authorize access · Fanout</title><style>
:root{color-scheme:dark;font-family:Inter,ui-sans-serif,system-ui,sans-serif;background:#090d0b;color:#eef4f0}
*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px;background:radial-gradient(circle at 50% 0,#18231d 0,#090d0b 52%)}
.card{width:min(100%,520px);padding:32px;border:1px solid #2a3931;border-radius:22px;background:#111713;box-shadow:0 28px 80px #0008}
.mark{display:grid;place-items:center;width:42px;height:42px;border-radius:12px;background:#627a47;color:#081008;font-weight:800;font-size:20px}
.eyebrow{margin:24px 0 8px;color:#8e9d94;font-size:12px;font-weight:700;letter-spacing:.16em;text-transform:uppercase}h1{margin:0;font-size:28px;line-height:1.2;letter-spacing:-.03em}
.lede{color:#a9b5ae;line-height:1.6}.detail{margin:24px 0;padding:18px;border:1px solid #29372f;border-radius:14px;background:#0d120f}.detail b{display:block;margin-bottom:5px}.detail span{color:#8e9d94;font-size:13px;overflow-wrap:anywhere}
.access{display:flex;gap:12px;margin:20px 0}.check{display:grid;place-items:center;flex:0 0 26px;height:26px;border-radius:50%;background:#1e392c;color:#71d6a3;font-weight:800}.access p{margin:2px 0;color:#a9b5ae;line-height:1.5}.signed{font-size:13px;color:#7f8c84}
.actions{display:grid;grid-template-columns:1fr 1.5fr;gap:12px;margin-top:28px}button{border:1px solid #314139;border-radius:12px;padding:13px 16px;background:#151d18;color:#edf3ef;font:inherit;font-weight:700;cursor:pointer}button.primary{border-color:#627a47;background:#627a47;color:#071007}button:hover{filter:brightness(1.08)}
@media(max-width:520px){.card{padding:24px}.actions{grid-template-columns:1fr}}
</style></head><body><main class="card"><div class="mark">F</div><p class="eyebrow">Secure connection</p><h1>Allow {{.ClientName}} to access Fanout?</h1>
<p class="lede">{{if .ReadOnly}}This application is requesting read-only access to your observability data through MCP. It cannot change settings, alerts, or dashboards.{{else}}This application is requesting the access listed below through MCP.{{end}}</p>
<div class="detail"><b>{{.ClientName}}</b><span>Returns to {{.Redirect}}</span><span>Scopes: {{.Scope}}</span></div>
{{range .Grants}}<div class="access"><span class="check">✓</span><div><b>{{.Title}}</b><p>{{.Detail}}</p></div></div>
{{end}}<p class="signed">Signed in as {{.Email}}</p>
<form method="post" action="/api/auth/oauth/authorize">
<input type="hidden" name="response_type" value="{{.Request.ResponseType}}"><input type="hidden" name="client_id" value="{{.Request.ClientID}}"><input type="hidden" name="redirect_uri" value="{{.Request.RedirectURI}}"><input type="hidden" name="scope" value="{{.Scope}}"><input type="hidden" name="state" value="{{.Request.State}}"><input type="hidden" name="code_challenge" value="{{.Request.CodeChallenge}}"><input type="hidden" name="code_challenge_method" value="{{.Request.CodeMethod}}"><input type="hidden" name="resource" value="{{.Request.Resource}}">
<div class="actions"><button name="decision" value="deny">Cancel</button><button class="primary" name="decision" value="approve">Allow access</button></div></form></main></body></html>`))
