package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testIn struct {
	Name string `json:"name"`
}

type testOut struct {
	Value int    `json:"value"`
	Msg   string `json:"msg"`
}

func TestWrap_EnvelopeShape(t *testing.T) {
	handler := func(ctx context.Context, req *mcp.CallToolRequest, in testIn) (*mcp.CallToolResult, testOut, error) {
		return nil, testOut{Value: 42, Msg: "hello"}, nil
	}

	wrapped := wrap("test_tool", handler)
	_, out, err := wrapped(context.Background(), nil, testIn{Name: "x"})
	if err != nil {
		t.Fatalf("wrap() error = %v", err)
	}

	result, ok := out.(Result)
	if !ok {
		t.Fatalf("wrap() returned %T, want Result", out)
	}

	if result.Type != "test_tool" {
		t.Errorf("Type = %q, want %q", result.Type, "test_tool")
	}
	if result.Meta.ExecTimeMs < 0 {
		t.Errorf("ExecTimeMs = %d, want >= 0", result.Meta.ExecTimeMs)
	}

	data, ok := result.Data.(testOut)
	if !ok {
		t.Fatalf("Data is %T, want testOut", result.Data)
	}
	if data.Value != 42 || data.Msg != "hello" {
		t.Errorf("Data = %+v, want {Value:42, Msg:hello}", data)
	}
}

func TestWrap_ErrorPropagation(t *testing.T) {
	handler := func(ctx context.Context, req *mcp.CallToolRequest, in testIn) (*mcp.CallToolResult, testOut, error) {
		return nil, testOut{}, fmt.Errorf("something broke")
	}

	wrapped := wrap("test_tool", handler)
	_, out, err := wrapped(context.Background(), nil, testIn{})
	if err == nil {
		t.Fatal("wrap() should propagate error")
	}
	if err.Error() != "something broke" {
		t.Errorf("error = %q, want %q", err.Error(), "something broke")
	}

	result, ok := out.(Result)
	if !ok {
		t.Fatalf("wrap() returned %T on error, want Result", out)
	}
	if result.Type != "test_tool" {
		t.Errorf("Type = %q on error, want %q", result.Type, "test_tool")
	}
}

func TestWrap_CallToolResultBypass(t *testing.T) {
	ctr := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "custom"}},
	}
	handler := func(ctx context.Context, req *mcp.CallToolRequest, in testIn) (*mcp.CallToolResult, testOut, error) {
		return ctr, testOut{}, nil
	}

	wrapped := wrap("test_tool", handler)
	callResult, out, err := wrapped(context.Background(), nil, testIn{})
	if err != nil {
		t.Fatalf("wrap() error = %v", err)
	}
	if callResult != ctr {
		t.Error("wrap() should pass through CallToolResult")
	}
	if out != nil {
		t.Errorf("out should be nil when CallToolResult is set, got %v", out)
	}
}

func TestWrap_JSONSerialization(t *testing.T) {
	handler := func(ctx context.Context, req *mcp.CallToolRequest, in testIn) (*mcp.CallToolResult, testOut, error) {
		return nil, testOut{Value: 7, Msg: "ok"}, nil
	}

	wrapped := wrap("overview", handler)
	_, out, _ := wrapped(context.Background(), nil, testIn{})

	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if m["type"] != "overview" {
		t.Errorf("JSON type = %v, want %q", m["type"], "overview")
	}
	if m["data"] == nil {
		t.Error("JSON data should not be nil")
	}
	if m["meta"] == nil {
		t.Error("JSON meta should not be nil")
	}

	meta, ok := m["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta is %T, want map", m["meta"])
	}
	if _, ok := meta["exec_time_ms"]; !ok {
		t.Error("meta should have exec_time_ms")
	}
}
