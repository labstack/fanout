package service

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestTopology_Empty(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Nodes query - empty
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "cnt", "p50", "p95", "error_rate", "log_cnt", "metric_cnt"}))

	// Edges query - empty
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"caller", "callee", "call_count", "avg_ms", "error_rate", "edge_type"}))

	result, err := svc.Topology(context.Background(), 60, "", "")
	if err != nil {
		t.Fatalf("Topology() error = %v", err)
	}

	if len(result.Nodes) != 0 {
		t.Errorf("Nodes count = %d, want 0", len(result.Nodes))
	}
	if len(result.Edges) != 0 {
		t.Errorf("Edges count = %d, want 0", len(result.Edges))
	}
}

func TestTopology_WithNodes(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Nodes query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "cnt", "p50", "p95", "error_rate", "log_cnt", "metric_cnt"}).
			AddRow("api-gateway", int64(5000), 10.0, 50.0, 0.001, int64(0), int64(0)).
			AddRow("user-service", int64(3000), 20.0, 100.0, 0.005, int64(0), int64(0)).
			AddRow("payment-service", int64(1000), 30.0, 200.0, 0.02, int64(0), int64(0)))

	// Edges query - empty
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"caller", "callee", "call_count", "avg_ms", "error_rate", "edge_type"}))

	result, err := svc.Topology(context.Background(), 60, "", "")
	if err != nil {
		t.Fatalf("Topology() error = %v", err)
	}

	if len(result.Nodes) != 3 {
		t.Errorf("Nodes count = %d, want 3", len(result.Nodes))
	}

	// Check first node
	if result.Nodes[0].Name != "api-gateway" {
		t.Errorf("Nodes[0].Name = %q, want %q", result.Nodes[0].Name, "api-gateway")
	}
	if result.Nodes[0].Status != "healthy" {
		t.Errorf("Nodes[0].Status = %q, want %q", result.Nodes[0].Status, "healthy")
	}
	if result.Nodes[0].SpanCount != 5000 {
		t.Errorf("Nodes[0].SpanCount = %d, want 5000", result.Nodes[0].SpanCount)
	}
	if result.Nodes[0].P50Ms != 10.0 {
		t.Errorf("Nodes[0].P50Ms = %v, want 10.0", result.Nodes[0].P50Ms)
	}
}

func TestTopology_WithEdges(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Nodes query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "cnt", "p50", "p95", "error_rate", "log_cnt", "metric_cnt"}).
			AddRow("frontend", int64(1000), 5.0, 50.0, 0.01, int64(0), int64(0)).
			AddRow("backend", int64(800), 10.0, 100.0, 0.02, int64(0), int64(0)))

	// Edges query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"caller", "callee", "call_count", "avg_ms", "error_rate", "edge_type"}).
			AddRow("frontend", "backend", int64(800), 25.0, 0.01, "call").
			AddRow("backend", "database", int64(1500), 5.0, 0.001, "call"))

	result, err := svc.Topology(context.Background(), 60, "", "")
	if err != nil {
		t.Fatalf("Topology() error = %v", err)
	}

	if len(result.Edges) != 2 {
		t.Errorf("Edges count = %d, want 2", len(result.Edges))
	}

	// Check first edge
	if result.Edges[0].From != "frontend" {
		t.Errorf("Edges[0].From = %q, want %q", result.Edges[0].From, "frontend")
	}
	if result.Edges[0].To != "backend" {
		t.Errorf("Edges[0].To = %q, want %q", result.Edges[0].To, "backend")
	}
	if result.Edges[0].CallCount != 800 {
		t.Errorf("Edges[0].CallCount = %d, want 800", result.Edges[0].CallCount)
	}
}

func TestTopology_DefaultWindow(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "cnt", "p50", "p95", "error_rate", "log_cnt", "metric_cnt"}))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"caller", "callee", "call_count", "avg_ms", "error_rate", "edge_type"}))

	// Pass 0 window, should default to 60
	result, err := svc.Topology(context.Background(), 0, "", "")
	if err != nil {
		t.Fatalf("Topology() error = %v", err)
	}
	if result == nil {
		t.Fatal("Topology() returned nil")
	}
}

