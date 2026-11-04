package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestDashboardRollupsBoundRoutesAndRepairLateData(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	commit := func(id string, n int) {
		t.Helper()
		spans := make([]telemetry.Span, n)
		logs := make([]telemetry.Log, n)
		for i := range spans {
			spans[i] = telemetry.Span{Namespace: "ns", ServiceName: "svc", TraceID: id, SpanID: fmt.Sprint(i), Kind: "SPAN_KIND_SERVER", StatusCode: "STATUS_CODE_ERROR", StartUnixNanos: at.UnixNano(), DurationMS: 10, Attributes: map[string]any{"http.route": fmt.Sprintf("/route/%03d", i)}}
			logs[i] = telemetry.Log{Namespace: "ns", ServiceName: "svc", TimeUnixNanos: at.UnixNano(), Severity: "ERROR"}
		}
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: id, Spans: spans, Logs: logs}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(n int) {
		t.Helper()
		if _, err := d.refreshServiceRollup(t.Context()); err != nil {
			t.Fatal(err)
		}
		requireRowCount(t, d.DB, `SELECT count(*) FROM endpoint_rollup WHERE namespace=?`, "ns", endpointRouteLimit+1)
		var count, hist, over int
		if err := d.DB.QueryRow(`SELECT sum(spans),sum(list_sum(map_values(hist))),sum(spans) FILTER(WHERE overflow) FROM endpoint_rollup`).Scan(&count, &hist, &over); err != nil {
			t.Fatal(err)
		}
		if count != n || hist != n || over == 0 {
			t.Fatalf("count=%d hist=%d overflow=%d want=%d", count, hist, over, n)
		}
		var logCount int
		if err := d.DB.QueryRow(`SELECT sum(logs) FROM log_rollup`).Scan(&logCount); err != nil || logCount != n {
			t.Fatalf("logs=%d want=%d: %v", logCount, n, err)
		}
	}
	commit("first", 200)
	check(200)
	commit("late", 200)
	check(400)
	for i := 0; i < 6; i++ {
		commit(fmt.Sprintf("extra-%d", i), 200)
	}
	check(1600)
	if n, err := repo.CompactParquet(t.Context(), d, 8); err != nil || n == 0 {
		t.Fatalf("compact=%d: %v", n, err)
	}
	check(1600)
	d.cfg.RetentionDays = 1
	if err := d.runRepositoryMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(1600)
	at = at.Add(-48 * time.Hour)
	commit("expired", 1)
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := d.runRepositoryMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(1600)

}

func TestLogRollupIncludesMissingService(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Add(-time.Minute)
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "no-service", Logs: []telemetry.Log{{Namespace: "ns", TimeUnixNanos: at.UnixNano(), Severity: "ERROR"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireRowCount(t, d.DB, `SELECT count(*) FROM log_rollup WHERE service='' AND logs=?`, 1, 1)
}

func TestResourceBoundaryCombinesExactEdges(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	spans := []telemetry.Span{}
	for m := range 60 {
		for _, sec := range []int{10, 30, 50} {
			spans = append(spans, telemetry.Span{Namespace: "ns", ServiceName: "svc", TraceID: fmt.Sprint(m, sec), SpanID: "span", StartUnixNanos: at.Add(time.Duration(m)*time.Minute + time.Duration(sec)*time.Second).UnixNano(), Resource: map[string]any{"service.version": "1"}})
		}
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "hour", Spans: spans}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	w := queryrows.Window{Start: at.Add(10*time.Minute + 20*time.Second + time.Nanosecond), End: at.Add(20*time.Minute + 40*time.Second + time.Nanosecond)}
	var first, last int64
	if err := d.QueryRowScan(queryrows.WithInventoryWindow(queryrows.WithWindow(t.Context(), w), w), []any{&first, &last}, `SELECT epoch_ns(first_seen),epoch_ns(last_seen) FROM resource_inventory WHERE service='svc'`); err != nil {
		t.Fatal(err)
	}
	if first != at.Add(10*time.Minute+30*time.Second).UnixNano() || last != at.Add(20*time.Minute+30*time.Second).UnixNano() {
		t.Fatalf("first=%d last=%d", first, last)
	}
	source := d.resourceInventorySource(repo.Parquet.BatchMetadata(), map[string]bool{"hour": true}, w)
	if !strings.Contains(source, "read_resource_seconds") || !strings.Contains(source, " OR start_time >= ") {
		t.Fatal(source)
	}
}

func TestEndpointRollupUsesConvertedRouteValues(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Add(-time.Minute).UnixNano()
	spans := []telemetry.Span{}
	for i, value := range []any{"/cart", int64(42), true, []byte("bytes"), []any{"array"}, map[string]any{"nested": "value"}} {
		spans = append(spans, telemetry.Span{Namespace: "ns", ServiceName: "svc", TraceID: fmt.Sprint(i), SpanID: "span", StartUnixNanos: at, Attributes: map[string]any{"http.route": value}})
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "converted-routes", Spans: spans}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	var differences int
	if err := d.QueryRowScan(t.Context(), []any{&differences}, `WITH expected AS (
 SELECT attributes['http.route']::VARCHAR AS route,count(*)::HUGEINT AS spans FROM spans WHERE attributes['http.route']::VARCHAR<>'' GROUP BY 1
 ), actual AS (SELECT route,sum(spans) AS spans FROM endpoint_rollup GROUP BY route), differences AS (
 (SELECT * FROM expected EXCEPT ALL SELECT * FROM actual) UNION ALL (SELECT * FROM actual EXCEPT ALL SELECT * FROM expected)) SELECT count(*) FROM differences`); err != nil || differences != 0 {
		t.Fatalf("converted route differences=%d: %v", differences, err)
	}
}
