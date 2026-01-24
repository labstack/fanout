package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TraceSearchParams contains trace search parameters.
type TraceSearchParams struct {
	Query      string
	Services   []string
	Operations []string          // op:checkout,payment
	Status     []string          // error, slow
	Duration   string            // e.g., ">1000", "<500"
	Attrs      map[string]string // attr:key=value filters
	TraceID    string            // trace:abc123
	SpanID     string            // span:def456
	Terms      []string
	Exclude    []string
	Window     int
	Limit      int
	Offset     int
	Namespace  string
	TenantID   string
}

// TraceRow represents a trace in search results.
type TraceRow struct {
	TraceID   string
	Service   string
	Operation string
	Duration  float64
	Status    string
	Time      string
}

// TraceSearchResult contains paginated trace results.
type TraceSearchResult struct {
	Traces  []TraceRow
	HasMore bool
}

// SearchTraces searches for traces with pagination.
func (s *Service) SearchTraces(ctx context.Context, p TraceSearchParams) (*TraceSearchResult, error) {
	if p.Window == 0 {
		p.Window = 60
	}
	if p.Limit == 0 {
		p.Limit = 50
	}

	p.Namespace, p.TenantID = s.defaults(p.Namespace, p.TenantID)
	spansGlob := s.duck.SpansGlob(p.TenantID, p.Namespace, p.Window)

	var filters []string
	var args []any

	filters = append(filters, fmt.Sprintf(
		`epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE`, p.Window))

	// Service filter
	if len(p.Services) > 0 {
		placeholders := makePlaceholders(len(p.Services))
		filters = append(filters, fmt.Sprintf(`"name=service_name" IN (%s)`, placeholders))
		for _, svc := range p.Services {
			args = append(args, svc)
		}
	}

	// Operation filter (span-level search)
	if len(p.Operations) > 0 {
		placeholders := makePlaceholders(len(p.Operations))
		filters = append(filters, fmt.Sprintf(`"name=name" IN (%s)`, placeholders))
		for _, op := range p.Operations {
			args = append(args, op)
		}
	}

	// Status filter
	for _, status := range p.Status {
		switch status {
		case "error":
			filters = append(filters, `"name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR')`)
		case "slow":
			filters = append(filters, `"name=duration_ms" > 1000`)
		}
	}

	// Duration filter
	if p.Duration != "" && len(p.Duration) > 1 {
		op := string(p.Duration[0])
		val := p.Duration[1:]
		if op == ">" || op == "<" {
			filters = append(filters, fmt.Sprintf(`"name=duration_ms" %s ?`, op))
			args = append(args, val)
		}
	}

	// Trace ID filter
	if p.TraceID != "" {
		filters = append(filters, `"name=trace_id" = ?`)
		args = append(args, p.TraceID)
	}

	// Span ID filter
	if p.SpanID != "" {
		filters = append(filters, `"name=span_id" = ?`)
		args = append(args, p.SpanID)
	}

	// Attribute filters (JSON extraction)
	for key, val := range p.Attrs {
		filters = append(filters, `json_extract_string(from_utf8("name=attributes_json"), ?) = ?`)
		args = append(args, "$."+key, val)
	}

	// Text search terms
	for _, term := range p.Terms {
		filters = append(filters, `("name=name" ILIKE ? OR "name=trace_id" ILIKE ?)`)
		args = append(args, "%"+term+"%", "%"+term+"%")
	}

	// Exclude terms
	for _, term := range p.Exclude {
		filters = append(filters, `"name=name" NOT ILIKE ?`)
		args = append(args, "%"+term+"%")
	}

	whereClause := "WHERE " + strings.Join(filters, " AND ")

	q := fmt.Sprintf(`
SELECT DISTINCT ON ("name=trace_id")
  "name=trace_id" as trace_id,
  "name=service_name" as service,
  "name=name" as operation,
  "name=duration_ms" as duration,
  "name=status_code" as status,
  epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) as ts
FROM read_parquet(%s, union_by_name=true)
%s
ORDER BY "name=trace_id", ts DESC
LIMIT %d OFFSET %d;
`, spansGlob, whereClause, p.Limit+1, p.Offset)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return &TraceSearchResult{Traces: []TraceRow{}}, nil
	}
	defer rows.Close()

	var traces []TraceRow
	for rows.Next() {
		var t TraceRow
		var statusCode string
		var ts any
		if err := rows.Scan(&t.TraceID, &t.Service, &t.Operation, &t.Duration, &statusCode, &ts); err != nil {
			continue
		}
		if statusCode == "STATUS_CODE_ERROR" || statusCode == "ERROR" {
			t.Status = "error"
		} else {
			t.Status = "ok"
		}
		t.Time = fmt.Sprintf("%v", ts)
		traces = append(traces, t)
	}

	hasMore := len(traces) > p.Limit
	if hasMore {
		traces = traces[:p.Limit]
	}

	return &TraceSearchResult{Traces: traces, HasMore: hasMore}, nil
}

