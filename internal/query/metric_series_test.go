package query

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func bindFixtureTimes(t *testing.T, text string, args []any) string {
	t.Helper()
	// Builder tests have no authored filters; their four arguments are times.
	// Render only these trusted fixture instants into a guarded SQL panel.
	for _, arg := range args {
		at, ok := arg.(time.Time)
		if !ok {
			t.Fatalf("fixture argument is %T", arg)
		}
		text = strings.Replace(text, "?", fmt.Sprintf("make_timestamp_ns(%d)", at.UnixNano()), 1)
	}
	if strings.Contains(text, "?") {
		t.Fatal("unbound fixture instant")
	}
	// SQL panels require the window macro as well as the builder's original
	// output bounds. The request window is explicitly extended by the caller.
	return strings.Replace(text, "FROM metrics WHERE ", "FROM metrics WHERE $__window(time) AND ", 1)
}

func metricIncreaseResult(t *testing.T, e *Executor, scope Scope, selectSQL string) Result {
	t.Helper()
	cte, args, err := metricSeriesCTE(nil, scope, []string{"counter", "histogram"})
	if err != nil {
		t.Fatal(err)
	}
	return runMetricPanel(t, e, Panel{Viz: "table", SQL: bindFixtureTimes(t, cte+selectSQL, args)},
		scope.Start.Add(-metricLookback), scope.End)
}

func TestMetricIncreaseOrdersDeduplicatesAndResets(t *testing.T) {
	e, db := metricFixture(t)
	at := fixtureStart
	for _, p := range []struct {
		seconds int
		value   float64
		ingest  int64
		start   int64
	}{
		{60, 160, 60, 1}, {-60, 40, -60, 1}, {0, 100, 0, 1},
		{60, 10, 59, 1},   // earlier retry at the same timestamp must lose
		{120, 5, 120, 1},  // value decrease: reset contributes 5
		{180, 20, 180, 2}, // start change without decrease: reset contributes 20
		{240, 50, 240, 0}, // absent start does not by itself reset
	} {
		m := metricCounter(at.Add(time.Duration(p.seconds)*time.Second), p.value)
		m.IngestedAt = at.Add(time.Duration(p.ingest) * time.Second).UnixNano()
		m.StartTimeUnixNanos = p.start
		insertMetrics(t, db, m)
	}
	// New resource identity is a new container series; its first point is not traffic.
	for i, value := range []float64{9000, 9060} {
		m := metricCounter(at.Add(time.Duration(150+i*60)*time.Second), value)
		m.Resource = map[string]any{"container.id": "new"}
		insertMetrics(t, db, m)
	}
	r := metricIncreaseResult(t, e, Scope{Start: at, End: at.Add(5 * time.Minute), Interval: time.Minute},
		" SELECT epoch_ms(time::TIMESTAMP_NS)::BIGINT AS time,increase,elapsed_s FROM metric_increases ORDER BY time")
	if got := metricValues(t, r, "increase"); !reflect.DeepEqual(got, []any{60.0, 60.0, 5.0, 20.0, 60.0, 30.0}) {
		t.Fatalf("increase=%v", got)
	}
	if got := metricValues(t, r, "elapsed_s"); !reflect.DeepEqual(got, []any{60.0, 60.0, 60.0, 60.0, 60.0, 60.0}) {
		t.Fatalf("elapsed=%v", got)
	}
}

func TestDeltaFirstPointNeedsElapsedOnlyForRate(t *testing.T) {
	e, db := metricFixture(t)
	for i := range 3 {
		m := metricCounter(fixtureStart.Add(time.Duration(i)*time.Minute), 12)
		m.Temporality = "delta"
		if i == 2 {
			m.StartTimeUnixNanos = fixtureStart.Add(90 * time.Second).UnixNano()
		}
		insertMetrics(t, db, m)
	}
	r := metricIncreaseResult(t, e, Scope{Start: fixtureStart, End: fixtureStart.Add(3 * time.Minute)},
		" SELECT epoch_ms(time::TIMESTAMP_NS)::BIGINT AS time,increase,elapsed_s FROM metric_increases ORDER BY time")
	if !reflect.DeepEqual(metricValues(t, r, "increase"), []any{12.0, 12.0, 12.0}) ||
		!reflect.DeepEqual(metricValues(t, r, "elapsed_s"), []any{nil, 60.0, 30.0}) {
		t.Fatalf("delta=%+v", r.Frame)
	}
}

