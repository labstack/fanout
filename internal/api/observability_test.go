package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/observability"
)

type fakeQueries struct {
	traceScope      observability.Scope
	overviewScope   observability.Scope
	topologyScope   observability.Scope
	dependencyScope observability.Scope
}

func (f *fakeQueries) Dependencies(_ context.Context, scope observability.Scope, options observability.DependencyOptions) (observability.Result[observability.Dependencies], error) {
	f.dependencyScope = scope
	return observability.Result[observability.Dependencies]{Schema: observability.DependenciesSchema, Data: observability.Dependencies{Service: options.Service, Direction: options.Direction, MaxDepth: options.MaxDepth, MaxNodes: options.MaxNodes}}, nil
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

func (f *fakeQueries) Performance(_ context.Context, _ observability.Scope, _ observability.PerformanceOptions) (observability.Result[observability.Performance], error) {
	return observability.Result[observability.Performance]{Schema: observability.PerformanceSchema}, nil
}

func (f *fakeQueries) Trace(_ context.Context, scope observability.Scope, _, _ string, _ int) (observability.Result[observability.TraceDetail], error) {
	f.traceScope = scope
	return observability.Result[observability.TraceDetail]{Schema: observability.TraceSchema}, nil
}

func (f *fakeQueries) Logs(_ context.Context, _ observability.Scope, _, _, _ string, _ int) (observability.Result[observability.Logs], error) {
	return observability.Result[observability.Logs]{Schema: observability.LogsSchema}, nil
}

func TestOverviewRouteUsesDurationScope(t *testing.T) {
	for _, tt := range []struct {
		window string
		want   time.Duration
	}{{"15m", 15 * time.Minute}, {"720h", 30 * 24 * time.Hour}} {
		t.Run(tt.window, func(t *testing.T) {
			queries := &fakeQueries{}
			h := NewObservabilityHandler(queries, 30)
			h.now = func() time.Time { return time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC) }
			e := echo.New()
			h.Register(e.Group("/api/observability"))

			req := httptest.NewRequest(http.MethodGet, "/api/observability/overview?window="+tt.window+"&namespace=prod", nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if got := queries.overviewScope.End.Sub(queries.overviewScope.Start); got != tt.want {
				t.Fatalf("window = %s, want %s", got, tt.want)
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
		})
	}
}

func TestRouteRejectsInvalidWindow(t *testing.T) {
	h := NewObservabilityHandler(&fakeQueries{}, 30)
	e := echo.New()
	h.Register(e.Group("/api/observability"))
	req := httptest.NewRequest(http.MethodGet, "/api/observability/topology?window=forever", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestDependencyRouteForwardsScopeAndBounds(t *testing.T) {
	backend := &fakeQueries{}
	h := NewObservabilityHandler(backend, 30)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return now }
	e := echo.New()
	h.Register(e.Group("/api/observability"))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/observability/services/dependencies?window=15m&namespace=prod&service=checkout&direction=upstream&max_depth=3&max_nodes=25", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var result observability.Result[observability.Dependencies]
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Schema != observability.DependenciesSchema || result.Data.Service != "checkout" || result.Data.Direction != "upstream" || result.Data.MaxDepth != 3 || result.Data.MaxNodes != 25 || backend.dependencyScope.Namespace != "prod" || !backend.dependencyScope.Start.Equal(now.Add(-15*time.Minute)) {
		t.Fatalf("scope or parameters lost: %#v %#v", result, backend.dependencyScope)
	}
	for _, bounds := range []string{"max_depth=0", "max_nodes=-1", "max_nodes=invalid"} {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/observability/services/dependencies?service=checkout&"+bounds, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid %s accepted: %d", bounds, rec.Code)
		}
	}
}

// A canceled client is distinct from a server deadline or an execution fault.
// Wrapped backend errors must retain that distinction at the HTTP boundary.
func TestObservabilityQueryErrorClassification(t *testing.T) {
	var canceled *echo.HTTPError
	if !errors.As(mapQueryError(fmt.Errorf("query endpoints: %w", context.Canceled)), &canceled) || canceled.Code != 499 {
		t.Fatal("client cancellation must map to 499")
	}
	err := mapQueryError(fmt.Errorf("query endpoints: %w", context.DeadlineExceeded))
	var httpErr *echo.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("mapQueryError returned %T, want an *echo.HTTPError", err)
	}
	if httpErr.Code != http.StatusServiceUnavailable {
		t.Fatalf("a timed-out query maps to %d, want %d", httpErr.Code, http.StatusServiceUnavailable)
	}
	// A genuine failure still reads as one.
	var failure *echo.HTTPError
	if !errors.As(mapQueryError(errors.New("boom")), &failure) || failure.Code != http.StatusInternalServerError {
		t.Fatal("a real query failure no longer maps to 500")
	}
}

