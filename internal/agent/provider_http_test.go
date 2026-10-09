package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
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

// openAIStream exercises the real HTTP provider against a local Responses server.
func openAIStream(t *testing.T) func(io.Reader, func(StreamEvent) error) error {
	t.Helper()
	return func(reader io.Reader, cb func(StreamEvent) error) error {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
				t.Errorf("request = %s %s, want POST /v1/responses", r.Method, r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer sk-test" {
				t.Errorf("authorization = %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.Copy(w, reader)
		}))
		defer server.Close()
		provider := &openAIProvider{apiKey: "sk-test", model: "gpt-6.1-sol", baseURL: server.URL, client: server.Client()}
		return provider.Stream(context.Background(), StreamParams{}, cb)
	}
}

func TestOpenAIResponsesTextAndUsage(t *testing.T) {
	input := lines(
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"Hello"}`,
		``,
		`data: {"type":"response.output_text.delta","delta":" world"}`,
		`data: {"type":"response.completed","response":{"output":[],"usage":{"input_tokens":12,"output_tokens":25,"output_tokens_details":{"reasoning_tokens":20}}}}`,
		// Terminal events must end the stream even if extra events arrive.
		`data: {"type":"response.output_text.delta","delta":"ignored"}`,
	)
	var events []StreamEvent
	err := openAIStream(t)(strings.NewReader(input), func(event StreamEvent) error {
		if event.Type != EventUsage {
			events = append(events, event)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Type != EventText || events[0].Delta != "Hello" || events[1].Delta != " world" {
		t.Fatalf("events = %#v", events)
	}
	stop := events[2]
	if stop.Type != EventStop || stop.StopReason != "end_turn" || stop.Usage == nil || *stop.Usage != (TokenUsage{InputTokens: 12, OutputTokens: 25, ReasoningTokens: 20}) {
		t.Errorf("stop = %#v", stop)
	}
}

func TestOpenAIResponsesToolCallArgumentDeltas(t *testing.T) {
	for _, tc := range []struct {
		name  string
		final []string
	}{
		{name: "deltas"},
		{name: "arguments_done", final: []string{
			`data: {"type":"response.function_call_arguments.done","output_index":1,"arguments":"{\"window\":60}"}`,
		}},
		{name: "item_done", final: []string{
			`data: {"type":"response.output_item.done","output_index":1,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"status","arguments":"{\"window\":60}"}}`,
		}},
		{name: "all_done", final: []string{
			`data: {"type":"response.function_call_arguments.done","output_index":1,"arguments":"{\"window\":60}"}`,
			`data: {"type":"response.output_item.done","output_index":1,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"status","arguments":"{\"window\":60}"}}`,
		}},
		{name: "completed_output", final: []string{
			`data: {"type":"response.completed","response":{"output":[{"type":"reasoning"},{"id":"fc_1","type":"function_call","call_id":"call_1","name":"status","arguments":"{\"window\":60}"}]}}`,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := []string{
				`data: {"type":"response.output_item.added","output_index":1,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"status","arguments":""}}`,
				`data: {"type":"response.function_call_arguments.delta","output_index":1,"item_id":"fc_1","delta":"{\"win"}`,
				`data: {"type":"response.function_call_arguments.delta","output_index":1,"item_id":"fc_1","delta":"dow\":60}"}`,
			}
			events = append(events, tc.final...)
			events = append(events, `data: {"type":"response.completed","response":{}}`)
			_, calls, stops, _, err := collect(t, openAIStream(t), lines(events...))
			if err != nil {
				t.Fatal(err)
			}
			want := []ToolCall{{ID: "call_1", Name: "status", Input: `{"window":60}`}}
			if !reflect.DeepEqual(calls, want) || !reflect.DeepEqual(stops, []string{"tool_calls"}) {
				t.Errorf("calls = %#v, stops = %v", calls, stops)
			}
		})
	}
}

func TestOpenAIResponsesParallelCallsAndEmptyArguments(t *testing.T) {
	input := lines(
		`data: {"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","call_id":"call_b","name":"topology","arguments":""}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":2,"delta":"{}"}`,
		`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call_a","name":"status","arguments":""}}`,
		`data: {"type":"response.completed","response":{}}`,
	)
	_, calls, _, _, err := collect(t, openAIStream(t), input)
	if err != nil {
		t.Fatal(err)
	}
	want := []ToolCall{{ID: "call_a", Name: "status", Input: "{}"}, {ID: "call_b", Name: "topology", Input: "{}"}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %#v, want %#v", calls, want)
	}
}

