package render

import "encoding/json"

func init() {
	Register(&badgeComponent{})
}

type badgeComponent struct{}

type badgeConfig struct {
	Label  string `json:"label"`
	Status string `json:"status"` // healthy, degraded, unhealthy, info, warning, error
}

func (c *badgeComponent) Type() string { return "badge" }

func (c *badgeComponent) Schema() *Schema {
	return &Schema{
		Description: "Status badge indicator",
		Properties: map[string]Property{
			"label":  {Type: "string", Description: "Badge text"},
			"status": {Type: "string", Description: "Status variant", Enum: []string{"healthy", "degraded", "unhealthy", "info", "warning", "error"}},
		},
		Required: []string{"label", "status"},
	}
}

func (c *badgeComponent) CSS() string { return "" } // Uses Shoelace badge

func (c *badgeComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg badgeConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return Output{}, err
	}

	var out Output
	if format == ASCII || format == Both {
		icon := "●"
		switch cfg.Status {
		case "healthy", "success":
			icon = "✓"
		case "degraded", "warning":
			icon = "!"
		case "unhealthy", "error":
			icon = "✗"
		case "info":
			icon = "i"
		}
		out.ASCII = "[" + icon + " " + cfg.Label + "]"
	}
	if format == HTML || format == Both {
		variant := "neutral"
		switch cfg.Status {
		case "healthy", "success":
			variant = "success"
		case "degraded", "warning":
			variant = "warning"
		case "unhealthy", "error":
			variant = "danger"
		case "info":
			variant = "primary"
		}
		out.HTML = `<div style="display:flex;align-items:center;"><sl-badge variant="` + variant + `">` + escapeHTML(cfg.Label) + `</sl-badge></div>`
	}
	return out, nil
}
