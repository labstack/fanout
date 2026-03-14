package mcp

import (
	"testing"
)

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

func TestBaselineComparison(t *testing.T) {
	b := BaselineComparison{
		P95Ratio:       18.9,
		BaselineP95Ms:  500.0,
		BaselineWindow: "7d",
	}

	if b.P95Ratio != 18.9 {
		t.Errorf("P95Ratio = %f, want 18.9", b.P95Ratio)
	}
	if b.BaselineP95Ms != 500.0 {
		t.Errorf("BaselineP95Ms = %f, want 500.0", b.BaselineP95Ms)
	}
	if b.BaselineWindow != "7d" {
		t.Errorf("BaselineWindow = %q, want %q", b.BaselineWindow, "7d")
	}
}

func TestChangePoint(t *testing.T) {
	cp := ChangePoint{
		Time:   "2026-03-14T16:28:00Z",
		Metric: "p95_ms",
		Before: 480.0,
		After:  9200.0,
	}

	if cp.Time != "2026-03-14T16:28:00Z" {
		t.Errorf("Time = %q", cp.Time)
	}
	if cp.Metric != "p95_ms" {
		t.Errorf("Metric = %q, want %q", cp.Metric, "p95_ms")
	}
	if cp.Before != 480.0 {
		t.Errorf("Before = %f, want 480.0", cp.Before)
	}
	if cp.After != 9200.0 {
		t.Errorf("After = %f, want 9200.0", cp.After)
	}
}

func TestLogPattern(t *testing.T) {
	lp := LogPattern{
		Pattern:  "Batch size exceeds threshold",
		Count:    12,
		Severity: "WARN",
	}

	if lp.Pattern != "Batch size exceeds threshold" {
		t.Errorf("Pattern = %q", lp.Pattern)
	}
	if lp.Count != 12 {
		t.Errorf("Count = %d, want 12", lp.Count)
	}
	if lp.Severity != "WARN" {
		t.Errorf("Severity = %q, want %q", lp.Severity, "WARN")
	}
}

func TestDiagnoseOut_NewFields(t *testing.T) {
	out := DiagnoseOut{
		Service:         "checkout",
		Status:          "degraded",
		SymptomDetected: "latency",
		Metrics: ServiceMetrics{
			P95Ms: 9200.0,
			ComparisonToBaseline: &BaselineComparison{
				P95Ratio:       18.9,
				BaselineP95Ms:  500.0,
				BaselineWindow: "7d",
			},
		},
		ChangePoints: []ChangePoint{
			{Time: "2026-03-14T16:28:00Z", Metric: "p95_ms", Before: 480.0, After: 9200.0},
		},
		CorrelatedLogPatterns: []LogPattern{
			{Pattern: "Batch size exceeds threshold", Count: 12, Severity: "WARN"},
		},
	}

	if out.SymptomDetected != "latency" {
		t.Errorf("SymptomDetected = %q, want %q", out.SymptomDetected, "latency")
	}
	if out.Metrics.ComparisonToBaseline == nil {
		t.Fatal("ComparisonToBaseline should not be nil")
	}
	if out.Metrics.ComparisonToBaseline.P95Ratio != 18.9 {
		t.Errorf("P95Ratio = %f, want 18.9", out.Metrics.ComparisonToBaseline.P95Ratio)
	}
	if len(out.ChangePoints) != 1 {
		t.Errorf("ChangePoints count = %d, want 1", len(out.ChangePoints))
	}
	if len(out.CorrelatedLogPatterns) != 1 {
		t.Errorf("CorrelatedLogPatterns count = %d, want 1", len(out.CorrelatedLogPatterns))
	}
}

func TestDiagnoseIn_SymptomField(t *testing.T) {
	tests := []struct {
		name    string
		symptom string
	}{
		{"auto (default)", ""},
		{"latency", "latency"},
		{"errors", "errors"},
		{"throughput_drop", "throughput_drop"},
		{"explicit auto", "auto"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := DiagnoseIn{
				Service: "my-service",
				Window:  15,
				Symptom: tc.symptom,
			}
			// Verify the field is accessible and holds the expected value.
			if in.Symptom != tc.symptom {
				t.Errorf("Symptom = %q, want %q", in.Symptom, tc.symptom)
			}
		})
	}
}
