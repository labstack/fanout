package main

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

type metricSample struct {
	Name   string
	Labels map[string]string
	Value  float64
}

// metricSnapshot retains both aggregate totals for legacy report fields and
// individual bounded-label series for operation-level distributions.
type metricSnapshot struct {
	Totals  map[string]float64
	Samples []metricSample
}

type distributionReport struct {
	Count  int64   `json:"count"`
	MeanMs float64 `json:"mean_ms"`
	P50Ms  float64 `json:"p50_ms"`
	P95Ms  float64 `json:"p95_ms"`
	P99Ms  float64 `json:"p99_ms"`
}

type backgroundOperationReport struct {
	DurationMs distributionReport `json:"duration_ms"`
	Outcomes   map[string]float64 `json:"outcomes,omitempty"`
}

type rollupReport struct {
	Enabled                   bool               `json:"enabled"`
	WatermarkTimestampSeconds float64            `json:"watermark_timestamp_seconds"`
	SourceTimestampSeconds    float64            `json:"source_timestamp_seconds"`
	LagSeconds                float64            `json:"lag_seconds"`
	BacklogChunks             float64            `json:"backlog_chunks"`
	RowsDelta                 float64            `json:"rows_delta"`
	DurationMs                distributionReport `json:"duration_ms"`
	Outcomes                  map[string]float64 `json:"outcomes,omitempty"`
}

// scrapeMetrics fetches a Prometheus text endpoint without retaining the URL
// or bearer token in benchmark evidence.
func scrapeMetrics(url, token string) (*metricSnapshot, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil) //nolint:noctx // short-lived CLI scrape
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return parseMetricSnapshot(resp.Body)
}

func parseMetricSnapshot(r io.Reader) (*metricSnapshot, error) {
	snapshot := &metricSnapshot{Totals: make(map[string]float64)}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		head, rawValue, ok := splitMetricLine(line)
		if !ok {
			return nil, fmt.Errorf("metrics line %d: missing sample value", lineNumber)
		}
		name, labels, err := parseMetricHead(head)
		if err != nil {
			return nil, fmt.Errorf("metrics line %d: %w", lineNumber, err)
		}
		value, err := strconv.ParseFloat(rawValue, 64)
		if err != nil {
			return nil, fmt.Errorf("metrics line %d: parse value: %w", lineNumber, err)
		}
		snapshot.Totals[name] += value
		snapshot.Samples = append(snapshot.Samples, metricSample{Name: name, Labels: labels, Value: value})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func splitMetricLine(line string) (head, value string, ok bool) {
	depth := 0
	quoted := false
	escaped := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if quoted {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				quoted = false
			}
			continue
		}
		switch ch {
		case '"':
			quoted = true
		case '{':
			depth++
		case '}':
			depth--
		case ' ', '\t':
			if depth == 0 {
				fields := strings.Fields(line[i:])
				if len(fields) == 0 {
					return "", "", false
				}
				return line[:i], fields[0], true
			}
		}
	}
	return "", "", false
}

func parseMetricHead(head string) (string, map[string]string, error) {
	open := strings.IndexByte(head, '{')
	if open < 0 {
		if head == "" {
			return "", nil, fmt.Errorf("empty metric name")
		}
		return head, nil, nil
	}
	if open == 0 || !strings.HasSuffix(head, "}") {
		return "", nil, fmt.Errorf("malformed metric labels")
	}
	labels, err := parsePrometheusLabels(head[open+1 : len(head)-1])
	if err != nil {
		return "", nil, err
	}
	return head[:open], labels, nil
}

