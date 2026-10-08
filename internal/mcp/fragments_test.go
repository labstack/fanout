package mcp

import (
	"encoding/json"
	"fmt"
	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/panel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestQueryTelemetryReturnsExecutableFragment(t *testing.T) {
	s := newPanelServer(t)
	p := panel.Panel{ID: "latency", Title: "Latency", Viz: "timeseries", Query: &panel.Query{From: "spans", Measures: []string{"p95(duration_ms)"}, Bucket: "auto"}}
	_, got, err := s.queryTelemetry(t.Context(), nil, QueryTelemetryInput{Panel: p})
	if err != nil {
		t.Fatal(err)
	}
	if got.Dashboard.Version != 1 || len(got.Dashboard.Panels) != 1 || len(got.Results) != 1 || got.Results[0].ID != p.ID {
		t.Fatalf("fragment=%+v", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["data"]; ok {
		t.Fatal("bespoke view data returned")
	}
	if _, ok := decoded["dashboard"]; !ok {
		t.Fatal("missing spec")
	}
	if got.Results[0].Status == panel.StatusEmpty && got.Results[0].Diagnosis == "" {
		t.Fatal("unexplained empty")
	}
}

func TestFragmentPresetsUseSingleUnitPanels(t *testing.T) {
	for _, name := range []string{"overview", "performance", "topology", "logs", "trace"} {
		d := fragmentPreset(name, "checkout", "shop", "ERROR", "timeout", 20)
		if len(d.Panels) == 0 {
			t.Fatalf("missing %s preset", name)
		}
		for _, p := range d.Panels {
			if p.ID == "latency_error_correlation" {
				t.Fatal("retired dual-axis panel")
			}
			if p.Viz == "timeseries" && len(p.Query.Measures) > 1 {
				t.Fatalf("split incompatible measures: %+v", p)
			}
		}
		s := panelServerFixture(t, true)
		results, err := s.panels.Run(t.Context(), panel.RunRequest{Dashboard: d})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		positive := false
		for _, r := range results {
			if r.Status == panel.StatusOK && r.Frame != nil && r.Frame.Rows > 0 {
				positive = true
			}
			if r.Status != panel.StatusOK && !(r.Status == panel.StatusEmpty && r.Diagnosis != "") {
				t.Fatalf("%s panel %s: status=%s diagnosis=%q error=%q", name, r.ID, r.Status, r.Diagnosis, r.Error)
			}
		}
		if !positive {
			t.Fatalf("no positive %s results", name)
		}
	}
}
func largeLogsFragment() PanelFragment {
	d := fragmentPreset("logs", "checkout", "shop", "ERROR", "", 100)
	f := &panel.Frame{Columns: []panel.Column{{Name: "service", Type: "string", Role: "dimension"}, {Name: "body", Type: "string", Role: "dimension"}}, Values: [][]any{{}, {}}, Rows: 1000}
	for i := 0; i < f.Rows; i++ {
		f.Values[0] = append(f.Values[0], "checkout")
		f.Values[1] = append(f.Values[1], strings.Repeat("x", 2000))
	}
	return PanelFragment{Dashboard: d, Results: []panel.Result{{ID: "volume", Status: panel.StatusEmpty, Diagnosis: "No volume buckets in fixture"}, {ID: "events", Status: panel.StatusOK, Frame: f}}}
}
func TestFragmentSummaryBoundedLogsFixture(t *testing.T) {
	f := largeLogsFragment()
	a := fragmentSummary(f)
	b := fragmentSummary(f)
	if len(a) > 16*1024 || a != b {
		t.Fatalf("summary bytes=%d deterministic=%v", len(a), a == b)
	}
	for _, label := range []string{"events", "ok", "1000", "truncated", "checkout"} {
		if !strings.Contains(a, label) {
			t.Fatalf("missing %q", label)
		}
	}
	if strings.Contains(a, strings.Repeat("x", 2000)) {
		t.Fatal("unclipped body escaped into model text")
	}
}
func TestFragmentPayloadCapLogsFixture(t *testing.T) {
	original := largeLogsFragment()
	got := boundedTestFragment(t, original)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	f := got.Results[1].Frame
	if len(raw) > 256*1024 || !f.Truncated || f.Note == "" || f.Rows >= 1000 {
		t.Fatalf("bytes=%d frame=%+v", len(raw), f)
	}
	for _, values := range f.Values {
		if len(values) != f.Rows {
			t.Fatal("column length mismatch")
		}
	}
	if original.Results[1].Frame.Rows != 1000 {
		t.Fatal("mutated source fixture")
	}
}

func TestFragmentSummaryBoundedWideUnicode(t *testing.T) {
	var results []panel.Result
	for i := range 40 {
		f := &panel.Frame{Rows: 10}
		for c := range 80 {
			f.Columns = append(f.Columns, panel.Column{Name: fmt.Sprintf("value_%d", c)})
			f.Values = append(f.Values, make([]any, 10))
			for row := range 10 {
				f.Values[c][row] = strings.Repeat("界", 1000)
			}
		}
		results = append(results, panel.Result{ID: fmt.Sprintf("panel_%d", i), Status: panel.StatusOK, Frame: f, SQL: "DO NOT EXPOSE SQL", Diagnosis: "diagnosis"})
	}
	got := fragmentSummary(PanelFragment{Results: results})
	if len(got) > 16*1024 || !utf8.ValidString(got) || got != fragmentSummary(PanelFragment{Results: results}) || strings.Contains(got, "DO NOT EXPOSE SQL") {
		t.Fatal("summary bound/determinism/UTF-8/SQL")
	}
	for _, r := range results {
		if !strings.Contains(got, r.ID+": status=ok rows=10 truncated=false diagnosis=diagnosis") {
			t.Fatalf("metadata lost: %s", r.ID)
		}
	}
}

func TestFragmentPayloadLargestPreviousTrendsAndTrace(t *testing.T) {
	original := largeLogsFragment()
	original.Results[0].Frame = &panel.Frame{Columns: []panel.Column{{Name: "count"}}, Values: [][]any{{1}}, Rows: 1}
	original.Results[1].Previous = original.Results[1].Frame
	original.Results[1].Frame.Trends = map[string][][]any{"body": make([][]any, 1000)}
	for i := range 1000 {
		original.Results[1].Frame.Trends["body"][i] = []any{1, 2, 3}
	}
	original.Trace = &observability.Result[observability.TraceDetail]{Provenance: observability.Provenance{Complete: true}, Data: observability.TraceDetail{SpanCount: 1000, Spans: make([]observability.TraceSpan, 1000), Logs: make([]observability.LogEntry, 1000)}}
	for i := range 1000 {
		original.Trace.Data.Spans[i].Operation = strings.Repeat("operation", 1000)
		original.Trace.Data.Logs[i].Body = strings.Repeat("log", 1000)
	}
	got := boundedTestFragment(t, original)
	if testFragmentBytes(t, got) > fragmentPayloadLimit || got.Results[0].Frame.Rows != 1 {
		t.Fatal("cap or largest selection")
	}
	for _, f := range []*panel.Frame{got.Results[1].Frame, got.Results[1].Previous} {
		if !f.Truncated || f.Note == "" {
			t.Fatal("missing visible truncation")
		}
		for _, values := range f.Values {
			if len(values) != f.Rows {
				t.Fatal("columns not aligned")
			}
		}
		for _, trends := range f.Trends {
			if len(trends) != f.Rows {
				t.Fatal("trends not aligned")
			}
		}
	}
	if !got.Trace.Data.Truncated || got.Trace.Provenance.Complete || got.Trace.Data.SpanCount != 1000 {
		t.Fatal("trace metadata lost")
	}
	if original.Results[1].Frame.Rows != 1000 || len(original.Trace.Data.Spans) != 1000 {
		t.Fatal("mutated original")
	}
}

func TestFragmentPayloadFixedMetadataAndSmallUnchanged(t *testing.T) {
	small := PanelFragment{Dashboard: fragmentPreset("trace", "", "", "", "", 50), Results: []panel.Result{{ID: "traces", Status: panel.StatusEmpty, Diagnosis: "No traces"}}}
	if !reflect.DeepEqual(boundedTestFragment(t, small), small) {
		t.Fatal("small payload changed")
	}
	var f PanelFragment
	for i := range 40 {
		id := fmt.Sprintf("p%d", i)
		f.Dashboard.Panels = append(f.Dashboard.Panels, panel.Panel{ID: id, Title: id, Viz: "text", Content: "Text"})
		f.Results = append(f.Results, panel.Result{ID: id, Status: panel.StatusError, SQL: strings.Repeat("sql", 100000), Error: strings.Repeat("error", 100000), Diagnosis: strings.Repeat("diagnosis", 100000), Frame: &panel.Frame{Columns: []panel.Column{}, Values: [][]any{}, Note: strings.Repeat("note", 100000)}})
	}
	got := boundedTestFragment(t, f)
	if testFragmentBytes(t, got) > fragmentPayloadLimit {
		t.Fatalf("fixed metadata bytes=%d", testFragmentBytes(t, got))
	}
}

func TestFragmentVarsRoundTripAndAppHelpers(t *testing.T) {
	s := newPanelServer(t)
	session := connectTestClient(t, s, &mcp.ClientCapabilities{Extensions: map[string]any{mcpUIExtension: map[string]any{"mimeTypes": []string{mcpAppMIME}}}})
	vars := map[string]any{"all": "$__all", "scalar": "checkout", "empty": []string{}, "multi": []string{"checkout", "payment"}}
	d := panel.Dashboard{Name: "Variables", Panels: []panel.Panel{{ID: "text", Title: "Text", Viz: "text", Content: "Text"}}, Variables: []panel.Variable{{Name: "all", Kind: "custom", Options: []string{"checkout"}, IncludeAll: true}, {Name: "scalar", Kind: "text"}, {Name: "empty", Kind: "custom", Options: []string{"checkout"}, Multi: true}, {Name: "multi", Kind: "custom", Options: []string{"checkout", "payment"}, Multi: true}}}
	got, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "query_panel_fragment", Arguments: map[string]any{"dashboard": d, "vars": vars}})
	if err != nil || got.IsError {
		t.Fatalf("call=%+v %v", got, err)
	}
	raw, _ := json.Marshal(got.StructuredContent)
	var out PanelFragment
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(out.Vars)
	want, _ := json.Marshal(vars)
	if string(encoded) != string(want) {
		t.Fatalf("vars=%s want %s", encoded, want)
	}
	options, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "resolve_panel_variables", Arguments: map[string]any{"dashboard": d, "vars": vars}})
	if err != nil || options.IsError {
		t.Fatalf("options=%+v %v", options, err)
	}
	for _, v := range []any{nil, []string{}, []string{"text"}} {
		bad, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "query_panel_fragment", Arguments: map[string]any{"dashboard": d, "panels": v}})
		if err == nil && !bad.IsError {
			t.Fatalf("panels %v accepted", v)
		}
	}
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "query_panel_fragment" {
			schema, _ := json.Marshal(tool.InputSchema)
			var decoded map[string]any
			_ = json.Unmarshal(schema, &decoded)
			if _, ok := decoded["properties"].(map[string]any)["panels"]; ok {
				t.Fatal("subset schema exposed")
			}
		}
	}
}

