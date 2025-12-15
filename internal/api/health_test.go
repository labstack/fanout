package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/labstack/fanout/internal/config"
)

func TestLiveness(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := &HealthHandler{cfg: config.Config{LakeDir: os.TempDir()}}
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

func TestReadiness_NoDuck(t *testing.T) {
	// This test requires a mock DuckDB connection
	// In production, we'd use an interface for dependency injection
	t.Skip("nil duck causes panic - requires mock")
}

func TestReadiness_BadLakeDir(t *testing.T) {
	// This test requires a mock DuckDB connection
	t.Skip("nil duck causes panic - requires mock")
}

func TestCheckResult(t *testing.T) {
	// Test CheckResult JSON marshaling
	result := CheckResult{
		Status:    "ok",
		LatencyMs: 5,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded CheckResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Status != "ok" {
		t.Errorf("expected ok, got %s", decoded.Status)
	}
}
