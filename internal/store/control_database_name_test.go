package store

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func controlDatabasePaths(t *testing.T) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "control")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(dir, "pitot.sqlite")
	return current, previousControlDatabasePath(current)
}

func TestControlDatabaseUpgradePreservesExistingUsers(t *testing.T) {
	current, previous := controlDatabasePaths(t)
	db, err := NewSQLite(previous)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO users (id, email, role) VALUES ('existing-admin', 'admin@example.test', 'admin')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = NewSQLite(current)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var role string
	if err := db.DB.QueryRow(`SELECT role FROM users WHERE id = 'existing-admin'`).Scan(&role); err != nil {
		t.Fatalf("existing administrator disappeared: %v", err)
	}
	if role != "admin" {
		t.Fatalf("role = %q", role)
	}
	if _, err := os.Lstat(previous); !os.IsNotExist(err) {
		t.Fatalf("previous database remains: %v", err)
	}
}

func TestControlDatabaseUpgradeRefusesBothNames(t *testing.T) {
	current, previous := controlDatabasePaths(t)
	for _, path := range []string{current, previous} {
		if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := NewSQLite(current)
	if err == nil {
		db.Close()
		t.Fatal("opened ambiguous control database")
	}
	if !strings.Contains(err.Error(), "both control database names exist") {
		t.Fatalf("ambiguous database error = %v", err)
	}
	for _, path := range []string{current, previous} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "preserve" {
			t.Fatalf("database modified: data=%q err=%v", data, err)
		}
	}
}

func TestControlDatabaseUpgradeMovesSidecarsWithoutCopying(t *testing.T) {
	current, previous := controlDatabasePaths(t)
	suffixes := []string{"", "-wal", "-shm", "-journal"}
	before := make(map[string]os.FileInfo)
	for _, suffix := range suffixes {
		path := previous + suffix
		if err := os.WriteFile(path, []byte("preserve"+suffix), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		before[suffix] = info
	}
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	if err := MigrateControlDatabase(current); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range suffixes {
		data, err := os.ReadFile(current + suffix)
		if err != nil || string(data) != "preserve"+suffix {
			t.Fatalf("sidecar %q: data=%q err=%v", suffix, data, err)
		}
		info, err := os.Stat(current + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(before[suffix], info) || info.Mode().Perm() != 0o600 {
			t.Fatalf("file %q was copied or permissions changed", suffix)
		}
		if _, err := os.Lstat(previous + suffix); !os.IsNotExist(err) {
			t.Fatalf("previous file %q remains: %v", suffix, err)
		}
	}
	if err := MigrateControlDatabase(current); err != nil {
		t.Fatal(err)
	}
	if strings.Count(logs.String(), "migrated control database to the current name") != 1 {
		t.Fatalf("migration log = %q", logs.String())
	}
}

func TestControlDatabaseUpgradeLeavesCurrentFilesUntouched(t *testing.T) {
	current, _ := controlDatabasePaths(t)
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.WriteFile(current+suffix, []byte("current"+suffix), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := MigrateControlDatabase(current); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		data, err := os.ReadFile(current + suffix)
		if err != nil || string(data) != "current"+suffix {
			t.Fatalf("current file %q changed: data=%q err=%v", suffix, data, err)
		}
	}
}

func TestControlDatabaseUpgradeRecoversCommittedWAL(t *testing.T) {
	current, previous := controlDatabasePaths(t)
	db, err := NewSQLite(previous)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE); PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO users (id, email, role) VALUES ('wal-admin', 'admin@example.test', 'admin')`); err != nil {
		t.Fatal(err)
	}
	// Snapshot a quiescent writer's file set before close checkpoints the WAL.
	// Restoring those bytes models an unclean shutdown without an open writer.
	files := make(map[string][]byte)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(previous + suffix)
		if err != nil {
			t.Fatal(err)
		}
		files[suffix] = data
	}
	if len(files["-wal"]) <= 32 {
		t.Fatal("fixture has no committed WAL frames")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for suffix, data := range files {
		if err := os.WriteFile(previous+suffix, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err = NewSQLite(current)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var role string
	if err := db.DB.QueryRow(`SELECT role FROM users WHERE id='wal-admin'`).Scan(&role); err != nil || role != "admin" {
		t.Fatalf("committed WAL user lost: role=%q err=%v", role, err)
	}
}

func TestControlDatabaseUpgradeRefusesPartialFileSets(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		for _, state := range []string{"target_sidecar", "orphan_source"} {
			t.Run(suffix+"/"+state, func(t *testing.T) {
				current, previous := controlDatabasePaths(t)
				path := previous + suffix
				if state == "target_sidecar" {
					if err := os.WriteFile(previous, []byte("old"), 0o600); err != nil {
						t.Fatal(err)
					}
					path = current + suffix
				}
				if err := os.WriteFile(path, []byte("pending"), 0o600); err != nil {
					t.Fatal(err)
				}
				if db, err := NewSQLite(current); err == nil {
					db.Close()
					t.Fatal("opened partial migration")
				} else if !strings.Contains(err.Error(), "control database sidecars") {
					t.Fatal(err)
				}
				if _, err := os.Lstat(current); !os.IsNotExist(err) {
					t.Fatalf("created current database: %v", err)
				}
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "pending" {
					t.Fatalf("sidecar overwritten: data=%q err=%v", data, err)
				}
			})
		}
	}
}

func TestControlDatabaseUpgradeAllowsFreshInstall(t *testing.T) {
	current, _ := controlDatabasePaths(t)
	db, err := NewSQLite(current)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var users int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil || users != 0 {
		t.Fatalf("fresh users = %d, err=%v", users, err)
	}
}
