package api

import (
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
)

// AuthHandler handles passwordless email login endpoints.
type AuthHandler struct {
	users     *auth.UserStore
	codes     *auth.CodeStore
	jwtSecret string
	smtp      auth.SMTPConfig
}

// RegisterAuthRoutes registers auth endpoints.
func RegisterAuthRoutes(e *echo.Echo, users *auth.UserStore, codes *auth.CodeStore, jwtSecret string, smtp auth.SMTPConfig) {
	h := &AuthHandler{
		users:     users,
		codes:     codes,
		jwtSecret: jwtSecret,
		smtp:      smtp,
	}

	e.GET("/api/auth/status", h.Status)
	e.POST("/api/auth/setup", h.Setup)
	e.POST("/api/auth/start", h.Start)
	e.POST("/api/auth/verify", h.Verify)
	e.POST("/api/auth/refresh", h.Refresh)
	e.GET("/api/auth/me", h.Me)
	e.POST("/api/auth/logout", h.Logout)
}

// jitter adds random delay to prevent timing attacks on user enumeration.
func jitter() {
	time.Sleep(time.Duration(50+rand.IntN(100)) * time.Millisecond)
}

// Status returns whether auth is set up (has users) or needs first-time setup.
func (h *AuthHandler) Status(c *echo.Context) error {
	users, err := h.users.List()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to check auth status")
	}
	return c.JSON(200, map[string]any{
		"setup_required": len(users) == 0,
		"auth_enabled":   true,
	})
}

// Setup creates the first admin user without email verification.
// Only works when zero users exist — one-time first-boot setup.
func (h *AuthHandler) Setup(c *echo.Context) error {
	users, err := h.users.List()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to check users")
	}
	if len(users) > 0 {
		return echo.NewHTTPError(http.StatusForbidden, "setup already complete")
	}

	var req struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := c.Bind(&req); err != nil || strings.TrimSpace(req.Email) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "email is required")
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))

	user, err := h.users.Create(email, req.Name, "admin")
	if err != nil {
		slog.Error("auth: setup create admin failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create admin")
	}

	if err := h.users.TouchLogin(user.ID); err != nil {
		slog.Error("auth: touch login failed", "user_id", user.ID, "err", err)
	}
	slog.Info("auth: first admin created via setup", "email", email)

	accessToken, err := auth.SignAccess(h.jwtSecret, user.ID, user.Email, user.Role)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create token")
	}

	refreshToken, err := auth.SignRefresh(h.jwtSecret, user.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create token")
	}

	c.SetCookie(&http.Cookie{
		Name:     "refresh_token",
		Value:    refreshToken,
		Path:     "/api/auth/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 60 * 60,
	})

	return c.JSON(200, map[string]string{
		"access_token": accessToken,
	})
}

// Start sends a verification code to the given email.
func (h *AuthHandler) Start(c *echo.Context) error {
	var req struct {
		Email string `json:"email"`
	}
	if err := c.Bind(&req); err != nil || strings.TrimSpace(req.Email) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "email is required")
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))

	// Check if user exists — be honest, this is an internal tool
	user, err := h.users.GetByEmail(email)
	if err != nil {
		return c.JSON(200, map[string]any{"code_sent": false, "reason": "no_account"})
	}
	if !user.Active {
		return c.JSON(200, map[string]any{"code_sent": false, "reason": "inactive"})
	}

	code, err := h.codes.Create(email)
	if err != nil {
		slog.Error("auth: create verification code", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create verification code")
	}

	go func() {
		if err := auth.SendCode(h.smtp, email, code); err != nil {
			slog.Error("auth: send verification email failed", "email", email, "err", err)
		}
	}()

	return c.JSON(200, map[string]any{"code_sent": true})
}

// Verify checks the code and returns JWT tokens.
func (h *AuthHandler) Verify(c *echo.Context) error {
	var req struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := c.Bind(&req); err != nil || req.Email == "" || req.Code == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "email and code are required")
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))

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
	if err != nil {
		jitter()
		return echo.NewHTTPError(http.StatusUnauthorized, "user not found")
	}

	if err := h.users.TouchLogin(user.ID); err != nil {
		slog.Error("auth: touch login failed", "user_id", user.ID, "err", err)
	}

	accessToken, err := auth.SignAccess(h.jwtSecret, user.ID, user.Email, user.Role)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create token")
	}

	refreshToken, err := auth.SignRefresh(h.jwtSecret, user.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create token")
	}

	c.SetCookie(&http.Cookie{
		Name:     "refresh_token",
		Value:    refreshToken,
		Path:     "/api/auth/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 60 * 60,
	})

	return c.JSON(200, map[string]string{
		"access_token": accessToken,
	})
}

// Refresh exchanges a refresh token for a new access token.
func (h *AuthHandler) Refresh(c *echo.Context) error {
	cookie, err := c.Cookie("refresh_token")
	if err != nil || cookie.Value == "" {
		return echo.NewHTTPError(http.StatusUnauthorized, "no refresh token")
	}

	userID, err := auth.VerifyRefresh(h.jwtSecret, cookie.Value)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid refresh token")
	}

	user, err := h.users.GetByID(userID)
	if err != nil || !user.Active {
		return echo.NewHTTPError(http.StatusUnauthorized, "user not found or inactive")
	}

	accessToken, err := auth.SignAccess(h.jwtSecret, user.ID, user.Email, user.Role)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create token")
	}

	return c.JSON(200, map[string]string{
		"access_token": accessToken,
	})
}

// Me returns the current authenticated user.
func (h *AuthHandler) Me(c *echo.Context) error {
	claims := GetAuthClaims(c)
	if claims == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}

	user, err := h.users.GetByID(claims.Subject)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}

	return c.JSON(200, user)
}

// Logout clears the refresh token cookie.
func (h *AuthHandler) Logout(c *echo.Context) error {
	c.SetCookie(&http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     "/api/auth/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	return c.JSON(200, map[string]bool{"ok": true})
}

// GetAuthClaims retrieves JWT claims from the echo context (set by middleware).
func GetAuthClaims(c *echo.Context) *auth.Claims {
	v := c.Get("auth_claims")
	if v == nil {
		return nil
	}
	claims, _ := v.(*auth.Claims)
	return claims
}

// RequireRole returns a middleware that checks the user's role.
// Roles are hierarchical: admin > operator > viewer.
func RequireRole(minRole string) echo.MiddlewareFunc {
	levels := map[string]int{"viewer": 0, "operator": 1, "admin": 2}
	minLevel := levels[minRole]

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			claims := GetAuthClaims(c)
			if claims == nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
			}
			userLevel, ok := levels[claims.Role]
			if !ok || userLevel < minLevel {
				return echo.NewHTTPError(http.StatusForbidden, "insufficient permissions")
			}
			return next(c)
		}
	}
}
