package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlWriteTimeoutExceedsBusyTimeout(t *testing.T) {
	if ControlWriteTimeout < ControlDBBusyTimeout+ControlWriteMargin {
		t.Fatalf("control write timeout %s must exceed busy timeout %s by margin %s", ControlWriteTimeout, ControlDBBusyTimeout, ControlWriteMargin)
	}
}

func TestNewSQLite_InMemory(t *testing.T) {
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer s.Close()

	tables := []string{
		"alert_rules", "alerts", "users", "verifications", "sessions",
		"user_identities", "auth_audit_events", "oauth_clients",
		"oauth_authorization_codes", "oauth_tokens", "settings", "agui_threads",
		"agui_runs", "dashboards", "dashboard_widgets", "goose_db_version",
	}
	for _, tbl := range tables {
		var name string
		err := s.DB.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", tbl, err)
		}
	}
}

func TestNewSQLite_ReopenPreservesDataAndMigrationVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sqlite")
	s, err := NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO settings (key, value) VALUES ('example', '{"enabled":true}')`); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id > 0 AND is_applied = 1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before == 0 {
		t.Fatal("fresh database has no recorded migrations")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var value string
	if err := s.DB.QueryRow(`SELECT value FROM settings WHERE key = 'example'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != `{"enabled":true}` {
		t.Fatalf("saved value = %q", value)
	}
	var applied int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id > 0 AND is_applied = 1`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != before {
		t.Fatalf("applied migrations after reopening = %d, want %d", applied, before)
	}
}

func TestNewSQLite_RejectsUnversionedDatabaseWithoutChangingSchemaOrData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// A user table may start with "sqlite" without being an internal "sqlite_" table.
	if _, err := db.Exec(`CREATE TABLE sqliteState (value TEXT NOT NULL); INSERT INTO sqliteState VALUES ('keep me')`); err != nil {
		t.Fatal(err)
	}
	if s, err := NewSQLite(path); err == nil {
		s.Close()
		t.Fatal("opened an unversioned database")
	} else if !strings.Contains(err.Error(), "automatic conversion is not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
	var value string
	if err := db.QueryRow(`SELECT value FROM sqliteState`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "keep me" {
		t.Fatalf("existing data changed to %q", value)
	}
	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 1 {
		t.Fatalf("created tables in rejected database: %d", tables)
	}
}

func TestNewSQLite_WALMode(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/test.db"

	s, err := NewSQLite(dbPath)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer s.Close()

	var mode string
	if err := s.DB.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want %q", mode, "wal")
	}
}

func TestNewSQLite_RejectsExistingSchemaWithUnappliedGooseState(t *testing.T) {
	for _, state := range []string{"empty", "zero", "unapplied"} {
		t.Run(state, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "control.sqlite")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`CREATE TABLE alert_rules (value TEXT NOT NULL);
				INSERT INTO alert_rules VALUES ('keep me');
				CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY, version_id INTEGER NOT NULL, is_applied INTEGER NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			versions := 0
			if state != "empty" {
				version, applied := 0, 1
				if state == "unapplied" {
					version, applied = 20260930000000, 0
				}
				if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, ?)`, version, applied); err != nil {
					t.Fatal(err)
				}
				versions = 1
			}
			if s, err := NewSQLite(path); err == nil {
				s.Close()
				t.Fatal("opened existing schema without an applied migration")
			} else if !strings.Contains(err.Error(), "automatic conversion is not supported") {
				t.Fatalf("unexpected error: %v", err)
			}
			var value string
			if err := db.QueryRow(`SELECT value FROM alert_rules`).Scan(&value); err != nil || value != "keep me" {
				t.Fatalf("existing data changed: value=%q, err=%v", value, err)
			}
			var tables, rows int
			if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table'`).Scan(&tables); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version`).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			if tables != 2 || rows != versions {
				t.Fatalf("rejected database changed: tables=%d, versions=%d", tables, rows)
			}
		})
	}
}

func TestNewSQLite_InitializesWithOnlyGooseMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE goose_db_version (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		version_id INTEGER NOT NULL, is_applied INTEGER NOT NULL,
		tstamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP);
		INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, 1)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var applied int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id > 0 AND is_applied=1`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("applied versions = %d, want 1", applied)
	}
}
