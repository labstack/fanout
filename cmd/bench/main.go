// Command bench is fanout's local stress/soak generator and benchmark
// reporter. It pushes synthetic OTLP (traces, logs, metrics) straight at the
// gRPC ingest port with knobs for rate, duration, service/namespace count,
// attribute cardinality, and error rate, then reports OTLP export latency
// percentiles and — when pointed at fanout's /-/metrics — the server-side
// deltas that matter (rows accepted/dropped, file growth, rollup/flush time).
//
// Unlike telemetrygen (single service per process), bench emits cross-service
// parent/child traces and producer/consumer pairs, so it exercises BOTH rollups
// — service_rollup and edge_rollup (call + messaging topology) — plus the
// GROUP BY cardinality cost. It reuses fanout's own vendored OTLP proto, so it
// adds no new dependencies.
//
// Example — a 10-minute soak at 2k traces/s across 50 services, with a report:
//
//	go run ./cmd/bench -rate 2000 -duration 10m -services 50 -attr-cardinality 200 \
//	  -token "$INGEST_TOKEN" \
//	  -metrics-url http://localhost:7520/-/metrics -metrics-token "$FANOUT_METRICS_TOKEN" -report run.json
//
// The metrics endpoint requires -metrics-token unless FANOUT_METRICS_PUBLIC=true.
package main

import (
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
	"sort"
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
	// querySessionCookie is the Cookie header from a signed-in Fanout account.
	// It is deliberately omitted from reports and logs.
	querySessionCookie string
	// Pass/fail thresholds (0 = no threshold). Non-zero exit on violation so
	// the harness can gate CI / releases.
	maxExportP95 float64
	maxQueryP95  float64
	// backfillHours, when >0, spreads each event's timestamp uniformly over the
	// last N hours (instead of "now"). Used to PRE-SEED a multi-hour dataset so
	// Parquet spans several hour partitions — required to exercise within-day
	// (hour-partition) pruning, which a same-hour run can't.
	backfillHours float64
	// seed makes the synthetic workload reproducible: same seed, same services,
	// endpoints, attributes, and error placement. Two runs are only comparable if
	// they share it.
	seed uint64
	// fanoutVersion is recorded in the report so a stored run.json says which
	// server build produced it. Report-only; it never changes Fanout config.
	fanoutVersion string
	// stepDuration is how long each adaptive step runs. Unused when -rate is
	// set explicitly, which pins the harness to a single fixed-rate trial.
	stepDuration time.Duration
}

