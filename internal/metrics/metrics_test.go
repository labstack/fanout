package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestDataPlaneInstrumentationDisabledIsNoop(t *testing.T) {
	original := dataPlaneInstrumentation
	dataPlaneInstrumentation = "disabled"
	t.Cleanup(func() { dataPlaneInstrumentation = original })

	WriteGateWait.Reset()
	RollupComponentTotal.Reset()
	RollupEnabled.Reset()
	DuckLakeOperationTotal.Reset()

	if DataPlaneInstrumentationEnabled() {
		t.Fatal("instrumentation unexpectedly enabled")
	}
	timer := StartDataPlaneTimer()
	time.Sleep(time.Millisecond)
	if got := timer.Seconds(); got != 0 {
		t.Fatalf("disabled timer = %v, want 0", got)
	}
	RecordWriteGate("ingest_spans", 1, 1)
	RecordRollupComponent(RollupService, RollupSuccess, 1, 1)
	UpdateRollupProgress(RollupService, true, 1, 2, 1)
	RecordDuckLakeOperation(DuckLakeMerge, DuckLakeSuccess, 1)

	if got := histogramSampleCount(t, "fanout_write_gate_wait_seconds", "operation", "ingest_spans"); got != 0 {
		t.Fatalf("disabled write-gate samples = %d, want 0", got)
	}
	if got := testutil.ToFloat64(RollupComponentTotal.WithLabelValues("service", "success")); got != 0 {
		t.Fatalf("disabled rollup count = %v, want 0", got)
	}
	if got := testutil.ToFloat64(RollupEnabled.WithLabelValues("service")); got != 0 {
		t.Fatalf("disabled rollup gauge = %v, want 0", got)
	}
	if got := testutil.ToFloat64(DuckLakeOperationTotal.WithLabelValues("merge", "success")); got != 0 {
		t.Fatalf("disabled DuckLake count = %v, want 0", got)
	}
}

func TestRecordIngest(t *testing.T) {
	// Reset metrics for test isolation
	IngestTotal.Reset()

	RecordIngest("spans", 100)
	RecordIngest("spans", 50)
	RecordIngest("logs", 25)

	// Check spans counter
	spansCount := testutil.ToFloat64(IngestTotal.WithLabelValues("spans"))
	if spansCount != 150 {
		t.Errorf("IngestTotal[spans] = %f, want 150", spansCount)
	}

	// Check logs counter
	logsCount := testutil.ToFloat64(IngestTotal.WithLabelValues("logs"))
	if logsCount != 25 {
		t.Errorf("IngestTotal[logs] = %f, want 25", logsCount)
	}
}

func TestRecordFlush(t *testing.T) {
	// Reset metrics
	FlushTotal.Reset()
	FlushBytes.Reset()

	RecordFlush("spans", 1024, 0.5)
	RecordFlush("spans", 2048, 0.3)

	// Check counter incremented
	flushCount := testutil.ToFloat64(FlushTotal.WithLabelValues("spans"))
	if flushCount != 2 {
		t.Errorf("FlushTotal[spans] = %f, want 2", flushCount)
	}

	// Check bytes accumulated
	bytesCount := testutil.ToFloat64(FlushBytes.WithLabelValues("spans"))
	if bytesCount != 3072 {
		t.Errorf("FlushBytes[spans] = %f, want 3072", bytesCount)
	}
}

func TestRecordQuery(t *testing.T) {
	// Reset metrics
	QueryTotal.Reset()

	RecordQuery("/services", "ok", 0.1)
	RecordQuery("/services", "ok", 0.2)
	RecordQuery("/services", "error", 0.5)

	// Check success queries
	okCount := testutil.ToFloat64(QueryTotal.WithLabelValues("/services", "ok"))
	if okCount != 2 {
		t.Errorf("QueryTotal[/services,ok] = %f, want 2", okCount)
	}

	// Check error queries
	errCount := testutil.ToFloat64(QueryTotal.WithLabelValues("/services", "error"))
	if errCount != 1 {
		t.Errorf("QueryTotal[/services,error] = %f, want 1", errCount)
	}
}

