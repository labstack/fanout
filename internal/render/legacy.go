package render

// Legacy types for backward compatibility with MCP tools.
// These wrap the new Component-based system while maintaining the old API.

// Table renders tabular data (legacy interface)
type Table struct {
	Title    string
	Headers  []string
	Rows     [][]string
	MaxWidth int
}

func (t *Table) Render(format Format) Output {
	c, _ := Get("table")
	if c == nil {
		return Output{}
	}
	// Convert to JSON config
	cfg := `{"title":"` + escapeJSON(t.Title) + `","headers":[`
	for i, h := range t.Headers {
		if i > 0 {
			cfg += ","
		}
		cfg += `"` + escapeJSON(h) + `"`
	}
	cfg += `],"rows":[`
	for i, row := range t.Rows {
		if i > 0 {
			cfg += ","
		}
		cfg += `[`
		for j, cell := range row {
			if j > 0 {
				cfg += ","
			}
			cfg += `"` + escapeJSON(cell) + `"`
		}
		cfg += `]`
	}
	cfg += `],"max_width":` + itoa(t.MaxWidth) + `}`
	out, _ := c.Render([]byte(cfg), format)
	out.Title = t.Title
	return out
}

// Badge renders a status badge (legacy interface)
type Badge struct {
	Label  string
	Status string
}

func (b *Badge) Render(format Format) Output {
	c, _ := Get("badge")
	if c == nil {
		return Output{}
	}
	cfg := `{"label":"` + escapeJSON(b.Label) + `","status":"` + escapeJSON(b.Status) + `"}`
	out, _ := c.Render([]byte(cfg), format)
	return out
}

// Bar renders a progress bar (legacy interface)
type Bar struct {
	Label string
	Value float64
	Max   float64
	Unit  string
}

func (b *Bar) Render(format Format) Output {
	c, _ := Get("bar")
	if c == nil {
		return Output{}
	}
	cfg := `{"label":"` + escapeJSON(b.Label) + `","value":` + formatFloat(b.Value) + `,"max":` + formatFloat(b.Max) + `,"unit":"` + escapeJSON(b.Unit) + `"}`
	out, _ := c.Render([]byte(cfg), format)
	return out
}

// Sparkline renders a mini chart (legacy interface)
type Sparkline struct {
	Label  string
	Values []float64
}

func (s *Sparkline) Render(format Format) Output {
	c, _ := Get("sparkline")
	if c == nil {
		return Output{}
	}
	cfg := `{"label":"` + escapeJSON(s.Label) + `","values":[`
	for i, v := range s.Values {
		if i > 0 {
			cfg += ","
		}
		cfg += formatFloat(v)
	}
	cfg += `]}`
	out, _ := c.Render([]byte(cfg), format)
	return out
}

// Text renders plain text (legacy interface)
type Text struct {
	Content string
	Style   string
}

func (t *Text) Render(format Format) Output {
	c, _ := Get("text")
	if c == nil {
		return Output{}
	}
	cfg := `{"content":"` + escapeJSON(t.Content) + `","style":"` + escapeJSON(t.Style) + `"}`
	out, _ := c.Render([]byte(cfg), format)
	return out
}

// Metric renders a single metric value (legacy interface)
type Metric struct {
	Label string
	Value string
	Unit  string
	Trend string
}

func (m *Metric) Render(format Format) Output {
	c, _ := Get("metric")
	if c == nil {
		return Output{}
	}
	cfg := `{"label":"` + escapeJSON(m.Label) + `","value":"` + escapeJSON(m.Value) + `","unit":"` + escapeJSON(m.Unit) + `","trend":"` + escapeJSON(m.Trend) + `"}`
	out, _ := c.Render([]byte(cfg), format)
	return out
}

// Grid renders multiple items in a grid layout (legacy interface)
type Grid struct {
	Title string
	Items []Renderer
	Cols  int
}

