package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/fanout/internal/db/generated"
)

// ErrUserNotFound is returned when a requested user does not exist.
var ErrUserNotFound = errors.New("user not found")

// User represents an authenticated user.
type User struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Name       string `json:"name,omitempty"`
	Role       string `json:"role"`
	Active     bool   `json:"active"`
	HasAPIKey  bool   `json:"has_api_key"`
	LoggedInAt string `json:"logged_in_at,omitempty"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// UserStore provides CRUD operations for users.
type UserStore struct {
	q *generated.Queries
}

// NewUserStore creates a UserStore backed by the given database connection.
func NewUserStore(db *sql.DB) *UserStore {
	return &UserStore{q: generated.New(db)}
}

// toUser converts a generated.User to the domain User type.
func toUser(u generated.User) User {
	return User{
		ID:         u.ID,
		Email:      u.Email,
		Name:       u.Name.String,
		Role:       u.Role,
		Active:     u.Active == 1,
		HasAPIKey:  u.KeyHash.Valid && u.KeyHash.String != "",
		LoggedInAt: u.LoggedInAt.String,
		CreatedAt:  u.CreatedAt,
		UpdatedAt:  u.UpdatedAt,
	}
}

// Create adds a new user.
func (s *UserStore) Create(email, name, role string) (User, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return User{}, fmt.Errorf("auth: generate user id: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	u, err := s.q.CreateUser(context.Background(), generated.CreateUserParams{
		ID:        id.String(),
		Email:     email,
		Name:      sql.NullString{String: name, Valid: name != ""},
		Role:      role,
		CreatedAt: now,
		UpdatedAt: now,
	})
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
	existing, err := s.GetByID(id)
	if err != nil {
		return User{}, err
	}
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
	activeInt := int64(0)
	if existing.Active {
		activeInt = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	u, err := s.q.UpdateUser(context.Background(), generated.UpdateUserParams{
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
	return toUser(u), nil
}

// Delete removes a user by ID.
func (s *UserStore) Delete(id string) error {
	res, err := s.q.DeleteUser(context.Background(), id)
	if err != nil {
		return fmt.Errorf("auth: delete user: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// TouchLogin updates the logged_in_at timestamp.
func (s *UserStore) TouchLogin(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	return s.q.TouchLogin(context.Background(), generated.TouchLoginParams{
		LoggedInAt: sql.NullString{String: now, Valid: true},
		UpdatedAt:  now,
		ID:         id,
	})
}

// EnsureAdmin creates the admin user if it doesn't exist.
func (s *UserStore) EnsureAdmin(email string) error {
	_, err := s.GetByEmail(email)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrUserNotFound) {
		return err
	}
	_, err = s.Create(email, "", "admin")
	return err
}

// GenerateAPIKey creates a new API key for the user. Returns the plaintext key.
// The key is stored as a SHA-256 hash in the database.
func (s *UserStore) GenerateAPIKey(userID string) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate api key: %w", err)
	}
	key := "fo_" + hex.EncodeToString(b)
	hash := hashAPIKey(key)

	now := time.Now().UTC().Format(time.RFC3339)
	err := s.q.SetAPIKeyHash(context.Background(), generated.SetAPIKeyHashParams{
		KeyHash:   sql.NullString{String: hash, Valid: true},
		UpdatedAt: now,
		ID:        userID,
	})
	if err != nil {
		return "", fmt.Errorf("auth: store api key: %w", err)
	}
	return key, nil
}

// RevokeAPIKey removes the API key for a user.
func (s *UserStore) RevokeAPIKey(userID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	return s.q.RevokeAPIKey(context.Background(), generated.RevokeAPIKeyParams{
		UpdatedAt: now,
		ID:        userID,
	})
}

// GetByAPIKey looks up a user by their API key (plaintext → hash → lookup).
func (s *UserStore) GetByAPIKey(key string) (User, error) {
	hash := hashAPIKey(key)
	u, err := s.q.GetUserByKeyHash(context.Background(), sql.NullString{String: hash, Valid: true})
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("auth: get user by api key: %w", err)
	}
	return toUser(u), nil
}

func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}
