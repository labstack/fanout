package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

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
	// Reset metrics
	RollupTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_fanout_rollup_total",
		Help: "Test total rollup operations",
	})

	RecordRollup(1000, 2.5)
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
