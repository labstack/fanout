//go:build readbench

package panel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

// BenchmarkPanelQueries24Hours measures the existing executor directly. The
// generated dataset is regression-only; representative runs require replay.
func BenchmarkPanelQueries24Hours(b *testing.B) {
	b.StopTimer()
	var data panelBenchData
	input := os.Getenv("FANOUT_PANEL_BENCH_DATA_DIR")
	if input != "" {
		volume := 1
		if raw := os.Getenv("FANOUT_PANEL_BENCH_VOLUME"); raw != "" {
			var err error
			volume, err = strconv.Atoi(raw)
			if err != nil || (volume != 1 && volume != 10) {
				b.Fatal("FANOUT_PANEL_BENCH_VOLUME must be 1 or 10")
			}
		}
		data = preparePanelReplay(b, input, volume)
	} else {
		if os.Getenv("FANOUT_PANEL_BENCH_VOLUME") != "" {
			b.Fatal("volume requires replay input")
		}
		data = preparePanelRegression(b)
	}
	engine, cfg := panelBenchEngine(b, data)
	var version, memory string
	var threads int
	if err := engine.DB.QueryRowContext(b.Context(), "SELECT version(),current_setting('memory_limit'),current_setting('threads')").Scan(&version, &memory, &threads); err != nil {
		b.Fatal(err)
	}
	b.Logf("representative=%t platform=%s/%s engine=%s resolved_memory=%s configured_threads=%d effective_threads=%d connections=%d executor_parallel=4", input != "", runtime.GOOS, runtime.GOARCH, version, memory, cfg.DuckDBThreads, threads, cfg.DuckDBMaxConns)
	raw, err := json.Marshal(data)
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("dataset=%s", raw)
	dashboard := panelBenchmarkDashboard(data)
	shapes := append([]Panel(nil), dashboard.Panels...)
	shapes = append(shapes, Panel{ID: "p95_by_service", Title: "p95 by service", Viz: "table", Query: &Query{From: "spans", Measures: []string{"p95(duration_ms)"}, By: []string{"service"}}}, Panel{ID: "error_rate_window", Title: "Whole window error rate", Viz: "table", Query: &Query{From: "spans", Measures: []string{"error_rate()"}}})
	shapes = append(shapes, panelBenchmarkMetricShapes(b, panelBenchmarkMetricNames(b, engine, data))...)
	var failing []string
	for _, shape := range shapes {
		var p95 float64
		b.Run(shape.ID, func(b *testing.B) {
			spec := dashboard
			spec.Panels = []Panel{shape}
			// Ordinary panels do not pay variable-option scans. The selected
			// traces panel and complete dashboard retain the query variable.
			if shape.ID != "traces_selected" {
				spec.Variables = nil
			}
			p95 = measurePanelBenchmark(b, engine, spec, data.End)
		})
		if p95 > 500 {
			failing = append(failing, shape.ID)
		}
	}
	b.Run("dashboard_twelve", func(b *testing.B) { measurePanelBenchmark(b, engine, dashboard, data.End) })
	b.Logf("PRELIMINARY S7_GATE representative=%t target_p95_ms=500 failing_shapes=%v; dashboard_twelve is S6 executor evidence only, not browser paint time", input != "", failing)
}

func panelBenchmarkDashboard(data panelBenchData) Dashboard {
	service := data.Services[0]
	for _, s := range data.Services {
		if s == "checkout" {
			service = s
			break
		}
	}
	literal := "'" + strings.ReplaceAll(service, "'", "''") + "'"
	d := Dashboard{Name: "Dashboard performance", Time: Time{From: &data.Start, To: &data.End, Refresh: "off"}, Variables: []Variable{{Name: "service", Kind: "query", From: "spans", Field: "service", Default: AllValue, IncludeAll: true}}}
	add := func(id, viz, from string, measures, where, by []string, bucket string) {
		d.Panels = append(d.Panels, Panel{ID: id, Title: id, Viz: viz, Width: 4, Height: "s", Query: &Query{From: from, Measures: measures, Where: where, By: by, Bucket: bucket}})
	}
	add("traces_all", "table", "spans", []string{"count_distinct(trace_id)"}, []string{"trace_id <> ''"}, nil, "")
	add("traces_checkout", "table", "spans", []string{"count_distinct(trace_id)"}, []string{"trace_id <> ''", "service = " + literal}, nil, "")
	add("traces_selected", "table", "spans", []string{"count_distinct(trace_id)"}, []string{"trace_id <> ''", "service = $service"}, nil, "")
	add("count", "stat", "spans", []string{"count()"}, nil, nil, "")
	add("rate", "stat", "spans", []string{"rate()"}, nil, nil, "")
	add("logs", "stat", "logs", []string{"count()"}, nil, nil, "")
	add("traffic", "timeseries", "spans", []string{"count()"}, nil, []string{"service"}, "auto")
	add("latency", "timeseries", "spans", []string{"p95(duration_ms)"}, nil, nil, "auto")
	add("errors", "timeseries", "spans", []string{"error_rate()"}, nil, nil, "auto")
	add("log_volume", "timeseries", "logs", []string{"count()"}, nil, nil, "auto")
	add("service_counts", "bar", "spans", []string{"count()"}, nil, []string{"service"}, "")
	add("severity_counts", "bar", "logs", []string{"count()"}, nil, []string{"severity"}, "")
	return d
}