func TestFragmentPresetsExecutedSummariesAndEndpointScope(t *testing.T) {
	s := panelServerFixture(t, true)
	for _, name := range []string{"overview", "performance", "topology", "logs", "trace"} {
		content, f, err := s.presetFragment(t.Context(), QueryInput{Namespace: "shop"}, name, "checkout", "ERROR", "timeout")
		if err != nil {
			t.Fatal(err)
		}
		if len(content.Content[0].(*mcp.TextContent).Text) > 16*1024 {
			t.Fatal("summary cap")
		}
		for _, r := range f.Results {
			if r.Status == panel.StatusError {
				t.Fatalf("%s: %+v", name, r)
			}
			if r.ID == "endpoints" {
				if r.Frame == nil {
					t.Fatal("missing endpoints frame")
				}
				cart := false
				for c, col := range r.Frame.Columns {
					if col.Name == "http_route" {
						for _, value := range r.Frame.Values[c] {
							if value == "/cart" {
								cart = true
							}
							if value == "" {
								t.Fatal("empty route included")
							}
						}
					}
				}
				if !cart {
					t.Fatal("missing /cart endpoint")
				}
			}
		}
		for _, p := range f.Dashboard.Panels {
			cap := 400
			if p.Viz == "traces" {
				cap = 50
			}
			if p.Viz == "logs" {
				cap = 100
			}
			if p.Query.Limit != cap {
				t.Fatalf("%s cap=%d", p.ID, p.Query.Limit)
			}
		}
	}
}

