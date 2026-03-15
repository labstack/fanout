package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// validGroupByFields is the allowlist for group_by values.
var validGroupByFields = map[string]bool{
	"service":          true,
	"operation":        true,
	"status":           true,
	"kind":             true,
	"http.method":      true,
	"http.status_code": true,
}

// spanKindMap maps lowercase user-facing kind names to OTLP enum values.
var spanKindMap = map[string]string{
	"server":   "SPAN_KIND_SERVER",
	"client":   "SPAN_KIND_CLIENT",
	"producer": "SPAN_KIND_PRODUCER",
	"consumer": "SPAN_KIND_CONSUMER",
	"internal": "SPAN_KIND_INTERNAL",
}

// Spans searches, filters, or aggregates trace spans.
// When p.GroupBy is non-empty, returns aggregated SpanGroup results.
// Otherwise returns raw SpanRow results.
func (s *Service) Spans(ctx context.Context, p SpanParams) (*SpansResult, error) {
	if p.Window == 0 {
		p.Window = 15
	}
	if p.Limit == 0 {
		p.Limit = 100
	}
	if p.Status == "" {
		p.Status = "all"
	}

	p.Namespace, p.TenantID = s.defaults(p.Namespace, p.TenantID)

	// Validate group_by fields
	for _, field := range p.GroupBy {
		if !validGroupByFields[field] {
			return nil, fmt.Errorf("invalid group_by field %q: allowed fields are service, operation, status, kind, http.method, http.status_code", field)
		}
	}

	if len(p.GroupBy) > 0 {
		return s.spansGrouped(ctx, p)
	}
	return s.spansUngrouped(ctx, p)
}

// buildSpanFilters builds the WHERE clause filters and argument list for span queries.
// The `prefix` parameter is prepended to each filter (e.g. "AND ").
func buildSpanFilters(p SpanParams) (filters []string, args []any) {
	if p.Query != "" {
		escaped := strings.NewReplacer("%", "\\%", "_", "\\_").Replace(p.Query)
		filters = append(filters, `(operation ILIKE ? OR status_message ILIKE ?)`)
		args = append(args, "%"+escaped+"%", "%"+escaped+"%")
	}
	if p.Operation != "" {
		filters = append(filters, `operation = ?`)
		args = append(args, p.Operation)
	}
	if p.Service != "" {
		filters = append(filters, `service = ?`)
		args = append(args, p.Service)
	}
	switch p.Status {
	case "error":
		filters = append(filters, `status IN ('STATUS_CODE_ERROR', 'ERROR')`)
	case "ok":
		filters = append(filters, `status = 'STATUS_CODE_OK'`)
	case "slow":
		filters = append(filters, `duration_ms > 1000`)
	}
	if p.Kind != "" {
		otlpKind, ok := spanKindMap[strings.ToLower(p.Kind)]
		if !ok {
			// Pass through verbatim if not in the map (e.g. already SPAN_KIND_SERVER)
			otlpKind = p.Kind
		}
		filters = append(filters, `kind = ?`)
		args = append(args, otlpKind)
	}
	if p.MinDurationMs != nil {
		filters = append(filters, `duration_ms >= ?`)
		args = append(args, *p.MinDurationMs)
	}
	if p.MaxDurationMs != nil {
		filters = append(filters, `duration_ms <= ?`)
		args = append(args, *p.MaxDurationMs)
	}
	if p.Namespace != "" {
		filters = append(filters, `namespace = ?`)
		args = append(args, p.Namespace)
	}
	if p.TenantID != "" {
		filters = append(filters, `tenant = ?`)
		args = append(args, p.TenantID)
	}
	for key, val := range p.Attrs {
		filters = append(filters, `json_extract_string(attributes_json, ?) = ?`)
		args = append(args, "$."+key, val)
	}
	return filters, args
}

// filterClause converts a filter list to a SQL WHERE fragment (without "WHERE").
func filterClause(filters []string) string {
	if len(filters) == 0 {
		return ""
	}
	return "AND " + strings.Join(filters, " AND ")
}

