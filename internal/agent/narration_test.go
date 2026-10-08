package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
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
	if strings.Contains(out.String(), "Fixing the sort.") {
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
		if strings.Contains(out.String(), hidden) {
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
		{name: "partial tool", reason: "max_tokens", text: "Partial response", tool: true, truncated: true, want: "Partial response\n\n[Response truncated: output limit reached.]"},
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

func TestRuntimeBuffersTextUntilSuccessfulStop(t *testing.T) {
	emitter, out := newTestEmitter()
	p := narrationProviderFunc(func(_ context.Context, _ StreamParams, cb func(StreamEvent) error) error {
		if err := cb(StreamEvent{Type: EventText, Delta: "Buffered final"}); err != nil {
			return err
		}
		if strings.Contains(out.String(), "TEXT_MESSAGE_") {
			t.Fatal("text escaped before stop")
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
			if len(messages) != 0 || strings.Contains(out.String(), "TEXT_MESSAGE_") || strings.Contains(out.String(), "Unfinished text") {
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
	if !w.failed && strings.Contains(string(p), w.target) {
		w.failed = true
		return 0, errors.New("emitter unavailable")
	}
	return w.out.Write(p)
}

func TestRuntimeFinalEmitterFailureDoesNotPersistAnswer(t *testing.T) {
	for _, eventType := range []string{"TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END"} {
		t.Run(eventType, func(t *testing.T) {
			emitter, _ := newTestEmitter()
			writer := &narrationFailWriter{target: eventType}
			emitter.writer = writer
			messages := []agtypes.Message{}
			_, err := NewRuntime(textProvider{}, &fakeTools{}, nil).execute(t.Context(), "thread", "run", &messages, emitter)
			if err == nil || !writer.failed {
				t.Fatal("missing emitter error")
			}
			if len(messages) != 0 || strings.Contains(writer.out.String(), "RUN_FINISHED") {
				t.Fatal("failed delivery recorded success")
			}
			if !strings.Contains(writer.out.String(), "RUN_ERROR") {
				t.Fatal("missing failure outcome")
			}
		})
	}
}
