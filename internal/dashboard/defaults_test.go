package dashboard

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestDefaultSpecPassesTheFullCheck(t *testing.T) {
	e, _ := defaultFixture(t)
	spec := DefaultSpec()
	if err := e.Validate(t.Context(), &spec); err != nil {
		t.Fatal(err)
	}
}

func defaultFixture(t *testing.T) (*panel.Executor, *telemetrystore.Repository) {
	t.Helper()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(); _ = repo.Close() })
	e := panel.NewExecutor(d, 30)
	e.SetRollupReader(observability.New(d, d, 30))
	return e, repo
}

func runDefault(t *testing.T, e *panel.Executor, start, end time.Time) map[string]panel.Result {
	t.Helper()
	spec := DefaultSpec()
	spec.Time = panel.Time{From: &start, To: &end}
	results, err := e.Run(t.Context(), panel.RunRequest{Dashboard: spec})
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]panel.Result)
	for _, r := range results {
		if r.Status == panel.StatusError {
			t.Fatalf("%s: %s", r.ID, r.Error)
		}
		out[r.ID] = r
	}
	return out
}

func defaultValues(t *testing.T, r panel.Result, column string) []any {
	t.Helper()
	if r.Frame != nil {
		for i, c := range r.Frame.Columns {
			if c.Name == column {
				return r.Frame.Values[i]
			}
		}
	}
	t.Fatalf("missing column %s: %+v", column, r)
	return nil
}

func TestDefaultSpecKeepsExistingPanelsBeforeSystemSections(t *testing.T) {
	spec := DefaultSpec()
	var ids []string
	for i, p := range spec.Panels {
		ids = append(ids, p.ID)
		if i >= 8 && (!strings.Contains(p.Description, "Collector `") || !strings.Contains(p.Description, "https://fanout.run/start/send-telemetry#watch-hosts-containers-and-endpoints")) {
			t.Fatalf("receiver guidance missing: %+v", p)
		}
	}
	want := []string{"requests", "errors", "latency_p95", "error_logs", "traffic", "latency", "error_services", "endpoints", "check_states", "check_latency", "service_health", "service_map", "hosts", "containers", "versions"}
	if !reflect.DeepEqual(ids, want) || spec.Annotations.Deploys == nil || !*spec.Annotations.Deploys {
		t.Fatalf("default spec: %+v", spec)
	}
}

func TestDefaultSystemPanelsUseExistingEmptyState(t *testing.T) {
	e, _ := defaultFixture(t)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	results := runDefault(t, e, start, start.Add(time.Hour))
	for _, id := range []string{"check_states", "check_latency", "hosts", "containers", "versions"} {
		if results[id].Status != panel.StatusEmpty {
			t.Fatalf("%s: %+v", id, results[id])
		}
	}
}

