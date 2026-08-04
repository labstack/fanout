package agent

import (
	"context"
	"testing"

	fanoutmcp "github.com/labstack/fanout/internal/mcp"
	"github.com/labstack/fanout/internal/observability"
)

type registryQueries struct{}

func (registryQueries) Overview(context.Context, observability.Scope, int) (observability.Result[observability.Overview], error) {
	return observability.Result[observability.Overview]{}, nil
}

func (registryQueries) Topology(context.Context, observability.Scope, int) (observability.Result[observability.Topology], error) {
	return observability.Result[observability.Topology]{}, nil
}

func (registryQueries) Performance(context.Context, observability.Scope, string, int) (observability.Result[observability.Performance], error) {
	return observability.Result[observability.Performance]{}, nil
}

func (registryQueries) Trace(context.Context, observability.Scope, string, string, int) (observability.Result[observability.TraceDetail], error) {
	return observability.Result[observability.TraceDetail]{}, nil
}

func (registryQueries) Logs(context.Context, observability.Scope, string, string, string, int) (observability.Result[observability.Logs], error) {
	return observability.Result[observability.Logs]{}, nil
}

func TestToolRegistryNegotiatesMCPApps(t *testing.T) {
	server := fanoutmcp.New(registryQueries{}, nil, "test")
	registry, err := NewToolRegistry(context.Background(), server.MCP())
	if err != nil {
		t.Fatalf("NewToolRegistry: %v", err)
	}
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	want := map[string]string{
		"observability_overview": "ui://fanout/observability-overview.html",
		"service_topology":       "ui://fanout/service-topology.html",
		"service_performance":    "ui://fanout/service-performance.html",
		"trace_detail":           "ui://fanout/trace-detail.html",
		"search_logs":            "ui://fanout/log-explorer.html",
	}
	if len(registry.apps) != len(want) {
		t.Fatalf("registered MCP apps = %v, want %v", registry.apps, want)
	}
	for tool, resourceURI := range want {
		if got := registry.apps[tool]; got != resourceURI {
			t.Errorf("app URI for %s = %q, want %q", tool, got, resourceURI)
		}
	}
}
