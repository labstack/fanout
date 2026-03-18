package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

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
// tools directly.
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

// Defs returns a copy of all tool definitions for the LLM.
func (r *ToolRegistry) Defs() []ToolDef {
	out := make([]ToolDef, len(r.defs))
	copy(out, r.defs)
	return out
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
