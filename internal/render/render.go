package render

import (
	"strings"
)

// Format specifies output format
type Format string

const (
	ASCII Format = "ascii"
	HTML  Format = "html"
	Both  Format = "both"
	Data  Format = "data" // raw JSON only
)

// Output contains rendered content
type Output struct {
	ASCII string `json:"ascii,omitempty"`
	HTML  string `json:"html,omitempty"`
	Title string `json:"title,omitempty"`
}

// Renderer renders data to ASCII and HTML
type Renderer interface {
	Render(format Format) Output
}

// Table renders tabular data
type Table struct {
	Title    string
	Headers  []string
	Rows     [][]string
	MaxWidth int // optional max column width
}

// Tree renders hierarchical data
type Tree struct {
	Title string
	Root  *Node
}

// Node is a tree node
type Node struct {
	Label    string
	Value    string
	Children []*Node
	Meta     map[string]string // extra data like status, duration
}

// Badge renders a status badge
type Badge struct {
	Label  string
	Status string // healthy, degraded, unhealthy, info, warning, error
}

// Bar renders a progress/percentage bar
type Bar struct {
	Label string
	Value float64
	Max   float64
	Unit  string
}

// Sparkline renders a mini chart
type Sparkline struct {
	Label  string
	Values []float64
}

// Grid renders multiple items in a grid layout
type Grid struct {
	Title string
	Items []Renderer
	Cols  int
}

// Panel groups content with a title
type Panel struct {
	Title   string
	Content []Renderer
}

// Text renders plain text
type Text struct {
	Content string
	Style   string // bold, dim, etc.
}

// Metric renders a single metric value
type Metric struct {
	Label string
	Value string
	Unit  string
	Trend string // up, down, stable
}

// Compose combines multiple renderers
type Compose struct {
	Title    string
	Items    []Renderer
	Vertical bool // stack vertically vs horizontally
}

// Render implements Renderer
func (t *Table) Render(format Format) Output {
	var out Output
	out.Title = t.Title
	if format == ASCII || format == Both {
		out.ASCII = t.renderASCII()
	}
	if format == HTML || format == Both {
		out.HTML = t.renderHTML()
	}
	return out
}

func (t *Table) renderASCII() string {
	if len(t.Headers) == 0 && len(t.Rows) == 0 {
		return ""
	}

	// Calculate column widths
	widths := make([]int, len(t.Headers))
	for i, h := range t.Headers {
		widths[i] = len(h)
	}
	for _, row := range t.Rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Apply max width
	if t.MaxWidth > 0 {
		for i := range widths {
			if widths[i] > t.MaxWidth {
				widths[i] = t.MaxWidth
			}
		}
	}

	var sb strings.Builder

	// Title
	if t.Title != "" {
		sb.WriteString(t.Title + "\n")
	}

	// Header
	sb.WriteString("┌")
	for i, w := range widths {
		sb.WriteString(strings.Repeat("─", w+2))
		if i < len(widths)-1 {
			sb.WriteString("┬")
		}
	}
	sb.WriteString("┐\n")

	sb.WriteString("│")
	for i, h := range t.Headers {
		sb.WriteString(" " + padRight(h, widths[i]) + " │")
	}
	sb.WriteString("\n")

	sb.WriteString("├")
	for i, w := range widths {
		sb.WriteString(strings.Repeat("─", w+2))
		if i < len(widths)-1 {
			sb.WriteString("┼")
		}
	}
	sb.WriteString("┤\n")

	// Rows
	for _, row := range t.Rows {
		sb.WriteString("│")
		for i, cell := range row {
			if i < len(widths) {
				c := cell
				if len(c) > widths[i] {
					c = c[:widths[i]-1] + "…"
				}
				sb.WriteString(" " + padRight(c, widths[i]) + " │")
			}
		}
		sb.WriteString("\n")
	}

	// Bottom
	sb.WriteString("└")
	for i, w := range widths {
		sb.WriteString(strings.Repeat("─", w+2))
		if i < len(widths)-1 {
			sb.WriteString("┴")
		}
	}
	sb.WriteString("┘")

	return sb.String()
}

func (t *Table) renderHTML() string {
	var sb strings.Builder
	sb.WriteString(`<table class="table">`)
	if t.Title != "" {
		sb.WriteString(`<caption>` + t.Title + `</caption>`)
	}
	sb.WriteString(`<thead><tr>`)
	for _, h := range t.Headers {
		sb.WriteString(`<th>` + h + `</th>`)
	}
	sb.WriteString(`</tr></thead><tbody>`)
	for _, row := range t.Rows {
		sb.WriteString(`<tr>`)
		for _, cell := range row {
			sb.WriteString(`<td>` + cell + `</td>`)
		}
		sb.WriteString(`</tr>`)
	}
	sb.WriteString(`</tbody></table>`)
	return sb.String()
}

// Render implements Renderer
func (b *Badge) Render(format Format) Output {
	var out Output
	if format == ASCII || format == Both {
		out.ASCII = b.renderASCII()
	}
	if format == HTML || format == Both {
		out.HTML = b.renderHTML()
	}
	return out
}

