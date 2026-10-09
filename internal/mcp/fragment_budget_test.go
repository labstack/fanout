package mcp

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/panel"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func nonuniformFragment(values []any) PanelFragment {
	return PanelFragment{Dashboard: fragmentPreset("logs", "", "", "", "", 100), Results: []panel.Result{
		{ID: "volume", Status: "ok", Frame: &panel.Frame{Columns: []panel.Column{{Name: "count", Type: "number", Role: "measure"}}, Values: [][]any{{1, 2, 3}}, Rows: 3}},
		{ID: "events", Status: "ok", Frame: &panel.Frame{Columns: []panel.Column{{Name: "body", Type: "string", Role: "dimension"}}, Values: [][]any{values}, Rows: len(values)}},
	}}
}
func TestHeadHeavyRowsKeepSmallPanelAndMaximalPrefix(t *testing.T) {
	values := make([]any, 500)
	for i := range values {
		n := 1000
		if i < 250 {
			n = 2000
		}
		values[i] = strings.Repeat("x", n)
	}
	out, err := boundFragment(t.Context(), nonuniformFragment(values))
	if err != nil {
		t.Fatal(err)
	}
	if out.Results[0].Frame.Rows != 3 || out.Results[1].Frame.Rows == 0 {
		t.Fatalf("lost recoverable rows: %d, %d", out.Results[0].Frame.Rows, out.Results[1].Frame.Rows)
	}
	size, _ := jsonBytes(out)
	if size > fragmentPayloadLimit {
		t.Fatal(size)
	}
	rows := out.Results[1].Frame.Rows
	out.Results[1].Frame.Rows++
	out.Results[1].Frame.Values[0] = append(out.Results[1].Frame.Values[0], values[rows])
	size, _ = jsonBytes(out)
	if size <= fragmentPayloadLimit {
		t.Fatalf("unnecessarily cut a log row: %d bytes", size)
	}
}
func TestNonuniformRowsProperty(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			values := make([]any, 500)
			for i := range values {
				values[i] = strings.Repeat("<x", 50+rng.Intn(1900))
			}
			out, err := boundFragment(t.Context(), nonuniformFragment(values))
			if err != nil {
				t.Fatal(err)
			}
			size, _ := jsonBytes(out)
			if size > fragmentPayloadLimit || out.Results[0].Frame.Rows != 3 || out.Results[1].Frame.Rows == 0 {
				t.Fatalf("size=%d rows=%d,%d", size, out.Results[0].Frame.Rows, out.Results[1].Frame.Rows)
			}
		})
	}
}
func TestLargeTraceKeepsRootErrorsAndSlowest(t *testing.T) {
	f := nonuniformFragment([]any{"trace"})
	spans := make([]observability.TraceSpan, 2000)
	for i := range spans {
		op := strings.Repeat("o", 50)
		if i < 600 {
			op = strings.Repeat("O", 600)
		}
		spans[i] = observability.TraceSpan{SpanID: fmt.Sprint(i), ParentSpanID: "0", Operation: op, Service: "svc", DurationMS: float64(i)}
	}
	spans[0].ParentSpanID = ""
	spans[1998].Status = "ERROR"
	spans[1997].Status = "ERROR"
	f.Trace = &observability.Result[observability.TraceDetail]{Data: observability.TraceDetail{TraceID: "t1", Spans: spans, Logs: []observability.LogEntry{}, Services: []string{"svc"}, SpanCount: 2000}, Provenance: observability.Provenance{Complete: true}}
	out, err := boundFragment(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	if out.Trace == nil {
		t.Fatal("trace detail was dropped")
	}
	found := map[string]bool{}
	for _, s := range out.Trace.Data.Spans {
		found[s.SpanID] = true
	}
	for _, id := range []string{"0", "1997", "1998", "1999"} {
		if !found[id] {
			t.Fatalf("lost priority span %s", id)
		}
	}
	if !out.Trace.Data.Truncated || out.Trace.Provenance.Complete || out.Trace.Summary != payloadLimitNote || len(out.Trace.Data.Spans) >= 2000 {
		t.Fatal("missing reduction note/provenance")
	}
	size, _ := jsonBytes(out)
	if size > fragmentPayloadLimit {
		t.Fatal(size)
	}
}
func TestNullArgumentsRawJSONRPC(t *testing.T) {
	s := newPanelServer(t)
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s.mcp }, &sdk.StreamableHTTPOptions{Stateless: true})
	for _, name := range []string{"get_telemetry_schema", "list_dashboards", "get_observability_overview"} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/api/mcp", strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"_meta":{"io.fanout/owner-id":"owner"},"arguments":null}}`, name)))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json, text/event-stream")
			request.Header.Set("MCP-Protocol-Version", "2025-11-25")
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, request)
			body := recorder.Body.String()
			if recorder.Code != http.StatusOK || strings.Contains(body, `"isError":true`) || strings.Contains(body, `"error":{`) {
				t.Fatalf("status=%d body=%s", recorder.Code, body)
			}
		})
	}
}
func TestViewMetadataOnWire(t *testing.T) {
	s := newPanelServer(t)
	client := connectTestClient(t, s, nil)
	for _, name := range []string{"query_telemetry", "get_service_topology"} {
		args := map[string]any{}
		kind := "preset"
		if name == "query_telemetry" {
			kind = "query"
			args["panel"] = map[string]any{"id": "p", "title": "Map", "viz": "service_map", "query": map[string]any{"from": "spans"}}
		}
		out, err := client.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil || out.IsError {
			t.Fatalf("%s: %v %+v", name, err, out)
		}
		raw, _ := json.Marshal(out.StructuredContent)
		var value struct {
			View struct {
				Kind string
				Key  string
			}
		}
		if err = json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		if value.View.Kind != kind || len(value.View.Key) != 64 {
			t.Fatalf("%s missing server identity: %s", name, raw)
		}
	}
}

func TestTracePrioritySurvivesNeighbourTrimming(t *testing.T) {
	values := make([]any, 500)
	for i := range values {
		values[i] = strings.Repeat("log", 400)
	}
	f := nonuniformFragment(values)
	spans := make([]observability.TraceSpan, 2000)
	for i := range spans {
		spans[i] = observability.TraceSpan{SpanID: fmt.Sprint(i), ParentSpanID: "0", Operation: strings.Repeat("operation", 80), DurationMS: float64(i)}
	}
	spans[0].ParentSpanID = ""
	spans[1998].Status = "ERROR"
	f.Trace = &observability.Result[observability.TraceDetail]{Data: observability.TraceDetail{Spans: spans}}
	out, err := boundFragment(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	if out.Trace != nil {
		for _, span := range out.Trace.Data.Spans {
			found[span.SpanID] = true
		}
	}
	for _, id := range []string{"0", "1998", "1999"} {
		if !found[id] {
			t.Fatalf("lost priority span %s while neighbouring rows could be cut", id)
		}
	}
}