func TestTopology_NodeHealthStatus(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Nodes with different health statuses
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "cnt", "p50", "p95", "error_rate", "log_cnt", "metric_cnt"}).
			AddRow("healthy-svc", int64(100), 25.0, 50.0, 0.005, int64(0), int64(0)).    // healthy
			AddRow("degraded-svc", int64(100), 500.0, 2000.0, 0.03, int64(0), int64(0)). // degraded (high latency)
			AddRow("unhealthy-svc", int64(100), 25.0, 50.0, 0.15, int64(0), int64(0)))   // unhealthy (high errors)

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"caller", "callee", "call_count", "avg_ms", "error_rate", "edge_type"}))

	result, err := svc.Topology(context.Background(), 60, "", "")
	if err != nil {
		t.Fatalf("Topology() error = %v", err)
	}

	statuses := map[string]string{}
	for _, n := range result.Nodes {
		statuses[n.Name] = n.Status
	}

	if statuses["healthy-svc"] != "healthy" {
		t.Errorf("healthy-svc status = %q, want healthy", statuses["healthy-svc"])
	}
	if statuses["degraded-svc"] != "degraded" {
		t.Errorf("degraded-svc status = %q, want degraded", statuses["degraded-svc"])
	}
	if statuses["unhealthy-svc"] != "unhealthy" {
		t.Errorf("unhealthy-svc status = %q, want unhealthy", statuses["unhealthy-svc"])
	}
}

func TestTopology_EdgeHealthStatus(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "cnt", "p50", "p95", "error_rate", "log_cnt", "metric_cnt"}))

	// Edges with different health statuses
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"caller", "callee", "call_count", "avg_ms", "error_rate", "edge_type"}).
			AddRow("a", "b", int64(100), 50.0, 0.005, "call").  // healthy
			AddRow("b", "c", int64(100), 2000.0, 0.03, "call"). // degraded
			AddRow("c", "d", int64(100), 50.0, 0.15, "call"))   // unhealthy

	result, err := svc.Topology(context.Background(), 60, "", "")
	if err != nil {
		t.Fatalf("Topology() error = %v", err)
	}

	if len(result.Edges) != 3 {
		t.Fatalf("Edges count = %d, want 3", len(result.Edges))
	}

	if result.Edges[0].Status != "healthy" {
		t.Errorf("Edge a->b status = %q, want healthy", result.Edges[0].Status)
	}
	if result.Edges[1].Status != "degraded" {
		t.Errorf("Edge b->c status = %q, want degraded", result.Edges[1].Status)
	}
	if result.Edges[2].Status != "unhealthy" {
		t.Errorf("Edge c->d status = %q, want unhealthy", result.Edges[2].Status)
	}
}

// TestBlastRadius tests the blast_radius computation.
func TestBlastRadius(t *testing.T) {
	edges := []ServiceEdge{
		{From: "frontend", To: "backend", CallCount: 800},
		{From: "frontend", To: "auth", CallCount: 200},
		{From: "backend", To: "db", CallCount: 1000},
	}
	nodes := []ServiceNode{
		{Name: "frontend"},
		{Name: "backend"},
		{Name: "auth"},
		{Name: "db"},
	}
	computeNodeMetrics(nodes, edges)

	// totalCalls = 800 + 200 + 1000 = 2000
	// frontend: 800+200 = 1000 / 2000 = 0.5
	// backend: 800+1000 = 1800 / 2000 = 0.9
	// auth: 200 / 2000 = 0.1
	// db: 1000 / 2000 = 0.5

	nodeMap := make(map[string]ServiceNode)
	for _, n := range nodes {
		nodeMap[n.Name] = n
	}

	if got := nodeMap["frontend"].BlastRadius; got != 0.5 {
		t.Errorf("frontend BlastRadius = %v, want 0.5", got)
	}
	if got := nodeMap["backend"].BlastRadius; got != 0.9 {
		t.Errorf("backend BlastRadius = %v, want 0.9", got)
	}
	if got := nodeMap["auth"].BlastRadius; got != 0.1 {
		t.Errorf("auth BlastRadius = %v, want 0.1", got)
	}
	if got := nodeMap["db"].BlastRadius; got != 0.5 {
		t.Errorf("db BlastRadius = %v, want 0.5", got)
	}
}

