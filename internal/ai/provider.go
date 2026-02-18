package ai

import "context"

// Role identifies the sender of a message.
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
	EventToolUse                  // LLM requested a tool call
	EventStop                     // generation stopped
	EventError                    // provider error
)

// Provider streams LLM completions.
type Provider interface {
	Stream(ctx context.Context, params StreamParams, cb StreamCallback) error
}

// StreamParams configures a streaming request.
type StreamParams struct {
	System    string
	Messages  []Message
	Tools     []ToolDef
	MaxTokens int
}

// StreamCallback receives streaming events from a Provider. The provider must:
//   - Emit zero or more EventText events with text deltas
//   - Emit zero or more EventToolUse events (ToolCall must be non-nil)
//   - Emit exactly one EventStop as the final event
//   - Stop streaming if the callback returns a non-nil error
type StreamCallback func(event StreamEvent) error

// StreamEvent is a single event from the LLM stream.
type StreamEvent struct {
	Type       EventType
	Delta      string    // text token (EventText)
	ToolCall   *ToolCall // completed tool call (EventToolUse)
	StopReason string    // "end_turn" or "tool_use" (EventStop)
	Error      string    // error message (EventError)
}

// Message is a conversation turn.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall   // assistant tool calls
	ToolResult *ToolResult  // tool result
}

// ToolCall is a request from the LLM to invoke a tool.
type ToolCall struct {
	ID    string
	Name  string
	Input string // raw JSON
}

// ToolResult is the response from executing a tool.
type ToolResult struct {
	ToolCallID string
	Content    string // JSON result
	IsError    bool
}

// ToolDef describes a tool available to the LLM.
type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"` // JSON Schema object
}
