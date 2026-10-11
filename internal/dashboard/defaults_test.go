package dashboard

import (
	"fmt"
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
