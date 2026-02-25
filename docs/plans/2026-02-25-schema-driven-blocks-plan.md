# Schema-Driven Block Output Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace per-tool block mappings with LLM-driven schema-constrained output via a synthetic "respond" tool.

**Architecture:** A "respond" tool is added to every LLM request. Its `input_schema` is a JSON schema generated from Go block structs. When the LLM calls "respond", the orchestrator intercepts it (never executes), parses `{text, blocks[]}`, validates blocks, and sends them to the client. `tool_blocks.go` is deleted entirely.

**Tech Stack:** Go `reflect` package for schema generation (no new deps). Existing block structs in `blocks.go`.

---

### Task 1: Generate JSON Schema from Block Structs

**Files:**
- Create: `internal/ai/schema_gen.go`
- Create: `internal/ai/schema_gen_test.go`

**Step 1: Write the failing test**

```go
// internal/ai/schema_gen_test.go
package ai

import (
	"encoding/json"
	"testing"
)

func TestGenerateResponseSchema_ValidJSON(t *testing.T) {
	schema := generateResponseSchema()
	if len(schema) == 0 {
		t.Fatal("schema is empty")
	}
	var m map[string]any
	if err := json.Unmarshal(schema, &m); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
}

func TestGenerateResponseSchema_HasRequiredFields(t *testing.T) {
	schema := generateResponseSchema()
	var m map[string]any
	json.Unmarshal(schema, &m)

	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema missing properties")
	}
	if _, ok := props["text"]; !ok {
		t.Error("schema missing 'text' property")
	}
	if _, ok := props["blocks"]; !ok {
		t.Error("schema missing 'blocks' property")
	}

	required, ok := m["required"].([]any)
	if !ok {
		t.Fatal("schema missing required array")
	}
	found := map[string]bool{}
	for _, r := range required {
		found[r.(string)] = true
	}
	if !found["text"] || !found["blocks"] {
		t.Errorf("required = %v, want text and blocks", required)
	}
}

func TestGenerateResponseSchema_BlocksHasAllTypes(t *testing.T) {
	schema := generateResponseSchema()
	raw := string(schema)

	// All 14 block types should appear in the schema
	types := []string{
		"text", "metrics", "table", "timeseries", "bar", "heatmap",
		"trace_waterfall", "topology", "flame_graph", "sankey",
		"dep_matrix", "endpoints", "correlation", "tail",
	}
	for _, bt := range types {
		if !containsString(raw, `"`+bt+`"`) {
			t.Errorf("schema missing block type %q", bt)
		}
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && jsonContains(s, substr)
}

func jsonContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/ai/ -run TestGenerateResponseSchema -v`
Expected: FAIL — `generateResponseSchema` not defined

**Step 3: Write the implementation**

Create `internal/ai/schema_gen.go`. This builds the JSON schema by hand for the top-level response shape and discriminated union, then uses `reflect` to auto-generate each block type's data schema from the existing Go structs.

