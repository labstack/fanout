package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/labstack/echo/v5"
	"golang.org/x/oauth2"

	appauth "github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
)

const oidcFlowTTL = 10 * time.Minute

type OIDCHandler struct {
	cfg        config.Config
	provider   *oidc.Provider
	verifier   *oidc.IDTokenVerifier
	oauth      oauth2.Config
	users      *appauth.UserStore
	identities *appauth.IdentityStore
	sessions   *appauth.BrowserSessions
	audit      *appauth.AuditStore
	limiter    *appauth.KeyedLimiter
}

func RegisterOIDCRoutes(ctx context.Context, e *echo.Echo, cfg config.Config, users *appauth.UserStore, identities *appauth.IdentityStore, sessions *appauth.BrowserSessions, audit *appauth.AuditStore) error {
	if strings.ToLower(strings.TrimSpace(cfg.AuthMode)) != "oidc" {
		return nil
	}
	if users == nil || identities == nil || sessions == nil || audit == nil {
		return fmt.Errorf("OIDC dependencies are required")
	}
	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuerURL)
	if err != nil {
		return fmt.Errorf("discover OIDC provider: %w", err)
	}
	callback := strings.TrimRight(cfg.PublicURL, "/") + "/api/auth/oidc/callback"
	h := &OIDCHandler{
		cfg: cfg, provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID}),
		oauth: oauth2.Config{
			ClientID: cfg.OIDCClientID, ClientSecret: cfg.OIDCClientSecret,
			Endpoint: provider.Endpoint(), RedirectURL: callback,
			Scopes: []string{oidc.ScopeOpenID, "profile", "email", "groups"},
		},
		users: users, identities: identities, sessions: sessions, audit: audit,
		limiter: appauth.NewKeyedLimiter(30, 15*time.Minute),
	}
	e.GET("/api/auth/oidc/start", h.Start)
	e.GET("/api/auth/oidc/callback", h.Callback)
	return nil
}

func (h *OIDCHandler) Start(c *echo.Context) error {
	if !h.limiter.Allow(c.RealIP()) {
		return rateLimited(c, 15*time.Minute)
	}
	state, err := randomURLToken(32)
	if err != nil {
		slog.Error("OIDC state generation failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to start login")
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		slog.Error("OIDC nonce generation failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to start login")
	}
	verifier, err := randomURLToken(48)
	if err != nil {
		slog.Error("OIDC PKCE verifier generation failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to start login")
	}
	ctx := c.Request().Context()
	returnTo := safeReturnTo(c.QueryParam("return_to"))
	if err := h.sessions.BeginOIDCSession(ctx, oidcFlowTTL, oidcFlowTTL, state, nonce, verifier, returnTo); err != nil {
		slog.Error("OIDC pre-authentication session creation failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to start login")
	}
	challenge := sha256.Sum256([]byte(verifier))
	redirect := h.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:])),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	return c.Redirect(http.StatusFound, redirect)
}

type oidcClaims struct {
	Subject       string   `json:"sub"`
	Email         string   `json:"email"`
	EmailVerified *bool    `json:"email_verified"`
	Nonce         string   `json:"nonce"`
	Groups        []string `json:"groups"`
}

