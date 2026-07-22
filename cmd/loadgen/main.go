// Command loadgen is fanout's local stress/soak generator and benchmark
// reporter. It pushes synthetic OTLP (traces, logs, metrics) straight at the
// gRPC ingest port with knobs for rate, duration, service/namespace count,
// attribute cardinality, and error rate, then reports OTLP export latency
// percentiles and — when pointed at fanout's /-/metrics — the server-side
// deltas that matter (rows accepted/dropped, file growth, rollup/flush time).
//
// Unlike telemetrygen (single service per process), loadgen emits cross-service
// parent/child traces and producer/consumer pairs, so it exercises BOTH rollups
// — service_rollup and edge_rollup (call + messaging topology) — plus the
// GROUP BY cardinality cost. It reuses fanout's own vendored OTLP proto, so it
// adds no new dependencies.
//
// Example — a 10-minute soak at 2k traces/s across 50 services, with a report:
//
//	go run ./cmd/loadgen -rate 2000 -duration 10m -services 50 -attr-cardinality 200 \
//	  -metrics-url https://demo.fanout.test/-/metrics -metrics-token "$METRICS_TOKEN" -report run.json
//
// The metrics endpoint requires -metrics-token unless METRICS_PUBLIC=true.
//
// Run fanout locally with PUBLIC_INGEST=true for tokenless ingest, or pass -token.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type config struct {
	endpoint     string
	token        string
	rate         float64
	duration     time.Duration
	workers      int
	services     int
	namespaces   int
	cardinality  int
	errorRate    float64
	msgRatio     float64
	sendLogs     bool
	sendMetrics  bool
	metricsURL   string
	metricsToken string
	reportPath   string
	// Query-under-load: drive the read path concurrently with ingest.
	queryURL     string
	queryWorkers int
	queryRate    float64
	// Pass/fail thresholds (0 = no threshold). Non-zero exit on violation so
	// the harness can gate CI / releases.
	maxExportP95 float64
	maxQueryP95  float64
	// backfillHours, when >0, spreads each event's timestamp uniformly over the
	// last N hours (instead of "now"). Used to PRE-SEED a multi-hour dataset so
	// the lake spans several hour partitions — required to exercise within-day
	// (hour-partition) pruning, which a same-hour run can't.
	backfillHours float64
}

