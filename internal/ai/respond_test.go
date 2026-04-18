package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/env"
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
	orch := NewOrchestrator(provider, tools, nil, env.Config{})

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
	if len(doneEvent.Blocks) != 1 {
		t.Fatalf("got %d blocks, want 1 (text only)", len(doneEvent.Blocks))
	}
	if doneEvent.Blocks[0].Type != BlockText {
		t.Errorf("block type = %q, want text", doneEvent.Blocks[0].Type)
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
	orch := NewOrchestrator(provider, tools, nil, env.Config{})

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

func TestOrchestrator_RespondTool_MergesSuggestedBlocks(t *testing.T) {
	respondInput, _ := json.Marshal(map[string]any{
		"text": "Here's the data.",
	})

	provider := &scriptedProvider{
		steps: []scriptedStep{
			{
				toolCalls:  []ToolCall{{ID: "t1", Name: "status", Input: `{}`}},
				stopReason: "tool_use",
			},
			{
				toolCalls:  []ToolCall{{ID: "r1", Name: respondToolName, Input: string(respondInput)}},
				stopReason: "tool_use",
			},
		},
	}

	tools := &ToolRegistry{
		defs: []ToolDef{{Name: "status"}},
		handlers: map[string]ToolHandler{
			"status": func(_ context.Context, _ json.RawMessage) (string, []Block, error) {
				return `{"ok":true}`, []Block{
					MakeMetricsBlock([]MetricItem{{Label: "P95", Value: 42.5, Unit: "ms", Status: "ok"}}),
				}, nil
			},
		},
	}
	orch := NewOrchestrator(provider, tools, nil, env.Config{})

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
	if len(doneEvent.Blocks) != 2 {
		t.Fatalf("got %d blocks, want 2 (text + suggested metrics)", len(doneEvent.Blocks))
	}
	if doneEvent.Blocks[0].Type != BlockText {
		t.Errorf("first block type = %q, want text", doneEvent.Blocks[0].Type)
	}
	if doneEvent.Blocks[1].Type != BlockMetrics {
		t.Errorf("second block type = %q, want metrics", doneEvent.Blocks[1].Type)
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
	orch := NewOrchestrator(provider, tools, nil, env.Config{})

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
	var schema map[string]any
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatalf("schema unmarshal: %v", err)
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props["text"]; !ok {
		t.Error("schema missing text property")
	}
	if _, ok := props["blocks"]; ok {
		t.Error("schema should not contain blocks property")
	}
}

func TestDashboardRespondToolDef_HasSchema(t *testing.T) {
	def := dashboardRespondToolDef()
	if def.Name != dashboardRespondToolName {
		t.Errorf("name = %q, want %q", def.Name, dashboardRespondToolName)
	}
	if def.InputSchema == nil {
		t.Fatal("InputSchema is nil")
	}

	var schema map[string]any
	if err := json.Unmarshal(def.InputSchema.(json.RawMessage), &schema); err != nil {
		t.Fatalf("schema unmarshal: %v", err)
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props["headline"]; !ok {
		t.Error("schema missing headline property")
	}
	if _, ok := props["brief"]; !ok {
		t.Error("schema missing brief property")
	}
	if _, ok := props["actions"]; !ok {
		t.Error("schema missing actions property")
	}
}

func TestOrchestrator_Dashboard_ReturnsStructuredResult(t *testing.T) {
	dashboardInput, _ := json.Marshal(map[string]any{
		"headline": "Checkout is the main risk",
		"brief":    "Checkout latency and errors dominate the current incident picture.",
		"actions": []map[string]any{
			{"label": "Diagnose checkout", "prompt": "Diagnose checkout", "kind": "drill"},
			{"label": "Explain regression", "prompt": "Explain the checkout regression", "kind": "explain"},
		},
	})

	provider := &scriptedProvider{
		steps: []scriptedStep{
			{
				toolCalls:  []ToolCall{{ID: "t1", Name: "status", Input: `{}`}},
				stopReason: "tool_use",
			},
			{
				toolCalls:  []ToolCall{{ID: "d1", Name: dashboardRespondToolName, Input: string(dashboardInput)}},
				stopReason: "tool_use",
			},
		},
	}

	tools := &ToolRegistry{
		defs: []ToolDef{{Name: "status"}},
		handlers: map[string]ToolHandler{
			"status": func(_ context.Context, _ json.RawMessage) (string, []Block, error) {
				return `{"ok":true}`, []Block{
					MakeMetricsBlock([]MetricItem{{Label: "P95", Value: 42.5, Unit: "ms", Status: "ok"}}),
				}, nil
			},
		},
	}
	orch := NewOrchestrator(provider, tools, nil, env.Config{})

	result, err := orch.Dashboard(context.Background(), 60, "")
	if err != nil {
		t.Fatalf("Dashboard() error: %v", err)
	}
	if result.Headline != "Checkout is the main risk" {
		t.Errorf("Headline = %q", result.Headline)
	}
	if !strings.Contains(result.Brief, "Checkout latency") {
		t.Errorf("Brief = %q", result.Brief)
	}
	if len(result.Actions) != 2 {
		t.Fatalf("Actions = %d, want 2", len(result.Actions))
	}
	if len(result.Blocks) != 1 || result.Blocks[0].Type != BlockMetrics {
		t.Fatalf("Blocks = %#v, want one metrics block", result.Blocks)
	}
}