func (g *Grid) Render(format Format) Output {
	var out Output
	out.Title = g.Title

	if g.Cols == 0 {
		g.Cols = 2
	}

	var asciiParts, htmlParts []string
	for _, item := range g.Items {
		r := item.Render(format)
		if r.ASCII != "" {
			asciiParts = append(asciiParts, r.ASCII)
		}
		if r.HTML != "" {
			htmlParts = append(htmlParts, r.HTML)
		}
	}

	if format == ASCII || format == Both {
		if g.Title != "" {
			out.ASCII = g.Title + "\n" + join(asciiParts, "\n")
		} else {
			out.ASCII = join(asciiParts, "\n")
		}
	}
	if format == HTML || format == Both {
		out.HTML = `<div class="grid grid-` + itoa(g.Cols) + `">` + join(htmlParts, "") + `</div>`
	}
	return out
}

// Panel groups content with a title (legacy interface)
type Panel struct {
	Title   string
	Content []Renderer
}

func (p *Panel) Render(format Format) Output {
	var out Output
	out.Title = p.Title

	var asciiParts, htmlParts []string
	for _, item := range p.Content {
		r := item.Render(format)
		if r.ASCII != "" {
			asciiParts = append(asciiParts, r.ASCII)
		}
		if r.HTML != "" {
			htmlParts = append(htmlParts, r.HTML)
		}
	}

	if format == ASCII || format == Both {
		content := join(asciiParts, "\n")
		out.ASCII = boxASCII(p.Title, content)
	}
	if format == HTML || format == Both {
		out.HTML = `<sl-card><div slot="header">` + escapeHTML(p.Title) + `</div>` + join(htmlParts, "") + `</sl-card>`
	}
	return out
}

func escapeJSON(s string) string {
	result := ""
	for _, c := range s {
		switch c {
		case '"':
			result += `\"`
		case '\\':
			result += `\\`
		case '\n':
			result += `\n`
		case '\r':
			result += `\r`
		case '\t':
			result += `\t`
		default:
			result += string(c)
		}
	}
	return result
}

// MetricCompare renders a metric with comparison (legacy interface)
type MetricCompare struct {
	Label   string
	Value   string
	Compare string
	Period  string
}

func (m *MetricCompare) Render(format Format) Output {
	c, _ := Get("metric-compare")
	if c == nil {
		return Output{}
	}
	cfg := `{"label":"` + escapeJSON(m.Label) + `","value":"` + escapeJSON(m.Value) + `","compare":"` + escapeJSON(m.Compare) + `","period":"` + escapeJSON(m.Period) + `"}`
	out, _ := c.Render([]byte(cfg), format)
	return out
}

// Histogram renders a distribution chart (legacy interface)
type Histogram struct {
	Label   string
	Buckets []float64
	Counts  []int
}

func (h *Histogram) Render(format Format) Output {
	c, _ := Get("histogram")
	if c == nil {
		return Output{}
	}
	cfg := `{"label":"` + escapeJSON(h.Label) + `","buckets":[`
	for i, b := range h.Buckets {
		if i > 0 {
			cfg += ","
		}
		cfg += formatFloat(b)
	}
	cfg += `],"counts":[`
	for i, cnt := range h.Counts {
		if i > 0 {
			cfg += ","
		}
		cfg += itoa(cnt)
	}
	cfg += `]}`
	out, _ := c.Render([]byte(cfg), format)
	return out
}

// Timeline renders a sequence of events (legacy interface)
type Timeline struct {
	Events []TimelineEvent
}

type TimelineEvent struct {
	Time  string
	Label string
	Type  string
}

func (t *Timeline) Render(format Format) Output {
	c, _ := Get("timeline")
	if c == nil {
		return Output{}
	}
	cfg := `{"events":[`
	for i, e := range t.Events {
		if i > 0 {
			cfg += ","
		}
		cfg += `{"time":"` + escapeJSON(e.Time) + `","label":"` + escapeJSON(e.Label) + `","type":"` + escapeJSON(e.Type) + `"}`
	}
	cfg += `]}`
	out, _ := c.Render([]byte(cfg), format)
	return out
}

