package observability

import (
	"reflect"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestTopologyExternalPeersIgnoreOutOfWindowParquetSpans(t *testing.T) {
	start := time.Date(2026, 10, 1, 12, 0, 0, 123, time.UTC)
	end := start.Add(time.Minute)
	for name, outside := range map[string]time.Time{"before": start.Add(-time.Nanosecond), "exclusive_end": end} {
		t.Run(name, func(t *testing.T) {
			var separate Topology
			for _, shared := range []bool{false, true} {
				_, repo, svc := newCompletedReadTest(t)
				clients := []telemetry.Span{
					{Namespace: "shop", ServiceName: "api", PeerService: "observed-peer", TraceID: "observed-trace", SpanID: "observed-client", Kind: "SPAN_KIND_CLIENT", StatusCode: "ERROR", StartUnixNanos: start.UnixNano(), EndUnixNanos: start.Add(time.Millisecond).UnixNano(), DurationMS: 1, IngestedAt: end.UnixNano()},
					{Namespace: "shop", ServiceName: "api", PeerService: "child-peer", TraceID: "child-trace", SpanID: "child-client", Kind: "SPAN_KIND_CLIENT", StatusCode: "ERROR", StartUnixNanos: start.UnixNano(), EndUnixNanos: start.Add(time.Millisecond).UnixNano(), DurationMS: 1, IngestedAt: end.UnixNano()},
				}
				stale := []telemetry.Span{
					{Namespace: "shop", ServiceName: "observed-peer", TraceID: "old-trace", SpanID: "old-observation", Kind: "SPAN_KIND_SERVER", StartUnixNanos: outside.UnixNano(), EndUnixNanos: outside.Add(time.Millisecond).UnixNano(), DurationMS: 1, IngestedAt: end.UnixNano()},
					{Namespace: "shop", ServiceName: "child-service", TraceID: "child-trace", SpanID: "old-child", ParentSpanID: "child-client", Kind: "SPAN_KIND_SERVER", StartUnixNanos: outside.UnixNano(), EndUnixNanos: outside.Add(time.Millisecond).UnixNano(), DurationMS: 1, IngestedAt: end.UnixNano()},
				}
				if shared {
					clients = append(clients, stale...)
				} else if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "outside", Spans: stale}); err != nil {
					t.Fatal(err)
				}
				if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "clients", Spans: clients}); err != nil {
					t.Fatal(err)
				}
				wantFiles := 2
				if shared {
					wantFiles = 1
				}
				if len(repo.Parquet.BatchMetadata()) != wantFiles {
					t.Fatalf("expected %d Parquet batches", wantFiles)
				}
				// Use the production reader's footer-pruned snapshot, not SQL fixture tables.
				got, err := svc.Topology(t.Context(), Scope{Namespace: "shop", Start: start, End: end}, 50)
				if err != nil {
					t.Fatal(err)
				}
				if len(got.Data.Nodes) != 2 || len(got.Data.Edges) != 2 {
					t.Fatalf("shared=%v: missing failing peers: %+v", shared, got.Data)
				}
				for _, node := range got.Data.Nodes {
					if node.Health != HealthUnhealthy || node.Spans != 1 || node.ErrorRate != 1 {
						t.Fatalf("shared=%v: peer=%+v", shared, node)
					}
				}
				if shared && !reflect.DeepEqual(got.Data, separate) {
					t.Fatalf("file grouping changed topology: shared=%+v separate=%+v", got.Data, separate)
				}
				separate = got.Data
			}
		})
	}
}
