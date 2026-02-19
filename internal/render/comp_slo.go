package render

import "encoding/json"

func init() {
	Register(&sloComponent{})
}

type sloComponent struct{}

type sloConfig struct {
	Name      string  `json:"name"`
	Current   float64 `json:"current"`          // current SLI (e.g., 99.92)
	Target    float64 `json:"target"`           // target SLO (e.g., 99.9)
	BudgetPct float64 `json:"budget_remaining"` // budget remaining %
	BurnRate  float64 `json:"burn_rate"`        // current burn rate
}

func (c *sloComponent) Type() string { return "slo" }

func (c *sloComponent) Schema() *Schema {
	return &Schema{
		Description: "SLO compliance and error budget display",
		Properties: map[string]Property{
			"name":             {Type: "string", Description: "SLO name"},
			"current":          {Type: "number", Description: "Current SLI value (e.g., 99.92)"},
			"target":           {Type: "number", Description: "Target SLO (e.g., 99.9)"},
			"budget_remaining": {Type: "number", Description: "Error budget remaining percentage"},
			"burn_rate":        {Type: "number", Description: "Current burn rate multiplier"},
		},
		Required: []string{"name", "current", "target"},
	}
}

func (c *sloComponent) CSS() string {
	return `
.slo-card .slo-metrics {
	display: flex;
	gap: 2rem;
	margin-bottom: 1rem;
}
.slo-current, .slo-target {
	text-align: center;
}
.slo-value {
	font-size: 1.5rem;
	font-weight: 700;
	display: block;
}
.slo-label {
	font-size: 0.75rem;
	color: var(--text-muted);
	text-transform: uppercase;
	display: block;
	margin-top: 0.25rem;
}
.slo-budget {
	margin-bottom: 0.5rem;
}
.slo-budget span {
	font-size: 0.875rem;
	margin-bottom: 0.25rem;
	display: block;
}
.slo-burn {
	font-size: 0.875rem;
	color: var(--text-secondary);
}`
}

func (c *sloComponent) Render(config json.RawMessage, format Format) (Output, error) {
	var cfg sloConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return Output{}, err
	}

	var out Output
	out.Title = cfg.Name

	if format == ASCII || format == Both {
		status := "✓ MEETING"
		if cfg.Current < cfg.Target {
			status = "✗ BREACHED"
		}
		out.ASCII = cfg.Name + ": " + formatFloat(cfg.Current) + "% (target: " + formatFloat(cfg.Target) + "%) " + status +
			"\n  Budget: " + formatFloat(cfg.BudgetPct) + "% remaining, Burn rate: " + formatFloat(cfg.BurnRate) + "x"
	}

	if format == HTML || format == Both {
		status := "success"
		statusText := "Meeting SLO"
		if cfg.Current < cfg.Target {
			status = "danger"
			statusText = "SLO Breached"
		} else if cfg.BudgetPct < 30 {
			status = "warning"
			statusText = "Budget Low"
		}
		budgetPct := clampInt(int(cfg.BudgetPct), 0, 100)

		out.HTML = `<sl-card class="slo-card">` +
			`<div slot="header">` + escapeHTML(cfg.Name) + ` <sl-badge variant="` + status + `">` + statusText + `</sl-badge></div>` +
			`<div class="slo-metrics">` +
			`<div class="slo-current"><span class="slo-value">` + formatFloat(cfg.Current) + `%</span><span class="slo-label">Current</span></div>` +
			`<div class="slo-target"><span class="slo-value">` + formatFloat(cfg.Target) + `%</span><span class="slo-label">Target</span></div>` +
			`</div>` +
			`<div class="slo-budget">` +
			`<span>Error Budget: ` + formatFloat(cfg.BudgetPct) + `%</span>` +
			`<sl-progress-bar value="` + itoa(budgetPct) + `"></sl-progress-bar>` +
			`</div>` +
			`<div class="slo-burn">Burn Rate: ` + formatFloat(cfg.BurnRate) + `x</div>` +
			`</sl-card>`
	}
	return out, nil
}