func TestOpenAIResponsesIncomplete(t *testing.T) {
	for _, tc := range []struct{ reason, want string }{{"max_output_tokens", "length"}, {"content_filter", "content_filter"}, {"", "incomplete"}} {
		t.Run(tc.want, func(t *testing.T) {
			input := lines(
				`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"partial","name":"status","arguments":""}}`,
				`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{"}`,
				`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","status":"incomplete","call_id":"partial","name":"status","arguments":"{"}}`,
				`data: {"type":"response.incomplete","response":{"output":[{"type":"function_call","status":"incomplete","call_id":"partial","name":"status","arguments":"{"}],"incomplete_details":{"reason":"`+tc.reason+`"},"usage":{"input_tokens":1,"output_tokens":2}}}`,
			)
			var stop StreamEvent
			err := openAIStream(t)(strings.NewReader(input), func(event StreamEvent) error {
				if event.Type == EventUsage {
					if event.Usage == nil || event.Usage.InputTokens != 1 || event.Usage.OutputTokens != 2 {
						t.Errorf("usage snapshot = %#v", event.Usage)
					}
					return nil
				}
				if event.Type != EventStop {
					t.Errorf("unexpected event: %#v", event)
				}
				stop = event
				return nil
			})
			if err != nil || stop.StopReason != tc.want || stop.Usage == nil || stop.Usage.OutputTokens != 2 {
				t.Errorf("stop = %#v, err = %v", stop, err)
			}
		})
	}
}

func TestOpenAIResponsesErrors(t *testing.T) {
	for _, input := range []string{
		`data: {"type":"response.failed","response":{"error":{"message":"rate limit exceeded"}}}`,
		`data: {"type":"error","message":"rate limit exceeded","code":"rate_limit_error"}`,
		`data: {"type":"error","error":{"message":"rate limit exceeded"}}`,
	} {
		_, _, stops, errs, err := collect(t, openAIStream(t), lines(input, `data: {"type":"response.completed","response":{}}`))
		if err != nil || len(stops) != 0 || !reflect.DeepEqual(errs, []string{"rate limit exceeded"}) {
			t.Errorf("stops = %v, errors = %v, err = %v", stops, errs, err)
		}
	}
}

func TestOpenAIResponsesStreamEndsWithoutTerminalResponse(t *testing.T) {
	_, _, _, _, err := collect(t, openAIStream(t), lines(`data: {"type":"response.output_text.delta","delta":"partial"}`))
	if err == nil || !strings.Contains(err.Error(), "terminal response") {
		t.Fatalf("err = %v, want terminal-response error", err)
	}
}

func TestOpenAIResponsesCallbackError(t *testing.T) {
	want := errors.New("callback failed")
	for _, input := range []string{
		`data: {"type":"response.output_text.delta","delta":"text"}`,
		lines(
			`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"call_1","name":"status","arguments":"{}"}}`,
			`data: {"type":"response.completed","response":{}}`,
		),
		`data: {"type":"response.completed","response":{}}`,
		`data: {"type":"error","message":"failed"}`,
	} {
		err := openAIStream(t)(strings.NewReader(lines(input)), func(StreamEvent) error { return want })
		if !errors.Is(err, want) {
			t.Errorf("err = %v, want callback error", err)
		}
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

func TestOpenAIResponsesRequestBodyShape(t *testing.T) {
	body := captureRequestBody(t, lines(`data: {"type":"response.completed","response":{}}`), func(baseURL string) error {
		provider, err := NewProvider("openai", "sk-test", "", baseURL+"/")
		if err != nil {
			return err
		}
		params := StreamParams{
			System: "be helpful",
			Messages: []ProviderMessage{
				{Role: RoleUser, Content: "hi"},
				{Role: RoleAssistant, Content: "checking", ToolCalls: []ToolCall{{ID: "call_1", Name: "status", Input: `{"window":60}`}}},
				{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: "call_1", Content: `{"ok":true}`}},
			},
			Tools:     []ToolDef{{Name: "status", Description: "check", InputSchema: map[string]any{"type": "object"}}},
			MaxTokens: 4096,
		}
		return provider.Stream(context.Background(), params, func(StreamEvent) error { return nil })
	})
	for _, key := range []string{"max_tokens", "max_completion_tokens", "messages", "temperature", "reasoning_effort"} {
		if _, ok := body[key]; ok {
			t.Errorf("unexpected request field %q", key)
		}
	}
	if body["max_output_tokens"] != float64(4096) || body["stream"] != true || body["store"] != false || body["instructions"] != "be helpful" || body["model"] != "gpt-6.1-sol" {
		t.Errorf("body = %#v", body)
	}
	if !reflect.DeepEqual(body["include"], []any{"reasoning.encrypted_content"}) {
		t.Errorf("include = %#v", body["include"])
	}
	wantTools := []any{map[string]any{"type": "function", "name": "status", "description": "check", "parameters": map[string]any{"type": "object"}, "strict": false}}
	if !reflect.DeepEqual(body["tools"], wantTools) {
		t.Errorf("tools = %#v", body["tools"])
	}
	wantInput := []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "checking"}}},
		map[string]any{"type": "function_call", "call_id": "call_1", "name": "status", "arguments": `{"window":60}`},
		map[string]any{"type": "function_call_output", "call_id": "call_1", "output": `{"ok":true}`},
	}
	if !reflect.DeepEqual(body["input"], wantInput) {
		t.Errorf("input = %#v, want %#v", body["input"], wantInput)
	}
}