func TestMetricSeriesIdentitySeparatesEveryField(t *testing.T) {
	for _, field := range []string{"namespace", "service", "name", "unit", "scope_name", "scope_version", "attributes", "resource"} {
		t.Run(field, func(t *testing.T) {
			e, db := metricFixture(t)
			insertMetrics(t, db, metricCounter(fixtureStart.Add(-time.Minute), 0))
			for i, value := range []float64{9000, 9060} {
				m := metricCounter(fixtureStart.Add(time.Duration(i)*time.Minute), value)
				switch field {
				case "namespace":
					m.Namespace = "other"
				case "service":
					m.ServiceName = "other"
				case "name":
					m.Name = "other"
				case "unit":
					m.Unit = "other"
				case "scope_name":
					m.ScopeName = "other"
				case "scope_version":
					m.ScopeVersion = "other"
				case "attributes":
					m.Attributes = map[string]any{"literal.dot": int64(1)}
				case "resource":
					m.Resource = map[string]any{"literal.dot": true}
				}
				insertMetrics(t, db, m)
			}
			r := metricIncreaseResult(t, e, Scope{Start: fixtureStart, End: fixtureStart.Add(2 * time.Minute)},
				" SELECT epoch_ms(time::TIMESTAMP_NS)::BIGINT AS time,increase,elapsed_s FROM metric_increases ORDER BY time")
			if !reflect.DeepEqual(metricValues(t, r, "increase"), []any{60.0}) {
				t.Fatalf("identity=%+v", r.Frame)
			}
		})
	}
}

func TestHistogramIncreaseResetsAllChannels(t *testing.T) {
	e, db := metricFixture(t)
	for i, p := range []struct {
		bounds, counts string
		count          int64
		sum            float64
		start          int64
	}{
		{"[10,20]", "[10,20,30]", 60, 100, 1},
		{"[10,20]", "[12,23,35]", 70, 130, 1},
		{"[5,20]", "[20,30,40]", 90, 200, 1}, // bounds change while total increases
		{"[5,20]", "[1,2,3]", 6, 12, 1},      // count decrease
		{"[5,20]", "[2,3,4]", 9, 18, 2},      // start change
	} {
		m := metricCounter(fixtureStart.Add(time.Duration(i)*time.Minute), 0)
		m.Type, m.Monotonic = "histogram", false
		m.HistBoundsJSON, m.HistCountsJSON, m.HistCount, m.HistSum, m.StartTimeUnixNanos = p.bounds, p.counts, p.count, p.sum, p.start
		insertMetrics(t, db, m)
	}
	cte, args, err := metricSeriesCTE(nil, Scope{Start: fixtureStart, End: fixtureStart.Add(5 * time.Minute), Interval: time.Minute}, []string{"histogram"})
	if err != nil {
		t.Fatal(err)
	}
	r := runMetricPanel(t, e, Panel{Viz: "table", SQL: bindFixtureTimes(t, cte+" SELECT count_increase,sum_increase,to_json(bound_increases) AS weights FROM metric_increases ORDER BY time", args)}, fixtureStart.Add(-metricLookback), fixtureStart.Add(5*time.Minute))
	wantCounts := []float64{10, 90, 6, 9}
	wantSums := []float64{30, 200, 12, 18}
	wantBounds := [][]float64{{2, 3, 5}, {20, 30, 40}, {1, 2, 3}, {2, 3, 4}}
	if r.Frame.Rows != 4 {
		t.Fatalf("rows=%d", r.Frame.Rows)
	}
	for i, weights := range metricValues(t, r, "weights") {
		var got []float64
		if err := json.Unmarshal([]byte(weights.(string)), &got); err != nil {
			t.Fatal(err)
		}
		if metricValues(t, r, "count_increase")[i] != wantCounts[i] || metricValues(t, r, "sum_increase")[i] != wantSums[i] || !reflect.DeepEqual(got, wantBounds[i]) {
			t.Fatalf("row %d: %+v", i, r.Frame)
		}
	}
}

