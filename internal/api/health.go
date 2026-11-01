package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/engine"
)

// HealthHandler handles health check endpoints
type HealthHandler struct {
	duck *engine.Duck
	// pool is the engine's read pool, which tells a stalled engine from a busy one.
	pool    readPool
	cfg     config.Config
	started time.Time
}

type readPool interface {
	Stalled() bool
	Ping(context.Context) error
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(duck *engine.Duck, cfg config.Config) *HealthHandler {
	h := &HealthHandler{duck: duck, cfg: cfg, started: time.Now()}
	if duck != nil {
		h.pool = duck
	}
	return h
}

// CheckResult represents a single health check result
type CheckResult struct {
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
	Detail    string `json:"detail,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// HealthResponse represents the full health check response
type HealthResponse struct {
	Status        string                 `json:"status"`
	Checks        map[string]CheckResult `json:"checks"`
	RuntimeSizing config.RuntimeSizing   `json:"runtime_sizing"`
}

// livenessPingTimeout keeps the confirming ping inside the container health
// check's 3 s timeout.
const livenessPingTimeout = time.Second

// Liveness returns 200 while the process can serve (Kubernetes liveness
// probe). A read pool that has made no progress for longer than reads may run
// is wedged and only a restart frees it, so liveness fails once a ping
// confirms it; an idle pool records no progress either, but answers.
func (h *HealthHandler) Liveness(c *echo.Context) error {
	if h.pool != nil && h.pool.Stalled() {
		ctx, cancel := context.WithTimeout(c.Request().Context(), livenessPingTimeout)
		defer cancel()
		if err := h.pool.Ping(ctx); err != nil {
			return c.String(http.StatusServiceUnavailable, "engine stalled")
		}
	}
	return c.String(http.StatusOK, "ok")
}

// Readiness checks all dependencies (Kubernetes readiness probe)
func (h *HealthHandler) Readiness(c *echo.Context) error {
	resp := HealthResponse{
		Status:        "ready",
		Checks:        make(map[string]CheckResult),
		RuntimeSizing: h.cfg.RuntimeSizing(),
	}

	// The engine probes share one deadline and run concurrently, so a busy
	// engine answers within it rather than after each probe's own.
	ctx, cancel := context.WithTimeout(c.Request().Context(), readinessTimeout)
	defer cancel()
	probes := map[string]func(context.Context) CheckResult{"duckdb": h.checkDuckDB, "telemetry": h.checkTelemetry, "rollups": h.checkRollups}
	type named struct {
		name   string
		result CheckResult
	}
	results := make(chan named, len(probes))
	for name, probe := range probes {
		go func() { results <- named{name, probe(ctx)} }()
	}
	for range probes {
		result := <-results
		resp.Checks[result.name] = result.result
	}

	resp.Checks["data"] = h.checkDataDir()
	resp.Checks["maintenance"] = h.checkMaintenance()

	// Determine overall status
	hasUnhealthy := false
	hasDegraded := false
	for _, check := range resp.Checks {
		if check.Status == "unhealthy" {
			hasUnhealthy = true
		} else if check.Status == "degraded" {
			hasDegraded = true
		}
	}

	if hasUnhealthy {
		resp.Status = "unhealthy"
		return c.JSON(http.StatusServiceUnavailable, resp)
	}
	if hasDegraded {
		resp.Status = "degraded"
		return c.JSON(http.StatusOK, resp)
	}

	return c.JSON(http.StatusOK, resp)
}

func (h *HealthHandler) checkDuckDB(ctx context.Context) CheckResult {
	if h.duck == nil {
		return CheckResult{
			Status: "unhealthy",
			Error:  "duckdb not initialized",
		}
	}

	start := time.Now()

	var one int
	if err := h.duck.DB.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return h.failedCheck(ctx, start, err)
	}

	return CheckResult{
		Status:    "ok",
		LatencyMs: time.Since(start).Milliseconds(),
	}
}

// diskDegradedPct is the free-space fraction below which the data dir reports
// degraded. A full disk fails Parquet flushes and stops ingest, so we surface
// pressure before that point.
const diskDegradedPct = 10.0

func (h *HealthHandler) checkDataDir() CheckResult {
	// Check if the data directory exists and is writable.
	testFile := filepath.Join(h.cfg.DataDir, ".health-check")

	// Try to create a temp file
	f, err := os.Create(testFile)
	if err != nil {
		return CheckResult{
			Status: "unhealthy",
			Error:  "data dir not writable: " + err.Error(),
		}
	}
	f.Close()
	os.Remove(testFile)

	// Writability alone doesn't catch a near-full disk — surface free space.
	// Best-effort: if Statfs fails, stay "ok" (the dir is writable) but make the
	// gap visible rather than indistinguishable from a healthy disk.
	var stat syscall.Statfs_t
	if err := syscall.Statfs(h.cfg.DataDir, &stat); err != nil {
		slog.Warn("data dir statfs failed; free-space check skipped", "dir", h.cfg.DataDir, "err", err)
		return CheckResult{Status: "ok", Detail: "free space unknown: statfs failed"}
	}
	bsize := uint64(stat.Bsize)
	return diskSpaceResult(stat.Bavail*bsize, uint64(stat.Blocks)*bsize)
}

// diskSpaceResult classifies free vs total bytes into ok/degraded. Pure so it's
// unit-testable without a real filesystem.
func diskSpaceResult(freeBytes, totalBytes uint64) CheckResult {
	if totalBytes == 0 {
		return CheckResult{Status: "ok"}
	}
	freePct := float64(freeBytes) / float64(totalBytes) * 100
	res := CheckResult{
		Status: "ok",
		Detail: fmt.Sprintf("%.1f%% free (%d MiB)", freePct, freeBytes>>20),
	}
	if freePct < diskDegradedPct {
		res.Status = "degraded"
		res.Error = fmt.Sprintf("low disk space: %.1f%% free — ingest stops when telemetry cannot commit", freePct)
	}
	return res
}

// maintenanceStaleThreshold is how long after the last executed maintenance pass
// the loop is considered stalled. Maintenance executes every
// MaintenanceInterval; 2× that (floored at 1h) with no pass means it wedged.
func maintenanceStaleThreshold(maintEvery time.Duration) time.Duration {
	stale := 2 * maintEvery
	if stale < time.Hour {
		stale = time.Hour
	}
	return stale
}

// readinessTimeout is the engine probes' shared deadline, under the 1 s a
// Kubernetes probe allows by default and the container check's 3 s.
var readinessTimeout = 800 * time.Millisecond

// failedCheck classifies a probe error. A probe that ran out its deadline
// waited behind long reads holding the engine's connections: the instance
// still serves, so it is degraded rather than unready, and /readyz does not
// flap out of rotation under load. A stalled read pool times out the same
// way but never recovers, so it fails readiness, as /healthz does. Any other
// error is a failing engine. The deadline is checked on ctx as well: a driver
// may report the cancelled statement in its own words.
func (h *HealthHandler) failedCheck(ctx context.Context, start time.Time, err error) CheckResult {
	res := CheckResult{Status: "unhealthy", LatencyMs: time.Since(start).Milliseconds(), Error: err.Error()}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		if h.pool != nil && h.pool.Stalled() {
			res.Detail = "engine stalled"
		} else {
			res.Status, res.Detail = "degraded", "engine busy"
		}
	}
	return res
}

func (h *HealthHandler) checkTelemetry(ctx context.Context) CheckResult {
	if h.duck == nil {
		return CheckResult{
			Status: "unhealthy",
			Error:  "duckdb not initialized",
		}
	}

	start := time.Now()

	var one int
	// Use the same snapshot gate as public reads so a short compaction/retention
	// publication cannot race the Parquet scan and report a false outage.
	err := h.duck.QueryRowScan(ctx, []any{&one}, "SELECT 1 FROM telemetry.spans LIMIT 1")
	if errors.Is(err, engine.ErrParquetReadWait) {
		return CheckResult{
			Status:    "degraded",
			LatencyMs: time.Since(start).Milliseconds(),
			Error:     err.Error(),
		}
	}
	if err != nil && err != sql.ErrNoRows {
		return h.failedCheck(ctx, start, err)
	}

	res := CheckResult{
		Status:    "ok",
		LatencyMs: time.Since(start).Milliseconds(),
	}
	if err == sql.ErrNoRows {
		res.Detail = "telemetry repository ready, no spans yet"
	}
	return res
}

// checkMaintenance surfaces the maintenance loop's own health (retention,
// compaction, and cache checkpointing). A failing pass reports "degraded", not
// "unhealthy":
// ingest and queries still work while maintenance fails, and a restart
// wouldn't fix it — pulling the instance from rotation would only hide the
// signal that storage growth is no longer being reclaimed. A maintenance pass
// that never executes (e.g. the rollup loop is wedged before it) also reports
// degraded once the process has been up past a startup grace period. A loop
// that stalls AFTER a clean pass is caught too: a last-pass time older than
// maintenanceStaleThreshold reports degraded.
func (h *HealthHandler) checkMaintenance() CheckResult {
	if h.duck == nil {
		return CheckResult{
			Status: "unhealthy",
			Error:  "duckdb not initialized",
		}
	}

	lastOK, lastAt, failures, lastErr := h.duck.MaintenanceHealth()
	return maintenanceResult(lastOK, lastAt, lastErr, failures, h.started,
		h.cfg.RollupInterval,
		h.cfg.MaintenanceInterval,
		time.Now())
}

// maintenanceResult classifies the maintenance loop's health. now/started are
// injected so every branch — a failing pass, never-ran-past-grace, and a loop
// that stalled after a clean pass — is unit-testable without a running Duck.
func maintenanceResult(lastOK, lastAt time.Time, lastErr error, consecutiveFailures int, started time.Time, rollupEvery, maintEvery time.Duration, now time.Time) CheckResult {
	res := CheckResult{Status: "ok"}
	if !lastAt.IsZero() {
		res.UpdatedAt = lastAt.UTC().Format(time.RFC3339)
	}
	if lastErr != nil {
		res.Status = "degraded"
		res.Error = lastErr.Error()
		res.Detail = fmt.Sprintf("%d consecutive failed passes", consecutiveFailures)
		if !lastOK.IsZero() {
			res.Detail += "; last clean pass: " + lastOK.UTC().Format(time.RFC3339)
		}
		return res
	}
	if lastAt.IsZero() {
		grace := 3 * rollupEvery
		if grace < 5*time.Minute {
			grace = 5 * time.Minute
		}
		if now.Sub(started) > grace {
			res.Status = "degraded"
			res.Detail = "maintenance has not run since process start"
		} else {
			res.Detail = "no maintenance pass yet"
		}
	} else {
		// Caught a clean pass before, but the loop may have wedged since:
		// lastAt stops advancing while maintenance should keep executing. Mirror
		// runMaintenance's flooring of a 0/unset interval to 1h so staleness is
		// still monitored at MaintenanceInterval=0 (where the loop keeps
		// running hourly) instead of being silently skipped.
		if maintEvery <= 0 {
			maintEvery = time.Hour
		}
		if since := now.Sub(lastAt); since > maintenanceStaleThreshold(maintEvery) {
			res.Status = "degraded"
			res.Detail = fmt.Sprintf("maintenance stale: last pass %s ago", since.Round(time.Second))
		}
	}
	return res
}

func (h *HealthHandler) checkRollups(ctx context.Context) CheckResult {
	if h.duck == nil {
		return CheckResult{
			Status: "unhealthy",
			Error:  "duckdb not initialized",
		}
	}

	start := time.Now()

	var updatedAt sql.NullTime
	var cacheCount int
	var ageSeconds int64
	err := h.duck.DB.QueryRowContext(ctx, `
SELECT
  MAX(updated_at),
  COUNT(*),
  COALESCE(date_diff('second', MAX(updated_at), now()), 0)
FROM rollup_state`).Scan(&updatedAt, &cacheCount, &ageSeconds)
	if err != nil {
		return h.failedCheck(ctx, start, err)
	}

	res := CheckResult{
		Status:    "ok",
		LatencyMs: time.Since(start).Milliseconds(),
	}
	if cacheCount == 0 || !updatedAt.Valid {
		res.Detail = "no rollup state yet"
		return res
	}

	res.UpdatedAt = updatedAt.Time.UTC().Format(time.RFC3339)
	age := time.Duration(ageSeconds) * time.Second
	threshold := 3 * h.cfg.RollupInterval
	if threshold < 2*time.Minute {
		threshold = 2 * time.Minute
	}

	if age > threshold {
		res.Status = "degraded"
		res.Detail = fmt.Sprintf("rollups stale by %s", age.Round(time.Second))
		return res
	}

	res.Detail = fmt.Sprintf("last refreshed %s ago", age.Round(time.Second))
	return res
}

// RegisterHealthRoutes registers health check endpoints
func RegisterHealthRoutes(e *echo.Echo, duck *engine.Duck, cfg config.Config) {
	h := NewHealthHandler(duck, cfg)
	e.GET("/healthz", h.Liveness)
	e.GET("/readyz", h.Readiness)
}
