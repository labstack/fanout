package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
)

// Trace returns a complete distributed trace with auto root-cause analysis.
// window limits the search to recent parquet files (in minutes, default 1440 = 24h).
func (s *Service) Trace(ctx context.Context, traceID string, includeLogs bool, window int) (*TraceResult, error) {
	if traceID == "" {
		return nil, fmt.Errorf("trace_id is required")
	}

	// Default to 24 hours if no window specified
	if window <= 0 {
		window = 1440
	}

	out := &TraceResult{
		TraceID:      traceID,
		Services:     []string{},
		Spans:        []SpanInfo{},
		Logs:         []LogInfo{},
		CriticalPath: []string{},
	}

	// Use config defaults for partition
	namespace, tenantID := s.defaults("", "")

	q := fmt.Sprintf(`
SELECT span_id,
       parent_span_id,
       service,
       operation,
       kind,
       strftime(start_time, '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS start_time,
       duration_ms,
       status,
       status_message,
       start_unix_nano,
       events_json,
       links_json,
       trace_state,
       flags,
       scope_name,
       scope_version,
       attributes_json,
       resource_json
FROM spans
WHERE trace_id = ?
  AND tenant = ?
  AND (? = '' OR namespace = ?)
  AND start_time >= now() - INTERVAL %d MINUTE
ORDER BY start_unix_nano ASC
LIMIT 200;
`, window)

	rows, err := s.duck.DB.QueryContext(ctx, q, traceID, tenantID, namespace, namespace)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	type spanWithNano struct {
		SpanInfo
		startNano int64
	}

	var spans []spanWithNano
	services := make(map[string]bool)

	for rows.Next() {
		var r spanWithNano
		var parentID, statusMsg, eventsJSON, linksJSON, traceState, flags, scopeName, scopeVersion, attrsJSON, resourceJSON any
		if err := rows.Scan(&r.SpanID, &parentID, &r.Service, &r.Name, &r.Kind, &r.StartTime, &r.Duration, &r.Status, &statusMsg, &r.startNano,
			&eventsJSON, &linksJSON, &traceState, &flags, &scopeName, &scopeVersion, &attrsJSON, &resourceJSON); err != nil {
			slog.Warn("scan failed", "method", "Trace", "err", err)
			continue
		}
		if parentID != nil {
			r.ParentID = fmt.Sprintf("%v", parentID)
		}
		if statusMsg != nil {
			r.StatusMsg = fmt.Sprintf("%v", statusMsg)
		}
		if traceState != nil {
			r.TraceState = fmt.Sprintf("%v", traceState)
		}
		if flags != nil {
			if f, ok := flags.(int64); ok {
				r.Flags = uint32(f)
			} else if f, ok := flags.(uint32); ok {
				r.Flags = f
			}
		}
		if scopeName != nil {
			r.ScopeName = fmt.Sprintf("%v", scopeName)
		}
		if scopeVersion != nil {
			r.ScopeVersion = fmt.Sprintf("%v", scopeVersion)
		}
		// Parse events JSON
		if b := jsonValueBytes(eventsJSON); len(b) > 0 {
			var events []SpanEvent
			if err := json.Unmarshal(b, &events); err != nil {
				slog.Debug("events JSON parse failed", "span_id", r.SpanID, "err", err)
			} else {
				r.Events = events
			}
		}
		// Parse links JSON
		if b := jsonValueBytes(linksJSON); len(b) > 0 {
			var links []SpanLink
			if err := json.Unmarshal(b, &links); err != nil {
				slog.Debug("links JSON parse failed", "span_id", r.SpanID, "err", err)
			} else {
				r.Links = links
			}
		}
		// Parse attributes JSON
		if b := jsonValueBytes(attrsJSON); len(b) > 0 {
			var attrs map[string]any
			if err := json.Unmarshal(b, &attrs); err != nil {
				slog.Debug("attributes JSON parse failed", "span_id", r.SpanID, "err", err)
			} else {
				r.Attributes = attrs
			}
		}
		// Parse resource JSON
		if b := jsonValueBytes(resourceJSON); len(b) > 0 {
			var res map[string]any
			if err := json.Unmarshal(b, &res); err != nil {
				slog.Debug("resource JSON parse failed", "span_id", r.SpanID, "err", err)
			} else {
				r.Resource = res
			}
		}
		services[r.Service] = true
		if r.Status == "STATUS_CODE_ERROR" || r.Status == "ERROR" {
			out.HasError = true
		}
		spans = append(spans, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("trace row iteration failed: %w", err)
	}

	// Calculate self time and find root
	var rootSpan *spanWithNano
	childDurations := make(map[string]float64)

	for i := range spans {
		sp := &spans[i]
		if sp.ParentID == "" {
			rootSpan = sp
			if sp.Duration > out.Duration {
				out.Duration = sp.Duration
			}
		}
		if sp.ParentID != "" {
			childDurations[sp.ParentID] += sp.Duration
		}
	}

	for i := range spans {
		sp := &spans[i]
		sp.SelfTime = sp.Duration - childDurations[sp.SpanID]
		if sp.SelfTime < 0 {
			sp.SelfTime = 0
		}
	}

	// Find critical path (spans with highest self time that are errors or slow)
	type criticalSpan struct {
		span     *spanWithNano
		priority float64
	}
	var criticals []criticalSpan

	for i := range spans {
		sp := &spans[i]
		isError := sp.Status == "STATUS_CODE_ERROR" || sp.Status == "ERROR"
		// Critical if self_time is >10% of total trace duration or >100ms absolute.
		threshold := out.Duration * 0.10
		if threshold < 10 {
			threshold = 10 // minimum 10ms to avoid flagging everything in fast traces
		}
		isSlow := sp.SelfTime > threshold

		if isError || isSlow {
			sp.IsCritical = true
			priority := sp.SelfTime
			if isError {
				priority += 10000 // Errors have highest priority
			}
			criticals = append(criticals, criticalSpan{sp, priority})
		}
	}

	sort.Slice(criticals, func(i, j int) bool {
		return criticals[i].priority > criticals[j].priority
	})

	for i, c := range criticals {
		if i >= 5 {
			break
		}
		out.CriticalPath = append(out.CriticalPath, c.span.SpanID)
	}

	// Auto root cause detection
	if out.HasError {
		// Find first error span
		for i := range spans {
			sp := &spans[i]
			if sp.Status == "STATUS_CODE_ERROR" || sp.Status == "ERROR" {
				desc := sp.StatusMsg
				if desc == "" {
					desc = fmt.Sprintf("Error in %s: %s", sp.Service, sp.Name)
				}
				out.RootCause = &RootCause{
					SpanID:      sp.SpanID,
					Service:     sp.Service,
					Operation:   sp.Name,
					Reason:      "error",
					Description: desc,
				}
				break
			}
		}
	} else if rootSpan != nil && rootSpan.Duration > 1000 {
		// Find slowest span
		var slowest *spanWithNano
		for i := range spans {
			sp := &spans[i]
			if slowest == nil || sp.SelfTime > slowest.SelfTime {
				slowest = sp
			}
		}
		if slowest != nil && slowest.SelfTime > 100 {
			out.RootCause = &RootCause{
				SpanID:      slowest.SpanID,
				Service:     slowest.Service,
				Operation:   slowest.Name,
				Reason:      "latency",
				Description: fmt.Sprintf("Slow operation: %s in %s (%.0fms self time)", slowest.Name, slowest.Service, slowest.SelfTime),
			}
		}
	}

	// Convert to output format
	for _, sp := range spans {
		out.Spans = append(out.Spans, sp.SpanInfo)
	}
	out.SpanCount = len(out.Spans)

	for svc := range services {
		out.Services = append(out.Services, svc)
	}

	// Fetch correlated logs
	if includeLogs {
		out.Logs = s.fetchTraceLogs(ctx, traceID, window)
	}

	return out, nil
}

// CompareTrace fetches a second trace and aligns spans by operation name to produce
// a side-by-side comparison against the primary trace result.
func (s *Service) CompareTrace(ctx context.Context, primary *TraceResult, otherTraceID string, window int) (*TraceComparison, error) {
	other, err := s.Trace(ctx, otherTraceID, false, window)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch trace %s: %w", otherTraceID, err)
	}

	// Build a map of operation -> total duration for each trace (service+operation key).
	type opKey struct {
		service   string
		operation string
	}
	primaryMap := make(map[opKey]float64)
	for _, sp := range primary.Spans {
		k := opKey{sp.Service, sp.Name}
		primaryMap[k] += sp.Duration
	}

	otherMap := make(map[opKey]float64)
	for _, sp := range other.Spans {
		k := opKey{sp.Service, sp.Name}
		otherMap[k] += sp.Duration
	}

	// Compute diffs for operations present in both traces.
	var diffs []SpanDiff
	seen := make(map[opKey]bool)
	for k, thisMs := range primaryMap {
		if seen[k] {
			continue
		}
		seen[k] = true
		otherMs := otherMap[k]
		delta := thisMs - otherMs
		if delta < 0 {
			delta = -delta
		}
		diffs = append(diffs, SpanDiff{
			Operation: k.operation,
			Service:   k.service,
			ThisMs:    thisMs,
			OtherMs:   otherMs,
			DeltaMs:   delta,
		})
	}
	// Also include operations only in the other trace.
	for k, otherMs := range otherMap {
		if seen[k] {
			continue
		}
		seen[k] = true
		diffs = append(diffs, SpanDiff{
			Operation: k.operation,
			Service:   k.service,
			ThisMs:    0,
			OtherMs:   otherMs,
			DeltaMs:   otherMs,
		})
	}

	// Sort by delta descending so biggest differences appear first.
	sort.Slice(diffs, func(i, j int) bool {
		return diffs[i].DeltaMs > diffs[j].DeltaMs
	})

	delta := primary.Duration - other.Duration
	if delta < 0 {
		delta = -delta
	}

	return &TraceComparison{
		OtherTraceID:    otherTraceID,
		OtherDurationMs: other.Duration,
		DurationDeltaMs: delta,
		SpanDiffs:       diffs,
	}, nil
}

