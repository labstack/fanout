package query

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/env"
)

func TestRollupOnceRebuildsAffectedServiceBuckets(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables failed: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews failed: %v", err)
	}

	d := &Duck{
		DB:              db,
		cfg:             env.Config{RetentionDays: 30},
		lastMaintenance: time.Now(),
	}
	ctx := context.Background()

	bucket := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-a",
		traceID:   "trace-1",
		spanID:    "span-1",
		service:   "checkout",
		operation: "POST /checkout",
		start:     bucket.Add(5 * time.Second),
		duration:  50 * time.Millisecond,
		ingested:  100,
	})

	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("first rollupOnce failed: %v", err)
	}
	var ready int64
	if err := db.QueryRow(`SELECT last_ingested_unix_nano FROM rollup_state WHERE cache_key = ?`, EndpointReadyStateKey).Scan(&ready); err != nil {
		t.Fatalf("query endpoint readiness: %v", err)
	}
	if ready != 1 {
		t.Fatalf("endpoint readiness = %d, want 1 after initial backfill", ready)
	}

	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-a",
		traceID:   "trace-2",
		spanID:    "span-2",
		service:   "checkout",
		operation: "POST /checkout",
		start:     bucket.Add(15 * time.Second),
		duration:  70 * time.Millisecond,
		ingested:  200,
	})
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-b",
		traceID:   "trace-3",
		spanID:    "span-3",
		service:   "checkout",
		operation: "POST /checkout",
		start:     bucket.Add(25 * time.Second),
		duration:  40 * time.Millisecond,
		ingested:  300,
	})

	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("second rollupOnce failed: %v", err)
	}

	requireServiceRollupSpans(t, db, "ns-a", bucket, "checkout", 2)
	requireServiceRollupSpans(t, db, "ns-b", bucket, "checkout", 1)
	requireRowCount(t, db, `
SELECT count(*)
FROM service_rollup
WHERE bucket = ?
  AND service = 'checkout'`, bucket, 2)
}

func TestRollupOnceRebuildsAffectedEndpointBuckets(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables failed: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews failed: %v", err)
	}
	d := &Duck{DB: db, cfg: env.Config{RetentionDays: 30}, lastMaintenance: time.Now()}
	ctx := context.Background()
	bucket := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)

	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-a", traceID: "t-1", spanID: "s-1", service: "checkout",
		operation: "POST checkout", httpMethod: "POST", httpRoute: "/checkout",
		start: bucket.Add(5 * time.Second), duration: 10 * time.Millisecond, ingested: 100,
	})
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-a", traceID: "t-2", spanID: "s-2", service: "checkout",
		operation: "POST checkout", httpMethod: "POST", httpRoute: "/checkout", status: "STATUS_CODE_ERROR",
		start: bucket.Add(10 * time.Second), duration: 100 * time.Millisecond, ingested: 100,
	})
	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("first rollupOnce failed: %v", err)
	}
	// A late row in the same minute must rebuild, not append to, the cached
	// bucket; otherwise calls and histogram bins double count prior rows.
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-a", traceID: "t-3", spanID: "s-3", service: "checkout",
		operation: "POST checkout", httpMethod: "POST", httpRoute: "/checkout",
		start: bucket.Add(15 * time.Second), duration: 500 * time.Millisecond, ingested: 200,
	})
	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("second rollupOnce failed: %v", err)
	}

	var calls, errors, histogramCount int64
	if err := db.QueryRow(`
SELECT calls, error_count, duration_buckets.le_300000::BIGINT
FROM endpoint_rollup
WHERE namespace = 'ns-a' AND bucket = ? AND service = 'checkout'
  AND method = 'POST' AND path = '/checkout'`, bucket).Scan(&calls, &errors, &histogramCount); err != nil {
		t.Fatalf("query endpoint_rollup failed: %v", err)
	}
	if calls != 3 || errors != 1 || histogramCount != 3 {
		t.Fatalf("endpoint_rollup = calls:%d errors:%d histogram:%d, want 3/1/3", calls, errors, histogramCount)
	}
}