// TraceDetailResult contains full trace info for UI rendering.
type TraceDetailResult struct {
	TraceID     string
	RootService string
	RootOp      string
	Duration    float64
	SpanCount   int
	RootCause   string
	Spans       []SpanDetailInfo
	Logs        []LogInfo
}

// SpanDetailInfo extends SpanInfo with UI-specific fields.
type SpanDetailInfo struct {
	SpanID       string
	ParentID     string
	Service      string
	Operation    string
	Duration     float64
	SelfTime     float64
	Status       string
	StatusMsg    string
	Kind         string
	StartOffset  float64
	Depth        int
	Events       []SpanEvent
	Links        []SpanLink
	TraceState   string
	Flags        uint32
	ScopeName    string
	ScopeVersion string
	Attributes   map[string]any
}

// TraceDetail returns trace info formatted for UI rendering.
func (s *Service) TraceDetail(ctx context.Context, traceID string, window int, namespace, tenantID string) (*TraceDetailResult, error) {
	if window == 0 {
		window = 1440
	}

	namespace, tenantID = s.defaults(namespace, tenantID)
	spansGlob := s.duck.SpansGlob(tenantID, namespace, window)

	out := &TraceDetailResult{
		TraceID: traceID,
		Spans:   []SpanDetailInfo{},
		Logs:    []LogInfo{},
	}

	q := fmt.Sprintf(`
SELECT
  "name=span_id" as span_id,
  "name=parent_span_id" as parent_id,
  "name=service_name" as service,
  "name=name" as operation,
  "name=duration_ms" as duration,
  "name=status_code" as status,
  COALESCE("name=status_msg", '') as status_msg,
  COALESCE("name=kind", '') as kind,
  "name=start_unix_nano" as start_nano,
  "name=events_json" as events_json,
  "name=links_json" as links_json,
  "name=trace_state" as trace_state,
  "name=flags" as flags,
  "name=scope_name" as scope_name,
  "name=scope_version" as scope_version,
  "name=attributes_json" as attributes_json
FROM read_parquet(%s, union_by_name=true)
WHERE "name=trace_id" = ?
ORDER BY "name=start_unix_nano" ASC;
`, spansGlob)

	rows, err := s.duck.DB.QueryContext(ctx, q, traceID)
	if err != nil {
		return out, nil
	}
	defer rows.Close()

	var minStart int64 = -1
	var maxEnd int64 = -1
	type spanRaw struct {
		spanID       string
		parentID     string
		service      string
		operation    string
		duration     float64
		status       string
		statusMsg    string
		kind         string
		startNano    int64
		events       []SpanEvent
		links        []SpanLink
		traceState   string
		flags        uint32
		scopeName    string
		scopeVersion string
		attributes   map[string]any
	}
	var spans []spanRaw
	parentMap := make(map[string]string)
	childDurations := make(map[string]float64)

	for rows.Next() {
		var sp spanRaw
		var eventsJSON, linksJSON, traceState, flags, scopeName, scopeVersion, attrsJSON any
		if err := rows.Scan(&sp.spanID, &sp.parentID, &sp.service, &sp.operation,
			&sp.duration, &sp.status, &sp.statusMsg, &sp.kind, &sp.startNano,
			&eventsJSON, &linksJSON, &traceState, &flags, &scopeName, &scopeVersion, &attrsJSON); err != nil {
			continue
		}
		// Parse events JSON
		if eventsJSON != nil {
			if b, ok := eventsJSON.([]byte); ok && len(b) > 0 {
				_ = json.Unmarshal(b, &sp.events)
			}
		}
		// Parse links JSON
		if linksJSON != nil {
			if b, ok := linksJSON.([]byte); ok && len(b) > 0 {
				_ = json.Unmarshal(b, &sp.links)
			}
		}
		// Parse attributes JSON
		if attrsJSON != nil {
			if b, ok := attrsJSON.([]byte); ok && len(b) > 0 {
				_ = json.Unmarshal(b, &sp.attributes)
			}
		}
		if traceState != nil {
			sp.traceState = fmt.Sprintf("%v", traceState)
		}
		if flags != nil {
			if f, ok := flags.(int64); ok {
				sp.flags = uint32(f)
			}
		}
		if scopeName != nil {
			sp.scopeName = fmt.Sprintf("%v", scopeName)
		}
		if scopeVersion != nil {
			sp.scopeVersion = fmt.Sprintf("%v", scopeVersion)
		}
		if minStart == -1 || sp.startNano < minStart {
			minStart = sp.startNano
		}
		endNano := sp.startNano + int64(sp.duration*1e6)
		if maxEnd == -1 || endNano > maxEnd {
			maxEnd = endNano
		}
		parentMap[sp.spanID] = sp.parentID
		spans = append(spans, sp)
	}

	// Calculate child durations for self-time
	for _, sp := range spans {
		if sp.parentID != "" {
			childDurations[sp.parentID] += sp.duration
		}
	}

	// Calculate depths
	getDepth := func(spanID string) int {
		depth := 0
		current := spanID
		for {
			parent, ok := parentMap[current]
			if !ok || parent == "" {
				break
			}
			depth++
			current = parent
			if depth > 20 {
				break
			}
		}
		return depth
	}

	for _, sp := range spans {
		status := "ok"
		if sp.status == "STATUS_CODE_ERROR" || sp.status == "ERROR" {
			status = "error"
			if out.RootCause == "" {
				out.RootCause = fmt.Sprintf("%s: %s", sp.service, sp.operation)
			}
		}

		offset := float64(sp.startNano-minStart) / 1e6
		if sp.parentID == "" {
			out.RootService = sp.service
			out.RootOp = sp.operation
		}

		selfTime := sp.duration - childDurations[sp.spanID]
		if selfTime < 0 {
			selfTime = 0
		}

		out.Spans = append(out.Spans, SpanDetailInfo{
			SpanID:       sp.spanID,
			ParentID:     sp.parentID,
			Service:      sp.service,
			Operation:    sp.operation,
			Duration:     sp.duration,
			SelfTime:     selfTime,
			Status:       status,
			StatusMsg:    sp.statusMsg,
			Kind:         sp.kind,
			StartOffset:  offset,
			Depth:        getDepth(sp.spanID),
			Events:       sp.events,
			Links:        sp.links,
			TraceState:   sp.traceState,
			Flags:        sp.flags,
			ScopeName:    sp.scopeName,
			ScopeVersion: sp.scopeVersion,
			Attributes:   sp.attributes,
		})
	}

	out.SpanCount = len(out.Spans)
	if minStart != -1 && maxEnd != -1 && maxEnd > minStart {
		out.Duration = float64(maxEnd-minStart) / 1e6
	}
	if len(out.Spans) > 0 && out.Duration == 0 {
		out.Duration = out.Spans[0].Duration
	}

	// Fetch logs
	logsGlob := s.duck.LogsGlob(tenantID, namespace, window)
	q = fmt.Sprintf(`
SELECT
  strftime(epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ') as ts,
  CASE WHEN "name=observed_time_unix_nano" > 0
       THEN strftime(epoch_ms(CAST("name=observed_time_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ')
       ELSE NULL END as observed_ts,
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
WHERE "name=trace_id" = ?
ORDER BY "name=time_unix_nano" ASC
LIMIT 50;
`, logsGlob)

	rows, err = s.duck.DB.QueryContext(ctx, q, traceID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var log LogInfo
			log.TraceID = traceID
			var observedTime, severityNum, spanID, flags, scopeName, scopeVersion, attrsJSON any
			if err := rows.Scan(&log.Time, &observedTime, &log.Service, &log.Severity, &severityNum,
				&log.Body, &spanID, &flags, &scopeName, &scopeVersion, &attrsJSON); err == nil {
				if observedTime != nil {
					log.ObservedTime = fmt.Sprintf("%v", observedTime)
				}
				if severityNum != nil {
					if num, ok := severityNum.(int64); ok {
						log.SeverityNumber = int32(num)
					}
				}
				if spanID != nil {
					log.SpanID = fmt.Sprintf("%v", spanID)
				}
				if flags != nil {
					if f, ok := flags.(int64); ok {
						log.Flags = uint32(f)
					}
				}
				if scopeName != nil {
					log.ScopeName = fmt.Sprintf("%v", scopeName)
				}
				if scopeVersion != nil {
					log.ScopeVersion = fmt.Sprintf("%v", scopeVersion)
				}
				if attrsJSON != nil {
					if b, ok := attrsJSON.([]byte); ok && len(b) > 0 {
						_ = json.Unmarshal(b, &log.Attributes)
					}
				}
				out.Logs = append(out.Logs, log)
			}
		}
	}

	return out, nil
}

