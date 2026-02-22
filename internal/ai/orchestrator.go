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
	"github.com/microcosm-cc/bluemonday"
)

const maxIterations = 10

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

// NewSanitizer creates the shared bluemonday HTML sanitizer policy.
// Used by both the Orchestrator and the UI handler for bookmark sanitization.
func NewSanitizer() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// Allow Shoelace custom elements
	p.AllowElements("sl-card", "sl-badge", "sl-tag", "sl-icon", "sl-progress-bar",
		"sl-spinner", "sl-tooltip", "sl-alert", "sl-button", "sl-divider",
		"sl-details", "sl-tab-group", "sl-tab", "sl-tab-panel")
	// Allow specific CSS properties (not blanket style attr, to prevent UI redressing)
	p.AllowStyles(
		"color", "background", "background-color", "font-size", "font-weight",
		"text-align", "display", "grid-template-columns", "gap", "padding", "margin",
		"border", "border-color", "border-radius", "width", "height", "max-width", "min-width",
		"flex", "flex-direction", "align-items", "justify-content",
		"opacity", "text-transform", "letter-spacing", "line-height",
		"overflow", "white-space", "text-overflow",
		"padding-left", "padding-right", "padding-top", "padding-bottom",
		"margin-left", "margin-right", "margin-top", "margin-bottom",
	).Globally()
	// Allow only the data-* attributes used by viz renderers (not blanket AllowDataAttributes)
	p.AllowAttrs(
		// Container data attributes parsed by V.util.parseData
		"data-graph", "data-timeseries", "data-spans", "data-matrix",
		"data-frames", "data-barchart", "data-heatmap", "data-correlation",
		"data-flow", "data-endpoints",
		// Interactive element indices used by renderer event handlers
		"data-idx", "data-edge-idx", "data-node-id", "data-series",
		"data-ti", "data-bi", "data-link-idx", "data-from", "data-to",
		"data-marker-panel", "data-marker-t",
	).Globally()
	p.AllowAttrs("class").Globally()
	p.AllowAttrs("slot").Globally()
	p.AllowAttrs("variant", "size", "pill", "name", "label", "value", "open", "closable").Globally()
	// Allow SVG for inline charts and viz renderers
	p.AllowElements("svg", "path", "line", "rect", "circle", "text", "g", "defs",
		"linearGradient", "stop", "polyline", "polygon", "marker", "tspan")
	p.AllowAttrs("viewBox", "xmlns", "fill", "stroke", "stroke-width", "d", "x", "y",
		"x1", "y1", "x2", "y2", "cx", "cy", "r", "rx", "ry", "width", "height",
		"transform", "text-anchor", "font-size", "opacity", "points",
		"offset", "stop-color", "id", "gradientUnits",
		"refX", "refY", "markerWidth", "markerHeight", "orient", "marker-end",
		"fill-opacity", "stroke-opacity", "stroke-linecap", "stroke-linejoin",
		"stroke-dasharray", "font-weight", "font-family", "letter-spacing",
		"text-transform", "dominant-baseline", "text-decoration").Globally()
	return p
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
	Type    ClientEventType `json:"type"`              // CEToken, CEToolCall, CEToolResult, CEError, CEDone
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
	systemBlocks := o.buildSystemBlocks(ctx, window, namespace)
	var tailCfg *TailConfig
	var blocks []Block

	for i := 0; i < maxIterations; i++ {
		// Check for cancellation between iterations
		if ctx.Err() != nil {
			return conversation, tailCfg, ctx.Err()
		}

		var textBuf strings.Builder
		var toolCalls []ToolCall
		var stopReason string
		var hadError bool

		err := o.provider.Stream(ctx, StreamParams{
			SystemBlocks: systemBlocks,
			Messages:     conversation,
			Tools:        o.tools.Defs(),
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

		// If the LLM didn't request tool use, we're done
		if stopReason != "tool_use" {
			doneEvt := ClientEvent{Type: CEDone, ID: fmt.Sprintf("r-%d", time.Now().UnixMilli())}
			if text := textBuf.String(); text != "" {
				blocks = append([]Block{MakeTextBlock(text)}, blocks...)
			}
			doneEvt.Blocks = blocks
			if err := send(doneEvt); err != nil {
				return conversation, tailCfg, err
			}
			conversation = compactToolResults(conversation)
			return conversation, tailCfg, nil
		}

		// Execute tool calls in parallel
		type toolExecResult struct {
			tc      ToolCall
			result  string
			isError bool
		}
		results := make([]toolExecResult, len(toolCalls))

		var wg sync.WaitGroup
		for i, tc := range toolCalls {
			results[i].tc = tc
			wg.Add(1)
			go func(idx int, tc ToolCall) {
				defer wg.Done()
				result, toolBlocks, err := o.tools.Execute(ctx, tc.Name, json.RawMessage(tc.Input))
				if err != nil {
					results[idx].result = fmt.Sprintf(`{"error": %q}`, err.Error())
					results[idx].isError = true
				} else {
					results[idx].result = result
					blocks = append(blocks, toolBlocks...)
				}
			}(i, tc)
		}
		wg.Wait()

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
	maxIterMsg := "<p><em>Reached maximum tool iterations. Please refine your question for more details.</em></p>"
	if err := send(ClientEvent{Type: CEToken, Content: maxIterMsg}); err != nil {
		return conversation, tailCfg, err
	}
	blocks = append([]Block{MakeTextBlock(maxIterMsg)}, blocks...)
	if err := send(ClientEvent{Type: CEDone, ID: fmt.Sprintf("r-%d", time.Now().UnixMilli()), Blocks: blocks}); err != nil {
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

Tools: status (start here) → diagnose (deep-dive) → find (search spans/logs) →
tail (live log streaming) → trace (full trace, needs trace_id) → timeline (trends) →
topology (dependency map) → compare (side-by-side) → metrics (explore metrics) →
query (custom SQL, last resort).

Write plain text. Be direct, cite specific numbers, explain root causes with next steps.
Data visualizations are rendered automatically from tool results — do not describe data
that the user can already see in the charts and tables.`

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
