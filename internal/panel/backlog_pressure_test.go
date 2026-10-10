//go:build readbench

package panel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/prometheus/client_golang/prometheus"
)

// The deadline includes seeding. All disposable files stay in the private workspace.
func pressureBatches(t *testing.T) int {
	t.Helper()
	if raw := os.Getenv("FANOUT_BACKLOG_BATCHES"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			t.Fatalf("invalid FANOUT_BACKLOG_BATCHES %q", raw)
		}
		return n
	}
	return 4096
}
func pressureDuration(t *testing.T) time.Duration {
	t.Helper()
	if raw := os.Getenv("FANOUT_BACKLOG_DURATION"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			t.Fatalf("invalid FANOUT_BACKLOG_DURATION %q", raw)
		}
		return d
	}
	return 2 * time.Minute
}
func pressureBatch(batch int, base time.Time, live bool) telemetrystore.Batch {
	prefix := "pressure"
	if live {
		prefix = "live"
	}
	b := telemetrystore.Batch{ID: fmt.Sprintf("%s-%06d", prefix, batch)}
	for i := range 512 {
		n := base.Add(-24*time.Hour + time.Duration((batch*512+i)%86400)*time.Second).UnixNano()
		ingested := base.Add(time.Duration(batch) * time.Millisecond).UnixNano()
		if live {
			n = base.Add(-time.Second).UnixNano()
			if batch%2 == 0 {
				n = base.Add(-12 * time.Hour).UnixNano()
			}
			ingested = base.Add(time.Duration(batch+1) * time.Second).UnixNano()
		}
		version := "v1"
		if i%2 == 0 {
			version = "v2"
		}
		service := fmt.Sprintf("svc%d", i%6)
		trace := fmt.Sprintf("%s-trace-%d-%d", prefix, batch, i/4)
		b.Spans = append(b.Spans, telemetry.Span{Namespace: "pressure", ServiceName: service, TraceID: trace, SpanID: fmt.Sprintf("s%d", i), StartUnixNanos: n, EndUnixNanos: n + int64(i%1000+1)*1000000, DurationMS: float64(i%1000 + 1), Kind: "SPAN_KIND_SERVER", StatusCode: "STATUS_CODE_OK", ServiceVersion: version, IngestedAt: ingested})
		if i < 192 {
			b.Logs = append(b.Logs, telemetry.Log{Namespace: "pressure", ServiceName: service, TraceID: trace, EventUnixNanos: n, TimeUnixNanos: n, IngestedAt: ingested, Body: "request complete", Severity: "INFO"})
		}
	}
	return b
}
func pressureError(t *testing.T, label string, started time.Time, err error) {
	t.Helper()
	var native *duckdb.Error
	typed := errors.As(err, &native)
	kind := "none"
	if typed {
		kind = fmt.Sprint(native.Type)
	}
	t.Logf("statement=%s started=%s ended=%s duration=%s error=%v native_type=%s canceled=%t deadline=%t", label, started.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), time.Since(started), err, kind, errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded))
}

type pressureLog struct{ t *testing.T }

func (h pressureLog) Enabled(context.Context, slog.Level) bool { return true }
func (h pressureLog) Handle(_ context.Context, r slog.Record) error {
	h.t.Logf("internal_log time=%s level=%s message=%q", r.Time.UTC().Format(time.RFC3339Nano), r.Level, r.Message)
	r.Attrs(func(a slog.Attr) bool {
		if err, ok := a.Value.Any().(error); ok {
			pressureError(h.t, "internal_log/"+r.Message, r.Time, err)
		} else {
			h.t.Logf("internal_attr %s=%v", a.Key, a.Value)
		}
		return true
	})
	return nil
}
func (h pressureLog) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h pressureLog) WithGroup(string) slog.Handler      { return h }

// Same /proc + ps methodology as processRSSMiB, but unsupported diagnostics
// are reported as inconclusive rather than skipping a pressure test.
var pressureRSSState struct {
	sync.Mutex
	blocked error
}

