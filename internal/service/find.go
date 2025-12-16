package service

import (
	"context"
	"fmt"
	"strings"
)

// FindParams contains search parameters.
type FindParams struct {
	Query    string
	Service  string
	Type     string // spans, logs, both
	Status   string // error, slow, all
	Window   int
	Severity []string
	Limit    int
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
		filters = append(filters, fmt.Sprintf(`"name=name" ILIKE '%%%s%%'`, escapeLike(p.Query)))
	}
	if p.Service != "" {
		filters = append(filters, fmt.Sprintf(`"name=service_name" = '%s'`, escapeLike(p.Service)))
	}
	if p.Status == "error" {
		filters = append(filters, `"name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR')`)
	} else if p.Status == "slow" {
		filters = append(filters, `"name=duration_ms" > 1000`)
	}

	filterStr := ""
	if len(filters) > 0 {
		filterStr = "AND " + strings.Join(filters, " AND ")
	}

	q := fmt.Sprintf(`
SELECT "name=trace_id" as trace_id,
       "name=span_id" as span_id,
       "name=service_name" as service,
       "name=name" as operation,
       "name=duration_ms" as duration_ms,
       "name=status_code" as status,
       strftime(epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS start_time
FROM read_parquet(%s)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  %s
ORDER BY "name=start_unix_nano" DESC
LIMIT %d;
`, s.duck.SpansGlob(p.Window), p.Window, filterStr, p.Limit+1)

	rows, err := s.duck.DB.QueryContext(ctx, q)
	if err != nil {
		return []SpanResult{}, false
	}
	defer rows.Close()

	var spans []SpanResult
	for rows.Next() {
		var r SpanResult
		rows.Scan(&r.TraceID, &r.SpanID, &r.Service, &r.Name, &r.Duration, &r.Status, &r.StartTime)
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
		filters = append(filters, fmt.Sprintf(`"name=body" ~ '%s'`, escapeLike(p.Query)))
	}
	if p.Service != "" {
		filters = append(filters, fmt.Sprintf(`"name=service_name" = '%s'`, escapeLike(p.Service)))
	}
	if len(p.Severity) > 0 {
		quoted := make([]string, len(p.Severity))
		for i, sev := range p.Severity {
			quoted[i] = fmt.Sprintf("'%s'", escapeLike(sev))
		}
		filters = append(filters, fmt.Sprintf(`"name=severity" IN (%s)`, strings.Join(quoted, ",")))
	}

	filterStr := ""
	if len(filters) > 0 {
		filterStr = "AND " + strings.Join(filters, " AND ")
	}

	q := fmt.Sprintf(`
SELECT strftime(epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS ts,
       "name=service_name" as service,
       "name=severity" as severity,
       "name=body" as body,
       "name=trace_id" as trace_id
FROM read_parquet(%s)
WHERE epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  %s
ORDER BY "name=time_unix_nano" DESC
LIMIT %d;
`, s.duck.LogsGlob(p.Window), p.Window, filterStr, p.Limit+1)

	rows, err := s.duck.DB.QueryContext(ctx, q)
	if err != nil {
		return []LogResult{}, false
	}
	defer rows.Close()

	var logs []LogResult
	for rows.Next() {
		var r LogResult
		var traceID any
		rows.Scan(&r.Time, &r.Service, &r.Severity, &r.Body, &traceID)
		if traceID != nil {
			r.TraceID = fmt.Sprintf("%v", traceID)
		}
		logs = append(logs, r)
	}

	hasMore := len(logs) > p.Limit
	if hasMore {
		logs = logs[:p.Limit]
	}
	return logs, hasMore
}
