package mcp

import (
	"math"
	"strings"
	"testing"
)

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
	// services mode
	in := CompareIn{
		Mode:     "services",
		Services: []string{"api-v1", "api-v2", "api-v3"},
		Window:   "2h",
	}

	if in.Mode != "services" {
		t.Errorf("Mode = %q, want services", in.Mode)
	}
	if len(in.Services) != 3 {
		t.Errorf("Services count = %d, want 3", len(in.Services))
	}
	if in.Window != "2h" {
		t.Errorf("Window = %q, want 2h", in.Window)
	}

	// time mode
	inTime := CompareIn{
		Mode:    "time",
		Service: "checkout",
		Left:    map[string]string{"window": "2026-03-14T12:00:00Z/2026-03-14T14:00:00Z"},
		Right:   map[string]string{"window": "2026-03-14T14:00:00Z/2026-03-14T16:00:00Z"},
	}
	if inTime.Mode != "time" {
		t.Errorf("Mode = %q, want time", inTime.Mode)
	}
	if inTime.Service != "checkout" {
		t.Errorf("Service = %q, want checkout", inTime.Service)
	}
	if inTime.Left["window"] == "" {
		t.Error("Left.window should not be empty")
	}

	// operations mode
	inOps := CompareIn{
		Mode:    "operations",
		Service: "api",
		Left:    map[string]string{"operation": "GET /v1/users"},
		Right:   map[string]string{"operation": "GET /v2/users"},
	}
	if inOps.Mode != "operations" {
		t.Errorf("Mode = %q, want operations", inOps.Mode)
	}
	if inOps.Left["operation"] != "GET /v1/users" {
		t.Errorf("Left.operation = %q, want GET /v1/users", inOps.Left["operation"])
	}
}

func TestCompareOut(t *testing.T) {
	out := CompareOut{
		Mode: "services",
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
	if out.Mode != "services" {
		t.Errorf("Mode = %q, want services", out.Mode)
	}
}

func TestCompareOutTimeMode(t *testing.T) {
	out := CompareOut{
		Mode:       "time",
		LeftLabel:  "Before (12:00\u201314:00)",
		RightLabel: "After (14:00\u201316:00)",
		Comparison: map[string]MetricDiff{
			"latency": {
				LeftValue:                14.2,
				RightValue:               18.8,
				ChangePct:                32.4,
				Direction:                "regression",
				StatisticallySignificant: true,
			},
			"errors": {
				LeftValue:  0.01,
				RightValue: 0.03,
				ChangePct:  200.0,
				Direction:  "regression",
			},
			"throughput": {
				LeftValue:  100.0,
				RightValue: 95.0,
				ChangePct:  -5.0,
				Direction:  "stable",
			},
		},
		Verdict: "Regression in latency (+32%) and errors (+200%)",
	}

	if out.Mode != "time" {
		t.Errorf("Mode = %q, want time", out.Mode)
	}
	if out.LeftLabel == "" {
		t.Error("LeftLabel should not be empty")
	}
	if out.RightLabel == "" {
		t.Error("RightLabel should not be empty")
	}
	latency, ok := out.Comparison["latency"]
	if !ok {
		t.Fatal("comparison should contain latency key")
	}
	if latency.Direction != "regression" {
		t.Errorf("latency.Direction = %q, want regression", latency.Direction)
	}
	if !latency.StatisticallySignificant {
		t.Error("latency should be statistically significant")
	}
	if out.Verdict == "" {
		t.Error("Verdict should not be empty")
	}
}

func TestMetricDiff(t *testing.T) {
	tests := []struct {
		name           string
		leftVal        float64
		rightVal       float64
		higherIsBad    bool
		wantDirection  string
		wantChangePct  float64
		checkChangePct bool
	}{
		{"regression latency", 100.0, 150.0, true, "regression", 50.0, true},
		{"improvement latency", 100.0, 70.0, true, "improvement", -30.0, true},
		{"stable latency", 100.0, 103.0, true, "stable", 3.0, true},
		{"regression throughput", 100.0, 70.0, false, "regression", -30.0, true},
		{"improvement throughput", 100.0, 130.0, false, "improvement", 30.0, true},
		// When left is 0, changePct stays 0 and direction is "stable" (no percentage basis)
		{"zero left value", 0.0, 50.0, true, "stable", 0.0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			diff := makeMetricDiff(tc.leftVal, tc.rightVal, nil, nil, tc.higherIsBad)
			if diff.Direction != tc.wantDirection {
				t.Errorf("Direction = %q, want %q", diff.Direction, tc.wantDirection)
			}
			if tc.checkChangePct {
				got := math.Round(diff.ChangePct*10) / 10
				want := math.Round(tc.wantChangePct*10) / 10
				if math.Abs(got-want) > 0.1 {
					t.Errorf("ChangePct = %.1f, want %.1f", got, want)
				}
			}
		})
	}
}

