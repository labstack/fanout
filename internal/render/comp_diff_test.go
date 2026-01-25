package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDiffComponent_Type(t *testing.T) {
	c := &diffComponent{}
	if c.Type() != "diff" {
		t.Errorf("Type() = %q, want %q", c.Type(), "diff")
	}
}

func TestDiffComponent_Schema(t *testing.T) {
	c := &diffComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if _, ok := schema.Properties["title"]; !ok {
		t.Error("Schema missing 'title' property")
	}
	if _, ok := schema.Properties["rows"]; !ok {
		t.Error("Schema missing 'rows' property")
	}
}

func TestDiffComponent_CSS(t *testing.T) {
	c := &diffComponent{}
	css := c.CSS()
	if css == "" {
		t.Error("CSS() returned empty string")
	}
	if !strings.Contains(css, "diff-table") {
		t.Error("CSS() should contain diff-table class")
	}
}

func TestDiffComponent_Render(t *testing.T) {
	c := &diffComponent{}

	tests := []struct {
		name      string
		config    map[string]any
		format    Format
		wantASCII string
		wantHTML  string
	}{
		{
			name: "with title and rows",
			config: map[string]any{
				"title": "Before/After Deployment",
				"rows": []map[string]any{
					{"metric": "P95 Latency", "before": "100ms", "after": "80ms"},
					{"metric": "Error Rate", "before": "2%", "after": "1%"},
				},
			},
			format:    Both,
			wantASCII: "Before/After",
			wantHTML:  "diff-table",
		},
		{
			name: "improved metrics",
			config: map[string]any{
				"rows": []map[string]any{
					{"metric": "Response Time", "before": "200", "after": "100"},
				},
			},
			format:   HTML,
			wantHTML: "diff-improved",
		},
		{
			name: "regressed metrics",
			config: map[string]any{
				"rows": []map[string]any{
					{"metric": "Errors", "before": "10", "after": "50"},
				},
			},
			format:   HTML,
			wantHTML: "diff-regressed",
		},
		{
			name: "empty rows",
			config: map[string]any{
				"title": "Empty Comparison",
				"rows":  []map[string]any{},
			},
			format:    ASCII,
			wantASCII: "Empty Comparison",
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

func TestDiffComponent_Render_InvalidJSON(t *testing.T) {
	c := &diffComponent{}
	_, err := c.Render([]byte(`{invalid`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}
