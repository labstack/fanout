package agent

import (
	"context"
	"encoding/json"
	fanoutmcp "github.com/labstack/fanout/internal/mcp"
	"github.com/labstack/fanout/internal/panel"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	controlstore "github.com/labstack/fanout/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAppToolModelContentAndVisibility(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	fragment := map[string]any{"dashboard": map[string]any{"version": 1, "name": "Fixture", "panels": []any{map[string]any{"id": "text", "title": "Text", "viz": "text", "content": "hello"}}}, "results": []any{map[string]any{"id": "text", "status": "ok"}}}
	mcp.AddTool(server, &mcp.Tool{Name: "query_telemetry", Meta: mcp.Meta{"ui": map[string]any{"resourceUri": "ui://fanout/panels.html", "visibility": []string{"model", "app"}}}}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "compact summary"}}}, fragment, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "query_panel_fragment", Meta: mcp.Meta{"ui": map[string]any{"visibility": []string{"app"}}}}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
		return nil, fragment, nil
	})
	registry, err := NewToolRegistry(t.Context(), server)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	denied, err := registry.Execute(t.Context(), ToolCall{Name: "query_panel_fragment", Input: `{}`})
	if err != nil || !denied.IsError || denied.Structured != nil || denied.AppResourceURI != "" {
		t.Fatalf("app-only model call accepted: %+v %v", denied, err)
	}
	for _, d := range registry.Definitions() {
		if d.Name == "query_panel_fragment" {
			t.Fatal("app-only tool exposed to model")
		}
	}
	got, err := registry.Execute(t.Context(), ToolCall{Name: "query_telemetry", Input: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "compact summary" || got.Structured == nil {
		t.Fatalf("execution=%+v", got)
	}
	if _, err := registry.session.CallTool(t.Context(), &mcp.CallToolParams{Name: "query_panel_fragment", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
}

func TestFragmentActivityPersistsExactContent(t *testing.T) {
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store := NewStore(database.DB)
	input := agtypes.RunAgentInput{ThreadID: "fragment-thread", RunID: "run", Messages: []agtypes.Message{{ID: "user", Role: agtypes.RoleUser, Content: "show"}}}
	messages, err := store.StartRun(t.Context(), "owner", input)
	if err != nil {
		t.Fatal(err)
	}
	server := fanoutmcp.NewWithIntelligence(registryQueries{}, nil, panel.NewExecutor(nil, 30), nil, "test")
	tools, err := NewToolRegistry(t.Context(), server.MCP())
	if err != nil {
		t.Fatal(err)
	}
	defer tools.Close()
	call := ToolCall{ID: "call", Name: "query_telemetry", Input: `{"panel":{"id":"text","title":"Text","viz":"text","content":"hello"},"time":{"from":"2026-10-07T18:45:00Z","to":"2026-10-07T19:45:00Z","refresh":"off"}}`}
	expected, err := tools.Execute(t.Context(), call)
	if err != nil || expected.IsError {
		t.Fatalf("real MCP fixture: %+v %v", expected, err)
	}
	fragment := expected.Structured
	provider := &scriptedProvider{steps: [][]StreamEvent{{{Type: EventToolUse, ToolCall: &call}, {Type: EventStop, StopReason: "tool_calls"}}, {{Type: EventStop, StopReason: "end_turn"}}}}
	runtime := NewRuntime(provider, tools, nil)
	emitter, _ := newTestEmitter()
	if _, err := runtime.execute(t.Context(), input.ThreadID, input.RunID, &messages, emitter); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(t.Context(), "owner", input.ThreadID, input.RunID, messages, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	thread, err := store.Thread(t.Context(), "owner", input.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range thread.Messages {
		if message.Role == agtypes.RoleTool && message.Content != expected.Content {
			t.Fatal("full frames persisted as model content")
		}
		if message.Role != agtypes.RoleActivity {
			continue
		}
		found = true
		raw, _ := json.Marshal(message.Content)
		var content map[string]json.RawMessage
		if err := json.Unmarshal(raw, &content); err != nil {
			t.Fatal(err)
		}
		keys := map[string]bool{}
		for key := range content {
			keys[key] = true
		}
		if !reflect.DeepEqual(keys, map[string]bool{"resource_uri": true, "tool_name": true, "tool_input": true, "tool_result": true, "is_error": true}) {
			t.Fatalf("keys=%v", keys)
		}
		want, _ := json.Marshal(fragment)
		if string(content["tool_result"]) != string(want) {
			t.Fatalf("fragment changed on reload: %s", raw)
		}
	}
	if !found {
		t.Fatal("missing activity or re-query")
	}
}

func TestAppToolContentKeepsTextAndNonAppMarshalFailureIsError(t *testing.T) {
	r := &ToolRegistry{apps: map[string]string{"app": "ui://fanout/panels.html"}}
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "compact"}}, StructuredContent: map[string]any{"invalid": make(chan int)}}
	content, err := r.modelContent("app", result)
	if err != nil || content != "compact" {
		t.Fatalf("app content=%s %v", content, err)
	}
	if _, err := r.modelContent("ordinary", result); err == nil {
		t.Fatal("non-app marshal failure silently fell back")
	}
}

func TestAppSummariesBoundedOnSuccessAndError(t *testing.T) {
	registry := &ToolRegistry{apps: map[string]string{"app": "ui://fanout/panels.html"}}
	for _, failed := range []bool{false, true} {
		result := &mcp.CallToolResult{IsError: failed, StructuredContent: map[string]any{"frame": strings.Repeat("body", 100000)}, Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("界", 20000)}}}
		got, err := registry.modelContent("app", result)
		if err != nil || len(got) > 16*1024 || !utf8.ValidString(got) || strings.Contains(got, "body") {
			t.Fatalf("failed=%t bytes=%d err=%v", failed, len(got), err)
		}
	}
}

