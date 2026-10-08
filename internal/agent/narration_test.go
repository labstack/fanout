package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	controlstore "github.com/labstack/fanout/internal/store"
)

func TestRuntimeKeepsToolNarrationOutOfTranscript(t *testing.T) {
	p := &scriptedProvider{steps: [][]StreamEvent{
		{{Type: EventText, Delta: "Fixing the sort."}, {Type: EventToolUse, ToolCall: &ToolCall{ID: "call-1", Name: "edit_dashboard", Input: `{"id":"board","operations":[]}`}}, {Type: EventStop, StopReason: "tool_use"}},
		{{Type: EventText, Delta: "Updated the latency panel."}, {Type: EventStop, StopReason: "end_turn"}},
	}}
	tools := &fakeTools{execution: ToolExecution{Content: `{"dashboard":{"id":"board","version":2}}`}}
	r := NewRuntime(p, tools, nil)
	emitter, out := newTestEmitter()
	messages := []agtypes.Message{{ID: "user", Role: agtypes.RoleUser, Content: "Sort latency worst first"}}
	if _, err := r.execute(context.Background(), "thread", "run", &messages, emitter); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(answerSSE(out.String()), "Fixing the sort.") {
		t.Fatal("intermediate text escaped on SSE")
	}
	if !strings.Contains(out.String(), "Updated the latency panel.") {
		t.Fatal("final answer missing")
	}
	for _, m := range messages {
		if m.Role == agtypes.RoleAssistant && len(m.ToolCalls) > 0 && messageText(m.Content) != "" {
			t.Fatalf("persisted narration: %+v", m)
		}
	}
	seen := false
	for _, m := range p.got[1] {
		if m.Role == RoleAssistant && m.Content == "Fixing the sort." && len(m.ToolCalls) == 1 {
			seen = true
		}
	}
	if !seen {
		t.Fatal("provider continuation lost tool-bearing text")
	}
	assertEventOrder(t, out.String(), "RUN_STARTED", "TOOL_CALL_START", "TOOL_CALL_END", "TOOL_CALL_RESULT", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END", "RUN_FINISHED")
}

func TestRuntimeSuppressesNarrationAcrossToolSteps(t *testing.T) {
	opaque := []json.RawMessage{json.RawMessage(`{"type":"reasoning","id":"private-reasoning"}`)}
	p := &scriptedProvider{steps: [][]StreamEvent{
		{{Type: EventText, Delta: "Before tool."}, {Type: EventToolUse, ToolCall: &ToolCall{ID: "one", Name: "edit_dashboard", Input: `{}`}}, {Type: EventText, Delta: "After tool."}, {Type: EventStop, StopReason: "tool_use", ProviderItems: opaque}},
		{{Type: EventToolUse, ToolCall: &ToolCall{ID: "two", Name: "edit_dashboard", Input: `{}`}}, {Type: EventText, Delta: "Another step."}, {Type: EventStop, StopReason: "tool_use"}},
		{{Type: EventText, Delta: "Final "}, {Type: EventText, Delta: "answer."}, {Type: EventStop, StopReason: "end_turn"}},
	}}
	emitter, out := newTestEmitter()
	messages := []agtypes.Message{{ID: "user", Role: agtypes.RoleUser, Content: "Edit"}}
	tools := &fakeTools{execution: ToolExecution{Content: `{"ok":true}`, AppResourceURI: "ui://fixture", Structured: map[string]any{"ok": true}}}
	if _, err := NewRuntime(p, tools, nil).execute(t.Context(), "thread", "run", &messages, emitter); err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"Before tool.", "After tool.", "Another step.", "private-reasoning"} {
		if strings.Contains(answerSSE(out.String()), hidden) {
			t.Fatalf("SSE leaked %q", hidden)
		}
	}
	if strings.Count(out.String(), `"type":"TEXT_MESSAGE_CONTENT"`) != 1 || !strings.Contains(out.String(), "Final answer.") {
		t.Fatal(out.String())
	}
	assertEventOrder(t, out.String(), "TOOL_CALL_RESULT", "ACTIVITY_SNAPSHOT", "TOOL_CALL_START", "TOOL_CALL_RESULT", "ACTIVITY_SNAPSHOT", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END", "RUN_FINISHED")
	var sawOpaque, sawSecond bool
	for _, m := range p.got[2] {
		if m.Content == "Before tool.After tool." && len(m.ToolCalls) == 1 && len(m.ProviderItems) == 1 {
			sawOpaque = true
		}
		if m.Content == "Another step." && len(m.ToolCalls) == 1 {
			sawSecond = true
		}
	}
	if !sawOpaque || !sawSecond {
		t.Fatalf("continuation lost: %#v", p.got[2])
	}
	for _, m := range providerMessages(messages) {
		if len(m.ToolCalls) > 0 && m.Content != "" {
			t.Fatalf("reload reintroduced narration: %+v", m)
		}
	}
}

func TestRuntimeFinalOutcomesStayVisible(t *testing.T) {
	for _, tc := range []struct {
		name, reason, text, want string
		tool, truncated          bool
	}{
		{name: "text final", reason: "end_turn", text: "Final answer", want: "Final answer"},
		{name: "empty final", reason: "end_turn"},
		{name: "refusal text", reason: "refusal", text: "I cannot help.", want: "I cannot help."},
		{name: "empty refusal", reason: "refusal", want: "The model refused to answer this request."},
		{name: "partial tool", reason: "max_tokens", text: "Partial response", tool: true, truncated: true, want: "The response was cut off before it finished."},
		{name: "empty partial tool", reason: "length", tool: true, truncated: true, want: "The response was cut off before it finished."},
		{name: "incomplete", reason: "incomplete", text: "Partial", truncated: true, want: "Partial\n\nThe response was cut off before it finished."},
		{name: "filtered", reason: "content_filter", truncated: true, want: "The response was cut off before it finished."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := []StreamEvent{{Type: EventText, Delta: tc.text}}
			if tc.tool {
				step = append(step, StreamEvent{Type: EventToolUse, ToolCall: &ToolCall{ID: "partial", Name: "create_dashboard", Input: `{"dashboard":`}})
			}
			step = append(step, StreamEvent{Type: EventStop, StopReason: tc.reason})
			p := &scriptedProvider{steps: [][]StreamEvent{step}}
			tools := &fakeTools{}
			emitter, out := newTestEmitter()
			messages := []agtypes.Message{}
			truncated, err := NewRuntime(p, tools, nil).execute(t.Context(), "thread", "run", &messages, emitter)
			if err != nil || truncated != tc.truncated {
				t.Fatalf("truncated=%t err=%v", truncated, err)
			}
			if len(tools.calls) != 0 || strings.Contains(out.String(), "TOOL_CALL_START") {
				t.Fatal("partial tool executed")
			}
			if tc.want == "" {
				if len(messages) != 0 || strings.Contains(out.String(), "TEXT_MESSAGE_") {
					t.Fatal("empty final emitted text")
				}
			} else {
				if len(messages) != 1 || messageText(messages[0].Content) != tc.want {
					t.Fatalf("messages=%#v", messages)
				}
				encoded, _ := json.Marshal(tc.want)
				if !strings.Contains(out.String(), string(encoded)) {
					t.Fatal(out.String())
				}
				assertEventOrder(t, out.String(), "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END", "RUN_FINISHED")
			}
			if !strings.Contains(out.String(), "RUN_FINISHED") {
				t.Fatal("missing terminal outcome")
			}
		})
	}
}

