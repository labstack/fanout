package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
)

type meteredProvider struct{ scriptedProvider }

func (*meteredProvider) usageIdentity() (string, string) { return "fake", "observed-model" }

func TestRuntimeRecordsAuthoritativeUsageOncePerCall(t *testing.T) {
	for _, terminal := range []StreamEvent{{Type: EventStop, StopReason: "end_turn"}, {Type: EventStop, StopReason: "length"}, {Type: EventError, Error: "private provider body"}} {
		name := terminal.StopReason
		if name == "" {
			name = "provider_error"
		}
		t.Run(name, func(t *testing.T) {
			u := &TokenUsage{InputTokens: 100, OutputTokens: 20, ReasoningTokens: 5, CacheReadTokens: 10, CacheWriteTokens: 3}
			terminal.Usage = u
			p := &meteredProvider{scriptedProvider{steps: [][]StreamEvent{
				{{Type: EventToolUse, ToolCall: &ToolCall{ID: "tool", Name: "get_dashboard", Input: `{}`}}, {Type: EventStop, StopReason: "tool_calls", Usage: u}},
				{terminal},
			}}}
			runtime := NewRuntime(p, &fakeTools{execution: ToolExecution{Content: `{"usage":{"input_tokens":999999}}`}}, nil)
			emitter, output := newTestEmitter()
			messages := []agtypes.Message{}
			_, _ = runtime.execute(context.Background(), "thread", "run", &messages, emitter)
			count := 0
			for _, raw := range emitter.events {
				var e struct {
					Name  string         `json:"name"`
					Value map[string]any `json:"value"`
				}
				if err := json.Unmarshal(raw, &e); err != nil {
					t.Fatal(err)
				}
				if e.Name != "model_call_usage" {
					continue
				}
				count++
				if e.Value["run_id"] != "run" || e.Value["provider"] != "fake" || e.Value["model"] != "observed-model" || e.Value["step"] != float64(count) {
					t.Fatalf("correlation: %#v", e.Value)
				}
				usage, ok := e.Value["usage"].(map[string]any)
				if !ok || usage["input_tokens"] != float64(100) || usage["output_tokens"] != float64(20) || usage["reasoning_tokens"] != float64(5) || usage["cache_read_tokens"] != float64(10) || usage["cache_write_tokens"] != float64(3) {
					t.Fatalf("usage: %#v", e.Value)
				}
			}
			if count != 2 {
				t.Fatalf("usage records=%d, want 2", count)
			}
			if strings.Contains(output.String(), "private provider body") {
				t.Fatal("provider body leaked")
			}
		})
	}
}

func TestProviderReportedUsageSurvivesFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		parse func(*strings.Reader, func(StreamEvent) error) error
	}{
		{"openai", `data: {"type":"response.failed","response":{"usage":{"input_tokens":12,"output_tokens":3},"error":{"message":"private"}}}` + "\n", func(r *strings.Reader, cb func(StreamEvent) error) error { return parseOpenAI(r, cb) }},
		{"anthropic", `data: {"type":"message_start","message":{"usage":{"input_tokens":12,"output_tokens":3}}}` + "\n" + `data: {"type":"error","error":{"message":"private"}}` + "\n", func(r *strings.Reader, cb func(StreamEvent) error) error { return parseAnthropic(r, cb) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var usage *TokenUsage
			_ = tc.parse(strings.NewReader(tc.input), func(e StreamEvent) error {
				if e.Usage != nil {
					usage = e.Usage
				}
				return nil
			})
			if usage == nil || usage.InputTokens != 12 || usage.OutputTokens != 3 {
				t.Fatalf("lost failure usage: %#v", usage)
			}
		})
	}
}

func TestRuntimeMarksMissingUsageWithoutInventingTokens(t *testing.T) {
	runtime := NewRuntime(textProvider{}, &fakeTools{}, nil)
	emitter, _ := newTestEmitter()
	messages := []agtypes.Message{}
	_, _ = runtime.execute(context.Background(), "thread", "run", &messages, emitter)
	for _, raw := range emitter.events {
		var e map[string]any
		_ = json.Unmarshal(raw, &e)
		if e["name"] == "model_call_usage" {
			if e["value"].(map[string]any)["usage"] != nil {
				t.Fatal("invented usage")
			}
			return
		}
	}
	t.Fatal("missing usage record")
}

func TestOpenAIUsageSurvivesMalformedTerminalOutput(t *testing.T) {
	var usage *TokenUsage
	err := parseOpenAI(strings.NewReader(`data: {"type":"response.completed","response":{"usage":{"input_tokens":12,"output_tokens":3},"output":[false]}}`+"\n"), func(e StreamEvent) error {
		if e.Usage != nil {
			usage = e.Usage
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected malformed output error")
	}
	if usage == nil || usage.InputTokens != 12 || usage.OutputTokens != 3 {
		t.Fatalf("lost reported usage: %#v", usage)
	}
}