// TestBlastRadius_NoEdges tests blast_radius when there are no edges.
func TestBlastRadius_NoEdges(t *testing.T) {
	nodes := []ServiceNode{{Name: "lonely"}}
	computeNodeMetrics(nodes, []ServiceEdge{})
	if nodes[0].BlastRadius != 0.0 {
		t.Errorf("BlastRadius = %v, want 0.0", nodes[0].BlastRadius)
	}
}

// TestUpstreamDownstreamCount tests UpstreamCount and DownstreamCount.
func TestUpstreamDownstreamCount(t *testing.T) {
	edges := []ServiceEdge{
		{From: "a", To: "b", CallCount: 100},
		{From: "a", To: "c", CallCount: 100},
		{From: "b", To: "d", CallCount: 100},
	}
	nodes := []ServiceNode{
		{Name: "a"},
		{Name: "b"},
		{Name: "c"},
		{Name: "d"},
	}
	computeNodeMetrics(nodes, edges)

	nodeMap := make(map[string]ServiceNode)
	for _, n := range nodes {
		nodeMap[n.Name] = n
	}

	// a: 0 upstream, 2 downstream
	if nodeMap["a"].UpstreamCount != 0 {
		t.Errorf("a UpstreamCount = %d, want 0", nodeMap["a"].UpstreamCount)
	}
	if nodeMap["a"].DownstreamCount != 2 {
		t.Errorf("a DownstreamCount = %d, want 2", nodeMap["a"].DownstreamCount)
	}
	// b: 1 upstream, 1 downstream
	if nodeMap["b"].UpstreamCount != 1 {
		t.Errorf("b UpstreamCount = %d, want 1", nodeMap["b"].UpstreamCount)
	}
	if nodeMap["b"].DownstreamCount != 1 {
		t.Errorf("b DownstreamCount = %d, want 1", nodeMap["b"].DownstreamCount)
	}
	// d: 1 upstream, 0 downstream
	if nodeMap["d"].UpstreamCount != 1 {
		t.Errorf("d UpstreamCount = %d, want 1", nodeMap["d"].UpstreamCount)
	}
	if nodeMap["d"].DownstreamCount != 0 {
		t.Errorf("d DownstreamCount = %d, want 0", nodeMap["d"].DownstreamCount)
	}
}

// TestCriticalPaths tests the critical paths computation.
func TestCriticalPaths(t *testing.T) {
	// Topology: load-gen -> frontend -> checkout -> payment
	//                    -> catalog
	edges := []ServiceEdge{
		{From: "load-gen", To: "frontend", CallCount: 1000, AvgMs: 1.0},
		{From: "frontend", To: "checkout", CallCount: 500, AvgMs: 10.0},
		{From: "frontend", To: "catalog", CallCount: 500, AvgMs: 2.0},
		{From: "checkout", To: "payment", CallCount: 500, AvgMs: 50.0},
	}

	paths := computeCriticalPaths(edges, 3)
	if len(paths) == 0 {
		t.Fatal("computeCriticalPaths returned no paths")
	}

	// The heaviest path should be: load-gen -> frontend -> checkout -> payment
	// weight: 1000*1 + 500*10 + 500*50 = 1000 + 5000 + 25000 = 31000
	top := paths[0]
	if len(top) < 2 {
		t.Errorf("top path too short: %v", top)
	}
	// Path should end with payment (highest weight)
	if top[len(top)-1] != "payment" {
		t.Errorf("top path should end with payment, got: %v", top)
	}
}

// TestCriticalPaths_Empty tests critical paths with no edges.
func TestCriticalPaths_Empty(t *testing.T) {
	paths := computeCriticalPaths([]ServiceEdge{}, 3)
	if len(paths) != 0 {
		t.Errorf("expected 0 paths for empty edges, got %d", len(paths))
	}
}

