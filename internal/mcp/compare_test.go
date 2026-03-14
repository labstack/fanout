package mcp

import "testing"

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
		Window:   "1h",
	}

	if len(in.Services) != 3 {
		t.Errorf("Services count = %d, want 3", len(in.Services))
	}
	if in.Window != "1h" {
		t.Errorf("Window = %q, want %q", in.Window, "1h")
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