func pressureRSS() (float64, error) {
	pressureRSSState.Lock()
	defer pressureRSSState.Unlock()
	if pressureRSSState.blocked != nil {
		return 0, pressureRSSState.blocked
	}
	if raw, err := os.ReadFile("/proc/self/statm"); err == nil {
		f := strings.Fields(string(raw))
		if len(f) > 1 {
			n, e := strconv.ParseFloat(f[1], 64)
			return n * float64(os.Getpagesize()) / (1 << 20), e
		}
	}
	raw, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		pressureRSSState.blocked = err
		return 0, err
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
	return n / 1024, err
}
func pressureDiagnostics(ctx context.Context, t *testing.T, d *query.Duck, repo *telemetrystore.Repository, phase string) {
	t.Helper()
	stats := d.DB.Stats()
	var usage syscall.Rusage
	cpuErr := syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	cpuSeconds := float64(usage.Utime.Sec+usage.Stime.Sec) + float64(usage.Utime.Usec+usage.Stime.Usec)/1e6
	t.Logf("cpu phase=%s seconds=%.6f error=%v", phase, cpuSeconds, cpuErr)
	rss, rssErr := pressureRSS()
	var goMem runtime.MemStats
	runtime.ReadMemStats(&goMem)
	t.Logf("diagnostics phase=%s platform=%s/%s cpus=%d rss_mib=%.2f rss_error=%v go_heap_bytes=%d go_total_alloc_bytes=%d read_pool=%+v", phase, runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), rss, rssErr, goMem.HeapAlloc, goMem.TotalAlloc, stats)
	files, err := repo.Parquet.Stats()
	t.Logf("files phase=%s stats=%+v error=%v active_batches=%d rows=%d", phase, files, err, len(repo.Parquet.BatchMetadata()), repo.RowCount())
	// Never queue diagnostics behind the pressure workers. Five read slots are
	// fixed for all phases; busy samples are recorded and retried between phases.
	if stats.InUse >= stats.MaxOpenConnections {
		t.Logf("diagnostics phase=%s sql_sample=busy", phase)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for _, q := range []string{
		"SELECT version(),current_setting('memory_limit'),current_setting('threads'),current_setting('temp_directory')<>''",
		"SELECT tag,memory_usage_bytes,temporary_storage_bytes FROM duckdb_memory() ORDER BY tag",
		"SELECT coalesce(sum(size),0) FROM duckdb_temporary_files()",
		"SELECT cache_key,last_ingested_unix_nano FROM rollup_state ORDER BY cache_key",
		"SELECT (SELECT count(*) FROM read_batches),(SELECT count(*) FROM version_rollup_batches),(SELECT count(*) FROM version_rollup),(SELECT count(*) FROM anomaly_log)",
	} {
		rows, err := d.DB.QueryContext(ctx, q)
		if err != nil {
			pressureError(t, "diagnostics/"+q, time.Now(), err)
			continue
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Logf("diagnostic_columns error=%v", err)
			continue
		}
		for rows.Next() {
			v := make([]any, len(cols))
			p := make([]any, len(cols))
			for i := range v {
				p[i] = &v[i]
			}
			if err := rows.Scan(p...); err != nil {
				t.Logf("diagnostic_scan error=%v", err)
				break
			}
			t.Logf("diagnostic phase=%s columns=%v values=%v", phase, cols, v)
		}
		if err := rows.Err(); err != nil {
			pressureError(t, "diagnostics/rows", time.Now(), err)
		}
		rows.Close()
	}
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Logf("metrics error=%v", err)
		return
	}
	for _, f := range families {
		if f.GetName() == "fanout_write_gate_wait_seconds" || f.GetName() == "fanout_write_gate_hold_seconds" {
			for _, m := range f.Metric {
				t.Logf("gate phase=%s metric=%s labels=%v count=%d seconds_sum=%.6f", phase, f.GetName(), m.Label, m.GetHistogram().GetSampleCount(), m.GetHistogram().GetSampleSum())
			}
		}
	}
}