func TestIsSignificant(t *testing.T) {
	tests := []struct {
		name    string
		left    []float64
		right   []float64
		wantSig bool
	}{
		{
			name:    "too few left buckets",
			left:    []float64{10, 11, 12, 13},
			right:   []float64{20, 21, 22, 23, 24},
			wantSig: false,
		},
		{
			name:    "too few right buckets",
			left:    []float64{10, 11, 12, 13, 14},
			right:   []float64{20, 21, 22},
			wantSig: false,
		},
		{
			name:    "clearly significant difference",
			left:    []float64{10, 10, 10, 10, 10},
			right:   []float64{100, 100, 100, 100, 100},
			wantSig: true,
		},
		{
			name:    "not significant (same values)",
			left:    []float64{10, 11, 10, 11, 10},
			right:   []float64{11, 10, 11, 10, 11},
			wantSig: false,
		},
		{
			name:    "empty slices",
			left:    nil,
			right:   nil,
			wantSig: false,
		},
		{
			name:    "significant with variance",
			left:    []float64{10, 12, 11, 10, 11, 12, 10},
			right:   []float64{50, 52, 51, 50, 51, 52, 50},
			wantSig: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isSignificant(tc.left, tc.right)
			if got != tc.wantSig {
				t.Errorf("isSignificant() = %v, want %v", got, tc.wantSig)
			}
		})
	}
}

func TestMeanStdDev(t *testing.T) {
	tests := []struct {
		name       string
		vals       []float64
		wantMean   float64
		wantStddev float64
	}{
		{
			name:       "empty",
			vals:       nil,
			wantMean:   0,
			wantStddev: 0,
		},
		{
			name:       "single value",
			vals:       []float64{5.0},
			wantMean:   5.0,
			wantStddev: 0,
		},
		{
			name:       "uniform values",
			vals:       []float64{10, 10, 10, 10},
			wantMean:   10,
			wantStddev: 0,
		},
		{
			name:       "known values",
			vals:       []float64{2, 4, 4, 4, 5, 5, 7, 9},
			wantMean:   5.0,
			wantStddev: 2.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mean, stddev := meanStdDev(tc.vals)
			if math.Abs(mean-tc.wantMean) > 0.001 {
				t.Errorf("mean = %.3f, want %.3f", mean, tc.wantMean)
			}
			if math.Abs(stddev-tc.wantStddev) > 0.001 {
				t.Errorf("stddev = %.3f, want %.3f", stddev, tc.wantStddev)
			}
		})
	}
}

func TestResolveFocus(t *testing.T) {
	// Default focus
	got := resolveFocus(nil)
	if len(got) != 3 {
		t.Errorf("default focus len = %d, want 3", len(got))
	}

	// Custom focus
	custom := []string{"latency", "errors"}
	got = resolveFocus(custom)
	if len(got) != 2 {
		t.Errorf("custom focus len = %d, want 2", len(got))
	}
	if got[0] != "latency" || got[1] != "errors" {
		t.Errorf("custom focus = %v, want [latency errors]", got)
	}
}

