package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
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

const systemPrompt = `You are Fanout's observability assistant. When a view is attached to your reply it is the picture: never draw diagrams, trees, or charts in text, never use code fences to draw boxes, arrows, or trees, and never add a table or list that restates what an attached view already shows; your prose adds only what the view omits. Use get_observability_overview first for broad health questions, get_intelligence_snapshot for the latest precomputed anomalies and recurring log patterns, get_service_topology for direct dependency edges, get_service_dependencies for bounded upstream or downstream reachability from a service, get_service_performance for activity/latency/endpoints/comparisons, inspect_trace for trace or root-cause inspection, and search_logs for log questions. Treat structured outputs as authoritative. You build and change the user's dashboards. Build one whenever the user asks for an overview, asks why something is slow, failing or changing, asks to compare, break down or track telemetry, or asks for anything they would want to look at again; answer a single factual question with a view instead. To build one, read get_telemetry_schema, draft a complete spec of panels that answer the request, run preview_panels, fix every invalid panel, replace or explain every empty one, and only then call create_dashboard. Cover every part of the request: when a part has no data, keep its panel and say why in the panel description instead of dropping it. Title each panel with exactly what it measures. Prefer a few precise panels over many vague ones: headline stats first, then the time series that explain them, then a table of the worst offenders. After saving, reply in two or three sentences with what the dashboard shows and what stands out. Use filter values exactly as the schema lists them. To change a dashboard, get_dashboard first and use edit_dashboard so unrelated panels stay as they are; replace only when the user asks for a redesign. State the time window you used, distinguish missing data from healthy behavior, and never invent services, metrics, or causal claims. Keep answers concise because attached views provide interactive details. Never expose implementation details to the user: do not mention protocol names, tool names, schemas, query IDs, data-source names, storage engines, providers, or internal execution steps. Refer to attached interactive content simply as a view.` + dashboardAnalysisGuidance

const dashboardAnalysisGuidance = ` For analysis dashboards, use only the types the question needs; do not fill a dashboard with all of them. Use heatmap for latency changes, histogram for distributions, scatter for relationships, state_timeline for threshold states, logs for events, log_patterns for repeated messages, traces for slow or erroring traces, service_map for dependencies, and health for service health. Use drill for span or log evidence; keep checked filters. Include annotations for change investigations; deploys and detector findings do not prove causes. Use a deploy split for scoped before/since comparisons; retain missing-deploy explanations. A definition, explanation, or single fact is an answer intent; do not create or replace a dashboard for it. Preserve every requested facet and explain absent telemetry without inventing it. In replies, never name schema fields to the user.`

// Error categories used to pick a client-safe RUN_ERROR message; the raw
// error (which can include provider response bodies) stays server-side.
var (
	errProvider  = errors.New("model provider error")
	errStepLimit = errors.New("agent step limit exceeded")
)

// maxOutputTokens leaves room for a complete dashboard spec in one tool call
// and for the reasoning tokens that gpt-5.x and later count as output.
const maxOutputTokens = 32000

// toolExecutor is the tool surface the runtime needs; *ToolRegistry implements it.
type toolExecutor interface {
	Definitions() []ToolDef
	Execute(context.Context, ToolCall) (ToolExecution, error)
}

type Runtime struct {
	provider   Provider
	tools      toolExecutor
	store      *Store
	maxSteps   int
	runTimeout time.Duration
}

func NewRuntime(provider Provider, tools toolExecutor, store *Store) *Runtime {
	return &Runtime{provider: provider, tools: tools, store: store, maxSteps: 16, runTimeout: 5 * time.Minute}
}

func (r *Runtime) Register(group *echo.Group) {
	group.POST("/runs", r.Run)
	group.GET("/threads", r.ListThreads)
	group.GET("/threads/:threadID", r.GetThread)
	group.PATCH("/threads/:threadID", r.RenameThread)
	group.DELETE("/threads/:threadID", r.DeleteThread)
}

type threadCursor struct {
	UpdatedAt string `json:"updatedAt"`
	ThreadID  string `json:"threadId"`
}

