package observability

import (
	"context"
	"database/sql"
	"math"
	"strings"
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

func TestHistogramQuantileInterpolatesInsideTheBucket(t *testing.T) {
	// Six calls at 5, 10, 100, 500, 1000 and 2000ms.
	cumulative := []float64{0, 0, 0, 0, 1, 2, 2, 2, 3, 3, 4, 4, 5, 6, 6, 6, 6}
	if got := histogramQuantile(cumulative, 6, 0.50); math.Abs(got-100) > 1e-6 {
		t.Fatalf("p50 = %v, want 100", got)
	}
	// The bound alone would report 2000 for both, which is the tell that a
	// table is reporting buckets rather than latencies.
	p95, p99 := histogramQuantile(cumulative, 6, 0.95), histogramQuantile(cumulative, 6, 0.99)
	if math.Abs(p95-1700) > 1e-6 || math.Abs(p99-1940) > 1e-6 {
		t.Fatalf("p95/p99 = %v/%v, want 1700/1940", p95, p99)
	}
	if p95 >= p99 {
		t.Fatalf("p95 %v should sit below p99 %v", p95, p99)
	}
}

func TestHistogramQuantileHandlesEmptyAndOverflow(t *testing.T) {
	if got := histogramQuantile(nil, 0, 0.95); got != 0 {
		t.Fatalf("no data = %v, want 0", got)
	}
	if got := histogramQuantile([]float64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, 5, 0.95); got != 300000 {
		t.Fatalf("everything past the last boundary = %v, want the boundary itself", got)
	}
	// Ten calls somewhere in the 2.5–5ms bucket: the 95th sits near the top of
	// that bucket rather than exactly on its boundary, which is the point of
	// interpolating at all.
	if got := histogramQuantile([]float64{0, 0, 0, 0, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10}, 10, 0.95); math.Abs(got-4.875) > 1e-9 {
		t.Fatalf("quantile inside the last populated bucket = %v, want 4.875", got)
	}
}

func TestComparisonIgnoresSubMillisecondLatencyMoves(t *testing.T) {
	// 3.9ms to 4.0ms is 2.2%, which was enough to paint a red regression arrow
	// beside a number that had not meaningfully moved.
	quiet := comparisonMetric("P50 latency", "ms", 3.9, 4.0, true)
	if quiet.Direction != DirectionStable {
		t.Fatalf("direction = %q, want stable for a 0.1ms move", quiet.Direction)
	}
	if quiet.Significant {
		t.Fatalf("a 0.1ms move should never be significant: %#v", quiet)
	}
	// A real move still reads as one.
	real := comparisonMetric("P95 latency", "ms", 13690, 10410, true)
	if real.Direction != DirectionImprovement || !real.Significant {
		t.Fatalf("13.7s to 10.4s = %#v, want a significant improvement", real)
	}
	// The floor is for latency only; a rate moving by less than a point is
	// still a rate that moved.
	rate := comparisonMetric("Error rate", "%", 0.10, 0.40, true)
	if rate.Direction != DirectionRegression {
		t.Fatalf("error rate = %#v, want a regression", rate)
	}
}

func TestEndpointDurationBucketsAreConsistent(t *testing.T) {
	previous := 0.0
	for _, bucket := range endpointDurationBuckets {
		if bucket.Bound <= previous {
			t.Fatalf("bound %v follows %v; the boundaries must increase or interpolation reads a negative bucket width", bucket.Bound, previous)
		}
		previous = bucket.Bound
	}
	// The query reads these counts positionally, so its SELECT list has to be
	// this list — a reordering would report one bucket's count against
	// another's boundary and quietly shift every percentile.
	if !strings.Contains(endpointRollupQuery, endpointDurationColumns()) {
		t.Fatalf("the rollup query does not select %q in order", endpointDurationColumns())
	}
}

func TestComparisonFloorAlsoSilencesThePercentage(t *testing.T) {
	// No traffic in the earlier half sends change through the divide-by-zero
	// branch and out at 100%. Printed inside a grey "stable" badge, that is the
	// same number-against-colour contradiction the floor exists to remove.
	quiet := comparisonMetric("P50 latency", "ms", 0, 0.5, true)
	if quiet.Direction != DirectionStable || quiet.ChangePct != 0 {
		t.Fatalf("metric = %#v, want a stable reading with no percentage", quiet)
	}
	if quiet.Before != 0 || quiet.After != 0.5 {
		t.Fatalf("metric = %#v, the underlying figures should still be reported", quiet)
	}
}

// Interpolating inside the bucket moves an endpoint whose calls sit under a
// boundary out of the class the bound alone put it in. 100 calls at 700ms are
// healthy; reporting the 750ms bound called them degraded.
func TestInterpolationGradesAnEndpointOnItsCallsNotItsBucket(t *testing.T) {
	cumulative := make([]float64, len(endpointDurationBuckets))
	for i, bucket := range endpointDurationBuckets {
		if bucket.Bound >= 750 {
			cumulative[i] = 100
		}
	}
	p95 := histogramQuantile(cumulative, 100, 0.95)
	if p95 >= latencyDegradedThresholdMS {
		t.Fatalf("p95 = %v, want it under the %vms boundary its calls sit below", p95, latencyDegradedThresholdMS)
	}
	if classify(0, p95) != HealthHealthy {
		t.Fatalf("health = %q, want healthy for calls that never reached the boundary", classify(0, p95))
	}
}
