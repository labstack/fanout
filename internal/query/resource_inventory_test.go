package query

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/parquet-go/parquet-go"
)

func inventoryContext(ctx context.Context, w queryrows.Window) context.Context {
	return queryrows.WithInventoryWindow(queryrows.WithWindow(ctx, w), w)
}

// Compare full typed identities, multiplicities and nanosecond extrema inside
// one pinned snapshot; JSON is deliberately not used as resource identity.
func assertInventoryOracle(t *testing.T, d *Duck, w queryrows.Window, want int) {
	t.Helper()
	var raw []string
	for _, signal := range []string{"spans", "logs", "metrics"} {
		clock := "time"
		if signal == "spans" {
			clock = "start_time"
		}
		raw = append(raw, `SELECT `+sqlLiteral(signal)+` AS signal,namespace,service,resource,min(`+clock+`) AS first_seen,max(`+clock+`) AS last_seen FROM `+signal+` WHERE `+windowPredicate(clock, w)+` GROUP BY namespace,service,resource`)
	}
	statement := `WITH expected AS (` + strings.Join(raw, " UNION ALL ") + `), differences AS ((SELECT * FROM resource_inventory EXCEPT ALL SELECT * FROM expected) UNION ALL (SELECT * FROM expected EXCEPT ALL SELECT * FROM resource_inventory)) SELECT (SELECT count(*) FROM differences),(SELECT count(*) FROM resource_inventory)`
	var differences, count int
	if err := d.QueryRowScan(inventoryContext(t.Context(), w), []any{&differences, &count}, statement); err != nil || differences != 0 || want >= 0 && count != want {
		t.Fatalf("inventory/raw differences=%d rows=%d want=%d: %v", differences, count, want, err)
	}
}

func resourceBatch(id string, times []int64, resource map[string]any) telemetrystore.Batch {
	b := telemetrystore.Batch{ID: id}
	for i, n := range times {
		// Deliberately different event/start/end/observed/ingest clocks.
		b.Spans = append(b.Spans, telemetry.Span{Namespace: "prod", ServiceName: "api", TraceID: id, SpanID: fmt.Sprint(i), StartUnixNanos: n, EndUnixNanos: n + int64(time.Hour), IngestedAt: 1, Resource: resource})
		b.Logs = append(b.Logs, telemetry.Log{Namespace: "prod", ServiceName: "api", TimeUnixNanos: n, EventUnixNanos: n, ObservedTimeNanos: n + int64(time.Hour), IngestedAt: 1, Resource: resource})
		b.Metrics = append(b.Metrics, telemetry.Metric{Namespace: "prod", ServiceName: "api", TimeUnixNanos: n, EventUnixNanos: n, StartTimeUnixNanos: n - int64(time.Hour), IngestedAt: 1, Resource: resource, Type: "gauge"})
	}
	return b
}

