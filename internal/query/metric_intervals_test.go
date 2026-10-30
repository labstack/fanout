package query

import (
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestMetricGapDeltasAttributeWholeIncreaseAndBoundExpansion(t *testing.T) {
	for _, kind := range []string{"counter", "histogram"} {
		t.Run(kind, func(t *testing.T) {
			e, db := metricFixture(t)
			first := fixtureStart.Add(time.Second)
			later := first.Add(24 * time.Hour)
			if kind == "counter" {
				insertMetrics(t, db, metricCounter(first, 100))
				insertMetrics(t, db, metricCounter(later, 86500))
			} else {
				insertMetrics(t, db, metricHistogram(first, "[10,20]", "[10,10,0]", 20, 300, "a"))
				insertMetrics(t, db, metricHistogram(later, "[10,20]", "[43210,43210,0]", 86420, 1296300, "a"))
			}
			scope := Scope{Start: fixtureStart, End: later.Add(6 * time.Minute), Interval: 10 * time.Second}
			cte, args, err := metricSeriesCTE(nil, scope, []string{kind})
			if err != nil {
				t.Fatal(err)
			}
			spread, spreadArgs := metricSpreadCTE(scope)
			cte += spread
			args = append(args, spreadArgs...)
			// Count the expansion CTE itself, before clipping or aggregation
			// could conceal amplification. This is an engine assertion, not a
			// time or output-size proxy.
			var deltas, maxRows int64
			if err := db.QueryRowContext(t.Context(), cte+` SELECT count(*),max(n) FROM (
                SELECT series_id,time,count(*) AS n FROM metric_interval_buckets GROUP BY series_id,time)`, args...).Scan(&deltas, &maxRows); err != nil {
				t.Fatal(err)
			}
			limit := int64(math.Ceil(600/scope.Interval.Seconds())) + 1
			if deltas != 1 || maxRows != 1 || maxRows > limit {
				t.Fatalf("expanded deltas=%d max_rows=%d bound=%d", deltas, maxRows, limit)
			}
			var bucket int64
			var covered, increase, sum float64
			var weights string
			if err := db.QueryRowContext(t.Context(), cte+` SELECT _bucket,elapsed_s,
                coalesce(increase,count_increase),coalesce(sum_increase,0),
                coalesce(to_json(bound_increases)::VARCHAR,'null') FROM metric_spread`, args...).Scan(&bucket, &covered, &increase, &sum, &weights); err != nil {
				t.Fatal(err)
			}
			wantBucket := alignedBucketMillis(later, scope.Interval)
			if bucket != wantBucket || covered != 86400 || increase != 86400 {
				t.Fatalf("gap bucket=%d covered=%v increase=%v", bucket, covered, increase)
			}
			if kind == "histogram" && (sum != 1296000 || weights != "[43200.0,43200.0,0.0]") {
				t.Fatalf("histogram gap sum=%v weights=%s", sum, weights)
			}
			measures := []string{"sum(value)"}
			wantSum := 86400.0
			if kind == "histogram" {
				measures = append(measures, "avg(value)", "p95(value)")
				wantSum = 1296000
			}
			r := runMetricMeasure(t, e, measures, "10s", nil, scope.Start, scope.End)
			// The original baseline point keeps its existing NULL panel cell.
			if !reflect.DeepEqual(metricValues(t, r, "time"), []any{fixtureStart.UnixMilli(), wantBucket}) ||
				!reflect.DeepEqual(metricValues(t, r, "sum"), []any{nil, wantSum}) {
				t.Fatalf("gap increase escaped its point bucket: %+v", r.Frame)
			}
			if kind == "histogram" && (metricValues(t, r, "avg")[1] != 15.0 || metricValues(t, r, "p95")[1] != 19.0) {
				t.Fatalf("gap distribution=%+v", r.Frame)
			}
			// Execute the fixed 10-second scope directly: full-window panel
			// execution may automatically widen buckets to respect its limit.
			var rows, carried, firstBucket, lastBucket int64
			var minRate, maxRate, totalIncrease float64
			if err := db.QueryRowContext(t.Context(), cte+metricRateCarryCTE(scope)+` SELECT
                count(*),count(*) FILTER (WHERE _carry),min(_bucket),max(_bucket),
                min(_rate_share/_rate_covered),max(_rate_share/_rate_covered),
                sum(coalesce(increase,count_increase)) FROM metric_rate_rows`, args...).Scan(
				&rows, &carried, &firstBucket, &lastBucket, &minRate, &maxRate, &totalIncrease); err != nil {
				t.Fatal(err)
			}
			if rows != 31 || carried != 30 || firstBucket != wantBucket || lastBucket != wantBucket+300000 ||
				minRate != 1 || maxRate != 1 || totalIncrease != 86400 {
				t.Fatalf("gap carry rows=%d carried=%d buckets=%d..%d rate=%v..%v increase=%v",
					rows, carried, firstBucket, lastBucket, minRate, maxRate, totalIncrease)
			}
		})
	}
}

func TestMetricExactlyLookbackDeltasStillSpread(t *testing.T) {
	for _, kind := range []string{"counter", "histogram"} {
		t.Run(kind, func(t *testing.T) {
			e, db := metricFixture(t)
			first := fixtureStart.Add(time.Second)
			later := first.Add(10 * time.Minute)
			if kind == "counter" {
				insertMetrics(t, db, metricCounter(first, 100))
				insertMetrics(t, db, metricCounter(later, 700))
			} else {
				insertMetrics(t, db, metricHistogram(first, "[10,20]", "[10,10,0]", 20, 300, "a"))
				insertMetrics(t, db, metricHistogram(later, "[10,20]", "[310,310,0]", 620, 9300, "a"))
			}
			scope := Scope{Start: fixtureStart, End: later.Add(time.Second), Interval: 10 * time.Second}
			cte, args, err := metricSeriesCTE(nil, scope, []string{kind})
			if err != nil {
				t.Fatal(err)
			}
			spread, spreadArgs := metricSpreadCTE(scope)
			cte += spread
			args = append(args, spreadArgs...)
			var expanded int64
			var covered, increase float64
			if err := db.QueryRowContext(t.Context(), cte+` SELECT
                (SELECT count(*) FROM metric_interval_buckets),sum(elapsed_s),
                sum(coalesce(increase,count_increase)) FROM metric_spread`, args...).Scan(&expanded, &covered, &increase); err != nil {
				t.Fatal(err)
			}
			if expanded != 61 || covered != 600 || increase != 600 {
				t.Fatalf("lookback boundary expanded=%d covered=%v increase=%v", expanded, covered, increase)
			}
			r := runMetricMeasure(t, e, []string{"rate()", "sum(value)"}, "10s", nil, scope.Start, scope.End)
			if r.Frame.Rows != 61 {
				t.Fatalf("lookback boundary buckets=%d want 61", r.Frame.Rows)
			}
			for i, rate := range metricValues(t, r, "rate") {
				want := 10.0
				if i == 0 {
					want = 9
				} else if i == 60 {
					want = 1
				}
				if kind == "histogram" {
					want *= 15
				}
				if rate != 1.0 || metricValues(t, r, "sum")[i] != want {
					t.Fatalf("lookback boundary bucket %d: %+v", i, r.Frame)
				}
			}
		})
	}
}

func TestMetricGapDeltaAtWindowStartRetainsFullElapsed(t *testing.T) {
	for _, kind := range []string{"counter", "histogram"} {
		t.Run(kind, func(t *testing.T) {
			e, db := metricFixture(t)
			later := fixtureStart.Add(7 * time.Second)
			m := metricCounter(later, 86400)
			wantSum := 86400.0
			if kind == "histogram" {
				m = metricHistogram(later, "[10,20]", "[43200,43200,0]", 86400, 1296000, "a")
				wantSum = 1296000
			}
			m.Temporality, m.StartTimeUnixNanos = "delta", later.Add(-24*time.Hour).UnixNano()
			insertMetrics(t, db, m)
			for _, bucket := range []string{"", "10s"} {
				r := runMetricMeasure(t, e, []string{"rate()", "sum(value)"}, bucket, nil, later, later.Add(time.Second))
				if r.Frame.Rows != 1 || metricValues(t, r, "rate")[0] != 1.0 || metricValues(t, r, "sum")[0] != wantSum {
					t.Fatalf("gap at clipped window start bucket=%q: %+v", bucket, r.Frame)
				}
			}
		})
	}
}

func TestMetricCounterTotalsHoldAcrossStaggeredExportIntervals(t *testing.T) {
	e, db := metricFixture(t)
	for series := range 21 {
		period, phase := 60, 3*series
		if series == 20 {
			period, phase = 300, 0
		}
		for seconds := -600 + phase; seconds < 900; seconds += period {
			m := metricCounter(fixtureStart.Add(time.Duration(seconds)*time.Second), float64(seconds+1000))
			m.Attributes = map[string]any{"series": series}
			insertMetrics(t, db, m)
		}
	}
	for _, tc := range []struct {
		bucket string
		width  int
	}{{"10s", 10}, {"1m", 60}, {"5m", 300}} {
		r := runMetricMeasure(t, e, []string{"rate()", "sum(value)"}, tc.bucket, nil, fixtureStart, fixtureStart.Add(15*time.Minute))
		rates, sums := metricValues(t, r, "rate"), metricValues(t, r, "sum")
		if len(rates) != 900/tc.width {
			t.Fatalf("%s: %d buckets", tc.bucket, len(rates))
		}
		for i, rate := range rates {
			if rate != 21.0 {
				t.Fatalf("%s bucket %d rate=%v want 21", tc.bucket, i, rate)
			}
			// Every series has completed coverage through minute ten. Newest
			// rates carry, while increases require an observed interval.
			if (i+1)*tc.width <= 600 && sums[i] != float64(21*tc.width) {
				t.Fatalf("%s bucket %d sum=%v want %d", tc.bucket, i, sums[i], 21*tc.width)
			}
		}
	}
	r := runMetricPanel(t, e, Panel{Viz: "stat", Query: &Spec{From: "metrics", Where: []string{"name='net'"}, Measures: []string{"rate()"}}}, fixtureStart, fixtureStart.Add(15*time.Minute))
	if got := r.Frame.Totals[len(r.Frame.Totals)-1]; got != 21.0 {
		t.Fatalf("stat=%v want 21", got)
	}
}

func TestMetricGaugeAndUpdownTotalsCarryAndExpire(t *testing.T) {
	for _, kind := range []string{"gauge", "updown"} {
		t.Run(kind, func(t *testing.T) {
			e, db := metricFixture(t)
			for series := range 21 {
				period, phase := 60, 3*series
				if series == 20 {
					period, phase = 300, 0
				}
				for seconds := -300 + phase; seconds < 900; seconds += period {
					m := metricCounter(fixtureStart.Add(time.Duration(seconds)*time.Second), 10)
					m.Monotonic = false
					if kind == "gauge" {
						m.Type, m.Temporality = "gauge", ""
					}
					m.Attributes = map[string]any{"series": series}
					insertMetrics(t, db, m)
				}
			}
			for _, bucket := range []string{"10s", "1m", "5m"} {
				r := runMetricMeasure(t, e, []string{"sum(value)", "avg(value)"}, bucket, nil, fixtureStart, fixtureStart.Add(15*time.Minute))
				width, ok := ParseSpan(bucket)
				if !ok || len(metricValues(t, r, "sum")) != int(15*time.Minute/width) {
					t.Fatalf("%s: missing buckets", bucket)
				}
				for i, sum := range metricValues(t, r, "sum") {
					if sum != 210.0 || metricValues(t, r, "avg")[i] != 10.0 {
						t.Fatalf("%s bucket %d: sum=%v avg=%v", bucket, i, sum, metricValues(t, r, "avg")[i])
					}
				}
			}
			r := runMetricMeasure(t, e, []string{"sum(value)", "avg(value)", "count()", "min(value)", "max(value)", "p95(value)"}, "1m", nil, fixtureStart.Add(15*time.Minute), fixtureStart.Add(21*time.Minute))
			sums := metricValues(t, r, "sum")
			if len(sums) != 5 || sums[0] != 210.0 || sums[1] != 200.0 {
				t.Fatalf("staleness=%v", sums)
			}
			for i, count := range metricValues(t, r, "count") {
				if count != 0.0 || metricValues(t, r, "min")[i] != nil || metricValues(t, r, "max")[i] != nil || metricValues(t, r, "p95")[i] != nil {
					t.Fatalf("carried value became a point: %+v", r.Frame)
				}
			}
		})
	}
}

func TestMetricHistogramTotalsSpreadOneDistributionAcrossSeries(t *testing.T) {
	e, db := metricFixture(t)
	for series := range 20 {
		for seconds := -600 + 3*series; seconds < 900; seconds += 60 {
			n := (seconds-3*series+600)/60 + 1
			m := metricHistogram(fixtureStart.Add(time.Duration(seconds)*time.Second), "[10,20]", fmt.Sprintf("[%d,%d,0]", 30*n, 30*n), int64(60*n), float64(900*n), fmt.Sprint(series))
			insertMetrics(t, db, m)
		}
	}
	for _, bucket := range []string{"10s", "1m", "5m"} {
		r := runMetricMeasure(t, e, []string{"rate()", "sum(value)", "avg(value)", "p95(value)"}, bucket, nil, fixtureStart, fixtureStart.Add(15*time.Minute))
		width, ok := ParseSpan(bucket)
		if !ok || len(metricValues(t, r, "rate")) != int(15*time.Minute/width) {
			t.Fatalf("%s: missing buckets", bucket)
		}
		for i, rate := range metricValues(t, r, "rate") {
			if rate != 20.0 {
				t.Fatalf("%s bucket %d rate=%v", bucket, i, rate)
			}
			q := metricValues(t, r, "p95")[i]
			if q != nil && q != 19.0 {
				t.Fatalf("%s bucket %d p95=%v", bucket, i, q)
			}
			if q != nil && metricValues(t, r, "avg")[i] != 15.0 {
				t.Fatalf("%s bucket %d avg=%v", bucket, i, metricValues(t, r, "avg")[i])
			}
			if time.Duration(i+1)*width <= 10*time.Minute && metricValues(t, r, "sum")[i] != 300*width.Seconds() {
				t.Fatalf("%s bucket %d sum=%v", bucket, i, metricValues(t, r, "sum")[i])
			}
		}
	}
	r := runMetricMeasure(t, e, []string{"rate()", "sum(value)", "avg(value)", "p95(value)"}, "", nil, fixtureStart.Add(15*time.Minute), fixtureStart.Add(16*time.Minute))
	if metricValues(t, r, "rate")[0] != 20.0 || metricValues(t, r, "sum")[0] != nil || metricValues(t, r, "avg")[0] != nil || metricValues(t, r, "p95")[0] != nil {
		t.Fatalf("histogram carry leaked into observations: %+v", r.Frame)
	}
}

func TestMetricDeltaIntervalsUseStartsOrPreviousPoints(t *testing.T) {
	for _, withStart := range []bool{false, true} {
		t.Run(fmt.Sprint(withStart), func(t *testing.T) {
			e, db := metricFixture(t)
			for seconds := 0; seconds <= 120; seconds += 60 {
				m := metricCounter(fixtureStart.Add(time.Duration(seconds)*time.Second), 60)
				m.Temporality = "delta"
				if withStart {
					m.StartTimeUnixNanos = fixtureStart.Add(time.Duration(seconds-60) * time.Second).UnixNano()
				}
				insertMetrics(t, db, m)
			}
			r := runMetricMeasure(t, e, []string{"rate()", "sum(value)"}, "10s", nil, fixtureStart, fixtureStart.Add(2*time.Minute+time.Nanosecond))
			for i := range 12 {
				want := 10.0
				if !withStart && i == 0 {
					want += 60 // First interval-less delta retains point attribution.
				}
				if metricValues(t, r, "rate")[i] != 1.0 || metricValues(t, r, "sum")[i] != want {
					t.Fatalf("bucket %d: %+v", i, r.Frame)
				}
			}
		})
	}
}

func TestMetricRateCarryExpiresAndNeverAddsIncreases(t *testing.T) {
	e, db := metricFixture(t)
	insertMetrics(t, db, metricCounter(fixtureStart.Add(-2*time.Minute), 0))
	insertMetrics(t, db, metricCounter(fixtureStart.Add(-time.Minute), 60))
	r := runMetricMeasure(t, e, []string{"rate()", "sum(value)"}, "1m", nil, fixtureStart, fixtureStart.Add(6*time.Minute))
	if len(metricValues(t, r, "rate")) != 5 {
		t.Fatalf("expired rate still contributes: %+v", r.Frame)
	}
	for i, rate := range metricValues(t, r, "rate") {
		want := any(1.0)
		if i == 5 {
			want = nil
		}
		if rate != want || metricValues(t, r, "sum")[i] != nil {
			t.Fatalf("bucket %d carry=%v sum=%v", i, rate, metricValues(t, r, "sum")[i])
		}
	}
}

func TestMetricSpreadClipsSignedNanosecondWindows(t *testing.T) {
	e, db := metricFixture(t)
	start := time.Unix(0, -17).UTC()
	insertMetrics(t, db, metricCounter(start.Add(-time.Nanosecond), 0))
	insertMetrics(t, db, metricCounter(start.Add(time.Nanosecond), 6))
	for _, bucket := range []string{"", "10s"} {
		r := runMetricMeasure(t, e, []string{"rate()", "sum(value)"}, bucket, nil, start, start.Add(2*time.Nanosecond))
		if metricValues(t, r, "sum")[0] != 3.0 || math.Abs(metricValues(t, r, "rate")[0].(float64)-3e9) > 1e-6 {
			t.Fatalf("clipped nanos=%+v", r.Frame)
		}
	}
}

func TestMetricGaugeCarryReadsEarlierParquetBatchWithoutAddingPoints(t *testing.T) {
	engine, repo := newTestEngine(t)
	m := metricCounter(fixtureStart.Add(-time.Minute), 10)
	m.Type, m.Temporality, m.Monotonic = "gauge", "", false
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "gauge-lookback", Metrics: []telemetry.Metric{m}}); err != nil {
		t.Fatal(err)
	}
	r := runMetricMeasure(t, NewExecutor(engine, 30), []string{"sum(value)", "avg(value)", "min(value)", "count()"}, "10s", nil, fixtureStart, fixtureStart.Add(time.Minute))
	if r.Frame.Rows != 6 {
		t.Fatalf("carry buckets=%+v", r.Frame)
	}
	for i, sum := range metricValues(t, r, "sum") {
		if sum != 10.0 || metricValues(t, r, "avg")[i] != 10.0 || metricValues(t, r, "min")[i] != nil || metricValues(t, r, "count")[i] != 0.0 {
			t.Fatalf("carry or point scope=%+v", r.Frame)
		}
	}
}