func panelBenchmarkMetricShapes(tb testing.TB, names map[string]string) []Panel {
	tb.Helper()
	if len(names) == 0 {
		tb.Log("skipping metric shapes: no metrics in benchmark window")
		return nil
	}
	var shapes []Panel
	for _, shape := range []struct {
		id, kind, measure string
		by                []string
	}{
		{"metric_gauge_avg_by_service", "gauge", "avg(value)", []string{"service"}},
		// These are the existing structured measures: rate() counts points per
		// second, and p95(hist_sum) takes the percentile of histogram point sums.
		{"metric_counter_rate", "sum", "rate()", nil},
		{"metric_histogram_p95", "histogram", "p95(hist_sum)", nil},
	} {
		name, ok := names[shape.kind]
		if !ok {
			tb.Logf("skipping metric shape=%s: no %s metrics in benchmark window", shape.id, shape.kind)
			continue
		}
		literal := "'" + strings.ReplaceAll(name, "'", "''") + "'"
		shapes = append(shapes, Panel{ID: shape.id, Title: shape.id, Viz: "timeseries", Query: &Query{From: "metrics", Measures: []string{shape.measure}, Where: []string{"type = '" + shape.kind + "'", "name = " + literal}, By: shape.by, Bucket: "auto"}})
		tb.Logf("metric shape=%s type=%s name=%q measure=%s", shape.id, shape.kind, name, shape.measure)
	}
	return shapes
}

func measurePanelBenchmark(b *testing.B, engine Engine, spec Dashboard, end time.Time) float64 {
	b.StopTimer()
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return end }
	run := func() {
		results, err := e.Run(b.Context(), RunRequest{Dashboard: spec})
		if err != nil || len(results) != len(spec.Panels) {
			b.Fatalf("executor results=%d error=%v", len(results), err)
		}
		for _, r := range results {
			if r.Status != StatusOK || r.Frame == nil || r.Frame.Rows == 0 || r.AnnotationError != "" {
				b.Fatalf("panel=%s status=%s error=%s annotation_error=%s", r.ID, r.Status, r.Error, r.AnnotationError)
			}
		}
	}
	run() // validation, native pages, and executor checks are warm before timing.
	samples := make([]float64, 0, b.N)
	b.ReportAllocs()
	b.ResetTimer()
	b.StartTimer()
	for range b.N {
		at := time.Now()
		run()
		samples = append(samples, float64(time.Since(at))/float64(time.Millisecond))
	}
	b.StopTimer()
	sort.Float64s(samples)
	if len(samples) == 0 {
		b.Fatal("no warm samples")
	}
	p50 := samples[(50*len(samples)+99)/100-1]
	p95 := samples[(95*len(samples)+99)/100-1]
	b.ReportMetric(p50, "p50_ms")
	b.ReportMetric(p95, "p95_ms")
	b.ReportMetric(float64(len(samples)), "samples")
	b.Logf("PRELIMINARY shape=%s warm=true samples=%d p50_ms=%.3f p95_ms=%.3f exceeds_500ms=%t exceeds_1500ms=%t", b.Name(), len(samples), p50, p95, p95 > 500, p95 > 1500)
	// Go calibrates once at N=1 even with -benchtime=100x. The actual gate
	// report requires >=100 warm samples and never averages different shapes.
	if b.N < 100 {
		b.Log("insufficient samples for S7 verdict; rerun with -benchtime=100x or greater")
	}
	return p95
}

