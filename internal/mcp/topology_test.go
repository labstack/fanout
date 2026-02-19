package mcp

import "testing"

func TestRenderTopology(t *testing.T) {
	topo := &TopologyOut{
		Nodes: []ServiceNode{
			{Name: "api", Status: "healthy", SpanCount: 1000, P95Ms: 50.0, ErrorRate: 0.01},
			{Name: "db", Status: "degraded", SpanCount: 500, P95Ms: 100.0, ErrorRate: 0.05},
			{Name: "cache", Status: "unhealthy", SpanCount: 200, P95Ms: 200.0, ErrorRate: 0.15},
		},
		Edges: []ServiceEdge{
			{From: "api", To: "db", CallCount: 500, AvgMs: 50.0, ErrorRate: 0.02, Status: "healthy"},
			{From: "api", To: "cache", CallCount: 300, AvgMs: 10.0, ErrorRate: 0.0, Status: "healthy"},
		},
		ServiceCount: 3,
		EdgeCount:    2,
	}

	output := renderTopology(topo)

	if output.ASCII == "" {
		t.Error("renderTopology() should produce ASCII output")
	}
	if output.HTML == "" {
		t.Error("renderTopology() should produce HTML output")
	}
}

func TestRenderTopology_Empty(t *testing.T) {
	topo := &TopologyOut{
		Nodes:        []ServiceNode{},
		Edges:        []ServiceEdge{},
		ServiceCount: 0,
		EdgeCount:    0,
	}

	output := renderTopology(topo)

	if output.ASCII == "" {
		t.Error("renderTopology() should produce ASCII output for empty data")
	}
}

func TestRenderTopology_NoEdges(t *testing.T) {
	topo := &TopologyOut{
		Nodes: []ServiceNode{
			{Name: "standalone", Status: "healthy", SpanCount: 100, P95Ms: 10.0, ErrorRate: 0.0},
		},
		Edges:        []ServiceEdge{},
		ServiceCount: 1,
		EdgeCount:    0,
	}

	output := renderTopology(topo)

	if output.ASCII == "" || output.HTML == "" {
		t.Error("renderTopology() should produce output with no edges")
	}
}

func TestServiceNode(t *testing.T) {
	n := ServiceNode{
		Name:      "test-service",
		Status:    "healthy",
		SpanCount: 1000,
		P95Ms:     50.0,
		ErrorRate: 0.01,
	}

	if n.Name != "test-service" {
		t.Errorf("Name = %q, want %q", n.Name, "test-service")
	}
	if n.SpanCount != 1000 {
		t.Errorf("SpanCount = %d, want 1000", n.SpanCount)
	}
}

func TestServiceEdge(t *testing.T) {
	e := ServiceEdge{
		From:      "api",
		To:        "db",
		CallCount: 500,
		AvgMs:     25.0,
		ErrorRate: 0.02,
		Status:    "healthy",
	}

	if e.From != "api" {
		t.Errorf("From = %q, want %q", e.From, "api")
	}
	if e.To != "db" {
		t.Errorf("To = %q, want %q", e.To, "db")
	}
	if e.CallCount != 500 {
		t.Errorf("CallCount = %d, want 500", e.CallCount)
	}
}

func TestTopologyIn(t *testing.T) {
	in := TopologyIn{
		Window:    60,
		Namespace: "production",
		TenantID:  "tenant-1",
		Format:    "html",
	}

	if in.Window != 60 {
		t.Errorf("Window = %d, want 60", in.Window)
	}
	if in.Namespace != "production" {
		t.Errorf("Namespace = %q, want %q", in.Namespace, "production")
	}
}
