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
func GenerateCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("auth: generate code: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// HashCode produces an HMAC-SHA256 hex digest of the code.
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

// CodeStore manages verification codes in SQLite.
type CodeStore struct {
	db     *sql.DB
	secret string
}

// NewCodeStore creates a CodeStore with the given HMAC secret.
func NewCodeStore(db *sql.DB, secret string) *CodeStore {
	return &CodeStore{db: db, secret: secret}
}

// Create stores a verification code for the given email. Returns the plaintext code.
func (s *CodeStore) Create(email string) (string, error) {
	code, err := GenerateCode()
	if err != nil {
		return "", err
	}
	hash := HashCode(code, s.secret)
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("auth: generate code id: %w", err)
	}
	expiresAt := time.Now().Add(codeTTL).UTC().Format(time.RFC3339)

	_, err = s.db.Exec(
		`INSERT INTO verification_codes (id, email, code_hash, expires_at) VALUES (?, ?, ?, ?)`,
		id.String(), email, hash, expiresAt,
	)
	if err != nil {
		return "", fmt.Errorf("auth: create code: %w", err)
	}
	return code, nil
}

// Verify checks the code for the given email. Returns true if valid.
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

	if !rows.Next() {
		rows.Close()
		return false, nil
	}

	var id, hash, expiresAt string
	var attempts, used int
	if err := rows.Scan(&id, &hash, &attempts, &used, &expiresAt); err != nil {
		rows.Close()
		return false, fmt.Errorf("auth: scan code: %w", err)
	}
	// Close rows before running UPDATEs to avoid SQLite connection contention
	rows.Close()

	// Check expiry
	exp, parseErr := time.Parse(time.RFC3339, expiresAt)
	if parseErr != nil {
		return false, fmt.Errorf("auth: corrupt expiry timestamp: %w", parseErr)
	}
	if time.Now().After(exp) {
		return false, nil
	}

	// Check max attempts
	if attempts >= maxAttempts {
		return false, nil
	}

	// Verify
	if !VerifyHash(code, hash, s.secret) {
		if _, err := s.db.Exec(`UPDATE verification_codes SET attempts = attempts + 1 WHERE id = ?`, id); err != nil {
			return false, fmt.Errorf("auth: increment attempts: %w", err)
		}
		return false, nil
	}

	// Mark used — must succeed to prevent replay
	if _, err := s.db.Exec(`UPDATE verification_codes SET used = 1 WHERE id = ?`, id); err != nil {
		return false, fmt.Errorf("auth: mark code used: %w", err)
	}
	return true, nil
}

// Cleanup deletes expired codes older than 1 hour.
func (s *CodeStore) Cleanup() error {
	cutoff := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`DELETE FROM verification_codes WHERE expires_at < ?`, cutoff)
	return err
}
