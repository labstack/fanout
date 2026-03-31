package ai

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/service"
)

const respondToolName = "respond"

// respondToolDef builds the synthetic respond tool definition.
// This tool is NOT registered in the ToolRegistry — it is appended to the
// tools list for the provider call and intercepted by the orchestrator.
func respondToolDef() ToolDef {
	return ToolDef{
		Name:        respondToolName,
		Description: "Produce your final response with markdown text and visualization blocks. Call this as your last action.",
		InputSchema: generateResponseSchema(), // from schema_gen.go
	}
}

// Orchestrator manages the agentic loop: user question → LLM → tools → stream.
type Orchestrator struct {
	provider Provider
	tools    *ToolRegistry
	svc      *service.Service
	cfg      config.Config

	// Cached services list (refreshed every 60s)
	servicesMu    sync.RWMutex
	servicesList  []string
	servicesStale time.Time
}

// NewOrchestrator creates an orchestrator with the given provider and tools.
// Panics if provider or tools are nil.
func NewOrchestrator(provider Provider, tools *ToolRegistry, svc *service.Service, cfg config.Config) *Orchestrator {
	if provider == nil {
		panic("ai: NewOrchestrator called with nil provider")
	}
	if tools == nil {
		panic("ai: NewOrchestrator called with nil tools")
	}

	return &Orchestrator{
		provider: provider,
		tools:    tools,
		svc:      svc,
		cfg:      cfg,
	}
}

// ClientEventType identifies the kind of event sent to the browser.
type ClientEventType string

// ClientEvent type constants.
const (
	CEToken      ClientEventType = "token"
	CEToolCall   ClientEventType = "tool_call"
	CEToolResult ClientEventType = "tool_result"
	CEError      ClientEventType = "error"
	CEDone       ClientEventType = "done"
)

// ClientEvent is sent from the orchestrator to the WebSocket client.
type ClientEvent struct {
	Type    ClientEventType `json:"type"`
	Content string          `json:"content,omitempty"` // text content
	Name    string          `json:"name,omitempty"`    // tool name
	Input   string          `json:"input,omitempty"`   // tool input (for display)
	Error   string          `json:"error,omitempty"`   // error message
	ID      string          `json:"id,omitempty"`      // response ID
	Blocks  []Block         `json:"blocks,omitempty"`  // structured blocks for client rendering
}

// SendFunc writes a client event to the WebSocket.
type SendFunc func(event ClientEvent) error

// stepResult captures the outcome of a single LLM call.
type stepResult struct {
	done            bool // true if response is complete (respond called or text-only)
	err             error
	suggestedBlocks []Block // blocks suggested by tool handlers (for merging into respond)
}

