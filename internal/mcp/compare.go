package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/render"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// compare - Side-by-side service comparison

type CompareIn struct {
	Services []string `json:"services" jsonschema:"Services to compare (2-4),required"`
	Window   int      `json:"window,omitempty" jsonschema:"Time window in minutes,default=60"`
	Format   string   `json:"format,omitempty" jsonschema:"Output format: ascii, html, both, data (default=ascii)"`
}

type CompareMetrics struct {
	Service    string  `json:"service"`
	Requests   int64   `json:"requests"`
	ErrorRate  float64 `json:"error_rate"`
	P50Ms      float64 `json:"p50_ms"`
	P95Ms      float64 `json:"p95_ms"`
	AvgMs      float64 `json:"avg_ms"`
	ErrorCount int64   `json:"error_count"`
}

type CompareOut struct {
	Services []CompareMetrics `json:"services"`
	Winner   string           `json:"winner"`
	Summary  string           `json:"summary"`
	Render   *render.Output   `json:"render,omitempty"`
}

func (s *Server) compare(ctx context.Context, req *mcp.CallToolRequest, in CompareIn) (*mcp.CallToolResult, CompareOut, error) {
	if len(in.Services) < 2 {
		return nil, CompareOut{}, fmt.Errorf("need at least 2 services to compare")
	}
	if len(in.Services) > 4 {
		return nil, CompareOut{}, fmt.Errorf("max 4 services to compare")
	}

	window := in.Window
	if window == 0 {
		window = 60
	}

	// Build IN clause for services
	quoted := make([]string, len(in.Services))
	for i, svc := range in.Services {
		quoted[i] = fmt.Sprintf("'%s'", strings.ReplaceAll(svc, "'", "''"))
	}

	// Query metrics for all services at once
	sql := fmt.Sprintf(`
		SELECT
			service,
			COALESCE(SUM(spans), 0) as requests,
			COALESCE(AVG(error_rate), 0) as error_rate,
			COALESCE(AVG(p50_ms), 0) as p50_ms,
			COALESCE(AVG(p95_ms), 0) as p95_ms
		FROM service_rollup
		WHERE service IN (%s) AND bucket >= NOW() - INTERVAL '%d minutes'
		GROUP BY service
		ORDER BY requests DESC
	`, strings.Join(quoted, ","), window)

	resp := s.duck.ExecuteSQL(ctx, query.SQLRequest{Query: sql, MaxRows: 10})
	if resp.Error != "" {
		return nil, CompareOut{}, fmt.Errorf("query failed: %s", resp.Error)
	}

	// Parse results
	var metrics []CompareMetrics
	for _, row := range resp.Results {
		m := CompareMetrics{
			Service:   getString(row, "service"),
			Requests:  getInt64(row, "requests"),
			ErrorRate: getFloat64(row, "error_rate"),
			P50Ms:     getFloat64(row, "p50_ms"),
			P95Ms:     getFloat64(row, "p95_ms"),
		}
		m.ErrorCount = int64(float64(m.Requests) * m.ErrorRate)
		if m.Requests > 0 {
			m.AvgMs = (m.P50Ms + m.P95Ms) / 2
		}
		metrics = append(metrics, m)
	}

	// Add empty entries for services with no data
	found := make(map[string]bool)
	for _, m := range metrics {
		found[m.Service] = true
	}
	for _, svc := range in.Services {
		if !found[svc] {
			metrics = append(metrics, CompareMetrics{Service: svc})
		}
	}

	// Determine winner (lowest P95 with acceptable error rate)
	winner := ""
	bestScore := float64(-1)
	for _, m := range metrics {
		if m.Requests == 0 {
			continue
		}
		// Score: lower is better (P95 * (1 + error_rate*10))
		score := m.P95Ms * (1 + m.ErrorRate*10)
		if bestScore < 0 || score < bestScore {
			bestScore = score
			winner = m.Service
		}
	}

	// Build summary
	summary := fmt.Sprintf("Compared %d services over %d minutes. ", len(metrics), window)
	if winner != "" {
		summary += fmt.Sprintf("%s has best performance.", winner)
	}

	out := CompareOut{
		Services: metrics,
		Winner:   winner,
		Summary:  summary,
	}

	// Render
	format := parseFormat(in.Format)
	if format != render.Data {
		rendered := renderCompare(&out)
		out.Render = &rendered
	}

	return nil, out, nil
}

func renderCompare(c *CompareOut) render.Output {
	// Build comparison table
	headers := []string{"Service", "Requests", "Error Rate", "P50", "P95"}
	var rows [][]string
	for _, m := range c.Services {
		status := ""
		if m.Service == c.Winner {
			status = " ★"
		}
		rows = append(rows, []string{
			m.Service + status,
			fmt.Sprintf("%d", m.Requests),
			fmt.Sprintf("%.2f%%", m.ErrorRate*100),
			fmt.Sprintf("%.1fms", m.P50Ms),
			fmt.Sprintf("%.1fms", m.P95Ms),
		})
	}

	table := &render.Table{
		Title:   "Service Comparison",
		Headers: headers,
		Rows:    rows,
	}

	// Winner badge
	var badge *render.Badge
	if c.Winner != "" {
		badge = &render.Badge{Label: c.Winner + " wins", Status: "healthy"}
	}

	// Compose
	items := []render.Renderer{
		&render.Text{Content: c.Summary},
		table,
	}
	if badge != nil {
		items = append(items, badge)
	}

	composed := &render.Compose{
		Vertical: true,
		Items:    items,
	}

	return composed.Render(render.Both)
}

// Helper functions for row parsing
func getString(row map[string]interface{}, key string) string {
	if v, ok := row[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getInt64(row map[string]interface{}, key string) int64 {
	if v, ok := row[key]; ok {
		switch n := v.(type) {
		case int64:
			return n
		case float64:
			return int64(n)
		case int:
			return int64(n)
		}
	}
	return 0
}

func getFloat64(row map[string]interface{}, key string) float64 {
	if v, ok := row[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int64:
			return float64(n)
		case int:
			return float64(n)
		}
	}
	return 0
}