func (h *OIDCHandler) Callback(c *echo.Context) error {
	ctx := c.Request().Context()
	state, expectedNonce, pkceVerifier, returnTo := h.sessions.OIDCFlow(ctx)
	if state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(c.QueryParam("state"))) != 1 {
		return h.oidcDenied(c, "invalid state")
	}
	// A callback consumes the flow even when the provider or token exchange
	// fails, preventing state, nonce, and verifier replay.
	h.sessions.ClearOIDCFlow(ctx)
	if providerError := c.QueryParam("error"); providerError != "" {
		return h.oidcDenied(c, "identity provider denied login")
	}
	token, err := h.oauth.Exchange(ctx, c.QueryParam("code"), oauth2.VerifierOption(pkceVerifier))
	if err != nil {
		return h.oidcDenied(c, "code exchange failed")
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return h.oidcDenied(c, "missing ID token")
	}
	idToken, err := h.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return h.oidcDenied(c, "invalid ID token")
	}
	var claims oidcClaims
	if err := idToken.Claims(&claims); err != nil || claims.Subject == "" {
		return h.oidcDenied(c, "invalid identity claims")
	}
	if h.cfg.OIDCEmailClaim != "" && h.cfg.OIDCEmailClaim != "email" {
		var dynamic map[string]json.RawMessage
		if err := idToken.Claims(&dynamic); err != nil {
			return h.oidcDenied(c, "invalid identity claims")
		}
		email, err := configuredOIDCEmail(dynamic, h.cfg.OIDCEmailClaim)
		if err != nil {
			slog.Error("OIDC configured email claim rejected", "claim", h.cfg.OIDCEmailClaim, "err", err)
			return h.oidcDenied(c, err.Error())
		}
		claims.Email = email
	}
	if expectedNonce == "" || subtle.ConstantTimeCompare([]byte(expectedNonce), []byte(claims.Nonce)) != 1 {
		return h.oidcDenied(c, "invalid nonce")
	}

	user, identity, err := h.resolveUser(ctx, idToken.Issuer, claims)
	if err != nil {
		slog.Warn("OIDC login denied", "issuer", idToken.Issuer, "subject", claims.Subject, "err", err)
		return h.oidcDenied(c, err.Error())
	}
	if !user.Active {
		return h.oidcDenied(c, "user is inactive")
	}
	if err := h.identities.TouchLogin(ctx, identity.ID); err != nil {
		slog.Error("OIDC identity login timestamp update failed", "identity_id", identity.ID, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to complete login")
	}
	if err := h.users.TouchLogin(user.ID); err != nil {
		slog.Error("OIDC user login timestamp update failed", "user_id", user.ID, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to complete login")
	}
	if err := h.sessions.EstablishAuthenticatedSession(ctx, user); err != nil {
		slog.Error("OIDC authenticated session establishment failed", "user_id", user.ID, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create session")
	}
	returnTo = safeReturnTo(returnTo)
	if returnTo == "" {
		returnTo = "/"
	}
	auditCtx, cancel := appauth.DetachedWriteContext(ctx)
	defer cancel()
	if err := h.audit.Record(auditCtx, appauth.AuditEvent{ActorUserID: user.ID, EventType: "login.succeeded", Outcome: "success", TargetType: "identity", TargetID: identity.ID, RemoteIP: c.RealIP(), UserAgent: c.Request().UserAgent(), Metadata: map[string]any{"mode": "oidc", "issuer": idToken.Issuer}}); err != nil {
		slog.Error("OIDC audit write failed", "err", err)
	}
	return c.Redirect(http.StatusFound, returnTo)
}

func configuredOIDCEmail(claims map[string]json.RawMessage, name string) (string, error) {
	raw, ok := claims[name]
	if !ok {
		return "", errors.New("configured email claim is absent")
	}
	var email string
	if err := json.Unmarshal(raw, &email); err != nil || strings.TrimSpace(email) == "" {
		return "", errors.New("configured email claim is not a non-empty string")
	}
	return email, nil
}

func (h *OIDCHandler) resolveUser(ctx context.Context, issuer string, claims oidcClaims) (appauth.User, appauth.UserIdentity, error) {
	identity, err := h.identities.Find(ctx, issuer, claims.Subject)
	if err == nil {
		user, userErr := h.users.GetByIDContext(ctx, identity.UserID)
		return user, identity, userErr
	}
	if !errors.Is(err, appauth.ErrUserNotFound) {
		return appauth.User{}, appauth.UserIdentity{}, err
	}
	email, err := appauth.NormalizeEmail(claims.Email)
	if err != nil {
		return appauth.User{}, appauth.UserIdentity{}, errors.New("OIDC provider did not supply a usable email")
	}
	if h.cfg.OIDCEmailVerification == "required" && (claims.EmailVerified == nil || !*claims.EmailVerified) {
		return appauth.User{}, appauth.UserIdentity{}, errors.New("OIDC email is not verified")
	}
	user, err := h.users.GetByEmailContext(ctx, email)
	if errors.Is(err, appauth.ErrUserNotFound) {
		if !h.cfg.OIDCAutoProvision || !h.provisionAllowed(email, claims.Groups) {
			return appauth.User{}, appauth.UserIdentity{}, errors.New("identity is not provisioned")
		}
		count, countErr := h.users.CountUsers()
		if countErr != nil {
			return appauth.User{}, appauth.UserIdentity{}, countErr
		}
		if count == 0 {
			return appauth.User{}, appauth.UserIdentity{}, errors.New("first administrator setup is required")
		}
		user, err = h.users.CreateWithAudit(email, "", h.provisionRole(claims.Groups), appauth.AuditEvent{EventType: "user.provisioned", Outcome: "success", Metadata: map[string]any{"mode": "oidc"}})
	}
	if err != nil {
		return appauth.User{}, appauth.UserIdentity{}, err
	}
	if !user.Active {
		return appauth.User{}, appauth.UserIdentity{}, errors.New("user is inactive")
	}
	if h.cfg.OIDCEmailVerification == "issuer" && !h.provisionAllowed(email, claims.Groups) {
		return appauth.User{}, appauth.UserIdentity{}, errors.New("identity does not satisfy the issuer-mode allow policy")
	}
	linked, err := h.identities.CountForUser(ctx, user.ID)
	if err != nil {
		return appauth.User{}, appauth.UserIdentity{}, err
	}
	if linked != 0 {
		return appauth.User{}, appauth.UserIdentity{}, errors.New("user already has a linked identity")
	}
	identity, err = h.identities.LinkWithAudit(ctx, user.ID, issuer, claims.Subject, email, h.audit, appauth.AuditEvent{ActorUserID: user.ID, EventType: "identity.linked", Outcome: "success", Metadata: map[string]any{"issuer": issuer}})
	if err != nil {
		if !errors.Is(err, appauth.ErrIdentityConflict) {
			return appauth.User{}, appauth.UserIdentity{}, err
		}
		// A concurrent first login may have created the same link. Only that
		// specific uniqueness race is safe to resolve by reading it back.
		identity, err = h.identities.Find(ctx, issuer, claims.Subject)
		if err != nil || identity.UserID != user.ID {
			return appauth.User{}, appauth.UserIdentity{}, errors.New("identity linking failed")
		}
	}
	return user, identity, nil
}

func (h *OIDCHandler) provisionAllowed(email string, groups []string) bool {
	if intersects(groups, csvValues(h.cfg.OIDCAllowedGroups)) {
		return true
	}
	parts := strings.Split(email, "@")
	return len(parts) == 2 && slices.Contains(csvValues(h.cfg.OIDCAllowedDomains), strings.ToLower(parts[1]))
}

func (h *OIDCHandler) provisionRole(groups []string) appauth.Role {
	if intersects(groups, csvValues(h.cfg.OIDCAdminGroups)) {
		return appauth.RoleAdmin
	}
	if intersects(groups, csvValues(h.cfg.OIDCOperatorGroups)) {
		return appauth.RoleOperator
	}
	return appauth.Role(h.cfg.OIDCDefaultRole)
}

func (h *OIDCHandler) oidcDenied(c *echo.Context, reason string) error {
	ctx, cancel := appauth.DetachedWriteContext(c.Request().Context())
	defer cancel()
	if err := h.audit.Record(ctx, appauth.AuditEvent{EventType: "oidc.denied", Outcome: "denied", RemoteIP: c.RealIP(), UserAgent: c.Request().UserAgent(), Metadata: map[string]any{"reason": reason}}); err != nil {
		slog.Error("OIDC denial audit write failed", "err", err)
	}
	return echo.NewHTTPError(http.StatusUnauthorized, "OIDC login denied")
}

func randomURLToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func csvValues(raw string) []string {
	values := []string{}
	for _, value := range strings.Split(raw, ",") {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func intersects(actual, allowed []string) bool {
	for _, value := range actual {
		if slices.Contains(allowed, strings.ToLower(strings.TrimSpace(value))) {
			return true
		}
	}
	return false
}

func safeReturnTo(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" {
		return ""
	}
	return u.RequestURI()
}
