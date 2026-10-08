package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
	controlstore "github.com/labstack/fanout/internal/store"
	mcpgoauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type cancelValidator struct{}

func (cancelValidator) Validate(ctx context.Context, _ *panel.Dashboard) error { return ctx.Err() }

func newToolServer(t *testing.T, validator dashboard.Validator, panels panelExecutor) *Server {
	t.Helper()
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, owner := range []string{"owner", "other"} {
		if _, err := database.DB.Exec(`INSERT INTO users(id,email,name,role,active) VALUES(?,?,?,'admin',1)`, owner, owner+"@example.test", owner); err != nil {
			t.Fatal(err)
		}
	}
	s := NewWithIntelligence(&fakeObservability{}, dashboard.New(database.DB, validator), nil, nil, "test")
	s.panels = panels
	return s
}

func requestFor(owner string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{dashboard.OwnerMetaKey: owner}}}
}

func textDashboard(name string) panel.Dashboard {
	return panel.Dashboard{Name: name, Panels: []panel.Panel{{ID: "notes", Title: "Notes", Viz: "text", Content: "hello"}}}
}

func TestCreateDashboardAcceptsTheDocumentedShape(t *testing.T) {
	s := newPanelServer(t)
	session := connectTestClient(t, s, nil)
	// Metadata carries the in-process owner, as the agent runtime does.
	call := func(args string) *mcp.CallToolResult {
		t.Helper()
		var parsed map[string]any
		if err := json.Unmarshal([]byte(args), &parsed); err != nil {
			t.Fatal(err)
		}
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_dashboard", Arguments: parsed, Meta: mcp.Meta{dashboard.OwnerMetaKey: "owner"}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := call(`{"dashboard":{"name":"Checkout","panels":[
		{"id":"requests","title":"Requests","viz":"stat","query":{"from":"spans","measures":["count()"]}},
		{"id":"latency","title":"Latency","viz":"timeseries","query":{"from":"spans","measures":["p95(duration_ms)"],"bucket":"auto"}},
		{"id":"notes","title":"Notes","viz":"text","content":"Money path."}]}}`)
	if first.IsError {
		t.Fatalf("documented shape rejected: %+v", first.Content)
	}
	raw, _ := json.Marshal(first.StructuredContent)
	var out dashboardOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Dashboard.Spec.Version != 1 || out.Dashboard.Spec.Time.Range == "" {
		t.Fatalf("stored spec lacks defaults: %+v", out.Dashboard.Spec)
	}
	second := call(`{"dashboard":{"name":"Per service","time":{"range":"1h"},
		"variables":[{"name":"service","kind":"query","from":"spans","field":"service"}],
		"panels":[{"id":"requests","title":"Requests","viz":"stat","query":{"from":"spans","where":["service = $service"],"measures":["count()"]}}]}}`)
	if second.IsError {
		t.Fatalf("spec with variables and time rejected: %+v", second.Content)
	}
}

func TestStaleBaseVersionIsReportedAndMatchingOneSucceeds(t *testing.T) {
	s := newToolServer(t, structural{}, nil)
	ctx := t.Context()
	_, created, err := s.dashboardCreate(ctx, requestFor("owner"), DashboardCreateInput{Dashboard: textDashboard("Stale")})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Dashboard.ID
	rename := []dashboard.Operation{{Op: "rename", Name: "Renamed"}}
	_, bumped, err := s.dashboardEdit(ctx, requestFor("owner"), DashboardEditInput{ID: id, Operations: rename, BaseVersion: 1})
	if err != nil || bumped.Dashboard.Version != 2 {
		t.Fatalf("matching base_version = %+v %v", bumped.Dashboard, err)
	}
	const stale = "the dashboard changed since you read it"
	if _, _, err := s.dashboardEdit(ctx, requestFor("owner"), DashboardEditInput{ID: id, Operations: rename, BaseVersion: 1}); err == nil || !strings.Contains(err.Error(), stale) {
		t.Fatalf("stale edit error = %v", err)
	}
	replacement := textDashboard("Replaced")
	if _, _, err := s.dashboardReplace(ctx, requestFor("owner"), DashboardReplaceInput{ID: id, Dashboard: replacement, BaseVersion: 1}); err == nil || !strings.Contains(err.Error(), stale) {
		t.Fatalf("stale replace error = %v", err)
	}
	_, replaced, err := s.dashboardReplace(ctx, requestFor("owner"), DashboardReplaceInput{ID: id, Dashboard: replacement, BaseVersion: 2})
	if err != nil || replaced.Dashboard.Version != 3 {
		t.Fatalf("matching replace = %+v %v", replaced.Dashboard, err)
	}
}

func TestInvalidSpecsReturnProblemsWithPaths(t *testing.T) {
	s := newPanelServer(t)
	ctx := t.Context()
	bad := panel.Dashboard{Name: "Bad", Panels: []panel.Panel{{ID: "typo", Title: "Typo", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"p95(duraton_ms)"}}}}}
	_, _, err := s.dashboardCreate(ctx, requestFor("owner"), DashboardCreateInput{Dashboard: bad})
	var problems panel.Problems
	if !errors.As(err, &problems) || !strings.Contains(err.Error(), "panels[0]") {
		t.Fatalf("create error = %v", err)
	}
	_, created, err := s.dashboardCreate(ctx, requestFor("owner"), DashboardCreateInput{Dashboard: textDashboard("Good")})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.dashboardEdit(ctx, requestFor("owner"), DashboardEditInput{ID: created.Dashboard.ID, Operations: []dashboard.Operation{{Op: "frobnicate"}}})
	problems = nil
	if !errors.As(err, &problems) || !strings.Contains(err.Error(), "operations[0]") {
		t.Fatalf("edit error = %v", err)
	}
}

func TestCancelledContextSurfacesAsCancellation(t *testing.T) {
	s := newToolServer(t, cancelValidator{}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err := s.dashboardCreate(ctx, requestFor("owner"), DashboardCreateInput{Dashboard: textDashboard("Cancelled")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestOtherOwnersDashboardIsNotFound(t *testing.T) {
	s := newToolServer(t, structural{}, nil)
	ctx := t.Context()
	_, created, err := s.dashboardCreate(ctx, requestFor("owner"), DashboardCreateInput{Dashboard: textDashboard("Private")})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Dashboard.ID
	other := requestFor("other")
	_, _, getErr := s.dashboardGet(ctx, other, DashboardIDInput{ID: id})
	_, _, editErr := s.dashboardEdit(ctx, other, DashboardEditInput{ID: id, Operations: []dashboard.Operation{{Op: "rename", Name: "Mine"}}})
	_, _, replaceErr := s.dashboardReplace(ctx, other, DashboardReplaceInput{ID: id, Dashboard: textDashboard("Mine")})
	for name, err := range map[string]error{"get": getErr, "edit": editErr, "replace": replaceErr} {
		if err == nil || err.Error() != "dashboard not found" {
			t.Errorf("%s by another owner = %v", name, err)
		}
	}
	_, got, err := s.dashboardGet(ctx, requestFor("owner"), DashboardIDInput{ID: id})
	if err != nil || got.Dashboard.Version != 1 || got.Dashboard.Name != "Private" {
		t.Fatalf("owner's dashboard changed: %+v %v", got.Dashboard, err)
	}
}

func TestSaveReportsPanelsItCouldNotCheck(t *testing.T) {
	// The structural validator accepts a filter the executor then rejects, so
	// the post-save run fails after the dashboard is already stored.
	panels := newPanelServer(t).panels
	s := newToolServer(t, structural{}, panels)
	spec := panel.Dashboard{Name: "Unchecked", Panels: []panel.Panel{{ID: "requests", Title: "Requests", Viz: "stat", Query: &panel.Query{From: "spans", Where: []string{"service = = 'x'"}, Measures: []string{"count()"}}}}}
	result, out, err := s.dashboardCreate(t.Context(), requestFor("owner"), DashboardCreateInput{Dashboard: spec})
	if err != nil {
		t.Fatal(err)
	}
	if out.Dashboard.Version != 1 {
		t.Fatalf("dashboard not saved: %+v", out.Dashboard)
	}
	if text := result.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "Panels were not checked:") {
		t.Fatalf("summary hides the failed check: %q", text)
	}
}

func TestBuildOriginMetadataTrustBoundary(t *testing.T) {
	s := newToolServer(t, structural{}, nil)
	origin := dashboard.BuildOrigin{ThreadID: "missing", MessageID: "user", RequestExcerpt: "Spoof"}
	req := requestFor("owner")
	req.Params.Meta[dashboard.BuildOriginMetaKey] = origin
	if _, _, err := s.dashboardCreate(t.Context(), req, DashboardCreateInput{Dashboard: textDashboard("Invalid local")}); err == nil {
		t.Fatal("nonowned local origin silently ignored")
	}
	req.Extra = &mcp.RequestExtra{TokenInfo: &mcpgoauth.TokenInfo{UserID: "owner", Scopes: []string{dashboard.OAuthScope}}}
	// Even an in-process context origin must be stripped at a remote boundary.
	_, out, err := s.dashboardCreate(dashboard.WithBuildOrigin(t.Context(), origin), req, DashboardCreateInput{Dashboard: textDashboard("Remote")})
	if err != nil || out.Dashboard.ID == "" {
		t.Fatal(out, err)
	}
	list, err := s.dashboards.List(t.Context(), "owner")
	if err != nil || len(list) != 1 || list[0].Origin != nil {
		t.Fatalf("remote fabricated provenance: %+v %v", list, err)
	}
}

func TestInProcessCreatePersistsInjectedOrigin(t *testing.T) {
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	_, err = database.DB.Exec(`INSERT INTO users(id,email) VALUES ('owner','owner@example.test'); INSERT INTO agui_threads(thread_id,owner_id) VALUES ('source','owner')`)
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithIntelligence(&fakeObservability{}, dashboard.New(database.DB, structural{}), nil, nil, "test")
	session := connectTestClient(t, s, nil)
	origin := dashboard.BuildOrigin{ThreadID: "source", MessageID: "request", RequestExcerpt: "Build volume"}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_dashboard", Arguments: map[string]any{"dashboard": textDashboard("Local origin")}, Meta: mcp.Meta{dashboard.OwnerMetaKey: "owner", dashboard.BuildOriginMetaKey: origin}})
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	list, err := s.dashboards.List(t.Context(), "owner")
	if err != nil || len(list) != 1 || list[0].Origin == nil || *list[0].Origin != origin {
		t.Fatalf("origin=%+v %v", list, err)
	}
}

func TestDashboardMCPResultsExcludePrivateBuildProvenance(t *testing.T) {
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	_, err = database.DB.Exec(`INSERT INTO users(id,email) VALUES ('owner','owner@example.test'); INSERT INTO agui_threads(thread_id,owner_id) VALUES ('private-thread','owner')`)
	if err != nil {
		t.Fatal(err)
	}
	service := dashboard.New(database.DB, structural{})
	origin := dashboard.BuildOrigin{ThreadID: "private-thread", MessageID: "private-message", RequestExcerpt: "PRIVATE REQUEST"}
	board, err := service.Create(dashboard.WithBuildOrigin(t.Context(), origin), "owner", textDashboard("Private"), dashboard.Author{Kind: "agent", ID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithIntelligence(&fakeObservability{}, service, nil, nil, "test")
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "in_process", true: "remote"}[remote], func(t *testing.T) {
			req := requestFor("owner")
			if remote {
				req.Extra = &mcp.RequestExtra{TokenInfo: &mcpgoauth.TokenInfo{UserID: "owner", Scopes: []string{dashboard.OAuthScope}}}
			}
			_, list, err := s.dashboardList(t.Context(), req, struct{}{})
			if err != nil {
				t.Fatal(err)
			}
			_, get, err := s.dashboardGet(t.Context(), req, DashboardIDInput{ID: board.ID})
			if err != nil {
				t.Fatal(err)
			}
			_, edited, err := s.dashboardEdit(t.Context(), req, DashboardEditInput{ID: board.ID, Operations: []dashboard.Operation{{Op: "rename", Name: "Private"}}})
			if err != nil {
				t.Fatal(err)
			}
			for _, out := range []any{list, get, edited} {
				raw, err := json.Marshal(out)
				if err != nil {
					t.Fatal(err)
				}
				for _, private := range []string{"origin", "thread_id", "message_id", "request_excerpt", "PRIVATE REQUEST", "private-thread", "private-message"} {
					if strings.Contains(string(raw), private) {
						t.Fatalf("private %s leaked: %s", private, raw)
					}
				}
			}
		})
	}
}
func TestOAuthIdentityCannotFallBackToInjectedOwnerMetadata(t *testing.T) {
	req := requestFor("owner")
	req.Extra = &mcp.RequestExtra{TokenInfo: &mcpgoauth.TokenInfo{Scopes: []string{dashboard.OAuthScope}}}
	if _, err := dashboardOwner(req); err == nil {
		t.Fatal("empty OAuth identity accepted owner metadata")
	}
}
