package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/intelligence"
	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/panel"
	controlstore "github.com/labstack/fanout/internal/store"
	mcpgoauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeObservability struct {
	scope observability.Scope
}

// structural validates without DuckDB; filter checks live in internal/panel.
type structural struct{}

func (structural) Validate(_ context.Context, d *panel.Dashboard) error {
	panel.Normalize(d)
	if problems := panel.Validate(d); len(problems) > 0 {
		return problems
	}
	return nil
}

func withName(d panel.Dashboard, name string) panel.Dashboard {
	d.Name = name
	return d
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
	if _, err := database.DB.ExecContext(ctx, `INSERT INTO users(id,email,display_name,role,status) VALUES('owner','owner@example.test','Owner','admin','active')`); err != nil {
		t.Fatal(err)
	}
	server := NewWithIntelligence(&fakeObservability{}, dashboard.New(database.DB, structural{}), nil, nil, "test")
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}, Extra: &mcp.RequestExtra{TokenInfo: &mcpgoauth.TokenInfo{UserID: "owner", Scopes: []string{dashboard.OAuthScope}}}}
	spec := panel.Dashboard{Panels: []panel.Panel{{ID: "notes", Title: "Notes", Viz: "text", Content: "hello"}}}
	_, output, err := server.dashboardCreate(ctx, req, DashboardCreateInput{Dashboard: withName(spec, "AI overview")})
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
		if _, err := database.DB.ExecContext(ctx, `INSERT INTO users(id,email,display_name,role,status) VALUES(?,?,?,'admin','active')`, owner, owner+"@example.test", owner); err != nil {
			t.Fatal(err)
		}
	}
	server := NewWithIntelligence(&fakeObservability{}, dashboard.New(database.DB, structural{}), nil, nil, "test")
	// A remote client always carries TokenInfo (ProtectMCP guarantees it), so a
	// spoofed _meta owner key must lose to the token identity.
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{dashboard.OwnerMetaKey: "attacker"}},
		Extra:  &mcp.RequestExtra{TokenInfo: &mcpgoauth.TokenInfo{UserID: "owner", Scopes: []string{dashboard.OAuthScope}}},
	}
	spec := panel.Dashboard{Panels: []panel.Panel{{ID: "notes", Title: "Notes", Viz: "text", Content: "hello"}}}
	if _, _, err := server.dashboardCreate(ctx, req, DashboardCreateInput{Dashboard: withName(spec, "Spoof check")}); err != nil {
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
	server := NewWithIntelligence(&fakeObservability{}, nil, nil, nil, "test")
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

func (f *fakeObservability) Dependencies(_ context.Context, scope observability.Scope, options observability.DependencyOptions) (observability.Result[observability.Dependencies], error) {
	f.scope = scope
	return observability.Result[observability.Dependencies]{Schema: observability.DependenciesSchema, Data: observability.Dependencies{Service: options.Service, Direction: options.Direction, MaxDepth: options.MaxDepth, MaxNodes: options.MaxNodes}}, nil
}

func (f *fakeObservability) Trace(_ context.Context, scope observability.Scope, _, _ string, _ int) (observability.Result[observability.TraceDetail], error) {
	f.scope = scope
	return observability.Result[observability.TraceDetail]{Schema: observability.TraceSchema, Summary: "trace", Data: observability.TraceDetail{Spans: nil}}, nil
}

func TestOverviewReturnsSummaryAndStructuredOutput(t *testing.T) {
	for _, tt := range []struct {
		window string
		want   time.Duration
	}{{"15m", 15 * time.Minute}, {"720h", 30 * 24 * time.Hour}} {
		t.Run(tt.window, func(t *testing.T) {
			s := newPanelServer(t)
			result, output, err := s.overview(t.Context(), nil, QueryInput{Window: tt.window, Namespace: "prod"})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Content) != 1 || output.Dashboard.Version != 1 || len(output.Results) != 1 {
				t.Fatalf("result=%+v", output)
			}
			if got := output.Dashboard.Time.To.Sub(*output.Dashboard.Time.From); got != tt.want {
				t.Fatalf("window=%s", got)
			}
		})
	}
}

func TestInvalidWindowIsToolError(t *testing.T) {
	s := NewWithIntelligence(&fakeObservability{}, nil, nil, nil, "test")
	if _, _, err := s.topology(context.Background(), nil, QueryInput{Window: "later"}); err == nil {
		t.Fatal("expected invalid window error")
	}
}

