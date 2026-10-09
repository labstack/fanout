package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolFailuresCarryMatchingErrorObjectsInBothContentPaths(t *testing.T) {
	s := newPanelServer(t)
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "fail_tool"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
		return nil, struct{}{}, errors.New("The tool could not complete.")
	})
	client := connectTestClient(t, s, nil)
	result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "fail_tool", Arguments: map[string]any{}})
	if err != nil || !result.IsError {
		t.Fatal(result, err)
	}
	if result.StructuredContent != nil {
		t.Fatalf("structured content would break the tool's output schema: %v", result.StructuredContent)
	}
	if len(result.Content) != 1 {
		t.Fatal(result.Content)
	}
	raw := []byte(result.Content[0].(*mcp.TextContent).Text)
	var body struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "tool_failed" || body.Error.Message != "The tool could not complete." {
		t.Fatal(string(raw))
	}
}

func TestUnknownToolsKeepJSONRPCFailures(t *testing.T) {
	s := newPanelServer(t)
	client := connectTestClient(t, s, nil)
	result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "missing_tool", Arguments: map[string]any{}})
	if err == nil || result != nil {
		t.Fatal(result, err)
	}
}