func TestRollupOnceRebuildsAffectedEdgeBuckets(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables failed: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews failed: %v", err)
	}

	d := &Duck{
		DB:              db,
		cfg:             env.Config{RetentionDays: 30},
		lastMaintenance: time.Now(),
	}
	ctx := context.Background()

	bucket := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Minute)
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-a",
		traceID:   "trace-1",
		spanID:    "parent-1",
		service:   "frontend",
		operation: "GET /checkout",
		kind:      "SPAN_KIND_SERVER",
		start:     bucket.Add(5 * time.Second),
		duration:  10 * time.Millisecond,
		ingested:  100,
	})
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace:    "ns-a",
		traceID:      "trace-1",
		spanID:       "child-1",
		parentSpanID: "parent-1",
		service:      "payments",
		operation:    "POST charge",
		kind:         "SPAN_KIND_CLIENT",
		start:        bucket.Add(10 * time.Second),
		duration:     80 * time.Millisecond,
		ingested:     100,
	})

	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("first rollupOnce failed: %v", err)
	}

	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-a",
		traceID:   "trace-2",
		spanID:    "parent-2",
		service:   "frontend",
		operation: "GET /checkout",
		kind:      "SPAN_KIND_SERVER",
		start:     bucket.Add(20 * time.Second),
		duration:  10 * time.Millisecond,
		ingested:  200,
	})
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace:    "ns-a",
		traceID:      "trace-2",
		spanID:       "child-2",
		parentSpanID: "parent-2",
		service:      "payments",
		operation:    "POST charge",
		kind:         "SPAN_KIND_CLIENT",
		start:        bucket.Add(25 * time.Second),
		duration:     90 * time.Millisecond,
		ingested:     200,
	})

	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("second rollupOnce failed: %v", err)
	}

	var calls int64
	err := db.QueryRow(`
SELECT calls
FROM edge_rollup
WHERE namespace = 'ns-a'
  AND bucket = ?
  AND caller = 'frontend'
  AND callee = 'payments'
  AND edge_type = 'call'`, bucket).Scan(&calls)
	if err != nil {
		t.Fatalf("query edge_rollup failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("edge_rollup calls = %d, want 2", calls)
	}
}

func TestRollupOnceIgnoresRowsWithoutBucketTimestamp(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables failed: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews failed: %v", err)
	}

	d := &Duck{
		DB:              db,
		cfg:             env.Config{RetentionDays: 30},
		lastMaintenance: time.Now(),
	}
	ctx := context.Background()

	bucket := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	insertRollupTestSpan(t, db, rollupTestSpan{
		namespace: "ns-a",
		traceID:   "trace-1",
		spanID:    "span-1",
		service:   "checkout",
		operation: "POST /checkout",
		start:     bucket.Add(10 * time.Second),
		duration:  50 * time.Millisecond,
		ingested:  100,
	})

	if _, err := db.Exec(`
INSERT INTO lake.logs (
  namespace,
  log_time,
  time_unix_nano,
  severity,
  severity_number,
  body,
  service,
  ingested_at,
  ingested_unix_nano
)
VALUES ('ns-a', NULL, 0, 'INFO', 9, 'missing time', 'checkout', now(), 200)`); err != nil {
		t.Fatalf("insert lake.logs failed: %v", err)
	}

	if _, err := db.Exec(`
INSERT INTO lake.metrics (
  namespace,
  metric_time,
  time_unix_nano,
  name,
  metric_type,
  service,
  value,
  ingested_at,
  ingested_unix_nano
)
VALUES ('ns-a', NULL, 0, 'cpu.usage', 'gauge', 'checkout', 1.0, now(), 300)`); err != nil {
		t.Fatalf("insert lake.metrics failed: %v", err)
	}

	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("rollupOnce failed: %v", err)
	}

	requireServiceRollupSpans(t, db, "ns-a", bucket, "checkout", 1)
	requireRowCount(t, db, `
SELECT count(*)
FROM service_rollup
WHERE namespace = 'ns-a'`, nil, 1)
}

