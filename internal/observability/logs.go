package observability

import (
	"context"
	"fmt"
	"strings"
)

// The user-supplied search filter must compare against the REDACTED body,
// not the raw column: a telemetry viewer could otherwise confirm a secret's
// presence by probing search=<candidate> even though the display shows
// [REDACTED]. redactedBodySQL replays the exact
// Go-side patterns inside DuckDB (same RE2 engine; parity pinned by
// TestRedactSQLMatchesGo), and both the row query and the histogram query
// use it so their counts can never disagree and leak the same signal.
var redactedBodySQL = redactLogBodySQL("COALESCE(body, '')")

var logsEntriesQuery = `
SELECT time, COALESCE(severity, ''), COALESCE(service, ''), COALESCE(body, ''),
       COALESCE(trace_id, ''), COALESCE(span_id, '')
FROM logs
WHERE time >= ? AND time < ? AND namespace = ?
  AND (? = '' OR service = ?)
  AND (? = '' OR upper(severity) = upper(?))
  AND (? = '' OR ` + redactedBodySQL + ` ILIKE ?)
ORDER BY time DESC
LIMIT ?`

var logsBucketsQuery = `
SELECT time_bucket(INTERVAL '5 minutes', time) AS point_time,
       COALESCE(NULLIF(upper(severity), ''), 'UNSPECIFIED'), CAST(COUNT(*) AS BIGINT)
FROM logs
WHERE time >= ? AND time < ? AND namespace = ?
  AND (? = '' OR service = ?)
  AND (? = '' OR upper(severity) = upper(?))
  AND (? = '' OR ` + redactedBodySQL + ` ILIKE ?)
GROUP BY point_time, severity
ORDER BY point_time ASC, severity ASC`

func (s *Service) Logs(ctx context.Context, scope Scope, service, severity, search string, limit int) (Result[Logs], error) {
	scope, err := s.normalizeScope(scope)
	if err != nil {
		return Result[Logs]{}, err
	}
	limit, err = normalizeLimit(limit)
	if err != nil {
		return Result[Logs]{}, err
	}
	service = strings.TrimSpace(service)
	severity = strings.TrimSpace(severity)
	search = strings.TrimSpace(search)
	pattern := search
	if pattern != "" {
		pattern = "%" + pattern + "%"
	}

	data := Logs{Entries: []LogEntry{}, Buckets: []LogBucket{}}
	rows, err := s.db.QueryContext(ctx, logsEntriesQuery, scope.Start, scope.End, scope.Namespace, service, service, severity, severity, search, pattern, limit)
	if err != nil {
		return Result[Logs]{}, fmt.Errorf("query logs: %w", err)
	}
	for rows.Next() {
		var entry LogEntry
		if err := rows.Scan(&entry.Time, &entry.Severity, &entry.Service, &entry.Body, &entry.TraceID, &entry.SpanID); err != nil {
			rows.Close()
			return Result[Logs]{}, fmt.Errorf("scan log: %w", err)
		}
		entry.Body = redactLogBody(entry.Body)
		data.Entries = append(data.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Result[Logs]{}, fmt.Errorf("iterate logs: %w", err)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx, logsBucketsQuery, scope.Start, scope.End, scope.Namespace, service, service, severity, severity, search, pattern)
	if err != nil {
		return Result[Logs]{}, fmt.Errorf("query log histogram: %w", err)
	}
	for rows.Next() {
		var bucket LogBucket
		if err := rows.Scan(&bucket.Time, &bucket.Severity, &bucket.Count); err != nil {
			rows.Close()
			return Result[Logs]{}, fmt.Errorf("scan log histogram: %w", err)
		}
		data.Buckets = append(data.Buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Result[Logs]{}, fmt.Errorf("iterate log histogram: %w", err)
	}
	rows.Close()

	return Result[Logs]{
		Schema:     LogsSchema,
		Summary:    fmt.Sprintf("%d logs matched the selected telemetry window", len(data.Entries)),
		Data:       data,
		Provenance: s.provenanceFor(scope, "logs"),
	}, nil
}
