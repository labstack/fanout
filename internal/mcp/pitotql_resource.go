package mcp

import (
	"context"

	"github.com/labstack/pitot/internal/pitotql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const previousQueryReferenceURI = "pitot://fanql/reference" // Published read alias.

const pitotqlReferenceURI = "pitot://pitotql/reference"

func (s *Server) registerPitotQLResource() {

	s.mcp.AddResource(&mcp.Resource{
		URI: pitotqlReferenceURI, Name: "PitotQL reference", MIMEType: "text/markdown",
		Description: "The PitotQL preview language reference, generated from the server registries.",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: pitotqlReferenceURI, MIMEType: "text/markdown", Text: pitotql.Reference()}}}, nil
	})
}

// canonicalQueryResource accepts the published URI without advertising it.
func canonicalQueryResource(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if read, ok := req.(*mcp.ReadResourceRequest); ok && method == "resources/read" && read.Params != nil && read.Params.URI == previousQueryReferenceURI {
			copied := *read
			params := *read.Params
			params.URI = pitotqlReferenceURI
			copied.Params = &params
			req = &copied
		}
		return next(ctx, method, req)
	}
}
