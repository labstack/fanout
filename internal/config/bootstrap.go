package config

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	bootstrapGroupKey = "bootstrap"
	BootstrapTTL      = time.Hour
)

var (
	ErrBootstrapTokenMissing = errors.New("bootstrap token unavailable")
	ErrBootstrapTokenExpired = errors.New("bootstrap token expired")
	ErrBootstrapTokenInvalid = errors.New("invalid bootstrap token")
)

// BootstrapConfig stores the first-boot setup token hash and expiry.
type BootstrapConfig struct {
	Token     string `json:"token"`
	TokenHash string `json:"token_hash"`
	ExpiresAt string `json:"expires_at"`
}

// GetBootstrap returns the current bootstrap token config, if any.
func (s *Store) GetBootstrap(ctx context.Context) (BootstrapConfig, error) {
	var cfg BootstrapConfig
	if err := s.Get(ctx, bootstrapGroupKey, &cfg); err != nil {
		return BootstrapConfig{}, err
	}
	return cfg, nil
}

// RotateBootstrap generates and stores a new one-time bootstrap token.
func (s *Store) RotateBootstrap(ctx context.Context, updatedBy, reason string) (string, BootstrapConfig, error) {
	token, err := GenerateBootstrapToken()
	if err != nil {
		return "", BootstrapConfig{}, err
	}
	cfg := BootstrapConfig{
		Token:     token,
		TokenHash: HashBootstrapToken(token),
		ExpiresAt: time.Now().UTC().Add(BootstrapTTL).Format(time.RFC3339),
	}
	if err := s.Upsert(ctx, bootstrapGroupKey, cfg, updatedBy, reason); err != nil {
		return "", BootstrapConfig{}, err
	}
	return token, cfg, nil
}

// EnsureBootstrap returns the current bootstrap token if it is still valid, otherwise it rotates a new one.
func (s *Store) EnsureBootstrap(ctx context.Context, updatedBy, reason string) (string, BootstrapConfig, error) {
	cfg, err := s.GetBootstrap(ctx)
	if err != nil {
		return "", BootstrapConfig{}, err
	}
	if cfg.reusable(time.Now().UTC()) {
		return cfg.Token, cfg, nil
	}
	return s.RotateBootstrap(ctx, updatedBy, reason)
}

// ClearBootstrap removes the bootstrap token config.
func (s *Store) ClearBootstrap(ctx context.Context) error {
	return s.Delete(ctx, bootstrapGroupKey)
}

// GenerateBootstrapToken returns a readable first-boot setup token.
func GenerateBootstrapToken() (string, error) {
	raw, err := randomHexToken(10)
	if err != nil {
		return "", fmt.Errorf("generate bootstrap token: %w", err)
	}
	return formatGroupedToken("fanout-setup-", raw, 4), nil
}

// HashBootstrapToken hashes the plaintext bootstrap token for storage.
func HashBootstrapToken(token string) string {
	return hashToken(token)
}

// CheckBootstrapToken verifies the plaintext bootstrap token and expiry.
func CheckBootstrapToken(token string, cfg BootstrapConfig, now time.Time) error {
	if strings.TrimSpace(cfg.TokenHash) == "" || strings.TrimSpace(cfg.ExpiresAt) == "" {
		return ErrBootstrapTokenMissing
	}
	expiresAt, err := cfg.expiresAt()
	if err != nil {
		return fmt.Errorf("invalid bootstrap expiry: %w", err)
	}
	if now.UTC().After(expiresAt) {
		return ErrBootstrapTokenExpired
	}
	if !checkToken(strings.TrimSpace(token), cfg.TokenHash) {
		return ErrBootstrapTokenInvalid
	}
	return nil
}

func (c BootstrapConfig) reusable(now time.Time) bool {
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.TokenHash) == "" || strings.TrimSpace(c.ExpiresAt) == "" {
		return false
	}
	expiresAt, err := c.expiresAt()
	if err != nil || now.UTC().After(expiresAt) {
		return false
	}
	return checkToken(c.Token, c.TokenHash)
}

func (c BootstrapConfig) expiresAt() (time.Time, error) {
	return time.Parse(time.RFC3339, c.ExpiresAt)
}
