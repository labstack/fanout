package service

import (
	"context"
	"encoding/json"
	"fmt"
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

	// Get spans (using optimized glob for time window)
	// Use union_by_name=true to handle schema evolution (old files may not have new columns)
	q := fmt.Sprintf(`
SELECT "name=span_id" as span_id,
       "name=parent_span_id" as parent_span_id,
       "name=service_name" as service,
       "name=name" as operation,
       "name=kind" as kind,
       strftime(epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS start_time,
       "name=duration_ms" as duration_ms,
       "name=status_code" as status,
       "name=status_msg" as status_msg,
       "name=start_unix_nano" as start_nano,
       "name=events_json" as events_json,
       "name=links_json" as links_json,
       "name=trace_state" as trace_state,
       "name=flags" as flags,
       "name=scope_name" as scope_name,
       "name=scope_version" as scope_version,
       "name=attributes_json" as attributes_json
FROM read_parquet(%s, union_by_name=true)
WHERE "name=trace_id" = '%s'
ORDER BY "name=start_unix_nano" ASC;
`, s.duck.SpansGlob(tenantID, namespace, window), escapeSQL(traceID))

	rows, err := s.duck.DB.QueryContext(ctx, q)
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
	spanByID := make(map[string]*spanWithNano)

	for rows.Next() {
		var r spanWithNano
		var parentID, statusMsg, eventsJSON, linksJSON, traceState, flags, scopeName, scopeVersion, attrsJSON any
		if err := rows.Scan(&r.SpanID, &parentID, &r.Service, &r.Name, &r.Kind, &r.StartTime, &r.Duration, &r.Status, &statusMsg, &r.startNano,
			&eventsJSON, &linksJSON, &traceState, &flags, &scopeName, &scopeVersion, &attrsJSON); err != nil {
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
		if eventsJSON != nil {
			if b, ok := eventsJSON.([]byte); ok && len(b) > 0 {
				var events []SpanEvent
				if err := json.Unmarshal(b, &events); err == nil {
					r.Events = events
				}
			}
		}
		// Parse links JSON
		if linksJSON != nil {
			if b, ok := linksJSON.([]byte); ok && len(b) > 0 {
				var links []SpanLink
				if err := json.Unmarshal(b, &links); err == nil {
					r.Links = links
				}
			}
		}
		// Parse attributes JSON
		if attrsJSON != nil {
			if b, ok := attrsJSON.([]byte); ok && len(b) > 0 {
				var attrs map[string]any
				if err := json.Unmarshal(b, &attrs); err == nil {
					r.Attributes = attrs
				}
			}
		}
		services[r.Service] = true
		if r.Status == "STATUS_CODE_ERROR" || r.Status == "ERROR" {
			out.HasError = true
		}
		spans = append(spans, r)
		spanByID[r.SpanID] = &spans[len(spans)-1]
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
		isSlow := sp.SelfTime > 100

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

func (s *Service) fetchTraceLogs(ctx context.Context, traceID string, window int) []LogInfo {
	logs := []LogInfo{}
	namespace, tenantID := s.defaults("", "")

	q := fmt.Sprintf(`
SELECT strftime(epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ') AS ts,
       CASE WHEN "name=observed_time_unix_nano" > 0
            THEN strftime(epoch_ms(CAST("name=observed_time_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ')
            ELSE NULL END AS observed_ts,
       "name=service_name" as service,
       "name=severity" as severity,
       "name=severity_number" as severity_number,
       "name=body" as body,
       "name=span_id" as span_id,
       "name=flags" as flags,
       "name=scope_name" as scope_name,
       "name=scope_version" as scope_version,
       "name=attributes_json" as attributes_json
FROM read_parquet(%s, union_by_name=true)
WHERE "name=trace_id" = '%s'
ORDER BY "name=time_unix_nano" ASC
LIMIT 100;
`, s.duck.LogsGlob(tenantID, namespace, window), escapeSQL(traceID))

	rows, err := s.duck.DB.QueryContext(ctx, q)
	if err != nil {
		return logs
	}
	defer rows.Close()

	for rows.Next() {
		var r LogInfo
		r.TraceID = traceID
		var observedTime, spanID, flags, scopeName, scopeVersion, attrsJSON any
		var severityNum any
		rows.Scan(&r.Time, &observedTime, &r.Service, &r.Severity, &severityNum, &r.Body, &spanID, &flags, &scopeName, &scopeVersion, &attrsJSON)
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
		if attrsJSON != nil {
			if b, ok := attrsJSON.([]byte); ok && len(b) > 0 {
				var attrs map[string]any
				if err := json.Unmarshal(b, &attrs); err == nil {
					r.Attributes = attrs
				}
			}
		}
		logs = append(logs, r)
	}
	return logs
}
