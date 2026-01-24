package service

import (
	"context"
	"fmt"
	"strings"
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
		spans, hasMore := s.findSpans(ctx, p)
		out.Spans = spans
		if hasMore {
			out.HasMore = true
		}
	}

	// Search logs
	if p.Type == "logs" || p.Type == "both" {
		logs, hasMore := s.findLogs(ctx, p)
		out.Logs = logs
		if hasMore {
			out.HasMore = true
		}
	}

	return out, nil
}

func (s *Service) findSpans(ctx context.Context, p FindParams) ([]SpanResult, bool) {
	var filters []string

	if p.Query != "" {
		filters = append(filters, fmt.Sprintf(`"name=name" ILIKE '%%%s%%'`, escapeLikePattern(p.Query)))
	}
	if p.Service != "" {
		filters = append(filters, fmt.Sprintf(`"name=service_name" = '%s'`, escapeSQL(p.Service)))
	}
	if p.Operation != "" {
		filters = append(filters, fmt.Sprintf(`"name=name" = '%s'`, escapeSQL(p.Operation)))
	}
	if p.Status == "error" {
		filters = append(filters, `"name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR')`)
	} else if p.Status == "slow" {
		filters = append(filters, `"name=duration_ms" > 1000`)
	}
	// Attribute filters
	for key, val := range p.Attrs {
		filters = append(filters, fmt.Sprintf(
			`json_extract_string(from_utf8("name=attributes_json"), '$.%s') = '%s'`,
			escapeSQL(key), escapeSQL(val)))
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

	rows, err := s.duck.DB.QueryContext(ctx, q)
	if err != nil {
		return []SpanResult{}, false
	}
	defer rows.Close()

	var spans []SpanResult
	for rows.Next() {
		var r SpanResult
		var scopeName, scopeVersion any
		rows.Scan(&r.TraceID, &r.SpanID, &r.Service, &r.Name, &r.Duration, &r.Status, &r.StartTime, &scopeName, &scopeVersion)
		if scopeName != nil {
			r.ScopeName = fmt.Sprintf("%v", scopeName)
		}
		if scopeVersion != nil {
			r.ScopeVersion = fmt.Sprintf("%v", scopeVersion)
		}
		spans = append(spans, r)
	}

	hasMore := len(spans) > p.Limit
	if hasMore {
		spans = spans[:p.Limit]
	}
	return spans, hasMore
}

func (s *Service) findLogs(ctx context.Context, p FindParams) ([]LogResult, bool) {
	var filters []string

	if p.Query != "" {
		filters = append(filters, fmt.Sprintf(`"name=body" ~ '%s'`, escapeSQL(p.Query)))
	}
	if p.Service != "" {
		filters = append(filters, fmt.Sprintf(`"name=service_name" = '%s'`, escapeSQL(p.Service)))
	}
	if len(p.Severity) > 0 {
		quoted := make([]string, len(p.Severity))
		for i, sev := range p.Severity {
			quoted[i] = fmt.Sprintf("'%s'", escapeSQL(sev))
		}
		filters = append(filters, fmt.Sprintf(`"name=severity" IN (%s)`, strings.Join(quoted, ",")))
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

	rows, err := s.duck.DB.QueryContext(ctx, q)
	if err != nil {
		return []LogResult{}, false
	}
	defer rows.Close()

	var logs []LogResult
	for rows.Next() {
		var r LogResult
		var observedTime, traceID, spanID, scopeName, scopeVersion any
		var severityNum any
		rows.Scan(&r.Time, &observedTime, &r.Service, &r.Severity, &severityNum, &r.Body, &traceID, &spanID, &scopeName, &scopeVersion)
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

	hasMore := len(logs) > p.Limit
	if hasMore {
		logs = logs[:p.Limit]
	}
	return logs, hasMore
}