func TestMetricReadScopeIncludesSeparateLookbackBatch(t *testing.T) {
	engine, repo := newTestEngine(t)
	at := fixtureStart
	for i, p := range []struct {
		offset time.Duration
		value  float64
	}{{-time.Minute, 100}, {0, 160}, {time.Minute, 220}} {
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprintf("lookback%d", i), Metrics: []telemetry.Metric{metricCounter(at.Add(p.offset), p.value)}}); err != nil {
			t.Fatal(err)
		}
	}
	p := Panel{ID: "rate", Title: "Rate", Viz: "timeseries", Query: &Spec{From: "metrics", Where: []string{"name='net'"}, Measures: []string{"rate()"}, Bucket: "1m"}}
	original := Scope{Start: at, End: at.Add(2 * time.Minute), Interval: time.Minute}
	read := metricReadScope(&p, original)
	if !read.Start.Equal(at.Add(-10*time.Minute)) || !original.Start.Equal(at) {
		t.Fatalf("read=%+v original=%+v", read, original)
	}
	e := NewExecutor(engine, 30)
	cte, args, err := metricSeriesCTE(nil, original, []string{"counter"})
	if err != nil {
		t.Fatal(err)
	}
	built := runMetricPanel(t, e, Panel{Viz: "table", SQL: bindFixtureTimes(t, cte+" SELECT _bucket AS time,increase,elapsed_s FROM metric_increases ORDER BY time", args)}, read.Start, read.End)
	if built.Frame.Rows != 2 || metricValues(t, built, "increase")[0] != 60.0 {
		t.Fatalf("lookback frame=%+v", built.Frame)
	}
	// A real executor SQL panel independently confirms baseline visibility.
	r := runMetricPanel(t, e, Panel{Viz: "table", SQL: `WITH prior AS (
        SELECT time,value,lag(value) OVER (ORDER BY time) AS previous FROM metrics
        WHERE $__window(time)
        AND name='net') SELECT value-previous AS increase FROM prior
        WHERE time >= CAST('2026-10-01 12:00:00' AS TIMESTAMPTZ_NS) ORDER BY time`}, read.Start, read.End)
	if !reflect.DeepEqual(metricValues(t, r, "increase"), []any{60.0, 60.0}) {
		t.Fatalf("snapshot=%+v", r.Frame)
	}
	structured := runMetricPanel(t, e, p, original.Start, original.End)
	if !reflect.DeepEqual(metricValues(t, structured, "rate"), []any{1.0, 1.0}) {
		t.Fatalf("structured lookback=%+v", structured.Frame)
	}
}

func TestHistogramPanelsShareResetIncreases(t *testing.T) {
	e, db := metricFixture(t)
	for _, m := range []telemetry.Metric{
		metricHistogram(fixtureStart.Add(-time.Minute), "[10,20]", "[10,20,30]", 60, 100, "a"),
		metricHistogram(fixtureStart, "[10,20]", "[12,23,35]", 70, 130, "a"),
		metricHistogram(fixtureStart.Add(time.Minute), "[5,20]", "[20,30,40]", 90, 200, "a"),
		metricHistogram(fixtureStart.Add(2*time.Minute), "[5,20]", "[1,2,3]", 6, 12, "a"),
	} {
		insertMetrics(t, db, m)
	}
	for _, viz := range []string{"histogram", "heatmap"} {
		q := &Spec{From: "metrics", Where: []string{"name='net'"}, Measures: []string{"count()"}, Histogram: &Histogram{Field: "value", Buckets: "explicit"}}
		if viz == "heatmap" {
			q.Bucket = "1m"
		}
		r := runMetricPanel(t, e, Panel{Viz: viz, Query: q}, fixtureStart, fixtureStart.Add(3*time.Minute))
		total := 0.0
		for _, n := range metricValues(t, r, "count") {
			total += n.(float64)
		}
		if total != 96 {
			t.Fatalf("%s count=%g want 96", viz, total)
		}
		if viz == "heatmap" {
			times := metricValues(t, r, "time")
			counts := metricValues(t, r, "count")
			buckets := map[int64]float64{}
			for i, n := range counts {
				buckets[times[i].(int64)] += n.(float64)
			}
			if buckets[fixtureStart.UnixMilli()] != 90 || buckets[fixtureStart.Add(time.Minute).UnixMilli()] != 6 || buckets[fixtureStart.Add(2*time.Minute).UnixMilli()] != 0 {
				t.Fatalf("buckets=%v", buckets)
			}
		}
	}
	// Single delta without elapsed still contributes observations.
	if _, err := db.Exec("DELETE FROM telemetry.metrics"); err != nil {
		t.Fatal(err)
	}
	m := metricHistogram(fixtureStart, "[10]", "[3,2]", 5, 30, "a")
	m.Temporality = "delta"
	insertMetrics(t, db, m)
	h := runMetricPanel(t, e, Panel{Viz: "histogram", Query: &Spec{From: "metrics", Where: []string{"name='net'"}, Measures: []string{"count()"}, Histogram: &Histogram{Field: "value", Buckets: "explicit"}}}, fixtureStart, fixtureStart.Add(time.Minute))
	if !reflect.DeepEqual(metricValues(t, h, "count"), []any{3.0, 2.0}) {
		t.Fatalf("delta counts=%+v", h.Frame)
	}
}

