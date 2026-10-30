package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestVersionPendingBudgetsAndSingleRetries(t *testing.T) {
	now := time.Now()
	mixed := func(id string, n int) telemetry.BatchMetadata {
		return telemetry.BatchMetadata{ID: id, Spans: n, Logs: n, Metrics: n}
	}
	for _, tc := range []struct {
		name     string
		batches  []telemetry.BatchMetadata
		failures map[string]versionRollupFailure
		want     []string
	}{
		{name: "exact row budget", batches: []telemetry.BatchMetadata{mixed("a", 300000), {ID: "b", Logs: 100000}, mixed("c", 1)}, want: []string{"a", "b"}},
		{name: "combined signals exceed rows", batches: []telemetry.BatchMetadata{mixed("a", 200000), mixed("b", 200000)}, want: []string{"a"}},
		{name: "oversized alone", batches: []telemetry.BatchMetadata{mixed("large", versionRollupRowBudget/3+1), mixed("new", 1)}, want: []string{"large"}},
		{name: "oversized waits for next pass", batches: []telemetry.BatchMetadata{mixed("old", 1), mixed("large", versionRollupRowBudget/3+1)}, want: []string{"old"}},
		{name: "failed group retries singly", batches: []telemetry.BatchMetadata{mixed("retry", 1), mixed("new", 1)}, failures: map[string]versionRollupFailure{"retry": {retrySingly: true}}, want: []string{"retry"}},
		{name: "retry waits for next pass", batches: []telemetry.BatchMetadata{mixed("old", 1), mixed("retry", 1)}, failures: map[string]versionRollupFailure{"retry": {retrySingly: true}}, want: []string{"old"}},
		{name: "backoff permits newer work", batches: []telemetry.BatchMetadata{mixed("retry", 1), mixed("new", 1)}, failures: map[string]versionRollupFailure{"retry": {nextAttempt: now.Add(time.Minute)}}, want: []string{"new"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pendingVersionBatches(tc.batches, nil, tc.failures, now)
			ids := make([]string, len(got))
			for i, b := range got {
				ids[i] = b.ID
			}
			if fmt.Sprint(ids) != fmt.Sprint(tc.want) {
				t.Fatalf("got %v want %v", ids, tc.want)
			}
		})
	}
	batches := make([]telemetry.BatchMetadata, 65)
	for i := range batches {
		batches[i] = mixed(fmt.Sprint(i), 1)
	}
	if got := pendingVersionBatches(batches, nil, nil, now); len(got) != 64 {
		t.Fatalf("batch cap: %d", len(got))
	}
}

