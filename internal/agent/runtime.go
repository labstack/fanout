package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"
	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/api"
	"github.com/labstack/fanout/internal/dashboard"
	appid "github.com/labstack/fanout/internal/id"
)

const systemPrompt = `You are Fanout's observability assistant. Use observability_overview first for broad health questions, service_topology for dependencies, service_performance for activity/latency/endpoints/comparisons, trace_detail for trace or root-cause inspection, and search_logs for log questions. Treat structured outputs as authoritative. You can also list, inspect, create, and update the user's named dashboards. When asked to create a dashboard, compose a useful complete layout using overview, topology, activity, performance, trace, logs, and assistant widgets; use unique stable widget IDs, valid non-overlapping 12-column positions, and the requested time window. Creating is additive. Only update an existing dashboard after the user explicitly asks to change or replace it; inspect it first and preserve unrelated views. State the time window you used, distinguish missing data from healthy behavior, and never invent services, metrics, or causal claims. Keep answers concise because attached views provide interactive details. Never expose implementation details to the user: do not mention protocol names, tool names, schemas, query IDs, data-source names, storage engines, providers, or internal execution steps. Refer to attached interactive content simply as a view.`

// Error categories used to pick a client-safe RUN_ERROR message; the raw
// error (which can include provider response bodies) stays server-side.
var (
	errProvider  = errors.New("model provider error")
	errStepLimit = errors.New("agent step limit exceeded")
)

// toolExecutor is the tool surface the runtime needs; *ToolRegistry implements it.
type toolExecutor interface {
	Definitions() []ToolDef
	Execute(context.Context, ToolCall) (ToolExecution, error)
}

type Runtime struct {
	provider Provider
	tools    toolExecutor
	store    *Store
	maxSteps int
}

func NewRuntime(provider Provider, tools toolExecutor, store *Store) *Runtime {
	return &Runtime{provider: provider, tools: tools, store: store, maxSteps: 8}
}

func (r *Runtime) Register(group *echo.Group) {
	group.POST("", r.Run)
	group.GET("/threads/:threadID", r.GetThread)
}

func (r *Runtime) GetThread(c *echo.Context) error {
	user := api.GetCurrentUser(c)
	if user == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	thread, err := r.store.Thread(c.Request().Context(), user.ID, c.Param("threadID"))
	if errors.Is(err, ErrThreadNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "thread not found")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to load thread").Wrap(err)
	}
	return c.JSON(http.StatusOK, thread)
}

func (r *Runtime) Run(c *echo.Context) error {
	user := api.GetCurrentUser(c)
	if user == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var input agtypes.RunAgentInput
	if err := c.Bind(&input); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid AG-UI input")
	}
	if input.ThreadID == "" {
		threadID, err := appid.New()
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to generate thread id").Wrap(err)
		}
		input.ThreadID = threadID
	}
	if input.RunID == "" {
		runID, err := appid.New()
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to generate run id").Wrap(err)
		}
		input.RunID = runID
	}
	if err := r.store.StartRun(c.Request().Context(), user.ID, input); err != nil {
		if errors.Is(err, ErrThreadNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "thread not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to start agent run").Wrap(err)
	}

	response := c.Response()
	response.Header().Set(echo.HeaderContentType, "text/event-stream")
	response.Header().Set(echo.HeaderCacheControl, "no-cache, no-transform")
	response.Header().Set(echo.HeaderConnection, "keep-alive")
	response.WriteHeader(http.StatusOK)
	emitter := &eventEmitter{ctx: c.Request().Context(), writer: response, sse: sse.NewSSEWriter()}
	messages := append([]agtypes.Message(nil), input.Messages...)
	runCtx := dashboard.WithOwner(c.Request().Context(), user.ID)
	truncated, runErr := r.execute(runCtx, input.ThreadID, input.RunID, &messages, emitter)
	if runErr != nil {
		slog.Error("agent run failed", "thread_id", input.ThreadID, "run_id", input.RunID, "err", runErr)
	}

	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(c.Request().Context()), 5*time.Second)
	defer cancel()
	if err := r.store.FinishRun(persistCtx, user.ID, input.ThreadID, input.RunID, messages, emitter.events, truncated, runErr); err != nil {
		// A persist failure after a successful stream silently loses the conversation.
		slog.Error("agent run persist failed", "thread_id", input.ThreadID, "run_id", input.RunID, "err", err)
	}
	// The SSE stream already carried the outcome (RUN_FINISHED or RUN_ERROR).
	return nil
}