func pressurePanelSeed(ctx context.Context, t *testing.T, batches int) (*query.Duck, *telemetrystore.Repository, time.Time) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".superpowers", "sdd", "2026-10-09-agent-dashboards-m4", "task-2-artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "pressure-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("scratch_state=%s", dir)
	base := time.Now().UTC().Truncate(time.Minute)
	cfg := config.Config{DataDir: dir, RetentionDays: 30, DuckDBMemory: "4GB", DuckDBThreads: 4, DuckDBMaxConns: 5, RollupInterval: time.Second}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := repo.Close(); err != nil {
			t.Errorf("repository close: %v", err)
		}
	})
	started := time.Now()
	seeded := 0
	defer func() {
		if seeded != batches {
			t.Logf("seed_duration=%s seed_batches=%d requested=%d seed_incomplete=true", time.Since(started), seeded, batches)
		}
	}()
	for batch := range batches {
		if err := ctx.Err(); err != nil {
			t.Fatal(err)
		}
		if err := repo.Commit(ctx, pressureBatch(batch, base, false)); err != nil {
			t.Fatal(err)
		}
		seeded++
		if seeded%256 == 0 {
			t.Logf("seed_progress batches=%d duration=%s", seeded, time.Since(started))
		}
	}
	t.Logf("seed_complete duration=%s spans=%d logs=%d", time.Since(started), batches*512, batches*192)
	opened := time.Now()
	d, err := query.NewDuck(ctx, cfg, repo)
	pressureError(t, "NewDuck", opened, err)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("engine close: %v", err)
		}
	})
	return d, repo, base
}

func pressurePanelDuck(ctx context.Context, t *testing.T, batches int) (*query.Duck, *telemetrystore.Repository, time.Time, time.Time) {
	d, repo, base := pressurePanelSeed(ctx, t, batches)
	return d, repo, base.Add(-24 * time.Hour), base
}
func pressureSpec(start, end time.Time) Dashboard {
	return Dashboard{Name: "Backlog pressure", Time: Time{From: &start, To: &end}, Panels: []Panel{
		{ID: "table", Title: "Service counts", Viz: "table", Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}}},
		{ID: "count", Title: "Span count", Viz: "stat", Query: &Query{From: "spans", Measures: []string{"count()"}}},
		{ID: "p95", Title: "Latency", Viz: "table", Query: &Query{From: "spans", Measures: []string{"p95(duration_ms)"}, By: []string{"service"}}},
		{ID: "logs", Title: "Log count", Viz: "stat", Query: &Query{From: "logs", Measures: []string{"count()"}}},
	}}
}