// LogSearchParams contains log search parameters.
type LogSearchParams struct {
	Query     string
	Services  []string
	Severity  []string
	Terms     []string
	Exclude   []string
	Window    int
	Limit     int
	Offset    int
	Namespace string
	TenantID  string
}

// LogRow represents a log in search results.
type LogRow struct {
	Time           string
	ObservedTime   string
	Service        string
	Namespace      string
	Severity       string
	SeverityNumber int32
	Body           string
	TraceID        string
	SpanID         string
	Flags          uint32
	ScopeName      string
	ScopeVersion   string
	Attributes     map[string]any
}

// LogSearchResult contains paginated log results.
type LogSearchResult struct {
	Logs    []LogRow
	HasMore bool
}

// SearchLogs searches logs with pagination.
func (s *Service) SearchLogs(ctx context.Context, p LogSearchParams) (*LogSearchResult, error) {
	if p.Window == 0 {
		p.Window = 60
	}
	if p.Limit == 0 {
		p.Limit = 100
	}

	p.Namespace, p.TenantID = s.defaults(p.Namespace, p.TenantID)
	logsGlob := s.duck.LogsGlob(p.TenantID, p.Namespace, p.Window)

	var filters []string
	var args []any

	filters = append(filters, fmt.Sprintf(
		`epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE`, p.Window))

	// Service filter
	if len(p.Services) > 0 {
		placeholders := makePlaceholders(len(p.Services))
		filters = append(filters, fmt.Sprintf(`"name=service_name" IN (%s)`, placeholders))
		for _, svc := range p.Services {
			args = append(args, svc)
		}
	}

	// Severity filter
	if len(p.Severity) > 0 {
		placeholders := makePlaceholders(len(p.Severity))
		filters = append(filters, fmt.Sprintf(`"name=severity" IN (%s)`, placeholders))
		for _, sev := range p.Severity {
			args = append(args, sev)
		}
	}

	// Text search
	for _, term := range p.Terms {
		filters = append(filters, `"name=body" ILIKE ?`)
		args = append(args, "%"+term+"%")
	}

	// Exclude
	for _, term := range p.Exclude {
		filters = append(filters, `"name=body" NOT ILIKE ?`)
		args = append(args, "%"+term+"%")
	}

	whereClause := "WHERE " + strings.Join(filters, " AND ")

	q := fmt.Sprintf(`
SELECT
  strftime(epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ') as ts,
  CASE WHEN "name=observed_time_unix_nano" > 0
       THEN strftime(epoch_ms(CAST("name=observed_time_unix_nano"/1000000 AS BIGINT)), '%%Y-%%m-%%dT%%H:%%M:%%SZ')
       ELSE NULL END as observed_ts,
  "name=service_name" as service,
  COALESCE("name=namespace", '') as namespace,
  "name=severity" as severity,
  "name=severity_number" as severity_number,
  "name=body" as body,
  COALESCE("name=trace_id", '') as trace_id,
  "name=span_id" as span_id,
  "name=flags" as flags,
  "name=scope_name" as scope_name,
  "name=scope_version" as scope_version,
  "name=attributes_json" as attributes_json
FROM read_parquet(%s, union_by_name=true)
%s
ORDER BY "name=time_unix_nano" DESC
LIMIT %d OFFSET %d;
`, logsGlob, whereClause, p.Limit+1, p.Offset)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return &LogSearchResult{Logs: []LogRow{}}, nil
	}
	defer rows.Close()

	var logs []LogRow
	for rows.Next() {
		var log LogRow
		var observedTime, severityNum, spanID, flags, scopeName, scopeVersion, attrsJSON any
		if err := rows.Scan(&log.Time, &observedTime, &log.Service, &log.Namespace, &log.Severity, &severityNum,
			&log.Body, &log.TraceID, &spanID, &flags, &scopeName, &scopeVersion, &attrsJSON); err != nil {
			continue
		}
		if observedTime != nil {
			log.ObservedTime = fmt.Sprintf("%v", observedTime)
		}
		if severityNum != nil {
			if num, ok := severityNum.(int64); ok {
				log.SeverityNumber = int32(num)
			}
		}
		if spanID != nil {
			log.SpanID = fmt.Sprintf("%v", spanID)
		}
		if flags != nil {
			if f, ok := flags.(int64); ok {
				log.Flags = uint32(f)
			}
		}
		if scopeName != nil {
			log.ScopeName = fmt.Sprintf("%v", scopeName)
		}
		if scopeVersion != nil {
			log.ScopeVersion = fmt.Sprintf("%v", scopeVersion)
		}
		if attrsJSON != nil {
			if b, ok := attrsJSON.([]byte); ok && len(b) > 0 {
				_ = json.Unmarshal(b, &log.Attributes)
			}
		}
		logs = append(logs, log)
	}

	hasMore := len(logs) > p.Limit
	if hasMore {
		logs = logs[:p.Limit]
	}

	return &LogSearchResult{Logs: logs, HasMore: hasMore}, nil
}

