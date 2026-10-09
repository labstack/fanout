package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/query"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestSeedPayloadsAreDeterministic(t *testing.T) {
	base := time.Date(2026, 10, 8, 12, 0, 0, 123456789, time.UTC)
	a, err := payloads(13, base)
	if err != nil {
		t.Fatal(err)
	}
	b, err := payloads(13, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, signal := range []string{"traces", "logs"} {
		if len(a[signal]) == 0 || !bytes.Equal(a[signal], b[signal]) {
			t.Fatalf("%s payload differs", signal)
		}
	}
	c, err := payloads(14, base)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a["traces"], c["traces"]) {
		t.Fatal("seed does not affect traces")
	}
}

func TestAllPanelsFixtureValidates(t *testing.T) {
	data, err := os.ReadFile("../../../ui/host/e2e/fixtures/all-panels.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec panel.Dashboard
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	panel.Normalize(&spec)
	if problems := panel.Validate(&spec); len(problems) != 0 {
		t.Fatalf("invalid fixture: %v", problems)
	}
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	engine, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	if _, problems, err := panel.Check(t.Context(), engine, &spec); err != nil || len(problems) != 0 {
		t.Fatalf("fixture check: %v; %v", err, problems)
	}
	types := map[string]bool{}
	for _, p := range spec.Panels {
		types[p.Viz] = true
	}
	if len(spec.Panels) != 15 || len(types) != 15 {
		t.Fatal("fixture must exercise all fifteen types once")
	}
	if len(spec.Variables) != 1 || spec.Variables[0].Kind != "query" || !spec.Variables[0].Multi {
		t.Fatal("missing multi-select query variable")
	}
	if spec.Annotations == nil || spec.Annotations.Deploys == nil || !*spec.Annotations.Deploys || spec.Annotations.Anomalies == nil || !*spec.Annotations.Anomalies {
		t.Fatal("annotations must be enabled")
	}
}

func TestSeedCoversDistributedTelemetry(t *testing.T) {
	base := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	data, err := payloads(13, base)
	if err != nil {
		t.Fatal(err)
	}
	spans := &collectortrace.ExportTraceServiceRequest{}
	if err := proto.Unmarshal(data["traces"], spans); err != nil {
		t.Fatal(err)
	}
	logs := &collectorlogs.ExportLogsServiceRequest{}
	if err := proto.Unmarshal(data["logs"], logs); err != nil {
		t.Fatal(err)
	}
	services, severities := map[string]bool{}, map[int32]bool{}
	ids := map[string]*trace.Span{}
	parents := 0
	errors := 0
	versions := map[string]bool{}
	db, producer, consumer, routes := false, false, false, false
	for _, resource := range spans.ResourceSpans {
		name, version := "", ""
		for _, a := range resource.Resource.Attributes {
			if a.Key == "service.name" {
				name = a.Value.GetStringValue()
				services[name] = true
			}
			if a.Key == "service.version" {
				version = a.Value.GetStringValue()
			}
		}
		for _, scope := range resource.ScopeSpans {
			for _, span := range scope.Spans {
				at := time.Unix(0, int64(span.StartTimeUnixNano))
				if at.Before(base.Add(-2*time.Hour)) || !at.Before(base) {
					t.Fatal("span outside near-now window")
				}
				if name == "checkout" {
					versions[version] = true
					want := "1.0.0"
					if !at.Before(base.Add(-time.Hour)) {
						want = "1.1.0"
					}
					if version != want {
						t.Fatal("deploy split is not one hour ago")
					}
				}
				if len(span.ParentSpanId) > 0 {
					parent := ids[string(span.TraceId)+string(span.ParentSpanId)]
					if parent == nil {
						t.Fatal("missing cross-service parent")
					}
					parents++
				}
				ids[string(span.TraceId)+string(span.SpanId)] = span
				if span.Status.Code == trace.Status_STATUS_CODE_ERROR {
					errors++
				}
				method, route, messaging := false, false, false
				for _, a := range span.Attributes {
					switch a.Key {
					case "db.system":
						db = span.Kind == trace.Span_SPAN_KIND_CLIENT
					case "http.request.method":
						method = true
					case "http.route":
						route = true
					case "messaging.system":
						messaging = a.Value.GetStringValue() == "kafka"
					}
				}
				routes = routes || method && route && span.Kind == trace.Span_SPAN_KIND_SERVER
				producer = producer || messaging && span.Kind == trace.Span_SPAN_KIND_PRODUCER
				consumer = consumer || messaging && span.Kind == trace.Span_SPAN_KIND_CONSUMER
			}
		}
	}
	count := 0
	bodies := map[string]int{}
	for _, resource := range logs.ResourceLogs {
		for _, scope := range resource.ScopeLogs {
			for _, log := range scope.LogRecords {
				if ids[string(log.TraceId)+string(log.SpanId)] == nil {
					t.Fatal("log missing correlated span")
				}
				at := time.Unix(0, int64(log.TimeUnixNano))
				if at.Before(base.Add(-2*time.Hour)) || !at.Before(base) {
					t.Fatal("log outside near-now window")
				}
				severities[int32(log.SeverityNumber)] = true
				bodies[log.Body.GetStringValue()]++
				count++
			}
		}
	}
	if len(ids) != 720 || parents != 600 || errors != 36 || count != 720 || len(services) != 6 || len(severities) != 4 || len(versions) != 2 || !db || !producer || !consumer || !routes {
		t.Fatalf("incomplete seed: spans=%d parents=%d errors=%d logs=%d services=%d severities=%d versions=%d", len(ids), parents, errors, count, len(services), len(severities), len(versions))
	}
	for _, count := range bodies {
		if count < 2 {
			t.Fatal("log templates must repeat")
		}
	}
}

func TestSeedPostsAuthenticatedProtobuf(t *testing.T) {
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer smoke-test-token" || r.Header.Get("Content-Type") != "application/x-protobuf" {
			t.Error("incorrect OTLP request")
		}
		switch r.URL.Path {
		case "/v1/traces", "/v1/logs":
			seen[r.URL.Path] = true
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("smoke-test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(server.URL, file); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatal("both signals must be posted")
	}
}