func (b *Badge) renderASCII() string {
	icon := "●"
	switch b.Status {
	case "healthy", "success":
		icon = "✓"
	case "degraded", "warning":
		icon = "!"
	case "unhealthy", "error":
		icon = "✗"
	case "info":
		icon = "i"
	}
	return "[" + icon + " " + b.Label + "]"
}

func (b *Badge) renderHTML() string {
	variant := "neutral"
	switch b.Status {
	case "healthy", "success":
		variant = "success"
	case "degraded", "warning":
		variant = "warning"
	case "unhealthy", "error":
		variant = "danger"
	case "info":
		variant = "primary"
	}
	return `<sl-badge variant="` + variant + `">` + b.Label + `</sl-badge>`
}

// Render implements Renderer
func (b *Bar) Render(format Format) Output {
	var out Output
	out.Title = b.Label
	if format == ASCII || format == Both {
		out.ASCII = b.renderASCII()
	}
	if format == HTML || format == Both {
		out.HTML = b.renderHTML()
	}
	return out
}

func (b *Bar) renderASCII() string {
	pct := b.Value / b.Max
	if pct > 1 {
		pct = 1
	}
	if pct < 0 {
		pct = 0
	}
	width := 20
	filled := int(pct * float64(width))
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return b.Label + " " + bar + " " + formatFloat(b.Value) + b.Unit
}

func (b *Bar) renderHTML() string {
	pct := int(b.Value / b.Max * 100)
	if pct > 100 {
		pct = 100
	}
	return `<div class="bar-container"><span class="bar-label">` + b.Label + `</span>` +
		`<sl-progress-bar value="` + itoa(pct) + `"></sl-progress-bar>` +
		`<span class="bar-value">` + formatFloat(b.Value) + b.Unit + `</span></div>`
}

// Render implements Renderer
func (s *Sparkline) Render(format Format) Output {
	var out Output
	out.Title = s.Label
	if format == ASCII || format == Both {
		out.ASCII = s.renderASCII()
	}
	if format == HTML || format == Both {
		out.HTML = s.renderHTML()
	}
	return out
}

func (s *Sparkline) renderASCII() string {
	if len(s.Values) == 0 {
		return s.Label + " ─"
	}
	chars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	min, max := s.Values[0], s.Values[0]
	for _, v := range s.Values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	rng := max - min
	if rng == 0 {
		rng = 1
	}
	var sb strings.Builder
	sb.WriteString(s.Label + " ")
	for _, v := range s.Values {
		idx := int((v - min) / rng * float64(len(chars)-1))
		sb.WriteRune(chars[idx])
	}
	return sb.String()
}

func (s *Sparkline) renderHTML() string {
	// Simple SVG sparkline
	if len(s.Values) == 0 {
		return `<span>` + s.Label + `</span>`
	}
	width := 100
	height := 20
	min, max := s.Values[0], s.Values[0]
	for _, v := range s.Values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	rng := max - min
	if rng == 0 {
		rng = 1
	}

	var points strings.Builder
	for i, v := range s.Values {
		x := float64(i) / float64(len(s.Values)-1) * float64(width)
		y := float64(height) - (v-min)/rng*float64(height)
		if i > 0 {
			points.WriteString(" ")
		}
		points.WriteString(formatFloat(x) + "," + formatFloat(y))
	}

	return `<div class="sparkline"><span>` + s.Label + `</span>` +
		`<svg width="` + itoa(width) + `" height="` + itoa(height) + `">` +
		`<polyline points="` + points.String() + `" fill="none" stroke="var(--accent)" stroke-width="1.5"/></svg></div>`
}

// Render implements Renderer
func (m *Metric) Render(format Format) Output {
	var out Output
	if format == ASCII || format == Both {
		out.ASCII = m.renderASCII()
	}
	if format == HTML || format == Both {
		out.HTML = m.renderHTML()
	}
	return out
}

func (m *Metric) renderASCII() string {
	trend := ""
	switch m.Trend {
	case "up":
		trend = " ↑"
	case "down":
		trend = " ↓"
	}
	return m.Label + ": " + m.Value + m.Unit + trend
}

func (m *Metric) renderHTML() string {
	trend := ""
	switch m.Trend {
	case "up":
		trend = `<sl-icon name="arrow-up"></sl-icon>`
	case "down":
		trend = `<sl-icon name="arrow-down"></sl-icon>`
	}
	return `<div class="metric"><span class="metric-value">` + m.Value + m.Unit + `</span>` +
		trend + `<span class="metric-label">` + m.Label + `</span></div>`
}

// Render implements Renderer
func (t *Tree) Render(format Format) Output {
	var out Output
	out.Title = t.Title
	if format == ASCII || format == Both {
		out.ASCII = t.renderASCII()
	}
	if format == HTML || format == Both {
		out.HTML = t.renderHTML()
	}
	return out
}

