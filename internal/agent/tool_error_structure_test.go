package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	fanoutmcp "github.com/labstack/fanout/internal/mcp"
	controlstore "github.com/labstack/fanout/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type errorPathTools struct {
	execute func(context.Context, ToolCall) (ToolExecution, error)
}

func (errorPathTools) ReadOnly(name string) bool { return reviewedReadOnly(name, nil) }
func (errorPathTools) Definitions() []ToolDef    { return nil }
func (f errorPathTools) Execute(ctx context.Context, call ToolCall) (ToolExecution, error) {
	return f.execute(ctx, call)
}

func TestRuntimeToolErrorsAreStructuredLiveAndPersisted(t *testing.T) {
	for _, path := range []string{"tool_error", "transport_error", "panic", "timeout", "refused_app_only", "interrupted", "run_timeout", "mcp_panic"} {
		t.Run(path, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			name := "restore_dashboard_version"
			var tools toolExecutor = errorPathTools{execute: func(ctx context.Context, _ ToolCall) (ToolExecution, error) {
				switch path {
				case "tool_error":
					return ToolExecution{Content: "Dashboard unavailable", IsError: true}, nil
				case "transport_error":
					return ToolExecution{}, errors.New("MCP session closed")
				case "panic":
					panic("private panic body")
				case "timeout":
					return ToolExecution{}, context.DeadlineExceeded
				case "interrupted":
					cancel()
					return ToolExecution{}, ctx.Err()
				case "run_timeout":
					<-ctx.Done()
					return ToolExecution{}, ctx.Err()
				default:
					t.Fatal("unexpected tool path")
					return ToolExecution{}, nil
				}
			}}
			if path == "refused_app_only" || path == "mcp_panic" {
				server := fanoutmcp.NewWithIntelligence(registryQueries{}, nil, nil, nil, "test")
				name = "query_panel_fragment"
				if path == "mcp_panic" {
					name = "panic_tool"
					mcp.AddTool(server.MCP(), &mcp.Tool{Name: name}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
						panic("private panic body")
					})
				}
				registry, err := NewToolRegistry(ctx, server.MCP())
				if err != nil {
					t.Fatal(err)
				}
				defer registry.Close()
				tools = registry
			}
			call := ToolCall{ID: "save", Name: name, Input: `{}`}
			provider := &scriptedProvider{steps: [][]StreamEvent{{{Type: EventToolUse, ToolCall: &call}, {Type: EventStop, StopReason: "tool_calls"}}, {{Type: EventStop, StopReason: "end_turn"}}}}
			runtime := NewRuntime(provider, tools, nil)
			if path == "run_timeout" {
				runtime.runTimeout = 10 * time.Millisecond
			}
			db, err := controlstore.NewSQLite(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store := NewStore(db.DB)
			messages, err := store.StartRun(t.Context(), "owner", agtypes.RunAgentInput{ThreadID: "thread", RunID: "run", Messages: []agtypes.Message{{ID: "user", Role: agtypes.RoleUser, Content: "Restore"}}})
			if err != nil {
				t.Fatal(err)
			}
			emitter, output := newTestEmitter()
			_, runErr := runtime.execute(ctx, "thread", "run", &messages, emitter)
			interrupted := path == "interrupted" || path == "run_timeout"
			if (runErr != nil) != interrupted {
				t.Fatalf("run error=%v", runErr)
			}
			if err := store.FinishRun(t.Context(), "owner", "thread", "run", messages, emitter.events, false, runErr); err != nil {
				t.Fatal(err)
			}
			reloaded, err := store.Thread(t.Context(), "owner", "thread")
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, message := range reloaded.Messages {
				if message.Role != agtypes.RoleTool {
					continue
				}
				found = true
				var payload map[string]any
				if json.Unmarshal([]byte(messageText(message.Content)), &payload) != nil || payload["error"] == nil && payload["isError"] != true || message.Error == "" {
					t.Fatalf("unstructured error: %+v", message)
				}
				if interrupted && (message.Error != "interrupted" || payload["error"] != "interrupted") {
					t.Fatalf("unknown outcome lost: %+v", message)
				}
			}
			if !found || strings.Contains(output.String(), "private panic body") {
				t.Fatal("missing structured error or leaked panic")
			}
			if !interrupted {
				found = false
				for _, line := range strings.Split(output.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var event struct {
						Type    string `json:"type"`
						Content string `json:"content"`
					}
					if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil || event.Type != "TOOL_CALL_RESULT" {
						continue
					}
					var payload map[string]any
					if json.Unmarshal([]byte(event.Content), &payload) != nil || payload["error"] == nil && payload["isError"] != true {
						t.Fatalf("unstructured live error: %s", event.Content)
					}
					found = true
				}
				if !found {
					t.Fatal("missing live tool result")
				}
				last := provider.got[1][len(provider.got[1])-1]
				if last.ToolResult == nil || !last.ToolResult.IsError {
					t.Fatal("model lost error flag")
				}
			}
		})
	}
}
