package ai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTruncateJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		max   int
		want  string
	}{
		{"short", `{"a":1}`, 100, `{"a":1}`},
		{"exact", "abcde", 5, "abcde"},
		{"long", "abcdefghij", 5, "abcde...(truncated)"},
		{"empty", "", 10, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateJSON(tt.input, tt.max)
			if got != tt.want {
				t.Errorf("truncateJSON(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
			}
		})
	}
}

func TestTruncateResult(t *testing.T) {
	short := "short"
	if got := truncateResult(short, 100); got != short {
		t.Errorf("short: got %q, want %q", got, short)
	}

	long := strings.Repeat("x", 100)
	got := truncateResult(long, 50)
	if !strings.HasPrefix(got, strings.Repeat("x", 50)) {
		t.Error("truncated result should start with first 50 chars")
	}
	if !strings.Contains(got, "[Result truncated") {
		t.Error("truncated result should contain truncation message")
	}
}

func TestSummarizeToolResult_Object(t *testing.T) {
	input := `{"services":[{"name":"a"},{"name":"b"}],"healthy":true,"count":5}`
	got := summarizeToolResult(input)
	if !strings.Contains(got, "[compacted]") {
		t.Errorf("result = %q, want [compacted] suffix", got)
	}
	if !strings.Contains(got, `"services": [2 items]`) {
		t.Errorf("result = %q, want array length summary", got)
	}
	if !strings.Contains(got, `"count": ...`) {
		t.Errorf("result = %q, want scalar summary", got)
	}
}

func TestSummarizeToolResult_Array(t *testing.T) {
	input := `[1,2,3]`
	got := summarizeToolResult(input)
	if !strings.Contains(got, "[3 items]") {
		t.Errorf("result = %q, want [3 items]", got)
	}
}

func TestSummarizeToolResult_InvalidJSON(t *testing.T) {
	input := strings.Repeat("x", 200)
	got := summarizeToolResult(input)
	if len(got) > 160 {
		t.Errorf("non-JSON result too long: %d chars", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("non-JSON result should end with ...: %q", got)
	}
}

func TestSummarizeToolResult_ShortInvalidJSON(t *testing.T) {
	input := "not json"
	got := summarizeToolResult(input)
	if got != input {
		t.Errorf("short non-JSON: got %q, want %q", got, input)
	}
}

func TestCompactToolResults(t *testing.T) {
	longResult := `{"services":[` + strings.Repeat(`{"name":"svc","status":"ok"},`, 20) + `{"name":"last"}]}` // valid JSON > 200 chars

	msgs := []Message{
		UserMessage("first question"),
		AssistantMessage("", []ToolCall{{ID: "tc1", Name: "status", Input: "{}"}}),
		ToolMessage("tc1", longResult, false),
		UserMessage("second question"),
		AssistantMessage("", []ToolCall{{ID: "tc2", Name: "diagnose", Input: "{}"}}),
		ToolMessage("tc2", longResult, false),
	}

	compacted := compactToolResults(msgs)

	// First tool result (older) should be compacted
	if !strings.Contains(compacted[2].ToolResult.Content, "[compacted]") {
		t.Errorf("older tool result should be compacted: %q", compacted[2].ToolResult.Content)
	}

	// Second tool result (recent batch) should be preserved
	if compacted[5].ToolResult.Content != longResult {
		t.Error("recent tool result should be preserved intact")
	}

	// Original should not be mutated
	if msgs[2].ToolResult.Content != longResult {
		t.Error("original messages should not be mutated")
	}
}

func TestCompactToolResults_PreservesErrors(t *testing.T) {
	longError := strings.Repeat("error ", 100)

	msgs := []Message{
		UserMessage("q"),
		AssistantMessage("", []ToolCall{{ID: "tc1", Name: "status", Input: "{}"}}),
		ToolMessage("tc1", longError, true),
		AssistantMessage("", []ToolCall{{ID: "tc2", Name: "diagnose", Input: "{}"}}),
		ToolMessage("tc2", "ok", false),
	}

	compacted := compactToolResults(msgs)

	// Error results should never be compacted
	if compacted[2].ToolResult.Content != longError {
		t.Error("error tool results should not be compacted")
	}
}

func TestCompactToolResults_NoToolCalls(t *testing.T) {
	msgs := []Message{
		UserMessage("hello"),
		AssistantMessage("hi there", nil),
	}

	compacted := compactToolResults(msgs)
	if len(compacted) != 2 {
		t.Errorf("compacted = %d messages, want 2", len(compacted))
	}
}

func TestCompactToolResults_ShortResultsUnchanged(t *testing.T) {
	msgs := []Message{
		UserMessage("q"),
		AssistantMessage("", []ToolCall{{ID: "tc1", Name: "status", Input: "{}"}}),
		ToolMessage("tc1", `{"ok":true}`, false),
		AssistantMessage("", []ToolCall{{ID: "tc2", Name: "diagnose", Input: "{}"}}),
		ToolMessage("tc2", "ok", false),
	}

	compacted := compactToolResults(msgs)

	// Short result (< 200 chars) should not be compacted
	if compacted[2].ToolResult.Content != `{"ok":true}` {
		t.Errorf("short result was compacted: %q", compacted[2].ToolResult.Content)
	}
}

func TestNewOrchestrator_PanicsOnNilProvider(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil provider")
		}
	}()
	NewOrchestrator(nil, &ToolRegistry{}, nil, config.Config{})
}

