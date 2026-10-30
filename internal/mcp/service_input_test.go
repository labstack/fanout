package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/engine"
	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/labstack/fanout/internal/toolerror"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func serviceInputFixture(t testing.TB) (*Server, *engine.Duck, *telemetrystore.Repository, time.Time, time.Time) {
	t.Helper()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	duck, err := engine.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = duck.Close() })
	to := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	batch := telemetrystore.Batch{ID: "service-input"}
	for i, service := range []string{"checkout", "fraud-detection", "accounting"} {
		for j := range 6 - 2*i {
			n := from.Add(time.Minute).UnixNano() + int64(j)
			batch.Spans = append(batch.Spans, telemetry.Span{Namespace: "shop", ServiceName: service, TraceID: service, SpanID: fmt.Sprint(j), Name: "orders process", Kind: "SPAN_KIND_CONSUMER", StartUnixNanos: n, EndUnixNanos: n + 1000000, DurationMS: 1, IngestedAt: n, DBSystem: "postgresql", PeerService: "broker", Attributes: map[string]any{"messaging.system": "kafka"}})
		}
	}
	batch.Logs = []telemetry.Log{{Namespace: "shop", ServiceName: "logger", TraceID: "logs-only", TimeUnixNanos: from.Add(time.Minute).UnixNano(), Severity: "INFO", Body: "log only"}}
	batch.Metrics = []telemetry.Metric{{Namespace: "shop", ServiceName: "collector", Name: "load", Type: "gauge", TimeUnixNanos: from.Add(time.Minute).UnixNano(), Value: 1}}
	// These names are valid, but have no traffic in the requested window.
	batch.Spans = append(batch.Spans, telemetry.Span{Namespace: "shop", ServiceName: "idle", TraceID: "idle", SpanID: "idle", StartUnixNanos: from.Add(-time.Hour).UnixNano(), EndUnixNanos: from.Add(-time.Hour).UnixNano() + 1}, telemetry.Span{Namespace: "other", ServiceName: "elsewhere", TraceID: "elsewhere", SpanID: "elsewhere", StartUnixNanos: from.Add(time.Minute).UnixNano(), EndUnixNanos: from.Add(time.Minute).UnixNano() + 1})
	if err := repo.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	executor := query.NewExecutor(duck, 30)
	queries := observability.New(duck, duck, 30)
	executor.SetRollupReader(queries)
	s := NewWithIntelligence(queries, nil, executor, nil, "test")
	s.now = func() time.Time { return to }
	return s, duck, repo, from, to
}

func TestServiceInputsRejectUnknownBeforeRendering(t *testing.T) {
	s, _, _, from, to := serviceInputFixture(t)
	client := connectTestClient(t, s, &mcp.ClientCapabilities{Extensions: map[string]any{mcpUIExtension: map[string]any{"mimeTypes": []string{mcpAppMIME}}}})
	for _, name := range []string{"get_service_performance", "get_service_dependencies", "search_logs", "inspect_trace"} {
		t.Run(name, func(t *testing.T) {
			for _, service := range []string{"missing", "kafka", "postgresql", "broker", "elsewhere", "Checkout", "checkout' OR true --"} {
				args := map[string]any{"service": service, "namespace": "shop"}
				if name == "get_service_dependencies" {
					args["window"] = "1h"
				} else {
					args["from"], args["to"] = from, to
				}
				result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
				if err != nil || result == nil || !result.IsError || result.StructuredContent != nil {
					t.Fatalf("%s: result=%+v err=%v", service, result, err)
				}
				var failure toolerror.Result
				if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &failure); err != nil || failure.Error == nil || failure.Error.Code != "unknown_service" {
					t.Fatalf("%s: error=%+v decode=%v", service, failure, err)
				}
				message := failure.Error.Message
				if !strings.Contains(message, "checkout, fraud-detection, accounting, collector, logger") || strings.Contains(message, "idle,") || strings.Contains(message, "elsewhere,") {
					t.Fatalf("incorrect traffic/namespace guidance: %s", message)
				}
				kind := map[string]string{"kafka": "messaging system", "postgresql": "database system", "broker": "peer/callee"}[service]
				if kind != "" && !strings.Contains(message, service+" is a "+kind+" used by checkout, fraud-detection, accounting") {
					t.Fatalf("missing alias users: %s", message)
				}
			}
		})
	}
}