func TestHistogramSpecRejectsRemovedTemporality(t *testing.T) {
	raw := []byte(`{"name":"Hist","panels":[{"id":"h","title":"Hist","viz":"histogram","query":{"from":"metrics","where":["name='net'"],"measures":["count()"],"histogram":{"field":"value","buckets":"explicit","temporality":"delta"}}}]}`)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var d Dashboard
	if err := decoder.Decode(&d); err == nil || !strings.Contains(err.Error(), `unknown field "temporality"`) {
		t.Fatalf("decode=%v", err)
	}
}

func TestMetricIncreaseIgnoresUnconvertibleStart(t *testing.T) {
	for _, kind := range []string{"counter", "histogram"} {
		t.Run(kind, func(t *testing.T) {
			e, db := metricFixture(t)
			for i, start := range []int64{1, math.MaxInt64, 2, 0} {
				m := metricCounter(fixtureStart.Add(time.Duration(i)*time.Minute), float64(100+6*i))
				if kind == "histogram" {
					// A decreasing sum can represent negative observations. Only
					// count, bounds, or two usable start times detect a reset.
					m = metricHistogram(fixtureStart.Add(time.Duration(i)*time.Minute), "[10]", fmt.Sprintf("[2,%d]", 3+i), int64(5+i), float64(100-3*i), "a")
				}
				m.StartTimeUnixNanos = start
				insertMetrics(t, db, m)
			}
			r := metricIncreaseResult(t, e, Scope{Start: fixtureStart, End: fixtureStart.Add(4 * time.Minute)},
				" SELECT increase,count_increase,sum_increase,to_json(bound_increases) AS weights FROM metric_increases ORDER BY time")
			if kind == "counter" {
				if got := metricValues(t, r, "increase"); !reflect.DeepEqual(got, []any{6.0, 6.0, 6.0}) {
					t.Fatalf("NULL start reset counter: %v", got)
				}
			} else if !reflect.DeepEqual(metricValues(t, r, "count_increase"), []any{1.0, 1.0, 1.0}) ||
				!reflect.DeepEqual(metricValues(t, r, "sum_increase"), []any{-3.0, -3.0, -3.0}) ||
				!reflect.DeepEqual(metricValues(t, r, "weights"), []any{"[0,1]", "[0,1]", "[0,1]"}) {
				t.Fatalf("NULL start or negative sum reset histogram: %+v", r.Frame)
			}
		})
	}
}

func TestMetricIncreaseSkipsInvalidValuesAndResolvesTiedRetries(t *testing.T) {
	e, db := metricFixture(t)
	for _, p := range []struct {
		minute int
		value  float64
		ingest int
	}{
		{-1, 100, -1}, {0, 110, 0}, {0, math.NaN(), 1},
		{1, 125, 1}, {1, 120, 1}, {2, math.Inf(1), 2}, {3, 125, 3},
	} {
		m := metricCounter(fixtureStart.Add(time.Duration(p.minute)*time.Minute), p.value)
		m.IngestedAt = fixtureStart.Add(time.Duration(p.ingest) * time.Minute).UnixNano()
		insertMetrics(t, db, m)
	}
	r := metricIncreaseResult(t, e, Scope{Start: fixtureStart, End: fixtureStart.Add(4 * time.Minute)},
		" SELECT increase,elapsed_s FROM metric_increases ORDER BY time")
	if !reflect.DeepEqual(metricValues(t, r, "increase"), []any{10.0, 15.0, 0.0}) ||
		!reflect.DeepEqual(metricValues(t, r, "elapsed_s"), []any{60.0, 60.0, 120.0}) {
		t.Fatalf("invalid values or tied retry affected baseline: %+v", r.Frame)
	}
}