func (r *Runtime) execute(ctx context.Context, threadID, runID string, messages *[]agtypes.Message, emitter *eventEmitter) (bool, error) {
	truncated := false
	if err := emitter.emit(events.NewRunStartedEvent(threadID, runID)); err != nil {
		return truncated, err
	}
	conversation := providerMessages(*messages)
	for step := 0; step < r.maxSteps; step++ {
		messageID, err := appid.New()
		if err != nil {
			return truncated, err
		}
		var text strings.Builder
		var toolCalls []ToolCall
		var stopReason string
		textStarted := false
		streamErr := r.provider.Stream(ctx, StreamParams{System: systemPrompt, Messages: conversation, Tools: r.tools.Definitions(), MaxTokens: 4096}, func(event StreamEvent) error {
			switch event.Type {
			case EventError:
				return fmt.Errorf("%w: %s", errProvider, event.Error)
			case EventText:
				if event.Delta == "" {
					return nil
				}
				if !textStarted {
					if err := emitter.emit(events.NewTextMessageStartEvent(messageID, events.WithRole("assistant"))); err != nil {
						return err
					}
					textStarted = true
				}
				text.WriteString(event.Delta)
				return emitter.emit(events.NewTextMessageContentEvent(messageID, event.Delta))
			case EventToolUse:
				if event.ToolCall != nil {
					toolCalls = append(toolCalls, *event.ToolCall)
				}
			case EventStop:
				stopReason = event.StopReason
			}
			return nil
		})
		if streamErr == nil && stoppedAtTokenLimit(stopReason) {
			// The model hit MaxTokens: the answer is incomplete, not a clean success.
			truncated = true
			slog.Warn("agent response truncated at token limit", "thread_id", threadID, "run_id", runID, "stop_reason", stopReason)
			if textStarted {
				const notice = "\n\n[Response truncated: output limit reached.]"
				if err := emitter.emit(events.NewTextMessageContentEvent(messageID, notice)); err == nil {
					text.WriteString(notice)
				}
			}
		}
		if textStarted {
			if err := emitter.emit(events.NewTextMessageEndEvent(messageID)); err != nil && streamErr == nil {
				streamErr = err
			}
		}
		if streamErr != nil {
			return truncated, r.fail(threadID, runID, streamErr, emitter)
		}

		agCalls := make([]agtypes.ToolCall, len(toolCalls))
		for i, call := range toolCalls {
			agCalls[i] = agtypes.ToolCall{ID: call.ID, Type: agtypes.ToolCallTypeFunction, Function: agtypes.FunctionCall{Name: call.Name, Arguments: call.Input}}
		}
		if text.Len() > 0 || len(agCalls) > 0 {
			*messages = append(*messages, agtypes.Message{ID: messageID, Role: agtypes.RoleAssistant, Content: text.String(), ToolCalls: agCalls})
			conversation = append(conversation, ProviderMessage{Role: RoleAssistant, Content: text.String(), ToolCalls: toolCalls})
		}
		if len(toolCalls) == 0 {
			if err := emitter.emit(events.NewRunFinishedEventWithOptions(threadID, runID, events.WithSuccessOutcome())); err != nil {
				return truncated, err
			}
			return truncated, nil
		}

		for _, call := range toolCalls {
			if err := emitter.emit(events.NewToolCallStartEvent(call.ID, call.Name, events.WithParentMessageID(messageID))); err != nil {
				return truncated, err
			}
			if err := emitter.emit(events.NewToolCallArgsEvent(call.ID, call.Input)); err != nil {
				return truncated, err
			}
			if err := emitter.emit(events.NewToolCallEndEvent(call.ID)); err != nil {
				return truncated, err
			}
			execution, err := r.tools.Execute(ctx, call)
			if err != nil {
				execution = ToolExecution{Content: fmt.Sprintf(`{"error":%q}`, err.Error()), IsError: true}
			}
			toolMessageID, err := appid.New()
			if err != nil {
				return truncated, err
			}
			if err := emitter.emit(events.NewToolCallResultEvent(toolMessageID, call.ID, execution.Content)); err != nil {
				return truncated, err
			}
			*messages = append(*messages, agtypes.Message{ID: toolMessageID, Role: agtypes.RoleTool, Content: execution.Content, ToolCallID: call.ID, Error: errorString(execution.IsError)})
			conversation = append(conversation, ProviderMessage{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: call.ID, Content: execution.Content, IsError: execution.IsError}})
			if execution.AppResourceURI != "" {
				activityID, err := appid.New()
				if err != nil {
					return truncated, err
				}
				content := map[string]any{"resourceUri": execution.AppResourceURI, "toolName": call.Name, "toolInput": json.RawMessage(call.Input), "toolResult": execution.Structured, "isError": execution.IsError}
				if content["toolResult"] == nil {
					content["toolResult"] = execution.Content
				}
				if err := emitter.emit(events.NewActivitySnapshotEvent(activityID, "mcp-app", content)); err != nil {
					return truncated, err
				}
				*messages = append(*messages, agtypes.Message{ID: activityID, Role: agtypes.RoleActivity, ActivityType: "mcp-app", Content: content})
			}
		}
	}
	return truncated, r.fail(threadID, runID, fmt.Errorf("%w: exceeded %d tool steps", errStepLimit, r.maxSteps), emitter)
}