// orderByClause returns the ORDER BY expression for ungrouped queries.
func orderByClause(orderBy string) string {
	switch orderBy {
	case "duration":
		return "duration_ms DESC"
	default:
		return "start_time DESC"
	}
}

// groupOrderByClause returns the ORDER BY expression for grouped queries.
func groupOrderByClause(orderBy string) string {
	switch orderBy {
	case "error_rate":
		return "error_rate DESC"
	case "duration":
		return "p95_ms DESC"
	default:
		return "count DESC"
	}
}

// groupByExpr maps a group_by field name to its SQL expression.
// For attribute fields (http.*) we use json_extract_string on attributes_json.
func groupByExpr(field string) string {
	switch field {
	case "http.method":
		return `json_extract_string(attributes_json, '$.http.method')`
	case "http.status_code":
		return `json_extract_string(attributes_json, '$.http.status_code')`
	default:
		return field
	}
}

// spansUngrouped returns raw span rows.
func (s *Service) spansUngrouped(ctx context.Context, p SpanParams) (*SpansResult, error) {
	filters, args := buildSpanFilters(p)
	whereClause := filterClause(filters)
	orderExpr := orderByClause(p.OrderBy)

	q := fmt.Sprintf(`
SELECT trace_id, span_id, service, operation, kind,
       strftime(start_time, '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS start_time,
       duration_ms, status, attributes_json
FROM spans
WHERE start_time >= now() - INTERVAL %d MINUTE
  %s
ORDER BY %s
LIMIT %d;
`, p.Window, whereClause, orderExpr, p.Limit+1)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		slog.Warn("spans ungrouped query failed", "err", err)
		return &SpansResult{Spans: []SpanRow{}}, fmt.Errorf("spans query: %w", err)
	}
	defer rows.Close()

	var spans []SpanRow
	var scanErrors int
	for rows.Next() {
		var r SpanRow
		var attrsJSON sql.NullString
		if err := rows.Scan(&r.TraceID, &r.SpanID, &r.Service, &r.Operation, &r.Kind,
			&r.StartTime, &r.DurationMs, &r.Status, &attrsJSON); err != nil {
			scanErrors++
			slog.Warn("scan failed", "method", "spansUngrouped", "err", err)
			continue
		}
		if attrsJSON.Valid && attrsJSON.String != "" {
			r.Attributes = parseAttrsJSON(attrsJSON.String)
		}
		spans = append(spans, r)
	}
	if err := rows.Err(); err != nil {
		return &SpansResult{Spans: []SpanRow{}}, fmt.Errorf("spans rows: %w", err)
	}
	if scanErrors > 0 && len(spans) == 0 {
		return nil, fmt.Errorf("spans: all %d rows failed to scan", scanErrors)
	}

	totalMatched := len(spans)
	if len(spans) > p.Limit {
		spans = spans[:p.Limit]
	}
	if spans == nil {
		spans = []SpanRow{}
	}

	return &SpansResult{
		Spans:        spans,
		TotalMatched: totalMatched,
	}, nil
}

