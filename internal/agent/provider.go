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

// EventType classifies a stream event.
type EventType int

const (
	EventText    EventType = iota // text token
	EventToolUse                  // model requested a tool call
	EventStop                     // generation stopped
	EventError                    // provider error
)

// Provider streams model completions. The provider must:
//   - Emit zero or more EventText events with text deltas
//   - Emit zero or more EventToolUse events (ToolCall must be non-nil)
//   - Emit exactly one EventStop OR EventError as the final event
//   - EventError is terminal: the provider must return after emitting it
//   - Stop streaming if the callback returns a non-nil error
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

// StreamEvent is a single event from the model stream; Type discriminates
// which payload field is set.
type StreamEvent struct {
	Type       EventType
	Delta      string    // text token (EventText)
	ToolCall   *ToolCall // completed tool call (EventToolUse)
	StopReason string    // e.g. "end_turn", "tool_calls", "length", "max_tokens" (EventStop)
	Error      string    // error message (EventError)
}

type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string { return fmt.Sprintf("model API error %d: %s", e.StatusCode, e.Body) }
