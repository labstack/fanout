package agent

import (
	"context"
	"fmt"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Provider interface {
	Stream(context.Context, StreamParams, func(StreamEvent) error) error
}

type StreamParams struct {
	System    string
	Messages  []ProviderMessage
	Tools     []ToolDef
	MaxTokens int
}

type ProviderMessage struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolResult *ToolResult
}

type ToolCall struct {
	ID    string
	Name  string
	Input string
}

type ToolResult struct {
	ToolCallID string
	Content    string
	IsError    bool
}

type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

type StreamEvent struct {
	Delta      string
	ToolCall   *ToolCall
	StopReason string
	Error      string
}

type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string { return fmt.Sprintf("model API error %d: %s", e.StatusCode, e.Body) }
