package api

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
)

// UserHandler handles admin user management endpoints.
type UserHandler struct {
	users *auth.UserStore
	smtp  auth.SMTPConfig
}

// RegisterUserRoutes registers user management endpoints.
func RegisterUserRoutes(e *echo.Echo, users *auth.UserStore, smtp auth.SMTPConfig) {
	h := &UserHandler{users: users, smtp: smtp}
	adminOnly := RequireRole("admin")

	e.GET("/api/users", h.ListUsers, adminOnly)
	e.POST("/api/users", h.CreateUser, adminOnly)
	e.PUT("/api/users/:id", h.UpdateUser, adminOnly)
	e.DELETE("/api/users/:id", h.DeleteUser, adminOnly)
	e.POST("/api/auth/api-key", h.GenerateAPIKey)
	e.DELETE("/api/auth/api-key", h.RevokeAPIKey)
}

// ListUsers returns all users.
func (h *UserHandler) ListUsers(c *echo.Context) error {
	users, err := h.users.List()
	if err != nil {
		slog.Error("list users failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to list users")
	}
	return c.JSON(200, users)
}

// CreateUser adds a new user (admin only).
func (h *UserHandler) CreateUser(c *echo.Context) error {
	var req struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	}
	if err := c.Bind(&req); err != nil || strings.TrimSpace(req.Email) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "email is required")
	}
	role := req.Role
	if role == "" {
		role = "operator"
	}
	if role != "viewer" && role != "operator" && role != "admin" {
		return echo.NewHTTPError(http.StatusBadRequest, "role must be viewer, operator, or admin")
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	user, err := h.users.Create(email, req.Name, role)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return echo.NewHTTPError(http.StatusConflict, "user already exists")
		}
		slog.Error("create user failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create user")
	}

	// Send invitation email
	go func() {
		if err := auth.SendInvite(h.smtp, email); err != nil {
			slog.Error("auth: send invitation email failed", "email", email, "err", err)
		}
	}()

	return c.JSON(201, user)
}

// UpdateUser modifies a user's fields (admin only).
func (h *UserHandler) UpdateUser(c *echo.Context) error {
	id := c.Param("id")
	var req struct {
		Email  *string `json:"email"`
		Name   *string `json:"name"`
		Role   *string `json:"role"`
		Active *bool   `json:"active"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request")
	}
	if req.Role != nil && *req.Role != "viewer" && *req.Role != "operator" && *req.Role != "admin" {
		return echo.NewHTTPError(http.StatusBadRequest, "role must be viewer, operator, or admin")
	}

	user, err := h.users.Update(id, req.Email, req.Name, req.Role, req.Active)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		slog.Error("update user failed", "id", id, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to update user")
	}
	return c.JSON(200, user)
}

// GenerateAPIKey creates a new API key for the authenticated user.
func (h *UserHandler) GenerateAPIKey(c *echo.Context) error {
	claims := GetAuthClaims(c)
	if claims == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	key, err := h.users.GenerateAPIKey(claims.Subject)
	if err != nil {
		slog.Error("generate api key failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to generate api key")
	}
	return c.JSON(200, map[string]string{"api_key": key})
}

// RevokeAPIKey removes the API key for the authenticated user.
func (h *UserHandler) RevokeAPIKey(c *echo.Context) error {
	claims := GetAuthClaims(c)
	if claims == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if err := h.users.RevokeAPIKey(claims.Subject); err != nil {
		slog.Error("revoke api key failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to revoke api key")
	}
	return c.JSON(200, map[string]bool{"ok": true})
}

// DeleteUser removes a user (admin only).
func (h *UserHandler) DeleteUser(c *echo.Context) error {
	id := c.Param("id")
	if err := h.users.Delete(id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		slog.Error("delete user failed", "id", id, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to delete user")
	}
	return c.NoContent(204)
}
