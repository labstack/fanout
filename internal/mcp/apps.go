package mcp

import (
	"context"
	"embed"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpAppMIME        = "text/html;profile=mcp-app"
	overviewAppURI    = "ui://fanout/observability-overview.html"
	topologyAppURI    = "ui://fanout/service-topology.html"
	performanceAppURI = "ui://fanout/service-performance.html"
	traceAppURI       = "ui://fanout/trace-detail.html"
	logsAppURI        = "ui://fanout/log-explorer.html"
)

//go:embed apps/*.html
var appFiles embed.FS

func (s *Server) registerAppResources() {
	s.addAppResource("Observability overview", overviewAppURI, "apps/overview.html")
	s.addAppResource("Service topology", topologyAppURI, "apps/topology.html")
	s.addAppResource("Service performance", performanceAppURI, "apps/performance.html")
	s.addAppResource("Trace detail", traceAppURI, "apps/trace.html")
	s.addAppResource("Log explorer", logsAppURI, "apps/logs.html")
}

func (s *Server) addAppResource(name, uri, path string) {
	s.mcp.AddResource(&mcp.Resource{
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
			URI: uri, MIMEType: mcpAppMIME, Text: string(html),
		}}}, nil
	})
}
