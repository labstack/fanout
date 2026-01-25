package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPanelComponent_Type(t *testing.T) {
	c := &panelComponent{}
	if c.Type() != "panel" {
		t.Errorf("Type() = %q, want %q", c.Type(), "panel")
	}
}

func TestPanelComponent_Schema(t *testing.T) {
	c := &panelComponent{}
	schema := c.Schema()

	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if _, ok := schema.Properties["title"]; !ok {
		t.Error("Schema missing 'title' property")
	}
	if _, ok := schema.Properties["content"]; !ok {
		t.Error("Schema missing 'content' property")
	}
}

func TestPanelComponent_Render(t *testing.T) {
	c := &panelComponent{}

	tests := []struct {
		name      string
		config    map[string]any
		format    Format
		wantASCII string
		wantHTML  string
	}{
		{
			name: "empty panel ASCII",
			config: map[string]any{
				"title":   "Summary",
				"content": []map[string]any{},
			},
			format:    ASCII,
			wantASCII: "Summary",
		},
		{
			name: "empty panel HTML",
			config: map[string]any{
				"title":   "Summary",
				"content": []map[string]any{},
			},
			format:   HTML,
			wantHTML: "sl-card",
		},
		{
			name: "with nested content",
			config: map[string]any{
				"title": "Stats",
				"content": []map[string]any{
					{"type": "badge", "config": map[string]any{"label": "OK", "status": "healthy"}},
				},
			},
			format:   HTML,
			wantHTML: "sl-card",
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

func TestPanelComponent_Render_InvalidJSON(t *testing.T) {
	c := &panelComponent{}
	_, err := c.Render([]byte(`{invalid`), ASCII)
	if err == nil {
		t.Error("Render() with invalid JSON should return error")
	}
}

func TestBoxASCII(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		content string
		want    string
	}{
		{
			name:    "simple box",
			title:   "Title",
			content: "content",
			want:    "Title",
		},
		{
			name:    "multiline content",
			title:   "Box",
			content: "line1\nline2\nline3",
			want:    "Box",
		},
		{
			name:    "empty content",
			title:   "Empty",
			content: "",
			want:    "Empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := boxASCII(tc.title, tc.content)
			if !strings.Contains(result, tc.want) {
				t.Errorf("boxASCII() = %q, want to contain %q", result, tc.want)
			}
			// Should have box drawing characters
			if !strings.Contains(result, "┌") {
				t.Error("boxASCII() should contain top-left corner")
			}
			if !strings.Contains(result, "└") {
				t.Error("boxASCII() should contain bottom-left corner")
			}
		})
	}
}
