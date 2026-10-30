package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/fanql"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/toolerror"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestQueryTelemetryAcceptsFanQLText(t *testing.T) {
	s := panelServerFixture(t, true)
	p := query.Panel{ID: "t", Title: "t", Viz: "table"}
	input := QueryTelemetryInput{Panel: p, Text: "spans where status:error | count() by service"}
	_, got, err := s.queryTelemetry(t.Context(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	want := &query.Spec{From: "spans", Where: []string{"status = 'STATUS_CODE_ERROR'"}, Measures: []string{"count()"}, By: []string{"service"}}
	if !reflect.DeepEqual(got.Dashboard.Panels[0].Query, want) {
		t.Fatalf("query: %+v", got.Dashboard.Panels[0].Query)
	}
	p.Query = want
	_, structured, err := s.queryTelemetry(t.Context(), nil, QueryTelemetryInput{Panel: p, Time: &got.Dashboard.Time})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Results[0].Frame, structured.Results[0].Frame) {
		t.Fatalf("text frame %+v; structured %+v", got.Results[0].Frame, structured.Results[0].Frame)
	}
	if input.Panel.Query != nil {
		t.Fatal("mutated caller's panel")
	}
}

type fanqlRunCounter struct {
	panelExecutor
	calls int
}

func (e *fanqlRunCounter) Run(ctx context.Context, req query.RunRequest) ([]query.Result, error) {
	e.calls++
	return e.panelExecutor.Run(ctx, req)
}

func checkFanQLToolFailure(t *testing.T, input QueryTelemetryInput, code, message string) {
	t.Helper()
	s := newPanelServer(t)
	counter := &fanqlRunCounter{panelExecutor: s.panels}
	s.panels = counter
	result, out, err := s.queryTelemetry(t.Context(), nil, input)
	var raw []byte
	if err != nil {
		raw = []byte(err.Error())
	} else if result != nil && result.IsError {
		raw, _ = json.Marshal(out)
	} else {
		t.Fatal("invalid input accepted")
	}
	var failure toolerror.Result
	if json.Unmarshal(raw, &failure) != nil || failure.Error == nil || failure.Error.Code != code || !strings.Contains(failure.Error.Message, message) {
		t.Fatalf("error: %v", err)
	}
	if counter.calls != 0 {
		t.Fatalf("executor ran %d times", counter.calls)
	}
}

func TestQueryTelemetryRejectsTextWithQueryOrSQL(t *testing.T) {
	for _, p := range []query.Panel{{Query: &query.Spec{From: "spans"}}, {SQL: "SELECT 1"}} {
		checkFanQLToolFailure(t, QueryTelemetryInput{Panel: p, Text: "spans"}, "invalid_input", "text cannot be combined with panel.query or panel.sql")
	}
}

func TestQueryTelemetryReportsFanQLProblems(t *testing.T) {
	checkFanQLToolFailure(t, QueryTelemetryInput{Text: "spans where a:1 b:2 or c:3"}, "invalid_fanql", "The query has problems.")
}

func TestFanQLReferenceResource(t *testing.T) {
	s := newPanelServer(t)
	client := connectTestClient(t, s, nil)
	listed, err := client.ListResources(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range listed.Resources {
		if r.URI == "fanout://fanql/reference" {
			found = true
			if r.Name != "FanQL reference" || r.MIMEType != "text/markdown" {
				t.Fatal(r)
			}
		}
	}
	if !found {
		t.Fatal("FanQL reference missing from resources/list")
	}
	r, err := client.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "fanout://fanql/reference"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Contents) != 1 || r.Contents[0].MIMEType != "text/markdown" || r.Contents[0].Text != fanql.Reference() || !strings.Contains(r.Contents[0].Text, "# FanQL") || !strings.Contains(r.Contents[0].Text, "Preview") {
		t.Fatal(r)
	}
}

func TestQueryTelemetryDescriptionPrefersStructuredInput(t *testing.T) {
	client := connectTestClient(t, newPanelServer(t), nil)
	listed, err := client.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name != "query_telemetry" {
			continue
		}
		d := tool.Description
		if !strings.Contains(d, "panel.query") || strings.Index(d, "text") < strings.Index(d, "panel.query") || !strings.Contains(d, "fanout://fanql/reference") {
			t.Fatal(d)
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatal("query must remain read-only")
		}
		raw, _ := json.Marshal(tool.InputSchema)
		var input struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Fatal(err)
		}
		if input.Properties["text"] == nil || input.Properties["fanql_version"] != nil {
			t.Fatalf("text contract: %s", raw)
		}
		for _, field := range input.Required {
			if field == "text" {
				t.Fatalf("text must remain optional: %s", raw)
			}
		}
		raw, _ = json.Marshal(tool.OutputSchema)
		if strings.Contains(string(raw), "fanql_version") {
			t.Fatalf("output version: %s", raw)
		}
		return
	}
	t.Fatal("query_telemetry missing")
}

