package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/db/generated"
	"github.com/labstack/fanout/internal/secret"
)

const ingestKey = "ingest"

// Ingest holds the ingest-auth configuration. TokenHash is set during
// interactive first-admin setup, or issued and rotated from the Settings page
// (the only path for an administrator created from FANOUT_FIRST_ADMIN_EMAIL); while unset,
// the authorizer rejects all OTLP requests. Collectors
// present the token via `Authorization: Bearer`.
type Ingest struct {
	TokenHash string `json:"token_hash"`
}

func DefaultIngest() Ingest {
	return Ingest{}
}

func (s *Store) GetIngest(ctx context.Context) (Ingest, error) {
	ingest := DefaultIngest()
	if err := s.Get(ctx, ingestKey, &ingest); err != nil {
		return Ingest{}, err
	}
	return ingest, nil
}

func (s *Store) SetIngest(ctx context.Context, ingest Ingest) error {
	return s.Upsert(ctx, ingestKey, ingest)
}

func (s *Store) SetIngestWithAudit(ctx context.Context, ingest Ingest, audit *auth.AuditStore, event auth.AuditEvent) error {
	if audit == nil {
		return fmt.Errorf("settings: audit store is required for ingest updates")
	}
	writeCtx, cancel := auth.DetachedWriteContext(ctx)
	defer cancel()
	payload, err := json.Marshal(ingest)
	if err != nil {
		return fmt.Errorf("settings: encode ingest: %w", err)
	}
	tx, err := s.db.BeginTx(writeCtx, nil)
	if err != nil {
		return fmt.Errorf("settings: begin ingest update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := generated.New(tx).UpsertSetting(writeCtx, generated.UpsertSettingParams{Key: ingestKey, Value: string(payload), UpdatedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		return fmt.Errorf("settings: upsert ingest: %w", err)
	}
	if err := audit.RecordTx(writeCtx, tx, event); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("settings: commit ingest update: %w", err)
	}
	return nil
}

func GenerateIngestToken() (string, string, error) {
	raw, err := secret.NewSecret()
	if err != nil {
		return "", "", fmt.Errorf("generate ingest token: %w", err)
	}
	token := "foi_" + raw
	return token, HashIngestToken(token), nil
}

func HashIngestToken(token string) string {
	return hashToken(token)
}

func CheckIngestToken(token, hash string) bool {
	return secret.ValidSecret(token, "foi_") && checkToken(token, hash)
}