func (r *Runtime) ListThreads(c *echo.Context) error {
	ownerID, ownerErr := api.RequestOwner(c)
	if ownerErr != nil {
		return ownerErr
	}
	limit := 30
	if raw := strings.TrimSpace(c.QueryParam("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return echo.NewHTTPError(http.StatusBadRequest, "limit must be between 1 and 100")
		}
		limit = parsed
	}
	query := strings.TrimSpace(c.QueryParam("q"))
	if len([]rune(query)) > 200 {
		return echo.NewHTTPError(http.StatusBadRequest, "search query is too long")
	}
	var cursor threadCursor
	if raw := strings.TrimSpace(c.QueryParam("cursor")); raw != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.UpdatedAt == "" || cursor.ThreadID == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid thread cursor")
		}
	}
	items, err := r.store.Threads(c.Request().Context(), ownerID, ThreadListOptions{
		Query:         query,
		Limit:         limit + 1,
		BeforeUpdated: cursor.UpdatedAt,
		BeforeID:      cursor.ThreadID,
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to list threads").Wrap(err)
	}
	nextCursor := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		encoded, err := json.Marshal(threadCursor{UpdatedAt: last.Updated, ThreadID: last.ThreadID})
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to paginate threads").Wrap(err)
		}
		nextCursor = base64.RawURLEncoding.EncodeToString(encoded)
	}
	return c.JSON(http.StatusOK, map[string]any{"threads": items, "nextCursor": nextCursor})
}

func (r *Runtime) GetThread(c *echo.Context) error {
	ownerID, ownerErr := api.RequestOwner(c)
	if ownerErr != nil {
		return ownerErr
	}
	thread, err := r.store.Thread(c.Request().Context(), ownerID, c.Param("threadID"))
	if errors.Is(err, ErrThreadNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "thread not found")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to load thread").Wrap(err)
	}
	return c.JSON(http.StatusOK, thread)
}

func (r *Runtime) RenameThread(c *echo.Context) error {
	ownerID, ownerErr := api.RequestOwner(c)
	if ownerErr != nil {
		return ownerErr
	}
	var input struct {
		Title string `json:"title"`
	}
	if err := c.Bind(&input); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid thread title")
	}
	title := strings.TrimSpace(input.Title)
	if title == "" || len([]rune(title)) > 120 {
		return echo.NewHTTPError(http.StatusBadRequest, "thread title must be between 1 and 120 characters")
	}
	if err := r.store.RenameThread(c.Request().Context(), ownerID, c.Param("threadID"), title); errors.Is(err, ErrThreadNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "thread not found")
	} else if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to rename thread").Wrap(err)
	}
	return c.JSON(http.StatusOK, map[string]string{"title": title})
}

func (r *Runtime) DeleteThread(c *echo.Context) error {
	ownerID, ownerErr := api.RequestOwner(c)
	if ownerErr != nil {
		return ownerErr
	}
	if err := r.store.DeleteThread(c.Request().Context(), ownerID, c.Param("threadID")); errors.Is(err, ErrThreadNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "thread not found")
	} else if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to delete thread").Wrap(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (r *Runtime) Run(c *echo.Context) error {
	ownerID, ownerErr := api.RequestOwner(c)
	if ownerErr != nil {
		return ownerErr
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
	// The seed is the stored thread plus this request's new user turn, not the
	// history the browser posted: the server owns the record, so earlier runs'
	// attached views survive and a request cannot rewrite what came before.
	seed, err := r.store.StartRun(c.Request().Context(), ownerID, input)
	if err != nil {
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
	messages := append([]agtypes.Message(nil), seed...)
	runCtx := dashboard.WithOwner(c.Request().Context(), ownerID)
	truncated, runErr := r.execute(runCtx, input.ThreadID, input.RunID, &messages, emitter)
	if runErr != nil {
		slog.Error("agent run failed", "thread_id", input.ThreadID, "run_id", input.RunID, "err", clientErrorMessage(runErr))
	}

	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(c.Request().Context()), 5*time.Second)
	defer cancel()
	if err := r.store.FinishRun(persistCtx, ownerID, input.ThreadID, input.RunID, messages, emitter.events, truncated, runErr); err != nil {
		// A persist failure after a successful stream silently loses the conversation.
		slog.Error("agent run persist failed", "thread_id", input.ThreadID, "run_id", input.RunID, "err", err)
	}
	// The SSE stream already carried the outcome (RUN_FINISHED or RUN_ERROR).
	return nil
}

