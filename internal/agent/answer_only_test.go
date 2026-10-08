package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/auth"
	controlstore "github.com/labstack/fanout/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAnswerOnlyAllowsReviewedAndAnnotatedReadsAndRefusesUnknownBehavior(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "behavior", Version: "1"}, nil)
	executed := map[string]int{}
	cases := []struct {
		name        string
		annotations *mcp.ToolAnnotations
		readOnly    bool
	}{
		{"query_telemetry", nil, true}, {"get_dashboard", nil, true}, {"list_dashboard_versions", nil, true},
		{"annotated_read", &mcp.ToolAnnotations{ReadOnlyHint: true}, true},
		{"annotated_write", &mcp.ToolAnnotations{}, false}, {"unclassified", nil, false},
		{"create_dashboard", &mcp.ToolAnnotations{ReadOnlyHint: true}, false},
	}
	for _, item := range cases {
		mcp.AddTool(server, &mcp.Tool{Name: item.name, Annotations: item.annotations}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
			executed[item.name]++
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Read evidence"}}}, struct{}{}, nil
		})
	}
	registry, err := NewToolRegistry(t.Context(), server)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	runtime := NewRuntime(textProvider{}, registry, nil)
	ctx := context.WithValue(t.Context(), answerOnlyKey{}, true)
	for _, item := range cases {
		result, err := runtime.executeTool(ctx, ToolCall{Name: item.name, Input: `{}`})
		if err != nil || result.IsError == item.readOnly {
			t.Fatalf("%s result=%+v err=%v", item.name, result, err)
		}
		want := 0
		if item.readOnly {
			want = 1
		}
		if executed[item.name] != want {
			t.Fatalf("%s executed %d times", item.name, executed[item.name])
		}
	}
	result, err := runtime.executeTool(ctx, ToolCall{Name: "not_registered", Input: `{}`})
	if err != nil || !result.IsError {
		t.Fatal("unknown tool permitted", result, err)
	}
}

func TestAnswerOnlyRefusesWritesBeforeExecuteAndAllowsNextOrdinaryTurn(t *testing.T) {
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.DB.Exec(`INSERT INTO users(id,email) VALUES ('owner','owner@example.test')`); err != nil {
		t.Fatal(err)
	}
	names := []string{"create_dashboard", "edit_dashboard", "replace_dashboard", "restore_dashboard_version", "delete_annotation", "unknown_tool"}
	var step []StreamEvent
	for _, name := range names {
		step = append(step, StreamEvent{Type: EventToolUse, ToolCall: &ToolCall{ID: name, Name: name, Input: `{}`}})
	}
	step = append(step, StreamEvent{Type: EventStop, StopReason: "tool_calls"})
	provider := &scriptedProvider{steps: [][]StreamEvent{step, {{Type: EventText, Delta: "The observed panel has an invalid measure; use count instead."}, {Type: EventStop, StopReason: "end_turn"}}}}
	tools := &fakeTools{execution: ToolExecution{Content: `{}`}}
	runtime := NewRuntime(provider, tools, NewStore(database.DB))
	run := func(id string, props any) string {
		t.Helper()
		raw, err := json.Marshal(agtypes.RunAgentInput{ThreadID: "explain", RunID: id, ForwardedProps: props, Messages: []agtypes.Message{{ID: id, Role: agtypes.RoleUser, Content: "Explain this error"}}})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/agent/runs", strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.Set("auth_user", &auth.User{ID: "owner", Role: "admin"})
		if err := runtime.Run(c); err != nil {
			t.Fatal(err)
		}
		return rec.Body.String()
	}
	stream := run("answer", map[string]any{"answer_only": true})
	if len(tools.calls) != 0 {
		t.Fatalf("answer-only executed writes: %+v", tools.calls)
	}
	for _, message := range provider.got[1] {
		if message.Role == RoleTool && (message.ToolResult == nil || !message.ToolResult.IsError) {
			t.Fatal("write refusal not delivered as tool error")
		}
	}
	if !strings.Contains(stream, "The observed panel has an invalid measure") {
		t.Fatal("no readable explanation", stream)
	}
	var versions int
	if err := database.DB.QueryRow(`SELECT count(*) FROM dashboard_versions`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 0 {
		t.Fatal("answer-only created a version")
	}
	provider.got = nil
	run("ordinary", nil)
	if len(tools.calls) != len(names) {
		t.Fatalf("ordinary turn retained answer-only mode: %+v", tools.calls)
	}
}

func TestAnswerOnlyMalformedForwardedPropsRejectedBeforeStartRun(t *testing.T) {
	for _, props := range []any{"true", []any{}, map[string]any{"answer_only": "true"}, map[string]any{"answer_only": 1}, map[string]any{"answer_only": nil}} {
		raw, err := json.Marshal(agtypes.RunAgentInput{ThreadID: "bad", RunID: "bad", ForwardedProps: props})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/agent/runs", strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		c := echo.New().NewContext(req, httptest.NewRecorder())
		c.Set("auth_user", &auth.User{ID: "owner", Role: "admin"})
		// A nil store proves malformed requests cannot reach StartRun.
		err = NewRuntime(textProvider{}, &fakeTools{}, nil).Run(c)
		httpErr, ok := err.(*echo.HTTPError)
		if !ok || httpErr.Code != http.StatusBadRequest {
			t.Fatalf("props=%+v err=%v", props, err)
		}
	}
}
