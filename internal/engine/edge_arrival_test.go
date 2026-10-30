package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestEdgeRollupRepairsRelatedArrivals(t *testing.T) {
	for _, tc := range []struct {
		name        string
		parentFirst bool
		gap         time.Duration
		want        int64
	}{
		{"child first across minute boundary", false, time.Minute, 1},
		{"parent first across minute boundary", true, time.Minute, 1},
		{"child at arrival gap boundary", false, edgeArrivalGap, 1},
		{"child beyond arrival gap", false, edgeArrivalGap + time.Nanosecond, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, repo := newBatchCacheTest(t)
			base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
			stamp := base.Add(time.Hour).UnixNano()
			first := stamp
			d.publicationBounds = func() (int64, int64, int64) { return stamp, stamp, first }
			child := telemetry.Span{Namespace: "shop", TraceID: "trace", SpanID: "child", ParentSpanID: "parent", ServiceName: "checkout", StartUnixNanos: base.Add(time.Second).UnixNano()}
			parent := telemetry.Span{Namespace: "shop", TraceID: "trace", SpanID: "parent", ServiceName: "gateway", StartUnixNanos: base.Add(-time.Second).UnixNano()}
			a, b := child, parent
			if tc.parentFirst {
				a, b = parent, child
			}
			commit := func(id string, span telemetry.Span) {
				span.IngestedAt = stamp
				if err := commitRestoredFixture(repo, t.Context(), telemetrystore.Batch{ID: id, Spans: []telemetry.Span{span}}); err != nil {
					t.Fatal(err)
				}
			}
			refresh := func() {
				for i := 0; i < 10; i++ {
					_, pending, err := d.refreshEdgeRollupPass(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					if !pending {
						return
					}
				}
				t.Fatal("edge pass did not converge")
			}
			commit("first", a)
			refresh()
			requireRowCount(t, d.DB, "SELECT count(*) FROM edge_rollup WHERE edge_type=?", "call", 0)
			// Advance the completed ingest watermark with an unrelated publication.
			// The repair horizon is measured from that watermark, not from wall time.
			stamp += int64(tc.gap) - 1
			commit("progress", telemetry.Span{Namespace: "other", TraceID: "trace", SpanID: "parent", ServiceName: "unrelated", StartUnixNanos: base.Add(-time.Second).UnixNano()})
			refresh()
			stamp++
			commit("second", b)
			refresh()
			var calls int64
			if err := d.DB.QueryRow(`SELECT coalesce(sum(calls),0) FROM edge_rollup WHERE namespace='shop' AND caller='gateway' AND callee='checkout' AND bucket=?`, base).Scan(&calls); err != nil || calls != tc.want {
				t.Fatalf("calls=%d want=%d err=%v", calls, tc.want, err)
			}
			requireRowCount(t, d.DB, "SELECT count(*) FROM edge_rollup WHERE caller=?", "unrelated", 0)
		})
	}
}

func TestEdgeRollupMessagingArrivalsUseAggregateKeys(t *testing.T) {
	for _, producerFirst := range []bool{true, false} {
		t.Run(fmt.Sprint("producerFirst=", producerFirst), func(t *testing.T) {
			d, repo := newBatchCacheTest(t)
			base := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
			makeSpan := func(kind, service, ns, destination, system string) telemetry.Span {
				return telemetry.Span{Namespace: ns, TraceID: service, SpanID: service, ServiceName: service, Kind: kind, StartUnixNanos: base.UnixNano(), Attributes: map[string]any{"messaging.destination.name": destination, "messaging.system": system}}
			}
			producer := makeSpan("SPAN_KIND_PRODUCER", "publisher", "shop", "jobs", "kafka")
			consumer := makeSpan("SPAN_KIND_CONSUMER", "worker", "shop", "jobs", "kafka")
			first, second := producer, consumer
			if !producerFirst {
				first, second = consumer, producer
			}
			commit := func(id string, spans ...telemetry.Span) {
				if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: id, Spans: spans}); err != nil {
					t.Fatal(err)
				}
				if _, _, err := d.refreshEdgeRollupPass(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			commit("first", first)
			commit("second", second, makeSpan("SPAN_KIND_PRODUCER", "wrong-namespace", "other", "jobs", "kafka"), makeSpan("SPAN_KIND_PRODUCER", "wrong-destination", "shop", "elsewhere", "kafka"), makeSpan("SPAN_KIND_PRODUCER", "wrong-system", "shop", "jobs", "other"))
			requireRowCount(t, d.DB, `SELECT count(*) FROM edge_rollup WHERE namespace='shop' AND caller='publisher' AND callee='worker' AND calls=1 AND edge_type=?`, "messaging", 1)
			requireRowCount(t, d.DB, `SELECT count(*) FROM edge_rollup WHERE caller LIKE ?`, "wrong-%", 0)
		})
	}
}

func TestEdgeArrivalLookupPrunesOldParquetAndReadsKeys(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Minute)
	watermark := at.UnixNano()
	for i, stamp := range []int64{watermark - int64(6*time.Minute), watermark - int64(time.Minute), watermark + 1} {
		span := telemetry.Span{Namespace: "ns", TraceID: "trace", SpanID: fmt.Sprint(i), ParentSpanID: "2", ServiceName: "api", StartUnixNanos: at.UnixNano(), IngestedAt: stamp}
		if err := commitRestoredFixture(repo, t.Context(), telemetrystore.Batch{ID: fmt.Sprint("batch-", i), Spans: []telemetry.Span{span}}); err != nil {
			t.Fatal(err)
		}
	}
	var key, plan string
	if err := d.DB.QueryRow("EXPLAIN (ANALYZE, FORMAT JSON) "+createEdgePassBucketsSQL, watermark, watermark+1).Scan(&key, &plan); err != nil {
		t.Fatal(err)
	}
	var profile any
	if err := json.Unmarshal([]byte(plan), &profile); err != nil {
		t.Fatal(err)
	}
	var recent map[string]any
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["type"] == "TABLE_SCAN" {
				extra := x["extra_info"].(map[string]any)
				cols := fmt.Sprint(extra["Projections"])
				if strings.Contains(cols, "parent_span_id") {
					recent = x
					for _, unrelated := range []string{"duration", "status", "resource", "service", "body"} {
						if strings.Contains(cols, unrelated) {
							t.Fatalf("repair lookup reads non-key column %q: %s", unrelated, cols)
						}
					}
				}
			}
			for _, child := range x {
				visit(child)
			}
		case []any:
			for _, child := range x {
				visit(child)
			}
		}
	}
	visit(profile)
	if recent == nil || recent["row_groups_scanned"] != float64(1) || recent["total_row_groups_to_scan"] != float64(3) {
		t.Fatalf("recent ingestion lookup did not prune two unrelated groups: %+v", recent)
	}
}