func preparePanelRegression(b *testing.B) panelBenchData {
	start := time.Date(2026, 10, 1, 0, 0, 0, 123, time.UTC)
	data := panelBenchData{Dir: panelBenchScratch(b), SourceHash: "generated-96x2048-four-spans-per-trace", Start: start, End: start.Add(24 * time.Hour), Volume: 1, PeriodHours: 24}
	repo, err := telemetrystore.Open(filepath.Join(data.Dir, "telemetry"))
	if err != nil {
		b.Fatal(err)
	}
	defer repo.Close()
	for batch := range 96 {
		rows := telemetrystore.Batch{ID: fmt.Sprintf("regression-%03d", batch)}
		for i := range 2048 {
			n := start.Add(time.Duration(batch)*15*time.Minute + time.Duration(i)*15*time.Minute/2048).UnixNano()
			service := fmt.Sprintf("svc%d", (i/4)%6)
			trace := fmt.Sprintf("trace-%d-%d", batch, i/4)
			status := "STATUS_CODE_OK"
			if i%20 == 0 {
				status = "STATUS_CODE_ERROR"
			}
			rows.Spans = append(rows.Spans, telemetry.Span{Namespace: "regression", ServiceName: service, TraceID: trace, SpanID: fmt.Sprint(i), StartUnixNanos: n, EndUnixNanos: n + int64(i%1000+1)*1000000, DurationMS: float64(i%1000 + 1), Kind: "SPAN_KIND_SERVER", StatusCode: status, IngestedAt: n, Attributes: map[string]any{"http.route": "/request", "attempt": int64(i % 4)}})
			rows.Logs = append(rows.Logs, telemetry.Log{Namespace: "regression", ServiceName: service, TraceID: trace, TimeUnixNanos: n, EventUnixNanos: n, IngestedAt: n, Severity: []string{"INFO", "WARN", "ERROR"}[i%3], Body: "request complete"})
		}
		if err := repo.Commit(b.Context(), rows); err != nil {
			b.Fatal(err)
		}
		data.Spans += 2048
		data.Logs += 2048
	}
	for i := range 6 {
		data.Services = append(data.Services, fmt.Sprintf("svc%d", i))
	}
	data.Namespaces = []string{"regression"}
	stats, err := repo.Parquet.Stats()
	if err != nil {
		b.Fatal(err)
	}
	for _, s := range stats {
		data.Files += s.Files
		data.Bytes += s.Bytes
	}
	return data
}

