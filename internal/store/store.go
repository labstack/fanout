package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// SQLite wraps a database/sql.DB backed by modernc SQLite.
type SQLite struct {
	DB *sql.DB
}

// NewSQLite opens (or creates) an SQLite database at dbPath and runs
// schema migrations. Use ":memory:" for an in-memory database.
func NewSQLite(dbPath string) (*SQLite, error) {
	var dsn string
	if dbPath == ":memory:" {
		dsn = "file::memory:?_pragma=foreign_keys(1)"
	} else {
		dsn = dbPath + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open sqlite: %w", err)
	}

	s := &SQLite{DB: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return s, nil
}

// Close closes the underlying database connection.
func (s *SQLite) Close() error {
	return s.DB.Close()
}

func (s *SQLite) migrate() error {
	// Create migration tracking table
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS _migrations (
		version TEXT PRIMARY KEY,
		applied_at TEXT DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	// Read embedded migration files
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	// Sort and filter to .sql files only
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	// Apply unapplied migrations in order
	for _, name := range files {
		var count int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM _migrations WHERE version = ?`, name).Scan(&count); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if count > 0 {
			continue // already applied
		}

		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		if _, err := s.DB.Exec(string(content)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}

		if _, err := s.DB.Exec(`INSERT INTO _migrations (version) VALUES (?)`, name); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}

		slog.Info("migration applied", "version", name)
	}

	return nil
}
