package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MetricSummary represents aggregated metric info.
type MetricSummary struct {
	Name     string
	Type     string
	Count    int64
	Avg      float64
	Min      float64
	Max      float64
	Services []string
	Trend    []float64
}

// MetricsResult contains metric summaries.
type MetricsResult struct {
	Metrics []MetricSummary
}

// MetricsParams contains metric search parameters.
type MetricsParams struct {
	Names     []string
	Services  []string
	Types     []string
	Terms     []string
	Window    int
	Namespace string
	TenantID  string
}

// Metrics returns aggregated metrics with sparklines.
func (s *Service) Metrics(ctx context.Context, p MetricsParams) (*MetricsResult, error) {
	if p.Window == 0 {
		p.Window = 60
	}

	p.Namespace, p.TenantID = s.defaults(p.Namespace, p.TenantID)
	metricsGlob := s.duck.MetricsGlob(p.TenantID, p.Namespace, p.Window)

	var filters []string
	var args []any

	filters = append(filters, fmt.Sprintf(
		`epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE`, p.Window))

	// Name filter
	for _, n := range p.Names {
		if containsWildcard(n) {
			filters = append(filters, `"name=name" ILIKE ?`)
			args = append(args, wildcardToLike(n))
		} else {
			filters = append(filters, `"name=name" = ?`)
			args = append(args, n)
		}
	}

	// Service filter
	if len(p.Services) > 0 {
		placeholders := makePlaceholders(len(p.Services))
		filters = append(filters, fmt.Sprintf(`"name=service_name" IN (%s)`, placeholders))
		for _, svc := range p.Services {
			args = append(args, svc)
		}
	}

	// Type filter
	if len(p.Types) > 0 {
		placeholders := makePlaceholders(len(p.Types))
		filters = append(filters, fmt.Sprintf(`"name=mtype" IN (%s)`, placeholders))
		for _, t := range p.Types {
			args = append(args, t)
		}
	}

	// Text search
	for _, term := range p.Terms {
		filters = append(filters, `"name=name" ILIKE ?`)
		args = append(args, "%"+term+"%")
	}

	whereClause := "WHERE " + strings.Join(filters, " AND ")

	q := fmt.Sprintf(`
SELECT
  "name=name" as metric_name,
  COALESCE("name=mtype", 'unknown') as mtype,
  COUNT(*) as cnt,
  AVG(COALESCE("name=value", 0)) as avg_val,
  MIN(COALESCE("name=value", 0)) as min_val,
  MAX(COALESCE("name=value", 0)) as max_val,
  LIST(DISTINCT "name=service_name") as services
FROM read_parquet(%s, union_by_name=true)
%s
GROUP BY "name=name", "name=mtype"
ORDER BY metric_name
LIMIT 100;
`, metricsGlob, whereClause)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return &MetricsResult{Metrics: []MetricSummary{}}, nil
	}
	defer rows.Close()

	var metrics []MetricSummary
	var metricNames []string

	for rows.Next() {
		var m MetricSummary
		var services any
		if err := rows.Scan(&m.Name, &m.Type, &m.Count, &m.Avg, &m.Min, &m.Max, &services); err != nil {
			continue
		}
		m.Services = parseServiceList(services)
		metricNames = append(metricNames, m.Name)
		metrics = append(metrics, m)
	}

	// Get sparklines
	if len(metricNames) > 0 {
		sparklines := s.metricSparklines(ctx, metricNames, p.Window, p.Namespace, p.TenantID)
		for i := range metrics {
			if trend, ok := sparklines[metrics[i].Name]; ok {
				metrics[i].Trend = trend
			}
		}
	}

	return &MetricsResult{Metrics: metrics}, nil
}

// MetricDetailResult contains time-series data for a single metric.
type MetricDetailResult struct {
	Name     string
	Type     string
	Services []string
	Count    int64
	Avg      float64
	Min      float64
	Max      float64
	Points   []MetricPoint
}

// MetricPoint is a single time-series point.
type MetricPoint struct {
	Time string
	Avg  float64
	Min  float64
	Max  float64
}

