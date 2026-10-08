package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/query"
	controlstore "github.com/labstack/fanout/internal/store"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

// The Go service/executor/encoder own these fixtures. Only volatile IDs,
// timestamps and execution durations are normalized; wire fields stay intact.
func TestDashboardEvalGoldensCurrent(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	engine, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	from := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	if err = repo.Commit(t.Context(), telemetrystore.Batch{ID: "eval_fixture", Spans: []telemetry.Span{{Namespace: "shop", ServiceName: "checkout", TraceID: "trace", SpanID: "span", Name: "request", Kind: "SPAN_KIND_SERVER", StartUnixNanos: from.UnixNano(), EndUnixNanos: from.Add(time.Millisecond).UnixNano(), DurationMS: 1, StatusCode: "STATUS_CODE_OK", IngestedAt: from.UnixNano()}}}); err != nil {
		t.Fatal(err)
	}
	executor := panel.NewExecutor(engine, 30)
	db, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.DB.Exec(`INSERT INTO users (id,email) VALUES ('owner','owner@example.test')`); err != nil {
		t.Fatal(err)
	}
	service := dashboard.New(db.DB, executor)
	authored := panel.Dashboard{Name: "Eval fixture", Time: panel.Time{From: &from, To: &to}, Panels: []panel.Panel{
		{ID: "actual_latency", Title: "Latency", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"p95(duration_ms)"}, Where: []string{"service = 'checkout'", "namespace = 'shop'"}}, Unit: "ms", Thresholds: []panel.Threshold{{Value: 100, Status: "warn"}}},
		{ID: "actual_text", Title: "Explanation", Viz: "text", Content: "Seeded checkout request.", Description: "Fixture context."},
	}}
	authoredJSON := evalObject(t, authored)
	created, err := service.Create(t.Context(), "owner", authored, dashboard.Author{Kind: "agent", ID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	added := panel.Panel{ID: "added_panel", Title: "Added fixture panel", Viz: "timeseries", Query: &panel.Query{From: "spans", Measures: []string{"count()"}}}
	addedJSON := evalObject(t, added)
	edited, err := service.Edit(t.Context(), "owner", created.ID, []dashboard.Operation{{Op: "add_panel", Panel: &added, After: "actual_latency"}}, created.Version, dashboard.Author{Kind: "agent", ID: "owner"}, "Add fixture")
	if err != nil {
		t.Fatal(err)
	}
	statAdded := panel.Panel{ID: "added_stat", Title: "Added stat", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"count()"}}}
	statJSON := evalObject(t, statAdded)
	statEdited, err := service.Edit(t.Context(), "owner", created.ID, []dashboard.Operation{{Op: "add_panel", Panel: &statAdded}}, edited.Version, dashboard.Author{Kind: "agent", ID: "owner"}, "Add stat fixture")
	if err != nil {
		t.Fatal(err)
	}
	results, err := executor.Run(t.Context(), panel.RunRequest{Dashboard: created.Spec})
	if err != nil {
		t.Fatal(err)
	}
	addedResults, err := executor.Run(t.Context(), panel.RunRequest{Dashboard: edited.Spec})
	if err != nil {
		t.Fatal(err)
	}
	for i := range results {
		results[i].ElapsedMS = 0
	}
	for i := range addedResults {
		addedResults[i].ElapsedMS = 0
	}
	if results[1].Status != "ok" || results[1].Frame != nil {
		t.Fatalf("text shape: %#v", results[1])
	}
	record := evalObject(t, created)
	record["id"] = "golden_board"
	record["created_at"] = "2026-10-01T12:00:00Z"
	record["updated_at"] = "2026-10-01T12:00:00Z"
	content, _ := json.Marshal(map[string]any{"dashboard": record})
	wire := func(terminal StreamEvent) string {
		terminal.Usage = &TokenUsage{InputTokens: 2, OutputTokens: 1}
		p := &meteredProvider{scriptedProvider{steps: [][]StreamEvent{{{Type: EventToolUse, ToolCall: &ToolCall{ID: "save", Name: "create_dashboard", Input: `{}`}}, {Type: EventStop, StopReason: "tool_calls", Usage: &TokenUsage{InputTokens: 2, OutputTokens: 1}}}, {terminal}}}}
		runtime := NewRuntime(p, &fakeTools{execution: ToolExecution{Content: string(content)}}, nil)
		emitter, out := newTestEmitter()
		messages := []agtypes.Message{}
		_, _ = runtime.execute(t.Context(), "thread", "run", &messages, emitter)
		return evalStableSSE(t, out.String())
	}
	incomplete := wire(StreamEvent{Type: EventStop, StopReason: "length"})
	if !strings.Contains(incomplete, `"name":"model_configuration"`) {
		t.Fatal("missing configured default identity")
	}
	failure := wire(StreamEvent{Type: EventError, Error: "private provider body"})
	if !strings.Contains(incomplete, `"status":"incomplete"`) || strings.Contains(incomplete, `"truncated"`) {
		t.Fatal("missing real incomplete status")
	}
	if !strings.Contains(failure, `"type":"RUN_ERROR"`) || strings.Contains(failure, "private provider body") {
		t.Fatal("run error shape")
	}
	value := map[string]any{"authored_spec": authoredJSON, "saved_record": record, "saved_spec": created.Spec, "add_panel": addedJSON, "stat_add_panel": statJSON, "stat_added_spec": statEdited.Spec, "added_spec": edited.Spec, "results": results, "added_results": addedResults, "incomplete_sse": incomplete, "error_sse": failure}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	path := filepath.Join("..", "..", "scripts", "dashboard-eval", "testdata", "server.json")
	if os.Getenv("FANOUT_EVAL_UPDATE_GOLDENS") == "1" {
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
	checked, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, checked) {
		t.Fatal("dashboard eval goldens stale; run just dashboard-eval-generate")
	}
}
func evalObject(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func evalStableSSE(t *testing.T, wire string) string {
	t.Helper()
	ids := map[string]string{}
	var out strings.Builder
	for _, line := range strings.Split(wire, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var e map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"timestamp", "messageId", "parentMessageId"} {
			if v, ok := e[key]; ok {
				if key == "timestamp" {
					e[key] = 0
				} else {
					s := v.(string)
					if ids[s] == "" {
						ids[s] = "message_" + string(rune('a'+len(ids)))
					}
					e[key] = ids[s]
				}
			}
		}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		out.WriteString("data: " + string(raw) + "\n\n")
	}
	return out.String()
}
