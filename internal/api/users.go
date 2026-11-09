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
	ownership      accountOwnership
}

// userBodyLimit bounds user administration requests, which carry a few short fields.
const userBodyLimit = 16 << 10

// accountOwnership says which account changes belong to the identity provider
// and are refused by the browser user routes. /api/auth/status publishes it so
// the browser never has to work it out.
type accountOwnership string

const (
	accountOwnershipLocal accountOwnership = "local"
	// accountOwnershipRoles applies with an OIDC group-to-role mapping and
	// auto-provisioning: the provider decides who has an account, created at
	// first sign-in, and which role it holds.
	accountOwnershipRoles accountOwnership = "identity_provider_roles"
	// accountOwnershipRolesNoProvisioning is the same mapping without
	// auto-provisioning. Nothing can create an account, so people without one
	// stay out until provisioning or SCIM is configured.
	accountOwnershipRolesNoProvisioning accountOwnership = "identity_provider_roles_no_provisioning"
	// accountOwnershipDirectory adds a SCIM token to the mapping: the directory
	// also owns each account's status, email, and existence.
	accountOwnershipDirectory accountOwnership = "identity_provider_directory"
)

// accountOwnershipFor needs the mapping, so a SCIM token without one leaves
// local administration unchanged.
func accountOwnershipFor(cfg config.Config) accountOwnership {
	if strings.ToLower(strings.TrimSpace(cfg.AuthMode)) != "oidc" || !cfg.OIDCRoleMappingConfigured() {
		return accountOwnershipLocal
	}
	if cfg.SCIMToken != "" {
		return accountOwnershipDirectory
	}
	if !cfg.OIDCAutoProvision {
		return accountOwnershipRolesNoProvisioning
	}
	return accountOwnershipRoles
}

// ownedByIdentityProvider is the refusal for a change the provider owns. The
// message names the kind of change and never the configuration behind it.
func ownedByIdentityProvider(message string) error {
	return newAPIError(http.StatusForbidden, "managed_by_identity_provider", message)
}

// RegisterUserRoutes registers user management endpoints.
func RegisterUserRoutes(e *echo.Echo, users *auth.UserStore, smtp auth.SMTPConfig, cfg config.Config) {
	mode := strings.ToLower(strings.TrimSpace(cfg.AuthMode))
	if mode == "" {
		mode = "local"
	}
	h := &UserHandler{users: users, smtp: smtp, mode: mode, smtpConfigured: cfg.SMTPConfigured(), ownership: accountOwnershipFor(cfg)}
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
	if users == nil {
		users = []auth.User{}
	}
	return c.JSON(200, map[string][]auth.User{"users": users})
}

// CreateUser adds a new user (admin only).
func (h *UserHandler) CreateUser(c *echo.Context) error {
	if h.ownership != accountOwnershipLocal {
		return ownedByIdentityProvider("Accounts cannot be created in Fanout when roles come from the identity provider.")
	}
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
	if req.Email != nil && h.ownership == accountOwnershipDirectory {
		return ownedByIdentityProvider("Email addresses are managed by the identity provider, not in Fanout.")
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
	if h.ownership != accountOwnershipLocal {
		return ownedByIdentityProvider("Roles are assigned by the identity provider, not in Fanout.")
	}
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
	if h.ownership == accountOwnershipDirectory {
		return ownedByIdentityProvider("Account status is managed by the identity provider, not in Fanout.")
	}
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
			return newAPIError(http.StatusConflict, "last_active_administrator", err.Error())
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
	if h.ownership == accountOwnershipDirectory {
		return ownedByIdentityProvider("Accounts are deactivated by the identity provider, not deleted in Fanout.")
	}
	id := c.Param("id")
	if err := h.users.DeleteWithAudit(id, userAuditEvent(c, "user.deleted")); err != nil {
		if err == auth.ErrLastActiveAdmin {
			return newAPIError(http.StatusConflict, "last_active_administrator", err.Error())
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
