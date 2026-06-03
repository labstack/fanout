package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/fanout/internal/query"
)

// errTest is a sentinel error for testing.
var errTest = fmt.Errorf("mock query error")

// --- Overview basics: compact mode (MCP-style, health/services/issues) ---

func TestOverview_NoData(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}))

	result, err := svc.Overview(context.Background(), OverviewParams{Window: 15})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if result == nil {
		t.Fatal("Overview() returned nil")
	}
	if result.Health == nil {
		t.Fatal("Health should be populated by default")
	}
	if result.Health.TotalServices != 0 {
		t.Errorf("TotalServices = %d, want 0", result.Health.TotalServices)
	}
	if result.Health.Score != 0.0 {
		t.Errorf("Score = %v, want 0.0", result.Health.Score)
	}
}

func TestOverview_HealthScore(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("svc-a", int64(1000), 10.0, 100.0, 0.001).
		AddRow("svc-b", int64(500), 50.0, 800.0, 0.07)

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Overview(context.Background(), OverviewParams{Window: 15})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}

	if result.Health.TotalServices != 2 {
		t.Errorf("TotalServices = %d, want 2", result.Health.TotalServices)
	}

	// svc-a score: 1.0
	// svc-b score: 0.3*0.4 + 0.7*0.3 + 1.0*0.3 = 0.63
	wantGlobal := (1.0 + 0.63) / 2
	if abs(result.Health.Score-wantGlobal) > 1e-9 {
		t.Errorf("Health.Score = %v, want ~%v", result.Health.Score, wantGlobal)
	}

	if len(result.Services) != 2 {
		t.Fatalf("Services count = %d, want 2", len(result.Services))
	}
}

func TestOverview_ByStatus(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("healthy-svc", int64(1000), 10.0, 100.0, 0.001).
		AddRow("degraded-svc", int64(500), 50.0, 800.0, 0.02).
		AddRow("unhealthy-svc", int64(100), 200.0, 100.0, 0.15)

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Overview(context.Background(), OverviewParams{Window: 15})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}

	if result.Health.ByStatus["healthy"] != 1 {
		t.Errorf("ByStatus[healthy] = %d, want 1", result.Health.ByStatus["healthy"])
	}
	if result.Health.ByStatus["degraded"] != 1 {
		t.Errorf("ByStatus[degraded] = %d, want 1", result.Health.ByStatus["degraded"])
	}
	if result.Health.ByStatus["unhealthy"] != 1 {
		t.Errorf("ByStatus[unhealthy] = %d, want 1", result.Health.ByStatus["unhealthy"])
	}
}

func TestOverview_IncludeFilter(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("svc-a", int64(1000), 10.0, 100.0, 0.001)

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Overview(context.Background(), OverviewParams{
		Window:  15,
		Include: []string{"health"},
	})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}

	if result.Health == nil || result.Health.TotalServices != 1 {
		t.Errorf("Health.TotalServices = unexpected, got %+v", result.Health)
	}
	if len(result.Services) != 0 {
		t.Errorf("Services count = %d, want 0 (not included)", len(result.Services))
	}
	if len(result.Issues) != 0 {
		t.Errorf("Issues count = %d, want 0 (not included)", len(result.Issues))
	}
}

func TestOverview_SortBySeverity(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("good-svc", int64(1000), 10.0, 100.0, 0.001).
		AddRow("bad-svc", int64(100), 200.0, 100.0, 0.15)

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Overview(context.Background(), OverviewParams{
		Window: 15,
		SortBy: "severity",
	})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}

	if len(result.Services) < 2 {
		t.Fatalf("Services count = %d, want 2", len(result.Services))
	}
	if result.Services[0].Service != "bad-svc" {
		t.Errorf("first service = %q, want %q (worst first)", result.Services[0].Service, "bad-svc")
	}
}

func TestOverview_SortByErrorRate(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("low-err", int64(1000), 10.0, 100.0, 0.001).
		AddRow("high-err", int64(1000), 10.0, 100.0, 0.15)

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Overview(context.Background(), OverviewParams{
		Window: 15,
		SortBy: "error_rate",
	})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}

	if len(result.Services) < 2 {
		t.Fatalf("Services count = %d, want 2", len(result.Services))
	}
	if result.Services[0].Service != "high-err" {
		t.Errorf("first service = %q, want %q (highest error rate first)", result.Services[0].Service, "high-err")
	}
}