func TestMetricIncreaseKeepsSignedNanosecondBoundaries(t *testing.T) {
	e, db := metricFixture(t)
	at := time.Unix(0, -17).UTC()
	for i, value := range []float64{100, 105, 108, 999} {
		insertMetrics(t, db, metricCounter(at.Add(time.Duration(i-1)*time.Nanosecond), value))
	}
	r := metricIncreaseResult(t, e, Scope{Start: at, End: at.Add(2 * time.Nanosecond)},
		" SELECT epoch_ns(time::TIMESTAMP_NS) AS nanos,increase,elapsed_s FROM metric_increases ORDER BY time")
	if !reflect.DeepEqual(metricValues(t, r, "nanos"), []any{-17.0, -16.0}) ||
		!reflect.DeepEqual(metricValues(t, r, "increase"), []any{5.0, 3.0}) ||
		!reflect.DeepEqual(metricValues(t, r, "elapsed_s"), []any{1e-9, 1e-9}) {
		t.Fatalf("nanosecond window/elapsed: %+v", r.Frame)
	}
}

func TestMetricHistogramRawKeepsZeroBoundsAndEmptyBoundLists(t *testing.T) {
	e, db := metricFixture(t)
	for _, m := range []telemetry.Metric{
		metricHistogram(fixtureStart, "[-10,0,10]", "[0,0,3,0]", 3, 15, "finite"),
		metricHistogram(fixtureStart, "[]", "[2]", 2, 0, "overflow"),
	} {
		m.Temporality = "delta"
		insertMetrics(t, db, m)
	}
	scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Minute)}
	raw, args, err := metricHistogramRaw("attributes['series']::VARCHAR AS dim", nil, scope)
	if err != nil {
		t.Fatal(err)
	}
	r := runMetricPanel(t, e, Panel{Viz: "table", SQL: "SELECT dim,lo,hi,n::DOUBLE AS weight FROM (" + bindFixtureTimes(t, raw, args) + ") ORDER BY dim,lo NULLS FIRST"}, scope.Start.Add(-metricLookback), scope.End)
	if !reflect.DeepEqual(metricValues(t, r, "weight"), []any{0.0, 0.0, 3.0, 0.0, 2.0}) ||
		!reflect.DeepEqual(metricValues(t, r, "hi"), []any{-10.0, 0.0, 10.0, nil, nil}) {
		t.Fatalf("raw histogram lost bounds: %+v", r.Frame)
	}
	h := runMetricPanel(t, e, Panel{Viz: "histogram", Query: &Spec{From: "metrics", Measures: []string{"count()"}, Histogram: &Histogram{Field: "value", Buckets: "explicit"}}}, scope.Start, scope.End)
	if !reflect.DeepEqual(metricValues(t, h, "count"), []any{2.0, 3.0}) {
		t.Fatalf("display retained zero weights: %+v", h.Frame)
	}
}

func TestMetricHistogramReadsSeparateLookbackBatch(t *testing.T) {
	engine, repo := newTestEngine(t)
	for i := range 4 {
		m := metricHistogram(fixtureStart.Add(time.Duration(i-1)*time.Minute), "[10]", fmt.Sprintf("[%d,%d]", 10+i, 20+2*i), int64(30+3*i), 0, "a")
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprintf("histlookback%d", i), Metrics: []telemetry.Metric{m}}); err != nil {
			t.Fatal(err)
		}
	}
	e := NewExecutor(engine, 30)
	p := Panel{Viz: "heatmap", Query: &Spec{From: "metrics", Where: []string{"name='net'", "attributes['series']='a'"}, Measures: []string{"count()"}, Bucket: "1m", Histogram: &Histogram{Field: "value", Buckets: "explicit"}}}
	r := runMetricPanel(t, e, p, fixtureStart, fixtureStart.Add(2*time.Minute))
	if !reflect.DeepEqual(metricValues(t, r, "count"), []any{1.0, 2.0}) || r.FromMS != fixtureStart.UnixMilli() || r.ToMS != fixtureStart.Add(2*time.Minute).UnixMilli() {
		t.Fatalf("lookback or visible window: %+v", r)
	}
}

