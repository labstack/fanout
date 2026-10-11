package panel

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestHistogramMeasuresInterpolateSummedIncreases(t *testing.T) {
	e, db := metricFixture(t)
	for _, m := range []telemetry.Metric{
		metricHistogram(fixtureStart.Add(-time.Minute), "[10,20]", "[1,1,0]", 2, 20, "a"),
		metricHistogram(fixtureStart, "[10,20]", "[11,11,0]", 22, 320, "a"),
		metricHistogram(fixtureStart.Add(-time.Minute), "[10,20]", "[0,1,0]", 1, 20, "b"),
		metricHistogram(fixtureStart, "[10,20]", "[0,11,0]", 11, 220, "b"),
	} {
		insertMetrics(t, db, m)
	}
	sumIncrease, countIncrease := 500.0, 30.0
	wantAverage := sumIncrease / countIncrease
	for _, tc := range []struct {
		measure string
		want    any
	}{
		{"rate()", .5}, {"sum(value)", 500.0}, {"avg(value)", wantAverage},
		{"p50(value)", 12.5}, {"p75(value)", 16.25}, {"p95(value)", 19.25},
		{"quantile(value, 0.95)", 19.25}, {"min(value)", nil}, {"max(value)", nil}, {"last(value)", nil},
	} {
		r := runMetricMeasure(t, e, []string{tc.measure}, "", nil, fixtureStart.Add(-time.Minute), fixtureStart.Add(time.Nanosecond))
		got := r.Frame.Values[len(r.Frame.Columns)-1]
		if len(got) != 1 || got[0] != tc.want {
			t.Fatalf("%s=%v want %v", tc.measure, got, tc.want)
		}
	}
}

