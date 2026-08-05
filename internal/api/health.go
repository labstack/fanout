package api

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/query"
)

// HealthHandler handles health check endpoints
type HealthHandler struct {
	duck    *query.Duck
	cfg     env.Config
	started time.Time
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(duck *query.Duck, cfg env.Config) *HealthHandler {
	return &HealthHandler{duck: duck, cfg: cfg, started: time.Now()}
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
	RuntimeSizing env.RuntimeSizing      `json:"runtime_sizing"`
}

// Liveness returns 200 if process is running (Kubernetes liveness probe)
func (h *HealthHandler) Liveness(c *echo.Context) error {
	return c.String(http.StatusOK, "ok")
}

// Readiness checks all dependencies (Kubernetes readiness probe)
func (h *HealthHandler) Readiness(c *echo.Context) error {
	resp := HealthResponse{
		Status:        "ready",
		Checks:        make(map[string]CheckResult),
		RuntimeSizing: h.cfg.RuntimeSizing(),
	}

	// Check DuckDB
	resp.Checks["duckdb"] = h.checkDuckDB()
	resp.Checks["ducklake"] = h.checkDuckLake()

	// Check data directory
	resp.Checks["data"] = h.checkDataDir()
	resp.Checks["rollups"] = h.checkRollups()
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

func (h *HealthHandler) checkDuckDB() CheckResult {
	if h.duck == nil {
		return CheckResult{
			Status: "unhealthy",
			Error:  "duckdb not initialized",
		}
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var one int
	if err := h.duck.DB.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return CheckResult{
			Status:    "unhealthy",
			LatencyMs: time.Since(start).Milliseconds(),
			Error:     err.Error(),
		}
	}

	return CheckResult{
		Status:    "ok",
		LatencyMs: time.Since(start).Milliseconds(),
	}
}

// diskDegradedPct is the free-space fraction below which the data dir reports
// degraded. A full disk silently fails Parquet flushes (the writer drops rows
// once its retry buffer overflows), so we surface pressure before that point.
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
		res.Error = fmt.Sprintf("low disk space: %.1f%% free — ingest flushes drop rows when the disk fills", freePct)
	}
	return res
}

// maintenanceStaleThreshold is how long after the last executed maintenance pass
// the loop is considered stalled. Maintenance executes every
// MaintenanceEverySeconds; 2× that (floored at 1h) with no pass means it wedged.
func maintenanceStaleThreshold(maintEvery time.Duration) time.Duration {
	stale := 2 * maintEvery
	if stale < time.Hour {
		stale = time.Hour
	}
	return stale
}

func (h *HealthHandler) checkDuckLake() CheckResult {
	if h.duck == nil {
		return CheckResult{
			Status: "unhealthy",
			Error:  "duckdb not initialized",
		}
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var one int
	err := h.duck.DB.QueryRowContext(ctx, "SELECT 1 FROM lake.spans LIMIT 1").Scan(&one)
	if err != nil && err != sql.ErrNoRows {
		return CheckResult{
			Status:    "unhealthy",
			LatencyMs: time.Since(start).Milliseconds(),
			Error:     err.Error(),
		}
	}

	res := CheckResult{
		Status:    "ok",
		LatencyMs: time.Since(start).Milliseconds(),
	}
	if err == sql.ErrNoRows {
		res.Detail = "telemetry attached, no spans yet"
	}
	return res
}

// checkMaintenance surfaces the maintenance loop's own health (retention +
// DuckLake compaction). A failing pass reports "degraded", not "unhealthy":
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

	lastOK, lastAt, lastErr := h.duck.MaintenanceHealth()
	return maintenanceResult(lastOK, lastAt, lastErr, h.started,
		time.Duration(h.cfg.RollupEvery)*time.Second,
		time.Duration(h.cfg.MaintenanceEverySeconds)*time.Second,
		time.Now())
}

// maintenanceResult classifies the maintenance loop's health. now/started are
// injected so every branch — a failing pass, never-ran-past-grace, and a loop
// that stalled after a clean pass — is unit-testable without a running Duck.
func maintenanceResult(lastOK, lastAt time.Time, lastErr error, started time.Time, rollupEvery, maintEvery time.Duration, now time.Time) CheckResult {
	res := CheckResult{Status: "ok"}
	if !lastAt.IsZero() {
		res.UpdatedAt = lastAt.UTC().Format(time.RFC3339)
	}
	if lastErr != nil {
		res.Status = "degraded"
		res.Error = lastErr.Error()
		if !lastOK.IsZero() {
			res.Detail = "last clean pass: " + lastOK.UTC().Format(time.RFC3339)
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
		// still monitored at MaintenanceEverySeconds=0 (where the loop keeps
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

func (h *HealthHandler) checkRollups() CheckResult {
	if h.duck == nil {
		return CheckResult{
			Status: "unhealthy",
			Error:  "duckdb not initialized",
		}
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

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
		return CheckResult{
			Status:    "unhealthy",
			LatencyMs: time.Since(start).Milliseconds(),
			Error:     err.Error(),
		}
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
	threshold := time.Duration(h.cfg.RollupEvery*3) * time.Second
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
func RegisterHealthRoutes(e *echo.Echo, duck *query.Duck, cfg env.Config) {
	h := NewHealthHandler(duck, cfg)
	e.GET("/healthz", h.Liveness)
	e.GET("/readyz", h.Readiness)
	e.GET("/api/health", h.Readiness)
}
