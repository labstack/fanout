package engine

import (
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"testing"
	"time"
)

func TestPeerRollupLateChildRepairsEarlierMinute(t *testing.T) {
	d, repo := versionEngine(t)
	at := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	client := telemetry.Span{Namespace: "ns", ServiceName: "api", PeerService: "remote", TraceID: "trace", SpanID: "client", Kind: "SPAN_KIND_CLIENT", StartUnixNanos: at.UnixNano(), EndUnixNanos: at.Add(time.Second).UnixNano(), DurationMS: 1000, StatusCode: "STATUS_CODE_ERROR"}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "client", Spans: []telemetry.Span{client}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.refreshEdgeRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	var p95 float64
	if err := d.DB.QueryRow(`SELECT calls,p95_ms FROM edge_rollup WHERE edge_type='peer'`).Scan(&count, &p95); err != nil || count != 1 || p95 != 1000 {
		t.Fatalf("peer %d %v: %v", count, p95, err)
	}
	child := client
	child.ServiceName = "remote"
	child.SpanID = "child"
	child.ParentSpanID = "client"
	child.Kind = "SPAN_KIND_SERVER"
	child.PeerService = ""
	child.StartUnixNanos = at.Add(time.Minute).UnixNano()
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "child", Spans: []telemetry.Span{child}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.refreshEdgeRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM edge_rollup WHERE edge_type='peer'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale peer %d: %v", count, err)
	}
	if err := d.DB.QueryRow(`SELECT sum(calls) FROM edge_rollup WHERE edge_type='call'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("call %d: %v", count, err)
	}
}