// step executes one LLM call: stream → handle tool calls → execute tools.
// It appends to *conversation so the caller sees updates.
func (o *Orchestrator) step(ctx context.Context, conversation *[]Message,
	systemBlocks []SystemBlock, tools []ToolDef, toolChoice *ToolChoice, stepName string, send SendFunc, namespace string) stepResult {

	if ctx.Err() != nil {
		return stepResult{err: ctx.Err()}
	}

	var textBuf strings.Builder
	var toolCalls []ToolCall
	var stopReason string
	var hadError bool

	llmStart := time.Now()
	err := o.provider.Stream(ctx, StreamParams{
		SystemBlocks: systemBlocks,
		Messages:     *conversation,
		Tools:        tools,
		ToolChoice:   toolChoice,
		MaxTokens:    4096,
	}, func(event StreamEvent) error {
		switch event.Type {
		case EventText:
			textBuf.WriteString(event.Delta)
			return send(ClientEvent{Type: CEToken, Content: event.Delta})

		case EventToolUse:
			if event.ToolCall == nil {
				return fmt.Errorf("EventToolUse with nil ToolCall")
			}
			toolCalls = append(toolCalls, *event.ToolCall)
			return send(ClientEvent{
				Type:  CEToolCall,
				Name:  event.ToolCall.Name,
				Input: truncateJSON(event.ToolCall.Input, 200),
			})

		case EventStop:
			stopReason = event.StopReason
			return nil

		case EventError:
			hadError = true
			return send(ClientEvent{Type: CEError, Error: event.Error})

		default:
			slog.Warn("unknown stream event type", "type", event.Type)
		}
		return nil
	})

	llmMs := time.Since(llmStart).Milliseconds()

	// Log tool names requested by the LLM
	var toolNames []string
	for _, tc := range toolCalls {
		toolNames = append(toolNames, tc.Name)
	}
	slog.Info("llm stream complete", "step", stepName, "llm_ms", llmMs, "stop", stopReason, "tools", toolNames)

	if err != nil {
		slog.Error("provider stream error", "err", err, "step", stepName)
		errMsg := "LLM request failed"
		var apiErr *APIError
		isAPIErr := errors.As(err, &apiErr)
		switch {
		case ctx.Err() != nil:
			errMsg = "Request cancelled"
		case isAPIErr && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403):
			errMsg = "LLM authentication failed — check AI_API_KEY"
		case isAPIErr && apiErr.StatusCode == 429:
			errMsg = "LLM rate limited — please try again shortly"
		case isAPIErr && apiErr.StatusCode >= 500:
			errMsg = "LLM provider error — please try again"
		}
		if sendErr := send(ClientEvent{Type: CEError, Error: errMsg}); sendErr != nil {
			slog.Warn("failed to send error to client", "send_err", sendErr)
		}
		return stepResult{err: err}
	}

	// If the provider emitted an error event (e.g. premature stream end),
	// don't send CEDone — the client already received CEError.
	if hadError {
		return stepResult{err: fmt.Errorf("provider emitted error event (already sent to client)")}
	}

	// Record assistant message
	*conversation = append(*conversation, AssistantMessage(textBuf.String(), toolCalls))

	// If the LLM didn't request tool use, we're done (graceful fallback)
	if stopReason != "tool_use" {
		doneEvt := ClientEvent{Type: CEDone, ID: fmt.Sprintf("r-%d", time.Now().UnixMilli())}
		if text := textBuf.String(); text != "" {
			doneEvt.Blocks = []Block{MakeTextBlock(text)}
		}
		if err := send(doneEvt); err != nil {
			return stepResult{err: err}
		}
		return stepResult{done: true}
	}

	// Separate respond call from real tool calls
	var respondCall *ToolCall
	var realToolCalls []ToolCall
	for i := range toolCalls {
		if toolCalls[i].Name == respondToolName {
			respondCall = &toolCalls[i]
		} else {
			realToolCalls = append(realToolCalls, toolCalls[i])
		}
	}

	if respondCall != nil {
		// Mark the respond tool as complete on the client
		if err := send(ClientEvent{Type: CEToolResult, Name: respondToolName}); err != nil {
			return stepResult{err: err}
		}

		// If the LLM called both respond and real tools, execute the real
		// tools first so their results are in the conversation history.
		var toolSuggestedBlocks []Block
		if len(realToolCalls) > 0 {
			suggested, err := o.executeTools(ctx, realToolCalls, conversation, send, namespace, stepName)
			if err != nil {
				return stepResult{err: err}
			}
			toolSuggestedBlocks = suggested
		}

		// Add a synthetic tool_result so the conversation stays valid
		// for subsequent turns (Anthropic requires tool_result after tool_use).
		*conversation = append(*conversation, ToolMessage(respondCall.ID, `{"ok":true}`, false))

		// Parse structured response; fall back to streamed text on failure
		var blocks []Block
		var resp struct {
			Text   string  `json:"text"`
			Blocks []Block `json:"blocks"`
		}
		if err := json.Unmarshal([]byte(respondCall.Input), &resp); err != nil {
			slog.Error("failed to parse respond tool input", "err", err, "input_preview", truncateJSON(respondCall.Input, 500))
			text := textBuf.String()
			if text == "" {
				text = "I encountered an error formatting my response."
			}
			blocks = []Block{MakeTextBlock(text)}
		} else {
			blocks = validateBlocks(resp.Blocks)
			if resp.Text != "" {
				blocks = append([]Block{MakeTextBlock(resp.Text)}, blocks...)
			}
			// Validate and append tool-suggested blocks
			blocks = append(blocks, validateBlocks(toolSuggestedBlocks)...)
		}

		if err := send(ClientEvent{
			Type:   CEDone,
			ID:     fmt.Sprintf("r-%d", time.Now().UnixMilli()),
			Blocks: blocks,
		}); err != nil {
			return stepResult{err: err}
		}
		return stepResult{done: true}
	}

	// No respond call — execute real tool calls and return (not done yet)
	if len(realToolCalls) == 0 {
		slog.Warn("LLM returned tool_use stop reason but no tool calls", "step", stepName)
		return stepResult{}
	}
	suggested, err := o.executeTools(ctx, realToolCalls, conversation, send, namespace, stepName)
	if err != nil {
		return stepResult{err: err}
	}
	return stepResult{suggestedBlocks: suggested}
}

