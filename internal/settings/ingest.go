package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/db/generated"
)

const ingestKey = "ingest"

// Ingest holds the ingest-auth configuration. TokenHash is set during
// first-admin setup and rotatable from the Settings page; when unset
// (pre-setup), the authorizer rejects all OTLP requests. Collectors
// present the token via `x-fanout-ingest-token` or `Authorization: Bearer`.
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
	payload, err := json.Marshal(ingest)
	if err != nil {
		return fmt.Errorf("settings: encode ingest: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("settings: begin ingest update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := generated.New(tx).UpsertSetting(ctx, generated.UpsertSettingParams{Key: ingestKey, Value: string(payload), UpdatedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		return fmt.Errorf("settings: upsert ingest: %w", err)
	}
	if audit != nil {
		if err := audit.RecordTx(ctx, tx, event); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("settings: commit ingest update: %w", err)
	}
	return nil
}

func GenerateIngestToken() (string, string, error) {
	raw, err := randomHexToken(24)
	if err != nil {
		return "", "", fmt.Errorf("generate ingest token: %w", err)
	}
	token := "fo_" + raw
	return token, HashIngestToken(token), nil
}

func HashIngestToken(token string) string {
	return hashToken(token)
}

func CheckIngestToken(token, hash string) bool {
	return checkToken(token, hash)
}