// Every registered route carries the deadline, not just the wrapper in
// isolation: dropping bounded() from Register is the mistake a refactor makes,
// and it left the whole suite green.
func TestRegisteredObservabilityRoutesCarryADeadline(t *testing.T) {
	deadlines := map[string]bool{}
	handler := &ObservabilityHandler{queries: deadlineProbe{seen: deadlines}, now: time.Now}
	e := echo.New()
	handler.Register(e.Group("/api/observability"))
	for _, path := range []string{"/api/observability/overview", "/api/observability/topology", "/api/observability/services/dependencies", "/api/observability/performance", "/api/observability/logs", "/api/observability/trace"} {
		recorder := httptest.NewRecorder()
		e.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path+"?window=1h", nil))
		if !deadlines[path] {
			t.Errorf("%s reached the query layer with no deadline", path)
		}
	}
}

// deadlineProbe records, per route, whether the context that reached it had a
// deadline. It answers every query with an empty result.
type deadlineProbe struct{ seen map[string]bool }

func (p deadlineProbe) Dependencies(ctx context.Context, _ observability.Scope, _ observability.DependencyOptions) (observability.Result[observability.Dependencies], error) {
	p.note(ctx, "/api/observability/services/dependencies")
	return observability.Result[observability.Dependencies]{}, nil
}

func (p deadlineProbe) note(ctx context.Context, path string) {
	if _, ok := ctx.Deadline(); ok {
		p.seen[path] = true
	}
}

func (p deadlineProbe) Overview(ctx context.Context, scope observability.Scope, limit int) (observability.Result[observability.Overview], error) {
	p.note(ctx, "/api/observability/overview")
	return observability.Result[observability.Overview]{}, nil
}

func (p deadlineProbe) Topology(ctx context.Context, scope observability.Scope, limit int) (observability.Result[observability.Topology], error) {
	p.note(ctx, "/api/observability/topology")
	return observability.Result[observability.Topology]{}, nil
}

func (p deadlineProbe) Performance(ctx context.Context, scope observability.Scope, opts observability.PerformanceOptions) (observability.Result[observability.Performance], error) {
	p.note(ctx, "/api/observability/performance")
	return observability.Result[observability.Performance]{}, nil
}

func (p deadlineProbe) Logs(ctx context.Context, scope observability.Scope, service, severity, search string, limit int) (observability.Result[observability.Logs], error) {
	p.note(ctx, "/api/observability/logs")
	return observability.Result[observability.Logs]{}, nil
}

func (p deadlineProbe) Trace(ctx context.Context, scope observability.Scope, traceID, service string, limit int) (observability.Result[observability.TraceDetail], error) {
	p.note(ctx, "/api/observability/trace")
	return observability.Result[observability.TraceDetail]{}, nil
}

// The handlers carry a deadline of their own, because the client's timeout is
// not something the server can rely on.
func TestObservabilityHandlersCarryADeadline(t *testing.T) {
	var seen time.Duration
	handler := (&ObservabilityHandler{}).bounded(func(c *echo.Context) error {
		deadline, ok := c.Request().Context().Deadline()
		if !ok {
			t.Fatal("handler ran with no deadline")
		}
		seen = time.Until(deadline)
		return nil
	})
	e := echo.New()
	request := httptest.NewRequest(http.MethodGet, "/api/observability/overview", nil)
	c := e.NewContext(request, httptest.NewRecorder())
	if err := handler(c); err != nil {
		t.Fatalf("bounded handler: %v", err)
	}
	if seen <= 0 || seen > observabilityDeadline {
		t.Fatalf("deadline left %s, want a positive value no greater than %s", seen, observabilityDeadline)
	}
}

func TestM2TraceAbsoluteWindow(t *testing.T) {
	queries := &fakeQueries{}
	h := NewObservabilityHandler(queries, 7)
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return now }
	e := echo.New()
	h.Register(e.Group("/api/observability"))
	cases := []struct {
		query string
		code  int
	}{
		{"?trace_id=abc&from=2026-10-01T12:00:00.123456789Z&to=2026-10-01T13:00:00.123456789Z&window=15m", 200},
		{"?from=2026-10-01T12:00:00Z", 400},
		{"?from=bad&to=2026-10-01T13:00:00Z", 400},
		{"?from=2026-10-01T13:00:00Z&to=2026-10-01T12:00:00Z", 400},
		{"?from=2026-09-01T12:00:00Z&to=2026-10-01T12:00:00Z", 400},
		{"?window=15m", 200},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/observability/trace"+tc.query, nil))
		if rec.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.query, rec.Code, rec.Body)
		}
		if tc.code == 200 {
			if tc.query == "?window=15m" {
				if !queries.traceScope.End.Equal(now) || queries.traceScope.End.Sub(queries.traceScope.Start) != 15*time.Minute {
					t.Fatalf("window changed: %+v", queries.traceScope)
				}
			} else {
				want := time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.UTC)
				if !queries.traceScope.Start.Equal(want) || queries.traceScope.End.Sub(want) != time.Hour {
					t.Fatalf("captured window lost: %+v", queries.traceScope)
				}
			}
		}
	}
}
