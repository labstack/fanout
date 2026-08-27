package main

import (
	"math"
	"strings"
	"testing"
)

func TestParseMetricSnapshotPreservesLabelsAndTotals(t *testing.T) {
	snapshot := mustMetricSnapshot(t, `
# HELP fanout_test_total test metric
fanout_test_total{operation="compaction",detail="quoted\" value\\path"} 2
fanout_test_total{operation="maintenance",detail="plain"} 3
`)

	if got := snapshot.total("fanout_test_total"); got != 5 {
		t.Fatalf("total = %v, want 5", got)
	}
	if got := snapshot.filteredTotal("fanout_test_total", map[string]string{"operation": "compaction"}); got != 2 {
		t.Fatalf("merge total = %v, want 2", got)
	}
	if got := snapshot.Samples[0].Labels["detail"]; got != "quoted\" value\\path" {
		t.Fatalf("decoded label = %q", got)
	}
}

func TestServerDeltaReportsOperationAndRuntimeDistributions(t *testing.T) {
	base := mustMetricSnapshot(t, `
fanout_ingest_rows_total{signal="spans"} 10
fanout_ingest_rows_total{signal="logs"} 5
fanout_rows_dropped_total{signal="spans"} 0
fanout_parquet_files{signal="spans"} 2
fanout_parquet_files{signal="logs"} 1
fanout_parquet_size_bytes{signal="spans"} 60
fanout_parquet_size_bytes{signal="logs"} 40
fanout_ingest_queue_depth{signal="spans"} 2
fanout_ingest_queue_depth{signal="logs"} 1
process_cpu_seconds_total 10
process_resident_memory_bytes 400
go_memstats_heap_alloc_bytes 100
go_memstats_alloc_bytes_total 1000
go_gc_duration_seconds_sum 0.1
fanout_rollup_duration_seconds_sum 1
fanout_rollup_duration_seconds_count 2
fanout_flush_duration_seconds_sum{signal="spans"} 0.5
fanout_flush_duration_seconds_count{signal="spans"} 5
fanout_query_duration_seconds_sum{endpoint="overview"} 2
fanout_query_duration_seconds_count{endpoint="overview"} 4
fanout_write_gate_wait_seconds_bucket{operation="ingest_spans",le="0.001"} 1
fanout_write_gate_wait_seconds_bucket{operation="ingest_spans",le="0.010"} 2
fanout_write_gate_wait_seconds_bucket{operation="ingest_spans",le="0.100"} 2
fanout_write_gate_wait_seconds_bucket{operation="ingest_spans",le="+Inf"} 2
fanout_write_gate_wait_seconds_sum{operation="ingest_spans"} 0.012
fanout_write_gate_wait_seconds_count{operation="ingest_spans"} 2
fanout_write_gate_hold_seconds_bucket{operation="ingest_spans",le="0.010"} 1
fanout_write_gate_hold_seconds_bucket{operation="ingest_spans",le="0.100"} 1
fanout_write_gate_hold_seconds_bucket{operation="ingest_spans",le="+Inf"} 1
fanout_write_gate_hold_seconds_sum{operation="ingest_spans"} 0.005
fanout_write_gate_hold_seconds_count{operation="ingest_spans"} 1
fanout_telemetry_operation_duration_seconds_bucket{operation="compaction",le="0.010"} 1
fanout_telemetry_operation_duration_seconds_bucket{operation="compaction",le="0.100"} 1
fanout_telemetry_operation_duration_seconds_bucket{operation="compaction",le="1.000"} 1
fanout_telemetry_operation_duration_seconds_bucket{operation="compaction",le="+Inf"} 1
fanout_telemetry_operation_duration_seconds_sum{operation="compaction"} 0.005
fanout_telemetry_operation_duration_seconds_count{operation="compaction"} 1
fanout_telemetry_operation_total{operation="compaction",result="success"} 1
fanout_telemetry_operation_total{operation="compaction",result="throttled"} 2
fanout_rollup_enabled{rollup="service"} 1
fanout_rollup_watermark_timestamp_seconds{rollup="service"} 100
fanout_rollup_source_timestamp_seconds{rollup="service"} 105
fanout_rollup_lag_seconds{rollup="service"} 5
fanout_rollup_backlog_chunks{rollup="service"} 1
fanout_rollup_component_rows_total{rollup="service"} 10
fanout_rollup_component_duration_seconds_bucket{rollup="service",le="0.010"} 1
fanout_rollup_component_duration_seconds_bucket{rollup="service",le="0.100"} 1
fanout_rollup_component_duration_seconds_bucket{rollup="service",le="+Inf"} 1
fanout_rollup_component_duration_seconds_sum{rollup="service"} 0.008
fanout_rollup_component_duration_seconds_count{rollup="service"} 1
fanout_rollup_component_total{rollup="service",result="success"} 1
`)
	final := mustMetricSnapshot(t, `
fanout_ingest_rows_total{signal="spans"} 30
fanout_ingest_rows_total{signal="logs"} 15
fanout_rows_dropped_total{signal="spans"} 0
fanout_parquet_files{signal="spans"} 3
fanout_parquet_files{signal="logs"} 2
fanout_parquet_size_bytes{signal="spans"} 100
fanout_parquet_size_bytes{signal="logs"} 80
fanout_ingest_queue_depth{signal="spans"} 0
fanout_ingest_queue_depth{signal="logs"} 1
process_cpu_seconds_total 14
process_resident_memory_bytes 512
go_memstats_heap_alloc_bytes 256
go_memstats_alloc_bytes_total 2600
go_gc_duration_seconds_sum 0.3
fanout_rollup_duration_seconds_sum 2.5
fanout_rollup_duration_seconds_count 4
fanout_flush_duration_seconds_sum{signal="spans"} 1.1
fanout_flush_duration_seconds_count{signal="spans"} 8
fanout_query_duration_seconds_sum{endpoint="overview"} 3.5
fanout_query_duration_seconds_count{endpoint="overview"} 7
fanout_write_gate_wait_seconds_bucket{operation="ingest_spans",le="0.001"} 2
fanout_write_gate_wait_seconds_bucket{operation="ingest_spans",le="0.010"} 4
fanout_write_gate_wait_seconds_bucket{operation="ingest_spans",le="0.100"} 5
fanout_write_gate_wait_seconds_bucket{operation="ingest_spans",le="+Inf"} 5
fanout_write_gate_wait_seconds_sum{operation="ingest_spans"} 0.043
fanout_write_gate_wait_seconds_count{operation="ingest_spans"} 5
fanout_write_gate_hold_seconds_bucket{operation="ingest_spans",le="0.010"} 2
fanout_write_gate_hold_seconds_bucket{operation="ingest_spans",le="0.100"} 3
fanout_write_gate_hold_seconds_bucket{operation="ingest_spans",le="+Inf"} 3
fanout_write_gate_hold_seconds_sum{operation="ingest_spans"} 0.035
fanout_write_gate_hold_seconds_count{operation="ingest_spans"} 3
fanout_telemetry_operation_duration_seconds_bucket{operation="compaction",le="0.010"} 1
fanout_telemetry_operation_duration_seconds_bucket{operation="compaction",le="0.100"} 2
fanout_telemetry_operation_duration_seconds_bucket{operation="compaction",le="1.000"} 3
fanout_telemetry_operation_duration_seconds_bucket{operation="compaction",le="+Inf"} 3
fanout_telemetry_operation_duration_seconds_sum{operation="compaction"} 0.125
fanout_telemetry_operation_duration_seconds_count{operation="compaction"} 3
fanout_telemetry_operation_total{operation="compaction",result="success"} 3
fanout_telemetry_operation_total{operation="compaction",result="throttled"} 5
fanout_telemetry_operation_total{operation="compaction",result="error"} 1
fanout_rollup_enabled{rollup="service"} 1
fanout_rollup_watermark_timestamp_seconds{rollup="service"} 200
fanout_rollup_source_timestamp_seconds{rollup="service"} 212
fanout_rollup_lag_seconds{rollup="service"} 12
fanout_rollup_backlog_chunks{rollup="service"} 2
fanout_rollup_component_rows_total{rollup="service"} 25
fanout_rollup_component_duration_seconds_bucket{rollup="service",le="0.010"} 1
fanout_rollup_component_duration_seconds_bucket{rollup="service",le="0.100"} 3
fanout_rollup_component_duration_seconds_bucket{rollup="service",le="+Inf"} 3
fanout_rollup_component_duration_seconds_sum{rollup="service"} 0.108
fanout_rollup_component_duration_seconds_count{rollup="service"} 3
fanout_rollup_component_total{rollup="service",result="success"} 2
fanout_rollup_component_total{rollup="service",result="noop"} 2
`)

	report := serverDelta(base, final, 8)
	if report == nil || !report.BaselineAvailable {
		t.Fatalf("server report = %#v", report)
	}
	assertFloat(t, "ingest rows start", report.IngestRowsStart, 15)
	assertFloat(t, "ingest rows end", report.IngestRowsEnd, 45)
	assertFloat(t, "ingest rows", report.IngestRowsDelta, 30)
	assertFloat(t, "Parquet files delta", report.ParquetFilesDelta, 2)
	assertFloat(t, "Parquet size delta", report.ParquetSizeBytesDelta, 80)
	assertFloat(t, "Parquet growth rate", report.ParquetGrowthBytesPerSec, 10)
	assertFloat(t, "average rollup", report.AvgRollupMs, 750)
	assertFloat(t, "average flush", report.AvgFlushMs, 200)
	assertFloat(t, "average query", report.AvgQueryMs, 500)
	assertFloat(t, "cpu seconds start", report.CPUSecondsStart, 10)
	assertFloat(t, "cpu seconds end", report.CPUSecondsEnd, 14)
	assertFloat(t, "cpu seconds", report.CPUSecondsDelta, 4)
	assertFloat(t, "cpu cores", report.CPUCores, 0.5)
	assertFloat(t, "rss", report.RSSBytes, 512)
	assertFloat(t, "heap", report.HeapAllocBytes, 256)
	assertFloat(t, "allocation start", report.AllocBytesStart, 1000)
	assertFloat(t, "allocation end", report.AllocBytesEnd, 2600)
	assertFloat(t, "allocation delta", report.AllocBytesDelta, 1600)
	assertFloat(t, "allocation rate", report.AllocBytesPerSec, 200)
	assertFloat(t, "gc pause start", report.GCPauseSecondsStart, 0.1)
	assertFloat(t, "gc pause end", report.GCPauseSecondsEnd, 0.3)
	assertFloat(t, "gc pause", report.GCPauseSecondsDelta, 0.2)

	wait := report.WriteGateWaitMs["ingest_spans"]
	if wait.Count != 3 {
		t.Fatalf("wait count = %d, want 3", wait.Count)
	}
	assertFloat(t, "wait mean", wait.MeanMs, 10.33)
	assertFloat(t, "wait p50", wait.P50Ms, 10)
	assertFloat(t, "wait p95", wait.P95Ms, 100)
	assertFloat(t, "hold mean", report.WriteGateHoldMs["ingest_spans"].MeanMs, 15)

	merge := report.TelemetryOperations["compaction"]
	if merge.DurationMs.Count != 2 {
		t.Fatalf("merge duration count = %d, want 2", merge.DurationMs.Count)
	}
	assertFloat(t, "merge duration mean", merge.DurationMs.MeanMs, 60)
	assertFloat(t, "merge p95", merge.DurationMs.P95Ms, 1000)
	assertFloat(t, "merge successes", merge.Outcomes["success"], 2)
	assertFloat(t, "merge throttles", merge.Outcomes["throttled"], 3)
	assertFloat(t, "merge errors", merge.Outcomes["error"], 1)

	service := report.Rollups["service"]
	if !service.Enabled || service.DurationMs.Count != 2 {
		t.Fatalf("service rollup = %#v", service)
	}
	assertFloat(t, "rollup watermark", service.WatermarkTimestampSeconds, 200)
	assertFloat(t, "rollup source", service.SourceTimestampSeconds, 212)
	assertFloat(t, "rollup lag", service.LagSeconds, 12)
	assertFloat(t, "rollup backlog", service.BacklogChunks, 2)
	assertFloat(t, "rollup rows", service.RowsDelta, 15)
	assertFloat(t, "rollup duration", service.DurationMs.MeanMs, 50)
	assertFloat(t, "rollup success", service.Outcomes["success"], 1)
	assertFloat(t, "rollup noop", service.Outcomes["noop"], 2)
}

func TestServerDeltaWithoutBaselineIsExplicit(t *testing.T) {
	final := mustMetricSnapshot(t, "process_cpu_seconds_total 2\n")
	report := serverDelta(nil, final, 10)
	if report == nil || report.BaselineAvailable {
		t.Fatalf("server report = %#v", report)
	}
}

func mustMetricSnapshot(t *testing.T, text string) *metricSnapshot {
	t.Helper()
	snapshot, err := parseMetricSnapshot(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.0001 {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}
