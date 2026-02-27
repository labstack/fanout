package ai

import (
	"context"
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

const maxIterations = 10

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
	CETail       ClientEventType = "tail"
	CETailEnd    ClientEventType = "tail_end"
)

// TailConfig holds parameters for a live log tailing session.
type TailConfig struct {
	Service   string    `json:"service"`
	Pattern   string    `json:"pattern,omitempty"`
	Severity  string    `json:"severity,omitempty"`
	Namespace string    `json:"namespace,omitempty"`
	Since     time.Time `json:"since"`
}

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

// Run executes the agentic loop for a user message.
// Returns the updated conversation, an optional TailConfig if the tail tool was invoked, and any error.
func (o *Orchestrator) Run(ctx context.Context, conversation []Message, window int, namespace string, send SendFunc) ([]Message, *TailConfig, error) {
	runStart := time.Now()
	systemBlocks := o.buildSystemBlocks(ctx, window, namespace)
	var tailCfg *TailConfig

	defer func() {
		slog.Info("orchestrator complete", "total_ms", time.Since(runStart).Milliseconds())
	}()

	for i := 0; i < maxIterations; i++ {
		// Check for cancellation between iterations
		if ctx.Err() != nil {
			return conversation, tailCfg, ctx.Err()
		}

		var textBuf strings.Builder
		var toolCalls []ToolCall
		var stopReason string
		var hadError bool

		tools := append(o.tools.Defs(), respondToolDef())

		llmStart := time.Now()
		err := o.provider.Stream(ctx, StreamParams{
			SystemBlocks: systemBlocks,
			Messages:     conversation,
			Tools:        tools,
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
		slog.Info("llm stream complete", "iteration", i, "llm_ms", llmMs, "stop", stopReason, "tools", toolNames)

		if err != nil {
			slog.Error("provider stream error", "err", err, "iteration", i)
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
			return conversation, tailCfg, err
		}

		// If the provider emitted an error event (e.g. premature stream end),
		// don't send CEDone — the client already received CEError.
		if hadError {
			return conversation, tailCfg, fmt.Errorf("provider emitted error event (already sent to client)")
		}

		// Record assistant message
		conversation = append(conversation, AssistantMessage(textBuf.String(), toolCalls))

		// If the LLM didn't request tool use, we're done (graceful fallback)
		if stopReason != "tool_use" {
			doneEvt := ClientEvent{Type: CEDone, ID: fmt.Sprintf("r-%d", time.Now().UnixMilli())}
			if text := textBuf.String(); text != "" {
				doneEvt.Blocks = []Block{MakeTextBlock(text)}
			}
			if err := send(doneEvt); err != nil {
				return conversation, tailCfg, err
			}
			conversation = compactToolResults(conversation)
			return conversation, tailCfg, nil
		}

		// Check for respond tool call
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
				return conversation, tailCfg, err
			}

			// If the LLM called both respond and real tools, execute the real
			// tools first so their results are in the conversation history.
			if len(realToolCalls) > 0 {
				type toolExecResult struct {
					tc      ToolCall
					result  string
					isError bool
				}
				sideResults := make([]toolExecResult, len(realToolCalls))
				var sideWg sync.WaitGroup
				for si, stc := range realToolCalls {
					sideResults[si].tc = stc
					sideWg.Add(1)
					go func(idx int, tc ToolCall) {
						defer sideWg.Done()
						defer func() {
							if r := recover(); r != nil {
								slog.Error("tool execution panicked", "tool", tc.Name, "panic", r)
								sideResults[idx].result = fmt.Sprintf(`{"error": "internal error executing %s"}`, tc.Name)
								sideResults[idx].isError = true
							}
						}()
						result, err := o.tools.Execute(ctx, tc.Name, json.RawMessage(tc.Input))
						if err != nil {
							sideResults[idx].result = fmt.Sprintf(`{"error": %q}`, err.Error())
							sideResults[idx].isError = true
						} else {
							sideResults[idx].result = result
						}
					}(si, stc)
				}
				sideWg.Wait()
				if ctx.Err() != nil {
					return conversation, tailCfg, ctx.Err()
				}
				for _, r := range sideResults {
					if r.tc.Name == "tail" && !r.isError {
						var tailResult struct {
							Tail *TailConfig `json:"tail"`
						}
						if err := json.Unmarshal([]byte(r.result), &tailResult); err != nil {
							slog.Warn("failed to unmarshal tail config", "err", err)
						} else if tailResult.Tail != nil {
							tailCfg = tailResult.Tail
							if tailCfg.Namespace == "" {
								tailCfg.Namespace = namespace
							}
						}
					}
					if sendErr := send(ClientEvent{Type: CEToolResult, Name: r.tc.Name}); sendErr != nil {
						return conversation, tailCfg, sendErr
					}
					conversation = append(conversation, ToolMessage(r.tc.ID, truncateResult(r.result, 8192), r.isError))
				}
			}

			// Add a synthetic tool_result so the conversation stays valid
			// for subsequent turns (Anthropic requires tool_result after tool_use).
			conversation = append(conversation, ToolMessage(respondCall.ID, `{"ok":true}`, false))

			// Parse structured response
			var resp struct {
				Text   string  `json:"text"`
				Blocks []Block `json:"blocks"`
			}
			if err := json.Unmarshal([]byte(respondCall.Input), &resp); err != nil {
				slog.Error("failed to parse respond tool input", "err", err, "input_preview", truncateJSON(respondCall.Input, 500))
				// Fallback: use streamed text
				text := textBuf.String()
				if text == "" {
					text = "I encountered an error formatting my response."
				}
				doneEvt := ClientEvent{
					Type:   CEDone,
					ID:     fmt.Sprintf("r-%d", time.Now().UnixMilli()),
					Blocks: []Block{MakeTextBlock(text)},
				}
				if err := send(doneEvt); err != nil {
					return conversation, tailCfg, err
				}
				return conversation, tailCfg, nil
			}

			// Validate blocks
			blocks := validateBlocks(resp.Blocks)

			// Prepend text as a TextBlock
			if resp.Text != "" {
				blocks = append([]Block{MakeTextBlock(resp.Text)}, blocks...)
			}

			doneEvt := ClientEvent{
				Type:   CEDone,
				ID:     fmt.Sprintf("r-%d", time.Now().UnixMilli()),
				Blocks: blocks,
			}
			if err := send(doneEvt); err != nil {
				return conversation, tailCfg, err
			}
			conversation = compactToolResults(conversation)
			return conversation, tailCfg, nil
		}

		// Otherwise execute real tool calls (existing logic)
		toolCalls = realToolCalls

		// Execute tool calls in parallel
		type toolExecResult struct {
			tc       ToolCall
			result   string
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
				result, err := o.tools.Execute(ctx, tc.Name, json.RawMessage(tc.Input))
				results[idx].duration = time.Since(t0)
				if err != nil {
					results[idx].result = fmt.Sprintf(`{"error": %q}`, err.Error())
					results[idx].isError = true
				} else {
					results[idx].result = result
				}
			}(i, tc)
		}
		wg.Wait()

		// Log tool execution timing
		for _, r := range results {
			slog.Info("tool executed", "tool", r.tc.Name, "ms", r.duration.Milliseconds(), "error", r.isError, "result_bytes", len(r.result))
		}
		slog.Info("tools batch complete", "iteration", i, "count", len(results), "wall_ms", time.Since(toolsStart).Milliseconds())

		if ctx.Err() != nil {
			return conversation, tailCfg, ctx.Err()
		}

		// Send results to WebSocket sequentially (preserves order)
		for idx := range results {
			r := &results[idx]

			// Special handling for tail tool: extract TailConfig
			if r.tc.Name == "tail" && !r.isError {
				var tailResult struct {
					Tail *TailConfig `json:"tail"`
				}
				if err := json.Unmarshal([]byte(r.result), &tailResult); err != nil {
					slog.Warn("failed to unmarshal tail config", "err", err)
				} else if tailResult.Tail != nil {
					tailCfg = tailResult.Tail
					if tailCfg.Namespace == "" {
						tailCfg.Namespace = namespace
					}
					// Since is set by the tail tool from the latest log timestamp.
					// If zero (no initial logs), runTail applies a lookback.
				}
			}

			// Always send tool_result to clear the spinner
			if sendErr := send(ClientEvent{Type: CEToolResult, Name: r.tc.Name}); sendErr != nil {
				return conversation, tailCfg, sendErr
			}

			// Add tool result to conversation
			conversation = append(conversation, ToolMessage(r.tc.ID, truncateResult(r.result, 8192), r.isError))
		}

		// Compact older tool results before next iteration to reduce tokens
		conversation = compactToolResults(conversation)
	}

	slog.Warn("orchestrator hit max iterations", "max", maxIterations)
	maxIterMsg := "Reached maximum tool iterations. Please refine your question for more details."
	if err := send(ClientEvent{Type: CEToken, Content: maxIterMsg}); err != nil {
		return conversation, tailCfg, err
	}
	doneEvt := ClientEvent{
		Type:   CEDone,
		ID:     fmt.Sprintf("r-%d", time.Now().UnixMilli()),
		Blocks: []Block{MakeTextBlock(maxIterMsg)},
	}
	if err := send(doneEvt); err != nil {
		return conversation, tailCfg, err
	}
	conversation = compactToolResults(conversation)
	return conversation, tailCfg, nil
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

