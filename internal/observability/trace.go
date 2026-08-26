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

const traceSpansQuery = `
SELECT span_id, coalesce(parent_span_id, ''), service, operation, kind, start_time,
       duration_ms, status, coalesce(status_message, '')
FROM spans
WHERE trace_id = ? AND start_time >= ? AND start_time < ? AND (? = '' OR namespace = ?)
ORDER BY start_time ASC, duration_ms DESC
LIMIT ?`

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

	dataSource := "fanout_segments"
	data := TraceDetail{TraceID: traceID, Services: []string{}, Spans: []TraceSpan{}, Logs: []LogEntry{}}
	if traceID != "" {
		unlock := s.repository.ReadLock()
		storedSpans, readErr := s.repository.Spans.Trace(traceID)
		if readErr != nil {
			unlock()
			return Result[TraceDetail]{}, fmt.Errorf("read trace segments: %w", readErr)
		}
		unlock()
		startNanos, endNanos := scope.Start.UnixNano(), scope.End.UnixNano()
		for _, row := range storedSpans {
			if row.StartUnixNanos < startNanos || row.StartUnixNanos >= endNanos ||
				(scope.Namespace != "" && row.Namespace != scope.Namespace) {
				continue
			}
			data.Spans = append(data.Spans, TraceSpan{SpanID: row.SpanID, ParentSpanID: row.ParentSpanID, Service: row.ServiceName, Operation: row.Name, Kind: row.Kind, Start: time.Unix(0, row.StartUnixNanos).UTC(), DurationMS: row.DurationMS, Status: row.StatusCode, StatusMessage: row.StatusMsg})
		}
		sort.Slice(data.Spans, func(i, j int) bool {
			if data.Spans[i].Start.Equal(data.Spans[j].Start) {
				return data.Spans[i].DurationMS > data.Spans[j].DurationMS
			}
			return data.Spans[i].Start.Before(data.Spans[j].Start)
		})
		if len(data.Spans) > limit {
			data.Spans = data.Spans[:limit]
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

		traceLogs := earliestLogHeap{}
		readErr = s.repository.Logs.Scan(startNanos, endNanos, func(row telemetry.Log) bool {
			select {
			case <-ctx.Done():
				return false
			default:
			}
			if row.TraceID != traceID || (scope.Namespace != "" && row.Namespace != scope.Namespace) {
				return true
			}
			retainEarliest(&traceLogs, LogEntry{Time: time.Unix(0, row.EventUnixNanos).UTC(), Severity: row.Severity, Service: row.ServiceName, Body: redactLogBody(row.Body), TraceID: row.TraceID, SpanID: row.SpanID}, limit)
			return true
		})
		if readErr != nil {
			return Result[TraceDetail]{}, fmt.Errorf("read trace logs: %w", readErr)
		}
		if err := ctx.Err(); err != nil {
			return Result[TraceDetail]{}, err
		}
		data.Logs = append(data.Logs, traceLogs...)
		sort.Slice(data.Logs, func(i, j int) bool { return data.Logs[i].Time.Before(data.Logs[j].Time) })
	}
	if traceID != "" && len(data.Spans) == 0 {
		data, err = s.traceFromParquet(ctx, scope, traceID, limit)
		if err != nil {
			return Result[TraceDetail]{}, err
		}
		dataSource = "parquet"
	}

	summary := "No traces found in this telemetry window"
	if traceID != "" {
		summary = fmt.Sprintf("Trace %s contains %d spans across %d services", traceID, len(data.Spans), len(data.Services))
	}
	return Result[TraceDetail]{Schema: TraceSchema, Summary: summary, Data: data, Provenance: s.provenanceFor(scope, dataSource)}, nil
}

func (s *Service) traceFromParquet(ctx context.Context, scope Scope, traceID string, limit int) (TraceDetail, error) {
	data := TraceDetail{TraceID: traceID, Services: []string{}, Spans: []TraceSpan{}, Logs: []LogEntry{}}
	rows, err := s.db.QueryContext(ctx, traceSpansQuery, traceID, scope.Start, scope.End, scope.Namespace, scope.Namespace, limit)
	if err != nil {
		return TraceDetail{}, fmt.Errorf("query trace parquet spans: %w", err)
	}
	for rows.Next() {
		var span TraceSpan
		if err := rows.Scan(&span.SpanID, &span.ParentSpanID, &span.Service, &span.Operation, &span.Kind, &span.Start, &span.DurationMS, &span.Status, &span.StatusMessage); err != nil {
			rows.Close()
			return TraceDetail{}, fmt.Errorf("scan trace parquet span: %w", err)
		}
		data.Spans = append(data.Spans, span)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return TraceDetail{}, fmt.Errorf("iterate trace parquet spans: %w", err)
	}
	rows.Close()

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
	for service := range serviceSet {
		data.Services = append(data.Services, service)
	}
	sort.Strings(data.Services)

	rows, err = s.db.QueryContext(ctx, traceLogsQuery, traceID, scope.Start, scope.End, scope.Namespace, scope.Namespace, limit)
	if err != nil {
		return TraceDetail{}, fmt.Errorf("query trace parquet logs: %w", err)
	}
	for rows.Next() {
		var entry LogEntry
		if err := rows.Scan(&entry.Time, &entry.Severity, &entry.Service, &entry.Body, &entry.TraceID, &entry.SpanID); err != nil {
			rows.Close()
			return TraceDetail{}, fmt.Errorf("scan trace parquet log: %w", err)
		}
		entry.Body = redactLogBody(entry.Body)
		data.Logs = append(data.Logs, entry)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return TraceDetail{}, fmt.Errorf("iterate trace parquet logs: %w", err)
	}
	rows.Close()
	return data, nil
}
