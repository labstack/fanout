package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/labstack/fanout/internal/dashboard"
	fanoutmcp "github.com/labstack/fanout/internal/mcp"
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
	registry := &ToolRegistry{session: session, serverSession: serverSession, apps: map[string]string{}}
	for _, tool := range listed.Tools {
		if fanoutmcp.AppOnly(tool.Meta) {
			continue
		}
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
	allowed := false
	for _, definition := range r.definitions {
		if definition.Name == call.Name {
			allowed = true
			break
		}
	}
	if !allowed {
		return ToolExecution{Content: "This tool is unavailable to the model: " + call.Name, IsError: true}, nil
	}
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

// Titles and transport IDs do not make two projections different views. A
// custom service map followed by the topology preset gets one app activity;
// both tool results still reach the model. Scope, vars and rendering options
// remain part of the identity.
func fragmentIdentity(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var fragment fanoutmcp.PanelFragment
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&fragment); err != nil || len(fragment.Dashboard.Panels) == 0 {
		return ""
	}
	fragment.Dashboard.Name = ""
	var mapData any
	if len(fragment.Dashboard.Panels) == 1 && fragment.Dashboard.Panels[0].Viz == "service_map" && len(fragment.Results) == 1 && fragment.Results[0].Frame != nil && !fragment.Results[0].Frame.Truncated {
		// Different safe caps can return exactly the same complete graph. Require
		// identical projected data before treating those limits as equivalent.
		if query := fragment.Dashboard.Panels[0].Query; query != nil {
			query.Limit = 0
		}
		mapData = fragment.Results[0].Frame
	}
	for i := range fragment.Dashboard.Panels {
		p := &fragment.Dashboard.Panels[i]
		p.ID = ""
		p.Title = ""
		if p.Query != nil {
			sort.Strings(p.Query.Where)
		}
	}
	var vars any = fragment.Vars
	if fragment.Vars == nil {
		vars = map[string]any{}
	}
	var windows [][2]int64
	if fragment.Dashboard.Time.From == nil {
		for _, result := range fragment.Results {
			windows = append(windows, [2]int64{result.FromMS, result.ToMS})
		}
	}
	if fragment.Dashboard.Time.From != nil {
		at := fragment.Dashboard.Time.From.UTC()
		fragment.Dashboard.Time.From = &at
	}
	if fragment.Dashboard.Time.To != nil {
		at := fragment.Dashboard.Time.To.UTC()
		fragment.Dashboard.Time.To = &at
	}
	key, err := json.Marshal(struct {
		Dashboard any        `json:"dashboard"`
		Vars      any        `json:"vars"`
		Windows   [][2]int64 `json:"windows,omitempty"`
		MapData   any        `json:"map,omitempty"`
	}{fragment.Dashboard, vars, windows, mapData})
	if err != nil {
		return ""
	}
	return string(key)
}