func main() {
	var cfg config
	flag.StringVar(&cfg.endpoint, "endpoint", "demo.fanout.test:4317", "OTLP gRPC endpoint")
	flag.StringVar(&cfg.token, "token", "", "ingest token (x-fanout-ingest-token); omit when fanout runs with PUBLIC_INGEST=true")
	flag.Float64Var(&cfg.rate, "rate", 1000, "target traces per second (aggregate)")
	flag.DurationVar(&cfg.duration, "duration", time.Minute, "run duration; 0 means run until interrupted")
	flag.IntVar(&cfg.workers, "workers", 8, "concurrent senders")
	flag.IntVar(&cfg.services, "services", 20, "number of distinct services")
	flag.IntVar(&cfg.namespaces, "namespaces", 1, "number of distinct namespaces")
	flag.IntVar(&cfg.cardinality, "attr-cardinality", 100, "distinct values per high-cardinality attribute")
	flag.Float64Var(&cfg.errorRate, "error-rate", 0.05, "fraction of spans marked STATUS_CODE_ERROR (0..1)")
	flag.Float64Var(&cfg.msgRatio, "messaging-ratio", 0.2, "fraction of traces that also emit a producer/consumer pair (0..1)")
	flag.BoolVar(&cfg.sendLogs, "logs", true, "also emit logs")
	flag.BoolVar(&cfg.sendMetrics, "metrics", true, "also emit metrics")
	flag.StringVar(&cfg.metricsURL, "metrics-url", "", "fanout /-/metrics URL to capture server-side deltas (e.g. https://demo.fanout.test/-/metrics)")
	flag.StringVar(&cfg.metricsToken, "metrics-token", "", "bearer token for a private fanout /-/metrics endpoint")
	flag.StringVar(&cfg.reportPath, "report", "", "write a JSON performance report to this path")
	flag.StringVar(&cfg.queryURL, "query-url", "", "fanout HTTP base URL to drive read load under ingest (e.g. https://demo.fanout.test)")
	flag.IntVar(&cfg.queryWorkers, "query-workers", 0, "concurrent query workers (0 = ingest only)")
	flag.Float64Var(&cfg.queryRate, "query-rate", 50, "target queries/sec (aggregate) when query-workers > 0")
	flag.Float64Var(&cfg.maxExportP95, "max-export-p95-ms", 0, "fail (exit 1) if ingest export p95 exceeds this")
	flag.Float64Var(&cfg.maxQueryP95, "max-query-p95-ms", 0, "fail (exit 1) if query p95 exceeds this")
	flag.Float64Var(&cfg.backfillHours, "backfill-hours", 0, "spread event timestamps over the last N hours (pre-seed a multi-hour dataset); 0 = use now()")
	flag.Parse()

	if cfg.services < 2 {
		log.Fatal("-services must be >= 2 (cross-service edges need a caller and a callee)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.duration)
		defer cancel()
	}

	conn, err := grpc.NewClient(cfg.endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial %s: %v", cfg.endpoint, err)
	}
	defer conn.Close()

	g := &generator{
		cfg:      cfg,
		traces:   collectortrace.NewTraceServiceClient(conn),
		logs:     collectorlogs.NewLogsServiceClient(conn),
		metrics:  collectormetrics.NewMetricsServiceClient(conn),
		lat:      newHistogram(),
		queryLat: newHistogram(),
		queryLatByOperation: map[string]*histogram{
			"overview":    newHistogram(),
			"topology":    newHistogram(),
			"performance": newHistogram(),
			"trace":       newHistogram(),
			"logs":        newHistogram(),
		},
		http: &http.Client{Timeout: 30 * time.Second},
		svcNames: func() []string {
			names := make([]string, cfg.services)
			for i := range names {
				names[i] = fmt.Sprintf("svc-%02d", i)
			}
			return names
		}(),
	}

	fmt.Printf("loadgen → %s | rate=%.0f/s workers=%d services=%d namespaces=%d cardinality=%d error-rate=%.2f\n",
		cfg.endpoint, cfg.rate, cfg.workers, cfg.services, cfg.namespaces, cfg.cardinality, cfg.errorRate)

	// Baseline server metrics before load (for end-of-run deltas).
	var baseline map[string]float64
	if cfg.metricsURL != "" {
		if baseline, err = scrapeMetrics(cfg.metricsURL, cfg.metricsToken); err != nil {
			fmt.Fprintf(os.Stderr, "  warn: baseline metrics scrape failed: %v\n", err)
		}
	}

	interval := time.Duration(float64(time.Second) * float64(cfg.workers) / cfg.rate)
	start := time.Now()

	var wg sync.WaitGroup
	for w := 0; w < cfg.workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.run(ctx, interval)
		}()
	}
	if cfg.queryURL != "" && cfg.queryWorkers > 0 {
		qInterval := time.Duration(float64(time.Second) * float64(cfg.queryWorkers) / cfg.queryRate)
		fmt.Printf("query load: %d workers @ %.0f q/s → %s/api/observability/{overview,topology,performance,trace,logs}\n", cfg.queryWorkers, cfg.queryRate, strings.TrimRight(cfg.queryURL, "/"))
		for w := 0; w < cfg.queryWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				g.runQueries(ctx, qInterval)
			}()
		}
	}

	done := make(chan struct{})
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				close(done)
				return
			case <-t.C:
				elapsed := time.Since(start).Seconds()
				sent := g.tracesSent.Load()
				fmt.Printf("  %5.0fs  traces=%d (%.0f/s)  spans=%d  p95=%.1fms  errors=%d\n",
					elapsed, sent, float64(sent)/elapsed, g.spansSent.Load(), g.lat.quantile(0.95), g.sendErrs.Load())
			}
		}
	}()

	wg.Wait()
	<-done
	elapsed := time.Since(start).Seconds()

	rep := report{
		Endpoint:        cfg.endpoint,
		DurationSec:     round2(elapsed),
		TargetRate:      cfg.rate,
		Workers:         cfg.workers,
		Services:        cfg.services,
		TracesSent:      g.tracesSent.Load(),
		SpansSent:       g.spansSent.Load(),
		LogsSent:        g.logsSent.Load(),
		MetricsSent:     g.metricsSent.Load(),
		SendErrors:      g.sendErrs.Load(),
		AvgTracesPerSec: round2(float64(g.tracesSent.Load()) / elapsed),
		ExportLatencyMs: g.lat.snapshot(),
	}
	if cfg.queryURL != "" && cfg.queryWorkers > 0 {
		ql := g.queryLat.snapshot()
		rep.QueryLatencyMs = &ql
		rep.QueryLatencyByOperation = make(map[string]latencyReport, len(g.queryLatByOperation))
		for operation, histogram := range g.queryLatByOperation {
			rep.QueryLatencyByOperation[operation] = histogram.snapshot()
		}
		rep.QueriesRun = g.queriesRun.Load()
		rep.QueryErrors = g.queryErrs.Load()
	}
	if cfg.metricsURL != "" {
		if final, ferr := scrapeMetrics(cfg.metricsURL, cfg.metricsToken); ferr != nil {
			fmt.Fprintf(os.Stderr, "  warn: final metrics scrape failed: %v\n", ferr)
		} else {
			rep.Server = serverDelta(baseline, final)
		}
	}

	printReport(rep)
	if cfg.reportPath != "" {
		if err := writeReport(cfg.reportPath, rep); err != nil {
			fmt.Fprintf(os.Stderr, "  warn: write report: %v\n", err)
		} else {
			fmt.Printf("report written: %s\n", cfg.reportPath)
		}
	}

	// Pass/fail thresholds → non-zero exit so the harness can gate on it.
	// Always-on failure signals first: a run that ingested nothing, dropped rows,
	// or hit send/query errors must FAIL — otherwise p95-of-survivors reads 0 and
	// a totally broken run looks like a clean pass.
	var fails []string
	if rep.ExportLatencyMs.Count == 0 {
		fails = append(fails, "no OTLP exports succeeded")
	}
	if attempted := rep.ExportLatencyMs.Count + rep.SendErrors; rep.SendErrors > 0 && attempted > 0 {
		if rate := float64(rep.SendErrors) / float64(attempted); rate > maxSendErrorRate {
			fails = append(fails, fmt.Sprintf("send error rate %.3f%% (%d/%d) > %.1f%%",
				rate*100, rep.SendErrors, attempted, maxSendErrorRate*100))
		}
	}
	if rep.QueryErrors > 0 {
		fails = append(fails, fmt.Sprintf("query errors=%d", rep.QueryErrors))
	}
	if rep.Server != nil && rep.Server.RowsDroppedDelta > 0 {
		fails = append(fails, fmt.Sprintf("rows dropped=%.0f", rep.Server.RowsDroppedDelta))
	}
	if cfg.maxExportP95 > 0 && rep.ExportLatencyMs.P95Ms > cfg.maxExportP95 {
		fails = append(fails, fmt.Sprintf("export p95 %.0fms > %.0fms", rep.ExportLatencyMs.P95Ms, cfg.maxExportP95))
	}
	if cfg.maxQueryP95 > 0 && rep.QueryLatencyMs != nil && rep.QueryLatencyMs.P95Ms > cfg.maxQueryP95 {
		fails = append(fails, fmt.Sprintf("query p95 %.0fms > %.0fms", rep.QueryLatencyMs.P95Ms, cfg.maxQueryP95))
	}
	if len(fails) > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: %s\n", strings.Join(fails, "; "))
		os.Exit(1)
	}
}