// adaptive reports whether this run discovers its own rate. An explicit -rate
// pins the harness to one trial, which is what a regression gate wants; the
// default ramps, which is what a capacity question wants.
func (c config) adaptive() bool { return c.rate <= 0 }

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var cfg config
	flag.StringVar(&cfg.endpoint, "endpoint", "localhost:4317", "OTLP gRPC endpoint")
	flag.StringVar(&cfg.token, "token", "", "ingest bearer token; required")
	flag.Float64Var(&cfg.rate, "rate", 0, "target traces per second (aggregate); 0 ramps adaptively to find what this server sustains")
	flag.DurationVar(&cfg.duration, "duration", time.Minute, "run duration for a fixed -rate run; 0 means run until interrupted")
	flag.IntVar(&cfg.workers, "workers", 0, "concurrent senders; 0 sizes them from the driver's cores")
	flag.DurationVar(&cfg.stepDuration, "step", 30*time.Second, "duration of each step while ramping adaptively")
	flag.IntVar(&cfg.services, "services", 20, "number of distinct services")
	flag.IntVar(&cfg.namespaces, "namespaces", 1, "number of distinct namespaces")
	flag.IntVar(&cfg.cardinality, "attr-cardinality", 100, "distinct values per high-cardinality attribute")
	flag.Float64Var(&cfg.errorRate, "error-rate", 0.05, "fraction of spans marked STATUS_CODE_ERROR (0..1)")
	flag.Float64Var(&cfg.msgRatio, "messaging-ratio", 0.2, "fraction of traces that also emit a producer/consumer pair (0..1)")
	flag.BoolVar(&cfg.sendLogs, "logs", true, "also emit logs")
	flag.BoolVar(&cfg.sendMetrics, "metrics", true, "also emit metrics")
	flag.StringVar(&cfg.metricsURL, "metrics-url", "", "fanout /-/metrics URL to capture server-side deltas (e.g. http://localhost:7520/-/metrics)")
	flag.StringVar(&cfg.metricsToken, "metrics-token", "", "bearer token for a private fanout /-/metrics endpoint")
	flag.StringVar(&cfg.reportPath, "report", "", "write a JSON performance report to this path")
	flag.StringVar(&cfg.queryURL, "query-url", "", "fanout HTTP base URL to drive read load under ingest (e.g. http://localhost:7520)")
	flag.IntVar(&cfg.queryWorkers, "query-workers", 0, "concurrent query workers (0 = ingest only)")
	flag.Float64Var(&cfg.queryRate, "query-rate", 50, "target queries/sec (aggregate) when query-workers > 0")
	flag.StringVar(&cfg.querySessionCookie, "query-session-cookie", "", "Cookie header from an authenticated Fanout account; required when query-workers > 0")
	flag.Float64Var(&cfg.maxExportP95, "max-export-p95-ms", 0, "fail (exit 1) if ingest export p95 exceeds this")
	flag.Float64Var(&cfg.maxQueryP95, "max-query-p95-ms", 0, "fail (exit 1) if query p95 exceeds this")
	flag.Float64Var(&cfg.backfillHours, "backfill-hours", 0, "spread event timestamps over the last N hours (pre-seed a multi-hour dataset); 0 = use now()")
	flag.Uint64Var(&cfg.seed, "seed", 1, "deterministic synthetic workload seed")
	flag.StringVar(&cfg.fanoutVersion, "fanout-version", "unknown", "Fanout build/version identifier under test, recorded in the report")
	flag.Parse()

	// Self-size before validation: a zero worker count is how the operator asks
	// for auto-sizing, but every trial downstream needs a concrete number.
	if cfg.workers <= 0 {
		cfg.workers = autoWorkers(numCPU())
	}
	if err := validateConfig(cfg); err != nil {
		log.Fatal(err)
	}

	// No global deadline: in adaptive mode the run is a sequence of steps, each
	// bounded separately. Signals still cut the whole thing short.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	conn, err := grpc.NewClient(cfg.endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial %s: %v", cfg.endpoint, err)
	}
	defer conn.Close()

	fmt.Printf("bench → %s | workers=%d services=%d namespaces=%d cardinality=%d error-rate=%.2f\n",
		cfg.endpoint, cfg.workers, cfg.services, cfg.namespaces, cfg.cardinality, cfg.errorRate)

	var baseline *metricSnapshot
	var infrastructureFailures []string

	// Session clock, spanning the ramp and the confirmation pass. Each trial
	// keeps its own clock for its own rate; the manifest describes the whole run.
	start := time.Now()

	// Ramp first to find what this server sustains, then confirm at that rate.
	// The headline numbers therefore describe a rate the machine actually holds,
	// not the saturated step that ended the ramp.
	var steps []rampStep
	var stopReason string
	if cfg.adaptive() {
		policy := defaultRampPolicy()
		fmt.Printf("adaptive: ramping in %s steps (%d max) to find the sustainable rate\n", cfg.stepDuration, policy.MaxSteps)
		for {
			decision := decideNextStep(steps, policy)
			if decision.Stop {
				stopReason = decision.Reason
				break
			}
			trial := cfg
			trial.rate = decision.NextRate
			trial.duration = cfg.stepDuration
			sg, selapsed := runTrial(ctx, trial, conn, true)
			step := rampStep{
				TargetRate:   round2(decision.NextRate),
				AchievedRate: round2(float64(sg.tracesSent.Load()) / selapsed),
				ExportP95Ms:  round2(sg.lat.quantile(0.95)),
			}
			if trial.queryURL != "" && trial.queryWorkers > 0 {
				step.QueryP95Ms = round2(sg.queryLat.quantile(0.95))
			}
			steps = append(steps, step)
			fmt.Printf("  step %d: offered %.0f/s → sustained %.0f/s, export p95 %.1fms\n",
				len(steps), step.TargetRate, step.AchievedRate, step.ExportP95Ms)
			if ctx.Err() != nil {
				stopReason = "interrupted"
				break
			}
		}
		cfg.rate = sustainableRate(steps, policy)
		if cfg.rate <= 0 {
			cfg.rate = seedRate(numCPU())
		}
		fmt.Printf("adaptive: stopped because %s — sustainable %.0f/s, peak %.0f/s. Confirming at %.0f/s for %s.\n",
			stopReason, cfg.rate, saturationRate(steps), cfg.rate, cfg.duration)
	}

	// Baseline immediately before the confirmation pass, not before the ramp.
	// Every server-side rate is a delta divided by the confirmation pass's
	// elapsed time, so a baseline taken earlier would fold the entire ramp into
	// the numerator and report, say, 3.26 busy cores on a two-core machine.
	if cfg.metricsURL != "" {
		if baseline, err = scrapeMetrics(cfg.metricsURL, cfg.metricsToken); err != nil {
			fmt.Fprintf(os.Stderr, "  warn: baseline metrics scrape failed: %v\n", err)
			infrastructureFailures = append(infrastructureFailures, "baseline metrics scrape failed")
		}
	}

	g, elapsed := runTrial(ctx, cfg, conn, false)

	rep := report{
		Manifest:        newRunManifest(cfg, start.UTC(), time.Now().UTC()),
		Endpoint:        cfg.endpoint,
		DurationSec:     round2(elapsed),
		TargetRate:      cfg.rate,
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
			infrastructureFailures = append(infrastructureFailures, "final metrics scrape failed")
		} else {
			rep.Server = serverDelta(baseline, final, elapsed)
		}
	}

	if cfg.adaptive() || len(steps) > 0 {
		policy := defaultRampPolicy()
		rampEstimate := sustainableRate(steps, policy)
		confirmed := rep.AvgTracesPerSec
		sustainable := rampEstimate
		if confirmed > 0 && confirmed < sustainable {
			sustainable = confirmed
		}
		rep.Capacity = &capacityReport{
			Steps:                    steps,
			SustainableTracesPerSec:  round2(sustainable),
			RampEstimateTracesPerSec: round2(rampEstimate),
			ConfirmedTracesPerSec:    round2(confirmed),
			SaturationTracesPerSec:   round2(saturationRate(steps)),
			StopReason:               stopReason,
			Workers:                  cfg.workers,
			DriverLogicalCPUs:        numCPU(),
		}
	}

	rep.Failures = evaluateReport(cfg, rep, infrastructureFailures)
	rep.Passed = len(rep.Failures) == 0
	printReport(rep)

	// A report that was asked for and not written is a failed run, not a warning.
	// Callers that aggregate trials cannot tell "missing" from "fine" after the
	// fact, so exiting 0 here turns a lost trial into a silent pass upstream.
	reportWritten := true
	if cfg.reportPath != "" {
		if err := writeReport(cfg.reportPath, rep); err != nil {
			fmt.Fprintf(os.Stderr, "  error: write report: %v\n", err)
			reportWritten = false
		} else {
			fmt.Printf("report written: %s\n", cfg.reportPath)
		}
	}

	if !rep.Passed {
		fmt.Fprintf(os.Stderr, "FAIL: %s\n", strings.Join(rep.Failures, "; "))
		os.Exit(1)
	}
	if !reportWritten {
		os.Exit(1)
	}
}

