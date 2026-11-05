package ingest

import (
	"reflect"
	"testing"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/telemetry"
)

// Exponential histograms are stored as sent: scale, zero bucket, offsets and
// dense bucket counts, with optional min and max. Queries derive bounds.
func TestExponentialHistogramKeepsNativeLayout(t *testing.T) {
	sum, low, high := 9.5, 0.25, 7.0
	ch := make(chan telemetry.Metric, 1)
	srv := NewServer(config.Config{DefaultNamespace: "test"}, newTestSubmitter(nil, nil, ch))
	_, err := srv.exportMetrics(t.Context(), &collectormetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{
			{Name: "sizes", Data: &metricspb.Metric_ExponentialHistogram{ExponentialHistogram: &metricspb.ExponentialHistogram{
				AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
				DataPoints: []*metricspb.ExponentialHistogramDataPoint{{
					Count: 12, Sum: &sum, Min: &low, Max: &high, Scale: -2, ZeroCount: 1, ZeroThreshold: 0.5, Flags: 1,
					Positive: &metricspb.ExponentialHistogramDataPoint_Buckets{Offset: 3, BucketCounts: []uint64{2, 3}},
					Negative: &metricspb.ExponentialHistogramDataPoint_Buckets{Offset: -1, BucketCounts: []uint64{6}},
				}},
			}}},
		}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := <-ch
	want := telemetry.Histogram{Count: 12, Sum: 9.5, HasSum: true, Min: 0.25, HasMin: true, Max: 7, HasMax: true,
		Exponential: true, Scale: -2, ZeroCount: 1, ZeroThreshold: 0.5,
		PositiveOffset: 3, PositiveCounts: []uint64{2, 3}, NegativeOffset: -1, NegativeCounts: []uint64{6}}
	if got.Type != "histogram" || got.Temporality != "delta" || got.Flags != 1 || !reflect.DeepEqual(got.Histogram, want) {
		t.Fatalf("point = %+v", got)
	}
}
