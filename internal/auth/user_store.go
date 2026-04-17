package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
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
	db *sql.DB
}

// NewUserStore creates a UserStore backed by the given database connection.
func NewUserStore(db *sql.DB) *UserStore {
	return &UserStore{db: db}
}

// Create adds a new user.
func (s *UserStore) Create(email, name, role string) (User, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return User{}, fmt.Errorf("auth: generate user id: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.Exec(
		`INSERT INTO users (id, email, name, role, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id.String(), email, name, role, now, now,
	)
	if err != nil {
		return User{}, fmt.Errorf("auth: create user: %w", err)
	}
	return s.GetByID(id.String())
}

// GetByID returns a user by ID.
func (s *UserStore) GetByID(id string) (User, error) {
	row := s.db.QueryRow(
		`SELECT id, email, name, role, active, key_hash, logged_in_at, created_at, updated_at FROM users WHERE id = ?`, id,
	)
	return scanUserRow(row)
}

// GetByEmail returns a user by email address.
func (s *UserStore) GetByEmail(email string) (User, error) {
	row := s.db.QueryRow(
		`SELECT id, email, name, role, active, key_hash, logged_in_at, created_at, updated_at FROM users WHERE email = ?`, email,
	)
	return scanUserRow(row)
}

// List returns all users ordered by creation time.
func (s *UserStore) List() ([]User, error) {
	rows, err := s.db.Query(
		`SELECT id, email, name, role, active, key_hash, logged_in_at, created_at, updated_at FROM users ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("auth: list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var name, keyHash, loggedIn sql.NullString
		var active int
		if err := rows.Scan(&u.ID, &u.Email, &name, &u.Role, &active, &keyHash, &loggedIn, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("auth: scan user: %w", err)
		}
		u.Name = name.String
		u.HasAPIKey = keyHash.Valid && keyHash.String != ""
		u.LoggedInAt = loggedIn.String
		u.Active = active == 1
		users = append(users, u)
	}
	if users == nil {
		users = []User{}
	}
	return users, rows.Err()
}

// Update modifies a user's fields. Nil pointers are skipped.
func (s *UserStore) Update(id string, email, name, role *string, active *bool) (User, error) {
	u, err := s.GetByID(id)
	if err != nil {
		return User{}, err
	}
	if email != nil {
		u.Email = *email
	}
	if name != nil {
		u.Name = *name
	}
	if role != nil {
		u.Role = *role
	}
	if active != nil {
		u.Active = *active
	}
	now := time.Now().UTC().Format(time.RFC3339)
	activeInt := 0
	if u.Active {
		activeInt = 1
	}
	_, err = s.db.Exec(
		`UPDATE users SET email=?, name=?, role=?, active=?, updated_at=? WHERE id=?`,
		u.Email, u.Name, u.Role, activeInt, now, id,
	)
	if err != nil {
		return User{}, fmt.Errorf("auth: update user: %w", err)
	}
	return s.GetByID(id)
}

// Delete removes a user by ID.
func (s *UserStore) Delete(id string) error {
	res, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
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
	_, err := s.db.Exec(`UPDATE users SET logged_in_at=?, updated_at=? WHERE id=?`, now, now, id)
	return err
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

func scanUserRow(row *sql.Row) (User, error) {
	var u User
	var name, keyHash, loggedIn sql.NullString
	var active int
	err := row.Scan(&u.ID, &u.Email, &name, &u.Role, &active, &keyHash, &loggedIn, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("auth: scan user: %w", err)
	}
	u.Name = name.String
	u.HasAPIKey = keyHash.Valid && keyHash.String != ""
	u.LoggedInAt = loggedIn.String
	u.Active = active == 1
	return u, nil
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
	_, err := s.db.Exec(`UPDATE users SET key_hash=?, updated_at=? WHERE id=?`, hash, now, userID)
	if err != nil {
		return "", fmt.Errorf("auth: store api key: %w", err)
	}
	return key, nil
}

// RevokeAPIKey removes the API key for a user.
func (s *UserStore) RevokeAPIKey(userID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`UPDATE users SET key_hash=NULL, updated_at=? WHERE id=?`, now, userID)
	return err
}

// GetByAPIKey looks up a user by their API key (plaintext → hash → lookup).
func (s *UserStore) GetByAPIKey(key string) (User, error) {
	hash := hashAPIKey(key)
	row := s.db.QueryRow(
		`SELECT id, email, name, role, active, key_hash, logged_in_at, created_at, updated_at FROM users WHERE key_hash = ?`, hash,
	)
	return scanUserRow(row)
}

func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}
