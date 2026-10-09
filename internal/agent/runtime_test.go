package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"
	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/auth"
	fanoutmcp "github.com/labstack/fanout/internal/mcp"
	"github.com/labstack/fanout/internal/panel"

	"github.com/labstack/fanout/internal/dashboard"
	controlstore "github.com/labstack/fanout/internal/store"
)

type textProvider struct{}

func (textProvider) Stream(_ context.Context, _ StreamParams, callback func(StreamEvent) error) error {
	if err := callback(StreamEvent{Type: EventText, Delta: "Telemetry "}); err != nil {
		return err
	}
	if err := callback(StreamEvent{Type: EventText, Delta: "looks healthy."}); err != nil {
		return err
	}
	return callback(StreamEvent{Type: EventStop, StopReason: "end_turn"})
}

// scriptedProvider replays one scripted event sequence per Stream call and
// records the messages each call received.
type scriptedProvider struct {
	steps [][]StreamEvent
	got   [][]ProviderMessage
}

func (p *scriptedProvider) Stream(_ context.Context, params StreamParams, callback func(StreamEvent) error) error {
	p.got = append(p.got, append([]ProviderMessage(nil), params.Messages...))
	step := len(p.got) - 1
	if step >= len(p.steps) {
		// Loop on the last step so maxSteps tests can run unbounded.
		step = len(p.steps) - 1
	}
	for _, event := range p.steps[step] {
		if err := callback(event); err != nil {
			return err
		}
	}
	return nil
}

type fakeTools struct {
	defs      []ToolDef
	execution ToolExecution
	err       error
	calls     []ToolCall
}

func (f *fakeTools) ReadOnly(name string) bool { return reviewedReadOnly(name, nil) }

func (f *fakeTools) Definitions() []ToolDef { return f.defs }

func (f *fakeTools) Execute(_ context.Context, call ToolCall) (ToolExecution, error) {
	f.calls = append(f.calls, call)
	return f.execution, f.err
}

func newTestEmitter() (*eventEmitter, *bytes.Buffer) {
	var output bytes.Buffer
	return &eventEmitter{ctx: context.Background(), writer: &output, sse: sse.NewSSEWriter()}, &output
}

// assertEventOrder checks that the given event types appear in the stream in
// the given order (the AG-UI contract).
func assertEventOrder(t *testing.T, stream string, eventTypes ...string) {
	t.Helper()
	last := -1
	for _, eventType := range eventTypes {
		index := strings.Index(stream[last+1:], `"type":"`+eventType+`"`)
		if index < 0 {
			t.Fatalf("stream missing %s after offset %d: %s", eventType, last, stream)
		}
		last += 1 + index
	}
}

