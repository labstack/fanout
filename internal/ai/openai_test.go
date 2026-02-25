package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenAIProvider_Defaults(t *testing.T) {
	p := NewOpenAIProvider("sk-test", "", "")
	if p.model != "gpt-4o" {
		t.Errorf("model = %q, want default gpt-4o", p.model)
	}
	if p.baseURL != "https://api.openai.com" {
		t.Errorf("baseURL = %q, want default", p.baseURL)
	}
}

func TestOpenAIProvider_CustomModel(t *testing.T) {
	p := NewOpenAIProvider("sk-test", "gpt-4o-mini", "https://custom.openai.com/")
	if p.model != "gpt-4o-mini" {
		t.Errorf("model = %q, want gpt-4o-mini", p.model)
	}
	if p.baseURL != "https://custom.openai.com" {
		t.Errorf("baseURL = %q, want trailing slash stripped", p.baseURL)
	}
}

func TestOpenAIParseSSE_TextOnly(t *testing.T) {
	sse := lines(
		`data: {"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"content":" world"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	)

	var texts []string
	var stopReason string
	p := &OpenAIProvider{}
	err := p.parseSSE(strings.NewReader(sse), func(e StreamEvent) error {
		switch e.Type {
		case EventText:
			texts = append(texts, e.Delta)
		case EventStop:
			stopReason = e.StopReason
		}
		return nil
	})
	if err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	if got := strings.Join(texts, ""); got != "Hello world" {
		t.Errorf("text = %q, want %q", got, "Hello world")
	}
	if stopReason != "end_turn" {
		t.Errorf("stopReason = %q, want %q", stopReason, "end_turn")
	}
}

func TestOpenAIParseSSE_ToolCalls(t *testing.T) {
	sse := lines(
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"status","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"win"}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"dow\":60}"}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)

	var toolCalls []ToolCall
	var stopReason string
	p := &OpenAIProvider{}
	err := p.parseSSE(strings.NewReader(sse), func(e StreamEvent) error {
		switch e.Type {
		case EventToolUse:
			toolCalls = append(toolCalls, *e.ToolCall)
		case EventStop:
			stopReason = e.StopReason
		}
		return nil
	})
	if err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(toolCalls))
	}
	if toolCalls[0].ID != "call_1" {
		t.Errorf("tool ID = %q, want %q", toolCalls[0].ID, "call_1")
	}
	if toolCalls[0].Name != "status" {
		t.Errorf("tool name = %q, want %q", toolCalls[0].Name, "status")
	}
	if toolCalls[0].Input != `{"window":60}` {
		t.Errorf("tool input = %q, want %q", toolCalls[0].Input, `{"window":60}`)
	}
	if stopReason != "tool_use" {
		t.Errorf("stopReason = %q, want %q", stopReason, "tool_use")
	}
}

func TestOpenAIParseSSE_ParallelToolCalls(t *testing.T) {
	sse := lines(
		// Two tool calls streamed in parallel via different indices
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"status","arguments":"{}"}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","function":{"name":"topology","arguments":"{}"}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)

	var toolCalls []ToolCall
	p := &OpenAIProvider{}
	err := p.parseSSE(strings.NewReader(sse), func(e StreamEvent) error {
		if e.Type == EventToolUse {
			toolCalls = append(toolCalls, *e.ToolCall)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	if len(toolCalls) != 2 {
		t.Fatalf("got %d tool calls, want 2", len(toolCalls))
	}
	// Should be emitted in index order
	if toolCalls[0].Name != "status" {
		t.Errorf("first tool = %q, want status", toolCalls[0].Name)
	}
	if toolCalls[1].Name != "topology" {
		t.Errorf("second tool = %q, want topology", toolCalls[1].Name)
	}
}

func TestOpenAIParseSSE_ContentFilter(t *testing.T) {
	sse := lines(
		`data: {"choices":[{"delta":{},"finish_reason":"content_filter"}]}`,
		`data: [DONE]`,
	)

	var errMsg string
	p := &OpenAIProvider{}
	p.parseSSE(strings.NewReader(sse), func(e StreamEvent) error {
		if e.Type == EventError {
			errMsg = e.Error
		}
		return nil
	})
	if !strings.Contains(errMsg, "content filter") {
		t.Errorf("error = %q, want mention of content filter", errMsg)
	}
}

func TestOpenAIParseSSE_LengthFinish(t *testing.T) {
	sse := lines(
		`data: {"choices":[{"delta":{"content":"truncated"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"length"}]}`,
		`data: [DONE]`,
	)

	var stopReason string
	p := &OpenAIProvider{}
	err := p.parseSSE(strings.NewReader(sse), func(e StreamEvent) error {
		if e.Type == EventStop {
			stopReason = e.StopReason
		}
		return nil
	})
	if err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	if stopReason != "end_turn" {
		t.Errorf("stopReason = %q, want %q", stopReason, "end_turn")
	}
}

func TestOpenAIParseSSE_ErrorEvent(t *testing.T) {
	sse := lines(
		`data: {"error":{"message":"rate limit exceeded","type":"rate_limit_error"}}`,
	)

	var errMsg string
	p := &OpenAIProvider{}
	p.parseSSE(strings.NewReader(sse), func(e StreamEvent) error {
		if e.Type == EventError {
			errMsg = e.Error
		}
		return nil
	})
	if errMsg != "rate limit exceeded" {
		t.Errorf("error = %q, want %q", errMsg, "rate limit exceeded")
	}
}

func TestOpenAIParseSSE_IncompleteStream(t *testing.T) {
	sse := lines(
		`data: {"choices":[{"delta":{"content":"partial"},"finish_reason":null}]}`,
	)

	var gotError bool
	p := &OpenAIProvider{}
	err := p.parseSSE(strings.NewReader(sse), func(e StreamEvent) error {
		if e.Type == EventError {
			gotError = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("parseSSE should not return error: %v", err)
	}
	if !gotError {
		t.Error("expected EventError for incomplete stream")
	}
}

func TestOpenAIParseSSE_EmptyChoices(t *testing.T) {
	sse := lines(
		`data: {"choices":[]}`,
		`data: {"choices":[{"delta":{"content":"ok"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	)

	var texts []string
	p := &OpenAIProvider{}
	err := p.parseSSE(strings.NewReader(sse), func(e StreamEvent) error {
		if e.Type == EventText {
			texts = append(texts, e.Delta)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	if len(texts) != 1 || texts[0] != "ok" {
		t.Errorf("texts = %v, want [ok]", texts)
	}
}

func TestOpenAIParseSSE_TooManyParseErrors(t *testing.T) {
	sse := lines(
		`data: {bad 1`,
		`data: {bad 2`,
		`data: {bad 3`,
		`data: {bad 4`,
		`data: {bad 5`,
	)

	p := &OpenAIProvider{}
	err := p.parseSSE(strings.NewReader(sse), func(e StreamEvent) error { return nil })
	if err == nil {
		t.Fatal("expected error after 5 consecutive parse failures")
	}
}

func TestOpenAIBuildRequest_SystemBlocks(t *testing.T) {
	p := NewOpenAIProvider("sk-test", "", "")
	body := p.buildRequest(StreamParams{
		SystemBlocks: []SystemBlock{
			{Text: "Part 1"},
			{Text: "Part 2"},
		},
		Messages:  []Message{UserMessage("hi")},
		MaxTokens: 1024,
	})

	msgs := body["messages"].([]map[string]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (system + user)", len(msgs))
	}
	if msgs[0]["role"] != "system" {
		t.Errorf("first msg role = %q, want system", msgs[0]["role"])
	}
	content := msgs[0]["content"].(string)
	if !strings.Contains(content, "Part 1") || !strings.Contains(content, "Part 2") {
		t.Errorf("system content = %q, want both parts joined", content)
	}
}

func TestOpenAIBuildRequest_Tools(t *testing.T) {
	p := NewOpenAIProvider("sk-test", "", "")
	body := p.buildRequest(StreamParams{
		Messages: []Message{UserMessage("hi")},
		Tools: []ToolDef{
			{Name: "test", Description: "A test tool", InputSchema: map[string]any{"type": "object"}},
		},
		MaxTokens: 1024,
	})

	tools := body["tools"].([]map[string]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(tools))
	}
	if tools[0]["type"] != "function" {
		t.Errorf("tool type = %q, want function", tools[0]["type"])
	}
	fn := tools[0]["function"].(map[string]any)
	if fn["name"] != "test" {
		t.Errorf("tool name = %q, want test", fn["name"])
	}
}

func TestOpenAIBuildRequest_ToolCallMessages(t *testing.T) {
	p := NewOpenAIProvider("sk-test", "", "")
	body := p.buildRequest(StreamParams{
		Messages: []Message{
			UserMessage("check"),
			AssistantMessage("", []ToolCall{{ID: "tc1", Name: "status", Input: "{}"}}),
			ToolMessage("tc1", `{"ok":true}`, false),
		},
		MaxTokens: 1024,
	})

	msgs := body["messages"].([]map[string]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3", len(msgs))
	}

	// Tool result uses "tool" role in OpenAI format
	if msgs[2]["role"] != "tool" {
		t.Errorf("tool result role = %q, want %q (OpenAI format)", msgs[2]["role"], "tool")
	}
	if msgs[2]["tool_call_id"] != "tc1" {
		t.Errorf("tool_call_id = %q, want %q", msgs[2]["tool_call_id"], "tc1")
	}
}

func TestOpenAIBuildRequest_RespondToolStrict(t *testing.T) {
	p := NewOpenAIProvider("sk-test", "", "")
	body := p.buildRequest(StreamParams{
		Messages: []Message{UserMessage("hi")},
		Tools: []ToolDef{
			{Name: "status", Description: "Check status", InputSchema: map[string]any{"type": "object"}},
			respondToolDef(),
		},
		MaxTokens: 1024,
	})

	tools := body["tools"].([]map[string]any)
	if len(tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(tools))
	}

	// status tool should NOT have strict
	statusFn := tools[0]["function"].(map[string]any)
	if statusFn["name"] != "status" {
		t.Fatalf("first tool = %q, want status", statusFn["name"])
	}
	if _, ok := statusFn["strict"]; ok {
		t.Error("non-respond tool should not have strict field")
	}

	// respond tool should have strict: true
	respondFn := tools[1]["function"].(map[string]any)
	if respondFn["name"] != respondToolName {
		t.Fatalf("second tool = %q, want %s", respondFn["name"], respondToolName)
	}
	if respondFn["strict"] != true {
		t.Error("respond tool should have strict: true")
	}

	// respond tool parameters should have additionalProperties: false (strictified)
	params := respondFn["parameters"]
	paramsBytes, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	var schema map[string]any
	json.Unmarshal(paramsBytes, &schema)
	if schema["additionalProperties"] != false {
		t.Error("respond tool schema should have additionalProperties: false after strictify")
	}
}

func TestOpenAIParseSSE_EmptyToolArgs(t *testing.T) {
	sse := lines(
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","function":{"name":"status","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)

	var toolCalls []ToolCall
	p := &OpenAIProvider{}
	p.parseSSE(strings.NewReader(sse), func(e StreamEvent) error {
		if e.Type == EventToolUse {
			toolCalls = append(toolCalls, *e.ToolCall)
		}
		return nil
	})
	if len(toolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(toolCalls))
	}
	// Empty args should default to "{}"
	if toolCalls[0].Input != "{}" {
		t.Errorf("empty tool input = %q, want %q", toolCalls[0].Input, "{}")
	}
}
