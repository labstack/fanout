package service

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
)

// logGroupByColumns maps MCP-facing group-by names to SQL column names.
// Single source of truth — validLogGroupByFields is derived from it.
var logGroupByColumns = map[string]string{
	"service":  "service",
	"severity": "severity",
	"template": "body_template",
}

// validLogGroupByFields is the allowlist for log group_by values.
var validLogGroupByFields = func() map[string]bool {
	m := make(map[string]bool, len(logGroupByColumns))
	for k := range logGroupByColumns {
		m[k] = true
	}
	return m
}()

// mapGroupByCols converts MCP group-by field names to SQL column names.
func mapGroupByCols(fields []string) []string {
	cols := make([]string, len(fields))
	for i, f := range fields {
		cols[i] = logGroupByColumns[f]
	}
	return cols
}

// Logs searches, filters, or aggregates log entries.
// When p.GroupBy is non-empty, returns aggregated LogGroup results.
// Otherwise returns raw LogRow results.
func (s *Service) Logs(ctx context.Context, p LogParams) (*LogsResult, error) {
	if p.Window == 0 {
		p.Window = 15
	}
	if p.Limit == 0 {
		p.Limit = 100
	}

	p.Namespace, p.TenantID = s.defaults(p.Namespace, p.TenantID)

	// Validate group_by fields
	for _, field := range p.GroupBy {
		if !validLogGroupByFields[field] {
			return nil, fmt.Errorf("invalid group_by field %q: allowed fields are service, severity, template", field)
		}
	}

	if len(p.GroupBy) > 0 {
		return s.logsGrouped(ctx, p)
	}
	return s.logsUngrouped(ctx, p)
}

