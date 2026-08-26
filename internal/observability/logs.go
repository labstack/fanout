package observability

import (
	"container/heap"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

// newestLogHeap keeps its oldest entry at the root so a full-window scan only
// retains the newest bounded result set.
type newestLogHeap []LogEntry

func (h newestLogHeap) Len() int           { return len(h) }
func (h newestLogHeap) Less(i, j int) bool { return h[i].Time.Before(h[j].Time) }
func (h newestLogHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *newestLogHeap) Push(value any)    { *h = append(*h, value.(LogEntry)) }
func (h *newestLogHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}

// earliestLogHeap keeps its newest entry at the root so trace correlation can
// retain the earliest bounded result set even when batches arrive out of order.
type earliestLogHeap []LogEntry

func (h earliestLogHeap) Len() int           { return len(h) }
func (h earliestLogHeap) Less(i, j int) bool { return h[i].Time.After(h[j].Time) }
func (h earliestLogHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *earliestLogHeap) Push(value any)    { *h = append(*h, value.(LogEntry)) }
func (h *earliestLogHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}

func retainNewest(entries *newestLogHeap, entry LogEntry, limit int) {
	if entries.Len() < limit {
		heap.Push(entries, entry)
	} else if entry.Time.After((*entries)[0].Time) {
		(*entries)[0] = entry
		heap.Fix(entries, 0)
	}
}

func retainEarliest(entries *earliestLogHeap, entry LogEntry, limit int) {
	if entries.Len() < limit {
		heap.Push(entries, entry)
	} else if entry.Time.Before((*entries)[0].Time) {
		(*entries)[0] = entry
		heap.Fix(entries, 0)
	}
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
	type bucketKey struct {
		time     int64
		severity string
	}
	buckets := make(map[bucketKey]int64)
	entries := newestLogHeap{}
	matched := 0
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
		matched++
		retainNewest(&entries, LogEntry{Time: entryTime, Severity: row.Severity, Service: row.ServiceName, Body: body, TraceID: row.TraceID, SpanID: row.SpanID}, limit)
		bucketSeverity := strings.ToUpper(row.Severity)
		if bucketSeverity == "" {
			bucketSeverity = "UNSPECIFIED"
		}
		bucketNanos := entryTime.Truncate(5 * time.Minute).UnixNano()
		buckets[bucketKey{time: bucketNanos, severity: bucketSeverity}]++
		return true
	})
	if err != nil {
		return Result[Logs]{}, fmt.Errorf("read log segments: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Result[Logs]{}, err
	}
	data.Entries = append(data.Entries, entries...)
	sort.Slice(data.Entries, func(i, j int) bool { return data.Entries[i].Time.After(data.Entries[j].Time) })
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
		Schema: LogsSchema, Summary: fmt.Sprintf("%d logs matched the selected telemetry window", matched),
		Data: data, Provenance: s.provenanceFor(scope, "fanout_segments"),
	}, nil
}
