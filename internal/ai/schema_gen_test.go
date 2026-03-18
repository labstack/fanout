package ai

import (
	"encoding/json"
	"fmt"
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
		"dep_matrix", "endpoints", "correlation", "logs",
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

// TestSchemaCoversAllBlockTypes verifies the generated JSON schema has a variant
// for every BlockType in the registry and that each variant's data schema matches
// the Go struct fields — catching drift between structs and schema at test time.
func TestSchemaCoversAllBlockTypes(t *testing.T) {
	schema := generateResponseSchema()
	var m map[string]any
	if err := json.Unmarshal(schema, &m); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	// Extract oneOf variants from blocks.items
	props := m["properties"].(map[string]any)
	blocks := props["blocks"].(map[string]any)
	items := blocks["items"].(map[string]any)
	oneOf := items["oneOf"].([]any)

	// Build map of type→variant from schema
	schemaVariants := map[string]map[string]any{}
	for _, v := range oneOf {
		variant := v.(map[string]any)
		vProps := variant["properties"].(map[string]any)
		typeObj := vProps["type"].(map[string]any)
		constVal := typeObj["const"].(string)
		schemaVariants[constVal] = vProps["data"].(map[string]any)
	}

	// Check every registry entry has a matching variant with correct fields
	for _, entry := range BlockTypeRegistry {
		typeName := string(entry.Type)
		dataSchema, ok := schemaVariants[typeName]
		if !ok {
			t.Errorf("schema missing variant for block type %q", typeName)
			continue
		}

		// Compare fields: reflectSchema vs schema variant
		expected := reflectSchema(entry.Data)
		expectedProps, _ := expected["properties"].(map[string]any)
		schemaProps, _ := dataSchema["properties"].(map[string]any)

		if expectedProps == nil && schemaProps == nil {
			continue
		}

		// Check no missing fields
		for fieldName := range expectedProps {
			if _, ok := schemaProps[fieldName]; !ok {
				t.Errorf("block type %q: schema missing field %q", typeName, fieldName)
			}
		}
		// Check no extra fields
		for fieldName := range schemaProps {
			if _, ok := expectedProps[fieldName]; !ok {
				t.Errorf("block type %q: schema has extra field %q", typeName, fieldName)
			}
		}
	}

	// Check no extra variants in schema
	registryTypes := map[string]bool{}
	for _, entry := range BlockTypeRegistry {
		registryTypes[string(entry.Type)] = true
	}
	for typeName := range schemaVariants {
		if !registryTypes[typeName] {
			t.Errorf("schema has variant %q not in BlockTypeRegistry", typeName)
		}
	}
}

// TestStrictSchemaValid verifies that strictifySchema(generateResponseSchema()) produces
// valid JSON where all objects have additionalProperties: false and all properties
// appear in required.
func TestStrictSchemaValid(t *testing.T) {
	schema := generateResponseSchema()
	strict, _ := strictifySchema(schema)

	var m map[string]any
	if err := json.Unmarshal(strict, &m); err != nil {
		t.Fatalf("strict schema is not valid JSON: %v", err)
	}

	// Recursively verify all objects
	var check func(path string, node any)
	check = func(path string, node any) {
		obj, ok := node.(map[string]any)
		if !ok {
			return
		}

		typ, _ := obj["type"].(string)
		if typ == "object" {
			if props, ok := obj["properties"].(map[string]any); ok && len(props) > 0 {
				if obj["additionalProperties"] != false {
					t.Errorf("%s: missing additionalProperties: false", path)
				}
				req, _ := obj["required"].([]any)
				reqSet := map[string]bool{}
				for _, r := range req {
					if s, ok := r.(string); ok {
						reqSet[s] = true
					}
				}
				for name := range props {
					if !reqSet[name] {
						t.Errorf("%s: property %q not in required", path, name)
					}
				}

				// Recurse into properties
				for name, prop := range props {
					check(path+"."+name, prop)
				}
			}
		}

		// Recurse into array items
		if typ == "array" {
			if items, ok := obj["items"]; ok {
				check(path+".items", items)
			}
		}

		// Recurse into oneOf/anyOf
		if oneOf, ok := obj["oneOf"].([]any); ok {
			for i, v := range oneOf {
				check(fmt.Sprintf("%s.oneOf[%d]", path, i), v)
			}
		}
		if anyOf, ok := obj["anyOf"].([]any); ok {
			for i, v := range anyOf {
				check(fmt.Sprintf("%s.anyOf[%d]", path, i), v)
			}
		}
	}

	check("root", m)

	// Also verify the result round-trips cleanly
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("strict schema cannot be re-marshaled: %v", err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(out, &roundTrip); err != nil {
		t.Fatalf("strict schema round-trip failed: %v", err)
	}

}
