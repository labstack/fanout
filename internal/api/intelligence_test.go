package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/pitot/internal/auth"
	"github.com/labstack/pitot/internal/intelligence"
)

type fakeIntelligenceSnapshots struct {
	snapshot *intelligence.Snapshot
}

func (f fakeIntelligenceSnapshots) LatestSnapshot() *intelligence.Snapshot {
	return f.snapshot
}

func TestIntelligenceLatestReturnsSnapshot(t *testing.T) {
	want := &intelligence.Snapshot{
		GeneratedAt: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC),
		Timeframe:   "last_15m",
		Summary:     "System healthy (98/100).",
		HealthScore: 98,
		Insights:    []intelligence.Insight{{Type: "trend", Message: "Errors increasing"}},
		Patterns:    []intelligence.Pattern{{Template: "request failed", Count: 4}},
		Anomalies:   []intelligence.Anomaly{{ServiceName: "checkout", Current: 12}},
	}
	e := echo.New()
	h := &intelligenceHandler{snapshots: fakeIntelligenceSnapshots{snapshot: want}}
	e.GET("/api/insights", h.latest)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/insights", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got intelligence.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !reflect.DeepEqual(got, *want) {
		t.Fatalf("snapshot = %#v, want %#v", got, *want)
	}
}

func TestIntelligenceLatestReportsNotReady(t *testing.T) {
	e := echo.New()
	h := &intelligenceHandler{snapshots: fakeIntelligenceSnapshots{}}
	e.GET("/api/insights", h.latest)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/insights", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestInsightsRoutePreservesSnapshotAndRequiresTelemetryRead(t *testing.T) {
	s := newTestAuthServer(t)
	want := &intelligence.Snapshot{GeneratedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Timeframe: "last_15m", HealthScore: 75, Summary: "Errors increased", Insights: []intelligence.Insight{{Message: "Check checkout"}}, Patterns: []intelligence.Pattern{{Template: "failed", Count: 3}}, Anomalies: []intelligence.Anomaly{{ServiceName: "checkout"}}}
	RegisterIntelligenceRoutes(s.e, fakeIntelligenceSnapshots{snapshot: want})
	anonymous := httptest.NewRecorder()
	s.e.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/api/insights", nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", anonymous.Code)
	}
	viewer, err := s.users.CreateWithAudit("insights@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, sessionRequest(http.MethodGet, "/api/insights", nil, s.login(t, viewer)))
	var got intelligence.Snapshot
	if rec.Code != http.StatusOK {
		t.Fatalf("insights = %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, *want) {
		t.Fatalf("snapshot = %#v, want %#v", got, *want)
	}
}