func TestServiceInputsKeepKnownAndIdleServices(t *testing.T) {
	s, duck, _, from, to := serviceInputFixture(t)
	for _, cached := range []bool{false, true} {
		if cached {
			if _, err := duck.RefreshReadCaches(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		for _, service := range []string{"checkout", "idle", "logger", "collector"} {
			result, out, err := s.performance(t.Context(), nil, PerformanceInput{Service: service, Namespace: "shop", From: &from, To: &to})
			if service != "checkout" {
				if err == nil || !strings.Contains(err.Error(), `"no_data"`) || result != nil || len(out.Results) != 0 {
					t.Fatalf("%s: %+v %v", service, out, err)
				}
				volume := map[string]string{"idle": "spans=0, logs=0, metrics=0", "logger": "spans=0, logs=1, metrics=0", "collector": "spans=0, logs=0, metrics=1"}[service]
				if !strings.Contains(err.Error(), volume) {
					t.Fatalf("missing volumes: %v", err)
				}
				continue
			}
			if err != nil || result.IsError || len(out.Results) != 4 || out.Results[3].Status != query.StatusOK {
				t.Fatalf("cached=%v %s: %+v %v", cached, service, out, err)
			}
		}
	}
	result, out, err := s.logs(t.Context(), nil, LogsInput{Service: "logger", Namespace: "shop", From: &from, To: &to})
	if err != nil || result.IsError || len(out.Results) == 0 || out.Results[0].Status != query.StatusOK {
		t.Fatalf("log-only service lost: %+v %+v %v", result, out, err)
	}
	result, out, err = s.trace(t.Context(), nil, TraceInput{Service: "checkout", Namespace: "shop", From: &from, To: &to})
	if err != nil || result.IsError || out.Trace == nil || len(out.Trace.Data.Spans) != 6 {
		t.Fatalf("known trace lost: %+v %+v %v", result, out, err)
	}
}

type serviceGuidanceSpy struct {
	panelExecutor
	calls int
}

func (e *serviceGuidanceSpy) ServiceGuidance(ctx context.Context, start, end time.Time, namespace, service string) (string, error) {
	e.calls++
	return e.panelExecutor.ServiceGuidance(ctx, start, end, namespace, service)
}

func (e *serviceGuidanceSpy) EmptyGuidance(ctx context.Context, req query.RunRequest) (string, string, error) {
	for _, p := range req.Dashboard.Panels {
		if p.Query != nil {
			for _, f := range p.Query.Where {
				if strings.HasPrefix(f, "service = ") {
					e.calls++
					return e.panelExecutor.EmptyGuidance(ctx, req)
				}
			}
		}
	}
	return e.panelExecutor.EmptyGuidance(ctx, req)
}

func TestServiceInputsWithResultsSkipMembership(t *testing.T) {
	s, duck, _, from, to := serviceInputFixture(t)
	if _, err := duck.DB.Exec(`INSERT INTO edge_rollup VALUES ('shop',?,'checkout','broker',6,1,0,'call')`, from.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	spy := &serviceGuidanceSpy{panelExecutor: s.panels}
	s.panels = spy
	client := connectTestClient(t, s, nil)
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"get_service_performance", map[string]any{"service": "checkout", "namespace": "shop", "from": from, "to": to}},
		{"get_service_dependencies", map[string]any{"service": "checkout", "namespace": "shop", "window": "1h"}},
		// An observed dependency is a result even if the root is only a callee.
		{"get_service_dependencies", map[string]any{"service": "broker", "namespace": "shop", "window": "1h"}},
		{"search_logs", map[string]any{"service": "logger", "namespace": "shop", "from": from, "to": to}},
		{"inspect_trace", map[string]any{"service": "checkout", "namespace": "shop", "from": from, "to": to}},
		// Exact trace details survive an optional service filter with no list rows.
		{"inspect_trace", map[string]any{"service": "missing", "trace_id": "checkout", "namespace": "shop", "from": from, "to": to}},
	} {
		t.Run(tc.name+"/"+tc.args["service"].(string), func(t *testing.T) {
			result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
			if err != nil || result.IsError || result.StructuredContent == nil {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if spy.calls != 0 {
				t.Fatalf("nonempty tool performed %d membership lookups", spy.calls)
			}
		})
	}
}

func TestServiceInputsValidateOnlyAfterAllResultsEmpty(t *testing.T) {
	s, _, _, from, to := serviceInputFixture(t)
	executor := s.panels
	for _, tc := range []struct {
		name     string
		service  string
		statuses []string
		runErr   error
		lookups  int
		unknown  bool
		empty    bool
	}{
		{name: "unknown", service: "missing", statuses: []string{query.StatusEmpty, query.StatusEmpty}, lookups: 1, unknown: true, empty: true},
		{name: "known empty", service: "idle", statuses: []string{query.StatusEmpty, query.StatusEmpty}, lookups: 1, empty: true},
		{name: "no service", statuses: []string{query.StatusEmpty, query.StatusEmpty}, empty: true},
		{name: "one populated", service: "missing", statuses: []string{query.StatusEmpty, query.StatusOK}},
		{name: "panel error", service: "missing", statuses: []string{query.StatusEmpty, query.StatusError}},
		{name: "execution error", service: "missing", runErr: errors.New("execution failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ran := false
			spy := &serviceGuidanceSpy{panelExecutor: receiptExecutor{panelExecutor: executor, run: func(context.Context, query.RunRequest) ([]query.Result, error) {
				ran = true
				if spyCalls := s.panels.(*serviceGuidanceSpy).calls; spyCalls != 0 {
					t.Errorf("membership ran before preset: %d lookups", spyCalls)
				}
				var results []query.Result
				for i, status := range tc.statuses {
					results = append(results, query.Result{ID: fmt.Sprint(i), Status: status})
				}
				return results, tc.runErr
			}}}
			s.panels = spy
			result, out, err := s.performance(t.Context(), nil, PerformanceInput{Service: tc.service, Namespace: "shop", From: &from, To: &to})
			if !ran || spy.calls != tc.lookups {
				t.Fatalf("ran=%v membership=%d want %d", ran, spy.calls, tc.lookups)
			}
			if tc.unknown {
				if err == nil || !strings.Contains(err.Error(), `"unknown_service"`) || result != nil || len(out.Results) != 0 {
					t.Fatalf("unknown result=%+v out=%+v err=%v", result, out, err)
				}
			} else if tc.empty {
				if err == nil || !strings.Contains(err.Error(), `"no_data"`) || result != nil || len(out.Results) != 0 {
					t.Fatalf("empty result=%+v out=%+v err=%v", result, out, err)
				}
			} else if (err != nil) != (tc.runErr != nil) || tc.runErr == nil && (result == nil || len(out.Results) != len(tc.statuses)) {
				t.Fatalf("result=%+v out=%+v err=%v", result, out, err)
			}
		})
	}
}

func TestServiceGuidanceBoundsOversizedName(t *testing.T) {
	s, _, _, from, to := serviceInputFixture(t)
	// Such a name exceeds the preset filter limit now that execution runs first.
	// Exercise the shared error bound directly to retain that regression check.
	err := s.checkService(t.Context(), observability.Scope{Namespace: "shop", Start: from, End: to}, strings.Repeat("<\\\"\n界", 4096))
	var failure toolerror.Result
	if err == nil || len(err.Error()) > 16*1024 || !utf8.ValidString(err.Error()) || json.Unmarshal([]byte(err.Error()), &failure) != nil || failure.Error == nil || failure.Error.Code != "unknown_service" || !strings.HasSuffix(failure.Error.Message, "…") {
		t.Fatalf("invalid bounded error: %v", err)
	}
}

func TestServiceInputsClampWindowBeforeValidation(t *testing.T) {
	s, duck, repo, _, to := serviceInputFixture(t)
	executor := query.NewExecutor(duck, 7)
	queries := observability.New(duck, duck, 7)
	executor.SetRollupReader(queries)
	s.panels, s.queries = executor, queries
	old := to.Add(-10 * 24 * time.Hour).UnixNano()
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "outside-effective-window", Logs: []telemetry.Log{{Namespace: "shop", ServiceName: "old-window-only", TimeUnixNanos: old}}}); err != nil {
		t.Fatal(err)
	}
	client := connectTestClient(t, s, nil)
	wantWindow := to.Add(-7*24*time.Hour).Format(time.RFC3339Nano) + "/" + to.Format(time.RFC3339Nano)
	for _, name := range []string{"get_service_performance", "get_service_dependencies", "search_logs", "inspect_trace"} {
		t.Run(name, func(t *testing.T) {
			result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{"service": map[bool]string{true: "logger", false: "checkout"}[name == "search_logs"], "namespace": "shop", "window": "720h"}})
			if err != nil || result.IsError {
				t.Fatalf("clamped known service: %+v %v", result, err)
			}
			encoded, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			if name == "get_service_dependencies" {
				var out observability.Result[observability.Dependencies]
				if err := json.Unmarshal(encoded, &out); err != nil || out.Provenance.Window != wantWindow {
					t.Fatalf("dependency scope: %s %v", encoded, err)
				}
			} else {
				var out PanelFragment
				if err := json.Unmarshal(encoded, &out); err != nil || len(out.Results) == 0 {
					t.Fatalf("fragment: %s %v", encoded, err)
				}
				for _, panelResult := range out.Results {
					if panelResult.FromMS != to.Add(-7*24*time.Hour).UnixMilli() || panelResult.ToMS != to.UnixMilli() {
						t.Fatalf("panel scope: %+v", panelResult)
					}
				}
				if out.Trace != nil && out.Trace.Provenance.Window != wantWindow {
					t.Fatalf("trace scope: %+v", out.Trace.Provenance)
				}
			}
			result, err = client.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{"service": "missing", "namespace": "shop", "window": "720h"}})
			var failure toolerror.Result
			if err != nil || !result.IsError || json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &failure) != nil || failure.Error == nil || failure.Error.Code != "unknown_service" || strings.Contains(failure.Error.Message, "old-window-only") {
				t.Fatalf("effective-window guidance: %+v %+v %v", result, failure, err)
			}
		})
	}
}