// calls on a messaging edge counts consumed messages on the destination —
// NOT producer-span × consumer-span pairs, which the pre-aggregated join
// replaced (the raw join was quadratic per bucket and its COUNT(*) had no
// meaning).
func TestRollupOnceMessagingEdgeCountsConsumedMessages(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables failed: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews failed: %v", err)
	}

	d := &Duck{
		DB:              db,
		cfg:             env.Config{RetentionDays: 30},
		lastMaintenance: time.Now(),
	}
	ctx := context.Background()

	const msgAttrs = `{"messaging.destination.name": "orders", "messaging.system": "kafka"}`
	bucket := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Minute)
	for i, producer := range []string{"p-1", "p-2"} {
		insertRollupTestSpan(t, db, rollupTestSpan{
			namespace: "ns-a",
			traceID:   "trace-" + producer,
			spanID:    producer,
			service:   "checkout",
			operation: "orders publish",
			kind:      "SPAN_KIND_PRODUCER",
			attrs:     msgAttrs,
			start:     bucket.Add(time.Duration(i) * time.Second),
			duration:  time.Millisecond,
			ingested:  100,
		})
	}
	for i, consumer := range []string{"c-1", "c-2", "c-3"} {
		insertRollupTestSpan(t, db, rollupTestSpan{
			namespace: "ns-a",
			traceID:   "trace-" + consumer,
			spanID:    consumer,
			service:   "fulfillment",
			operation: "orders process",
			kind:      "SPAN_KIND_CONSUMER",
			attrs:     msgAttrs,
			start:     bucket.Add(time.Duration(10+i) * time.Second),
			duration:  time.Millisecond,
			ingested:  100,
		})
	}

	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("rollupOnce failed: %v", err)
	}

	var calls int64
	err := db.QueryRow(`
SELECT calls
FROM edge_rollup
WHERE namespace = 'ns-a'
  AND bucket = ?
  AND caller = 'checkout'
  AND callee = 'fulfillment'
  AND edge_type = 'messaging'`, bucket).Scan(&calls)
	if err != nil {
		t.Fatalf("query edge_rollup failed: %v", err)
	}
	if calls != 3 {
		t.Fatalf("messaging edge calls = %d, want 3 (consumed messages, not 6 producer-consumer pairs)", calls)
	}
}

// The call_edges parent join is bounded to the affected bucket range ±1h.
// A parent that was rolled up in an earlier pass and started more than an
// hour before the late-arriving child's bucket is outside the bound, so the
// edge is dropped — this pins both the bound and the accepted data loss.
func TestRollupOnceDropsCallEdgeParentOutsideWindow(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables failed: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews failed: %v", err)
	}

	d := &Duck{
		DB:              db,
		cfg:             env.Config{RetentionDays: 30},
		lastMaintenance: time.Now(),
	}
	ctx := context.Background()

	childBucket := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Minute)
	// ns-far: parent 2h before the child. ns-near: parent 50m before.
	for _, tc := range []struct {
		namespace string
		parentAge time.Duration
	}{
		{namespace: "ns-far", parentAge: 2 * time.Hour},
		{namespace: "ns-near", parentAge: 50 * time.Minute},
	} {
		insertRollupTestSpan(t, db, rollupTestSpan{
			namespace: tc.namespace,
			traceID:   "trace-1",
			spanID:    "parent-1",
			service:   "frontend",
			operation: "GET /slow",
			start:     childBucket.Add(-tc.parentAge),
			duration:  time.Millisecond,
			ingested:  100,
		})
	}
	// First pass rolls the parents up, so the second pass's affected set
	// contains only the children's bucket.
	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("first rollupOnce failed: %v", err)
	}
	for _, ns := range []string{"ns-far", "ns-near"} {
		insertRollupTestSpan(t, db, rollupTestSpan{
			namespace:    ns,
			traceID:      "trace-1",
			spanID:       "child-1",
			parentSpanID: "parent-1",
			service:      "payments",
			operation:    "POST charge",
			kind:         "SPAN_KIND_CLIENT",
			start:        childBucket.Add(5 * time.Second),
			duration:     time.Millisecond,
			ingested:     200,
		})
	}
	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("second rollupOnce failed: %v", err)
	}

	requireRowCount(t, db, `
SELECT count(*)
FROM edge_rollup
WHERE namespace = 'ns-far'
  AND edge_type = 'call'`, nil, 0)
	requireRowCount(t, db, `
SELECT count(*)
FROM edge_rollup
WHERE namespace = 'ns-near'
  AND edge_type = 'call'`, nil, 1)
}