// executeTools runs tool calls in parallel, sends results to the client,
// and appends tool result messages to the conversation.
// Returns any suggested blocks from tool handlers.
func (o *Orchestrator) executeTools(ctx context.Context, toolCalls []ToolCall,
	conversation *[]Message, send SendFunc, namespace string, stepName string) ([]Block, error) {

	type toolExecResult struct {
		tc       ToolCall
		result   string
		blocks   []Block // suggested blocks from tool
		isError  bool
		duration time.Duration
	}
	results := make([]toolExecResult, len(toolCalls))

	toolsStart := time.Now()
	var wg sync.WaitGroup
	for i, tc := range toolCalls {
		results[i].tc = tc
		wg.Add(1)
		go func(idx int, tc ToolCall) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					slog.Error("tool execution panicked", "tool", tc.Name, "panic", r)
					results[idx].result = fmt.Sprintf(`{"error": "internal error executing %s"}`, tc.Name)
					results[idx].isError = true
				}
			}()
			t0 := time.Now()
			result, blocks, err := o.tools.Execute(ctx, tc.Name, json.RawMessage(tc.Input))
			results[idx].duration = time.Since(t0)
			if err != nil {
				results[idx].result = fmt.Sprintf(`{"error": %q}`, err.Error())
				results[idx].isError = true
			} else {
				results[idx].result = result
				results[idx].blocks = blocks
			}
		}(i, tc)
	}
	wg.Wait()

	// Log tool execution timing
	for _, r := range results {
		level := slog.LevelInfo
		attrs := []slog.Attr{
			slog.String("tool", r.tc.Name),
			slog.Int64("ms", r.duration.Milliseconds()),
			slog.Bool("error", r.isError),
			slog.Int("result_bytes", len(r.result)),
		}
		if r.tc.Name == "query" {
			level = slog.LevelWarn
			attrs = append(attrs, slog.String("hint", "consider a specialized tool"))
			attrs = append(attrs, slog.String("sql", truncateJSON(r.tc.Input, 500)))
		}
		slog.LogAttrs(ctx, level, "tool executed", attrs...)
	}
	slog.Info("tools batch complete", "step", stepName, "count", len(results), "wall_ms", time.Since(toolsStart).Milliseconds())

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Collect suggested blocks from all tools
	var suggestedBlocks []Block
	// Send results to WebSocket sequentially (preserves order)
	for idx := range results {
		r := &results[idx]

		// Always send tool_result to clear the spinner
		if sendErr := send(ClientEvent{Type: CEToolResult, Name: r.tc.Name}); sendErr != nil {
			return nil, sendErr
		}

		// Add tool result to conversation
		*conversation = append(*conversation, ToolMessage(r.tc.ID, truncateResult(r.result, 8192), r.isError))
		suggestedBlocks = append(suggestedBlocks, r.blocks...)
	}

	return suggestedBlocks, nil
}