type generator struct {
	cfg      config
	traces   collectortrace.TraceServiceClient
	logs     collectorlogs.LogsServiceClient
	metrics  collectormetrics.MetricsServiceClient
	lat      *histogram
	queryLat *histogram
	// Populated before workers start and never mutated. Histograms use atomics.
	queryLatByOperation map[string]*histogram
	http                *http.Client
	svcNames            []string

	tracesSent  atomic.Int64
	spansSent   atomic.Int64
	logsSent    atomic.Int64
	metricsSent atomic.Int64
	sendErrs    atomic.Int64
	lastErr     atomic.Pointer[error]
	queriesRun  atomic.Int64
	queryErrs   atomic.Int64
}

// queryWindows rotate so the read load hits both small and wide rollup windows,
// exercising the latency SLOs under ingest. The shared HTTP/MCP observability
// kernel accepts Go duration strings and caps the window at 24 hours.
var queryWindows = []string{"15m", "1h", "6h", "12h", "24h"}

type queryTarget struct {
	operation string
	path      string
}

func queryTargetAt(i int) queryTarget {
	operations := [...]string{"overview", "topology", "performance", "trace", "logs"}
	operationIndex := i % len(operations)
	round := i / len(operations)
	// Offset each successive round so every operation exercises every window;
	// using i for both dimensions locks equal-length lists into fixed pairs.
	window := queryWindows[(operationIndex+round)%len(queryWindows)]
	operation := operations[operationIndex]
	return queryTarget{
		operation: operation,
		path:      fmt.Sprintf("/api/observability/%s?window=%s&limit=100", operation, window),
	}
}

