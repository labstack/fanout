package observability

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func newTestRepository(t *testing.T) *telemetrystore.Repository {
	t.Helper()
	repository, err := telemetrystore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open telemetry repository: %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return repository
}

func newMockService(t *testing.T) (*Service, sqlmock.Sqlmock, *telemetrystore.Repository) {
	return newMockServiceWithRetention(t, 30)
}

func newMockServiceWithRetention(t *testing.T, retentionDays int) (*Service, sqlmock.Sqlmock, *telemetrystore.Repository) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := newTestRepository(t)
	svc := New(SQLDB(db), repository.Parquet, retentionDays)
	svc.now = func() time.Time { return time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC) }
	return svc, mock, repository
}

func TestNormalizeScopeDefaultsAndBounds(t *testing.T) {
	svc, _, _ := newMockService(t)
	scope, err := svc.normalizeScope(Scope{})
	if err != nil {
		t.Fatalf("normalizeScope: %v", err)
	}
	if scope.Namespace != "" {
		t.Fatalf("namespace = %q, want all namespaces", scope.Namespace)
	}
	if got := scope.End.Sub(scope.Start); got != time.Hour {
		t.Fatalf("window = %s, want 1h", got)
	}

	if _, err = svc.normalizeScope(Scope{Start: scope.End.Add(-25 * time.Hour), End: scope.End}); err != nil {
		t.Fatalf("25h scope rejected: %v", err)
	}
	if _, err = svc.normalizeScope(Scope{Start: scope.End.Add(-30 * 24 * time.Hour), End: scope.End}); err != nil {
		t.Fatalf("30d scope rejected: %v", err)
	}
	_, err = svc.normalizeScope(Scope{Start: scope.End.Add(-30*24*time.Hour - time.Nanosecond), End: scope.End})
	if !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("error = %v, want ErrInvalidScope", err)
	}
}

func TestUnlimitedRetentionKeepsFiniteQueryWindow(t *testing.T) {
	svc, _, _ := newMockServiceWithRetention(t, 0)
	if svc.maxWindow != defaultMaxWindow {
		t.Fatalf("maxWindow = %s, want %s", svc.maxWindow, defaultMaxWindow)
	}
}

func TestTimelineBucketWidth(t *testing.T) {
	tests := []struct {
		window time.Duration
		want   string
	}{
		{6 * time.Hour, "5 minutes"},
		{12 * time.Hour, "15 minutes"},
		// A day at five minutes is 288 points on one card; the line stops being
		// a shape and becomes a band.
		{24 * time.Hour, "15 minutes"},
		{48 * time.Hour, "30 minutes"},
		{7 * 24 * time.Hour, "30 minutes"},
		{30 * 24 * time.Hour, "4 hours"},
		{31 * 24 * time.Hour, "1 day"},
	}
	for _, tt := range tests {
		if got := timelineBucketWidth(tt.window); got != tt.want {
			t.Errorf("timelineBucketWidth(%s) = %q, want %q", tt.window, got, tt.want)
		}
	}
}

func TestTimelineQueriesUseAdaptiveBucket(t *testing.T) {
	for name, query := range map[string]string{} {
		if !strings.Contains(query, "INTERVAL '4 hours'") {
			t.Errorf("%s query does not use the 30-day bucket: %s", name, query)
		}
	}
}