func TestMetricReadScopeLeavesSQLAndOtherSignalsUnchanged(t *testing.T) {
	scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Minute)}
	for _, p := range []Panel{{SQL: "SELECT * FROM metrics WHERE $__window(time)"}, {Query: &Spec{From: "spans"}}, {Query: &Spec{From: "logs"}}} {
		if got := metricReadScope(&p, scope); !reflect.DeepEqual(got, scope) {
			t.Fatalf("unexpected lookback: %+v", got)
		}
	}
}

func TestHistogramIncreaseRebinsChangedBounds(t *testing.T) {
	for _, tc := range []struct {
		name, kind              string
		bounds, counts          []string
		countsTotal, startTimes []int64
		sums                    []float64
		wantCounts, wantSums    []any
		wantWeights             []any
	}{
		// Exact explicitExpHistogram outputs, also asserted by ingest goldens:
		// scale 0, zero count 5, positive offset 1/counts [10], then either
		// positive offset 0/counts [1,10] or negative offset 0/counts [1].
		{"new_minimum_with_nonzero_zero_count", "exponential_histogram",
			[]string{"[-5e-324,0,2,4]", "[-5e-324,0,1,2,4]"}, []string{"[0,5,0,10,0]", "[0,5,0,1,10,0]"},
			[]int64{15, 16}, []int64{0, 0}, []float64{30, 31.5},
			[]any{1.0}, []any{1.5}, []any{"[0,0,0,1,0,0]"}},
		{"first_negative_with_nonzero_zero_count", "exponential_histogram",
			[]string{"[-5e-324,0,2,4]", "[-2,-1,-5e-324,0,2,4]"}, []string{"[0,5,0,10,0]", "[0,1,0,5,0,10,0]"},
			[]int64{15, 16}, []int64{0, 0}, []float64{30, 28.5},
			[]any{1.0}, []any{-1.5}, []any{"[0,1,0,0,0,0,0]"}},
		{"unchanged_bounds_with_decreasing_bucket", "histogram",
			[]string{"[1,2]", "[1,2]"}, []string{"[0,100,0]", "[0,50,60]"},
			[]int64{100, 110}, []int64{0, 0}, []float64{150, 255},
			[]any{110.0}, []any{255.0}, []any{"[0,50,60]"}},
		{"growing_exponential_range", "exponential_histogram",
			[]string{"[1,2]", "[1,2,4]", "[1,2,4]"}, []string{"[0,100,0]", "[0,100,1,0]", "[0,102,1,0]"},
			[]int64{100, 101, 103}, []int64{1, 0, 2}, []float64{150, 153, 156},
			[]any{1.0, 2.0}, []any{3.0, 3.0}, []any{"[0,0,1,0]", "[0,2,0,0]"}},
		{"downscale", "exponential_histogram",
			[]string{"[1,2,4,8]", "[1,4]"}, []string{"[2,3,4,5,6]", "[3,9,15]"},
			[]int64{20, 27}, []int64{1, 1}, []float64{100, 130},
			[]any{7.0}, []any{30.0}, []any{"[1,2,4]"}},
		{"refinement", "exponential_histogram",
			[]string{"[1,4]", "[1,2,4]"}, []string{"[0,5,0]", "[0,3,3,0]"},
			[]int64{5, 6}, []int64{1, 1}, []float64{10, 15},
			[]any{6.0}, []any{15.0}, []any{"[0,3,3,0]"}},
		{"compatible_but_negative_bucket", "histogram",
			[]string{"[1,2,4,8]", "[1,4]"}, []string{"[2,3,4,5,6]", "[1,9,15]"},
			[]int64{20, 25}, []int64{1, 1}, []float64{100, 120},
			[]any{25.0}, []any{120.0}, []any{"[1,9,15]"}},
		{"nonzero_overflow_straddles_new_bound", "histogram",
			[]string{"[1,2]", "[1,2,4]"}, []string{"[0,1,2]", "[0,2,1,1]"},
			[]int64{3, 4}, []int64{1, 1}, []float64{10, 15},
			[]any{4.0}, []any{15.0}, []any{"[0,2,1,1]"}},
		{"collapse_to_overflow", "histogram",
			[]string{"[1,2]", "[]"}, []string{"[2,3,4]", "[12]"},
			[]int64{9, 12}, []int64{1, 1}, []float64{10, 15},
			[]any{3.0}, []any{5.0}, []any{"[3]"}},
		{"changed_start_resets_compatible_bounds", "histogram",
			[]string{"[1,2]", "[1,2,4]"}, []string{"[0,100,0]", "[0,100,1,0]"},
			[]int64{100, 101}, []int64{1, 2}, []float64{150, 153},
			[]any{101.0}, []any{153.0}, []any{"[0,100,1,0]"}},
		{"decreasing_count_resets_compatible_bounds", "histogram",
			[]string{"[1,2]", "[1,2,4]"}, []string{"[0,100,0]", "[0,90,1,0]"},
			[]int64{100, 91}, []int64{1, 1}, []float64{150, 140},
			[]any{91.0}, []any{140.0}, []any{"[0,90,1,0]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, db := metricFixture(t)
			for i := range tc.bounds {
				m := metricHistogram(fixtureStart.Add(time.Duration(i-1)*time.Minute), tc.bounds[i], tc.counts[i], tc.countsTotal[i], tc.sums[i], "a")
				m.Type, m.StartTimeUnixNanos = tc.kind, tc.startTimes[i]
				insertMetrics(t, db, m)
			}
			scope := Scope{Start: fixtureStart, End: fixtureStart.Add(2 * time.Minute)}
			r := metricIncreaseResult(t, e, scope, " SELECT count_increase,sum_increase,to_json(bound_increases) AS weights FROM metric_increases ORDER BY time")
			if !reflect.DeepEqual(metricValues(t, r, "count_increase"), tc.wantCounts) ||
				!reflect.DeepEqual(metricValues(t, r, "sum_increase"), tc.wantSums) ||
				!reflect.DeepEqual(metricValues(t, r, "weights"), tc.wantWeights) {
				t.Fatalf("rebinned increases=%+v", r.Frame)
			}
			if tc.name == "growing_exponential_range" {
				h := runMetricPanel(t, e, Panel{Viz: "histogram", Query: &Spec{From: "metrics", Measures: []string{"count()"}, Histogram: &Histogram{Field: "value", Buckets: "explicit"}}}, scope.Start, scope.End)
				if !reflect.DeepEqual(metricValues(t, h, "count"), []any{2.0}) {
					t.Fatalf("growing range distribution=%+v", h.Frame)
				}
			}
		})
	}
}