// SLO renders error budget and SLO compliance (legacy interface)
type SLO struct {
	Name      string
	Current   float64
	Target    float64
	BudgetPct float64
	BurnRate  float64
}

func (s *SLO) Render(format Format) Output {
	c, _ := Get("slo")
	if c == nil {
		return Output{}
	}
	cfg := `{"name":"` + escapeJSON(s.Name) + `","current":` + formatFloat(s.Current) + `,"target":` + formatFloat(s.Target) + `,"budget_remaining":` + formatFloat(s.BudgetPct) + `,"burn_rate":` + formatFloat(s.BurnRate) + `}`
	out, _ := c.Render([]byte(cfg), format)
	return out
}

// Diff renders a before/after comparison table (legacy interface)
type Diff struct {
	Title string
	Rows  []DiffRow
}

type DiffRow struct {
	Metric string
	Before string
	After  string
}

func (d *Diff) Render(format Format) Output {
	c, _ := Get("diff")
	if c == nil {
		return Output{}
	}
	cfg := `{"title":"` + escapeJSON(d.Title) + `","rows":[`
	for i, r := range d.Rows {
		if i > 0 {
			cfg += ","
		}
		cfg += `{"metric":"` + escapeJSON(r.Metric) + `","before":"` + escapeJSON(r.Before) + `","after":"` + escapeJSON(r.After) + `"}`
	}
	cfg += `]}`
	out, _ := c.Render([]byte(cfg), format)
	return out
}

// Heatmap renders a 2D value distribution (legacy interface)
type Heatmap struct {
	Title   string
	XLabels []string
	YLabels []string
	Values  [][]float64
}

func (h *Heatmap) Render(format Format) Output {
	c, _ := Get("heatmap")
	if c == nil {
		return Output{}
	}
	cfg := `{"title":"` + escapeJSON(h.Title) + `","x_labels":[`
	for i, l := range h.XLabels {
		if i > 0 {
			cfg += ","
		}
		cfg += `"` + escapeJSON(l) + `"`
	}
	cfg += `],"y_labels":[`
	for i, l := range h.YLabels {
		if i > 0 {
			cfg += ","
		}
		cfg += `"` + escapeJSON(l) + `"`
	}
	cfg += `],"values":[`
	for i, row := range h.Values {
		if i > 0 {
			cfg += ","
		}
		cfg += `[`
		for j, v := range row {
			if j > 0 {
				cfg += ","
			}
			cfg += formatFloat(v)
		}
		cfg += `]`
	}
	cfg += `]}`
	out, _ := c.Render([]byte(cfg), format)
	return out
}

// ThresholdBar renders a bar with warning/critical markers (legacy interface)
type ThresholdBar struct {
	Label string
	Value float64
	Max   float64
	Warn  float64
	Crit  float64
	Unit  string
}

func (t *ThresholdBar) Render(format Format) Output {
	c, _ := Get("threshold-bar")
	if c == nil {
		return Output{}
	}
	cfg := `{"label":"` + escapeJSON(t.Label) + `","value":` + formatFloat(t.Value) + `,"max":` + formatFloat(t.Max) + `,"warn":` + formatFloat(t.Warn) + `,"crit":` + formatFloat(t.Crit) + `,"unit":"` + escapeJSON(t.Unit) + `"}`
	out, _ := c.Render([]byte(cfg), format)
	return out
}

// StatGroup renders multiple related stats in one card (legacy interface)
type StatGroup struct {
	Title string
	Stats []Stat
}

type Stat struct {
	Label string
	Value string
}

func (s *StatGroup) Render(format Format) Output {
	c, _ := Get("stat-group")
	if c == nil {
		return Output{}
	}
	cfg := `{"title":"` + escapeJSON(s.Title) + `","stats":[`
	for i, st := range s.Stats {
		if i > 0 {
			cfg += ","
		}
		cfg += `{"label":"` + escapeJSON(st.Label) + `","value":"` + escapeJSON(st.Value) + `"}`
	}
	cfg += `]}`
	out, _ := c.Render([]byte(cfg), format)
	return out
}
