package panel

import (
	"fmt"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

// fixtureStart is the start of every fixture window: twelve hours of data
// would be unnecessary; tests query 12:00–13:00 UTC on 2026-10-01.
var fixtureStart = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newTestEngine(t *testing.T) (*query.Duck, *telemetrystore.Repository) {
	t.Helper()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(); _ = repo.Close() })
	return d, repo
}

var batchSeq int

func commit(t *testing.T, repo *telemetrystore.Repository, spans []telemetry.Span, logs []telemetry.Log) {
	t.Helper()
	batchSeq++
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprintf("fixture-%d", batchSeq), Spans: spans, Logs: logs}); err != nil {
		t.Fatal(err)
	}
}

// shopSpans is one minute-spaced server span per (service, route) for an
// hour: checkout /cart (slow after 12:30, with errors), checkout /quote, and
// frontend /api/cart.
func shopSpans() []telemetry.Span {
	var out []telemetry.Span
	for minute := range 60 {
		at := fixtureStart.Add(time.Duration(minute) * time.Minute)
		add := func(service, route string, ms float64, status string) {
			n := at.UnixNano()
			out = append(out, telemetry.Span{
				Namespace: "shop", ServiceName: service, TraceID: fmt.Sprintf("%s-%s-%d", service, route, minute), SpanID: fmt.Sprintf("%s-%d", route, minute),
				Name: "GET " + route, Kind: "SPAN_KIND_SERVER", StartUnixNanos: n, EndUnixNanos: n + int64(ms*1e6), DurationMS: ms,
				StatusCode: status, HTTPMethod: "GET", HTTPRoute: route, ServiceVersion: "v1",
				Attributes: map[string]any{"http.route": route, "customer.tier": "gold"}, IngestedAt: n,
			})
		}
		slow, status := 100.0, "STATUS_CODE_OK"
		if minute >= 30 {
			slow = 900
			if minute%5 == 0 {
				status = "STATUS_CODE_ERROR"
			}
		}
		add("checkout", "/cart", slow, status)
		add("checkout", "/quote", 40, "STATUS_CODE_OK")
		add("frontend", "/api/cart", 20, "STATUS_CODE_OK")
	}
	return out
}
