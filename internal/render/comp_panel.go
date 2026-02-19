package render

import (
	"encoding/json"
	"strings"
)

func init() {
	Register(&panelComponent{})
}

type panelComponent struct{}

type panelConfig struct {
	Title   string            `json:"title"`
	Content []json.RawMessage `json:"content"`
}

type panelItemConfig struct {
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config"`
}

func (c *panelComponent) Type() string { return "panel" }

func (c *panelComponent) Schema() *Schema {
	return &Schema{
		Description: "Card panel containing grouped components",
		Properties: map[string]Property{
			"title":   {Type: "string", Description: "Panel title"},
			"content": {Type: "array", Description: "Components inside the panel", Items: &Property{Type: "object"}},
		},
		Required: []string{"title", "content"},
	}
}

func (c *panelComponent) CSS() string { return "" } // Uses sl-card

func (c *panelComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg panelConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return Output{}, err
	}

	var out Output
	out.Title = cfg.Title

	var asciiParts, htmlParts []string

	for _, itemRaw := range cfg.Content {
		var item panelItemConfig
		if err := json.Unmarshal(itemRaw, &item); err != nil {
			continue
		}

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
		content := strings.Join(asciiParts, "\n")
		out.ASCII = boxASCII(cfg.Title, content)
	}

	if format == HTML || format == Both {
		out.HTML = `<sl-card><div slot="header">` + escapeHTML(cfg.Title) + `</div>` +
			strings.Join(htmlParts, "") + `</sl-card>`
	}
	return out, nil
}

func boxASCII(title, content string) string {
	lines := strings.Split(content, "\n")
	maxLen := len(title) + 2
	for _, l := range lines {
		if len(l) > maxLen {
			maxLen = len(l)
		}
	}

	var sb strings.Builder
	sb.WriteString("┌─ " + title + " " + strings.Repeat("─", maxLen-len(title)-1) + "┐\n")
	for _, l := range lines {
		sb.WriteString("│ " + padRight(l, maxLen) + " │\n")
	}
	sb.WriteString("└" + strings.Repeat("─", maxLen+2) + "┘")
	return sb.String()
}