// FetchMetricContext queries service_rollup for a 5-minute window around the trace's
// earliest span start time and returns per-service metric snapshots.
func (s *Service) FetchMetricContext(ctx context.Context, result *TraceResult) []MetricContext {
	if len(result.Spans) == 0 {
		return nil
	}
	namespace, tenantID := s.defaults("", "")

	// Derive the earliest start time from span start times (stored as RFC3339 strings).
	// We use the first span since spans are already ordered by start_unix_nano ASC.
	// Re-query for the start nano directly from the TraceResult's first span StartTime.
	// Instead of reparsing, we rely on the Trace() internals having ordered spans by
	// start_unix_nano, so we can derive a reasonable timestamp via a direct rollup query.

	// Build service list from result.
	if len(result.Services) == 0 {
		return nil
	}

	// Query service_rollup using the first span's start_time.
	// We convert the ISO8601 start_time to a timestamp and search ±2.5 min around it.
	startTime := result.Spans[0].StartTime // e.g. "2024-01-01T10:00:00Z"

	placeholders := makePlaceholders(len(result.Services))
	args := make([]any, 0, len(result.Services)+2)
	args = append(args, startTime, startTime, tenantID, namespace, namespace)
	for _, svc := range result.Services {
		args = append(args, svc)
	}

	q := fmt.Sprintf(`
SELECT service,
       AVG(p50_ms) as p50_ms,
       AVG(p95_ms) as p95_ms,
       AVG(error_rate) as error_rate,
       SUM(spans) as total_spans
FROM service_rollup
WHERE bucket BETWEEN (TIMESTAMPTZ ? - INTERVAL '2.5 minutes') AND (TIMESTAMPTZ ? + INTERVAL '2.5 minutes')
  AND tenant = ?
  AND (? = '' OR namespace = ?)
  AND service IN (%s)
GROUP BY service
ORDER BY service ASC;
`, placeholders)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		slog.Warn("FetchMetricContext: rollup query failed", "err", err)
		return nil
	}
	defer rows.Close()

	var contexts []MetricContext
	for rows.Next() {
		var mc MetricContext
		var totalSpans float64
		if err := rows.Scan(&mc.Service, &mc.AtTraceTime.P50Ms, &mc.AtTraceTime.P95Ms, &mc.AtTraceTime.ErrorRate, &totalSpans); err != nil {
			slog.Warn("FetchMetricContext: scan failed", "err", err)
			continue
		}
		mc.AtTraceTime.SpansPerMin = totalSpans / 5.0
		contexts = append(contexts, mc)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("FetchMetricContext: rows iteration error", "err", err)
	}
	return contexts
}

