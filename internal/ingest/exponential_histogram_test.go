package ingest

import (
	"math"
	"reflect"
	"testing"
	"time"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

func TestExponentialHistogramRetainsEveryObservation(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		scale, positiveOffset, negativeOffset int32
		positive, negative                    []uint64
		zero                                  uint64
		bounds                                []float64
		counts                                []uint64
	}{
		{"scale_zero_negative_offset", 0, -1, 0, []uint64{2, 3}, []uint64{4, 1}, 6, []float64{-4, -2, -1, -math.SmallestNonzeroFloat64, 0, .5, 1, 2}, []uint64{0, 1, 4, 0, 6, 0, 2, 3, 0}},
		{"scale_three_positive_offset", 3, 2, -1, []uint64{2, 3}, []uint64{4}, 1, []float64{-1, -math.Pow(math.Pow(2, math.Pow(2, -3)), -1), -math.SmallestNonzeroFloat64, 0, math.Pow(math.Pow(2, math.Pow(2, -3)), 2), math.Pow(math.Pow(2, math.Pow(2, -3)), 3), math.Pow(math.Pow(2, math.Pow(2, -3)), 4)}, []uint64{0, 4, 0, 1, 0, 2, 3, 0}},
		{"scale_negative_two", -2, 1, 0, []uint64{7, 9}, nil, 0, []float64{-math.SmallestNonzeroFloat64, 0, 16, 256, 4096}, []uint64{0, 0, 0, 7, 9, 0}},
		{"zero_only", 0, 0, 0, nil, nil, 9, []float64{-math.SmallestNonzeroFloat64, 0}, []uint64{0, 9, 0}},
		{"negative_only", 0, 0, 1, nil, []uint64{2, 6}, 0, []float64{-8, -4, -2, -math.SmallestNonzeroFloat64, 0}, []uint64{0, 6, 2, 0, 0, 0}},
		{"negative_and_zero", 0, 0, 1, nil, []uint64{2, 6}, 3, []float64{-8, -4, -2, -math.SmallestNonzeroFloat64, 0}, []uint64{0, 6, 2, 0, 3, 0}},
		{"positive_only", 0, 0, 0, []uint64{2, 3}, nil, 4, []float64{-math.SmallestNonzeroFloat64, 0, 1, 2, 4}, []uint64{0, 4, 0, 2, 3, 0}},
		{"empty", 0, 0, 0, nil, nil, 0, []float64{-math.SmallestNonzeroFloat64, 0}, []uint64{0, 0, 0}},
		// At scale -10 each bucket spans 2^1024: every bound past 1 overflows,
		// so the positive buckets above it fold into the overflow bucket.
		{"overflowing_scale", -10, 0, 0, []uint64{5, 7}, nil, 1, []float64{-math.SmallestNonzeroFloat64, 0, 1}, []uint64{0, 1, 0, 12}},
		{"overflowing_negative_scale", -10, 0, 0, nil, []uint64{3, 4}, 2, []float64{-1, -math.SmallestNonzeroFloat64, 0}, []uint64{7, 0, 2, 0}},
		// A huge negative offset underflows the lowest bounds to zero; the
		// buckets they bounded merge instead of producing unordered bounds.
		{"underflowing_offset", 0, -1100, 0, []uint64{2, 3, 4}, nil, 1, []float64{-math.SmallestNonzeroFloat64, 0}, []uint64{0, 1, 9}},
		// These exact conversion layouts feed the panel builder regression tests.
		{"nonzero_zero_count_baseline", 0, 1, 0, []uint64{10}, nil, 5, []float64{-math.SmallestNonzeroFloat64, 0, 2, 4}, []uint64{0, 5, 0, 10, 0}},
		{"new_minimum_with_nonzero_zero_count", 0, 0, 0, []uint64{1, 10}, nil, 5, []float64{-math.SmallestNonzeroFloat64, 0, 1, 2, 4}, []uint64{0, 5, 0, 1, 10, 0}},
		{"first_negative_with_nonzero_zero_count", 0, 1, 0, []uint64{10}, []uint64{1}, 5, []float64{-2, -1, -math.SmallestNonzeroFloat64, 0, 2, 4}, []uint64{0, 1, 0, 5, 0, 10, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dp := &metricspb.ExponentialHistogramDataPoint{
				Scale:     tc.scale,
				ZeroCount: tc.zero,
				Positive:  &metricspb.ExponentialHistogramDataPoint_Buckets{Offset: tc.positiveOffset, BucketCounts: tc.positive},
				Negative:  &metricspb.ExponentialHistogramDataPoint_Buckets{Offset: tc.negativeOffset, BucketCounts: tc.negative},
			}
			dp.Count = tc.zero
			for _, n := range tc.positive {
				dp.Count += n
			}
			for _, n := range tc.negative {
				dp.Count += n
			}
			bounds, counts := explicitExpHistogram(dp)
			if len(counts) != len(bounds)+1 || !reflect.DeepEqual(counts, tc.counts) {
				t.Fatalf("bounds=%v counts=%v", bounds, counts)
			}
			if len(bounds) != len(tc.bounds) {
				t.Fatalf("bounds=%v", bounds)
			}
			for i, want := range tc.bounds {
				if math.Abs(bounds[i]-want) > math.Abs(want)*1e-12 {
					t.Fatalf("bound %d=%g want %g", i, bounds[i], want)
				}
			}
			for i := 1; i < len(bounds); i++ {
				if bounds[i] <= bounds[i-1] {
					t.Fatalf("bounds are not strictly ascending: %v", bounds)
				}
			}
			var total uint64
			for _, n := range counts {
				total += n
			}
			if total != dp.Count {
				t.Fatalf("total=%d count=%d", total, dp.Count)
			}
		})
	}
}

