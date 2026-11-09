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
		"migrations/20260930000000_initial.sql":                 "2f256d45df0a604c198e165af00b66f63d9f36b13e27f2898c485da524883203",
		"migrations/20261004000000_dashboard_specs.sql":         "11894224e7c3ef0efcacae9a6f5f8f6d4af27ddd81ed8868b96b44fb9bbf7d36",
		"migrations/20261008000001_dashboard_origins.sql":       "6e7e89adf6e51633da0d73b052b12eec08ae3e7dd21ca315a4ac2fecfa703501",
		"migrations/20261009000000_user_profile_status.sql":     "aed4c0a316a5fb67b1e60c82d45c6b3ec34a2fbb44721ef772f6b5d3df546659",
		"migrations/20261030000000_alert_last_evaluated_at.sql": "577ef95a1964aebf72d8ecb9610c310910121f7bb735dbb287966525b2fb537a",
		"migrations/20261031000000_alert_created_at.sql":        "74b75784794f4207790b21d94db4322d5d76cf4a423741788b22ac4f8f2c55f9",
		"migrations/20261031180000_alert_episodes.sql":          "2406758ae63a8534194ab4cda1257529cfc85b8aaa5d5dc8a3b2f53293d9e2ae",
		"migrations/20261108120000_alert_email.sql":             "20718266d9fda9a35d556dc82540373597f041edbadd885e4e14e6ee57999e4e",
		"migrations/20261109120000_ingest_tokens.sql":           "690d48e3df6ee7f1f3ca9682a3914db236daafe8b78033f1934e261860454219",
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
