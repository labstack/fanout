package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
)

// UserHandler handles admin user management endpoints.
type UserHandler struct {
	users          *auth.UserStore
	smtp           auth.SMTPConfig
	mode           string
	smtpConfigured bool
}

// userBodyLimit bounds user administration requests, which carry a few short fields.
const userBodyLimit = 16 << 10

// RegisterUserRoutes registers user management endpoints.
func RegisterUserRoutes(e *echo.Echo, users *auth.UserStore, smtp auth.SMTPConfig, cfg config.Config) {
	mode := strings.ToLower(strings.TrimSpace(cfg.AuthMode))
	if mode == "" {
		mode = "local"
	}
	h := &UserHandler{users: users, smtp: smtp, mode: mode, smtpConfigured: cfg.SMTPConfigured()}
	adminOnly := RequireCapability(ManageUsers)

	e.GET("/api/users", h.ListUsers, adminOnly)
	e.POST("/api/users", h.CreateUser, adminOnly)
	e.PATCH("/api/users/:id", h.UpdateUser, adminOnly)
	e.PATCH("/api/users/:id/role", h.UpdateRole, adminOnly)
	e.PATCH("/api/users/:id/status", h.UpdateStatus, adminOnly)
	e.DELETE("/api/users/:id", h.DeleteUser, adminOnly)
	e.POST("/api/users/:id/access/revoke", h.RevokeAccess, adminOnly)
}

// RevokeAccess invalidates existing browser sessions and OAuth tokens.
// The account remains active and can sign in again.
func (h *UserHandler) RevokeAccess(c *echo.Context) error {
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
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
	}
	if err := decodeStrict(c, &req, userBodyLimit); err != nil {
		return err
	}
	role := auth.Role(req.Role)
	if role == "" {
		role = auth.RoleOperator
	}
	if !auth.ValidRole(string(role)) {
		return echo.NewHTTPError(http.StatusBadRequest, "role must be viewer, operator, or admin")
	}

	email, err := auth.NormalizeEmail(req.Email)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	user, err := h.users.CreateWithAudit(email, req.DisplayName, role, userAuditEvent(c, "user.created"))
	if err != nil {
		if errors.Is(err, auth.ErrUserConflict) {
			return echo.NewHTTPError(http.StatusConflict, "user already exists")
		}
		slog.Error("create user failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create user")
	}

	resp := struct {
		auth.User
		InviteDelivery    string `json:"invite_delivery,omitempty"`
		LoginLinkRequired bool   `json:"login_link_required,omitempty"`
	}{User: user}

	// SMTP is optional in local mode. When it is absent, creating a user remains
	// a successful operation and the response tells the operator to mint the
	// existing audited, single-use login link. A configured relay is different:
	// failure to deliver is surfaced so an apparent success never claims mail was
	// sent. The user row stays in either case so the operator can retry safely.
	if h.mode == "local" {
		if !h.smtpConfigured {
			resp.InviteDelivery = "not_configured"
			resp.LoginLinkRequired = true
			return c.JSON(http.StatusCreated, resp)
		}
		if err := auth.SendInvite(h.smtp, email); err != nil {
			slog.Error("auth: send invitation email failed", "email", email, "err", err)
			return echo.NewHTTPError(http.StatusServiceUnavailable, "user created but invite email delivery failed")
		}
		resp.InviteDelivery = "email"
	}

	return c.JSON(http.StatusCreated, resp)
}

// UpdateUser modifies only a user's profile (admin only).
func (h *UserHandler) UpdateUser(c *echo.Context) error {
	var req struct {
		Email       *string `json:"email"`
		DisplayName *string `json:"display_name"`
	}
	if err := decodeStrict(c, &req, userBodyLimit); err != nil {
		return err
	}
	if req.Email != nil {
		email, err := auth.NormalizeEmail(*req.Email)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		req.Email = &email
	}
	return h.updateUser(c, req.Email, req.DisplayName, nil, nil, "user.updated")
}

// UpdateRole modifies a user's role (admin only).
func (h *UserHandler) UpdateRole(c *echo.Context) error {
	var req struct {
		Role string `json:"role"`
	}
	if err := decodeStrict(c, &req, userBodyLimit); err != nil {
		return err
	}
	if !auth.ValidRole(req.Role) {
		return echo.NewHTTPError(http.StatusBadRequest, "role must be viewer, operator, or admin")
	}
	role := auth.Role(req.Role)
	return h.updateUser(c, nil, nil, &role, nil, "role.changed")
}

// UpdateStatus modifies a user's status (admin only).
func (h *UserHandler) UpdateStatus(c *echo.Context) error {
	var req struct {
		Status string `json:"status"`
	}
	if err := decodeStrict(c, &req, userBodyLimit); err != nil {
		return err
	}
	if !auth.ValidUserStatus(req.Status) {
		return echo.NewHTTPError(http.StatusBadRequest, "status must be active or suspended")
	}
	status := auth.UserStatus(req.Status)
	eventType := auth.AuditEventType("user.updated")
	if status == auth.UserStatusSuspended {
		eventType = "user.suspended"
	}
	return h.updateUser(c, nil, nil, nil, &status, eventType)
}

func (h *UserHandler) updateUser(c *echo.Context, email, displayName *string, role *auth.Role, status *auth.UserStatus, eventType auth.AuditEventType) error {
	id := c.Param("id")
	user, err := h.users.UpdateWithAudit(id, email, displayName, role, status, userAuditEvent(c, eventType))
	if err != nil {
		if errors.Is(err, auth.ErrUserConflict) {
			return echo.NewHTTPError(http.StatusConflict, "user already exists")
		}
		if errors.Is(err, auth.ErrLastActiveAdmin) {
			return echo.NewHTTPError(http.StatusConflict, err.Error())
		}
		if errors.Is(err, auth.ErrUserNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		slog.Error("update user failed", "id", id, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to update user")
	}
	return c.JSON(http.StatusOK, user)
}

// DeleteUser removes a user (admin only).
func (h *UserHandler) DeleteUser(c *echo.Context) error {
	id := c.Param("id")
	if err := h.users.DeleteWithAudit(id, userAuditEvent(c, "user.deleted")); err != nil {
		if err == auth.ErrLastActiveAdmin {
			return echo.NewHTTPError(http.StatusConflict, err.Error())
		}
		if errors.Is(err, auth.ErrUserNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		slog.Error("delete user failed", "id", id, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to delete user")
	}
	return c.NoContent(204)
}

func userAuditEvent(c *echo.Context, eventType auth.AuditEventType) auth.AuditEvent {
	event := auth.AuditEvent{EventType: eventType, Outcome: "success", RemoteIP: c.RealIP(), UserAgent: c.Request().UserAgent()}
	if actor := GetCurrentUser(c); actor != nil {
		event.ActorUserID = actor.ID
	}
	return event
}
