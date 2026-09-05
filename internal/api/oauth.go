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
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/dashboard"

	appauth "github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/brand"
	mcpgoauth "github.com/modelcontextprotocol/go-sdk/auth"
)

const mcpReadScope = appauth.MCPScopeTelemetryRead

const browserMCPSessionBearer = "fanout-browser-session"

type browserMCPUserContextKey struct{}

var mcpSupportedScopes = []string{mcpReadScope, dashboard.OAuthScope}

type MCPAuthorization struct {
	store           *appauth.OAuthStore
	users           *appauth.UserStore
	resource        string
	issuer          string
	metadataURL     string
	allowedHost     string
	registerLimiter *appauth.KeyedLimiter
}

func NewMCPAuthorization(store *appauth.OAuthStore, users *appauth.UserStore, resourceURL string) (*MCPAuthorization, error) {
	if store == nil || users == nil {
		return nil, fmt.Errorf("MCP authorization dependencies are required")
	}
	resource, err := url.Parse(resourceURL)
	if err != nil || resource.Scheme != "https" || resource.Host == "" || resource.Path != "/mcp" {
		return nil, fmt.Errorf("invalid MCP resource URL")
	}
	issuer := resource.Scheme + "://" + resource.Host
	return &MCPAuthorization{
		store:       store,
		users:       users,
		resource:    resource.String(),
		issuer:      issuer,
		metadataURL: issuer + "/.well-known/oauth-protected-resource/mcp",
		allowedHost: resource.Host,
		// Registration is unauthenticated by design (RFC 7591 dynamic
		// registration). Bound how many clients one source can create before
		// the janitor collects the abandoned ones. The budget is per client
		// IP, which collapses to one bucket when Fanout sits behind a proxy
		// and server.trusted_proxy_cidrs is unset, so it is set well above a
		// plausible team rollout rather than at the tightest defensible value.
		registerLimiter: appauth.NewKeyedLimiter(60, 15*time.Minute),
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
	scopeChecked := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requiredScope := requiredMCPToolScope(r)
		if requiredScope != "" {
			info := mcpgoauth.TokenInfoFromContext(r.Context())
			if info == nil || !slices.Contains(info.Scopes, requiredScope) {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(
					`Bearer error="insufficient_scope", scope=%q, resource_metadata=%q`,
					requiredScope,
					h.metadataURL,
				))
				http.Error(w, "insufficient scope", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
	protected := mcpgoauth.RequireBearerToken(h.verifyMCPToken, &mcpgoauth.RequireBearerTokenOptions{
		ResourceMetadataURL: h.metadataURL,
		Scopes:              []string{mcpReadScope},
	})(scopeChecked)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(strings.TrimSpace(r.Host), h.allowedHost) {
			http.Error(w, "MCP request host does not match the configured public URL", http.StatusMisdirectedRequest)
			return
		}
		protected.ServeHTTP(w, r)
	})
}

func requiredMCPToolScope(r *http.Request) string {
	if r.Header.Get("Mcp-Method") != "tools/call" {
		return ""
	}
	switch r.Header.Get("Mcp-Name") {
	case "dashboard_list", "dashboard_get", "dashboard_create", "dashboard_update":
		return dashboard.OAuthScope
	default:
		return ""
	}
}

// ProtectBrowserMCP adapts an already-authenticated browser session to the
// standard MCP transport identity consumed by the SDK. The public /mcp route
// remains OAuth bearer-only; this adapter is used only by the same-origin
// /api/mcp route after AuthMiddleware has validated the session and CSRF
// header.
func ProtectBrowserMCP(sessions *appauth.BrowserSessions, next http.Handler) echo.HandlerFunc {
	if sessions == nil || next == nil {
		panic("api: browser MCP dependencies are required")
	}
	protected := mcpgoauth.RequireBearerToken(func(ctx context.Context, raw string, _ *http.Request) (*mcpgoauth.TokenInfo, error) {
		user, ok := ctx.Value(browserMCPUserContextKey{}).(appauth.User)
		if raw != browserMCPSessionBearer || !ok || user.ID == "" {
			return nil, mcpgoauth.ErrInvalidToken
		}
		scopes := []string{mcpReadScope}
		if HasCapability(user, ManageOwnDashboards) {
			scopes = append(scopes, dashboard.OAuthScope)
		}
		return &mcpgoauth.TokenInfo{
			Scopes:     scopes,
			Expiration: sessions.Deadline(ctx),
			UserID:     user.ID,
			Extra:      map[string]any{"credential": "browser_session"},
		}, nil
	}, nil)(next)

	return func(c *echo.Context) error {
		user := GetCurrentUser(c)
		if user == nil {
			return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
		}
		ctx := context.WithValue(c.Request().Context(), browserMCPUserContextKey{}, *user)
		request := c.Request().Clone(ctx)
		request.Header.Set("Authorization", "Bearer "+browserMCPSessionBearer)
		protected.ServeHTTP(c.Response(), request)
		return nil
	}
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
	if !userCanUseMCPScopes(user, record.Scope) {
		return nil, mcpgoauth.ErrInvalidToken
	}
	return &mcpgoauth.TokenInfo{
		Scopes:     strings.Fields(record.Scope),
		Expiration: record.ExpiresAt,
		UserID:     record.UserID,
		Extra: map[string]any{
			"client_id": record.ClientID,
		},
	}, nil
}

func (h *MCPAuthorization) ProtectedResourceMetadata(c *echo.Context) error {
	setDiscoveryHeaders(c)
	return c.JSON(http.StatusOK, map[string]any{
		"resource":                 h.resource,
		"resource_name":            "Fanout Observability",
		"authorization_servers":    []string{h.issuer},
		"scopes_supported":         mcpSupportedScopes,
		"bearer_methods_supported": []string{"header"},
	})
}

func (h *MCPAuthorization) AuthorizationServerMetadata(c *echo.Context) error {
	setDiscoveryHeaders(c)
	return c.JSON(http.StatusOK, map[string]any{
		"issuer":                                         h.issuer,
		"authorization_endpoint":                         h.issuer + "/api/auth/oauth/authorize",
		"token_endpoint":                                 h.issuer + "/oauth/token",
		"registration_endpoint":                          h.issuer + "/oauth/register",
		"scopes_supported":                               mcpSupportedScopes,
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"code_challenge_methods_supported":               []string{"S256"},
		"authorization_response_iss_parameter_supported": true,
		"client_id_metadata_document_supported":          false,
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
	if !h.registerLimiter.Allow(c.RealIP()) {
		c.Response().Header().Set("Retry-After", "900")
		return oauthJSONError(c, http.StatusTooManyRequests, "temporarily_unavailable", "too many client registrations")
	}
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
			return h.redirectOAuthError(c, req.RedirectURI, req.State, errorCode, description)
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
	grantedScope := authorizationScope(req.Scope)
	if !userCanUseMCPScopes(user, grantedScope) {
		return h.redirectOAuthError(c, req.RedirectURI, req.State, "invalid_scope", "requested scope is not available to this account")
	}
	if c.Request().Method == http.MethodGet {
		redirectOrigin, formActionSource, err := redirectURIOrigin(req.RedirectURI)
		if err != nil {
			slog.Error("registered OAuth redirect URI is invalid", "client_id", req.ClientID, "err", err)
			return oauthJSONError(c, http.StatusInternalServerError, "server_error", "authorization failed")
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		// Chromium applies form-action across the redirect after this
		// same-origin form POST. Permit the exact validated callback origin
		// where CSP can express it. CSP host-source cannot represent bracketed
		// IPv6 literals, routable or loopback, so use the callback's validated
		// scheme as the tightest working fallback.
		c.Response().Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self' "+formActionSource+"; base-uri 'none'; frame-ancestors 'none'")
		c.Response().Header().Set("Content-Type", "text/html; charset=utf-8")
		return oauthConsentTemplate.Execute(c.Response(), map[string]any{
			"ClientName": client.ClientName,
			"Redirect":   redirectOrigin,
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
		return h.redirectOAuthError(c, req.RedirectURI, req.State, "access_denied", "authorization was denied")
	}
	code, err := h.store.CreateAuthorizationCode(c.Request().Context(), appauth.OAuthAuthorizationCode{
		ClientID:      req.ClientID,
		UserID:        user.ID,
		RedirectURI:   req.RedirectURI,
		Scope:         grantedScope,
		Resource:      h.resource,
		CodeChallenge: req.CodeChallenge,
	})
	if err != nil {
		slog.Error("oauth authorization code creation failed", "client_id", req.ClientID, "user_id", user.ID, "err", err)
		return oauthJSONError(c, http.StatusInternalServerError, "server_error", "authorization failed")
	}
	slog.Info("oauth authorization approved", "client_id", req.ClientID, "user_id", user.ID, "scope", grantedScope)
	return h.redirectOAuthSuccess(c, req.RedirectURI, req.State, code)
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
	canonical, _ := appauth.CanonicalMCPOAuthScope(requested)
	return canonical
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
	_, ok := appauth.CanonicalMCPOAuthScope(strings.Join(scopes, " "))
	return ok
}

func userCanUseMCPScopes(user appauth.User, raw string) bool {
	canonical, ok := appauth.CanonicalMCPOAuthScope(raw)
	if !ok {
		return false
	}
	for _, scope := range strings.Fields(canonical) {
		switch scope {
		case mcpReadScope:
			if !HasCapability(user, ReadTelemetry) {
				return false
			}
		case dashboard.OAuthScope:
			if !HasCapability(user, ManageOwnDashboards) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (h *MCPAuthorization) browserUser(c *echo.Context) (appauth.User, bool) {
	user := GetCurrentUser(c)
	if user == nil || !user.Active {
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
		if _, present := c.Request().PostForm["scope"]; present {
			return oauthJSONError(c, http.StatusBadRequest, "invalid_request", "scope is not allowed for an authorization_code grant")
		}
		pair, err = h.exchangeAuthorizationCode(c, clientID)
	case "refresh_token":
		resource := c.Request().PostForm.Get("resource")
		if resource == "" {
			resource = h.resource
		}
		if resource != h.resource {
			return oauthJSONError(c, http.StatusBadRequest, "invalid_target", "resource must identify this MCP server")
		}
		pair, err = h.store.RotateRefreshToken(
			c.Request().Context(),
			clientID,
			c.Request().PostForm.Get("refresh_token"),
			resource,
			c.Request().PostForm.Get("scope"),
		)
	default:
		return oauthJSONError(c, http.StatusBadRequest, "unsupported_grant_type", "unsupported grant type")
	}
	if err != nil {
		if errors.Is(err, appauth.ErrInvalidOAuthScope) {
			return oauthJSONError(c, http.StatusBadRequest, "invalid_scope", "requested scope exceeds the original grant")
		}
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
	_, _, err := redirectURIOrigin(raw)
	return err
}

func redirectURIOrigin(raw string) (string, string, error) {
	if len(raw) > 2048 {
		return "", "", fmt.Errorf("redirect URI is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", "", fmt.Errorf("redirect URI must be an absolute URL without a fragment")
	}
	host := u.Hostname()
	loopback := strings.EqualFold(host, "localhost")
	ip := net.ParseIP(host)
	if ip != nil {
		loopback = loopback || ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", "", fmt.Errorf("redirect URI must use HTTPS or loopback HTTP")
	}

	// Only emit a canonical host-source accepted by CSP. This also prevents a
	// syntactically parseable but hostile authority from injecting directives.
	// Preserve registered IP spelling in the origin shown on the consent card:
	// net.IP.String would collapse IPv4-mapped IPv6 to IPv4 and misrepresent
	// the client's registered callback.
	originHost := strings.ToLower(host)
	if ip == nil {
		for _, char := range originHost {
			if (char < 'a' || char > 'z') &&
				(char < '0' || char > '9') &&
				char != '.' && char != '-' {
				return "", "", fmt.Errorf("redirect URI host must be an IP address or ASCII hostname (use punycode for internationalized domains)")
			}
		}
	}
	if originHost == "" {
		return "", "", fmt.Errorf("redirect URI must include a host")
	}

	port := u.Port()
	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", "", fmt.Errorf("redirect URI port is invalid")
		}
		originHost = net.JoinHostPort(originHost, port)
	} else if ip != nil && strings.Contains(originHost, ":") {
		originHost = "[" + originHost + "]"
	}
	origin := u.Scheme + "://" + originHost
	formActionSource := origin
	if strings.Contains(host, ":") {
		formActionSource = u.Scheme + ":"
	}
	return origin, formActionSource, nil
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

func (h *MCPAuthorization) redirectOAuthSuccess(c *echo.Context, redirectURI, state, code string) error {
	u, _ := url.Parse(redirectURI)
	query := u.Query()
	query.Set("code", code)
	query.Set("iss", h.issuer)
	if state != "" {
		query.Set("state", state)
	}
	u.RawQuery = query.Encode()
	return c.Redirect(http.StatusFound, u.String())
}

func (h *MCPAuthorization) redirectOAuthError(c *echo.Context, redirectURI, state, code, description string) error {
	u, _ := url.Parse(redirectURI)
	query := u.Query()
	query.Set("error", code)
	query.Set("error_description", description)
	query.Set("iss", h.issuer)
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
:root{color-scheme:light dark;font-family:Inter,ui-sans-serif,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#fff;color:#1f2923}
*{box-sizing:border-box}html,body{width:100%}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px;overflow-x:hidden;background:radial-gradient(circle at 50% -12%,rgba(70,192,142,.12),transparent 38%),linear-gradient(180deg,#f8faf9,#fff 62%)}
.card{width:100%;max-width:520px;min-width:0;padding:40px;border:1px solid rgba(31,41,55,.08);border-radius:28px;background:rgba(255,255,255,.92);box-shadow:0 28px 70px rgba(31,41,55,.10),0 3px 10px rgba(31,41,55,.04);backdrop-filter:blur(18px)}
.brand{display:flex;align-items:center;gap:14px}.brand-mark{display:block;width:46px;height:46px}.brand-name{font-size:18px;font-weight:800;line-height:1;letter-spacing:.17em;text-transform:uppercase}
.eyebrow{margin:28px 0 8px;color:#087f5b;font-size:12px;font-weight:700;letter-spacing:.12em;text-transform:uppercase}h1{margin:0;font-size:34px;font-weight:650;line-height:1.08;letter-spacing:-.035em}
.lede{margin:16px 0 0;color:#66736c;line-height:1.6}.detail{display:grid;gap:5px;margin:24px 0;padding:18px;border:1px solid #e1e7e3;border-radius:14px;background:#f7f9f8}.detail b{display:block}.detail span{color:#748078;font-size:13px;overflow-wrap:anywhere}.detail .warn{color:#b8621b;font-weight:600}
.access{display:flex;gap:12px;margin:20px 0}.access>div{min-width:0}.check{display:grid;place-items:center;flex:0 0 26px;height:26px;border-radius:50%;background:#e6fcf5;color:#087f5b;font-weight:800}.access p{margin:2px 0;color:#66736c;line-height:1.5;overflow-wrap:anywhere}.signed{font-size:13px;color:#748078}
.actions{display:grid;grid-template-columns:1fr 1.5fr;gap:12px;margin-top:28px}button{border:1px solid #d7ddd9;border-radius:12px;padding:13px 16px;background:#fff;color:#26322b;font:inherit;font-weight:700;cursor:pointer}button.primary{border-color:#0ca678;background:#0ca678;color:#fff}button:hover{filter:brightness(.97)}button:focus-visible{outline:3px solid rgba(12,166,120,.25);outline-offset:2px}
@media(prefers-color-scheme:dark){:root{background:#101512;color:#eef4f0}body{background:radial-gradient(circle at 50% -12%,rgba(70,192,142,.14),transparent 38%),#101512}.card{border-color:#303a35;background:rgba(27,33,30,.94);box-shadow:0 28px 70px #0007}.eyebrow{color:#63e6be}.lede,.access p{color:#aab5af}.detail{border-color:#38443e;background:#151b18}.detail span,.signed{color:#8c9892}.detail .warn{color:#f0a868}.check{background:#153b2e;color:#63e6be}button{border-color:#3b4741;background:#232b27;color:#eef4f0}button.primary{border-color:#20c997;background:#20c997;color:#07140f}}
@media(max-width:520px){body{display:flex;align-items:center;justify-content:center;padding:16px}.card{width:calc(100vw - 32px);max-width:calc(100vw - 32px);padding:28px 24px}.actions{grid-template-columns:1fr}h1{font-size:30px}}
</style></head><body><main class="card"><div class="brand"><span class="brand-mark">` + brand.MarkSVG + `</span><span class="brand-name">Fanout</span></div><p class="eyebrow">Secure connection</p><h1>Allow {{.ClientName}} to access Fanout?</h1>
<p class="lede">{{if .ReadOnly}}This application is requesting read-only access to your observability data through MCP. It cannot change settings, alerts, or dashboards.{{else}}This application is requesting the access listed below through MCP.{{end}}</p>
<div class="detail"><b>{{.ClientName}}</b><span class="warn">Unverified client — this name was chosen by whoever registered it</span><span>Returns to <b>{{.Redirect}}</b></span><span>Scopes: {{.Scope}}</span></div>
{{range .Grants}}<div class="access"><span class="check">✓</span><div><b>{{.Title}}</b><p>{{.Detail}}</p></div></div>
{{end}}<p class="signed">Signed in as {{.Email}}</p>
<form method="post" action="/api/auth/oauth/authorize">
<input type="hidden" name="response_type" value="{{.Request.ResponseType}}"><input type="hidden" name="client_id" value="{{.Request.ClientID}}"><input type="hidden" name="redirect_uri" value="{{.Request.RedirectURI}}"><input type="hidden" name="scope" value="{{.Scope}}"><input type="hidden" name="state" value="{{.Request.State}}"><input type="hidden" name="code_challenge" value="{{.Request.CodeChallenge}}"><input type="hidden" name="code_challenge_method" value="{{.Request.CodeMethod}}"><input type="hidden" name="resource" value="{{.Request.Resource}}">
<div class="actions"><button name="decision" value="deny">Cancel</button><button class="primary" name="decision" value="approve">Allow access</button></div></form></main></body></html>`))