// runTrial executes one fixed-rate trial and returns its generator and the
// wall-clock seconds it covered. Each trial gets a fresh generator so its
// histograms describe that rate alone; a shared one would blend every step of
// a ramp into a single indistinguishable distribution.
//
// quiet suppresses per-5s progress lines, which are noise during a ramp and
// useful during the confirmation pass.
func runTrial(ctx context.Context, cfg config, conn *grpc.ClientConn, quiet bool) (*generator, float64) {
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

	trialCtx := ctx
	if cfg.duration > 0 {
		var cancel context.CancelFunc
		trialCtx, cancel = context.WithTimeout(ctx, cfg.duration)
		defer cancel()
	}

	interval := time.Duration(float64(time.Second) * float64(cfg.workers) / cfg.rate)
	start := time.Now()

	var wg sync.WaitGroup
	for w := 0; w < cfg.workers; w++ {
		worker := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(cfg.seed, uint64(worker)+1))
			g.run(trialCtx, interval, rng)
		}()
	}
	if cfg.queryURL != "" && cfg.queryWorkers > 0 {
		qInterval := time.Duration(float64(time.Second) * float64(cfg.queryWorkers) / cfg.queryRate)
		if !quiet {
			fmt.Printf("query load: %d workers @ %.0f q/s → %s/api/observability/{overview,topology,performance,trace,logs}\n",
				cfg.queryWorkers, cfg.queryRate, strings.TrimRight(cfg.queryURL, "/"))
		}
		for w := 0; w < cfg.queryWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				g.runQueries(trialCtx, qInterval)
			}()
		}
	}

	done := make(chan struct{})
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-trialCtx.Done():
				close(done)
				return
			case <-t.C:
				if quiet {
					continue
				}
				elapsed := time.Since(start).Seconds()
				sent := g.tracesSent.Load()
				fmt.Printf("  %5.0fs  traces=%d (%.0f/s)  spans=%d  p95=%.1fms  errors=%d\n",
					elapsed, sent, float64(sent)/elapsed, g.spansSent.Load(), g.lat.quantile(0.95), g.sendErrs.Load())
			}
		}
	}()

	wg.Wait()
	<-done
	return g, time.Since(start).Seconds()
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
var (
	queryOperations = []string{"overview", "topology", "performance", "trace", "logs"}
	queryWindows    = []string{"15m", "1h", "6h", "12h", "24h"}
)

