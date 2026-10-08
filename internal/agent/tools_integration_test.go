package agent

import (
	"context"
	"encoding/json"
	"github.com/labstack/fanout/internal/dashboard"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

func TestToolRegistryInjectsAuthenticatedBuildOrigin(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	var meta mcp.Meta
	mcp.AddTool(server, &mcp.Tool{Name: "capture"}, func(_ context.Context, req *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		meta = req.Params.Meta
		return nil, map[string]any{}, nil
	})
	registry, err := NewToolRegistry(t.Context(), server)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	origin := dashboard.BuildOrigin{ThreadID: "server-thread", MessageID: "server-user", RequestExcerpt: "Actual request"}
	ctx := dashboard.WithBuildOrigin(dashboard.WithOwner(t.Context(), "owner"), origin)
	_, err = registry.Execute(ctx, ToolCall{Name: "capture", Input: `{"_meta":{"io.fanout/build-origin":{"thread_id":"spoof"}}}`})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(meta[dashboard.BuildOriginMetaKey])
	var got dashboard.BuildOrigin
	_ = json.Unmarshal(raw, &got)
	if meta[dashboard.OwnerMetaKey] != "owner" || got != origin {
		t.Fatalf("metadata=%+v", meta)
	}
	_, err = registry.Execute(dashboard.WithBuildOrigin(t.Context(), origin), ToolCall{Name: "capture", Input: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if meta[dashboard.BuildOriginMetaKey] != nil {
		t.Fatal("origin without authenticated owner")
	}
}