func TestNewOrchestrator_PanicsOnNilTools(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil tools")
		}
	}()
	NewOrchestrator(&mockProvider{}, nil, nil, config.Config{})
}

type mockProvider struct{}

func (m *mockProvider) Stream(_ context.Context, _ StreamParams, _ StreamCallback) error {
	return nil
}

// --- MCP integration test ---

type greetInput struct {
	Name string `json:"name" jsonschema:"The name to greet"`
}
type greetOutput struct {
	Greeting string `json:"greeting"`
}

func TestToolRegistryMCPIntegration(t *testing.T) {
	ctx := context.Background()

	// 1. Create a standalone MCP server and register a test tool.
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "greet",
		Description: "Say hello",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in greetInput) (*mcp.CallToolResult, greetOutput, error) {
		return nil, greetOutput{Greeting: "hello " + in.Name}, nil
	})

	// 2. Create the AI tool registry connected to this test server.
	//    Pass nil for svc — AI-only tools won't be called in this test.
	registry, err := NewToolRegistry(ctx, mcpServer, nil, config.Config{})
	if err != nil {
		t.Fatalf("NewToolRegistry failed: %v", err)
	}
	defer registry.Close()

	defs := registry.Defs()

	// 3. Verify the "greet" MCP tool appears in Defs() with correct name and description.
	var greetDef *ToolDef
	for i := range defs {
		if defs[i].Name == "greet" {
			greetDef = &defs[i]
			break
		}
	}
	if greetDef == nil {
		t.Fatal("expected 'greet' tool in registry defs, not found")
	}
	if greetDef.Description != "Say hello" {
		t.Errorf("greet description = %q, want %q", greetDef.Description, "Say hello")
	}

	// 4. Verify InputSchema is non-nil (schema was generated from struct tags).
	if greetDef.InputSchema == nil {
		t.Error("greet InputSchema should not be nil")
	}

	// 5. Verify AI-only tools (metrics, tail) are present in Defs().
	//    They get registered even with nil svc (closures capture svc but don't call it during registration).
	aiTools := map[string]bool{"metrics": false, "tail": false}
	for _, d := range defs {
		if _, ok := aiTools[d.Name]; ok {
			aiTools[d.Name] = true
		}
	}
	for name, found := range aiTools {
		if !found {
			t.Errorf("expected AI-only tool %q in registry defs, not found", name)
		}
	}

	// 6. Execute the greet tool via the MCP session and verify the result.
	input, _ := json.Marshal(map[string]string{"name": "world"})
	result, err := registry.Execute(ctx, "greet", json.RawMessage(input))
	if err != nil {
		t.Fatalf("Execute('greet') failed: %v", err)
	}
	if !strings.Contains(result, "hello world") {
		t.Errorf("Execute('greet') = %q, want it to contain %q", result, "hello world")
	}

	// 7. Verify executing an unknown tool returns an error.
	_, err = registry.Execute(ctx, "nonexistent", json.RawMessage(`{}`))
	if err == nil {
		t.Error("Execute('nonexistent') should return an error")
	}

	// 8. Execute with empty input — MCP validates schema, returns error (not a panic).
	_, err = registry.Execute(ctx, "greet", json.RawMessage(`{}`))
	if err == nil {
		t.Error("Execute('greet') with empty input should fail schema validation")
	}

	// 9. Close the registry and verify subsequent Execute fails with clear message.
	if err := registry.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
	_, err = registry.Execute(ctx, "greet", json.RawMessage(input))
	if err == nil {
		t.Error("Execute after Close() should return an error")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("Execute after Close() error = %q, want it to mention 'closed'", err)
	}
}

func TestExtractMCPText(t *testing.T) {
	// Single text content
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "hello"},
		},
	}
	got := extractMCPText(result)
	if got != "hello" {
		t.Errorf("extractMCPText single = %q, want %q", got, "hello")
	}

	// Multiple text contents joined with newline
	result = &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "line1"},
			&mcp.TextContent{Text: "line2"},
		},
	}
	got = extractMCPText(result)
	if got != "line1\nline2" {
		t.Errorf("extractMCPText multi = %q, want %q", got, "line1\nline2")
	}

	// Empty content
	result = &mcp.CallToolResult{}
	got = extractMCPText(result)
	if got != "" {
		t.Errorf("extractMCPText empty = %q, want empty", got)
	}
}

func TestToolRegistryMCPIntegration_IsError(t *testing.T) {
	ctx := context.Background()

	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "fail",
		Description: "Always fails",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: "something broke"}},
		}, struct{}{}, nil
	})

	registry, err := NewToolRegistry(ctx, mcpServer, nil, config.Config{})
	if err != nil {
		t.Fatalf("NewToolRegistry failed: %v", err)
	}
	defer registry.Close()

	_, err = registry.Execute(ctx, "fail", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("Execute('fail') should return an error")
	}
	if !strings.Contains(err.Error(), "something broke") {
		t.Errorf("error = %q, want it to contain %q", err, "something broke")
	}
}
