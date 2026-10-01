package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies embedded SQLite migrations. sqlc reads the same SQL files
// when generating query bindings. Providers have no shared global registry.
func Migrate(ctx context.Context, db *sql.DB) error {
	var hasTables, hasVersionTable bool
	if err := db.QueryRowContext(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name NOT GLOB 'sqlite_*' AND name != ?),
			EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`,
		goose.DefaultTablename, goose.DefaultTablename).Scan(&hasTables, &hasVersionTable); err != nil {
		return fmt.Errorf("inspect migration state: %w", err)
	}
	if hasTables {
		var hasAppliedVersion bool
		if hasVersionTable {
			if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM goose_db_version WHERE version_id > 0 AND is_applied = 1)`).Scan(&hasAppliedVersion); err != nil {
				return fmt.Errorf("inspect applied migrations: %w", err)
			}
		}
		if !hasAppliedVersion {
			return errors.New("control database must be empty or managed by Goose; automatic conversion is not supported")
		}
	}
	dir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, dir,
		goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	for _, result := range results {
		slog.Info("control database migration applied", "version", result.Source.Version, "duration", result.Duration)
	}
	return nil
}