func parsePrometheusLabels(raw string) (map[string]string, error) {
	labels := make(map[string]string)
	for i := 0; i < len(raw); {
		for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t' || raw[i] == ',') {
			i++
		}
		if i == len(raw) {
			break
		}
		keyStart := i
		for i < len(raw) && raw[i] != '=' {
			i++
		}
		if i == len(raw) {
			return nil, fmt.Errorf("malformed label assignment")
		}
		key := strings.TrimSpace(raw[keyStart:i])
		i++
		for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t') {
			i++
		}
		if key == "" || i == len(raw) || raw[i] != '"' {
			return nil, fmt.Errorf("malformed label value")
		}
		valueStart := i
		i++
		escaped := false
		for i < len(raw) {
			if escaped {
				escaped = false
				i++
				continue
			}
			if raw[i] == '\\' {
				escaped = true
				i++
				continue
			}
			if raw[i] == '"' {
				break
			}
			i++
		}
		if i == len(raw) {
			return nil, fmt.Errorf("unterminated label value")
		}
		value, err := strconv.Unquote(raw[valueStart : i+1])
		if err != nil {
			return nil, fmt.Errorf("parse label %q: %w", key, err)
		}
		labels[key] = value
		i++
		for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t') {
			i++
		}
		if i < len(raw) && raw[i] != ',' {
			return nil, fmt.Errorf("malformed separator after label %q", key)
		}
	}
	return labels, nil
}

func (s *metricSnapshot) total(name string) float64 {
	if s == nil {
		return 0
	}
	return s.Totals[name]
}

func (s *metricSnapshot) filteredTotal(name string, labels map[string]string) float64 {
	if s == nil {
		return 0
	}
	if len(labels) == 0 {
		return s.total(name)
	}
	var total float64
	for _, sample := range s.Samples {
		if sample.Name == name && hasLabels(sample.Labels, labels) {
			total += sample.Value
		}
	}
	return total
}

func (s *metricSnapshot) labelValues(name, label string, filters map[string]string) []string {
	if s == nil {
		return nil
	}
	values := make(map[string]struct{})
	for _, sample := range s.Samples {
		if sample.Name != name || !hasLabels(sample.Labels, filters) {
			continue
		}
		if value, ok := sample.Labels[label]; ok {
			values[value] = struct{}{}
		}
	}
	return sortedKeys(values)
}