```go
// internal/ai/schema_gen.go
package ai

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
)

var (
	responseSchemaOnce sync.Once
	responseSchemaJSON json.RawMessage
)

// generateResponseSchema builds the JSON schema for the respond tool's input.
// The schema describes {text: string, blocks: Block[]}, where Block is a
// discriminated union on "type" with per-type "data" shapes.
// Computed once and cached.
func generateResponseSchema() json.RawMessage {
	responseSchemaOnce.Do(func() {
		// Build oneOf variants for each block type
		blockVariants := []map[string]any{
			blockVariant("text", "Markdown callout or narrative section", reflectSchema(TextBlockData{})),
			blockVariant("metrics", "2-6 KPI summary cards", reflectSchema(MetricsBlockData{})),
			blockVariant("table", "Tabular data", reflectSchema(TableBlockData{})),
			blockVariant("timeseries", "Trends over time", reflectSchema(TimeseriesBlockData{})),
			blockVariant("bar", "Ranked comparisons", reflectSchema(BarBlockData{})),
			blockVariant("heatmap", "Latency distributions over time", reflectSchema(HeatmapBlockData{})),
			blockVariant("trace_waterfall", "Single distributed trace", reflectSchema(TraceWaterfallData{})),
			blockVariant("topology", "Service dependency graph", reflectSchema(TopologyData{})),
			blockVariant("flame_graph", "Aggregated span breakdown", reflectSchema(FlameGraphData{})),
			blockVariant("sankey", "Request flow between services", reflectSchema(SankeyData{})),
			blockVariant("dep_matrix", "NxN service health grid", reflectSchema(DepMatrixData{})),
			blockVariant("endpoints", "Per-endpoint breakdown", reflectSchema(EndpointsData{})),
			blockVariant("correlation", "Multi-signal correlation", reflectSchema(CorrelationData{})),
			blockVariant("tail", "Log entries", reflectSchema(TailData{})),
		}

		schema := map[string]any{
			"type":     "object",
			"required": []string{"text", "blocks"},
			"additionalProperties": false,
			"properties": map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "Markdown narrative. Be direct, cite specific numbers, explain root causes.",
				},
				"blocks": map[string]any{
					"type":        "array",
					"description": "Visualization blocks. Choose the best type for each piece of data. 1-3 blocks typical.",
					"items": map[string]any{
						"oneOf": blockVariants,
					},
				},
			},
		}

		b, _ := json.Marshal(schema)
		responseSchemaJSON = b
	})
	return responseSchemaJSON
}

// blockVariant builds one branch of the discriminated union.
func blockVariant(typeName, description string, dataSchema map[string]any) map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"type", "data"},
		"additionalProperties": false,
		"description":          description,
		"properties": map[string]any{
			"type": map[string]any{
				"type":  "string",
				"const": typeName,
			},
			"data": dataSchema,
		},
	}
}

// reflectSchema generates a JSON Schema object from a Go struct using reflection.
// Handles: string, int/float, bool, slices, nested structs, pointers.
// Reads json tags for field names and jsonschema tags for descriptions.
func reflectSchema(v any) map[string]any {
	return reflectType(reflect.TypeOf(v))
}

func reflectType(t reflect.Type) map[string]any {
	if t.Kind() == reflect.Ptr {
		return reflectType(t.Elem())
	}

	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Slice:
		return map[string]any{
			"type":  "array",
			"items": reflectType(t.Elem()),
		}
	case reflect.Map:
		// map[string]any → object with no specific properties
		return map[string]any{"type": "object"}
	case reflect.Struct:
		return reflectStruct(t)
	default:
		return map[string]any{"type": "string"}
	}
}

func reflectStruct(t reflect.Type) map[string]any {
	props := map[string]any{}
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		jsonTag := field.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}
		name, opts := parseJSONTag(jsonTag)
		if name == "" {
			name = field.Name
		}

		schema := reflectType(field.Type)

		// Add description from jsonschema tag if present
		if desc := field.Tag.Get("jsonschema"); desc != "" {
			if d, ok := parseTagValue(desc, "description"); ok {
				schema["description"] = d
			}
		}

		// Handle pointer fields as nullable/optional
		isOptional := opts == "omitempty" || field.Type.Kind() == reflect.Ptr
		if !isOptional {
			required = append(required, name)
		}

		props[name] = schema
	}

	result := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

func parseJSONTag(tag string) (string, string) {
	if tag == "" {
		return "", ""
	}
	parts := strings.SplitN(tag, ",", 2)
	name := parts[0]
	opts := ""
	if len(parts) > 1 {
		opts = parts[1]
	}
	return name, opts
}

func parseTagValue(tag, key string) (string, bool) {
	// Simple key=value parser for struct tags like `jsonschema:"description=some text"`
	for _, part := range strings.Split(tag, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, key+"=") {
			return part[len(key)+1:], true
		}
	}
	return "", false
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/ai/ -run TestGenerateResponseSchema -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/ai/schema_gen.go internal/ai/schema_gen_test.go
git commit -m "feat: add JSON schema generation from block structs"
```

