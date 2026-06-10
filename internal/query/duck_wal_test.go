package query

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/labstack/fanout/internal/env"
)

// catalogJournalMode opens the SQLite catalog at path with the modernc driver
// (registered via duck.go's blank import) and returns its journal_mode.
func catalogJournalMode(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	defer db.Close()
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	return mode
}

// TestEnableCatalogWAL covers the catalog WAL bootstrap in isolation: it must
// set WAL, be idempotent, and recover (not permanently lock) when a populated
// -wal file from an unclean writer is still present.
func TestEnableCatalogWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.sqlite")

	if err := enableCatalogWAL(path); err != nil {
		t.Fatalf("enableCatalogWAL: %v", err)
	}
	if mode := catalogJournalMode(t, path); !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}

	// Write through a connection and abandon it without checkpointing, leaving a
	// populated -wal file — the rough shape a crashed/stuck writer leaves behind.
	// Re-running the bootstrap must still succeed and report WAL.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE t(id INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec("INSERT INTO t VALUES (1),(2),(3)"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Intentionally do NOT close db before re-running the bootstrap.

	if err := enableCatalogWAL(path); err != nil {
		t.Fatalf("enableCatalogWAL (idempotent/recovery): %v", err)
	}
	if mode := catalogJournalMode(t, path); !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode after reopen = %q, want wal", mode)
	}
}

// newTestDuck spins up a real DuckDB+DuckLake against a temp dir, skipping when
// the DuckLake/SQLite extensions aren't available in the environment.
func newTestDuck(t *testing.T, maxConns int) *Duck {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cfg := env.Config{
		DataDir:        t.TempDir(),
		RollupEvery:    60,
		DuckDBMemory:   "128MB",
		DuckDBMaxConns: maxConns,
	}
	d, err := NewDuck(ctx, cfg)
	if err != nil {
		if strings.Contains(err.Error(), "LOAD ducklake") ||
			strings.Contains(err.Error(), "LOAD sqlite") ||
			strings.Contains(err.Error(), "ATTACH") {
			t.Skipf("DuckLake extensions unavailable: %v", err)
		}
		t.Fatalf("NewDuck() error = %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// TestNewDuckCatalogUsesWAL verifies NewDuck leaves the DuckLake catalog in WAL
// mode end-to-end.
func TestNewDuckCatalogUsesWAL(t *testing.T) {
	d := newTestDuck(t, 1)
	if mode := catalogJournalMode(t, d.cfg.TelemetryDuckLakePath()); !strings.EqualFold(mode, "wal") {
		t.Fatalf("catalog journal_mode = %q, want wal", mode)
	}
}

// TestPoolConcurrentReadsNoLock reproduces the production incident: with a
// multi-connection pool, concurrent read queries running alongside serialized
// writes must not fail with "database is locked" or "Cannot switch temporary
// directory". It exercises both fixes — WAL read/write concurrency and the
// once-only temp_directory boot — since opening multiple pooled connections is
// what triggered both errors.
func TestPoolConcurrentReadsNoLock(t *testing.T) {
	d := newTestDuck(t, 4)
	ctx := context.Background()

	if _, err := d.DB.ExecContext(ctx, "CREATE TABLE lake.concurrency_probe(id INTEGER, val VARCHAR)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	const workers = 8
	const iters = 25
	var wg sync.WaitGroup
	errCh := make(chan error, workers*iters)

	// Writers: serialized via the shared write lock, exactly as the ingest path
	// holds it around appender flushes.
	for w := 0; w < workers/2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				mu := d.WriteLock()
				mu.Lock()
				_, err := d.DB.ExecContext(ctx,
					"INSERT INTO lake.concurrency_probe VALUES (?, ?)", w*iters+i, "x")
				mu.Unlock()
				if err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}
	// Readers: concurrent, no write lock — must not collide with the writers.
	for r := 0; r < workers/2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				var n int
				if err := d.DB.QueryRowContext(ctx,
					"SELECT count(*) FROM lake.concurrency_probe").Scan(&n); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "database is locked") ||
			strings.Contains(msg, "switch temporary directory") {
			t.Fatalf("regression — pool>1 catalog/temp error: %v", err)
		}
		t.Fatalf("unexpected error: %v", err)
	}
}