// ServiceTrends returns per-service sparkline data.
func (s *Service) ServiceTrends(ctx context.Context, window int, namespace, tenantID string) (map[string][]int64, error) {
	out := make(map[string][]int64)

	namespace, tenantID = s.defaults(namespace, tenantID)
	spansGlob := s.duck.SpansGlob(tenantID, namespace, window)

	q := fmt.Sprintf(`
SELECT
  "name=service_name" as service,
  time_bucket(INTERVAL '5 minutes', epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT))) as bucket,
  COUNT(*) as cnt
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
GROUP BY "name=service_name", bucket
ORDER BY service, bucket ASC;
`, spansGlob, window)

	rows, err := s.duck.DB.QueryContext(ctx, q)
	if err != nil {
		return out, nil
	}
	defer rows.Close()

	for rows.Next() {
		var service string
		var bucket any
		var cnt int64
		if err := rows.Scan(&service, &bucket, &cnt); err != nil {
			continue
		}
		out[service] = append(out[service], cnt)
	}

	return out, nil
}

// MetricSummary represents aggregated metric info.
type MetricSummary struct {
	Name     string
	Type     string
	Count    int64
	Avg      float64
	Min      float64
	Max      float64
	Services []string
	Trend    []float64
}

// MetricsResult contains metric summaries.
type MetricsResult struct {
	Metrics []MetricSummary
}