func TestPanelBenchmarkReplayTilesSignedTimesAndDistinctIdentities(t *testing.T) {
	input := t.TempDir()
	seed := t.TempDir()
	repo, err := telemetrystore.Open(seed)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Unix(0, int64(time.Hour)).UTC()
	n := start.Add(time.Minute).UnixNano()
	err = repo.Commit(t.Context(), telemetrystore.Batch{ID: "signed", Spans: []telemetry.Span{{Namespace: "bench", ServiceName: "checkout", TraceID: "trace", SpanID: "span", StartUnixNanos: n, EndUnixNanos: n + 1, IngestedAt: 1, Attributes: map[string]any{"literal.dot": int64(7)}}}, Logs: []telemetry.Log{{Namespace: "bench", ServiceName: "checkout", TraceID: "trace", EventUnixNanos: n, TimeUnixNanos: n, IngestedAt: 1, Severity: "INFO", Body: "complete"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	// Ingest's optional timestamp fallback accepts positive raw OTLP times.
	// Build signed replay instants with the same physical-row transformation
	// used for tiling, then verify them independently with the native verifier.
	dst := filepath.Join(input, "parquet", "batches", "signed.batch")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	m := telemetry.BatchMetadata{Version: 3, ID: "signed"}
	for _, signal := range []string{"spans", "logs"} {
		err := tilePanelParquet(filepath.Join(seed, "parquet", "batches", "signed.batch", signal+".parquet"), filepath.Join(dst, signal+".parquet"), signal, "source_", start, start.Add(time.Hour), -3*time.Hour, &m, map[string]bool{}, map[string]bool{})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := panelTraceIndex(dst); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "metadata.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(input, "parquet", "staging", "keep")
	if err := os.MkdirAll(filepath.Dir(canary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canary, []byte("immutable source"), 0o644); err != nil {
		t.Fatal(err)
	}
	data := preparePanelReplay(t, input, 2)
	if data.End.Sub(data.Start) != 24*time.Hour || data.Spans != 48 || data.Logs != 48 {
		t.Fatalf("wrong tiling: %+v", data)
	}
	issues, err := telemetrystore.VerifyBatches(filepath.Join(data.Dir, "telemetry"))
	if err != nil || len(issues) != 0 {
		t.Fatalf("invalid tiled batches: %v %v", issues, err)
	}
	if _, err := os.Stat(filepath.Join(input, "query")); !os.IsNotExist(err) {
		t.Fatalf("source query state changed: %v", err)
	}
	if got, err := os.ReadFile(canary); err != nil || string(got) != "immutable source" {
		t.Fatalf("source staging changed: %q %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(dst, "metadata.json")); err != nil || string(got) != string(raw) {
		t.Fatalf("source metadata changed: %v", err)
	}
	assertPanelReplay(t, data, 48)
}

func TestPanelBenchmarkReplayRejectsUnsupportedInputBeforeCopy(t *testing.T) {
	input := t.TempDir()
	dir := filepath.Join(input, "parquet", "batches", "bad.batch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"version":2,"id":"bad","spans":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := panelReplayMetadata(input); err == nil {
		t.Fatal("unsupported input accepted")
	}
}

func TestPanelBenchmarkReplayClipsEmptySignalsWithinMixedBatches(t *testing.T) {
	input := t.TempDir()
	repo, err := telemetrystore.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	end := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	n := end.Add(-time.Minute).UnixNano()
	err = repo.Commit(t.Context(), telemetrystore.Batch{ID: "mixed", Spans: []telemetry.Span{{TraceID: "trace", SpanID: "span", ServiceName: "checkout", StartUnixNanos: n, EndUnixNanos: n + 1, IngestedAt: n}}, Logs: []telemetry.Log{{EventUnixNanos: end.Add(-30 * time.Hour).UnixNano(), IngestedAt: n, Body: "old"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "current", Logs: []telemetry.Log{{EventUnixNanos: n, TimeUnixNanos: n, IngestedAt: n, Body: "current", Severity: "INFO"}}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	data := preparePanelReplay(t, input, 1)
	if data.Spans != 1 || data.Logs != 1 {
		t.Fatalf("clipped counts: %+v", data)
	}
	panelBenchEngine(t, data) // Repository rejects empty files with zero metadata counts.
}

func TestPanelBenchmarkMetricNamesSelectMostCommonInWindow(t *testing.T) {
	engine, repo := newTestEngine(t)
	data := panelBenchData{Start: fixtureStart.Add(time.Nanosecond), End: fixtureStart.Add(time.Hour + time.Nanosecond)}
	if got := panelBenchmarkMetricNames(t, engine, data); len(got) != 0 {
		t.Fatalf("empty metrics selected names: %v", got)
	}
	if shapes := panelBenchmarkMetricShapes(t, nil); len(shapes) != 0 {
		t.Fatalf("empty metrics produced shapes: %v", shapes)
	}
	var metrics []telemetry.Metric
	add := func(kind, name string, at time.Time, count int) {
		for i := range count {
			n := at.UnixNano()
			metrics = append(metrics, telemetry.Metric{ServiceName: fmt.Sprintf("svc%d", i%2), Type: kind, Name: name, EventUnixNanos: n, TimeUnixNanos: n, IngestedAt: n, Value: float64(i + 1), HistSum: float64(i + 1), HistCount: 1, HistBoundsJSON: "[10]", HistCountsJSON: "[1,0]"})
		}
	}
	add("gauge", "rare", data.Start, 1)
	add("gauge", "cpu'usage", data.End.Add(-time.Nanosecond), 3)
	add("gauge", "outside", data.Start.Add(-time.Nanosecond), 4)
	add("gauge", "outside", data.End, 4)
	add("sum", "requests", data.Start, 2)
	add("sum", "rare", data.Start, 1)
	add("histogram", "z_latency", data.Start, 2)
	add("histogram", "a_latency", data.Start, 2)
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "metric-names", Metrics: metrics}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"gauge": "cpu'usage", "sum": "requests", "histogram": "a_latency"}
	if got := panelBenchmarkMetricNames(t, engine, data); !reflect.DeepEqual(got, want) {
		t.Fatalf("metric names = %v, want %v", got, want)
	}
	shapes := panelBenchmarkMetricShapes(t, want)
	if len(shapes) != 3 {
		t.Fatalf("metric shapes = %d, want 3", len(shapes))
	}
	results, err := NewExecutor(engine, 30).Run(t.Context(), RunRequest{Dashboard: Dashboard{Name: "Metric shapes", Time: Time{From: &data.Start, To: &data.End}, Panels: shapes}})
	if err != nil || len(results) != 3 {
		t.Fatalf("metric shape results = %v, error = %v", results, err)
	}
	for _, result := range results {
		if result.Status != StatusOK || result.Frame == nil || result.Frame.Rows == 0 {
			t.Fatalf("metric shape did not execute: %+v", result)
		}
		for _, value := range result.Frame.Values[len(result.Frame.Values)-1] {
			if value == nil {
				t.Fatalf("metric shape %s returned a null measure", result.ID)
			}
		}
	}
	data.End = data.Start.Add(time.Nanosecond)
	want["gauge"] = "rare"
	if got := panelBenchmarkMetricNames(t, engine, data); !reflect.DeepEqual(got, want) {
		t.Fatalf("narrow window metric names = %v, want %v", got, want)
	}
}