// maxSendErrorRate tolerates a handful of transient export errors (e.g. a gRPC
// DeadlineExceeded during a rollup pause) at high volume — they are noise, not a
// capacity failure. The real data-integrity gate is server-side rows-dropped.
// A genuine backpressure failure produces a send-error rate far above this.
const maxSendErrorRate = 0.001 // 0.1%

// runQueries drives the shared HTTP/MCP observability read path concurrently
// with ingest, recording latency into queryLat.
func (g *generator) runQueries(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	i := 0
	base := strings.TrimRight(g.cfg.queryURL, "/")
	reportedFailure := make(map[string]bool)
	reportFailure := func(operation, detail string) {
		if reportedFailure[operation] {
			return
		}
		reportedFailure[operation] = true
		log.Printf("query %s first failure: %s", operation, detail)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			target := queryTargetAt(i)
			url := base + target.path
			i++
			t0 := time.Now()
			resp, err := g.http.Get(url) //nolint:noctx // bounded by client timeout
			if err != nil {
				if ctx.Err() == nil {
					g.queryErrs.Add(1)
					reportFailure(target.operation, err.Error())
				}
				continue
			}
			contentType := resp.Header.Get("Content-Type")
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK || readErr != nil ||
				!strings.HasPrefix(contentType, "application/json") ||
				!json.Valid(body) {
				g.queryErrs.Add(1)
				sample := body
				if len(sample) > 256 {
					sample = sample[:256]
				}
				reportFailure(target.operation, fmt.Sprintf("status=%d content_type=%q read_err=%v body=%q", resp.StatusCode, contentType, readErr, sample))
				continue
			}
			latency := time.Since(t0)
			g.queryLat.record(latency)
			g.queryLatByOperation[target.operation].record(latency)
			g.queriesRun.Add(1)
		}
	}
}

func (g *generator) run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.sendTrace(ctx)
			if g.cfg.sendLogs {
				g.sendLog(ctx)
			}
			if g.cfg.sendMetrics {
				g.sendMetric(ctx)
			}
		}
	}
}

// countSendErr records a failed export, except when the run context is already
// done — in-flight requests cancelled at the duration deadline or on Ctrl-C are
// a clean shutdown, not a fanout failure, and shouldn't inflate the error count.
func (g *generator) countSendErr(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return
	}
	g.sendErrs.Add(1)
	if g.lastErr.CompareAndSwap(nil, &err) { // surface the first real error once
		fmt.Fprintf(os.Stderr, "  send error: %v\n", err)
	}
}

func (g *generator) outCtx(ctx context.Context) context.Context {
	if g.cfg.token == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, "x-fanout-ingest-token", g.cfg.token)
}

