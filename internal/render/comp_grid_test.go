package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGridComponent_Type(t *testing.T) {
	c := &gridComponent{}
	if c.Type() != "grid" {
		t.Errorf("Type() = %q, want %q", c.Type(), "grid")
	}
}

func TestGridComponent_Schema(t *testing.T) {
	c := &gridComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if _, ok := schema.Properties["cols"]; !ok {
		t.Error("Schema missing 'cols' property")
	}
	if _, ok := schema.Properties["items"]; !ok {
		t.Error("Schema missing 'items' property")
	}
}

func TestGridComponent_Render(t *testing.T) {
	c := &gridComponent{}

	tests := []struct {
		name      string
		config    map[string]any
		format    Format
		wantASCII string
		wantHTML  string
	}{
		{
			name: "empty grid ASCII",
			config: map[string]any{
				"title": "Stats",
				"cols":  2,
				"items": []map[string]any{},
			},
			format:    ASCII,
			wantASCII: "Stats",
		},
		{
			name: "empty grid HTML",
			config: map[string]any{
				"cols":  2,
				"items": []map[string]any{},
			},
			format:   HTML,
			wantHTML: "grid-2",
		},
		{
			name: "cols defaults to 2",
			config: map[string]any{
				"cols":  0,
				"items": []map[string]any{},
			},
			format:   HTML,
			wantHTML: "grid-2",
		},
		{
			name: "cols 3",
			config: map[string]any{
				"cols":  3,
				"items": []map[string]any{},
			},
			format:   HTML,
			wantHTML: "grid-3",
		},
		{
			name: "cols 4",
			config: map[string]any{
				"cols":  4,
				"items": []map[string]any{},
			},
			format:   HTML,
			wantHTML: "grid-4",
		},
		{
			name: "cols > 4 defaults to 2",
			config: map[string]any{
				"cols":  5,
				"items": []map[string]any{},
			},
			format:   HTML,
			wantHTML: "grid-2",
		},
		{
			name: "with nested items",
			config: map[string]any{
				"cols": 2,
				"items": []map[string]any{
					{"type": "badge", "config": map[string]any{"label": "OK", "status": "healthy"}},
				},
			},
			format:   HTML,
			wantHTML: "grid",
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

func TestGridComponent_Render_InvalidJSON(t *testing.T) {
	c := &gridComponent{}
	_, err := c.Render([]byte(`{invalid`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}

func TestGridComponent_CSS(t *testing.T) {
	c := &gridComponent{}
	css := c.CSS()
	if css == "" {
		t.Error("CSS() should not be empty")
	}
	if !strings.Contains(css, ".grid") {
		t.Error("CSS() should contain .grid class")
	}
}
