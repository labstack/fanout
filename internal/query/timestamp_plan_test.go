package query

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

func TestNativeTimestampPredicatesPruneParquetAndPreserveNanoseconds(t *testing.T) {
	// Use real format-3 files with three disjoint row groups, and a non-UTC
	// process timezone. Nanosecond window parameters must not lose precision.
	t.Setenv("TZ", "America/Los_Angeles")
	store, err := telemetry.OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base := time.Date(2026, 11, 1, 9, 0, 0, 123456789, time.UTC) // repeated DST hour
	spans := make([]telemetry.Span, 150000)
	for i := range spans {
		n := base.Add(time.Duration(i/50000)*time.Minute + time.Duration(i%50000)).UnixNano()
		spans[i] = telemetry.Span{TraceID: "trace", SpanID: fmt.Sprintf("%06d", i), StartUnixNanos: n, EndUnixNanos: n + 1, ServiceName: "api", DurationMS: 1}
	}
	if err := store.CommitBatch(context.Background(), telemetry.BatchMetadata{ID: "time"}, spans, nil, nil); err != nil {
		t.Fatal(err)
	}
	db := openTestDuck(t)
	if err := CreateParquetViews(db, store.Dir()); err != nil {
		t.Fatal(err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"SET enable_profiling='json'", "SET profiling_output=" + sqlLiteral(t.TempDir()+"/profile.json")} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	// Values carry a different timezone representation of the same UTC instant.
	lo := base.Add(time.Minute + 10).In(time.FixedZone("PST", -8*3600))
	hi := lo.Add(time.Nanosecond)
	const query = `SELECT COUNT(*), MIN(start_time)::TIMESTAMP_NS, approx_quantile(duration_ms, 0.95)
 FROM spans WHERE start_time >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS GROUP BY service`
	var count int64
	var got time.Time
	var quantile float64
	if err := db.QueryRow(query, lo, hi).Scan(&count, &got, &quantile); err != nil {
		t.Fatal(err)
	}
	if count != 1 || got.UnixNano() != lo.UnixNano() || quantile != 1 {
		t.Fatalf("native result count=%d time=%s quantile=%g", count, got, quantile)
	}
	var key, plan string
	if err := db.QueryRow("EXPLAIN ANALYZE "+query, lo, hi).Scan(&key, &plan); err != nil {
		t.Fatal(err)
	}
	var profile any
	if err := json.Unmarshal([]byte(plan), &profile); err != nil {
		t.Fatal(err)
	}
	var scan map[string]any
	var visit func(any)
	visit = func(value any) {
		switch x := value.(type) {
		case map[string]any:
			if x["type"] == "TABLE_SCAN" {
				scan = x
			}
			for _, v := range x {
				visit(v)
			}
		case []any:
			for _, v := range x {
				visit(v)
			}
		}
	}
	visit(profile)
	if scan == nil {
		t.Fatalf("no Parquet scan: %s", plan)
	}
	if scan["row_groups_scanned"] != float64(1) || scan["total_row_groups_to_scan"] != float64(3) {
		t.Fatalf("row groups not pruned: %#v", scan)
	}
	filters := scan["extra_info"].(map[string]any)["Filters"].(string)
	if strings.Contains(filters, "CAST(start_time") || !strings.Contains(filters, "TIMESTAMPTZ_NS") {
		t.Fatalf("timestamp filter casts the column: %s", filters)
	}
	// The old view cast is a negative control: it must not pass the guard.
	old := strings.ReplaceAll(query, "WHERE start_time", "WHERE start_time::TIMESTAMP_NS")
	old = strings.ReplaceAll(old, "AND start_time <", "AND start_time::TIMESTAMP_NS <")
	old = strings.ReplaceAll(old, "?::TIMESTAMP_NS::TIMESTAMPTZ_NS", "?::TIMESTAMP_NS")
	if err := db.QueryRow("EXPLAIN ANALYZE "+old, lo, hi).Scan(&key, &plan); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(plan), &profile); err != nil {
		t.Fatal(err)
	}
	scan = nil
	visit(profile)
	if scan == nil || scan["row_groups_scanned"] != float64(3) {
		t.Fatalf("negative control unexpectedly pruned: %#v", scan)
	}
}
