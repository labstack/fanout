package mcp

import "testing"

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		max      int
		expected string
	}{
		// Short strings unchanged
		{"hello", 10, "hello"},
		{"", 10, ""},
		{"abc", 3, "abc"},
		{"ab", 5, "ab"},

		// Exact length
		{"hello", 5, "hello"},

		// Truncation needed
		{"hello world", 8, "hello..."},
		{"this is a long string", 10, "this is..."},
		{"abcdefghij", 6, "abc..."},

		// Edge cases
		{"hello", 3, "..."},
		{"hi", 2, "hi"},
		{"x", 1, "x"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := truncate(tc.input, tc.max)
			if result != tc.expected {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.input, tc.max, result, tc.expected)
			}
		})
	}
}

func TestServiceMetrics(t *testing.T) {
	m := ServiceMetrics{
		P50Ms:     10.0,
		P95Ms:     50.0,
		P99Ms:     100.0,
		ErrorRate: 0.05,
		Count:     1000,
	}

	if m.P50Ms != 10.0 {
		t.Errorf("P50Ms = %f, want 10.0", m.P50Ms)
	}
	if m.P95Ms != 50.0 {
		t.Errorf("P95Ms = %f, want 50.0", m.P95Ms)
	}
	if m.Count != 1000 {
		t.Errorf("Count = %d, want 1000", m.Count)
	}
}

func TestErrorDetail(t *testing.T) {
	e := ErrorDetail{
		Message:      "connection refused",
		Count:        42,
		ExampleTrace: "trace-xyz-123",
	}

	if e.Message != "connection refused" {
		t.Errorf("Message = %q, want %q", e.Message, "connection refused")
	}
	if e.Count != 42 {
		t.Errorf("Count = %d, want 42", e.Count)
	}
	if e.ExampleTrace != "trace-xyz-123" {
		t.Errorf("ExampleTrace = %q, want %q", e.ExampleTrace, "trace-xyz-123")
	}
}

func TestSlowOperation(t *testing.T) {
	op := SlowOperation{
		Name:  "POST /api/import",
		P95Ms: 2500.0,
		Count: 50,
	}

	if op.Name != "POST /api/import" {
		t.Errorf("Name = %q", op.Name)
	}
	if op.P95Ms != 2500.0 {
		t.Errorf("P95Ms = %f", op.P95Ms)
	}
}

func TestDependency(t *testing.T) {
	d := Dependency{
		Service:   "redis",
		Status:    "healthy",
		ErrorRate: 0.001,
		AvgMs:     2.0,
		Calls:     10000,
	}

	if d.Service != "redis" {
		t.Errorf("Service = %q", d.Service)
	}
	if d.Status != "healthy" {
		t.Errorf("Status = %q", d.Status)
	}
	if d.Calls != 10000 {
		t.Errorf("Calls = %d", d.Calls)
	}
}
