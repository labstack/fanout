package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestSpanRollupReaggregatesAndRetires(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	const ns, svc = "ns\x00'", "svc\x00'"
	commit := func(id string, n int) {
		t.Helper()
		spans := make([]telemetry.Span, n)
		for i := range spans {
			kind, status := "SPAN_KIND_SERVER", "STATUS_CODE_OK"
			if i%2 == 0 {
				kind = "SPAN_KIND_CLIENT"
			}
			if i%3 == 0 {
				status = "STATUS_CODE_ERROR"
			}
			spans[i] = telemetry.Span{Namespace: ns, ServiceName: svc, TraceID: id, SpanID: fmt.Sprint(i), Kind: kind, StatusCode: status, StartUnixNanos: at.UnixNano(), DurationMS: float64(i), EndUnixNanos: at.Add(time.Duration(i) * time.Millisecond).UnixNano()}
		}
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: id, Spans: spans}); err != nil {
			t.Fatal(err)
		}
	}
	retainedFrom := time.Unix(0, 0).UTC()
	check := func(want int) {
		t.Helper()
		if _, err := d.refreshServiceRollup(t.Context()); err != nil {
			t.Fatal(err)
		}
		var count, buckets int
		if err := d.DB.QueryRow(`SELECT sum(spans), sum(list_sum(map_values(hist))) FROM span_rollup WHERE namespace=? AND service=?`, ns, svc).Scan(&count, &buckets); err != nil {
			t.Fatal(err)
		}
		if count != want || buckets != want {
			t.Fatalf("spans=%d hist=%d want=%d", count, buckets, want)
		}
		var diffs int
		if err := d.QueryRowScan(t.Context(), []any{&diffs}, `WITH expected AS (
   SELECT namespace,date_trunc('minute',start_time::TIMESTAMP_NS) bucket,service,kind,status,count(*) spans,sum(duration_ms) duration_sum,min(duration_ms) duration_min,max(duration_ms) duration_max FROM spans WHERE start_time >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS GROUP BY ALL
  ), differences AS ((SELECT * FROM expected EXCEPT ALL SELECT * EXCLUDE(hist) FROM span_rollup) UNION ALL (SELECT * EXCLUDE(hist) FROM span_rollup EXCEPT ALL SELECT * FROM expected)) SELECT count(*) FROM differences`, retainedFrom); err != nil || diffs != 0 {
			t.Fatalf("differences=%d: %v", diffs, err)
		}
	}
	commit("one", 6)
	check(6)
	requireRowCount(t, d.DB, `SELECT count(*) FROM span_rollup WHERE namespace=?`, ns, 4)
	requireRowCount(t, d.DB, `SELECT count(*) FROM span_rollup WHERE list_contains(map_keys(hist),-2147483648) AND namespace=?`, ns, 1)
	stamp, err := d.RollupWatermark(t.Context())
	if err != nil || stamp.IsZero() || stamp.After(time.Now()) {
		t.Fatalf("watermark=%v: %v", stamp, err)
	}
	commit("late", 4)
	check(10)
	for i := 0; i < 6; i++ {
		commit(fmt.Sprintf("extra-%d", i), 1)
	}
	check(16)
	if n, err := repo.CompactParquet(t.Context(), d, 8); err != nil || n == 0 {
		t.Fatalf("compact=%d: %v", n, err)
	}
	check(16)
	d.cfg.RetentionDays = 1
	if err := d.runRepositoryMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(16)
	at = at.Add(-48 * time.Hour)
	commit("expired", 2)
	check(18)
	if err := d.runRepositoryMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	retainedFrom = time.Now().Add(-24 * time.Hour)
	check(16)
	requireRowCount(t, d.DB, `SELECT count(*) FROM span_rollup WHERE bucket < ?::TIMESTAMP_NS`, time.Now().Add(-24*time.Hour), 0)
}

func TestSpanRollupSchemaReset(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateCacheTables(db); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`INSERT INTO service_rollup(namespace,bucket,service,spans) VALUES ('ns',now(),'svc',1)`, `INSERT INTO span_rollup VALUES ('ns',now(),'svc','kind','status',1,1,1,1,map([0],[1::UBIGINT]))`, `INSERT INTO rollup_state VALUES ('` + serviceRollupStateKey + `',123,now()), ('` + edgeRollupStateKey + `',456,now())`, `UPDATE rollup_schema_version SET version=-1`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := CreateCacheTables(db); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"service_rollup", "span_rollup"} {
		requireRowCount(t, db, "SELECT count(*) FROM "+table+" WHERE ?", true, 0)
	}
	requireRowCount(t, db, `SELECT count(*) FROM rollup_state WHERE cache_key=?`, serviceRollupStateKey, 0)
	requireRowCount(t, db, `SELECT count(*) FROM rollup_state WHERE cache_key=?`, edgeRollupStateKey, 0)
}

func TestSpanRollupIncludesMissingService(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "no-service", Spans: []telemetry.Span{{Namespace: "ns", TraceID: "trace", SpanID: "span", StartUnixNanos: at.UnixNano(), DurationMS: 0}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireRowCount(t, d.DB, `SELECT count(*) FROM span_rollup WHERE service='' AND spans=?`, 1, 1)
	requireRowCount(t, d.DB, `SELECT count(*) FROM service_rollup WHERE ?`, true, 0)
}

func TestSpanRollupFailureRollsBackServiceAndWatermark(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Add(-time.Hour)
	commit := func(id string) {
		t.Helper()
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: id, Spans: []telemetry.Span{{Namespace: "ns", ServiceName: "svc", TraceID: id, SpanID: "span", StartUnixNanos: at.UnixNano(), DurationMS: 1}}}); err != nil {
			t.Fatal(err)
		}
	}
	commit("one")
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	before, err := d.RollupWatermark(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	commit("two")
	if _, err := d.DB.Exec(`ALTER TABLE span_rollup ADD COLUMN incompatible BOOLEAN`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.refreshServiceRollup(t.Context()); err == nil {
		t.Fatal("incompatible insert succeeded")
	}
	for _, table := range []string{"service_rollup", "span_rollup"} {
		requireRowCount(t, d.DB, "SELECT count(*) FROM "+table+" WHERE spans=?", 1, 1)
	}
	after, err := d.RollupWatermark(t.Context())
	if err != nil || !after.Equal(before) {
		t.Fatalf("watermark advanced after rollback: %v %v: %v", before, after, err)
	}
	if _, err := d.DB.Exec(`ALTER TABLE span_rollup DROP COLUMN incompatible`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"service_rollup", "span_rollup"} {
		requireRowCount(t, d.DB, "SELECT count(*) FROM "+table+" WHERE spans=?", 2, 1)
	}
}
