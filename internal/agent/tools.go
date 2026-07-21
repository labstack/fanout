package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ToolExecution struct {
	Content        string
	Structured     any
	AppResourceURI string
	IsError        bool
}

type ToolRegistry struct {
	session       *mcp.ClientSession
	serverSession *mcp.ServerSession
	definitions   []ToolDef
	apps          map[string]string
}

func NewToolRegistry(ctx context.Context, server *mcp.Server) (*ToolRegistry, error) {
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect internal MCP server: %w", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "fanout-agent", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		serverSession.Close()
		return nil, fmt.Errorf("connect internal MCP client: %w", err)
	}
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		session.Close()
		serverSession.Close()
		return nil, fmt.Errorf("list MCP tools: %w", err)
	}
	registry := &ToolRegistry{session: session, serverSession: serverSession, apps: map[string]string{}}
	for _, tool := range listed.Tools {
		registry.definitions = append(registry.definitions, ToolDef{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
		if resourceURI := appResourceURI(tool.Meta); resourceURI != "" {
			registry.apps[tool.Name] = resourceURI
		}
	}
	return registry, nil
}

func (r *ToolRegistry) Close() error {
	if r == nil {
		return nil
	}
	if r.session != nil {
		_ = r.session.Close()
	}
	if r.serverSession != nil {
		return r.serverSession.Close()
	}
	return nil
}

func (r *ToolRegistry) Definitions() []ToolDef { return append([]ToolDef(nil), r.definitions...) }

func (r *ToolRegistry) Execute(ctx context.Context, call ToolCall) (ToolExecution, error) {
	arguments := json.RawMessage(call.Input)
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	params := &mcp.CallToolParams{Name: call.Name, Arguments: arguments}
	if owner := dashboard.OwnerFromContext(ctx); owner != "" {
		params.Meta = mcp.Meta{dashboard.OwnerMetaKey: owner}
	}
	result, err := r.session.CallTool(ctx, params)
	if err != nil {
		return ToolExecution{}, fmt.Errorf("call MCP tool %s: %w", call.Name, err)
	}
	content := textContent(result.Content)
	if result.StructuredContent != nil {
		if encoded, marshalErr := json.Marshal(result.StructuredContent); marshalErr == nil {
			content = string(encoded)
		}
	}
	if content == "" {
		content = "{}"
	}
	return ToolExecution{Content: content, Structured: result.StructuredContent, AppResourceURI: r.apps[call.Name], IsError: result.IsError}, nil
}

func textContent(contents []mcp.Content) string {
	parts := make([]string, 0, len(contents))
	for _, content := range contents {
		if text, ok := content.(*mcp.TextContent); ok && strings.TrimSpace(text.Text) != "" {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func appResourceURI(meta mcp.Meta) string {
	if ui, ok := meta["ui"].(map[string]any); ok {
		if value, ok := ui["resourceUri"].(string); ok {
			return value
		}
	}
	if value, ok := meta["ui/resourceUri"].(string); ok {
		return value
	}
	return ""
}
