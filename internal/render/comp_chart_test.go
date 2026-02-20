package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChartComponent_Type(t *testing.T) {
	c := &chartComponent{}
	if c.Type() != "chart" {
		t.Errorf("Type() = %q, want %q", c.Type(), "chart")
	}
}

func TestChartComponent_Schema(t *testing.T) {
	c := &chartComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if _, ok := schema.Properties["title"]; !ok {
		t.Error("Schema missing 'title' property")
	}
	if _, ok := schema.Properties["mark"]; !ok {
		t.Error("Schema missing 'mark' property")
	}
	if _, ok := schema.Properties["spec"]; !ok {
		t.Error("Schema missing 'spec' property")
	}
}

func TestChartComponent_CSS(t *testing.T) {
	c := &chartComponent{}
	// CSS returns empty string, uses chart class defined elsewhere
	_ = c.CSS()
}

func TestChartComponent_Render(t *testing.T) {
	c := &chartComponent{}

	tests := []struct {
		name      string
		config    map[string]any
		format    Format
		wantASCII string
		wantHTML  string
	}{
		{
			name: "with full spec",
			config: map[string]any{
				"title": "Request Latency",
				"spec": map[string]any{
					"$schema": "https://vega.github.io/schema/vega-lite/v6.json",
					"mark":    "line",
					"data":    map[string]any{"values": []map[string]any{{"x": 1, "y": 10}}},
				},
			},
			format:    Both,
			wantASCII: "[Chart: Request Latency]",
			wantHTML:  "data-vega",
		},
		{
			name: "with mark type",
			config: map[string]any{
				"title": "Error Rate",
				"mark":  "bar",
				"data": map[string]any{
					"values": []map[string]any{
						{"category": "A", "value": 10},
						{"category": "B", "value": 20},
					},
				},
			},
			format:    Both,
			wantASCII: "[bar chart: Error Rate]",
			wantHTML:  "sl-card",
		},
		{
			name: "with encoding",
			config: map[string]any{
				"title": "Time Series",
				"mark":  "line",
				"data": map[string]any{
					"values": []map[string]any{{"time": "2024-01-01", "value": 100}},
				},
				"encoding": map[string]any{
					"x": map[string]any{"field": "time", "type": "temporal"},
					"y": map[string]any{"field": "value", "type": "quantitative"},
				},
			},
			format:   HTML,
			wantHTML: "data-vega",
		},
		{
			name: "with custom height",
			config: map[string]any{
				"title":  "Custom Height",
				"mark":   "area",
				"height": 300,
			},
			format:   HTML,
			wantHTML: `height`,
		},
		{
			name: "no title",
			config: map[string]any{
				"mark": "point",
			},
			format:    ASCII,
			wantASCII: "[point chart: ]",
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

func TestChartComponent_Render_InvalidJSON(t *testing.T) {
	c := &chartComponent{}
	_, err := c.Render([]byte(`{invalid`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}