func hasLabels(actual, required map[string]string) bool {
	for key, value := range required {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func serverDelta(base, final *metricSnapshot, durationSeconds float64) *serverReport {
	if final == nil {
		return nil
	}
	baselineAvailable := base != nil
	if base == nil {
		base = &metricSnapshot{Totals: make(map[string]float64)}
	}
	delta := func(name string) float64 { return final.total(name) - base.total(name) }
	rate := func(value float64) float64 {
		if durationSeconds <= 0 {
			return 0
		}
		return round2(value / durationSeconds)
	}
	lakePartitionsStart := base.total("fanout_lake_partitions")
	lakeSizeStart := base.total("fanout_lake_size_bytes")
	cpuSeconds := delta("process_cpu_seconds_total")
	allocBytes := delta("go_memstats_alloc_bytes_total")
	return &serverReport{
		BaselineAvailable:     baselineAvailable,
		IngestRowsStart:       base.total("fanout_ingest_rows_total"),
		IngestRowsEnd:         final.total("fanout_ingest_rows_total"),
		IngestRowsDelta:       delta("fanout_ingest_rows_total"),
		RowsDroppedStart:      base.total("fanout_rows_dropped_total"),
		RowsDroppedEnd:        final.total("fanout_rows_dropped_total"),
		RowsDroppedDelta:      delta("fanout_rows_dropped_total"),
		LakePartitionsStart:   lakePartitionsStart,
		LakePartitions:        final.total("fanout_lake_partitions"),
		LakePartitionsDelta:   final.total("fanout_lake_partitions") - lakePartitionsStart,
		LakeSizeBytesStart:    lakeSizeStart,
		LakeSizeBytes:         final.total("fanout_lake_size_bytes"),
		LakeSizeBytesDelta:    final.total("fanout_lake_size_bytes") - lakeSizeStart,
		LakeGrowthBytesPerSec: rate(final.total("fanout_lake_size_bytes") - lakeSizeStart),
		IngestQueueDepth:      final.total("fanout_ingest_queue_depth"),
		AvgRollupMs:           averageDurationMs(base, final, "fanout_rollup_duration_seconds"),
		AvgFlushMs:            averageDurationMs(base, final, "fanout_flush_duration_seconds"),
		AvgQueryMs:            averageDurationMs(base, final, "fanout_query_duration_seconds"),
		CPUSecondsStart:       round4(base.total("process_cpu_seconds_total")),
		CPUSecondsEnd:         round4(final.total("process_cpu_seconds_total")),
		CPUSecondsDelta:       round4(cpuSeconds),
		CPUCores:              perSecond(cpuSeconds, durationSeconds),
		RSSBytes:              final.total("process_resident_memory_bytes"),
		HeapAllocBytes:        final.total("go_memstats_heap_alloc_bytes"),
		AllocBytesStart:       base.total("go_memstats_alloc_bytes_total"),
		AllocBytesEnd:         final.total("go_memstats_alloc_bytes_total"),
		AllocBytesDelta:       allocBytes,
		AllocBytesPerSec:      rate(allocBytes),
		GCPauseSecondsStart:   round4(base.total("go_gc_duration_seconds_sum")),
		GCPauseSecondsEnd:     round4(final.total("go_gc_duration_seconds_sum")),
		GCPauseSecondsDelta:   round4(delta("go_gc_duration_seconds_sum")),
		WriteGateWaitMs:       histogramReports(base, final, "fanout_write_gate_wait_seconds", "operation"),
		WriteGateHoldMs:       histogramReports(base, final, "fanout_write_gate_hold_seconds", "operation"),
		DuckLakeOperations:    backgroundReports(base, final),
		Rollups:               rollupReports(base, final),
	}
}

func averageDurationMs(base, final *metricSnapshot, name string) float64 {
	count := final.total(name+"_count") - base.total(name+"_count")
	if count <= 0 {
		return 0
	}
	sum := final.total(name+"_sum") - base.total(name+"_sum")
	return round2(sum / count * 1000)
}

func perSecond(value, durationSeconds float64) float64 {
	if durationSeconds <= 0 {
		return 0
	}
	return round4(value / durationSeconds)
}

func histogramReports(base, final *metricSnapshot, name, groupLabel string) map[string]distributionReport {
	groups := unionStrings(
		base.labelValues(name+"_count", groupLabel, nil),
		final.labelValues(name+"_count", groupLabel, nil),
	)
	if len(groups) == 0 {
		return nil
	}
	reports := make(map[string]distributionReport, len(groups))
	for _, group := range groups {
		reports[group] = histogramDelta(base, final, name, map[string]string{groupLabel: group})
	}
	return reports
}

func histogramDelta(base, final *metricSnapshot, name string, filters map[string]string) distributionReport {
	countValue := final.filteredTotal(name+"_count", filters) - base.filteredTotal(name+"_count", filters)
	if countValue <= 0 {
		return distributionReport{}
	}
	count := int64(math.Round(countValue))
	sum := final.filteredTotal(name+"_sum", filters) - base.filteredTotal(name+"_sum", filters)
	bounds := unionStrings(
		base.labelValues(name+"_bucket", "le", filters),
		final.labelValues(name+"_bucket", "le", filters),
	)
	type histogramBound struct {
		raw   string
		value float64
	}
	finiteBounds := make([]histogramBound, 0, len(bounds))
	for _, raw := range bounds {
		if raw == "+Inf" || raw == "Inf" {
			continue
		}
		bound, err := strconv.ParseFloat(raw, 64)
		if err == nil {
			finiteBounds = append(finiteBounds, histogramBound{raw: raw, value: bound})
		}
	}
	sort.Slice(finiteBounds, func(i, j int) bool { return finiteBounds[i].value < finiteBounds[j].value })
	quantile := func(q float64) float64 {
		if len(finiteBounds) == 0 {
			return 0
		}
		target := math.Ceil(q * countValue)
		for _, bound := range finiteBounds {
			bucketFilters := copyLabels(filters)
			bucketFilters["le"] = bound.raw
			cumulative := final.filteredTotal(name+"_bucket", bucketFilters) - base.filteredTotal(name+"_bucket", bucketFilters)
			if cumulative >= target {
				return round2(bound.value * 1000)
			}
		}
		return round2(finiteBounds[len(finiteBounds)-1].value * 1000)
	}
	return distributionReport{
		Count:  count,
		MeanMs: round2(sum / countValue * 1000),
		P50Ms:  quantile(0.50),
		P95Ms:  quantile(0.95),
		P99Ms:  quantile(0.99),
	}
}

func backgroundReports(base, final *metricSnapshot) map[string]backgroundOperationReport {
	operations := unionStrings(
		base.labelValues("fanout_ducklake_operation_total", "operation", nil),
		final.labelValues("fanout_ducklake_operation_total", "operation", nil),
		base.labelValues("fanout_ducklake_operation_duration_seconds_count", "operation", nil),
		final.labelValues("fanout_ducklake_operation_duration_seconds_count", "operation", nil),
	)
	if len(operations) == 0 {
		return nil
	}
	reports := make(map[string]backgroundOperationReport, len(operations))
	for _, operation := range operations {
		filters := map[string]string{"operation": operation}
		reports[operation] = backgroundOperationReport{
			DurationMs: histogramDelta(base, final, "fanout_ducklake_operation_duration_seconds", filters),
			Outcomes:   counterOutcomes(base, final, "fanout_ducklake_operation_total", "result", filters),
		}
	}
	return reports
}

func rollupReports(base, final *metricSnapshot) map[string]rollupReport {
	rollups := unionStrings(
		base.labelValues("fanout_rollup_enabled", "rollup", nil),
		final.labelValues("fanout_rollup_enabled", "rollup", nil),
		base.labelValues("fanout_rollup_component_total", "rollup", nil),
		final.labelValues("fanout_rollup_component_total", "rollup", nil),
	)
	if len(rollups) == 0 {
		return nil
	}
	reports := make(map[string]rollupReport, len(rollups))
	for _, rollup := range rollups {
		filters := map[string]string{"rollup": rollup}
		reports[rollup] = rollupReport{
			Enabled:                   final.filteredTotal("fanout_rollup_enabled", filters) > 0,
			WatermarkTimestampSeconds: final.filteredTotal("fanout_rollup_watermark_timestamp_seconds", filters),
			SourceTimestampSeconds:    final.filteredTotal("fanout_rollup_source_timestamp_seconds", filters),
			LagSeconds:                final.filteredTotal("fanout_rollup_lag_seconds", filters),
			BacklogChunks:             final.filteredTotal("fanout_rollup_backlog_chunks", filters),
			RowsDelta:                 final.filteredTotal("fanout_rollup_component_rows_total", filters) - base.filteredTotal("fanout_rollup_component_rows_total", filters),
			DurationMs:                histogramDelta(base, final, "fanout_rollup_component_duration_seconds", filters),
			Outcomes:                  counterOutcomes(base, final, "fanout_rollup_component_total", "result", filters),
		}
	}
	return reports
}

func counterOutcomes(base, final *metricSnapshot, name, outcomeLabel string, filters map[string]string) map[string]float64 {
	outcomes := unionStrings(
		base.labelValues(name, outcomeLabel, filters),
		final.labelValues(name, outcomeLabel, filters),
	)
	if len(outcomes) == 0 {
		return nil
	}
	deltas := make(map[string]float64, len(outcomes))
	for _, outcome := range outcomes {
		outcomeFilters := copyLabels(filters)
		outcomeFilters[outcomeLabel] = outcome
		deltas[outcome] = final.filteredTotal(name, outcomeFilters) - base.filteredTotal(name, outcomeFilters)
	}
	return deltas
}

func copyLabels(labels map[string]string) map[string]string {
	copy := make(map[string]string, len(labels)+1)
	for key, value := range labels {
		copy[key] = value
	}
	return copy
}

func unionStrings(groups ...[]string) []string {
	values := make(map[string]struct{})
	for _, group := range groups {
		for _, value := range group {
			values[value] = struct{}{}
		}
	}
	return sortedKeys(values)
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func round4(value float64) float64 {
	value = math.Round(value*10000) / 10000
	if value == 0 {
		return 0
	}
	return value
}
