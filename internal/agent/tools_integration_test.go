package agent

import (
	"context"
	"testing"

	"github.com/labstack/fanout/internal/intelligence"
	fanoutmcp "github.com/labstack/fanout/internal/mcp"
	"github.com/labstack/fanout/internal/observability"
)

type registryQueries struct{}

type registryIntelligence struct{}

func (registryIntelligence) LatestSnapshot() *intelligence.IntelligenceSnapshot {
	return &intelligence.IntelligenceSnapshot{Summary: "healthy", HealthScore: 100}
}

func (registryQueries) Overview(context.Context, observability.Scope, int) (observability.Result[observability.Overview], error) {
	return observability.Result[observability.Overview]{}, nil
}

func (registryQueries) Topology(context.Context, observability.Scope, int) (observability.Result[observability.Topology], error) {
	return observability.Result[observability.Topology]{}, nil
}

func (registryQueries) Dependencies(context.Context, observability.Scope, observability.DependencyOptions) (observability.Result[observability.Dependencies], error) {
	return observability.Result[observability.Dependencies]{}, nil
}

func (registryQueries) Trace(context.Context, observability.Scope, string, string, int) (observability.Result[observability.TraceDetail], error) {
	return observability.Result[observability.TraceDetail]{}, nil
}

func TestToolRegistryNegotiatesMCPApps(t *testing.T) {
	server := fanoutmcp.NewWithIntelligence(registryQueries{}, nil, nil, registryIntelligence{}, "test")
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
		"query_telemetry":            "ui://fanout/panels.html",
		"get_observability_overview": "ui://fanout/panels.html",
		"get_service_topology":       "ui://fanout/panels.html",
		"get_service_performance":    "ui://fanout/panels.html",
		"inspect_trace":              "ui://fanout/panels.html",
		"search_logs":                "ui://fanout/panels.html",
	}
	if len(registry.apps) != len(want) {
		t.Fatalf("registered MCP apps = %v, want %v", registry.apps, want)
	}
	for tool, resourceURI := range want {
		if got := registry.apps[tool]; got != resourceURI {
			t.Errorf("app URI for %s = %q, want %q", tool, got, resourceURI)
		}
	}
	foundIntelligence := false
	for _, definition := range registry.Definitions() {
		if definition.Name == "get_intelligence_snapshot" {
			foundIntelligence = true
			break
		}
	}
	if !foundIntelligence {
		t.Fatal("get_intelligence_snapshot was not registered for the agent")
	}
}
