package observability

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const recentTraceQuery = `
SELECT trace_id
FROM spans
WHERE start_time >= ? AND start_time < ? AND namespace = ? AND trace_id <> '' AND (? = '' OR service = ?)
GROUP BY trace_id
ORDER BY MAX(CASE WHEN upper(status) IN ('ERROR', 'STATUS_CODE_ERROR') THEN 1 ELSE 0 END) DESC,
         MAX(end_time) - MIN(start_time) DESC
LIMIT 1`

const traceSpansQuery = `
SELECT span_id, COALESCE(parent_span_id, ''), service, operation, kind, start_time,
       duration_ms, COALESCE(status, ''), COALESCE(status_message, '')
FROM spans
WHERE start_time >= ? AND start_time < ? AND namespace = ? AND trace_id = ?
ORDER BY start_time ASC, duration_ms DESC
LIMIT ?`

const traceLogsQuery = `
SELECT time, COALESCE(severity, ''), COALESCE(service, ''), COALESCE(body, ''),
       COALESCE(trace_id, ''), COALESCE(span_id, '')
FROM logs
WHERE time >= ? AND time < ? AND namespace = ? AND trace_id = ?
ORDER BY time ASC
LIMIT ?`

func (s *Service) Trace(ctx context.Context, scope Scope, traceID, service string, limit int) (Result[TraceDetail], error) {
	scope, err := s.normalizeScope(scope)
	if err != nil {
		return Result[TraceDetail]{}, err
	}
	limit, err = normalizeLimit(limit)
	if err != nil {
		return Result[TraceDetail]{}, err
	}
	traceID = strings.TrimSpace(traceID)
	service = strings.TrimSpace(service)
	if traceID == "" {
		rows, queryErr := s.db.QueryContext(ctx, recentTraceQuery, scope.Start, scope.End, scope.Namespace, service, service)
		if queryErr != nil {
			return Result[TraceDetail]{}, fmt.Errorf("query recent trace: %w", queryErr)
		}
		if rows.Next() {
			if err := rows.Scan(&traceID); err != nil {
				rows.Close()
				return Result[TraceDetail]{}, fmt.Errorf("scan recent trace: %w", err)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return Result[TraceDetail]{}, fmt.Errorf("iterate recent trace: %w", err)
		}
		rows.Close()
	}

	data := TraceDetail{TraceID: traceID, Services: []string{}, Spans: []TraceSpan{}, Logs: []LogEntry{}}
	if traceID != "" {
		rows, queryErr := s.db.QueryContext(ctx, traceSpansQuery, scope.Start, scope.End, scope.Namespace, traceID, limit)
		if queryErr != nil {
			return Result[TraceDetail]{}, fmt.Errorf("query trace spans: %w", queryErr)
		}
		serviceSet := map[string]struct{}{}
		var first time.Time
		var last time.Time
		for rows.Next() {
			var span TraceSpan
			if err := rows.Scan(&span.SpanID, &span.ParentSpanID, &span.Service, &span.Operation, &span.Kind, &span.Start, &span.DurationMS, &span.Status, &span.StatusMessage); err != nil {
				rows.Close()
				return Result[TraceDetail]{}, fmt.Errorf("scan trace span: %w", err)
			}
			if first.IsZero() || span.Start.Before(first) {
				first = span.Start
			}
			end := span.Start.Add(time.Duration(span.DurationMS * float64(time.Millisecond)))
			if end.After(last) {
				last = end
			}
			if strings.Contains(strings.ToUpper(span.Status), "ERROR") {
				data.HasError = true
			}
			serviceSet[span.Service] = struct{}{}
			data.Spans = append(data.Spans, span)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return Result[TraceDetail]{}, fmt.Errorf("iterate trace spans: %w", err)
		}
		rows.Close()
		if !first.IsZero() {
			data.DurationMS = last.Sub(first).Seconds() * 1000
		}
		for name := range serviceSet {
			if name != "" {
				data.Services = append(data.Services, name)
			}
		}
		sort.Strings(data.Services)

		rows, queryErr = s.db.QueryContext(ctx, traceLogsQuery, scope.Start, scope.End, scope.Namespace, traceID, limit)
		if queryErr != nil {
			return Result[TraceDetail]{}, fmt.Errorf("query trace logs: %w", queryErr)
		}
		for rows.Next() {
			var entry LogEntry
			if err := rows.Scan(&entry.Time, &entry.Severity, &entry.Service, &entry.Body, &entry.TraceID, &entry.SpanID); err != nil {
				rows.Close()
				return Result[TraceDetail]{}, fmt.Errorf("scan trace log: %w", err)
			}
			entry.Body = redactLogBody(entry.Body)
			data.Logs = append(data.Logs, entry)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return Result[TraceDetail]{}, fmt.Errorf("iterate trace logs: %w", err)
		}
		rows.Close()
	}

	summary := "No traces found in this telemetry window"
	if traceID != "" {
		summary = fmt.Sprintf("Trace %s contains %d spans across %d services", traceID, len(data.Spans), len(data.Services))
	}
	return Result[TraceDetail]{Schema: TraceSchema, Summary: summary, Data: data, Provenance: provenanceFor(scope, "spans + logs")}, nil
}
