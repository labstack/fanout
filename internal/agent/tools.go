package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/labstack/fanout/internal/dashboard"
	fanoutmcp "github.com/labstack/fanout/internal/mcp"
	"github.com/labstack/fanout/internal/toolerror"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpUIExtension = fanoutmcp.UIExtension
	mcpAppMIME     = fanoutmcp.AppMIME
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
	mutations     map[string]bool
	readOnly      map[string]bool
}

func NewToolRegistry(ctx context.Context, server *mcp.Server) (*ToolRegistry, error) {
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect internal MCP server: %w", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "fanout-agent", Version: "1"}, &mcp.ClientOptions{
		Capabilities: &mcp.ClientCapabilities{Extensions: map[string]any{
			mcpUIExtension: map[string]any{"mimeTypes": []string{mcpAppMIME}},
		}},
	})
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
	registry := &ToolRegistry{session: session, serverSession: serverSession, apps: map[string]string{}, mutations: map[string]bool{}, readOnly: map[string]bool{}}
	for _, tool := range listed.Tools {
		if fanoutmcp.AppOnly(tool.Meta) {
			continue
		}
		registry.definitions = append(registry.definitions, ToolDef{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
		registry.readOnly[tool.Name] = reviewedReadOnly(tool.Name, tool.Annotations)
		if fanoutmcp.RequiredToolScope(tool.Name) == dashboard.OAuthScope && tool.Annotations != nil && !tool.Annotations.ReadOnlyHint {
			registry.mutations[tool.Name] = true
		}
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

// MCP metadata classifies registered tools. Explicit writes remain writes even
// if their annotations drift; tools without metadata require a reviewed entry.
func reviewedReadOnly(name string, annotations *mcp.ToolAnnotations) bool {
	switch name {
	case "create_dashboard", "edit_dashboard", "replace_dashboard", "restore_dashboard_version":
		return false
	}
	if annotations != nil {
		return annotations.ReadOnlyHint
	}
	switch name {
	case "get_observability_overview", "get_service_topology", "get_service_dependencies", "get_service_performance", "inspect_trace", "search_logs", "get_intelligence_snapshot", "get_telemetry_schema", "query_telemetry", "preview_panels", "list_dashboards", "get_dashboard", "list_dashboard_versions":
		return true
	default:
		return false
	}
}

func (r *ToolRegistry) ReadOnly(name string) bool { return r.readOnly[name] }

func (r *ToolRegistry) Execute(ctx context.Context, call ToolCall) (ToolExecution, error) {
	allowed := false
	for _, definition := range r.definitions {
		if definition.Name == call.Name {
			allowed = true
			break
		}
	}
	if !allowed {
		return ToolExecution{Content: toolerror.JSON("tool_failed", "This tool is unavailable to the model: "+call.Name), IsError: true}, nil
	}
	arguments := json.RawMessage(call.Input)
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	params := &mcp.CallToolParams{Name: call.Name, Arguments: arguments}
	if owner := dashboard.OwnerFromContext(ctx); owner != "" {
		params.Meta = mcp.Meta{dashboard.OwnerMetaKey: owner}
		if origin, ok := dashboard.BuildOriginFromContext(ctx); ok {
			params.Meta[dashboard.BuildOriginMetaKey] = origin
		}
	}
	result, err := r.session.CallTool(ctx, params)
	if err != nil {
		return ToolExecution{}, fmt.Errorf("call MCP tool %s: %w", call.Name, err)
	}
	if r.mutations[call.Name] && !result.IsError {
		dashboard.MarkSaveCommitted(ctx)
	}
	content, err := r.modelContent(call.Name, result)
	if err != nil {
		return ToolExecution{}, err
	}
	return ToolExecution{Content: content, Structured: result.StructuredContent, AppResourceURI: r.apps[call.Name], IsError: result.IsError}, nil
}

func (r *ToolRegistry) modelContent(name string, result *mcp.CallToolResult) (string, error) {
	content := textContent(result.Content)
	if r.apps[name] == "" && result.StructuredContent != nil {
		if encoded, marshalErr := json.Marshal(result.StructuredContent); marshalErr == nil {
			content = string(encoded)
		} else {
			return "", fmt.Errorf("encode MCP structured content for %s: %w", name, marshalErr)
		}
	}
	if r.apps[name] != "" && len(content) > 16*1024 {
		content = content[:16*1024]
		for !utf8.ValidString(content) {
			content = content[:len(content)-1]
		}
	}
	if content == "" {
		content = "{}"
	}
	return content, nil
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
	return ""
}

// Consume the server's identity; never recompute it in the agent or host.
func fragmentView(value any) fanoutmcp.FragmentView {
	if f, ok := value.(fanoutmcp.PanelFragment); ok {
		return f.View
	}
	if f, ok := value.(*fanoutmcp.PanelFragment); ok && f != nil {
		return f.View
	}
	if f, ok := value.(map[string]any); ok {
		if v, ok := f["view"].(map[string]any); ok {
			kind, _ := v["kind"].(string)
			key, _ := v["key"].(string)
			return fanoutmcp.FragmentView{Kind: kind, Key: key}
		}
	}
	return fanoutmcp.FragmentView{}
}
