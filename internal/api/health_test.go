package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/engine"
)

func TestLiveness(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := &HealthHandler{cfg: config.Config{DataDir: os.TempDir()}}
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

	h := NewHealthHandler(nil, config.Config{DataDir: os.TempDir()})
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

func TestReadinessReportsSizingResolvedByLoader(t *testing.T) {
	environ := []string{
		"FANOUT_AUTH_CODE_SECRET=0123456789abcdef0123456789abcdef",
		"ANTHROPIC_API_KEY=sk-test",
		"FANOUT_SMTP_HOST=smtp.example.com",
		"FANOUT_SMTP_USERNAME=user",
		"FANOUT_SMTP_PASSWORD=pass",
		"FANOUT_SMTP_FROM=noreply@example.com",
		"FANOUT_DATA_DIR=" + t.TempDir(),
	}
	cfg, err := config.Load(config.LoadOptions{Environ: environ})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	if err := NewHealthHandler(nil, cfg).Readiness(e.NewContext(req, rec)); err != nil {
		t.Fatalf("Readiness: %v", err)
	}

	var resp HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.RuntimeSizing.DuckDBMemoryAuto || !resp.RuntimeSizing.DuckDBMaxConnsAuto {
		t.Fatalf("loader-selected sizing lost auto provenance: %+v", resp.RuntimeSizing)
	}
	if resp.RuntimeSizing.DuckDBMaxConns < 2 {
		t.Fatalf("reported max connections = %d, want resolved value", resp.RuntimeSizing.DuckDBMaxConns)
	}
	if resp.RuntimeSizing.MemorySource == "" {
		t.Fatalf("reported sizing has no memory source: %+v", resp.RuntimeSizing)
	}
}

func TestReadiness_HealthyTelemetryAndRollups(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// The engine probes run concurrently.
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("SELECT 1$").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectQuery("SELECT 1 FROM telemetry.spans LIMIT 1").
		WillReturnRows(sqlmock.NewRows([]string{"1"}))
	mock.ExpectQuery("SELECT\\s+MAX\\(updated_at\\),\\s+COUNT\\(\\*\\),\\s+COALESCE\\(date_diff\\('second', MAX\\(updated_at\\), now\\(\\)\\), 0\\)").
		WillReturnRows(sqlmock.NewRows([]string{"max", "count", "age_seconds"}).AddRow(time.Now().UTC(), 2, int64(30)))

	h := NewHealthHandler(&engine.Duck{DB: db}, config.Config{
		DataDir:        os.TempDir(),
		RollupInterval: time.Minute,
		DuckDBMemory:   "3GB",
		DuckDBMaxConns: 2,
		DuckDBThreads:  1,
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
	// degraded on a low-disk CI/dev box. This test is about telemetry+rollups, so
	// tolerate a data-only degradation but require the actual subjects to be ok.
	if resp.Status != "ready" && resp.Status != "degraded" {
		t.Fatalf("status = %q, want ready or degraded", resp.Status)
	}
	for _, key := range []string{"duckdb", "telemetry", "data", "rollups", "maintenance"} {
		if _, ok := resp.Checks[key]; !ok {
			t.Fatalf("missing %s check", key)
		}
	}
	if sizing := resp.RuntimeSizing; sizing.DuckDBMemory != "3GB" || sizing.DuckDBMaxConns != 2 || sizing.DuckDBThreads != 1 {
		t.Fatalf("runtime sizing = %+v, want effective DuckDB configuration", sizing)
	}
	if resp.RuntimeSizing.GOMAXPROCS <= 0 {
		t.Fatalf("runtime sizing GOMAXPROCS = %d, want positive", resp.RuntimeSizing.GOMAXPROCS)
	}
	for _, key := range []string{"duckdb", "telemetry", "rollups", "maintenance"} {
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

// A long query holding the engine's connections made /readyz answer 503
// once: a busy engine is still ready and must not flap out of rotation.
func TestReadinessReportsABusyEngineAsDegraded(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	original := readinessTimeout
	readinessTimeout = 20 * time.Millisecond
	t.Cleanup(func() { readinessTimeout = original })
	slow := 200 * time.Millisecond
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("SELECT 1$").WillDelayFor(slow).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectQuery("SELECT 1 FROM telemetry.spans LIMIT 1").WillDelayFor(slow).WillReturnRows(sqlmock.NewRows([]string{"1"}))
	mock.ExpectQuery("SELECT\\s+MAX\\(updated_at\\)").WillDelayFor(slow).WillReturnRows(sqlmock.NewRows([]string{"max", "count", "age_seconds"}).AddRow(time.Now().UTC(), 2, int64(30)))

	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/readyz", nil), rec)
	started := time.Now()
	if err := NewHealthHandler(&engine.Duck{DB: db}, config.Config{DataDir: os.TempDir(), RollupInterval: time.Minute}).Readiness(c); err != nil {
		t.Fatal(err)
	}
	// One shared deadline, not one per probe: three sequential 5 s probes
	// outlasted the 3 s container health check.
	if elapsed := time.Since(started); elapsed >= 3*readinessTimeout {
		t.Fatalf("readiness took %s, want one shared %s deadline", elapsed, readinessTimeout)
	}
	if original >= time.Second {
		t.Fatalf("readiness deadline %s must stay under a 1 s probe timeout", original)
	}
	var resp HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || resp.Status != "degraded" {
		t.Fatalf("status = %d %q, want 200 degraded: %+v", rec.Code, resp.Status, resp.Checks)
	}
	for _, key := range []string{"duckdb", "telemetry", "rollups"} {
		if check := resp.Checks[key]; check.Status != "degraded" || check.Detail != "engine busy" {
			t.Fatalf("%s check = %+v, want degraded and busy", key, check)
		}
	}
}

// A failing engine is not merely busy.
func TestReadinessReportsAFailingEngineAsUnhealthy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT 1").WillReturnError(errors.New("database is closed"))
	if check := NewHealthHandler(&engine.Duck{DB: db}, config.Config{}).checkDuckDB(t.Context()); check.Status != "unhealthy" {
		t.Fatalf("duckdb check = %+v, want unhealthy", check)
	}
}

type fakePool struct {
	stalled  bool
	overruns int
	live     error
}

func (p *fakePool) Stalled() bool              { return p.stalled }
func (p *fakePool) Overruns() int              { return p.overruns }
func (p *fakePool) Live(context.Context) error { return p.live }

// Probes time out alike on an overloaded engine and a stalled one. Only a
// stall in Fanout's own work fails readiness and liveness, so the supervisor
// restarts it; an overloaded engine, or one running a query past its
// deadline, stays alive and reads as degraded.
func TestOnlyAStalledEngineFailsReadinessAndLiveness(t *testing.T) {
	original := readinessTimeout
	readinessTimeout = 20 * time.Millisecond
	t.Cleanup(func() { readinessTimeout = original })
	for _, test := range []struct {
		name            string
		pool            *fakePool
		ready, live     int
		status, details string
		queries         string
	}{
		{"stalled", &fakePool{stalled: true, live: engine.ErrStalled}, http.StatusServiceUnavailable, http.StatusServiceUnavailable, "unhealthy", "engine stalled", "ok"},
		{"overloaded", &fakePool{}, http.StatusOK, http.StatusOK, "degraded", "engine busy", "ok"},
		{"overrunning", &fakePool{overruns: 1}, http.StatusOK, http.StatusOK, "degraded", "engine busy", "degraded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			mock.MatchExpectationsInOrder(false)
			mock.ExpectQuery("SELECT 1$").WillDelayFor(time.Second).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
			mock.ExpectQuery("SELECT 1 FROM telemetry.spans LIMIT 1").WillDelayFor(time.Second).WillReturnRows(sqlmock.NewRows([]string{"1"}))
			mock.ExpectQuery("SELECT\\s+MAX\\(updated_at\\)").WillDelayFor(time.Second).WillReturnRows(sqlmock.NewRows([]string{"max", "count", "age_seconds"}).AddRow(time.Now().UTC(), 2, int64(30)))
			h := NewHealthHandler(&engine.Duck{DB: db}, config.Config{DataDir: os.TempDir(), RollupInterval: time.Minute})
			h.pool = test.pool

			rec := httptest.NewRecorder()
			if err := h.Readiness(echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/readyz", nil), rec)); err != nil {
				t.Fatal(err)
			}
			var resp HealthResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if check := resp.Checks["duckdb"]; rec.Code != test.ready || check.Status != test.status || check.Detail != test.details {
				t.Fatalf("readiness = %d %+v, want %d %s %s", rec.Code, check, test.ready, test.status, test.details)
			}
			if check := resp.Checks["queries"]; check.Status != test.queries {
				t.Fatalf("queries check = %+v, want %s", check, test.queries)
			}
			live := httptest.NewRecorder()
			if err := h.Liveness(echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/healthz", nil), live)); err != nil {
				t.Fatal(err)
			}
			if live.Code != test.live {
				t.Fatalf("liveness = %d, want %d", live.Code, test.live)
			}
		})
	}
}

// A maintenance pass that never executes must degrade once the startup grace
// period elapses — the wedged-first-rollup case the check exists to expose.
func TestCheckMaintenance_DegradedWhenNeverRanPastGrace(t *testing.T) {
	h := &HealthHandler{
		duck:    &engine.Duck{},
		cfg:     config.Config{RollupInterval: time.Minute},
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

func TestRegisterHealthRoutes_RegistersReadiness(t *testing.T) {
	e := echo.New()
	RegisterHealthRoutes(e, nil, config.Config{DataDir: os.TempDir()})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz status = %d, want 503", rec.Code)
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
		failures       int
		started        time.Time
		maintEvery     time.Duration
		wantStatus     string
	}{
		{"clean recent pass", now.Add(-10 * time.Minute), now.Add(-10 * time.Minute), nil, 0, now.Add(-2 * time.Hour), time.Hour, "ok"},
		{"failing pass", now.Add(-3 * time.Hour), now.Add(-time.Hour), errors.New("boom"), 1, now.Add(-4 * time.Hour), time.Hour, "degraded"},
		{"repeated failure remains routable", now.Add(-4 * time.Hour), now.Add(-time.Hour), errors.New("boom"), 3, now.Add(-5 * time.Hour), time.Hour, "degraded"},
		{"never ran, past grace", time.Time{}, time.Time{}, nil, 0, now.Add(-10 * time.Minute), time.Hour, "degraded"},
		{"never ran, within grace", time.Time{}, time.Time{}, nil, 0, now.Add(-time.Minute), time.Hour, "ok"},
		{"stalled after clean pass", now.Add(-5 * time.Hour), now.Add(-5 * time.Hour), nil, 0, now.Add(-6 * time.Hour), time.Hour, "degraded"},
		// maintEvery=0 must still detect staleness: the loop floors 0→1h and keeps
		// running, so the check mirrors that (2h stale > 1h floored threshold).
		{"stalled, interval unset (floored)", now.Add(-3 * time.Hour), now.Add(-3 * time.Hour), nil, 0, now.Add(-4 * time.Hour), 0, "degraded"},
		{"recent pass, interval unset", now.Add(-10 * time.Minute), now.Add(-10 * time.Minute), nil, 0, now.Add(-4 * time.Hour), 0, "ok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := maintenanceResult(c.lastOK, c.lastAt, c.lastErr, c.failures, c.started, rollupEvery, c.maintEvery, now)
			if res.Status != c.wantStatus {
				t.Errorf("status = %q, want %q (detail=%q err=%q)", res.Status, c.wantStatus, res.Detail, res.Error)
			}
		})
	}
}
