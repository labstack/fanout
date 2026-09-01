package observability

import (
	"context"
	"fmt"
	"strings"
	"time"
)

var logFilters = `
WHERE time >= ? AND time < ?
  AND (? = '' OR namespace = ?)
  AND (? = '' OR service = ?)
  AND (? = '' OR lower(severity) = lower(?))
  AND (? = '' OR contains(lower(` + redactLogBodySQL("body") + `), lower(?)))`

// DuckDB answers the two questions the API asks: the newest `limit` entries
// and per-bucket counts. LIMIT bounds the entry stream and GROUP BY bounds the
// histogram stream regardless of the retained Parquet row count.
var logEntriesQuery = `
SELECT time, severity, coalesce(service, ''), ` + redactLogBodySQL("body") + `,
       coalesce(trace_id, ''), coalesce(span_id, '')
	FROM logs` + logFilters + `
ORDER BY time DESC
LIMIT ?`

var logBucketsQueryTemplate = `
SELECT time_bucket(INTERVAL '%s', time) AS point_time,
       coalesce(nullif(upper(severity), ''), 'UNSPECIFIED') AS bucket_severity,
       CAST(count(*) AS BIGINT)
	FROM logs` + logFilters + `
GROUP BY point_time, bucket_severity
ORDER BY point_time ASC, bucket_severity ASC`

func logBucketsSQL(window time.Duration) string {
	return fmt.Sprintf(logBucketsQueryTemplate, timelineBucketWidth(window))
}

func (s *Service) Logs(ctx context.Context, scope Scope, service, severity, search string, limit int) (Result[Logs], error) {
	scope, err := s.normalizeScope(scope)
	if err != nil {
		return Result[Logs]{}, err
	}
	limit, err = normalizeLimit(limit)
	if err != nil {
		return Result[Logs]{}, err
	}
	service, severity, search = strings.TrimSpace(service), strings.TrimSpace(severity), strings.TrimSpace(search)
	search = strings.ToLower(search)
	data := Logs{Entries: []LogEntry{}, Buckets: []LogBucket{}}
	filters := []any{scope.Start, scope.End, scope.Namespace, scope.Namespace, service, service, severity, severity, search, search}
	rows, err := s.db.QueryContext(ctx, logEntriesQuery, append(append([]any{}, filters...), limit)...)
	if err != nil {
		return Result[Logs]{}, fmt.Errorf("query logs: %w", err)
	}
	for rows.Next() {
		var entry LogEntry
		if err := rows.Scan(&entry.Time, &entry.Severity, &entry.Service, &entry.Body, &entry.TraceID, &entry.SpanID); err != nil {
			rows.Close()
			return Result[Logs]{}, fmt.Errorf("scan log: %w", err)
		}
		// Keep Go redaction as a defense-in-depth boundary even though DuckDB
		// applies the equivalent expression before filtering and transfer.
		entry.Body = redactLogBody(entry.Body)
		data.Entries = append(data.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Result[Logs]{}, fmt.Errorf("iterate logs: %w", err)
	}
	rows.Close()

	matched := 0
	bucketRows, err := s.db.QueryContext(ctx, logBucketsSQL(scope.End.Sub(scope.Start)), filters...)
	if err != nil {
		return Result[Logs]{}, fmt.Errorf("query log histogram: %w", err)
	}
	for bucketRows.Next() {
		var bucket LogBucket
		if err := bucketRows.Scan(&bucket.Time, &bucket.Severity, &bucket.Count); err != nil {
			bucketRows.Close()
			return Result[Logs]{}, fmt.Errorf("scan log bucket: %w", err)
		}
		bucket.Time = bucket.Time.UTC()
		matched += int(bucket.Count)
		data.Buckets = append(data.Buckets, bucket)
	}
	if err := bucketRows.Err(); err != nil {
		bucketRows.Close()
		return Result[Logs]{}, fmt.Errorf("iterate log histogram: %w", err)
	}
	bucketRows.Close()
	return Result[Logs]{
		Schema: LogsSchema, Summary: fmt.Sprintf("%d logs matched the selected telemetry window", matched),
		Data: data, Provenance: s.provenanceFor(scope, "parquet"),
	}, nil
}
