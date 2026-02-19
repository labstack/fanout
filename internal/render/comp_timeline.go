package render

import (
	"encoding/json"
	"strings"
)

func init() {
	Register(&timelineComponent{})
}

type timelineComponent struct{}

type timelineConfig struct {
	Events []timelineEventConfig `json:"events"`
}

type timelineEventConfig struct {
	Time  string `json:"time"`
	Label string `json:"label"`
	Type  string `json:"type"` // deploy, error, warning, info, success
}

func (c *timelineComponent) Type() string { return "timeline" }

func (c *timelineComponent) Schema() *Schema {
	return &Schema{
		Description: "Chronological event timeline",
		Properties: map[string]Property{
			"events": {
				Type:        "array",
				Description: "Timeline events",
				Items: &Property{
					Type: "object",
					Properties: map[string]Property{
						"time":  {Type: "string", Description: "Event timestamp"},
						"label": {Type: "string", Description: "Event description"},
						"type":  {Type: "string", Description: "Event type", Enum: []string{"deploy", "error", "warning", "info", "success"}},
					},
				},
			},
		},
		Required: []string{"events"},
	}
}

func (c *timelineComponent) CSS() string {
	return `
.timeline {
	display: flex;
	flex-direction: column;
	gap: 0.75rem;
	padding: 0.75rem 0;
}
.timeline-event {
	display: flex;
	align-items: center;
	gap: 1rem;
	padding: 0.75rem 1rem;
	border-left: 3px solid var(--border-color);
	background: var(--bg-tertiary);
	border-radius: 0 0.5rem 0.5rem 0;
}
.timeline-event sl-icon { font-size: 1.25rem; flex-shrink: 0; }
.timeline-time {
	font-size: 0.8rem;
	color: var(--text-muted);
	font-family: monospace;
	min-width: 60px;
	flex-shrink: 0;
	padding-right: 0.5rem;
	border-right: 1px solid var(--border-color);
}
.timeline-label {
	font-size: 0.875rem;
	flex: 1;
}
.timeline-neutral { border-left-color: var(--text-muted); }
.timeline-primary { border-left-color: var(--accent); }
.timeline-primary sl-icon { color: var(--accent); }
.timeline-success { border-left-color: var(--success); }
.timeline-success sl-icon { color: var(--success); }
.timeline-warning { border-left-color: var(--warning); }
.timeline-warning sl-icon { color: var(--warning); }
.timeline-danger { border-left-color: var(--danger); }
.timeline-danger sl-icon { color: var(--danger); }`
}

func (c *timelineComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg timelineConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return Output{}, err
	}

	var out Output

	if format == ASCII || format == Both {
		var sb strings.Builder
		for i, e := range cfg.Events {
			prefix := "├─"
			if i == len(cfg.Events)-1 {
				prefix = "└─"
			}
			icon := "●"
			switch e.Type {
			case "error":
				icon = "✗"
			case "warning":
				icon = "!"
			case "deploy":
				icon = "▲"
			case "success":
				icon = "✓"
			}
			sb.WriteString(prefix + " [" + e.Time + "] " + icon + " " + e.Label + "\n")
		}
		out.ASCII = strings.TrimSuffix(sb.String(), "\n")
	}

	if format == HTML || format == Both {
		var sb strings.Builder
		sb.WriteString(`<div class="timeline">`)
		for _, e := range cfg.Events {
			variant := "neutral"
			icon := "circle"
			switch e.Type {
			case "error":
				variant = "danger"
				icon = "x-circle"
			case "warning":
				variant = "warning"
				icon = "exclamation-triangle"
			case "deploy":
				variant = "primary"
				icon = "rocket-takeoff"
			case "success":
				variant = "success"
				icon = "check-circle"
			}
			sb.WriteString(`<div class="timeline-event timeline-` + variant + `">`)
			sb.WriteString(`<sl-icon name="` + icon + `"></sl-icon>`)
			sb.WriteString(`<span class="timeline-time">` + escapeHTML(e.Time) + `</span>`)
			sb.WriteString(`<span class="timeline-label">` + escapeHTML(e.Label) + `</span>`)
			sb.WriteString(`</div>`)
		}
		sb.WriteString(`</div>`)
		out.HTML = sb.String()
	}
	return out, nil
}
