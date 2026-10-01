package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies embedded SQLite migrations. sqlc reads the same SQL files
// when generating query bindings. Providers have no shared global registry.
func Migrate(ctx context.Context, db *sql.DB) error {
	var hasTables, hasVersions bool
	if err := db.QueryRowContext(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name NOT GLOB 'sqlite_*'),
			EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`,
		goose.DefaultTablename).Scan(&hasTables, &hasVersions); err != nil {
		return fmt.Errorf("inspect migration state: %w", err)
	}
	if hasTables && !hasVersions {
		return errors.New("control database must be empty or managed by Goose; automatic conversion is not supported")
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
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
