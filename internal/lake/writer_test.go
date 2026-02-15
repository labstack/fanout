package lake

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPartitionPathFormat(t *testing.T) {
	// Test that partition path format matches expected
	ts := time.Date(2024, 6, 15, 14, 30, 0, 0, time.UTC)
	year, month, day := ts.Date()
	hour := ts.Hour()

	path := filepath.Join("/tmp/lake", "spans",
		fmt.Sprintf("year=%04d", year),
		fmt.Sprintf("month=%02d", int(month)),
		fmt.Sprintf("day=%02d", day),
		fmt.Sprintf("hour=%02d", hour),
	)

	expected := "/tmp/lake/spans/year=2024/month=06/day=15/hour=14"
	if path != expected {
		t.Errorf("expected %s, got %s", expected, path)
	}
}

func TestWriterFlush(t *testing.T) {
	// Create temp dir
	lakeDir, err := os.MkdirTemp("", "fanout-writer-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(lakeDir)

	// Create channels
	chSpans := make(chan SpanRow, 100)
	chLogs := make(chan LogRow, 100)
	chMetrics := make(chan MetricRow, 100)

	cfg := config.Config{
		LakeDir:      lakeDir,
		FlushSeconds: 1,
		MaxRows:      10,
		DefaultNS:    "default",
	}
	w := NewWriter(cfg, chSpans, chLogs, chMetrics)

	// Start writer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = w.Run(ctx)
	}()

	// Send some spans
	now := time.Now().UnixNano()
	for i := 0; i < 5; i++ {
		chSpans <- SpanRow{
			TraceID:        "trace-1",
			SpanID:         "span-" + string(rune('0'+i)),
			ServiceName:    "test-service",
			Namespace:      "default",
			Name:           "test-op",
			StartUnixNanos: now,
			EndUnixNanos:   now + 1000000,
			DurationMs:     1.0,
			IngestedAt:     now,
		}
	}

	// Wait for flush
	time.Sleep(2 * time.Second)

	// Check parquet file exists (new path: tenant=*/namespace=*/year=*/...)
	matches, err := filepath.Glob(filepath.Join(lakeDir, "spans", "tenant=*", "namespace=*", "year=*", "month=*", "day=*", "hour=*", "part-*.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Error("expected parquet file to be created")
	}
}

func TestWriterMaxRowsFlush(t *testing.T) {
	// Create temp dir
	lakeDir, err := os.MkdirTemp("", "fanout-maxrows-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(lakeDir)

	// Channels
	chSpans := make(chan SpanRow, 100)
	chLogs := make(chan LogRow, 100)
	chMetrics := make(chan MetricRow, 100)

	cfg := config.Config{
		LakeDir:      lakeDir,
		FlushSeconds: 60, // Long interval
		MaxRows:      5,  // Small max to trigger flush
		DefaultNS:    "default",
	}
	w := NewWriter(cfg, chSpans, chLogs, chMetrics)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = w.Run(ctx)
	}()

	// Send more than MaxRows
	now := time.Now().UnixNano()
	for i := 0; i < 6; i++ {
		chSpans <- SpanRow{
			TraceID:        "trace-2",
			SpanID:         "span-" + string(rune('0'+i)),
			ServiceName:    "test",
			Namespace:      "default",
			Name:           "op",
			StartUnixNanos: now,
			EndUnixNanos:   now + 1000000,
			DurationMs:     1.0,
			IngestedAt:     now,
		}
	}

	// Short wait - MaxRows should trigger immediate flush
	time.Sleep(500 * time.Millisecond)

	matches, err := filepath.Glob(filepath.Join(lakeDir, "spans", "tenant=*", "namespace=*", "year=*", "month=*", "day=*", "hour=*", "part-*.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Error("expected flush due to MaxRows")
	}
}

func TestWriterRetryOnFailure(t *testing.T) {
	// Use a read-only directory to force writeParquet to fail
	lakeDir, err := os.MkdirTemp("", "fanout-retry-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(lakeDir)

	chSpans := make(chan SpanRow, 100)
	chLogs := make(chan LogRow, 100)
	chMetrics := make(chan MetricRow, 100)

	cfg := config.Config{
		LakeDir:      filepath.Join(lakeDir, "readonly"),
		FlushSeconds: 1,
		MaxRows:      5,
		DefaultNS:    "default",
	}
	w := NewWriter(cfg, chSpans, chLogs, chMetrics)

	// Create the spans dir then make it read-only to force write failures
	spansDir := filepath.Join(cfg.LakeDir, "spans", "tenant=", "namespace=default")
	if err := os.MkdirAll(spansDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(spansDir, 0o444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(spansDir, 0o755)

	// Reset error counter
	metrics.FlushErrors.Reset()

	// Manually add rows to buffer and flush
	now := time.Now().UnixNano()
	w.bufSpans = []SpanRow{
		{TraceID: "t1", SpanID: "s1", ServiceName: "svc", Namespace: "default", Name: "op", StartUnixNanos: now, EndUnixNanos: now + 1e6, DurationMs: 1.0, IngestedAt: now},
		{TraceID: "t1", SpanID: "s2", ServiceName: "svc", Namespace: "default", Name: "op", StartUnixNanos: now, EndUnixNanos: now + 1e6, DurationMs: 1.0, IngestedAt: now},
	}

	w.flushLocked()

	// Failed rows should be retained in buffer
	if len(w.bufSpans) != 2 {
		t.Errorf("expected 2 rows retained in buffer after failure, got %d", len(w.bufSpans))
	}

	// FlushErrors metric should have incremented
	errCount := testutil.ToFloat64(metrics.FlushErrors.WithLabelValues("spans"))
	if errCount < 1 {
		t.Errorf("expected FlushErrors > 0, got %f", errCount)
	}
}

func TestWriterRetryBufferCap(t *testing.T) {
	lakeDir, err := os.MkdirTemp("", "fanout-retrycap-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(lakeDir)

	chSpans := make(chan SpanRow, 10)
	chLogs := make(chan LogRow, 10)
	chMetrics := make(chan MetricRow, 10)

	cfg := config.Config{
		LakeDir:      filepath.Join(lakeDir, "readonly"),
		FlushSeconds: 60,
		MaxRows:      3, // maxRetry = 9
		DefaultNS:    "default",
	}
	w := NewWriter(cfg, chSpans, chLogs, chMetrics)

	// Create dir then make read-only
	spansDir := filepath.Join(cfg.LakeDir, "spans", "tenant=", "namespace=default")
	if err := os.MkdirAll(spansDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(spansDir, 0o444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(spansDir, 0o755)

	// Fill buffer with more than maxRetry (9) rows
	now := time.Now().UnixNano()
	for i := 0; i < 15; i++ {
		w.bufSpans = append(w.bufSpans, SpanRow{
			TraceID: fmt.Sprintf("t%d", i), SpanID: fmt.Sprintf("s%d", i),
			ServiceName: "svc", Namespace: "default", Name: "op",
			StartUnixNanos: now, EndUnixNanos: now + 1e6, DurationMs: 1.0, IngestedAt: now,
		})
	}

	// Reset dropped counter
	metrics.RowsDropped.Reset()

	w.flushLocked()

	// Buffer should be capped at maxRetry (3 * 3 = 9)
	if len(w.bufSpans) != 9 {
		t.Errorf("expected retry buffer capped at 9, got %d", len(w.bufSpans))
	}

	// RowsDropped metric should record the 6 dropped rows (15 - 9)
	dropped := testutil.ToFloat64(metrics.RowsDropped.WithLabelValues("spans"))
	if dropped != 6 {
		t.Errorf("expected RowsDropped = 6, got %f", dropped)
	}
}