type queryTarget struct {
	operation string
	path      string
}

func queryTargetAt(i int) queryTarget {
	operationIndex := i % len(queryOperations)
	round := i / len(queryOperations)
	// Offset each successive round so every operation exercises every window;
	// using i for both dimensions locks equal-length lists into fixed pairs.
	window := queryWindows[(operationIndex+round)%len(queryWindows)]
	operation := queryOperations[operationIndex]
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
			// Bound to the run context so a query issued just before the
			// deadline is cancelled rather than running on for the client
			// timeout, which both delays the final scrape and hides its own
			// failure (ctx.Err() is non-nil by the time it returns).
			req, reqErr := g.newQueryRequest(ctx, url)
			if reqErr != nil {
				g.queryErrs.Add(1)
				reportFailure(target.operation, reqErr.Error())
				continue
			}
			resp, err := g.http.Do(req)
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

func (g *generator) newQueryRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", g.cfg.querySessionCookie)
	return req, nil
}

func (g *generator) run(ctx context.Context, interval time.Duration, rng *rand.Rand) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.sendTrace(ctx, rng)
			if g.cfg.sendLogs {
				g.sendLog(ctx, rng)
			}
			if g.cfg.sendMetrics {
				g.sendMetric(ctx, rng)
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
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+g.cfg.token)
}

