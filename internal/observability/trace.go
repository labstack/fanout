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

	data := TraceDetail{TraceID: traceID, Services: []string{}, Spans: []TraceSpan{}, Logs: []LogEntry{}}
	if traceID != "" {
		unlock := s.repository.ReadLock()
		storedSpans, readErr := s.repository.Spans.Trace(traceID)
		if readErr != nil {
			unlock()
			return Result[TraceDetail]{}, fmt.Errorf("read trace segments: %w", readErr)
		}
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

		readErr = s.repository.Logs.Scan(startNanos, endNanos, func(row telemetry.Log) bool {
			if row.TraceID != traceID || (scope.Namespace != "" && row.Namespace != scope.Namespace) {
				return true
			}
			data.Logs = append(data.Logs, LogEntry{Time: time.Unix(0, row.EventUnixNanos).UTC(), Severity: row.Severity, Service: row.ServiceName, Body: redactLogBody(row.Body), TraceID: row.TraceID, SpanID: row.SpanID})
			return len(data.Logs) < limit
		})
		unlock()
		if readErr != nil {
			return Result[TraceDetail]{}, fmt.Errorf("read trace logs: %w", readErr)
		}
		sort.Slice(data.Logs, func(i, j int) bool { return data.Logs[i].Time.Before(data.Logs[j].Time) })
	}

	summary := "No traces found in this telemetry window"
	if traceID != "" {
		summary = fmt.Sprintf("Trace %s contains %d spans across %d services", traceID, len(data.Spans), len(data.Services))
	}
	return Result[TraceDetail]{Schema: TraceSchema, Summary: summary, Data: data, Provenance: s.provenanceFor(scope, "fanout_segments")}, nil
}
