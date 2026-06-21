package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/query"
)

func TestLiveness(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := &HealthHandler{cfg: env.Config{DataDir: os.TempDir()}}
	err := h.Liveness(c)
	if err != nil {
		t.Fatalf("Liveness error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if body != "ok" {
		t.Errorf("expected body 'ok', got %s", body)
	}
}

func TestReadiness_NilDuck(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := NewHealthHandler(nil, env.Config{DataDir: os.TempDir()})
	err := h.Readiness(c)
	if err != nil {
		t.Fatalf("Readiness error: %v", err)
	}

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}

	var resp HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if resp.Status != "unhealthy" {
		t.Errorf("expected status 'unhealthy', got %s", resp.Status)
	}

	duckCheck, ok := resp.Checks["duckdb"]
	if !ok {
		t.Fatal("missing duckdb check")
	}
	if duckCheck.Status != "unhealthy" {
		t.Errorf("expected duckdb check 'unhealthy', got %s", duckCheck.Status)
	}
	if duckCheck.Error != "duckdb not initialized" {
		t.Errorf("expected error 'duckdb not initialized', got %s", duckCheck.Error)
	}
}

func TestReadiness_HealthyDuckLakeAndRollups(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	mock.ExpectQuery("SELECT 1").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectQuery("SELECT 1 FROM lake.spans LIMIT 1").
		WillReturnRows(sqlmock.NewRows([]string{"1"}))
	mock.ExpectQuery("SELECT\\s+MAX\\(updated_at\\),\\s+COUNT\\(\\*\\),\\s+COALESCE\\(date_diff\\('second', MAX\\(updated_at\\), now\\(\\)\\), 0\\)").
		WillReturnRows(sqlmock.NewRows([]string{"max", "count", "age_seconds"}).AddRow(time.Now().UTC(), 2, int64(30)))

	h := NewHealthHandler(&query.Duck{DB: db}, env.Config{
		DataDir:     os.TempDir(),
		RollupEvery: 60,
	})
	if err := h.Readiness(c); err != nil {
		t.Fatalf("Readiness error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	// The "data" check reads real host free space, which can legitimately be
	// degraded on a low-disk CI/dev box. This test is about ducklake+rollups, so
	// tolerate a data-only degradation but require the actual subjects to be ok.
	if resp.Status != "ready" && resp.Status != "degraded" {
		t.Fatalf("status = %q, want ready or degraded", resp.Status)
	}
	for _, key := range []string{"duckdb", "ducklake", "data", "rollups", "maintenance"} {
		if _, ok := resp.Checks[key]; !ok {
			t.Fatalf("missing %s check", key)
		}
	}
	for _, key := range []string{"duckdb", "ducklake", "rollups", "maintenance"} {
		if got := resp.Checks[key].Status; got != "ok" {
			t.Fatalf("%s check = %q, want ok", key, got)
		}
	}
	// Freshly started handler + no maintenance pass yet = within the startup
	// grace period, so the check reports ok with a detail, not degraded.
	if m := resp.Checks["maintenance"]; m.Status != "ok" || m.Detail != "no maintenance pass yet" {
		t.Fatalf("maintenance check = %+v, want ok / 'no maintenance pass yet'", m)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

// A maintenance pass that never executes must degrade once the startup grace
// period elapses — the wedged-first-rollup case the check exists to expose.
func TestCheckMaintenance_DegradedWhenNeverRanPastGrace(t *testing.T) {
	h := &HealthHandler{
		duck:    &query.Duck{},
		cfg:     env.Config{RollupEvery: 60},
		started: time.Now().Add(-10 * time.Minute),
	}

	res := h.checkMaintenance()
	if res.Status != "degraded" {
		t.Fatalf("status = %q, want degraded", res.Status)
	}
	if res.Detail != "maintenance has not run since process start" {
		t.Fatalf("detail = %q, want 'maintenance has not run since process start'", res.Detail)
	}
}

func TestRegisterHealthRoutes_RegistersAPIHealth(t *testing.T) {
	e := echo.New()
	RegisterHealthRoutes(e, nil, env.Config{DataDir: os.TempDir()})

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/api/health status = %d, want 503", rec.Code)
	}
}

func TestDiskSpaceResult(t *testing.T) {
	const gib = uint64(1) << 30
	cases := []struct {
		name       string
		free, tot  uint64
		wantStatus string
	}{
		{"plenty free", 50 * gib, 100 * gib, "ok"},
		{"exactly at threshold", 10 * gib, 100 * gib, "ok"},     // 10.0% not < 10
		{"below threshold", 9 * gib, 100 * gib, "degraded"},     // 9% < 10
		{"nearly full", 100 * (1 << 20), 100 * gib, "degraded"}, // ~0.1%
		{"unknown total", 0, 0, "ok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := diskSpaceResult(c.free, c.tot)
			if got.Status != c.wantStatus {
				t.Errorf("diskSpaceResult(%d,%d).Status = %q, want %q", c.free, c.tot, got.Status, c.wantStatus)
			}
			if c.tot > 0 && got.Detail == "" {
				t.Errorf("expected a free-space Detail, got empty")
			}
			if got.Status == "degraded" && got.Error == "" {
				t.Errorf("degraded result should carry an Error explaining low space")
			}
		})
	}
}

func TestMaintenanceStaleThreshold(t *testing.T) {
	cases := []struct {
		every time.Duration
		want  time.Duration
	}{
		{time.Hour, 2 * time.Hour},     // 2× the interval
		{3 * time.Hour, 6 * time.Hour}, // 2×
		{60 * time.Second, time.Hour},  // floored at 1h
		{20 * time.Minute, time.Hour},  // 40m → floored to 1h
	}
	for _, c := range cases {
		if got := maintenanceStaleThreshold(c.every); got != c.want {
			t.Errorf("maintenanceStaleThreshold(%s) = %s, want %s", c.every, got, c.want)
		}
	}
}

func TestMaintenanceResult(t *testing.T) {
	now := time.Now()
	rollupEvery := 60 * time.Second
	cases := []struct {
		name           string
		lastOK, lastAt time.Time
		lastErr        error
		started        time.Time
		maintEvery     time.Duration
		wantStatus     string
	}{
		{"clean recent pass", now.Add(-10 * time.Minute), now.Add(-10 * time.Minute), nil, now.Add(-2 * time.Hour), time.Hour, "ok"},
		{"failing pass", now.Add(-3 * time.Hour), now.Add(-time.Hour), errors.New("boom"), now.Add(-4 * time.Hour), time.Hour, "degraded"},
		{"never ran, past grace", time.Time{}, time.Time{}, nil, now.Add(-10 * time.Minute), time.Hour, "degraded"},
		{"never ran, within grace", time.Time{}, time.Time{}, nil, now.Add(-time.Minute), time.Hour, "ok"},
		{"stalled after clean pass", now.Add(-5 * time.Hour), now.Add(-5 * time.Hour), nil, now.Add(-6 * time.Hour), time.Hour, "degraded"},
		// maintEvery=0 must still detect staleness: the loop floors 0→1h and keeps
		// running, so the check mirrors that (2h stale > 1h floored threshold).
		{"stalled, interval unset (floored)", now.Add(-3 * time.Hour), now.Add(-3 * time.Hour), nil, now.Add(-4 * time.Hour), 0, "degraded"},
		{"recent pass, interval unset", now.Add(-10 * time.Minute), now.Add(-10 * time.Minute), nil, now.Add(-4 * time.Hour), 0, "ok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := maintenanceResult(c.lastOK, c.lastAt, c.lastErr, c.started, rollupEvery, c.maintEvery, now)
			if res.Status != c.wantStatus {
				t.Errorf("status = %q, want %q (detail=%q err=%q)", res.Status, c.wantStatus, res.Detail, res.Error)
			}
		})
	}
}
