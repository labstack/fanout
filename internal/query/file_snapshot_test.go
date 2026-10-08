package query

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query/writegate"
	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func newBatchCacheTest(t *testing.T) (*Duck, *telemetrystore.Repository) {
	t.Helper()
	return newBatchCacheTestAt(t, filepath.Join(t.TempDir(), "données-🙂"))
}

func newBatchCacheTestAt(t *testing.T, dataDir string) (*Duck, *telemetrystore.Repository) {
	t.Helper()
	cfg := config.Config{DataDir: dataDir, DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2}
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
	w.Kind = queryrows.TraceCandidateRead
	var count int
	q := `SELECT count(DISTINCT trace_id) FROM (SELECT trace_id FROM trace_candidates UNION ALL SELECT trace_id FROM trace_tail)`
	if err := d.QueryRowScan(queryrows.WithWindow(t.Context(), w), []any{&count}, q); err != nil || count != 5 {
		t.Fatalf("partial/late trace candidates=%d: %v", count, err)
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
	markers, err := batchMarkers(t.Context(), d.DB)
	if err != nil || len(markers) != 9 {
		t.Fatalf("output was not derived from eight complete inputs: %v %v", markers, err)
	}
	check := func() {
		t.Helper()
		w := queryrows.Window{Start: at.Add(-time.Minute), End: at.Add(time.Minute)}
		var count int
		w.Kind = queryrows.TraceCandidateRead
		w.Namespace = "prod"
		w.Service = "api"
		if err := d.QueryRowScan(queryrows.WithWindow(t.Context(), w), []any{&count}, `SELECT (SELECT count(*) FROM trace_candidates)+(SELECT count(*) FROM trace_tail)`); err != nil {
			t.Fatal(err)
		}
		// A replacement derived from complete inputs is immediately cached even
		// while the retired input markers remain. It has one candidate and no tail.
		if count != 1 {
			t.Fatalf("candidate coverage=%d", count)
		}
	}
	check()
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	check()
	markers, err = batchMarkers(t.Context(), d.DB)
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
	if err := d.DB.QueryRow(`SELECT count(*) FROM read_trace_parts`).Scan(&count); err != nil || count != 0 {
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
	for _, table := range []string{"read_batches", "read_trace_parts", "read_trace_candidates"} {
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
	// Fail after the span writes, before acknowledgement.
	if _, err := d.DB.Exec(`DROP TABLE read_trace_candidates`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err == nil {
		t.Fatal("broken cache schema accepted")
	}
	for _, table := range []string{"read_batches", "read_trace_parts"} {
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

func TestReadCacheSemanticVersionRebuildsAllContributions(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Minute)
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "semantic", Spans: []telemetry.Span{{TraceID: "semantic-trace", StartUnixNanos: at.UnixNano(), EndUnixNanos: at.UnixNano() + 1, IngestedAt: at.UnixNano()}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`UPDATE read_trace_candidates SET min_start=0`, `UPDATE read_cache_version SET version=3`, `CREATE TABLE read_endpoints (value INTEGER)`, `CREATE TABLE read_logs (value INTEGER)`, `CREATE TABLE read_obsolete (value INTEGER)`, `CREATE TABLE endpoint_rollup (value INTEGER)`, `INSERT INTO rollup_state VALUES ('endpoint_rollup:prod',1,now())`} {
		if _, err := d.DB.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := CreateCacheTables(d.DB); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"read_batches", "read_trace_parts", "read_trace_candidates"} {
		var count int
		if err := d.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s survived semantic change: %d %v", table, count, err)
		}
	}
	var obsolete int
	if err := d.DB.QueryRow(`SELECT count(*) FROM duckdb_tables() WHERE table_name IN ('read_obsolete','endpoint_rollup','read_endpoints','read_logs')`).Scan(&obsolete); err != nil || obsolete != 0 {
		t.Fatalf("obsolete tables=%d: %v", obsolete, err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM rollup_state WHERE starts_with(cache_key,'endpoint_rollup')`).Scan(&obsolete); err != nil || obsolete != 0 {
		t.Fatalf("obsolete watermark=%d: %v", obsolete, err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	var minStart int64
	if err := d.DB.QueryRow(`SELECT min_start FROM read_trace_candidates`).Scan(&minStart); err != nil || minStart != at.UnixNano() {
		t.Fatalf("rebuilt bound=%d: %v", minStart, err)
	}
	var version int
	if err := d.DB.QueryRow(`SELECT version FROM read_cache_version`).Scan(&version); err != nil || version != 4 {
		t.Fatalf("version=%d: %v", version, err)
	}

	// A matching version preserves valid acknowledgements on the next startup.
	if err := CreateCacheTables(d.DB); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM read_batches`).Scan(&obsolete); err != nil || obsolete != 1 {
		t.Fatalf("current cache discarded: %d %v", obsolete, err)
	}
}

func TestBroadGlobRetainsCapturedFileSet(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC()
	for i := range 64 {
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprintf("glob-%d", i), Spans: []telemetry.Span{{TraceID: "trace", SpanID: fmt.Sprint(i), StartUnixNanos: at.UnixNano(), IngestedAt: at.UnixNano()}}}); err != nil {
			t.Fatal(err)
		}
	}
	captured := repo.Parquet.BatchMetadata()
	source := d.snapshotSource("spans", captured, captured)
	if !strings.Contains(source, "*.batch") {
		t.Fatal("broad selection did not use glob")
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "new-after-capture", Spans: []telemetry.Span{{TraceID: "new", StartUnixNanos: at.UnixNano(), IngestedAt: at.UnixNano()}}}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.DB.QueryRow("SELECT count(*) FROM (" + source + ")").Scan(&count); err != nil || count != 64 {
		t.Fatalf("captured rows=%d: %v", count, err)
	}
	narrow := d.snapshotSource("spans", captured[:1], captured)
	if strings.Contains(narrow, "*.batch") {
		t.Fatal("narrow selection used glob")
	}
	if err := d.DB.QueryRow("SELECT count(*) FROM (" + narrow + ")").Scan(&count); err != nil || count != 1 {
		t.Fatalf("narrow rows=%d: %v", count, err)
	}
}

func TestBatchFilenameIdentityWithRelativeDataDir(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, filepath.Join(t.TempDir(), "données-🙂"))
	if err != nil {
		t.Fatal(err)
	}
	d, repo := newBatchCacheTestAt(t, relative)
	for i := range readCacheBatchLimit {
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprint("relative", i), Spans: []telemetry.Span{{TraceID: fmt.Sprint(i), StartUnixNanos: 1, EndUnixNanos: 2, IngestedAt: 1}}, Logs: []telemetry.Log{{TimeUnixNanos: 1, IngestedAt: 1}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.DB.QueryRow(`SELECT count(*) FROM read_trace_parts WHERE batch_id IN (SELECT batch_id FROM read_batches)`).Scan(&count); err != nil || count != readCacheBatchLimit {
		t.Fatalf("relative cache batch identity=%d: %v", count, err)
	}
	active := repo.Parquet.BatchMetadata()
	if err := d.DB.QueryRow(`SELECT count(*) FROM (` + d.snapshotSource("logs", active, active) + `)`).Scan(&count); err != nil || count != readCacheBatchLimit {
		t.Fatalf("relative glob identity=%d: %v", count, err)
	}
}

func TestPreparedReplacementDoesNotHoldCacheWriteGate(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "input", Logs: []telemetry.Log{{TimeUnixNanos: 1, IngestedAt: 1}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Pin a reader so publication has to drain. The worker must still be able to
	// acknowledge new ingest and must not collect the prepared output marker.
	if err := d.parquetMu.RLockContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer d.parquetMu.RUnlock()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- d.PublishParquetReplacement(ctx, telemetry.BatchMetadata{ID: "output"}, []string{"input"}, func(context.Context) error { return nil })
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		markers, err := batchMarkers(t.Context(), d.DB)
		if err != nil {
			t.Fatal(err)
		}
		if markers["output"] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("replacement was not prepared")
		}
		time.Sleep(time.Millisecond)
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "new-input", Logs: []telemetry.Log{{TimeUnixNanos: 2, IngestedAt: 2}}}); err != nil {
		t.Fatal(err)
	}
	passCtx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if _, err := d.RefreshReadCaches(passCtx); err != nil {
		t.Fatalf("worker blocked behind publication: %v", err)
	}
	markers, err := batchMarkers(t.Context(), d.DB)
	if err != nil || !markers["output"] || !markers["new-input"] {
		t.Fatalf("prepared output/new ingest lost: %v %v", markers, err)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled publication succeeded")
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	markers, err = batchMarkers(t.Context(), d.DB)
	if err != nil || markers["output"] {
		t.Fatalf("failed output was not collected: %v %v", markers, err)
	}
}

func TestRetiredTraceRepairsOnlyItsActiveScopeAndBounds(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Hour)
	old := at.Add(-2 * time.Hour).UnixNano()
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "retire-scope", Spans: []telemetry.Span{{Namespace: "gone", ServiceName: "retired", TraceID: "shared", StartUnixNanos: old, EndUnixNanos: old + 1, StatusCode: "ERROR", IngestedAt: old}}}); err != nil {
		t.Fatal(err)
	}
	now := at.UnixNano()
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "keep-scope", Spans: []telemetry.Span{
		{Namespace: "prod", ServiceName: "api", TraceID: "shared", StartUnixNanos: now, EndUnixNanos: now + 1, IngestedAt: now},
		{Namespace: "prod", ServiceName: "api", TraceID: "winner", StartUnixNanos: now, EndUnixNanos: now + 1000, IngestedAt: now},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if count, err := repo.PruneParquet(t.Context(), d, at.Add(-time.Hour).UnixNano(), 8); err != nil || count != 1 {
		t.Fatalf("prune=%d: %v", count, err)
	}
	check := func() {
		t.Helper()
		for _, ns := range []string{"", "prod"} {
			w := queryrows.Window{Start: at.Add(-3 * time.Hour), End: at.Add(time.Minute), Kind: queryrows.TraceCandidateRead, Namespace: ns}
			var id string
			if err := d.QueryRowScan(queryrows.WithWindow(t.Context(), w), []any{&id}, `SELECT trace_id FROM trace_candidates ORDER BY has_error DESC,max_end-min_start DESC,trace_id LIMIT 1`); err != nil || id != "winner" {
				t.Fatalf("namespace %q winner=%q: %v", ns, id, err)
			}
		}
	}
	check()
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	check()
	var namespace, service string
	var hasError int
	if err := d.DB.QueryRow(`SELECT namespace,service,has_error FROM read_trace_candidates WHERE trace_id='shared'`).Scan(&namespace, &service, &hasError); err != nil || namespace != "prod" || service != "api" || hasError != 0 {
		t.Fatalf("remaining scope=%q/%q error=%d: %v", namespace, service, hasError, err)
	}
}

func TestTraceCacheFollowsMultipleCompactionLevels(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Hour)
	for i := range 64 {
		n := at.Add(time.Duration(i) * time.Second).UnixNano()
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprint("level", i), Spans: []telemetry.Span{{TraceID: "shared", StartUnixNanos: n, EndUnixNanos: n + 1, IngestedAt: n}}}); err != nil {
			t.Fatal(err)
		}
	}
	for range 4 {
		if _, err := d.RefreshReadCaches(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for range 9 {
		if count, err := repo.CompactParquet(t.Context(), d, 8); err != nil || count != 8 {
			t.Fatalf("compact=%d: %v", count, err)
		}
	}
	if batches := repo.Parquet.BatchMetadata(); len(batches) != 1 || batches[0].Generation != 2 {
		t.Fatalf("replacement=%v", batches)
	}
	w := queryrows.Window{Start: at.Add(-time.Minute), End: at.Add(2 * time.Minute), Kind: queryrows.TraceCandidateRead}
	var count int
	if err := d.QueryRowScan(queryrows.WithWindow(t.Context(), w), []any{&count}, `SELECT count(*) FROM trace_candidates`); err != nil || count != 1 {
		t.Fatalf("candidate=%d: %v", count, err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM read_batches`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("retired markers=%d: %v", count, err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM read_trace_candidates`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("trace index=%d: %v", count, err)
	}
}

func TestCompletedBatchWriterProgressesDuringAnalyticalTransaction(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "independent", Spans: []telemetry.Span{{TraceID: "trace", StartUnixNanos: 1, EndUnixNanos: 2, IngestedAt: 1}}}); err != nil {
		t.Fatal(err)
	}
	unlock := d.writeGate.Lock(writegate.WriteRollupService)
	defer unlock()
	tx, err := d.writer().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO rollup_state VALUES ('blocked-analytical-pass',1,now())`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := d.RefreshReadCaches(ctx); err != nil {
		t.Fatalf("cache stalled behind disjoint analytical write: %v", err)
	}
	markers, err := batchMarkers(ctx, d.DB)
	if err != nil || !markers["independent"] {
		t.Fatalf("independent batch not published: %v %v", markers, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestReadCacheRowBudgetAndOversizedFileProgress(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	old := make([]telemetry.Span, readCacheRowBudget+1)
	for i := range old {
		old[i] = telemetry.Span{TraceID: fmt.Sprint(i), StartUnixNanos: 1, EndUnixNanos: 2, IngestedAt: 1}
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "oversized", Spans: old}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "newest", Logs: []telemetry.Log{{TimeUnixNanos: 2, IngestedAt: 2}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	markers, err := batchMarkers(t.Context(), d.DB)
	if err != nil || !markers["newest"] || markers["oversized"] {
		t.Fatalf("row budget not respected: %v %v", markers, err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	markers, err = batchMarkers(t.Context(), d.DB)
	if err != nil || !markers["oversized"] {
		t.Fatalf("oversized file made no progress: %v %v", markers, err)
	}
	var count int
	if err := d.DB.QueryRow(`SELECT count(*) FROM read_trace_parts WHERE batch_id='oversized'`).Scan(&count); err != nil || count != len(old) {
		t.Fatalf("oversized count=%d: %v", count, err)
	}
}
