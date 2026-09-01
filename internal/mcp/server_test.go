package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/intelligence"
	"github.com/labstack/fanout/internal/observability"
	controlstore "github.com/labstack/fanout/internal/store"
	mcpgoauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeObservability struct {
	scope observability.Scope
}

type fakeIntelligence struct {
	snapshot *intelligence.IntelligenceSnapshot
}

func (f fakeIntelligence) LatestSnapshot() *intelligence.IntelligenceSnapshot {
	return f.snapshot
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

func TestDashboardOwnerIgnoresSpoofedMetaWhenTokenPresent(t *testing.T) {
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	for _, owner := range []string{"owner", "attacker"} {
		if _, err := database.DB.ExecContext(ctx, `INSERT INTO users(id,email,name,role,active) VALUES(?,?,?,'admin',1)`, owner, owner+"@example.test", owner); err != nil {
			t.Fatal(err)
		}
	}
	server := New(&fakeObservability{}, dashboard.New(database.DB), "test")
	// A remote client always carries TokenInfo (ProtectMCP guarantees it), so a
	// spoofed _meta owner key must lose to the token identity.
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{dashboard.OwnerMetaKey: "attacker"}},
		Extra:  &mcp.RequestExtra{TokenInfo: &mcpgoauth.TokenInfo{UserID: "owner", Scopes: []string{dashboard.OAuthScope}}},
	}
	state := dashboard.State{Filters: dashboard.Filters{Window: "1h"}, Widgets: []dashboard.Widget{{ID: "health", Type: "overview", Title: "System health", Enabled: true}}, Layout: []dashboard.Layout{{I: "health", X: 0, Y: 0, W: 12, H: 3}}}
	if _, _, err := server.dashboardCreate(ctx, req, DashboardCreateInput{Name: "Spoof check", State: state}); err != nil {
		t.Fatal(err)
	}
	var ownerID string
	if err := database.DB.QueryRowContext(ctx, `SELECT owner_id FROM dashboards WHERE name='Spoof check'`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if ownerID != "owner" {
		t.Fatalf("dashboard owner = %q, want token identity \"owner\"", ownerID)
	}
}

