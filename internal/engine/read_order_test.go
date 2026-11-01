package engine

import (
	"context"
	"testing"
	"time"

	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

// A reader queued for a pool connection does not yet read Parquet, so it must
// not hold the snapshot gate: a publisher would otherwise wait out the queue.
func TestReadersQueuedForAConnectionDoNotHoldPublication(t *testing.T) {
	reads := map[string]func(*Duck, context.Context){
		"query": func(d *Duck, ctx context.Context) {
			if rows, err := d.QueryContext(ctx, "SELECT 1"); err == nil {
				_ = rows.Close()
			}
		},
		"row": func(d *Duck, ctx context.Context) {
			var one int
			_ = d.QueryRowScan(ctx, []any{&one}, "SELECT 1")
		},
		"prepare": func(d *Duck, ctx context.Context) {
			_, _, _ = d.PrepareTelemetrySQL(ctx, "SELECT 1", "SELECT 1", 1)
		},
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			db := openTestDuck(t)
			d := &Duck{DB: db}
			held, err := d.acquireRead(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { defer close(done); read(d, t.Context()) }()
			for db.Stats().WaitCount == 0 {
				time.Sleep(time.Millisecond)
			}
			const queue = 500 * time.Millisecond
			release := time.AfterFunc(queue, func() { _ = held.Close() })
			defer release.Stop()
			started := time.Now()
			if err := d.PublishParquet(t.Context(), func(context.Context) error { return nil }); err != nil {
				t.Fatal(err)
			}
			wait := time.Since(started)
			if release.Stop() {
				_ = held.Close()
			}
			<-done
			t.Logf("publish waited %s behind a reader queued %s for a connection", wait, queue)
			if wait >= queue/2 {
				t.Fatalf("publish waited %s for a reader still queued for a connection", wait)
			}
		})
	}
}

// A reader the gate holds back reads nothing, so it must not hold a pool
// connection either: readiness pings, annotation reads and the watchdog use
// the same pool and would queue behind it for the publisher's whole drain.
func TestReadersBlockedByAPublisherHoldNoConnection(t *testing.T) {
	blockers := map[string]func(*Duck) (release func()){
		"active": func(d *Duck) func() {
			mustLock(&d.parquetMu)
			return d.parquetMu.Unlock
		},
		"pending": func(d *Duck) func() {
			// A held reader keeps the publisher queued; past its grace, new
			// readers wait for it.
			d.parquetMu.writerGrace = time.Nanosecond
			mustRLock(t, &d.parquetMu)
			queued := make(chan error, 1)
			go func() { queued <- d.parquetMu.LockContext(context.Background()) }()
			for waitingParquetWriters(&d.parquetMu) == 0 {
				time.Sleep(time.Millisecond)
			}
			return func() {
				d.parquetMu.RUnlock()
				if err := <-queued; err == nil {
					d.parquetMu.Unlock()
				}
			}
		},
	}
	for name, block := range blockers {
		t.Run(name, func(t *testing.T) {
			db := openTestDuck(t)
			d := &Duck{DB: db}
			release := block(d)
			done := make(chan error, 1)
			go func() {
				var one int
				done <- d.QueryRowScan(t.Context(), []any{&one}, "SELECT 1")
			}()
			time.Sleep(50 * time.Millisecond)
			if inUse := db.Stats().InUse; inUse != 0 {
				t.Fatalf("a reader blocked by the publisher holds %d connections", inUse)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if err := d.Ping(ctx); err != nil {
				t.Fatalf("readiness ping queued behind a blocked reader: %v", err)
			}
			release()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

// emptyRepository is the telemetry repository every Duck has, with no batches.
func emptyRepository(t testing.TB) *telemetrystore.Repository {
	t.Helper()
	repository, err := telemetrystore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return repository
}