// staticSystemPrompt contains the cacheable portion of the system prompt.
// Keep this as a const so it can be used as a single cache-friendly block.
const staticSystemPrompt = `You are the AI assistant for Fanout, an observability platform.
You help users understand system health, investigate issues, and analyze telemetry data.

## Tools

Investigation tools (use these to gather data):
status (start here) → diagnose (deep-dive) → find (search spans/logs) →
tail (live log streaming) → trace (full trace, needs trace_id) → timeline (trends) →
topology (dependency map) → compare (side-by-side) → metrics (explore metrics) →
query (custom SQL, last resort).

## Response

After gathering data, call the respond tool with:
- text: Markdown analysis. Be direct, cite specific numbers, explain root causes with next steps.
- blocks: Visualization blocks from the types below.

## Block Types

- metrics       — 2-6 KPI summary cards (throughput, latency, error rate)
- table         — tabular data, top errors, search results, comparisons
- timeseries    — trends over time (latency, throughput, error rate over time)
- bar           — ranked comparisons (top endpoints, slowest operations)
- heatmap       — latency distributions over time buckets
- trace_waterfall — single distributed trace visualization
- topology      — service dependency graph with health
- flame_graph   — aggregated span breakdowns
- sankey        — request flow between services
- dep_matrix    — NxN service health grid
- endpoints     — per-endpoint performance breakdown
- correlation   — multi-signal correlation (latency vs errors vs throughput)
- tail          — log entries

## Rules

- Block data MUST come from tool results. Never fabricate data points.
- Prefer visualization over text. Don't describe data the user can see in charts.
- 1-3 blocks per response. More is clutter.
- Most specific type wins: endpoints > table for endpoint data, trace_waterfall > table for traces.
- Do not include a text block in the blocks array — use the text field instead.`

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