func TestDashboardOwnerRejectsTokenMissingDashboardScope(t *testing.T) {
	server := New(&fakeObservability{}, nil, "test")
	// Even with a spoofed _meta owner key, a token lacking the dashboard scope
	// must be rejected outright — the meta fallback never applies once
	// TokenInfo is present.
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{dashboard.OwnerMetaKey: "owner"}},
		Extra:  &mcp.RequestExtra{TokenInfo: &mcpgoauth.TokenInfo{UserID: "owner", Scopes: []string{"mcp:read"}}},
	}
	_, _, err := server.dashboardList(context.Background(), req, struct{}{})
	if err == nil || !strings.Contains(err.Error(), "dashboard permission") {
		t.Fatalf("scope-less token error = %v, want dashboard permission rejection", err)
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
	for _, tt := range []struct {
		window string
		want   time.Duration
	}{{"15m", 15 * time.Minute}, {"720h", 30 * 24 * time.Hour}} {
		t.Run(tt.window, func(t *testing.T) {
			backend := &fakeObservability{}
			s := New(backend, nil, "test")
			s.now = func() time.Time { return time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC) }
			result, output, err := s.overview(context.Background(), nil, QueryInput{Window: tt.window, Namespace: "prod"})
			if err != nil {
				t.Fatalf("overview: %v", err)
			}
			if len(result.Content) != 1 || output.Schema != observability.OverviewSchema {
				t.Fatalf("unexpected result: %#v %#v", result, output)
			}
			if got := backend.scope.End.Sub(backend.scope.Start); got != tt.want {
				t.Fatalf("window = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestInvalidWindowIsToolError(t *testing.T) {
	s := New(&fakeObservability{}, nil, "test")
	if _, _, err := s.topology(context.Background(), nil, QueryInput{Window: "later"}); err == nil {
		t.Fatal("expected invalid window error")
	}
}

func TestIntelligenceSnapshotReturnsStructuredOutput(t *testing.T) {
	want := &intelligence.IntelligenceSnapshot{
		GeneratedAt: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC),
		Timeframe:   "last_15m",
		Summary:     "One anomaly requires attention.",
		HealthScore: 75,
		Anomalies: []intelligence.Anomaly{{
			Type:        intelligence.AnomalyLatencyDegradation,
			ServiceName: "checkout",
		}},
	}
	s := NewWithIntelligence(&fakeObservability{}, nil, fakeIntelligence{snapshot: want}, "test")
	result, output, err := s.intelligenceSnapshot(context.Background(), nil, struct{}{})
	if err != nil {
		t.Fatalf("intelligence snapshot: %v", err)
	}
	if output.GeneratedAt != want.GeneratedAt || output.HealthScore != want.HealthScore || len(output.Anomalies) != 1 {
		t.Fatalf("output = %#v, want %#v", output, *want)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content = %#v", result.Content)
	}
}

func TestIntelligenceSnapshotReportsNotReady(t *testing.T) {
	s := NewWithIntelligence(&fakeObservability{}, nil, fakeIntelligence{}, "test")
	if _, _, err := s.intelligenceSnapshot(context.Background(), nil, struct{}{}); err == nil {
		t.Fatal("expected not-ready error")
	}
}

func TestToolsAdvertiseReadableMCPApps(t *testing.T) {
	t.Run("negotiated client", func(t *testing.T) {
		server := New(&fakeObservability{}, nil, "test")
		session := connectTestClient(t, server, &mcp.ClientCapabilities{Extensions: map[string]any{
			mcpUIExtension: map[string]any{"mimeTypes": []string{mcpAppMIME}},
		}})

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
			if len(result.Contents) != 1 || result.Contents[0].URI != uri || result.Contents[0].MIMEType != mcpAppMIME || !strings.Contains(result.Contents[0].Text, "<html") {
				t.Fatalf("invalid MCP App resource %s: %#v", uri, result.Contents)
			}
			ui, ok := result.Contents[0].Meta["ui"].(map[string]any)
			if !ok || ui["csp"] == nil {
				t.Fatalf("MCP App resource %s has no CSP metadata: %#v", uri, result.Contents[0].Meta)
			}
		}
	})

	t.Run("client without extension", func(t *testing.T) {
		server := New(&fakeObservability{}, nil, "test")
		session := connectTestClient(t, server, nil)
		listed, err := session.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(listed.Tools) != 5 {
			t.Fatalf("tool count = %d, want 5", len(listed.Tools))
		}
		for _, tool := range listed.Tools {
			if _, ok := tool.Meta["ui"]; ok {
				t.Fatalf("tool %s advertised unnegotiated nested UI metadata: %#v", tool.Name, tool.Meta)
			}
			if _, ok := tool.Meta["ui/resourceUri"]; ok {
				t.Fatalf("tool %s advertised unnegotiated legacy UI metadata: %#v", tool.Name, tool.Meta)
			}
		}
	})
}

func TestServerAdvertisesInstructionsAndStaticCacheHints(t *testing.T) {
	server := New(&fakeObservability{}, nil, "test")
	session := connectTestClient(t, server, nil)
	if instructions := session.InitializeResult().Instructions; !strings.Contains(instructions, "observability_overview") || !strings.Contains(instructions, "authenticated user") {
		t.Fatalf("server instructions = %q", instructions)
	}

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if tools.TTLMs != staticCatalogTTLMs || tools.CacheScope != "public" {
		t.Fatalf("tools cache hints = %d %q", tools.TTLMs, tools.CacheScope)
	}
	for _, tool := range tools.Tools {
		if tool.OutputSchema == nil {
			t.Fatalf("tool %s has no structured output schema", tool.Name)
		}
	}
	called, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "observability_overview",
		Arguments: map[string]any{
			"window": "15m",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if called.IsError || called.StructuredContent == nil || len(called.Content) == 0 {
		t.Fatalf("overview lacks portable text and structured fallbacks: %#v", called)
	}

	resources, err := session.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resources.TTLMs != staticCatalogTTLMs || resources.CacheScope != "public" {
		t.Fatalf("resources cache hints = %d %q", resources.TTLMs, resources.CacheScope)
	}
	if len(resources.Resources) == 0 {
		t.Fatal("server advertised no MCP App resources")
	}
	read, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: resources.Resources[0].URI})
	if err != nil {
		t.Fatal(err)
	}
	if read.TTLMs != staticCatalogTTLMs || read.CacheScope != "public" {
		t.Fatalf("resource read cache hints = %d %q", read.TTLMs, read.CacheScope)
	}
	templates, err := session.ListResourceTemplates(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if templates.TTLMs != staticCatalogTTLMs || templates.CacheScope != "public" {
		t.Fatalf("resource templates cache hints = %d %q", templates.TTLMs, templates.CacheScope)
	}
}

func connectTestClient(t *testing.T, server *Server, capabilities *mcp.ClientCapabilities) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverConnected := make(chan error, 1)
	go func() {
		_, err := server.MCP().Connect(context.Background(), serverTransport, nil)
		serverConnected <- err
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, &mcp.ClientOptions{Capabilities: capabilities})
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := <-serverConnected; err != nil {
		t.Fatal(err)
	}
	return session
}