// Run executes a two-step orchestration for a user message: gather data, then respond.
// Returns the updated conversation and any error.
func (o *Orchestrator) Run(ctx context.Context, conversation []Message, window int, namespace string, send SendFunc) (msgs []Message, retErr error) {
	runStart := time.Now()
	systemBlocks := o.buildSystemBlocks(ctx, window, namespace)

	defer func() {
		msgs = compactToolResults(conversation)
		slog.Info("orchestrator complete", "total_ms", time.Since(runStart).Milliseconds())
	}()

	// Step 1: Gather — all tools available
	allTools := append(o.tools.Defs(), respondToolDef())
	r := o.step(ctx, &conversation, systemBlocks, allTools, nil, "gather", send, namespace)
	if r.done || r.err != nil {
		return conversation, r.err
	}

	// Step 2: Respond — respond tool only.
	// Inject a synthesis directive so the LLM focuses on analyzing
	// the data it already gathered rather than trailing off.
	conversation = append(conversation, UserMessage(
		"Now synthesize the tool results into a complete response. "+
			"Do NOT suggest further investigation — analyze what you have."))

	// Wrap send to merge tool-suggested blocks from the gather step into the
	// final CEDone event. This lets tool handlers influence the response
	// without the LLM needing to build complex data structures.
	gatherBlocks := r.suggestedBlocks
	respondSend := send
	if len(gatherBlocks) > 0 {
		respondSend = func(event ClientEvent) error {
			if event.Type == CEDone {
				event.Blocks = append(event.Blocks, validateBlocks(gatherBlocks)...)
			}
			return send(event)
		}
	}
	r2 := o.step(ctx, &conversation, systemBlocks, []ToolDef{respondToolDef()}, &ToolChoice{Name: respondToolName}, "respond", respondSend, namespace)
	if r2.err != nil {
		return conversation, r2.err
	}

	return conversation, nil
}

// SuggestedQuestions returns contextual starter questions.
func (o *Orchestrator) SuggestedQuestions(ctx context.Context) []string {
	services := o.cachedServices(ctx)

	suggestions := []string{
		"What's the overall system health?",
		"Are there any anomalies in the last hour?",
	}

	if len(services) > 0 {
		suggestions = append(suggestions,
			fmt.Sprintf("How is %s performing?", services[0]),
		)
	}
	if len(services) > 1 {
		suggestions = append(suggestions,
			fmt.Sprintf("Compare %s and %s", services[0], services[1]),
		)
	}

	return suggestions
}

func (o *Orchestrator) cachedServices(ctx context.Context) []string {
	if o.svc == nil {
		return nil
	}

	o.servicesMu.RLock()
	if time.Now().Before(o.servicesStale) {
		defer o.servicesMu.RUnlock()
		return o.servicesList
	}
	o.servicesMu.RUnlock()

	o.servicesMu.Lock()
	defer o.servicesMu.Unlock()

	// Double-check after acquiring write lock
	if time.Now().Before(o.servicesStale) {
		return o.servicesList
	}

	namespaces := o.svc.Namespaces(o.cfg.LakeDir, "")
	ns := ""
	if len(namespaces) > 0 {
		ns = namespaces[0]
	}

	topo, err := o.svc.Topology(ctx, 60, ns, "")
	if err != nil {
		if o.servicesList == nil {
			slog.Error("initial services cache load failed — system prompt will lack service context", "err", err)
		} else {
			slog.Warn("failed to refresh services cache, using stale data", "err", err)
		}
		// Backoff: don't retry for at least 10 seconds on failure
		o.servicesStale = time.Now().Add(10 * time.Second)
		return o.servicesList
	}

	services := make([]string, 0, len(topo.Nodes))
	for _, n := range topo.Nodes {
		services = append(services, n.Name)
	}

	o.servicesList = services
	o.servicesStale = time.Now().Add(60 * time.Second)
	return services
}

