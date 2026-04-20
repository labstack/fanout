# Auth Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add passwordless email auth with JWT tokens, three roles (viewer/operator/admin), and SMTP email delivery to the self-hosted Fanout binary. Matches monk's auth flow (magic link → JWT) adapted for Go.

**Architecture:** User store in SQLite (existing `fanout.sqlite`), SMTP for email codes, HS256 JWT for sessions. Auth middleware replaces the current `API_TOKEN` check while keeping it as a fallback. If SMTP is not configured, auth is disabled (current behavior preserved).

**Tech Stack:** Go stdlib `net/smtp` + `crypto/hmac` + `github.com/golang-jwt/jwt/v5`

**Frontend plan:** Separate doc — `2026-04-16-auth-frontend.md` (login page, auth context, protected routes, user management).

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/config/config.go` | Modify | Add SMTP + auth config fields |
| `internal/store/store.go` | Modify | Add users + verification_codes tables to migrations |
| `internal/auth/user_store.go` | Create | User CRUD (create, get by email/id, list, update, delete) |
| `internal/auth/user_store_test.go` | Create | Tests for user store |
| `internal/auth/code.go` | Create | Verification code generation, HMAC hashing, validation |
| `internal/auth/code_test.go` | Create | Tests for code logic |
| `internal/auth/smtp.go` | Create | SMTP email sender (send verification code) |
| `internal/auth/smtp_test.go` | Create | Tests for email rendering |
| `internal/auth/jwt.go` | Create | JWT sign/verify (access + refresh tokens) |
| `internal/auth/jwt_test.go` | Create | Tests for JWT |
| `internal/api/auth.go` | Create | Auth HTTP handlers (start, verify, refresh, me, logout) |
| `internal/api/auth_test.go` | Create | Tests for auth handlers |
| `internal/api/users.go` | Create | Admin user management endpoints (list, create, update, delete) |
| `go.mod` / `go.sum` | Modify | Add `github.com/golang-jwt/jwt/v5` dependency |
| `cmd/fanout/main.go` | Modify | Wire auth store, register routes, update middleware |

---

## Task 1: Config — SMTP + Auth Fields

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add auth config fields**

Add to the `Config` struct after the AI fields:

```go
// Auth
SMTPHost  string // SMTP server host
SMTPPort  int    // SMTP server port (default 587)
SMTPUser  string // SMTP username
SMTPPass  string // SMTP password
SMTPFrom  string // Sender email address
AdminEmail string // First admin user (created on boot)
JWTSecret  string // HS256 signing key (auto-generated if empty)
```

Add to `Load()`:

```go
SMTPHost:   os.Getenv("SMTP_HOST"),
SMTPPort:   getenvInt("SMTP_PORT", 587),
SMTPUser:   os.Getenv("SMTP_USER"),
SMTPPass:   os.Getenv("SMTP_PASS"),
SMTPFrom:   getenv("SMTP_FROM", "Fanout <noreply@fanout.dev>"),
AdminEmail: os.Getenv("ADMIN_EMAIL"),
JWTSecret:  os.Getenv("JWT_SECRET"),
```

Add a helper method:

```go
// AuthEnabled returns true if SMTP is configured for passwordless login.
func (c Config) AuthEnabled() bool {
	return c.SMTPHost != "" && c.SMTPUser != "" && c.SMTPPass != ""
}
```

Add to `Validate()`:

```go
if c.AuthEnabled() {
	if c.SMTPFrom == "" {
		return fmt.Errorf("SMTP_FROM is required when auth is enabled")
	}
	if c.SMTPPort <= 0 {
		return fmt.Errorf("SMTP_PORT must be > 0 when auth is enabled")
	}
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/config/...`

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(auth): add SMTP and auth config fields"
```

---

## Task 2: SQLite Migrations — Users + Verification Codes

**Files:**
- Modify: `internal/store/store.go`

- [ ] **Step 1: Add users and verification_codes tables**

Add to the `stmts` slice in `migrate()`:

```go
`CREATE TABLE IF NOT EXISTS users (
	id         TEXT PRIMARY KEY,
	email      TEXT NOT NULL UNIQUE,
	name       TEXT,
	role       TEXT NOT NULL DEFAULT 'operator',
	active     INTEGER NOT NULL DEFAULT 1,
	logged_in_at TEXT,
	created_at TEXT DEFAULT (datetime('now')),
	updated_at TEXT DEFAULT (datetime('now'))
)`,
`CREATE TABLE IF NOT EXISTS verification_codes (
	id         TEXT PRIMARY KEY,
	email      TEXT NOT NULL,
	code_hash  TEXT NOT NULL,
	attempts   INTEGER NOT NULL DEFAULT 0,
	used       INTEGER NOT NULL DEFAULT 0,
	expires_at TEXT NOT NULL,
	created_at TEXT DEFAULT (datetime('now'))
)`,
`CREATE INDEX IF NOT EXISTS idx_verification_codes_email ON verification_codes(email)`,
```

- [ ] **Step 2: Run existing tests**

Run: `go test ./internal/store/ -v`

- [ ] **Step 3: Commit**

```bash
git add internal/store/store.go
git commit -m "feat(auth): add users and verification_codes tables"
```

---

## Task 3: User Store

**Files:**
- Create: `internal/auth/user_store.go`
- Create: `internal/auth/user_store_test.go`

- [ ] **Step 1: Create user store**

```go
package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var ErrUserNotFound = errors.New("user not found")

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

type UserStore struct {
	db *sql.DB
}

func NewUserStore(db *sql.DB) *UserStore {
	return &UserStore{db: db}
}

func (s *UserStore) Create(email, name, role string) (User, error) {
	id, _ := uuid.NewV7()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		`INSERT INTO users (id, email, name, role, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id.String(), email, name, role, now, now,
	)
	if err != nil {
		return User{}, fmt.Errorf("auth: create user: %w", err)
	}
	return s.GetByID(id.String())
}