func (s *Service) fetchTraceLogs(ctx context.Context, traceID string, window int) []LogInfo {
	logs := []LogInfo{}
	namespace, tenantID := s.defaults("", "")

	q := fmt.Sprintf(`
SELECT strftime(time, '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS ts,
       CASE WHEN observed_time_unix_nano > 0
            THEN strftime(observed_time, '%%Y-%%m-%%dT%%H:%%M:%%SZ')
            ELSE NULL END AS observed_ts,
       service,
       severity,
       severity_number,
       body,
       span_id,
       flags,
       scope_name,
       scope_version,
       attributes_json
FROM logs
WHERE trace_id = ?
  AND tenant = ?
  AND (? = '' OR namespace = ?)
  AND time >= now() - INTERVAL %d MINUTE
ORDER BY time_unix_nano ASC
LIMIT 100;
`, window)

	rows, err := s.duck.DB.QueryContext(ctx, q, traceID, tenantID, namespace, namespace)
	if err != nil {
		slog.Warn("fetch trace logs query failed", "method", "fetchTraceLogs", "trace_id", traceID, "err", err)
		return logs
	}
	defer rows.Close()

	for rows.Next() {
		var r LogInfo
		r.TraceID = traceID
		var observedTime, spanID, flags, scopeName, scopeVersion, attrsJSON any
		var severityNum any
		if err := rows.Scan(&r.Time, &observedTime, &r.Service, &r.Severity, &severityNum, &r.Body, &spanID, &flags, &scopeName, &scopeVersion, &attrsJSON); err != nil {
			slog.Warn("scan failed", "method", "fetchTraceLogs", "err", err)
			continue
		}
		if observedTime != nil {
			r.ObservedTime = fmt.Sprintf("%v", observedTime)
		}
		if severityNum != nil {
			if num, ok := severityNum.(int64); ok {
				r.SeverityNumber = int32(num)
			} else if num, ok := severityNum.(int32); ok {
				r.SeverityNumber = num
			}
		}
		if spanID != nil {
			r.SpanID = fmt.Sprintf("%v", spanID)
		}
		if flags != nil {
			if f, ok := flags.(int64); ok {
				r.Flags = uint32(f)
			} else if f, ok := flags.(uint32); ok {
				r.Flags = f
			}
		}
		if scopeName != nil {
			r.ScopeName = fmt.Sprintf("%v", scopeName)
		}
		if scopeVersion != nil {
			r.ScopeVersion = fmt.Sprintf("%v", scopeVersion)
		}
		if b := jsonValueBytes(attrsJSON); len(b) > 0 {
			var attrs map[string]any
			if err := json.Unmarshal(b, &attrs); err != nil {
				slog.Debug("log attributes JSON parse failed", "trace_id", traceID, "err", err)
			} else {
				r.Attributes = attrs
			}
		}
		logs = append(logs, r)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("fetch trace logs iteration error", "method", "fetchTraceLogs", "trace_id", traceID, "err", err)
	}
	return logs
}

func jsonValueBytes(v any) []byte {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		if len(x) == 0 {
			return nil
		}
		return x
	case string:
		if x == "" {
			return nil
		}
		return []byte(x)
	default:
		s := fmt.Sprintf("%v", x)
		if s == "" || s == "<nil>" {
			return nil
		}
		return []byte(s)
	}
}