// MetricDetail returns time-series detail for a single metric.
func (s *Service) MetricDetail(ctx context.Context, name string, window int, namespace, tenantID string) (*MetricDetailResult, error) {
	if window == 0 {
		window = 60
	}
	namespace, tenantID = s.defaults(namespace, tenantID)
	metricsGlob := s.duck.MetricsGlob(tenantID, namespace, window)

	// Summary
	q := fmt.Sprintf(`
SELECT
  COALESCE("name=mtype", 'unknown') as mtype,
  COUNT(*) as cnt,
  AVG(COALESCE("name=value", 0)) as avg_val,
  MIN(COALESCE("name=value", 0)) as min_val,
  MAX(COALESCE("name=value", 0)) as max_val,
  LIST(DISTINCT "name=service_name") as services
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  AND "name=name" = ?
GROUP BY "name=mtype";
`, metricsGlob, window)

	out := &MetricDetailResult{Name: name}
	var services any
	row := s.duck.DB.QueryRowContext(ctx, q, name)
	if err := row.Scan(&out.Type, &out.Count, &out.Avg, &out.Min, &out.Max, &services); err != nil {
		slog.Warn("query failed", "method", "MetricDetail.summary", "metric", name, "err", err)
		return out, nil
	}
	out.Services = parseServiceList(services)

	// Time series
	bucketMins := window / 30
	if bucketMins < 1 {
		bucketMins = 1
	}
	tsQ := fmt.Sprintf(`
SELECT
  strftime(time_bucket(INTERVAL '%d minutes', epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT))), '%%Y-%%m-%%dT%%H:%%M:00Z') as bucket,
  AVG(COALESCE("name=value", 0)) as avg_val,
  MIN(COALESCE("name=value", 0)) as min_val,
  MAX(COALESCE("name=value", 0)) as max_val
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  AND "name=name" = ?
GROUP BY bucket
ORDER BY bucket ASC;
`, bucketMins, metricsGlob, window)

	rows, err := s.duck.DB.QueryContext(ctx, tsQ, name)
	if err != nil {
		slog.Warn("query failed", "method", "MetricDetail.timeseries", "metric", name, "err", err)
		return out, nil
	}
	defer rows.Close()

	for rows.Next() {
		var p MetricPoint
		if err := rows.Scan(&p.Time, &p.Avg, &p.Min, &p.Max); err != nil {
			slog.Warn("scan failed", "method", "MetricDetail.timeseries", "err", err)
			continue
		}
		out.Points = append(out.Points, p)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("iteration error", "method", "MetricDetail.timeseries", "err", err)
	}

	return out, nil
}

func (s *Service) metricSparklines(ctx context.Context, names []string, window int, namespace, tenantID string) map[string][]float64 {
	out := make(map[string][]float64)

	metricsGlob := s.duck.MetricsGlob(tenantID, namespace, window)
	bucketMins := window / 12
	if bucketMins < 1 {
		bucketMins = 1
	}

	placeholders := makePlaceholders(len(names))
	var args []any
	for _, n := range names {
		args = append(args, n)
	}

	q := fmt.Sprintf(`
SELECT
  "name=name" as metric_name,
  time_bucket(INTERVAL '%d minutes', epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT))) as bucket,
  AVG(COALESCE("name=value", 0)) as avg_val
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  AND "name=name" IN (%s)
GROUP BY "name=name", bucket
ORDER BY metric_name, bucket ASC;
`, bucketMins, metricsGlob, window, placeholders)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		slog.Warn("query failed", "method", "metricSparklines", "err", err)
		return out
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var bucket any
		var avg float64
		if err := rows.Scan(&name, &bucket, &avg); err != nil {
			slog.Warn("scan failed", "method", "metricSparklines", "err", err)
			continue
		}
		out[name] = append(out[name], avg)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("iteration error", "method", "metricSparklines", "err", err)
	}

	return out
}

// CompareResult contains comparison data for multiple services.
type CompareResult struct {
	Services []CompareService
	Winner   string
	Summary  string
}

