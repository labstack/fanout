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
		sqlmock.NewRows([]string{"service", "cnt", "p95", "error_rate"}))

	// Edges query - empty
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"caller", "callee", "call_count", "avg_ms", "error_rate"}))

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
		sqlmock.NewRows([]string{"service", "cnt", "p95", "error_rate"}).
			AddRow("api-gateway", int64(5000), 50.0, 0.001).
			AddRow("user-service", int64(3000), 100.0, 0.005).
			AddRow("payment-service", int64(1000), 200.0, 0.02))

	// Edges query - empty
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"caller", "callee", "call_count", "avg_ms", "error_rate"}))

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
}

func TestTopology_WithEdges(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Nodes query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"service", "cnt", "p95", "error_rate"}).
			AddRow("frontend", int64(1000), 50.0, 0.01).
			AddRow("backend", int64(800), 100.0, 0.02))

	// Edges query
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"caller", "callee", "call_count", "avg_ms", "error_rate"}).
			AddRow("frontend", "backend", int64(800), 25.0, 0.01).
			AddRow("backend", "database", int64(1500), 5.0, 0.001))

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
		sqlmock.NewRows([]string{"service", "cnt", "p95", "error_rate"}))
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"caller", "callee", "call_count", "avg_ms", "error_rate"}))

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
		sqlmock.NewRows([]string{"service", "cnt", "p95", "error_rate"}).
			AddRow("healthy-svc", int64(100), 50.0, 0.005).   // healthy
			AddRow("degraded-svc", int64(100), 2000.0, 0.03). // degraded (high latency)
			AddRow("unhealthy-svc", int64(100), 50.0, 0.15))  // unhealthy (high errors)

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"caller", "callee", "call_count", "avg_ms", "error_rate"}))

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
		sqlmock.NewRows([]string{"service", "cnt", "p95", "error_rate"}))

	// Edges with different health statuses
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"caller", "callee", "call_count", "avg_ms", "error_rate"}).
			AddRow("a", "b", int64(100), 50.0, 0.005).  // healthy
			AddRow("b", "c", int64(100), 2000.0, 0.03). // degraded
			AddRow("c", "d", int64(100), 50.0, 0.15))   // unhealthy

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
