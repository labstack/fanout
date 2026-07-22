package api

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/settings"
)

type AuthHandler struct {
	users         *auth.UserStore
	codes         *auth.CodeStore
	setup         *auth.Setup
	settings      *settings.Store
	sessions      *auth.BrowserSessions
	audit         *auth.AuditStore
	smtp          auth.SMTPConfig
	cfg           env.Config
	setupLimiter  *auth.KeyedLimiter
	startLimiter  *auth.KeyedLimiter
	verifyLimiter *auth.KeyedLimiter
}

func RegisterAuthRoutes(e *echo.Echo, users *auth.UserStore, codes *auth.CodeStore, setup *auth.Setup, settingsStore *settings.Store, sessions *auth.BrowserSessions, audit *auth.AuditStore, smtp auth.SMTPConfig, cfg env.Config) {
	if users == nil || codes == nil || setup == nil || settingsStore == nil || sessions == nil || audit == nil {
		panic("api: auth route dependencies are required")
	}
	if cfg.AuthMode == "" {
		cfg.AuthMode = "local"
	}
	h := &AuthHandler{
		users:         users,
		codes:         codes,
		setup:         setup,
		settings:      settingsStore,
		sessions:      sessions,
		audit:         audit,
		smtp:          smtp,
		cfg:           cfg,
		setupLimiter:  auth.NewKeyedLimiter(10, 15*time.Minute),
		startLimiter:  auth.NewKeyedLimiter(20, 15*time.Minute),
		verifyLimiter: auth.NewKeyedLimiter(30, 15*time.Minute),
	}

	e.GET("/api/auth/status", h.Status)
	e.POST("/api/auth/setup", h.Setup)
	e.POST("/api/auth/start", h.Start)
	e.POST("/api/auth/verify", h.Verify)
	e.GET("/api/auth/me", h.Me)
	e.POST("/api/auth/logout", h.Logout)
}

func jitter() {
	time.Sleep(time.Duration(50+rand.IntN(100)) * time.Millisecond)
}

func (h *AuthHandler) Status(c *echo.Context) error {
	count, err := h.users.CountUsers()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to check auth status")
	}
	return c.JSON(200, map[string]any{
		"setup_required": count == 0,
		"auth_enabled":   true,
		"auth_mode":      strings.ToLower(strings.TrimSpace(h.cfg.AuthMode)),
		// Lets the SPA boot anonymously into read-only mode: when true, an
		// unauthenticated GET /api/auth/me returns the synthetic viewer, so the
		// frontend can render the explicitly public telemetry views without a login.
		"public_read": h.cfg.PublicRead,
	})
}