func TestRuntimeEmitsStandardAGUISequence(t *testing.T) {
	runtime := NewRuntime(textProvider{}, &ToolRegistry{}, nil)
	emitter, output := newTestEmitter()
	messages := []agtypes.Message{{ID: "user-1", Role: agtypes.RoleUser, Content: "status?"}}
	truncated, err := runtime.execute(context.Background(), "thread-1", "run-1", &messages, emitter)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("truncated = true for a clean end_turn stop")
	}
	assertEventOrder(t, output.String(), "RUN_STARTED", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END", "RUN_FINISHED")
	if len(messages) != 2 || messages[1].Content != "Telemetry looks healthy." {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestRuntimeToolCallLoop(t *testing.T) {
	provider := &scriptedProvider{steps: [][]StreamEvent{
		{
			{Type: EventToolUse, ToolCall: &ToolCall{ID: "call-1", Name: "get_observability_overview", Input: `{"window":"1h"}`}},
			{Type: EventStop, StopReason: "tool_calls"},
		},
		{
			{Type: EventText, Delta: "All services healthy."},
			{Type: EventStop, StopReason: "end_turn"},
		},
	}}
	tools := &fakeTools{
		defs:      []ToolDef{{Name: "get_observability_overview"}},
		execution: ToolExecution{Content: `{"ok":true}`, Structured: map[string]any{"ok": true}, AppResourceURI: "ui://overview"},
	}
	runtime := NewRuntime(provider, tools, nil)
	emitter, output := newTestEmitter()
	messages := []agtypes.Message{{ID: "user-1", Role: agtypes.RoleUser, Content: "status?"}}
	if _, err := runtime.execute(context.Background(), "thread-1", "run-1", &messages, emitter); err != nil {
		t.Fatal(err)
	}

	assertEventOrder(t, output.String(),
		"RUN_STARTED", "TOOL_CALL_START", "TOOL_CALL_ARGS", "TOOL_CALL_END", "TOOL_CALL_RESULT",
		"ACTIVITY_SNAPSHOT", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END", "RUN_FINISHED")

	if len(tools.calls) != 1 || tools.calls[0].Name != "get_observability_overview" || tools.calls[0].Input != `{"window":"1h"}` {
		t.Fatalf("tool calls = %#v", tools.calls)
	}

	// The second provider call must see the assistant tool call and the tool result.
	if len(provider.got) != 2 {
		t.Fatalf("provider called %d times, want 2", len(provider.got))
	}
	second := provider.got[1]
	var sawCall, sawResult bool
	for _, message := range second {
		if message.Role == RoleAssistant && len(message.ToolCalls) == 1 && message.ToolCalls[0].ID == "call-1" {
			sawCall = true
		}
		if message.Role == RoleTool && message.ToolResult != nil && message.ToolResult.ToolCallID == "call-1" && message.ToolResult.Content == `{"ok":true}` {
			sawResult = true
		}
	}
	if !sawCall || !sawResult {
		t.Fatalf("second provider call missing tool call/result: %#v", second)
	}

	// The activity snapshot for the MCP app must land in the persisted messages.
	var activity *agtypes.Message
	for i := range messages {
		if messages[i].Role == agtypes.RoleActivity {
			activity = &messages[i]
		}
	}
	if activity == nil || activity.ActivityType != "mcp-app" {
		t.Fatalf("missing mcp-app activity message: %#v", messages)
	}
	content, ok := activity.Content.(map[string]any)
	if !ok || content["resource_uri"] != "ui://overview" {
		t.Fatalf("activity content = %#v", activity.Content)
	}
}

func TestRuntimeProviderErrorSanitizedAndPersisted(t *testing.T) {
	provider := &scriptedProvider{steps: [][]StreamEvent{
		{{Type: EventText, Delta: "Unfinished provider text"}, {Type: EventError, Error: "upstream 529: {\"secret\":\"provider body\"}"}},
	}}
	runtime := NewRuntime(provider, &fakeTools{}, nil)
	emitter, output := newTestEmitter()
	messages := []agtypes.Message{{ID: "user-1", Role: agtypes.RoleUser, Content: "status?"}}
	truncated, runErr := runtime.execute(context.Background(), "thread-1", "run-1", &messages, emitter)
	if runErr == nil {
		t.Fatal("expected provider error")
	}
	stream := output.String()
	assertEventOrder(t, stream, "RUN_STARTED", "RUN_ERROR")
	if !strings.Contains(stream, "Fanout could not reach the model provider. Please try again.") {
		t.Errorf("RUN_ERROR missing generic message: %s", stream)
	}
	if strings.Contains(stream, "secret") {
		t.Errorf("RUN_ERROR leaks provider body: %s", stream)
	}
	if strings.Contains(answerSSE(stream), "Unfinished provider text") || strings.Contains(answerSSE(stream), "TEXT_MESSAGE_") || len(messages) != 1 {
		t.Fatalf("failed step leaked text: stream=%s messages=%#v", stream, messages)
	}

	// The raw error must still be persisted on the run for operators.
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store := NewStore(database.DB)
	input := agtypes.RunAgentInput{ThreadID: "thread-1", RunID: "run-1", Messages: messages}
	if _, err := store.StartRun(context.Background(), "owner-1", input); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(context.Background(), "owner-1", "thread-1", "run-1", messages, emitter.events, truncated, runErr); err != nil {
		t.Fatal(err)
	}
	var status, errorText string
	if err := database.DB.QueryRow(`SELECT status, error FROM agui_runs WHERE run_id = 'run-1'`).Scan(&status, &errorText); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || !strings.Contains(errorText, "provider body") {
		t.Errorf("run persisted as status=%q error=%q, want failed with raw error", status, errorText)
	}
}

func TestRuntimeToolExecutionErrorFeedsModel(t *testing.T) {
	provider := &scriptedProvider{steps: [][]StreamEvent{
		{
			{Type: EventToolUse, ToolCall: &ToolCall{ID: "call-1", Name: "search_logs", Input: `{}`}},
			{Type: EventStop, StopReason: "tool_calls"},
		},
		{
			{Type: EventText, Delta: "Could not search logs."},
			{Type: EventStop, StopReason: "end_turn"},
		},
	}}
	tools := &fakeTools{defs: []ToolDef{{Name: "search_logs"}}, err: errors.New("mcp session closed")}
	runtime := NewRuntime(provider, tools, nil)
	emitter, output := newTestEmitter()
	messages := []agtypes.Message{{ID: "user-1", Role: agtypes.RoleUser, Content: "logs?"}}
	if _, err := runtime.execute(context.Background(), "thread-1", "run-1", &messages, emitter); err != nil {
		t.Fatal(err)
	}
	assertEventOrder(t, output.String(), "TOOL_CALL_RESULT", "RUN_FINISHED")
	second := provider.got[1]
	var sawErrorResult bool
	for _, message := range second {
		if message.Role == RoleTool && message.ToolResult != nil && message.ToolResult.IsError && strings.Contains(message.ToolResult.Content, `"code":"tool_failed"`) && !strings.Contains(message.ToolResult.Content, "mcp session closed") {
			sawErrorResult = true
		}
	}
	if !sawErrorResult {
		t.Fatalf("second provider call missing error tool result: %#v", second)
	}
}

func TestRuntimeStepLimitExceeded(t *testing.T) {
	provider := &scriptedProvider{steps: [][]StreamEvent{
		{
			{Type: EventToolUse, ToolCall: &ToolCall{ID: "call-loop", Name: "get_observability_overview", Input: `{}`}},
			{Type: EventStop, StopReason: "tool_calls"},
		},
	}}
	tools := &fakeTools{defs: []ToolDef{{Name: "get_observability_overview"}}, execution: ToolExecution{Content: `{}`}}
	runtime := &Runtime{provider: provider, tools: tools, maxSteps: 2}
	emitter, output := newTestEmitter()
	messages := []agtypes.Message{{ID: "user-1", Role: agtypes.RoleUser, Content: "loop"}}
	_, err := runtime.execute(context.Background(), "thread-1", "run-1", &messages, emitter)
	if !errors.Is(err, errStepLimit) {
		t.Fatalf("err = %v, want errStepLimit", err)
	}
	if len(provider.got) != 2 {
		t.Fatalf("provider called %d times, want maxSteps=2", len(provider.got))
	}
	stream := output.String()
	assertEventOrder(t, stream, "RUN_STARTED", "RUN_ERROR")
	if !strings.Contains(stream, "Fanout reached its step limit before finishing. Try a narrower question.") {
		t.Errorf("RUN_ERROR missing step limit message: %s", stream)
	}
}

func TestRuntimeSurfacesTokenLimitTruncation(t *testing.T) {
	provider := &scriptedProvider{steps: [][]StreamEvent{
		{
			{Type: EventText, Delta: "The first half of an ans"},
			{Type: EventStop, StopReason: "length"},
		},
	}}
	runtime := NewRuntime(provider, &fakeTools{}, nil)
	emitter, output := newTestEmitter()
	messages := []agtypes.Message{{ID: "user-1", Role: agtypes.RoleUser, Content: "long question"}}
	truncated, err := runtime.execute(context.Background(), "thread-1", "run-1", &messages, emitter)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatal("truncated = false for stop reason length")
	}
	stream := output.String()
	assertEventOrder(t, stream, "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END", "RUN_FINISHED")
	if !strings.Contains(stream, "truncated") {
		t.Errorf("stream missing truncation notice: %s", stream)
	}
	if len(messages) != 2 || !strings.Contains(fmt.Sprint(messages[1].Content), "truncated") {
		t.Fatalf("assistant message missing truncation notice: %#v", messages)
	}
}

func TestSystemPromptForbidsTextDiagrams(t *testing.T) {
	for _, want := range []string{
		"never draw diagrams, trees, or charts in text",
		"never add a table or list that restates what an attached view already shows",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("system prompt is missing %q", want)
		}
	}
}

func TestSystemPromptBuildsDashboardsForDashboardShapedRequests(t *testing.T) {
	for _, want := range []string{
		"Build one whenever the user asks for an overview, asks why something is slow, failing or changing",
		"when a part has no data, keep its panel and say why",
		"Title each panel with exactly what it measures",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("system prompt is missing %q", want)
		}
	}
}

