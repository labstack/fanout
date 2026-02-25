package ai

import (
	"encoding/json"
	"log/slog"
	"sort"
)

// strictifySchema transforms a JSON Schema to satisfy OpenAI's strict mode requirements:
//   - Objects with properties get additionalProperties: false (property-less objects like map[string]any are left open)
//   - All properties are listed in required
//   - Formerly-optional properties are wrapped in anyOf: [{original}, {type: "null"}]
//
// The input and output are both raw JSON schema bytes.
func strictifySchema(schema json.RawMessage) (json.RawMessage, error) {
	var node any
	if err := json.Unmarshal(schema, &node); err != nil {
		slog.Error("strictifySchema: failed to unmarshal schema", "err", err)
		return schema, err
	}
	result := strictifyNode(node)
	out, err := json.Marshal(result)
	if err != nil {
		slog.Error("strictifySchema: failed to remarshal schema", "err", err)
		return schema, err
	}
	return out, nil
}

func strictifyNode(node any) any {
	obj, ok := node.(map[string]any)
	if !ok {
		return node
	}

	result := make(map[string]any, len(obj))
	for k, v := range obj {
		result[k] = v
	}

	typ, _ := result["type"].(string)

	if typ == "object" {
		// Only add additionalProperties: false when properties are defined.
		// Property-less objects (e.g. map[string]any → {"type":"object"}) must
		// remain open or OpenAI rejects any keys.
		if props, ok := result["properties"].(map[string]any); ok {
			result["additionalProperties"] = false
			// Collect existing required set
			existingRequired := map[string]bool{}
			if req, ok := result["required"].([]any); ok {
				for _, r := range req {
					if s, ok := r.(string); ok {
						existingRequired[s] = true
					}
				}
			}

			// All property names become required
			allRequired := make([]string, 0, len(props))
			newProps := make(map[string]any, len(props))

			for name, schema := range props {
				// Recurse into the property schema
				strict := strictifyNode(schema)

				if !existingRequired[name] {
					// Wrap optional property in anyOf with null
					newProps[name] = wrapNullable(strict)
				} else {
					newProps[name] = strict
				}
				allRequired = append(allRequired, name)
			}

			result["properties"] = newProps
			if len(allRequired) > 0 {
				// Sort for deterministic output
				sort.Strings(allRequired)
				result["required"] = allRequired
			}
		}
	}

	// Recurse into array items
	if typ == "array" {
		if items, ok := result["items"]; ok {
			result["items"] = strictifyNode(items)
		}
	}

	// Recurse into oneOf variants
	if oneOf, ok := result["oneOf"].([]any); ok {
		newOneOf := make([]any, len(oneOf))
		for i, v := range oneOf {
			newOneOf[i] = strictifyNode(v)
		}
		result["oneOf"] = newOneOf
	}

	// Recurse into anyOf variants
	if anyOf, ok := result["anyOf"].([]any); ok {
		newAnyOf := make([]any, len(anyOf))
		for i, v := range anyOf {
			newAnyOf[i] = strictifyNode(v)
		}
		result["anyOf"] = newAnyOf
	}

	// Recurse into allOf variants
	if allOf, ok := result["allOf"].([]any); ok {
		newAllOf := make([]any, len(allOf))
		for i, v := range allOf {
			newAllOf[i] = strictifyNode(v)
		}
		result["allOf"] = newAllOf
	}

	return result
}

// wrapNullable wraps a schema in anyOf: [{schema}, {type: "null"}] for OpenAI strict mode.
// If the schema is already nullable or is an anyOf, it adds null to the existing variants.
func wrapNullable(schema any) any {
	obj, ok := schema.(map[string]any)
	if !ok {
		return map[string]any{
			"anyOf": []any{schema, map[string]any{"type": "null"}},
		}
	}

	// If already has anyOf, append null variant if not present
	if existing, ok := obj["anyOf"].([]any); ok {
		for _, v := range existing {
			if m, ok := v.(map[string]any); ok {
				if m["type"] == "null" {
					return obj // already nullable
				}
			}
		}
		result := make(map[string]any, len(obj))
		for k, v := range obj {
			result[k] = v
		}
		extended := make([]any, len(existing), len(existing)+1)
		copy(extended, existing)
		result["anyOf"] = append(extended, map[string]any{"type": "null"})
		return result
	}

	return map[string]any{
		"anyOf": []any{obj, map[string]any{"type": "null"}},
	}
}