type narrationProviderFunc func(context.Context, StreamParams, func(StreamEvent) error) error

func (f narrationProviderFunc) Stream(ctx context.Context, p StreamParams, cb func(StreamEvent) error) error {
	return f(ctx, p, cb)
}

func TestRuntimeStreamsProvisionalTextBeforeSuccessfulStop(t *testing.T) {
	emitter, out := newTestEmitter()
	p := narrationProviderFunc(func(_ context.Context, _ StreamParams, cb func(StreamEvent) error) error {
		if err := cb(StreamEvent{Type: EventText, Delta: "Buffered final"}); err != nil {
			return err
		}
		assertEventOrder(t, out.String(), "THINKING_START", "THINKING_TEXT_MESSAGE_START", "THINKING_TEXT_MESSAGE_CONTENT")
		if strings.Contains(out.String(), `"type":"TEXT_MESSAGE_CONTENT"`) {
			t.Fatal("provisional text became an answer before stop")
		}
		return cb(StreamEvent{Type: EventStop, StopReason: "end_turn"})
	})
	messages := []agtypes.Message{}
	if _, err := NewRuntime(p, &fakeTools{}, nil).execute(t.Context(), "thread", "run", &messages, emitter); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Buffered final") {
		t.Fatal("missing final")
	}
}