// eventTime returns the timestamp for an emitted event: now(), or — when
// backfillHours>0 — a time spread uniformly over the last N hours so a pre-seed
// run populates multiple hour partitions (to exercise within-day pruning).
func (g *generator) eventTime(rng *rand.Rand) time.Time {
	if g.cfg.backfillHours <= 0 {
		return time.Now()
	}
	return time.Now().Add(-time.Duration(rng.Float64() * g.cfg.backfillHours * float64(time.Hour)))
}

// sendTrace emits one trace: a SERVER span in the caller service and a CLIENT
// child span in a different callee service (a call edge), plus — for a fraction
// of traces — a PRODUCER/CONSUMER pair on a messaging destination (a messaging
// edge). Each ResourceSpans carries one service.name, matching how fanout keys
// service_rollup and edge_rollup.
func (g *generator) sendTrace(ctx context.Context, rng *rand.Rand) {
	ns := g.namespace(rng)
	caller := g.svcNames[rng.IntN(g.cfg.services)]
	callee := g.svcNames[rng.IntN(g.cfg.services)]
	for callee == caller {
		callee = g.svcNames[rng.IntN(g.cfg.services)]
	}

	traceID := randBytes(rng, 16)
	parentID := randBytes(rng, 8)
	childID := randBytes(rng, 8)
	now := g.eventTime(rng)
	startNano := uint64(now.UnixNano())
	parentEnd := uint64(now.Add(time.Duration(20+rng.IntN(400)) * time.Millisecond).UnixNano())
	childEnd := uint64(now.Add(time.Duration(5+rng.IntN(200)) * time.Millisecond).UnixNano())

	resSpans := []*tracepb.ResourceSpans{
		g.resourceSpans(caller, ns, &tracepb.Span{
			TraceId: traceID, SpanId: parentID,
			Name: "GET /" + g.route(rng), Kind: tracepb.Span_SPAN_KIND_SERVER,
			StartTimeUnixNano: startNano, EndTimeUnixNano: parentEnd,
			Attributes: g.spanAttrs(rng), Status: g.status(rng),
		}),
		g.resourceSpans(callee, ns, &tracepb.Span{
			TraceId: traceID, SpanId: childID, ParentSpanId: parentID,
			Name: "rpc." + g.route(rng), Kind: tracepb.Span_SPAN_KIND_CLIENT,
			StartTimeUnixNano: startNano, EndTimeUnixNano: childEnd,
			Attributes: g.spanAttrs(rng), Status: g.status(rng),
		}),
	}
	spans := 2

	if rng.Float64() < g.cfg.msgRatio {
		dest := fmt.Sprintf("topic-%d", rng.IntN(8))
		msgAttrs := []*common.KeyValue{
			strAttr("messaging.destination.name", dest),
			strAttr("messaging.system", "kafka"),
		}
		producer := g.svcNames[rng.IntN(g.cfg.services)]
		consumer := g.svcNames[rng.IntN(g.cfg.services)]
		for consumer == producer {
			consumer = g.svcNames[rng.IntN(g.cfg.services)]
		}
		resSpans = append(resSpans,
			g.resourceSpans(producer, ns, &tracepb.Span{
				TraceId: randBytes(rng, 16), SpanId: randBytes(rng, 8),
				Name: dest + " publish", Kind: tracepb.Span_SPAN_KIND_PRODUCER,
				StartTimeUnixNano: startNano, EndTimeUnixNano: parentEnd,
				Attributes: msgAttrs, Status: g.status(rng),
			}),
			g.resourceSpans(consumer, ns, &tracepb.Span{
				TraceId: randBytes(rng, 16), SpanId: randBytes(rng, 8),
				Name: dest + " process", Kind: tracepb.Span_SPAN_KIND_CONSUMER,
				StartTimeUnixNano: startNano, EndTimeUnixNano: childEnd,
				Attributes: msgAttrs, Status: g.status(rng),
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
			Scope: &common.InstrumentationScope{Name: "bench"},
			Spans: []*tracepb.Span{span},
		}},
	}
}

func (g *generator) sendLog(ctx context.Context, rng *rand.Rand) {
	ns := g.namespace(rng)
	svc := g.svcNames[rng.IntN(g.cfg.services)]
	sev := "INFO"
	sevNum := logspb.SeverityNumber_SEVERITY_NUMBER_INFO
	if rng.Float64() < g.cfg.errorRate {
		sev, sevNum = "ERROR", logspb.SeverityNumber_SEVERITY_NUMBER_ERROR
	}
	req := &collectorlogs.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{
		Resource: &resourcepb.Resource{Attributes: []*common.KeyValue{
			strAttr("service.name", svc), strAttr("service.namespace", ns),
		}},
		ScopeLogs: []*logspb.ScopeLogs{{
			LogRecords: []*logspb.LogRecord{{
				TimeUnixNano:   uint64(g.eventTime(rng).UnixNano()),
				SeverityText:   sev,
				SeverityNumber: sevNum,
				Body:           &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "request " + g.route(rng)}},
				Attributes:     g.spanAttrs(rng),
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

func (g *generator) sendMetric(ctx context.Context, rng *rand.Rand) {
	ns := g.namespace(rng)
	svc := g.svcNames[rng.IntN(g.cfg.services)]
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
						TimeUnixNano: uint64(g.eventTime(rng).UnixNano()),
						Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: rng.Float64() * 500},
						Attributes:   g.spanAttrs(rng),
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

func (g *generator) namespace(rng *rand.Rand) string {
	if g.cfg.namespaces <= 1 {
		return "default"
	}
	return fmt.Sprintf("ns-%02d", rng.IntN(g.cfg.namespaces))
}

func (g *generator) route(rng *rand.Rand) string {
	return fmt.Sprintf("r%d", rng.IntN(20))
}

// spanAttrs carries a high-cardinality key to stress the rollup GROUP BYs and
// attribute extraction.
func (g *generator) spanAttrs(rng *rand.Rand) []*common.KeyValue {
	return []*common.KeyValue{
		strAttr("http.method", []string{"GET", "POST", "PUT", "DELETE"}[rng.IntN(4)]),
		strAttr("user.id", fmt.Sprintf("u-%d", rng.IntN(g.cfg.cardinality))),
	}
}

func (g *generator) status(rng *rand.Rand) *tracepb.Status {
	if rng.Float64() < g.cfg.errorRate {
		return &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR}
	}
	return &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK}
}

func strAttr(k, v string) *common.KeyValue {
	return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: v}}}
}

