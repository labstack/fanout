package observability

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

const recentTraceQuery = `
SELECT trace_id
FROM spans
WHERE start_time >= ? AND start_time < ? AND (? = '' OR namespace = ?) AND trace_id <> '' AND (? = '' OR service = ?)
GROUP BY trace_id
ORDER BY MAX(CASE WHEN upper(status) IN ('ERROR', 'STATUS_CODE_ERROR') THEN 1 ELSE 0 END) DESC,
         MAX(end_time) - MIN(start_time) DESC
LIMIT 1`

const traceLogsQuery = `
SELECT time, severity, coalesce(service, ''), body, coalesce(trace_id, ''), coalesce(span_id, '')
FROM logs
WHERE trace_id = ? AND time >= ? AND time < ? AND (? = '' OR namespace = ?)
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
	traceID, service = strings.TrimSpace(traceID), strings.TrimSpace(service)
	if traceID == "" {
		rows, queryErr := s.db.QueryContext(ctx, recentTraceQuery, scope.Start, scope.End, scope.Namespace, scope.Namespace, service, service)
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

	dataSource := "parquet_index"
	data := TraceDetail{TraceID: traceID, Services: []string{}, Spans: []TraceSpan{}, Logs: []LogEntry{}}
	if traceID != "" {
		storedSpans, readErr := s.repository.Trace(ctx, telemetry.TraceQuery{
			TraceID: traceID, Namespace: scope.Namespace,
			StartNanos: scope.Start.UnixNano(), EndNanos: scope.End.UnixNano(), Limit: limit,
		})
		if readErr != nil {
			return Result[TraceDetail]{}, fmt.Errorf("read indexed Parquet trace: %w", readErr)
		}
		for _, row := range storedSpans {
			data.Spans = append(data.Spans, TraceSpan{SpanID: row.SpanID, ParentSpanID: row.ParentSpanID, Service: row.ServiceName, Operation: row.Name, Kind: row.Kind, Start: time.Unix(0, row.StartUnixNanos).UTC(), DurationMS: row.DurationMS, Status: row.StatusCode, StatusMessage: row.StatusMsg})
		}

		serviceSet := make(map[string]struct{})
		var first, last time.Time
		for _, span := range data.Spans {
			if first.IsZero() || span.Start.Before(first) {
				first = span.Start
			}
			if end := span.Start.Add(time.Duration(span.DurationMS * float64(time.Millisecond))); end.After(last) {
				last = end
			}
			if strings.Contains(strings.ToUpper(span.Status), "ERROR") {
				data.HasError = true
			}
			if span.Service != "" {
				serviceSet[span.Service] = struct{}{}
			}
		}
		if !first.IsZero() {
			data.DurationMS = last.Sub(first).Seconds() * 1000
		}
		for name := range serviceSet {
			data.Services = append(data.Services, name)
		}
		sort.Strings(data.Services)
		data.Logs, err = s.traceLogsFromParquet(ctx, scope, traceID, limit)
		if err != nil {
			return Result[TraceDetail]{}, err
		}
	}

	summary := "No traces found in this telemetry window"
	if traceID != "" {
		summary = fmt.Sprintf("Trace %s contains %d spans across %d services", traceID, len(data.Spans), len(data.Services))
	}
	return Result[TraceDetail]{Schema: TraceSchema, Summary: summary, Data: data, Provenance: s.provenanceFor(scope, dataSource)}, nil
}

func (s *Service) traceLogsFromParquet(ctx context.Context, scope Scope, traceID string, limit int) ([]LogEntry, error) {
	rows, err := s.db.QueryContext(ctx, traceLogsQuery, traceID, scope.Start, scope.End, scope.Namespace, scope.Namespace, limit)
	if err != nil {
		return nil, fmt.Errorf("query trace parquet logs: %w", err)
	}
	logs := make([]LogEntry, 0, limit)
	for rows.Next() {
		var entry LogEntry
		if err := rows.Scan(&entry.Time, &entry.Severity, &entry.Service, &entry.Body, &entry.TraceID, &entry.SpanID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan trace parquet log: %w", err)
		}
		entry.Body = redactLogBody(entry.Body)
		logs = append(logs, entry)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate trace parquet logs: %w", err)
	}
	rows.Close()
	return logs, nil
}
