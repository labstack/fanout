package api

import (
	"encoding/json"
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

	if resp.Status != "ready" {
		t.Fatalf("status = %q, want ready", resp.Status)
	}
	for _, key := range []string{"duckdb", "ducklake", "data", "rollups"} {
		if _, ok := resp.Checks[key]; !ok {
			t.Fatalf("missing %s check", key)
		}
	}
	if resp.Checks["ducklake"].Status != "ok" {
		t.Fatalf("ducklake status = %q, want ok", resp.Checks["ducklake"].Status)
	}
	if resp.Checks["rollups"].Status != "ok" {
		t.Fatalf("rollups status = %q, want ok", resp.Checks["rollups"].Status)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
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