func TestRuntimeCancellationDiscardsBufferedText(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			p := narrationProviderFunc(func(ctx context.Context, _ StreamParams, cb func(StreamEvent) error) error {
				if err := cb(StreamEvent{Type: EventText, Delta: "Unfinished text"}); err != nil {
					return err
				}
				if deadline {
					<-ctx.Done()
				} else {
					cancel()
				}
				return nil
			})
			runtime := NewRuntime(p, &fakeTools{}, nil)
			if deadline {
				runtime.runTimeout = time.Millisecond
			}
			emitter, out := newTestEmitter()
			messages := []agtypes.Message{}
			_, err := runtime.execute(ctx, "thread", "run", &messages, emitter)
			want := context.Canceled
			if deadline {
				want = errStepLimit
			}
			if !errors.Is(err, want) {
				t.Fatalf("err=%v", err)
			}
			if len(messages) != 0 || strings.Contains(answerSSE(out.String()), "TEXT_MESSAGE_") || strings.Contains(answerSSE(out.String()), "Unfinished text") {
				t.Fatal("failed text escaped")
			}
			assertEventOrder(t, out.String(), "RUN_STARTED", "RUN_ERROR")
		})
	}
}

type narrationFailWriter struct {
	target string
	failed bool
	out    strings.Builder
}

func (w *narrationFailWriter) Write(p []byte) (int, error) {
	if !w.failed && (strings.Contains(string(p), `"type":"`+w.target+`"`) || strings.Contains(string(p), `"name":"`+w.target+`"`)) {
		w.failed = true
		return 0, errors.New("emitter unavailable")
	}
	return w.out.Write(p)
}