func randBytes(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.IntN(256))
	}
	return b
}

// ── Export-latency histogram ───────────────────────────────────────────────
// Lock-free, fixed-bucket (log-spaced ms) so it's safe and bounded under a
// multi-hour soak. Percentiles are bucket-granular approximations (the upper
// bound of the bucket the quantile falls in).

// A 0.5% geometric step keeps quantile error an order of magnitude below the
// smallest change worth acting on, while staying fixed-size and bounded for
// multi-hour soaks. The old 20-bucket ladder (…610, 1000, 2000, 5000…) could
// not resolve anything finer than a doubling: two identical runs would report
// p95 as 1000ms and 2000ms purely from which side of a boundary they landed.
// Keep the release SLO as an exact boundary so values at or below it cannot
// round up into a failing bucket.
var latBoundsMs = buildLatencyBounds()

func buildLatencyBounds() []float64 {
	const (
		first = 0.5
		last  = 30000.0
		ratio = 1.005
	)
	bounds := make([]float64, 0, 2300)
	for value := first; value < last; value *= ratio {
		bounds = append(bounds, value)
	}
	// An explicit boundary at 1.5s keeps query latency legible around the point
	// where a dashboard stops feeling interactive. Histogram resolution, not a
	// threshold — nothing passes or fails here.
	const queryLegibilityBoundMs = 1500
	bounds = append(bounds, queryLegibilityBoundMs, last)
	sort.Float64s(bounds)

	unique := bounds[:0]
	for _, bound := range bounds {
		if len(unique) == 0 || bound != unique[len(unique)-1] {
			unique = append(unique, bound)
		}
	}
	return unique
}

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
	index := sort.SearchFloat64s(latBoundsMs, ms)
	if index < len(latBoundsMs) {
		h.counts[index].Add(1)
		return
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

// ── Report ─────────────────────────────────────────────────────────────────

type report struct {
	Manifest runManifest `json:"manifest"`
	Passed   bool        `json:"passed"`
	Failures []string    `json:"failures,omitempty"`
	Endpoint string      `json:"endpoint"`
	// Elapsed wall clock, as opposed to the requested duration recorded in
	// Manifest.Workload. Worker/service counts live in the manifest only.
	DurationSec             float64                  `json:"duration_sec"`
	TargetRate              float64                  `json:"target_rate"`
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
	Capacity                *capacityReport          `json:"capacity,omitempty"`
}

// capacityReport is what an adaptive run discovered about the machine. Absent
// from a fixed -rate run, which asserts a rate rather than discovering one.
type capacityReport struct {
	Steps []rampStep `json:"steps"`
	// SustainableTracesPerSec is the rate to quote: the lower of what the ramp
	// found and what the confirmation pass actually held.
	//
	// They diverge when capacity is not stationary. Rollup and merge cost grows
	// with the stored dataset, so a ramp can measure a machine whose capacity is
	// falling underneath it and hand back a rate that no longer holds minutes
	// later. Quoting the confirmed figure keeps the headline number one that was
	// demonstrated for a full pass at the largest dataset of the run.
	SustainableTracesPerSec float64 `json:"sustainable_traces_per_sec"`
	// RampEstimateTracesPerSec is what the ramp concluded before confirmation.
	RampEstimateTracesPerSec float64 `json:"ramp_estimate_traces_per_sec"`
	// ConfirmedTracesPerSec is what the confirmation pass held.
	ConfirmedTracesPerSec float64 `json:"confirmed_traces_per_sec"`
	// SaturationTracesPerSec is the most throughput seen at any offered rate,
	// including rates the server could not keep up with.
	SaturationTracesPerSec float64 `json:"saturation_traces_per_sec"`
	StopReason             string  `json:"stop_reason"`
	// Driver-side context: a ramp is only as trustworthy as the machine that
	// generated it, so record what was doing the sending.
	Workers           int `json:"driver_workers"`
	DriverLogicalCPUs int `json:"driver_logical_cpus"`
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
	BaselineAvailable        bool                                 `json:"baseline_available"`
	ProcessStartTime         float64                              `json:"process_start_time_seconds"`
	ProcessRestarted         bool                                 `json:"process_restarted"`
	IngestRowsStart          float64                              `json:"ingest_rows_start"`
	IngestRowsEnd            float64                              `json:"ingest_rows_end"`
	IngestRowsDelta          float64                              `json:"ingest_rows_delta"`
	RowsDroppedStart         float64                              `json:"rows_dropped_start"`
	RowsDroppedEnd           float64                              `json:"rows_dropped_end"`
	RowsDroppedDelta         float64                              `json:"rows_dropped_delta"`
	ParquetFilesStart        float64                              `json:"parquet_partitions_start"`
	ParquetFiles             float64                              `json:"parquet_partitions"`
	ParquetFilesDelta        float64                              `json:"parquet_partitions_delta"`
	ParquetSizeBytesStart    float64                              `json:"parquet_size_bytes_start"`
	ParquetSizeBytes         float64                              `json:"parquet_size_bytes"`
	ParquetSizeBytesDelta    float64                              `json:"parquet_size_bytes_delta"`
	ParquetGrowthBytesPerSec float64                              `json:"parquet_growth_bytes_per_sec"`
	IngestQueueDepth         float64                              `json:"ingest_queue_depth"`
	AvgRollupMs              float64                              `json:"avg_rollup_ms"`
	AvgFlushMs               float64                              `json:"avg_flush_ms"`
	AvgQueryMs               float64                              `json:"avg_query_ms"`
	CPUSecondsStart          float64                              `json:"cpu_seconds_start"`
	CPUSecondsEnd            float64                              `json:"cpu_seconds_end"`
	CPUSecondsDelta          float64                              `json:"cpu_seconds_delta"`
	CPUCores                 float64                              `json:"cpu_cores"`
	RSSBytes                 float64                              `json:"rss_bytes"`
	HeapAllocBytes           float64                              `json:"heap_alloc_bytes"`
	AllocBytesStart          float64                              `json:"alloc_bytes_start"`
	AllocBytesEnd            float64                              `json:"alloc_bytes_end"`
	AllocBytesDelta          float64                              `json:"alloc_bytes_delta"`
	AllocBytesPerSec         float64                              `json:"alloc_bytes_per_sec"`
	GCPauseSecondsStart      float64                              `json:"gc_pause_seconds_start"`
	GCPauseSecondsEnd        float64                              `json:"gc_pause_seconds_end"`
	GCPauseSecondsDelta      float64                              `json:"gc_pause_seconds_delta"`
	WriteGateWaitMs          map[string]distributionReport        `json:"write_gate_wait_ms,omitempty"`
	WriteGateHoldMs          map[string]distributionReport        `json:"write_gate_hold_ms,omitempty"`
	TelemetryOperations      map[string]backgroundOperationReport `json:"telemetry_operations,omitempty"`
	Rollups                  map[string]rollupReport              `json:"rollups,omitempty"`
}

func printReport(r report) {
	fmt.Printf("\n── performance report ──────────────────────────────────────\n")
	fmt.Printf("duration       %.1fs  (target %.0f traces/s → actual %.0f)\n", r.DurationSec, r.TargetRate, r.AvgTracesPerSec)
	fmt.Printf("sent           traces=%d spans=%d logs=%d metrics=%d\n", r.TracesSent, r.SpansSent, r.LogsSent, r.MetricsSent)
	fmt.Printf("send errors    %d\n", r.SendErrors)
	if c := r.Capacity; c != nil {
		fmt.Printf("capacity       sustainable=%.0f/s  (ramp estimate=%.0f/s, confirmed=%.0f/s, peak=%.0f/s)\n",
			c.SustainableTracesPerSec, c.RampEstimateTracesPerSec, c.ConfirmedTracesPerSec, c.SaturationTracesPerSec)
		// A confirmation well under the ramp's estimate is the signature of
		// capacity falling as the dataset grows, not of a bad measurement.
		if c.RampEstimateTracesPerSec > 0 && c.ConfirmedTracesPerSec < c.RampEstimateTracesPerSec*0.9 {
			fmt.Printf("               note: confirmation held %.0f%% of the ramp estimate — capacity fell as the dataset grew\n",
				100*c.ConfirmedTracesPerSec/c.RampEstimateTracesPerSec)
		}
	}
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
		fmt.Printf("  parquet_partitions=%.0f  parquet_size=%.1fMB  ingest_queue_depth=%.0f\n",
			s.ParquetFiles, s.ParquetSizeBytes/(1<<20), s.IngestQueueDepth)
		fmt.Printf("  avg rollup=%.1fms  flush=%.1fms  query=%.1fms\n", s.AvgRollupMs, s.AvgFlushMs, s.AvgQueryMs)
		fmt.Printf("  cpu=%.2f core(s)  rss=%.1fMB  alloc=%.1fMB/s  parquet_growth=%.1fMB\n",
			s.CPUCores, s.RSSBytes/(1<<20), s.AllocBytesPerSec/(1<<20), s.ParquetSizeBytesDelta/(1<<20))
	}
	if r.Passed {
		fmt.Printf("verdict        PASS\n")
	} else {
		fmt.Printf("verdict        FAIL (%s)\n", strings.Join(r.Failures, "; "))
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
