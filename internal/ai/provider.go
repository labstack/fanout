package ai

import (
	"context"
	"fmt"
)

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

// SystemBlock is a segment of the system prompt. Blocks with CacheControl set
// to "ephemeral" are eligible for Anthropic prompt caching.
type SystemBlock struct {
	Text         string
	CacheControl string // "" or "ephemeral"
}

// StreamParams configures a streaming request.
type StreamParams struct {
	System       string        // simple system prompt (used if SystemBlocks is empty)
	SystemBlocks []SystemBlock // structured system prompt with cache hints
	Messages     []Message
	Tools        []ToolDef
	MaxTokens    int
}

// StreamCallback receives streaming events from a Provider. The provider must:
//   - Emit zero or more EventText events with text deltas
//   - Emit zero or more EventToolUse events (ToolCall must be non-nil)
//   - Emit exactly one EventStop OR EventError as the final event
//   - EventError is terminal: the provider must return after emitting it
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
	ToolCalls  []ToolCall  // assistant tool calls
	ToolResult *ToolResult // tool result
}

// UserMessage creates a user message.
func UserMessage(content string) Message {
	return Message{Role: RoleUser, Content: content}
}

// AssistantMessage creates an assistant message with optional tool calls.
func AssistantMessage(content string, toolCalls []ToolCall) Message {
	return Message{Role: RoleAssistant, Content: content, ToolCalls: toolCalls}
}

// ToolMessage creates a tool result message.
func ToolMessage(toolCallID, content string, isError bool) Message {
	return Message{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: toolCallID, Content: content, IsError: isError}}
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

// APIError represents an HTTP error from an LLM provider.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Body)
}

// ToolDef describes a tool available to the LLM.
type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"` // JSON Schema object
}
