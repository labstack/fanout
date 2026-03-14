package mcp

import "testing"

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
	}

	if in.Window != 60 {
		t.Errorf("Window = %d, want 60", in.Window)
	}
	if in.Namespace != "production" {
		t.Errorf("Namespace = %q, want %q", in.Namespace, "production")
	}
}