func TestFragmentTraceCapturedWindowAndSelectedID(t *testing.T) {
	s := panelServerFixture(t, true)
	from := time.Now().UTC().Add(-time.Hour)
	to := from.Add(time.Hour)
	_, got, err := s.trace(t.Context(), nil, TraceInput{TraceID: "trace-1", Namespace: "shop", From: &from, To: &to, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Dashboard.Time.From.Equal(from) || !got.Dashboard.Time.To.Equal(to) || got.Trace == nil || got.Trace.Data.TraceID != "trace-1" {
		t.Fatal("captured window or selected trace lost")
	}
	for _, bad := range []TraceInput{{From: &from}, {To: &to}, {From: &from, To: &from}, {From: &from, To: &to, Window: "1h"}, {Limit: 501}, {Limit: -1}} {
		if _, _, err := s.trace(t.Context(), nil, bad); err == nil {
			t.Fatalf("bad input accepted: %+v", bad)
		}
	}
}

func TestFragmentPayloadCapFixedWideFramesAndScopes(t *testing.T) {
	f := PanelFragment{Dashboard: fragmentPreset("trace", "", "", "", "", 50)}
	for i := range 40 {
		r := panel.Result{ID: fmt.Sprintf("p%d", i), Status: panel.StatusOK, Frame: &panel.Frame{Rows: 1}, AnnotationScope: &panel.AnnotationMatch{}}
		for range 300 {
			r.Frame.Columns = append(r.Frame.Columns, panel.Column{Name: strings.Repeat("label", 50), Type: "string", Role: "dimension"})
			r.Frame.Values = append(r.Frame.Values, []any{"v"})
			r.AnnotationScope.Services = append(r.AnnotationScope.Services, panel.AnnotationService{Namespace: "shop", Service: strings.Repeat("service", 100)})
		}
		f.Results = append(f.Results, r)
	}
	got := boundedTestFragment(t, f)
	if testFragmentBytes(t, got) > fragmentPayloadLimit {
		t.Fatalf("fixed metadata escaped cap: %d", testFragmentBytes(t, got))
	}
	for _, r := range got.Results {
		if !r.Frame.Truncated || r.Frame.Note == "" || len(r.Frame.Columns) != len(r.Frame.Values) {
			t.Fatal("missing/alignment metadata")
		}
	}
}

func TestFragmentPresetsRejectExplicitZeroLimitBeforeExecution(t *testing.T) {
	s := newPanelServer(t)
	client := connectTestClient(t, s, nil)
	for _, name := range []string{"get_observability_overview", "get_service_topology", "get_service_performance", "inspect_trace", "search_logs"} {
		result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{"limit": 0}})
		if err == nil && !result.IsError {
			t.Fatalf("explicit zero limit accepted: %s", name)
		}
	}
}

