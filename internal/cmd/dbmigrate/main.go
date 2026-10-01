// dbmigrate applies the same embedded control migrations and database guard as
// Fanout startup, without starting the server or loading runtime configuration.
package main

import (
	"flag"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/labstack/fanout/internal/store"
)

func main() {
	path := flag.String("db", "data/control/fanout.sqlite", "SQLite control database path")
	flag.Parse()
	if err := os.MkdirAll(filepath.Dir(*path), 0755); err != nil {
		slog.Error("create control database directory", "err", err)
		os.Exit(1)
	}
	db, err := store.NewSQLite(*path)
	if err != nil {
		slog.Error("apply control database migrations", "err", err)
		os.Exit(1)
	}
	if err := db.Close(); err != nil {
		slog.Error("close control database", "err", err)
		os.Exit(1)
	}
}
