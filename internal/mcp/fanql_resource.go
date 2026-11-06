package mcp

import (
	"context"

	"github.com/labstack/fanout/internal/fanql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const fanqlReferenceURI = "fanout://fanql/reference"

func (s *Server) registerFanQLResource() {
	s.mcp.AddResource(&mcp.Resource{
		URI: fanqlReferenceURI, Name: "FanQL reference", MIMEType: "text/markdown",
		Description: "The FanQL preview language reference, generated from the server registries.",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: fanqlReferenceURI, MIMEType: "text/markdown", Text: fanql.Reference()}}}, nil
	})
}
