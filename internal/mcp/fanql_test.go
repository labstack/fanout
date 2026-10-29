package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/fanql"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/toolerror"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestQueryTelemetryAcceptsFanQLText(t *testing.T) {
	s := panelServerFixture(t, true)
	p := panel.Panel{ID: "t", Title: "t", Viz: "table"}
	input := QueryTelemetryInput{Panel: p, Text: "spans where status:error | count() by service"}
	_, got, err := s.queryTelemetry(t.Context(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	want := &panel.Query{From: "spans", Where: []string{"status = 'STATUS_CODE_ERROR'"}, Measures: []string{"count()"}, By: []string{"service"}}
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

func (e *fanqlRunCounter) Run(ctx context.Context, req panel.RunRequest) ([]panel.Result, error) {
	e.calls++
	return e.panelExecutor.Run(ctx, req)
}

func checkFanQLToolFailure(t *testing.T, input QueryTelemetryInput, code, message string) {
	t.Helper()
	s := newPanelServer(t)
	counter := &fanqlRunCounter{panelExecutor: s.panels}
	s.panels = counter
	_, _, err := s.queryTelemetry(t.Context(), nil, input)
	if err == nil {
		t.Fatal("invalid input accepted")
	}
	var failure toolerror.Result
	if json.Unmarshal([]byte(err.Error()), &failure) != nil || failure.Error == nil || failure.Error.Code != code || !strings.Contains(failure.Error.Message, message) {
		t.Fatalf("error: %v", err)
	}
	if counter.calls != 0 {
		t.Fatalf("executor ran %d times", counter.calls)
	}
}

func TestQueryTelemetryRejectsTextWithQueryOrSQL(t *testing.T) {
	for _, p := range []panel.Panel{{Query: &panel.Query{From: "spans"}}, {SQL: "SELECT 1"}} {
		checkFanQLToolFailure(t, QueryTelemetryInput{Panel: p, Text: "spans"}, "invalid_input", "text cannot be combined with panel.query or panel.sql")
	}
}

func TestQueryTelemetryReportsFanQLProblems(t *testing.T) {
	checkFanQLToolFailure(t, QueryTelemetryInput{Text: "spans where a:1 b:2 or c:3"}, "invalid_fanql", "mixed and/or needs parentheses")
}

func TestQueryTelemetryRejectsUnsupportedFanQLVersion(t *testing.T) {
	checkFanQLToolFailure(t, QueryTelemetryInput{Text: "spans", FanQLVersion: 2}, "unsupported_fanql_version", "this server reads FanQL 1")
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
	if len(r.Contents) != 1 || r.Contents[0].MIMEType != "text/markdown" || r.Contents[0].Text != fanql.Reference() || !strings.Contains(r.Contents[0].Text, "FanQL 1") || !strings.Contains(r.Contents[0].Text, "Preview") {
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
		for _, field := range []string{`"text"`, `"fanql_version"`} {
			if !strings.Contains(string(raw), field) {
				t.Fatalf("missing %s: %s", field, raw)
			}
		}
		return
	}
	t.Fatal("query_telemetry missing")
}
