package mcp

import (
	"context"
	"embed"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpAppMIME   = "text/html;profile=mcp-app"
	panelsAppURI = "ui://fanout/panels.html"
)

//go:embed apps/*.html
var appFiles embed.FS

func (s *Server) registerAppResources() {
	s.addAppResource("Telemetry panels", panelsAppURI, "apps/panels.html")
}

func (s *Server) addAppResource(name, uri, path string) {
	meta := mcp.Meta{"ui": map[string]any{"csp": map[string]any{}}}
	s.mcp.AddResource(&mcp.Resource{
		Meta:        meta,
		Name:        name,
		Title:       name,
		URI:         uri,
		MIMEType:    mcpAppMIME,
		Description: "Interactive Fanout observability view delivered as an MCP App.",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		html, err := appFiles.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: uri, MIMEType: mcpAppMIME, Text: string(html), Meta: meta,
		}}}, nil
	})
}