func TestServiceInputsKeepRetainedEventTimes(t *testing.T) {
	s, duck, repo, from, to := serviceInputFixture(t)
	for _, offset := range []struct {
		name string
		at   time.Time
	}{{"late", to.Add(-365 * 24 * time.Hour)}, {"future", to.Add(365 * 24 * time.Hour)}} {
		for _, signal := range []string{"log", "metric"} {
			service := offset.name + "-" + signal
			batch := telemetrystore.Batch{ID: service}
			if signal == "log" {
				batch.Logs = []telemetry.Log{{Namespace: "shop", ServiceName: service, TimeUnixNanos: offset.at.UnixNano(), Body: "retained"}}
			} else {
				batch.Metrics = []telemetry.Metric{{Namespace: "shop", ServiceName: service, TimeUnixNanos: offset.at.UnixNano(), Name: "retained", Type: "gauge", Value: 1}}
			}
			if err := repo.Commit(t.Context(), batch); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, cached := range []bool{false, true} {
		if cached {
			if _, err := duck.RefreshReadCaches(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		for _, service := range []string{"late-log", "late-metric", "future-log", "future-metric"} {
			for _, namespace := range []string{"shop", "", "other"} {
				message, err := s.panels.ServiceGuidance(t.Context(), from, to, namespace, service)
				if err != nil || (message == "") != (namespace != "other") {
					t.Errorf("cached=%v namespace=%q service=%q: %q %v", cached, namespace, service, message, err)
				}
			}
			result, out, err := s.performance(t.Context(), nil, PerformanceInput{Service: service, Namespace: "shop", From: &from, To: &to})
			if err == nil || !strings.Contains(err.Error(), `"no_data"`) || result != nil || len(out.Results) != 0 {
				t.Fatalf("retained service lost its no_data response: cached=%v service=%s out=%+v err=%v", cached, service, out, err)
			}
		}
		message, err := s.panels.ServiceGuidance(t.Context(), from, to, "shop", "missing")
		if err != nil || strings.Contains(message, "late-") || strings.Contains(message, "future-") {
			t.Fatalf("event time leaked into suggestions: %q %v", message, err)
		}
	}
}

func TestServiceGuidanceLimitsSuggestionsAndScopesAliases(t *testing.T) {
	s, duck, repo, from, to := serviceInputFixture(t)
	batch := telemetrystore.Batch{ID: "more-services"}
	for i := range 12 {
		for j := range 20 - i {
			n := from.Add(2*time.Minute).UnixNano() + int64(j)
			batch.Spans = append(batch.Spans, telemetry.Span{Namespace: "shop", ServiceName: fmt.Sprintf("rank%02d", i), TraceID: fmt.Sprint(i), SpanID: fmt.Sprint(j), StartUnixNanos: n, EndUnixNanos: n + 1, PeerService: "alias", DBSystem: "alias", Attributes: map[string]any{"messaging.system": "alias"}})
		}
	}
	if err := repo.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	if _, err := duck.DB.Exec(`INSERT INTO edge_rollup VALUES ('shop',?,'checkout','external',9,1,0,'call'),('other',?,'elsewhere','external',99,1,0,'call')`, from.Add(time.Minute), from.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	message, err := s.panels.ServiceGuidance(t.Context(), from, to, "shop", "external")
	if err != nil || !strings.Contains(message, "external is a peer/callee used by checkout.") || strings.Contains(message, "elsewhere") || strings.Contains(message, "rank10") || strings.Contains(message, "rank11") {
		t.Fatalf("guidance=%s err=%v", message, err)
	}
	for i := range 10 {
		if !strings.Contains(message, fmt.Sprintf("rank%02d", i)) {
			t.Fatal(message)
		}
	}
	message, err = s.panels.ServiceGuidance(t.Context(), from, to, "shop", "alias")
	if err != nil || strings.Contains(message, "rank10") || strings.Contains(message, "rank11") {
		t.Fatalf("unbounded aliases: %s %v", message, err)
	}
	for _, kind := range []string{"messaging system", "database system", "peer/callee"} {
		prefix := "alias is a " + kind + " used by "
		_, names, ok := strings.Cut(message, prefix)
		if !ok || strings.Count(strings.SplitN(names, ".", 2)[0], ", ") != 9 {
			t.Fatalf("expected ten users for %s: %s", kind, message)
		}
	}
	// Validity is independent of the ten suggestions, and namespace is exact.
	for _, tc := range []struct{ namespace, service string }{{"shop", "rank11"}, {"other", "elsewhere"}, {"", "elsewhere"}} {
		message, err := s.panels.ServiceGuidance(t.Context(), from, to, tc.namespace, tc.service)
		if err != nil || message != "" {
			t.Fatalf("known %+v: %s %v", tc, message, err)
		}
	}
	message, err = s.panels.ServiceGuidance(t.Context(), to.Add(time.Hour), to.Add(2*time.Hour), "shop", "kafka")
	if err != nil || strings.Contains(message, "used by") || !strings.Contains(message, "No known services have traffic") {
		t.Fatalf("alias leaked outside window: %s %v", message, err)
	}
}

func BenchmarkServiceInput(b *testing.B) {
	s, duck, repo, from, to := serviceInputFixture(b)
	// Synthetic demo-sized traffic; no private investigation data is needed.
	batch := telemetrystore.Batch{ID: "benchmark-traffic"}
	services := []string{"checkout", "fraud-detection", "accounting"}
	for i := range 54000 {
		n := from.Add(time.Duration(i) * time.Second / 15).UnixNano()
		batch.Spans = append(batch.Spans, telemetry.Span{Namespace: "shop", ServiceName: services[i%len(services)], TraceID: fmt.Sprint(i), SpanID: fmt.Sprint(i), Name: "orders process", Kind: "SPAN_KIND_CONSUMER", StartUnixNanos: n, EndUnixNanos: n + 1000000, DurationMS: 1, IngestedAt: n, Attributes: map[string]any{"messaging.system": "kafka"}})
	}
	if err := repo.Commit(b.Context(), batch); err != nil {
		b.Fatal(err)
	}
	if _, err := duck.DB.Exec(`INSERT INTO service_rollup VALUES ('shop',?,'checkout',6,6,1,1,0,0,0)`, from.Add(time.Minute)); err != nil {
		b.Fatal(err)
	}
	if _, err := duck.RefreshReadCaches(b.Context()); err != nil {
		b.Fatal(err)
	}
	d := fragmentPreset("performance", "checkout", "shop", "", "", 0)
	d.Time = query.Time{From: &from, To: &to, Refresh: "off"}
	for _, name := range []string{"rollup_check", "inventory_check", "unknown_guidance", "preset_baseline", "preset_lazy"} {
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				var err error
				switch name {
				case "rollup_check":
					_, err = s.panels.ServiceGuidance(b.Context(), from, to, "shop", "checkout")
				case "inventory_check":
					_, err = s.panels.ServiceGuidance(b.Context(), from, to, "shop", "accounting")
				case "unknown_guidance":
					_, err = s.panels.ServiceGuidance(b.Context(), from, to, "shop", "kafka")
				case "preset_baseline":
					_, _, err = s.runFragment(b.Context(), query.RunRequest{Dashboard: d}, "preset")
				case "preset_lazy":
					_, _, err = s.performance(b.Context(), nil, PerformanceInput{Service: "checkout", Namespace: "shop", From: &from, To: &to})
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Each measured cold operation gets a fresh DuckDB engine and executor, with
// no completed-batch cache populated. Files remain in the OS page cache; this
// measures application/native cache coldness without requiring privileged I/O.
func BenchmarkServiceInputRetainedBatches(b *testing.B) {
	cfg := config.Config{DataDir: b.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = repo.Close() })
	to := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	const batches = 256
	for i := range batches {
		batch := telemetrystore.Batch{ID: fmt.Sprintf("retained-%03d", i)}
		for j := range 64 {
			n := from.Add(time.Duration(i*64+j) * time.Second / 8).UnixNano()
			batch.Spans = append(batch.Spans, telemetry.Span{Namespace: "shop", ServiceName: "checkout", TraceID: fmt.Sprint(i, j), SpanID: fmt.Sprint(j), Name: "process", Kind: "SPAN_KIND_CONSUMER", StartUnixNanos: n, EndUnixNanos: n + 1000000, DurationMS: 1, Attributes: map[string]any{"messaging.system": "kafka"}})
			batch.Logs = append(batch.Logs, telemetry.Log{Namespace: "shop", ServiceName: "logger", TimeUnixNanos: n, Body: "retained"})
			batch.Metrics = append(batch.Metrics, telemetry.Metric{Namespace: "shop", ServiceName: "collector", TimeUnixNanos: n, Name: "load", Type: "gauge", Value: 1})
		}
		if err := repo.Commit(b.Context(), batch); err != nil {
			b.Fatal(err)
		}
	}
	for _, name := range []string{"cold_inventory", "cold_unknown_guidance", "cold_preset_baseline", "cold_preset_lazy", "cold_unknown_baseline", "cold_unknown_lazy"} {
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				b.StopTimer()
				duck, err := engine.NewDuck(b.Context(), cfg, repo)
				if err != nil {
					b.Fatal(err)
				}
				var cached int
				if err := duck.DB.QueryRow(`SELECT count(*) FROM read_batches`).Scan(&cached); err != nil || cached != 0 {
					b.Fatalf("expected cold batch cache: %d %v", cached, err)
				}
				executor := query.NewExecutor(duck, 7)
				queries := observability.New(duck, duck, 7)
				executor.SetRollupReader(queries)
				s := NewWithIntelligence(queries, nil, executor, nil, "test")
				service := "checkout"
				if name == "cold_unknown_baseline" || name == "cold_unknown_lazy" {
					service = "kafka"
				}
				d := fragmentPreset("performance", service, "shop", "", "", 0)
				d.Time = query.Time{From: &from, To: &to, Refresh: "off"}
				var out PanelFragment
				var message string
				b.StartTimer()
				switch name {
				case "cold_inventory":
					message, err = executor.ServiceGuidance(b.Context(), from, to, "shop", "checkout")
				case "cold_unknown_guidance":
					message, err = executor.ServiceGuidance(b.Context(), from, to, "shop", "kafka")
				case "cold_preset_baseline", "cold_unknown_baseline":
					_, out, err = s.runFragment(b.Context(), query.RunRequest{Dashboard: d}, "preset")
				case "cold_preset_lazy", "cold_unknown_lazy":
					_, out, err = s.performance(b.Context(), nil, PerformanceInput{Service: service, Namespace: "shop", From: &from, To: &to})
				}
				b.StopTimer()
				if closeErr := duck.Close(); closeErr != nil {
					b.Fatal(closeErr)
				}
				if name == "cold_unknown_lazy" {
					var failure toolerror.Result
					if err == nil || json.Unmarshal([]byte(err.Error()), &failure) != nil || failure.Error == nil || failure.Error.Code != "unknown_service" || !strings.Contains(failure.Error.Message, "messaging system used by checkout") || len(out.Results) != 0 {
						b.Fatalf("unexpected unknown result: %+v %v", out, err)
					}
				} else if err != nil {
					b.Fatal(err)
				}
				if name == "cold_inventory" && message != "" || name == "cold_unknown_guidance" && !strings.Contains(message, "messaging system used by checkout") {
					b.Fatalf("unexpected guidance: %q", message)
				}
				if strings.Contains(name, "preset") || name == "cold_unknown_baseline" {
					if len(out.Results) != 4 {
						b.Fatalf("expected four panel results: %+v", out.Results)
					}
					for _, result := range out.Results {
						want := query.StatusOK
						if name == "cold_unknown_baseline" {
							want = query.StatusEmpty
						}
						if result.Status != want {
							b.Fatalf("preset failed: %+v", result)
						}
					}
				}
				b.StartTimer()
			}
			b.ReportMetric(batches, "batches")
		})
	}
}
