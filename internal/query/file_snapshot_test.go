package query

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func newBatchCacheTest(t *testing.T) (*Duck, *telemetrystore.Repository) {
	t.Helper()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	duck, err := NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = duck.Close(); _ = repo.Close() })
	return duck, repo
}
func TestSnapshotUsesActualEventBoundsAndPreservesParameters(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i := range 3 {
		event := at.Add(time.Duration(i-1) * time.Hour).UnixNano()
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprint("batch", i), Spans: []telemetry.Span{{TraceID: "trace", SpanID: fmt.Sprint(i), StartUnixNanos: event, EndUnixNanos: event + 1, IngestedAt: at.UnixNano()}}, Logs: []telemetry.Log{{TimeUnixNanos: event, IngestedAt: at.UnixNano()}}, Metrics: []telemetry.Metric{{TimeUnixNanos: event, IngestedAt: at.UnixNano()}}}); err != nil {
			t.Fatal(err)
		}
	}
	w := queryrows.Window{Start: at, End: at.Add(time.Minute)}
	ctx := queryrows.WithWindow(t.Context(), w)
	rows, err := d.QueryContext(ctx, `SELECT (SELECT count(*) FROM spans WHERE start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS), (SELECT count(*) FROM logs WHERE time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS), (SELECT count(*) FROM metrics WHERE time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS)`, w.Start, w.End, w.Start, w.End, w.Start, w.End)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var spans, logs, metrics int
	for rows.Next() {
		if err := rows.Scan(&spans, &logs, &metrics); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if spans != 1 || logs != 1 || metrics != 1 {
		t.Fatalf("counts=%d,%d,%d", spans, logs, metrics)
	}
	conn, err := d.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rewritten, err := d.bindSnapshot(t.Context(), conn, `SELECT * FROM telemetry.logs`, w)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rewritten, "batch0.batch") || strings.Contains(rewritten, "batch2.batch") || !strings.Contains(rewritten, "batch1.batch") {
		t.Fatal(rewritten)
	}
	rewritten, err = d.bindSnapshot(t.Context(), conn, `WITH logs AS (SELECT 7 AS value) SELECT value FROM logs`, w)
	if err != nil {
		t.Fatal(err)
	}
	var value int
	if err := conn.QueryRowContext(t.Context(), rewritten).Scan(&value); err != nil || value != 7 {
		t.Fatalf("CTE shadow: %d %v", value, err)
	}
	for _, statement := range []string{
		`SELECT count(telemetry.logs.namespace) FROM telemetry.logs`,
		`WITH x AS (SELECT count(*) AS n FROM logs) SELECT n FROM x`,
		`SELECT count(*) FROM logs WHERE '__fanout_snapshot_metrics'<>''`,
	} {
		if err := d.QueryRowScan(ctx, []any{&value}, statement); err != nil || value != 1 {
			t.Fatalf("snapshot SQL %s: %d %v", statement, value, err)
		}
	}

}
func TestCompletedBatchCachesIncludeLatePublicationAndPartialMinutes(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	commit := func(id string, seconds []int) {
		t.Helper()
		b := telemetrystore.Batch{ID: id}
		for i, second := range seconds {
			n := at.Add(time.Duration(second) * time.Second).UnixNano()
			b.Spans = append(b.Spans, telemetry.Span{Namespace: "prod", ServiceName: "api", TraceID: fmt.Sprint("trace", i), SpanID: fmt.Sprint(id, i), StartUnixNanos: n, EndUnixNanos: n + 1000000, DurationMS: 1, IngestedAt: at.UnixNano(), StatusCode: "ERROR"})
			b.Logs = append(b.Logs, telemetry.Log{Namespace: "prod", ServiceName: "api", TimeUnixNanos: n, Severity: "Error", IngestedAt: at.UnixNano()})
		}
		if err := repo.Commit(t.Context(), b); err != nil {
			t.Fatal(err)
		}
	}
	commit("cached", []int{5, 30, 60, 90, 125})
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	commit("late", []int{35, 75, 130})
	w := queryrows.Window{Start: at.Add(20 * time.Second), End: at.Add(140 * time.Second), Namespace: "prod", Service: "api"}
	for _, kind := range []queryrows.ReadKind{queryrows.EndpointRead, queryrows.LogHistogramRead} {
		w.Kind = kind
		ctx := queryrows.WithWindow(t.Context(), w)
		q := `SELECT (SELECT coalesce(sum(calls),0) FROM endpoint_minutes)+(SELECT count(*) FROM endpoint_tail)`
		if kind == queryrows.LogHistogramRead {
			q = `SELECT (SELECT coalesce(sum(count),0) FROM log_minutes)+(SELECT coalesce(sum(count),0) FROM log_tail)`
		}
		rows, err := d.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		for rows.Next() {
			if err := rows.Scan(&count); err != nil {
				t.Fatal(err)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if count != 7 {
			t.Fatalf("kind %d count %d", kind, count)
		}
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	// A transaction sees complete contribution sets and markers together.
	var markers, parts int
	if err := d.DB.QueryRow(`SELECT count(*) FROM read_batches`).Scan(&markers); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM read_trace_parts`).Scan(&parts); err != nil {
		t.Fatal(err)
	}
	if markers != 2 || parts != 8 {
		t.Fatalf("markers=%d parts=%d", markers, parts)
	}
}
func TestUnknownEventStatisticsNeverExcludeFiles(t *testing.T) {
	b := telemetry.BatchMetadata{Spans: 1}
	w := queryrows.Window{Start: time.Unix(0, 1), End: time.Unix(0, 2)}
	if !overlapping(b, "spans", w) {
		t.Fatal("unknown bounds excluded")
	}
	b.SpanTime = telemetry.TimeRange{Known: true, MinNanos: -2, MaxNanos: 0}
	if overlapping(b, "spans", w) {
		t.Fatal("disjoint negative bounds included")
	}
}

func TestBatchCachesStayExactThroughCompactionAndRetention(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Hour)
	for i := range 8 {
		n := at.Add(time.Duration(i) * time.Second).UnixNano()
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprint("part", i), Spans: []telemetry.Span{{Namespace: "prod", ServiceName: "api", TraceID: "trace", SpanID: fmt.Sprint(i), StartUnixNanos: n, EndUnixNanos: n + 1, IngestedAt: at.UnixNano()}}, Logs: []telemetry.Log{{Namespace: "prod", TimeUnixNanos: n, IngestedAt: at.UnixNano()}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if count, err := repo.CompactParquet(t.Context(), d, 8); err != nil || count != 8 {
		t.Fatalf("compact %d: %v", count, err)
	}
	check := func() {
		t.Helper()
		w := queryrows.Window{Start: at.Add(-time.Minute), End: at.Add(time.Minute), Kind: queryrows.EndpointRead}
		var count int
		if err := d.QueryRowScan(queryrows.WithWindow(t.Context(), w), []any{&count}, `SELECT (SELECT coalesce(sum(calls),0) FROM endpoint_minutes)+(SELECT count(*) FROM endpoint_tail)`); err != nil || count != 8 {
			t.Fatalf("after replacement count=%d: %v", count, err)
		}
		w.Kind = queryrows.TraceCandidateRead
		w.Namespace = "prod"
		w.Service = "api"
		if err := d.QueryRowScan(queryrows.WithWindow(t.Context(), w), []any{&count}, `SELECT (SELECT count(*) FROM trace_candidates)+(SELECT count(*) FROM trace_tail)`); err != nil {
			t.Fatal(err)
		}
		// Before the replacement is cached every raw span is visible. Afterwards
		// the one exact cached trace is visible and the tail contains no rows.
		if count != 8 && count != 1 {
			t.Fatalf("candidate coverage=%d", count)
		}
	}
	check()
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	check()
	markers, err := batchMarkers(t.Context(), d.DB)
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 1 {
		t.Fatalf("obsolete markers survived: %#v", markers)
	}
	if count, err := repo.PruneParquet(t.Context(), d, at.Add(time.Second).UnixNano(), 8); err != nil || count != 1 {
		t.Fatalf("prune %d: %v", count, err)
	}
	w := queryrows.Window{Start: at.Add(-time.Minute), End: at.Add(time.Minute), Kind: queryrows.TraceCandidateRead, Namespace: "prod", Service: "api"}
	var count int
	if err := d.QueryRowScan(queryrows.WithWindow(t.Context(), w), []any{&count}, `SELECT (SELECT count(*) FROM trace_candidates)+(SELECT count(*) FROM trace_tail)`); err != nil || count != 0 {
		t.Fatalf("retired trace visible: %d: %v", count, err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM read_traces`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retired index retained: %d %v", count, err)
	}
}
func TestReadCacheCancellationLeavesNoMarkerOrPartialContributions(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "cancelled", Spans: []telemetry.Span{{TraceID: "trace", StartUnixNanos: 1, IngestedAt: 1}}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := d.RefreshReadCaches(ctx); err == nil {
		t.Fatal("cancelled backfill succeeded")
	}
	for _, table := range []string{"read_batches", "read_endpoints", "read_trace_parts", "read_traces"} {
		var count int
		if err := d.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s partial rows=%d: %v", table, count, err)
		}
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestFailedBatchCacheTransactionPublishesNoContributions(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "atomic", Spans: []telemetry.Span{{TraceID: "trace", StartUnixNanos: 1, IngestedAt: 1}}, Logs: []telemetry.Log{{TimeUnixNanos: 1, IngestedAt: 1}}}); err != nil {
		t.Fatal(err)
	}
	// Fail after the span and exact-log-time writes, before acknowledgement.
	if _, err := d.DB.Exec(`DROP TABLE read_logs`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err == nil {
		t.Fatal("broken cache schema accepted")
	}
	for _, table := range []string{"read_batches", "read_span_events", "read_log_times", "read_endpoints", "read_trace_parts", "read_traces"} {
		var count int
		if err := d.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s partial rows=%d: %v", table, count, err)
		}
	}
	if err := createBatchCaches(d.DB); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.DB.QueryRow(`SELECT count(*) FROM read_batches`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("retry markers=%d: %v", count, err)
	}
}

func TestReadCacheWorkerAcknowledgesNewFilesWithoutRollupTick(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); d.runReadCacheLoop(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "new", Logs: []telemetry.Log{{TimeUnixNanos: 1, IngestedAt: 1}}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("new publication was not cached")
		case <-tick.C:
			var count int
			if err := d.DB.QueryRow(`SELECT count(*) FROM read_batches`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count == 1 {
				return
			}
		}
	}
}
