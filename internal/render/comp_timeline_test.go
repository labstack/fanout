package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTimelineComponent_Type(t *testing.T) {
	c := &timelineComponent{}
	if c.Type() != "timeline" {
		t.Errorf("Type() = %q, want %q", c.Type(), "timeline")
	}
}

func TestTimelineComponent_Schema(t *testing.T) {
	c := &timelineComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if _, ok := schema.Properties["events"]; !ok {
		t.Error("Schema missing 'events' property")
	}
}

func TestTimelineComponent_CSS(t *testing.T) {
	c := &timelineComponent{}
	css := c.CSS()
	if css == "" {
		t.Error("CSS() returned empty string")
	}
	if !strings.Contains(css, "timeline") {
		t.Error("CSS() should contain timeline class")
	}
}

func TestTimelineComponent_Render(t *testing.T) {
	c := &timelineComponent{}

	tests := []struct {
		name      string
		config    map[string]any
		format    Format
		wantASCII string
		wantHTML  string
	}{
		{
			name: "mixed events",
			config: map[string]any{
				"events": []map[string]any{
					{"time": "09:00", "label": "Deployment started", "type": "deploy"},
					{"time": "09:05", "label": "Health checks passing", "type": "success"},
					{"time": "09:10", "label": "High latency detected", "type": "warning"},
					{"time": "09:15", "label": "Service recovered", "type": "info"},
				},
			},
			format:    Both,
			wantASCII: "Deployment",
			wantHTML:  "timeline",
		},
		{
			name: "error event",
			config: map[string]any{
				"events": []map[string]any{
					{"time": "10:00", "label": "Database connection failed", "type": "error"},
				},
			},
			format:    Both,
			wantASCII: "✗",
			wantHTML:  "danger",
		},
		{
			name: "success event",
			config: map[string]any{
				"events": []map[string]any{
					{"time": "11:00", "label": "All tests passed", "type": "success"},
				},
			},
			format:    Both,
			wantASCII: "✓",
			wantHTML:  "success",
		},
		{
			name: "warning event",
			config: map[string]any{
				"events": []map[string]any{
					{"time": "12:00", "label": "CPU usage high", "type": "warning"},
				},
			},
			format:    Both,
			wantASCII: "!",
			wantHTML:  "warning",
		},
		{
			name: "deploy event",
			config: map[string]any{
				"events": []map[string]any{
					{"time": "13:00", "label": "v2.0 deployed", "type": "deploy"},
				},
			},
			format:    Both,
			wantASCII: "▲",
			wantHTML:  "rocket",
		},
		{
			name: "empty events",
			config: map[string]any{
				"events": []map[string]any{},
			},
			format: Both,
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

func TestTimelineComponent_Render_InvalidJSON(t *testing.T) {
	c := &timelineComponent{}
	_, err := c.Render([]byte(`{invalid`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}