---

### Task 2: Block Validation

**Files:**
- Create: `internal/ai/validate.go`
- Create: `internal/ai/validate_test.go`

**Step 1: Write the failing tests**

```go
// internal/ai/validate_test.go
package ai

import (
	"encoding/json"
	"math"
	"testing"
)

func TestValidateBlocks_DropsEmpty(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockMetrics, MetricsBlockData{Items: nil}),
		NewBlock(BlockMetrics, MetricsBlockData{Items: []MetricItem{{Label: "ok", Value: 1, Unit: "ms", Status: "ok"}}}),
	}
	valid := validateBlocks(blocks)
	if len(valid) != 1 {
		t.Errorf("got %d blocks, want 1 (empty dropped)", len(valid))
	}
}

func TestValidateBlocks_DropsNaN(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockMetrics, MetricsBlockData{Items: []MetricItem{
			{Label: "bad", Value: math.NaN(), Unit: "ms", Status: "ok"},
		}}),
	}
	valid := validateBlocks(blocks)
	if len(valid) != 0 {
		t.Errorf("got %d blocks, want 0 (NaN dropped)", len(valid))
	}
}

func TestValidateBlocks_DropsInfinity(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockMetrics, MetricsBlockData{Items: []MetricItem{
			{Label: "bad", Value: math.Inf(1), Unit: "ms", Status: "ok"},
		}}),
	}
	valid := validateBlocks(blocks)
	if len(valid) != 0 {
		t.Errorf("got %d blocks, want 0 (Inf dropped)", len(valid))
	}
}

func TestValidateBlocks_DropsTimeseriesMismatch(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockTimeseries, TimeseriesBlockData{
			Title:  "test",
			Labels: []string{"a", "b"},
			Series: []TimeseriesSeries{
				{Label: "s1", Values: []float64{1}}, // length mismatch
			},
		}),
	}
	valid := validateBlocks(blocks)
	if len(valid) != 0 {
		t.Errorf("got %d blocks, want 0 (length mismatch dropped)", len(valid))
	}
}

func TestValidateBlocks_KeepsValidTimeseries(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockTimeseries, TimeseriesBlockData{
			Title:  "test",
			Labels: []string{"a", "b"},
			Series: []TimeseriesSeries{
				{Label: "s1", Values: []float64{1, 2}},
			},
		}),
	}
	valid := validateBlocks(blocks)
	if len(valid) != 1 {
		t.Errorf("got %d blocks, want 1", len(valid))
	}
}

func TestValidateBlocks_DropsTopologyBadEdge(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockTopology, TopologyData{
			Nodes: []TopologyNode{{ID: "a", Status: "ok"}},
			Edges: []TopologyEdge{{Source: "a", Target: "nonexistent"}},
		}),
	}
	valid := validateBlocks(blocks)
	if len(valid) != 0 {
		t.Errorf("got %d blocks, want 0 (bad edge dropped)", len(valid))
	}
}

func TestValidateBlocks_KeepsValidTopology(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockTopology, TopologyData{
			Nodes: []TopologyNode{{ID: "a", Status: "ok"}, {ID: "b", Status: "ok"}},
			Edges: []TopologyEdge{{Source: "a", Target: "b"}},
		}),
	}
	valid := validateBlocks(blocks)
	if len(valid) != 1 {
		t.Errorf("got %d blocks, want 1", len(valid))
	}
}

func TestValidateBlocks_TextAlwaysValid(t *testing.T) {
	blocks := []Block{MakeTextBlock("hello")}
	valid := validateBlocks(blocks)
	if len(valid) != 1 {
		t.Errorf("got %d blocks, want 1", len(valid))
	}
}

func TestValidateBlocks_UnmarshalFailure(t *testing.T) {
	blocks := []Block{{Type: BlockMetrics, Data: json.RawMessage(`invalid`)}}
	valid := validateBlocks(blocks)
	if len(valid) != 0 {
		t.Errorf("got %d blocks, want 0 (unmarshal fail)", len(valid))
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/ai/ -run TestValidateBlocks -v`
Expected: FAIL — `validateBlocks` not defined

