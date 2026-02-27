package mcp

import (
	"context"
	"fmt"
	"strings"

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

	window := clampInt(in.Window, minWindow, maxWindow, 60) // default 60 for compare

	// Build parameterized IN clause for services
	placeholders := make([]string, len(in.Services))
	args := make([]any, len(in.Services))
	for i, svc := range in.Services {
		placeholders[i] = "?"
		args[i] = svc
	}

	// Query metrics for all services at once
	q := fmt.Sprintf(`
		SELECT
			service,
			COALESCE(SUM(spans), 0) AS requests,
			COALESCE(AVG(CASE WHEN spans > 0 THEN error_rate END), 0) AS error_rate,
			COALESCE(AVG(CASE WHEN spans > 0 THEN p50_ms END), 0) AS p50_ms,
			COALESCE(AVG(CASE WHEN spans > 0 THEN p95_ms END), 0) AS p95_ms,
			COALESCE(SUM(log_count), 0) AS log_count,
			COALESCE(SUM(metric_count), 0) AS metric_count
		FROM service_rollup
		WHERE service IN (%s) AND bucket >= NOW() - INTERVAL '%d minutes'
		GROUP BY service
		ORDER BY (COALESCE(SUM(spans), 0) + COALESCE(SUM(log_count), 0) + COALESCE(SUM(metric_count), 0)) DESC
	`, strings.Join(placeholders, ","), window)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, CompareOut{}, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	// Parse results
	var metrics []CompareMetrics
	for rows.Next() {
		var m CompareMetrics
		var logCount, metricCount int64
		if err := rows.Scan(&m.Service, &m.Requests, &m.ErrorRate, &m.P50Ms, &m.P95Ms, &logCount, &metricCount); err != nil {
			continue
		}
		// Count all signals for determining if service has data
		if m.Requests == 0 && (logCount > 0 || metricCount > 0) {
			m.Requests = logCount + metricCount
		}
		m.ErrorCount = int64(float64(m.Requests) * m.ErrorRate)
		if m.Requests > 0 {
			m.AvgMs = (m.P50Ms + m.P95Ms) / 2
		}
		metrics = append(metrics, m)
	}
	if err := rows.Err(); err != nil {
		return nil, CompareOut{}, fmt.Errorf("compare iteration: %w", err)
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