// fail reports the failure to the client with a sanitized message and returns
// the raw error for server-side logging and persistence.
func (r *Runtime) fail(threadID, runID string, err error, emitter *eventEmitter) error {
	if emitErr := emitter.emit(events.NewRunErrorEvent(clientErrorMessage(err), events.WithRunID(runID))); emitErr != nil {
		slog.Error("agent RUN_ERROR emit failed", "thread_id", threadID, "run_id", runID, "err", emitErr)
	}
	return err
}

// clientErrorMessage maps a run error to a short message safe for the wire.
// Provider API responses can contain internal details and never leave the server.
func clientErrorMessage(err error) string {
	var apiErr *APIError
	switch {
	case errors.As(err, &apiErr), errors.Is(err, errProvider):
		return "model provider unavailable"
	case errors.Is(err, errStepLimit):
		return "step limit exceeded"
	default:
		return "agent run failed"
	}
}

// stoppedAtTokenLimit reports whether the provider stopped because MaxTokens
// was reached (OpenAI "length", Anthropic "max_tokens").
func stoppedAtTokenLimit(stopReason string) bool {
	return stopReason == "length" || stopReason == "max_tokens"
}

func providerMessages(messages []agtypes.Message) []ProviderMessage {
	out := make([]ProviderMessage, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case agtypes.RoleUser:
			out = append(out, ProviderMessage{Role: RoleUser, Content: messageText(message.Content)})
		case agtypes.RoleAssistant:
			calls := make([]ToolCall, len(message.ToolCalls))
			for i, call := range message.ToolCalls {
				calls[i] = ToolCall{ID: call.ID, Name: call.Function.Name, Input: call.Function.Arguments}
			}
			out = append(out, ProviderMessage{Role: RoleAssistant, Content: messageText(message.Content), ToolCalls: calls})
		case agtypes.RoleTool:
			out = append(out, ProviderMessage{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: message.ToolCallID, Content: messageText(message.Content), IsError: message.Error != ""}})
		}
	}
	return out
}

func messageText(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	if content == nil {
		return ""
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return fmt.Sprint(content)
	}
	return string(raw)
}

func errorString(isError bool) string {
	if isError {
		return "tool execution failed"
	}
	return ""
}

type eventEmitter struct {
	ctx    context.Context
	writer io.Writer
	sse    *sse.SSEWriter
	events [][]byte
}

func (e *eventEmitter) emit(event events.Event) error {
	if err := e.sse.WriteEvent(e.ctx, e.writer, event); err != nil {
		return err
	}
	raw, err := event.ToJSON()
	if err != nil {
		return err
	}
	e.events = append(e.events, raw)
	return nil
}
