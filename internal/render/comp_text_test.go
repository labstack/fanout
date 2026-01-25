package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTextComponent_Type(t *testing.T) {
	c := &textComponent{}
	if c.Type() != "text" {
		t.Errorf("Type() = %q, want %q", c.Type(), "text")
	}
}

func TestTextComponent_Schema(t *testing.T) {
	c := &textComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if _, ok := schema.Properties["content"]; !ok {
		t.Error("Schema missing 'content' property")
	}
}

func TestTextComponent_Render(t *testing.T) {
	c := &textComponent{}

	tests := []struct {
		name      string
		config    map[string]string
		format    Format
		wantASCII string
		wantHTML  string
	}{
		{
			name:      "plain text ASCII",
			config:    map[string]string{"content": "Hello World"},
			format:    ASCII,
			wantASCII: "Hello World",
		},
		{
			name:      "bold text ASCII",
			config:    map[string]string{"content": "Important", "style": "bold"},
			format:    ASCII,
			wantASCII: "Important",
		},
		{
			name:     "plain text HTML",
			config:   map[string]string{"content": "Hello World"},
			format:   HTML,
			wantHTML: "Hello World",
		},
		{
			name:     "bold text HTML",
			config:   map[string]string{"content": "Important", "style": "bold"},
			format:   HTML,
			wantHTML: "sl-alert",
		},
		{
			name:     "warning text HTML",
			config:   map[string]string{"content": "Warning msg", "style": "warning"},
			format:   HTML,
			wantHTML: "warning",
		},
		{
			name:     "error text HTML",
			config:   map[string]string{"content": "Error msg", "style": "error"},
			format:   HTML,
			wantHTML: "danger",
		},
		{
			name:     "dim text HTML",
			config:   map[string]string{"content": "Dim msg", "style": "dim"},
			format:   HTML,
			wantHTML: "text-dim",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfgJSON, _ := json.Marshal(tc.config)
			out, err := c.Render(cfgJSON, tc.format)
			if err != nil {
				t.Errorf("Render() error = %v", err)
				return
			}

			if tc.wantASCII != "" && out.ASCII != tc.wantASCII {
				t.Errorf("Render() ASCII = %q, want %q", out.ASCII, tc.wantASCII)
			}

			if tc.wantHTML != "" && !strings.Contains(out.HTML, tc.wantHTML) {
				t.Errorf("Render() HTML = %q, want to contain %q", out.HTML, tc.wantHTML)
			}
		})
	}
}

func TestTextComponent_Render_InvalidJSON(t *testing.T) {
	c := &textComponent{}
	_, err := c.Render([]byte(`{invalid`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}
