package ai

import (
	"encoding/json"
	"testing"
)

func TestStrictifySchema_AddsAdditionalProperties(t *testing.T) {
	input := `{
		"type": "object",
		"properties": {
			"name": {"type": "string"}
		},
		"required": ["name"]
	}`

	result, _ := strictifySchema(json.RawMessage(input))
	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if m["additionalProperties"] != false {
		t.Errorf("additionalProperties = %v, want false", m["additionalProperties"])
	}
}

func TestStrictifySchema_NestedObjects(t *testing.T) {
	input := `{
		"type": "object",
		"required": ["child"],
		"properties": {
			"child": {
				"type": "object",
				"required": ["id"],
				"properties": {
					"id": {"type": "string"}
				}
			}
		}
	}`

	result, _ := strictifySchema(json.RawMessage(input))
	var m map[string]any
	json.Unmarshal(result, &m)

	// Top-level
	if m["additionalProperties"] != false {
		t.Error("top-level missing additionalProperties: false")
	}

	// Nested
	props := m["properties"].(map[string]any)
	child := props["child"].(map[string]any)
	if child["additionalProperties"] != false {
		t.Error("nested object missing additionalProperties: false")
	}
}

func TestStrictifySchema_OptionalFieldsNullable(t *testing.T) {
	input := `{
		"type": "object",
		"required": ["name"],
		"properties": {
			"name": {"type": "string"},
			"color": {"type": "string"}
		}
	}`

	result, _ := strictifySchema(json.RawMessage(input))
	var m map[string]any
	json.Unmarshal(result, &m)

	// All properties must be required
	req := m["required"].([]any)
	reqSet := map[string]bool{}
	for _, r := range req {
		reqSet[r.(string)] = true
	}
	if !reqSet["name"] || !reqSet["color"] {
		t.Errorf("required = %v, want both name and color", req)
	}

	// "color" was optional, should be wrapped in anyOf with null
	props := m["properties"].(map[string]any)
	color := props["color"].(map[string]any)
	anyOf, ok := color["anyOf"].([]any)
	if !ok {
		t.Fatalf("optional field 'color' not wrapped in anyOf: %v", color)
	}
	if len(anyOf) != 2 {
		t.Fatalf("anyOf has %d variants, want 2", len(anyOf))
	}

	// Check that null is one of the variants
	hasNull := false
	for _, v := range anyOf {
		if vm, ok := v.(map[string]any); ok && vm["type"] == "null" {
			hasNull = true
		}
	}
	if !hasNull {
		t.Error("anyOf missing null variant")
	}

	// "name" was already required, should NOT be wrapped
	name := props["name"].(map[string]any)
	if _, ok := name["anyOf"]; ok {
		t.Error("required field 'name' should not be wrapped in anyOf")
	}
}

func TestStrictifySchema_PreservesRequiredAsIs(t *testing.T) {
	input := `{
		"type": "object",
		"required": ["id", "value"],
		"properties": {
			"id": {"type": "string"},
			"value": {"type": "number"}
		}
	}`

	result, _ := strictifySchema(json.RawMessage(input))
	var m map[string]any
	json.Unmarshal(result, &m)

	props := m["properties"].(map[string]any)
	// Both were required — neither should be wrapped
	id := props["id"].(map[string]any)
	if _, ok := id["anyOf"]; ok {
		t.Error("already-required field 'id' should not be wrapped")
	}
	value := props["value"].(map[string]any)
	if _, ok := value["anyOf"]; ok {
		t.Error("already-required field 'value' should not be wrapped")
	}
}

func TestStrictifySchema_ArrayItems(t *testing.T) {
	input := `{
		"type": "array",
		"items": {
			"type": "object",
			"required": ["name"],
			"properties": {
				"name": {"type": "string"}
			}
		}
	}`

	result, _ := strictifySchema(json.RawMessage(input))
	var m map[string]any
	json.Unmarshal(result, &m)

	items := m["items"].(map[string]any)
	if items["additionalProperties"] != false {
		t.Error("array items object missing additionalProperties: false")
	}
}

func TestStrictifySchema_OneOfVariants(t *testing.T) {
	input := `{
		"type": "array",
		"items": {
			"oneOf": [
				{
					"type": "object",
					"required": ["type"],
					"properties": {
						"type": {"type": "string", "const": "text"}
					}
				},
				{
					"type": "object",
					"required": ["type"],
					"properties": {
						"type": {"type": "string", "const": "bar"}
					}
				}
			]
		}
	}`

	result, _ := strictifySchema(json.RawMessage(input))
	var m map[string]any
	json.Unmarshal(result, &m)

	items := m["items"].(map[string]any)
	oneOf := items["oneOf"].([]any)
	for i, v := range oneOf {
		variant := v.(map[string]any)
		if variant["additionalProperties"] != false {
			t.Errorf("oneOf[%d] missing additionalProperties: false", i)
		}
	}
}

func TestStrictifySchema_ResponseSchema(t *testing.T) {
	// Run against the actual generated response schema
	schema := generateResponseSchema()
	strict, _ := strictifySchema(schema)

	var m map[string]any
	if err := json.Unmarshal(strict, &m); err != nil {
		t.Fatalf("strict schema is not valid JSON: %v", err)
	}

	// Top-level must have additionalProperties: false
	if m["additionalProperties"] != false {
		t.Error("top-level missing additionalProperties: false")
	}

	props := m["properties"].(map[string]any)
	if _, ok := props["blocks"]; ok {
		t.Fatal("response schema should not include blocks")
	}
	text := props["text"].(map[string]any)
	if text["type"] != "string" {
		t.Errorf("text property type = %v, want string", text["type"])
	}
}

func TestStrictifySchema_InvalidJSON(t *testing.T) {
	input := json.RawMessage(`{invalid`)
	result, err := strictifySchema(input)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
	// Should return input unchanged
	if string(result) != string(input) {
		t.Errorf("invalid JSON should be returned as-is")
	}
}
