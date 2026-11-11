package engine

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/pitot/internal/queryrows"
	"github.com/labstack/pitot/internal/telemetry"
	telemetrystore "github.com/labstack/pitot/internal/telemetry/store"
)

func commitCurrentBatches(t *testing.T, repo *telemetrystore.Repository, prefix string, n int, at time.Time) {
	t.Helper()
	for i := range n {
		event := at.UnixNano() + int64(i)
		if err := repo.Commit(t.Context(), telemetrystore.Batch{
			ID:      fmt.Sprintf("%s-%03d", prefix, i),
			Spans:   []telemetry.Span{{Namespace: "ns", ServiceName: "svc", TraceID: fmt.Sprint("trace-", i), SpanID: fmt.Sprint(i), StartUnixNanos: event, EndUnixNanos: event + 1, StatusCode: "STATUS_CODE_OK"}},
			Logs:    []telemetry.Log{{Namespace: "ns", ServiceName: "svc", TimeUnixNanos: event}},
			Metrics: []telemetry.Metric{{Namespace: "ns", ServiceName: "svc", Name: "m", TimeUnixNanos: event}},
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// Compaction, retention and retired-file deletion run while readers query
// continuously. Every windowed read sees each retained row exactly once, and
// no read fails because a file it captured was deleted.
func TestCompactionAndRetentionUnderConcurrentReads(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	const current = 80
	if err := commitRestoredFixture(repo, t.Context(), telemetrystore.Batch{ID: "expired", Spans: []telemetry.Span{{TraceID: "old", IngestedAt: 1}}}); err != nil {
		t.Fatal(err)
	}
	commitCurrentBatches(t, repo, "current", current, at)

	w := queryrows.Window{Start: at.Add(-time.Minute), End: at.Add(time.Minute)}
	query := `SELECT (SELECT count(*) FROM spans WHERE start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS),
(SELECT count(*) FROM logs WHERE time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS),
(SELECT count(*) FROM metrics WHERE time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS)`
	stop := make(chan struct{})
	var reads atomic.Int64
	var wg sync.WaitGroup
	failures := make(chan error, 16)
	for range 3 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				var spans, logs, metrics int
				err := d.QueryRowScan(queryrows.WithWindow(t.Context(), w), []any{&spans, &logs, &metrics}, query, w.Start, w.End, w.Start, w.End, w.Start, w.End)
				if err == nil && (spans != current || logs != current || metrics != current) {
					err = fmt.Errorf("windowed read saw spans=%d logs=%d metrics=%d, want %d each", spans, logs, metrics, current)
				}
				// A statement without a window reads the whole catalog: the
				// expired batch may or may not have been pruned yet, but no
				// row may appear twice.
				var all int
				if err == nil {
					err = d.QueryRowScan(t.Context(), []any{&all}, `SELECT count(*) FROM spans`)
				}
				if err == nil && all != current && all != current+1 {
					err = fmt.Errorf("unwindowed read saw %d spans, want %d or %d", all, current, current+1)
				}
				if err != nil {
					failures <- err
					return
				}
				reads.Add(1)
			}
		})
	}
	ctx := t.Context()
	for idle := 0; idle < 2; {
		compacted, err := repo.CompactParquet(ctx, d, 8)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.PruneParquet(ctx, d, 2, 10); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CollectRetiredParquet(); err != nil {
			t.Fatal(err)
		}
		if compacted == 0 {
			idle++
		}
	}
	close(stop)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if reads.Load() == 0 {
		t.Fatal("no read completed during compaction")
	}
	if n := len(repo.Parquet.BatchMetadata()); n >= current {
		t.Fatalf("compaction did not reduce %d batches (now %d)", current, n)
	}
	if _, err := repo.CollectRetiredParquet(); err != nil {
		t.Fatal(err)
	}
	if n := repo.Parquet.RetiringBatches(); n != 0 {
		t.Fatalf("%d retired batches remain after readers left", n)
	}
	t.Logf("%d reads during compaction to %d batches", reads.Load(), len(repo.Parquet.BatchMetadata()))
}

// Rollups and unwindowed statements name relations whose glob also matches
// inputs a pinned reader still holds. They must read the catalog instead.
func TestRollupsDoNotCountRetiredInputs(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	bucket := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	const inputs = 8
	commitCurrentBatches(t, repo, "input", inputs, bucket)
	pin := d.pinParquet()
	if n, err := repo.CompactParquet(t.Context(), d, inputs); err != nil || n != inputs {
		t.Fatalf("compacted %d: %v", n, err)
	}
	var onDisk int
	if err := d.DB.QueryRow(`SELECT count(*) FROM read_parquet(?)`, repo.Parquet.Pattern("spans")).Scan(&onDisk); err != nil || onDisk != 2*inputs {
		t.Fatalf("precondition: %d span rows on disk, want inputs and output (%d): %v", onDisk, 2*inputs, err)
	}
	var all int
	if err := d.QueryRowScan(t.Context(), []any{&all}, `SELECT count(*) FROM telemetry.spans`); err != nil || all != inputs {
		t.Fatalf("unwindowed read = %d, want %d: %v", all, inputs, err)
	}
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	var spans int64
	if err := d.DB.QueryRow(`SELECT sum(spans) FROM service_rollup WHERE service='svc'`).Scan(&spans); err != nil || spans != inputs {
		t.Fatalf("service rollup spans = %d, want %d: %v", spans, inputs, err)
	}
	if _, err := d.refreshEdgeRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The rollup's snapshot views are dropped with its transaction.
	var views int
	if err := d.writer().QueryRow(`SELECT count(*) FROM duckdb_views() WHERE temporary AND view_name IN ('spans','logs','metrics')`).Scan(&views); err != nil || views != 0 {
		t.Fatalf("temporary views left on the writer: %d %v", views, err)
	}
	d.unpinParquet(pin)
	if n, err := repo.CollectRetiredParquet(); err != nil || n != inputs {
		t.Fatalf("collected %d: %v", n, err)
	}
}

// A long reader does not delay publication, and its captured files survive
// until it finishes.
func TestPublicationDoesNotWaitForALongReader(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	commitCurrentBatches(t, repo, "input", 8, at)
	w := queryrows.Window{Start: at.Add(-time.Minute), End: at.Add(time.Minute)}
	rows, err := d.QueryContext(queryrows.WithWindow(t.Context(), w), `SELECT span_id FROM spans WHERE start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS`, w.Start, w.End)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	started := time.Now()
	if n, err := repo.CompactParquet(ctx, d, 8); err != nil || n != 8 {
		t.Fatalf("compaction under an open reader: %d %v", n, err)
	}
	if n, err := repo.CollectRetiredParquet(); err != nil || n != 0 {
		t.Fatalf("collected %d inputs an open reader captured: %v", n, err)
	}
	count := 0
	for rows.Next() {
		var id sql.NullString
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if err := rows.Err(); err != nil || count != 8 {
		t.Fatalf("long reader read %d rows: %v", count, err)
	}
	_ = rows.Close()
	t.Logf("compaction published in %s with a reader open", time.Since(started))
	if n, err := repo.CollectRetiredParquet(); err != nil || n != 8 {
		t.Fatalf("collected %d after the reader finished: %v", n, err)
	}
}
