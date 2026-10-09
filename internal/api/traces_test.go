package api

import (
	"context"
	"errors"
	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/observability"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type traceProbe struct {
	scope   observability.Scope
	id      string
	bounded bool
	err     error
}

func (p *traceProbe) Trace(ctx context.Context, s observability.Scope, id, service string, limit int) (observability.Result[observability.TraceDetail], error) {
	p.scope = s
	p.id = id
	_, p.bounded = ctx.Deadline()
	return observability.Result[observability.TraceDetail]{}, p.err
}
func TestTraceRoutePreservesCapturedNanoseconds(t *testing.T) {
	p := &traceProbe{}
	s := newTestAuthServer(t)
	if _, err := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	viewer, err := s.users.CreateWithAudit("viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	RegisterTraceRoutes(s.e, p, 7)
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, sessionRequest(http.MethodGet, "/api/traces/abc?namespace=shop&from=2026-10-01T12:00:00.123456789Z&to=2026-10-01T13:00:00.123456789Z", nil, s.login(t, viewer)))
	if rec.Code != 200 || p.id != "abc" || p.scope.Namespace != "shop" || p.scope.Start.Nanosecond() != 123456789 || p.scope.End.Sub(p.scope.Start) != time.Hour || !p.bounded {
		t.Fatalf("%d %+v", rec.Code, p)
	}
}

func TestTraceRouteRejectsInvalidScopesAndRetiredPaths(t *testing.T) {
	s := newTestAuthServer(t)
	admin, err := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	p := &traceProbe{}
	RegisterTraceRoutes(s.e, p, 7)
	cookie := s.login(t, admin)
	valid := "?from=2026-10-01T12:00:00Z&to=2026-10-01T13:00:00Z"
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/traces/abc", 400}, {"GET", "/api/traces/abc?from=2026-10-01T12:00:00Z", 400},
		{"GET", "/api/traces/abc?from=2026-10-01T12:00:00Z&to=2026-10-09T12:00:00Z", 400},
		{"GET", "/api/traces/abc" + valid + "&limit=0", 400}, {"GET", "/api/traces/abc" + valid + "&limit=501", 400},
		{"GET", "/api/traces/abc" + valid + "&limit=NaN", 400}, {"GET", "/api/traces/abc?from=2026-10-02T12:00:00Z&to=2026-10-01T12:00:00Z", 400},
		{"POST", "/api/traces/abc" + valid, 405}, {"GET", "/api/traces/", 404}, {"GET", "/api/traces/abc/extra" + valid, 404},
		{"GET", "/api/observability/trace" + valid, 404}, {"GET", "/api/observability/overview", 404}, {"GET", "/api/observability/performance", 404},
		{"GET", "/api/observability/topology", 404}, {"GET", "/api/observability/logs", 404}, {"GET", "/api/observability/dependencies", 404},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.e.ServeHTTP(rec, sessionRequest(tc.method, tc.path, nil, cookie))
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, httptest.NewRequest("GET", "/api/traces/abc"+valid, nil))
	if rec.Code != 401 {
		t.Fatalf("anonymous=%d", rec.Code)
	}
}
func TestTraceRouteClassifiesQueryFailures(t *testing.T) {
	s := newTestAuthServer(t)
	admin, err := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	p := &traceProbe{}
	RegisterTraceRoutes(s.e, p, 7)
	cookie := s.login(t, admin)
	for _, tc := range []struct {
		err  error
		want int
	}{{context.Canceled, 499}, {context.DeadlineExceeded, 504}, {observability.ErrInvalidScope, 400}, {observability.ErrInvalidLimit, 400}, {errors.New("engine failure"), 500}} {
		p.err = tc.err
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, sessionRequest("GET", "/api/traces/abc?from=2026-10-01T12:00:00Z&to=2026-10-01T13:00:00Z", nil, cookie))
		if rec.Code != tc.want {
			t.Fatalf("err=%v status=%d", tc.err, rec.Code)
		}
	}
}
