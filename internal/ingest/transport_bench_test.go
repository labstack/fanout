//go:build transportbench

package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	httpserver "github.com/labstack/fanout/internal/server"
	"github.com/labstack/fanout/internal/settings"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// Opt-in experiment, not part of normal tests. Both paths use real auth and
// conversion; durable also waits for the production writer's Parquet commit.
// Client and server share this process: CPU/allocations include both sides.
// Run: go test -tags transportbench ./internal/ingest -run '^TestTransportComparison$' -v -count=1 -timeout=10m
func TestTransportComparison(t *testing.T) {
	t.Logf("environment go=%s os=%s arch=%s CPUs=%d GOMAXPROCS=%d", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.GOMAXPROCS(0))
	for _, durable := range []bool{false, true} {
		for _, concurrency := range []int{1, 16} {
			for rep := range 3 {
				modes := []string{"native", "shared"}
				if rep%2 == 1 {
					modes[0], modes[1] = modes[1], modes[0]
				}
				for _, mode := range modes {
					t.Run(fmt.Sprintf("durable=%t/c=%d/rep=%d/%s", durable, concurrency, rep+1, mode), func(t *testing.T) {
						runTransportComparison(t, mode, durable, concurrency)
					})
				}
			}
		}
	}
}

type transportCountSubmitter struct {
	downstream batchSubmitter
	rows       atomic.Int64
}

func (s *transportCountSubmitter) Submit(ctx context.Context, b telemetrystore.Batch) error {
	if s.downstream != nil {
		if err := s.downstream.Submit(ctx, b); err != nil {
			return err
		}
	}
	s.rows.Add(int64(len(b.Spans) + len(b.Logs) + len(b.Metrics)))
	return nil
}

func runTransportComparison(t *testing.T, mode string, durable bool, concurrency int) {
	store := newRuntimeStore(t)
	token, hash, err := settings.GenerateIngestToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetIngest(context.Background(), settings.Ingest{TokenHash: hash}); err != nil {
		t.Fatal(err)
	}
	sink := &transportCountSubmitter{}
	if durable {
		root := t.TempDir()
		repo, err := telemetrystore.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		writer := telemetrystore.NewWriter(repo, 50000)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- writer.Run(ctx) }()
		sink.downstream = writer
		t.Cleanup(func() {
			cancel()
			writer.Wait()
			if err := <-done; err != nil {
				t.Error(err)
			}
			if err := repo.Close(); err != nil {
				t.Error(err)
			}
			issues, err := telemetrystore.VerifyBatches(root)
			if err != nil || len(issues) > 0 {
				t.Errorf("Parquet verification: %v, issues=%v", err, issues)
			}
		})
	}
	g := grpc.NewServer(GRPCServerOptions(store)...)
	srv := NewServer(config.Config{DefaultNamespace: "default"}, sink)
	RegisterOTLP(g, srv)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveErr := make(chan error, 1)
	if mode == "native" {
		go func() { serveErr <- g.Serve(lis) }()
		t.Cleanup(func() {
			g.Stop()
			if err := <-serveErr; err != nil && err != grpc.ErrServerStopped {
				t.Error(err)
			}
		})
	} else {
		h := NewHTTPHandler(srv, store)
		hs := httpserver.New(lis.Addr().String(), http.NotFoundHandler(), h, g)
		go func() { serveErr <- hs.Serve(lis) }()
		t.Cleanup(func() {
			g.Stop()
			_ = hs.Close()
			if err := <-serveErr; err != http.ErrServerClosed {
				t.Error(err)
			}
		})
	}
	tr, lo, me := testTraceRequest(), testLogsRequest(), testMetricsRequest()
	// Equal, nonempty batches for all three signals, reused read-only by clients.
	for i := 1; i < 1000; i++ {
		tr.ResourceSpans[0].ScopeSpans[0].Spans = append(tr.ResourceSpans[0].ScopeSpans[0].Spans, tr.ResourceSpans[0].ScopeSpans[0].Spans[0])
		lo.ResourceLogs[0].ScopeLogs[0].LogRecords = append(lo.ResourceLogs[0].ScopeLogs[0].LogRecords, lo.ResourceLogs[0].ScopeLogs[0].LogRecords[0])
		me.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].GetGauge().DataPoints = append(me.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].GetGauge().DataPoints, me.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].GetGauge().DataPoints[0])
	}
	calls := make([]func(int) error, concurrency)
	for i := range calls {
		conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		tc, lc, mc := collectortrace.NewTraceServiceClient(conn), collectorlogs.NewLogsServiceClient(conn), collectormetrics.NewMetricsServiceClient(conn)
		calls[i] = func(signal int) error {
			ctx, cancel := context.WithTimeout(metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+token), 10*time.Second)
			defer cancel()
			switch signal % 3 {
			case 0:
				_, err = tc.Export(ctx, tr)
			case 1:
				_, err = lc.Export(ctx, lo)
			case 2:
				_, err = mc.Export(ctx, me)
			}
			return err
		}
		// Warm each connection and each signal before measuring.
		for signal := range 3 {
			if err := calls[i](signal); err != nil {
				t.Fatal(err)
			}
		}
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	cpuBefore := transportCPU()
	rowsBefore := sink.rows.Load()
	start := time.Now()
	deadline := start.Add(10 * time.Second)
	var wg sync.WaitGroup
	latencies := make([][]float64, concurrency)
	errors := make([]error, concurrency)
	for worker := range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := worker; time.Now().Before(deadline); n++ {
				at := time.Now()
				if err := calls[worker](n); err != nil {
					errors[worker] = err
					return
				}
				latencies[worker] = append(latencies[worker], float64(time.Since(at))/float64(time.Millisecond))
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	cpu := transportCPU() - cpuBefore
	runtime.ReadMemStats(&after)
	var samples []float64
	for i := range concurrency {
		if errors[i] != nil {
			t.Fatal(errors[i])
		}
		samples = append(samples, latencies[i]...)
	}
	sort.Float64s(samples)
	rows := sink.rows.Load() - rowsBefore
	if rows != int64(len(samples)*1000) {
		t.Fatalf("server rows %d != acknowledged %d", rows, len(samples)*1000)
	}
	if len(samples) == 0 {
		t.Fatal("no exports")
	}
	result := map[string]any{"mode": mode, "durable": durable, "concurrency": concurrency, "rows": rows, "seconds": elapsed, "rows_per_second": float64(rows) / elapsed, "p50_ms": samples[(len(samples)-1)*50/100], "p95_ms": samples[(len(samples)-1)*95/100], "p99_ms": samples[(len(samples)-1)*99/100], "cpu_cores_client_and_server": cpu / elapsed, "allocated_bytes_per_row": float64(after.TotalAlloc-before.TotalAlloc) / float64(rows), "errors": 0}
	encoded, _ := json.Marshal(result)
	t.Log("RESULT " + string(encoded))
}

func transportCPU() float64 {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		panic(err)
	}
	return float64(usage.Utime.Sec+usage.Stime.Sec) + float64(usage.Utime.Usec+usage.Stime.Usec)/1e6
}
