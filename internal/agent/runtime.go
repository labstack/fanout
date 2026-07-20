package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/api"
)

const systemPrompt = `You are Fanout's observability assistant. Use observability_overview first for broad health questions, service_topology for dependencies, service_performance for activity/latency/endpoints/comparisons, trace_detail for trace or root-cause inspection, and search_logs for log questions. Treat structured outputs as authoritative. State the time window you used, distinguish missing data from healthy behavior, and never invent services, metrics, or causal claims. Keep answers concise because the attached view provides the interactive details. Never expose implementation details to the user: do not mention protocol names, tool names, schemas, query IDs, data-source names, storage engines, providers, or internal execution steps. Refer to attached interactive content simply as a view.`

type Runtime struct {
	provider Provider
	tools    *ToolRegistry
	store    *Store
	maxSteps int
}

func NewRuntime(provider Provider, tools *ToolRegistry, store *Store) *Runtime {
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
		input.ThreadID = uuid.NewString()
	}
	if input.RunID == "" {
		input.RunID = uuid.NewString()
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
	runErr := r.execute(c.Request().Context(), input.ThreadID, input.RunID, &messages, emitter)

	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(c.Request().Context()), 5*time.Second)
	defer cancel()
	if err := r.store.FinishRun(persistCtx, user.ID, input.ThreadID, input.RunID, messages, emitter.events, runErr); err != nil && runErr == nil {
		runErr = err
	}
	if runErr != nil {
		return nil
	}
	return nil
}

func (r *Runtime) execute(ctx context.Context, threadID, runID string, messages *[]agtypes.Message, emitter *eventEmitter) error {
	if err := emitter.emit(events.NewRunStartedEvent(threadID, runID)); err != nil {
		return err
	}
	conversation := providerMessages(*messages)
	for step := 0; step < r.maxSteps; step++ {
		messageID := uuid.NewString()
		var text strings.Builder
		var toolCalls []ToolCall
		textStarted := false
		streamErr := r.provider.Stream(ctx, StreamParams{System: systemPrompt, Messages: conversation, Tools: r.tools.Definitions(), MaxTokens: 4096}, func(event StreamEvent) error {
			if event.Error != "" {
				return errors.New(event.Error)
			}
			if event.Delta != "" {
				if !textStarted {
					if err := emitter.emit(events.NewTextMessageStartEvent(messageID, events.WithRole("assistant"))); err != nil {
						return err
					}
					textStarted = true
				}
				text.WriteString(event.Delta)
				return emitter.emit(events.NewTextMessageContentEvent(messageID, event.Delta))
			}
			if event.ToolCall != nil {
				toolCalls = append(toolCalls, *event.ToolCall)
			}
			return nil
		})
		if textStarted {
			if err := emitter.emit(events.NewTextMessageEndEvent(messageID)); err != nil && streamErr == nil {
				streamErr = err
			}
		}
		if streamErr != nil {
			return r.fail(runID, streamErr, emitter)
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
				return err
			}
			return nil
		}

		for _, call := range toolCalls {
			if err := emitter.emit(events.NewToolCallStartEvent(call.ID, call.Name, events.WithParentMessageID(messageID))); err != nil {
				return err
			}
			if err := emitter.emit(events.NewToolCallArgsEvent(call.ID, call.Input)); err != nil {
				return err
			}
			if err := emitter.emit(events.NewToolCallEndEvent(call.ID)); err != nil {
				return err
			}
			execution, err := r.tools.Execute(ctx, call)
			if err != nil {
				execution = ToolExecution{Content: fmt.Sprintf(`{"error":%q}`, err.Error()), IsError: true}
			}
			toolMessageID := uuid.NewString()
			if err := emitter.emit(events.NewToolCallResultEvent(toolMessageID, call.ID, execution.Content)); err != nil {
				return err
			}
			*messages = append(*messages, agtypes.Message{ID: toolMessageID, Role: agtypes.RoleTool, Content: execution.Content, ToolCallID: call.ID, Error: errorString(execution.IsError)})
			conversation = append(conversation, ProviderMessage{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: call.ID, Content: execution.Content, IsError: execution.IsError}})
			if execution.AppResourceURI != "" {
				activityID := uuid.NewString()
				content := map[string]any{"resourceUri": execution.AppResourceURI, "toolName": call.Name, "toolInput": json.RawMessage(call.Input), "toolResult": execution.Structured, "isError": execution.IsError}
				if content["toolResult"] == nil {
					content["toolResult"] = execution.Content
				}
				if err := emitter.emit(events.NewActivitySnapshotEvent(activityID, "mcp-app", content)); err != nil {
					return err
				}
				*messages = append(*messages, agtypes.Message{ID: activityID, Role: agtypes.RoleActivity, ActivityType: "mcp-app", Content: content})
			}
		}
	}
	return r.fail(runID, fmt.Errorf("agent exceeded %d tool steps", r.maxSteps), emitter)
}

func (r *Runtime) fail(runID string, err error, emitter *eventEmitter) error {
	_ = emitter.emit(events.NewRunErrorEvent(err.Error(), events.WithRunID(runID)))
	return err
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
