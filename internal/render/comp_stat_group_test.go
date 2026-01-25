package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStatGroupComponent_Type(t *testing.T) {
	c := &statGroupComponent{}
	if c.Type() != "stat-group" {
		t.Errorf("Type() = %q, want %q", c.Type(), "stat-group")
	}
}

func TestStatGroupComponent_Schema(t *testing.T) {
	c := &statGroupComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if _, ok := schema.Properties["title"]; !ok {
		t.Error("Schema missing 'title' property")
	}
	if _, ok := schema.Properties["stats"]; !ok {
		t.Error("Schema missing 'stats' property")
	}
}

func TestStatGroupComponent_CSS(t *testing.T) {
	c := &statGroupComponent{}
	css := c.CSS()
	if css == "" {
		t.Error("CSS() returned empty string")
	}
	if !strings.Contains(css, "stat-group") {
		t.Error("CSS() should contain stat-group class")
	}
}

func TestStatGroupComponent_Render(t *testing.T) {
	c := &statGroupComponent{}

	tests := []struct {
		name      string
		config    map[string]any
		format    Format
		wantASCII string
		wantHTML  string
	}{
		{
			name: "with title and stats",
			config: map[string]any{
				"title": "Service Overview",
				"stats": []map[string]any{
					{"label": "Requests", "value": "1.2M"},
					{"label": "Errors", "value": "0.1%"},
					{"label": "P95", "value": "45ms"},
				},
			},
			format:    Both,
			wantASCII: "Service Overview",
			wantHTML:  "stat-group",
		},
		{
			name: "without title",
			config: map[string]any{
				"stats": []map[string]any{
					{"label": "CPU", "value": "45%"},
					{"label": "Memory", "value": "72%"},
				},
			},
			format:   HTML,
			wantHTML: "stat-grid",
		},
		{
			name: "empty stats",
			config: map[string]any{
				"title": "Empty",
				"stats": []map[string]any{},
			},
			format:    ASCII,
			wantASCII: "Empty",
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

func TestStatGroupComponent_Render_InvalidJSON(t *testing.T) {
	c := &statGroupComponent{}
	_, err := c.Render([]byte(`{invalid`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}
