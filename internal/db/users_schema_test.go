package db

import (
	"database/sql"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func TestUserProfileStatusMigration(t *testing.T) {
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
	if _, err := provider.UpTo(t.Context(), 20261008000001); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO users (id, email, name, role, active, auth_version) VALUES
		('active-user', 'active@example.test', 'Active User', 'admin', 1, 7),
		('suspended-user', 'suspended@example.test', 'Suspended User', 'operator', 0, 3),
		('unnamed-user', 'unnamed@example.test', NULL, 'viewer', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, displayName, role, status string
		version                       int
	}{
		{"active-user", "Active User", "admin", "active", 7},
		{"suspended-user", "Suspended User", "operator", "suspended", 3},
		{"unnamed-user", "", "viewer", "active", 1},
	} {
		var displayName sql.NullString
		var role, status string
		var version int
		if err := database.QueryRow(`SELECT display_name, role, status, auth_version FROM users WHERE id = ?`, tc.id).Scan(&displayName, &role, &status, &version); err != nil {
			t.Fatal(err)
		}
		if displayName.String != tc.displayName || role != tc.role || status != tc.status || version != tc.version {
			t.Fatalf("migrated %s = %q %q %q %d", tc.id, displayName.String, role, status, version)
		}
	}
	for _, column := range []string{"name", "active"} {
		if _, err := database.Exec(`SELECT ` + column + ` FROM users`); err == nil {
			t.Fatalf("superseded column %s remains", column)
		}
	}
	for _, status := range []any{"disabled", "", nil} {
		if _, err := database.Exec(`UPDATE users SET status = ? WHERE id = 'active-user'`, status); err == nil {
			t.Fatalf("status constraint accepted %v", status)
		}
	}
	if _, err := database.Exec(`INSERT INTO users (id, email) VALUES ('default-user', 'default@example.test')`); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := database.QueryRow(`SELECT status FROM users WHERE id = 'default-user'`).Scan(&status); err != nil || status != "active" {
		t.Fatalf("default status = %q, %v", status, err)
	}
	if err := Migrate(t.Context(), database); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}