func TestQueryTelemetryFanQLMetadataAndStructuredProblems(t *testing.T) {
	s := panelServerFixture(t, true)
	_, out, err := s.queryTelemetry(t.Context(), nil, QueryTelemetryInput{Panel: query.Panel{ID: "t", Title: "t", Viz: "table"}, Text: "spans where status:error | count() by service"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Notices) != 0 {
		t.Fatalf("metadata: %+v", out)
	}
	result, failed, err := s.queryTelemetry(t.Context(), nil, QueryTelemetryInput{Text: "spans where a:1 b:2 or c:3"})
	raw, _ := json.Marshal(failed)
	var failure struct {
		Error    toolerror.Error `json:"error"`
		Problems query.Problems  `json:"problems"`
	}
	if err != nil || result == nil || !result.IsError || json.Unmarshal(raw, &failure) != nil || failure.Error.Code != "invalid_fanql" || len(failure.Problems) != 1 || failure.Problems[0].Span == nil || len(failure.Problems[0].FixIts) != 2 {
		t.Fatalf("structured failure: %v", err)
	}
	client := connectTestClient(t, s, nil)
	listed, err := client.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "query_telemetry" {
			for _, syntax := range []string{"source [where] filters | measures", "spans where status:error | count() by service", "logs where severity>=warn | count() by service every 1m"} {
				if !strings.Contains(tool.Description, syntax) {
					t.Errorf("missing inline syntax %q", syntax)
				}
			}
		}
	}
}

func TestQueryTelemetryFanQLProblemsStayStructuredThroughMiddleware(t *testing.T) {
	client := connectTestClient(t, newPanelServer(t), nil)
	result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "query_telemetry", Arguments: map[string]any{
		"panel": map[string]any{"id": "errors", "title": "Errors", "viz": "table"},
		"text":  "spans where a:1 b:2 or c:3",
	}})
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("failure: %+v %v", result, err)
	}
	for _, content := range []any{result.StructuredContent, json.RawMessage(result.Content[0].(*mcp.TextContent).Text)} {
		raw, _ := json.Marshal(content)
		var body struct {
			Error    toolerror.Error `json:"error"`
			Problems query.Problems  `json:"problems"`
		}
		if json.Unmarshal(raw, &body) != nil || body.Error.Code != "invalid_fanql" || len(body.Problems) != 1 || body.Problems[0].Span == nil || len(body.Problems[0].FixIts) != 2 {
			t.Fatalf("structured failure: %s", raw)
		}
	}
}

func TestQueryTelemetryUsesNeutralFailure(t *testing.T) {
	out := QueryTelemetryOutput{Error: &fanql.Failure{Code: "invalid_fanql", Message: "The query has problems."}}
	raw, err := json.Marshal(out)
	if err != nil || string(raw) != `{"error":{"code":"invalid_fanql","message":"The query has problems."}}` {
		t.Fatalf("failure: %s %v", raw, err)
	}
}

func TestPresetSchemasExcludeFanQLMetadata(t *testing.T) {
	client := connectTestClient(t, newPanelServer(t), nil)
	listed, err := client.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		raw, _ := json.Marshal(tool.OutputSchema)
		for _, field := range []string{`"notices"`} {
			has := strings.Contains(string(raw), field)
			if tool.Name == "query_telemetry" && !has || tool.Name != "query_telemetry" && has {
				t.Errorf("%s metadata %s: %s", tool.Name, field, raw)
			}
		}
	}
}

func TestFragmentRawJSONIsNotAnInteger(t *testing.T) {
	type input struct {
		Payload json.RawMessage `json:"payload"`
	}
	s := newPanelServer(t)
	tool := &mcp.Tool{Name: "test_raw_json", Description: "Test raw JSON"}
	fragmentTool(s, tool, func(context.Context, *mcp.CallToolRequest, input) (*mcp.CallToolResult, variableOptions, error) {
		return nil, variableOptions{}, nil
	})
	raw, _ := json.Marshal(tool.InputSchema)
	var schema struct {
		Properties map[string]struct {
			Type any `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(schema.Properties["payload"].Type, "integer") {
		t.Fatalf("arbitrary JSON restricted to integer: %s", raw)
	}
}
