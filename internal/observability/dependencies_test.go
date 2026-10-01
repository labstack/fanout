package observability

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/queryrows"
)

func TestDependenciesKeyedTraversalIsBoundedAndScoped(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := query.CreateTables(db); err != nil {
		t.Fatal(err)
	}
	if err := query.CreateViews(db); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for _, edge := range []struct{ ns, a, b string }{
		{"prod", "a", "b"}, {"prod", "a", "c"}, {"prod", "b", "d"}, {"prod", "c", "d"}, {"prod", "d", "a"}, {"prod", "d", "e"},
		{"other", "b", "ghost"}, // This must not invent an a -> ghost path.
	} {
		if _, err := db.Exec("INSERT INTO edge_rollup (namespace,bucket,caller,callee,edge_type,calls,avg_ms,error_rate) VALUES (?,?,?,?,'call',1,1,0)", edge.ns, start, edge.a, edge.b); err != nil {
			t.Fatal(err)
		}
	}
	// A high-traffic edge outside the window must never participate.
	if _, err := db.Exec("INSERT INTO edge_rollup (namespace,bucket,caller,callee,edge_type,calls,avg_ms,error_rate) VALUES ('prod',?,'a','old','call',1000000,1,0)", start.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	svc := New(SQLDB(db), newTestRepository(t).Parquet, 30)
	scope := Scope{Start: start, End: start.Add(time.Minute)}
	for _, test := range []struct {
		options                DependencyOptions
		want                   []DependencyNode
		truncated, depth, node bool
	}{
		{DependencyOptions{Service: "a"}, []DependencyNode{{"prod", "a", 0}, {"prod", "b", 1}, {"prod", "c", 1}, {"prod", "d", 2}, {"prod", "e", 3}}, false, false, false},
		{DependencyOptions{Service: "a", MaxDepth: 1}, []DependencyNode{{"prod", "a", 0}, {"prod", "b", 1}, {"prod", "c", 1}}, true, true, false},
		{DependencyOptions{Service: "a", MaxNodes: 2}, []DependencyNode{{"prod", "a", 0}, {"prod", "b", 1}}, true, false, true},
		{DependencyOptions{Service: "a", Direction: "upstream"}, []DependencyNode{{"prod", "a", 0}, {"prod", "d", 1}, {"prod", "b", 2}, {"prod", "c", 2}}, false, false, false},
		{DependencyOptions{Service: "absent"}, []DependencyNode{}, false, false, false},
	} {
		result, err := svc.Dependencies(context.Background(), scope, test.options)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(result.Data.Nodes, test.want) || result.Data.Truncated != test.truncated || result.Data.DepthLimitReached != test.depth || result.Data.NodeLimitReached != test.node || result.Provenance.Complete == test.truncated {
			t.Fatalf("%#v: %#v", test.options, result)
		}
	}
	if _, err := svc.Dependencies(context.Background(), scope, DependencyOptions{Service: "a", MaxNodes: 501}); err == nil {
		t.Fatal("unbounded traversal accepted")
	}
	// The cap includes roots in every namespace, even when a root has no edges.
	if _, err := db.Exec("INSERT INTO service_rollup (namespace,bucket,service) VALUES ('aaa',?,'a')", start); err != nil {
		t.Fatal(err)
	}
	rootCap, err := svc.Dependencies(context.Background(), scope, DependencyOptions{Service: "a", MaxNodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rootCap.Data.Nodes, []DependencyNode{{"aaa", "a", 0}}) || !rootCap.Data.NodeLimitReached || rootCap.Data.DepthLimitReached || !rootCap.Data.Truncated {
		t.Fatalf("root admission cap: %#v", rootCap)
	}
	scope.Namespace = "aaa"
	isolated, err := svc.Dependencies(context.Background(), scope, DependencyOptions{Service: "a", MaxNodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(isolated.Data.Nodes) != 1 || isolated.Data.Truncated || !isolated.Provenance.Complete {
		t.Fatalf("isolated root at exact cap: %#v", isolated)
	}
	scope.Namespace = "prod"
	exact, err := svc.Dependencies(context.Background(), scope, DependencyOptions{Service: "a", MaxDepth: 3, MaxNodes: 5})
	if err != nil {
		t.Fatal(err)
	}
	if exact.Data.Truncated || len(exact.Data.Nodes) != 5 {
		t.Fatalf("complete graph at exact cap: %#v", exact)
	}
	both, err := svc.Dependencies(context.Background(), scope, DependencyOptions{Service: "a", MaxDepth: 1, MaxNodes: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !both.Data.Truncated || !both.Data.DepthLimitReached || !both.Data.NodeLimitReached {
		t.Fatalf("simultaneous exhaustion: %#v", both)
	}
	// A later shorter path must yield its minimum hop count, not a path count.
	if _, err := db.Exec("INSERT INTO edge_rollup (namespace,bucket,caller,callee,calls) VALUES ('prod',?,'a','e',1)", start); err != nil {
		t.Fatal(err)
	}
	shortest, err := svc.Dependencies(context.Background(), scope, DependencyOptions{Service: "a"})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range shortest.Data.Nodes {
		if node.Service == "e" && node.Hops != 1 {
			t.Fatalf("minimum hop count: %#v", node)
		}
	}
}

type dependencyQueryProbe func(context.Context) (queryrows.Rows, error)

func (p dependencyQueryProbe) QueryContext(ctx context.Context, _ string, _ ...any) (queryrows.Rows, error) {
	return p(ctx)
}

func TestDependenciesDeadlineAndCancellation(t *testing.T) {
	scope := Scope{Start: time.Now().Add(-time.Minute), End: time.Now()}
	var queryCtx context.Context
	probeErr := errors.New("probe")
	svc := New(dependencyQueryProbe(func(ctx context.Context) (queryrows.Rows, error) {
		queryCtx = ctx
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > dependencyDeadline {
			t.Fatal("dependency query lacks a bounded service deadline")
		}
		return nil, probeErr
	}), newTestRepository(t).Parquet, 30)
	if _, err := svc.Dependencies(context.Background(), scope, DependencyOptions{Service: "a"}); !errors.Is(err, probeErr) {
		t.Fatalf("query error lost: %v", err)
	}
	if queryCtx.Err() != context.Canceled {
		t.Fatal("service did not cancel its query context on exit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	svc.db = dependencyQueryProbe(func(queryCtx context.Context) (queryrows.Rows, error) {
		if deadline, _ := queryCtx.Deadline(); time.Until(deadline) > 10*time.Millisecond {
			t.Fatal("service extended its caller's deadline")
		}
		<-queryCtx.Done()
		return nil, queryCtx.Err()
	})
	if _, err := svc.Dependencies(ctx, scope, DependencyOptions{Service: "a"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation was not propagated: %v", err)
	}
}
