package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// SQLite wraps a database/sql.DB backed by modernc SQLite.
type SQLite struct {
	DB *sql.DB
}

// NewSQLite opens (or creates) an SQLite database at dbPath and runs
// schema migrations. Use ":memory:" for an in-memory database.
func NewSQLite(dbPath string) (*SQLite, error) {
	dsn := dbPath
	if dbPath != ":memory:" {
		dsn = dbPath + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)"
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
	stmts := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS alert_rules (
			id                   TEXT PRIMARY KEY,
			name                 TEXT NOT NULL,
			description          TEXT,
			enabled              INTEGER DEFAULT 1,
			service              TEXT,
			namespace            TEXT DEFAULT '',
			expression           TEXT NOT NULL,
			for_seconds          INTEGER DEFAULT 60,
			cooldown_s           INTEGER DEFAULT 600,
			repeat_interval_s    INTEGER DEFAULT 3600,
			webhook_url          TEXT,
			webhook_headers      TEXT,
			webhook_template     TEXT,
			notify_on_resolve    INTEGER DEFAULT 0,
			created_at           TEXT DEFAULT (datetime('now')),
			updated_at           TEXT DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS alerts (
			id                   TEXT PRIMARY KEY,
			rule_id              TEXT NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
			service              TEXT NOT NULL,
			state                TEXT NOT NULL,
			value                REAL,
			fired_at             TEXT,
			resolved_at          TEXT,
			repeated_at          TEXT,
			last_eval            TEXT,
			last_delivery_status TEXT,
			last_delivery_at     TEXT,
			created_at           TEXT DEFAULT (datetime('now')),
			UNIQUE(rule_id, service)
		)`,
	}

	for _, stmt := range stmts {
		if _, err := s.DB.Exec(stmt); err != nil {
			return fmt.Errorf("exec stmt: %w", err)
		}
	}
	return nil
}

