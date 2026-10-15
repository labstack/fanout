package query

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestBackgroundPassesYieldToQueuedPublication(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "pending", Spans: []telemetry.Span{{TraceID: "trace", StartUnixNanos: 1, EndUnixNanos: 2}}}); err != nil {
		t.Fatal(err)
	}
	mustRLock(t, &d.parquetMu)
	defer d.parquetMu.RUnlock()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	publisher := make(chan error, 1)
	go func() { publisher <- d.parquetMu.LockContext(ctx) }()
	waitForQueuedWriter(t, &d.parquetMu)
	if err := d.lockParquetRead(t.Context(), readerQuery); err != nil {
		t.Fatalf("query refused inside grace: %v", err)
	}
	d.parquetMu.RUnlock()
	// A cache pass must skip immediately, even without a caller deadline.
	cacheDone := make(chan error, 1)
	go func() { _, err := d.RefreshReadCaches(t.Context()); cacheDone <- err }()
	select {
	case err := <-cacheDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("read cache waited behind publication")
	}
	markers, err := batchMarkers(t.Context(), d.DB)
	if err != nil || markers["pending"] {
		t.Fatalf("cache entered ahead of queued publisher: %v %v", markers, err)
	}
	for name, pass := range map[string]func(context.Context) error{
		"service": func(ctx context.Context) error { _, err := d.refreshServiceRollup(ctx); return err },
		"edge":    func(ctx context.Context) error { _, err := d.refreshEdgeRollup(ctx); return err },
		"version": func(ctx context.Context) error { _, err := d.RefreshVersionRollup(ctx); return err },
		"stats": func(ctx context.Context) error {
			err := d.lockParquetRead(ctx, readerStats)
			if err == nil {
				d.parquetMu.RUnlock()
			}
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			waitCtx, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer stop()
			if err := pass(waitCtx); !errors.Is(err, ErrParquetReadWait) || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("background pass did not wait behind publisher: %v", err)
			}
		})
	}
	cancel()
	if err := <-publisher; !errors.Is(err, context.Canceled) {
		t.Fatalf("publisher cancellation: %v", err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.refreshEdgeRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	d.updateParquetStats(t.Context())
	markers, err = batchMarkers(t.Context(), d.DB)
	if err != nil || !markers["pending"] {
		t.Fatalf("cache did not resume: %v %v", markers, err)
	}
}