// sendTrace emits one trace: a SERVER span in the caller service and a CLIENT
// child span in a different callee service (a call edge), plus — for a fraction
// of traces — a PRODUCER/CONSUMER pair on a messaging destination (a messaging
// edge). Each ResourceSpans carries one service.name, matching how fanout keys
// service_rollup and edge_rollup.
// eventTime returns the timestamp for an emitted event: now(), or — when
// backfillHours>0 — a time spread uniformly over the last N hours so a pre-seed
// run populates multiple hour partitions (to exercise within-day pruning).
func (g *generator) eventTime() time.Time {
	if g.cfg.backfillHours <= 0 {
		return time.Now()
	}
	return time.Now().Add(-time.Duration(rand.Float64() * g.cfg.backfillHours * float64(time.Hour)))
}

func (g *generator) sendTrace(ctx context.Context) {
	ns := g.namespace()
	caller := g.svcNames[rand.IntN(g.cfg.services)]
	callee := g.svcNames[rand.IntN(g.cfg.services)]
	for callee == caller {
		callee = g.svcNames[rand.IntN(g.cfg.services)]
	}

	traceID := randBytes(16)
	parentID := randBytes(8)
	childID := randBytes(8)
	now := g.eventTime()
	startNano := uint64(now.UnixNano())
	parentEnd := uint64(now.Add(time.Duration(20+rand.IntN(400)) * time.Millisecond).UnixNano())
	childEnd := uint64(now.Add(time.Duration(5+rand.IntN(200)) * time.Millisecond).UnixNano())

	resSpans := []*tracepb.ResourceSpans{
		g.resourceSpans(caller, ns, &tracepb.Span{
			TraceId: traceID, SpanId: parentID,
			Name: "GET /" + g.route(), Kind: tracepb.Span_SPAN_KIND_SERVER,
			StartTimeUnixNano: startNano, EndTimeUnixNano: parentEnd,
			Attributes: g.spanAttrs(), Status: g.status(),
		}),
		g.resourceSpans(callee, ns, &tracepb.Span{
			TraceId: traceID, SpanId: childID, ParentSpanId: parentID,
			Name: "rpc." + g.route(), Kind: tracepb.Span_SPAN_KIND_CLIENT,
			StartTimeUnixNano: startNano, EndTimeUnixNano: childEnd,
			Attributes: g.spanAttrs(), Status: g.status(),
		}),
	}
	spans := 2

	if rand.Float64() < g.cfg.msgRatio {
		dest := fmt.Sprintf("topic-%d", rand.IntN(8))
		msgAttrs := []*common.KeyValue{
			strAttr("messaging.destination.name", dest),
			strAttr("messaging.system", "kafka"),
		}
		producer := g.svcNames[rand.IntN(g.cfg.services)]
		consumer := g.svcNames[rand.IntN(g.cfg.services)]
		for consumer == producer {
			consumer = g.svcNames[rand.IntN(g.cfg.services)]
		}
		resSpans = append(resSpans,
			g.resourceSpans(producer, ns, &tracepb.Span{
				TraceId: randBytes(16), SpanId: randBytes(8),
				Name: dest + " publish", Kind: tracepb.Span_SPAN_KIND_PRODUCER,
				StartTimeUnixNano: startNano, EndTimeUnixNano: parentEnd,
				Attributes: msgAttrs, Status: g.status(),
			}),
			g.resourceSpans(consumer, ns, &tracepb.Span{
				TraceId: randBytes(16), SpanId: randBytes(8),
				Name: dest + " process", Kind: tracepb.Span_SPAN_KIND_CONSUMER,
				StartTimeUnixNano: startNano, EndTimeUnixNano: childEnd,
				Attributes: msgAttrs, Status: g.status(),
			}),
		)
		spans += 2
	}

	t0 := time.Now()
	_, err := g.traces.Export(g.outCtx(ctx), &collectortrace.ExportTraceServiceRequest{ResourceSpans: resSpans})
	if err != nil {
		g.countSendErr(ctx, err)
		return
	}
	g.lat.record(time.Since(t0))
	g.tracesSent.Add(1)
	g.spansSent.Add(int64(spans))
}

