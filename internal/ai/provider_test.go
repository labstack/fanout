package ai

import "testing"

func TestUserMessage(t *testing.T) {
	m := UserMessage("hello")
	if m.Role != RoleUser {
		t.Errorf("Role = %q, want %q", m.Role, RoleUser)
	}
	if m.Content != "hello" {
		t.Errorf("Content = %q, want %q", m.Content, "hello")
	}
}

func TestAssistantMessage(t *testing.T) {
	tcs := []ToolCall{{ID: "1", Name: "status", Input: "{}"}}
	m := AssistantMessage("thinking", tcs)
	if m.Role != RoleAssistant {
		t.Errorf("Role = %q, want %q", m.Role, RoleAssistant)
	}
	if m.Content != "thinking" {
		t.Errorf("Content = %q, want %q", m.Content, "thinking")
	}
	if len(m.ToolCalls) != 1 || m.ToolCalls[0].Name != "status" {
		t.Errorf("ToolCalls = %+v, want one call named 'status'", m.ToolCalls)
	}
}

func TestAssistantMessage_NoToolCalls(t *testing.T) {
	m := AssistantMessage("done", nil)
	if m.Role != RoleAssistant {
		t.Errorf("Role = %q, want %q", m.Role, RoleAssistant)
	}
	if len(m.ToolCalls) != 0 {
		t.Errorf("ToolCalls = %+v, want empty", m.ToolCalls)
	}
}

func TestToolMessage(t *testing.T) {
	m := ToolMessage("call-1", `{"ok":true}`, false)
	if m.Role != RoleTool {
		t.Errorf("Role = %q, want %q", m.Role, RoleTool)
	}
	if m.ToolResult == nil {
		t.Fatal("ToolResult is nil")
	}
	if m.ToolResult.ToolCallID != "call-1" {
		t.Errorf("ToolCallID = %q, want %q", m.ToolResult.ToolCallID, "call-1")
	}
	if m.ToolResult.IsError {
		t.Error("IsError = true, want false")
	}
}

func TestToolMessage_Error(t *testing.T) {
	m := ToolMessage("call-2", "something failed", true)
	if !m.ToolResult.IsError {
		t.Error("IsError = false, want true")
	}
}

func TestAPIError(t *testing.T) {
	err := &APIError{StatusCode: 429, Body: "rate limited"}
	got := err.Error()
	if got != "API error 429: rate limited" {
		t.Errorf("Error() = %q, want %q", got, "API error 429: rate limited")
	}
}