func TestOverview_TopIssues(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("high-error-svc", int64(100), 10.0, 100.0, 0.10).
		AddRow("slow-svc", int64(100), 100.0, 800.0, 0.001)

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Overview(context.Background(), OverviewParams{Window: 15})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}

	if len(result.Issues) != 2 {
		t.Errorf("Issues count = %d, want 2", len(result.Issues))
	}

	var foundErr, foundLat bool
	for _, issue := range result.Issues {
		if issue.Issue == "high_error_rate" {
			foundErr = true
			if issue.Service != "high-error-svc" {
				t.Errorf("high_error_rate issue service = %q, want %q", issue.Service, "high-error-svc")
			}
			if issue.Threshold != 0.05 {
				t.Errorf("high_error_rate threshold = %v, want 0.05", issue.Threshold)
			}
		}
		if issue.Issue == "p95_latency" {
			foundLat = true
			if issue.Service != "slow-svc" {
				t.Errorf("p95_latency issue service = %q, want %q", issue.Service, "slow-svc")
			}
			if issue.Threshold != 500 {
				t.Errorf("p95_latency threshold = %v, want 500", issue.Threshold)
			}
		}
	}
	if !foundErr {
		t.Error("expected high_error_rate issue")
	}
	if !foundLat {
		t.Error("expected p95_latency issue")
	}
}

func TestOverview_DefaultWindow(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}))

	result, err := svc.Overview(context.Background(), OverviewParams{Window: 0})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if result == nil {
		t.Fatal("Overview() returned nil")
	}
}

func TestOverview_QueryError(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnError(errTest)

	_, err := svc.Overview(context.Background(), OverviewParams{Window: 15})
	if err == nil {
		t.Fatal("Overview() should return error on query failure")
	}
}

// --- Overview with incidents: rich UI mode (sparklines + top errors + lifecycle) ---

func incidentsIncludeParams(window int, tracker *IncidentTracker) OverviewParams {
	return OverviewParams{
		Window:  window,
		Include: []string{"health", "services", "sparklines", "incidents"},
		Limit:   200,
		Tracker: tracker,
	}
}