func (r *Runtime) execute(ctx context.Context, threadID, runID string, messages *[]agtypes.Message, emitter *eventEmitter) (bool, error) {
	type appView struct {
		id    string
		kind  string
		index int
	}
	seenAppViews := map[string]appView{}
	caller := ctx
	timeout := r.runTimeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	truncated := false
	deadlineError := func(err error) error {
		if caller.Err() == nil && ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("%w: 5-minute time limit reached", errStepLimit)
		}
		return err
	}
	if err := emitter.emit(events.NewRunStartedEvent(threadID, runID)); err != nil {
		return truncated, err
	}
	conversation := providerMessages(*messages)
	for step := 0; step < r.maxSteps; step++ {
		if ctx.Err() != nil {
			return truncated, r.fail(threadID, runID, deadlineError(ctx.Err()), emitter)
		}
		messageID, err := appid.New()
		if err != nil {
			return truncated, err
		}
		var text strings.Builder
		var toolCalls []ToolCall
		var stopReason string
		var providerItems []json.RawMessage
		var usage *TokenUsage
		textStarted := false
		appendText := func(delta string) error {
			if delta == "" {
				return nil
			}
			if !textStarted {
				if err := emitter.emit(events.NewTextMessageStartEvent(messageID, events.WithRole("assistant"))); err != nil {
					return err
				}
				textStarted = true
			}
			text.WriteString(delta)
			return emitter.emit(events.NewTextMessageContentEvent(messageID, delta))
		}
		streamErr := r.provider.Stream(ctx, StreamParams{System: systemPrompt, Messages: conversation, Tools: r.tools.Definitions(), MaxTokens: maxOutputTokens}, func(event StreamEvent) error {
			// Reported snapshots replace earlier counts for this call. Never use
			// tool text or assistant narration to infer token usage.
			if event.Usage != nil {
				copy := *event.Usage
				usage = &copy
			}
			switch event.Type {
			case EventError:
				return fmt.Errorf("%w: %s", errProvider, event.Error)
			case EventText:
				return appendText(event.Delta)
			case EventToolUse:
				if event.ToolCall != nil {
					toolCalls = append(toolCalls, *event.ToolCall)
				}
			case EventStop:
				stopReason = event.StopReason
				providerItems = event.ProviderItems
			}
			return nil
		})
		if ctx.Err() != nil {
			streamErr = ctx.Err()
		}
		provider, model := runtimeUsageIdentity(r.provider)
		status := "completed"
		if streamErr != nil {
			status = "error"
		} else if stoppedAtTokenLimit(stopReason) || stopReason == "incomplete" || stopReason == "content_filter" {
			status = "incomplete"
		}
		var reported any
		if usage != nil {
			reported = map[string]int{"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens, "reasoning_tokens": usage.ReasoningTokens, "cache_read_tokens": usage.CacheReadTokens, "cache_write_tokens": usage.CacheWriteTokens}
		}
		usageEvent := events.NewCustomEvent("model_call_usage", events.WithValue(map[string]any{
			"run_id": runID, "step": step + 1, "provider": provider, "model": model, "status": status, "usage": reported,
		}))
		// Even a disconnected client must leave the authoritative record in
		// the persisted run events. The structured log is the controller's
		// recovery source when SSE could not deliver it.
		if err := emitter.emit(usageEvent); err != nil {
			if raw, marshalErr := usageEvent.ToJSON(); marshalErr == nil {
				emitter.events = append(emitter.events, raw)
			}
			if streamErr == nil {
				streamErr = err
			}
		}
		if streamErr == nil {
			logFields := []any{"thread_id", threadID, "run_id", runID, "step", step + 1, "provider", provider, "model", model, "status", status, "stop_reason", stopReason}
			if usage != nil {
				logFields = append(logFields, "input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens, "reasoning_tokens", usage.ReasoningTokens, "cache_read_tokens", usage.CacheReadTokens, "cache_write_tokens", usage.CacheWriteTokens)
			}
			if stoppedAtTokenLimit(stopReason) || stopReason == "incomplete" || stopReason == "content_filter" {
				truncated = true
				toolCalls = nil
				slog.Warn("agent response truncated", logFields...)
				notice := "The response was cut off before it finished."
				if textStarted {
					if stoppedAtTokenLimit(stopReason) {
						notice = "\n\n[Response truncated: output limit reached.]"
					} else {
						notice = "\n\n" + notice
					}
				}
				streamErr = appendText(notice)
			} else {
				slog.Info("llm stream complete", logFields...)
				if stopReason == "refusal" && !textStarted {
					streamErr = appendText("The model refused to answer this request.")
				}
			}
		}
		if streamErr != nil {
			slog.Info("llm stream failed", "thread_id", threadID, "run_id", runID, "step", step+1, "provider", provider, "model", model, "status", status, "usage", reported)
		}
		if textStarted {
			if err := emitter.emit(events.NewTextMessageEndEvent(messageID)); err != nil && streamErr == nil {
				streamErr = err
			}
		}
		if streamErr != nil {
			return truncated, r.fail(threadID, runID, deadlineError(streamErr), emitter)
		}

		agCalls := make([]agtypes.ToolCall, len(toolCalls))
		for i, call := range toolCalls {
			agCalls[i] = agtypes.ToolCall{ID: call.ID, Type: agtypes.ToolCallTypeFunction, Function: agtypes.FunctionCall{Name: call.Name, Arguments: call.Input}}
		}
		if text.Len() > 0 || len(agCalls) > 0 {
			*messages = append(*messages, agtypes.Message{ID: messageID, Role: agtypes.RoleAssistant, Content: text.String(), ToolCalls: agCalls})
		}
		// Opaque reasoning belongs to this run's provider conversation, not
		// the persisted AG-UI history or client events.
		if text.Len() > 0 || len(toolCalls) > 0 || len(providerItems) > 0 {
			conversation = append(conversation, ProviderMessage{Role: RoleAssistant, Content: text.String(), ToolCalls: toolCalls, ProviderItems: providerItems})
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
			if ctx.Err() != nil {
				return truncated, r.fail(threadID, runID, deadlineError(ctx.Err()), emitter)
			}
			if err != nil {
				// Relayed to the model as a tool error; log it so repeated
				// tool-transport failures are findable server-side.
				slog.Warn("agent tool execution failed", "thread_id", threadID, "run_id", runID, "tool", call.Name, "err", err)
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
			if execution.AppResourceURI != "" && !execution.IsError {
				view := fragmentView(execution.Structured)
				previous, seen := seenAppViews[view.Key]
				if view.Key != "" && seen && previous.kind == "preset" && view.Kind != "preset" {
					continue
				}
				activityID := previous.id
				if !seen || view.Key == "" {
					var err error
					activityID, err = appid.New()
					if err != nil {
						return truncated, err
					}
				}
				content := map[string]any{"resource_uri": execution.AppResourceURI, "tool_name": call.Name, "tool_input": json.RawMessage(call.Input), "tool_result": execution.Structured, "is_error": execution.IsError}
				if content["tool_result"] == nil {
					content["tool_result"] = execution.Content
				}
				if err := emitter.emit(events.NewActivitySnapshotEvent(activityID, "mcp-app", content)); err != nil {
					return truncated, err
				}
				message := agtypes.Message{ID: activityID, Role: agtypes.RoleActivity, ActivityType: "mcp-app", Content: content}
				index := len(*messages)
				if seen && view.Key != "" {
					index = previous.index
					(*messages)[index] = message
				} else {
					*messages = append(*messages, message)
				}
				if view.Key != "" {
					seenAppViews[view.Key] = appView{activityID, view.Kind, index}
				}
			}
		}
	}
	return truncated, r.fail(threadID, runID, fmt.Errorf("%w: exceeded %d tool steps", errStepLimit, r.maxSteps), emitter)
}

func runtimeUsageIdentity(provider Provider) (string, string) {
	switch p := provider.(type) {
	case *openAIProvider:
		return "openai", p.model
	case *anthropicProvider:
		return "anthropic", p.model
	case interface{ usageIdentity() (string, string) }:
		return p.usageIdentity()
	default:
		return "unknown", "unknown"
	}
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
		if strings.Contains(err.Error(), "5-minute") {
			return "The agent reached its 5-minute time limit. Try a smaller request."
		}
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
