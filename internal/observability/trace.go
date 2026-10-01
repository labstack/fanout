package observability

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
)

const recentTraceQuery = `
SELECT trace_id
FROM spans
WHERE start_time >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND (? = '' OR namespace = ?) AND trace_id <> '' AND (? = '' OR service = ?)
GROUP BY trace_id
ORDER BY MAX(CASE WHEN upper(status) IN ('ERROR', 'STATUS_CODE_ERROR') THEN 1 ELSE 0 END) DESC,
         MAX(end_unix_nano) - MIN(start_unix_nano) DESC, trace_id ASC
LIMIT 1`

const traceLogsQuery = `
SELECT time::TIMESTAMP_NS, severity, coalesce(service, ''), body, coalesce(trace_id, ''), coalesce(span_id, '')
FROM logs
WHERE trace_id = ? AND time >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND time < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND (? = '' OR namespace = ?)
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
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: scope.Start, End: scope.End, Namespace: scope.Namespace, Service: service})
	if traceID == "" {
		candidateQuery := recentTraceQuery
		candidateCtx := ctx
		if reader, ok := s.db.(queryrows.BatchReader); ok && reader.CompletedBatchReads() {
			candidateQuery = completedTraceQuery
			candidateCtx = queryrows.WithWindow(ctx, queryrows.Window{Start: scope.Start, End: scope.End, Namespace: scope.Namespace, Service: service, Kind: queryrows.TraceCandidateRead})
		}
		rows, queryErr := s.db.QueryContext(candidateCtx, candidateQuery, scope.Start, scope.End, scope.Namespace, scope.Namespace, service, service)
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
		storedSpans, totals, readErr := s.repository.Trace(ctx, telemetry.TraceQuery{
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
		// has_error describe the whole trace, so they come from the totals the
		// repository accumulated over every span it walked -- not from this
		// page, which is why trace_detail used to report the page's span count
		// and duration as the trace's.
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
		// crosses, how long it took, whether it failed — describes the whole
		// trace, so a narrow page cannot silently redefine it.
		//
		// These come from the index walk that produced the page: it already
		// decodes every row of the trace and applies the same predicates, and
		// only the heap is bounded by limit. An aggregate over every batch file
		// would answer the same question and cost a second pass.
		data.SpanCount, data.ServiceCount = totals.Spans, totals.Services
		data.DurationMS, data.HasError = totals.DurationMS, totals.HasError
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

const completedTraceQuery = `WITH tail AS (
 SELECT trace_id,min(start_unix_nano) AS min_start,max(end_unix_nano) AS max_end,
 max(CASE WHEN upper(status) IN ('ERROR','STATUS_CODE_ERROR') THEN 1 ELSE 0 END) AS has_error
 FROM trace_tail
 WHERE start_time>=$1::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<$2::TIMESTAMP_NS::TIMESTAMPTZ_NS
 AND ($3='' OR namespace=$4) AND ($5='' OR service=$6) AND trace_id<>'' GROUP BY trace_id
), untouched AS (
 SELECT trace_id,min_start,max_end,has_error FROM trace_candidates
 WHERE trace_id NOT IN (SELECT trace_id FROM tail)
 ORDER BY has_error DESC,max_end-min_start DESC,trace_id LIMIT 1
), touched AS (
 SELECT t.trace_id, least(t.min_start,c.min_start) AS min_start,greatest(t.max_end,c.max_end) AS max_end,
 greatest(t.has_error,c.has_error) AS has_error
 FROM tail t LEFT JOIN trace_candidates c ON t.trace_id=c.trace_id
), candidates AS (SELECT * FROM untouched UNION ALL SELECT * FROM touched)
SELECT trace_id FROM candidates ORDER BY has_error DESC,max_end-min_start DESC,trace_id LIMIT 1`
