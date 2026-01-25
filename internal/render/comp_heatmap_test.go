package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHeatmapComponent_Type(t *testing.T) {
	c := &heatmapComponent{}
	if c.Type() != "heatmap" {
		t.Errorf("Type() = %q, want %q", c.Type(), "heatmap")
	}
}

func TestHeatmapComponent_Schema(t *testing.T) {
	c := &heatmapComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if _, ok := schema.Properties["title"]; !ok {
		t.Error("Schema missing 'title' property")
	}
	if _, ok := schema.Properties["x_labels"]; !ok {
		t.Error("Schema missing 'x_labels' property")
	}
	if _, ok := schema.Properties["y_labels"]; !ok {
		t.Error("Schema missing 'y_labels' property")
	}
	if _, ok := schema.Properties["values"]; !ok {
		t.Error("Schema missing 'values' property")
	}
}

func TestHeatmapComponent_CSS(t *testing.T) {
	c := &heatmapComponent{}
	// CSS returns empty string, uses chart class
	_ = c.CSS()
}

func TestHeatmapComponent_Render(t *testing.T) {
	c := &heatmapComponent{}

	tests := []struct {
		name      string
		config    map[string]any
		format    Format
		wantASCII string
		wantHTML  string
	}{
		{
			name: "with data",
			config: map[string]any{
				"title":    "Error Rates by Service/Hour",
				"x_labels": []string{"00:00", "06:00", "12:00", "18:00"},
				"y_labels": []string{"API", "DB", "Cache"},
				"values": [][]float64{
					{0.01, 0.02, 0.01, 0.03},
					{0.005, 0.01, 0.02, 0.01},
					{0.001, 0.001, 0.002, 0.001},
				},
			},
			format:    Both,
			wantASCII: "Error Rates",
			wantHTML:  "data-vega",
		},
		{
			name: "empty values",
			config: map[string]any{
				"x_labels": []string{"A", "B"},
				"y_labels": []string{"X", "Y"},
				"values":   [][]float64{{0, 0}, {0, 0}},
			},
			format:   HTML,
			wantHTML: "Heatmap",
		},
		{
			name: "no title",
			config: map[string]any{
				"x_labels": []string{"1", "2"},
				"y_labels": []string{"a"},
				"values":   [][]float64{{1.0, 2.0}},
			},
			format:   HTML,
			wantHTML: "Heatmap",
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

func TestHeatmapComponent_Render_InvalidJSON(t *testing.T) {
	c := &heatmapComponent{}
	_, err := c.Render([]byte(`{invalid`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}
