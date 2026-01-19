package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
)

// HealthHandler handles health check endpoints
type HealthHandler struct {
	duck *query.Duck
	cfg  config.Config
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(duck *query.Duck, cfg config.Config) *HealthHandler {
	return &HealthHandler{duck: duck, cfg: cfg}
}

// CheckResult represents a single health check result
type CheckResult struct {
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

// HealthResponse represents the full health check response
type HealthResponse struct {
	Status string                 `json:"status"`
	Checks map[string]CheckResult `json:"checks"`
}

// Liveness returns 200 if process is running (Kubernetes liveness probe)
func (h *HealthHandler) Liveness(c echo.Context) error {
	return c.String(http.StatusOK, "ok")
}

// Readiness checks all dependencies (Kubernetes readiness probe)
func (h *HealthHandler) Readiness(c echo.Context) error {
	resp := HealthResponse{
		Status: "ready",
		Checks: make(map[string]CheckResult),
	}

	// Check DuckDB
	resp.Checks["duckdb"] = h.checkDuckDB()

	// Check lake directory
	resp.Checks["lake"] = h.checkLakeDir()

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
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := h.duck.DB.QueryContext(ctx, "SELECT 1")
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return CheckResult{
			Status:    "unhealthy",
			LatencyMs: latency,
			Error:     err.Error(),
		}
	}
	rows.Close()

	return CheckResult{
		Status:    "ok",
		LatencyMs: latency,
	}
}

func (h *HealthHandler) checkLakeDir() CheckResult {
	// Check if lake directory exists and is writable
	testFile := filepath.Join(h.cfg.LakeDir, ".health-check")

	// Try to create a temp file
	f, err := os.Create(testFile)
	if err != nil {
		return CheckResult{
			Status: "unhealthy",
			Error:  "lake dir not writable: " + err.Error(),
		}
	}
	f.Close()
	os.Remove(testFile)

	return CheckResult{
		Status: "ok",
	}
}

// RegisterHealthRoutes registers health check endpoints
func RegisterHealthRoutes(e *echo.Echo, duck *query.Duck, cfg config.Config) {
	h := NewHealthHandler(duck, cfg)
	e.GET("/healthz", h.Liveness)
	e.GET("/readyz", h.Readiness)
}
