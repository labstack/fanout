package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/intelligence"
)

type fakeIntelligenceSnapshots struct {
	snapshot *intelligence.IntelligenceSnapshot
}

func (f fakeIntelligenceSnapshots) LatestSnapshot() *intelligence.IntelligenceSnapshot {
	return f.snapshot
}

func TestIntelligenceLatestReturnsSnapshot(t *testing.T) {
	want := &intelligence.IntelligenceSnapshot{
		GeneratedAt: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC),
		Timeframe:   "last_15m",
		Summary:     "System healthy (98/100).",
		HealthScore: 98,
		Insights:    []intelligence.Insight{},
		Patterns:    []intelligence.Pattern{},
		Anomalies:   []intelligence.Anomaly{},
	}
	e := echo.New()
	h := &intelligenceHandler{snapshots: fakeIntelligenceSnapshots{snapshot: want}}
	e.GET("/api/intelligence", h.latest)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/intelligence", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got intelligence.IntelligenceSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.GeneratedAt != want.GeneratedAt || got.Timeframe != want.Timeframe || got.HealthScore != want.HealthScore || got.Summary != want.Summary {
		t.Fatalf("snapshot = %#v, want %#v", got, *want)
	}
}

func TestIntelligenceLatestReportsNotReady(t *testing.T) {
	e := echo.New()
	h := &intelligenceHandler{snapshots: fakeIntelligenceSnapshots{}}
	e.GET("/api/intelligence", h.latest)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/intelligence", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