func TestDefaultSystemPanelsShowCapacitySilenceVantageAndVersions(t *testing.T) {
	e, repo := defaultFixture(t)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	metric := func(at time.Time, name, host string, value float64, attrs map[string]any) telemetry.Metric {
		return telemetry.Metric{Namespace: "test", ServiceName: "svc", Name: name, Type: "gauge", Unit: "1", Value: value, Attributes: attrs, Resource: map[string]any{"host.name": host}, TimeUnixNanos: at.UnixNano(), EventUnixNanos: at.UnixNano(), IngestedAt: at.UnixNano()}
	}
	// Separate immutable files make CPU rate depend on the pre-window point.
	for i, offset := range []time.Duration{-time.Minute, 0, time.Minute} {
		at := start.Add(offset)
		var metrics []telemetry.Metric
		for _, host := range []string{"warn", "bad"} {
			if offset != time.Minute {
				for _, state := range []string{"idle", "user"} {
					v := 100 + float64(i)*60
					if state == "user" {
						v = 100 + float64(i)*180
					}
					m := metric(at, "system.cpu.time", host, v, map[string]any{"state": state})
					m.Type, m.Monotonic, m.Temporality, m.Unit = "sum", true, "cumulative", "s"
					metrics = append(metrics, m)
				}
			}
			if offset < 0 {
				continue
			}
			for _, name := range []string{"system.memory.usage", "system.filesystem.usage"} {
				for _, state := range []string{"used", "free"} {
					v := 80.0
					if host == "bad" {
						v = 90
					}
					if state == "free" {
						v = 100 - v
					}
					metrics = append(metrics, metric(at, name, host, v, map[string]any{"state": state, "mountpoint": "/"}))
				}
			}
		}
		if offset >= 0 {
			attrs := map[string]any{"http.url": "https://example.test", "http.method": "GET", "http.status_class": "2xx"}
			metrics = append(metrics, metric(at, "httpcheck.status", "bad", 1, attrs))
			latency := metric(at, "httpcheck.duration", "bad", 0.25, attrs)
			latency.Unit = "s"
			metrics = append(metrics, latency)
			for _, name := range []string{"container.cpu.utilization", "container.memory.usage.total", "container.memory.usage.limit"} {
				v := .9
				if name == "container.memory.usage.total" {
					v = 90
				}
				if name == "container.memory.usage.limit" {
					v = 100
				}
				m := metric(at, name, "bad", v, nil)
				m.Resource["container.id"], m.Resource["container.name"] = "id", "container"
				metrics = append(metrics, m)
			}
		}
		for j := range metrics {
			metrics[j].SeriesID = telemetry.MetricSeriesID(metrics[j])
		}
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprintf("metrics-%d", i), Metrics: metrics}); err != nil {
			t.Fatal(err)
		}
	}
	resource := map[string]any{"host.name": "bad", "service.version": "v1", "process.runtime.name": "Go", "process.runtime.version": "1.27", "telemetry.sdk.language": "go", "telemetry.sdk.version": "1.40"}
	span := telemetry.Span{Namespace: "test", ServiceName: "svc", TraceID: "trace", SpanID: "span", Kind: "SPAN_KIND_SERVER", StartUnixNanos: start.UnixNano(), EndUnixNanos: start.Add(time.Second).UnixNano(), DurationMS: 1000, Resource: resource}
	v2 := metric(start.Add(time.Minute), "version", "bad", 1, nil)
	v2.Resource = map[string]any{"service.version": "v2", "telemetry.sdk.language": "go"}
	v2.SeriesID = telemetry.MetricSeriesID(v2)
	log := telemetry.Log{Namespace: "test", ServiceName: "svc", TimeUnixNanos: start.Add(30 * time.Second).UnixNano(), EventUnixNanos: start.Add(30 * time.Second).UnixNano(), Resource: map[string]any{"service.version": "v1.5"}}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "versions", Spans: []telemetry.Span{span}, Logs: []telemetry.Log{log}, Metrics: []telemetry.Metric{v2}}); err != nil {
		t.Fatal(err)
	}
	results := runDefault(t, e, start, start.Add(2*time.Minute))
	if !reflect.DeepEqual(defaultValues(t, results["hosts"], "host"), []any{"bad", "warn"}) || !reflect.DeepEqual(defaultValues(t, results["hosts"], "cpu_percent"), []any{75.0, 75.0}) || !reflect.DeepEqual(defaultValues(t, results["hosts"], "filesystem_percent"), []any{90.0, 80.0}) {
		t.Fatalf("hosts: %+v", results["hosts"].Frame)
	}
	if defaultValues(t, results["containers"], "memory_percent")[0] != 90.0 || defaultValues(t, results["containers"], "cpu_percent")[0] != 90.0 {
		t.Fatalf("containers: %+v", results["containers"].Frame)
	}
	if defaultValues(t, results["check_latency"], "latency")[0] != 250.0 || !strings.Contains(defaultValues(t, results["check_states"], "endpoint_from")[0].(string), "all checks run on service hosts") {
		t.Fatal("latency or local vantage warning missing")
	}
	if !reflect.DeepEqual(defaultValues(t, results["versions"], "version"), []any{"v2", "v1.5", "v1"}) || defaultValues(t, results["versions"], "first_seen")[0] != start.Add(time.Minute).UnixMilli() || defaultValues(t, results["versions"], "runtime_name")[2] != "Go" || defaultValues(t, results["versions"], "sdk_version")[2] != "1.40" {
		t.Fatalf("versions: %+v", results["versions"].Frame)
	}
	for _, end := range []time.Time{start.Add(3*time.Minute - time.Nanosecond), start.Add(3 * time.Minute), start.Add(8 * time.Minute)} {
		results = runDefault(t, e, start, end)
		state := "reachable"
		if !end.Before(start.Add(3 * time.Minute)) {
			state = "no recent result"
		}
		values := defaultValues(t, results["check_states"], "state")
		if values[len(values)-1] != state {
			t.Fatalf("%s: %+v", end, results["check_states"].Frame)
		}
		if state == "no recent result" && (defaultValues(t, results["containers"], "reporting")[0] != "not reporting" || defaultValues(t, results["containers"], "status")[0] != 100.0) {
			t.Fatal("stopped container not marked bad")
		}
		if defaultValues(t, results["hosts"], "reporting")[0] != "not reporting" || defaultValues(t, results["hosts"], "status")[0] != 100.0 {
			t.Fatal("stopped host not marked bad")
		}
	}
}