func TestBuildVerdict(t *testing.T) {
	tests := []struct {
		name         string
		comparison   map[string]MetricDiff
		wantNoSig    bool
		wantContains string
	}{
		{
			name: "all stable",
			comparison: map[string]MetricDiff{
				"latency":    {Direction: "stable"},
				"errors":     {Direction: "stable"},
				"throughput": {Direction: "stable"},
			},
			wantNoSig: true,
		},
		{
			name: "regression present",
			comparison: map[string]MetricDiff{
				"latency": {Direction: "regression", ChangePct: 32.4},
			},
			wantContains: "Regression",
		},
		{
			name: "improvement present",
			comparison: map[string]MetricDiff{
				"throughput": {Direction: "improvement", ChangePct: 20.0},
			},
			wantContains: "Improvement",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict := buildVerdict(tc.comparison)
			if tc.wantNoSig && verdict != "No significant differences detected." {
				t.Errorf("verdict = %q, want no-differences message", verdict)
			}
			if tc.wantContains != "" && !strings.Contains(verdict, tc.wantContains) {
				t.Errorf("verdict = %q, should contain %q", verdict, tc.wantContains)
			}
		})
	}
}

func TestAggregateBuckets(t *testing.T) {
	t.Run("empty buckets", func(t *testing.T) {
		got := aggregateBuckets(nil)
		if got.P95Ms != 0 || got.Throughput != 0 {
			t.Errorf("empty buckets should return zero stats, got %+v", got)
		}
	})

	t.Run("buckets with data", func(t *testing.T) {
		buckets := []rollupBucket{
			{P95Ms: 100, P50Ms: 50, ErrorRate: 0.01, Spans: 100},
			{P95Ms: 120, P50Ms: 60, ErrorRate: 0.02, Spans: 200},
			{P95Ms: 80, P50Ms: 40, ErrorRate: 0.0, Spans: 150},
		}
		got := aggregateBuckets(buckets)
		if got.P95Ms <= 0 {
			t.Errorf("P95Ms should be positive, got %f", got.P95Ms)
		}
		if got.Throughput <= 0 {
			t.Errorf("Throughput should be positive, got %f", got.Throughput)
		}
	})

	t.Run("buckets with zero spans skipped for latency avg", func(t *testing.T) {
		buckets := []rollupBucket{
			{P95Ms: 100, P50Ms: 50, ErrorRate: 0.01, Spans: 100},
			{P95Ms: 0, P50Ms: 0, ErrorRate: 0, Spans: 0},
		}
		got := aggregateBuckets(buckets)
		// Only one non-zero bucket contributes to averages
		if got.P95Ms != 100 {
			t.Errorf("P95Ms = %f, want 100 (zero-span buckets excluded)", got.P95Ms)
		}
	})
}

func TestBuildComparison(t *testing.T) {
	left := aggStats{P95Ms: 100, P50Ms: 50, ErrorRate: 0.01, Throughput: 200}
	right := aggStats{P95Ms: 150, P50Ms: 75, ErrorRate: 0.03, Throughput: 180}

	comparison := buildComparison(left, right, nil, nil, []string{"latency", "errors", "throughput"})

	if len(comparison) != 3 {
		t.Errorf("comparison len = %d, want 3", len(comparison))
	}

	latency, ok := comparison["latency"]
	if !ok {
		t.Fatal("missing latency key")
	}
	if latency.LeftValue != 100 {
		t.Errorf("latency.LeftValue = %f, want 100", latency.LeftValue)
	}
	if latency.RightValue != 150 {
		t.Errorf("latency.RightValue = %f, want 150", latency.RightValue)
	}
	if latency.Direction != "regression" {
		t.Errorf("latency.Direction = %q, want regression", latency.Direction)
	}

	throughput, ok := comparison["throughput"]
	if !ok {
		t.Fatal("missing throughput key")
	}
	// throughput dropped 10% — that's a regression (higher is better, drop is regression)
	if throughput.Direction != "regression" {
		t.Errorf("throughput.Direction = %q, want regression (drop in throughput)", throughput.Direction)
	}
}

func TestCompareInModeDefault(t *testing.T) {
	in := CompareIn{}
	if in.Mode != "" {
		t.Errorf("CompareIn.Mode default should be empty string, got %q", in.Mode)
	}
	// Simulate handler default
	mode := in.Mode
	if mode == "" {
		mode = "services"
	}
	if mode != "services" {
		t.Errorf("resolved mode = %q, want services", mode)
	}
}
