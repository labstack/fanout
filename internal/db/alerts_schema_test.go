package db

import (
	"database/sql"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

// Episode starts written by SQLite's datetime('now') and by the engine as
// RFC 3339 with a variable fraction become one sortable RFC 3339 UTC form.
func TestAlertCreatedAtMigration(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrations, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 20261030000000); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO alert_rules (id, name, expression) VALUES ('r', 'rule', 'error_rate > 0.1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO alerts (id, rule_id, service, state, created_at) VALUES
		('old', 'r', 'a', 'firing', '2026-10-31 20:23:37'),
		('whole', 'r', 'b', 'firing', '2026-10-31T20:23:37Z'),
		('fraction', 'r', 'c', 'firing', '2026-10-31T20:23:37.3196Z'),
		('nanos', 'r', 'd', 'firing', '2026-10-31T20:23:37.123456789Z')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"old":      "2026-10-31T20:23:37.000000000Z",
		"whole":    "2026-10-31T20:23:37.000000000Z",
		"fraction": "2026-10-31T20:23:37.319600000Z",
		"nanos":    "2026-10-31T20:23:37.123456789Z",
	}
	rows, err := database.Query(`SELECT id, created_at FROM alerts`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, created string
		if err := rows.Scan(&id, &created); err != nil {
			t.Fatal(err)
		}
		if created != want[id] {
			t.Errorf("%s created_at = %q, want %q", id, created, want[id])
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// Existing alerts survive the episode migration, after which a rule and
// service keep many resolved episodes and at most one open alert.
func TestAlertEpisodesMigration(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrations, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 20261031000000); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO alert_rules (id, name, expression) VALUES ('r', 'rule', 'error_rate > 0.1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO alerts (id, rule_id, service, state, last_evaluated_at) VALUES ('kept', 'r', 'a', 'resolved', 'then')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	var evaluated string
	if err := database.QueryRow(`SELECT last_evaluated_at FROM alerts WHERE id = 'kept'`).Scan(&evaluated); err != nil || evaluated != "then" {
		t.Fatalf("kept alert: %q, %v", evaluated, err)
	}
	if _, err := database.Exec(`INSERT INTO alerts (id, rule_id, service, state) VALUES ('again', 'r', 'a', 'resolved'), ('open', 'r', 'a', 'firing')`); err != nil {
		t.Fatalf("store episodes: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO alerts (id, rule_id, service, state) VALUES ('second', 'r', 'a', 'pending')`); err == nil {
		t.Fatal("stored a second open alert")
	}
}
