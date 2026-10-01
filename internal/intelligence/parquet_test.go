package intelligence

import (
	"fmt"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

// Exercise the production Parquet timestamp type, not a mutable TIMESTAMP table.
// Detector methods return nil on SQL errors, so require an actual anomaly from
// each query rather than merely accepting an empty snapshot.
func TestDetectorsReadNativeParquetTimestamps(t *testing.T) {
	ctx := t.Context()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 1}
	repository, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	duck, err := query.NewDuck(ctx, cfg, repository)
	if err != nil {
		t.Fatal(err)
	}
	defer duck.Close()
	start := time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.UTC)
	end := start.Add(15 * time.Minute)
	batch := telemetrystore.Batch{ID: "detectors"}
	for bucket, count := range []int{1, 2, 3, 30, 30, 30} {
		at := start.Add(time.Duration(bucket-3) * 5 * time.Minute)
		latency := float64(bucket+1) * 10
		status := "STATUS_CODE_OK"
		if bucket >= 3 {
			latency = 1000
			status = "STATUS_CODE_ERROR"
		}
		for i := range count {
			nanos := at.Add(time.Duration(i) * time.Second).UnixNano()
			batch.Spans = append(batch.Spans, telemetry.Span{
				Namespace: "prod", ServiceName: "checkout", TraceID: fmt.Sprintf("trace-%d-%d", bucket, i), SpanID: "span",
				Kind: "SPAN_KIND_SERVER", StatusCode: status, DurationMS: latency,
				StartUnixNanos: nanos, EndUnixNanos: nanos + int64(latency*1e6), IngestedAt: nanos,
			})
		}
	}
	for i := range 3 {
		nanos := start.Add(time.Duration(i) * time.Second).UnixNano()
		batch.Logs = append(batch.Logs, telemetry.Log{Namespace: "prod", ServiceName: "checkout", Severity: "ERROR", Body: "checkout failed", TimeUnixNanos: nanos, IngestedAt: nanos})
	}
	if err := repository.Commit(ctx, batch); err != nil {
		t.Fatal(err)
	}
	detector := NewDetector(duck, DefaultDetectorConfig())
	for name, detect := range map[string]func() []Anomaly{
		"error rate": func() []Anomaly { return detector.detectErrorRateAnomalies(ctx, start, end) },
		"latency":    func() []Anomaly { return detector.detectLatencyAnomalies(ctx, start, end) },
		"volume":     func() []Anomaly { return detector.detectVolumeAnomalies(ctx, start, end) },
	} {
		t.Run(name, func(t *testing.T) {
			anomalies := detect()
			if len(anomalies) != 1 || anomalies[0].ServiceName != "checkout" || anomalies[0].Current <= anomalies[0].Baseline {
				t.Fatalf("expected checkout anomaly, got %#v", anomalies)
			}
		})
	}
	patterns := detector.detectLogPatterns(ctx, start, end)
	if len(patterns) != 1 || patterns[0].Count != 3 || patterns[0].FirstSeen.UnixNano() != start.UnixNano() {
		t.Fatalf("log patterns: %#v", patterns)
	}
}