func TestMetricSeriesFiltersKindsAtInput(t *testing.T) {
	e, db := metricFixture(t)
	m := metricCounter(fixtureStart, 100)
	insertMetrics(t, db, m)
	m.Type, m.Name = "gauge", "gauge"
	insertMetrics(t, db, m)
	insertMetrics(t, db, metricHistogram(fixtureStart, "[10]", "[1,2]", 3, 10, "a"))
	scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Minute)}
	for _, tc := range []struct {
		kinds []string
		want  []any
	}{
		{[]string{"counter", "gauge", "histogram"}, []any{"counter", "gauge", "histogram"}},
		{[]string{"counter"}, []any{"counter"}},
		{[]string{"gauge", "counter"}, []any{"counter", "gauge"}},
		{[]string{"histogram"}, []any{"histogram"}},
	} {
		cte, args, err := metricSeriesCTE(nil, scope, tc.kinds)
		if err != nil {
			t.Fatal(err)
		}
		r := runMetricPanel(t, e, Panel{Viz: "table", SQL: bindFixtureTimes(t, cte+" SELECT type FROM metric_points ORDER BY type", args)}, scope.Start.Add(-metricLookback), scope.End)
		if !reflect.DeepEqual(metricValues(t, r, "type"), tc.want) {
			t.Fatalf("kinds=%v points=%+v", tc.kinds, r.Frame)
		}
	}
}

