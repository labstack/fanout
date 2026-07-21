package observability

import (
	"context"
	"database/sql"
	"math"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

func TestEndpointRollupQueryMergesBucketsAndExactBoundaries(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`SET TimeZone='UTC'`); err != nil {
		t.Fatalf("set timezone: %v", err)
	}
	if _, err := db.Exec(`
CREATE TABLE spans (
  namespace VARCHAR, service VARCHAR, start_time TIMESTAMP, duration_ms DOUBLE,
  status VARCHAR, http_method VARCHAR, http_route VARCHAR, operation VARCHAR
);
CREATE TABLE endpoint_rollup (
  namespace TEXT, bucket TIMESTAMP, service TEXT, method TEXT, path TEXT,
  calls BIGINT, error_count BIGINT, duration_count BIGINT,
  duration_buckets STRUCT(
    le_0_1 UBIGINT, le_0_5 UBIGINT, le_1 UBIGINT, le_2_5 UBIGINT,
    le_5 UBIGINT, le_10 UBIGINT, le_25 UBIGINT, le_50 UBIGINT,
    le_100 UBIGINT, le_250 UBIGINT, le_500 UBIGINT, le_750 UBIGINT,
    le_1000 UBIGINT, le_2000 UBIGINT, le_5000 UBIGINT,
    le_30000 UBIGINT, le_300000 UBIGINT
  )
);`); err != nil {
		t.Fatalf("create tables: %v", err)
	}

	start := time.Date(2026, 7, 20, 10, 0, 30, 0, time.UTC)
	end := start.Add(3 * time.Minute)
	for _, seed := range []struct {
		bucket time.Time
		values string
		errors int
	}{
		{start.Truncate(time.Minute).Add(time.Minute), "(10.0),(100.0)", 0},
		{start.Truncate(time.Minute).Add(2 * time.Minute), "(500.0),(1000.0)", 1},
	} {
		q := `INSERT INTO endpoint_rollup
SELECT 'prod', ?, 'checkout', 'GET', '/pay', COUNT(*), ?, COUNT(ms), struct_pack(
  le_0_1 := COUNT(*) FILTER (WHERE ms <= 0.1), le_0_5 := COUNT(*) FILTER (WHERE ms <= 0.5),
  le_1 := COUNT(*) FILTER (WHERE ms <= 1), le_2_5 := COUNT(*) FILTER (WHERE ms <= 2.5),
  le_5 := COUNT(*) FILTER (WHERE ms <= 5), le_10 := COUNT(*) FILTER (WHERE ms <= 10),
  le_25 := COUNT(*) FILTER (WHERE ms <= 25), le_50 := COUNT(*) FILTER (WHERE ms <= 50),
  le_100 := COUNT(*) FILTER (WHERE ms <= 100), le_250 := COUNT(*) FILTER (WHERE ms <= 250),
  le_500 := COUNT(*) FILTER (WHERE ms <= 500), le_750 := COUNT(*) FILTER (WHERE ms <= 750),
  le_1000 := COUNT(*) FILTER (WHERE ms <= 1000), le_2000 := COUNT(*) FILTER (WHERE ms <= 2000),
  le_5000 := COUNT(*) FILTER (WHERE ms <= 5000), le_30000 := COUNT(*) FILTER (WHERE ms <= 30000),
  le_300000 := COUNT(*) FILTER (WHERE ms <= 300000)
)
FROM (VALUES ` + seed.values + `) t(ms)`
		if _, err := db.Exec(q, seed.bucket, seed.errors); err != nil {
			t.Fatalf("seed endpoint_rollup: %v", err)
		}
	}
	// Include raw rows for every minute. The query must use only the two partial
	// boundary minutes from raw spans; interior raw rows are already represented
	// by the rollup and would double count if the boundary predicate were wrong.
	if _, err := db.Exec(`INSERT INTO spans VALUES
('prod','checkout',?,5.0,'OK','GET','/pay','GET /pay'),
('prod','checkout',?,10.0,'OK','GET','/pay','GET /pay'),
('prod','checkout',?,100.0,'OK','GET','/pay','GET /pay'),
('prod','checkout',?,500.0,'ERROR','GET','/pay','GET /pay'),
('prod','checkout',?,1000.0,'OK','GET','/pay','GET /pay'),
('prod','checkout',?,2000.0,'ERROR','GET','/pay','GET /pay')`,
		start.Add(10*time.Second),
		start.Truncate(time.Minute).Add(time.Minute+10*time.Second),
		start.Truncate(time.Minute).Add(time.Minute+20*time.Second),
		start.Truncate(time.Minute).Add(2*time.Minute+10*time.Second),
		start.Truncate(time.Minute).Add(2*time.Minute+20*time.Second),
		end.Add(-10*time.Second),
	); err != nil {
		t.Fatalf("seed spans: %v", err)
	}

	svc := New(db, "prod")
	svc.endpointReady.Store(true)
	svc.endpointMature.Store(true)
	var cachedCalls, totalCachedCalls int64
	var minBucket, maxBucket time.Time
	if err := db.QueryRow(`SELECT COALESCE(SUM(calls), 0)::BIGINT, MIN(bucket), MAX(bucket) FROM endpoint_rollup`).Scan(&totalCachedCalls, &minBucket, &maxBucket); err != nil {
		t.Fatalf("query all cached calls: %v", err)
	}
	if err := db.QueryRow(`SELECT COALESCE(SUM(calls), 0)::BIGINT FROM endpoint_rollup
WHERE bucket >= ? AND bucket < ? AND namespace = 'prod' AND service = 'checkout'`,
		start.Truncate(time.Minute).Add(time.Minute), end.Truncate(time.Minute)).Scan(&cachedCalls); err != nil {
		t.Fatalf("query cached calls: %v", err)
	}
	if cachedCalls != 4 {
		t.Fatalf("cached calls = %d, want 4 (total=%d buckets=%s..%s)", cachedCalls, totalCachedCalls, minBucket, maxBucket)
	}
	got, source, err := svc.queryEndpoints(context.Background(), Scope{Namespace: "prod", Start: start, End: end}, "checkout", 10)
	if err != nil {
		t.Fatalf("queryEndpoints: %v", err)
	}
	if source != "endpoint_rollup + boundary spans" || len(got) != 1 {
		t.Fatalf("queryEndpoints = (%#v, %q), want one rollup endpoint", got, source)
	}
	endpoint := got[0]
	if endpoint.Calls != 6 || endpoint.P50MS != 100 || endpoint.P95MS != 2000 || endpoint.P99MS != 2000 {
		t.Fatalf("endpoint = %#v, want calls=6 p50=100 p95=p99=2000", endpoint)
	}
	if math.Abs(endpoint.ErrorRate-2.0/6.0) > 1e-9 {
		t.Fatalf("error rate = %v, want %v", endpoint.ErrorRate, 2.0/6.0)
	}
}
