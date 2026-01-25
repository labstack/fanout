package service

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// UnifiedParams contains parameters for unified timeline query.
type UnifiedParams struct {
	Service   string
	Window    int
	Limit     int
	Namespace string
	TenantID  string
}

// UnifiedEvent represents an event in the unified timeline.
type UnifiedEvent struct {
	Time     string  `json:"time"`
	Type     string  `json:"type"` // span, log, metric
	Service  string  `json:"service"`
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Status   string  `json:"status,omitempty"`
	TraceID  string  `json:"trace_id,omitempty"`
	SpanID   string  `json:"span_id,omitempty"`
	Severity string  `json:"severity,omitempty"`
	Duration float64 `json:"duration_ms,omitempty"`
}

// UnifiedResult contains the unified timeline data.
type UnifiedResult struct {
	Events      []UnifiedEvent `json:"events"`
	SpanCount   int            `json:"span_count"`
	LogCount    int            `json:"log_count"`
	MetricCount int            `json:"metric_count"`
	HasMore     bool           `json:"has_more"`
}

// Unified returns a unified timeline of traces, logs, and metrics.
func (s *Service) Unified(ctx context.Context, p UnifiedParams) (*UnifiedResult, error) {
	if p.Window == 0 {
		p.Window = 15
	}
	if p.Limit == 0 {
		p.Limit = 100
	}

	p.Namespace, p.TenantID = s.defaults(p.Namespace, p.TenantID)

	out := &UnifiedResult{
		Events: []UnifiedEvent{},
	}

	// Run queries in parallel
	var wg sync.WaitGroup
	var spans, logs, metrics []UnifiedEvent

	wg.Add(3)
	go func() {
		defer wg.Done()
		spans = s.queryUnifiedSpans(ctx, p)
	}()
	go func() {
		defer wg.Done()
		logs = s.queryUnifiedLogs(ctx, p)
	}()
	go func() {
		defer wg.Done()
		metrics = s.queryUnifiedMetrics(ctx, p)
	}()
	wg.Wait()

	out.SpanCount = len(spans)
	out.LogCount = len(logs)
	out.MetricCount = len(metrics)

	// Merge all events
	all := make([]UnifiedEvent, 0, len(spans)+len(logs)+len(metrics))
	all = append(all, spans...)
	all = append(all, logs...)
	all = append(all, metrics...)

	// Sort by time descending
	sort.Slice(all, func(i, j int) bool {
		return all[i].Time > all[j].Time
	})

	// Apply limit
	if len(all) > p.Limit {
		out.HasMore = true
		all = all[:p.Limit]
	}

	out.Events = all
	return out, nil
}

func (s *Service) queryUnifiedSpans(ctx context.Context, p UnifiedParams) []UnifiedEvent {
	var events []UnifiedEvent

	svcFilter := ""
	if p.Service != "" {
		svcFilter = fmt.Sprintf(`AND "name=service_name" = '%s'`, escapeSQL(p.Service))
	}

	q := fmt.Sprintf(`
SELECT strftime(epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS ts,
       "name=service_name" as service,
       "name=name" as operation,
       "name=duration_ms" as duration,
       "name=status_code" as status,
       "name=trace_id" as trace_id,
       "name=span_id" as span_id
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  %s
ORDER BY "name=start_unix_nano" DESC
LIMIT %d;
`, s.duck.SpansGlob(p.TenantID, p.Namespace, p.Window), p.Window, svcFilter, p.Limit)

	rows, err := s.duck.DB.QueryContext(ctx, q)
	if err != nil {
		return events
	}
	defer rows.Close()

	for rows.Next() {
		var e UnifiedEvent
		e.Type = "span"
		var status string
		rows.Scan(&e.Time, &e.Service, &e.Name, &e.Duration, &status, &e.TraceID, &e.SpanID)
		if status == "STATUS_CODE_ERROR" || status == "ERROR" {
			e.Status = "error"
		} else {
			e.Status = "ok"
		}
		e.Value = fmt.Sprintf("%.1fms", e.Duration)
		events = append(events, e)
	}

	return events
}

func (s *Service) queryUnifiedLogs(ctx context.Context, p UnifiedParams) []UnifiedEvent {
	var events []UnifiedEvent

	svcFilter := ""
	if p.Service != "" {
		svcFilter = fmt.Sprintf(`AND "name=service_name" = '%s'`, escapeSQL(p.Service))
	}

	q := fmt.Sprintf(`
SELECT strftime(epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS ts,
       "name=service_name" as service,
       "name=severity" as severity,
       "name=body" as body,
       "name=trace_id" as trace_id,
       "name=span_id" as span_id
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  %s
ORDER BY "name=time_unix_nano" DESC
LIMIT %d;
`, s.duck.LogsGlob(p.TenantID, p.Namespace, p.Window), p.Window, svcFilter, p.Limit)

	rows, err := s.duck.DB.QueryContext(ctx, q)
	if err != nil {
		return events
	}
	defer rows.Close()

	for rows.Next() {
		var e UnifiedEvent
		var traceID, spanID any
		e.Type = "log"
		rows.Scan(&e.Time, &e.Service, &e.Severity, &e.Value, &traceID, &spanID)
		e.Name = e.Severity
		if traceID != nil {
			e.TraceID = fmt.Sprintf("%v", traceID)
		}
		if spanID != nil {
			e.SpanID = fmt.Sprintf("%v", spanID)
		}
		// Truncate body for display
		if len(e.Value) > 80 {
			e.Value = e.Value[:77] + "..."
		}
		events = append(events, e)
	}

	return events
}

func (s *Service) queryUnifiedMetrics(ctx context.Context, p UnifiedParams) []UnifiedEvent {
	var events []UnifiedEvent

	svcFilter := ""
	if p.Service != "" {
		svcFilter = fmt.Sprintf(`AND "name=service_name" = '%s'`, escapeSQL(p.Service))
	}

	q := fmt.Sprintf(`
SELECT strftime(epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS ts,
       "name=service_name" as service,
       "name=name" as metric_name,
       "name=value" as value,
       "name=unit" as unit
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  %s
ORDER BY "name=time_unix_nano" DESC
LIMIT %d;
`, s.duck.MetricsGlob(p.TenantID, p.Namespace, p.Window), p.Window, svcFilter, p.Limit)

	rows, err := s.duck.DB.QueryContext(ctx, q)
	if err != nil {
		return events
	}
	defer rows.Close()

	for rows.Next() {
		var e UnifiedEvent
		var value float64
		var unit any
		e.Type = "metric"
		rows.Scan(&e.Time, &e.Service, &e.Name, &value, &unit)
		unitStr := ""
		if unit != nil {
			unitStr = fmt.Sprintf("%v", unit)
		}
		e.Value = fmt.Sprintf("%.2f%s", value, unitStr)
		events = append(events, e)
	}

	return events
}