//go:embed prompts/observer.md
var staticSystemPrompt string

func (o *Orchestrator) buildSystemBlocks(ctx context.Context, window int, namespace string) []SystemBlock {
	services := o.cachedServices(ctx)

	// Static block — eligible for Anthropic prompt caching
	static := SystemBlock{
		Text:         staticSystemPrompt,
		CacheControl: "ephemeral",
	}

	// Dynamic block — changes every request (time, window, services)
	var sb strings.Builder
	sb.WriteString("## Context\n")
	sb.WriteString(fmt.Sprintf("- Time: %s UTC | Window: %dm", time.Now().UTC().Format("2006-01-02T15:04:05"), window))
	if namespace != "" {
		sb.WriteString(fmt.Sprintf(" | Namespace: %s", namespace))
	}
	if len(services) > 0 {
		sb.WriteString(fmt.Sprintf("\n- Services: %s", strings.Join(services, ", ")))
	}
	sb.WriteByte('\n')

	dynamic := SystemBlock{Text: sb.String()}

	return []SystemBlock{static, dynamic}
}

// truncateJSON shortens a JSON string for display in tool_call events.
func truncateJSON(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(truncated)"
}

// truncateResult shortens tool results to fit context windows.
// Tries to cut at a JSON structure boundary to avoid broken JSON.
func truncateResult(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Walk backwards from the cut point to find a clean JSON boundary
	cutAt := maxLen
	for cutAt > maxLen-200 && cutAt > 0 {
		c := s[cutAt]
		if c == ',' || c == ']' || c == '}' {
			cutAt++ // include the boundary character
			break
		}
		cutAt--
	}
	if cutAt <= 0 || cutAt > maxLen {
		cutAt = maxLen
	}
	return s[:cutAt] + "\n\n[Result truncated. Ask the user to refine their query for more specific data.]"
}

// compactToolResults returns a copy of the conversation with tool results from
// older turns replaced by short summaries. The most recent tool result batch is
// kept intact (the LLM may reference it). The original slice is not mutated.
func compactToolResults(msgs []Message) []Message {
	// Find the index of the last assistant message with tool calls —
	// everything after that is the "recent batch" we preserve.
	lastAssistantWithTools := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == RoleAssistant && len(msgs[i].ToolCalls) > 0 {
			lastAssistantWithTools = i
			break
		}
	}

	out := make([]Message, len(msgs))
	copy(out, msgs)

	for i := range out {
		if i > lastAssistantWithTools {
			break // preserve recent batch
		}
		if out[i].Role == RoleTool && out[i].ToolResult != nil && !out[i].ToolResult.IsError {
			if len(out[i].ToolResult.Content) > 200 {
				// Copy the ToolResult to avoid mutating the original
				tr := *out[i].ToolResult
				tr.Content = summarizeToolResult(tr.Content)
				out[i].ToolResult = &tr
			}
		}
	}
	return out
}

// summarizeToolResult produces a ~150 byte summary of a JSON tool result,
// showing top-level key names (sorted) and array lengths.
func summarizeToolResult(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		// Not valid JSON — just truncate
		if len(s) > 150 {
			return s[:150] + "..."
		}
		return s
	}

	var sb strings.Builder
	sb.WriteString("{")
	switch obj := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for i, k := range keys {
			if i > 0 {
				sb.WriteString(", ")
			}
			switch arr := obj[k].(type) {
			case []any:
				sb.WriteString(fmt.Sprintf("%q: [%d items]", k, len(arr)))
			default:
				sb.WriteString(fmt.Sprintf("%q: ...", k))
			}
			if sb.Len() > 140 {
				sb.WriteString(", ...")
				break
			}
		}
	case []any:
		sb.WriteString(fmt.Sprintf("[%d items]", len(obj)))
	default:
		sb.WriteString("...")
	}
	sb.WriteString("} [compacted]")
	return sb.String()
}
