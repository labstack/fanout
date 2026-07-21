package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// lines joins SSE lines with \n.
func lines(ss ...string) string {
	return strings.Join(ss, "\n") + "\n"
}

// collect runs a parser over SSE input and buckets the emitted events.
func collect(t *testing.T, parse func(io.Reader, func(StreamEvent) error) error, input string) (texts []string, toolCalls []ToolCall, stopReasons []string, errs []string, parseErr error) {
	t.Helper()
	parseErr = parse(strings.NewReader(input), func(event StreamEvent) error {
		switch event.Type {
		case EventText:
			texts = append(texts, event.Delta)
		case EventToolUse:
			toolCalls = append(toolCalls, *event.ToolCall)
		case EventStop:
			stopReasons = append(stopReasons, event.StopReason)
		case EventError:
			errs = append(errs, event.Error)
		}
		return nil
	})
	return texts, toolCalls, stopReasons, errs, parseErr
}

func TestParseOpenAITextOnly(t *testing.T) {
	input := lines(
		`data: {"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"content":" world"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	)
	texts, toolCalls, stopReasons, _, err := collect(t, parseOpenAI, input)
	if err != nil {
		t.Fatalf("parseOpenAI: %v", err)
	}
	if got := strings.Join(texts, ""); got != "Hello world" {
		t.Errorf("text = %q, want %q", got, "Hello world")
	}
	if len(toolCalls) != 0 {
		t.Errorf("tool calls = %#v, want none", toolCalls)
	}
	if len(stopReasons) != 1 || stopReasons[0] != "stop" {
		t.Errorf("stopReasons = %v, want [stop]", stopReasons)
	}
}

func TestParseOpenAIToolCallFragmentAccumulation(t *testing.T) {
	input := lines(
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"status","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"win"}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"dow\":60}"}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)
	_, toolCalls, stopReasons, _, err := collect(t, parseOpenAI, input)
	if err != nil {
		t.Fatalf("parseOpenAI: %v", err)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(toolCalls))
	}
	if toolCalls[0].ID != "call_1" || toolCalls[0].Name != "status" {
		t.Errorf("tool call = %#v", toolCalls[0])
	}
	if toolCalls[0].Input != `{"window":60}` {
		t.Errorf("tool input = %q, want %q", toolCalls[0].Input, `{"window":60}`)
	}
	if len(stopReasons) != 1 || stopReasons[0] != "tool_calls" {
		t.Errorf("stopReasons = %v, want [tool_calls]", stopReasons)
	}
}

func TestParseOpenAIParallelToolCallsIndexOrder(t *testing.T) {
	input := lines(
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","function":{"name":"topology","arguments":"{}"}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"status","arguments":"{}"}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)
	_, toolCalls, _, _, err := collect(t, parseOpenAI, input)
	if err != nil {
		t.Fatalf("parseOpenAI: %v", err)
	}
	if len(toolCalls) != 2 {
		t.Fatalf("got %d tool calls, want 2", len(toolCalls))
	}
	if toolCalls[0].Name != "status" || toolCalls[1].Name != "topology" {
		t.Errorf("tool calls not in index order: %#v", toolCalls)
	}
}

func TestParseOpenAIEmptyToolArgsDefault(t *testing.T) {
	input := lines(
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","function":{"name":"status","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)
	_, toolCalls, _, _, err := collect(t, parseOpenAI, input)
	if err != nil {
		t.Fatalf("parseOpenAI: %v", err)
	}
	if len(toolCalls) != 1 || toolCalls[0].Input != "{}" {
		t.Errorf("empty tool input = %#v, want {}", toolCalls)
	}
}

func TestParseOpenAIStreamEndsWithoutFinishReason(t *testing.T) {
	input := lines(
		`data: {"choices":[{"delta":{"content":"partial"},"finish_reason":null}]}`,
	)
	_, _, _, _, err := collect(t, parseOpenAI, input)
	if err == nil || !strings.Contains(err.Error(), "finish reason") {
		t.Fatalf("err = %v, want finish-reason error", err)
	}
}

func TestParseOpenAIErrorEvent(t *testing.T) {
	input := lines(
		`data: {"error":{"message":"rate limit exceeded","type":"rate_limit_error"}}`,
	)
	_, _, _, errs, err := collect(t, parseOpenAI, input)
	if err != nil {
		t.Fatalf("parseOpenAI: %v", err)
	}
	if len(errs) != 1 || errs[0] != "rate limit exceeded" {
		t.Errorf("errs = %v, want [rate limit exceeded]", errs)
	}
}

func TestParseAnthropicTextOnly(t *testing.T) {
	input := lines(
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		`data: {"type":"message_stop"}`,
	)
	texts, toolCalls, stopReasons, _, err := collect(t, parseAnthropic, input)
	if err != nil {
		t.Fatalf("parseAnthropic: %v", err)
	}
	if got := strings.Join(texts, ""); got != "Hello world" {
		t.Errorf("text = %q, want %q", got, "Hello world")
	}
	if len(toolCalls) != 0 {
		t.Errorf("tool calls = %#v, want none", toolCalls)
	}
	if len(stopReasons) != 1 || stopReasons[0] != "end_turn" {
		t.Errorf("stopReasons = %v, want [end_turn]", stopReasons)
	}
}

func TestParseAnthropicInputJSONDeltaAccumulation(t *testing.T) {
	input := lines(
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_01","name":"status"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"win"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"dow\":60}"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		`data: {"type":"message_stop"}`,
	)
	_, toolCalls, stopReasons, _, err := collect(t, parseAnthropic, input)
	if err != nil {
		t.Fatalf("parseAnthropic: %v", err)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(toolCalls))
	}
	if toolCalls[0].ID != "toolu_01" || toolCalls[0].Name != "status" || toolCalls[0].Input != `{"window":60}` {
		t.Errorf("tool call = %#v", toolCalls[0])
	}
	if len(stopReasons) != 1 || stopReasons[0] != "tool_use" {
		t.Errorf("stopReasons = %v, want [tool_use]", stopReasons)
	}
}

func TestParseAnthropicEmptyToolInputDefault(t *testing.T) {
	input := lines(
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_02","name":"status"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		`data: {"type":"message_stop"}`,
	)
	_, toolCalls, _, _, err := collect(t, parseAnthropic, input)
	if err != nil {
		t.Fatalf("parseAnthropic: %v", err)
	}
	if len(toolCalls) != 1 || toolCalls[0].Input != "{}" {
		t.Errorf("empty tool input = %#v, want {}", toolCalls)
	}
}

func TestParseAnthropicErrorEvent(t *testing.T) {
	input := lines(
		`data: {"type":"error","error":{"message":"overloaded"}}`,
	)
	_, _, _, errs, err := collect(t, parseAnthropic, input)
	if err != nil {
		t.Fatalf("parseAnthropic: %v", err)
	}
	if len(errs) != 1 || errs[0] != "overloaded" {
		t.Errorf("errs = %v, want [overloaded]", errs)
	}
}

func TestParseAnthropicStreamEndsWithoutStopReason(t *testing.T) {
	input := lines(
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
	)
	_, _, _, _, err := collect(t, parseAnthropic, input)
	if err == nil || !strings.Contains(err.Error(), "stop reason") {
		t.Fatalf("err = %v, want stop-reason error", err)
	}
}

func TestParseAnthropicSkipsNonDataLines(t *testing.T) {
	input := lines(
		`event: message_start`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
	)
	texts, _, _, _, err := collect(t, parseAnthropic, input)
	if err != nil {
		t.Fatalf("parseAnthropic: %v", err)
	}
	if len(texts) != 1 || texts[0] != "ok" {
		t.Errorf("texts = %v, want [ok]", texts)
	}
}

// captureRequestBody serves one canned SSE response and returns the decoded
// request body the provider sent.
func captureRequestBody(t *testing.T, sseBody string, run func(baseURL string) error) map[string]any {
	t.Helper()
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseBody)
	}))
	defer server.Close()
	if err := run(server.URL); err != nil {
		t.Fatalf("stream: %v", err)
	}
	return body
}

// Pins the gpt-5.x request shape: max_completion_tokens (max_tokens is
// rejected by gpt-5.x models) and function-wrapped tools.
func TestOpenAIRequestBodyShape(t *testing.T) {
	sseBody := lines(
		`data: {"choices":[{"delta":{"content":"ok"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	)
	body := captureRequestBody(t, sseBody, func(baseURL string) error {
		provider := &openAIProvider{apiKey: "sk-test", model: "gpt-5.4", baseURL: baseURL, client: http.DefaultClient}
		params := StreamParams{
			System:    "be helpful",
			Messages:  []ProviderMessage{{Role: RoleUser, Content: "hi"}},
			Tools:     []ToolDef{{Name: "status", Description: "check", InputSchema: map[string]any{"type": "object"}}},
			MaxTokens: 4096,
		}
		return provider.Stream(context.Background(), params, func(StreamEvent) error { return nil })
	})
	if got, ok := body["max_completion_tokens"].(float64); !ok || got != 4096 {
		t.Errorf("max_completion_tokens = %v, want 4096", body["max_completion_tokens"])
	}
	if _, ok := body["max_tokens"]; ok {
		t.Error("request body must not contain max_tokens (rejected by gpt-5.x)")
	}
	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
	messages := body["messages"].([]any)
	if len(messages) != 2 || messages[0].(map[string]any)["role"] != "system" {
		t.Errorf("messages = %#v, want system message first", messages)
	}
	tools := body["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["function"].(map[string]any)["name"] != "status" {
		t.Errorf("tool = %#v, want function-wrapped status tool", tool)
	}
}

func TestAnthropicRequestBodyShape(t *testing.T) {
	sseBody := lines(
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
	)
	body := captureRequestBody(t, sseBody, func(baseURL string) error {
		provider := &anthropicProvider{apiKey: "sk-test", model: "claude-sonnet-4-6", baseURL: baseURL, client: http.DefaultClient}
		params := StreamParams{
			System: "be helpful",
			Messages: []ProviderMessage{
				{Role: RoleUser, Content: "check"},
				{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "tc1", Name: "status", Input: `{"window":60}`}}},
				{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: "tc1", Content: `{"ok":true}`}},
			},
			MaxTokens: 4096,
		}
		return provider.Stream(context.Background(), params, func(StreamEvent) error { return nil })
	})
	if got, ok := body["max_tokens"].(float64); !ok || got != 4096 {
		t.Errorf("max_tokens = %v, want 4096", body["max_tokens"])
	}
	if body["system"] != "be helpful" {
		t.Errorf("system = %v, want top-level system prompt", body["system"])
	}
	messages := body["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
	assistant := messages[1].(map[string]any)
	content := assistant["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["type"] != "tool_use" {
		t.Errorf("assistant content = %#v, want tool_use block", content)
	}
	toolResult := messages[2].(map[string]any)
	if toolResult["role"] != "user" {
		t.Errorf("tool result role = %v, want user (Anthropic format)", toolResult["role"])
	}
}
