package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/sqlite"

	appdb "github.com/labstack/fanout/internal/db"

	_ "modernc.org/sqlite"
)

// SQLite wraps a database/sql.DB backed by modernc SQLite.
type SQLite struct {
	DB *sql.DB
}

// NewSQLite opens (or creates) an SQLite database at dbPath and runs
// schema migrations via Atlas. Use ":memory:" for an in-memory database.
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
	if dbPath == ":memory:" {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
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
	ctx := context.Background()

	// Open Atlas SQLite driver
	drv, err := sqlite.Open(s.DB)
	if err != nil {
		return fmt.Errorf("open atlas driver: %w", err)
	}

	// Load the single embedded Atlas migration source of truth.
	dir, err := appdb.OpenMigrationDir()
	if err != nil {
		return fmt.Errorf("open migrations: %w", err)
	}
	defer dir.Close()

	// Create revision tracking backed by SQLite
	rrw := newSQLiteRevisions(s.DB)
	if err := rrw.init(); err != nil {
		return fmt.Errorf("init revision table: %w", err)
	}

	// Create executor and apply pending migrations
	ex, err := migrate.NewExecutor(drv, dir, rrw)
	if err != nil {
		return fmt.Errorf("create migration executor: %w", err)
	}

	if err := ex.ExecuteN(ctx, -1); err != nil {
		if errors.Is(err, migrate.ErrNoPendingFiles) {
			return nil
		}
		return fmt.Errorf("apply migrations: %w", err)
	}

	slog.Info("migrations applied")
	return nil
}

// sqliteRevisions implements migrate.RevisionReadWriter for SQLite.
type sqliteRevisions struct {
	db *sql.DB
}

func newSQLiteRevisions(db *sql.DB) *sqliteRevisions {
	return &sqliteRevisions{db: db}
}

func (r *sqliteRevisions) init() error {
	_, err := r.db.Exec(`CREATE TABLE IF NOT EXISTS atlas_schema_revisions (
		version        TEXT PRIMARY KEY,
		description    TEXT DEFAULT '',
		applied        INTEGER DEFAULT 0,
		total          INTEGER DEFAULT 0,
		executed_at    TEXT DEFAULT (datetime('now')),
		execution_time INTEGER DEFAULT 0,
		hash           TEXT DEFAULT '',
		operator_version TEXT DEFAULT ''
	)`)
	return err
}

func (r *sqliteRevisions) Ident() *migrate.TableIdent {
	return &migrate.TableIdent{Name: "atlas_schema_revisions"}
}

func (r *sqliteRevisions) ReadRevisions(_ context.Context) ([]*migrate.Revision, error) {
	rows, err := r.db.Query(`SELECT version, description, applied, total, executed_at, execution_time, hash FROM atlas_schema_revisions ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var revs []*migrate.Revision
	for rows.Next() {
		var rev migrate.Revision
		var executedAt string
		if err := rows.Scan(&rev.Version, &rev.Description, &rev.Applied, &rev.Total, &executedAt, &rev.ExecutionTime, &rev.Hash); err != nil {
			return nil, err
		}
		rev.ExecutedAt, _ = time.Parse(time.RFC3339, executedAt)
		if rev.ExecutedAt.IsZero() {
			rev.ExecutedAt, _ = time.Parse("2006-01-02 15:04:05", executedAt)
		}
		revs = append(revs, &rev)
	}
	return revs, rows.Err()
}

func (r *sqliteRevisions) ReadRevision(_ context.Context, version string) (*migrate.Revision, error) {
	var rev migrate.Revision
	var executedAt string
	err := r.db.QueryRow(
		`SELECT version, description, applied, total, executed_at, execution_time, hash FROM atlas_schema_revisions WHERE version = ?`,
		version,
	).Scan(&rev.Version, &rev.Description, &rev.Applied, &rev.Total, &executedAt, &rev.ExecutionTime, &rev.Hash)
	if err == sql.ErrNoRows {
		return nil, migrate.ErrRevisionNotExist
	}
	if err != nil {
		return nil, err
	}
	rev.ExecutedAt, _ = time.Parse(time.RFC3339, executedAt)
	return &rev, nil
}

func (r *sqliteRevisions) WriteRevision(_ context.Context, rev *migrate.Revision) error {
	_, err := r.db.Exec(
		`INSERT OR REPLACE INTO atlas_schema_revisions (version, description, applied, total, executed_at, execution_time, hash) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rev.Version, rev.Description, rev.Applied, rev.Total, rev.ExecutedAt.Format(time.RFC3339), rev.ExecutionTime, rev.Hash,
	)
	return err
}

func (r *sqliteRevisions) DeleteRevision(_ context.Context, version string) error {
	_, err := r.db.Exec(`DELETE FROM atlas_schema_revisions WHERE version = ?`, version)
	return err
}
