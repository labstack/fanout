package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/query"
)

// HealthHandler handles health check endpoints
type HealthHandler struct {
	duck *query.Duck
	cfg  env.Config
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(duck *query.Duck, cfg env.Config) *HealthHandler {
	return &HealthHandler{duck: duck, cfg: cfg}
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
	Status string                 `json:"status"`
	Checks map[string]CheckResult `json:"checks"`
}

// Liveness returns 200 if process is running (Kubernetes liveness probe)
func (h *HealthHandler) Liveness(c *echo.Context) error {
	return c.String(http.StatusOK, "ok")
}

// Readiness checks all dependencies (Kubernetes readiness probe)
func (h *HealthHandler) Readiness(c *echo.Context) error {
	resp := HealthResponse{
		Status: "ready",
		Checks: make(map[string]CheckResult),
	}

	// Check DuckDB
	resp.Checks["duckdb"] = h.checkDuckDB()
	resp.Checks["ducklake"] = h.checkDuckLake()

	// Check data directory
	resp.Checks["data"] = h.checkDataDir()
	resp.Checks["rollups"] = h.checkRollups()

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

	return CheckResult{
		Status: "ok",
	}
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
