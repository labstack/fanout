//go:build readbench

package observability

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"syscall"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

// TestCompletedReadBenchmark measures actual dashboard methods over identical
// immutable format-3 files. Run with the readbench tag on an otherwise idle
// machine; the normal test gate does not run this resource-intensive fixture.
func TestCompletedReadBenchmark(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "2GB", DuckDBThreads: 4, DuckDBMaxConns: 4, RollupInterval: 100 * time.Millisecond}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	at := time.Now().UTC().Truncate(time.Hour)
	rowsPerBatch := 25000
	if os.Getenv("FANOUT_READ_BENCH_LARGE") == "1" {
		rowsPerBatch = 100000
	}
	for batch := range 12 {
		b := telemetrystore.Batch{ID: fmt.Sprint("hour", batch)}
		for i := range rowsPerBatch {
			n := at.Add(-time.Duration(batch)*time.Hour - time.Duration(i%1800)*time.Second).UnixNano()
			trace := fmt.Sprintf("trace%d-%d", batch, i/2)
			service := fmt.Sprint("service", i%4)
			status := "OK"
			if i%101 == 0 {
				status = "ERROR"
			}
			b.Spans = append(b.Spans, telemetry.Span{Namespace: "prod", ServiceName: service, TraceID: trace, SpanID: fmt.Sprint(i), StartUnixNanos: n, EndUnixNanos: n + int64(i%1000+1)*1000000, DurationMS: float64(i%1000 + 1), StatusCode: status, HTTPMethod: "GET", HTTPRoute: fmt.Sprint("/route", i%8), IngestedAt: at.UnixNano(), Attributes: map[string]any{"http.route": fmt.Sprint("/route", i%8)}, Resource: map[string]any{"service.name": service}})
			b.Logs = append(b.Logs, telemetry.Log{Namespace: "prod", ServiceName: service, TraceID: trace, TimeUnixNanos: n, Severity: []string{"INFO", "WARN", "ERROR"}[i%3], Body: "request complete", IngestedAt: at.UnixNano()})
		}
		if err := repo.Commit(t.Context(), b); err != nil {
			t.Fatal(err)
		}
	}
	// Many old immutable files exercise binder/file selection without changing
	// the hot window's row count. The newest entry query can bind newest first.
	for i := range 300 {
		n := at.Add(-time.Duration(24+i) * time.Hour).UnixNano()
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprint("old", i), Logs: []telemetry.Log{{TimeUnixNanos: n, IngestedAt: at.UnixNano(), Body: "old"}}, Metrics: []telemetry.Metric{{TimeUnixNanos: n, IngestedAt: at.UnixNano(), Value: 1}}}); err != nil {
			t.Fatal(err)
		}
	}
	d, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	svc := New(d, d, 30)
	raw := New(SQLDB(d.DB), d, 30)
	if _, err := d.DB.Exec(`CHECKPOINT`); err != nil {
		t.Fatal(err)
	}
	emptyCatalog, err := os.Stat(cfg.QueryDuckDBPath())
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for attempt := 0; ; attempt++ {
		if attempt > 312 {
			t.Fatal("cache backfill made no progress")
		}
		if _, err := d.RefreshReadCaches(t.Context()); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := d.DB.QueryRow(`SELECT count(*) FROM read_batches`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 312 {
			break
		}
	}

	t.Logf("backfill=%s rows_per_signal=%d immutable_batches=312", time.Since(started), rowsPerBatch*12)
	if _, err := d.DB.Exec(`CHECKPOINT`); err != nil {
		t.Fatal(err)
	}
	catalog, err := os.Stat(cfg.QueryDuckDBPath())
	if err != nil {
		t.Fatal(err)
	}
	stats, err := repo.Parquet.Stats()
	if err != nil {
		t.Fatal(err)
	}
	var parquetBytes int64
	for _, stat := range stats {
		parquetBytes += stat.Bytes
	}
	footprint, _ := json.Marshal(map[string]any{"experiment": "cache_footprint", "rows_per_signal": rowsPerBatch * 12, "empty_catalog_bytes": emptyCatalog.Size(), "catalog_bytes": catalog.Size(), "parquet_bytes": parquetBytes})
	t.Log("RESULT", string(footprint))
	broad := Scope{Start: at.Add(-24 * time.Hour), End: at.Add(time.Second), Namespace: "prod"}
	narrow := Scope{Start: at.Add(-20 * time.Minute), End: at.Add(time.Second), Namespace: "prod"}
	for _, scope := range []Scope{broad, narrow} {
		for name, method := range map[string]func(*Service) error{
			"trace": func(s *Service) error { _, err := s.Trace(t.Context(), scope, "", "", 100); return err },
		} {
			for _, implementation := range []struct {
				name    string
				service *Service
			}{{"raw", raw}, {"completed", svc}} {
				if err := method(implementation.service); err != nil {
					t.Fatal(err)
				}
				runtime.GC()
				before := readCPU()
				timings := []float64{}
				for range 10 {
					start := time.Now()
					if err := method(implementation.service); err != nil {
						t.Fatal(err)
					}
					timings = append(timings, float64(time.Since(start))/float64(time.Millisecond))
				}
				cpu := readCPU() - before
				sort.Float64s(timings)
				report := map[string]any{"method": name, "implementation": implementation.name, "window_minutes": scope.End.Sub(scope.Start).Minutes(), "median_ms": (timings[4] + timings[5]) / 2, "p95_ms": timings[9], "cpu_seconds_per_request": cpu / 10, "rows_per_signal": rowsPerBatch * 12}
				data, _ := json.Marshal(report)
				t.Log("RESULT", string(data))
			}
		}
	}
}
func readCPU() float64 {
	var r syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &r)
	return float64(r.Utime.Sec+r.Stime.Sec) + float64(r.Utime.Usec+r.Stime.Usec)/1e6
}
