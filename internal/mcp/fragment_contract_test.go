package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/labstack/fanout/internal/observability"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/labstack/fanout/internal/panel"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOmittedArgumentsRawJSONRPC(t *testing.T) {
	s := newPanelServer(t)
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s.mcp }, &sdk.StreamableHTTPOptions{Stateless: true})
	for _, name := range []string{"get_telemetry_schema", "list_dashboards", "get_observability_overview"} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/api/mcp", strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q}}`, name)))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json, text/event-stream")
			request.Header.Set("MCP-Protocol-Version", "2025-11-25")
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, request)
			body := recorder.Body.String()
			var response struct{ Error json.RawMessage }
			for _, line := range strings.Split(body, "\n") {
				if strings.HasPrefix(line, "data: ") {
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &response); err != nil {
						t.Fatal(err)
					}
				}
			}
			if strings.Contains(body, "unexpected end of JSON") || len(response.Error) != 0 || recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, body)
			}
		})
	}
}

func TestLimitCheckOnlyAppliesToRegisteredLimitInputs(t *testing.T) {
	s := newPanelServer(t)
	client := connectTestClient(t, s, nil)
	result, err := client.CallTool(t.Context(), &sdk.CallToolParams{Name: "get_telemetry_schema", Arguments: map[string]any{"limit": 0}})
	if err == nil && result != nil {
		for _, content := range result.Content {
			if text, ok := content.(*sdk.TextContent); ok && strings.Contains(text.Text, "limit must be from 1 to 500") {
				t.Fatal("fragment validation intercepted an unrelated tool")
			}
		}
	}
}

func TestEscapedPayloadAndIntegerPrecision(t *testing.T) {
	f := PanelFragment{Dashboard: fragmentPreset("trace", "", "", "", "", 50)}
	for i := range 40 {
		f.Results = append(f.Results, panel.Result{ID: fmt.Sprint(i), Status: panel.StatusError, SQL: strings.Repeat("\x00", 10000), Error: strings.Repeat("\x00", 10000), Diagnosis: strings.Repeat("\x00", 10000), Frame: &panel.Frame{Columns: []panel.Column{}, Values: [][]any{}, Note: strings.Repeat("\x00", 10000)}})
	}
	got := boundedTestFragment(t, f)
	raw, err := json.Marshal(got)
	if err != nil || len(raw) > fragmentPayloadLimit {
		t.Fatalf("escaped payload=%d err=%v", len(raw), err)
	}
	n := int64(9007199254740993)
	f.Results = []panel.Result{{ID: "traces", Status: panel.StatusOK, Frame: &panel.Frame{Columns: []panel.Column{{Name: "count", Type: "number", Role: "measure"}}, Values: [][]any{{n}}, Rows: 1}}}
	got = boundedTestFragment(t, f)
	if fmt.Sprint(got.Results[0].Frame.Values[0][0]) != fmt.Sprint(n) {
		t.Fatal("integer precision lost")
	}
}

func TestSummaryReadableCells(t *testing.T) {
	result := panel.Result{ID: "latency", Status: panel.StatusOK, Frame: &panel.Frame{Rows: 7, Columns: []panel.Column{{Name: "time", Type: "time", Role: "time"}, {Name: "p95", Type: "number", Role: "measure", Unit: "ms"}, {Name: "absent", Type: "string"}}, Values: [][]any{{int64(1791486900000), int64(1791486900000), int64(1791486900000), int64(1791486900000), int64(1791486900000), int64(1791486900000), int64(1791486900000)}, {42, 42, 42, 42, 42, 42, 42}, {nil, nil, nil, nil, nil, nil, nil}}}}
	text := fragmentSummary(PanelFragment{Results: []panel.Result{result}})
	if !strings.Contains(text, time.UnixMilli(1791486900000).UTC().Format(time.RFC3339)) || !strings.Contains(text, "42 ms") || strings.Contains(text, "absent=") {
		t.Fatalf("unreadable summary: %s", text)
	}
	if strings.Count(text, "p95=") != 5 {
		t.Fatalf("sample count: %s", text)
	}
	result.Frame.Truncated = true
	if !strings.Contains(fragmentSummary(PanelFragment{Results: []panel.Result{result}}), "truncated=true") {
		t.Fatal("missing true truncation")
	}
}

