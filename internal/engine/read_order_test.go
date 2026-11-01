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
		"sql": func(d *Duck, ctx context.Context) {
			d.ExecuteSQL(ctx, SQLRequest{Query: "SELECT 1"})
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