func TestOverview_IncidentsWithServicesAndSummary(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	tracker := NewIncidentTracker()

	// Pre-tick the tracker so the degraded service has an open incident.
	tracker.Tick("svc-degraded", "degraded", 0.05, 1500.0, time.Now().Add(-2*time.Minute))
	tracker.Tick("svc-degraded", "degraded", 0.05, 1500.0, time.Now())

	// Main rollup query: 1 healthy + 1 degraded service.
	rollupRows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("svc-healthy", int64(1000), 10.0, 100.0, 0.001).
		AddRow("svc-degraded", int64(500), 50.0, 1500.0, 0.05)
	mock.ExpectQuery("SELECT").WillReturnRows(rollupRows)

	// Sparklines query (for all services).
	sparkRows := sqlmock.NewRows([]string{"service", "bucket", "spans", "error_rate"}).
		AddRow("svc-healthy", time.Now().Add(-2*time.Minute), int64(100), 0.001).
		AddRow("svc-healthy", time.Now().Add(-1*time.Minute), int64(110), 0.001).
		AddRow("svc-degraded", time.Now().Add(-2*time.Minute), int64(50), 0.05).
		AddRow("svc-degraded", time.Now().Add(-1*time.Minute), int64(60), 0.06)
	mock.ExpectQuery("SELECT").WillReturnRows(sparkRows)

	// Top errors query (for degraded services only).
	errRows := sqlmock.NewRows([]string{"service", "message", "cnt"}).
		AddRow("svc-degraded", "connection timeout", int64(42)).
		AddRow("svc-degraded", "bad gateway", int64(10))
	mock.ExpectQuery("SELECT").WillReturnRows(errRows)

	result, err := svc.Overview(context.Background(), incidentsIncludeParams(60, tracker))
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if result == nil {
		t.Fatal("Overview() returned nil")
	}

	if result.Health.TotalServices != 2 {
		t.Errorf("Health.TotalServices = %d, want 2", result.Health.TotalServices)
	}
	if result.Health.ByStatus["healthy"] != 1 {
		t.Errorf("ByStatus[healthy] = %d, want 1", result.Health.ByStatus["healthy"])
	}
	if result.Health.ByStatus["degraded"] != 1 {
		t.Errorf("ByStatus[degraded] = %d, want 1", result.Health.ByStatus["degraded"])
	}
	if result.Health.ByStatus["unhealthy"] != 0 {
		t.Errorf("ByStatus[unhealthy] = %d, want 0", result.Health.ByStatus["unhealthy"])
	}

	if len(result.Incidents) != 1 {
		t.Fatalf("Incidents count = %d, want 1", len(result.Incidents))
	}
	inc := result.Incidents[0]
	if inc.Service != "svc-degraded" {
		t.Errorf("Incident.Service = %q, want %q", inc.Service, "svc-degraded")
	}
	if inc.Status != "degraded" {
		t.Errorf("Incident.Status = %q, want %q", inc.Status, "degraded")
	}
	if len(inc.TopErrors) == 0 {
		t.Error("Incident.TopErrors should not be empty")
	} else if inc.TopErrors[0].Message != "connection timeout" {
		t.Errorf("TopErrors[0].Message = %q, want %q", inc.TopErrors[0].Message, "connection timeout")
	}

	// The services list still includes everything — consumers can filter by status.
	if len(result.Services) != 2 {
		t.Fatalf("Services count = %d, want 2 (all services, consumer filters)", len(result.Services))
	}

	if result.Health.ThroughputPerMin <= 0 {
		t.Errorf("ThroughputPerMin = %v, want > 0", result.Health.ThroughputPerMin)
	}

	// Verify sparklines are populated on the services list.
	var healthyTraffic []float64
	for _, s := range result.Services {
		if s.Service == "svc-healthy" {
			healthyTraffic = s.SparklineTraffic
		}
	}
	if len(healthyTraffic) == 0 {
		t.Error("svc-healthy SparklineTraffic should not be empty")
	}
	if len(inc.SparklineErrRate) == 0 {
		t.Error("incident SparklineErrRate should not be empty")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

// --- Activity & recent-errors sections (Home command center) ---

func TestOverview_ActivitySection(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Main rollup query (one service, so we don't early-return).
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
			AddRow("svc-a", int64(1000), 10.0, 100.0, 0.001))
	// Activity aggregate query (bucket, spans, error_rate). WithArgs pins the
	// namespace bind order/count — a swapped/dropped placeholder would leak.
	mock.ExpectQuery("SELECT").WithArgs("prod", "prod").WillReturnRows(
		sqlmock.NewRows([]string{"bucket", "spans", "error_rate"}).
			AddRow(time.Now().Add(-2*time.Minute), int64(200), 0.01).
			AddRow(time.Now().Add(-1*time.Minute), int64(220), 0.02))

	result, err := svc.Overview(context.Background(), OverviewParams{
		Window:    60,
		Namespace: "prod",
		Include:   []string{"health", "services", "activity"},
		Limit:     200,
	})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if result.Activity == nil {
		t.Fatal("Activity should be populated when requested")
	}
	if len(result.Activity.Buckets) != 2 {
		t.Fatalf("Activity.Buckets = %d, want 2", len(result.Activity.Buckets))
	}
	if result.Activity.Buckets[0].Spans != 200 {
		t.Errorf("Buckets[0].Spans = %d, want 200", result.Activity.Buckets[0].Spans)
	}
	if result.Activity.Buckets[1].ErrorRate != 0.02 {
		t.Errorf("Buckets[1].ErrorRate = %v, want 0.02", result.Activity.Buckets[1].ErrorRate)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestOverview_RecentErrorsSection(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Main rollup query — a service WITH errors so the gate opens.
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
			AddRow("svc-a", int64(1000), 10.0, 100.0, 0.08))
	// Global recent-errors query (service, message, cnt). WithArgs pins the
	// namespace bind order/count — a swapped/dropped placeholder would leak.
	mock.ExpectQuery("SELECT").WithArgs("prod", "prod").WillReturnRows(
		sqlmock.NewRows([]string{"service", "message", "cnt"}).
			AddRow("svc-a", "connection timeout", int64(120)).
			AddRow("svc-a", "bad gateway", int64(30)))

	result, err := svc.Overview(context.Background(), OverviewParams{
		Window:    60,
		Namespace: "prod",
		Include:   []string{"health", "services", "recent_errors"},
		Limit:     200,
	})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if len(result.RecentErrors) != 2 {
		t.Fatalf("RecentErrors = %d, want 2", len(result.RecentErrors))
	}
	if result.RecentErrors[0].Message != "connection timeout" || result.RecentErrors[0].Count != 120 {
		t.Errorf("RecentErrors[0] = %+v, want {svc-a, connection timeout, 120}", result.RecentErrors[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

// A healthy fleet (no service with errors) must skip the raw-spans scan
// entirely — only the main rollup query runs.
func TestOverview_RecentErrorsGatedWhenHealthy(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
			AddRow("svc-a", int64(1000), 10.0, 100.0, 0.0))
	// No recent-errors query expected.

	result, err := svc.Overview(context.Background(), OverviewParams{
		Window:  60,
		Include: []string{"health", "services", "recent_errors"},
		Limit:   200,
	})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if len(result.RecentErrors) != 0 {
		t.Errorf("RecentErrors = %d, want 0 (gated on healthy fleet)", len(result.RecentErrors))
	}
	if result.RecentErrors == nil {
		t.Error("RecentErrors should be non-nil empty when requested")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations (gate should skip spans scan): %v", err)
	}
}

// A failed recent-errors scan must surface as RecentErrorsUnavailable (not an
// empty feed that the UI would render as a false "no errors" all-clear).
func TestOverview_RecentErrorsUnavailableOnError(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
			AddRow("svc-a", int64(1000), 10.0, 100.0, 0.08))
	// Gate opens (errorRate > 0); the spans scan then fails.
	mock.ExpectQuery("SELECT").WillReturnError(errTest)

	result, err := svc.Overview(context.Background(), OverviewParams{
		Window:  60,
		Include: []string{"health", "services", "recent_errors"},
		Limit:   200,
	})
	if err != nil {
		t.Fatalf("Overview() error = %v (a failed best-effort section must not fail the request)", err)
	}
	if !result.RecentErrorsUnavailable {
		t.Error("RecentErrorsUnavailable = false, want true after scan error")
	}
	if len(result.RecentErrors) != 0 {
		t.Errorf("RecentErrors = %d, want 0 on failure", len(result.RecentErrors))
	}
}

// When the new sections aren't requested, no extra queries run and the fields
// stay nil.
func TestOverview_ActivityRecentErrorsNotIncluded(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
			AddRow("svc-a", int64(1000), 10.0, 100.0, 0.08))

	result, err := svc.Overview(context.Background(), OverviewParams{
		Window:  60,
		Include: []string{"health", "services"},
		Limit:   200,
	})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if result.Activity != nil {
		t.Errorf("Activity = %+v, want nil (not requested)", result.Activity)
	}
	if result.RecentErrors != nil {
		t.Errorf("RecentErrors = %+v, want nil (not requested)", result.RecentErrors)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

// A cached snapshot for an MCP-shaped call (no activity) must NOT satisfy a
// Home-shaped call (with activity) — the cache key distinguishes them.
func TestOverview_CacheSeparatesNewSections(t *testing.T) {
	cacheCtx, cancel := context.WithCancel(context.Background())
	query.InitQueryCache(cacheCtx)
	t.Cleanup(func() {
		cancel()
		query.QueryCache = nil
	})

	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Call 1: compact (no activity) — one query, gets cached.
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
			AddRow("svc-a", int64(1000), 10.0, 100.0, 0.001))
	// Call 2: with activity — must miss the cache and run BOTH its queries.
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
			AddRow("svc-a", int64(1000), 10.0, 100.0, 0.001))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"bucket", "spans", "error_rate"}).
			AddRow(time.Now(), int64(100), 0.0))

	compact := OverviewParams{Window: 77, Include: []string{"health", "services"}, Limit: 200}
	withActivity := OverviewParams{Window: 77, Include: []string{"health", "services", "activity"}, Limit: 200}

	if _, err := svc.Overview(context.Background(), compact); err != nil {
		t.Fatalf("compact Overview() error = %v", err)
	}
	result, err := svc.Overview(context.Background(), withActivity)
	if err != nil {
		t.Fatalf("activity Overview() error = %v", err)
	}
	if result.Activity == nil || len(result.Activity.Buckets) != 1 {
		t.Fatalf("Activity not populated on cache-separated call: %+v", result.Activity)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("cache key did not separate sections (activity call reused compact cache): %v", err)
	}
}

func TestOverview_IncidentsEmptyState(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	tracker := NewIncidentTracker()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}))

	result, err := svc.Overview(context.Background(), incidentsIncludeParams(60, tracker))
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if result == nil {
		t.Fatal("Overview() returned nil")
	}

	if result.Health == nil {
		t.Fatal("Health should be populated")
	}
	if result.Health.TotalServices != 0 {
		t.Errorf("TotalServices = %d, want 0", result.Health.TotalServices)
	}

	if result.Incidents == nil {
		t.Error("Incidents should be non-nil")
	}
	if len(result.Incidents) != 0 {
		t.Errorf("Incidents count = %d, want 0", len(result.Incidents))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestOverview_IncidentsUnhealthyService(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rollupRows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("bad-svc", int64(100), 50.0, 200.0, 0.15)
	mock.ExpectQuery("SELECT").WillReturnRows(rollupRows)

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "bucket", "spans", "error_rate"}))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "message", "cnt"}))

	result, err := svc.Overview(context.Background(), incidentsIncludeParams(60, nil))
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}

	if result.Health.ByStatus["unhealthy"] != 1 {
		t.Errorf("ByStatus[unhealthy] = %d, want 1", result.Health.ByStatus["unhealthy"])
	}
	if len(result.Incidents) != 1 {
		t.Fatalf("Incidents count = %d, want 1", len(result.Incidents))
	}
	if result.Incidents[0].Status != "unhealthy" {
		t.Errorf("Incident.Status = %q, want %q", result.Incidents[0].Status, "unhealthy")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestOverview_IncidentsSortedWorstFirst(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rollupRows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("svc-bad", int64(100), 50.0, 200.0, 0.15).   // score 0.6
		AddRow("svc-worse", int64(100), 50.0, 6000.0, 0.15) // score 0.3
	mock.ExpectQuery("SELECT").WillReturnRows(rollupRows)

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "bucket", "spans", "error_rate"}))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "message", "cnt"}))

	result, err := svc.Overview(context.Background(), incidentsIncludeParams(60, nil))
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}

	if len(result.Incidents) != 2 {
		t.Fatalf("Incidents count = %d, want 2", len(result.Incidents))
	}

	if result.Incidents[0].Service != "svc-worse" {
		t.Errorf("Incidents[0].Service = %q, want %q (worst first)", result.Incidents[0].Service, "svc-worse")
	}
	if result.Incidents[1].Service != "svc-bad" {
		t.Errorf("Incidents[1].Service = %q, want %q", result.Incidents[1].Service, "svc-bad")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestOverview_IncidentsNilTracker(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rollupRows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("svc-a", int64(200), 10.0, 80.0, 0.001)
	mock.ExpectQuery("SELECT").WillReturnRows(rollupRows)
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "bucket", "spans", "error_rate"}))

	result, err := svc.Overview(context.Background(), incidentsIncludeParams(60, nil))
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if result == nil {
		t.Fatal("Overview() returned nil")
	}
	if result.Health.TotalServices != 1 {
		t.Errorf("TotalServices = %d, want 1", result.Health.TotalServices)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestOverview_UsesCachedSnapshot(t *testing.T) {
	cacheCtx, cancel := context.WithCancel(context.Background())
	query.InitQueryCache(cacheCtx)
	t.Cleanup(func() {
		cancel()
		query.QueryCache = nil
	})

	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rollupRows := sqlmock.NewRows([]string{"service", "span_cnt", "p50_ms", "p95_ms", "error_rate"}).
		AddRow("svc-a", int64(200), 10.0, 80.0, 0.001)
	mock.ExpectQuery("SELECT").WillReturnRows(rollupRows)
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "bucket", "spans", "error_rate"}))

	if _, err := svc.Overview(context.Background(), incidentsIncludeParams(60, nil)); err != nil {
		t.Fatalf("first Overview() error = %v", err)
	}
	// Second call should hit the cache — no further mock expectations.
	if _, err := svc.Overview(context.Background(), incidentsIncludeParams(60, nil)); err != nil {
		t.Fatalf("second Overview() error = %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}
