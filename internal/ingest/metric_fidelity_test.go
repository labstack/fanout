package ingest

import (
	"fmt"
	"testing"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/telemetry"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMetricIngestKeepsKindTemporalityAndStart(t *testing.T) {
	at, start := uint64(1790856000000000017), uint64(1790855940000000009)
	point := func() *metricspb.NumberDataPoint {
		return &metricspb.NumberDataPoint{TimeUnixNano: at, StartTimeUnixNano: start,
			Value: &metricspb.NumberDataPoint_AsInt{AsInt: 12}}
	}
	metrics := []*metricspb.Metric{
		{Name: "g", Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{point()}}}},
		{Name: "c", Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{IsMonotonic: true, AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, DataPoints: []*metricspb.NumberDataPoint{point()}}}},
		{Name: "d", Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{IsMonotonic: true, AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA, DataPoints: []*metricspb.NumberDataPoint{point()}}}},
		{Name: "u", Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, DataPoints: []*metricspb.NumberDataPoint{point()}}}},
		{Name: "h", Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA, DataPoints: []*metricspb.HistogramDataPoint{{TimeUnixNano: at, StartTimeUnixNano: start, Count: 2, ExplicitBounds: []float64{1}, BucketCounts: []uint64{1, 1}}}}}},
		{Name: "e", Data: &metricspb.Metric_ExponentialHistogram{ExponentialHistogram: &metricspb.ExponentialHistogram{AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, DataPoints: []*metricspb.ExponentialHistogramDataPoint{{TimeUnixNano: at, StartTimeUnixNano: start, Count: 1, Positive: &metricspb.ExponentialHistogramDataPoint_Buckets{BucketCounts: []uint64{1}}}}}}},
		{Name: "s", Data: &metricspb.Metric_Summary{Summary: &metricspb.Summary{DataPoints: []*metricspb.SummaryDataPoint{{TimeUnixNano: at, StartTimeUnixNano: start, Count: 1}}}}},
	}
	ch := make(chan telemetry.Metric, len(metrics))
	srv := NewServer(config.Config{DefaultNamespace: "test"}, newTestSubmitter(nil, nil, ch))
	_, err := srv.exportMetrics(t.Context(), &collectormetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: metrics}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{"gauge", "sum", "sum", "sum", "histogram", "exponential_histogram", "summary"}
	temps := []string{"", "cumulative", "delta", "cumulative", "delta", "cumulative", ""}
	for i := range metrics {
		got := <-ch
		if got.Type != kinds[i] || got.Temporality != temps[i] || got.Monotonic != (i == 1 || i == 2) || got.StartTimeUnixNanos != int64(start) {
			t.Fatalf("point %d: %+v", i, got)
		}
	}
}

func TestMetricIngestRejectsInvalidAggregateTemporalityBeforeSubmit(t *testing.T) {
	for _, temporality := range []metricspb.AggregationTemporality{metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_UNSPECIFIED, 99} {
		for _, metric := range []*metricspb.Metric{
			{Name: "sum", Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{AggregationTemporality: temporality, DataPoints: []*metricspb.NumberDataPoint{{}}}}},
			{Name: "histogram", Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{AggregationTemporality: temporality, DataPoints: []*metricspb.HistogramDataPoint{{}}}}},
			{Name: "exponential_histogram", Data: &metricspb.Metric_ExponentialHistogram{ExponentialHistogram: &metricspb.ExponentialHistogram{AggregationTemporality: temporality, DataPoints: []*metricspb.ExponentialHistogramDataPoint{{}}}}},
		} {
			t.Run(fmt.Sprintf("%s/%d", metric.Name, temporality), func(t *testing.T) {
				ch := make(chan telemetry.Metric, 2)
				srv := NewServer(config.Config{DefaultNamespace: "test"}, newTestSubmitter(nil, nil, ch))
				_, err := srv.exportMetrics(t.Context(), &collectormetrics.ExportMetricsServiceRequest{
					ResourceMetrics: []*metricspb.ResourceMetrics{{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{
						{Name: "valid", Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{{}}}}}, metric,
					}}}}},
				})
				if status.Code(err) != codes.InvalidArgument || len(ch) != 0 {
					t.Fatalf("error=%v submitted=%d", err, len(ch))
				}
			})
		}
	}
}
