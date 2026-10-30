package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/toolerror"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTimestampLiteralHintSurvivesMCPPreviewAndDashboardRejection(t *testing.T) {
	now := time.Date(2026, 10, 20, 18, 6, 0, 0, time.UTC)
	s := panelServerFixtureAt(t, true, now)
	from := now.Add(-time.Hour)
	window := query.Time{From: &from, To: &now}
	for _, tc := range []struct {
		name, predicate string
		invalid         bool
	}{
		{"timestamptz", "start_time < TIMESTAMPTZ '2026-10-20 17:36:15+00'", true},
		{"between_timestamptz", "start_time BETWEEN TIMESTAMPTZ '2026-10-20 17:36:00+00' AND TIMESTAMPTZ '2026-10-20 17:36:14+00'", true},
		{"timestamp", "start_time < TIMESTAMP '2026-10-20 17:36:15'", false},
		{"between_timestamp", "start_time BETWEEN TIMESTAMP '2026-10-20 17:36:00' AND TIMESTAMP '2026-10-20 17:36:14'", false},
		{"explicit_cast", "start_time < TIMESTAMPTZ '2026-10-20 17:36:15+00'::TIMESTAMPTZ_NS", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := query.Panel{ID: "calls", Title: "Calls", Viz: "table", SQL: "SELECT count(*) AS n FROM spans WHERE $__window(start_time) AND " + tc.predicate}
			_, out, err := s.previewPanels(t.Context(), nil, PreviewInput{Panels: []query.Panel{p}, Time: &window})
			if err != nil || len(out.Panels) != 1 {
				t.Fatalf("preview=%+v err=%v", out, err)
			}
			got := out.Panels[0]
			if !tc.invalid {
				if got.Status != "ok" || got.Rows != 1 || len(got.Sample) != 1 || len(got.Sample[0]) != 1 || got.Sample[0][0] != float64(15) {
					t.Fatalf("preview=%+v, want count 15", got)
				}
				return
			}
			if got.Status != "invalid" || len(got.Problems) != 1 || got.Problems[0].Path != "panels[0].sql" || got.Problems[0].Hint != query.SQLTimestampGuide {
				t.Fatalf("preview lost hint: %+v", got)
			}
			session := connectTestClient(t, s, nil)
			result, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{
				Name: "create_dashboard", Arguments: DashboardCreateInput{Dashboard: query.Dashboard{Name: "Time literals", Time: window, Panels: []query.Panel{p}}},
				Meta: mcpsdk.Meta{dashboard.OwnerMetaKey: "owner"},
			})
			if err != nil || !result.IsError {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			check := func(raw []byte) {
				t.Helper()
				var failure dashboardOutput
				if err := json.Unmarshal(raw, &failure); err != nil || failure.Error == nil || failure.Error.Code != "invalid_spec" || !strings.Contains(failure.Error.Message, query.SQLTimestampGuide) {
					t.Fatalf("failure=%s err=%v", raw, err)
				}
				if len(failure.Problems) != 1 || failure.Problems[0].Path != "panels[0].sql" || failure.Problems[0].Hint != query.SQLTimestampGuide {
					t.Fatalf("lost problem hint: %+v", failure.Problems)
				}
			}
			check([]byte(result.Content[0].(*mcpsdk.TextContent).Text))
			raw, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			check(raw)
		})
	}
}

type presentationExecutor struct {
	receiptExecutor
	guidance string
}

func (p presentationExecutor) EmptyGuidance(context.Context, query.RunRequest) (string, string, error) {
	return "no_data", p.guidance, nil
}

