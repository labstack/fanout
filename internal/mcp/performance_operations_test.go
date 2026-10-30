package mcp

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/engine"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestPerformanceSlowOperationsAcrossProtocols(t *testing.T) {
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
	from := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	cases := []struct{ protocol, service, kind, slow, fast, route string }{
		{"grpc", "checkout", "SPAN_KIND_SERVER", "CheckoutService/PlaceOrder", "CheckoutService/GetCart", ""},
		{"kafka", "fraud-detection", "SPAN_KIND_CONSUMER", "orders process", "refunds process", ""},
		{"http", "frontend", "SPAN_KIND_SERVER", "GET /cart", "GET /health", "/cart"},
	}
	var spans []telemetry.Span
	add := func(namespace, service, operation, kind, route string, ms float64) {
		n := from.Add(time.Minute).UnixNano()
		id := fmt.Sprintf("span-%d", len(spans))
		spans = append(spans, telemetry.Span{
			Namespace: namespace, ServiceName: service, TraceID: id, SpanID: id,
			Name: operation, Kind: kind, HTTPRoute: route, DurationMS: ms,
			StartUnixNanos: n, EndUnixNanos: n + int64(ms*1e6), IngestedAt: n,
		})
	}
	for _, tc := range cases {
		for _, ms := range []float64{100, 300} {
			add("shop", tc.service, tc.slow, tc.kind, tc.route, ms)
		}
		for _, ms := range []float64{20, 40} {
			add("shop", tc.service, tc.fast, tc.kind, tc.route, ms)
		}
		for _, kind := range []string{"SPAN_KIND_CLIENT", "SPAN_KIND_INTERNAL", "SPAN_KIND_PRODUCER"} {
			// Excluded kinds must neither create groups nor inflate a handled
			// operation's p95. Even an HTTP route must not admit these spans.
			add("shop", tc.service, "excluded "+kind, kind, "/outbound", 10000)
			add("shop", tc.service, tc.fast, kind, tc.route, 10000)
		}
		add("other", tc.service, "other namespace", tc.kind, tc.route, 10000)
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "operations", Spans: spans}); err != nil {
		t.Fatal(err)
	}
	s := &Server{panels: query.NewExecutor(duck, 30)}
	for _, tc := range cases {
		t.Run(tc.protocol, func(t *testing.T) {
			for _, limit := range []int{0, 1} {
				_, out, err := s.performance(t.Context(), nil, PerformanceInput{Service: tc.service, Namespace: "shop", From: &from, To: &to, Limit: limit})
				if err != nil {
					t.Fatal(err)
				}
				p := out.Dashboard.Panels[3]
				if p.ID != "operations" || p.Title != "Slow operations" || p.Viz != "table" || p.Unit != "ms" || p.Drill != "traces" || !reflect.DeepEqual(p.Query.By, []string{"operation"}) {
					t.Fatalf("operations panel = %+v", p)
				}
				wantLimit := 400
				if limit > 0 {
					wantLimit = limit
				}
				if p.Query.Limit != wantLimit {
					t.Fatalf("limit = %d, want %d", p.Query.Limit, wantLimit)
				}
				r := out.Results[3]
				wantOperations := []any{tc.slow, tc.fast}
				wantP95 := []float64{290, 39}
				if limit > 0 {
					wantOperations, wantP95 = wantOperations[:limit], wantP95[:limit]
				}
				if r.ID != "operations" || r.Status != query.StatusOK || r.Frame == nil || r.Frame.Rows != len(wantOperations) {
					t.Fatalf("operations result = %+v", r)
				}
				if len(r.Frame.Columns) != 2 || r.Frame.Columns[0].Name != "operation" || r.Frame.Columns[1].Name != "p95" || !reflect.DeepEqual(r.Frame.Values[0], wantOperations) {
					t.Fatalf("operations frame = %+v, want %v / %v", r.Frame, wantOperations, wantP95)
				}
				for i, want := range wantP95 {
					if got := numeric(r.Frame.Values[1][i]); got != want {
						t.Errorf("%s p95 = %v, want %v", wantOperations[i], got, want)
					}
				}
			}
		})
	}
}
