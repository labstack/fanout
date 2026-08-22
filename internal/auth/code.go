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
	db     *sql.DB
	q      *generated.Queries
	secret string
}

// NewCodeStore creates a CodeStore with the given HMAC secret.
func NewCodeStore(db *sql.DB, secret string) *CodeStore {
	return &CodeStore{db: db, q: generated.New(db), secret: secret}
}

func (s *CodeStore) RecentCount(email string, since time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM verifications WHERE email = ? AND datetime(created_at) >= datetime(?)`,
		email, since.UTC().Format(time.RFC3339),
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("auth: count recent codes: %w", err)
	}
	return count, nil
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

	// Reserve this guess before comparing it. Reading the counter and
	// incrementing it separately lets concurrent requests all observe a count
	// below the limit and evaluate more guesses than maxAttempts allows.
	reserved, err := s.db.ExecContext(ctx,
		`UPDATE verifications SET attempts = attempts + 1 WHERE id = ? AND used = 0 AND attempts < ?`,
		row.ID, maxAttempts)
	if err != nil {
		return false, fmt.Errorf("auth: reserve code attempt: %w", err)
	}
	claimed, err := reserved.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("auth: inspect code attempt: %w", err)
	}
	if claimed != 1 {
		return false, nil
	}

	// Verify
	if !VerifyHash(code, row.CodeHash, s.secret) {
		return false, nil
	}

	// Mark used atomically. Two concurrent successful hash checks race here;
	// exactly one may transition the row from unused to used.
	result, err := s.db.ExecContext(ctx, `UPDATE verifications SET used = 1 WHERE id = ? AND used = 0`, row.ID)
	if err != nil {
		return false, fmt.Errorf("auth: mark code used: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("auth: inspect code use: %w", err)
	}
	if changed != 1 {
		return false, nil
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