func TestRollupWindow(t *testing.T) {
	const chunk = rollupChunkNanos
	tests := []struct {
		name          string
		lastWatermark int64
		minIngested   int64
		rawMax        int64
		wantStart     int64
		wantEnd       int64
		wantChunked   bool
	}{
		{
			name:          "first run starts at oldest row, not epoch",
			lastWatermark: 0, minIngested: 5000, rawMax: 6000,
			wantStart: 4999, wantEnd: 6000, wantChunked: false,
		},
		{
			name:          "first run with zero minIngested opens at zero",
			lastWatermark: 0, minIngested: 0, rawMax: 100,
			wantStart: 0, wantEnd: 100, wantChunked: false,
		},
		{
			name:          "wide backlog clips to one chunk",
			lastWatermark: 1000, minIngested: 0, rawMax: 1000 + 3*chunk,
			wantStart: 1000, wantEnd: 1000 + chunk, wantChunked: true,
		},
		{
			name:          "exactly one chunk wide is not chunked",
			lastWatermark: 1000, minIngested: 0, rawMax: 1000 + chunk,
			wantStart: 1000, wantEnd: 1000 + chunk, wantChunked: false,
		},
		{
			name:          "one past a chunk is chunked",
			lastWatermark: 1000, minIngested: 0, rawMax: 1000 + chunk + 1,
			wantStart: 1000, wantEnd: 1000 + chunk, wantChunked: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, chunked := rollupWindow(tt.lastWatermark, tt.minIngested, tt.rawMax)
			if start != tt.wantStart || end != tt.wantEnd || chunked != tt.wantChunked {
				t.Fatalf("rollupWindow(%d, %d, %d) = (%d, %d, %v), want (%d, %d, %v)",
					tt.lastWatermark, tt.minIngested, tt.rawMax,
					start, end, chunked, tt.wantStart, tt.wantEnd, tt.wantChunked)
			}
		})
	}
}

// A backlog wider than rollupChunkNanos must be processed across passes, one
// chunk each, not in a single unbounded statement (the 2026-06-13 prod
// incident: a first-run edge rollup over 12 days spilled 375 GiB and never
// committed).
func TestRollupOnceChunksWideBacklog(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables failed: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews failed: %v", err)
	}

	d := &Duck{
		DB:              db,
		cfg:             env.Config{RetentionDays: 30},
		lastMaintenance: time.Now(),
	}
	ctx := context.Background()

	bucket := time.Now().UTC().Truncate(time.Minute).Add(-5 * time.Minute)
	// Each era gets a lone span (service rollup) and a parent+child pair
	// (edge rollup) so BOTH rollups' chunked watermarks are asserted.
	insertEra := func(namespace string, ingested int64) {
		insertRollupTestSpan(t, db, rollupTestSpan{
			namespace: namespace,
			traceID:   "trace-solo",
			spanID:    "span-solo",
			service:   "checkout",
			operation: "POST /checkout",
			start:     bucket.Add(5 * time.Second),
			duration:  50 * time.Millisecond,
			ingested:  ingested,
		})
		insertRollupTestSpan(t, db, rollupTestSpan{
			namespace: namespace,
			traceID:   "trace-edge",
			spanID:    "parent-1",
			service:   "frontend",
			operation: "GET /checkout",
			start:     bucket.Add(10 * time.Second),
			duration:  time.Millisecond,
			ingested:  ingested,
		})
		insertRollupTestSpan(t, db, rollupTestSpan{
			namespace:    namespace,
			traceID:      "trace-edge",
			spanID:       "child-1",
			parentSpanID: "parent-1",
			service:      "payments",
			operation:    "POST charge",
			kind:         "SPAN_KIND_CLIENT",
			start:        bucket.Add(11 * time.Second),
			duration:     time.Millisecond,
			ingested:     ingested,
		})
	}
	insertEra("ns-old", 100)
	// Ingested two chunk-widths later: outside both the first pass's chunk
	// and the second's.
	insertEra("ns-new", 100+2*rollupChunkNanos)

	edgeCount := func(namespace string) int {
		t.Helper()
		var got int
		if err := db.QueryRow(`
SELECT count(*)
FROM edge_rollup
WHERE namespace = ?
  AND edge_type = 'call'`, namespace).Scan(&got); err != nil {
			t.Fatalf("count edge_rollup failed: %v", err)
		}
		return got
	}

	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("first rollupOnce failed: %v", err)
	}
	var endpointReady int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(last_ingested_unix_nano), 0)::BIGINT FROM rollup_state WHERE cache_key = ?`, EndpointReadyStateKey).Scan(&endpointReady); err != nil {
		t.Fatalf("query endpoint readiness after first chunk: %v", err)
	}
	if endpointReady != 0 {
		t.Fatalf("endpoint readiness after first chunk = %d, want 0", endpointReady)
	}
	requireServiceRollupSpans(t, db, "ns-old", bucket, "checkout", 1)
	if got := edgeCount("ns-old"); got != 1 {
		t.Fatalf("ns-old call edges after first pass = %d, want 1", got)
	}
	requireRowCount(t, db, `
