package ai

import (
	"encoding/json"
	"fmt"
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
	if _, ok := props["blocks"]; ok {
		t.Error("schema should not have 'blocks' property")
	}

	required, ok := m["required"].([]any)
	if !ok {
		t.Fatal("schema missing required array")
	}
	found := map[string]bool{}
	for _, r := range required {
		found[r.(string)] = true
	}
	if !found["text"] {
		t.Errorf("required = %v, want text", required)
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

func TestBlockTypeRegistry_ReflectSchemasRemainValid(t *testing.T) {
	for _, entry := range BlockTypeRegistry {
		schema := reflectSchema(entry.Data)
		if schema["type"] != "object" {
			t.Errorf("%s schema type = %v, want object", entry.Type, schema["type"])
		}
		props, _ := schema["properties"].(map[string]any)
		if props == nil {
			t.Errorf("%s schema missing properties", entry.Type)
		}
		if len(props) == 0 {
			t.Errorf("%s schema has no properties", entry.Type)
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
