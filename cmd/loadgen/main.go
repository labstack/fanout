// Command loadgen is fanout's local stress/soak generator. It pushes synthetic
// OTLP (traces, logs, metrics) straight at the gRPC ingest port with knobs for
// rate, duration, service/namespace count, attribute cardinality, and error
// rate.
//
// Unlike telemetrygen (single service per process), loadgen emits cross-service
// parent/child traces and producer/consumer pairs, so it exercises BOTH rollups
// — service_rollup and edge_rollup (call + messaging topology) — plus the
// GROUP BY cardinality cost. It reuses fanout's own vendored OTLP proto, so it
// adds no new dependencies.
//
// Example — a 10-minute soak at 2k traces/s across 50 services, watching for
// the file/snapshot accumulation that took prod down:
//
//	go run ./cmd/loadgen -rate 2000 -duration 10m -services 50 -attr-cardinality 200
//	# in another shell: watch -n5 'curl -s localhost:7520/-/metrics | grep -E "fanout_lake_partitions|fanout_ingest_queue_depth|fanout_rollup_last_success"'
//
// Run fanout locally with PUBLIC_READ=true for tokenless ingest, or pass -token.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
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
	endpoint    string
	token       string
	rate        float64
	duration    time.Duration
	workers     int
	services    int
	namespaces  int
	cardinality int
	errorRate   float64
	msgRatio    float64
	sendLogs    bool
	sendMetrics bool
}

func main() {
	var cfg config
	flag.StringVar(&cfg.endpoint, "endpoint", "localhost:4317", "OTLP gRPC endpoint")
	flag.StringVar(&cfg.token, "token", "", "ingest token (x-fanout-ingest-token); omit when fanout runs with PUBLIC_READ=true")
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
		cfg:     cfg,
		traces:  collectortrace.NewTraceServiceClient(conn),
		logs:    collectorlogs.NewLogsServiceClient(conn),
		metrics: collectormetrics.NewMetricsServiceClient(conn),
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

	// Per-worker pacing: each worker sends one trace every `interval`.
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

	// Progress line every 5s until the run ends.
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
				fmt.Printf("  %5.0fs  traces=%d (%.0f/s)  spans=%d  errors=%d\n",
					elapsed, sent, float64(sent)/elapsed, g.spansSent.Load(), g.sendErrs.Load())
			}
		}
	}()

	wg.Wait()
	<-done
	elapsed := time.Since(start).Seconds()
	fmt.Printf("\ndone: %.1fs | traces=%d spans=%d logs=%d metrics=%d | send errors=%d | avg %.0f traces/s\n",
		elapsed, g.tracesSent.Load(), g.spansSent.Load(), g.logsSent.Load(), g.metricsSent.Load(),
		g.sendErrs.Load(), float64(g.tracesSent.Load())/elapsed)
}

type generator struct {
	cfg      config
	traces   collectortrace.TraceServiceClient
	logs     collectorlogs.LogsServiceClient
	metrics  collectormetrics.MetricsServiceClient
	svcNames []string

	tracesSent  atomic.Int64
	spansSent   atomic.Int64
	logsSent    atomic.Int64
	metricsSent atomic.Int64
	sendErrs    atomic.Int64
	lastErr     atomic.Pointer[error]
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
	now := time.Now()
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

	_, err := g.traces.Export(g.outCtx(ctx), &collectortrace.ExportTraceServiceRequest{ResourceSpans: resSpans})
	if err != nil {
		g.countSendErr(ctx, err)
		return
	}
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
				TimeUnixNano:   uint64(time.Now().UnixNano()),
				SeverityText:   sev,
				SeverityNumber: sevNum,
				Body:           &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "request " + g.route()}},
				Attributes:     g.spanAttrs(),
			}},
		}},
	}}}
	if _, err := g.logs.Export(g.outCtx(ctx), req); err != nil {
		g.countSendErr(ctx, err)
		return
	}
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
						TimeUnixNano: uint64(time.Now().UnixNano()),
						Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: rand.Float64() * 500},
						Attributes:   g.spanAttrs(),
					}},
				}},
			}},
		}},
	}}}
	if _, err := g.metrics.Export(g.outCtx(ctx), req); err != nil {
		g.countSendErr(ctx, err)
		return
	}
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
