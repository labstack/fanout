package config

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
)

const ingestGroupKey = "ingest"

type IngestMode string

const (
	IngestModePrivate IngestMode = "private"
	IngestModePublic  IngestMode = "public"
)

// IngestConfig controls OTLP exposure and token auth.
type IngestConfig struct {
	Mode           IngestMode `json:"mode"`
	PublicEndpoint string     `json:"public_endpoint"`
	TokenHash      string     `json:"token_hash"`
}

// DefaultIngestConfig returns the default private ingest mode.
func DefaultIngestConfig() IngestConfig {
	return IngestConfig{Mode: IngestModePrivate}
}

// Validate checks the config shape.
func (c IngestConfig) Validate() error {
	switch c.Mode {
	case IngestModePrivate:
		return nil
	case IngestModePublic:
		if c.PublicEndpoint == "" {
			return errors.New("public endpoint is required")
		}
		if c.TokenHash == "" {
			return errors.New("ingest token is required")
		}
		return nil
	default:
		return fmt.Errorf("invalid ingest mode: %s", c.Mode)
	}
}

// GetIngest returns the current ingest config, defaulting to private mode.
func (s *Store) GetIngest(ctx context.Context) (IngestConfig, error) {
	cfg := DefaultIngestConfig()
	if err := s.Get(ctx, ingestGroupKey, &cfg); err != nil {
		return IngestConfig{}, err
	}
	if err := cfg.Validate(); err != nil {
		return IngestConfig{}, fmt.Errorf("config: invalid ingest config: %w", err)
	}
	return cfg, nil
}

// SetIngest persists the current ingest config.
func (s *Store) SetIngest(ctx context.Context, cfg IngestConfig, updatedBy, reason string) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	return s.Upsert(ctx, ingestGroupKey, cfg, updatedBy, reason)
}

// ResetIngest returns OTLP to its default private mode.
func (s *Store) ResetIngest(ctx context.Context, updatedBy, reason string) error {
	return s.Upsert(ctx, ingestGroupKey, DefaultIngestConfig(), updatedBy, reason)
}

// GenerateIngestToken returns a plaintext token plus its hash for storage.
func GenerateIngestToken() (string, string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate ingest token: %w", err)
	}
	token := "fi_" + hex.EncodeToString(buf)
	return token, HashIngestToken(token), nil
}

// HashIngestToken hashes a plaintext ingest token for storage.
func HashIngestToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CheckIngestToken verifies a plaintext token against a stored hash.
func CheckIngestToken(token, hash string) bool {
	expected := HashIngestToken(token)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(hash)) == 1
}
