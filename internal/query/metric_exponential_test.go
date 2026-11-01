package query

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

// exponentialBuckets reads the explicit bounds and counts the metric series
// builder derives from one stored exponential histogram point.
func exponentialBuckets(t *testing.T, h telemetry.Histogram) ([]float64, []uint64) {
	t.Helper()
	_, db := metricFixture(t)
	m := metricExponential(fixtureStart, h, 0)
	insertMetrics(t, db, m)
	cte, args, err := metricPointsCTE(metricRead{}, Scope{Start: fixtureStart, End: fixtureStart.Add(time.Minute)}, []string{"histogram"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var bounds, counts string
	if err := db.db().QueryRowContext(t.Context(), cte+" SELECT to_json(_bounds)::VARCHAR,to_json(_counts)::VARCHAR FROM metric_points", args...).Scan(&bounds, &counts); err != nil {
		t.Fatal(err)
	}
	var b []float64
	var c []uint64
	if err := json.Unmarshal([]byte(bounds), &b); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(counts), &c); err != nil {
		t.Fatal(err)
	}
	var total uint64
	for _, n := range c {
		total += n
	}
	if len(c) != len(b)+1 || total != m.Count {
		t.Fatalf("bounds=%v counts=%v total=%d count=%d", b, c, total, m.Count)
	}
	for i := 1; i < len(b); i++ {
		if b[i] <= b[i-1] {
			t.Fatalf("bounds are not strictly ascending: %v", b)
		}
	}
	return b, c
}

func TestExponentialHistogramRetainsEveryObservation(t *testing.T) {
	tiny := math.SmallestNonzeroFloat64
	step := math.Pow(2, math.Pow(2, -3))
	for _, tc := range []struct {
		name                                  string
		scale, positiveOffset, negativeOffset int32
		positive, negative                    []uint64
		zero                                  uint64
		bounds                                []float64
		counts                                []uint64
	}{
		{"scale_zero_negative_offset", 0, -1, 0, []uint64{2, 3}, []uint64{4, 1}, 6, []float64{-4, -2, -1, -tiny, 0, .5, 1, 2}, []uint64{0, 1, 4, 0, 6, 0, 2, 3, 0}},
		{"scale_three_positive_offset", 3, 2, -1, []uint64{2, 3}, []uint64{4}, 1, []float64{-1, -math.Pow(step, -1), -tiny, 0, math.Pow(step, 2), math.Pow(step, 3), math.Pow(step, 4)}, []uint64{0, 4, 0, 1, 0, 2, 3, 0}},
		{"scale_negative_two", -2, 1, 0, []uint64{7, 9}, nil, 0, []float64{-tiny, 0, 16, 256, 4096}, []uint64{0, 0, 0, 7, 9, 0}},
		{"zero_only", 0, 0, 0, nil, nil, 9, []float64{-tiny, 0}, []uint64{0, 9, 0}},
		{"negative_only", 0, 0, 1, nil, []uint64{2, 6}, 0, []float64{-8, -4, -2, -tiny, 0}, []uint64{0, 6, 2, 0, 0, 0}},
		{"negative_and_zero", 0, 0, 1, nil, []uint64{2, 6}, 3, []float64{-8, -4, -2, -tiny, 0}, []uint64{0, 6, 2, 0, 3, 0}},
		{"positive_only", 0, 0, 0, []uint64{2, 3}, nil, 4, []float64{-tiny, 0, 1, 2, 4}, []uint64{0, 4, 0, 2, 3, 0}},
		{"empty", 0, 0, 0, nil, nil, 0, []float64{-tiny, 0}, []uint64{0, 0, 0}},
		// At scale -10 each bucket spans 2^1024: every bound past 1 overflows,
		// so the positive buckets above it fold into the overflow bucket.
		{"overflowing_scale", -10, 0, 0, []uint64{5, 7}, nil, 1, []float64{-tiny, 0, 1}, []uint64{0, 1, 0, 12}},
		{"overflowing_negative_scale", -10, 0, 0, nil, []uint64{3, 4}, 2, []float64{-1, -tiny, 0}, []uint64{7, 0, 2, 0}},
		// A huge negative offset underflows the lowest bounds to zero; the
		// buckets they bounded merge instead of producing unordered bounds.
		{"underflowing_offset", 0, -1100, 0, []uint64{2, 3, 4}, nil, 1, []float64{-tiny, 0}, []uint64{0, 1, 9}},
		{"nonzero_zero_count_baseline", 0, 1, 0, []uint64{10}, nil, 5, []float64{-tiny, 0, 2, 4}, []uint64{0, 5, 0, 10, 0}},
		{"new_minimum_with_nonzero_zero_count", 0, 0, 0, []uint64{1, 10}, nil, 5, []float64{-tiny, 0, 1, 2, 4}, []uint64{0, 5, 0, 1, 10, 0}},
		{"first_negative_with_nonzero_zero_count", 0, 1, 0, []uint64{10}, []uint64{1}, 5, []float64{-2, -1, -tiny, 0, 2, 4}, []uint64{0, 1, 0, 5, 0, 10, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bounds, counts := exponentialBuckets(t, telemetry.Histogram{Scale: tc.scale, ZeroCount: tc.zero,
				PositiveOffset: tc.positiveOffset, PositiveCounts: tc.positive, NegativeOffset: tc.negativeOffset, NegativeCounts: tc.negative})
			if !reflect.DeepEqual(counts, tc.counts) || len(bounds) != len(tc.bounds) {
				t.Fatalf("bounds=%v counts=%v", bounds, counts)
			}
			for i, want := range tc.bounds {
				if math.Abs(bounds[i]-want) > math.Abs(want)*1e-12 {
					t.Fatalf("bound %d=%g want %g", i, bounds[i], want)
				}
			}
		})
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
		bounds, counts := exponentialBuckets(t, telemetry.Histogram{ZeroThreshold: tc.threshold, ZeroCount: 5,
			PositiveCounts: []uint64{3}, NegativeCounts: []uint64{2}})
		if !reflect.DeepEqual(bounds, tc.bounds) || !reflect.DeepEqual(counts, tc.counts) {
			t.Fatalf("threshold=%g: bounds=%v counts=%v", tc.threshold, bounds, counts)
		}
	}
}

// Every list operation of the derivation is linear in buckets, so a point
// with a million overflowing buckets folds them quickly.
func TestExponentialHistogramWithManyOverflowingBucketsConvertsInLinearTime(t *testing.T) {
	positive := make([]uint64, 1_000_000)
	for i := range positive {
		positive[i] = 1
	}
	started := time.Now()
	bounds, counts := exponentialBuckets(t, telemetry.Histogram{Scale: -10, PositiveCounts: positive})
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("conversion took %s", elapsed)
	}
	if !reflect.DeepEqual(bounds, []float64{-math.SmallestNonzeroFloat64, 0, 1}) || !reflect.DeepEqual(counts, []uint64{0, 0, 0, uint64(len(positive))}) {
		t.Fatalf("bounds=%v counts=%v", bounds, counts)
	}
}
