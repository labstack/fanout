package render

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Component defines the interface for all renderable components
type Component interface {
	// Type returns the component type identifier (e.g., "metric", "table")
	Type() string

	// Schema returns JSON Schema for config validation
	Schema() *Schema

	// CSS returns component-specific styles
	CSS() string

	// Render produces output in the specified format
	Render(config json.RawMessage, format Format) (Output, error)
}

// Schema defines component configuration schema
type Schema struct {
	Description string              `json:"description"`
	Properties  map[string]Property `json:"properties"`
	Required    []string            `json:"required,omitempty"`
}

// Property defines a schema property
type Property struct {
	Type        string              `json:"type"`
	Description string              `json:"description,omitempty"`
	Enum        []string            `json:"enum,omitempty"`
	Items       *Property           `json:"items,omitempty"`
	Properties  map[string]Property `json:"properties,omitempty"`
	Default     any                 `json:"default,omitempty"`
}

// Registry manages component registration and lookup
type Registry struct {
	mu         sync.RWMutex
	components map[string]Component
}

// Global registry instance
var registry = &Registry{
	components: make(map[string]Component),
}

// Register adds a component to the registry
func Register(c Component) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.components[c.Type()] = c
}

// Get retrieves a component by type
func Get(typ string) (Component, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	c, ok := registry.components[typ]
	return c, ok
}

// Types returns all registered component types
func Types() []string {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	types := make([]string, 0, len(registry.components))
	for t := range registry.components {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

// AllCSS returns combined CSS from all components
func AllCSS() string {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	var sb strings.Builder
	types := Types()
	for _, t := range types {
		if c, ok := registry.components[t]; ok {
			if css := c.CSS(); css != "" {
				sb.WriteString("/* " + t + " */\n")
				sb.WriteString(css)
				sb.WriteString("\n\n")
			}
		}
	}
	return sb.String()
}

// ToolDescription generates MCP tool description from all schemas
func ToolDescription() string {
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString("Section types and their config:\n")

	types := Types()
	for _, t := range types {
		c := registry.components[t]
		schema := c.Schema()
		sb.WriteString("- " + t + ": " + schemaToExample(t, schema) + "\n")
	}
	return sb.String()
}

// schemaToExample generates example JSON from schema
func schemaToExample(typ string, s *Schema) string {
	if s == nil {
		return "{}"
	}
	parts := make([]string, 0, len(s.Properties))
	for name, prop := range s.Properties {
		val := exampleValue(prop)
		parts = append(parts, `"`+name+`": `+val)
	}
	sort.Strings(parts)
	return "{" + strings.Join(parts, ", ") + "}"
}

func exampleValue(p Property) string {
	switch p.Type {
	case "string":
		if len(p.Enum) > 0 {
			return `"` + p.Enum[0] + `"`
		}
		return `"..."`
	case "number", "integer":
		return "0"
	case "boolean":
		return "false"
	case "array":
		if p.Items != nil {
			return "[" + exampleValue(*p.Items) + "]"
		}
		return "[]"
	case "object":
		return "{...}"
	default:
		return "null"
	}
}

// RenderSection renders a section using the registry
func RenderSection(typ string, config json.RawMessage, format Format) (Output, error) {
	c, ok := Get(typ)
	if !ok {
		return Output{}, fmt.Errorf("unknown component type: %s", typ)
	}
	return c.Render(config, format)
}

// Validate checks config against component schema
func Validate(typ string, config json.RawMessage) error {
	c, ok := Get(typ)
	if !ok {
		return fmt.Errorf("unknown component type: %s", typ)
	}
	schema := c.Schema()
	if schema == nil {
		return nil
	}
	return validateConfig(config, schema)
}

func validateConfig(config json.RawMessage, schema *Schema) error {
	var data map[string]any
	if err := json.Unmarshal(config, &data); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}

	// Check required fields
	for _, req := range schema.Required {
		if _, ok := data[req]; !ok {
			return fmt.Errorf("missing required field: %s", req)
		}
	}

	// Type check properties
	for name, prop := range schema.Properties {
		val, ok := data[name]
		if !ok {
			continue
		}
		if err := validateType(name, val, prop); err != nil {
			return err
		}
	}
	return nil
}

func validateType(name string, val any, prop Property) error {
	switch prop.Type {
	case "string":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("field %s: expected string", name)
		}
	case "number":
		switch val.(type) {
		case float64, int, int64:
			// ok
		default:
			return fmt.Errorf("field %s: expected number", name)
		}
	case "integer":
		switch v := val.(type) {
		case float64:
			if v != float64(int(v)) {
				return fmt.Errorf("field %s: expected integer", name)
			}
		case int, int64:
			// ok
		default:
			return fmt.Errorf("field %s: expected integer", name)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("field %s: expected boolean", name)
		}
	case "array":
		if _, ok := val.([]any); !ok {
			return fmt.Errorf("field %s: expected array", name)
		}
	case "object":
		if _, ok := val.(map[string]any); !ok {
			return fmt.Errorf("field %s: expected object", name)
		}
	}
	return nil
}