func TestRecordRollup(t *testing.T) {
	totalBefore := testutil.ToFloat64(RollupTotal)
	rowsBefore := testutil.ToFloat64(RollupRows)
	RollupLastSuccess.Set(123)
	RecordRollup(1000, 2.5, true)
	if got := testutil.ToFloat64(RollupTotal); got != totalBefore+1 {
		t.Errorf("RollupTotal = %f, want %f", got, totalBefore+1)
	}
	if got := testutil.ToFloat64(RollupRows); got != rowsBefore+1000 {
		t.Errorf("RollupRows = %f, want %f", got, rowsBefore+1000)
	}
	if got := testutil.ToFloat64(RollupLastSuccess); got <= 123 {
		t.Errorf("RollupLastSuccess = %f, want current timestamp", got)
	}

	RollupLastSuccess.Set(456)
	RecordRollup(10, 0.1, false)
	if got := testutil.ToFloat64(RollupLastSuccess); got != 456 {
		t.Errorf("failed cycle advanced RollupLastSuccess to %f", got)
	}
}

func TestRecordRollupComponentAndProgress(t *testing.T) {
	RollupComponentTotal.Reset()
	RollupComponentRows.Reset()
	RollupEnabled.Reset()
	RollupWatermark.Reset()
	RollupSourceMax.Reset()
	RollupLag.Reset()
	RollupBacklogChunks.Reset()

	RecordRollupComponent(RollupService, RollupSuccess, 12, 0.25)
	RecordRollupComponent(RollupEndpoint, RollupDisabled, 0, 0.01)
	if got := testutil.ToFloat64(RollupComponentTotal.WithLabelValues("service", "success")); got != 1 {
		t.Errorf("service success count = %f, want 1", got)
	}
	if got := testutil.ToFloat64(RollupComponentRows.WithLabelValues("service")); got != 12 {
		t.Errorf("service rows = %f, want 12", got)
	}
	if got := testutil.ToFloat64(RollupComponentTotal.WithLabelValues("endpoint", "disabled")); got != 1 {
		t.Errorf("endpoint disabled count = %f, want 1", got)
	}

	UpdateRollupProgress(RollupService, true, 10_000_000_000, 13_500_000_000, 2)
	if got := testutil.ToFloat64(RollupEnabled.WithLabelValues("service")); got != 1 {
		t.Errorf("service enabled = %f, want 1", got)
	}
	if got := testutil.ToFloat64(RollupWatermark.WithLabelValues("service")); got != 10 {
		t.Errorf("service watermark = %f, want 10", got)
	}
	if got := testutil.ToFloat64(RollupSourceMax.WithLabelValues("service")); got != 13.5 {
		t.Errorf("service source max = %f, want 13.5", got)
	}
	if got := testutil.ToFloat64(RollupLag.WithLabelValues("service")); got != 3.5 {
		t.Errorf("service lag = %f, want 3.5", got)
	}
	if got := testutil.ToFloat64(RollupBacklogChunks.WithLabelValues("service")); got != 2 {
		t.Errorf("service backlog chunks = %f, want 2", got)
	}

	UpdateRollupProgress(RollupEndpoint, false, 0, 0, 0)
	if got := testutil.ToFloat64(RollupEnabled.WithLabelValues("endpoint")); got != 0 {
		t.Errorf("endpoint enabled = %f, want 0", got)
	}
}

func TestRecordDuckLakeOperationOutcomes(t *testing.T) {
	DuckLakeOperationTotal.Reset()
	DuckLakeOperationDuration.Reset()

	RecordDuckLakeOperation(DuckLakeMerge, DuckLakeSuccess, 0.2)
	RecordDuckLakeOperation(DuckLakeMerge, DuckLakeThrottled, 0)
	RecordDuckLakeOperation(DuckLakeMerge, DuckLakeDisabled, 0)
	RecordDuckLakeOperation(DuckLakeMaintenance, DuckLakeError, 1.5)

	for _, tc := range []struct {
		operation string
		result    string
	}{
		{"merge", "success"},
		{"merge", "throttled"},
		{"merge", "disabled"},
		{"maintenance", "error"},
	} {
		if got := testutil.ToFloat64(DuckLakeOperationTotal.WithLabelValues(tc.operation, tc.result)); got != 1 {
			t.Errorf("DuckLakeOperationTotal[%s,%s] = %f, want 1", tc.operation, tc.result, got)
		}
	}
	if got := histogramSampleCount(t, "fanout_ducklake_operation_duration_seconds", "operation", "merge"); got != 1 {
		t.Errorf("merge duration samples = %d, want 1 (skipped outcomes must not distort duration)", got)
	}
	if got := histogramSampleCount(t, "fanout_ducklake_operation_duration_seconds", "operation", "maintenance"); got != 1 {
		t.Errorf("maintenance duration samples = %d, want 1", got)
	}
}