func TestVersionMixedBacklogDrainsAndAllowsAnomalyPersistence(t *testing.T) {
	// Exercise the budget on the same eight-thread host shape as its sizing
	// measurement, rather than the two-thread/256MB small annotation fixture.
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "2GB", DuckDBThreads: 8, DuckDBMaxConns: 8, RetentionDays: 30}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDuck(t.Context(), cfg, repo)
	if err != nil {
		_ = repo.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(); _ = repo.Close() })
	const total = 60
	// 4.95m rows in 180 real Parquet files. Each full pass admits 12
	// batches (990k rows), preserving all signals and complete acknowledgements.
	for i := range total {
		size := 5000
		if i%2 == 1 {
			size = 50000
		}
		commitMixedVersionBatch(t, repo, fmt.Sprintf("mixed-%03d", i), size)
	}
	for pass, want := range []int64{12, 12, 12, 12, 12, 0} {
		if n, err := d.RefreshVersionRollup(t.Context()); err != nil || n != want {
			t.Fatalf("pass %d: batches=%d want=%d err=%v", pass, n, want, err)
		}
	}
	var marked, limited int
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches`).Scan(&marked); err != nil || marked != total {
		t.Fatalf("markers=%d err=%v", marked, err)
	}
	if err := d.DB.QueryRow(`SELECT last_ingested_unix_nano FROM rollup_state WHERE cache_key='version_rollup_v1_limited'`).Scan(&limited); err != nil || limited != 0 {
		t.Fatalf("limited=%d err=%v", limited, err)
	}
	var versions int
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup`).Scan(&versions); err != nil || versions != 120 {
		t.Fatalf("versions=%d err=%v", versions, err)
	}
	// Requeue the real backlog while publication blocks admission. Anomalies
	// must persist before publication releases, and the loop must retry later.
	if _, err := d.writer().Exec(`DELETE FROM version_rollup_batches`); err != nil {
		t.Fatal(err)
	}
	if err := d.parquetMu.LockContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			d.parquetMu.Unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	done := make(chan struct{})
	go func() { defer close(done); d.runVersionRollupLoop(ctx) }()
	defer func() { cancel(); <-done }()
	if n, err := d.RefreshVersionRollup(ctx); err != nil || n != 0 {
		t.Fatalf("publication skip=%d %v", n, err)
	}
	now := time.Now().UTC()
	finding := annotations.Anomaly{Namespace: "namespace-0", Service: "service-0", Kind: "volume", From: now.Add(-time.Minute), To: now, Title: "Low volume", Severity: "warn"}
	persisted := make(chan error, 1)
	go func() { persisted <- d.RecordAnomalies(ctx, []annotations.Anomaly{finding}, now) }()
	start := time.Now()
	select {
	case err := <-persisted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("anomaly persistence starved behind busy version loop")
	}
	d.parquetMu.Unlock()
	locked = false
	t.Logf("anomaly persisted during blocked Parquet admission in %v", time.Since(start))
	var count int
	if err := d.DB.QueryRow(`SELECT count(*) FROM anomaly_log WHERE title='Low volume'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("persisted=%d err=%v", count, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches`).Scan(&marked); err != nil {
			t.Fatal(err)
		}
		if marked > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("version loop did not retry after publication")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func commitMixedVersionBatch(t testing.TB, repo *telemetrystore.Repository, id string, size int) {
	t.Helper()
	at := time.Now().UTC().Add(-time.Hour).UnixNano()
	batch := telemetrystore.Batch{ID: id, Spans: make([]telemetry.Span, size), Logs: make([]telemetry.Log, size), Metrics: make([]telemetry.Metric, size)}
	for i := range size {
		ns := fmt.Sprintf("namespace-%d", i%3)
		svc := fmt.Sprintf("service-%d", i%24)
		version := fmt.Sprintf("v%d", i%5)
		resource := map[string]any{"service.version": version, "deployment.environment": "production", "host.name": fmt.Sprintf("host-%d", i%31), "count": int64(i), "enabled": true}
		attrs := map[string]any{"http.method": "GET", "http.status_code": int64(200), "http.route": "/api/items"}
		stamp := at + int64(i)*1000
		batch.Spans[i] = telemetry.Span{Namespace: ns, ServiceName: svc, ServiceVersion: version, TraceID: fmt.Sprintf("%s-%d", id, i/2), SpanID: fmt.Sprint(i), Name: "GET /api/items", Kind: "SPAN_KIND_SERVER", StartUnixNanos: stamp, EndUnixNanos: stamp + 1000000, DurationMS: 1, StatusCode: "STATUS_CODE_OK", Resource: resource, Attributes: attrs}
		batch.Logs[i] = telemetry.Log{Namespace: ns, ServiceName: svc, EventUnixNanos: stamp, Severity: "INFO", Body: fmt.Sprintf("request %d completed successfully", i), Resource: resource, Attributes: attrs}
		batch.Metrics[i] = telemetry.Metric{Namespace: ns, ServiceName: svc, EventUnixNanos: stamp, Name: "http.requests", Type: "sum", Value: float64(i), Resource: resource, Attributes: attrs}
	}
	if err := repo.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
}
