package api

import (
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/settings"
)

type AuthHandler struct {
	users         *auth.UserStore
	codes         *auth.CodeStore
	setup         *auth.Setup
	settings      *settings.Store
	jwtSecret     string
	refreshSecret string
	smtp          auth.SMTPConfig
}

func RegisterAuthRoutes(e *echo.Echo, users *auth.UserStore, codes *auth.CodeStore, setup *auth.Setup, settingsStore *settings.Store, jwtSecret, refreshSecret string, smtp auth.SMTPConfig) {
	h := &AuthHandler{
		users:         users,
		codes:         codes,
		setup:         setup,
		settings:      settingsStore,
		jwtSecret:     jwtSecret,
		refreshSecret: refreshSecret,
		smtp:          smtp,
	}

	e.GET("/api/auth/status", h.Status)
	e.POST("/api/auth/setup", h.Setup)
	e.POST("/api/auth/start", h.Start)
	e.POST("/api/auth/verify", h.Verify)
	e.POST("/api/auth/refresh", h.Refresh)
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
	})
}

func (h *AuthHandler) Setup(c *echo.Context) error {
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
	user, err := h.users.CreateFirstAdmin(email, req.Name)
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

	accessToken, err := h.issueTokens(c, user)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create token")
	}

	// Generate the ingest token only if one doesn't exist yet. A Setup retry
	// (ErrSetupComplete branch above) must not rotate and invalidate a token
	// live collectors may already be using — to rotate deliberately, the admin
	// uses the Settings page.
	resp := map[string]string{"access_token": accessToken}
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
		if err := h.settings.SetIngest(c.Request().Context(), settings.Ingest{TokenHash: ingestHash}); err != nil {
			slog.Error("auth: setup persist ingest token failed", "err", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to persist ingest token")
		}
		resp["ingest_token"] = ingestToken
		resp["ingest_header_name"] = "x-fanout-ingest-token"
	}

	slog.Info("auth: first admin setup completed", "email", email)
	h.setup.Clear()
	return c.JSON(200, resp)
}

func (h *AuthHandler) Start(c *echo.Context) error {
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
	user, err := h.users.GetByEmail(email)
	if err != nil || !user.Active {
		jitter()
		return c.JSON(200, map[string]bool{"code_sent": true})
	}

	code, err := h.codes.Create(email)
	if err != nil {
		slog.Error("auth: create verification code", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create verification code")
	}
	if err := auth.SendCode(h.smtp, email, code); err != nil {
		slog.Error("auth: send verification email failed", "email", email, "err", err)
		return echo.NewHTTPError(http.StatusServiceUnavailable, "email delivery unavailable")
	}

	return c.JSON(200, map[string]bool{"code_sent": true})
}

func (h *AuthHandler) Verify(c *echo.Context) error {
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
	ok, err := h.codes.Verify(email, req.Code)
	if err != nil {
		slog.Error("auth: verify code", "err", err)
		jitter()
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired code")
	}
	if !ok {
		jitter()
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired code")
	}

	user, err := h.users.GetByEmail(email)
	if err != nil || !user.Active {
		jitter()
		return echo.NewHTTPError(http.StatusUnauthorized, "user not found or inactive")
	}

	accessToken, err := h.issueTokens(c, user)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create token")
	}
	return c.JSON(200, map[string]string{"access_token": accessToken})
}

func (h *AuthHandler) Refresh(c *echo.Context) error {
	cookie, err := c.Cookie("refresh_token")
	if err != nil || cookie.Value == "" {
		return echo.NewHTTPError(http.StatusUnauthorized, "no refresh token")
	}

	claims, err := auth.VerifyRefresh(h.refreshSecret, cookie.Value)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid refresh token")
	}

	user, err := h.users.GetByID(claims.Subject)
	if err != nil || !user.Active {
		return echo.NewHTTPError(http.StatusUnauthorized, "user not found or inactive")
	}

	// A valid, unexpired refresh token for an active user is accepted as-is.
	// We intentionally do NOT reject tokens issued before the user's most recent
	// login (the old sessionRevoked/LoggedInAt check) — that enforced a single
	// active session per user and logged people out on a second tab/device or
	// after a redeploy. The monk server keeps last-login purely for analytics
	// and never enforces it; fanout now matches. Concurrent sessions coexist;
	// the tradeoff is no server-side revocation (see Logout).
	accessToken, err := h.issueTokens(c, user)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create token")
	}
	return c.JSON(200, map[string]string{"access_token": accessToken})
}

func (h *AuthHandler) Me(c *echo.Context) error {
	user := GetCurrentUser(c)
	if user == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	return c.JSON(200, user)
}

func (h *AuthHandler) Logout(c *echo.Context) error {
	// Clearing the cookie is the whole of logout. Refresh tokens are stateless
	// (signature + expiry only), matching the monk server's model — there is no
	// server-side revocation list, so a token can't be invalidated before its
	// TTL. This is deliberate: it's the cost of not evicting concurrent sessions
	// (see Refresh). A copy of the refresh token kept outside the browser stays
	// valid until it expires; add a per-session denylist if that's unacceptable.
	h.clearRefreshCookie(c)
	return c.JSON(200, map[string]bool{"ok": true})
}

func (h *AuthHandler) issueTokens(c *echo.Context, user auth.User) (string, error) {
	issuedAt := time.Now().UTC()
	refreshToken, err := auth.SignRefresh(h.refreshSecret, user.ID, issuedAt)
	if err != nil {
		return "", err
	}
	accessToken, err := auth.SignAccess(h.jwtSecret, user.ID)
	if err != nil {
		return "", err
	}
	if err := h.users.TouchLoginAt(user.ID, issuedAt); err != nil {
		return "", err
	}
	h.setRefreshCookie(c, refreshToken, issuedAt.Add(auth.RefreshTTL))
	return accessToken, nil
}

func (h *AuthHandler) setRefreshCookie(c *echo.Context, value string, expiresAt time.Time) {
	c.SetCookie(&http.Cookie{
		Name:     "refresh_token",
		Value:    value,
		Path:     "/api/auth/",
		HttpOnly: true,
		Secure:   isSecureRequest(c),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		Expires:  expiresAt,
	})
}

func (h *AuthHandler) clearRefreshCookie(c *echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     "/api/auth/",
		HttpOnly: true,
		Secure:   isSecureRequest(c),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

func isSecureRequest(c *echo.Context) bool {
	if c.Request().TLS != nil {
		return true
	}
	return strings.EqualFold(c.Request().Header.Get("X-Forwarded-Proto"), "https")
}
