package store

import (
	"testing"
)

func TestSessionWriteTimeoutExceedsBusyTimeout(t *testing.T) {
	if SessionWriteTimeout < ControlDBBusyTimeout+SessionWriteMargin {
		t.Fatalf("session write timeout %s must exceed busy timeout %s by margin %s", SessionWriteTimeout, ControlDBBusyTimeout, SessionWriteMargin)
	}
}

func TestNewSQLite_InMemory(t *testing.T) {
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer s.Close()

	tables := []string{"alert_rules", "alerts", "users", "verifications"}
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
