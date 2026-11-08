package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/db/generated"
	"github.com/labstack/fanout/internal/secret"
)

const ingestKey = "ingest"

// Ingest holds the ingest-auth configuration. TokenHash is set from
// FANOUT_INGEST_TOKEN at startup or during first-admin setup, and is rotatable
// from the Settings page; when unset, the authorizer rejects all OTLP requests.
// Collectors present the token via `Authorization: Bearer`.
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
	return commitIngest(writeCtx, tx, payload, audit, event)
}

// ApplyConfiguredIngestToken stores the hash of a token supplied by
// configuration when no token hash exists, and reports whether it did. An
// existing hash always wins, so rotation survives restarts that still carry the
// setting. Call it before any other writer uses the control database: the check
// and the write share a transaction, which a concurrent writer would abort.
func (s *Store) ApplyConfiguredIngestToken(ctx context.Context, token string, audit *auth.AuditStore) (bool, error) {
	if audit == nil {
		return false, fmt.Errorf("settings: audit store is required for ingest updates")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("settings: begin ingest update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current := DefaultIngest()
	row, err := generated.New(tx).GetSetting(ctx, ingestKey)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, fmt.Errorf("settings: get %s: %w", ingestKey, err)
	default:
		if err := json.Unmarshal([]byte(row.Value), &current); err != nil {
			return false, fmt.Errorf("settings: decode %s: %w", ingestKey, err)
		}
	}
	if current.TokenHash != "" {
		return false, nil
	}

	payload, err := json.Marshal(Ingest{TokenHash: HashIngestToken(token)})
	if err != nil {
		return false, fmt.Errorf("settings: encode ingest: %w", err)
	}
	event := auth.AuditEvent{EventType: "ingest_key.rotated", Outcome: "success", TargetType: "ingest", TargetID: "default", Metadata: map[string]any{"source": "configuration"}}
	if err := commitIngest(ctx, tx, payload, audit, event); err != nil {
		return false, err
	}
	return true, nil
}

func commitIngest(ctx context.Context, tx *sql.Tx, payload []byte, audit *auth.AuditStore, event auth.AuditEvent) error {
	if _, err := generated.New(tx).UpsertSetting(ctx, generated.UpsertSettingParams{Key: ingestKey, Value: string(payload), UpdatedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		return fmt.Errorf("settings: upsert ingest: %w", err)
	}
	if err := audit.RecordTx(ctx, tx, event); err != nil {
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
