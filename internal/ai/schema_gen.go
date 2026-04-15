package ai

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// BlockTypeEntry maps a BlockType constant to its Go data struct.
type BlockTypeEntry struct {
	Type BlockType
	Data any
}

// BlockTypeRegistry maps each BlockType constant to its corresponding Go data struct.
// Used by generateResponseSchema and cmd/genblocks for TypeScript generation.
var BlockTypeRegistry = []BlockTypeEntry{
	{BlockText, TextBlockData{}},
	{BlockMetrics, MetricsBlockData{}},
	{BlockTable, TableBlockData{}},
	{BlockTimeseries, TimeseriesBlockData{}},
	{BlockBar, BarBlockData{}},
	{BlockHeatmap, HeatmapBlockData{}},
	{BlockTraceWaterfall, TraceWaterfallData{}},
	{BlockTopology, TopologyData{}},
	{BlockFlameGraph, FlameGraphData{}},
	{BlockSankey, SankeyData{}},
	{BlockDepMatrix, DepMatrixData{}},
	{BlockEndpoints, EndpointsData{}},
	{BlockCorrelation, CorrelationData{}},
	{BlockLogs, LogsBlockData{}},
	{BlockComparison, ComparisonData{}},
}

var (
	responseSchemaOnce  sync.Once
	responseSchemaJSON  json.RawMessage
	dashboardSchemaOnce sync.Once
	dashboardSchemaJSON json.RawMessage
)

// generateResponseSchema returns a cached JSON Schema describing the "respond" tool's
// input: {text: string}. Visualization blocks are built deterministically from
// tool results, not emitted by the LLM.
func generateResponseSchema() json.RawMessage {
	responseSchemaOnce.Do(func() {
		schema := map[string]any{
			"type":                 "object",
			"required":             []string{"text"},
			"additionalProperties": false,
			"properties": map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "Markdown text response analyzing the tool results for the user.",
				},
			},
		}

		var err error
		responseSchemaJSON, err = json.Marshal(schema)
		if err != nil {
			panic(fmt.Sprintf("ai: failed to marshal response schema: %v", err))
		}
	})
	return responseSchemaJSON
}

// generateDashboardSchema returns a cached JSON Schema describing the
// dashboard-specific structured AI response. Blocks remain deterministic and
// are appended from tool results rather than emitted by the model.
func generateDashboardSchema() json.RawMessage {
	dashboardSchemaOnce.Do(func() {
		schema := map[string]any{
			"type":                 "object",
			"required":             []string{"headline", "brief", "actions"},
			"additionalProperties": false,
			"properties": map[string]any{
				"headline": map[string]any{
					"type":        "string",
					"description": "Short dashboard headline summarizing the current situation.",
				},
				"brief": map[string]any{
					"type":        "string",
					"description": "Concise markdown briefing describing what changed, what matters, and what to watch.",
				},
				"actions": map[string]any{
					"type":        "array",
					"description": "Concrete next dashboard actions the user can take.",
					"items": map[string]any{
						"type":                 "object",
						"required":             []string{"label", "prompt", "kind"},
						"additionalProperties": false,
						"properties": map[string]any{
							"label": map[string]any{
								"type":        "string",
								"description": "Short user-facing action label.",
							},
							"prompt": map[string]any{
								"type":        "string",
								"description": "Prompt to send into the investigation chat when this action is clicked.",
							},
							"kind": map[string]any{
								"type":        "string",
								"description": "Action category such as explain, drill, compare, or alert.",
							},
						},
					},
				},
			},
		}

		var err error
		dashboardSchemaJSON, err = json.Marshal(schema)
		if err != nil {
			panic(fmt.Sprintf("ai: failed to marshal dashboard schema: %v", err))
		}
	})
	return dashboardSchemaJSON
}

// reflectSchema produces a JSON Schema object definition from a Go struct value.
// It reads json tags for field names and handles omitempty/pointer optionality.
func reflectSchema(v any) map[string]any {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return reflectTypeSchema(t)
}

// reflectTypeSchema recursively generates a JSON Schema for the given reflect.Type.
func reflectTypeSchema(t reflect.Type) map[string]any {
	// Unwrap pointer types.
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}

	case reflect.Bool:
		return map[string]any{"type": "boolean"}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}

	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}

	case reflect.Slice:
		elem := t.Elem()
		return map[string]any{
			"type":  "array",
			"items": reflectTypeSchema(elem),
		}

	case reflect.Map:
		// map[string]any → object with no fixed properties.
		return map[string]any{"type": "object"}

	case reflect.Struct:
		return reflectStructSchema(t)

	case reflect.Interface:
		// any / interface{} → no type constraint.
		return map[string]any{}

	default:
		return map[string]any{"type": "string"}
	}
}

// reflectStructSchema generates a JSON Schema for a struct type, reading json tags.
func reflectStructSchema(t reflect.Type) map[string]any {
	props := map[string]any{}
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}

		name, opts := parseJSONTag(tag)
		if name == "" {
			name = field.Name
		}

		isOptional := opts.contains("omitempty") || field.Type.Kind() == reflect.Ptr

		fieldType := field.Type
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}

		props[name] = reflectTypeSchema(fieldType)

		if !isOptional {
			required = append(required, name)
		}
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

// jsonTagOptions holds comma-separated options from a json struct tag.
type jsonTagOptions string

func (o jsonTagOptions) contains(opt string) bool {
	for o != "" {
		var name string
		i := strings.Index(string(o), ",")
		if i >= 0 {
			name, o = string(o[:i]), jsonTagOptions(o[i+1:])
		} else {
			name, o = string(o), ""
		}
		if name == opt {
			return true
		}
	}
	return false
}

// parseJSONTag splits a json struct tag into the field name and remaining options.
func parseJSONTag(tag string) (string, jsonTagOptions) {
	i := strings.Index(tag, ",")
	if i == -1 {
		return tag, ""
	}
	return tag[:i], jsonTagOptions(tag[i+1:])
}
