package settings

import (
	"context"
	"errors"
	"fmt"
)

const ingestKey = "ingest"

type IngestMode string

const (
	IngestModePrivate IngestMode = "private"
	IngestModePublic  IngestMode = "public"
)

type IngestConfig struct {
	Mode           IngestMode `json:"mode"`
	PublicEndpoint string     `json:"public_endpoint"`
	TokenHash      string     `json:"token_hash"`
}

func DefaultIngestConfig() IngestConfig {
	return IngestConfig{Mode: IngestModePrivate}
}

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

func (s *Store) GetIngest(ctx context.Context) (IngestConfig, error) {
	cfg := DefaultIngestConfig()
	if err := s.Get(ctx, ingestKey, &cfg); err != nil {
		return IngestConfig{}, err
	}
	if err := cfg.Validate(); err != nil {
		return IngestConfig{}, fmt.Errorf("settings: invalid ingest config: %w", err)
	}
	return cfg, nil
}

func (s *Store) SetIngest(ctx context.Context, cfg IngestConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	return s.Upsert(ctx, ingestKey, cfg)
}

func (s *Store) ResetIngest(ctx context.Context) error {
	return s.Upsert(ctx, ingestKey, DefaultIngestConfig())
}

func GenerateIngestToken() (string, string, error) {
	raw, err := randomHexToken(24)
	if err != nil {
		return "", "", fmt.Errorf("generate ingest token: %w", err)
	}
	token := "fi_" + raw
	return token, HashIngestToken(token), nil
}

func HashIngestToken(token string) string {
	return hashToken(token)
}

func CheckIngestToken(token, hash string) bool {
	return checkToken(token, hash)
}