func TestMetricSeriesRejectsEmptyKinds(t *testing.T) {
	for _, kinds := range [][]string{nil, {}} {
		text, args, err := metricSeriesCTE(nil, Scope{}, kinds)
		if err == nil || err.Error() != "metric series requires at least one kind" || text != "" || args != nil {
			t.Fatalf("kinds=%v: text=%q args=%v err=%v", kinds, text, args, err)
		}
	}
}

func TestMetricSeriesKindFilterPreservesHistogramAgainstLaterGauge(t *testing.T) {
	e, db := metricFixture(t)
	insertMetrics(t, db, metricHistogram(fixtureStart.Add(-time.Minute), "[10]", "[3,4]", 7, 40, "a"))
	current := metricHistogram(fixtureStart, "[10]", "[4,6]", 10, 60, "a")
	insertMetrics(t, db, current)
	// Every series identity field and the event timestamp are identical. The
	// gauge would win retry deduplication if kinds were filtered afterwards.
	gauge := current
	gauge.Type, gauge.Value = "gauge", 100
	gauge.IngestedAt++
	insertMetrics(t, db, gauge)
	scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Minute)}
	cte, args, err := metricSeriesCTE(nil, scope, []string{"histogram"})
	if err != nil {
		t.Fatal(err)
	}
	r := runMetricPanel(t, e, Panel{Viz: "table", SQL: bindFixtureTimes(t,
		cte+" SELECT count_increase,to_json(bound_increases) AS weights FROM metric_increases", args)}, scope.Start.Add(-metricLookback), scope.End)
	if !reflect.DeepEqual(metricValues(t, r, "count_increase"), []any{3.0}) ||
		!reflect.DeepEqual(metricValues(t, r, "weights"), []any{"[1,2]"}) {
		t.Fatalf("later gauge displaced histogram: %+v", r.Frame)
	}
}

func TestHistogramIncreaseExcludesUnorderedBoundsBeforePairing(t *testing.T) {
	for _, tc := range []struct {
		name, bounds, counts string
	}{
		{"duplicate", "[1,1,2]", "[0,300,300,300]"},
		{"reordered", "[2,1]", "[0,450,450]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, db := metricFixture(t)
			for _, minute := range []int{-2, 0, 1} {
				m := metricHistogram(fixtureStart.Add(time.Duration(minute)*time.Minute), tc.bounds, tc.counts, 900, 900, "a")
				m.IngestedAt++ // malformed retry must not displace the valid point
				insertMetrics(t, db, m)
			}
			insertMetrics(t, db, metricHistogram(fixtureStart.Add(-time.Minute), "[1,2]", "[2,3,5]", 10, 10, "a"))
			insertMetrics(t, db, metricHistogram(fixtureStart.Add(time.Minute), "[1,2]", "[3,5,7]", 15, 15, "a"))
			r := metricIncreaseResult(t, e, Scope{Start: fixtureStart.Add(-time.Minute), End: fixtureStart.Add(2 * time.Minute)},
				" SELECT count_increase,elapsed_s,to_json(bound_increases) AS weights FROM metric_increases ORDER BY time")
			if !reflect.DeepEqual(metricValues(t, r, "count_increase"), []any{5.0}) ||
				!reflect.DeepEqual(metricValues(t, r, "elapsed_s"), []any{120.0}) ||
				!reflect.DeepEqual(metricValues(t, r, "weights"), []any{"[1,2,2]"}) {
				t.Fatalf("malformed bounds affected pairing: %+v", r.Frame)
			}
		})
	}
}

func TestMetricHistogramJSONPreservesSubnormalZeroBound(t *testing.T) {
	_, db := metricFixture(t)
	bounds, err := json.Marshal([]float64{-math.SmallestNonzeroFloat64, 0, 2, 4})
	if err != nil {
		t.Fatal(err)
	}
	var got float64
	if err := db.QueryRow(`SELECT (from_json(?,'["DOUBLE"]'))[1]`, string(bounds)).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if math.Float64bits(got) != math.Float64bits(-math.SmallestNonzeroFloat64) {
		t.Fatalf("JSON bound=%g bits=%x want=%x", got, math.Float64bits(got), math.Float64bits(-math.SmallestNonzeroFloat64))
	}
}
