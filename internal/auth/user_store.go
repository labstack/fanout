package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/labstack/fanout/internal/db/generated"
	appid "github.com/labstack/fanout/internal/id"
)

// ErrUserNotFound is returned when a requested user does not exist.
var ErrUserNotFound = errors.New("user not found")

// ErrLastActiveAdmin is returned when an operation would remove the final active admin.
var ErrLastActiveAdmin = errors.New("cannot remove the last active admin")

type Role string

const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
)

func ValidRole(role string) bool {
	switch Role(role) {
	case RoleViewer, RoleOperator, RoleAdmin:
		return true
	default:
		return false
	}
}

const userTimestampLayout = "2006-01-02T15:04:05.000Z07:00"

func userTimestamp(at time.Time) string {
	return at.UTC().Truncate(time.Millisecond).Format(userTimestampLayout)
}

// User represents an authenticated user.
type User struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Name        string `json:"name,omitempty"`
	Role        Role   `json:"role"`
	Active      bool   `json:"active"`
	AuthVersion int64  `json:"-"`
	LoggedInAt  string `json:"logged_in_at,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// UserStore provides CRUD operations for users.
type UserStore struct {
	db *sql.DB
	q  *generated.Queries
}

// NewUserStore creates a UserStore backed by the given database connection.
func NewUserStore(db *sql.DB) *UserStore {
	return &UserStore{db: db, q: generated.New(db)}
}

// toUser converts a generated.User to the domain User type.
func toUser(u generated.User) User {
	return User{
		ID:          u.ID,
		Email:       u.Email,
		Name:        u.Name.String,
		Role:        Role(u.Role),
		Active:      u.Active == 1,
		AuthVersion: u.AuthVersion,
		LoggedInAt:  u.LoggedInAt.String,
		CreatedAt:   u.CreatedAt,
		UpdatedAt:   u.UpdatedAt,
	}
}

// Create adds a new user.
func (s *UserStore) Create(email, name string, role Role) (User, error) {
	return s.create(email, name, role, nil)
}

func (s *UserStore) CreateWithAudit(email, name string, role Role, event AuditEvent) (User, error) {
	return s.create(email, name, role, &event)
}

func (s *UserStore) create(email, name string, role Role, event *AuditEvent) (User, error) {
	if !ValidRole(string(role)) {
		return User{}, fmt.Errorf("auth: invalid role %q", role)
	}
	params, err := newCreateUserParams(email, name, role)
	if err != nil {
		return User{}, err
	}
	if event == nil {
		u, err := s.q.CreateUser(context.Background(), params)
		if err != nil {
			return User{}, fmt.Errorf("auth: create user: %w", err)
		}
		return toUser(u), nil
	}
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("auth: begin user create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	u, err := generated.New(tx).CreateUser(ctx, params)
	if err != nil {
		return User{}, fmt.Errorf("auth: create user: %w", err)
	}
	event.TargetType = "user"
	event.TargetID = u.ID
	if err := recordAudit(ctx, tx, *event); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("auth: commit user create: %w", err)
	}
	return toUser(u), nil
}

// GetByID returns a user by ID.
func (s *UserStore) GetByID(id string) (User, error) {
	return s.GetByIDContext(context.Background(), id)
}

func (s *UserStore) GetByIDContext(ctx context.Context, id string) (User, error) {
	u, err := s.q.GetUserByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("auth: get user by id: %w", err)
	}
	return toUser(u), nil
}

// GetByEmail returns a user by email address.
func (s *UserStore) GetByEmail(email string) (User, error) {
	return s.GetByEmailContext(context.Background(), email)
}

func (s *UserStore) GetByEmailContext(ctx context.Context, email string) (User, error) {
	u, err := s.q.GetUserByEmail(ctx, email)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("auth: get user by email: %w", err)
	}
	return toUser(u), nil
}

// List returns all users ordered by creation time.
func (s *UserStore) List() ([]User, error) {
	rows, err := s.q.ListUsers(context.Background())
	if err != nil {
		return nil, fmt.Errorf("auth: list users: %w", err)
	}
	users := make([]User, len(rows))
	for i, row := range rows {
		users[i] = toUser(row)
	}
	return users, nil
}

// Update modifies a user's fields. Email, role, or active-state changes revoke every browser session.
func (s *UserStore) Update(id string, email, name *string, role *Role, active *bool) (User, error) {
	return s.update(id, email, name, role, active, nil)
}

func (s *UserStore) UpdateWithAudit(id string, email, name *string, role *Role, active *bool, event AuditEvent) (User, error) {
	return s.update(id, email, name, role, active, &event)
}

func (s *UserStore) update(id string, email, name *string, role *Role, active *bool, event *AuditEvent) (User, error) {
	ctx, cancel := DetachedWriteContext(context.Background())
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return User{}, fmt.Errorf("auth: open user conn: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return User{}, fmt.Errorf("auth: begin user update: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	q := generated.New(conn)
	row, err := q.GetUserByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("auth: get user by id: %w", err)
	}

	before := toUser(row)
	existing := before
	if email != nil {
		existing.Email = *email
	}
	if name != nil {
		existing.Name = *name
	}
	if role != nil {
		if !ValidRole(string(*role)) {
			return User{}, fmt.Errorf("auth: invalid role %q", *role)
		}
		existing.Role = *role
	}
	if active != nil {
		existing.Active = *active
	}
	if row.Role == "admin" && row.Active == 1 && (!existing.Active || existing.Role != "admin") {
		admins, err := q.CountActiveAdmins(ctx)
		if err != nil {
			return User{}, fmt.Errorf("auth: count active admins: %w", err)
		}
		if admins <= 1 {
			return User{}, ErrLastActiveAdmin
		}
	}

	activeInt := int64(0)
	if existing.Active {
		activeInt = 1
	}
	now := userTimestamp(time.Now())
	u, err := q.UpdateUser(ctx, generated.UpdateUserParams{
		Email:     existing.Email,
		Name:      sql.NullString{String: existing.Name, Valid: existing.Name != ""},
		Role:      string(existing.Role),
		Active:    activeInt,
		UpdatedAt: now,
		ID:        id,
	})
	if err != nil {
		return User{}, fmt.Errorf("auth: update user: %w", err)
	}
	securityChanged := before.Email != existing.Email || before.Role != existing.Role || before.Active != existing.Active
	if securityChanged {
		if err := q.IncrementUserAuthVersion(ctx, generated.IncrementUserAuthVersionParams{UpdatedAt: now, ID: id}); err != nil {
			return User{}, fmt.Errorf("auth: increment auth version: %w", err)
		}
		if _, err := conn.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
			return User{}, fmt.Errorf("auth: revoke user sessions: %w", err)
		}
		u, err = q.GetUserByID(ctx, id)
		if err != nil {
			return User{}, fmt.Errorf("auth: reload user after revocation: %w", err)
		}
	}
	if event != nil {
		event.TargetType = "user"
		event.TargetID = id
		if err := recordAudit(ctx, conn, *event); err != nil {
			return User{}, err
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return User{}, fmt.Errorf("auth: commit user update: %w", err)
	}
	committed = true
	return toUser(u), nil
}

// Delete removes a user and all of its browser sessions by ID.
func (s *UserStore) Delete(id string) error {
	return s.delete(id, nil)
}

func (s *UserStore) DeleteWithAudit(id string, event AuditEvent) error {
	return s.delete(id, &event)
}

func (s *UserStore) delete(id string, event *AuditEvent) error {
	ctx, cancel := DetachedWriteContext(context.Background())
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("auth: open user conn: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("auth: begin user delete: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	q := generated.New(conn)
	row, err := q.GetUserByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("auth: get user by id: %w", err)
	}
	if row.Role == "admin" && row.Active == 1 {
		admins, err := q.CountActiveAdmins(ctx)
		if err != nil {
			return fmt.Errorf("auth: count active admins: %w", err)
		}
		if admins <= 1 {
			return ErrLastActiveAdmin
		}
	}

	if event != nil {
		event.TargetType = "user"
		event.TargetID = id
		if err := recordAudit(ctx, conn, *event); err != nil {
			return err
		}
	}
	res, err := q.DeleteUser(ctx, id)
	if err != nil {
		return fmt.Errorf("auth: delete user: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("auth: count deleted users: %w", err)
	}
	if n == 0 {
		return ErrUserNotFound
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("auth: commit user delete: %w", err)
	}
	committed = true
	return nil
}

// TouchLogin updates the logged_in_at timestamp.
func (s *UserStore) TouchLogin(id string) error {
	return s.TouchLoginAt(id, time.Now().UTC())
}

func (s *UserStore) TouchLoginAt(id string, at time.Time) error {
	now := userTimestamp(at)
	return s.q.TouchLogin(context.Background(), generated.TouchLoginParams{
		LoggedInAt: sql.NullString{String: now, Valid: true},
		UpdatedAt:  now,
		ID:         id,
	})
}

// CountUsers returns the number of users in the database.
func (s *UserStore) CountUsers() (int64, error) {
	return s.q.CountUsers(context.Background())
}

// CountActiveAdmins returns the number of active admin users.
func (s *UserStore) CountActiveAdmins() (int64, error) {
	return s.q.CountActiveAdmins(context.Background())
}

// RevokeAllSessions invalidates and removes every browser session for a user.
func (s *UserStore) RevokeAllSessions(id string) error {
	return s.revokeAllSessions(id, nil)
}

func (s *UserStore) RevokeAllSessionsWithAudit(id string, event AuditEvent) error {
	return s.revokeAllSessions(id, &event)
}

func (s *UserStore) revokeAllSessions(id string, event *AuditEvent) error {
	ctx, cancel := DetachedWriteContext(context.Background())
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("auth: open session revocation conn: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("auth: begin session revocation: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	now := userTimestamp(time.Now())
	result, err := conn.ExecContext(ctx, `UPDATE users SET auth_version = auth_version + 1, updated_at = ? WHERE id = ?`, now, id)
	if err != nil {
		return fmt.Errorf("auth: increment auth version: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("auth: count revoked users: %w", err)
	}
	if rows == 0 {
		return ErrUserNotFound
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return fmt.Errorf("auth: delete user sessions: %w", err)
	}
	if event != nil {
		event.TargetType = "user"
		event.TargetID = id
		if err := recordAudit(ctx, conn, *event); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("auth: commit session revocation: %w", err)
	}
	committed = true
	return nil
}

// CreateFirstAdmin atomically creates the first admin user.
// Returns ErrSetupComplete if users already exist (race-safe).
func (s *UserStore) CreateFirstAdmin(email, name string) (User, error) {
	return s.createFirstAdmin(email, name, nil)
}

func (s *UserStore) CreateFirstAdminWithAudit(email, name string, event AuditEvent) (User, error) {
	return s.createFirstAdmin(email, name, &event)
}

func (s *UserStore) createFirstAdmin(email, name string, event *AuditEvent) (User, error) {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return User{}, fmt.Errorf("auth: open user conn: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return User{}, fmt.Errorf("auth: begin first admin setup: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	q := generated.New(conn)
	count, err := q.CountUsers(ctx)
	if err != nil {
		return User{}, fmt.Errorf("auth: check user count: %w", err)
	}
	if count > 0 {
		return User{}, ErrSetupComplete
	}

	params, err := newCreateUserParams(email, name, RoleAdmin)
	if err != nil {
		return User{}, err
	}
	u, err := q.CreateUser(ctx, params)
	if err != nil {
		return User{}, fmt.Errorf("auth: create first admin: %w", err)
	}
	if event != nil {
		event.TargetType = "user"
		event.TargetID = u.ID
		if err := recordAudit(ctx, conn, *event); err != nil {
			return User{}, err
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return User{}, fmt.Errorf("auth: commit first admin setup: %w", err)
	}
	committed = true
	return toUser(u), nil
}

// ErrSetupComplete is returned when setup is attempted but users already exist.
var ErrSetupComplete = errors.New("setup already complete")

func newCreateUserParams(email, name string, role Role) (generated.CreateUserParams, error) {
	id, err := appid.New()
	if err != nil {
		return generated.CreateUserParams{}, fmt.Errorf("auth: generate user id: %w", err)
	}
	now := userTimestamp(time.Now())
	return generated.CreateUserParams{
		ID:        id,
		Email:     email,
		Name:      sql.NullString{String: name, Valid: name != ""},
		Role:      string(role),
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}
