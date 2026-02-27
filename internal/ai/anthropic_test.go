package ai

import (
	"strings"
	"testing"
)

func TestAnthropicProvider_Defaults(t *testing.T) {
	p := NewAnthropicProvider("sk-test", "", "")
	if p.model != "claude-haiku-4-5" {
		t.Errorf("model = %q, want default", p.model)
	}
	if p.baseURL != "https://api.anthropic.com" {
		t.Errorf("baseURL = %q, want default", p.baseURL)
	}
}

func TestAnthropicProvider_CustomModel(t *testing.T) {
	p := NewAnthropicProvider("sk-test", "claude-opus-4-6", "https://custom.api.com/")
	if p.model != "claude-opus-4-6" {
		t.Errorf("model = %q, want claude-opus-4-6", p.model)
	}
	if p.baseURL != "https://custom.api.com" {
		t.Errorf("baseURL = %q, want trailing slash stripped", p.baseURL)
	}
}

func TestAnthropicParseSSE_TextOnly(t *testing.T) {
	sse := lines(
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		`data: {"type":"message_stop"}`,
	)

	var texts []string
	var stopReason string
	p := &AnthropicProvider{}
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

func TestAnthropicParseSSE_ToolUse(t *testing.T) {
	sse := lines(
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_01","name":"status"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"win"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"dow\":60}"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		`data: {"type":"message_stop"}`,
	)

	var toolCalls []ToolCall
	var stopReason string
	p := &AnthropicProvider{}
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
	if toolCalls[0].ID != "toolu_01" {
		t.Errorf("tool ID = %q, want %q", toolCalls[0].ID, "toolu_01")
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

func TestAnthropicParseSSE_EmptyToolInput(t *testing.T) {
	sse := lines(
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_02","name":"status"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		`data: {"type":"message_stop"}`,
	)

	var toolCalls []ToolCall
	p := &AnthropicProvider{}
	err := p.parseSSE(strings.NewReader(sse), func(e StreamEvent) error {
		if e.Type == EventToolUse {
			toolCalls = append(toolCalls, *e.ToolCall)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(toolCalls))
	}
	if toolCalls[0].Input != "{}" {
		t.Errorf("empty tool input = %q, want %q", toolCalls[0].Input, "{}")
	}
}

func TestAnthropicParseSSE_Error(t *testing.T) {
	sse := lines(
		`data: {"type":"error","error":{"message":"overloaded"}}`,
	)

	var errMsg string
	p := &AnthropicProvider{}
	p.parseSSE(strings.NewReader(sse), func(e StreamEvent) error {
		if e.Type == EventError {
			errMsg = e.Error
		}
		return nil
	})
	if errMsg != "overloaded" {
		t.Errorf("error = %q, want %q", errMsg, "overloaded")
	}
}

func TestAnthropicParseSSE_IncompleteStream(t *testing.T) {
	// Stream ends without message_delta
	sse := lines(
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
	)

	var gotError bool
	p := &AnthropicProvider{}
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

func TestAnthropicParseSSE_DoneMarker(t *testing.T) {
	// [DONE] marker should stop parsing without error about incomplete stream
	sse := lines(
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		`data: [DONE]`,
	)

	var gotStop bool
	p := &AnthropicProvider{}
	err := p.parseSSE(strings.NewReader(sse), func(e StreamEvent) error {
		if e.Type == EventStop {
			gotStop = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	if !gotStop {
		t.Error("expected EventStop")
	}
}

func TestAnthropicParseSSE_SkipsNonDataLines(t *testing.T) {
	sse := lines(
		`event: message_start`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
	)

	var texts []string
	p := &AnthropicProvider{}
	p.parseSSE(strings.NewReader(sse), func(e StreamEvent) error {
		if e.Type == EventText {
			texts = append(texts, e.Delta)
		}
		return nil
	})
	if len(texts) != 1 || texts[0] != "ok" {
		t.Errorf("texts = %v, want [ok]", texts)
	}
}

func TestAnthropicBuildRequest_SystemBlocks(t *testing.T) {
	p := NewAnthropicProvider("sk-test", "claude-sonnet-4-6", "")
	body := p.buildRequest(StreamParams{
		SystemBlocks: []SystemBlock{
			{Text: "You are helpful", CacheControl: "ephemeral"},
			{Text: "Context: now"},
		},
		Messages:  []Message{UserMessage("hi")},
		MaxTokens: 1024,
	})

	system, ok := body["system"].([]map[string]any)
	if !ok {
		t.Fatalf("system blocks not a []map[string]any: %T", body["system"])
	}
	if len(system) != 2 {
		t.Fatalf("system blocks = %d, want 2", len(system))
	}
	if system[0]["cache_control"] == nil {
		t.Error("first block should have cache_control")
	}
	if system[1]["cache_control"] != nil {
		t.Error("second block should not have cache_control")
	}
}

func TestAnthropicBuildRequest_ToolCalls(t *testing.T) {
	p := NewAnthropicProvider("sk-test", "", "")
	body := p.buildRequest(StreamParams{
		Messages: []Message{
			UserMessage("check health"),
			AssistantMessage("", []ToolCall{{ID: "tc1", Name: "status", Input: `{"window":60}`}}),
			ToolMessage("tc1", `{"healthy":true}`, false),
		},
		MaxTokens: 1024,
	})

	msgs := body["messages"].([]map[string]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3", len(msgs))
	}

	// Assistant message with tool use
	assistant := msgs[1]
	content, ok := assistant["content"].([]map[string]any)
	if !ok {
		t.Fatalf("assistant content not []map: %T", assistant["content"])
	}
	if len(content) != 1 || content[0]["type"] != "tool_use" {
		t.Errorf("assistant content = %+v, want tool_use block", content)
	}

	// Tool result (sent as user role in Anthropic format)
	tool := msgs[2]
	if tool["role"] != "user" {
		t.Errorf("tool result role = %q, want %q (Anthropic format)", tool["role"], "user")
	}
}

func TestAnthropicBuildRequest_ToolsCacheControl(t *testing.T) {
	p := NewAnthropicProvider("sk-test", "", "")
	body := p.buildRequest(StreamParams{
		Messages:  []Message{UserMessage("hi")},
		Tools:     []ToolDef{{Name: "a"}, {Name: "b"}},
		MaxTokens: 1024,
	})

	tools := body["tools"].([]map[string]any)
	if len(tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(tools))
	}
	// Only last tool should have cache_control
	if tools[0]["cache_control"] != nil {
		t.Error("first tool should not have cache_control")
	}
	if tools[1]["cache_control"] == nil {
		t.Error("last tool should have cache_control for prompt caching")
	}
}

func TestAnthropicParseSSE_ParseErrorTolerance(t *testing.T) {
	// 4 bad lines (tolerated), then a good one, then stop
	sse := lines(
		`data: {bad json 1`,
		`data: {bad json 2`,
		`data: {bad json 3`,
		`data: {bad json 4`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
	)

	var texts []string
	p := &AnthropicProvider{}
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

func TestAnthropicParseSSE_TooManyParseErrors(t *testing.T) {
	sse := lines(
		`data: {bad 1`,
		`data: {bad 2`,
		`data: {bad 3`,
		`data: {bad 4`,
		`data: {bad 5`,
	)

	p := &AnthropicProvider{}
	err := p.parseSSE(strings.NewReader(sse), func(e StreamEvent) error { return nil })
	if err == nil {
		t.Fatal("expected error after 5 consecutive parse failures")
	}
	if !strings.Contains(err.Error(), "5 consecutive failures") {
		t.Errorf("error = %q, want mention of 5 consecutive failures", err.Error())
	}
}

func TestAnthropicBuildRequest_CorruptToolInput(t *testing.T) {
	p := NewAnthropicProvider("sk-test", "", "")
	body := p.buildRequest(StreamParams{
		Messages: []Message{
			AssistantMessage("", []ToolCall{{ID: "tc1", Name: "test", Input: "not json"}}),
		},
		MaxTokens: 1024,
	})

	msgs := body["messages"].([]map[string]any)
	content := msgs[0]["content"].([]map[string]any)
	// Corrupt input should produce a text block (not tool_use) explaining the skip
	if len(content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(content))
	}
	if content[0]["type"] != "text" {
		t.Errorf("corrupt input block type = %q, want %q", content[0]["type"], "text")
	}
	text, _ := content[0]["text"].(string)
	if !strings.Contains(text, "skipped") {
		t.Errorf("corrupt input text = %q, want mention of 'skipped'", text)
	}
}

func TestAnthropicBuildRequest_SimpleSystem(t *testing.T) {
	p := NewAnthropicProvider("sk-test", "", "")
	body := p.buildRequest(StreamParams{
		System:    "Be helpful",
		Messages:  []Message{UserMessage("hi")},
		MaxTokens: 512,
	})

	if body["system"] != "Be helpful" {
		t.Errorf("system = %v, want string", body["system"])
	}
}

// lines joins SSE lines with \n
func lines(ss ...string) string {
	return strings.Join(ss, "\n") + "\n"
}
