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

type Store struct {
	q *generated.Queries
}

func NewStore(db *sql.DB) *Store {
	return &Store{q: generated.New(db)}
}

// Get leaves out unchanged when the row is missing so callers keep their defaults.
// A corrupt value surfaces as a decode error rather than silently falling through to defaults.
func (s *Store) Get(ctx context.Context, key string, out any) error {
	row, err := s.q.GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("settings: get %s: %w", key, err)
	}
	if err := json.Unmarshal([]byte(row.Value), out); err != nil {
		return fmt.Errorf("settings: decode %s: %w", key, err)
	}
	return nil
}

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

func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.q.DeleteSetting(ctx, key); err != nil {
		return fmt.Errorf("settings: delete %s: %w", key, err)
	}
	return nil
}
