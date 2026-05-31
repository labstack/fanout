package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

// TestOverviewResult_AlwaysEmitsUIArrays is a regression guard on the JSON
// tags of the UI-consumed array fields. The React home page reads
// `data.{services,incidents,alerts}.length` without optional chaining; the
// `[]`-not-omitted contract is what keeps it from crashing. That contract has
// two layers — this test pins layer 1 (struct tags); layer 2 (handler
// non-nil init, see (*UIHandler).Overview in internal/api/ui.go) is what
// upgrades `null` to `[]` on the wire. Together they're what the UI relies on.
//
// If someone reintroduces `,omitempty` on any of the three fields, the empty
// non-nil case here trips. The nil case documents the consequence of relying
// on the tag alone (`null` on the wire) — which would still crash the home
// page without the handler init, hence both layers.
func TestOverviewResult_AlwaysEmitsUIArrays(t *testing.T) {
	tests := []struct {
		name   string
		result *OverviewResult
		want   []string
	}{
		{
			name: "empty non-nil slices serialize as []",
			result: &OverviewResult{
				Services:  []OverviewService{},
				Incidents: []OverviewIncident{},
				Alerts:    []OverviewAlert{},
			},
			want: []string{`"services":[]`, `"incidents":[]`, `"alerts":[]`},
		},
		{
			name:   "nil slices still appear as null (no omitempty)",
			result: &OverviewResult{},
			want:   []string{`"services":null`, `"incidents":null`, `"alerts":null`},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.result)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			for _, field := range tc.want {
				if !strings.Contains(string(b), field) {
					t.Errorf("expected %s in JSON, got: %s", field, b)
				}
			}
		})
	}
}
