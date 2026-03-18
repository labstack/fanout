package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Result is the uniform envelope for all MCP tool responses.
type Result struct {
	Type string     `json:"type"`
	Data any        `json:"data"`
	Meta ResultMeta `json:"meta"`
}

// ResultMeta carries cross-cutting execution metadata.
type ResultMeta struct {
	ExecTimeMs int64 `json:"exec_time_ms"`
}

// wrap returns a new handler that wraps the original handler's output in a Result envelope.
// Returns `any` (not Result) so the MCP SDK skips output schema derivation.
func wrap[TIn, TOut any](toolType string, fn func(context.Context, *mcp.CallToolRequest, TIn) (*mcp.CallToolResult, TOut, error)) func(context.Context, *mcp.CallToolRequest, TIn) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in TIn) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		callResult, out, err := fn(ctx, req, in)
		if callResult != nil {
			return callResult, nil, err
		}
		return nil, Result{
			Type: toolType,
			Data: out,
			Meta: ResultMeta{ExecTimeMs: time.Since(start).Milliseconds()},
		}, err
	}
}
