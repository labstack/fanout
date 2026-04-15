package service

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAggregateBuckets(t *testing.T) {
	t.Run("empty buckets", func(t *testing.T) {
		got := AggregateBuckets(nil)
		if got.P95Ms != 0 || got.Throughput != 0 {
			t.Errorf("empty buckets should return zero stats, got %+v", got)
		}
	})

	t.Run("buckets with data", func(t *testing.T) {
		buckets := []RollupBucket{
			{P95Ms: 100, P50Ms: 50, ErrorRate: 0.01, Spans: 100},
			{P95Ms: 120, P50Ms: 60, ErrorRate: 0.02, Spans: 200},
			{P95Ms: 80, P50Ms: 40, ErrorRate: 0.0, Spans: 150},
		}
		got := AggregateBuckets(buckets)
		if got.P95Ms <= 0 {
			t.Errorf("P95Ms should be positive, got %f", got.P95Ms)
		}
		if got.Throughput <= 0 {
			t.Errorf("Throughput should be positive, got %f", got.Throughput)
		}
	})

	t.Run("buckets with zero spans skipped for latency avg", func(t *testing.T) {
		buckets := []RollupBucket{
			{P95Ms: 100, P50Ms: 50, ErrorRate: 0.01, Spans: 100},
			{P95Ms: 0, P50Ms: 0, ErrorRate: 0, Spans: 0},
		}
		got := AggregateBuckets(buckets)
		if got.P95Ms != 100 {
			t.Errorf("P95Ms = %f, want 100 (zero-span buckets excluded)", got.P95Ms)
		}
	})

	t.Run("all zero-span buckets", func(t *testing.T) {
		buckets := []RollupBucket{
			{P95Ms: 0, P50Ms: 0, ErrorRate: 0, Spans: 0},
			{P95Ms: 0, P50Ms: 0, ErrorRate: 0, Spans: 0},
		}
		got := AggregateBuckets(buckets)
		if got.Throughput != 0 {
			t.Errorf("Throughput should be 0 for zero-span buckets, got %f", got.Throughput)
		}
	})
}

func TestBuildComparison(t *testing.T) {
	left := AggStats{P95Ms: 100, P50Ms: 50, ErrorRate: 0.01, Throughput: 200}
	right := AggStats{P95Ms: 150, P50Ms: 75, ErrorRate: 0.03, Throughput: 180}

	comparison := BuildComparison(left, right, nil, nil, []string{"latency", "errors", "throughput"})

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
	if throughput.Direction != "regression" {
		t.Errorf("throughput.Direction = %q, want regression (drop in throughput)", throughput.Direction)
	}
}

func TestBuildComparison_WithBuckets(t *testing.T) {
	leftBuckets := []RollupBucket{
		{P95Ms: 10, P50Ms: 5, ErrorRate: 0.01, Spans: 100},
		{P95Ms: 10, P50Ms: 5, ErrorRate: 0.01, Spans: 100},
		{P95Ms: 10, P50Ms: 5, ErrorRate: 0.01, Spans: 100},
		{P95Ms: 10, P50Ms: 5, ErrorRate: 0.01, Spans: 100},
		{P95Ms: 10, P50Ms: 5, ErrorRate: 0.01, Spans: 100},
	}
	rightBuckets := []RollupBucket{
		{P95Ms: 100, P50Ms: 50, ErrorRate: 0.05, Spans: 100},
		{P95Ms: 100, P50Ms: 50, ErrorRate: 0.05, Spans: 100},
		{P95Ms: 100, P50Ms: 50, ErrorRate: 0.05, Spans: 100},
		{P95Ms: 100, P50Ms: 50, ErrorRate: 0.05, Spans: 100},
		{P95Ms: 100, P50Ms: 50, ErrorRate: 0.05, Spans: 100},
	}
	left := AggregateBuckets(leftBuckets)
	right := AggregateBuckets(rightBuckets)

	comparison := BuildComparison(left, right, leftBuckets, rightBuckets, []string{"latency"})
	latency := comparison["latency"]
	if !latency.StatisticallySignificant {
		t.Error("latency should be statistically significant with clearly different bucket data")
	}
}