func TestHistogramQuantileHandlesZeroAndOverflow(t *testing.T) {
	for _, tc := range []struct {
		label, bounds, counts string
		count                 int64
		want                  any
	}{
		{"lowest_positive", "[10,20]", "[10,0,0]", 10, 9.5},
		{"overflow", "[10,20]", "[1,1,18]", 20, 20.0},
		{"overflow_with_empty_finite_buckets", "[10,20]", "[0,0,20]", 20, 20.0},
		{"zero_total", "[10,20]", "[0,0,0]", 0, nil},
		{"no_finite_bound", "[]", "[10]", 10, nil},
		{"nonpositive_first", "[-10,0,10]", "[10,0,0,0]", 10, -10.0},
		{"exponential_shape", "[0.5,1,2]", "[4,2,4,0]", 10, 1.875},
	} {
		t.Run(tc.label, func(t *testing.T) {
			e, db := metricFixture(t)
			m := metricHistogram(fixtureStart, tc.bounds, tc.counts, tc.count, 10, "a")
			m.Temporality = "delta"
			m.StartTimeUnixNanos = fixtureStart.Add(-time.Minute).UnixNano()
			if tc.label == "exponential_shape" {
				m.Type = "exponential_histogram"
			}
			insertMetrics(t, db, m)
			r := runMetricMeasure(t, e, []string{"p95(value)"}, "", nil, fixtureStart.Add(-time.Minute), fixtureStart.Add(time.Nanosecond))
			if got := metricValues(t, r, "p95"); len(got) != 1 || got[0] != tc.want {
				t.Fatalf("p95=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestHistogramNegativeOnlyQuantileKeepsSign(t *testing.T) {
	e, db := metricFixture(t)
	// Ingest's conversion of scale=0, negative offset=1, counts=[2,6] with no
	// zero or positive observations, including the fixed zero bucket.
	m := metricHistogram(fixtureStart, "[-8,-4,-2,-5e-324,0]", "[0,6,2,0,0,0]", 8, -40, "a")
	m.Type, m.Temporality = "exponential_histogram", "delta"
	insertMetrics(t, db, m)
	r := runMetricMeasure(t, e, []string{"p75(value)"}, "", nil, fixtureStart, fixtureStart.Add(time.Minute))
	if got := metricValues(t, r, "p75"); len(got) != 1 || got[0] != -4.0 {
		t.Fatalf("negative quantile=%v want -4", got)
	}
}

func TestHistogramDeltaWithoutElapsedHasNoRate(t *testing.T) {
	e, db := metricFixture(t)
	m := metricHistogram(fixtureStart, "[10]", "[3,2]", 5, 30, "a")
	m.Temporality = "delta"
	insertMetrics(t, db, m)
	r := runMetricMeasure(t, e, []string{"rate()", "sum(value)", "avg(value)", "p95(value)"}, "", nil, fixtureStart, fixtureStart.Add(time.Minute))
	if got := metricValues(t, r, "rate"); len(got) != 1 || got[0] != nil {
		t.Fatalf("delta rate=%v", got)
	}
	if metricValues(t, r, "sum")[0] != 30.0 || metricValues(t, r, "avg")[0] != 6.0 || metricValues(t, r, "p95")[0] != 10.0 {
		t.Fatalf("delta without elapsed=%+v", r.Frame)
	}
}

func TestHistogramRateUsesPerSeriesCoveredElapsedTime(t *testing.T) {
	e, db := metricFixture(t)
	for _, p := range []struct {
		seconds, count int
		series         string
	}{{-60, 0, "a"}, {0, 60, "a"}, {30, 120, "a"}, {-60, 0, "b"}, {0, 120, "b"}, {90, 300, "b"}} {
		insertMetrics(t, db, metricHistogram(fixtureStart.Add(time.Duration(p.seconds)*time.Second), "[10]", fmt.Sprintf("[%d,0]", p.count), int64(p.count), float64(p.count), p.series))
	}
	r := runMetricMeasure(t, e, []string{"rate()", "sum(value)", "avg(value)", "p95(value)"}, "5m", nil, fixtureStart, fixtureStart.Add(2*time.Minute))
	a, b := 2.0, 2.0
	if !reflect.DeepEqual(metricValues(t, r, "rate"), []any{a + b}) || metricValues(t, r, "sum")[0] != 240.0 || metricValues(t, r, "avg")[0] != 1.0 {
		t.Fatalf("per-series measures=%+v", r.Frame)
	}
	for _, bucket := range []string{"10s", "1m", "1h"} {
		e, db := metricFixture(t)
		for i := -1; i < 3; i++ {
			count := 60 * (i + 1)
			insertMetrics(t, db, metricHistogram(fixtureStart.Add(time.Duration(i)*time.Minute), "[10]", fmt.Sprintf("[%d,0]", count), int64(count), float64(count), "a"))
		}
		r := runMetricMeasure(t, e, []string{"rate()"}, bucket, nil, fixtureStart, fixtureStart.Add(3*time.Minute))
		for _, v := range metricValues(t, r, "rate") {
			if v != 1.0 {
				t.Fatalf("%s rate=%v", bucket, v)
			}
		}
	}
}

func TestHistogramQuantileUsesUnionOfExportedBounds(t *testing.T) {
	e, db := metricFixture(t)
	for _, m := range []telemetry.Metric{
		metricHistogram(fixtureStart, "[10,20]", "[10,0,0]", 10, 100, "a"),
		metricHistogram(fixtureStart, "[5,20]", "[10,0,0]", 10, 50, "b"),
	} {
		m.Temporality = "delta"
		insertMetrics(t, db, m)
	}
	r := runMetricMeasure(t, e, []string{"p50(value)", "p75(value)", "p95(value)"}, "", nil, fixtureStart, fixtureStart.Add(time.Minute))
	for i, want := range []float64{5, 7.5, 9.5} {
		if r.Frame.Values[i][0] != want {
			t.Fatalf("union grid=%+v", r.Frame)
		}
	}
}

func TestHistogramOtherRecomputesQuantilesAndWeightedMean(t *testing.T) {
	e, db := metricFixture(t)
	for _, p := range []struct {
		service, bounds, counts string
		count                   int64
		sum                     float64
	}{
		{"a", "[100,200]", "[0,100,0]", 100, 15000},
		{"b", "[10,20]", "[20,0,0]", 20, 100},
		{"c", "[10,20]", "[0,80,0]", 80, 1200},
	} {
		m := metricHistogram(fixtureStart, p.bounds, p.counts, p.count, p.sum, p.service)
		m.ServiceName, m.Temporality = p.service, "delta"
		insertMetrics(t, db, m)
	}
	for _, viz := range []string{"bar", "timeseries"} {
		by, bucket := []string{"name", "service"}, ""
		if viz == "timeseries" {
			by, bucket = []string{"service"}, "1m"
		}
		for _, tc := range []struct {
			measure, alias string
			want           float64
		}{{"p95(value)", "p95", 19.375}, {"avg(value)", "avg", 13}, {"sum(value)", "sum", 1300}} {
			r := runMetricPanel(t, e, Panel{Viz: viz, Options: &Options{Top: 1}, Query: &Query{
				From: "metrics", Where: []string{"name='net'"}, By: by, Bucket: bucket,
				Measures: []string{tc.measure},
			}}, fixtureStart, fixtureStart.Add(time.Minute))
			found := false
			for i, service := range metricValues(t, r, "service") {
				if service == "Other (2)" {
					found = true
					if metricValues(t, r, tc.alias)[i] != tc.want {
						t.Fatalf("%s folded=%+v", viz, r.Frame)
					}
				} else if service != "a" {
					t.Fatalf("%s ranking=%+v", viz, r.Frame)
				}
			}
			if !found {
				t.Fatalf("missing Other: %+v", r.Frame)
			}
		}
	}
}

func TestHistogramMeasuresReadSeparateLookbackBatch(t *testing.T) {
	engine, repo := newTestEngine(t)
	for i := range 2 {
		m := metricHistogram(fixtureStart.Add(time.Duration(i-1)*time.Minute), "[10]", fmt.Sprintf("[%d,0]", 10+10*i), int64(10+10*i), float64(50+50*i), "a")
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprintf("histmeasure%d", i), Metrics: []telemetry.Metric{m}}); err != nil {
			t.Fatal(err)
		}
	}
	e := NewExecutor(engine, 30)
	for _, tc := range []struct {
		measure, alias string
		want           any
	}{{"rate()", "rate", 10.0 / 60.0}, {"sum(value)", "sum", nil}, {"avg(value)", "avg", nil}, {"p95(value)", "p95", nil}} {
		r := runMetricMeasure(t, e, []string{tc.measure}, "1m", nil, fixtureStart, fixtureStart.Add(time.Minute))
		if !reflect.DeepEqual(metricValues(t, r, tc.alias), []any{tc.want}) || r.FromMS != fixtureStart.UnixMilli() || r.ToMS != fixtureStart.Add(time.Minute).UnixMilli() {
			t.Fatalf("%s lookback=%+v", tc.measure, r)
		}
	}
}

func TestHistogramMeasuresResetTogetherAndKeepNegativeSums(t *testing.T) {
	e, db := metricFixture(t)
	for i, p := range []struct {
		bounds, counts string
		count          int64
		sum            float64
	}{
		{"[-20,-10,0,10]", "[0,2,4,2,0]", 8, 10},
		{"[-20,-10,0,10]", "[0,2,6,2,0]", 10, 0}, // negative observations decrease sum only
		{"[-20,-10,0,10]", "[0,1,8,3,0]", 12, 5}, // bucket decrease resets every channel
		{"[-20,0,10]", "[0,10,3,0]", 13, 1},      // compatible coarsening rebins
		{"[-20,0,10]", "[1,1,0,0]", 2, -5},       // count decrease resets
	} {
		insertMetrics(t, db, metricHistogram(fixtureStart.Add(time.Duration(i-1)*time.Minute), p.bounds, p.counts, p.count, p.sum, "a"))
	}
	r := runMetricMeasure(t, e, []string{"sum(value)", "avg(value)", "p50(value)"}, "1m", nil, fixtureStart, fixtureStart.Add(4*time.Minute))
	if !reflect.DeepEqual(metricValues(t, r, "sum"), []any{5.0, -4.0, -5.0, nil}) ||
		!reflect.DeepEqual(metricValues(t, r, "avg"), []any{5.0 / 12.0, -4.0, -2.5, nil}) ||
		!reflect.DeepEqual(metricValues(t, r, "p50"), []any{-3.75, -10.0, -20.0, nil}) {
		t.Fatalf("reset channels=%+v", r.Frame)
	}
}

func TestHistogramUndefinedQuantilesKeepAllGroups(t *testing.T) {
	e, db := metricFixture(t)
	for _, m := range []telemetry.Metric{
		metricHistogram(fixtureStart, "[10]", "[0,0]", 0, 0, "zero"),
		metricHistogram(fixtureStart, "[]", "[2]", 2, 10, "unbounded"),
		metricHistogram(fixtureStart, "[10]", "[2,0]", 2, 10, "defined"),
	} {
		m.Temporality = "delta"
		m.StartTimeUnixNanos = fixtureStart.Add(-time.Minute).UnixNano()
		insertMetrics(t, db, m)
	}
	r := runMetricMeasure(t, e, []string{"rate()", "sum(value)", "avg(value)", "p95(value)"}, "", []string{"attributes['series']"}, fixtureStart.Add(-time.Minute), fixtureStart.Add(time.Nanosecond))
	groups := metricValues(t, r, "series")
	if len(groups) != 3 {
		t.Fatalf("missing groups=%+v", r.Frame)
	}
	for i, group := range groups {
		if group == "zero" && (metricValues(t, r, "rate")[i] != 0.0 || metricValues(t, r, "sum")[i] != 0.0 || metricValues(t, r, "avg")[i] != nil) {
			t.Fatalf("zero measures=%+v", r.Frame)
		}
		want := any(nil)
		if group == "defined" {
			want = 9.5
		}
		if metricValues(t, r, "p95")[i] != want {
			t.Fatalf("%s quantile=%+v", group, r.Frame)
		}
	}
}

func TestHistogramSummaryDiagnosisStillSuggestsCount(t *testing.T) {
	e, db := metricFixture(t)
	m := metricCounter(fixtureStart, 10)
	m.Type, m.Temporality, m.Monotonic = "summary", "", false
	insertMetrics(t, db, m)
	r := runMetricMeasure(t, e, []string{"p95(value) as tail"}, "", nil, fixtureStart, fixtureStart.Add(time.Minute))
	if r.Status != StatusEmpty || r.Diagnosis != "p95(value) is not defined for net (summary); use count()" {
		t.Fatalf("summary diagnosis=%+v", r)
	}
}

func TestHistogramQuantilesOnConvertedExponentialLayout(t *testing.T) {
	e, db := metricFixture(t)
	// Ingest's conversion of scale=0, zero count 2, positive offset -1 and
	// counts [4,2,4]: an empty underflow bucket, the fixed zero bucket, an
	// empty gap and the positive buckets.
	m := metricHistogram(fixtureStart, "[-5e-324,0,0.5,1,2,4]", "[0,2,0,4,2,4,0]", 12, 20, "a")
	m.Type, m.Temporality = "exponential_histogram", "delta"
	m.StartTimeUnixNanos = fixtureStart.Add(-time.Minute).UnixNano()
	insertMetrics(t, db, m)
	r := runMetricMeasure(t, e, []string{"quantile(value, 0.25) as q25", "p50(value)", "p75(value)"}, "", nil, fixtureStart.Add(-time.Minute), fixtureStart.Add(time.Nanosecond))
	for alias, want := range map[string]float64{"q25": 0.625, "p50": 1, "p75": 2.5} {
		if got := metricValues(t, r, alias); len(got) != 1 || got[0] != want {
			t.Fatalf("%s=%v want %v", alias, got, want)
		}
	}
}