// Diagnostic runs precede the behavior regression so the actual failure, not
// an assumed batch count, selects its frozen workload.
func TestBacklogPressureMeasuresWindowedConcurrency(t *testing.T) { runPanelPressure(t, false) }
func TestPanelsRemainUsableDuringBacklog(t *testing.T)            { runPanelPressure(t, true) }
func runPanelPressure(t *testing.T, assertUsable bool) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()
	batches := pressureBatches(t)
	d, repo, start, end := pressurePanelDuck(ctx, t, batches)
	logger := slog.Default()
	slog.SetDefault(slog.New(pressureLog{t}))
	defer slog.SetDefault(logger)
	spec := pressureSpec(start, end)
	observed := pressurePanelEngine{Duck: d, record: func(c context.Context, stage, q string, started time.Time, err error) {
		pressureError(t, "panel_sql/"+stage, started, err)
		t.Logf("failed_sql stage=%s sql=%q ctx_error=%v", stage, q, c.Err())
	}}
	executor := NewExecutor(observed, 30)
	pressureDiagnostics(ctx, t, d, repo, "before_panels_alone")
	var failed, requests, published, versions, anomalies, anomalyDropped atomic.Int64
	run := func(c context.Context, phase string, worker int) []Result {
		started := time.Now()
		results, err := executor.Run(c, RunRequest{Dashboard: spec})
		requests.Add(1)
		pressureError(t, fmt.Sprintf("executor/%s/%d", phase, worker), started, err)
		if err != nil {
			failed.Add(1)
		}
		for _, r := range results {
			t.Logf("panel phase=%s worker=%d id=%s status=%s elapsed_ms=%d error=%q", phase, worker, r.ID, r.Status, r.ElapsedMS, r.Error)
			if r.Status == StatusError {
				failed.Add(1)
			}
		}
		return results
	}
	// Four concurrent dashboard requests retain the executor's production
	// fanout. When internal reads occupy the fifth slot, diagnostics defer
	// their SQL samples until the workers have joined.
	worker := func(c context.Context, phase string, id int) {
		for c.Err() == nil {
			started := time.Now()
			results, err := executor.Run(c, RunRequest{Dashboard: spec})
			requests.Add(1)
			pressureError(t, fmt.Sprintf("executor/%s/%d", phase, id), started, err)
			if err != nil && c.Err() == nil {
				failed.Add(1)
			}
			for _, r := range results {
				t.Logf("panel phase=%s worker=%d id=%s status=%s elapsed_ms=%d error=%q sql=%q", phase, id, r.ID, r.Status, r.ElapsedMS, r.Error, r.SQL)
				if r.Status == StatusError && c.Err() == nil {
					failed.Add(1)
				}
			}
			select {
			case <-c.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
	solo, stopSolo := context.WithTimeout(ctx, 30*time.Second)
	var soloWG sync.WaitGroup
	soloReady := make(chan struct{})
	for i := range 4 {
		soloWG.Add(1)
		go func() { defer soloWG.Done(); <-soloReady; worker(solo, "alone", i) }()
	}
	close(soloReady)
	soloWG.Wait()
	stopSolo()
	pressureDiagnostics(ctx, t, d, repo, "after_panels_alone")
	workloadStarted := time.Now()
	work, stopWork := context.WithTimeout(ctx, pressureDuration(t))
	defer stopWork()
	loop, stopLoop := context.WithCancel(ctx)
	loopDone := make(chan struct{})
	ready := make(chan struct{})
	var wg sync.WaitGroup
	go func() { <-ready; defer close(loopDone); d.RunRollups(loop) }()
	for i := range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); <-ready; worker(work, "combined", i) }()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ready
		for batch := range 32 {
			if work.Err() != nil {
				return
			}
			started := time.Now()
			err := repo.Commit(work, pressureBatch(batch, end, true))
			pressureError(t, "publication/combined", started, err)
			if err != nil {
				failed.Add(1)
				return
			}
			published.Add(1)
			select {
			case <-work.Done():
				return
			case <-time.After(pressureDuration(t) / 32):
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ready
		for work.Err() == nil {
			started := time.Now()
			n, err := d.RefreshVersionRollup(work)
			pressureError(t, "version_rollup/combined", started, err)
			versions.Add(n)
			if err != nil && work.Err() == nil {
				failed.Add(1)
			}
			select {
			case <-work.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ready
		for work.Err() == nil {
			started := time.Now()
			err := d.RecordAnomalies(work, []annotations.Anomaly{{Namespace: "pressure", Service: "svc0", Kind: "latency", From: end.Add(-time.Minute), To: end, Title: "Pressure probe", Severity: "warning"}}, end)
			pressureError(t, "anomaly_log/combined", started, err)
			// The detector writes a batch that misses the write gate with its
			// next batch, so admission timeouts here are delays, counted
			// separately. Any other anomaly error fails.
			switch {
			case err == nil:
				anomalies.Add(1)
			case work.Err() != nil:
			case errors.Is(err, context.DeadlineExceeded) && strings.Contains(err.Error(), "anomaly_log/write_gate"):
				anomalyDropped.Add(1)
			default:
				failed.Add(1)
			}
			select {
			case <-work.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}()
	// RSS sampling does not consume an engine connection. SQL diagnostics are
	// sampled only when a slot is available, and again after workers join.
	wg.Add(1)
	var peakRSS float64
	go func() {
		defer wg.Done()
		<-ready
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			rss, err := pressureRSS()
			peakRSS = max(peakRSS, rss)
			t.Logf("rss_sample mib=%.2f error=%v read_pool=%+v", rss, err, d.DB.Stats())
			select {
			case <-work.Done():
				return
			case <-ticker.C:
				pressureDiagnostics(work, t, d, repo, "combined")
			}
		}
	}()
	close(ready)
	wg.Wait()
	stopWork()
	t.Logf("workload_duration=%s requests=%d published=%d version_batches=%d anomaly_writes=%d failures=%d peak_rss_mib=%.2f", time.Since(workloadStarted), requests.Load(), published.Load(), versions.Load(), anomalies.Load(), failed.Load(), peakRSS)
	pressureDiagnostics(ctx, t, d, repo, "after_workers")
	var visibleVersions, visibleAnomalies int64
	progressCtx, stopProgress := context.WithTimeout(ctx, 5*time.Second)
	progressErr := d.DB.QueryRowContext(progressCtx, "SELECT (SELECT count(*) FROM version_rollup),(SELECT count(*) FROM anomaly_log)").Scan(&visibleVersions, &visibleAnomalies)
	stopProgress()
	t.Logf("pressure_progress visible_versions=%d visible_anomalies=%d error=%v", visibleVersions, visibleAnomalies, progressErr)
	// The drain remains finite, with RunRollups still running. Marker equality
	// uses active immutable batch IDs because compaction may retire inputs.
	drainStarted := time.Now()
	drain, stopDrain := context.WithTimeout(ctx, 3*time.Minute)
	drained := false
	for drain.Err() == nil {
		active := repo.Parquet.BatchMetadata()
		var cached, marked int64
		err := d.DB.QueryRowContext(drain, "SELECT (SELECT count(*) FROM read_batches),(SELECT count(*) FROM version_rollup_batches)").Scan(&cached, &marked)
		t.Logf("drain active=%d cached=%d version_marked=%d error=%v", len(active), cached, marked, err)
		if err == nil && cached == int64(len(active)) && marked == int64(len(active)) {
			var behind int
			err = d.DB.QueryRowContext(drain, "SELECT count(*) FROM rollup_state WHERE cache_key='service_rollup_v3' AND last_ingested_unix_nano >= ?", end.Add(time.Duration(published.Load())*time.Second).UnixNano()).Scan(&behind)
			if err == nil && behind == 1 {
				drained = true
				break
			}
		}
		select {
		case <-drain.Done():
		case <-time.After(time.Second):
		}
	}
	stopDrain()
	stopLoop()
	<-loopDone
	pressureDiagnostics(ctx, t, d, repo, "after_drain")
	finalResults := run(ctx, "after_drain", 0)
	exactCtx, stopExact := context.WithTimeout(ctx, 30*time.Second)
	defer stopExact()
	var spanCount, logCount, rolledSpans, rolledLogs int64
	err := d.DB.QueryRowContext(exactCtx, "SELECT coalesce(sum(spans),0),coalesce(sum(log_count),0) FROM service_rollup WHERE namespace='pressure'").Scan(&rolledSpans, &rolledLogs)
	pressureError(t, "service_rollup/exact_counts", time.Now(), err)
	for _, b := range repo.Parquet.BatchMetadata() {
		spanCount += int64(b.Spans)
		logCount += int64(b.Logs)
	}
	expectedSpans := int64(batches)*512 + published.Load()*512
	expectedLogs := int64(batches)*192 + published.Load()*192
	t.Logf("outcome drained=%t drain_duration=%s failures=%d expected_spans=%d active_spans=%d rolled_spans=%d expected_logs=%d active_logs=%d rolled_logs=%d versions_processed=%d anomaly_writes=%d", drained, time.Since(drainStarted), failed.Load(), expectedSpans, spanCount, rolledSpans, expectedLogs, logCount, rolledLogs, versions.Load(), anomalies.Load())
	rawExact := true
	for _, r := range finalResults {
		if r.ID != "count" && r.ID != "logs" {
			continue
		}
		expected := expectedSpans
		if r.ID == "logs" {
			expected = expectedLogs
		}
		if !pressureExactCount(t, r, expected) {
			rawExact = false
		}
	}
	if len(finalResults) != len(spec.Panels) {
		rawExact = false
	}
	t.Logf("anomaly_admission_dropped=%d", anomalyDropped.Load())
	if assertUsable {
		if failed.Load() > 0 {
			t.Errorf("panel/publication/version/anomaly failures=%d", failed.Load())
		}
		if anomalies.Load() == 0 {
			t.Error("no anomaly write succeeded under backlog")
		}
		if !drained || !rawExact || err != nil || spanCount != expectedSpans || logCount != expectedLogs || rolledSpans != expectedSpans || rolledLogs != expectedLogs {
			t.Error("backlog did not drain with exact counts")
		}
		if progressErr != nil || visibleVersions == 0 || visibleAnomalies == 0 {
			t.Error("no bounded version/anomaly progress during pressure")
		}
	} else {
		t.Log("outcome=diagnostic; a finite run without a defect is INCONCLUSIVE, not a robustness PASS")
	}
}

func pressureExactCount(t *testing.T, r Result, expected int64) bool {
	t.Helper()
	if r.Status != StatusOK || r.Frame == nil || len(r.Frame.Totals) != len(r.Frame.Columns) {
		t.Logf("raw_exact id=%s invalid_frame=true", r.ID)
		return false
	}
	for i, c := range r.Frame.Columns {
		if c.Role != "measure" || c.Name != "count" {
			continue
		}
		value, err := strconv.ParseFloat(fmt.Sprint(r.Frame.Totals[i]), 64)
		t.Logf("raw_exact id=%s count=%v expected=%d error=%v", r.ID, r.Frame.Totals[i], expected, err)
		return err == nil && value == float64(expected)
	}
	t.Logf("raw_exact id=%s missing_count_measure=true", r.ID)
	return false
}
func TestBacklogCountsReadWholeWindowMeasureTotals(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	d, _, start, end := pressurePanelDuck(ctx, t, 1)
	results, err := NewExecutor(d, 30).Run(ctx, RunRequest{Dashboard: pressureSpec(start, end)})
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, r := range results {
		if r.ID == "count" {
			checked++
			if !pressureExactCount(t, r, 512) {
				t.Fatal("incorrect span total")
			}
		}
		if r.ID == "logs" {
			checked++
			if !pressureExactCount(t, r, 192) {
				t.Fatal("incorrect log total")
			}
		}
	}
	if checked != 2 {
		t.Fatalf("missing count results: %v", results)
	}
}

// The proxy preserves the native engine and window context. It observes the
// actual failing SQL, including a stat's separate whole-window total query.
type pressurePanelEngine struct {
	*query.Duck
	record func(context.Context, string, string, time.Time, error)
}

func (e pressurePanelEngine) QueryContext(ctx context.Context, q string, args ...any) (queryrows.Rows, error) {
	started := time.Now()
	rows, err := e.Duck.QueryContext(ctx, q, args...)
	if err != nil {
		e.record(ctx, "query", q, started, err)
		return rows, err
	}
	return &pressurePanelRows{Rows: rows, ctx: ctx, q: q, started: started, record: e.record}, nil
}
func TestBacklogPanelErrorsIdentifyActualSQLAndKeepNativeCause(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	d, _, start, end := pressurePanelDuck(ctx, t, 1)
	q := "SELECT missing_pressure_column FROM spans WHERE start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS"
	var recorded string
	var recordedErr error
	e := pressurePanelEngine{Duck: d, record: func(_ context.Context, _, sql string, _ time.Time, err error) { recorded = sql; recordedErr = err }}
	_, err := e.QueryContext(queryrows.WithWindow(ctx, queryrows.Window{Start: start, End: end}), q, start, end)
	var native *duckdb.Error
	if err == nil || !errors.As(err, &native) {
		t.Fatalf("expected native binder failure: %v", err)
	}
	if recorded != q || recordedErr != err {
		t.Fatalf("actual SQL/native cause not captured: sql=%q error=%v", recorded, recordedErr)
	}
}

type pressurePanelRows struct {
	queryrows.Rows
	ctx     context.Context
	q       string
	started time.Time
	record  func(context.Context, string, string, time.Time, error)
	once    sync.Once
}

func (r *pressurePanelRows) failed(stage string, err error) {
	if err != nil {
		r.once.Do(func() { r.record(r.ctx, stage, r.q, r.started, err) })
	}
}
func (r *pressurePanelRows) Err() error { err := r.Rows.Err(); r.failed("rows", err); return err }
func (r *pressurePanelRows) Scan(dest ...any) error {
	err := r.Rows.Scan(dest...)
	r.failed("scan", err)
	return err
}
func (r *pressurePanelRows) Columns() ([]string, error) {
	cols, err := r.Rows.Columns()
	r.failed("columns", err)
	return cols, err
}
func (r *pressurePanelRows) Close() error { err := r.Rows.Close(); r.failed("close", err); return err }