func TestOverviewReturnsCanonicalEnvelope(t *testing.T) {
	svc, mock, _ := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	rows := sqlmock.NewRows([]string{"service", "spans", "error_rate", "p50_ms", "p95_ms", "log_count", "metric_count"}).
		AddRow("checkout", int64(1000), 0.08, 80.0, 2200.0, int64(20), int64(10)).
		AddRow("catalog", int64(500), 0.0, 25.0, 100.0, int64(4), int64(2))
	mock.ExpectQuery(regexp.QuoteMeta(overviewQuery)).
		WithArgs(start, end, "prod", "prod", "", "", 100).
		WillReturnRows(rows)

	result, err := svc.Overview(context.Background(), Scope{Namespace: "prod", Start: start, End: end}, 0)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if result.Schema != OverviewSchema {
		t.Fatalf("unexpected envelope: %#v", result)
	}
	if result.Data.Health != HealthUnhealthy || result.Data.Counts.Unhealthy != 1 || result.Data.Counts.Healthy != 1 {
		t.Fatalf("unexpected health: %#v", result.Data)
	}
	if result.Data.TotalSpans != 1500 {
		t.Fatalf("total spans = %d, want 1500", result.Data.TotalSpans)
	}
	if result.Provenance.Window == "" || !result.Provenance.Complete {
		t.Fatalf("missing provenance: %#v", result.Provenance)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOverviewReportsEmptyWindowAsUnknownNotHealthy(t *testing.T) {
	svc, mock, _ := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta(overviewQuery)).
		WithArgs(start, end, "nonexistent", "nonexistent", "", "", 100).
		WillReturnRows(sqlmock.NewRows([]string{"service", "spans", "error_rate", "p50_ms", "p95_ms", "log_count", "metric_count"}))

	result, err := svc.Overview(context.Background(), Scope{Namespace: "nonexistent", Start: start, End: end}, 0)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if result.Data.Health != HealthUnknown {
		t.Fatalf("health = %q, want %q: an empty window is not a clean bill of health", result.Data.Health, HealthUnknown)
	}
	if result.Data.ServiceCount != 0 || result.Data.Counts.Healthy != 0 {
		t.Fatalf("unexpected counts: %#v", result.Data)
	}
	if !strings.Contains(result.Summary, "no services reported") {
		t.Fatalf("summary = %q, want it to state the window is empty", result.Summary)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTopologyUsesSharedNodesAndTypedEdges(t *testing.T) {
	svc, mock, _ := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta(overviewQuery)).
		WithArgs(start, end, "prod", "prod", "", "", 50).
		WillReturnRows(sqlmock.NewRows([]string{"service", "spans", "error_rate", "p50_ms", "p95_ms", "log_count", "metric_count"}).
			AddRow("checkout", int64(10), 0.0, 20.0, 40.0, int64(1), int64(1)).
			AddRow("postgres", int64(10), 0.0, 10.0, 20.0, int64(0), int64(0)))
	mock.ExpectQuery(regexp.QuoteMeta(topologyEdgesQuery)).
		WithArgs(start, end, "prod", "prod", "", "", "", 50).
		WillReturnRows(sqlmock.NewRows([]string{"caller", "callee", "edge_type", "calls", "average_ms", "error_rate"}).
			AddRow("checkout", "postgres", "call", int64(10), 12.5, 0.0))

	result, err := svc.Topology(context.Background(), Scope{Namespace: "prod", Start: start, End: end}, 50)
	if err != nil {
		t.Fatalf("Topology: %v", err)
	}
	if len(result.Data.Nodes) != 2 || len(result.Data.Edges) != 1 {
		t.Fatalf("unexpected topology: %#v", result.Data)
	}
	if result.Data.Edges[0].Callee != "postgres" {
		t.Fatalf("unexpected edge/envelope: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTraceSelectsRecentErrorAndCorrelatesLogs(t *testing.T) {
	svc, mock, repository := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta(recentTraceQuery)).
		WithArgs(start, end, "prod", "prod", "checkout", "checkout").
		WillReturnRows(sqlmock.NewRows([]string{"trace_id"}).AddRow("trace-1"))
	if err := repository.Commit(context.Background(), telemetrystore.Batch{ID: "trace-fixture", Spans: []telemetry.Span{
		{Namespace: "prod", TraceID: "trace-1", SpanID: "root", ServiceName: "checkout", Name: "POST /pay", Kind: "SERVER", StartUnixNanos: start.UnixNano(), DurationMS: 200, StatusCode: "ERROR", StatusMsg: "declined"},
		{Namespace: "prod", TraceID: "trace-1", SpanID: "child", ParentSpanID: "root", ServiceName: "payments", Name: "charge", Kind: "CLIENT", StartUnixNanos: start.Add(20 * time.Millisecond).UnixNano(), DurationMS: 80, StatusCode: "OK"},
	}, Logs: []telemetry.Log{
		{Namespace: "prod", TimeUnixNanos: start.Add(150 * time.Millisecond).UnixNano(), Severity: "ERROR", ServiceName: "checkout", Body: "payment declined", TraceID: "trace-1", SpanID: "root"},
		{Namespace: "prod", TimeUnixNanos: start.Add(160 * time.Millisecond).UnixNano(), Severity: "ERROR", ServiceName: "payments", Body: `charge failed: token=abc123 {"client_secret":"cs_live_9"}`, TraceID: "trace-1", SpanID: "child"},
	}}); err != nil {
		t.Fatalf("commit trace fixture: %v", err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(traceLogsQuery)).
		WithArgs("trace-1", start, end, "prod", "prod", 20).
		WillReturnRows(sqlmock.NewRows([]string{"time", "severity", "service", "body", "trace_id", "span_id"}).
			AddRow(start.Add(150*time.Millisecond), "ERROR", "checkout", "payment declined", "trace-1", "root").
			AddRow(start.Add(160*time.Millisecond), "ERROR", "payments", `charge failed: token=abc123 {"client_secret":"cs_live_9"}`, "trace-1", "child"))

	result, err := svc.Trace(context.Background(), Scope{Namespace: "prod", Start: start, End: end}, "", "checkout", 20)
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}
	if result.Schema != TraceSchema || !result.Data.HasError || len(result.Data.Spans) != 2 || len(result.Data.Services) != 2 || len(result.Data.Logs) != 2 {
		t.Fatalf("unexpected trace detail: %#v", result)
	}
	if want := `charge failed: token=[REDACTED] {"client_secret":"[REDACTED]"}`; result.Data.Logs[1].Body != want {
		t.Fatalf("trace log body = %q, want %q (redaction bypassed)", result.Data.Logs[1].Body, want)
	}
	if result.Data.DurationMS != 200 {
		t.Fatalf("duration = %v, want 200ms", result.Data.DurationMS)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTraceLogsUseFullScopeEventTimeAcrossBatches(t *testing.T) {
	svc, mock, repository := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	batches := []telemetrystore.Batch{
		{ID: "trace-latest", Spans: []telemetry.Span{{Namespace: "prod", TraceID: "trace-order", SpanID: "root", StartUnixNanos: start.UnixNano(), DurationMS: 1}}, Logs: []telemetry.Log{{Namespace: "prod", TraceID: "trace-order", TimeUnixNanos: start.Add(30 * time.Millisecond).UnixNano(), Body: "latest"}}},
		{ID: "trace-earliest", Logs: []telemetry.Log{{Namespace: "prod", TraceID: "trace-order", TimeUnixNanos: start.Add(10 * time.Millisecond).UnixNano(), Body: "earliest"}}},
		{ID: "trace-middle", Logs: []telemetry.Log{{Namespace: "prod", TraceID: "trace-order", TimeUnixNanos: start.Add(20 * time.Millisecond).UnixNano(), Body: "middle"}}},
		{ID: "trace-outside-span", Logs: []telemetry.Log{{Namespace: "prod", TraceID: "trace-order", TimeUnixNanos: start.Add(2 * time.Minute).UnixNano(), Body: "unrelated later event"}}},
	}
	for _, batch := range batches {
		if err := repository.Commit(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
	}
	mock.ExpectQuery(regexp.QuoteMeta(traceLogsQuery)).
		WithArgs("trace-order", start, start.Add(time.Hour), "prod", "prod", 10).
		WillReturnRows(sqlmock.NewRows([]string{"time", "severity", "service", "body", "trace_id", "span_id"}).
			AddRow(start.Add(10*time.Millisecond), "", "", "earliest", "trace-order", "").
			AddRow(start.Add(20*time.Millisecond), "", "", "middle", "trace-order", "").
			AddRow(start.Add(30*time.Millisecond), "", "", "latest", "trace-order", "").
			AddRow(start.Add(2*time.Minute), "", "", "unrelated later event", "trace-order", ""))
	result, err := svc.Trace(context.Background(), Scope{Namespace: "prod", Start: start, End: start.Add(time.Hour)}, "trace-order", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Data.Logs) != 4 || result.Data.Logs[0].Body != "earliest" || result.Data.Logs[3].Body != "unrelated later event" {
		t.Fatalf("trace logs = %#v", result.Data.Logs)
	}
}

func TestTraceUsesIndexedParquet(t *testing.T) {
	svc, mock, repository := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	if err := repository.Commit(context.Background(), telemetrystore.Batch{ID: "indexed-trace", Spans: []telemetry.Span{{
		Namespace: "prod", TraceID: "parquet-trace", SpanID: "root", ServiceName: "checkout", Name: "pay",
		Kind: "SERVER", StartUnixNanos: start.UnixNano(), DurationMS: 25, StatusCode: "ERROR", StatusMsg: "declined",
	}}}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(traceLogsQuery)).
		WithArgs("parquet-trace", start, end, "prod", "prod", 10).
		WillReturnRows(sqlmock.NewRows([]string{"time", "severity", "service", "body", "trace_id", "span_id"}).
			AddRow(start.Add(time.Millisecond), "ERROR", "checkout", "token=secret", "parquet-trace", "root"))
	result, err := svc.Trace(context.Background(), Scope{Namespace: "prod", Start: start, End: end}, "parquet-trace", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Data.Spans) != 1 || len(result.Data.Logs) != 1 || !result.Data.HasError {
		t.Fatalf("indexed Parquet trace detail = %#v", result.Data)
	}
	if result.Provenance.DataSource != "parquet_index" {
		t.Fatalf("trace data source = %q", result.Provenance.DataSource)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTraceCombinesIndexedSpansAcrossBatches(t *testing.T) {
	svc, mock, repository := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	if err := repository.Commit(context.Background(), telemetrystore.Batch{ID: "trace-old-root", Spans: []telemetry.Span{{
		Namespace: "prod", TraceID: "split-trace", SpanID: "root", ServiceName: "frontend",
		StartUnixNanos: start.Add(10 * time.Minute).UnixNano(), DurationMS: 100, StatusCode: "ERROR",
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Commit(context.Background(), telemetrystore.Batch{ID: "trace-newer-child", Spans: []telemetry.Span{{
		Namespace: "prod", TraceID: "split-trace", SpanID: "child", ParentSpanID: "root", ServiceName: "backend",
		StartUnixNanos: start.Add(50 * time.Minute).UnixNano(), DurationMS: 25, StatusCode: "OK",
	}}}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(traceLogsQuery)).
		WithArgs("split-trace", start, end, "prod", "prod", 10).
		WillReturnRows(sqlmock.NewRows([]string{"time", "severity", "service", "body", "trace_id", "span_id"}))
	result, err := svc.Trace(context.Background(), Scope{Namespace: "prod", Start: start, End: end}, "split-trace", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Data.Spans) != 2 || !result.Data.HasError || len(result.Data.Services) != 2 || result.Provenance.DataSource != "parquet_index" {
		t.Fatalf("multi-batch trace = %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTraceReadsRecentRootFromParquetIndex(t *testing.T) {
	svc, mock, repository := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	cutoff := start.Add(30 * time.Minute)
	end := start.Add(time.Hour)
	if err := repository.Commit(context.Background(), telemetrystore.Batch{ID: "trace-after-rebuild", Spans: []telemetry.Span{{
		Namespace: "prod", TraceID: "new-trace", SpanID: "root", ServiceName: "frontend",
		StartUnixNanos: cutoff.Add(time.Minute).UnixNano(), DurationMS: 10, StatusCode: "OK",
	}}}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(traceLogsQuery)).
		WithArgs("new-trace", start, end, "prod", "prod", 10).
		WillReturnRows(sqlmock.NewRows([]string{"time", "severity", "service", "body", "trace_id", "span_id"}))
	result, err := svc.Trace(context.Background(), Scope{Namespace: "prod", Start: start, End: end}, "new-trace", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Data.Spans) != 1 || result.Data.Spans[0].SpanID != "root" || result.Provenance.DataSource != "parquet_index" {
		t.Fatalf("indexed trace = %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTraceFiltersIndexedSpansByNamespace(t *testing.T) {
	svc, mock, repository := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	cutoff := start.Add(30 * time.Minute)
	end := start.Add(time.Hour)
	if err := repository.Commit(context.Background(), telemetrystore.Batch{ID: "trace-cross-namespace", Spans: []telemetry.Span{
		{Namespace: "prod", TraceID: "shared-trace", SpanID: "child", ParentSpanID: "old-root", StartUnixNanos: cutoff.Add(time.Minute).UnixNano()},
		{Namespace: "staging", TraceID: "shared-trace", SpanID: "root", StartUnixNanos: cutoff.Add(2 * time.Minute).UnixNano()},
	}}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(traceLogsQuery)).
		WithArgs("shared-trace", start, end, "prod", "prod", 10).
		WillReturnRows(sqlmock.NewRows([]string{"time", "severity", "service", "body", "trace_id", "span_id"}))
	result, err := svc.Trace(context.Background(), Scope{Namespace: "prod", Start: start, End: end}, "shared-trace", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Data.Spans) != 1 || result.Data.Spans[0].SpanID != "child" || result.Provenance.DataSource != "parquet_index" {
		t.Fatalf("namespace-filtered trace = %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

var _ DB = queryrows.SQLAdapter{}
