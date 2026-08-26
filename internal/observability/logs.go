package observability

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

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
	type bucketKey struct {
		time     int64
		severity string
	}
	buckets := make(map[bucketKey]int64)
	unlock := s.repository.ReadLock()
	err = s.repository.Logs.Scan(scope.Start.UnixNano(), scope.End.UnixNano(), func(row telemetry.Log) bool {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		if (scope.Namespace != "" && row.Namespace != scope.Namespace) || (service != "" && row.ServiceName != service) || (severity != "" && !strings.EqualFold(row.Severity, severity)) {
			return true
		}
		body := redactLogBody(row.Body)
		if search != "" && !strings.Contains(strings.ToLower(body), search) {
			return true
		}
		entryTime := time.Unix(0, row.EventUnixNanos).UTC()
		data.Entries = append(data.Entries, LogEntry{Time: entryTime, Severity: row.Severity, Service: row.ServiceName, Body: body, TraceID: row.TraceID, SpanID: row.SpanID})
		bucketSeverity := strings.ToUpper(row.Severity)
		if bucketSeverity == "" {
			bucketSeverity = "UNSPECIFIED"
		}
		bucketNanos := entryTime.Truncate(5 * time.Minute).UnixNano()
		buckets[bucketKey{time: bucketNanos, severity: bucketSeverity}]++
		return true
	})
	unlock()
	if err != nil {
		return Result[Logs]{}, fmt.Errorf("read log segments: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Result[Logs]{}, err
	}
	sort.Slice(data.Entries, func(i, j int) bool { return data.Entries[i].Time.After(data.Entries[j].Time) })
	if len(data.Entries) > limit {
		data.Entries = data.Entries[:limit]
	}
	for key, count := range buckets {
		data.Buckets = append(data.Buckets, LogBucket{Time: time.Unix(0, key.time).UTC(), Severity: key.severity, Count: count})
	}
	sort.Slice(data.Buckets, func(i, j int) bool {
		if data.Buckets[i].Time.Equal(data.Buckets[j].Time) {
			return data.Buckets[i].Severity < data.Buckets[j].Severity
		}
		return data.Buckets[i].Time.Before(data.Buckets[j].Time)
	})
	return Result[Logs]{
		Schema: LogsSchema, Summary: fmt.Sprintf("%d logs matched the selected telemetry window", len(data.Entries)),
		Data: data, Provenance: s.provenanceFor(scope, "fanout_segments"),
	}, nil
}
