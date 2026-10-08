package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	controlstore "github.com/labstack/fanout/internal/store"
)

func TestOpenAIOutputItemsReplayVerbatim(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"type":"reasoning","id":"r1","encrypted_content":"secret1","summary":[]}`),
		json.RawMessage(`{"type":"message","id":"m1","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"checking","annotations":[]}]}`),
		json.RawMessage(`{"type":"reasoning","id":"r2","encrypted_content":"secret2","summary":[]}`),
		json.RawMessage(`{"type":"function_call","id":"f1","call_id":"c1","name":"status","arguments":"{}","status":"completed"}`),
	}
	var sse []string
	for _, index := range []int{3, 1, 0, 2} {
		sse = append(sse, fmt.Sprintf(`data: {"type":"response.output_item.done","output_index":%d,"item":%s}`, index, items[index]))
	}
	sse = append(sse,
		`data: {"type":"response.output_item.done","output_index":4,"item":{"type":"reasoning","summary":[]}}`,
		`data: {"type":"response.output_item.done","output_index":5,"item":{"type":"function_call","call_id":"unfinished","status":"incomplete","arguments":"{"}}`,
		`data: {"type":"response.completed","response":{}}`)
	var stop StreamEvent
	var calls []ToolCall
	if err := parseOpenAI(strings.NewReader(lines(sse...)), func(e StreamEvent) error {
		if e.Type == EventStop {
			stop = e
		}
		if e.Type == EventToolUse {
			calls = append(calls, *e.ToolCall)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stop.ProviderItems, items) {
		t.Errorf("items = %s, want %s", stop.ProviderItems, items)
	}
	if len(calls) != 1 || calls[0].ID != "c1" {
		t.Errorf("calls = %#v", calls)
	}
	// Test request replay independently of parser failures: retained output must
	// replace rebuilt text/calls and calls without executed results must vanish.
	unsafe := json.RawMessage(`{"type":"function_call","call_id":"not_executed","name":"status","arguments":"{}"}`)
	body := captureRequestBody(t, lines(`data: {"type":"response.completed","response":{}}`), func(url string) error {
		p, err := NewProvider("openai", "test", "", url)
		if err != nil {
			return err
		}
		return p.Stream(context.Background(), StreamParams{Messages: []ProviderMessage{
			{Role: RoleAssistant, Content: "rebuilt text", ToolCalls: calls, ProviderItems: append(append([]json.RawMessage(nil), items...), unsafe)},
			{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: "c1", Content: "ok"}},
		}}, func(StreamEvent) error { return nil })
	})
	input := body["input"].([]any)
	if len(input) != 5 {
		t.Fatalf("replayed input = %#v", input)
	}
	for i, raw := range items {
		var want any
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(input[i], want) {
			t.Errorf("input[%d] = %#v, want %#v", i, input[i], want)
		}
	}
}

func TestOpenAIIncompleteNeverEmitsTools(t *testing.T) {
	input := lines(
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"c1","name":"status","arguments":"{}","status":"completed"}}`,
		`data: {"type":"response.incomplete","response":{"output":[{"type":"function_call","call_id":"c1","name":"status","arguments":"{}","status":"completed"}],"incomplete_details":{"reason":"max_output_tokens"}}}`,
	)
	var emitted []StreamEvent
	if err := parseOpenAI(strings.NewReader(input), func(event StreamEvent) error {
		emitted = append(emitted, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(emitted) != 1 || emitted[0].Type != EventStop || emitted[0].StopReason != "length" || len(emitted[0].ProviderItems) != 0 {
		t.Errorf("incomplete response emitted %#v, want only a stop without function calls", emitted)
	}
}

func TestProviderItemsNeverSerialized(t *testing.T) {
	items := []json.RawMessage{json.RawMessage(`{"encrypted_content":"private"}`)}
	for _, value := range []any{ProviderMessage{Role: RoleAssistant, ProviderItems: items}, StreamEvent{Type: EventStop, ProviderItems: items}} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(string(raw)), "provider") || strings.Contains(string(raw), "private") {
			t.Errorf("serialized opaque items: %s", raw)
		}
	}
}

func TestProvidersIgnoreUnknownEventShapes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parse func(io.Reader, func(StreamEvent) error) error
		stop  string
	}{
		{"openai", parseOpenAI, `data: {"type":"response.completed","response":{}}`},
		{"anthropic", parseAnthropic, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, stops, _, err := collect(t, tc.parse, lines(`data: {"type":"future.ignored","delta":{"odd":true},"response":[],"content_block":42}`, tc.stop))
			if err != nil || !reflect.DeepEqual(stops, []string{"end_turn"}) {
				t.Errorf("stops = %v, err = %v", stops, err)
			}
		})
	}
}

func TestOpenAIEncryptedContentConfigurationError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"reasoning.encrypted_content is unsupported by this model"}}`)
	}))
	defer server.Close()
	p, err := NewProvider("openai", "test", "custom-model", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	err = p.Stream(context.Background(), StreamParams{}, func(StreamEvent) error { return nil })
	var apiErr *APIError
	if err == nil || !strings.Contains(err.Error(), "reasoning model") || !strings.Contains(err.Error(), "ai.model") || !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
		t.Errorf("configuration error = %v", err)
	}
	if got := clientErrorMessage(err); got != "Fanout could not reach the model provider. Please try again." {
		t.Errorf("client error = %q", got)
	}
}