func TestDefaultVersionsPreserveFirstSeenAndRuntimeSDKTuples(t *testing.T) {
	e, repo := defaultFixture(t)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Minute)
	base := map[string]any{"service.version": "v1", "process.runtime.name": "Go", "process.runtime.version": "1.27", "telemetry.sdk.language": "go", "telemetry.sdk.version": "1.40"}
	keys := []string{"process.runtime.name", "process.runtime.version", "telemetry.sdk.language", "telemetry.sdk.version"}
	alternatives := []string{"Other", "1.28", "other", "1.41"}
	var spans []telemetry.Span
	var logs []telemetry.Log
	var metrics []telemetry.Metric
	var want [][]any
	for i := 0; i < 8; i++ {
		resource := make(map[string]any)
		for k, v := range base {
			resource[k] = v
		}
		if i >= 1 && i <= 4 {
			resource[keys[i-1]] = alternatives[i-1]
		}
		if i == 5 {
			for _, k := range keys {
				delete(resource, k)
			}
		}
		namespace, service := "test", "svc"
		if i == 6 {
			namespace = "other"
		}
		if i == 7 {
			service = "other"
		}
		first := start.Add(time.Duration(i) * time.Second)
		want = append([][]any{{namespace, service, "v1", first.UnixMilli(), resource[keys[0]], resource[keys[1]], resource[keys[2]], resource[keys[3]]}}, want...)
		// The earliest signal rotates. Every signal repeats the same resource,
		// and an unrelated resource field must not split the final version row.
		for j, offset := range []time.Duration{30 * time.Second, 0, 20 * time.Second} {
			for signal := 0; signal < 3; signal++ {
				at := first.Add(offset + time.Duration((signal+i)%3)*time.Second)
				r := resource
				if j == 2 {
					r = make(map[string]any)
					for k, v := range resource {
						r[k] = v
					}
					r["host.name"] = "another host"
				}
				switch signal {
				case 0:
					spans = append(spans, telemetry.Span{Namespace: namespace, ServiceName: service, TraceID: fmt.Sprintf("trace-%d-%d", i, j), SpanID: "span", StartUnixNanos: at.UnixNano(), EndUnixNanos: at.Add(time.Millisecond).UnixNano(), Resource: r})
				case 1:
					logs = append(logs, telemetry.Log{Namespace: namespace, ServiceName: service, TimeUnixNanos: at.UnixNano(), EventUnixNanos: at.UnixNano(), Resource: r})
				case 2:
					m := telemetry.Metric{Namespace: namespace, ServiceName: service, Name: "version", Type: "gauge", Value: 1, TimeUnixNanos: at.UnixNano(), EventUnixNanos: at.UnixNano(), Resource: r}
					m.SeriesID = telemetry.MetricSeriesID(m)
					metrics = append(metrics, m)
				}
			}
		}
	}
	// Out-of-window observations must not lower first_seen or add a row.
	for _, at := range []time.Time{start.Add(-time.Nanosecond), end} {
		resource := base
		if at.Equal(end) {
			resource = map[string]any{"service.version": "outside"}
		}
		spans = append(spans, telemetry.Span{Namespace: "test", ServiceName: "svc", TraceID: at.String(), SpanID: "boundary", StartUnixNanos: at.UnixNano(), EndUnixNanos: at.Add(time.Millisecond).UnixNano(), Resource: resource})
		logs = append(logs, telemetry.Log{Namespace: "test", ServiceName: "svc", TimeUnixNanos: at.UnixNano(), EventUnixNanos: at.UnixNano(), Resource: resource})
		m := telemetry.Metric{Namespace: "test", ServiceName: "svc", Name: "version", Type: "gauge", Value: 1, TimeUnixNanos: at.UnixNano(), EventUnixNanos: at.UnixNano(), Resource: resource}
		m.SeriesID = telemetry.MetricSeriesID(m)
		metrics = append(metrics, m)
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "version-tuples", Spans: spans, Logs: logs, Metrics: metrics}); err != nil {
		t.Fatal(err)
	}
	spec := DefaultSpec()
	spec.Time = panel.Time{From: &start, To: &end}
	results, err := e.Run(t.Context(), panel.RunRequest{Dashboard: spec, Panels: []string{"versions"}})
	if err != nil {
		t.Fatal(err)
	}
	result := results[0]
	columns := []string{"namespace", "service", "version", "first_seen", "runtime_name", "runtime_version", "sdk_language", "sdk_version"}
	for col, name := range columns {
		var values []any
		for _, row := range want {
			values = append(values, row[col])
		}
		if got := defaultValues(t, result, name); !reflect.DeepEqual(got, values) {
			t.Fatalf("%s: got %v, want %v", name, got, values)
		}
	}
}