func (g *generator) resourceSpans(service, ns string, span *tracepb.Span) *tracepb.ResourceSpans {
	return &tracepb.ResourceSpans{
		Resource: &resourcepb.Resource{Attributes: []*common.KeyValue{
			strAttr("service.name", service),
			strAttr("service.namespace", ns),
		}},
		ScopeSpans: []*tracepb.ScopeSpans{{
			Scope: &common.InstrumentationScope{Name: "loadgen"},
			Spans: []*tracepb.Span{span},
		}},
	}
}

func (g *generator) sendLog(ctx context.Context) {
	ns := g.namespace()
	svc := g.svcNames[rand.IntN(g.cfg.services)]
	sev := "INFO"
	sevNum := logspb.SeverityNumber_SEVERITY_NUMBER_INFO
	if rand.Float64() < g.cfg.errorRate {
		sev, sevNum = "ERROR", logspb.SeverityNumber_SEVERITY_NUMBER_ERROR
	}
	req := &collectorlogs.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{
		Resource: &resourcepb.Resource{Attributes: []*common.KeyValue{
			strAttr("service.name", svc), strAttr("service.namespace", ns),
		}},
		ScopeLogs: []*logspb.ScopeLogs{{
			LogRecords: []*logspb.LogRecord{{
				TimeUnixNano:   uint64(g.eventTime().UnixNano()),
				SeverityText:   sev,
				SeverityNumber: sevNum,
				Body:           &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "request " + g.route()}},
				Attributes:     g.spanAttrs(),
			}},
		}},
	}}}
	t0 := time.Now()
	if _, err := g.logs.Export(g.outCtx(ctx), req); err != nil {
		g.countSendErr(ctx, err)
		return
	}
	g.lat.record(time.Since(t0))
	g.logsSent.Add(1)
}

func (g *generator) sendMetric(ctx context.Context) {
	ns := g.namespace()
	svc := g.svcNames[rand.IntN(g.cfg.services)]
	req := &collectormetrics.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{
		Resource: &resourcepb.Resource{Attributes: []*common.KeyValue{
			strAttr("service.name", svc), strAttr("service.namespace", ns),
		}},
		ScopeMetrics: []*metricspb.ScopeMetrics{{
			Metrics: []*metricspb.Metric{{
				Name: "request.duration",
				Unit: "ms",
				Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
					DataPoints: []*metricspb.NumberDataPoint{{
						TimeUnixNano: uint64(g.eventTime().UnixNano()),
						Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: rand.Float64() * 500},
						Attributes:   g.spanAttrs(),
					}},
				}},
			}},
		}},
	}}}
	t0 := time.Now()
	if _, err := g.metrics.Export(g.outCtx(ctx), req); err != nil {
		g.countSendErr(ctx, err)
		return
	}
	g.lat.record(time.Since(t0))
	g.metricsSent.Add(1)
}

func (g *generator) namespace() string {
	if g.cfg.namespaces <= 1 {
		return "default"
	}
	return fmt.Sprintf("ns-%02d", rand.IntN(g.cfg.namespaces))
}

func (g *generator) route() string {
	return fmt.Sprintf("r%d", rand.IntN(20))
}

// spanAttrs carries a high-cardinality key to stress the rollup GROUP BYs and
// attribute extraction.
func (g *generator) spanAttrs() []*common.KeyValue {
	return []*common.KeyValue{
		strAttr("http.method", []string{"GET", "POST", "PUT", "DELETE"}[rand.IntN(4)]),
		strAttr("user.id", fmt.Sprintf("u-%d", rand.IntN(g.cfg.cardinality))),
	}
}

func (g *generator) status() *tracepb.Status {
	if rand.Float64() < g.cfg.errorRate {
		return &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR}
	}
	return &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK}
}

func strAttr(k, v string) *common.KeyValue {
	return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: v}}}
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rand.IntN(256))
	}
	return b
}

// ── Export-latency histogram ───────────────────────────────────────────────
// Lock-free, fixed-bucket (log-spaced ms) so it's safe and bounded under a
// multi-hour soak. Percentiles are bucket-granular approximations (the upper
// bound of the bucket the quantile falls in).

var latBoundsMs = []float64{0.5, 1, 2, 3, 5, 8, 13, 21, 34, 55, 89, 144, 233, 377, 610, 1000, 2000, 5000, 10000, 30000}

