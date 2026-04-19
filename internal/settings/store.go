package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/labstack/fanout/internal/db/generated"
)

// Store persists app-managed settings in SQLite.
type Store struct {
	q *generated.Queries
}

// NewStore creates a settings store backed by the given database.
func NewStore(db *sql.DB) *Store {
	return &Store{q: generated.New(db)}
}

// Get decodes the stored setting value into out, or leaves defaults intact if the row is missing.
func (s *Store) Get(ctx context.Context, key string, out any) error {
	row, err := s.q.GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("settings: get %s: %w", key, err)
	}
	if row.Value == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(row.Value), out); err != nil {
		return fmt.Errorf("settings: decode %s: %w", key, err)
	}
	return nil
}

// Upsert writes a complete value payload for one setting key.
func (s *Store) Upsert(ctx context.Context, key string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("settings: encode %s: %w", key, err)
	}
	_, err = s.q.UpsertSetting(ctx, generated.UpsertSettingParams{
		Key:       key,
		Value:     string(payload),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("settings: upsert %s: %w", key, err)
	}
	return nil
}

// Delete removes a setting entirely.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.q.DeleteSetting(ctx, key); err != nil {
		return fmt.Errorf("settings: delete %s: %w", key, err)
	}
	return nil
}
