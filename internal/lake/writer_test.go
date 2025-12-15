package lake

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
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
			Name:           "test-op",
			StartUnixNanos: now,
			EndUnixNanos:   now + 1000000,
			DurationMs:     1.0,
			IngestedAt:     now,
		}
	}

	// Wait for flush
	time.Sleep(2 * time.Second)

	// Check parquet file exists
	matches, err := filepath.Glob(filepath.Join(lakeDir, "spans", "year=*", "month=*", "day=*", "hour=*", "part-*.parquet"))
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
			Name:           "op",
			StartUnixNanos: now,
			EndUnixNanos:   now + 1000000,
			DurationMs:     1.0,
			IngestedAt:     now,
		}
	}

	// Short wait - MaxRows should trigger immediate flush
	time.Sleep(500 * time.Millisecond)

	matches, err := filepath.Glob(filepath.Join(lakeDir, "spans", "year=*", "month=*", "day=*", "hour=*", "part-*.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Error("expected flush due to MaxRows")
	}
}
