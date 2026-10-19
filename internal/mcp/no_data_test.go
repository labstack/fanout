package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/toolerror"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAllEmptyFragmentsReturnGuidanceWithoutViews(t *testing.T) {
	s, _, _, from, to := serviceInputFixture(t)
	client := connectTestClient(t, s, nil)
	for _, tc := range []struct {
		name string
		args map[string]any
		want []string
	}{
		{"get_service_performance", map[string]any{"service": "collector"}, []string{"collector", "spans=0, logs=0, metrics=1"}},
		{"search_logs", map[string]any{"service": "logger", "severity": "ERROR"}, []string{"INFO (1)", "spans=0, logs=1, metrics=0"}},
		{"query_telemetry", map[string]any{"panel": panel.Panel{ID: "p", Title: "Collector latency", Viz: "stat", Query: &panel.Query{From: "spans", Where: []string{"namespace = 'shop' AND service = 'collector'"}, Measures: []string{"p95(duration_ms)"}}}}, []string{"Collector latency", "metrics=1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "query_telemetry" {
				tc.args["time"] = panel.Time{From: &from, To: &to}
			} else {
				tc.args["from"], tc.args["to"], tc.args["namespace"] = from, to, "shop"
			}
			result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
			if err != nil || !result.IsError || result.StructuredContent != nil {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			var failure toolerror.Result
			if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &failure); err != nil || failure.Error == nil || failure.Error.Code != "no_data" {
				t.Fatalf("failure=%+v err=%v", failure, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(failure.Error.Message, want) {
					t.Errorf("missing %q in %s", want, failure.Error.Message)
				}
			}
		})
	}
	// A mixed fragment remains a view, retaining its empty panel's diagnosis.
	d := fragmentPreset("performance", "checkout", "shop", "", "", 0)
	d.Time = panel.Time{From: &from, To: &to}
	d.Panels[3].Query.Where = append(d.Panels[3].Query.Where, "operation = 'absent'")
	result, out, err := s.runFragment(t.Context(), panel.RunRequest{Dashboard: d})
	if err != nil || result.IsError || len(out.Results) != 4 || out.Results[0].Status != "ok" || out.Results[3].Status != "empty" {
		t.Fatalf("partial=%+v err=%v", out, err)
	}
}

func TestEmptyStorePresetsReturnNoData(t *testing.T) {
	s := newPanelServer(t)
	client := connectTestClient(t, s, nil)
	for _, name := range []string{"get_observability_overview", "get_service_topology", "get_service_performance", "inspect_trace", "search_logs"} {
		result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err != nil || !result.IsError || result.StructuredContent != nil {
			t.Fatalf("%s: result=%+v err=%v", name, result, err)
		}
		var failure toolerror.Result
		if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &failure); err != nil || failure.Error == nil || failure.Error.Code != "no_data" {
			t.Fatalf("%s: failure=%+v err=%v", name, failure, err)
		}
	}
}

func TestDashboardMutationsRejectEmptyPanelsBeforeWriting(t *testing.T) {
	s, _, _, from, to := serviceInputFixture(t)
	s = newToolServer(t, structural{}, s.panels)
	spec := panel.Dashboard{Name: "Checked", Time: panel.Time{From: &from, To: &to}, Panels: []panel.Panel{
		{ID: "note", Title: "Notes", Viz: "text", Content: "Context"},
		{ID: "requests", Title: "Collector requests", Viz: "stat", Query: &panel.Query{From: "spans", Where: []string{"service = 'collector'"}, Measures: []string{"count()"}}},
	}}
	result, out, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: spec})
	if err != nil || !result.IsError || out.Error == nil || out.Error.Code != "invalid_spec" || out.Dashboard != nil || !strings.Contains(out.Error.Message, "panels[1]") || !strings.Contains(out.Error.Message, "Collector requests") || !strings.Contains(out.Error.Message, "Values seen") {
		t.Fatalf("rejected=%+v err=%v", out, err)
	}
	good := spec
	good.Panels = spec.Panels[:1]
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: good})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Dashboard.ID
	result, out, err = s.dashboardReplace(t.Context(), ownerRequest(), DashboardReplaceInput{ID: id, Dashboard: spec, BaseVersion: 1})
	if err != nil || !result.IsError || out.Error == nil || out.Error.Code != "invalid_spec" || !strings.Contains(out.Error.Message, "Collector requests") {
		t.Fatalf("replace: %v", err)
	}
	result, out, err = s.dashboardEdit(t.Context(), ownerRequest(), DashboardEditInput{ID: id, BaseVersion: 1, Operations: []dashboard.Operation{{Op: "add_panel", Panel: &spec.Panels[1]}}})
	if err != nil || !result.IsError || out.Error == nil || out.Error.Code != "invalid_spec" || !strings.Contains(out.Error.Message, "Collector requests") {
		t.Fatalf("edit: %v", err)
	}
	versions, err := s.dashboards.Versions(t.Context(), "owner", id)
	if err != nil || len(versions) != 1 {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
	// Browser/service saves retain the human-editable empty state.
	if _, err := s.dashboards.Create(t.Context(), "owner", withName(spec, "Human"), dashboard.Author{Kind: "user", ID: "owner"}); err != nil {
		t.Fatal(err)
	}
}

