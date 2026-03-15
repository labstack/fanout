package mcp

import "testing"

func TestServiceNode(t *testing.T) {
	n := ServiceNode{
		Service:         "test-service",
		Status:          "healthy",
		Requests:        1000,
		P50Ms:           10.0,
		P95Ms:           50.0,
		ErrorRate:       0.01,
		UpstreamCount:   2,
		DownstreamCount: 3,
		BlastRadius:     0.45,
	}

	if n.Service != "test-service" {
		t.Errorf("Service = %q, want %q", n.Service, "test-service")
	}
	if n.Requests != 1000 {
		t.Errorf("Requests = %d, want 1000", n.Requests)
	}
	if n.P50Ms != 10.0 {
		t.Errorf("P50Ms = %v, want 10.0", n.P50Ms)
	}
	if n.UpstreamCount != 2 {
		t.Errorf("UpstreamCount = %d, want 2", n.UpstreamCount)
	}
	if n.DownstreamCount != 3 {
		t.Errorf("DownstreamCount = %d, want 3", n.DownstreamCount)
	}
	if n.BlastRadius != 0.45 {
		t.Errorf("BlastRadius = %v, want 0.45", n.BlastRadius)
	}
}

func TestServiceEdge(t *testing.T) {
	e := ServiceEdge{
		Source:    "api",
		Target:    "db",
		EdgeType:  "call",
		Calls:     500,
		AvgMs:     25.0,
		ErrorRate: 0.02,
	}

	if e.Source != "api" {
		t.Errorf("Source = %q, want %q", e.Source, "api")
	}
	if e.Target != "db" {
		t.Errorf("Target = %q, want %q", e.Target, "db")
	}
	if e.Calls != 500 {
		t.Errorf("Calls = %d, want 500", e.Calls)
	}
	if e.EdgeType != "call" {
		t.Errorf("EdgeType = %q, want %q", e.EdgeType, "call")
	}
}

func TestTopologyIn(t *testing.T) {
	in := TopologyIn{
		Window:          "1h",
		EdgeType:        "call",
		Depth:           3,
		Service:         "frontend",
		IncludeInactive: true,
		Namespace:       "production",
		TenantID:        "tenant-1",
	}

	if in.Window != "1h" {
		t.Errorf("Window = %q, want %q", in.Window, "1h")
	}
	if in.EdgeType != "call" {
		t.Errorf("EdgeType = %q, want %q", in.EdgeType, "call")
	}
	if in.Depth != 3 {
		t.Errorf("Depth = %d, want 3", in.Depth)
	}
	if in.Service != "frontend" {
		t.Errorf("Service = %q, want %q", in.Service, "frontend")
	}
	if !in.IncludeInactive {
		t.Errorf("IncludeInactive = false, want true")
	}
	if in.Namespace != "production" {
		t.Errorf("Namespace = %q, want %q", in.Namespace, "production")
	}
}

func TestTopologyOut(t *testing.T) {
	out := TopologyOut{
		Nodes: []ServiceNode{
			{
				Service:     "frontend",
				Status:      "degraded",
				Requests:    4397,
				ErrorRate:   0.036,
				P50Ms:       2.62,
				P95Ms:       16.17,
				BlastRadius: 0.85,
			},
		},
		Edges: []ServiceEdge{
			{
				Source:    "frontend",
				Target:    "product-catalog",
				EdgeType:  "call",
				Calls:     1770,
				ErrorRate: 0.0,
				AvgMs:     1.86,
			},
		},
		CriticalPaths: [][]string{
			{"load-generator", "frontend", "checkout", "payment"},
		},
	}

	if len(out.Nodes) != 1 {
		t.Errorf("Nodes count = %d, want 1", len(out.Nodes))
	}
	if len(out.Edges) != 1 {
		t.Errorf("Edges count = %d, want 1", len(out.Edges))
	}
	if len(out.CriticalPaths) != 1 {
		t.Errorf("CriticalPaths count = %d, want 1", len(out.CriticalPaths))
	}
	if len(out.CriticalPaths[0]) != 4 {
		t.Errorf("CriticalPaths[0] length = %d, want 4", len(out.CriticalPaths[0]))
	}
	if out.Nodes[0].BlastRadius != 0.85 {
		t.Errorf("BlastRadius = %v, want 0.85", out.Nodes[0].BlastRadius)
	}
}