func TestOpenAIResponsesReasoningReplayWithinRun(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"opaque_one","extra":{"preserved":true}}`),
		json.RawMessage(`{"id":"msg_1","type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Checking.","annotations":[]}]}`),
		json.RawMessage(`{"id":"rs_2","type":"reasoning","summary":[],"encrypted_content":"opaque_two"}`),
		json.RawMessage(`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"status","arguments":"{}"}`),
	}
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests = append(requests, body)
		w.Header().Set("Content-Type", "text/event-stream")
		if len(requests) == 1 {
			_, _ = io.WriteString(w, lines(
				// Done events may arrive out of index order; replay output order.
				`data: {"type":"response.output_text.delta","delta":"Checking."}`,
				`data: {"type":"response.output_item.done","output_index":2,"item":`+string(items[2])+`}`,
				`data: {"type":"response.output_item.done","output_index":3,"item":`+string(items[3])+`}`,
				`data: {"type":"response.output_item.done","output_index":0,"item":`+string(items[0])+`}`,
				`data: {"type":"response.output_item.done","output_index":1,"item":`+string(items[1])+`}`,
				`data: {"type":"response.completed","response":{}}`,
			))
		} else {
			_, _ = io.WriteString(w, lines(
				`data: {"type":"response.output_text.delta","delta":"Healthy."}`,
				`data: {"type":"response.completed","response":{}}`,
			))
		}
	}))
	defer server.Close()
	provider := &openAIProvider{apiKey: "sk-test", model: "gpt-6.1-sol", baseURL: server.URL, client: server.Client()}
	runtime := NewRuntime(provider, &fakeTools{execution: ToolExecution{Content: `{"ok":true}`}}, nil)
	emitter, output := newTestEmitter()
	messages := []agtypes.Message{{ID: "user-1", Role: agtypes.RoleUser, Content: "status?"}}
	if _, err := runtime.execute(context.Background(), "thread-1", "run-1", &messages, emitter); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	input := requests[1]["input"].([]any)
	if len(input) != 6 {
		t.Fatalf("input = %#v, want user, reasoning, commentary, reasoning, call, result", input)
	}
	for index, raw := range items {
		var want any
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(input[index+1], want) {
			t.Errorf("output item %d = %#v, want %#v", index, input[index+1], want)
		}
	}
	call, result := input[4].(map[string]any), input[5].(map[string]any)
	if call["type"] != "function_call" || call["call_id"] != "call_1" || result["type"] != "function_call_output" || result["call_id"] != "call_1" {
		t.Errorf("call = %#v, result = %#v", call, result)
	}
	// FinishRun serializes these AG-UI messages and events, not conversation.
	stored, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "opaque_") || strings.Contains(output.String(), "opaque_") {
		t.Error("encrypted reasoning leaked into persisted messages or client events")
	}
	if len(messages) != 4 || messages[3].Content != "Healthy." {
		t.Errorf("messages = %#v", messages)
	}
}

func TestAnthropicRequestBodyShape(t *testing.T) {
	sseBody := lines(
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
	)
	body := captureRequestBody(t, sseBody, func(baseURL string) error {
		provider := &anthropicProvider{apiKey: "sk-test", model: "claude-sonnet-5-5", baseURL: baseURL, client: http.DefaultClient}
		params := StreamParams{
			System: "be helpful",
			Messages: []ProviderMessage{
				{Role: RoleUser, Content: "check"},
				{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "tc1", Name: "status", Input: `{"window":60}`}}, ProviderItems: []json.RawMessage{json.RawMessage(`{"type":"reasoning","encrypted_content":"ignored"}`)}},
				{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: "tc1", Content: `{"ok":true}`}},
			},
			MaxTokens: 4096,
		}
		return provider.Stream(context.Background(), params, func(StreamEvent) error { return nil })
	})
	if got, ok := body["max_tokens"].(float64); !ok || got != 4096 {
		t.Errorf("max_tokens = %v, want 4096", body["max_tokens"])
	}
	wantSystem := []any{map[string]any{"type": "text", "text": "be helpful", "cache_control": map[string]any{"type": "ephemeral"}}}
	if !reflect.DeepEqual(body["system"], wantSystem) {
		t.Errorf("system = %v, want cached system block", body["system"])
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