func TestMetricRateCarryFillsInternalDeltaGapsWithLatestRate(t *testing.T) {
	e, db := metricFixture(t)
	for i, seconds := range []int{-60, 0, 180, 360} {
		at := fixtureStart.Add(time.Duration(seconds) * time.Second)
		m := metricCounter(at, float64(60*(i+1)))
		m.Temporality = "delta"
		m.StartTimeUnixNanos = at.Add(-time.Minute).UnixNano()
		insertMetrics(t, db, m)
	}
	r := runMetricMeasure(t, e, []string{"rate()", "sum(value)"}, "1m", nil, fixtureStart, fixtureStart.Add(8*time.Minute))
	for i, want := range []float64{2, 2, 3, 3, 3, 4, 4, 4} {
		var sum any
		if i == 2 {
			sum = 180.0
		} else if i == 5 {
			sum = 240.0
		}
		if metricValues(t, r, "rate")[i] != want || metricValues(t, r, "sum")[i] != sum {
			t.Fatalf("bucket %d: %+v", i, r.Frame)
		}
	}
}

func TestMetricGaugeCarryUsesLastPointAcrossInternalGaps(t *testing.T) {
	e, db := metricFixture(t)
	for i, seconds := range []int{0, 20, 120} {
		m := metricCounter(fixtureStart.Add(time.Duration(seconds)*time.Second), float64(10+20*i))
		m.Type, m.Temporality, m.Monotonic = "gauge", "", false
		insertMetrics(t, db, m)
	}
	r := runMetricMeasure(t, e, []string{"sum(value)", "avg(value)", "count()"}, "1m", nil, fixtureStart, fixtureStart.Add(9*time.Minute))
	if r.Frame.Rows != 8 {
		t.Fatalf("expired carry created a bucket: %+v", r.Frame)
	}
	for i, want := range []float64{20, 30, 50, 50, 50, 50, 50, 50} {
		count := 0.0
		if i == 0 {
			count = 2
		} else if i == 2 {
			count = 1
		}
		if metricValues(t, r, "sum")[i] != want || metricValues(t, r, "avg")[i] != want || metricValues(t, r, "count")[i] != count {
			t.Fatalf("bucket %d: %+v", i, r.Frame)
		}
	}
}
