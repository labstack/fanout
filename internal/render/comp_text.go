package render

import "encoding/json"

func init() {
	Register(&textComponent{})
}

type textComponent struct{}

type textConfig struct {
	Content string `json:"content"`
	Style   string `json:"style"` // bold, dim, warning, error, success
}

func (c *textComponent) Type() string { return "text" }

func (c *textComponent) Schema() *Schema {
	return &Schema{
		Description: "Plain text with optional styling",
		Properties: map[string]Property{
			"content": {Type: "string", Description: "Text content to display"},
			"style":   {Type: "string", Description: "Style variant", Enum: []string{"bold", "dim", "warning", "error", "success"}},
		},
		Required: []string{"content"},
	}
}

func (c *textComponent) CSS() string {
	return `.text-dim { color: var(--text-muted); }`
}

func (c *textComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg textConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return Output{}, err
	}

	var out Output
	if format == ASCII || format == Both {
		out.ASCII = cfg.Content
	}
	if format == HTML || format == Both {
		content := escapeHTML(cfg.Content)
		switch cfg.Style {
		case "bold":
			out.HTML = `<sl-alert variant="primary" open><sl-icon slot="icon" name="info-circle"></sl-icon>` + content + `</sl-alert>`
		case "warning":
			out.HTML = `<sl-alert variant="warning" open><sl-icon slot="icon" name="exclamation-triangle"></sl-icon>` + content + `</sl-alert>`
		case "error":
			out.HTML = `<sl-alert variant="danger" open><sl-icon slot="icon" name="exclamation-octagon"></sl-icon>` + content + `</sl-alert>`
		case "success":
			out.HTML = `<sl-alert variant="success" open><sl-icon slot="icon" name="check2-circle"></sl-icon>` + content + `</sl-alert>`
		case "dim":
			out.HTML = `<span class="text-dim">` + content + `</span>`
		default:
			out.HTML = `<span>` + content + `</span>`
		}
	}
	return out, nil
}