type histogram struct {
	counts    []atomic.Int64 // counts[i] = samples in (bounds[i-1], bounds[i]]
	over      atomic.Int64   // samples > last bound
	n         atomic.Int64
	sumMicros atomic.Int64
}

func newHistogram() *histogram { return &histogram{counts: make([]atomic.Int64, len(latBoundsMs))} }

func (h *histogram) record(d time.Duration) {
	h.n.Add(1)
	h.sumMicros.Add(d.Microseconds())
	ms := float64(d) / float64(time.Millisecond)
	for i, b := range latBoundsMs {
		if ms <= b {
			h.counts[i].Add(1)
			return
		}
	}
	h.over.Add(1)
}

func (h *histogram) quantile(q float64) float64 {
	total := h.n.Load()
	if total == 0 {
		return 0
	}
	target := int64(math.Ceil(q * float64(total)))
	var cum int64
	for i := range latBoundsMs {
		cum += h.counts[i].Load()
		if cum >= target {
			return latBoundsMs[i]
		}
	}
	return latBoundsMs[len(latBoundsMs)-1] // in the overflow bucket: report the cap as a floor
}

func (h *histogram) snapshot() latencyReport {
	n := h.n.Load()
	mean := 0.0
	if n > 0 {
		mean = float64(h.sumMicros.Load()) / float64(n) / 1000.0
	}
	return latencyReport{
		Count:   n,
		MeanMs:  round2(mean),
		P50Ms:   h.quantile(0.50),
		P95Ms:   h.quantile(0.95),
		P99Ms:   h.quantile(0.99),
		Over30s: h.over.Load(),
	}
}

// ── Server-metrics scrape ──────────────────────────────────────────────────

// scrapeMetrics fetches a Prometheus text endpoint and sums each metric across
// its label series (good enough for the totals/gauges we report).
func scrapeMetrics(url, token string) (map[string]float64, error) {
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
	out := map[string]float64{}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20) // metric lines can be long with labels
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if i := strings.IndexByte(name, '{'); i >= 0 {
			name = name[:i]
		}
		if v, err := strconv.ParseFloat(fields[1], 64); err == nil {
			out[name] += v
		}
	}
	return out, sc.Err()
}

// serverDelta turns baseline+final scrapes into the curated report fields —
// counter deltas, gauge finals, and histogram-derived averages over the run.
func serverDelta(base, final map[string]float64) *serverReport {
	if final == nil {
		return nil
	}
	if base == nil {
		base = map[string]float64{}
	}
	avg := func(sumKey, countKey string) float64 {
		dc := final[countKey] - base[countKey]
		if dc <= 0 {
			return 0
		}
		return round2((final[sumKey] - base[sumKey]) / dc * 1000.0) // seconds → ms
	}
	return &serverReport{
		IngestRowsDelta:  final["fanout_ingest_rows_total"] - base["fanout_ingest_rows_total"],
		RowsDroppedDelta: final["fanout_rows_dropped_total"] - base["fanout_rows_dropped_total"],
		LakePartitions:   final["fanout_lake_partitions"],
		LakeSizeBytes:    final["fanout_lake_size_bytes"],
		IngestQueueDepth: final["fanout_ingest_queue_depth"],
		AvgRollupMs:      avg("fanout_rollup_duration_seconds_sum", "fanout_rollup_duration_seconds_count"),
		AvgFlushMs:       avg("fanout_flush_duration_seconds_sum", "fanout_flush_duration_seconds_count"),
		AvgQueryMs:       avg("fanout_query_duration_seconds_sum", "fanout_query_duration_seconds_count"),
	}
}

// ── Report ─────────────────────────────────────────────────────────────────