// MetricsParams contains metric search parameters.
type MetricsParams struct {
	Names     []string
	Services  []string
	Types     []string
	Terms     []string
	Window    int
	Namespace string
	TenantID  string
}

// Metrics returns aggregated metrics with sparklines.
func (s *Service) Metrics(ctx context.Context, p MetricsParams) (*MetricsResult, error) {
	if p.Window == 0 {
		p.Window = 60
	}

	p.Namespace, p.TenantID = s.defaults(p.Namespace, p.TenantID)
	metricsGlob := s.duck.MetricsGlob(p.TenantID, p.Namespace, p.Window)

	var filters []string
	var args []any

	filters = append(filters, fmt.Sprintf(
		`epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE`, p.Window))

	// Name filter
	for _, n := range p.Names {
		if containsWildcard(n) {
			filters = append(filters, `"name=name" ILIKE ?`)
			args = append(args, wildcardToLike(n))
		} else {
			filters = append(filters, `"name=name" = ?`)
			args = append(args, n)
		}
	}

	// Service filter
	if len(p.Services) > 0 {
		placeholders := makePlaceholders(len(p.Services))
		filters = append(filters, fmt.Sprintf(`"name=service_name" IN (%s)`, placeholders))
		for _, svc := range p.Services {
			args = append(args, svc)
		}
	}

	// Type filter
	if len(p.Types) > 0 {
		placeholders := makePlaceholders(len(p.Types))
		filters = append(filters, fmt.Sprintf(`"name=mtype" IN (%s)`, placeholders))
		for _, t := range p.Types {
			args = append(args, t)
		}
	}

	// Text search
	for _, term := range p.Terms {
		filters = append(filters, `"name=name" ILIKE ?`)
		args = append(args, "%"+term+"%")
	}

	whereClause := "WHERE " + strings.Join(filters, " AND ")

	q := fmt.Sprintf(`
SELECT
  "name=name" as metric_name,
  COALESCE("name=mtype", 'unknown') as mtype,
  COUNT(*) as cnt,
  AVG(COALESCE("name=value", 0)) as avg_val,
  MIN(COALESCE("name=value", 0)) as min_val,
  MAX(COALESCE("name=value", 0)) as max_val,
  LIST(DISTINCT "name=service_name") as services
FROM read_parquet(%s, union_by_name=true)
%s
GROUP BY "name=name", "name=mtype"
ORDER BY metric_name
LIMIT 100;
`, metricsGlob, whereClause)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return &MetricsResult{Metrics: []MetricSummary{}}, nil
	}
	defer rows.Close()

	var metrics []MetricSummary
	var metricNames []string

	for rows.Next() {
		var m MetricSummary
		var services any
		if err := rows.Scan(&m.Name, &m.Type, &m.Count, &m.Avg, &m.Min, &m.Max, &services); err != nil {
			continue
		}
		m.Services = parseServiceList(services)
		metricNames = append(metricNames, m.Name)
		metrics = append(metrics, m)
	}

	// Get sparklines
	if len(metricNames) > 0 {
		sparklines := s.metricSparklines(ctx, metricNames, p.Window, p.Namespace, p.TenantID)
		for i := range metrics {
			if trend, ok := sparklines[metrics[i].Name]; ok {
				metrics[i].Trend = trend
			}
		}
	}

	return &MetricsResult{Metrics: metrics}, nil
}

