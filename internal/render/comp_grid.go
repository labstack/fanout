package render

import (
	"encoding/json"
	"strings"
)

func init() {
	Register(&gridComponent{})
}

type gridComponent struct{}

type gridConfig struct {
	Title string            `json:"title"`
	Cols  int               `json:"cols"` // 2, 3, or 4
	Items []json.RawMessage `json:"items"`
}

type gridItemConfig struct {
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config"`
}

func (c *gridComponent) Type() string { return "grid" }

func (c *gridComponent) Schema() *Schema {
	return &Schema{
		Description: "Grid layout for arranging components in columns",
		Properties: map[string]Property{
			"title": {Type: "string", Description: "Grid section title"},
			"cols":  {Type: "integer", Description: "Number of columns (2, 3, or 4)", Default: 2},
			"items": {Type: "array", Description: "Components to arrange in grid", Items: &Property{Type: "object"}},
		},
		Required: []string{"items"},
	}
}

func (c *gridComponent) CSS() string {
	return `
.grid { display: grid; gap: 1rem; }
.grid-2 { grid-template-columns: repeat(2, 1fr); }
.grid-3 { grid-template-columns: repeat(3, 1fr); }
.grid-4 { grid-template-columns: repeat(4, 1fr); }
.grid > sl-badge { justify-self: start; align-self: center; }

@media (max-width: 1024px) {
	.grid-3, .grid-4 { grid-template-columns: repeat(2, 1fr); }
}
@media (max-width: 640px) {
	.grid-2, .grid-3, .grid-4 { grid-template-columns: 1fr; }
}`
}

func (c *gridComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg gridConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return Output{}, err
	}

	cols := cfg.Cols
	if cols < 2 || cols > 4 {
		cols = 2
	}

	var out Output
	out.Title = cfg.Title

	var asciiParts, htmlParts []string

	for _, itemRaw := range cfg.Items {
		var item gridItemConfig
		if err := json.Unmarshal(itemRaw, &item); err != nil {
			continue
		}

		// Render the nested component
		itemOut, err := RenderSection(item.Type, item.Config, format)
		if err != nil {
			continue
		}
		if itemOut.ASCII != "" {
			asciiParts = append(asciiParts, itemOut.ASCII)
		}
		if itemOut.HTML != "" {
			htmlParts = append(htmlParts, itemOut.HTML)
		}
	}

	if format == ASCII || format == Both {
		if cfg.Title != "" {
			out.ASCII = cfg.Title + "\n" + strings.Join(asciiParts, "\n")
		} else {
			out.ASCII = strings.Join(asciiParts, "\n")
		}
	}

	if format == HTML || format == Both {
		out.HTML = `<div class="grid grid-` + itoa(cols) + `">` +
			strings.Join(htmlParts, "") + `</div>`
	}
	return out, nil
}
