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

// TestPoolConcurrentReadsNoLock exercises the WAL fix: with a multi-connection
// pool, concurrent read queries running alongside serialized writes must not
// fail with "database is locked". A start barrier releases all goroutines at
// once so the pool is forced to open multiple physical connections (otherwise a
// low-contention run could funnel through a single reused handle and never
// exercise concurrency). The temp_directory fix is covered separately by
// TestTempDirectorySetOnce.
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
	start := make(chan struct{}) // released once all goroutines are parked

	// Writers: serialized via the shared write lock, exactly as the ingest path
	// holds it around appender flushes.
	for w := 0; w < workers/2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
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
			<-start
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
	close(start)
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

// TestTempDirectorySetOnce deterministically reproduces the "Cannot switch
// temporary directory" regression. temp_directory is an instance-global setting:
// once any connection has spilled to it, re-running SET temp_directory on a later
// connection's boot hook fails. The test forces a spill on a pinned connection,
// then opens a second pooled connection (which runs the boot hook) and asserts it
// doesn't fail. Reverting the sync.Once in openDuckDB makes this test fail.
func TestTempDirectorySetOnce(t *testing.T) {
	ctx := context.Background()
	// A small memory limit makes a modest sort spill to the temp directory.
	// Threads pinned low to keep the per-thread pinned working set inside the
	// tiny cap — with one worker per core (the default) the sort OOMs on
	// many-core machines before it can spill.
	cfg := env.Config{
		DataDir:        t.TempDir(),
		RollupEvery:    60,
		DuckDBMemory:   "64MB",
		DuckDBThreads:  2,
		DuckDBMaxConns: 4,
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
	defer d.Close()

	// Pin connection 1 and force it to spill to the instance temp directory.
	conn1, err := d.DB.Conn(ctx)
	if err != nil {
		t.Fatalf("conn1: %v", err)
	}
	defer conn1.Close()
	var sink int64
	if err := conn1.QueryRowContext(ctx,
		"SELECT count(*) FROM (SELECT i FROM range(20000000) t(i) ORDER BY i DESC)").Scan(&sink); err != nil {
		t.Fatalf("forced-spill query: %v", err)
	}

	// With conn1 still held, this must open a second physical connection and run
	// its boot hook. Pre-fix, that boot re-ran SET temp_directory after the temp
	// dir was in use and failed with "Cannot switch temporary directory".
	conn2, err := d.DB.Conn(ctx)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "switch temporary directory") {
			t.Fatalf("regression — temp_directory re-set on 2nd connection: %v", err)
		}
		t.Fatalf("conn2 (boots a fresh pooled connection): %v", err)
	}
	defer conn2.Close()
	if err := conn2.QueryRowContext(ctx, "SELECT 1").Scan(&sink); err != nil {
		t.Fatalf("conn2 query: %v", err)
	}
}
