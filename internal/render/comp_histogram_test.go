package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHistogramComponent_Type(t *testing.T) {
	c := &histogramComponent{}
	if c.Type() != "histogram" {
		t.Errorf("Type() = %q, want %q", c.Type(), "histogram")
	}
}

func TestHistogramComponent_Schema(t *testing.T) {
	c := &histogramComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if _, ok := schema.Properties["label"]; !ok {
		t.Error("Schema missing 'label' property")
	}
	if _, ok := schema.Properties["buckets"]; !ok {
		t.Error("Schema missing 'buckets' property")
	}
	if _, ok := schema.Properties["counts"]; !ok {
		t.Error("Schema missing 'counts' property")
	}
}

func TestHistogramComponent_Render(t *testing.T) {
	c := &histogramComponent{}

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
				"label":   "Latency Distribution",
				"buckets": []float64{10, 50, 100, 200, 500},
				"counts":  []int{100, 250, 150, 75, 25},
			},
			format:    Both,
			wantASCII: "Latency Distribution",
			wantHTML:  "data-vega",
		},
		{
			name: "empty buckets",
			config: map[string]any{
				"label":   "Empty Histogram",
				"buckets": []float64{},
				"counts":  []int{},
			},
			format:    Both,
			wantASCII: "no data",
			wantHTML:  "No data",
		},
		{
			name: "all zeros",
			config: map[string]any{
				"label":   "Zero Counts",
				"buckets": []float64{10, 20, 30},
				"counts":  []int{0, 0, 0},
			},
			format:    ASCII,
			wantASCII: "Zero Counts",
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

func TestHistogramComponent_Render_InvalidJSON(t *testing.T) {
	c := &histogramComponent{}
	_, err := c.Render([]byte(`{invalid`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}
