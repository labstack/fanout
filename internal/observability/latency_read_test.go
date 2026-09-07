package observability

import (
	"context"
	"database/sql"
	"math"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

// A bucket holding nothing but calls the service was waiting on must not decide
// the window. Excluding those spans inside the bucket is not enough on its own:
// a minute in which a ten-minute subscription is the only span still has to
// report something, and that something would otherwise become the service's
// worst bucket and its health.
func TestOverviewPrefersBucketsWhereTheServiceServedSomething(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
CREATE TABLE service_rollup (
  namespace VARCHAR, bucket TIMESTAMP, service VARCHAR,
  spans BIGINT, served_spans BIGINT, error_rate DOUBLE, p50_ms DOUBLE, p95_ms DOUBLE,
  log_count BIGINT, metric_count BIGINT
)`); err != nil {
		t.Fatalf("create service_rollup: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Minute)
	if _, err := db.Exec(`INSERT INTO service_rollup VALUES
    ('', ?, 'reviews', 1, 0, 0, 600000, 600000, 0, 0),
    ('', ?, 'reviews', 20, 20, 0, 30, 40, 0, 0),
    ('', ?, 'generator', 5, 0, 0, 250, 250, 0, 0)`,
		now.Add(-3*time.Minute), now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatalf("seed service_rollup: %v", err)
	}

	svc := New(SQLDB(db), newTestRepository(t).Parquet, 30)
	result, err := svc.Overview(context.Background(), Scope{Start: now.Add(-time.Hour), End: now.Add(time.Minute)}, 50)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	got := map[string]ServiceHealth{}
	for _, service := range result.Data.Services {
		got[service.Service] = service
	}
	if p95 := got["reviews"].P95MS; p95 > 100 {
		t.Fatalf("reviews p95 = %.0fms, want the minute it served (40ms), not the subscription it was holding", p95)
	}
	if got["reviews"].Health != HealthHealthy {
		t.Fatalf("reviews health = %q, want healthy", got["reviews"].Health)
	}
	// A service that served nothing all window keeps the only number it has.
	if p95 := got["generator"].P95MS; p95 != 250 {
		t.Fatalf("generator p95 = %.0fms, want 250ms rather than a flat zero", p95)
	}
}

// The card's headline figures are folded from the two comparison halves. A half
// spent holding a subscription open has no latency of its own, and letting it
// decide the number would put the card back in disagreement with the overview
// beside it.
func TestTotalsTakeLatencyFromHalvesThatServedSomething(t *testing.T) {
	served := performanceAggregate{Spans: 100, ServedSpans: 100, ErrorRate: 0.02, P50MS: 30, P95MS: 40}
	waiting := performanceAggregate{Spans: 3, ServedSpans: 0, ErrorRate: 0, P50MS: 600000, P95MS: 600000}

	totals := totalsOf(served, waiting)
	if totals.P95MS != 40 {
		t.Fatalf("p95 = %.0f, want the served half's 40ms rather than the subscription's 600000ms", totals.P95MS)
	}
	if totals.P50MS != 30 {
		t.Fatalf("p50 = %.0f, want 30ms", totals.P50MS)
	}
	// Counts and error rate still describe everything the service did.
	if totals.Spans != 103 {
		t.Fatalf("spans = %d, want every span in the window", totals.Spans)
	}
	if math.Abs(totals.ErrorRate-(0.02*100/103)) > 1e-9 {
		t.Fatalf("error rate = %v, want it weighted across all spans", totals.ErrorRate)
	}

	// With nothing served all window, the only numbers available are kept.
	onlyWaiting := totalsOf(waiting, waiting)
	if onlyWaiting.P95MS != 600000 {
		t.Fatalf("p95 = %.0f, want the subscription figure when there is nothing else", onlyWaiting.P95MS)
	}
}
