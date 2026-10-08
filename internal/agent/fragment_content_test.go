package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

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
	fragment := map[string]any{"dashboard": map[string]any{"version": 1, "name": "Fixture", "panels": []any{map[string]any{"id": "text", "title": "Text", "viz": "text", "content": "hello"}}}, "results": []any{map[string]any{"id": "text", "status": "ok"}}}
	provider := &scriptedProvider{steps: [][]StreamEvent{{{Type: EventToolUse, ToolCall: &ToolCall{ID: "call", Name: "query_telemetry", Input: `{}`}}, {Type: EventStop, StopReason: "tool_calls"}}, {{Type: EventStop, StopReason: "end_turn"}}}}
	tools := &fakeTools{execution: ToolExecution{Content: "compact summary", Structured: fragment, AppResourceURI: "ui://fanout/panels.html"}}
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
		if message.Role == agtypes.RoleTool && message.Content != "compact summary" {
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
	if !found || len(tools.calls) != 1 {
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