func (h *AuthHandler) Setup(c *echo.Context) error {
	if !h.setupLimiter.Allow(c.RealIP()) {
		return rateLimited(c, 15*time.Minute)
	}
	var req struct {
		Email      string `json:"email"`
		Name       string `json:"name"`
		SetupToken string `json:"setup_token"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "email is required")
	}
	switch h.setup.Verify(req.SetupToken) {
	case auth.SetupStatusOK:
	case auth.SetupStatusExpired:
		slog.Warn("auth: setup rejected", "reason", "expired")
		jitter()
		return echo.NewHTTPError(http.StatusGone, "setup token expired; restart Fanout to generate a new one")
	case auth.SetupStatusUnset:
		slog.Warn("auth: setup rejected", "reason", "unset")
		jitter()
		return echo.NewHTTPError(http.StatusGone, "setup window is closed; restart Fanout to reopen it")
	default:
		slog.Warn("auth: setup rejected", "reason", "wrong")
		jitter()
		return echo.NewHTTPError(http.StatusForbidden, "invalid setup token")
	}

	email, err := auth.NormalizeEmail(req.Email)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	createEvent := auth.AuditEvent{EventType: "setup.completed", Outcome: "success", RemoteIP: c.RealIP(), UserAgent: c.Request().UserAgent()}
	user, err := h.users.CreateFirstAdminWithAudit(email, req.Name, createEvent)
	if errors.Is(err, auth.ErrSetupComplete) {
		user, err = h.users.GetByEmail(email)
		if err != nil {
			slog.Error("auth: setup retry lookup failed", "email", email, "err", err)
			return echo.NewHTTPError(http.StatusForbidden, "setup already complete")
		}
		if !user.Active || user.Role != "admin" {
			slog.Warn("auth: setup retry user not eligible", "email", email, "active", user.Active, "role", user.Role)
			return echo.NewHTTPError(http.StatusForbidden, "setup already complete")
		}
	}
	if err != nil {
		slog.Error("auth: setup create admin failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create admin")
	}

	if err := h.establishSession(c, user); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create session")
	}

	// Generate the ingest token only if one doesn't exist yet. A Setup retry
	// (ErrSetupComplete branch above) must not rotate and invalidate a token
	// live collectors may already be using — to rotate deliberately, the admin
	// uses the Settings page.
	resp := map[string]string{"status": "authenticated"}
	current, err := h.settings.GetIngest(c.Request().Context())
	if err != nil {
		slog.Error("auth: setup load ingest config failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to load ingest config")
	}
	if current.TokenHash == "" {
		ingestToken, ingestHash, err := settings.GenerateIngestToken()
		if err != nil {
			slog.Error("auth: setup generate ingest token failed", "err", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to generate ingest token")
		}
		ingestEvent := auth.AuditEvent{ActorUserID: user.ID, EventType: "ingest_key.rotated", Outcome: "success", TargetType: "ingest", TargetID: "default", RemoteIP: c.RealIP(), UserAgent: c.Request().UserAgent(), Metadata: map[string]any{"source": "setup"}}
		if err := h.settings.SetIngestWithAudit(c.Request().Context(), settings.Ingest{TokenHash: ingestHash}, h.audit, ingestEvent); err != nil {
			slog.Error("auth: setup persist ingest token failed", "err", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to persist ingest token")
		}
		resp["ingest_token"] = ingestToken
		resp["ingest_header_name"] = "x-fanout-ingest-token"
		// The endpoint collectors should actually use. Behind a reverse proxy
		// this is the public TLS host (e.g. https://ingest.fanout.labstack.com),
		// NOT the internal :4317 — see suggestedIngestEndpoint.
		resp["suggested_endpoint"] = suggestedIngestEndpoint(c.Request(), h.cfg.OTLPGRPCAddr, h.cfg.IngestEndpoint)
	}

	slog.Info("auth: first admin setup completed", "email", email)
	h.setup.Clear()
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(200, resp)
}

func (h *AuthHandler) Start(c *echo.Context) error {
	if strings.ToLower(strings.TrimSpace(h.cfg.AuthMode)) != "local" {
		return echo.NewHTTPError(http.StatusNotFound, "local login is disabled")
	}
	if !h.startLimiter.Allow(c.RealIP()) {
		return rateLimited(c, 15*time.Minute)
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "email is required")
	}

	email, err := auth.NormalizeEmail(req.Email)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	recent, err := h.codes.RecentCount(email, time.Now().Add(-15*time.Minute))
	if err != nil {
		slog.Error("auth: check email cooldown", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to start login")
	}
	if recent >= 3 {
		return rateLimited(c, 15*time.Minute)
	}

	code, err := h.codes.Create(email)
	if err != nil {
		slog.Error("auth: create verification code", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create verification code")
	}
	h.recordAudit(c, auth.AuditEvent{EventType: "login.requested", Outcome: "accepted", TargetType: "email"})
	user, userErr := h.users.GetByEmail(email)
	if userErr != nil && !errors.Is(userErr, auth.ErrUserNotFound) {
		slog.Error("auth: login user lookup failed", "err", userErr)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to start login")
	}
	if errors.Is(userErr, auth.ErrUserNotFound) || !user.Active {
		jitter()
		return c.JSON(200, map[string]bool{"code_sent": true})
	}
	if err := auth.SendCode(h.smtp, email, code); err != nil {
		slog.Error("auth: send verification email failed", "email", email, "err", err)
		return echo.NewHTTPError(http.StatusServiceUnavailable, "email delivery unavailable")
	}
	return c.JSON(200, map[string]bool{"code_sent": true})
}

func (h *AuthHandler) Verify(c *echo.Context) error {
	if strings.ToLower(strings.TrimSpace(h.cfg.AuthMode)) != "local" {
		return echo.NewHTTPError(http.StatusNotFound, "local login is disabled")
	}
	var req struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := c.Bind(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "email and code are required")
	}

	email, err := auth.NormalizeEmail(req.Email)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if !h.verifyLimiter.Allow(c.RealIP() + "|" + email) {
		return rateLimited(c, 15*time.Minute)
	}
	ok, err := h.codes.Verify(email, req.Code)
	if err != nil {
		slog.Error("auth: verify code", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to verify code")
	}
	if !ok {
		h.recordAudit(c, auth.AuditEvent{EventType: "login.failed", Outcome: "denied", TargetType: "email", Metadata: map[string]any{"reason": "invalid_credentials"}})
		jitter()
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired code")
	}

	user, err := h.users.GetByEmail(email)
	if err != nil && !errors.Is(err, auth.ErrUserNotFound) {
		slog.Error("auth: verified user lookup failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to complete login")
	}
	if errors.Is(err, auth.ErrUserNotFound) || !user.Active {
		h.recordAudit(c, auth.AuditEvent{EventType: "login.failed", Outcome: "denied", TargetType: "email", Metadata: map[string]any{"reason": "inactive_or_missing"}})
		jitter()
		return echo.NewHTTPError(http.StatusUnauthorized, "user not found or inactive")
	}

	if err := h.establishSession(c, user); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create session")
	}
	h.recordAudit(c, auth.AuditEvent{ActorUserID: user.ID, EventType: "login.succeeded", Outcome: "success", TargetType: "user", TargetID: user.ID})
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(200, map[string]string{"status": "authenticated"})
}

func rateLimited(c *echo.Context, retryAfter time.Duration) error {
	c.Response().Header().Set("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())))
	return echo.NewHTTPError(http.StatusTooManyRequests, "too many requests")
}

func (h *AuthHandler) Me(c *echo.Context) error {
	user := GetCurrentUser(c)
	if user == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	return c.JSON(200, user)
}

func (h *AuthHandler) Logout(c *echo.Context) error {
	if err := h.sessions.Destroy(c.Request().Context()); err != nil {
		slog.Error("auth: logout session destroy failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to log out")
	}
	user := GetCurrentUser(c)
	event := auth.AuditEvent{EventType: "logout", Outcome: "success"}
	if user != nil {
		event.ActorUserID = user.ID
		event.TargetType = "user"
		event.TargetID = user.ID
	}
	h.recordAudit(c, event)
	return c.JSON(200, map[string]bool{"ok": true})
}

func (h *AuthHandler) recordAudit(c *echo.Context, event auth.AuditEvent) {
	event.RemoteIP = c.RealIP()
	event.UserAgent = c.Request().UserAgent()
	ctx, cancel := auth.DetachedWriteContext(c.Request().Context())
	defer cancel()
	if err := h.audit.Record(ctx, event); err != nil {
		slog.Error("authentication audit write failed", "event", event.EventType, "err", err)
	}
}

func (h *AuthHandler) establishSession(c *echo.Context, user auth.User) error {
	issuedAt := time.Now().UTC()
	if err := h.users.TouchLoginAt(user.ID, issuedAt); err != nil {
		return err
	}
	return h.sessions.EstablishAuthenticatedSession(c.Request().Context(), user)
}