func (s *Service) metricSparklines(ctx context.Context, names []string, window int, namespace, tenantID string) map[string][]float64 {
	out := make(map[string][]float64)

	metricsGlob := s.duck.MetricsGlob(tenantID, namespace, window)
	bucketMins := window / 12
	if bucketMins < 1 {
		bucketMins = 1
	}

	placeholders := makePlaceholders(len(names))
	var args []any
	for _, n := range names {
		args = append(args, n)
	}

	q := fmt.Sprintf(`
SELECT
  "name=name" as metric_name,
  time_bucket(INTERVAL '%d minutes', epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT))) as bucket,
  AVG(COALESCE("name=value", 0)) as avg_val
FROM read_parquet(%s, union_by_name=true)
WHERE epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  AND "name=name" IN (%s)
GROUP BY "name=name", bucket
ORDER BY metric_name, bucket ASC;
`, bucketMins, metricsGlob, window, placeholders)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var bucket any
		var avg float64
		if err := rows.Scan(&name, &bucket, &avg); err != nil {
			continue
		}
		out[name] = append(out[name], avg)
	}

	return out
}

// Namespaces discovers namespaces from filesystem.
func (s *Service) Namespaces(lakeDir, tenantID string) []string {
	if tenantID == "" {
		tenantID = s.cfg.TenantID.String()
	}

	var namespaces []string
	seen := make(map[string]bool)

	basePath := filepath.Join(lakeDir, "spans", fmt.Sprintf("tenant=%s", tenantID))
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return namespaces
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "namespace=") {
			ns := strings.TrimPrefix(entry.Name(), "namespace=")
			if ns != "" && !seen[ns] {
				seen[ns] = true
				namespaces = append(namespaces, ns)
			}
		}
	}

	sort.Strings(namespaces)
	return namespaces
}

// Helper functions

func makePlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	result := "?"
	for i := 1; i < n; i++ {
		result += ", ?"
	}
	return result
}

func containsWildcard(s string) bool {
	return strings.Contains(s, "*") || strings.Contains(s, "?")
}

func wildcardToLike(s string) string {
	s = strings.ReplaceAll(s, "*", "%")
	s = strings.ReplaceAll(s, "?", "_")
	return s
}

func parseServiceList(v any) []string {
	if v == nil {
		return nil
	}
	if list, ok := v.([]any); ok {
		var out []string
		for _, item := range list {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
