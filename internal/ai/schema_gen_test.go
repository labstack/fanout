package ai

import (
	"encoding/json"
	"strings"
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

	types := []string{
		"text", "metrics", "table", "timeseries", "bar", "heatmap",
		"trace_waterfall", "topology", "flame_graph", "sankey",
		"dep_matrix", "endpoints", "correlation", "tail",
	}
	for _, bt := range types {
		if !strings.Contains(raw, `"`+bt+`"`) {
			t.Errorf("schema missing block type %q", bt)
		}
	}
}

func TestReflectSchema_SimpleStruct(t *testing.T) {
	type S struct {
		Name string  `json:"name"`
		Val  float64 `json:"val"`
	}
	s := reflectSchema(S{})
	if s["type"] != "object" {
		t.Errorf("type = %v, want object", s["type"])
	}
	props := s["properties"].(map[string]any)
	if _, ok := props["name"]; !ok {
		t.Error("missing 'name' property")
	}
	if _, ok := props["val"]; !ok {
		t.Error("missing 'val' property")
	}
}

func TestReflectSchema_Slice(t *testing.T) {
	type S struct {
		Items []string `json:"items"`
	}
	s := reflectSchema(S{})
	props := s["properties"].(map[string]any)
	items := props["items"].(map[string]any)
	if items["type"] != "array" {
		t.Errorf("items type = %v, want array", items["type"])
	}
}

func TestReflectSchema_OptionalField(t *testing.T) {
	type S struct {
		Required string  `json:"required"`
		Optional string  `json:"optional,omitempty"`
		Pointer  *string `json:"pointer"`
	}
	s := reflectSchema(S{})
	req := s["required"].([]string)
	found := map[string]bool{}
	for _, r := range req {
		found[r] = true
	}
	if !found["required"] {
		t.Error("'required' field should be in required list")
	}
	if found["optional"] {
		t.Error("'optional' (omitempty) should NOT be in required list")
	}
	if found["pointer"] {
		t.Error("'pointer' (*string) should NOT be in required list")
	}
}