func TestAppToolErrorHasNoActivity(t *testing.T) {
	messages := []agtypes.Message{{ID: "user", Role: agtypes.RoleUser, Content: "show"}}
	provider := &scriptedProvider{steps: [][]StreamEvent{{{Type: EventToolUse, ToolCall: &ToolCall{ID: "call", Name: "query_telemetry", Input: `{}`}}, {Type: EventStop, StopReason: "tool_calls"}}, {{Type: EventStop, StopReason: "end_turn"}}}}
	runtime := NewRuntime(provider, &fakeTools{execution: ToolExecution{Content: "Invalid telemetry window", IsError: true, AppResourceURI: "ui://fanout/panels.html"}}, nil)
	emitter, _ := newTestEmitter()
	if _, err := runtime.execute(t.Context(), "thread", "run", &messages, emitter); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range messages {
		if message.Role == agtypes.RoleActivity {
			t.Fatal("error emitted an app activity")
		}
		if message.Role == agtypes.RoleTool {
			found = true
			if message.Content != "Invalid telemetry window" || message.Error == "" {
				t.Fatal("tool error lost")
			}
		}
	}
	if !found {
		t.Fatal("tool error missing")
	}
}

func TestDuplicateMapActivitiesStoppedAtSource(t *testing.T) {
	fragment := map[string]any{"view": map[string]any{"kind": "query", "key": "server-key"}, "dashboard": map[string]any{"version": 1, "name": "Map", "time": map[string]any{"from": "2026-10-07T18:45:00Z", "to": "2026-10-07T19:45:00Z"}, "panels": []any{map[string]any{"id": "services", "title": "Service dependencies", "viz": "service_map", "query": map[string]any{"from": "spans"}}}}, "results": []any{map[string]any{"id": "services", "status": "ok"}}}
	messages := []agtypes.Message{{ID: "user", Role: agtypes.RoleUser, Content: "map"}}
	provider := &scriptedProvider{steps: [][]StreamEvent{{{Type: EventToolUse, ToolCall: &ToolCall{ID: "custom", Name: "query_telemetry", Input: `{}`}}, {Type: EventToolUse, ToolCall: &ToolCall{ID: "preset", Name: "get_service_topology", Input: `{}`}}, {Type: EventStop, StopReason: "tool_calls"}}, {{Type: EventStop, StopReason: "end_turn"}}}}
	runtime := NewRuntime(provider, &fakeTools{execution: ToolExecution{Content: "Map summary", Structured: fragment, AppResourceURI: "ui://fanout/panels.html"}}, nil)
	emitter, _ := newTestEmitter()
	if _, err := runtime.execute(t.Context(), "thread", "run", &messages, emitter); err != nil {
		t.Fatal(err)
	}
	activities, tools := 0, 0
	for _, message := range messages {
		if message.Role == agtypes.RoleActivity {
			activities++
		}
		if message.Role == agtypes.RoleTool {
			tools++
		}
	}
	if activities != 1 || tools != 2 {
		t.Fatalf("activities=%d tool results=%d", activities, tools)
	}
}

type viewTools struct{ fakeTools }

func (f *viewTools) Execute(_ context.Context, call ToolCall) (ToolExecution, error) {
	kind := "query"
	if call.Name == "renamed_preset" {
		kind = "preset"
	}
	return ToolExecution{Content: kind, AppResourceURI: "ui://fanout/panels.html", Structured: fanoutmcp.PanelFragment{View: fanoutmcp.FragmentView{Kind: kind, Key: "authoritative-key"}}}, nil
}
func TestRuntimeUsesServerIdentityAndPresetWinner(t *testing.T) {
	for _, order := range [][]string{{"query", "renamed_preset"}, {"renamed_preset", "query"}} {
		messages := []agtypes.Message{{ID: "user", Role: agtypes.RoleUser, Content: "show"}}
		calls := []StreamEvent{}
		for i, name := range order {
			calls = append(calls, StreamEvent{Type: EventToolUse, ToolCall: &ToolCall{ID: string(rune('a' + i)), Name: name, Input: `{}`}})
		}
		calls = append(calls, StreamEvent{Type: EventStop, StopReason: "tool_calls"})
		provider := &scriptedProvider{steps: [][]StreamEvent{calls, {{Type: EventStop, StopReason: "end_turn"}}}}
		runtime := NewRuntime(provider, &viewTools{}, nil)
		emitter, _ := newTestEmitter()
		if _, err := runtime.execute(t.Context(), "thread", "run", &messages, emitter); err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, message := range messages {
			if message.Role == agtypes.RoleActivity {
				count++
				content := message.Content.(map[string]any)
				if content["tool_name"] != "renamed_preset" {
					t.Fatalf("wrong persisted winner: %v", content)
				}
			}
		}
		if count != 1 {
			t.Fatalf("activities=%d", count)
		}
	}
}
