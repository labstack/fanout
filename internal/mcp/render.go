package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/labstack/fanout/internal/render"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Report stores a shareable report snapshot
type Report struct {
	ID        string    `json:"id"`
	Query     string    `json:"query"`
	Summary   string    `json:"summary"`
	HTML      string    `json:"html"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ReportStore persists reports to disk
type ReportStore struct {
	mu  sync.RWMutex
	dir string
}

var reports = &ReportStore{
	dir: "lake/reports",
}

func (rs *ReportStore) Save(r *Report) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	// Ensure directory exists
	os.MkdirAll(rs.dir, 0755)

	// Write report to JSON file
	data, _ := json.Marshal(r)
	os.WriteFile(rs.dir+"/"+r.ID+".json", data, 0644)
}

func (rs *ReportStore) Get(id string) *Report {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	data, err := os.ReadFile(rs.dir + "/" + id + ".json")
	if err != nil {
		return nil
	}

	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil
	}
	return &r
}

// List returns all reports sorted by creation time (newest first)
func (rs *ReportStore) List() []*Report {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	entries, err := os.ReadDir(rs.dir)
	if err != nil {
		return nil
	}

	var reports []*Report
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(rs.dir + "/" + e.Name())
		if err != nil {
			continue
		}
		var r Report
		if err := json.Unmarshal(data, &r); err != nil {
			continue
		}
		reports = append(reports, &r)
	}

	// Sort by created_at descending
	for i := 0; i < len(reports)-1; i++ {
		for j := i + 1; j < len(reports); j++ {
			if reports[j].CreatedAt.After(reports[i].CreatedAt) {
				reports[i], reports[j] = reports[j], reports[i]
			}
		}
	}
	return reports
}

// Delete removes a report by ID
func (rs *ReportStore) Delete(id string) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	err := os.Remove(rs.dir + "/" + id + ".json")
	return err == nil
}

// Cleanup removes expired reports, returns count deleted
func (rs *ReportStore) Cleanup() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	entries, err := os.ReadDir(rs.dir)
	if err != nil {
		return 0
	}

	now := time.Now()
	deleted := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := rs.dir + "/" + e.Name()
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var r Report
		if err := json.Unmarshal(data, &r); err != nil {
			continue
		}
		if now.After(r.ExpiresAt) {
			os.Remove(path)
			deleted++
		}
	}
	return deleted
}

// render - Universal HTML renderer for LLM-generated reports

type RenderIn struct {
	Title    string    `json:"title" jsonschema:"Report title,required"`
	Sections []Section `json:"sections" jsonschema:"Report sections,required"`
}

type Section struct {
	Type   string `json:"type" jsonschema:"Component type: metric|table|chart|text|grid|panel|badge|bar|sparkline"`
	Title  string `json:"title,omitempty"`
	Config any    `json:"config"`
}

type RenderOut struct {
	HTML     string `json:"html"`
	ShareURL string `json:"share_url"`
	ReportID string `json:"report_id"`
}

// Component configs
type MetricConfig struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Unit  string `json:"unit,omitempty"`
	Trend string `json:"trend,omitempty"` // up, down, stable
}

type TableConfig struct {
	Title    string     `json:"title,omitempty"`
	Headers  []string   `json:"headers"`
	Rows     [][]string `json:"rows"`
	MaxWidth int        `json:"max_width,omitempty"`
}

type TextConfig struct {
	Content string `json:"content"`
	Style   string `json:"style,omitempty"` // bold, dim
}

type BadgeConfig struct {
	Label  string `json:"label"`
	Status string `json:"status"` // healthy, degraded, unhealthy, info, warning, error
}

type BarConfig struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Max   float64 `json:"max"`
	Unit  string  `json:"unit,omitempty"`
}

type SparklineConfig struct {
	Label  string    `json:"label"`
	Values []float64 `json:"values"`
}

type GridConfig struct {
	Title string    `json:"title,omitempty"`
	Items []Section `json:"items"`
	Cols  int       `json:"cols,omitempty"` // 2, 3, or 4
}

type PanelConfig struct {
	Title   string    `json:"title"`
	Content []Section `json:"content"`
}

// ChartConfig holds Vega-Lite spec
type ChartConfig struct {
	Spec json.RawMessage `json:"spec,omitempty"` // Full Vega-Lite spec
	// Or individual fields for simple charts
	Mark     string          `json:"mark,omitempty"`     // line, bar, point, area
	Data     json.RawMessage `json:"data,omitempty"`     // {values: [...]}
	Encoding json.RawMessage `json:"encoding,omitempty"` // {x: {...}, y: {...}}
	Width    int             `json:"width,omitempty"`
	Height   int             `json:"height,omitempty"`
}

func (s *Server) render(ctx context.Context, req *mcp.CallToolRequest, in RenderIn) (*mcp.CallToolResult, RenderOut, error) {
	// Build components
	var htmlParts []string

	for _, sec := range in.Sections {
		html, err := renderSection(sec)
		if err != nil {
			return nil, RenderOut{}, fmt.Errorf("section %q: %w", sec.Type, err)
		}
		htmlParts = append(htmlParts, html)
	}

	// Compose final HTML
	html := `<div class="compose compose-column">` + strings.Join(htmlParts, "") + `</div>`

	// Save report
	id := genID()
	report := &Report{
		ID:        id,
		Query:     in.Title,
		Summary:   in.Title,
		HTML:      html,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	reports.Save(report)

	return nil, RenderOut{
		HTML:     html,
		ShareURL: "/view/r/" + id,
		ReportID: id,
	}, nil
}

func renderSection(sec Section) (string, error) {
	// Marshal config to JSON for unmarshaling into specific types
	configJSON, err := json.Marshal(sec.Config)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}

	switch sec.Type {
	case "metric":
		var cfg MetricConfig
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return "", err
		}
		m := &render.Metric{Label: cfg.Label, Value: cfg.Value, Unit: cfg.Unit, Trend: cfg.Trend}
		return m.Render(render.HTML).HTML, nil

	case "table":
		var cfg TableConfig
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return "", err
		}
		title := cfg.Title
		if title == "" {
			title = sec.Title
		}
		t := &render.Table{Title: title, Headers: cfg.Headers, Rows: cfg.Rows, MaxWidth: cfg.MaxWidth}
		return t.Render(render.HTML).HTML, nil

	case "text":
		var cfg TextConfig
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return "", err
		}
		t := &render.Text{Content: cfg.Content, Style: cfg.Style}
		return t.Render(render.HTML).HTML, nil

	case "badge":
		var cfg BadgeConfig
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return "", err
		}
		b := &render.Badge{Label: cfg.Label, Status: cfg.Status}
		return b.Render(render.HTML).HTML, nil

	case "bar":
		var cfg BarConfig
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return "", err
		}
		b := &render.Bar{Label: cfg.Label, Value: cfg.Value, Max: cfg.Max, Unit: cfg.Unit}
		return b.Render(render.HTML).HTML, nil

	case "sparkline":
		var cfg SparklineConfig
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return "", err
		}
		sp := &render.Sparkline{Label: cfg.Label, Values: cfg.Values}
		return sp.Render(render.HTML).HTML, nil

	case "grid":
		var cfg GridConfig
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return "", err
		}
		cols := cfg.Cols
		if cols == 0 {
			cols = 2
		}
		var items []render.Renderer
		for _, item := range cfg.Items {
			html, err := renderSection(item)
			if err != nil {
				return "", err
			}
			items = append(items, &rawHTML{html})
		}
		g := &render.Grid{Title: cfg.Title, Items: items, Cols: cols}
		return g.Render(render.HTML).HTML, nil

	case "panel":
		var cfg PanelConfig
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return "", err
		}
		var content []render.Renderer
		for _, item := range cfg.Content {
			html, err := renderSection(item)
			if err != nil {
				return "", err
			}
			content = append(content, &rawHTML{html})
		}
		p := &render.Panel{Title: cfg.Title, Content: content}
		return p.Render(render.HTML).HTML, nil

	case "chart":
		var cfg ChartConfig
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return "", err
		}
		return renderChart(sec.Title, cfg), nil

	default:
		return "", fmt.Errorf("unknown component type: %s", sec.Type)
	}
}

// rawHTML wraps pre-rendered HTML
type rawHTML struct {
	html string
}

func (r *rawHTML) Render(format render.Format) render.Output {
	return render.Output{HTML: r.html}
}

func renderChart(title string, cfg ChartConfig) string {
	var spec json.RawMessage

	if cfg.Spec != nil {
		spec = cfg.Spec
	} else {
		// Build spec from parts
		width := cfg.Width
		if width == 0 {
			width = 400
		}
		height := cfg.Height
		if height == 0 {
			height = 200
		}

		built := map[string]any{
			"$schema": "https://vega.github.io/schema/vega-lite/v5.json",
			"width":   width,
			"height":  height,
		}
		if cfg.Mark != "" {
			built["mark"] = cfg.Mark
		}
		if cfg.Data != nil {
			var data any
			json.Unmarshal(cfg.Data, &data)
			built["data"] = data
		}
		if cfg.Encoding != nil {
			var enc any
			json.Unmarshal(cfg.Encoding, &enc)
			built["encoding"] = enc
		}
		spec, _ = json.Marshal(built)
	}

	// Escape spec for HTML attribute
	escaped := strings.ReplaceAll(string(spec), `"`, `&quot;`)

	html := `<sl-card>`
	if title != "" {
		html += `<div slot="header">` + title + `</div>`
	}
	html += `<div class="chart" data-vega="` + escaped + `"></div></sl-card>`
	return html
}

func genID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// GetReport retrieves a report by ID (used by HTTP handler)
func GetReport(id string) *Report {
	return reports.Get(id)
}

// ListReports returns all reports (used by HTTP handler)
func ListReports() []*Report {
	return reports.List()
}

// DeleteReport removes a report by ID (used by HTTP handler)
func DeleteReport(id string) bool {
	return reports.Delete(id)
}

// RunCleanup starts a background goroutine to clean expired reports
func RunCleanup(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Run once at startup
	reports.Cleanup()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reports.Cleanup()
		}
	}
}