func TestNewProviderNewestDefaults(t *testing.T) {
	for _, kind := range []string{"", "anthropic", "openai"} {
		p, err := NewProvider(kind, "test", "", "")
		if err != nil {
			t.Fatal(err)
		}
		switch p := p.(type) {
		case *openAIProvider:
			if p.model != "gpt-6.1-sol" {
				t.Errorf("openai model = %q", p.model)
			}
		case *anthropicProvider:
			if p.model != "claude-sonnet-5-5" {
				t.Errorf("anthropic model = %q", p.model)
			}
		}
	}
}

func TestAnthropicCacheBreakpoints(t *testing.T) {
	for _, finalRole := range []Role{RoleUser, RoleTool} {
		t.Run(string(finalRole), func(t *testing.T) {
			messages := []ProviderMessage{{Role: RoleUser, Content: "first"}, {Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "a", Name: "status", Input: "{}"}, {ID: "b", Name: "status", Input: "{}"}}}, {Role: RoleTool, ToolResult: &ToolResult{ToolCallID: "a", Content: "ok"}}}
			if finalRole == RoleUser {
				messages = append(messages, ProviderMessage{Role: RoleUser, Content: "next"})
			} else {
				messages = append(messages, ProviderMessage{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: "b", Content: "last"}})
			}
			body := captureRequestBody(t, lines(`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`), func(url string) error {
				p, err := NewProvider("anthropic", "test", "", url)
				if err != nil {
					return err
				}
				return p.Stream(context.Background(), StreamParams{System: "system", Tools: []ToolDef{{Name: "first"}, {Name: "last"}}, Messages: messages}, func(StreamEvent) error { return nil })
			})
			cache := map[string]any{"type": "ephemeral"}
			system, ok := body["system"].([]any)
			if !ok || len(system) != 1 {
				t.Fatalf("system blocks = %#v", body["system"])
			}
			if !reflect.DeepEqual(system[0].(map[string]any)["cache_control"], cache) {
				t.Errorf("system = %#v", system)
			}
			tools := body["tools"].([]any)
			if _, ok := tools[0].(map[string]any)["cache_control"]; ok {
				t.Error("earlier tool is cached")
			}
			if !reflect.DeepEqual(tools[1].(map[string]any)["cache_control"], cache) {
				t.Errorf("tools = %#v", tools)
			}
			for i, m := range body["messages"].([]any) {
				blocks, ok := m.(map[string]any)["content"].([]any)
				if !ok {
					t.Fatalf("message content is not blocks: %#v", m)
				}
				for j, block := range blocks {
					want := any(nil)
					if i == len(messages)-1 && j == len(blocks)-1 {
						want = cache
					}
					if got := block.(map[string]any)["cache_control"]; !reflect.DeepEqual(got, want) {
						t.Errorf("message %d block %d cache = %#v, want %#v", i, j, got, want)
					}
				}
			}
		})
	}
}

