package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	provider  Provider
	tools     *ToolRegistry
	svc       *service.Service
	cfg       config.Config
	sanitizer *bluemonday.Policy

	// Cached services list (refreshed every 60s)
	servicesMu    sync.RWMutex
	servicesList  []string
	servicesStale time.Time
}

// NewOrchestrator creates an orchestrator with the given provider and tools.
func NewOrchestrator(provider Provider, tools *ToolRegistry, svc *service.Service, cfg config.Config) *Orchestrator {
	p := bluemonday.UGCPolicy()
	// Allow Shoelace custom elements
	p.AllowElements("sl-card", "sl-badge", "sl-tag", "sl-icon", "sl-progress-bar",
		"sl-spinner", "sl-tooltip", "sl-alert", "sl-button", "sl-divider",
		"sl-details", "sl-tab-group", "sl-tab", "sl-tab-panel")
	// Allow style and data attributes (needed for CSS vars and Vega specs)
	p.AllowAttrs("style").Globally()
	p.AllowDataAttributes()
	p.AllowAttrs("class").Globally()
	p.AllowAttrs("slot").Globally()
	p.AllowAttrs("variant", "size", "pill", "name", "label", "value", "open", "closable").Globally()
	// Allow SVG for inline charts
	p.AllowElements("svg", "path", "line", "rect", "circle", "text", "g", "defs",
		"linearGradient", "stop", "polyline", "polygon")
	p.AllowAttrs("viewBox", "xmlns", "fill", "stroke", "stroke-width", "d", "x", "y",
		"x1", "y1", "x2", "y2", "cx", "cy", "r", "rx", "ry", "width", "height",
		"transform", "text-anchor", "font-size", "opacity", "points",
		"offset", "stop-color", "id", "gradientUnits").Globally()

	return &Orchestrator{
		provider:  provider,
		tools:     tools,
		svc:       svc,
		cfg:       cfg,
		sanitizer: p,
	}
}

// ClientEvent type constants.
const (
	CEToken      = "token"
	CEToolCall   = "tool_call"
	CEToolResult = "tool_result"
	CECard       = "card"
	CEError      = "error"
	CEDone       = "done"
)

// ClientEvent is sent from the orchestrator to the WebSocket client.
type ClientEvent struct {
	Type    string `json:"type"`              // CEToken, CEToolCall, CEToolResult, CECard, CEError, CEDone
	Content string `json:"content,omitempty"` // text content
	Name    string `json:"name,omitempty"`    // tool name
	Input   string `json:"input,omitempty"`   // tool input (for display)
	HTML    string `json:"html,omitempty"`    // sanitized HTML for cards
	Error   string `json:"error,omitempty"`   // error message
	ID      string `json:"id,omitempty"`      // response ID
}

// SendFunc writes a client event to the WebSocket.
type SendFunc func(event ClientEvent) error

// Run executes the agentic loop for a user message.
// Returns the updated conversation (with assistant/tool messages appended) and any error.
func (o *Orchestrator) Run(ctx context.Context, conversation []Message, window int, namespace string, send SendFunc) ([]Message, error) {
	system := o.buildSystemPrompt(ctx, window, namespace)

	for i := 0; i < maxIterations; i++ {
		// Check for cancellation between iterations
		if ctx.Err() != nil {
			return conversation, ctx.Err()
		}

		var textBuf strings.Builder
		var toolCalls []ToolCall
		var stopReason string

		err := o.provider.Stream(ctx, StreamParams{
			System:    system,
			Messages:  conversation,
			Tools:     o.tools.Defs(),
			MaxTokens: 4096,
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
				return send(ClientEvent{Type: CEError, Error: event.Error})

			default:
				slog.Warn("unknown stream event type", "type", event.Type)
			}
			return nil
		})

		if err != nil {
			slog.Error("provider stream error", "err", err, "iteration", i)
			_ = send(ClientEvent{Type: CEError, Error: "LLM request failed"})
			return conversation, err
		}

		// Record assistant message
		assistantMsg := Message{
			Role:      RoleAssistant,
			Content:   textBuf.String(),
			ToolCalls: toolCalls,
		}
		conversation = append(conversation, assistantMsg)

		// If no tool calls, we're done
		if stopReason == "end_turn" || len(toolCalls) == 0 {
			_ = send(ClientEvent{Type: CEDone, ID: fmt.Sprintf("r-%d", time.Now().UnixMilli())})
			return conversation, nil
		}

		// Execute tool calls
		for _, tc := range toolCalls {
			// Check for cancellation between tool calls
			if ctx.Err() != nil {
				return conversation, ctx.Err()
			}

			result, err := o.tools.Execute(ctx, tc.Name, json.RawMessage(tc.Input))
			isError := err != nil
			if isError {
				result = fmt.Sprintf(`{"error": %q}`, err.Error())
			}

			// Special handling for render tool: sanitize HTML and send as card
			if tc.Name == "render" && !isError {
				sanitized := o.sanitizer.Sanitize(result)
				if sendErr := send(ClientEvent{Type: CECard, HTML: sanitized}); sendErr != nil {
					slog.Warn("send card failed", "err", sendErr)
				}
				result = `{"rendered": true}`
			} else {
				if sendErr := send(ClientEvent{Type: CEToolResult, Name: tc.Name}); sendErr != nil {
					slog.Warn("send tool_result failed", "err", sendErr)
				}
			}

			// Add tool result to conversation
			conversation = append(conversation, Message{
				Role: RoleTool,
				ToolResult: &ToolResult{
					ToolCallID: tc.ID,
					Content:    truncateResult(result, 30000),
					IsError:    isError,
				},
			})
		}

		// Loop back for next LLM call with tool results
	}

	slog.Warn("orchestrator hit max iterations", "max", maxIterations)
	_ = send(ClientEvent{Type: CEDone, ID: fmt.Sprintf("r-%d", time.Now().UnixMilli())})
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
		slog.Warn("failed to refresh services cache", "err", err)
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