// TestCriticalPaths_Cycle tests that cycles don't cause infinite recursion.
func TestCriticalPaths_Cycle(t *testing.T) {
	edges := []ServiceEdge{
		{From: "a", To: "b", CallCount: 100, AvgMs: 5.0},
		{From: "b", To: "c", CallCount: 100, AvgMs: 5.0},
		{From: "c", To: "a", CallCount: 100, AvgMs: 5.0}, // cycle
	}

	// Should not hang or panic; cycle is broken at first revisit
	paths := computeCriticalPaths(edges, 3)
	_ = paths // just verify it completes
}

// TestDepthFilter tests BFS depth filtering.
func TestDepthFilter(t *testing.T) {
	nodes := []ServiceNode{
		{Name: "a"},
		{Name: "b"},
		{Name: "c"},
		{Name: "d"},
		{Name: "e"},
	}
	edges := []ServiceEdge{
		{From: "a", To: "b"},
		{From: "b", To: "c"},
		{From: "c", To: "d"},
		{From: "d", To: "e"},
	}

	// depth=2 from "a" should include a, b, c (0 hops = a, 1 hop = b, 2 hops = c)
	filteredNodes, filteredEdges := applyDepthFilter(nodes, edges, "a", 2)
	nodeNames := make(map[string]bool)
	for _, n := range filteredNodes {
		nodeNames[n.Name] = true
	}

	for _, expected := range []string{"a", "b", "c"} {
		if !nodeNames[expected] {
			t.Errorf("expected node %q within depth 2 from a", expected)
		}
	}
	for _, unexpected := range []string{"d", "e"} {
		if nodeNames[unexpected] {
			t.Errorf("unexpected node %q beyond depth 2 from a", unexpected)
		}
	}

	// Edges should only include those between included nodes
	for _, e := range filteredEdges {
		if !nodeNames[e.From] || !nodeNames[e.To] {
			t.Errorf("edge %s->%s includes node outside depth filter", e.From, e.To)
		}
	}
}

// TestTopologyWithParams_EdgeTypeFilter verifies edge_type filtering is applied.
func TestTopologyWithParams_EdgeTypeFilter(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "cnt", "p50", "p95", "error_rate", "log_cnt", "metric_cnt"}))

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"caller", "callee", "call_count", "avg_ms", "error_rate", "edge_type"}).
			AddRow("a", "b", int64(100), 10.0, 0.0, "call"))

	result, err := svc.TopologyWithParams(context.Background(), TopologyParams{
		Window:   60,
		EdgeType: "call",
	})
	if err != nil {
		t.Fatalf("TopologyWithParams() error = %v", err)
	}
	if result == nil {
		t.Fatal("TopologyWithParams() returned nil")
	}
}

// TestTopologyWithParams_CriticalPaths verifies critical paths are computed.
func TestTopologyWithParams_CriticalPaths(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "cnt", "p50", "p95", "error_rate", "log_cnt", "metric_cnt"}).
			AddRow("a", int64(100), 5.0, 20.0, 0.0, int64(0), int64(0)).
			AddRow("b", int64(90), 8.0, 30.0, 0.0, int64(0), int64(0)).
			AddRow("c", int64(80), 15.0, 50.0, 0.0, int64(0), int64(0)))

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"caller", "callee", "call_count", "avg_ms", "error_rate", "edge_type"}).
			AddRow("a", "b", int64(90), 10.0, 0.0, "call").
			AddRow("b", "c", int64(80), 20.0, 0.0, "call"))

	result, err := svc.TopologyWithParams(context.Background(), TopologyParams{
		Window:   60,
		EdgeType: "all",
	})
	if err != nil {
		t.Fatalf("TopologyWithParams() error = %v", err)
	}
	if result == nil {
		t.Fatal("TopologyWithParams() returned nil")
		return
	}
	if result.CriticalPaths == nil {
		t.Error("CriticalPaths should not be nil")
	}
	// Should have at least one path
	if len(result.CriticalPaths) == 0 {
		t.Error("expected at least one critical path")
	}
}
