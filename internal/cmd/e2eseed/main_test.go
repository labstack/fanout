package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/ingest"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/query"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
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
	if err := decodeFixture(data, &spec); err != nil {
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

func decodeFixture(data []byte, spec *panel.Dashboard) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(spec)
}

func TestAllPanelsFixtureRejectsUnknownFields(t *testing.T) {
	data, err := os.ReadFile("../../../ui/host/e2e/fixtures/all-panels.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []struct{ name, old, replacement string }{
		{"panel", `"id": "stat"`, `"titel": "typo", "id": "stat"`},
		{"variable", `"include_all": true`, `"includeAll": true, "include_all": true`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			mutated := bytes.Replace(data, []byte(mutation.old), []byte(mutation.replacement), 1)
			var spec panel.Dashboard
			if err := decodeFixture(mutated, &spec); err == nil {
				t.Fatal("fixture accepted an unknown field")
			}
		})
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
				if at.Before(base.Add(-24*time.Hour)) || !at.Before(base) {
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
				if at.Before(base.Add(-24*time.Hour)) || !at.Before(base) {
					t.Fatal("log outside near-now window")
				}
				severities[int32(log.SeverityNumber)] = true
				bodies[log.Body.GetStringValue()]++
				count++
			}
		}
	}
	if len(ids) != 8640 || parents != 7200 || errors != 432 || count != 8640 || len(services) != 6 || len(severities) != 4 || len(versions) != 2 || !db || !producer || !consumer || !routes {
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

func TestSeedCoversTwentyFourHoursDeterministically(t *testing.T) {
	base := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	data, err := payloads(13, base)
	if err != nil {
		t.Fatal(err)
	}
	var spans collectortrace.ExportTraceServiceRequest
	if err := proto.Unmarshal(data["traces"], &spans); err != nil {
		t.Fatal(err)
	}
	var lo, hi uint64
	count := 0
	for _, r := range spans.ResourceSpans {
		for _, s := range r.ScopeSpans {
			for _, p := range s.Spans {
				if count == 0 || p.StartTimeUnixNano < lo {
					lo = p.StartTimeUnixNano
				}
				if p.StartTimeUnixNano > hi {
					hi = p.StartTimeUnixNano
				}
				count++
			}
		}
	}
	if count != 8640 || int64(lo) != base.Add(-24*time.Hour).UnixNano() || int64(hi) < base.Add(-time.Minute).UnixNano() {
		t.Fatalf("count=%d bounds=%d..%d", count, lo, hi)
	}
	var logs collectorlogs.ExportLogsServiceRequest
	if err := proto.Unmarshal(data["logs"], &logs); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range logs.ResourceLogs {
		for _, s := range r.ScopeLogs {
			n += len(s.LogRecords)
		}
	}
	if n != 8640 {
		t.Fatalf("logs=%d", n)
	}
	again, err := payloads(13, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, signal := range []string{"traces", "logs"} {
		if !bytes.Equal(data[signal], again[signal]) {
			t.Fatalf("%s is not deterministic", signal)
		}
	}
}

// Seed through the product OTLP decoder, rather than maintaining a second row converter.
type fixtureSubmitter struct {
	repo     *telemetrystore.Repository
	sequence int
}

func (s *fixtureSubmitter) Submit(ctx context.Context, batch telemetrystore.Batch) error {
	s.sequence++
	batch.ID = fmt.Sprintf("e2e-fixture-%d", s.sequence)
	return s.repo.Commit(ctx, batch)
}

func TestPerformanceFixtureValidatesAndExecutes(t *testing.T) {
	data, err := os.ReadFile("../../../ui/host/e2e/fixtures/performance.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec panel.Dashboard
	if err := decodeFixture(data, &spec); err != nil {
		t.Fatal(err)
	}
	panel.Normalize(&spec)
	if problems := panel.Validate(&spec); len(problems) != 0 {
		t.Fatalf("invalid fixture: %v", problems)
	}
	if len(spec.Panels) != 12 || spec.Time.Range != "24h" || spec.Time.Refresh != "off" {
		t.Fatal("expected twelve panels over twenty-four hours")
	}
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	server := grpc.NewServer(grpc.MaxRecvMsgSize(16 << 20))
	ingest.RegisterOTLP(server, ingest.NewServer(cfg, &fixtureSubmitter{repo: repo}))
	listener := bufconn.Listen(16 << 20)
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	base := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	payload, err := payloads(13, base)
	if err != nil {
		t.Fatal(err)
	}
	var spans collectortrace.ExportTraceServiceRequest
	var logs collectorlogs.ExportLogsServiceRequest
	if err := proto.Unmarshal(payload["traces"], &spans); err != nil {
		t.Fatal(err)
	}
	if err := proto.Unmarshal(payload["logs"], &logs); err != nil {
		t.Fatal(err)
	}
	if _, err := collectortrace.NewTraceServiceClient(conn).Export(t.Context(), &spans); err != nil {
		t.Fatal(err)
	}
	if _, err := collectorlogs.NewLogsServiceClient(conn).Export(t.Context(), &logs); err != nil {
		t.Fatal(err)
	}
	engine, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	if _, problems, err := panel.Check(t.Context(), engine, &spec); err != nil || len(problems) != 0 {
		t.Fatalf("fixture check: %v; %v", err, problems)
	}
	from := base.Add(-24 * time.Hour)
	results, err := panel.NewExecutor(engine, 30).Run(t.Context(), panel.RunRequest{Dashboard: spec, Time: &panel.Time{From: &from, To: &base}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 12 {
		t.Fatalf("results=%d", len(results))
	}
	seen := map[string]bool{}
	for _, result := range results {
		if seen[result.ID] || result.Status != panel.StatusOK || result.Frame == nil || result.Frame.Rows == 0 {
			t.Fatalf("unpopulated/duplicate panel: %+v", result)
		}
		seen[result.ID] = true
		if result.FromMS != from.UnixMilli() || result.ToMS != base.UnixMilli() {
			t.Fatalf("not a 24-hour window: %+v", result)
		}
		if result.ID == "count" || result.ID == "logs" {
			if len(result.Frame.Totals) != 2 || result.Frame.Totals[1] != float64(8640) {
				t.Fatalf("%s count=%v", result.ID, result.Frame.Totals)
			}
		}
	}
	for _, p := range spec.Panels {
		if !seen[p.ID] {
			t.Fatalf("missing panel %s", p.ID)
		}
	}
}
