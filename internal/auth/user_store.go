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

// User represents an authenticated user.
type User struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Name       string `json:"name,omitempty"`
	Role       string `json:"role"`
	Active     bool   `json:"active"`
	LoggedInAt string `json:"logged_in_at,omitempty"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
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
		ID:         u.ID,
		Email:      u.Email,
		Name:       u.Name.String,
		Role:       u.Role,
		Active:     u.Active == 1,
		LoggedInAt: u.LoggedInAt.String,
		CreatedAt:  u.CreatedAt,
		UpdatedAt:  u.UpdatedAt,
	}
}

// Create adds a new user.
func (s *UserStore) Create(email, name, role string) (User, error) {
	params, err := newCreateUserParams(email, name, role)
	if err != nil {
		return User{}, err
	}
	u, err := s.q.CreateUser(context.Background(), params)
	if err != nil {
		return User{}, fmt.Errorf("auth: create user: %w", err)
	}
	return toUser(u), nil
}

// GetByID returns a user by ID.
func (s *UserStore) GetByID(id string) (User, error) {
	u, err := s.q.GetUserByID(context.Background(), id)
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
	u, err := s.q.GetUserByEmail(context.Background(), email)
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

// Update modifies a user's fields. Nil pointers are skipped.
func (s *UserStore) Update(id string, email, name, role *string, active *bool) (User, error) {
	ctx := context.Background()
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

	existing := toUser(row)
	if email != nil {
		existing.Email = *email
	}
	if name != nil {
		existing.Name = *name
	}
	if role != nil {
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
	now := time.Now().UTC().Format(time.RFC3339)
	u, err := q.UpdateUser(ctx, generated.UpdateUserParams{
		Email:     existing.Email,
		Name:      sql.NullString{String: existing.Name, Valid: existing.Name != ""},
		Role:      existing.Role,
		Active:    activeInt,
		UpdatedAt: now,
		ID:        id,
	})
	if err != nil {
		return User{}, fmt.Errorf("auth: update user: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return User{}, fmt.Errorf("auth: commit user update: %w", err)
	}
	committed = true
	return toUser(u), nil
}

// Delete removes a user by ID.
func (s *UserStore) Delete(id string) error {
	ctx := context.Background()
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

	res, err := q.DeleteUser(ctx, id)
	if err != nil {
		return fmt.Errorf("auth: delete user: %w", err)
	}
	n, _ := res.RowsAffected()
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
	now := at.UTC().Truncate(TokenTimePrecision).Format(time.RFC3339Nano)
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

// CreateFirstAdmin atomically creates the first admin user.
// Returns ErrSetupComplete if users already exist (race-safe).
func (s *UserStore) CreateFirstAdmin(email, name string) (User, error) {
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

	params, err := newCreateUserParams(email, name, "admin")
	if err != nil {
		return User{}, err
	}
	u, err := q.CreateUser(ctx, params)
	if err != nil {
		return User{}, fmt.Errorf("auth: create first admin: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return User{}, fmt.Errorf("auth: commit first admin setup: %w", err)
	}
	committed = true
	return toUser(u), nil
}

// ErrSetupComplete is returned when setup is attempted but users already exist.
var ErrSetupComplete = errors.New("setup already complete")

func newCreateUserParams(email, name, role string) (generated.CreateUserParams, error) {
	id, err := appid.New()
	if err != nil {
		return generated.CreateUserParams{}, fmt.Errorf("auth: generate user id: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return generated.CreateUserParams{
		ID:        id,
		Email:     email,
		Name:      sql.NullString{String: name, Valid: name != ""},
		Role:      role,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}