func TestBoundedMetricLabelsRejectUnknownValues(t *testing.T) {
	for name, call := range map[string]func(){
		"rollup component": func() { RecordRollupComponent(RollupComponent("tenant"), RollupSuccess, 0, 0) },
		"rollup result":    func() { RecordRollupComponent(RollupService, RollupResult("unknown"), 0, 0) },
		"lake operation":   func() { RecordDuckLakeOperation(DuckLakeOperation("query"), DuckLakeSuccess, 0) },
		"lake result":      func() { RecordDuckLakeOperation(DuckLakeMerge, DuckLakeResult("unknown"), 0) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("unknown bounded label did not panic")
				}
			}()
			call()
		})
	}
}

func TestUpdateLakeStats(t *testing.T) {
	// Reset metrics
	LakeSize.Reset()
	LakePartitions.Reset()

	UpdateLakeStats("spans", 1024*1024, 10)
	UpdateLakeStats("logs", 512*1024, 5)

	// Check spans size
	spansSize := testutil.ToFloat64(LakeSize.WithLabelValues("spans"))
	if spansSize != 1024*1024 {
		t.Errorf("LakeSize[spans] = %f, want %d", spansSize, 1024*1024)
	}

	// Check spans partitions
	spansPartitions := testutil.ToFloat64(LakePartitions.WithLabelValues("spans"))
	if spansPartitions != 10 {
		t.Errorf("LakePartitions[spans] = %f, want 10", spansPartitions)
	}

	// Check logs size
	logsSize := testutil.ToFloat64(LakeSize.WithLabelValues("logs"))
	if logsSize != 512*1024 {
		t.Errorf("LakeSize[logs] = %f, want %d", logsSize, 512*1024)
	}
}

func TestUpdateQueueDepth(t *testing.T) {
	// Reset metrics
	IngestQueueDepth.Reset()

	UpdateQueueDepth("spans", 100)
	UpdateQueueDepth("logs", 50)

	spansDepth := testutil.ToFloat64(IngestQueueDepth.WithLabelValues("spans"))
	if spansDepth != 100 {
		t.Errorf("IngestQueueDepth[spans] = %f, want 100", spansDepth)
	}

	logsDepth := testutil.ToFloat64(IngestQueueDepth.WithLabelValues("logs"))
	if logsDepth != 50 {
		t.Errorf("IngestQueueDepth[logs] = %f, want 50", logsDepth)
	}

	// Test updating to new value (gauge should overwrite)
	UpdateQueueDepth("spans", 200)
	spansDepth = testutil.ToFloat64(IngestQueueDepth.WithLabelValues("spans"))
	if spansDepth != 200 {
		t.Errorf("IngestQueueDepth[spans] after update = %f, want 200", spansDepth)
	}
}

func TestMetricVariables(t *testing.T) {
	// Verify all metric variables are initialized (not nil)
	if IngestTotal == nil {
		t.Error("IngestTotal is nil")
	}
	if IngestQueueDepth == nil {
		t.Error("IngestQueueDepth is nil")
	}
	if FlushTotal == nil {
		t.Error("FlushTotal is nil")
	}
	if FlushBytes == nil {
		t.Error("FlushBytes is nil")
	}
	if FlushDuration == nil {
		t.Error("FlushDuration is nil")
	}
	if QueryTotal == nil {
		t.Error("QueryTotal is nil")
	}
	if QueryDuration == nil {
		t.Error("QueryDuration is nil")
	}
	if RollupDuration == nil {
		t.Error("RollupDuration is nil")
	}
	if RollupRows == nil {
		t.Error("RollupRows is nil")
	}
	if RollupLastSuccess == nil {
		t.Error("RollupLastSuccess is nil")
	}
	if RollupComponentTotal == nil || RollupComponentDuration == nil || RollupComponentRows == nil {
		t.Error("rollup component metrics are nil")
	}
	if RollupEnabled == nil || RollupWatermark == nil || RollupSourceMax == nil || RollupLag == nil || RollupBacklogChunks == nil {
		t.Error("rollup progress metrics are nil")
	}
	if DuckLakeOperationTotal == nil || DuckLakeOperationDuration == nil {
		t.Error("DuckLake operation metrics are nil")
	}
	if LakeSize == nil {
		t.Error("LakeSize is nil")
	}
	if LakePartitions == nil {
		t.Error("LakePartitions is nil")
	}
	if HTTPRequestsTotal == nil {
		t.Error("HTTPRequestsTotal is nil")
	}
	if HTTPRequestDuration == nil {
		t.Error("HTTPRequestDuration is nil")
	}
}

func histogramSampleCount(t *testing.T, metricName, labelName, labelValue string) uint64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != metricName {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == labelName && label.GetValue() == labelValue {
					return metric.GetHistogram().GetSampleCount()
				}
			}
		}
		return 0
	}
	return 0
}
