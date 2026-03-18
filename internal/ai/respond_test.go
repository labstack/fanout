package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/config"
)

// scriptedProvider replays a sequence of scripted responses for testing.
type scriptedProvider struct {
	steps []scriptedStep
	call  int
}

type scriptedStep struct {
	text       string
	toolCalls  []ToolCall
	stopReason string
}

func (p *scriptedProvider) Stream(_ context.Context, _ StreamParams, cb StreamCallback) error {
	if p.call >= len(p.steps) {
		return fmt.Errorf("no more scripted steps")
	}
	step := p.steps[p.call]
	p.call++

	if step.text != "" {
		if err := cb(StreamEvent{Type: EventText, Delta: step.text}); err != nil {
			return err
		}
	}
	for _, tc := range step.toolCalls {
		tc := tc
		if err := cb(StreamEvent{Type: EventToolUse, ToolCall: &tc}); err != nil {
			return err
		}
	}
	return cb(StreamEvent{Type: EventStop, StopReason: step.stopReason})
}

func TestOrchestrator_RespondTool_ProducesBlocks(t *testing.T) {
	respondInput, _ := json.Marshal(map[string]any{
		"text": "System is healthy.",
		"blocks": []map[string]any{
			{
				"type": "metrics",
				"data": map[string]any{
					"items": []map[string]any{
						{"label": "P95", "value": 42.5, "unit": "ms", "status": "ok"},
					},
				},
			},
		},
	})

	provider := &scriptedProvider{
		steps: []scriptedStep{
			{
				toolCalls:  []ToolCall{{ID: "r1", Name: respondToolName, Input: string(respondInput)}},
				stopReason: "tool_use",
			},
		},
	}

	tools := &ToolRegistry{handlers: map[string]ToolHandler{}}
	orch := NewOrchestrator(provider, tools, nil, config.Config{})

	var doneEvent *ClientEvent
	send := func(event ClientEvent) error {
		if event.Type == CEDone {
			doneEvent = &event
		}
		return nil
	}

	conv := []Message{UserMessage("How's the system?")}
	_, err := orch.Run(context.Background(), conv, 60, "", send)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if doneEvent == nil {
		t.Fatal("no CEDone event received")
	}
	if len(doneEvent.Blocks) < 2 {
		t.Fatalf("got %d blocks, want >= 2 (text + metrics)", len(doneEvent.Blocks))
	}
	if doneEvent.Blocks[0].Type != BlockText {
		t.Errorf("first block type = %q, want text", doneEvent.Blocks[0].Type)
	}
	if doneEvent.Blocks[1].Type != BlockMetrics {
		t.Errorf("second block type = %q, want metrics", doneEvent.Blocks[1].Type)
	}
}

func TestOrchestrator_NoRespondTool_FallsBackToText(t *testing.T) {
	provider := &scriptedProvider{
		steps: []scriptedStep{
			{
				text:       "Just a plain text response.",
				stopReason: "end_turn",
			},
		},
	}

	tools := &ToolRegistry{handlers: map[string]ToolHandler{}}
	orch := NewOrchestrator(provider, tools, nil, config.Config{})

	var doneEvent *ClientEvent
	send := func(event ClientEvent) error {
		if event.Type == CEDone {
			doneEvent = &event
		}
		return nil
	}

	conv := []Message{UserMessage("Hello")}
	_, err := orch.Run(context.Background(), conv, 60, "", send)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if doneEvent == nil {
		t.Fatal("no CEDone event received")
	}
	if len(doneEvent.Blocks) != 1 {
		t.Fatalf("got %d blocks, want 1 (text fallback)", len(doneEvent.Blocks))
	}
	if doneEvent.Blocks[0].Type != BlockText {
		t.Errorf("block type = %q, want text", doneEvent.Blocks[0].Type)
	}
	var td TextBlockData
	json.Unmarshal(doneEvent.Blocks[0].Data, &td)
	if !strings.Contains(td.Content, "plain text") {
		t.Errorf("text = %q, want it to contain streamed text", td.Content)
	}
}

func TestOrchestrator_RespondTool_InvalidBlocks_Dropped(t *testing.T) {
	respondInput, _ := json.Marshal(map[string]any{
		"text": "Here's the data.",
		"blocks": []map[string]any{
			{
				"type": "metrics",
				"data": map[string]any{"items": []any{}}, // empty — invalid
			},
			{
				"type": "table",
				"data": map[string]any{
					"columns": []map[string]any{{"key": "k", "label": "K"}},
					"rows":    []map[string]any{{"k": "v"}},
				},
			},
		},
	})

	provider := &scriptedProvider{
		steps: []scriptedStep{
			{
				toolCalls:  []ToolCall{{ID: "r1", Name: respondToolName, Input: string(respondInput)}},
				stopReason: "tool_use",
			},
		},
	}

	tools := &ToolRegistry{handlers: map[string]ToolHandler{}}
	orch := NewOrchestrator(provider, tools, nil, config.Config{})

	var doneEvent *ClientEvent
	send := func(event ClientEvent) error {
		if event.Type == CEDone {
			doneEvent = &event
		}
		return nil
	}

	conv := []Message{UserMessage("Show data")}
	_, err := orch.Run(context.Background(), conv, 60, "", send)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	// Should have text + table (metrics dropped because empty items)
	if len(doneEvent.Blocks) != 2 {
		t.Fatalf("got %d blocks, want 2 (text + table, empty metrics dropped)", len(doneEvent.Blocks))
	}
}

func TestOrchestrator_RespondTool_InvalidJSON_Fallback(t *testing.T) {
	provider := &scriptedProvider{
		steps: []scriptedStep{
			{
				text:       "Some thinking text",
				toolCalls:  []ToolCall{{ID: "r1", Name: respondToolName, Input: "not valid json"}},
				stopReason: "tool_use",
			},
		},
	}

	tools := &ToolRegistry{handlers: map[string]ToolHandler{}}
	orch := NewOrchestrator(provider, tools, nil, config.Config{})

	var doneEvent *ClientEvent
	send := func(event ClientEvent) error {
		if event.Type == CEDone {
			doneEvent = &event
		}
		return nil
	}

	conv := []Message{UserMessage("test")}
	_, err := orch.Run(context.Background(), conv, 60, "", send)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if doneEvent == nil {
		t.Fatal("no CEDone event")
	}
	// Should fallback to text block with the streamed text
	if len(doneEvent.Blocks) != 1 {
		t.Fatalf("got %d blocks, want 1 (fallback text)", len(doneEvent.Blocks))
	}
	if doneEvent.Blocks[0].Type != BlockText {
		t.Errorf("block type = %q, want text", doneEvent.Blocks[0].Type)
	}
}

func TestRespondToolDef_HasSchema(t *testing.T) {
	def := respondToolDef()
	if def.Name != respondToolName {
		t.Errorf("name = %q, want %q", def.Name, respondToolName)
	}
	if def.InputSchema == nil {
		t.Error("InputSchema is nil")
	}
	// Verify schema is valid JSON
	b, err := json.Marshal(def.InputSchema)
	if err != nil {
		t.Fatalf("InputSchema marshal: %v", err)
	}
	if len(b) < 100 {
		t.Errorf("InputSchema too short (%d bytes), expected substantial schema", len(b))
	}
}
