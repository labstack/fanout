package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBadgeComponent_Type(t *testing.T) {
	c := &badgeComponent{}
	if c.Type() != "badge" {
		t.Errorf("Type() = %q, want %q", c.Type(), "badge")
	}
}

func TestBadgeComponent_Schema(t *testing.T) {
	c := &badgeComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if _, ok := schema.Properties["label"]; !ok {
		t.Error("Schema missing 'label' property")
	}
	if _, ok := schema.Properties["status"]; !ok {
		t.Error("Schema missing 'status' property")
	}
}

func TestBadgeComponent_CSS(t *testing.T) {
	c := &badgeComponent{}
	css := c.CSS()
	// Badge might have empty CSS if it relies on Shoelace
	_ = css
}

func TestBadgeComponent_Render(t *testing.T) {
	c := &badgeComponent{}

	tests := []struct {
		name      string
		config    map[string]string
		format    Format
		wantASCII string
		wantHTML  string
	}{
		{
			name:      "healthy badge ASCII",
			config:    map[string]string{"label": "Status", "status": "healthy"},
			format:    ASCII,
			wantASCII: "[✓ Status]",
		},
		{
			name:      "warning badge ASCII",
			config:    map[string]string{"label": "API", "status": "degraded"},
			format:    ASCII,
			wantASCII: "[! API]",
		},
		{
			name:      "error badge ASCII",
			config:    map[string]string{"label": "DB", "status": "unhealthy"},
			format:    ASCII,
			wantASCII: "[✗ DB]",
		},
		{
			name:      "info badge ASCII",
			config:    map[string]string{"label": "Info", "status": "info"},
			format:    ASCII,
			wantASCII: "[i Info]",
		},
		{
			name:     "HTML badge",
			config:   map[string]string{"label": "Status", "status": "healthy"},
			format:   HTML,
			wantHTML: "sl-badge",
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
				t.Errorf("Render() HTML should contain %q, got %q", tc.wantHTML, out.HTML)
			}
		})
	}
}

func TestBadgeComponent_Render_InvalidJSON(t *testing.T) {
	c := &badgeComponent{}
	_, err := c.Render([]byte(`{invalid`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}