func TestDefaultCapacityPreservesRetriesCountersNamespacesAndFreshness(t *testing.T) {
	e, repo := defaultFixture(t)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i, offset := range []time.Duration{-time.Minute, 0, time.Minute} {
		at := start.Add(offset)
		var metrics []telemetry.Metric
		for _, namespace := range []string{"alpha", "beta"} {
			metric := func(name string, value float64, attrs map[string]any) telemetry.Metric {
				return telemetry.Metric{Namespace: namespace, Name: name, Type: "gauge", Unit: "1", Value: value, Attributes: attrs, Resource: map[string]any{"host.name": "shared", "container.id": "shared-id"}, TimeUnixNanos: at.UnixNano(), EventUnixNanos: at.UnixNano(), IngestedAt: at.UnixNano()}
			}
			for _, state := range []string{"idle", "user"} {
				// Alpha's cumulative counter resets at the last point. Beta
				// reports deltas, so its last interval must use the point value.
				value := []float64{100, 160, 10}[i]
				if state == "user" {
					value = []float64{100, 280, 30}[i]
				}
				if namespace == "beta" {
					value = 30
					if state == "user" {
						value = 10
					}
				}
				m := metric("system.cpu.time", value, map[string]any{"state": state})
				m.Type, m.Monotonic, m.Temporality, m.Unit = "sum", true, "cumulative", "s"
				if namespace == "beta" {
					m.Temporality, m.StartTimeUnixNanos = "delta", at.Add(-time.Minute).UnixNano()
				}
				metrics = append(metrics, m)
			}
			if offset < 0 {
				continue
			}
			used := 80.0
			if namespace == "beta" {
				used = 20
			}
			metrics = append(metrics,
				metric("system.memory.usage", used, map[string]any{"state": "used"}),
				metric("system.memory.usage", 100-used, map[string]any{"state": "free"}),
				metric("container.cpu.utilization", used/100, nil),
				metric("container.memory.usage.total", used, nil),
				metric("container.memory.usage.limit", 100, nil))
			// Missing host/container identities must not become extra entities.
			missingHost := metric("system.memory.usage", 999, nil)
			missingHost.Resource = map[string]any{"container.id": "orphan"}
			missingContainer := metric("container.cpu.utilization", 999, nil)
			missingContainer.Resource = map[string]any{"host.name": "shared"}
			metrics = append(metrics, missingHost, missingContainer)
		}
		for j := range metrics {
			metrics[j].SeriesID = telemetry.MetricSeriesID(metrics[j])
		}
		// Retry copies live in another immutable file. Older ingestions lose
		// even with a larger value; equal ingestions use descending value.
		var retries, tiedPoints []telemetry.Metric
		for _, m := range metrics {
			older := m
			older.Value = 9999
			tied := m
			tied.Value = 0
			retries = append(retries, older)
			tiedPoints = append(tiedPoints, tied)
		}
		// Commit assigns ingestion timestamps per batch, so publish old copies
		// first and publish equal-ingestion copies with the real points.
		for j, points := range [][]telemetry.Metric{retries, append(metrics, tiedPoints...)} {
			if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprintf("capacity-%d-%d", i, j), Metrics: points}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, offset := range []time.Duration{2 * time.Minute, 3*time.Minute - time.Nanosecond, 3 * time.Minute, 6 * time.Minute, 6*time.Minute + time.Nanosecond} {
		end := start.Add(offset)
		spec := DefaultSpec()
		spec.Time = panel.Time{From: &start, To: &end}
		results, err := e.Run(t.Context(), panel.RunRequest{Dashboard: spec, Panels: []string{"hosts", "containers"}})
		if err != nil {
			t.Fatal(err)
		}
		for _, result := range results {
			if got := defaultValues(t, result, "namespace"); !reflect.DeepEqual(got, []any{"alpha", "beta"}) {
				t.Fatalf("%s at %s: namespaces %v", result.ID, offset, got)
			}
			state := "reporting"
			if offset >= 3*time.Minute {
				state = "not reporting"
				if got := defaultValues(t, result, "status"); !reflect.DeepEqual(got, []any{100.0, 100.0}) {
					t.Fatalf("%s at %s: status %v", result.ID, offset, got)
				}
			}
			if got := defaultValues(t, result, "reporting"); !reflect.DeepEqual(got, []any{state, state}) {
				t.Fatalf("%s at %s: reporting %v", result.ID, offset, got)
			}
			cpu := []any{75.0, 25.0}
			if result.ID == "containers" {
				cpu = []any{80.0, 20.0}
			}
			memory := []any{80.0, 20.0}
			if offset > 6*time.Minute {
				cpu, memory = []any{nil, nil}, []any{nil, nil}
			}
			for column, want := range map[string][]any{"cpu_percent": cpu, "memory_percent": memory} {
				got := defaultValues(t, result, column)
				for i, expected := range want {
					if number, ok := expected.(float64); ok {
						if value, ok := got[i].(float64); ok && math.Abs(value-number) <= 1e-12 {
							continue
						}
					} else if got[i] == expected {
						continue
					}
					t.Fatalf("%s at %s: %s got %v, want %v", result.ID, offset, column, got, want)
				}
			}
		}
	}
}
