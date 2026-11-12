package mcp

import (
	"context"

	"github.com/labstack/pitot/internal/pitotql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const pitotqlReferenceURI = "pitot://pitotql/reference"

func (s *Server) registerPitotQLResource() {

	s.mcp.AddResource(&mcp.Resource{
		URI: pitotqlReferenceURI, Name: "PitotQL reference", MIMEType: "text/markdown",
		Description: "The PitotQL preview language reference, generated from the server registries.",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: pitotqlReferenceURI, MIMEType: "text/markdown", Text: pitotql.Reference()}}}, nil
	})
}
