package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// FindParams contains search parameters.
type FindParams struct {
	Query     string
	Service   string
	Operation string // filter by operation name
	Type      string // spans, logs, both
	Status    string // error, slow, all
	Window    int
	Severity  []string
	Attrs     map[string]string // attr:key=value filters
	Limit     int
	Namespace string
	TenantID  string
}

// Find searches spans and logs with smart defaults.
func (s *Service) Find(ctx context.Context, p FindParams) (*FindResult, error) {
	if p.Window == 0 {
		p.Window = 15
	}
	if p.Limit == 0 {
		p.Limit = 50
	}
	if p.Type == "" {
		p.Type = "both"
	}

	// Always scope to single partition
	p.Namespace, p.TenantID = s.defaults(p.Namespace, p.TenantID)

	out := &FindResult{
		Spans: []SpanResult{},
		Logs:  []LogResult{},
	}

	// Search spans
	if p.Type == "spans" || p.Type == "both" {
		spans, hasMore, err := s.findSpans(ctx, p)
		if err != nil {
			return out, fmt.Errorf("findSpans: %w", err)
		}
		out.Spans = spans
		if hasMore {
			out.HasMore = true
		}
	}

	// Search logs
	if p.Type == "logs" || p.Type == "both" {
		logs, hasMore, err := s.findLogs(ctx, p)
		if err != nil {
			return out, fmt.Errorf("findLogs: %w", err)
		}
		out.Logs = logs
		if hasMore {
			out.HasMore = true
		}
	}

	return out, nil
}

func (s *Service) findSpans(ctx context.Context, p FindParams) ([]SpanResult, bool, error) {
	var filters []string
	var args []any

	if p.Query != "" {
		filters = append(filters, `"name=name" ILIKE ?`)
		escaped := strings.NewReplacer("%", "\\%", "_", "\\_").Replace(p.Query)
		args = append(args, "%"+escaped+"%")
	}
	if p.Service != "" {
		filters = append(filters, `"name=service_name" = ?`)
		args = append(args, p.Service)
	}
	if p.Operation != "" {
		filters = append(filters, `"name=name" = ?`)
		args = append(args, p.Operation)
	}
	if p.Status == "error" {
		filters = append(filters, `"name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR')`)
	} else if p.Status == "slow" {
		filters = append(filters, `"name=duration_ms" > 1000`)
	}
	// Attribute filters
	for key, val := range p.Attrs {
		filters = append(filters, `json_extract_string(from_utf8("name=attributes_json"), ?) = ?`)
		args = append(args, "$."+key, val)
	}

	filterStr := ""
	if len(filters) > 0 {
		filterStr = "AND " + strings.Join(filters, " AND ")
	}

	// Glob scoped to single partition, union_by_name for schema evolution
	q := fmt.Sprintf(`
SELECT "name=trace_id" as trace_id,
       "name=span_id" as span_id,
       "name=service_name" as service,
       "name=name" as operation,
       "name=duration_ms" as duration_ms,
       "name=status_code" as status,
       strftime(epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS start_time,
       "name=scope_name" as scope_name,
       "name=scope_version" as scope_version
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  %s
ORDER BY "name=start_unix_nano" DESC
LIMIT %d;
`, s.duck.SpansGlob(p.TenantID, p.Namespace, p.Window), p.Window, filterStr, p.Limit+1)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		slog.Warn("findSpans query failed", "err", err)
		return []SpanResult{}, false, fmt.Errorf("findSpans query: %w", err)
	}
	defer rows.Close()

	var spans []SpanResult
	for rows.Next() {
		var r SpanResult
		var scopeName, scopeVersion any
		if err := rows.Scan(&r.TraceID, &r.SpanID, &r.Service, &r.Name, &r.Duration, &r.Status, &r.StartTime, &scopeName, &scopeVersion); err != nil {
			slog.Warn("scan failed", "method", "findSpans", "err", err)
			continue
		}
		if scopeName != nil {
			r.ScopeName = fmt.Sprintf("%v", scopeName)
		}
		if scopeVersion != nil {
			r.ScopeVersion = fmt.Sprintf("%v", scopeVersion)
		}
		spans = append(spans, r)
	}
	if err := rows.Err(); err != nil {
		return spans, false, fmt.Errorf("findSpans rows iteration: %w", err)
	}

	hasMore := len(spans) > p.Limit
	if hasMore {
		spans = spans[:p.Limit]
	}
	return spans, hasMore, nil
}