func TestInspectTraceKeepsDetailsWithEmptyPanels(t *testing.T) {
	s, _, _, from, to := serviceInputFixture(t)
	for _, tc := range []struct {
		id          string
		spans, logs int
	}{
		{id: "checkout", spans: 6},
		{id: "logs-only", logs: 1},
	} {
		t.Run(tc.id, func(t *testing.T) {
			result, out, err := s.trace(t.Context(), nil, TraceInput{TraceID: tc.id, Service: "missing", Namespace: "shop", From: &from, To: &to})
			if err != nil || result == nil || result.IsError || out.Trace == nil {
				t.Fatalf("trace details discarded: result=%+v out=%+v err=%v", result, out, err)
			}
			if !allResultsEmpty(out.Results) || len(out.Trace.Data.Spans) != tc.spans || len(out.Trace.Data.Logs) != tc.logs {
				t.Fatalf("trace=%+v panels=%+v", out.Trace, out.Results)
			}
		})
	}
	_, _, err := s.trace(t.Context(), nil, TraceInput{TraceID: "absent", Namespace: "shop", From: &from, To: &to})
	if err == nil || !strings.Contains(err.Error(), `"no_data"`) {
		t.Fatalf("empty trace error=%v", err)
	}
}

func TestEmptyGuidanceOmitsUnrepresentableNamespaceScopes(t *testing.T) {
	s, _, _, from, to := serviceInputFixture(t)
	for _, predicate := range []string{
		"namespace IN ('shop', 'staging')",
		"namespace = 'shop' OR namespace = 'staging'",
		"namespace LIKE 'sho%'",
		"namespace != 'other'",
		"namespace IS NULL",
		"lower(namespace) = 'shop'",
		"CAST(namespace AS VARCHAR) = 'shop'",
		"namespace = ''",
		"namespace = 'shop' AND namespace = 'other'",
		"namespace = 'shop' AND namespace IN ('staging')",
	} {
		for _, service := range []string{"missing", "elsewhere", "checkout"} {
			t.Run(predicate+"/"+service, func(t *testing.T) {
				// The namespace restriction and service may be separate filters or
				// part of a conjunction; neither may widen guidance's lookup scope.
				for _, where := range [][]string{
					{predicate, "service = '" + service + "'"},
					{"(" + predicate + ") AND service = '" + service + "'"},
				} {
					d := panel.Dashboard{Name: "Scoped", Time: panel.Time{From: &from, To: &to}, Panels: []panel.Panel{{ID: "logs", Title: "Logs", Viz: "logs", Query: &panel.Query{From: "logs", Where: where}}}}
					code, note, err := s.panels.EmptyGuidance(t.Context(), panel.RunRequest{Dashboard: d})
					if err != nil || code != "no_data" || note != "" {
						t.Fatalf("unsafe scope produced service guidance: code=%q note=%q err=%v", code, note, err)
					}
				}
			})
		}
	}
}

func TestDashboardSaveAcceptsBatchCellBudgetTruncation(t *testing.T) {
	s := panelServerFixture(t, true)
	d := panel.Dashboard{Name: "Budget"}
	// TestBatchCellBudget's forty six-column tables exhaust the shared
	// 200,000-cell budget. A third span input brings this fixture to the
	// same 1,000-row per-frame cap before batch truncation.
	for i := range 40 {
		d.Panels = append(d.Panels, panel.Panel{ID: fmt.Sprintf("p%d", i), Title: "wide", Viz: "table", SQL: "SELECT a.span_id, a.service, a.operation, a.http_route, a.status, a.duration_ms FROM spans a, spans b, spans c WHERE $__window(a.start_time)"})
	}
	results, err := s.panels.Run(t.Context(), panel.RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	truncatedToZero := false
	for _, r := range results {
		if r.Status != panel.StatusOK || r.Frame == nil {
			t.Fatalf("populated fixture failed: %+v", r)
		}
		truncatedToZero = truncatedToZero || (r.Frame.Truncated && r.Frame.Rows == 0)
	}
	if !truncatedToZero || results[0].Frame.Rows != 1000 {
		t.Fatal("fixture must produce 1,000 rows per panel before exhausting the batch budget")
	}
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: d})
	if err != nil {
		t.Fatalf("create rejected populated budget-truncated panels: %v", err)
	}
	_, replaced, err := s.dashboardReplace(t.Context(), ownerRequest(), DashboardReplaceInput{ID: created.Dashboard.ID, BaseVersion: 1, Dashboard: d})
	if err != nil {
		t.Fatalf("replace rejected populated budget-truncated panels: %v", err)
	}
	_, edited, err := s.dashboardEdit(t.Context(), ownerRequest(), DashboardEditInput{ID: created.Dashboard.ID, BaseVersion: replaced.Dashboard.Version, Operations: []dashboard.Operation{{Op: "rename", Name: "Budget edited"}}})
	if err != nil || edited.Dashboard == nil || edited.Dashboard.Version != 3 {
		t.Fatalf("edit rejected populated budget-truncated panels: %+v %v", edited, err)
	}
}
