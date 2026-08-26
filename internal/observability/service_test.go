package observability

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
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

func newMockService(t *testing.T) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := New(db, newTestRepository(t))
	svc.now = func() time.Time { return time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC) }
	return svc, mock
}

func TestNormalizeScopeDefaultsAndBounds(t *testing.T) {
	svc, _ := newMockService(t)
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

	_, err = svc.normalizeScope(Scope{Start: scope.End.Add(-25 * time.Hour), End: scope.End})
	if !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("error = %v, want ErrInvalidScope", err)
	}
}

func TestOverviewReturnsCanonicalEnvelope(t *testing.T) {
	svc, mock := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	rows := sqlmock.NewRows([]string{"service", "spans", "error_rate", "p50_ms", "p95_ms", "log_count", "metric_count"}).
		AddRow("checkout", int64(1000), 0.08, 80.0, 2200.0, int64(20), int64(10)).
		AddRow("catalog", int64(500), 0.0, 25.0, 100.0, int64(4), int64(2))
	mock.ExpectQuery(regexp.QuoteMeta(overviewQuery)).
		WithArgs(start, end, "prod", "prod", 100).
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

func TestTopologyUsesSharedNodesAndTypedEdges(t *testing.T) {
	svc, mock := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta(overviewQuery)).
		WithArgs(start, end, "prod", "prod", 50).
		WillReturnRows(sqlmock.NewRows([]string{"service", "spans", "error_rate", "p50_ms", "p95_ms", "log_count", "metric_count"}).
			AddRow("checkout", int64(10), 0.0, 20.0, 40.0, int64(1), int64(1)).
			AddRow("postgres", int64(10), 0.0, 10.0, 20.0, int64(0), int64(0)))
	mock.ExpectQuery(regexp.QuoteMeta(topologyEdgesQuery)).
		WithArgs(start, end, "prod", "prod", 50).
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

func TestPerformanceReturnsAllVisualizationDatasets(t *testing.T) {
	svc, mock := newMockService(t)
	svc.endpointMature.Store(true)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	midpoint := start.Add(30 * time.Minute)

	mock.ExpectQuery(regexp.QuoteMeta(performancePointsQuery)).
		WithArgs(start, end, "prod", "prod", "checkout", "checkout").
		WillReturnRows(sqlmock.NewRows([]string{"point_time", "spans", "error_rate", "p50_ms", "p95_ms", "log_count", "metric_count"}).
			AddRow(start, int64(120), 0.10, 80.0, 220.0, int64(30), int64(8)))
	mock.ExpectQuery(regexp.QuoteMeta(endpointRollupStatusQuery)).
		WillReturnRows(sqlmock.NewRows([]string{"ready", "watermark"}).AddRow(true, end.UnixNano()))
	mock.ExpectQuery(regexp.QuoteMeta(endpointRollupQuery)).
		WithArgs(start, end, end, "prod", "checkout", 25).
		WillReturnRows(sqlmock.NewRows([]string{"method", "path", "calls", "p50_ms", "p95_ms", "p99_ms", "error_rate"}).
			AddRow("GET", "/pay", int64(50), 75.0, 210.0, 350.0, 0.08))
	mock.ExpectQuery(regexp.QuoteMeta(performanceHeatmapQuery)).
		WithArgs(start, end, "prod", "prod", start, end, "prod", "prod").
		WillReturnRows(sqlmock.NewRows([]string{"point_time", "service", "p95_ms"}).
			AddRow(start, "checkout", 220.0))
	mock.ExpectQuery(regexp.QuoteMeta(performanceAggregateQuery)).
		WithArgs(start, midpoint, "prod", "prod", "checkout", "checkout").
		WillReturnRows(sqlmock.NewRows([]string{"spans", "error_rate", "p50_ms", "p95_ms"}).AddRow(50.0, 0.12, 90.0, 240.0))
	mock.ExpectQuery(regexp.QuoteMeta(performanceAggregateQuery)).
		WithArgs(midpoint, end, "prod", "prod", "checkout", "checkout").
		WillReturnRows(sqlmock.NewRows([]string{"spans", "error_rate", "p50_ms", "p95_ms"}).AddRow(70.0, 0.06, 70.0, 180.0))

	result, err := svc.Performance(context.Background(), Scope{Namespace: "prod", Start: start, End: end}, "checkout", 25)
	if err != nil {
		t.Fatalf("Performance: %v", err)
	}
	if result.Schema != PerformanceSchema || len(result.Data.Points) != 1 || len(result.Data.Endpoints) != 1 || len(result.Data.Heatmap) != 1 || len(result.Data.Comparison) != 4 {
		t.Fatalf("incomplete performance result: %#v", result)
	}
	if result.Data.Endpoints[0].Health != HealthUnhealthy {
		t.Fatalf("endpoint health = %q, want unhealthy", result.Data.Endpoints[0].Health)
	}
	if result.Data.Comparison[1].Direction != "improvement" {
		t.Fatalf("error-rate comparison = %#v", result.Data.Comparison[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestQueryEndpointsFallsBackToRawUntilBackfillReady(t *testing.T) {
	svc, mock := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	mock.ExpectQuery(regexp.QuoteMeta(endpointRollupStatusQuery)).
		WillReturnRows(sqlmock.NewRows([]string{"ready", "watermark"}).AddRow(false, int64(0)))
	mock.ExpectQuery(regexp.QuoteMeta(rawEndpointsQuery)).
		WithArgs(start, end, "prod", "prod", "checkout", "checkout", 25).
		WillReturnRows(sqlmock.NewRows([]string{"method", "path", "calls", "p50_ms", "p95_ms", "p99_ms", "error_rate"}).
			AddRow("GET", "/pay", int64(2), 10.0, 20.0, 25.0, 0.0))

	endpoints, source, err := svc.queryEndpoints(context.Background(), Scope{Namespace: "prod", Start: start, End: end}, "checkout", 25)
	if err != nil {
		t.Fatalf("queryEndpoints: %v", err)
	}
	if source != "spans" || len(endpoints) != 1 {
		t.Fatalf("queryEndpoints = (%#v, %q), want one raw endpoint", endpoints, source)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestQueryEndpointsFallsBackToRawWhenRollupProbeFails(t *testing.T) {
	svc, mock := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	mock.ExpectQuery(regexp.QuoteMeta(endpointRollupStatusQuery)).
		WillReturnError(errors.New("rollup state temporarily unavailable"))
	mock.ExpectQuery(regexp.QuoteMeta(rawEndpointsQuery)).
		WithArgs(start, end, "prod", "prod", "checkout", "checkout", 25).
		WillReturnRows(sqlmock.NewRows([]string{"method", "path", "calls", "p50_ms", "p95_ms", "p99_ms", "error_rate"}).
			AddRow("GET", "/pay", int64(2), 10.0, 20.0, 25.0, 0.0))

	endpoints, source, err := svc.queryEndpoints(context.Background(), Scope{Namespace: "prod", Start: start, End: end}, "checkout", 25)
	if err != nil {
		t.Fatalf("queryEndpoints: %v", err)
	}
	if source != "spans" || len(endpoints) != 1 {
		t.Fatalf("queryEndpoints = (%#v, %q), want one raw endpoint", endpoints, source)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTraceSelectsRecentErrorAndCorrelatesLogs(t *testing.T) {
	svc, mock := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta(recentTraceQuery)).
		WithArgs(start, end, "prod", "prod", "checkout", "checkout").
		WillReturnRows(sqlmock.NewRows([]string{"trace_id"}).AddRow("trace-1"))
	if err := svc.repository.Commit(telemetrystore.Batch{ID: "trace-fixture", Spans: []telemetry.Span{
		{Namespace: "prod", TraceID: "trace-1", SpanID: "root", ServiceName: "checkout", Name: "POST /pay", Kind: "SERVER", StartUnixNanos: start.UnixNano(), DurationMS: 200, StatusCode: "ERROR", StatusMsg: "declined"},
		{Namespace: "prod", TraceID: "trace-1", SpanID: "child", ParentSpanID: "root", ServiceName: "payments", Name: "charge", Kind: "CLIENT", StartUnixNanos: start.Add(20 * time.Millisecond).UnixNano(), DurationMS: 80, StatusCode: "OK"},
	}, Logs: []telemetry.Log{
		{Namespace: "prod", TimeUnixNanos: start.Add(150 * time.Millisecond).UnixNano(), Severity: "ERROR", ServiceName: "checkout", Body: "payment declined", TraceID: "trace-1", SpanID: "root"},
		{Namespace: "prod", TimeUnixNanos: start.Add(160 * time.Millisecond).UnixNano(), Severity: "ERROR", ServiceName: "payments", Body: `charge failed: token=abc123 {"client_secret":"cs_live_9"}`, TraceID: "trace-1", SpanID: "child"},
	}}); err != nil {
		t.Fatalf("commit trace fixture: %v", err)
	}

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

func TestLogsAppliesFiltersAndBuildsHistogram(t *testing.T) {
	svc, mock := newMockService(t)
	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	if err := svc.repository.Commit(telemetrystore.Batch{ID: "logs-fixture", Logs: []telemetry.Log{
		{Namespace: "prod", TimeUnixNanos: start.UnixNano(), Severity: "ERROR", ServiceName: "checkout", Body: "payment declined", TraceID: "trace-1", SpanID: "root"},
		{Namespace: "prod", TimeUnixNanos: start.Add(time.Millisecond).UnixNano(), Severity: "ERROR", ServiceName: "checkout", Body: "card declined: token=abc123", TraceID: "trace-2", SpanID: "root2"},
		{Namespace: "prod", TimeUnixNanos: start.Add(2 * time.Millisecond).UnixNano(), Severity: "ERROR", ServiceName: "checkout", Body: `auth declined: {"password":"hunter2"}`, TraceID: "trace-3", SpanID: "root3"},
	}}); err != nil {
		t.Fatalf("commit logs fixture: %v", err)
	}

	result, err := svc.Logs(context.Background(), Scope{Namespace: "prod", Start: start, End: end}, "checkout", "error", "declined", 10)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if result.Schema != LogsSchema || len(result.Data.Entries) != 3 || len(result.Data.Buckets) != 1 || result.Data.Buckets[0].Count != 3 {
		t.Fatalf("unexpected logs result: %#v", result)
	}
	if want := "card declined: token=[REDACTED]"; result.Data.Entries[1].Body != want {
		t.Fatalf("log body = %q, want %q (redaction bypassed)", result.Data.Entries[1].Body, want)
	}
	if want := `auth declined: {"password":"[REDACTED]"}`; result.Data.Entries[0].Body != want {
		t.Fatalf("log body = %q, want %q (redaction bypassed)", result.Data.Entries[0].Body, want)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

var _ DB = (*sql.DB)(nil)
