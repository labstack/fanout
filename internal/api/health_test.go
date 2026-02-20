package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/labstack/echo/v5"
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

func TestReadiness_NilDuck(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := NewHealthHandler(nil, config.Config{LakeDir: os.TempDir()})
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
