package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	appdb "github.com/labstack/fanout/internal/db"

	_ "modernc.org/sqlite"
)

const (
	// ControlDBBusyTimeout is SQLite's writer-contention retry window. Security
	// and control-plane writes must outlive it so revocation and audited
	// mutations do not fail before SQLite has exhausted its retry policy.
	ControlDBBusyTimeout = 5 * time.Second
	ControlWriteMargin   = 2 * time.Second
	ControlWriteTimeout  = ControlDBBusyTimeout + ControlWriteMargin
)

// SQLite wraps a database/sql.DB backed by modernc SQLite.
type SQLite struct {
	DB *sql.DB
}

// NewSQLite opens (or creates) an SQLite database at dbPath and runs
// embedded schema migrations via Goose. Use ":memory:" for an in-memory database.
func NewSQLite(dbPath string) (*SQLite, error) {
	var dsn string
	if dbPath == ":memory:" {
		dsn = fmt.Sprintf("file::memory:?_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)", ControlDBBusyTimeout.Milliseconds())
	} else {
		dsn = fmt.Sprintf("%s?_pragma=journal_mode(wal)&_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)", dbPath, ControlDBBusyTimeout.Milliseconds())
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
	return appdb.Migrate(context.Background(), s.DB)
}