func TestExponentialHistogramWithManyOverflowingBucketsConvertsInLinearTime(t *testing.T) {
	positive := make([]uint64, 1_000_000)
	for i := range positive {
		positive[i] = 1
	}
	dp := &metricspb.ExponentialHistogramDataPoint{
		Scale:    -10,
		Count:    uint64(len(positive)),
		Positive: &metricspb.ExponentialHistogramDataPoint_Buckets{BucketCounts: positive},
	}
	started := time.Now()
	bounds, counts := explicitExpHistogram(dp)
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("conversion took %s", elapsed)
	}
	if !reflect.DeepEqual(bounds, []float64{-math.SmallestNonzeroFloat64, 0, 1}) || !reflect.DeepEqual(counts, []uint64{0, 0, 0, uint64(len(positive))}) {
		t.Fatalf("bounds=%v counts=%v", bounds, counts)
	}
}

func TestExponentialHistogramUsesNonzeroZeroThresholdAndFoldsOverlaps(t *testing.T) {
	for _, tc := range []struct {
		threshold float64
		bounds    []float64
		counts    []uint64
	}{
		{.5, []float64{-2, -1, -.5, .5, 1, 2}, []uint64{0, 2, 0, 5, 0, 3, 0}},
		{1, []float64{-2, -1, 1, 2}, []uint64{0, 2, 5, 3, 0}},
		{1.5, []float64{-2, -1, 1.5, 2}, []uint64{0, 2, 5, 3, 0}},
	} {
		dp := &metricspb.ExponentialHistogramDataPoint{
			ZeroThreshold: tc.threshold,
			ZeroCount:     5,
			Count:         10,
			Positive:      &metricspb.ExponentialHistogramDataPoint_Buckets{BucketCounts: []uint64{3}},
			Negative:      &metricspb.ExponentialHistogramDataPoint_Buckets{BucketCounts: []uint64{2}},
		}
		bounds, counts := explicitExpHistogram(dp)
		if !reflect.DeepEqual(bounds, tc.bounds) || !reflect.DeepEqual(counts, tc.counts) {
			t.Fatalf("threshold=%g: bounds=%v counts=%v", tc.threshold, bounds, counts)
		}
	}
}
