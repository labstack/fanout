package query

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestM2AnnotationWidenedRangeAccepted(t *testing.T) {
	d, _ := versionEngine(t)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, days := range []int{31, 430} {
		got, err := annotations.New(d).Read(t.Context(), annotations.Request{From: from, To: from.Add(time.Duration(days) * 24 * time.Hour)})
		if err != nil || len(got.Deploys) != 0 || len(got.Anomalies) != 0 {
			t.Fatalf("%d-day range: %+v %v", days, got, err)
		}
	}
}

func TestM2AnnotationWidenedRangeRejected(t *testing.T) {
	d, _ := versionEngine(t)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	_, err := annotations.New(d).Read(t.Context(), annotations.Request{From: from, To: from.Add(430*24*time.Hour + time.Nanosecond)})
	const want = "annotations need a positive range of at most 430 days and at most 100 services"
	if !errors.Is(err, annotations.ErrRequest) || err.Error() != want {
		t.Fatalf("range just over 430 days: want ErrRequest %q, got %v", want, err)
	}
}

func versionEngine(t *testing.T) (*Duck, *telemetrystore.Repository) {
	t.Helper()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RetentionDays: 30}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(); _ = repo.Close() })
	return d, repo
}

func TestM2VersionIncrementalLateBatch(t *testing.T) {
	d, repo := versionEngine(t)
	at := time.Now().UTC().Add(-time.Hour)
	add := func(id, version string, offset time.Duration) {
		t.Helper()
		n := at.Add(offset).UnixNano()
		err := repo.Commit(t.Context(), telemetrystore.Batch{ID: id, Spans: []telemetry.Span{{
			Namespace: "shop", ServiceName: "checkout", ServiceVersion: version,
			TraceID: id, SpanID: id, Name: "checkout", Kind: "SPAN_KIND_SERVER",
			StartUnixNanos: n, EndUnixNanos: n + 1000000, DurationMS: 1,
			IngestedAt: at.UnixNano(),
		}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	add("v-first", "v1", 0)
	if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	add("v-late", "v2", 10*time.Minute) // Same ingestion tip; published later.
	if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n, err := d.RefreshVersionRollup(t.Context()); err != nil || n != 0 {
		t.Fatalf("noop: %d %v", n, err)
	}
	got, err := annotations.New(d).Read(t.Context(), annotations.Request{From: at.Add(-time.Minute), To: at.Add(time.Hour), Namespace: "shop", Services: []string{"checkout"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Deploys) != 1 || got.Deploys[0].Version != "v2" || !got.Deploys[0].At.Equal(at.Add(10*time.Minute)) {
		t.Fatalf("deploys: %+v", got)
	}
	var markers int
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches`).Scan(&markers); err != nil || markers != 2 {
		t.Fatalf("batch markers: %d %v", markers, err)
	}
}

func TestM2AnnotationHistoryBounds(t *testing.T) {
	started := time.Now()
	d, _ := versionEngine(t)
	now := time.Now().UTC()
	if _, err := d.DB.Exec(`INSERT INTO anomaly_log SELECT 'shop','service-'||i,'latency',(?::TIMESTAMP_NS-i*INTERVAL '1 minute'-INTERVAL '1 minute')::TIMESTAMPTZ_NS,(?::TIMESTAMP_NS-i*INTERVAL '1 minute')::TIMESTAMPTZ_NS,'Slow','warn' FROM generate_series(1,10000) t(i)`, now, now); err != nil {
		t.Fatal(err)
	}
	finding := annotations.Anomaly{Namespace: "shop", Service: "new", Kind: "latency", From: now.Add(-time.Minute), To: now, Title: "Slow", Severity: "warn"}
	if err := d.RecordAnomalies(t.Context(), []annotations.Anomaly{finding}, now); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.DB.QueryRow(`SELECT count(*) FROM anomaly_log`).Scan(&n); err != nil || n != 10000 {
		t.Fatalf("cap: %d %v", n, err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM anomaly_log WHERE service='service-10000'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("oldest survived: %d %v", n, err)
	}
	if err := d.RecordAnomalies(t.Context(), nil, now.Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM anomaly_log`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("age: %d %v", n, err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("history bounds exceeded 1s: %v", elapsed)
	}
	t.Logf("history bounds took %v", time.Since(started))
}

func TestM2AnnotationSourcesNoRawScan(t *testing.T) {
	d, repo := versionEngine(t)
	at := time.Now().UTC().Add(-time.Hour)
	for i, version := range []string{"v1", "v2"} {
		n := at.Add(time.Duration(i) * time.Minute).UnixNano()
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprint("scan-", i), Spans: []telemetry.Span{{Namespace: "shop", ServiceName: "checkout", ServiceVersion: version, TraceID: "trace", SpanID: fmt.Sprint(i), StartUnixNanos: n, EndUnixNanos: n + 1, IngestedAt: n}}, Logs: []telemetry.Log{{Namespace: "shop", ServiceName: "checkout", Body: "real log", EventUnixNanos: n, IngestedAt: n}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.DrainVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	counter := &annotationCountingEngine{Duck: d}
	got, err := annotations.New(counter).Read(t.Context(), annotations.Request{From: at, To: at.Add(time.Hour), Namespace: "shop"})
	if err != nil || len(got.Deploys) != 1 || counter.calls != 3 || counter.raw != 0 {
		t.Fatalf("cache reads: %+v calls=%d raw=%d err=%v", got, counter.calls, counter.raw, err)
	}
}

type annotationCountingEngine struct {
	*Duck
	calls, raw int
}

func (e *annotationCountingEngine) QueryContext(ctx context.Context, text string, args ...any) (queryrows.Rows, error) {
	e.calls++
	for _, source := range []string{"FROM spans", "FROM logs", "FROM metrics", "read_parquet"} {
		if strings.Contains(text, source) {
			e.raw++
			return nil, fmt.Errorf("raw telemetry read: %s", source)
		}
	}
	return e.Duck.QueryContext(ctx, text, args...)
}

func TestM2VersionPassAndHistoryCaps(t *testing.T) {
	d, repo := versionEngine(t)
	at := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 65; i++ {
		id := fmt.Sprintf("version-%03d", i)
		n := at.Add(time.Duration(i) * time.Millisecond).UnixNano()
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: id, Spans: []telemetry.Span{{Namespace: "shop", ServiceName: "checkout", ServiceVersion: "v1", TraceID: id, SpanID: id, Name: "root", StartUnixNanos: n, EndUnixNanos: n + 1000, DurationMS: .001, IngestedAt: n}}}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := d.RefreshVersionRollup(t.Context()); err != nil || n != 64 {
		t.Fatalf("first pass %d %v", n, err)
	}
	if n, err := d.RefreshVersionRollup(t.Context()); err != nil || n != 1 {
		t.Fatalf("remaining pass %d %v", n, err)
	}
	_, err := d.DB.Exec(`DELETE FROM version_rollup`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.DB.Exec(`INSERT INTO version_rollup SELECT 'shop','checkout','v-'||i,(?::TIMESTAMP_NS + i*INTERVAL '1 millisecond')::TIMESTAMPTZ_NS,(?::TIMESTAMP_NS + i*INTERVAL '1 millisecond')::TIMESTAMPTZ_NS FROM generate_series(1,100005) t(i)`, at, at)
	if err != nil {
		t.Fatal(err)
	}
	d.versionRollupPasses = 64 // Make retirement due for the directly seeded cache rows.
	if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count, initial int
	if err := d.DB.QueryRow(`SELECT count(*),count(*) FILTER(WHERE service_version='v-1') FROM version_rollup`).Scan(&count, &initial); err != nil || count != 100000 || initial != 1 {
		t.Fatalf("bounded history: %d initial %d %v", count, initial, err)
	}
	got, err := annotations.New(d).Read(t.Context(), annotations.Request{From: at, To: at.Add(time.Hour), Namespace: "shop"})
	if err != nil || !got.Truncated || len(got.Deploys) != 1000 {
		t.Fatalf("disclosed cap: %+v %v", got, err)
	}
}
func TestM2VersionDrainAndLimitedReset(t *testing.T) {
	d, repo := versionEngine(t)
	at := time.Now().UTC().Add(-time.Hour)
	for i := range 70 {
		n := at.UnixNano()
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprintf("drain-%03d", i), Logs: []telemetry.Log{{Namespace: "shop", ServiceName: "checkout", Resource: map[string]any{"service.version": "v2"}, EventUnixNanos: n, IngestedAt: n}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	var limited int
	if err := d.DB.QueryRow(`SELECT last_ingested_unix_nano FROM rollup_state WHERE cache_key='version_rollup_v1_limited'`).Scan(&limited); err != nil || limited != 1 {
		t.Fatalf("backlog flag=%d %v", limited, err)
	}
	if n, err := d.DrainVersionRollup(t.Context()); err != nil || n != 6 {
		t.Fatalf("drain=%d %v", n, err)
	}
	if err := d.DB.QueryRow(`SELECT last_ingested_unix_nano FROM rollup_state WHERE cache_key='version_rollup_v1_limited'`).Scan(&limited); err != nil || limited != 0 {
		t.Fatalf("stuck backlog=%d %v", limited, err)
	}
	var marked int
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches`).Scan(&marked); err != nil || marked != 70 {
		t.Fatalf("markers=%d %v", marked, err)
	}
}

func commitVersionLogs(t *testing.T, repo *telemetrystore.Repository, id string, at time.Time, size int) {
	t.Helper()
	logs := make([]telemetry.Log, size)
	for i := range logs {
		n := at.UnixNano()
		version := "v1"
		if i == len(logs)-1 {
			n = at.Add(time.Minute).UnixNano()
			version = "v2"
		}
		logs[i] = telemetry.Log{Namespace: "shop", ServiceName: "checkout", Resource: map[string]any{"service.version": version}, EventUnixNanos: n, IngestedAt: at.UnixNano()}
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: id, Logs: logs}); err != nil {
		t.Fatal(err)
	}
}

func TestM2VersionOversizedBatchDrains(t *testing.T) {
	d, repo := versionEngine(t)
	at := time.Now().UTC().Add(-time.Hour)
	commitVersionLogs(t, repo, "oversized", at, 64001)
	if n, err := d.DrainVersionRollup(t.Context()); err != nil || n != 1 {
		t.Fatalf("oversized drain=%d %v", n, err)
	}
	var marked, limited int
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches WHERE batch_id='oversized'`).Scan(&marked); err != nil || marked != 1 {
		t.Fatalf("oversized marker=%d %v", marked, err)
	}
	if err := d.DB.QueryRow(`SELECT last_ingested_unix_nano FROM rollup_state WHERE cache_key='version_rollup_v1_limited'`).Scan(&limited); err != nil || limited != 0 {
		t.Fatalf("drained limited=%d %v", limited, err)
	}
	got, err := annotations.New(d).Read(t.Context(), annotations.Request{From: at, To: at.Add(time.Hour), Namespace: "shop"})
	if err != nil || got.Truncated || len(got.Deploys) != 1 || got.Deploys[0].Version != "v2" {
		t.Fatalf("oversized annotations=%+v %v", got, err)
	}
}

func TestM2VersionMixedBatchPasses(t *testing.T) {
	d, repo := versionEngine(t)
	at := time.Now().UTC().Add(-time.Hour)
	for i, size := range []int{1, 64001, 2, 64002, 3} {
		commitVersionLogs(t, repo, fmt.Sprintf("mixed-%d", i), at.Add(time.Duration(i)*time.Minute), size)
	}
	for pass := range 5 {
		if n, err := d.RefreshVersionRollup(t.Context()); err != nil || n != 1 {
			t.Fatalf("pass %d processed=%d %v", pass, n, err)
		}
		var marked, limited int
		if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches WHERE batch_id=?`, fmt.Sprintf("mixed-%d", pass)).Scan(&marked); err != nil || marked != 1 {
			t.Fatalf("oldest batch on pass %d marked=%d %v", pass, marked, err)
		}
		want := 1
		if pass == 4 {
			want = 0
		}
		if err := d.DB.QueryRow(`SELECT last_ingested_unix_nano FROM rollup_state WHERE cache_key='version_rollup_v1_limited'`).Scan(&limited); err != nil || limited != want {
			t.Fatalf("pass %d limited=%d want=%d %v", pass, limited, want, err)
		}
	}
	if n, err := d.RefreshVersionRollup(t.Context()); err != nil || n != 0 {
		t.Fatalf("drained pass=%d %v", n, err)
	}
}
func TestM2VersionResourceLogsAndMetrics(t *testing.T) {
	d, repo := versionEngine(t)
	at := time.Now().UTC().Add(-time.Hour)
	n := at.UnixNano()
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "resources", Logs: []telemetry.Log{{Namespace: "shop", ServiceName: "logsvc", Resource: map[string]any{"service.version": "log-v"}, EventUnixNanos: n, IngestedAt: n}}, Metrics: []telemetry.Metric{{Namespace: "shop", ServiceName: "metricsvc", Name: "n", Type: "gauge", Resource: map[string]any{"service.version": "metric-v"}, EventUnixNanos: n, IngestedAt: n}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DrainVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := d.DB.Query(`SELECT service,service_version,first_seen::TIMESTAMP_NS FROM version_rollup ORDER BY service`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for _, want := range [][2]string{{"logsvc", "log-v"}, {"metricsvc", "metric-v"}} {
		var service, version string
		var first time.Time
		if !rows.Next() {
			t.Fatal("missing resource row")
		}
		if err := rows.Scan(&service, &version, &first); err != nil || service != want[0] || version != want[1] || !first.Equal(at) {
			t.Fatalf("%s %s %v %v", service, version, first, err)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("extra rows or error: %v", rows.Err())
	}
}
func TestM2VersionMarkersCompactionRetention(t *testing.T) {
	d, repo := versionEngine(t)
	at := time.Now().UTC().Truncate(time.Hour)
	for i := range 8 {
		n := at.Add(time.Duration(i) * time.Second).UnixNano()
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprint("part-", i), Spans: []telemetry.Span{{Namespace: "shop", ServiceName: "checkout", ServiceVersion: "v1", TraceID: "trace", SpanID: fmt.Sprint(i), StartUnixNanos: n, EndUnixNanos: n + 1, IngestedAt: at.UnixNano()}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.DrainVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.CompactParquet(t.Context(), d, 8); err != nil || n != 8 {
		t.Fatalf("compact=%d %v", n, err)
	}
	var n int
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches`).Scan(&n); err != nil || n != 9 {
		t.Fatalf("output marker not transferred=%d %v", n, err)
	}
	if count, err := d.RefreshVersionRollup(t.Context()); err != nil || count != 0 {
		t.Fatalf("rescanned processed output=%d %v", count, err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("retired inputs=%d %v", n, err)
	}
	if count, err := repo.PruneParquet(t.Context(), d, at.Add(time.Hour).UnixNano(), 8); err != nil || count != 1 {
		t.Fatalf("prune=%d %v", count, err)
	}
	if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("retention markers=%d %v", n, err)
	}
}
func TestM2AnomalyCoalescesOpenEpisode(t *testing.T) {
	d, _ := versionEngine(t)
	at := time.Now().UTC().Add(-time.Hour)
	a := annotations.Anomaly{Namespace: "shop", Service: "checkout", Kind: "latency", From: at, To: at.Add(5 * time.Minute), Title: "Slow", Severity: "warn"}
	if err := d.RecordAnomalies(t.Context(), []annotations.Anomaly{a}, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	a.From = at.Add(time.Minute)
	a.To = at.Add(6 * time.Minute)
	a.Severity = "bad"
	if err := d.RecordAnomalies(t.Context(), []annotations.Anomaly{a}, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var count int
	var first, last time.Time
	var severity string
	if err := d.DB.QueryRow(`SELECT count(*),min(start_time)::TIMESTAMP_NS,max(end_time)::TIMESTAMP_NS,max(severity) FROM anomaly_log`).Scan(&count, &first, &last, &severity); err != nil || count != 1 || !first.Equal(at) || !last.Equal(a.To) || severity != "bad" {
		t.Fatalf("episode=%d %v %v %s %v", count, first, last, severity, err)
	}
	a.From = at.Add(20 * time.Minute)
	a.To = at.Add(25 * time.Minute)
	if err := d.RecordAnomalies(t.Context(), []annotations.Anomaly{a}, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM anomaly_log`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("separate episodes=%d %v", count, err)
	}
}

func TestFinalFixVersionSchemaRebuildClearsPair(t *testing.T) {
	for _, table := range []string{"version_rollup", "version_rollup_batches"} {
		t.Run(table, func(t *testing.T) {
			d, repo := versionEngine(t)
			commitVersionLogs(t, repo, "rebuild", time.Now().UTC().Add(-time.Hour), 3)
			if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
				t.Fatal(err)
			}
			column := "last_seen"
			if table == "version_rollup_batches" {
				column = "max_ingested"
			}
			if _, err := d.DB.Exec("ALTER TABLE " + table + " DROP COLUMN " + column); err != nil {
				t.Fatal(err)
			}
			if err := createAnnotationTables(d.DB); err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{"SELECT count(*) FROM version_rollup", "SELECT count(*) FROM version_rollup_batches", "SELECT count(*) FROM rollup_state WHERE cache_key LIKE 'version_rollup%'"} {
				var n int
				if err := d.DB.QueryRow(q).Scan(&n); err != nil || n != 0 {
					t.Fatalf("stale paired state: %s count=%d err=%v", q, n, err)
				}
			}
			if n, err := d.RefreshVersionRollup(t.Context()); err != nil || n != 1 {
				t.Fatalf("history not reread: %d %v", n, err)
			}
			var n int
			if err := d.DB.QueryRow("SELECT count(*) FROM version_rollup").Scan(&n); err != nil || n == 0 {
				t.Fatalf("missing rebuilt history: %d %v", n, err)
			}
		})
	}
}

type annotationCountingReader struct {
	queryrows.Queryer
	engine *annotationCountingEngine
}

func (r annotationCountingReader) QueryContext(ctx context.Context, q string, args ...any) (queryrows.Rows, error) {
	r.engine.calls++
	if strings.Contains(q, "FROM spans") || strings.Contains(q, "FROM logs") || strings.Contains(q, "FROM metrics") {
		r.engine.raw++
	}
	return r.Queryer.QueryContext(ctx, q, args...)
}
func (d *annotationCountingEngine) WithReadTransaction(ctx context.Context, read func(queryrows.Queryer) error) error {
	return d.Duck.WithReadTransaction(ctx, func(db queryrows.Queryer) error { return read(annotationCountingReader{Queryer: db, engine: d}) })
}
