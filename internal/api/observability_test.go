package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/observability"
)

type fakeQueries struct {
	overviewScope observability.Scope
	topologyScope observability.Scope
}

func (f *fakeQueries) Overview(_ context.Context, scope observability.Scope, _ int) (observability.Result[observability.Overview], error) {
	f.overviewScope = scope
	return observability.Result[observability.Overview]{
		Schema: observability.OverviewSchema,
		Data:   observability.Overview{Health: observability.HealthHealthy},
	}, nil
}

func (f *fakeQueries) Topology(_ context.Context, scope observability.Scope, _ int) (observability.Result[observability.Topology], error) {
	f.topologyScope = scope
	return observability.Result[observability.Topology]{
		Schema: observability.TopologySchema,
		Data:   observability.Topology{},
	}, nil
}

func TestOverviewRouteUsesDurationScope(t *testing.T) {
	queries := &fakeQueries{}
	h := NewObservabilityHandler(queries)
	h.now = func() time.Time { return time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC) }
	e := echo.New()
	h.Register(e.Group("/api/observability"))

	req := httptest.NewRequest(http.MethodGet, "/api/observability/overview?window=15m&namespace=prod", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := queries.overviewScope.End.Sub(queries.overviewScope.Start); got != 15*time.Minute {
		t.Fatalf("window = %s, want 15m", got)
	}
	if queries.overviewScope.Namespace != "prod" {
		t.Fatalf("namespace = %q, want prod", queries.overviewScope.Namespace)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["schema"] != observability.OverviewSchema {
		t.Fatalf("schema = %v", body["schema"])
	}
}

func TestRouteRejectsInvalidWindow(t *testing.T) {
	h := NewObservabilityHandler(&fakeQueries{})
	e := echo.New()
	h.Register(e.Group("/api/observability"))
	req := httptest.NewRequest(http.MethodGet, "/api/observability/topology?window=forever", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
