package mcp

import (
	"context"
	"errors"
	"fmt"
	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/panel"
	"strings"
	"testing"
	"time"
)

type pathErrorObservability struct {
	Observability
	err error
}

func (p pathErrorObservability) Trace(context.Context, observability.Scope, string, string, int) (observability.Result[observability.TraceDetail], error) {
	if p.err != nil {
		return observability.Result[observability.TraceDetail]{}, p.err
	}
	return observability.Result[observability.TraceDetail]{}, errors.New("IO error at /srv/telemetry/private/batch.parquet")
}
func TestFinalFixMCPRedactsEnginePaths(t *testing.T) {
	s := New(pathErrorObservability{}, nil, nil, "test")
	_, _, err := s.trace(t.Context(), nil, TraceInput{})
	if err == nil || strings.Contains(err.Error(), "/srv/") || !strings.Contains(err.Error(), "<path>") {
		t.Fatalf("MCP error=%v", err)
	}
}

type boundedWriteValidator struct{ t *testing.T }

func (v boundedWriteValidator) Validate(ctx context.Context, _ *panel.Dashboard) error {
	d, ok := ctx.Deadline()
	if !ok || time.Until(d) > 20*time.Second {
		v.t.Errorf("unbounded MCP write: %v %v", d, ok)
	}
	return context.DeadlineExceeded
}
func TestFinalFixMCPWriteValidationDeadline(t *testing.T) {
	s := newToolServer(t, boundedWriteValidator{t: t}, nil)
	_, _, err := s.dashboardCreate(t.Context(), requestFor("owner"), DashboardCreateInput{Dashboard: textDashboard("Deadline")})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error=%v", err)
	}
}

func TestFinalFixMCPRedactsWrappedDeadlinePaths(t *testing.T) {
	raw := fmt.Errorf("failed to read /srv/telemetry/a.parquet: %w", context.DeadlineExceeded)
	s := New(pathErrorObservability{err: raw}, nil, nil, "test")
	_, _, err := s.trace(t.Context(), nil, TraceInput{})
	if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "/srv/") {
		t.Fatalf("MCP deadline=%v", err)
	}
}