func TestProviderCacheUsage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parse func(io.Reader, func(StreamEvent) error) error
		input string
		want  map[string]int
	}{
		{"openai", parseOpenAI, lines(`data: {"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":25,"input_tokens_details":{"cached_tokens":80},"output_tokens_details":{"reasoning_tokens":20}}}}`), map[string]int{"InputTokens": 100, "OutputTokens": 25, "ReasoningTokens": 20, "CacheReadTokens": 80, "CacheWriteTokens": 0}},
		{"anthropic", parseAnthropic, lines(`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1,"cache_creation_input_tokens":50,"cache_read_input_tokens":80}}}`, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":25}}`), map[string]int{"InputTokens": 10, "OutputTokens": 25, "ReasoningTokens": 0, "CacheReadTokens": 80, "CacheWriteTokens": 50}},
		{"anthropic_delta", parseAnthropic, lines(`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":50,"cache_read_input_tokens":80}}}`, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":25,"cache_creation_input_tokens":0,"cache_read_input_tokens":90}}`), map[string]int{"InputTokens": 10, "OutputTokens": 25, "ReasoningTokens": 0, "CacheReadTokens": 90, "CacheWriteTokens": 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var usage *TokenUsage
			err := tc.parse(strings.NewReader(tc.input), func(e StreamEvent) error {
				if e.Type == EventStop {
					usage = e.Usage
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(usage)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]int
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("usage = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRuntimeOpenAITerminalNotices(t *testing.T) {
	for _, tc := range []struct {
		name, sse, notice, status string
		truncated                 bool
	}{
		{"incomplete", lines(`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"c1","name":"status","arguments":"{}","status":"completed"}}`, `data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`), "The response was cut off before it finished.", "truncated", true},
		{"incomplete_other", lines(`data: {"type":"response.incomplete","response":{}}`), "The response was cut off before it finished.", "truncated", true},
		{"incomplete_unknown_reason", lines(`data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"future_reason"}}}`), "The response was cut off before it finished.", "truncated", true},
		{"content_filter", lines(`data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"content_filter"}}}`), "The response was cut off before it finished.", "truncated", true},
		{"refusal", lines(`data: {"type":"response.refusal.delta","delta":"I cannot help with that."}`, `data: {"type":"response.completed","response":{}}`), "I cannot help with that.", "completed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, tc.sse) }))
			defer server.Close()
			p, err := NewProvider("openai", "test", "", server.URL)
			if err != nil {
				t.Fatal(err)
			}
			tools := &fakeTools{}
			runtime := NewRuntime(p, tools, nil)
			runtime.maxSteps = 1
			emitter, output := newTestEmitter()
			messages := []agtypes.Message{{ID: "u1", Role: agtypes.RoleUser, Content: "status?"}}
			db, err := controlstore.NewSQLite(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store := NewStore(db.DB)
			if _, err := store.StartRun(context.Background(), "owner", agtypes.RunAgentInput{ThreadID: "thread", RunID: "run", Messages: messages}); err != nil {
				t.Fatal(err)
			}
			truncated, runErr := runtime.execute(context.Background(), "thread", "run", &messages, emitter)
			if runErr != nil || truncated != tc.truncated {
				t.Errorf("truncated = %v, err = %v", truncated, runErr)
			}
			if len(tools.calls) != 0 {
				t.Errorf("executed tools: %#v", tools.calls)
			}
			assertEventOrder(t, output.String(), "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END", "RUN_FINISHED")
			if len(messages) != 2 || messages[1].Content != tc.notice {
				t.Errorf("messages = %#v, want notice %q", messages, tc.notice)
			}
			if err := store.FinishRun(context.Background(), "owner", "thread", "run", messages, emitter.events, truncated, runErr); err != nil {
				t.Fatal(err)
			}
			var status string
			if err := db.DB.QueryRow(`SELECT status FROM agui_runs WHERE run_id = 'run'`).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status != tc.status {
				t.Errorf("status = %q, want %q", status, tc.status)
			}
		})
	}
}

func TestRuntimeBlankStopNotices(t *testing.T) {
	for _, reason := range []string{"length", "max_tokens", "refusal"} {
		t.Run(reason, func(t *testing.T) {
			provider := &scriptedProvider{steps: [][]StreamEvent{{{Type: EventStop, StopReason: reason}}}}
			runtime := NewRuntime(provider, &fakeTools{}, nil)
			emitter, output := newTestEmitter()
			messages := []agtypes.Message{{ID: "u", Role: agtypes.RoleUser, Content: "question"}}
			truncated, err := runtime.execute(context.Background(), "thread", "run", &messages, emitter)
			if err != nil {
				t.Fatal(err)
			}
			want := "The response was cut off before it finished."
			if reason == "refusal" {
				want = "The model refused to answer this request."
			}
			if truncated != (reason != "refusal") {
				t.Errorf("truncated = %v", truncated)
			}
			assertEventOrder(t, output.String(), "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END", "RUN_FINISHED")
			if len(messages) != 2 || messages[1].Content != want {
				t.Errorf("messages = %#v, want %q", messages, want)
			}
		})
	}
}

func TestRuntimeLogsTokenUsage(t *testing.T) {
	// The process-wide logger is restored and these tests do not run in parallel.
	old := slog.Default()
	defer slog.SetDefault(old)
	for _, reason := range []string{"end_turn", "length"} {
		t.Run(reason, func(t *testing.T) {
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			var usage TokenUsage
			if err := json.Unmarshal([]byte(`{"InputTokens":100,"OutputTokens":25,"ReasoningTokens":20,"CacheReadTokens":80,"CacheWriteTokens":50}`), &usage); err != nil {
				t.Fatal(err)
			}
			p := &scriptedProvider{steps: [][]StreamEvent{{{Type: EventText, Delta: "text"}, {Type: EventStop, StopReason: reason, Usage: &usage}}}}
			runtime := NewRuntime(p, &fakeTools{}, nil)
			emitter, _ := newTestEmitter()
			messages := []agtypes.Message{{ID: "u", Role: agtypes.RoleUser, Content: "question"}}
			if _, err := runtime.execute(context.Background(), "thread", "run", &messages, emitter); err != nil {
				t.Fatal(err)
			}
			var record map[string]any
			if err := json.NewDecoder(&logs).Decode(&record); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]float64{"input_tokens": 100, "output_tokens": 25, "reasoning_tokens": 20, "cache_read_tokens": 80, "cache_write_tokens": 50} {
				if record[key] != want {
					t.Errorf("log %s = %#v, want %v: %#v", key, record[key], want, record)
				}
			}
			if reason == "end_turn" && record["msg"] != "llm stream complete" {
				t.Errorf("completion log = %#v", record)
			}
		})
	}
}