// CompareService holds metrics for one service in a comparison.
type CompareService struct {
	Name       string
	Requests   int64
	ErrorRate  float64
	P50Ms      float64
	P95Ms      float64
	ErrorCount int64
}

// Compare returns side-by-side metrics for 2-4 services.
func (s *Service) Compare(ctx context.Context, services []string, window int, namespace, tenantID string) (*CompareResult, error) {
	if window == 0 {
		window = 60
	}
	namespace, tenantID = s.defaults(namespace, tenantID)

	placeholders := makePlaceholders(len(services))
	var args []any
	for _, svc := range services {
		args = append(args, svc)
	}

	// Use raw spans for accurate comparison scoped by namespace/tenant
	spansGlob := s.duck.SpansGlob(tenantID, namespace, window)
	q := fmt.Sprintf(`
SELECT
  "name=service_name" as service,
  COUNT(*)::BIGINT as requests,
  COALESCE(AVG(CASE WHEN "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END), 0) as error_rate,
  COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p50_ms,
  COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p95_ms
FROM read_parquet(%s, union_by_name=true, hive_partitioning=true)
WHERE "name=service_name" IN (%s)
  AND epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
GROUP BY "name=service_name"
ORDER BY requests DESC;
`, spansGlob, placeholders, window)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		slog.Warn("query failed", "method", "Compare", "err", err)
		return &CompareResult{}, nil
	}
	defer rows.Close()

	var metrics []CompareService
	for rows.Next() {
		var m CompareService
		if err := rows.Scan(&m.Name, &m.Requests, &m.ErrorRate, &m.P50Ms, &m.P95Ms); err != nil {
			slog.Warn("scan failed", "method", "Compare", "err", err)
			continue
		}
		m.ErrorCount = int64(float64(m.Requests) * m.ErrorRate)
		metrics = append(metrics, m)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("iteration error", "method", "Compare", "err", err)
	}

	// Add empty entries for services with no data
	found := make(map[string]bool)
	for _, m := range metrics {
		found[m.Name] = true
	}
	for _, svc := range services {
		if !found[svc] {
			metrics = append(metrics, CompareService{Name: svc})
		}
	}

	// Determine winner
	winner := ""
	bestScore := float64(-1)
	for _, m := range metrics {
		if m.Requests == 0 {
			continue
		}
		score := m.P95Ms * (1 + m.ErrorRate*10)
		if bestScore < 0 || score < bestScore {
			bestScore = score
			winner = m.Name
		}
	}

	summary := fmt.Sprintf("Compared %d services over %d minutes.", len(metrics), window)
	if winner != "" {
		summary += fmt.Sprintf(" %s has best performance.", winner)
	}

	return &CompareResult{
		Services: metrics,
		Winner:   winner,
		Summary:  summary,
	}, nil
}

// Namespaces discovers namespaces from filesystem.
func (s *Service) Namespaces(lakeDir, tenantID string) []string {
	if tenantID == "" {
		tenantID = s.cfg.TenantID.String()
	}

	var namespaces []string
	seen := make(map[string]bool)

	basePath := filepath.Join(lakeDir, "spans", fmt.Sprintf("tenant=%s", tenantID))
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return namespaces
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "namespace=") {
			ns := strings.TrimPrefix(entry.Name(), "namespace=")
			if ns != "" && !seen[ns] {
				seen[ns] = true
				namespaces = append(namespaces, ns)
			}
		}
	}

	sort.Strings(namespaces)
	return namespaces
}

// Helper functions

func makePlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	result := "?"
	for i := 1; i < n; i++ {
		result += ", ?"
	}
	return result
}

func containsWildcard(s string) bool {
	return strings.Contains(s, "*") || strings.Contains(s, "?")
}

func wildcardToLike(s string) string {
	s = strings.ReplaceAll(s, "*", "%")
	s = strings.ReplaceAll(s, "?", "_")
	return s
}

func parseServiceList(v any) []string {
	if v == nil {
		return nil
	}
	if list, ok := v.([]any); ok {
		var out []string
		for _, item := range list {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