func TestSummaryKeepsEverySupportedUnit(t *testing.T) {
	for unit, want := range map[string]string{"ns": "42 ns", "per_minute": "42/min", "ratio": "42×"} {
		if got := summaryCell(panel.Column{Type: "number", Unit: unit}, 42); got != want {
			t.Fatalf("%s: %s, want %s", unit, got, want)
		}
	}
}

func TestClipSmallBudgets(t *testing.T) {
	for _, text := range []string{"hello", "界"} {
		for n := 0; n < 3; n++ {
			got := clip(text, n)
			if len(got) > n || !utf8.ValidString(got) {
				t.Fatalf("budget %d exceeded or invalid UTF-8: %q", n, got)
			}
		}
	}
}

func TestPresetsHaveTitlesAndSearchLimit(t *testing.T) {
	for _, kind := range []string{"overview", "performance", "topology", "logs", "trace"} {
		if fragmentPreset(kind, "", "", "", "", 50).Name == "Telemetry" {
			t.Fatalf("generic title: %s", kind)
		}
	}
	s := newPanelServer(t)
	if _, _, err := s.logs(t.Context(), nil, LogsInput{Search: strings.Repeat("x", 201)}); err == nil {
		t.Fatal("oversized search accepted")
	}
}

func TestEncodingFailureCancellationAndMetadata(t *testing.T) {
	f := PanelFragment{Dashboard: fragmentPreset("overview", "", "", "", "", 50), Results: []panel.Result{{ID: "health", Status: panel.StatusOK, Frame: &panel.Frame{Columns: []panel.Column{}, Values: [][]any{}, Totals: []any{math.NaN()}}}}}
	if _, err := boundFragment(t.Context(), f); err == nil {
		t.Fatal("encoding error swallowed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := boundFragment(ctx, f); err != context.Canceled {
		t.Fatalf("cancellation=%v", err)
	}
	f.Results[0].Frame.Totals = nil
	f.Trace = &observability.Result[observability.TraceDetail]{Summary: strings.Repeat("\x00", 100000), Data: observability.TraceDetail{TraceID: strings.Repeat("metadata", 100000), Spans: []observability.TraceSpan{}, Logs: []observability.LogEntry{}, Services: []string{}}}
	got, err := boundFragment(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	if testFragmentBytes(t, got) > fragmentPayloadLimit || !got.Results[0].Frame.Truncated || got.Results[0].Frame.Note == "" {
		t.Fatal("metadata cap not visible")
	}
}
func TestTopologyEdgesAndOverviewTraceSummary(t *testing.T) {
	f := PanelFragment{Dashboard: fragmentPreset("topology", "", "", "", "", 50)}
	frame := &panel.Frame{Columns: []panel.Column{{Name: "kind"}, {Name: "caller"}, {Name: "callee"}, {Name: "error_rate", Type: "number", Unit: "percent"}, {Name: "calls", Type: "number", Unit: "count"}}, Values: make([][]any, 5)}
	for i := range 20 {
		for c, v := range []any{"node", fmt.Sprint(i), nil, nil, nil} {
			frame.Values[c] = append(frame.Values[c], v)
		}
		frame.Rows++
	}
	for i := range 12 {
		for c, v := range []any{"edge", "checkout", fmt.Sprintf("edge_%02d", i), i / 3, 12 - i} {
			frame.Values[c] = append(frame.Values[c], v)
		}
		frame.Rows++
	}
	f.Results = []panel.Result{{ID: "services", Status: panel.StatusOK, Frame: frame}}
	text := fragmentSummary(f)
	if strings.Count(text, "caller=") != 10 || strings.Index(text, "edge_09") > strings.Index(text, "edge_10") {
		t.Fatalf("edges not ranked/sample bounded: %s", text)
	}
	frame.Health = &panel.HealthFrame{Health: "unhealthy", Counts: observability.HealthCounts{Healthy: 2, Degraded: 3, Unhealthy: 4}}
	f.Trace = &observability.Result[observability.TraceDetail]{Data: observability.TraceDetail{TraceID: "trace", DurationMS: 42, SpanCount: 7, HasError: true, Spans: []observability.TraceSpan{{Operation: "GET cart", DurationMS: 42, Status: "STATUS_CODE_ERROR", Start: time.Date(2026, 10, 7, 19, 0, 0, 0, time.UTC)}}}}
	text = fragmentSummary(f)
	for _, part := range []string{"healthy=2 degraded=3 unhealthy=4", "root=GET cart", "duration=42 ms", "status=Error spans=7", "slowest:", "erroring:"} {
		if !strings.Contains(text, part) {
			t.Fatalf("missing %s: %s", part, text)
		}
	}
}

// Actual structured MCP responses are also consumed by the host render tests.
func TestFragmentNamedToolsMCPSeededGoldens(t *testing.T) {
	now := time.Date(2026, 10, 7, 19, 45, 0, 0, time.UTC)
	s := panelServerFixtureAt(t, true, now)
	client := connectTestClient(t, s, nil)
	root := filepath.Join("..", "..", "ui", "host", "tests", "go-fragments")
	for kind, name := range map[string]string{"overview": "get_observability_overview", "topology": "get_service_topology", "performance": "get_service_performance", "logs": "search_logs", "trace": "inspect_trace"} {
		args := map[string]any{"namespace": "shop"}
		if kind == "trace" {
			args["trace_id"] = "trace-1"
		}
		result, err := client.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError {
			t.Fatalf("%s: %+v %v", name, result, err)
		}
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var f PanelFragment
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if err := decoder.Decode(&f); err != nil {
			t.Fatal(err)
		}
		for i := range f.Results {
			f.Results[i].ElapsedMS = 0
		}
		if f.Trace != nil {
			f.Trace.Provenance.QueryID = "seeded-trace"
			f.Trace.Provenance.Generated = now
		}
		raw, err = json.MarshalIndent(f, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, '\n')
		summary := fragmentSummary(f)
		if result.Content[0].(*sdk.TextContent).Text != summary {
			t.Fatal("summary diverged from structured response")
		}
		for suffix, contents := range map[string][]byte{".json": raw, ".summary.txt": []byte(summary)} {
			path := filepath.Join(root, kind+suffix)
			if os.Getenv("UPDATE_FRAGMENT_GOLDENS") == "1" {
				if err := os.MkdirAll(root, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, contents, 0644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(want) != string(contents) {
				t.Fatalf("%s golden changed", path)
			}
		}
	}
}

func TestToolDocsDescribeFragmentInputs(t *testing.T) {
	tools, err := DescribeTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if tool.Name == "query_telemetry" || tool.Name == "query_panel_fragment" || tool.Name == "list_panel_exemplars" || tool.Name == "resolve_panel_variables" {
			for _, input := range tool.Inputs {
				if input.Description == "" {
					t.Fatalf("%s.%s missing input description", tool.Name, input.Name)
				}
			}
		}
	}
	for _, tool := range tools {
		if tool.Name == "query_telemetry" && !strings.Contains(tool.Description, "128 KiB") {
			t.Fatal("missing authored input budget")
		}
	}
}

func TestInputBudgetReportsActualBytes(t *testing.T) {
	s := newPanelServer(t)
	_, _, err := s.queryTelemetry(t.Context(), nil, QueryTelemetryInput{Panel: panel.Panel{ID: "text", Title: "Text", Viz: "text", Content: strings.Repeat("x", 140000)}})
	if err == nil || !strings.Contains(err.Error(), "limit 131072 bytes, actual 140") {
		t.Fatalf("input budget error=%v", err)
	}
}

func BenchmarkBoundFragmentLarge(b *testing.B) {
	fragment := PanelFragment{Dashboard: fragmentPreset("logs", "", "", "", "", 100)}
	body := strings.Repeat("x", 346)
	for i := range 40 {
		values := make([]any, 5000)
		for row := range values {
			values[row] = body
		}
		fragment.Results = append(fragment.Results, panel.Result{ID: fmt.Sprintf("logs_%d", i), Status: panel.StatusOK, Frame: &panel.Frame{Columns: []panel.Column{{Name: "body", Type: "string", Role: "dimension"}}, Values: [][]any{values}, Rows: len(values)}})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		out, err := boundFragment(b.Context(), fragment)
		if err != nil {
			b.Fatal(err)
		}
		if size, err := jsonBytes(out); err != nil || size > fragmentPayloadLimit {
			b.Fatalf("size=%d err=%v", size, err)
		}
	}
}