type report struct {
	Endpoint                string                   `json:"endpoint"`
	DurationSec             float64                  `json:"duration_sec"`
	TargetRate              float64                  `json:"target_rate"`
	Workers                 int                      `json:"workers"`
	Services                int                      `json:"services"`
	TracesSent              int64                    `json:"traces_sent"`
	SpansSent               int64                    `json:"spans_sent"`
	LogsSent                int64                    `json:"logs_sent"`
	MetricsSent             int64                    `json:"metrics_sent"`
	SendErrors              int64                    `json:"send_errors"`
	AvgTracesPerSec         float64                  `json:"avg_traces_per_sec"`
	ExportLatencyMs         latencyReport            `json:"export_latency_ms"`
	QueriesRun              int64                    `json:"queries_run,omitempty"`
	QueryErrors             int64                    `json:"query_errors,omitempty"`
	QueryLatencyMs          *latencyReport           `json:"query_latency_ms,omitempty"`
	QueryLatencyByOperation map[string]latencyReport `json:"query_latency_by_operation,omitempty"`
	Server                  *serverReport            `json:"server,omitempty"`
}

type latencyReport struct {
	Count   int64   `json:"count"`
	MeanMs  float64 `json:"mean_ms"`
	P50Ms   float64 `json:"p50_ms"`
	P95Ms   float64 `json:"p95_ms"`
	P99Ms   float64 `json:"p99_ms"`
	Over30s int64   `json:"over_30s"`
}

type serverReport struct {
	IngestRowsDelta  float64 `json:"ingest_rows_delta"`
	RowsDroppedDelta float64 `json:"rows_dropped_delta"`
	LakePartitions   float64 `json:"lake_partitions"`
	LakeSizeBytes    float64 `json:"lake_size_bytes"`
	IngestQueueDepth float64 `json:"ingest_queue_depth"`
	AvgRollupMs      float64 `json:"avg_rollup_ms"`
	AvgFlushMs       float64 `json:"avg_flush_ms"`
	AvgQueryMs       float64 `json:"avg_query_ms"`
}

func printReport(r report) {
	fmt.Printf("\n── performance report ──────────────────────────────────────\n")
	fmt.Printf("duration       %.1fs  (target %.0f traces/s → actual %.0f)\n", r.DurationSec, r.TargetRate, r.AvgTracesPerSec)
	fmt.Printf("sent           traces=%d spans=%d logs=%d metrics=%d\n", r.TracesSent, r.SpansSent, r.LogsSent, r.MetricsSent)
	fmt.Printf("send errors    %d\n", r.SendErrors)
	l := r.ExportLatencyMs
	fmt.Printf("export latency mean=%.1fms  p50≈%.0f  p95≈%.0f  p99≈%.0f  >30s=%d  (n=%d)\n",
		l.MeanMs, l.P50Ms, l.P95Ms, l.P99Ms, l.Over30s, l.Count)
	if r.QueryLatencyMs != nil {
		q := r.QueryLatencyMs
		fmt.Printf("query latency  mean=%.0fms  p50≈%.0f  p95≈%.0f  p99≈%.0f  (n=%d, errors=%d)\n",
			q.MeanMs, q.P50Ms, q.P95Ms, q.P99Ms, q.Count, r.QueryErrors)
		for _, operation := range []string{"overview", "topology", "performance", "trace", "logs"} {
			if item, ok := r.QueryLatencyByOperation[operation]; ok {
				fmt.Printf("  %-11s p50≈%-5.0f p95≈%-5.0f p99≈%-5.0f (n=%d)\n",
					operation, item.P50Ms, item.P95Ms, item.P99Ms, item.Count)
			}
		}
	}
	if r.Server != nil {
		s := r.Server
		fmt.Printf("server (Δ over run):\n")
		fmt.Printf("  rows accepted=%.0f  dropped=%.0f\n", s.IngestRowsDelta, s.RowsDroppedDelta)
		fmt.Printf("  lake_partitions=%.0f  lake_size=%.1fMB  ingest_queue_depth=%.0f\n",
			s.LakePartitions, s.LakeSizeBytes/(1<<20), s.IngestQueueDepth)
		fmt.Printf("  avg rollup=%.1fms  flush=%.1fms  query=%.1fms\n", s.AvgRollupMs, s.AvgFlushMs, s.AvgQueryMs)
	}
	fmt.Printf("────────────────────────────────────────────────────────────\n")
}

func writeReport(path string, r report) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }
