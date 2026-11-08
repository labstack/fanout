package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/labstack/fanout/internal/db/generated"
	appid "github.com/labstack/fanout/internal/id"
	appstore "github.com/labstack/fanout/internal/store"
)

// ErrFirstUserMustBeAdmin refuses an account on an empty database that would
// leave the installation without an active administrator.
var ErrFirstUserMustBeAdmin = errors.New("the first account must be an active administrator")

// ProvisionRequest describes an account an identity provider wants to exist.
type ProvisionRequest struct {
	Issuer      string
	Subject     string
	Email       string
	DisplayName string
	Role        Role
	Status      UserStatus
}

// ProvisionFederated creates an account and its identity link in one
// transaction, so a crash cannot leave an unlinked account for a later login
// to claim by email.
//
// A request whose identity already exists changes nothing and returns the
// current account with created false. A delayed retry therefore cannot undo a
// later role change or suspension; the provisioner applies changes with an
// update.
//
// source supplies attribution for the audit events; its type and target are
// set here.
func (s *UserStore) ProvisionFederated(ctx context.Context, req ProvisionRequest, source AuditEvent) (User, UserIdentity, bool, error) {
	if !ValidRole(string(req.Role)) {
		return User{}, UserIdentity{}, false, fmt.Errorf("auth: invalid role %q", req.Role)
	}
	if !ValidUserStatus(string(req.Status)) {
		return User{}, UserIdentity{}, false, fmt.Errorf("auth: invalid status %q", req.Status)
	}
	ctx, cancel := DetachedWriteContext(ctx)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return User{}, UserIdentity{}, false, fmt.Errorf("auth: open user conn: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return User{}, UserIdentity{}, false, fmt.Errorf("auth: begin provisioning: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			rollbackConn(conn, "user provisioning")
		}
	}()

	q := generated.New(conn)
	identity, err := findIdentity(ctx, conn, `issuer = ? AND subject = ?`, req.Issuer, req.Subject)
	if err == nil {
		row, err := q.GetUserByID(ctx, identity.UserID)
		if err != nil {
			return User{}, UserIdentity{}, false, fmt.Errorf("auth: read provisioned user: %w", err)
		}
		return toUser(row), identity, false, nil
	}
	if !errors.Is(err, ErrUserNotFound) {
		return User{}, UserIdentity{}, false, err
	}

	// Holding the write lock makes this the same check setup performs. A
	// concurrent administrator that loses it sees a non-empty database and is
	// created normally below.
	empty, err := noUsersExist(ctx, q)
	if err != nil {
		return User{}, UserIdentity{}, false, err
	}
	if empty && (req.Role != RoleAdmin || req.Status != UserStatusActive) {
		return User{}, UserIdentity{}, false, ErrFirstUserMustBeAdmin
	}

	params, err := newCreateUserParams(req.Email, req.DisplayName, req.Role)
	if err != nil {
		return User{}, UserIdentity{}, false, err
	}
	row, err := q.CreateUser(ctx, params)
	if err != nil {
		if appstore.IsUniqueConstraint(err) {
			return User{}, UserIdentity{}, false, fmt.Errorf("%w: %v", ErrUserConflict, err)
		}
		return User{}, UserIdentity{}, false, fmt.Errorf("auth: create provisioned user: %w", err)
	}
	if req.Status != UserStatusActive {
		row, err = q.UpdateUser(ctx, generated.UpdateUserParams{
			Email: row.Email, DisplayName: row.DisplayName, Role: row.Role, Status: string(req.Status),
			UpdatedAt: row.UpdatedAt, ID: row.ID,
		})
		if err != nil {
			return User{}, UserIdentity{}, false, fmt.Errorf("auth: set provisioned status: %w", err)
		}
	}

	identityID, err := appid.New()
	if err != nil {
		return User{}, UserIdentity{}, false, fmt.Errorf("auth: identity id: %w", err)
	}
	now := userTimestamp(time.Now())
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO user_identities (id, user_id, issuer, subject, email_at_link, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, identityID, row.ID, req.Issuer, req.Subject, req.Email, now); err != nil {
		if appstore.IsUniqueConstraint(err) {
			return User{}, UserIdentity{}, false, fmt.Errorf("%w: %v", ErrIdentityConflict, err)
		}
		return User{}, UserIdentity{}, false, fmt.Errorf("auth: link provisioned identity: %w", err)
	}

	created := source
	created.EventType, created.Outcome, created.TargetType, created.TargetID = "user.provisioned", "success", "user", row.ID
	created.Metadata = withMetadata(source.Metadata, map[string]any{"role": row.Role, "status": row.Status, "first_user": empty})
	if err := recordAudit(ctx, conn, created); err != nil {
		return User{}, UserIdentity{}, false, err
	}
	linked := source
	linked.EventType, linked.Outcome, linked.TargetType, linked.TargetID = "identity.linked", "success", "identity", identityID
	linked.Metadata = withMetadata(source.Metadata, map[string]any{"issuer": req.Issuer, "user_id": row.ID})
	if err := recordAudit(ctx, conn, linked); err != nil {
		return User{}, UserIdentity{}, false, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return User{}, UserIdentity{}, false, fmt.Errorf("auth: commit provisioning: %w", err)
	}
	committed = true
	return toUser(row), UserIdentity{ID: identityID, UserID: row.ID, Issuer: req.Issuer, Subject: req.Subject, EmailAtLink: req.Email, CreatedAt: now}, true, nil
}

// UserChange lists the fields a directory update may set; nil leaves one alone.
type UserChange struct {
	Email       *string
	DisplayName *string
	Role        *Role
	Status      *UserStatus
}

// UpdateProvisioned applies a directory update in one write transaction,
// including the last-active-administrator check. A change to the current state
// is reported with changed false and writes nothing, so it is safe to repeat.
// The exception is repeating a suspension while a session or OAuth credential
// is still live: those are removed and audited. The identity link is never
// touched.
func (s *UserStore) UpdateProvisioned(id string, change UserChange, source AuditEvent) (User, bool, error) {
	event := source
	event.Outcome = "success"
	return s.applyUpdate(id, change.Email, change.DisplayName, change.Role, change.Status, &event, userUpdateOptions{
		skipUnchanged:     true,
		convergeSuspended: true,
		describe: func(before, after User, event *AuditEvent) {
			event.EventType, event.Metadata = describeProvisionedUpdate(before, after, source.Metadata)
		},
	})
}

// describeProvisionedUpdate never records addresses: the audit log names which
// fields changed and the role and status transitions.
func describeProvisionedUpdate(before, after User, base map[string]any) (AuditEventType, map[string]any) {
	changed := []string{}
	fields := map[string]any{}
	if before.Email != after.Email {
		changed = append(changed, "email")
	}
	if before.DisplayName != after.DisplayName {
		changed = append(changed, "display_name")
	}
	if before.Role != after.Role {
		changed = append(changed, "role")
		fields["role_from"], fields["role_to"] = string(before.Role), string(after.Role)
	}
	if before.Status != after.Status {
		changed = append(changed, "status")
		fields["status_from"], fields["status_to"] = string(before.Status), string(after.Status)
	}
	fields["changed"] = changed

	eventType := AuditEventType("user.updated")
	switch {
	case len(changed) == 0:
		// Only a repeated suspension that found live access gets here.
		eventType = "session.revoked"
		fields["reason"] = "repeated_suspension"
	case before.Status == UserStatusActive && after.Status != UserStatusActive:
		eventType = "user.suspended"
	case before.Status != UserStatusActive && after.Status == UserStatusActive:
		eventType = "user.reactivated"
	case before.Role != after.Role:
		eventType = "role.changed"
	}
	return eventType, withMetadata(base, fields)
}

func withMetadata(base, extra map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}