// spansGrouped returns aggregated span groups.
func (s *Service) spansGrouped(ctx context.Context, p SpanParams) (*SpansResult, error) {
	filters, args := buildSpanFilters(p)
	whereClause := filterClause(filters)
	orderExpr := groupOrderByClause(p.OrderBy)

	// Build SELECT and GROUP BY expressions
	var selectCols []string
	var groupCols []string
	for _, field := range p.GroupBy {
		expr := groupByExpr(field)
		// Alias attribute expressions to a simple name for scanning
		alias := strings.ReplaceAll(strings.ReplaceAll(field, ".", "_"), "-", "_")
		if expr != field {
			selectCols = append(selectCols, fmt.Sprintf(`%s AS %s`, expr, alias))
		} else {
			selectCols = append(selectCols, fmt.Sprintf(`%s AS %s`, expr, alias))
		}
		groupCols = append(groupCols, expr)
	}

	exemplarCol := "NULL::VARCHAR[]"
	if p.IncludeExemplars {
		exemplarCol = "list_slice(list(DISTINCT trace_id), 1, 3)"
	}

	selectList := strings.Join(selectCols, ", ")
	groupList := strings.Join(groupCols, ", ")

	q := fmt.Sprintf(`
SELECT
  %s,
  count(*) AS count,
  sum(CASE WHEN status IN ('STATUS_CODE_ERROR','ERROR') THEN 1 ELSE 0 END) AS error_count,
  avg(CASE WHEN status IN ('STATUS_CODE_ERROR','ERROR') THEN 1.0 ELSE 0.0 END) AS error_rate,
  approx_quantile(duration_ms, 0.50) AS p50_ms,
  approx_quantile(duration_ms, 0.95) AS p95_ms,
  approx_quantile(duration_ms, 0.99) AS p99_ms,
  %s AS exemplar_trace_ids
FROM spans
WHERE start_time >= now() - INTERVAL %d MINUTE
  %s
GROUP BY %s
ORDER BY %s
LIMIT %d;
`, selectList, exemplarCol, p.Window, whereClause, groupList, orderExpr, p.Limit+1)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		slog.Warn("spans grouped query failed", "err", err)
		return &SpansResult{Groups: []SpanGroup{}}, fmt.Errorf("spans grouped query: %w", err)
	}
	defer rows.Close()

	var groups []SpanGroup
	var scanErrors int
	for rows.Next() {
		g := SpanGroup{
			Key: make(map[string]string, len(p.GroupBy)),
		}

		// Build scan destinations: one per group_by field + 7 agg columns
		keyVals := make([]any, len(p.GroupBy))
		for i := range keyVals {
			var v sql.NullString
			keyVals[i] = &v
		}
		var exemplarJSON sql.NullString
		dest := append(keyVals,
			&g.Count, &g.ErrorCount, &g.ErrorRate,
			&g.P50Ms, &g.P95Ms, &g.P99Ms, &exemplarJSON)

		if err := rows.Scan(dest...); err != nil {
			scanErrors++
			slog.Warn("scan failed", "method", "spansGrouped", "err", err)
			continue
		}

		for i, field := range p.GroupBy {
			if ns, ok := keyVals[i].(*sql.NullString); ok && ns.Valid {
				g.Key[field] = ns.String
			} else {
				g.Key[field] = ""
			}
		}

		if exemplarJSON.Valid && exemplarJSON.String != "" && exemplarJSON.String != "NULL" {
			g.ExemplarTraceIDs = parseStringArray(exemplarJSON.String)
		}
		if g.ExemplarTraceIDs == nil {
			g.ExemplarTraceIDs = []string{}
		}

		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return &SpansResult{Groups: []SpanGroup{}}, fmt.Errorf("spans grouped rows: %w", err)
	}
	if scanErrors > 0 && len(groups) == 0 {
		return nil, fmt.Errorf("spans grouped: all %d rows failed to scan", scanErrors)
	}

	totalGroups := len(groups)
	if len(groups) > p.Limit {
		groups = groups[:p.Limit]
	}
	if groups == nil {
		groups = []SpanGroup{}
	}

	return &SpansResult{
		Groups:      groups,
		TotalGroups: totalGroups,
	}, nil
}

// parseAttrsJSON parses a JSON attributes string into a map.
// Returns nil on parse failure (non-fatal).
func parseAttrsJSON(s string) map[string]string {
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		switch val := v.(type) {
		case string:
			out[k] = val
		case float64:
			out[k] = fmt.Sprintf("%v", val)
		default:
			if v != nil {
				out[k] = fmt.Sprintf("%v", v)
			}
		}
	}
	return out
}

// parseStringArray parses a DuckDB list value (JSON array format) into []string.
func parseStringArray(s string) []string {
	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil
	}
	return arr
}