// Thinking frames are provisional live UI, never committed answer events.
func answerSSE(stream string) string {
	var out strings.Builder
	for _, line := range strings.Split(stream, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) == nil && !strings.HasPrefix(event.Type, "THINKING_") {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func TestRuntimeFinalEmitterFailurePreservesAnswer(t *testing.T) {
	for _, eventType := range []string{"TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END", "THINKING_END", "model_call_usage", "RUN_FINISHED"} {
		t.Run(eventType, func(t *testing.T) {
			emitter, _ := newTestEmitter()
			writer := &narrationFailWriter{target: eventType}
			emitter.writer = writer
			messages := []agtypes.Message{}
			_, err := NewRuntime(textProvider{}, &fakeTools{}, nil).execute(t.Context(), "thread", "run", &messages, emitter)
			if err == nil || !writer.failed {
				t.Fatal("missing emitter error")
			}
			if len(messages) != 1 || messages[0].Content != "Telemetry looks healthy." {
				t.Fatalf("final answer lost: %+v", messages)
			}
			if strings.Contains(writer.out.String(), "RUN_FINISHED") {
				t.Fatal("failed delivery recorded success")
			}
			if !strings.Contains(writer.out.String(), "RUN_ERROR") {
				t.Fatal("missing failure outcome")
			}
			db, openErr := controlstore.NewSQLite(":memory:")
			if openErr != nil {
				t.Fatal(openErr)
			}
			defer db.Close()
			store := NewStore(db.DB)
			if _, err := store.StartRun(t.Context(), "owner", agtypes.RunAgentInput{ThreadID: "thread", RunID: "run"}); err != nil {
				t.Fatal(err)
			}
			if err := store.FinishRun(t.Context(), "owner", "thread", "run", messages, emitter.events, false, err); err != nil {
				t.Fatal(err)
			}
			loaded, err := store.Thread(t.Context(), "owner", "thread")
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded.Messages) != 2 || loaded.Messages[0].Content != "Telemetry looks healthy." || loaded.Messages[1].ActivityType != "agent-outcome" {
				t.Fatalf("reloaded=%+v", loaded.Messages)
			}
		})
	}
}

func TestRuntimeThinkingStepsNeverPersistProvisionalText(t *testing.T) {
	p := &scriptedProvider{steps: [][]StreamEvent{
		{{Type: EventText, Delta: "Fixing "}, {Type: EventText, Delta: "the sort."}, {Type: EventToolUse, ToolCall: &ToolCall{ID: "call", Name: "edit_dashboard", Input: `{}`}}, {Type: EventStop, StopReason: "tool_use"}},
		{{Type: EventText, Delta: "Updated "}, {Type: EventText, Delta: "the panel."}, {Type: EventStop, StopReason: "end_turn"}},
	}}
	emitter, out := newTestEmitter()
	messages := []agtypes.Message{}
	if _, err := NewRuntime(p, &fakeTools{execution: ToolExecution{Content: `{}`}}, nil).execute(t.Context(), "thread", "run", &messages, emitter); err != nil {
		t.Fatal(err)
	}
	assertEventOrder(t, out.String(), "THINKING_START", "THINKING_TEXT_MESSAGE_START", "THINKING_TEXT_MESSAGE_CONTENT", "THINKING_TEXT_MESSAGE_CONTENT", "THINKING_TEXT_MESSAGE_END", "THINKING_END", "TOOL_CALL_START", "TOOL_CALL_RESULT", "THINKING_START", "THINKING_TEXT_MESSAGE_START", "THINKING_TEXT_MESSAGE_CONTENT", "THINKING_TEXT_MESSAGE_END", "THINKING_END", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END", "RUN_FINISHED")
	if strings.Count(out.String(), `"type":"TEXT_MESSAGE_CONTENT"`) != 1 {
		t.Fatal("tool narration became answer text")
	}
	for _, raw := range emitter.events {
		if strings.Contains(string(raw), "THINKING_") || strings.Contains(string(raw), "Fixing") {
			t.Fatalf("provisional event persisted: %s", raw)
		}
	}
	for _, m := range messages {
		if m.Role == agtypes.RoleAssistant && len(m.ToolCalls) > 0 && messageText(m.Content) != "" {
			t.Fatal("narration persisted")
		}
	}
}

func TestRuntimeErrorMessagesArePlain(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errProvider, "Fanout could not reach the model provider. Please try again."},
		{errStepLimit, "Fanout reached its step limit before finishing. Try a narrower question."},
		{fmt.Errorf("%w: 5-minute time limit reached", errStepLimit), "Fanout reached its 5-minute time limit. Try a smaller request."},
		{context.Canceled, "Stopped"},
		{errors.New("private unknown error"), "Fanout could not complete this analysis. Please try again."},
	} {
		if got := clientErrorMessage(tc.err); got != tc.want {
			t.Fatalf("got=%q want=%q", got, tc.want)
		}
	}
}

func TestRuntimeUnfinishedProviderToolTruncationHidesNarration(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			p := narrationProviderFunc(func(_ context.Context, _ StreamParams, cb func(StreamEvent) error) error {
				if provider == "openai" {
					return parseOpenAI(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"Building the dashboard.\"}\n"+"data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"call_id\":\"partial\",\"name\":\"create_dashboard\"}}\n"+"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"delta\":\"{\"}\n"+"data: {\"type\":\"response.incomplete\",\"response\":{\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n"), cb)
				}
				return parseAnthropic(strings.NewReader("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Building the dashboard.\"}}\n"+"data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"id\":\"partial\",\"name\":\"create_dashboard\"}}\n"+"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n"), cb)
			})
			emitter, _ := newTestEmitter()
			messages := []agtypes.Message{}
			tools := &fakeTools{}
			truncated, err := NewRuntime(p, tools, nil).execute(t.Context(), "thread", "run", &messages, emitter)
			if err != nil || !truncated {
				t.Fatalf("truncated=%t err=%v", truncated, err)
			}
			if len(tools.calls) != 0 || len(messages) != 1 || messages[0].Content != "The response was cut off before it finished." {
				t.Fatalf("messages=%+v calls=%v", messages, tools.calls)
			}
		})
	}
}
