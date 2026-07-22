package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/env"
)

// UserHandler handles admin user management endpoints.
type UserHandler struct {
	users *auth.UserStore
	smtp  auth.SMTPConfig
	mode  string
}

// RegisterUserRoutes registers user management endpoints.
func RegisterUserRoutes(e *echo.Echo, users *auth.UserStore, smtp auth.SMTPConfig, configs ...env.Config) {
	mode := "local"
	if len(configs) > 0 && strings.TrimSpace(configs[0].AuthMode) != "" {
		mode = strings.ToLower(strings.TrimSpace(configs[0].AuthMode))
	}
	h := &UserHandler{users: users, smtp: smtp, mode: mode}
	adminOnly := RequireCapability(ManageUsers)

	e.GET("/api/users", h.ListUsers, adminOnly)
	e.POST("/api/users", h.CreateUser, adminOnly)
	e.PUT("/api/users/:id", h.UpdateUser, adminOnly)
	e.DELETE("/api/users/:id", h.DeleteUser, adminOnly)
	e.POST("/api/users/:id/logout-all", h.LogoutAll, adminOnly)
}

func (h *UserHandler) LogoutAll(c *echo.Context) error {
	if err := h.users.RevokeAllSessionsWithAudit(c.Param("id"), userAuditEvent(c, "session.revoked")); err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		slog.Error("revoke user sessions failed", "id", c.Param("id"), "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to revoke sessions")
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
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
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "email is required")
	}
	role := req.Role
	if role == "" {
		role = "operator"
	}
	if role != "viewer" && role != "operator" && role != "admin" {
		return echo.NewHTTPError(http.StatusBadRequest, "role must be viewer, operator, or admin")
	}

	email, err := auth.NormalizeEmail(req.Email)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	user, err := h.users.CreateWithAudit(email, req.Name, role, userAuditEvent(c, "user.created"))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return echo.NewHTTPError(http.StatusConflict, "user already exists")
		}
		slog.Error("create user failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create user")
	}

	// Local mode sends an invitation synchronously so the admin sees an accurate
	// result: a 201 must mean the invite was actually delivered to the
	// MTA. OIDC users are pre-provisioned and enter through the configured IdP.
	// On mail failure, the user row stays so the admin can retry later.
	if h.mode == "local" {
		if err := auth.SendInvite(h.smtp, email); err != nil {
			slog.Error("auth: send invitation email failed", "email", email, "err", err)
			return echo.NewHTTPError(http.StatusServiceUnavailable, "user created but invite email delivery failed")
		}
	}

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
	if req.Email != nil {
		email, err := auth.NormalizeEmail(*req.Email)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		req.Email = &email
	}

	eventType := "user.updated"
	if req.Role != nil {
		eventType = "role.changed"
	} else if req.Active != nil && !*req.Active {
		eventType = "user.deactivated"
	}
	user, err := h.users.UpdateWithAudit(id, req.Email, req.Name, req.Role, req.Active, userAuditEvent(c, eventType))
	if err != nil {
		if err == auth.ErrLastActiveAdmin {
			return echo.NewHTTPError(http.StatusConflict, err.Error())
		}
		if strings.Contains(err.Error(), "not found") {
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		slog.Error("update user failed", "id", id, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to update user")
	}
	return c.JSON(200, user)
}

// DeleteUser removes a user (admin only).
func (h *UserHandler) DeleteUser(c *echo.Context) error {
	id := c.Param("id")
	if err := h.users.DeleteWithAudit(id, userAuditEvent(c, "user.deleted")); err != nil {
		if err == auth.ErrLastActiveAdmin {
			return echo.NewHTTPError(http.StatusConflict, err.Error())
		}
		if strings.Contains(err.Error(), "not found") {
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		slog.Error("delete user failed", "id", id, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to delete user")
	}
	return c.NoContent(204)
}

func userAuditEvent(c *echo.Context, eventType string) auth.AuditEvent {
	event := auth.AuditEvent{EventType: eventType, Outcome: "success", RemoteIP: c.RealIP(), UserAgent: c.Request().UserAgent()}
	if actor := GetCurrentUser(c); actor != nil && actor.ID != publicViewerID {
		event.ActorUserID = actor.ID
	}
	return event
}
