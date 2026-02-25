package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolHandler executes a tool and returns JSON result text.
type ToolHandler func(ctx context.Context, input json.RawMessage) (string, error)

// ToolRegistry maps tool names to definitions and handlers.
type ToolRegistry struct {
	session  *mcp.ClientSession
	defs     []ToolDef
	handlers map[string]ToolHandler
}

// NewToolRegistry creates the registry by connecting to the MCP server
// in-process and importing its tool definitions, then registering AI-only
// tools (metrics, tail) directly.
func NewToolRegistry(ctx context.Context, mcpServer *mcp.Server, svc *service.Service, cfg config.Config) (*ToolRegistry, error) {
	// Create in-memory transport pair
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	// Connect server side in a goroutine first — per the MCP SDK, servers
	// must be connected before clients, as the client initializes the session.
	// The goroutine ensures the server is reading before client.Connect below.
	serverErr := make(chan error, 1)
	go func() {
		_, err := mcpServer.Connect(ctx, serverTransport, nil)
		serverErr <- err
	}()

	// Connect client side (blocks until handshake completes)
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "fanout-ai",
		Version: "1.0.0",
	}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		return nil, fmt.Errorf("MCP in-memory client connect: %w", err)
	}
	if err := <-serverErr; err != nil {
		session.Close()
		return nil, fmt.Errorf("MCP in-memory server connect: %w", err)
	}

	// List tools from MCP server and convert to ToolDefs
	r := &ToolRegistry{
		session:  session,
		handlers: make(map[string]ToolHandler),
	}

	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("MCP ListTools: %w", err)
	}
	for _, t := range toolsResult.Tools {
		r.defs = append(r.defs, ToolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}

	// Register AI-only tools: metrics, tail

	r.register(ToolDef{
		Name:        "metrics",
		Description: "Explore available metrics: search by name, filter by service/type. Returns aggregated summaries with sparkline data.",
		InputSchema: jsonSchema(map[string]property{
			"name":      {Type: "string", Desc: "Metric name or pattern (supports wildcards like http.*)"},
			"service":   {Type: "string", Desc: "Filter by service name"},
			"type":      {Type: "string", Desc: "Filter by metric type: gauge, counter, histogram"},
			"window":    {Type: "integer", Desc: "Time window in minutes (default 60)"},
			"namespace": {Type: "string", Desc: "Namespace filter (optional)"},
		}),
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		var p struct {
			Name      string `json:"name"`
			Service   string `json:"service"`
			Type      string `json:"type"`
			Window    int    `json:"window"`
			Namespace string `json:"namespace"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if p.Window == 0 {
			p.Window = 60
		}
		var names []string
		if p.Name != "" {
			names = []string{p.Name}
		}
		var services []string
		if p.Service != "" {
			services = []string{p.Service}
		}
		var types []string
		if p.Type != "" {
			types = []string{p.Type}
		}
		res, err := svc.Metrics(ctx, service.MetricsParams{
			Names:     names,
			Services:  services,
			Types:     types,
			Window:    p.Window,
			Namespace: p.Namespace,
		})
		if err != nil {
			return "", err
		}
		return marshal(res)
	})

	r.register(ToolDef{
		Name:        "tail",
		Description: "Start live log tailing. Returns recent logs and begins streaming new entries in real-time. User sees a terminal-style log viewer. Use when user asks to tail, watch, or follow logs.",
		InputSchema: jsonSchema(map[string]property{
			"service":   {Type: "string", Desc: "Service name to tail (required)", Required: true},
			"pattern":   {Type: "string", Desc: "Filter log body by text pattern (optional)"},
			"severity":  {Type: "string", Desc: "Minimum severity: ERROR, WARN, INFO, DEBUG (optional)"},
			"namespace": {Type: "string", Desc: "Namespace filter (optional)"},
		}),
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		var p struct {
			Service   string `json:"service"`
			Pattern   string `json:"pattern"`
			Severity  string `json:"severity"`
			Namespace string `json:"namespace"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if p.Service == "" {
			return "", fmt.Errorf("service is required")
		}

		var severities []string
		if p.Severity != "" {
			severities = []string{p.Severity}
		}

		res, err := svc.Find(ctx, service.FindParams{
			Query:     p.Pattern,
			Service:   p.Service,
			Severity:  severities,
			Type:      "logs",
			Window:    5,
			Limit:     20,
			Namespace: p.Namespace,
		})
		if err != nil {
			return "", err
		}

		type tailResult struct {
			Logs []service.LogResult `json:"logs"`
			Tail struct {
				Service   string    `json:"service"`
				Pattern   string    `json:"pattern,omitempty"`
				Severity  string    `json:"severity,omitempty"`
				Namespace string    `json:"namespace,omitempty"`
				Since     time.Time `json:"since,omitempty"`
			} `json:"tail"`
		}
		out := tailResult{Logs: res.Logs}
		out.Tail.Service = p.Service
		out.Tail.Pattern = p.Pattern
		out.Tail.Severity = p.Severity
		out.Tail.Namespace = svc.ResolveNamespace(p.Namespace)
		// Set Since to the latest log timestamp so polling doesn't re-send them
		if len(res.Logs) > 0 {
			// Logs are returned newest-first from Find
			if t, err := time.Parse("2006-01-02T15:04:05Z", res.Logs[0].Time); err == nil {
				out.Tail.Since = t
			} else {
				slog.Warn("tail tool: failed to parse log time for Since cursor",
					"time", res.Logs[0].Time, "err", err)
			}
		}
		return marshal(out)
	})

	slog.Info("AI tool registry initialized",
		"mcp_tools", len(r.defs)-len(r.handlers),
		"ai_only_tools", len(r.handlers),
		"total", len(r.defs))
	return r, nil
}

// Close cleanly shuts down the in-process MCP client session.
func (r *ToolRegistry) Close() error {
	if r.session != nil {
		err := r.session.Close()
		r.session = nil
		return err
	}
	return nil
}

// Defs returns all tool definitions for the LLM.
func (r *ToolRegistry) Defs() []ToolDef {
	return r.defs
}

// Execute runs a tool by name and returns the result text.
// AI-only tools are checked first; all others are dispatched to the MCP server.
func (r *ToolRegistry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	// Check AI-only handlers first
	if h, ok := r.handlers[name]; ok {
		result, err := h(ctx, input)
		if err != nil {
			slog.Warn("tool execution failed", "tool", name, "err", err)
			return "", err
		}
		return result, nil
	}

	// Fall through to MCP server via in-memory session
	if r.session == nil {
		return "", fmt.Errorf("tool %s: registry is closed", name)
	}
	var args any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid tool arguments: %w", err)
		}
	}

	result, err := r.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		slog.Warn("MCP tool execution failed", "tool", name, "err", err)
		return "", err
	}
	if result.IsError {
		text := extractMCPText(result)
		slog.Warn("MCP tool returned error", "tool", name, "text", text)
		return "", fmt.Errorf("tool %s: %s", name, text)
	}
	return extractMCPText(result), nil
}