**Step 3: Write the implementation**

```go
// internal/ai/validate.go
package ai

import (
	"encoding/json"
	"log/slog"
	"math"
)

// validateBlocks checks semantic invariants on blocks and returns only valid ones.
// Invalid blocks are dropped individually with a warning log.
func validateBlocks(blocks []Block) []Block {
	valid := make([]Block, 0, len(blocks))
	for _, b := range blocks {
		if err := validateBlock(b); err != nil {
			slog.Warn("dropping invalid block", "type", b.Type, "err", err)
			continue
		}
		valid = append(valid, b)
	}
	return valid
}

func validateBlock(b Block) error {
	switch b.Type {
	case BlockText:
		var d TextBlockData
		return json.Unmarshal(b.Data, &d)
	case BlockMetrics:
		return validateMetrics(b.Data)
	case BlockTable:
		return validateTable(b.Data)
	case BlockTimeseries:
		return validateTimeseries(b.Data)
	case BlockBar:
		return validateBar(b.Data)
	case BlockHeatmap:
		return validateHeatmap(b.Data)
	case BlockTopology:
		return validateTopology(b.Data)
	case BlockSankey:
		return validateSankey(b.Data)
	case BlockTraceWaterfall:
		return validateTraceWaterfall(b.Data)
	case BlockCorrelation:
		return validateCorrelation(b.Data)
	default:
		// flame_graph, dep_matrix, endpoints, tail — just check unmarshal
		var m map[string]any
		return json.Unmarshal(b.Data, &m)
	}
}
```

Then add per-type validators that check: non-empty data, finite numbers, array length consistency, and reference integrity. Each validator unmarshals into the typed struct, checks invariants, and returns an error describing the problem (or nil).

