package ai

import "testing"

func TestTrimConversation_UnderLimit(t *testing.T) {
	msgs := []Message{
		UserMessage("q1"),
		AssistantMessage("a1", nil),
	}
	trimConversation(&msgs)
	if len(msgs) != 2 {
		t.Errorf("messages = %d, want 2 (unchanged)", len(msgs))
	}
}

func TestTrimConversation_TrimsAtUserBoundary(t *testing.T) {
	// Build a conversation exceeding 40 messages
	var msgs []Message
	for i := 0; i < 25; i++ {
		msgs = append(msgs, UserMessage("q"))
		msgs = append(msgs, AssistantMessage("a", nil))
	}
	// 50 messages total

	trimConversation(&msgs)

	if len(msgs) > 40 {
		t.Errorf("messages = %d, want <= 40", len(msgs))
	}
	// First message should be a user message (safe boundary)
	if msgs[0].Role != RoleUser {
		t.Errorf("first message role = %q, want %q", msgs[0].Role, RoleUser)
	}
}

func TestTrimConversation_SkipsToolMessages(t *testing.T) {
	// Build conversation where cut point falls on a tool message
	var msgs []Message
	for i := 0; i < 15; i++ {
		msgs = append(msgs, UserMessage("q"))
		msgs = append(msgs,
			AssistantMessage("", []ToolCall{{ID: "tc", Name: "status", Input: "{}"}}),
		)
		msgs = append(msgs, ToolMessage("tc", "result", false))
	}
	// 45 messages total

	trimConversation(&msgs)

	if len(msgs) > 40 {
		t.Errorf("messages = %d, want <= 40", len(msgs))
	}
	// Must start at a user boundary, not mid-tool-call
	if msgs[0].Role != RoleUser {
		t.Errorf("first message role = %q, want %q (user boundary)", msgs[0].Role, RoleUser)
	}
}

func TestTrimConversation_ExactlyAtLimit(t *testing.T) {
	var msgs []Message
	for i := 0; i < 20; i++ {
		msgs = append(msgs, UserMessage("q"))
		msgs = append(msgs, AssistantMessage("a", nil))
	}
	// 40 messages = maxMessages

	trimConversation(&msgs)

	if len(msgs) != 40 {
		t.Errorf("messages = %d, want 40 (unchanged at limit)", len(msgs))
	}
}
