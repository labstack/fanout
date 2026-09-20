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

const traceSummaryQuery = `
SELECT
  CAST(count(*) AS BIGINT),
  CAST(count(DISTINCT service) AS BIGINT),
  COALESCE((max(end_unix_nano) - min(start_unix_nano)) / 1000000.0, 0),
  COALESCE(bool_or(upper(status) IN ('ERROR', 'STATUS_CODE_ERROR')), false)
FROM spans
WHERE trace_id = ? AND start_time >= ? AND start_time < ? AND (? = '' OR namespace = ?)`

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

		// Only the page's own service list is derived here. Duration and
		// has_error describe the whole trace and come from the aggregate below.
		serviceSet := make(map[string]struct{})
		for _, span := range data.Spans {
			if span.Service != "" {
				serviceSet[span.Service] = struct{}{}
			}
		}
		for name := range serviceSet {
			data.Services = append(data.Services, name)
		}
		sort.Strings(data.Services)
		// The page above is what limit admitted. Everything the caller reads as
		// a fact about the trace — how many spans it has, how many services it
		// crosses, how long it took, whether it failed — comes from an
		// aggregate over the whole trace instead, so a narrow page cannot
		// silently redefine the trace. The aggregate reads no rows into this
		// process: it is counted in DuckDB and returns one row.
		if err := s.traceTotals(ctx, scope, traceID, &data); err != nil {
			return Result[TraceDetail]{}, err
		}
		data.Truncated = data.SpanCount > len(data.Spans)
		data.Logs, err = s.traceLogsFromParquet(ctx, scope, traceID, limit)
		if err != nil {
			return Result[TraceDetail]{}, err
		}
	}

	summary := "No traces found in this telemetry window"
	if traceID != "" {
		summary = fmt.Sprintf("Trace %s contains %d spans across %d services", traceID, data.SpanCount, data.ServiceCount)
		if data.Truncated {
			summary += fmt.Sprintf("; showing %d", len(data.Spans))
		}
	}
	return Result[TraceDetail]{Schema: TraceSchema, Summary: summary, Data: data, Provenance: s.provenanceFor(scope, dataSource)}, nil
}

// traceTotals fills the fields that describe the trace rather than the page.
// A trace that has aged out of the window reports zeros, which leaves the
// summary saying the trace holds no spans — true for the window asked about.
func (s *Service) traceTotals(ctx context.Context, scope Scope, traceID string, data *TraceDetail) error {
	rows, err := s.db.QueryContext(ctx, traceSummaryQuery, traceID, scope.Start, scope.End, scope.Namespace, scope.Namespace)
	if err != nil {
		return fmt.Errorf("query trace totals: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		var spanCount, serviceCount int64
		if err := rows.Scan(&spanCount, &serviceCount, &data.DurationMS, &data.HasError); err != nil {
			return fmt.Errorf("scan trace totals: %w", err)
		}
		data.SpanCount, data.ServiceCount = int(spanCount), int(serviceCount)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate trace totals: %w", err)
	}
	return nil
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