func TestCompareServices_QueriesRawSpans(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rows := sqlmock.NewRows([]string{"service", "requests", "error_rate", "p50_ms", "p95_ms", "error_count"}).
		AddRow("checkout", int64(120), 0.025, 40.0, 120.0, int64(3))

	mock.ExpectQuery(`FROM spans`).
		WillReturnRows(rows)

	result, err := svc.CompareServices(context.Background(), CompareServicesParams{
		Services: []string{"checkout", "payments"},
		Window:   60,
	})
	if err != nil {
		t.Fatalf("CompareServices() error = %v", err)
	}

	if len(result.Services) != 2 {
		t.Fatalf("CompareServices() services = %d, want 2", len(result.Services))
	}
	if result.Services[0].Service != "checkout" {
		t.Fatalf("first service = %q, want checkout", result.Services[0].Service)
	}
	if result.Services[0].P95Ms != 120 {
		t.Errorf("checkout P95Ms = %f, want 120", result.Services[0].P95Ms)
	}
	if result.Services[0].ErrorCount != 3 {
		t.Errorf("checkout ErrorCount = %d, want 3", result.Services[0].ErrorCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestQueryRollupBuckets_QueriesRawSpans(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	start := time.Unix(0, 0).UTC()
	end := start.Add(15 * time.Minute)

	rows := sqlmock.NewRows([]string{"p95_ms", "p50_ms", "error_rate", "total_spans"}).
		AddRow(100.0, 50.0, 0.01, int64(100)).
		AddRow(120.0, 60.0, 0.02, int64(80))

	mock.ExpectQuery(`FROM spans`).
		WithArgs("checkout", start, end).
		WillReturnRows(rows)

	buckets, err := svc.QueryRollupBuckets(context.Background(), "checkout", start, end)
	if err != nil {
		t.Fatalf("QueryRollupBuckets() error = %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("QueryRollupBuckets() len = %d, want 2", len(buckets))
	}
	if buckets[0].P95Ms != 100 || buckets[1].P95Ms != 120 {
		t.Fatalf("unexpected bucket p95s: %+v", buckets)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestMakeMetricDiff(t *testing.T) {
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
		{"zero left value", 0.0, 50.0, true, "stable", 0.0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			diff := MakeMetricDiff(tc.leftVal, tc.rightVal, nil, nil, tc.higherIsBad)
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
			got := IsSignificant(tc.left, tc.right)
			if got != tc.wantSig {
				t.Errorf("IsSignificant() = %v, want %v", got, tc.wantSig)
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
			mean, stddev := MeanStdDev(tc.vals)
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
	got := ResolveFocus(nil)
	if len(got) != 3 {
		t.Errorf("default focus len = %d, want 3", len(got))
	}

	// Custom focus
	custom := []string{"latency", "errors"}
	got = ResolveFocus(custom)
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
			verdict := BuildVerdict(tc.comparison)
			if tc.wantNoSig && verdict != "No significant differences detected." {
				t.Errorf("verdict = %q, want no-differences message", verdict)
			}
			if tc.wantContains != "" && !strings.Contains(verdict, tc.wantContains) {
				t.Errorf("verdict = %q, should contain %q", verdict, tc.wantContains)
			}
		})
	}
}

func TestServiceMetrics(t *testing.T) {
	m := ServiceMetrics{
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
}

func TestRollupBucket(t *testing.T) {
	b := RollupBucket{
		P95Ms:     100.5,
		P50Ms:     50.2,
		ErrorRate: 0.01,
		Spans:     1000,
	}
	if b.P95Ms != 100.5 {
		t.Errorf("P95Ms = %f, want 100.5", b.P95Ms)
	}
	if b.Spans != 1000 {
		t.Errorf("Spans = %d, want 1000", b.Spans)
	}
}