func TestDependencyToolForwardsScopeAndBounds(t *testing.T) {
	backend := &fakeObservability{}
	server := NewWithIntelligence(backend, nil, nil, nil, "test")
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	server.now = func() time.Time { return now }
	session := connectTestClient(t, server, nil)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_service_dependencies", Arguments: map[string]any{
		"window": "15m", "namespace": "prod", "service": "checkout", "direction": "upstream", "max_depth": 3, "max_nodes": 25,
	}})
	if err != nil || result.IsError {
		t.Fatalf("dependency call: %#v %v", result, err)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var output observability.Result[observability.Dependencies]
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	if output.Schema != observability.DependenciesSchema || output.Data.Service != "checkout" || output.Data.Direction != "upstream" || output.Data.MaxDepth != 3 || output.Data.MaxNodes != 25 || backend.scope.Namespace != "prod" || !backend.scope.Start.Equal(now.Add(-15*time.Minute)) {
		t.Fatalf("scope or parameters lost: %#v %#v", output, backend.scope)
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
	s := NewWithIntelligence(&fakeObservability{}, nil, nil, fakeIntelligence{snapshot: want}, "test")
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
	s := NewWithIntelligence(&fakeObservability{}, nil, nil, fakeIntelligence{}, "test")
	if _, _, err := s.intelligenceSnapshot(context.Background(), nil, struct{}{}); err == nil {
		t.Fatal("expected not-ready error")
	}
}

func TestToolsAdvertiseReadableMCPApps(t *testing.T) {
	t.Run("negotiated client", func(t *testing.T) {
		server := NewWithIntelligence(&fakeObservability{}, nil, nil, nil, "test")
		session := connectTestClient(t, server, &mcp.ClientCapabilities{Extensions: map[string]any{
			mcpUIExtension: map[string]any{"mimeTypes": []string{mcpAppMIME}},
		}})

		listed, err := session.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(listed.Tools) != 12 {
			t.Fatalf("tool count = %d, want 12", len(listed.Tools))
		}
		resources := map[string]bool{}
		for _, tool := range listed.Tools {
			if tool.Name == "get_service_dependencies" {
				if _, ok := tool.Meta["ui"]; ok {
					t.Fatal("dependency traversal advertised a UI resource")
				}
				continue
			}
			if tool.Name == "query_panel_fragment" || tool.Name == "list_panel_exemplars" || tool.Name == "resolve_panel_variables" {
				if !appOnly(tool.Meta) {
					t.Fatalf("not app-only: %s", tool.Name)
				}
				continue
			}
			if tool.Name == "get_telemetry_schema" || tool.Name == "preview_panels" {
				// Authoring tools for the agent; their results have no view.
				if _, ok := tool.Meta["ui"]; ok {
					t.Fatalf("tool %s advertised a UI resource", tool.Name)
				}
				continue
			}
			ui, ok := tool.Meta["ui"].(map[string]any)
			if !ok {
				t.Fatalf("tool %s has no nested ui metadata: %#v", tool.Name, tool.Meta)
			}
			uri, _ := ui["resourceUri"].(string)
			if uri != panelsAppURI {
				t.Fatalf("tool %s resource URI = %q", tool.Name, uri)
			}
			if _, exists := tool.Meta["ui/resourceUri"]; exists {
				t.Fatal("flat UI metadata present")
			}
			resources[uri] = true
		}
		if len(resources) != 1 {
			t.Fatalf("resource count = %d, want 1", len(resources))
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
		server := NewWithIntelligence(&fakeObservability{}, nil, nil, nil, "test")
		session := connectTestClient(t, server, nil)
		listed, err := session.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range listed.Tools {
			if tool.Name == "query_panel_fragment" || tool.Name == "list_panel_exemplars" || tool.Name == "resolve_panel_variables" {
				t.Fatalf("app helper exposed without negotiation: %s", tool.Name)
			}
		}
		if len(listed.Tools) != 9 {
			t.Fatalf("tool count = %d, want 9", len(listed.Tools))
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
	server := newPanelServer(t)
	session := connectTestClient(t, server, nil)
	if instructions := session.InitializeResult().Instructions; !strings.Contains(instructions, "get_observability_overview") || !strings.Contains(instructions, "authenticated user") {
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
		Name: "get_observability_overview",
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
