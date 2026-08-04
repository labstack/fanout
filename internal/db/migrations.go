package db

import (
	"embed"
	"fmt"
	"io/fs"

	"ariga.io/atlas/sql/migrate"
)

//go:embed migrations/*.sql migrations/atlas.sum
var migrationsFS embed.FS

// OpenMigrationDir loads the Atlas migration directory from the embedded source
// of truth used by sqlc and atlas diff generation.
func OpenMigrationDir() (*migrate.MemDir, error) {
	dir := migrate.OpenMemDir("fanout")

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		dir.Close()
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			dir.Close()
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		if err := dir.WriteFile(entry.Name(), content); err != nil {
			dir.Close()
			return nil, fmt.Errorf("write migration %s: %w", entry.Name(), err)
		}
	}

	return dir, nil
}
