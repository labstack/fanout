package db

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"testing"
)

// This ledger freezes committed migration bytes. Extend it for new migrations;
// never change or remove a checksum for a migration already published.
func TestMigrationChecksums(t *testing.T) {
	checksums := map[string]string{
		"migrations/20260930000000_initial.sql":           "2f256d45df0a604c198e165af00b66f63d9f36b13e27f2898c485da524883203",
		"migrations/20261004000000_dashboard_specs.sql":   "11894224e7c3ef0efcacae9a6f5f8f6d4af27ddd81ed8868b96b44fb9bbf7d36",
		"migrations/20261008000001_dashboard_origins.sql": "6e7e89adf6e51633da0d73b052b12eec08ae3e7dd21ca315a4ac2fecfa703501",
	}
	paths, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		want, ok := checksums[path]
		if !ok {
			t.Errorf("new migration %s needs a checksum in the ledger", path)
			continue
		}
		content, err := migrationsFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(content)); got != want {
			t.Errorf("migration %s changed: checksum %s, want %s; add a forward migration", path, got, want)
		}
		delete(checksums, path)
	}
	for path := range checksums {
		t.Errorf("migration %s was removed or renamed", path)
	}
}
