package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/config"
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