func TestFragmentNamedToolsMCPSeededContract(t *testing.T) {
	s := panelServerFixture(t, true)
	for _, apps := range []bool{false, true} {
		var capabilities *mcp.ClientCapabilities
		if apps {
			capabilities = &mcp.ClientCapabilities{Extensions: map[string]any{mcpUIExtension: map[string]any{"mimeTypes": []string{mcpAppMIME}}}}
		}
		client := connectTestClient(t, s, capabilities)
		for _, name := range []string{"get_observability_overview", "get_service_topology", "get_service_performance", "search_logs", "inspect_trace", "query_telemetry"} {
			args := map[string]any{"namespace": "shop"}
			if name == "query_telemetry" {
				args = map[string]any{"panel": panel.Panel{ID: "custom", Title: "Requests", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"count()"}}}}
			}
			if name == "inspect_trace" {
				args["trace_id"] = "trace-1"
			}
			result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
			if err != nil || result.IsError {
				t.Fatalf("apps=%t %s: %+v %v", apps, name, result, err)
			}
			raw, _ := json.Marshal(result.StructuredContent)
			var f PanelFragment
			if err := json.Unmarshal(raw, &f); err != nil {
				t.Fatal(err)
			}
			if len(raw) > fragmentPayloadLimit || f.Dashboard.Version != 1 || len(f.Dashboard.Panels) != len(f.Results) {
				t.Fatal("invalid full fragment")
			}
			positive := false
			for i, r := range f.Results {
				if f.Dashboard.Panels[i].ID != r.ID || r.Status == panel.StatusError || r.Status == panel.StatusEmpty && r.Diagnosis == "" {
					t.Fatalf("%s: %+v", name, r)
				}
				if r.Frame != nil && r.Frame.Rows > 0 {
					positive = true
				}
			}
			if !positive {
				t.Fatalf("%s has no positive result", name)
			}
			if name == "inspect_trace" && (f.Trace == nil || f.Trace.Data.TraceID != "trace-1") {
				t.Fatal("missing exact trace")
			}
			if result.Content[0].(*mcp.TextContent).Text != fragmentSummary(f) {
				t.Fatal("summary differs from bounded fragment")
			}
		}
	}
}

func TestQueryTelemetryDoesNotMutateAuthoredInput(t *testing.T) {
	s := newPanelServer(t)
	input := QueryTelemetryInput{Panel: panel.Panel{ID: "latency", Title: "Latency", Viz: "timeseries", Query: &panel.Query{From: "spans", Measures: []string{"p95(duration_ms)"}}}}
	before, _ := json.Marshal(input)
	_, _, err := s.queryTelemetry(t.Context(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(input)
	if string(after) != string(before) {
		t.Fatalf("authored input changed: %s", after)
	}
}

func boundedTestFragment(t *testing.T, f PanelFragment) PanelFragment {
	t.Helper()
	out, err := boundFragment(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func testFragmentBytes(t *testing.T, f PanelFragment) int {
	t.Helper()
	size, err := jsonBytes(f)
	if err != nil {
		t.Fatal(err)
	}
	return size
}