func TestResourceInventoryExactWindowsAndTypedIdentity(t *testing.T) {
	for _, at := range []int64{-100, 0, 1791370800000000001} {
		t.Run(fmt.Sprint(at), func(t *testing.T) {
			d, repo := newBatchCacheTest(t)
			resources := []map[string]any{nil, {}, {"n": int64(9007199254740993), "b": false, "bytes": []byte{0, 255}, "nested": []any{int64(1), map[string]any{"x": true}}, "dotted.key": "literal"}, {"n": "9007199254740993"}, {"n": true}}
			for i, r := range resources {
				if err := commitRestoredFixture(repo, t.Context(), resourceBatch(fmt.Sprint("typed", i), []int64{at - 1, at, at + 9, at + 10}, r)); err != nil {
					t.Fatal(err)
				}
			}
			if err := repo.Commit(t.Context(), resourceBatch("equal", []int64{at + 2}, resources[2])); err != nil {
				t.Fatal(err)
			}
			want := 15
			if at < 0 {
				want = 5
			}
			w := queryrows.Window{Start: time.Unix(0, at), End: time.Unix(0, at+10)}
			assertInventoryOracle(t, d, w, want)
			if _, err := d.RefreshReadCaches(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertInventoryOracle(t, d, w, want)
			// Widening the window makes every cached contribution interior.
			w.Start = time.Unix(0, at-2)
			w.End = time.Unix(0, at+11)
			assertInventoryOracle(t, d, w, want)
			if at <= 0 {
				return
			}
			conn, err := d.DB.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			bound, err := d.bindSnapshot(inventoryContext(t.Context(), w), conn, `SELECT * FROM resource_inventory`, w)
			if err != nil || strings.Contains(bound, "read_parquet") || strings.Contains(bound, "read_trace") {
				t.Fatalf("interior source: %s %v", bound, err)
			}
			rows, err := conn.QueryContext(t.Context(), "EXPLAIN "+bound)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var key, plan string
				if err := rows.Scan(&key, &plan); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(plan, "READ_PARQUET") {
					t.Fatal(plan)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResourceInventoryGapsClippedExtremaAndExpandedWindow(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 1, time.UTC)
	w := queryrows.Window{Start: at, End: at.Add(time.Minute)}
	for _, b := range []telemetrystore.Batch{
		resourceBatch("gap", []int64{at.Add(-time.Minute).UnixNano(), w.End.UnixNano()}, map[string]any{"id": "gap"}),
		resourceBatch("clipped", []int64{at.Add(-time.Minute).UnixNano(), at.Add(5 * time.Second).UnixNano(), at.Add(10 * time.Second).UnixNano(), w.End.UnixNano()}, map[string]any{"id": "clipped"}),
		resourceBatch("lookback", []int64{at.Add(-time.Second).UnixNano()}, map[string]any{"id": "lookback"}),
	} {
		if err := repo.Commit(t.Context(), b); err != nil {
			t.Fatal(err)
		}
	}
	assertInventoryOracle(t, d, w, 3)
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertInventoryOracle(t, d, w, 3)
	expanded := w
	expanded.Start = at.Add(-10 * time.Minute)
	ctx := queryrows.WithWindow(queryrows.WithInventoryWindow(t.Context(), w), expanded)
	var first, last int64
	if err := d.QueryRowScan(ctx, []any{&first, &last}, `SELECT min(epoch_ns(first_seen)),max(epoch_ns(last_seen)) FROM resource_inventory`); err != nil || first != at.Add(5*time.Second).UnixNano() || last != at.Add(10*time.Second).UnixNano() {
		t.Fatalf("expanded first/last=%d/%d: %v", first, last, err)
	}
}

func TestResourceInventorySignalSpecificUnknownBoundsAndPruning(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Unix(0, 100)
	b := resourceBatch("mixed", []int64{110}, map[string]any{"host.name": "local"})
	b.Logs = append(b.Logs, telemetry.Log{TimeUnixNanos: 90, EventUnixNanos: 90, IngestedAt: 1})
	if err := repo.Commit(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	w := queryrows.Window{Start: at, End: time.Unix(0, 120)}
	assertInventoryOracle(t, d, w, 3)
	batches := repo.Parquet.BatchMetadata()
	source := d.resourceInventorySource(batches, map[string]bool{"mixed": true}, w)
	if strings.Contains(source, "spans.parquet") || !strings.Contains(source, "logs.parquet") || strings.Contains(source, "metrics.parquet") {
		t.Fatal(source)
	}
	batches[0].SpanTime = telemetry.TimeRange{}
	source = d.resourceInventorySource(batches, map[string]bool{"mixed": true}, w)
	if !strings.Contains(source, "spans.parquet") {
		t.Fatal("unknown bounds omitted raw signal")
	}
	var count int
	if err := d.DB.QueryRow(`SELECT count(*) FROM (` + source + `)`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("unknown rows=%d %v", count, err)
	}
	// A span-only lookup must prune log and metric raw/cache branches.
	conn, err := d.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	bound, err := d.bindSnapshot(inventoryContext(t.Context(), w), conn, `SELECT resource['host.name']::VARCHAR FROM resource_inventory WHERE signal='spans'`, w)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := conn.QueryContext(t.Context(), "EXPLAIN "+bound)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, plan string
		if err := rows.Scan(&key, &plan); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(plan, "READ_PARQUET") || strings.Contains(plan, "read_trace") {
			t.Fatal(plan)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestResourceInventoryLatePublicationAndReadTransaction(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	w := queryrows.Window{Start: time.Unix(0, 1), End: time.Unix(0, 100)}
	if err := repo.Commit(t.Context(), resourceBatch("initial", []int64{10}, nil)); err != nil {
		t.Fatal(err)
	}
	conn, err := d.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := beginRead(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	defer endRead(conn, tx)
	// Fix the MVCC snapshot before the writer acknowledges the file.
	if _, err := batchMarkers(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	bound, err := d.bindSnapshot(inventoryContext(t.Context(), w), tx, `SELECT count(*) FROM resource_inventory`, w)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bound, "read_parquet") {
		t.Fatal("reader used a marker committed after its snapshot")
	}
	var count int
	if err := tx.QueryRow(bound).Scan(&count); err != nil || count != 3 {
		t.Fatalf("pinned reader rows=%d %v", count, err)
	}
	for _, b := range []telemetrystore.Batch{{ID: "late-log", Logs: resourceBatch("", []int64{5}, map[string]any{"late": true}).Logs}, {ID: "late-metric", Metrics: resourceBatch("", []int64{6}, map[string]any{"late": true}).Metrics}} {
		if err := repo.Commit(t.Context(), b); err != nil {
			t.Fatal(err)
		}
	}
	assertInventoryOracle(t, d, w, 5)
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertInventoryOracle(t, d, w, 5)
	// Already captured membership remains unchanged after late publication.
	if err := tx.QueryRow(bound).Scan(&count); err != nil || count != 3 {
		t.Fatalf("captured reader changed=%d %v", count, err)
	}
}

func TestResourceInventoryAtomicSignalAndMarkerFailures(t *testing.T) {
	for _, failure := range []string{"spans", "logs", "metrics", "marker"} {
		t.Run(failure, func(t *testing.T) {
			d, repo := newBatchCacheTest(t)
			if err := repo.Commit(t.Context(), resourceBatch("atomic", []int64{10}, nil)); err != nil {
				t.Fatal(err)
			}
			constraint := `CHECK (signal<>` + sqlLiteral(failure) + `)`
			if failure == "marker" {
				if _, err := d.DB.Exec(`DROP TABLE read_batches; CREATE TABLE read_batches(batch_id VARCHAR PRIMARY KEY CHECK(batch_id<>'atomic'),replacement_id VARCHAR)`); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := d.DB.Exec(`DROP TABLE read_resource_inventory; CREATE TABLE read_resource_inventory(batch_id VARCHAR,signal VARCHAR ` + constraint + `,namespace VARCHAR,service VARCHAR,resource VARIANT,first_seen TIMESTAMPTZ_NS,last_seen TIMESTAMPTZ_NS)`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := d.RefreshReadCaches(t.Context()); err == nil {
				t.Fatal("injected failure succeeded")
			}
			for _, table := range []string{"read_batches", "read_resource_inventory", "read_trace_parts", "read_trace_candidates"} {
				var count int
				if err := d.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("partial %s=%d %v", table, count, err)
				}
			}
			if _, err := d.DB.Exec(`DROP TABLE read_batches; DROP TABLE read_resource_inventory`); err != nil {
				t.Fatal(err)
			}
			if err := createBatchCaches(d.DB); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := d.RefreshReadCaches(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			assertInventoryOracle(t, d, queryrows.Window{Start: time.Unix(0, 1), End: time.Unix(0, 20)}, 3)
			var count int
			if err := d.DB.QueryRow(`SELECT count(*) FROM read_resource_inventory`).Scan(&count); err != nil || count != 3 {
				t.Fatalf("retry inventory=%d %v", count, err)
			}
		})
	}
}

func TestResourceInventoryFailedAndIncompleteReplacement(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	if err := repo.Commit(t.Context(), resourceBatch("input", []int64{10}, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := d.transferBatchCache(t.Context(), "incomplete", []string{"input", "missing"}); err != nil {
		t.Fatal(err)
	}
	markers, err := batchMarkers(t.Context(), d.DB)
	if err != nil || markers["incomplete"] {
		t.Fatalf("incomplete acknowledged: %v %v", markers, err)
	}
	failed := errors.New("publication failed")
	if err := d.PublishParquetReplacement(t.Context(), telemetry.BatchMetadata{ID: "prepared"}, []string{"input"}, func(context.Context) error { return failed }); !errors.Is(err, failed) {
		t.Fatal(err)
	}
	assertInventoryOracle(t, d, queryrows.Window{Start: time.Unix(0, 1), End: time.Unix(0, 20)}, 3)
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.DB.QueryRow(`SELECT count(*) FROM read_resource_inventory WHERE batch_id IN ('incomplete','prepared')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("inactive rows=%d %v", count, err)
	}
}

func TestResourceInventoryCompactionRetentionAndNamespaces(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			d, repo := newBatchCacheTest(t)
			for i := range 8 {
				b := resourceBatch(fmt.Sprint("part", i), []int64{int64(10 + i)}, map[string]any{"shared": true})
				if i%2 == 0 {
					b.Spans[0].Namespace = "other"
					b.Logs[0].Namespace = "other"
					b.Metrics[0].Namespace = "other"
				}
				if err := commitRestoredFixture(repo, t.Context(), b); err != nil {
					t.Fatal(err)
				}
			}
			w := queryrows.Window{Start: time.Unix(0, 1), End: time.Unix(0, 100)}
			if complete {
				if _, err := d.RefreshReadCaches(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			assertInventoryOracle(t, d, w, 6)
			if n, err := repo.CompactParquet(t.Context(), d, 8); err != nil || n != 8 {
				t.Fatalf("compaction=%d %v", n, err)
			}
			assertInventoryOracle(t, d, w, 6)
			if _, err := d.RefreshReadCaches(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertInventoryOracle(t, d, w, 6)
			var rows int
			if err := d.DB.QueryRow(`SELECT count(*) FROM read_resource_inventory`).Scan(&rows); err != nil || rows != 6 {
				t.Fatalf("merged inventory=%d %v", rows, err)
			}
			if n, err := repo.PruneParquet(t.Context(), d, 2, 8); err != nil || n != 1 {
				t.Fatalf("retention=%d %v", n, err)
			}
			assertInventoryOracle(t, d, w, 0)
			if _, err := d.RefreshReadCaches(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := d.DB.QueryRow(`SELECT count(*) FROM read_resource_inventory`).Scan(&rows); err != nil || rows != 0 {
				t.Fatalf("retired inventory=%d %v", rows, err)
			}
		})
	}
}

func TestResourceInventoryRetentionChangesExtremaBeforeCleanup(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			d, repo := newBatchCacheTest(t)
			for i := range 3 {
				event := int64(10 + i)
				if reverse {
					event = int64(12 - i)
				}
				b := resourceBatch(fmt.Sprint("retain", i), []int64{event}, map[string]any{"shared": true})
				b.Spans[0].IngestedAt = int64(i + 1)
				b.Logs[0].IngestedAt = int64(i + 1)
				b.Metrics[0].IngestedAt = int64(i + 1)
				if err := commitRestoredFixture(repo, t.Context(), b); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := d.RefreshReadCaches(t.Context()); err != nil {
				t.Fatal(err)
			}
			w := queryrows.Window{Start: time.Unix(0, 1), End: time.Unix(0, 100)}
			assertInventoryOracle(t, d, w, 3)
			if n, err := repo.PruneParquet(t.Context(), d, 2, 8); err != nil || n != 1 {
				t.Fatalf("retention=%d %v", n, err)
			}
			assertInventoryOracle(t, d, w, 3)
			var first, last int64
			wantFirst, wantLast := int64(11), int64(12)
			if reverse {
				wantFirst, wantLast = 10, 11
			}
			if err := d.QueryRowScan(inventoryContext(t.Context(), w), []any{&first, &last}, `SELECT min(epoch_ns(first_seen)),max(epoch_ns(last_seen)) FROM resource_inventory`); err != nil || first != wantFirst || last != wantLast {
				t.Fatalf("retained first/last=%d/%d %v", first, last, err)
			}
			if _, err := d.RefreshReadCaches(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertInventoryOracle(t, d, w, 3)

		})
	}
}

func TestResourceInventoryLogAndMetricOversizedProgress(t *testing.T) {
	for _, signal := range []string{"logs", "metrics"} {
		t.Run(signal, func(t *testing.T) {
			d, repo := newBatchCacheTest(t)
			b := telemetrystore.Batch{ID: "oversized"}
			if signal == "logs" {
				b.Logs = make([]telemetry.Log, readCacheRowBudget+1)
				for i := range b.Logs {
					b.Logs[i] = telemetry.Log{TimeUnixNanos: 1}
				}
			} else {
				b.Metrics = make([]telemetry.Metric, readCacheRowBudget+1)
				for i := range b.Metrics {
					b.Metrics[i] = telemetry.Metric{TimeUnixNanos: 1}
				}
			}
			if err := repo.Commit(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			if _, err := d.RefreshReadCaches(t.Context()); err != nil {
				t.Fatal(err)
			}
			markers, err := batchMarkers(t.Context(), d.DB)
			if err != nil || !markers[b.ID] {
				t.Fatalf("no oversized progress: %v %v", markers, err)
			}
			var rows int
			if err := d.DB.QueryRow(`SELECT count(*) FROM read_resource_inventory`).Scan(&rows); err != nil || rows != 1 {
				t.Fatalf("inventory=%d %v", rows, err)
			}
		})
	}
}

func TestResourceInventoryRequiresLogicalScopeAndReleasesCancelledReaders(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	if err := repo.Commit(t.Context(), resourceBatch("boundary", []int64{1, 10, 20}, nil)); err != nil {
		t.Fatal(err)
	}
	statement := `SELECT count(*) FROM resource_inventory`
	w := queryrows.Window{Start: time.Unix(0, 5), End: time.Unix(0, 15)}
	for _, ctx := range []context.Context{t.Context(), queryrows.WithWindow(t.Context(), w)} {
		if rows, err := d.QueryContext(ctx, statement); err == nil {
			rows.Close()
			t.Fatal("missing logical scope accepted")
		}
	}
	ctx, cancel := context.WithTimeout(inventoryContext(t.Context(), w), 20*time.Millisecond)
	defer cancel()
	rows, err := d.QueryContext(ctx, `SELECT sum(i) FROM resource_inventory CROSS JOIN range(1000000000000) t(i)`)
	if err == nil {
		for rows.Next() {
			var v any
			_ = rows.Scan(&v)
		}
		err = rows.Err()
		rows.Close()
	}
	if err == nil {
		t.Fatal("long inventory query was not interrupted")
	}
	assertInventoryOracle(t, d, w, 3)
	ctx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if err := d.PublishParquet(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("cancelled reader leaked publication lease: %v", err)
	}
}

func TestResourceInventoryRestartDiscardsInactivePreparation(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	if err := repo.Commit(t.Context(), resourceBatch("active", []int64{10}, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := d.transferBatchCache(t.Context(), "prepared", []string{"active"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewDuck(t.Context(), d.cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	w := queryrows.Window{Start: time.Unix(0, 1), End: time.Unix(0, 20)}
	assertInventoryOracle(t, reopened, w, 3)
	if _, err := reopened.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := reopened.DB.QueryRow(`SELECT count(*) FROM read_resource_inventory`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("restarted rows=%d %v", count, err)
	}
}

func TestResourceInventoryTransferFailureLeavesOutputUnacknowledged(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	if err := repo.Commit(t.Context(), resourceBatch("input", []int64{10}, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DB.Exec(`CREATE TABLE inventory_backup AS SELECT * FROM read_resource_inventory; DROP TABLE read_resource_inventory; CREATE TABLE read_resource_inventory(batch_id VARCHAR CHECK(batch_id<>'output'),signal VARCHAR,namespace VARCHAR,service VARCHAR,resource VARIANT,first_seen TIMESTAMPTZ_NS,last_seen TIMESTAMPTZ_NS); INSERT INTO read_resource_inventory SELECT * FROM inventory_backup; DROP TABLE inventory_backup`); err != nil {
		t.Fatal(err)
	}
	if err := d.transferBatchCache(t.Context(), "output", []string{"input"}); err == nil {
		t.Fatal("injected transfer succeeded")
	}
	markers, err := batchMarkers(t.Context(), d.DB)
	if err != nil || markers["output"] {
		t.Fatalf("failed transfer acknowledged: %v %v", markers, err)
	}
	var count int
	if err := d.DB.QueryRow(`SELECT count(*) FROM read_resource_inventory WHERE batch_id='output'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial output=%d %v", count, err)
	}
	assertInventoryOracle(t, d, queryrows.Window{Start: time.Unix(0, 1), End: time.Unix(0, 20)}, 3)
}

func TestResourceInventoryTransferFailureKeepsPublishedOutputRaw(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	for i := range 8 {
		if err := commitRestoredFixture(repo, t.Context(), resourceBatch(fmt.Sprint("part", i), []int64{int64(10 + i)}, nil)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DB.Exec(`CREATE TABLE inventory_backup AS SELECT * FROM read_resource_inventory; DROP TABLE read_resource_inventory; CREATE TABLE read_resource_inventory(batch_id VARCHAR CHECK(NOT starts_with(batch_id,'compact')),signal VARCHAR,namespace VARCHAR,service VARCHAR,resource VARIANT,first_seen TIMESTAMPTZ_NS,last_seen TIMESTAMPTZ_NS); INSERT INTO read_resource_inventory SELECT * FROM inventory_backup; DROP TABLE inventory_backup`); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.CompactParquet(t.Context(), d, 8); err != nil || n != 8 {
		t.Fatalf("publication=%d %v", n, err)
	}
	batches := repo.Parquet.BatchMetadata()
	if len(batches) != 1 {
		t.Fatalf("active files=%d", len(batches))
	}
	markers, err := batchMarkers(t.Context(), d.DB)
	if err != nil || markers[batches[0].ID] {
		t.Fatalf("failed output acknowledged: %v %v", markers, err)
	}
	w := queryrows.Window{Start: time.Unix(0, 1), End: time.Unix(0, 100)}
	assertInventoryOracle(t, d, w, 3)
	if !strings.Contains(d.resourceInventorySource(batches, markers, w), "read_parquet") {
		t.Fatal("unacknowledged output did not use raw files")
	}
	if _, err := d.DB.Exec(`DROP TABLE read_resource_inventory`); err != nil {
		t.Fatal(err)
	}
	if err := createBatchCaches(d.DB); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertInventoryOracle(t, d, w, 3)
}

func TestResourceInventorySignedPhysicalEventClocks(t *testing.T) {
	// Canonical OTLP admission substitutes absent/nonpositive log/metric clocks.
	// Rewrite only physical event clocks under the exact production schema so
	// the reader's signed timestamp contract is exercised for every signal.
	for _, at := range []int64{-100, 0} {
		t.Run(fmt.Sprint(at), func(t *testing.T) {
			d, repo := newBatchCacheTest(t)
			b := resourceBatch("signed", []int64{999, 1000, 1009, 1010}, map[string]any{"typed": int64(7)})
			if err := commitRestoredFixture(repo, t.Context(), b); err != nil {
				t.Fatal(err)
			}
			for _, signal := range []string{"spans", "logs", "metrics"} {
				path := filepath.Join(repo.Parquet.BatchesDir(), "signed.batch", signal+".parquet")
				signedInventoryFixture(t, path, at-1000)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := telemetrystore.Open(d.cfg.TelemetryDir())
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			d, err = NewDuck(t.Context(), d.cfg, reopened)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			w := queryrows.Window{Start: time.Unix(0, at), End: time.Unix(0, at+10)}
			assertInventoryOracle(t, d, w, 3)
			if _, err := d.RefreshReadCaches(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertInventoryOracle(t, d, w, 3)
			var first, last int64
			if err := d.QueryRowScan(inventoryContext(t.Context(), w), []any{&first, &last}, `SELECT min(epoch_ns(first_seen)),max(epoch_ns(last_seen)) FROM resource_inventory`); err != nil || first != at || last != at+9 {
				t.Fatalf("signed extrema=%d/%d %v", first, last, err)
			}
			w.Start = time.Unix(0, at-2)
			w.End = time.Unix(0, at+11)
			assertInventoryOracle(t, d, w, 3)
		})
	}
}

func signedInventoryFixture(t *testing.T, path string, offset int64) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	file, err := parquet.OpenFile(f, stat.Size())
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(path + ".signed")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	writer := parquet.NewGenericWriter[any](out, file.Schema())
	columns := file.Schema().Columns()
	for _, group := range file.RowGroups() {
		reader := group.Rows()
		for {
			rows := make([]parquet.Row, 1)
			n, err := reader.ReadRows(rows)
			if err != nil && err != io.EOF {
				t.Fatal(err)
			}
			if n == 0 {
				break
			}
			row := rows[0].Clone()
			for i, value := range row {
				name := columns[value.Column()][0]
				if name == "start_time" || name == "start_unix_nano" || name == "log_time" || name == "metric_time" || name == "time_unix_nano" {
					row[i] = parquet.Int64Value(value.Int64()+offset).Level(value.RepetitionLevel(), value.DefinitionLevel(), value.Column())
				}
			}
			if _, err := writer.WriteRows([]parquet.Row{row}); err != nil {
				t.Fatal(err)
			}
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".signed", path); err != nil {
		t.Fatal(err)
	}
}

func TestResourceInventoryPanelSQLBoundary(t *testing.T) {
	d, _ := newBatchCacheTest(t)
	for _, relation := range []string{"resource_inventory", "main.resource_inventory"} {
		statement := "SELECT signal,namespace,service,resource,first_seen,last_seen FROM " + relation
		if _, _, err := d.PrepareTelemetrySQL(t.Context(), statement, statement, 10); err != nil {
			t.Fatalf("panel relation %s: %v", relation, err)
		}
		if result := d.ExecuteSQL(t.Context(), SQLRequest{Query: statement}); result.Error == "" {
			t.Fatalf("unscoped inventory accepted: %+v", result)
		}
	}
	for _, statement := range []string{
		"SELECT * FROM telemetry.resource_inventory", "SELECT * FROM memory.main.resource_inventory",
		"SELECT * FROM read_resource_inventory", "SELECT * FROM read_batches",
		"SELECT * FROM read_parquet('private.parquet')", "SELECT * FROM query_table('read_resource_inventory')",
		"SELECT * FROM duckdb_tables()", "SELECT 1; SELECT 2", "DELETE FROM resource_inventory",
	} {
		if _, _, err := d.PrepareTelemetrySQL(t.Context(), statement, statement, 10); err == nil {
			t.Fatalf("unsafe panel SQL accepted: %s", statement)
		}
	}
}

func TestResourceInventoryProjectionPreservesTypesAndQualifiedBindings(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	resource := map[string]any{"n": int64(9007199254740993), "b": false, "bytes": []byte{0, 255}, "nested": []any{int64(1), map[string]any{"x": true}}, "dotted.key": "literal"}
	if err := repo.Commit(t.Context(), resourceBatch("types", []int64{10}, resource)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	w := queryrows.Window{Start: time.Unix(0, 1), End: time.Unix(0, 20)}
	statement := `SELECT main.resource_inventory.resource,resource['n']::BIGINT AS n,resource['b']::BOOLEAN AS b,resource['dotted.key']::VARCHAR AS dotted,epoch_ns(main.resource_inventory.first_seen) AS first FROM main.resource_inventory WHERE signal='spans'`
	prepared, serialized, err := d.PrepareTelemetrySQL(t.Context(), statement, statement, 10)
	if err != nil || len(serialized) != 5 || !serialized[0] {
		t.Fatalf("projection=%v %v", serialized, err)
	}
	rows, err := d.QueryContext(inventoryContext(t.Context(), w), prepared)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var payload, dotted string
	var n, first int64
	var b bool
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	if err := rows.Scan(&payload, &n, &b, &dotted, &first); err != nil || n != 9007199254740993 || b || dotted != "literal" || first != 10 || !strings.Contains(payload, `"n":9007199254740993`) || !strings.Contains(payload, `"nested":[1,{"x":true}]`) {
		t.Fatalf("typed row=%s %d %v %s %d: %v", payload, n, b, dotted, first, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected extra row/error: %v", rows.Err())
	}
}

func TestResourceInventoryTransferPreservesNullAndEmptyGroups(t *testing.T) {
	d, _ := newBatchCacheTest(t)
	for _, statement := range []string{
		`INSERT INTO read_batches(batch_id) VALUES ('a'),('b')`,
		`INSERT INTO read_resource_inventory SELECT id,'spans',namespace,service,resource,make_timestamp_ns(1)::TIMESTAMPTZ_NS,make_timestamp_ns(2)::TIMESTAMPTZ_NS FROM (VALUES ('a'),('b')) b(id) CROSS JOIN (VALUES (NULL::VARCHAR,NULL::VARCHAR,NULL::VARIANT),('','',NULL::VARIANT),('','',map()::VARIANT)) r(namespace,service,resource)`,
	} {
		if _, err := d.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.transferBatchCache(t.Context(), "out", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	var total, nulls, empty int
	if err := d.DB.QueryRow(`SELECT count(*),count(*) FILTER (WHERE namespace IS NULL AND service IS NULL AND resource IS NULL),count(*) FILTER (WHERE namespace='' AND service='' AND resource IS NOT NULL) FROM read_resource_inventory WHERE batch_id='out'`).Scan(&total, &nulls, &empty); err != nil || total != 3 || nulls != 1 || empty != 1 {
		t.Fatalf("null/empty groups=%d/%d/%d %v", total, nulls, empty, err)
	}
}
