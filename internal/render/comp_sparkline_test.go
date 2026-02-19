package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSparklineComponent_Type(t *testing.T) {
	c := &sparklineComponent{}
	if c.Type() != "sparkline" {
		t.Errorf("Type() = %q, want %q", c.Type(), "sparkline")
	}
}

func TestSparklineComponent_Schema(t *testing.T) {
	c := &sparklineComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if _, ok := schema.Properties["label"]; !ok {
		t.Error("Schema missing 'label' property")
	}
	if _, ok := schema.Properties["values"]; !ok {
		t.Error("Schema missing 'values' property")
	}
}

func TestSparklineComponent_Render(t *testing.T) {
	c := &sparklineComponent{}

	tests := []struct {
		name      string
		config    map[string]any
		format    Format
		wantASCII string
		wantHTML  string
	}{
		{
			name:      "empty values ASCII",
			config:    map[string]any{"label": "CPU", "values": []float64{}},
			format:    ASCII,
			wantASCII: "CPU ─",
		},
		{
			name:     "empty values HTML",
			config:   map[string]any{"label": "CPU", "values": []float64{}},
			format:   HTML,
			wantHTML: "<span>CPU</span>",
		},
		{
			name:      "with values ASCII",
			config:    map[string]any{"label": "Trend", "values": []float64{1, 2, 3, 4, 5}},
			format:    ASCII,
			wantASCII: "Trend", // Contains label
		},
		{
			name:     "with values HTML",
			config:   map[string]any{"label": "Trend", "values": []float64{1, 2, 3, 4, 5}},
			format:   HTML,
			wantHTML: "sparkline",
		},
		{
			name:      "constant values",
			config:    map[string]any{"label": "Flat", "values": []float64{5, 5, 5, 5, 5}},
			format:    ASCII,
			wantASCII: "Flat",
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

func TestSparklineComponent_Render_InvalidJSON(t *testing.T) {
	c := &sparklineComponent{}
	_, err := c.Render([]byte(`{invalid`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}

func TestSparklineComponent_CSS(t *testing.T) {
	c := &sparklineComponent{}
	css := c.CSS()
	if css == "" {
		t.Error("CSS() should not be empty")
	}
	if !strings.Contains(css, ".sparkline") {
		t.Error("CSS() should contain .sparkline class")
	}
}
