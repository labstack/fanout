package observability

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/engine"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestTopologyExternalPeerAggregatesAndScopes(t *testing.T) {
	d, repo, svc := newCompletedReadTest(t)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	var spans []telemetry.Span
	add := func(namespace, service, peer, kind, status string, duration float64) int {
		i := len(spans)
		spans = append(spans, telemetry.Span{
			Namespace: namespace, ServiceName: service, PeerService: peer,
			TraceID: fmt.Sprintf("trace-%d", i), SpanID: fmt.Sprintf("span-%d", i),
			Kind: kind, StatusCode: status, DurationMS: duration,
			StartUnixNanos: start.UnixNano(), EndUnixNanos: start.Add(time.Duration(duration * float64(time.Millisecond))).UnixNano(),
			IngestedAt: end.UnixNano(),
		})
		return i
	}
	first := add("shop", "api", "external", "SPAN_KIND_CLIENT", "STATUS_CODE_ERROR", 10)
	add("shop", "api", "external", "SPAN_KIND_CLIENT", "OK", 30)
	add("shop", "worker", "external", "SPAN_KIND_CLIENT", "STATUS_CODE_ERROR", 50)
	add("shop", "worker", "external", "SPAN_KIND_CLIENT", "error", 70)
	add("shop", "worker", "healthy", "SPAN_KIND_CLIENT", "OK", 100)
	add("other", "api", "external", "SPAN_KIND_CLIENT", "STATUS_CODE_ERROR", 50)

	// Neither same-service children nor matching IDs in another trace or
	// namespace make a remote peer instrumented.
	for _, identity := range []struct{ namespace, service, trace string }{
		{"shop", "api", spans[first].TraceID},
		{"shop", "child", "different-trace"},
		{"other", "child", spans[first].TraceID},
	} {
		i := add(identity.namespace, identity.service, "", "SPAN_KIND_SERVER", "OK", 1)
		spans[i].TraceID, spans[i].ParentSpanID = identity.trace, spans[first].SpanID
	}
	// Observations must suppress peers even when a service filter excludes
	// the observed service; a cross-service child suppresses its client too.
	add("shop", "api", "observed", "SPAN_KIND_CLIENT", "STATUS_CODE_ERROR", 900)
	add("shop", "observed", "", "SPAN_KIND_SERVER", "OK", 1)
	client := add("shop", "api", "child-peer", "SPAN_KIND_CLIENT", "STATUS_CODE_ERROR", 900)
	child := add("shop", "child", "", "SPAN_KIND_SERVER", "OK", 1)
	spans[child].TraceID, spans[child].ParentSpanID = spans[client].TraceID, spans[client].SpanID
	add("shop", "api", "api", "SPAN_KIND_CLIENT", "STATUS_CODE_ERROR", 900)
	add("shop", "api", "", "SPAN_KIND_CLIENT", "STATUS_CODE_ERROR", 900)
	add("shop", "api", "external", "SPAN_KIND_SERVER", "STATUS_CODE_ERROR", 900)
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "peers", Spans: spans}); err != nil {
		t.Fatal(err)
	}
	warmTopology(t, d)
	node := func(name string, count int64, rate, p50, p95 float64) ServiceHealth {
		return ServiceHealth{Service: name, Health: classify(rate, p95), Spans: count, ErrorRate: rate, P50MS: p50, P95MS: p95}
	}
	edge := func(caller, callee string, count int64, average, rate float64) Edge {
		return Edge{Caller: caller, Callee: callee, Type: "call", Calls: count, AverageMS: average, ErrorRate: rate}
	}
	healthy := node("healthy", 1, 0, 100, 100)
	shopNodes := []ServiceHealth{node("external", 4, .5, 40, 49), healthy}
	shopEdges := []Edge{edge("api", "external", 2, 20, .5), edge("worker", "external", 2, 60, .5), edge("worker", "healthy", 1, 100, 0)}
	for _, tc := range []struct {
		name, namespace, service string
		limit                    int
		want                     Topology
	}{
		{"namespace", "shop", "", 50, Topology{Nodes: shopNodes, Edges: shopEdges}},
		{"caller", "shop", "api", 50, Topology{Nodes: []ServiceHealth{node("external", 2, .5, 20, 29)}, Edges: shopEdges[:1]}},
		{"second_caller", "shop", "worker", 50, Topology{Nodes: []ServiceHealth{node("external", 2, .5, 60, 69), healthy}, Edges: shopEdges[1:]}},
		{"callee", "shop", "external", 50, Topology{Nodes: shopNodes[:1], Edges: shopEdges[:2]}},
		{"other_namespace", "other", "", 50, Topology{Nodes: []ServiceHealth{node("external", 1, 1, 50, 50)}, Edges: []Edge{edge("api", "external", 1, 50, 1)}}},
		{"all_namespaces", "", "", 50, Topology{Nodes: []ServiceHealth{node("external", 5, .6, 42, 49.2), healthy}, Edges: []Edge{edge("api", "external", 3, 30, 2.0/3), shopEdges[1], shopEdges[2]}}},
		{"empty", "missing", "", 50, Topology{Nodes: []ServiceHealth{}, Edges: []Edge{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.Topology(t.Context(), Scope{Namespace: tc.namespace, Service: tc.service, Start: start, End: end}, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			// Instrumented services and their call edges are covered separately.
			got.Nodes = slices.DeleteFunc(got.Nodes, func(n ServiceHealth) bool { return n.Service != "external" && n.Service != "healthy" })
			got.Edges = slices.DeleteFunc(got.Edges, func(e Edge) bool { return e.Callee != "external" && e.Callee != "healthy" })
			if len(got.Nodes) != len(tc.want.Nodes) || !reflect.DeepEqual(got.Edges, tc.want.Edges) {
				t.Fatalf("topology = %+v, want %+v", got, tc.want)
			}
			for i, node := range got.Nodes {
				want := tc.want.Nodes[i]
				if node.Service != want.Service || node.Health != want.Health || node.Spans != want.Spans ||
					node.ErrorRate != want.ErrorRate || node.LogCount != want.LogCount || node.MetricCount != want.MetricCount ||
					math.Abs(node.P50MS-want.P50MS) > 1e-12 || math.Abs(node.P95MS-want.P95MS) > 1e-12 {
					t.Fatalf("node %d = %+v, want %+v", i, node, want)
				}
			}
		})
	}
}

func TestTopologyExternalPeersIgnoreOutOfWindowParquetSpans(t *testing.T) {
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	for name, outside := range map[string]time.Time{"before": start.Add(-time.Minute), "exclusive_end": end} {
		t.Run(name, func(t *testing.T) {
			var separate Topology
			for _, shared := range []bool{false, true} {
				d, repo, svc := newCompletedReadTest(t)
				clients := []telemetry.Span{
					{Namespace: "shop", ServiceName: "api", PeerService: "observed-peer", TraceID: "observed-trace", SpanID: "observed-client", Kind: "SPAN_KIND_CLIENT", StatusCode: "STATUS_CODE_ERROR", StartUnixNanos: start.UnixNano(), EndUnixNanos: start.Add(time.Millisecond).UnixNano(), DurationMS: 1, IngestedAt: end.UnixNano()},
					{Namespace: "shop", ServiceName: "api", PeerService: "child-peer", TraceID: "child-trace", SpanID: "child-client", Kind: "SPAN_KIND_CLIENT", StatusCode: "STATUS_CODE_ERROR", StartUnixNanos: start.UnixNano(), EndUnixNanos: start.Add(time.Millisecond).UnixNano(), DurationMS: 1, IngestedAt: end.UnixNano()},
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
				warmTopology(t, d)
				// Read the completed edge summaries.
				got, err := svc.Topology(t.Context(), Scope{Namespace: "shop", Start: start, End: end}, 50)
				if err != nil {
					t.Fatal(err)
				}
				got.Nodes = slices.DeleteFunc(got.Nodes, func(n ServiceHealth) bool { return n.Service != "observed-peer" })
				got.Edges = slices.DeleteFunc(got.Edges, func(e Edge) bool { return e.Callee != "observed-peer" })
				if len(got.Nodes) != 1 || len(got.Edges) != 1 {
					t.Fatalf("shared=%v: missing failing peers: %+v", shared, got)
				}
				for _, node := range got.Nodes {
					if node.Health != HealthUnhealthy || node.Spans != 1 || node.ErrorRate != 1 {
						t.Fatalf("shared=%v: peer=%+v", shared, node)
					}
				}
				if shared && !reflect.DeepEqual(got, separate) {
					t.Fatalf("file grouping changed topology: shared=%+v separate=%+v", got, separate)
				}
				separate = got
			}
		})
	}
}

func warmTopology(t *testing.T, d *engine.Duck) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); d.RunRollups(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := d.DB.QueryRow(`SELECT count(*) FROM edge_rollup WHERE edge_type='peer'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("peer rollup did not complete")
}