func TestBuildOriginUsesLastAuthoritativeUserAndUnicodeLimit(t *testing.T) {
	seed := []agtypes.Message{{ID: "old", Role: agtypes.RoleUser, Content: "Old"}, {ID: "last", Role: agtypes.RoleUser, Content: strings.Repeat("🐈", 281)}, {ID: "assistant", Role: agtypes.RoleAssistant, Content: "Never use narration"}}
	got, ok := buildOriginForSeed("thread", seed)
	want := dashboard.BuildOrigin{ThreadID: "thread", MessageID: "last", RequestExcerpt: strings.Repeat("🐈", 280)}
	if !ok || got != want {
		t.Fatal(got, ok)
	}
	if _, ok := buildOriginForSeed("thread", seed[2:]); ok {
		t.Fatal("fabricated request")
	}
}

func TestBuildOriginSkipsMissingMessageIdentity(t *testing.T) {
	for _, id := range []string{"", "   "} {
		if _, ok := buildOriginForSeed("thread", []agtypes.Message{{ID: id, Role: agtypes.RoleUser, Content: "Build"}}); ok {
			t.Fatal("origin without message identity")
		}
	}
}

type originValidator struct{}

func (originValidator) Validate(_ context.Context, d *panel.Dashboard) error {
	panel.Normalize(d)
	if problems := panel.Validate(d); len(problems) > 0 {
		return problems
	}
	return nil
}
func TestRuntimeRunInjectsAuthoritativeOriginIntoDashboardTool(t *testing.T) {
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	_, err = database.DB.Exec(`INSERT INTO users(id,email) VALUES ('owner','owner@example.test')`)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(database.DB)
	seed := []agtypes.Message{{ID: "old-user", Role: agtypes.RoleUser, Content: "Old request"}, {ID: "actual-user", Role: agtypes.RoleUser, Content: "Authoritative stored request"}}
	_, err = store.StartRun(t.Context(), "owner", agtypes.RunAgentInput{ThreadID: "thread", RunID: "seed", Messages: seed})
	if err != nil {
		t.Fatal(err)
	}
	service := dashboard.New(database.DB, originValidator{})
	registry, err := NewToolRegistry(t.Context(), fanoutmcp.NewWithIntelligence(registryQueries{}, service, nil, nil, "test").MCP())
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	provider := &scriptedProvider{steps: [][]StreamEvent{{{Type: EventToolUse, ToolCall: &ToolCall{ID: "create", Name: "create_dashboard", Input: `{"dashboard":{"name":"Wired","panels":[{"id":"notes","title":"Notes","viz":"text","content":"hello"}]}}`}}, {Type: EventStop, StopReason: "tool_calls"}}, {{Type: EventText, Delta: "Done."}, {Type: EventStop, StopReason: "end_turn"}}}}
	input := agtypes.RunAgentInput{ThreadID: "thread", RunID: "run", Messages: []agtypes.Message{{ID: "actual-user", Role: agtypes.RoleUser, Content: "Browser forged request"}, {ID: "old-user", Role: agtypes.RoleUser, Content: "Browser last user is wrong"}}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/agent/runs", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("auth_user", &auth.User{ID: "owner", Role: "admin"})
	if err := NewRuntime(provider, registry, store).Run(c); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body)
	}
	list, err := service.List(t.Context(), "owner")
	want := dashboard.BuildOrigin{ThreadID: "thread", MessageID: "actual-user", RequestExcerpt: "Authoritative stored request"}
	if err != nil || len(list) != 1 || list[0].Origin == nil || *list[0].Origin != want {
		t.Fatalf("runtime origin=%+v err=%v stream=%s", list, err, rec.Body)
	}
}