func (r *ToolRegistry) register(def ToolDef, handler ToolHandler) {
	if _, exists := r.handlers[def.Name]; exists {
		panic("ai: duplicate tool registration: " + def.Name)
	}
	r.defs = append(r.defs, def)
	r.handlers[def.Name] = handler
}

func marshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// extractMCPText pulls text content from an MCP CallToolResult.
func extractMCPText(result *mcp.CallToolResult) string {
	var parts []string
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
			continue
		}
		// For non-TextContent types, attempt to extract a "text" field from JSON.
		data, err := c.MarshalJSON()
		if err != nil {
			slog.Warn("extractMCPText: MarshalJSON failed", "err", err)
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			slog.Warn("extractMCPText: Unmarshal failed", "err", err)
			continue
		}
		if text, ok := m["text"].(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// JSON Schema helpers

type property struct {
	Type     string    `json:"type"`
	Desc     string    `json:"description"`
	Required bool      `json:"-"`
	Items    *property `json:"items,omitempty"`
}

func jsonSchema(props map[string]property) map[string]any {
	properties := make(map[string]any)
	var required []string

	for name, p := range props {
		prop := map[string]any{
			"type":        p.Type,
			"description": p.Desc,
		}
		if p.Items != nil {
			prop["items"] = map[string]any{"type": p.Items.Type}
		}
		properties[name] = prop
		if p.Required {
			required = append(required, name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
