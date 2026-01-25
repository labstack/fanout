package mcp

import "testing"

func TestRenderCompare(t *testing.T) {
	compare := &CompareOut{
		Services: []CompareMetrics{
			{Service: "api-v1", Requests: 1000, ErrorRate: 0.01, P50Ms: 20.0, P95Ms: 50.0, AvgMs: 35.0, ErrorCount: 10},
			{Service: "api-v2", Requests: 800, ErrorRate: 0.005, P50Ms: 15.0, P95Ms: 40.0, AvgMs: 27.5, ErrorCount: 4},
		},
		Winner:  "api-v2",
		Summary: "Compared 2 services over 60 minutes. api-v2 has best performance.",
	}

	output := renderCompare(compare)

	if output.ASCII == "" {
		t.Error("renderCompare() should produce ASCII output")
	}
	if output.HTML == "" {
		t.Error("renderCompare() should produce HTML output")
	}
}

func TestRenderCompare_NoWinner(t *testing.T) {
	compare := &CompareOut{
		Services: []CompareMetrics{
			{Service: "new-service", Requests: 0, ErrorRate: 0, P50Ms: 0, P95Ms: 0},
			{Service: "another-new", Requests: 0, ErrorRate: 0, P50Ms: 0, P95Ms: 0},
		},
		Winner:  "",
		Summary: "Compared 2 services over 60 minutes. ",
	}

	output := renderCompare(compare)

	if output.ASCII == "" || output.HTML == "" {
		t.Error("renderCompare() should produce output with no winner")
	}
}

func TestRenderCompare_ThreeServices(t *testing.T) {
	compare := &CompareOut{
		Services: []CompareMetrics{
			{Service: "svc-a", Requests: 1000, ErrorRate: 0.01, P50Ms: 20.0, P95Ms: 50.0},
			{Service: "svc-b", Requests: 500, ErrorRate: 0.02, P50Ms: 30.0, P95Ms: 80.0},
			{Service: "svc-c", Requests: 200, ErrorRate: 0.005, P50Ms: 10.0, P95Ms: 25.0},
		},
		Winner:  "svc-c",
		Summary: "Compared 3 services over 60 minutes. svc-c has best performance.",
	}

	output := renderCompare(compare)

	if output.ASCII == "" || output.HTML == "" {
		t.Error("renderCompare() should produce output for 3 services")
	}
}

func TestGetString(t *testing.T) {
	tests := []struct {
		name     string
		row      map[string]interface{}
		key      string
		expected string
	}{
		{"string value", map[string]interface{}{"name": "test"}, "name", "test"},
		{"missing key", map[string]interface{}{"other": "value"}, "name", ""},
		{"non-string value", map[string]interface{}{"count": 42}, "count", ""},
		{"empty string", map[string]interface{}{"name": ""}, "name", ""},
		{"nil row", nil, "name", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := getString(tc.row, tc.key)
			if result != tc.expected {
				t.Errorf("getString() = %q, want %q", result, tc.expected)
			}
		})
	}
}

func TestGetInt64(t *testing.T) {
	tests := []struct {
		name     string
		row      map[string]interface{}
		key      string
		expected int64
	}{
		{"int64 value", map[string]interface{}{"count": int64(42)}, "count", 42},
		{"float64 value", map[string]interface{}{"count": float64(42.9)}, "count", 42},
		{"int value", map[string]interface{}{"count": 42}, "count", 42},
		{"missing key", map[string]interface{}{"other": 42}, "count", 0},
		{"string value", map[string]interface{}{"count": "42"}, "count", 0},
		{"nil row", nil, "count", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := getInt64(tc.row, tc.key)
			if result != tc.expected {
				t.Errorf("getInt64() = %d, want %d", result, tc.expected)
			}
		})
	}
}

func TestGetFloat64(t *testing.T) {
	tests := []struct {
		name     string
		row      map[string]interface{}
		key      string
		expected float64
	}{
		{"float64 value", map[string]interface{}{"rate": 0.05}, "rate", 0.05},
		{"int64 value", map[string]interface{}{"rate": int64(5)}, "rate", 5.0},
		{"int value", map[string]interface{}{"rate": 5}, "rate", 5.0},
		{"missing key", map[string]interface{}{"other": 0.05}, "rate", 0},
		{"string value", map[string]interface{}{"rate": "0.05"}, "rate", 0},
		{"nil row", nil, "rate", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := getFloat64(tc.row, tc.key)
			if result != tc.expected {
				t.Errorf("getFloat64() = %f, want %f", result, tc.expected)
			}
		})
	}
}

func TestCompareMetrics(t *testing.T) {
	m := CompareMetrics{
		Service:    "api-gateway",
		Requests:   10000,
		ErrorRate:  0.015,
		P50Ms:      25.0,
		P95Ms:      75.0,
		AvgMs:      50.0,
		ErrorCount: 150,
	}

	if m.Service != "api-gateway" {
		t.Errorf("Service = %q", m.Service)
	}
	if m.Requests != 10000 {
		t.Errorf("Requests = %d, want 10000", m.Requests)
	}
	if m.ErrorRate != 0.015 {
		t.Errorf("ErrorRate = %f, want 0.015", m.ErrorRate)
	}
	if m.P50Ms != 25.0 {
		t.Errorf("P50Ms = %f, want 25.0", m.P50Ms)
	}
	if m.P95Ms != 75.0 {
		t.Errorf("P95Ms = %f, want 75.0", m.P95Ms)
	}
}

func TestCompareIn(t *testing.T) {
	in := CompareIn{
		Services: []string{"api-v1", "api-v2", "api-v3"},
		Window:   120,
		Format:   "both",
	}

	if len(in.Services) != 3 {
		t.Errorf("Services count = %d, want 3", len(in.Services))
	}
	if in.Window != 120 {
		t.Errorf("Window = %d, want 120", in.Window)
	}
	if in.Format != "both" {
		t.Errorf("Format = %q, want %q", in.Format, "both")
	}
}

func TestCompareOut(t *testing.T) {
	out := CompareOut{
		Services: []CompareMetrics{
			{Service: "a", Requests: 100},
			{Service: "b", Requests: 200},
		},
		Winner:  "b",
		Summary: "b wins",
	}

	if len(out.Services) != 2 {
		t.Errorf("Services count = %d, want 2", len(out.Services))
	}
	if out.Winner != "b" {
		t.Errorf("Winner = %q, want %q", out.Winner, "b")
	}
}