Key checks:
- `validateMetrics`: items non-empty, all values finite
- `validateTimeseries`: labels non-empty, each series.values.length == labels.length, all values finite
- `validateHeatmap`: values[i].length == buckets.length, values.length == times.length
- `validateTopology`: edges reference existing node IDs
- `validateSankey`: links reference existing node IDs
- `validateCorrelation`: each panel.values.length == times.length
- `validateTraceWaterfall`: spans non-empty, parent refs valid (if non-null)

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/ai/ -run TestValidateBlocks -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/ai/validate.go internal/ai/validate_test.go
git commit -m "feat: add block validation for semantic invariants"
```

---

### Task 3: Update Tool Registry — Remove Block Returns

**Files:**
- Modify: `internal/ai/tools.go` — `ToolHandler` returns `(string, error)`, `Execute` returns `(string, error)`
- Modify: `internal/ai/orchestrator.go` — update call sites
- Modify: `internal/ai/orchestrator_test.go` — update mock expectations

**Step 1: Change ToolHandler and Execute signatures**

In `internal/ai/tools.go`:
- `ToolHandler` changes from `func(ctx, input) (string, []Block, error)` to `func(ctx, input) (string, error)`
- `Execute()` changes from `(string, []Block, error)` to `(string, error)`
- Remove the `toolResultToBlocks` call on line 259
- Update both AI-only tool handlers (metrics, tail) to return `(string, error)`
- Update the MCP dispatch path similarly

**Step 2: Update orchestrator.go call site**

In `orchestrator.go:248`, the tool execution goroutine currently does:
```go
result, toolBlocks, err := o.tools.Execute(...)
blocks = append(blocks, toolBlocks...)
```

Change to:
```go
result, err := o.tools.Execute(...)
```

Remove the `blocks = append(blocks, toolBlocks...)` line. The `var blocks []Block` accumulator at the top of `Run()` is no longer needed from tool execution — blocks will come from the respond tool instead.

**Step 3: Update tests**

In `orchestrator_test.go`, the `TestToolRegistryMCPIntegration` test calls `registry.Execute()` with 3 return values. Update to 2.

**Step 4: Run tests**

Run: `go test ./internal/ai/ -v`
Expected: PASS (compilation succeeds, all existing tests pass)

**Step 5: Commit**

```bash
git add internal/ai/tools.go internal/ai/orchestrator.go internal/ai/orchestrator_test.go
git commit -m "refactor: remove block returns from tool execution"
```

---

### Task 4: Delete tool_blocks.go

**Files:**
- Delete: `internal/ai/tool_blocks.go`

**Step 1: Delete the file**

Remove `internal/ai/tool_blocks.go` (548 lines).

**Step 2: Run tests**

Run: `go test ./internal/ai/ -v`
Expected: PASS — nothing references `toolResultToBlocks` anymore after Task 3.

**Step 3: Commit**

```bash
git rm internal/ai/tool_blocks.go
git commit -m "refactor: delete tool_blocks.go — LLM owns visualization"
```

---

### Task 5: Wire Respond Tool into Orchestrator

This is the core change. The orchestrator adds a "respond" tool, intercepts it in the agentic loop, and uses the structured output instead of accumulated tool blocks.

**Files:**
- Modify: `internal/ai/orchestrator.go`
- Create: `internal/ai/respond_test.go`

**Step 1: Write failing tests**

```go
// internal/ai/respond_test.go
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
	_, _, err := orch.Run(context.Background(), conv, 60, "", send)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if doneEvent == nil {
		t.Fatal("no CEDone event received")
	}
	if len(doneEvent.Blocks) < 2 {
		t.Fatalf("got %d blocks, want >= 2 (text + metrics)", len(doneEvent.Blocks))
	}
	// First block should be the text
	if doneEvent.Blocks[0].Type != BlockText {
		t.Errorf("first block type = %q, want text", doneEvent.Blocks[0].Type)
	}
	// Second block should be metrics
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
	_, _, err := orch.Run(context.Background(), conv, 60, "", send)
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
	_, _, err := orch.Run(context.Background(), conv, 60, "", send)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	// Should have text + table (metrics dropped because empty)
	if len(doneEvent.Blocks) != 2 {
		t.Fatalf("got %d blocks, want 2 (text + table, empty metrics dropped)", len(doneEvent.Blocks))
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/ai/ -run TestOrchestrator_Respond -v`
Expected: FAIL — `respondToolName` not defined, mock doesn't work with current orchestrator

**Step 3: Implement the respond tool in orchestrator**

Key changes to `internal/ai/orchestrator.go`:

1. Add `respondToolName` constant and `respondToolDef()` function that uses `generateResponseSchema()`.

2. In `Run()`, modify the loop:
   - Before calling `provider.Stream()`, prepend the respond tool to the tools list.
   - After tool calls arrive, check if any is the respond tool. If so, intercept it:
     - Parse the input as `{text string, blocks []Block}`
     - Run `validateBlocks()` on the blocks
     - Prepend a TextBlock with the text
     - Send `CEDone` with the blocks
     - Return (don't execute the "respond" tool)
   - If the LLM stops without calling respond (plain text), wrap text in a TextBlock as fallback.

3. Remove the old block accumulation logic (the `var blocks []Block` that collected from tool execution is no longer needed).

4. Update `staticSystemPrompt` to include block selection guidance and respond tool instructions.

**Step 4: Run full test suite**

Run: `go test ./internal/ai/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/ai/orchestrator.go internal/ai/respond_test.go
git commit -m "feat: wire respond tool into orchestrator for schema-driven blocks"
```

---

### Task 6: Update System Prompt

**Files:**
- Modify: `internal/ai/orchestrator.go` — update `staticSystemPrompt`

**Step 1: Replace the system prompt**

The new prompt should:
- Tell the LLM to use tools for investigation, then call `respond` as its final action
- Include a block selection guide (which of the 14 types to use when)
- Include rules: data from tool results only, 1-3 blocks, most specific type wins

```go
const staticSystemPrompt = `You are the AI assistant for Fanout, an observability platform.
You help users understand system health, investigate issues, and analyze telemetry data.

## Tools

Investigation tools (use these to gather data):
status (start here) → diagnose (deep-dive) → find (search spans/logs) →
tail (live log streaming) → trace (full trace, needs trace_id) → timeline (trends) →
topology (dependency map) → compare (side-by-side) → metrics (explore metrics) →
query (custom SQL, last resort).

## Response

After gathering data, call the respond tool with:
- text: Markdown analysis. Be direct, cite specific numbers, explain root causes with next steps.
- blocks: Visualization blocks from the types below.

## Block Types

- metrics       — 2-6 KPI summary cards (throughput, latency, error rate)
- table         — tabular data, top errors, search results, comparisons
- timeseries    — trends over time (latency, throughput, error rate over time)
- bar           — ranked comparisons (top endpoints, slowest operations)
- heatmap       — latency distributions over time buckets
- trace_waterfall — single distributed trace visualization
- topology      — service dependency graph with health
- flame_graph   — aggregated span breakdowns
- sankey        — request flow between services
- dep_matrix    — NxN service health grid
- endpoints     — per-endpoint performance breakdown
- correlation   — multi-signal correlation (latency vs errors vs throughput)
- tail          — log entries

## Rules

- Block data MUST come from tool results. Never fabricate data points.
- Prefer visualization over text. Don't describe data the user can see in charts.
- 1-3 blocks per response. More is clutter.
- Most specific type wins: endpoints > table for endpoint data, trace_waterfall > table for traces.
- Do not include a text block in the blocks array — use the text field instead.`
```

**Step 2: Run tests**

Run: `go test ./internal/ai/ -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/ai/orchestrator.go
git commit -m "feat: update system prompt with block selection guide"
```

---

### Task 7: Clean Up Sanitizer and Dead Code

**Files:**
- Modify: `internal/ai/orchestrator.go` — remove HTML sanitizer from orchestrator (move to `internal/api/ui.go` if still needed there)

**Step 1: Check if sanitizer is used outside bookmarks**

The `NewSanitizer()` function in `orchestrator.go` is only called from `internal/api/ui.go:34` for bookmark sanitization. Move it to `internal/api/ui.go` so orchestrator.go doesn't import `bluemonday`.

**Step 2: Move sanitizer**

- Move `NewSanitizer()` from `orchestrator.go` to `internal/api/ui.go` (make it unexported: `newSanitizer()`)
- Remove `bluemonday` import from `orchestrator.go`
- Update `internal/api/ui.go` to use local `newSanitizer()`
- Update `internal/api/ui_test.go` to use local function

**Step 3: Run full test suite**

Run: `go test ./internal/... -v`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/ai/orchestrator.go internal/api/ui.go internal/api/ui_test.go
git commit -m "refactor: move sanitizer to api package, clean up orchestrator imports"
```

---

### Task 8: Build and Smoke Test

**Step 1: Build the binary**

Run: `just build`
Expected: Compiles successfully

**Step 2: Run full test suite**

Run: `go test ./... -count=1`
Expected: All tests pass

**Step 3: Final commit if any fixes needed**

---

## Summary of Changes

| File | Lines | Change |
|------|-------|--------|
| `internal/ai/schema_gen.go` | +~150 | NEW: JSON schema from block structs |
| `internal/ai/schema_gen_test.go` | +~50 | NEW: Schema generation tests |
| `internal/ai/validate.go` | +~120 | NEW: Block semantic validation |
| `internal/ai/validate_test.go` | +~100 | NEW: Validation tests |
| `internal/ai/respond_test.go` | +~130 | NEW: Orchestrator respond tool tests |
| `internal/ai/orchestrator.go` | ~±100 | MODIFY: Respond tool, new system prompt |
| `internal/ai/tools.go` | ~-20 | MODIFY: Remove block returns |
| `internal/ai/tool_blocks.go` | -548 | DELETE |
| `internal/api/ui.go` | ~+50 | MODIFY: Absorb sanitizer |

**Net: ~+100 lines** (new code minus deleted `tool_blocks.go`)
