package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/labstack/fanout/internal/db/generated"
)

// Store persists app-managed config groups in SQLite.
type Store struct {
	q *generated.Queries
}

// NewStore creates a config store backed by the given database.
func NewStore(db *sql.DB) *Store {
	return &Store{q: generated.New(db)}
}

// Get decodes the stored config group into out, or leaves defaults intact if the row is missing.
func (s *Store) Get(ctx context.Context, groupKey string, out any) error {
	row, err := s.q.GetConfig(ctx, groupKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config: get %s: %w", groupKey, err)
	}
	if row.Overrides == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(row.Overrides), out); err != nil {
		return fmt.Errorf("config: decode %s: %w", groupKey, err)
	}
	return nil
}

// Upsert writes a complete config payload for one group.
func (s *Store) Upsert(ctx context.Context, groupKey string, value any, updatedBy, reason string) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("config: encode %s: %w", groupKey, err)
	}
	_, err = s.q.UpsertConfig(ctx, generated.UpsertConfigParams{
		GroupKey:   groupKey,
		Overrides:  string(payload),
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		UpdatedBy:  nullString(updatedBy),
		LastReason: nullString(reason),
	})
	if err != nil {
		return fmt.Errorf("config: upsert %s: %w", groupKey, err)
	}
	return nil
}

func nullString(v string) sql.NullString {
	return sql.NullString{String: v, Valid: v != ""}
}