func (o *Orchestrator) buildSystemPrompt(ctx context.Context, window int, namespace string) string {
	services := o.cachedServices(ctx)

	var sb strings.Builder
	sb.WriteString("You are the AI assistant for Fanout, an observability platform. ")
	sb.WriteString("You help users understand their system's health, investigate issues, and analyze telemetry data.\n\n")

	// Context
	sb.WriteString("## Context\n")
	sb.WriteString(fmt.Sprintf("- Current time: %s UTC\n", time.Now().UTC().Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("- Time window: last %d minutes\n", window))
	if namespace != "" {
		sb.WriteString(fmt.Sprintf("- Namespace: %s\n", namespace))
	}
	if len(services) > 0 {
		sb.WriteString(fmt.Sprintf("- Active services: %s\n", strings.Join(services, ", ")))
	}
	sb.WriteString("\n")

	// Tool usage
	sb.WriteString("## Tool Usage\n")
	sb.WriteString("- Use `status` first to get system overview\n")
	sb.WriteString("- Use `diagnose` to deep-dive into a specific service\n")
	sb.WriteString("- Use `find` to search spans/logs by pattern\n")
	sb.WriteString("- Use `trace` to inspect a full distributed trace (needs trace_id from find/diagnose)\n")
	sb.WriteString("- Use `timeline` for time-series trends and anomaly detection\n")
	sb.WriteString("- Use `topology` for service dependency maps\n")
	sb.WriteString("- Use `compare` to compare 2-4 services side-by-side\n")
	sb.WriteString("- Use `metrics` to explore available metric names\n")
	sb.WriteString("- Use `query` for custom SQL only when built-in tools aren't sufficient\n")
	sb.WriteString("- Use `render` to display visual HTML cards (charts, tables, grids)\n\n")

	// Design system for render tool
	sb.WriteString("## Design System (for render tool)\n")
	sb.WriteString("When using the `render` tool, generate HTML using these building blocks:\n\n")
	sb.WriteString("**CSS Custom Properties** (work in light and dark mode):\n")
	sb.WriteString("- Colors: `var(--text-primary)`, `var(--text-secondary)`, `var(--text-muted)`\n")
	sb.WriteString("- Backgrounds: `var(--bg-primary)`, `var(--bg-secondary)`, `var(--bg-tertiary)`\n")
	sb.WriteString("- Borders: `var(--border-color)`\n")
	sb.WriteString("- Status: `var(--success)` (#22c55e), `var(--warning)` (#f59e0b), `var(--danger)` (#ef4444)\n")
	sb.WriteString("- Signals: `var(--signal-trace)` (blue), `var(--signal-log)` (amber), `var(--signal-metric)` (green), `var(--signal-error)` (red)\n")
	sb.WriteString("- Typography: `var(--font-sans)`, `var(--font-mono)`\n")
	sb.WriteString("- Radius: `var(--radius)` (0.5rem)\n\n")
	sb.WriteString("**Shoelace Components**: `<sl-card>`, `<sl-badge>`, `<sl-tag>`, `<sl-icon>`, `<sl-progress-bar>`, `<sl-tooltip>`\n\n")
	sb.WriteString("**Layout Patterns**:\n")
	sb.WriteString("- Grid: `<div style=\"display:grid;grid-template-columns:repeat(3,1fr);gap:1rem\">`\n")
	sb.WriteString("- Metric card: `<sl-card><div class=\"metric-value\" style=\"font-size:1.5rem;font-weight:700\">99.9%</div><div class=\"metric-label\" style=\"font-size:0.7rem;color:var(--text-muted);text-transform:uppercase\">Uptime</div></sl-card>`\n")
	sb.WriteString("- Table: `<table class=\"table\"><thead><tr><th>Col</th></tr></thead><tbody>...</tbody></table>`\n\n")
	sb.WriteString("**Vega-Lite Charts**: Embed as `<div class=\"vega-chart\" data-spec='JSON_SPEC_HERE'></div>`\n")
	sb.WriteString("- Use `$schema: https://vega.github.io/schema/vega-lite/v5.json`\n")
	sb.WriteString("- Set `width: \"container\"` and `height: 200` for responsive sizing\n")
	sb.WriteString("- Use dark-aware colors from CSS vars or explicit hex values\n\n")

	// Response style
	sb.WriteString("## Response Style\n")
	sb.WriteString("- Be direct and concise. Lead with the answer.\n")
	sb.WriteString("- Cite specific numbers (e.g., \"P95 latency is 450ms, up from 120ms\").\n")
	sb.WriteString("- Use the `render` tool for visual data: charts, metric grids, comparison tables.\n")
	sb.WriteString("- Don't describe what you're about to render—just render it.\n")
	sb.WriteString("- When investigating issues, explain root cause and suggest actionable next steps.\n")

	return sb.String()
}

// truncateJSON shortens a JSON string for display.
func truncateJSON(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// truncateResult shortens tool results to fit context windows.
func truncateResult(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... [truncated]"
}