// buildLogFilters builds the WHERE clause filters and argument list for log queries.
func buildLogFilters(p LogParams) (filters []string, args []any) {
	if p.Query != "" {
		escaped := strings.NewReplacer("%", "\\%", "_", "\\_").Replace(p.Query)
		filters = append(filters, `body ILIKE ?`)
		args = append(args, "%"+escaped+"%")
	}
	if len(p.Severity) > 0 {
		placeholders := make([]string, len(p.Severity))
		for i, s := range p.Severity {
			placeholders[i] = "?"
			args = append(args, strings.ToUpper(s))
		}
		filters = append(filters, `UPPER(severity) IN (`+strings.Join(placeholders, ", ")+`)`)
	}
	if p.TraceID != "" {
		filters = append(filters, `trace_id = ?`)
		args = append(args, p.TraceID)
	}
	if p.Service != "" {
		filters = append(filters, `service = ?`)
		args = append(args, p.Service)
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

// logOrderByClause returns the ORDER BY expression for ungrouped log queries.
func logOrderByClause(orderBy string) string {
	switch orderBy {
	case "severity":
		return "severity ASC, time DESC"
	default:
		return "time DESC"
	}
}

// logGroupOrderByClause returns the ORDER BY expression for grouped log queries.
func logGroupOrderByClause(orderBy string) string {
	switch orderBy {
	case "severity":
		return "severity ASC"
	default:
		return "count DESC"
	}
}

// logsUngrouped returns raw log rows.
func (s *Service) logsUngrouped(ctx context.Context, p LogParams) (*LogsResult, error) {
	filters, args := buildLogFilters(p)
	whereClause := filterClause(filters)
	orderExpr := logOrderByClause(p.OrderBy)

	q := fmt.Sprintf(`
SELECT strftime(time, '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS time,
       service, severity, body, trace_id, span_id, attributes_json
FROM logs
WHERE time >= now() - INTERVAL %d MINUTE
  %s
ORDER BY %s
LIMIT %d;
`, p.Window, whereClause, orderExpr, p.Limit+1)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		slog.Warn("logs ungrouped query failed", "err", err)
		return &LogsResult{Logs: []LogRow{}}, fmt.Errorf("logs query: %w", err)
	}
	defer rows.Close()

	var logs []LogRow
	var scanErrors int
	for rows.Next() {
		var r LogRow
		var attrsJSON sql.NullString
		var traceID, spanID sql.NullString
		if err := rows.Scan(&r.Time, &r.Service, &r.Severity, &r.Body,
			&traceID, &spanID, &attrsJSON); err != nil {
			scanErrors++
			slog.Warn("scan failed", "method", "logsUngrouped", "err", err)
			continue
		}
		if traceID.Valid {
			r.TraceID = traceID.String
		}
		if spanID.Valid {
			r.SpanID = spanID.String
		}
		if attrsJSON.Valid && attrsJSON.String != "" {
			r.Attributes = parseAttrsJSON(attrsJSON.String)
		}
		logs = append(logs, r)
	}
	if err := rows.Err(); err != nil {
		return &LogsResult{Logs: []LogRow{}}, fmt.Errorf("logs rows: %w", err)
	}
	if scanErrors > 0 && len(logs) == 0 {
		return nil, fmt.Errorf("logs: all %d rows failed to scan", scanErrors)
	}

	totalMatched := len(logs)
	if len(logs) > p.Limit {
		logs = logs[:p.Limit]
	}
	if logs == nil {
		logs = []LogRow{}
	}

	return &LogsResult{
		Logs:         logs,
		TotalMatched: totalMatched,
	}, nil
}

// logsGrouped returns aggregated log groups.
func (s *Service) logsGrouped(ctx context.Context, p LogParams) (*LogsResult, error) {
	filters, args := buildLogFilters(p)
	whereClause := filterClause(filters)
	orderExpr := logGroupOrderByClause(p.OrderBy)

	sqlCols := mapGroupByCols(p.GroupBy)
	groupCols := strings.Join(sqlCols, ", ")
	selectCols := groupCols

	q := fmt.Sprintf(`
SELECT
  %s,
  count(*) AS count,
  to_json(list_slice(list(body), 1, 3))::VARCHAR AS sample_bodies,
  to_json(list_slice(list(trace_id) FILTER (WHERE trace_id IS NOT NULL AND trace_id != ''), 1, 3))::VARCHAR AS sample_trace_ids
FROM logs
WHERE time >= now() - INTERVAL %d MINUTE
  %s
GROUP BY %s
ORDER BY %s
LIMIT %d;
`, selectCols, p.Window, whereClause, groupCols, orderExpr, p.Limit+1)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		slog.Warn("logs grouped query failed", "err", err)
		return &LogsResult{Groups: []LogGroup{}}, fmt.Errorf("logs grouped query: %w", err)
	}
	defer rows.Close()

	var groups []LogGroup
	var scanErrors int
	for rows.Next() {
		g := LogGroup{
			Key: make(map[string]string, len(p.GroupBy)),
		}

		// Build scan destinations: one per group_by field + 3 agg columns
		keyVals := make([]any, len(p.GroupBy))
		for i := range keyVals {
			var v sql.NullString
			keyVals[i] = &v
		}
		var bodiesJSON, traceIDsJSON sql.NullString
		dest := append(keyVals, &g.Count, &bodiesJSON, &traceIDsJSON)

		if err := rows.Scan(dest...); err != nil {
			scanErrors++
			slog.Warn("scan failed", "method", "logsGrouped", "err", err)
			continue
		}

		for i, field := range p.GroupBy {
			if ns, ok := keyVals[i].(*sql.NullString); ok && ns.Valid {
				g.Key[field] = ns.String
			} else {
				g.Key[field] = ""
			}
		}

		if bodiesJSON.Valid && bodiesJSON.String != "" && bodiesJSON.String != "NULL" {
			g.SampleBodies = parseStringArray(bodiesJSON.String)
		}
		if g.SampleBodies == nil {
			g.SampleBodies = []string{}
		}

		if traceIDsJSON.Valid && traceIDsJSON.String != "" && traceIDsJSON.String != "NULL" {
			g.SampleTraceIDs = parseStringArray(traceIDsJSON.String)
		}
		if g.SampleTraceIDs == nil {
			g.SampleTraceIDs = []string{}
		}

		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return &LogsResult{Groups: []LogGroup{}}, fmt.Errorf("logs grouped rows: %w", err)
	}
	if scanErrors > 0 && len(groups) == 0 {
		return nil, fmt.Errorf("logs grouped: all %d rows failed to scan", scanErrors)
	}

	totalGroups := len(groups)
	if len(groups) > p.Limit {
		groups = groups[:p.Limit]
	}
	if groups == nil {
		groups = []LogGroup{}
	}

	return &LogsResult{
		Groups:      groups,
		TotalGroups: totalGroups,
	}, nil
}
