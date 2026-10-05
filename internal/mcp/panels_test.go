package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/query"
	appstore "github.com/labstack/fanout/internal/store"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newPanelServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	duck, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = duck.Close(); _ = repo.Close(); _ = sqlite.Close() })
	if _, err := sqlite.DB.Exec(`INSERT INTO users (id, email) VALUES ('owner', 'owner@example.com')`); err != nil {
		t.Fatal(err)
	}
	executor := panel.NewExecutor(duck, 30)
	return New(&fakeObservability{}, dashboard.New(sqlite.DB, executor), executor, "test")
}

func ownerRequest() *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{dashboard.OwnerMetaKey: "owner"}}}
}

func TestPreviewPanelsReportsEachPanel(t *testing.T) {
	s := newPanelServer(t)
	_, out, err := s.previewPanels(t.Context(), nil, PreviewInput{Panels: []panel.Panel{
		{ID: "requests", Title: "Requests", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"count()"}}},
		{ID: "typo", Title: "Typo", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"p95(duraton_ms)"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Panels[1].Status != "invalid" || !strings.Contains(out.Panels[1].Problems[0].Message+out.Panels[1].Problems[0].Hint, "duration_ms") {
		t.Fatalf("typo preview = %+v", out.Panels[1])
	}
	if out.Panels[0].Status != "not_run" {
		t.Fatalf("a valid panel is not run while another is invalid: %+v", out.Panels[0])
	}
	_, out, err = s.previewPanels(t.Context(), nil, PreviewInput{Panels: []panel.Panel{{ID: "requests", Title: "Requests", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"count()"}}}}})
	if err != nil || out.Panels[0].Status != "empty" || out.Panels[0].Diagnosis == "" {
		t.Fatalf("empty store preview = %+v %v", out.Panels, err)
	}
}

func TestCreateAndEditDashboardTools(t *testing.T) {
	s := newPanelServer(t)
	spec := panel.Dashboard{Name: "Checkout", Panels: []panel.Panel{
		{ID: "notes", Title: "Notes", Viz: "text", Content: "Money path."},
		{ID: "requests", Title: "Requests", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"count()"}}},
	}}
	result, out, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: spec})
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if out.Dashboard.Version != 1 || !strings.Contains(text, "requests") || !strings.Contains(text, "No spans") {
		t.Fatalf("create summary %q record %+v", text, out.Dashboard)
	}
	_, edited, err := s.dashboardEdit(t.Context(), ownerRequest(), DashboardEditInput{ID: out.Dashboard.ID, Operations: []dashboard.Operation{{Op: "remove_panel", ID: "requests"}}, Message: "Drop the empty panel"})
	if err != nil || edited.Dashboard.Version != 2 || len(edited.Dashboard.Spec.Panels) != 1 {
		t.Fatalf("edit = %+v %v", edited, err)
	}
}

func TestDashboardToolScopes(t *testing.T) {
	for _, name := range []string{"list_dashboards", "get_dashboard", "create_dashboard", "replace_dashboard", "edit_dashboard"} {
		if RequiredToolScope(name) != dashboard.OAuthScope {
			t.Errorf("%s scope = %q", name, RequiredToolScope(name))
		}
	}
	for _, name := range []string{"get_telemetry_schema", "preview_panels"} {
		if RequiredToolScope(name) != "" {
			t.Errorf("%s needs only telemetry:read", name)
		}
	}
}
