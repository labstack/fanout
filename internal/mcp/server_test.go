package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/observability"
	controlstore "github.com/labstack/fanout/internal/store"
	mcpgoauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeObservability struct {
	scope observability.Scope
}

func TestDashboardToolsUseAuthenticatedOwner(t *testing.T) {
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	if _, err := database.DB.ExecContext(ctx, `INSERT INTO users(id,email,name,role,active) VALUES('owner','owner@example.test','Owner','admin',1)`); err != nil {
		t.Fatal(err)
	}
	server := New(&fakeObservability{}, dashboard.New(database.DB), "test")
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}, Extra: &mcp.RequestExtra{TokenInfo: &mcpgoauth.TokenInfo{UserID: "owner", Scopes: []string{dashboard.OAuthScope}}}}
	state := dashboard.State{Filters: dashboard.Filters{Window: "1h"}, Widgets: []dashboard.Widget{{ID: "health", Type: "overview", Title: "System health", Enabled: true}}, Layout: []dashboard.Layout{{I: "health", X: 0, Y: 0, W: 12, H: 3}}}
	_, output, err := server.dashboardCreate(ctx, req, DashboardCreateInput{Name: "AI overview", State: state})
	if err != nil {
		t.Fatal(err)
	}
	if output.Dashboard.Name != "AI overview" {
		t.Fatalf("dashboard = %#v", output.Dashboard)
	}
	_, listed, err := server.dashboardList(ctx, req, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Dashboards) != 1 {
		t.Fatalf("dashboard count = %d, want 1", len(listed.Dashboards))
	}
	if _, _, err := server.dashboardList(ctx, &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}}, struct{}{}); err == nil {
		t.Fatal("unauthenticated dashboard tool succeeded")
	}
}

func (f *fakeObservability) Overview(_ context.Context, scope observability.Scope, _ int) (observability.Result[observability.Overview], error) {
	f.scope = scope
	return observability.Result[observability.Overview]{
		Schema:  observability.OverviewSchema,
		Summary: "2 services: 0 unhealthy, 0 degraded, 2 healthy",
		Data:    observability.Overview{ServiceCount: 2},
	}, nil
}

func (f *fakeObservability) Topology(_ context.Context, scope observability.Scope, _ int) (observability.Result[observability.Topology], error) {
	f.scope = scope
	return observability.Result[observability.Topology]{
		Schema:  observability.TopologySchema,
		Summary: "2 services connected by 1 dependency edge",
		Data:    observability.Topology{Edges: []observability.Edge{{Caller: "api", Callee: "db"}}},
	}, nil
}

func (f *fakeObservability) Performance(_ context.Context, scope observability.Scope, _ string, _ int) (observability.Result[observability.Performance], error) {
	f.scope = scope
	return observability.Result[observability.Performance]{Schema: observability.PerformanceSchema, Summary: "performance"}, nil
}

func (f *fakeObservability) Trace(_ context.Context, scope observability.Scope, _, _ string, _ int) (observability.Result[observability.TraceDetail], error) {
	f.scope = scope
	return observability.Result[observability.TraceDetail]{Schema: observability.TraceSchema, Summary: "trace"}, nil
}

func (f *fakeObservability) Logs(_ context.Context, scope observability.Scope, _, _, _ string, _ int) (observability.Result[observability.Logs], error) {
	f.scope = scope
	return observability.Result[observability.Logs]{Schema: observability.LogsSchema, Summary: "logs"}, nil
}

func TestOverviewReturnsSummaryAndStructuredOutput(t *testing.T) {
	backend := &fakeObservability{}
	s := New(backend, nil, "test")
	s.now = func() time.Time { return time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC) }
	result, output, err := s.overview(context.Background(), nil, QueryInput{Window: "15m", Namespace: "prod"})
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if len(result.Content) != 1 || output.Schema != observability.OverviewSchema {
		t.Fatalf("unexpected result: %#v %#v", result, output)
	}
	if got := backend.scope.End.Sub(backend.scope.Start); got != 15*time.Minute {
		t.Fatalf("window = %s, want 15m", got)
	}
}

func TestInvalidWindowIsToolError(t *testing.T) {
	s := New(&fakeObservability{}, nil, "test")
	if _, _, err := s.topology(context.Background(), nil, QueryInput{Window: "later"}); err == nil {
		t.Fatal("expected invalid window error")
	}
}

func TestToolsAdvertiseReadableMCPApps(t *testing.T) {
	server := New(&fakeObservability{}, nil, "test")
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverConnected := make(chan error, 1)
	go func() {
		_, err := server.MCP().Connect(context.Background(), serverTransport, nil)
		serverConnected <- err
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := <-serverConnected; err != nil {
		t.Fatal(err)
	}

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 5 {
		t.Fatalf("tool count = %d, want 5", len(listed.Tools))
	}
	resources := map[string]bool{}
	for _, tool := range listed.Tools {
		ui, ok := tool.Meta["ui"].(map[string]any)
		if !ok {
			t.Fatalf("tool %s has no nested ui metadata: %#v", tool.Name, tool.Meta)
		}
		uri, _ := ui["resourceUri"].(string)
		if !strings.HasPrefix(uri, "ui://") {
			t.Fatalf("tool %s resource URI = %q", tool.Name, uri)
		}
		if legacy, _ := tool.Meta["ui/resourceUri"].(string); legacy != uri {
			t.Fatalf("tool %s legacy resource URI = %q, want %q", tool.Name, legacy, uri)
		}
		resources[uri] = true
	}
	if len(resources) != 5 {
		t.Fatalf("resource count = %d, want 5", len(resources))
	}
	for uri := range resources {
		result, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatalf("read %s: %v", uri, err)
		}
		if len(result.Contents) != 1 || result.Contents[0].MIMEType != mcpAppMIME || !strings.Contains(result.Contents[0].Text, "<html") {
			t.Fatalf("invalid MCP App resource %s: %#v", uri, result.Contents)
		}
	}
}