func (s *UserStore) GetByID(id string) (User, error) {
	return s.queryOne(`SELECT id, email, name, role, active, logged_in_at, created_at, updated_at FROM users WHERE id = ?`, id)
}

func (s *UserStore) GetByEmail(email string) (User, error) {
	return s.queryOne(`SELECT id, email, name, role, active, logged_in_at, created_at, updated_at FROM users WHERE email = ?`, email)
}

func (s *UserStore) List() ([]User, error) {
	rows, err := s.db.Query(`SELECT id, email, name, role, active, logged_in_at, created_at, updated_at FROM users ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("auth: list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	if users == nil {
		users = []User{}
	}
	return users, rows.Err()
}

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
	_, err = s.db.Exec(
		`UPDATE users SET email=?, name=?, role=?, active=?, updated_at=? WHERE id=?`,
		u.Email, u.Name, u.Role, u.Active, now, id,
	)
	if err != nil {
		return User{}, fmt.Errorf("auth: update user: %w", err)
	}
	return s.GetByID(id)
}

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

func (s *UserStore) TouchLogin(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`UPDATE users SET logged_in_at=?, updated_at=? WHERE id=?`, now, now, id)
	return err
}

// EnsureAdmin creates the admin user if it doesn't exist.
func (s *UserStore) EnsureAdmin(email string) error {
	_, err := s.GetByEmail(email)
	if err == nil {
		return nil // already exists
	}
	if !errors.Is(err, ErrUserNotFound) {
		return err
	}
	_, err = s.Create(email, "", "admin")
	return err
}

func (s *UserStore) queryOne(query, arg string) (User, error) {
	row := s.db.QueryRow(query, arg)
	u, err := scanUserRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return u, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanUser(s scanner) (User, error) {
	var u User
	var name, loggedIn sql.NullString
	var active int
	err := s.Scan(&u.ID, &u.Email, &name, &u.Role, &active, &loggedIn, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return User{}, err
	}
	u.Name = name.String
	u.LoggedInAt = loggedIn.String
	u.Active = active == 1
	return u, nil
}

func scanUserRow(row *sql.Row) (User, error) {
	var u User
	var name, loggedIn sql.NullString
	var active int
	err := row.Scan(&u.ID, &u.Email, &name, &u.Role, &active, &loggedIn, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return User{}, err
	}
	u.Name = name.String
	u.LoggedInAt = loggedIn.String
	u.Active = active == 1
	return u, nil
}
```

- [ ] **Step 2: Create tests**

```go
package auth

import (
	"testing"

	appstore "github.com/labstack/fanout/internal/store"
)

func newTestUserStore(t *testing.T) *UserStore {
	t.Helper()
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })
	return NewUserStore(sqlite.DB)
}

func TestUserStore_CreateAndGet(t *testing.T) {
	s := newTestUserStore(t)

	u, err := s.Create("test@example.com", "Test User", "operator")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.Email != "test@example.com" {
		t.Errorf("email = %q, want test@example.com", u.Email)
	}
	if u.Role != "operator" {
		t.Errorf("role = %q, want operator", u.Role)
	}

	got, err := s.GetByEmail("test@example.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("id mismatch")
	}
}

func TestUserStore_EnsureAdmin(t *testing.T) {
	s := newTestUserStore(t)

	err := s.EnsureAdmin("admin@example.com")
	if err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}

	u, err := s.GetByEmail("admin@example.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if u.Role != "admin" {
		t.Errorf("role = %q, want admin", u.Role)
	}

	// Second call should be idempotent
	err = s.EnsureAdmin("admin@example.com")
	if err != nil {
		t.Fatalf("EnsureAdmin (second): %v", err)
	}
}

func TestUserStore_List(t *testing.T) {
	s := newTestUserStore(t)

	s.Create("a@example.com", "", "viewer")
	s.Create("b@example.com", "", "operator")

	users, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("len = %d, want 2", len(users))
	}
}

func TestUserStore_Delete(t *testing.T) {
	s := newTestUserStore(t)

	u, _ := s.Create("del@example.com", "", "viewer")
	err := s.Delete(u.ID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = s.GetByID(u.ID)
	if err == nil {
		t.Fatal("expected ErrUserNotFound after delete")
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/auth/ -run TestUserStore -v`

- [ ] **Step 4: Commit**

```bash
git add internal/auth/user_store.go internal/auth/user_store_test.go
git commit -m "feat(auth): add user store with CRUD operations"
```

---

## Task 4: Verification Codes

**Files:**
- Create: `internal/auth/code.go`
- Create: `internal/auth/code_test.go`

- [ ] **Step 1: Create code.go**

```go
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
)

const (
	codeTTL     = 5 * time.Minute
	maxAttempts = 3
)

// GenerateCode returns a random 6-digit code.
func GenerateCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	return fmt.Sprintf("%06d", n.Int64())
}

// HashCode produces an HMAC-SHA256 hex digest of the code using the given secret.
func HashCode(code, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(code))
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyHash checks a code against a hash using constant-time comparison.
func VerifyHash(code, hash, secret string) bool {
	expected := HashCode(code, secret)
	return hmac.Equal([]byte(expected), []byte(hash))
}

type CodeStore struct {
	db     *sql.DB
	secret string
}

func NewCodeStore(db *sql.DB, secret string) *CodeStore {
	return &CodeStore{db: db, secret: secret}
}

// Create stores a verification code for the given email. Returns the plaintext code.
func (s *CodeStore) Create(email string) (string, error) {
	code := GenerateCode()
	hash := HashCode(code, s.secret)
	id, _ := uuid.NewV7()
	expiresAt := time.Now().Add(codeTTL).UTC().Format(time.RFC3339)

	_, err := s.db.Exec(
		`INSERT INTO verification_codes (id, email, code_hash, expires_at) VALUES (?, ?, ?, ?)`,
		id.String(), email, hash, expiresAt,
	)
	if err != nil {
		return "", fmt.Errorf("auth: create code: %w", err)
	}
	return code, nil
}

// Verify checks the code for the given email. Returns true if valid.
// Marks the code as used and increments attempts on failure.
func (s *CodeStore) Verify(email, code string) (bool, error) {
	rows, err := s.db.Query(
		`SELECT id, code_hash, attempts, used, expires_at FROM verification_codes
		 WHERE email = ? AND used = 0
		 ORDER BY created_at DESC LIMIT 1`,
		email,
	)
	if err != nil {
		return false, fmt.Errorf("auth: query code: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return false, nil
	}

	var id, hash, expiresAt string
	var attempts, used int
	if err := rows.Scan(&id, &hash, &attempts, &used, &expiresAt); err != nil {
		return false, fmt.Errorf("auth: scan code: %w", err)
	}

	// Check expiry
	exp, _ := time.Parse(time.RFC3339, expiresAt)
	if time.Now().After(exp) {
		return false, nil
	}

	// Check max attempts
	if attempts >= maxAttempts {
		return false, nil
	}

	// Close rows before running UPDATEs to avoid SQLite connection contention
	rows.Close()

	// Verify
	if !VerifyHash(code, hash, s.secret) {
		s.db.Exec(`UPDATE verification_codes SET attempts = attempts + 1 WHERE id = ?`, id)
		return false, nil
	}

	// Mark used
	s.db.Exec(`UPDATE verification_codes SET used = 1 WHERE id = ?`, id)
	return true, nil
}

// Cleanup deletes expired codes older than 1 hour.
func (s *CodeStore) Cleanup() {
	cutoff := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	s.db.Exec(`DELETE FROM verification_codes WHERE expires_at < ?`, cutoff)
}
```

- [ ] **Step 2: Create tests**

```go
package auth

import (
	"testing"

	appstore "github.com/labstack/fanout/internal/store"
)

func newTestCodeStore(t *testing.T) *CodeStore {
	t.Helper()
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })
	return NewCodeStore(sqlite.DB, "test-secret")
}

func TestGenerateCode_SixDigits(t *testing.T) {
	code := GenerateCode()
	if len(code) != 6 {
		t.Errorf("code = %q, want 6 digits", code)
	}
}

func TestHashCode_Deterministic(t *testing.T) {
	h1 := HashCode("123456", "secret")
	h2 := HashCode("123456", "secret")
	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
}

func TestVerifyHash(t *testing.T) {
	hash := HashCode("123456", "secret")
	if !VerifyHash("123456", hash, "secret") {
		t.Error("valid code should verify")
	}
	if VerifyHash("000000", hash, "secret") {
		t.Error("wrong code should not verify")
	}
}

func TestCodeStore_CreateAndVerify(t *testing.T) {
	s := newTestCodeStore(t)

	code, err := s.Create("test@example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ok, err := s.Verify("test@example.com", code)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Error("valid code should verify")
	}

	// Used code should not verify again
	ok, _ = s.Verify("test@example.com", code)
	if ok {
		t.Error("used code should not verify again")
	}
}

func TestCodeStore_WrongCode(t *testing.T) {
	s := newTestCodeStore(t)

	s.Create("test@example.com")

	ok, _ := s.Verify("test@example.com", "000000")
	if ok {
		t.Error("wrong code should not verify")
	}
}

func TestCodeStore_MaxAttempts(t *testing.T) {
	s := newTestCodeStore(t)

	code, _ := s.Create("test@example.com")

	// Exhaust attempts
	for i := 0; i < 3; i++ {
		s.Verify("test@example.com", "000000")
	}

	// Correct code should now fail
	ok, _ := s.Verify("test@example.com", code)
	if ok {
		t.Error("should fail after max attempts")
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/auth/ -run "TestGenerateCode\|TestHashCode\|TestVerifyHash\|TestCodeStore" -v`

- [ ] **Step 4: Commit**

```bash
git add internal/auth/code.go internal/auth/code_test.go
git commit -m "feat(auth): add verification code generation and validation"
```

---

## Task 5: SMTP Email Sender

**Files:**
- Create: `internal/auth/smtp.go`
- Create: `internal/auth/smtp_test.go`

- [ ] **Step 1: Create smtp.go**

```go
package auth

import (
	"fmt"
	"net/smtp"
	"strings"
)

// SMTPConfig holds SMTP connection settings.
type SMTPConfig struct {
	Host string
	Port int
	User string
	Pass string
	From string
}

// SendCode sends a verification code email.
func SendCode(cfg SMTPConfig, to, code string) error {
	subject := fmt.Sprintf("Fanout login code: %s", code)
	body := fmt.Sprintf(
		"Your Fanout verification code is:\n\n  %s\n\nThis code expires in 5 minutes.\n\nIf you did not request this, you can safely ignore this email.",
		code,
	)

	msg := strings.Join([]string{
		"From: " + cfg.From,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}, "\r\n")

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	auth := smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)

	return smtp.SendMail(addr, auth, cfg.From, []string{to}, []byte(msg))
}
```

- [ ] **Step 2: Create test (template rendering only, no actual SMTP)**

```go
package auth

import "testing"

func TestSMTPConfig_Valid(t *testing.T) {
	cfg := SMTPConfig{
		Host: "smtp.resend.com",
		Port: 587,
		User: "resend",
		Pass: "re_test",
		From: "noreply@fanout.dev",
	}
	if cfg.Host == "" || cfg.Port == 0 {
		t.Error("config should be valid")
	}
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/auth/smtp.go internal/auth/smtp_test.go
git commit -m "feat(auth): add SMTP email sender for verification codes"
```

---

## Task 6: JWT Tokens

**Files:**
- Create: `internal/auth/jwt.go`
- Create: `internal/auth/jwt_test.go`

- [ ] **Step 1: Create jwt.go**

```go
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	AccessTTL  = 15 * time.Minute
	RefreshTTL = 7 * 24 * time.Hour
)

// Claims is the JWT payload for access tokens.
type Claims struct {
	jwt.RegisteredClaims
	Email string `json:"email"`
	Role  string `json:"role"`
}

// SignAccess creates a short-lived access token.
func SignAccess(secret, userID, email, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTTL)),
		},
		Email: email,
		Role:  role,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// SignRefresh creates a long-lived refresh token.
func SignRefresh(secret, userID string) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   userID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(RefreshTTL)),
		ID:        generateJTI(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// VerifyAccess validates an access token and returns the claims.
func VerifyAccess(secret, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

// VerifyRefresh validates a refresh token and returns the subject (user ID).
func VerifyRefresh(secret, tokenStr string) (string, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return "", err
	}
	claims, ok := token.Claims.(*jwt.RegisteredClaims)
	if !ok || !token.Valid {
		return "", fmt.Errorf("invalid token")
	}
	return claims.Subject, nil
}

// GenerateSecret creates a random 32-byte hex secret for JWT signing.
func GenerateSecret() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func generateJTI() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
```

- [ ] **Step 2: Create tests**

```go
package auth

import "testing"

func TestSignAndVerifyAccess(t *testing.T) {
	secret := "test-secret-key"

	token, err := SignAccess(secret, "user-123", "test@example.com", "admin")
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}

	claims, err := VerifyAccess(secret, token)
	if err != nil {
		t.Fatalf("VerifyAccess: %v", err)
	}

	if claims.Subject != "user-123" {
		t.Errorf("subject = %q, want user-123", claims.Subject)
	}
	if claims.Email != "test@example.com" {
		t.Errorf("email = %q, want test@example.com", claims.Email)
	}
	if claims.Role != "admin" {
		t.Errorf("role = %q, want admin", claims.Role)
	}
}

func TestVerifyAccess_WrongSecret(t *testing.T) {
	token, _ := SignAccess("secret-1", "user-123", "test@example.com", "admin")
	_, err := VerifyAccess("secret-2", token)
	if err == nil {
		t.Error("should fail with wrong secret")
	}
}

func TestSignAndVerifyRefresh(t *testing.T) {
	secret := "test-refresh-secret"

	token, err := SignRefresh(secret, "user-456")
	if err != nil {
		t.Fatalf("SignRefresh: %v", err)
	}

	userID, err := VerifyRefresh(secret, token)
	if err != nil {
		t.Fatalf("VerifyRefresh: %v", err)
	}
	if userID != "user-456" {
		t.Errorf("userID = %q, want user-456", userID)
	}
}

func TestGenerateSecret_Length(t *testing.T) {
	s := GenerateSecret()
	if len(s) != 64 { // 32 bytes = 64 hex chars
		t.Errorf("secret length = %d, want 64", len(s))
	}
}
```

- [ ] **Step 3: Add dependency**

Run: `go get github.com/golang-jwt/jwt/v5`

- [ ] **Step 4: Run tests**

Run: `go test ./internal/auth/ -run "TestSign\|TestVerify\|TestGenerate" -v`

- [ ] **Step 5: Commit**

```bash
git add internal/auth/jwt.go internal/auth/jwt_test.go go.mod go.sum
git commit -m "feat(auth): add JWT sign/verify for access and refresh tokens"
```

---

## Task 7: Auth HTTP Handlers

**Files:**
- Create: `internal/api/auth.go`
- Create: `internal/api/auth_test.go`

- [ ] **Step 1: Create auth.go**

```go
package api

import (
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
)

type AuthHandler struct {
	users     *auth.UserStore
	codes     *auth.CodeStore
	jwtSecret string
	smtp      auth.SMTPConfig
}

func RegisterAuthRoutes(e *echo.Echo, users *auth.UserStore, codes *auth.CodeStore, jwtSecret string, smtp auth.SMTPConfig) {
	h := &AuthHandler{
		users:     users,
		codes:     codes,
		jwtSecret: jwtSecret,
		smtp:      smtp,
	}

	e.POST("/api/auth/start", h.Start)
	e.POST("/api/auth/verify", h.Verify)
	e.POST("/api/auth/refresh", h.Refresh)
	e.GET("/api/auth/me", h.Me)
	e.POST("/api/auth/logout", h.Logout)
}

// jitter adds random delay to prevent timing attacks.
func jitter() {
	time.Sleep(time.Duration(50+rand.IntN(100)) * time.Millisecond)
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

	// Check if user exists and is active
	user, err := h.users.GetByEmail(email)
	if err != nil || !user.Active {
		jitter()
		return c.JSON(200, map[string]bool{"code_sent": true}) // Don't reveal if account exists
	}

	code, err := h.codes.Create(email)
	if err != nil {
		slog.Error("auth: create verification code", "err", err)
		jitter()
		return c.JSON(200, map[string]bool{"code_sent": true})
	}

	go func() {
		if err := auth.SendCode(h.smtp, email, code); err != nil {
			slog.Error("auth: send verification email failed — logging code for dev use", "email", email, "code", code, "err", err)
		}
	}()

	jitter()
	return c.JSON(200, map[string]bool{"code_sent": true})
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

	h.users.TouchLogin(user.ID)

	secret := h.jwtSecret
	accessToken, err := auth.SignAccess(secret, user.ID, user.Email, user.Role)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create token")
	}

	refreshToken, err := auth.SignRefresh(secret, user.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create token")
	}

	// Set refresh token as httpOnly cookie
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

	secret := h.jwtSecret
	userID, err := auth.VerifyRefresh(secret, cookie.Value)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid refresh token")
	}

	user, err := h.users.GetByID(userID)
	if err != nil || !user.Active {
		return echo.NewHTTPError(http.StatusUnauthorized, "user not found or inactive")
	}

	accessToken, err := auth.SignAccess(secret, user.ID, user.Email, user.Role)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create token")
	}

	return c.JSON(200, map[string]string{
		"access_token": accessToken,
	})
}

// Me returns the current authenticated user.
func (h *AuthHandler) Me(c *echo.Context) error {
	claims := getAuthClaims(c)
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
		MaxAge:   -1,
	})
	return c.JSON(200, map[string]bool{"ok": true})
}

// getAuthClaims retrieves JWT claims from the echo context (set by middleware).
func getAuthClaims(c *echo.Context) *auth.Claims {
	v := c.Get("auth_claims")
	if v == nil {
		return nil
	}
	claims, _ := v.(*auth.Claims)
	return claims
}
```

- [ ] **Step 2: Create basic tests**

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
	appstore "github.com/labstack/fanout/internal/store"
)

func newTestAuthHandler(t *testing.T) *AuthHandler {
	t.Helper()
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })

	users := auth.NewUserStore(sqlite.DB)
	codes := auth.NewCodeStore(sqlite.DB, "test-secret")
	cfg := config.Config{JWTSecret: "test-jwt-secret"}

	return &AuthHandler{users: users, codes: codes, cfg: cfg}
}

