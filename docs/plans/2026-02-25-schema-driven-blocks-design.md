# Design: Schema-Driven Block Output

## Problem

Each tool hardcodes its own block mappings in `tool_blocks.go` (548 lines). This means:
- Adding a new visualization requires changing both the tool and the mapping
- The LLM can't choose visualizations — it's locked to whatever the tool hardcodes
- Only 4 of 14 block types are reachable (metrics, table, timeseries, topology)

## Solution

The LLM produces `{text, blocks[]}` as structured output on its final turn. A synthetic "respond" tool enforces the schema. `tool_blocks.go` is deleted.

## Architecture

### Respond Tool

A synthetic tool added to every request:

```json
{
  "name": "respond",
  "description": "Produce your final response with text and visualization blocks.",
  "input_schema": <generated from Go block structs>
}
```

The orchestrator intercepts calls to "respond" — it's never executed. The tool's input IS the structured response.

### Why respond tool (not provider-specific mechanisms)

The architecture doc describes Anthropic "respond" tool vs OpenAI `response_format.json_schema`. The respond tool approach works for both providers with zero provider changes — both already stream tool calls correctly. Provider-specific optimizations (OpenAI strict mode) can be added later.

### Flow

```
User question
    |
Orchestrator loop (iterations 1..N):
    LLM streams text -> CEToken (user sees progress)
    LLM calls tools -> execute, return JSON
    |
Final iteration:
    LLM calls "respond" with {text, blocks[]}
    Orchestrator intercepts
    ValidateBlocks() drops invalid blocks
    CEDone with blocks -> client
```

### Edge Case: No Respond Call

If the LLM stops with plain text (no respond call), text is wrapped in a single TextBlock. Graceful degradation.

## Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/ai/schema_gen.go` | NEW | Generate JSON schema from Go block structs via reflection |
| `internal/ai/orchestrator.go` | MODIFY | Add respond tool, intercept in loop, validate blocks, new system prompt |
| `internal/ai/tool_blocks.go` | DELETE | Entirely removed |
| `internal/ai/tools.go` | MODIFY | `Execute()` returns `(string, error)` — no `[]Block` |
| `internal/ai/blocks.go` | MODIFY | Add `ValidateBlocks()` for semantic checks |
| `internal/ai/provider.go` | NO CHANGE | |
| `internal/ai/anthropic.go` | NO CHANGE | |
| `internal/ai/openai.go` | NO CHANGE | |

## Schema Generation

Generated from Go structs at startup via `jsonschema.Reflect()`:

```go
type ResponseShape struct {
    Text   string  `json:"text" jsonschema:"description=Markdown narrative"`
    Blocks []Block `json:"blocks" jsonschema:"description=Visualization blocks"`
}
```

The Block type is a discriminated union on `type`, with `data` varying per type. All 14 block types are included.

## Validation

Semantic checks the schema can't express:

| Check | Applies To |
|-------|-----------|
| Finite numbers (no NaN/Infinity) | metrics, timeseries, bar, heatmap |
| Array length consistency | timeseries (values == labels), heatmap, correlation |
| Non-empty data | all blocks |
| Internal references | topology (edge refs valid nodes), sankey, trace_waterfall |

Invalid blocks are dropped individually. Text and remaining blocks still delivered.

## System Prompt

New section added to static system prompt:

- Call `respond` as final action with `text` + `blocks`
- Block selection guide (14 types with "use when" guidance)
- Rules: data from tool results only, 1-3 blocks, most specific type wins