SELECT count(*)
FROM service_rollup
WHERE namespace = 'ns-new'`, nil, 0)
	if got := edgeCount("ns-new"); got != 0 {
		t.Fatalf("ns-new call edges after first pass = %d, want 0", got)
	}

	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("second rollupOnce failed: %v", err)
	}
	if _, err := d.rollupOnce(ctx); err != nil {
		t.Fatalf("third rollupOnce failed: %v", err)
	}
	if err := db.QueryRow(`SELECT COALESCE(MAX(last_ingested_unix_nano), 0)::BIGINT FROM rollup_state WHERE cache_key = ?`, EndpointReadyStateKey).Scan(&endpointReady); err != nil {
		t.Fatalf("query endpoint readiness after catch-up: %v", err)
	}
	if endpointReady != 1 {
		t.Fatalf("endpoint readiness after catch-up = %d, want 1", endpointReady)
	}
	requireServiceRollupSpans(t, db, "ns-new", bucket, "checkout", 1)
	if got := edgeCount("ns-new"); got != 1 {
		t.Fatalf("ns-new call edges after catch-up = %d, want 1", got)
	}
}

type rollupTestSpan struct {
	namespace    string
	traceID      string
	spanID       string
	parentSpanID string
	service      string
	operation    string
	httpMethod   string
	httpRoute    string
	status       string
	kind         string
	attrs        string
	start        time.Time
	duration     time.Duration
	ingested     int64
}

func insertRollupTestSpan(t *testing.T, db *sql.DB, span rollupTestSpan) {
	t.Helper()

	kind := span.kind
	if kind == "" {
		kind = "SPAN_KIND_SERVER"
	}
	status := span.status
	if status == "" {
		status = "STATUS_CODE_OK"
	}
	end := span.start.Add(span.duration)

	if _, err := db.Exec(`
INSERT INTO lake.spans (
  namespace,
  trace_id,
  span_id,
  parent_span_id,
  service,
  operation,
	  http_method,
	  http_route,
  kind,
  attributes_json,
  start_time,
  end_time,
  start_unix_nano,
  end_unix_nano,
  duration_ms,
  status,
  ingested_at,
  ingested_unix_nano
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		span.namespace,
		span.traceID,
		span.spanID,
		nullIfEmpty(span.parentSpanID),
		span.service,
		span.operation,
		nullIfEmpty(span.httpMethod),
		nullIfEmpty(span.httpRoute),
		kind,
		nullIfEmpty(span.attrs),
		span.start,
		end,
		span.start.UnixNano(),
		end.UnixNano(),
		float64(span.duration)/float64(time.Millisecond),
		status,
		time.Unix(0, span.ingested).UTC(),
		span.ingested,
	); err != nil {
		t.Fatalf("insert lake.spans failed: %v", err)
	}
}

func requireServiceRollupSpans(t *testing.T, db *sql.DB, namespace string, bucket time.Time, service string, want int64) {
	t.Helper()

	var got int64
	err := db.QueryRow(`
SELECT spans
FROM service_rollup
WHERE namespace = ?
  AND bucket = ?
  AND service = ?`, namespace, bucket, service).Scan(&got)
	if err != nil {
		t.Fatalf("query service_rollup failed: %v", err)
	}
	if got != want {
		t.Fatalf("service_rollup spans = %d, want %d", got, want)
	}
}

func requireRowCount(t *testing.T, db *sql.DB, q string, arg any, want int) {
	t.Helper()

	var got int
	var err error
	if arg == nil {
		err = db.QueryRow(q).Scan(&got)
	} else {
		err = db.QueryRow(q, arg).Scan(&got)
	}
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if got != want {
		t.Fatalf("row count = %d, want %d", got, want)
	}
}

func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}
