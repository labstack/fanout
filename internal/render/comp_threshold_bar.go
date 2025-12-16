package render

import (
	"encoding/json"
	"strings"
)

func init() {
	Register(&thresholdBarComponent{})
}

type thresholdBarComponent struct{}

type thresholdBarConfig struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Max   float64 `json:"max"`
	Warn  float64 `json:"warn"` // warning threshold
	Crit  float64 `json:"crit"` // critical threshold
	Unit  string  `json:"unit"`
}

func (c *thresholdBarComponent) Type() string { return "threshold-bar" }

func (c *thresholdBarComponent) Schema() *Schema {
	return &Schema{
		Description: "Progress bar with warning/critical threshold markers",
		Properties: map[string]Property{
			"label": {Type: "string", Description: "Bar label"},
			"value": {Type: "number", Description: "Current value"},
			"max":   {Type: "number", Description: "Maximum value"},
			"warn":  {Type: "number", Description: "Warning threshold"},
			"crit":  {Type: "number", Description: "Critical threshold"},
			"unit":  {Type: "string", Description: "Unit suffix"},
		},
		Required: []string{"label", "value", "max", "warn", "crit"},
	}
}

func (c *thresholdBarComponent) CSS() string {
	return `
.threshold-bar {
	display: flex;
	align-items: center;
	gap: 0.75rem;
	padding: 0.25rem 0;
}
.threshold-track {
	flex: 1;
	position: relative;
	height: 1.5rem;
}
.threshold-progress {
	width: 100%;
}
.threshold-marker {
	position: absolute;
	top: 0;
	width: 2px;
	height: 100%;
	pointer-events: none;
}
.threshold-warn { background: var(--warning); }
.threshold-crit { background: var(--danger); }
.threshold-primary::part(indicator) { background: var(--accent); }
.threshold-warning::part(indicator) { background: var(--warning); }
.threshold-danger::part(indicator) { background: var(--danger); }`
}

func (c *thresholdBarComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg thresholdBarConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return Output{}, err
	}

	// Normalize
	if cfg.Max <= 0 {
		cfg.Max = 100
	}
	pct := clamp(cfg.Value/cfg.Max, 0, 1)

	var out Output
	out.Title = cfg.Label

	if format == ASCII || format == Both {
		width := 30
		filled := int(pct * float64(width))
		warnPos := int(cfg.Warn / cfg.Max * float64(width))
		critPos := int(cfg.Crit / cfg.Max * float64(width))

		var bar strings.Builder
		for i := 0; i < width; i++ {
			if i < filled {
				if i >= critPos {
					bar.WriteString("▓")
				} else if i >= warnPos {
					bar.WriteString("▒")
				} else {
					bar.WriteString("█")
				}
			} else {
				bar.WriteString("░")
			}
		}
		status := ""
		if cfg.Value >= cfg.Crit {
			status = " [CRITICAL]"
		} else if cfg.Value >= cfg.Warn {
			status = " [WARNING]"
		}
		out.ASCII = cfg.Label + " " + bar.String() + " " + formatFloat(cfg.Value) + cfg.Unit + status
	}

	if format == HTML || format == Both {
		pctInt := int(pct * 100)
		warnPct := clampInt(int(cfg.Warn/cfg.Max*100), 0, 100)
		critPct := clampInt(int(cfg.Crit/cfg.Max*100), 0, 100)

		variant := "primary"
		if cfg.Value >= cfg.Crit {
			variant = "danger"
		} else if cfg.Value >= cfg.Warn {
			variant = "warning"
		}

		out.HTML = `<div class="threshold-bar">` +
			`<span class="bar-label">` + escapeHTML(cfg.Label) + `</span>` +
			`<div class="threshold-track">` +
			`<sl-progress-bar value="` + itoa(pctInt) + `" class="threshold-progress threshold-` + variant + `"></sl-progress-bar>` +
			`<div class="threshold-marker threshold-warn" style="left:` + itoa(warnPct) + `%"></div>` +
			`<div class="threshold-marker threshold-crit" style="left:` + itoa(critPct) + `%"></div>` +
			`</div>` +
			`<span class="bar-value">` + formatFloat(cfg.Value) + escapeHTML(cfg.Unit) + `</span>` +
			`</div>`
	}
	return out, nil
}
