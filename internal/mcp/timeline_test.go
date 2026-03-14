package mcp

import "testing"

func TestTimelineBucket(t *testing.T) {
	b := TimelineBucket{
		Time:         "2024-01-01 12:00",
		RequestCount: 500,
		ErrorCount:   5,
		P95Ms:        75.0,
		ErrorRate:    0.01,
		IsAnomaly:    true,
		AnomalyType:  "error_spike",
	}

	if b.Time != "2024-01-01 12:00" {
		t.Errorf("Time = %q", b.Time)
	}
	if b.RequestCount != 500 {
		t.Errorf("RequestCount = %d, want 500", b.RequestCount)
	}
	if !b.IsAnomaly {
		t.Error("IsAnomaly should be true")
	}
	if b.AnomalyType != "error_spike" {
		t.Errorf("AnomalyType = %q, want %q", b.AnomalyType, "error_spike")
	}
}

func TestAnomaly(t *testing.T) {
	a := Anomaly{
		Time:        "2024-01-01 12:15",
		Type:        "latency_spike",
		Description: "P95 latency increased by 50%",
		Value:       150.0,
		Expected:    100.0,
	}

	if a.Type != "latency_spike" {
		t.Errorf("Type = %q, want %q", a.Type, "latency_spike")
	}
	if a.Value != 150.0 {
		t.Errorf("Value = %f, want 150.0", a.Value)
	}
	if a.Expected != 100.0 {
		t.Errorf("Expected = %f, want 100.0", a.Expected)
	}
}

func TestTimelineIn(t *testing.T) {
	in := TimelineIn{
		Service:     "my-service",
		Window:      120,
		Namespace:   "prod",
		TenantID:    "t1",
		Granularity: 10,
	}

	if in.Service != "my-service" {
		t.Errorf("Service = %q", in.Service)
	}
	if in.Window != 120 {
		t.Errorf("Window = %d, want 120", in.Window)
	}
	if in.Granularity != 10 {
		t.Errorf("Granularity = %d, want 10", in.Granularity)
	}
}
