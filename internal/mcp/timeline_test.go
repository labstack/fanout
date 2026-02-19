package mcp

import "testing"

func TestRenderTimeline(t *testing.T) {
	timeline := &TimelineOut{
		Service: "api-gateway",
		Window:  60,
		Buckets: []TimelineBucket{
			{Time: "12:00", RequestCount: 100, ErrorCount: 1, P95Ms: 50.0, ErrorRate: 0.01},
			{Time: "12:05", RequestCount: 120, ErrorCount: 2, P95Ms: 55.0, ErrorRate: 0.017},
			{Time: "12:10", RequestCount: 90, ErrorCount: 0, P95Ms: 45.0, ErrorRate: 0.0},
		},
		Anomalies: []Anomaly{
			{Time: "12:05", Type: "latency_spike", Description: "P95 increased", Value: 55.0, Expected: 47.5},
		},
	}

	output := renderTimeline(timeline)

	if output.ASCII == "" {
		t.Error("renderTimeline() should produce ASCII output")
	}
	if output.HTML == "" {
		t.Error("renderTimeline() should produce HTML output")
	}
}

func TestRenderTimeline_Empty(t *testing.T) {
	timeline := &TimelineOut{
		Service:   "",
		Window:    15,
		Buckets:   []TimelineBucket{},
		Anomalies: []Anomaly{},
	}

	output := renderTimeline(timeline)

	if output.ASCII == "" {
		t.Error("renderTimeline() should produce ASCII output for empty data")
	}
}

func TestRenderTimeline_NoAnomalies(t *testing.T) {
	timeline := &TimelineOut{
		Service: "stable-service",
		Window:  30,
		Buckets: []TimelineBucket{
			{Time: "12:00", RequestCount: 100, ErrorCount: 1, P95Ms: 50.0, ErrorRate: 0.01},
			{Time: "12:05", RequestCount: 100, ErrorCount: 1, P95Ms: 50.0, ErrorRate: 0.01},
		},
		Anomalies: []Anomaly{},
	}

	output := renderTimeline(timeline)

	if output.ASCII == "" || output.HTML == "" {
		t.Error("renderTimeline() should produce output without anomalies")
	}
}

func TestRenderTimeline_SystemWide(t *testing.T) {
	// Service is empty for system-wide timeline
	timeline := &TimelineOut{
		Service: "",
		Window:  60,
		Buckets: []TimelineBucket{
			{Time: "12:00", RequestCount: 1000, ErrorCount: 10, P95Ms: 100.0, ErrorRate: 0.01},
		},
		Anomalies: []Anomaly{},
	}

	output := renderTimeline(timeline)

	// Should use "System Timeline" as title when service is empty
	if output.ASCII == "" || output.HTML == "" {
		t.Error("renderTimeline() should produce output for system-wide timeline")
	}
}

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
		Format:      "both",
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

func TestRenderTimeline_WithErrorAnomaly(t *testing.T) {
	timeline := &TimelineOut{
		Service: "error-service",
		Window:  30,
		Buckets: []TimelineBucket{
			{Time: "12:00", RequestCount: 100, ErrorCount: 50, P95Ms: 50.0, ErrorRate: 0.5, IsAnomaly: true, AnomalyType: "error_spike"},
		},
		Anomalies: []Anomaly{
			{Time: "12:00", Type: "error_spike", Description: "Error rate increased to 50%", Value: 0.5, Expected: 0.01},
		},
	}

	output := renderTimeline(timeline)

	if output.ASCII == "" || output.HTML == "" {
		t.Error("renderTimeline() should produce output with error anomaly")
	}
}