func (s *Service) findLogs(ctx context.Context, p FindParams) ([]LogResult, bool, error) {
	var filters []string
	var args []any

	if p.Query != "" {
		filters = append(filters, `"name=body" ILIKE ?`)
		escaped := strings.NewReplacer("%", "\\%", "_", "\\_").Replace(p.Query)
		args = append(args, "%"+escaped+"%")
	}
	if p.Service != "" {
		filters = append(filters, `"name=service_name" = ?`)
		args = append(args, p.Service)
	}
	if len(p.Severity) > 0 {
		placeholders := make([]string, len(p.Severity))
		for i, sev := range p.Severity {
			placeholders[i] = "?"
			args = append(args, sev)
		}
		filters = append(filters, fmt.Sprintf(`"name=severity" IN (%s)`, strings.Join(placeholders, ",")))
	}

	filterStr := ""
	if len(filters) > 0 {
		filterStr = "AND " + strings.Join(filters, " AND ")
	}

	// Glob scoped to single partition, union_by_name for schema evolution
	q := fmt.Sprintf(`
SELECT strftime(epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS ts,
       CASE WHEN "name=observed_time_unix_nano" > 0
            THEN strftime(epoch_ms(CAST("name=observed_time_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ')
            ELSE NULL END AS observed_ts,
       "name=service_name" as service,
       "name=severity" as severity,
       "name=severity_number" as severity_number,
       "name=body" as body,
       "name=trace_id" as trace_id,
       "name=span_id" as span_id,
       "name=scope_name" as scope_name,
       "name=scope_version" as scope_version
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  %s
ORDER BY "name=time_unix_nano" DESC
LIMIT %d;
`, s.duck.LogsGlob(p.TenantID, p.Namespace, p.Window), p.Window, filterStr, p.Limit+1)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		slog.Warn("findLogs query failed", "err", err)
		return []LogResult{}, false, fmt.Errorf("findLogs query: %w", err)
	}
	defer rows.Close()

	var logs []LogResult
	for rows.Next() {
		var r LogResult
		var observedTime, traceID, spanID, scopeName, scopeVersion any
		var severityNum any
		if err := rows.Scan(&r.Time, &observedTime, &r.Service, &r.Severity, &severityNum, &r.Body, &traceID, &spanID, &scopeName, &scopeVersion); err != nil {
			slog.Warn("scan failed", "method", "findLogs", "err", err)
			continue
		}
		if observedTime != nil {
			r.ObservedTime = fmt.Sprintf("%v", observedTime)
		}
		if severityNum != nil {
			if num, ok := severityNum.(int64); ok {
				r.SeverityNumber = int32(num)
			} else if num, ok := severityNum.(int32); ok {
				r.SeverityNumber = num
			}
		}
		if traceID != nil {
			r.TraceID = fmt.Sprintf("%v", traceID)
		}
		if spanID != nil {
			r.SpanID = fmt.Sprintf("%v", spanID)
		}
		if scopeName != nil {
			r.ScopeName = fmt.Sprintf("%v", scopeName)
		}
		if scopeVersion != nil {
			r.ScopeVersion = fmt.Sprintf("%v", scopeVersion)
		}
		logs = append(logs, r)
	}
	if err := rows.Err(); err != nil {
		return logs, false, fmt.Errorf("findLogs rows iteration: %w", err)
	}

	hasMore := len(logs) > p.Limit
	if hasMore {
		logs = logs[:p.Limit]
	}
	return logs, hasMore, nil
}

// TailParams defines filters for live log tailing.
type TailParams struct {
	Service   string
	Pattern   string
	Severity  string
	Namespace string
	TenantID  string
	Since     time.Time // only return logs after this timestamp
}

// TailLogs returns log entries newer than Since, ordered ascending (oldest first).
// Limited to 100 entries per call to prevent flooding.
func (s *Service) TailLogs(ctx context.Context, p TailParams) ([]LogResult, error) {
	p.Namespace, p.TenantID = s.defaults(p.Namespace, p.TenantID)

	sinceNano := p.Since.UnixNano()

	var filters []string
	var args []any

	// Inline the timestamp comparison to avoid parameter binding issues with INT64
	filters = append(filters, fmt.Sprintf(`"name=time_unix_nano" > %d`, sinceNano))

	if p.Service != "" {
		filters = append(filters, `"name=service_name" = ?`)
		args = append(args, p.Service)
	}
	if p.Pattern != "" {
		filters = append(filters, `"name=body" ILIKE ?`)
		escaped := strings.NewReplacer("%", "\\%", "_", "\\_").Replace(p.Pattern)
		args = append(args, "%"+escaped+"%")
	}
	if p.Severity != "" {
		filters = append(filters, `"name=severity" = ?`)
		args = append(args, p.Severity)
	}

	filterStr := ""
	if len(filters) > 0 {
		filterStr = "AND " + strings.Join(filters, " AND ")
	}

	// Use a 10-minute window for glob scoping — tail sessions look at recent data
	q := fmt.Sprintf(`
SELECT strftime(epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS ts,
       "name=service_name" as service,
       "name=severity" as severity,
       "name=body" as body,
       "name=trace_id" as trace_id,
       "name=time_unix_nano" as time_nano
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL 10 MINUTE
  %s
ORDER BY "name=time_unix_nano" ASC
LIMIT 100;
`, s.duck.LogsGlob(p.TenantID, p.Namespace, 10), filterStr)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []LogResult
	for rows.Next() {
		var r LogResult
		var traceID any
		var timeNano int64
		if err := rows.Scan(&r.Time, &r.Service, &r.Severity, &r.Body, &traceID, &timeNano); err != nil {
			slog.Warn("scan failed", "method", "TailLogs", "err", err)
			continue
		}
		if traceID != nil {
			r.TraceID = fmt.Sprintf("%v", traceID)
		}
		logs = append(logs, r)
	}
	if err := rows.Err(); err != nil {
		return logs, fmt.Errorf("TailLogs rows iteration: %w", err)
	}

	return logs, nil
}