func TestPresentationAcrossMCPPreviewsReceiptsGuidanceAndErrors(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)duckdb|parquet|sqlite|(?:binder|catalog|conversion|io|parser) error|variant|timestamptz_ns|timestamp_ns|telemetry\.|parquet_index|/srv/|cast\(\$|edit_dashboard`)
	s := newToolServer(t, structural{}, nil)
	for i, message := range []string{
		`Binder Error: Referenced column "missing_column" not found`,
		`Catalog Error: missing_function does not exist in telemetry.spans`,
		`Conversion Error: Cannot convert 'bad' to TIMESTAMPTZ_NS`,
		`IO Error: DuckDB read /srv/data/a.parquet`,
		`Parser Error: syntax error at or near "oops"`,
		`read indexed Parquet trace: open /srv/data/a.parquet: permission denied`,
		`exemplars: SQLite read /srv/data/control.sqlite: failed`,
		`discover spans attributes: Catalog Error: resource_inventory not found`,
		`Binder Error: Cannot compare VARIANT to CAST($x AS TIMESTAMPTZ_NS)`,
		`Binder Error: query_count in telemetry.service_rollup cannot compare DOUBLE with INTEGER` + "\nCandidate bindings: \"telemetry.edge_rollup.query_count\", \"resource_inventory.service\"\nLINE 1: SELECT query_count FROM telemetry.service_rollup",
	} {
		t.Run(message, func(t *testing.T) {
			check := func(value any) {
				t.Helper()
				raw, err := json.Marshal(value)
				if err != nil || forbidden.Match(raw) {
					t.Fatalf("unsafe publication: %s (%v)", raw, err)
				}
			}
			checkAuthor := func(got string) {
				t.Helper()
				if strings.Contains(message, "query_count") && got != "query_count in service_rollup cannot compare DOUBLE with INTEGER\nCandidate bindings: \"edge_rollup.query_count\", \"resource_inventory.service\"" {
					t.Errorf("lost author diagnostic: %q", got)
				}
			}
			s.panels = presentationExecutor{receiptExecutor: receiptExecutor{run: func(context.Context, query.RunRequest) ([]query.Result, error) {
				return []query.Result{{ID: "notes", Status: "error", Error: message, Diagnosis: "No matching data."}}, nil
			}}, guidance: "No matching data."}
			text, out, err := s.previewPanels(t.Context(), nil, PreviewInput{Panels: textDashboard("Preview").Panels})
			if err != nil {
				t.Fatal(err)
			}
			check(out)
			check(text)
			checkAuthor(out.Panels[0].Error)
			_, saved, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard(string(rune('A' + i)))})
			if err != nil {
				t.Fatal(err)
			}
			check(saved)
			guidance := s.noDataError(t.Context(), PanelFragment{Dashboard: textDashboard("Empty"), Results: []query.Result{{ID: "notes", Diagnosis: "No matching data."}}})
			if guidance == nil {
				t.Fatal("missing no_data guidance")
			}
			check(guidance.Error())
			s.panels = receiptExecutor{run: func(context.Context, query.RunRequest) ([]query.Result, error) {
				return nil, query.Problems{{Path: "panels[0].sql", Message: message, Hint: message}}
			}}
			text, out, err = s.previewPanels(t.Context(), nil, PreviewInput{Panels: textDashboard("Invalid").Panels})
			if err != nil {
				t.Fatal(err)
			}
			check(text)
			check(out)
			checkAuthor(out.Panels[0].Problems[0].Message)
			checkAuthor(out.Panels[0].Problems[0].Hint)
			if strings.Contains(message, "missing_column") && !strings.Contains(out.Panels[0].Problems[0].Message, "missing_column") {
				t.Fatal("lost missing column")
			}
			cause := errors.New(message)
			got := safePanelToolError(cause)
			if !errors.Is(got, cause) || got.Error() != query.ReadFailure {
				t.Fatalf("operational error: %v", got)
			}
			// Exercise actual MCP middleware and both error content paths.
			name := "failure_" + string(rune('a'+i))
			mcpsdk.AddTool(s.MCP(), &mcpsdk.Tool{Name: name}, func(context.Context, *mcpsdk.CallToolRequest, struct{}) (*mcpsdk.CallToolResult, any, error) {
				return nil, nil, errors.New(message)
			})
			session := connectTestClient(t, s, nil)
			result, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: name, Arguments: map[string]any{}})
			if err != nil || !result.IsError {
				t.Fatalf("MCP error: %+v %v", result, err)
			}
			check(result)
		})
	}
}

func TestStructuredFilterRuntimeDiagnosticReachesPreview(t *testing.T) {
	s := panelServerFixture(t, true)
	_, out, err := s.previewPanels(t.Context(), nil, PreviewInput{Panels: []query.Panel{{ID: "p", Title: "Calls", Viz: "table", Query: &query.Spec{From: "spans", Measures: []string{"count()"}, Where: []string{"CAST(service AS INTEGER) > 0"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	got := out.Panels[0]
	for _, detail := range []string{"checkout", "INT32", "service"} {
		if got.Status != "error" || !strings.Contains(got.Error, detail) {
			t.Fatalf("missing %s in preview: %+v", detail, got)
		}
	}
}

func TestProductDiagnosesSurvivePublicationBoundaries(t *testing.T) {
	const routeDiagnosis = "No spans match this combination of filters in this time range. Without HTTP route /api/cart, 120 match the remaining filters. Values seen: /cart (60), /quote (60)."
	for _, diagnosis := range []string{routeDiagnosis, "No logs for body /srv/checkout/a.parquet or value 'IO Error: DuckDB'."} {
		s := newToolServer(t, structural{}, nil)
		s.panels = presentationExecutor{receiptExecutor: receiptExecutor{run: func(context.Context, query.RunRequest) ([]query.Result, error) {
			return []query.Result{{ID: "notes", Status: "empty", Diagnosis: diagnosis}}, nil
		}}, guidance: diagnosis}
		_, out, err := s.previewPanels(t.Context(), nil, PreviewInput{Panels: textDashboard("Preview").Panels})
		if err != nil || out.Panels[0].Diagnosis != diagnosis {
			t.Fatalf("preview: %+v %v", out, err)
		}
		guidance := s.noDataError(t.Context(), PanelFragment{Dashboard: textDashboard("Empty"), Results: []query.Result{{ID: "notes", Diagnosis: diagnosis}}})
		if guidance == nil || !strings.Contains(guidance.Error(), diagnosis) {
			t.Fatalf("guidance: %v", guidance)
		}
		_, saved, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("Saved")})
		if err != nil || saved.Receipt.SaveCheck.Panels[0].Diagnosis != diagnosis {
			t.Fatalf("receipt: %+v %v", saved, err)
		}
		spec := textDashboard("Empty telemetry")
		spec.Panels[0] = query.Panel{ID: "notes", Title: "Calls", Viz: "table", Query: &query.Spec{From: "spans", Measures: []string{"count()"}}}
		result, rejected, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: spec})
		if err != nil || !result.IsError || rejected.Error == nil || rejected.Error.Code != "invalid_spec" || !strings.Contains(rejected.Error.Message, diagnosis) {
			t.Fatalf("save validation lost diagnosis: %+v %v", rejected, err)
		}
	}
}

func TestNoDataRouteGuidanceSurvivesMCPMiddleware(t *testing.T) {
	const diagnosis = "No spans match these filters. Without HTTP route /api/cart, 120 match the remaining filters."
	s := newToolServer(t, structural{}, presentationExecutor{
		receiptExecutor: receiptExecutor{run: func(context.Context, query.RunRequest) ([]query.Result, error) {
			return []query.Result{{ID: "calls", Status: query.StatusEmpty, Diagnosis: diagnosis}}, nil
		}},
		guidance: diagnosis,
	})
	session := connectTestClient(t, s, nil)
	result, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "query_telemetry",
		Arguments: map[string]any{"panel": query.Panel{
			ID: "calls", Title: "Calls", Viz: "table",
			Query: &query.Spec{From: "spans", Where: []string{"http_route = '/api/cart'"}},
		}},
	})
	if err != nil || !result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var failure toolerror.Result
	if err := json.Unmarshal([]byte(result.Content[0].(*mcpsdk.TextContent).Text), &failure); err != nil || failure.Error == nil || failure.Error.Code != "no_data" {
		t.Fatalf("failure=%+v err=%v", failure, err)
	}
	if !strings.Contains(failure.Error.Message, diagnosis) {
		t.Fatalf("lost route guidance: %s", failure.Error.Message)
	}
}

func TestDashboardRejectionsPreserveRouteDiagnosesThroughMiddleware(t *testing.T) {
	const diagnosis = "No spans match these filters. Without HTTP route /api/orders/{id}, 120 match the remaining filters."
	s := newToolServer(t, structural{}, receiptExecutor{run: func(context.Context, query.RunRequest) ([]query.Result, error) {
		return []query.Result{{ID: "calls", Status: query.StatusEmpty, Diagnosis: diagnosis}}, nil
	}})
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("Saved")})
	if err != nil {
		t.Fatal(err)
	}
	spec := query.Dashboard{Name: "Rejected", Panels: []query.Panel{{ID: "calls", Title: "Calls", Viz: "table", Query: &query.Spec{From: "spans", Measures: []string{"count()"}}}}}
	want := query.PresentProblems(query.Problems{query.DiagnosisProblem("panels[0]", "Calls: "+diagnosis, "Replace or drop this panel; dashboards contain only panels that return data.")}).Error()
	if query.PresentMessage(want) != query.ReadFailure {
		t.Fatal("route diagnosis no longer reproduces operational path presentation")
	}
	session := connectTestClient(t, s, nil)
	for _, tc := range []struct {
		name string
		args any
	}{
		{"create_dashboard", DashboardCreateInput{Dashboard: spec}},
		{"replace_dashboard", DashboardReplaceInput{ID: created.Dashboard.ID, Dashboard: spec}},
		{"edit_dashboard", DashboardEditInput{ID: created.Dashboard.ID, Operations: []dashboard.Operation{{Op: "remove_panel", ID: "notes"}, {Op: "add_panel", Panel: &spec.Panels[0]}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: tc.name, Arguments: tc.args, Meta: mcpsdk.Meta{dashboard.OwnerMetaKey: "owner"}})
			if err != nil || !result.IsError {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			var failure dashboardOutput
			if err := json.Unmarshal([]byte(result.Content[0].(*mcpsdk.TextContent).Text), &failure); err != nil || failure.Error == nil || failure.Error.Code != "invalid_spec" || failure.Error.Message != want {
				t.Fatalf("failure=%+v err=%v", failure.Error, err)
			}
			if len(failure.Problems) != 1 || failure.Problems[0].Path != "panels[0]" || failure.Problems[0].Message != "Calls: "+diagnosis || failure.Problems[0].Hint != "Replace or drop this panel; dashboards contain only panels that return data." {
				t.Fatalf("problems=%+v", failure.Problems)
			}
			raw, err := json.Marshal(result.StructuredContent)
			var structured dashboardOutput
			if err != nil || json.Unmarshal(raw, &structured) != nil || structured.Error == nil || *structured.Error != *failure.Error {
				t.Fatalf("structured=%s err=%v", raw, err)
			}
			if len(structured.Problems) != 1 || !reflect.DeepEqual(structured.Problems[0], failure.Problems[0]) {
				t.Fatalf("structured problems=%+v", structured.Problems)
			}
		})
	}
}