func TestAuthStart_EmptyEmail(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/start", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := newTestAuthHandler(t)
	err := h.Start(c)
	if err == nil {
		t.Fatal("expected error for empty email")
	}
}

func TestAuthVerify_InvalidCode(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/verify",
		strings.NewReader(`{"email":"test@example.com","code":"000000"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := newTestAuthHandler(t)
	err := h.Verify(c)
	if err == nil {
		t.Fatal("expected 401 for invalid code")
	}
	httpErr, _ := err.(*echo.HTTPError)
	if httpErr.Code != 401 {
		t.Errorf("code = %d, want 401", httpErr.Code)
	}
}

func TestAuthLogout(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := newTestAuthHandler(t)
	err := h.Logout(c)
	if err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/api/ -run TestAuth -v`

- [ ] **Step 4: Commit**

```bash
git add internal/api/auth.go internal/api/auth_test.go
git commit -m "feat(auth): add auth HTTP handlers (start, verify, refresh, me, logout)"
```

---

## Task 8: Auth Middleware + Wire Into Main

**Files:**
- Modify: `cmd/fanout/main.go`

- [ ] **Step 1: Update the auth middleware in main.go**

Replace the current `API_TOKEN` middleware block (lines ~148-171) with a dual auth check that supports both JWT and API_TOKEN:

The middleware should:
1. Skip auth for health, metrics, SPA routes, and `/api/auth/*` endpoints
2. If `Authorization: Bearer` header is present:
   - Try JWT verification first
   - If JWT fails and `API_TOKEN` is set, try constant-time token comparison
3. If neither works, return 401
4. On successful JWT auth, set `auth_claims` in echo context
5. If auth is not enabled (`!cfg.AuthEnabled()`), fall back to API_TOKEN-only behavior (current)

Add after the existing routes:

```go
// Auth routes (only if SMTP configured)
if cfg.AuthEnabled() {
	userStore := auth.NewUserStore(sqlite.DB)
	codeStore := auth.NewCodeStore(sqlite.DB, jwtSecret)

	// Create admin user on first boot
	if cfg.AdminEmail != "" {
		if err := userStore.EnsureAdmin(cfg.AdminEmail); err != nil {
			slog.Error("create admin user failed", "err", err)
		} else {
			slog.Info("admin user ensured", "email", cfg.AdminEmail)
		}
	}

	api.RegisterAuthRoutes(e, cfg, userStore, codeStore)
}
```

Generate JWT secret if not provided:

```go
jwtSecret := cfg.JWTSecret
if jwtSecret == "" {
	jwtSecret = auth.GenerateSecret()
	slog.Warn("JWT_SECRET not set — generated ephemeral secret (sessions won't survive restart)")
}
```

- [ ] **Step 2: Add role enforcement middleware helper**

In `internal/api/auth.go`, add:

```go
// RequireRole returns a middleware that checks the user's role.
// Roles are hierarchical: admin > operator > viewer.
func RequireRole(minRole string) echo.MiddlewareFunc {
	levels := map[string]int{"viewer": 0, "operator": 1, "admin": 2}
	minLevel := levels[minRole]

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			claims := getAuthClaims(c)
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
```

- [ ] **Step 3: Verify compilation and tests**

Run: `go build ./... && go test ./internal/auth/ ./internal/api/ -v`

- [ ] **Step 4: Commit**

```bash
git add cmd/fanout/main.go internal/api/auth.go
git commit -m "feat(auth): wire auth into main, dual JWT + API_TOKEN middleware"
```

---

## Task 9: Admin User Management Endpoints

**Files:**
- Create: `internal/api/users.go`

- [ ] **Step 1: Create users.go**

```go
package api

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
)

type UserHandler struct {
	users *auth.UserStore
}

func RegisterUserRoutes(e *echo.Echo, users *auth.UserStore) {
	h := &UserHandler{users: users}

	// All user management requires admin role — enforced by middleware in main.go
	e.GET("/api/users", h.ListUsers)
	e.POST("/api/users", h.CreateUser)
	e.PUT("/api/users/:id", h.UpdateUser)
	e.DELETE("/api/users/:id", h.DeleteUser)
}

func (h *UserHandler) ListUsers(c *echo.Context) error {
	users, err := h.users.List()
	if err != nil {
		slog.Error("list users failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to list users")
	}
	return c.JSON(200, users)
}

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

	user, err := h.users.Create(req.Email, req.Name, role)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return echo.NewHTTPError(http.StatusConflict, "user already exists")
		}
		slog.Error("create user failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create user")
	}
	return c.JSON(201, user)
}

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
```

- [ ] **Step 2: Wire into main.go with admin role enforcement**

In main.go, after `RegisterAuthRoutes`:

```go
api.RegisterUserRoutes(e, userStore)
```

The `/api/users` endpoints should be gated by admin role in the middleware skip list — they are NOT skipped from auth, and require the JWT claims to have `role: "admin"`.

- [ ] **Step 3: Verify and commit**

Run: `go build ./...`

```bash
git add internal/api/users.go cmd/fanout/main.go
git commit -m "feat(auth): add admin user management endpoints"
```

---

## Task 10: Full Build Verification

- [ ] **Step 1: Run all Go tests**

Run: `go test ./...`

- [ ] **Step 2: Run full build**

Run: `just check`

- [ ] **Step 3: Test auth flow manually**

```bash
# Start fanout with SMTP config
SMTP_HOST=smtp.resend.com SMTP_PORT=587 SMTP_USER=resend SMTP_PASS=re_test SMTP_FROM=noreply@test.com ADMIN_EMAIL=admin@test.com JWT_SECRET=test-secret ./fanout

# Start auth flow
curl -X POST http://localhost:7520/api/auth/start -H 'Content-Type: application/json' -d '{"email":"admin@test.com"}'

# Check server logs for the code (if SMTP fails in dev)
# Verify code
curl -X POST http://localhost:7520/api/auth/verify -H 'Content-Type: application/json' -d '{"email":"admin@test.com","code":"123456"}'

# Use access token
curl http://localhost:7520/api/auth/me -H 'Authorization: Bearer <token>'

# API_TOKEN still works
curl http://localhost:7520/api/overview -H 'Authorization: Bearer <api_token>'
```
