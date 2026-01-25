package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBarComponent_Type(t *testing.T) {
	c := &barComponent{}
	if c.Type() != "bar" {
		t.Errorf("Type() = %q, want %q", c.Type(), "bar")
	}
}

func TestBarComponent_Schema(t *testing.T) {
	c := &barComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if _, ok := schema.Properties["label"]; !ok {
		t.Error("Schema missing 'label' property")
	}
	if _, ok := schema.Properties["value"]; !ok {
		t.Error("Schema missing 'value' property")
	}
	if _, ok := schema.Properties["max"]; !ok {
		t.Error("Schema missing 'max' property")
	}
}

func TestBarComponent_CSS(t *testing.T) {
	c := &barComponent{}
	css := c.CSS()

	if css == "" {
		t.Error("CSS() returned empty string")
	}
}

func TestBarComponent_Render(t *testing.T) {
	c := &barComponent{}

	tests := []struct {
		name      string
		config    map[string]interface{}
		format    Format
		wantASCII string
		wantHTML  string
	}{
		{
			name: "basic bar ASCII",
			config: map[string]interface{}{
				"label": "CPU",
				"value": 50.0,
				"max":   100.0,
			},
			format:    ASCII,
			wantASCII: "CPU",
		},
		{
			name: "bar with unit",
			config: map[string]interface{}{
				"label": "Memory",
				"value": 4.0,
				"max":   8.0,
				"unit":  "GB",
			},
			format:    ASCII,
			wantASCII: "Memory",
		},
		{
			name: "HTML bar",
			config: map[string]interface{}{
				"label": "Disk",
				"value": 75.0,
				"max":   100.0,
			},
			format:   HTML,
			wantHTML: "sl-progress-bar",
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

			if tc.wantASCII != "" && !strings.Contains(out.ASCII, tc.wantASCII) {
				t.Errorf("Render() ASCII = %q, want to contain %q", out.ASCII, tc.wantASCII)
			}

			if tc.wantHTML != "" && !strings.Contains(out.HTML, tc.wantHTML) {
				t.Errorf("Render() HTML = %q, want to contain %q", out.HTML, tc.wantHTML)
			}
		})
	}
}

func TestBarComponent_Render_InvalidJSON(t *testing.T) {
	c := &barComponent{}
	_, err := c.Render([]byte(`{invalid`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}
