package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/labstack/fanout/internal/db/generated"
	appid "github.com/labstack/fanout/internal/id"
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
	q      *generated.Queries
	secret string
}

// NewCodeStore creates a CodeStore with the given HMAC secret.
func NewCodeStore(db *sql.DB, secret string) *CodeStore {
	return &CodeStore{q: generated.New(db), secret: secret}
}

// Create stores a verification code for the given email. Returns the plaintext code.
func (s *CodeStore) Create(email string) (string, error) {
	code, err := GenerateCode()
	if err != nil {
		return "", err
	}
	hash := HashCode(code, s.secret)
	id, err := appid.New()
	if err != nil {
		return "", fmt.Errorf("auth: generate code id: %w", err)
	}
	expiresAt := time.Now().Add(codeTTL).UTC().Format(time.RFC3339)

	err = s.q.CreateVerificationCode(context.Background(), generated.CreateVerificationCodeParams{
		ID:        id,
		Email:     email,
		CodeHash:  hash,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return "", fmt.Errorf("auth: create code: %w", err)
	}
	return code, nil
}

// Verify checks the code for the given email. Returns true if valid.
func (s *CodeStore) Verify(email, code string) (bool, error) {
	ctx := context.Background()

	row, err := s.q.GetLatestUnusedCode(ctx, email)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("auth: query code: %w", err)
	}

	// Check expiry
	exp, parseErr := time.Parse(time.RFC3339, row.ExpiresAt)
	if parseErr != nil {
		return false, fmt.Errorf("auth: corrupt expiry timestamp: %w", parseErr)
	}
	if time.Now().After(exp) {
		return false, nil
	}

	// Check max attempts
	if row.Attempts >= maxAttempts {
		return false, nil
	}

	// Verify
	if !VerifyHash(code, row.CodeHash, s.secret) {
		if err := s.q.IncrementCodeAttempts(ctx, row.ID); err != nil {
			return false, fmt.Errorf("auth: increment attempts: %w", err)
		}
		return false, nil
	}

	// Mark used — must succeed to prevent replay
	if err := s.q.MarkCodeUsed(ctx, row.ID); err != nil {
		return false, fmt.Errorf("auth: mark code used: %w", err)
	}
	return true, nil
}

// Cleanup deletes expired codes older than 1 hour.
func (s *CodeStore) Cleanup() error {
	cutoff := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if err := s.q.CleanupExpiredCodes(context.Background(), cutoff); err != nil {
		return fmt.Errorf("auth: cleanup codes: %w", err)
	}
	return nil
}