func (t *Tree) renderASCII() string {
	var sb strings.Builder
	if t.Title != "" {
		sb.WriteString(t.Title + "\n")
	}
	if t.Root != nil {
		renderNode(&sb, t.Root, "", true)
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

func renderNode(sb *strings.Builder, n *Node, prefix string, last bool) {
	connector := "├── "
	if last {
		connector = "└── "
	}

	line := prefix + connector + n.Label
	if n.Value != "" {
		line += ": " + n.Value
	}
	if status, ok := n.Meta["status"]; ok {
		line += " [" + status + "]"
	}
	if dur, ok := n.Meta["duration"]; ok {
		line += " (" + dur + ")"
	}
	sb.WriteString(line + "\n")

	childPrefix := prefix
	if last {
		childPrefix += "    "
	} else {
		childPrefix += "│   "
	}

	for i, child := range n.Children {
		renderNode(sb, child, childPrefix, i == len(n.Children)-1)
	}
}

func (t *Tree) renderHTML() string {
	var sb strings.Builder
	sb.WriteString(`<sl-tree>`)
	if t.Root != nil {
		renderHTMLNode(&sb, t.Root)
	}
	sb.WriteString(`</sl-tree>`)
	return sb.String()
}

func renderHTMLNode(sb *strings.Builder, n *Node) {
	sb.WriteString(`<sl-tree-item>`)
	sb.WriteString(n.Label)
	if n.Value != "" {
		sb.WriteString(`: <span class="tree-value">` + n.Value + `</span>`)
	}
	if status, ok := n.Meta["status"]; ok {
		sb.WriteString(` <sl-badge variant="` + statusVariant(status) + `" size="small">` + status + `</sl-badge>`)
	}
	for _, child := range n.Children {
		renderHTMLNode(sb, child)
	}
	sb.WriteString(`</sl-tree-item>`)
}

// Render implements Renderer
func (c *Compose) Render(format Format) Output {
	var out Output
	out.Title = c.Title

	var asciiParts, htmlParts []string
	for _, item := range c.Items {
		r := item.Render(format)
		if r.ASCII != "" {
			asciiParts = append(asciiParts, r.ASCII)
		}
		if r.HTML != "" {
			htmlParts = append(htmlParts, r.HTML)
		}
	}

	if format == ASCII || format == Both {
		sep := "  "
		if c.Vertical {
			sep = "\n\n"
		}
		if c.Title != "" {
			out.ASCII = c.Title + "\n" + strings.Join(asciiParts, sep)
		} else {
			out.ASCII = strings.Join(asciiParts, sep)
		}
	}
	if format == HTML || format == Both {
		dir := "row"
		if c.Vertical {
			dir = "column"
		}
		out.HTML = `<div class="compose compose-` + dir + `">` + strings.Join(htmlParts, "") + `</div>`
	}
	return out
}

// Render implements Renderer
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
		content := strings.Join(asciiParts, "\n")
		out.ASCII = boxASCII(p.Title, content)
	}
	if format == HTML || format == Both {
		out.HTML = `<sl-card><div slot="header">` + p.Title + `</div>` +
			strings.Join(htmlParts, "") + `</sl-card>`
	}
	return out
}

// Render implements Renderer
func (t *Text) Render(format Format) Output {
	var out Output
	if format == ASCII || format == Both {
		out.ASCII = t.Content
	}
	if format == HTML || format == Both {
		class := ""
		if t.Style != "" {
			class = ` class="text-` + t.Style + `"`
		}
		out.HTML = `<span` + class + `>` + t.Content + `</span>`
	}
	return out
}

// Render implements Renderer
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
		// Simple vertical layout for ASCII
		if g.Title != "" {
			out.ASCII = g.Title + "\n" + strings.Join(asciiParts, "\n")
		} else {
			out.ASCII = strings.Join(asciiParts, "\n")
		}
	}
	if format == HTML || format == Both {
		out.HTML = `<div class="grid grid-` + itoa(g.Cols) + `">` +
			strings.Join(htmlParts, "") + `</div>`
	}
	return out
}

// Helpers
func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func formatFloat(f float64) string {
	if f == float64(int(f)) {
		return itoa(int(f))
	}
	// Format with 2 decimal places
	i := int(f * 100)
	whole := i / 100
	frac := i % 100
	if frac < 0 {
		frac = -frac
	}
	if frac == 0 {
		return itoa(whole)
	}
	if frac%10 == 0 {
		return itoa(whole) + "." + itoa(frac/10)
	}
	fracStr := itoa(frac)
	if frac < 10 {
		fracStr = "0" + fracStr
	}
	return itoa(whole) + "." + fracStr
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var sb strings.Builder
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		sb.WriteByte(byte('0' + i%10))
		i /= 10
	}
	if neg {
		sb.WriteByte('-')
	}
	// Reverse
	s := sb.String()
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

func statusVariant(status string) string {
	switch status {
	case "healthy", "success", "ok":
		return "success"
	case "degraded", "warning", "slow":
		return "warning"
	case "unhealthy", "error", "failed":
		return "danger"
	default:
		return "neutral"
	}
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
