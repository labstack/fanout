package panel

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func runMetricMeasure(t *testing.T, e *Executor, measures []string, bucket string, by []string, start, end time.Time) Result {
	t.Helper()
	viz := "table"
	if bucket != "" {
		viz = "timeseries"
	}
	return runMetricPanel(t, e, Panel{Viz: viz, Query: &Query{From: "metrics", Where: []string{"name='net'"}, Measures: measures, By: by, Bucket: bucket}}, start, end)
}

func TestMetricRateUsesCoveredElapsedTime(t *testing.T) {
	e, db := metricFixture(t)
	for i := -1; i < 3; i++ {
		insertMetrics(t, db, metricCounter(fixtureStart.Add(time.Duration(i)*time.Minute), float64(100+60*(i+1))))
	}
	for _, bucket := range []string{"10s", "1m", "1h"} {
		r := runMetricMeasure(t, e, []string{"rate()"}, bucket, nil, fixtureStart, fixtureStart.Add(3*time.Minute))
		values := metricValues(t, r, "rate")
		if len(values) == 0 {
			t.Fatalf("%s: no rates", bucket)
		}
		for _, v := range values {
			if v != 1.0 {
				t.Fatalf("%s rate=%v want 1", bucket, v)
			}
		}
	}
	r := runMetricPanel(t, e, Panel{Viz: "stat", Query: &Query{From: "metrics", Where: []string{"name='net'"}, Measures: []string{"rate()"}}}, fixtureStart, fixtureStart.Add(3*time.Minute))
	if r.Frame.Totals[len(r.Frame.Totals)-1] != 1.0 {
		t.Fatalf("totals=%v", r.Frame.Totals)
	}
	auto := runMetricMeasure(t, e, []string{"rate()"}, "auto", nil, fixtureStart, fixtureStart.Add(3*time.Minute))
	duration, ok := ParseSpan(auto.Interval)
	if !ok || duration < time.Minute {
		t.Fatalf("auto=%q", auto.Interval)
	}
}

func TestMetricRestartDoesNotCarryOldSeries(t *testing.T) {
	e, db := metricFixture(t)
	for _, p := range []struct {
		minute int
		value  float64
		id     string
	}{{-1, 100, "old"}, {0, 160, "old"}, {1, 10000, "new"}, {2, 10060, "new"}} {
		m := metricCounter(fixtureStart.Add(time.Duration(p.minute)*time.Minute), p.value)
		m.Resource = map[string]any{"container.id": p.id}
		insertMetrics(t, db, m)
	}
	r := runMetricMeasure(t, e, []string{"rate()"}, "1m", nil, fixtureStart, fixtureStart.Add(3*time.Minute))
	times, values := metricValues(t, r, "time"), metricValues(t, r, "rate")
	got := map[int64]any{}
	for i, v := range values {
		got[times[i].(int64)] = v
	}
	if got[fixtureStart.UnixMilli()] != 1.0 || got[fixtureStart.Add(2*time.Minute).UnixMilli()] != 1.0 {
		t.Fatalf("restart=%v", got)
	}
	if v, exists := got[fixtureStart.Add(time.Minute).UnixMilli()]; exists && v != nil {
		t.Fatalf("first new point=%v", v)
	}
}

func TestMetricRateSumsPerSeriesAndIgnoresMissingElapsed(t *testing.T) {
	e, db := metricFixture(t)
	for _, p := range []struct {
		seconds int
		value   float64
		id      string
	}{{-60, 0, "a"}, {0, 60, "a"}, {30, 120, "a"}, {-60, 0, "b"}, {0, 120, "b"}, {90, 300, "b"}} {
		m := metricCounter(fixtureStart.Add(time.Duration(p.seconds)*time.Second), p.value)
		m.Attributes = map[string]any{"series": p.id}
		insertMetrics(t, db, m)
	}
	r := runMetricMeasure(t, e, []string{"rate()"}, "5m", nil, fixtureStart, fixtureStart.Add(2*time.Minute))
	a, b := 120.0/90.0, 300.0/150.0
	want := a + b
	if got := metricValues(t, r, "rate"); !reflect.DeepEqual(got, []any{want}) {
		t.Fatalf("rates=%v", got)
	}
	if _, err := db.Exec("DELETE FROM telemetry.metrics"); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		m := metricCounter(fixtureStart.Add(time.Duration(i)*time.Minute), 12)
		m.Temporality = "delta"
		insertMetrics(t, db, m)
	}
	sums := runMetricMeasure(t, e, []string{"sum(value)"}, "5m", nil, fixtureStart, fixtureStart.Add(2*time.Minute))
	rates := runMetricMeasure(t, e, []string{"rate()"}, "5m", nil, fixtureStart, fixtureStart.Add(2*time.Minute))
	if metricValues(t, sums, "sum")[0] != 24.0 || metricValues(t, rates, "rate")[0] != .2 {
		t.Fatalf("sum=%+v rate=%+v", sums.Frame, rates.Frame)
	}
}

func TestMetricMeasuresRespectKindsAndSeriesWeight(t *testing.T) {
	for _, kind := range []string{"gauge", "updown", "counter", "summary"} {
		t.Run(kind, func(t *testing.T) {
			e, db := metricFixture(t)
			for _, p := range []struct {
				seconds int
				value   float64
				id      string
			}{{-60, 0, "a"}, {0, 10, "a"}, {30, 30, "a"}, {-60, 0, "b"}, {0, 100, "b"}} {
				m := metricCounter(fixtureStart.Add(time.Duration(p.seconds)*time.Second), p.value)
				if kind == "gauge" || kind == "summary" {
					m.Type = kind
					m.Temporality = ""
					m.Monotonic = false
				}
				if kind == "updown" {
					m.Monotonic = false
				}
				m.Attributes = map[string]any{"series": p.id}
				insertMetrics(t, db, m)
			}
			want := map[string]any{"sum(value)": 120.0, "avg(value)": 60.0, "last(value)": 130.0, "min(value)": 10.0, "max(value)": 100.0, "p75(value)": 65.0, "quantile(value, 0.5)": 30.0}
			if kind == "counter" {
				want["sum(value)"] = 130.0
				want["avg(value)"] = nil
				want["p75(value)"] = nil
				want["quantile(value, 0.5)"] = nil
			}
			if kind == "summary" {
				for measure := range want {
					want[measure] = nil
				}
			}
			for text, value := range want {
				r := runMetricMeasure(t, e, []string{text}, "", nil, fixtureStart, fixtureStart.Add(time.Minute))
				got := r.Frame.Values[len(r.Frame.Columns)-1]
				if len(got) != 1 || got[0] != value {
					t.Fatalf("%s got=%v want=%v", text, got, value)
				}
			}
			r := runMetricMeasure(t, e, []string{"count()", "count_distinct(attributes['series'])", "share()"}, "", nil, fixtureStart, fixtureStart.Add(time.Minute))
			for i, value := range []float64{3, 2, 100} {
				if r.Frame.Values[i][0] != value {
					t.Fatalf("point operation=%+v", r.Frame)
				}
			}
		})
	}
}

func TestMetricNonValueFieldsKeepPerPointAggregates(t *testing.T) {
	for _, kind := range []string{"gauge", "updown", "counter", "histogram", "summary"} {
		t.Run(kind, func(t *testing.T) {
			e, db := metricFixture(t)
			for i, value := range []float64{10, 30, 100} {
				m := metricCounter(fixtureStart.Add(time.Duration(i)*time.Second), 999)
				if kind == "updown" {
					m.Monotonic = false
				}
				if kind != "updown" && kind != "counter" {
					m.Type = kind
					m.Monotonic = false
				}
				if kind == "gauge" || kind == "summary" {
					m.Temporality = ""
				}
				m.HistCount, m.HistSum = int64(value), value
				m.HistBoundsJSON, m.HistCountsJSON = "[10]", "[1,0]"
				series := "a"
				if i == 2 {
					series = "b"
				}
				m.Attributes = map[string]any{"series": series, "value": value}
				insertMetrics(t, db, m)
			}
			total, points := 140.0, 3.0
			wantAverage := total / points
			for _, field := range []string{"hist_count", "hist_sum", "attributes['value']"} {
				for _, tc := range []struct {
					fn   string
					want float64
				}{
					{"sum", 140}, {"avg", wantAverage}, {"min", 10}, {"max", 100}, {"last", 100}, {"p75", 65},
				} {
					r := runMetricMeasure(t, e, []string{tc.fn + "(" + field + ")"}, "", nil, fixtureStart, fixtureStart.Add(time.Minute))
					got := r.Frame.Values[len(r.Frame.Columns)-1]
					if len(got) != 1 || got[0] != tc.want {
						t.Fatalf("%s(%s)=%v want %v", tc.fn, field, got, tc.want)
					}
				}
			}
		})
	}
}

func TestMetricFiniteValuesDoNotPoisonLag(t *testing.T) {
	e, db := metricFixture(t)
	for i, v := range []float64{100, math.NaN(), math.Inf(1), math.Inf(-1), 160} {
		insertMetrics(t, db, metricCounter(fixtureStart.Add(time.Duration(i-1)*time.Minute), v))
	}
	r := runMetricMeasure(t, e, []string{"rate()"}, "5m", nil, fixtureStart, fixtureStart.Add(4*time.Minute))
	if !reflect.DeepEqual(metricValues(t, r, "rate"), []any{.25}) {
		t.Fatalf("finite=%+v", r.Frame)
	}
}

func TestMetricAuxiliaryAggregatesUseSeriesRates(t *testing.T) {
	e, db := metricFixture(t)
	for i := -4; i < 4; i++ {
		insertMetrics(t, db, metricCounter(fixtureStart.Add(time.Duration(i)*time.Minute), float64(1000+60*(i+4))))
	}
	start, end := fixtureStart, fixtureStart.Add(3*time.Minute)
	panels := []Panel{
		{ID: "stat", Title: "Rate", Viz: "stat", Query: &Query{From: "metrics", Where: []string{"name='net'"}, Measures: []string{"rate()"}}},
		{ID: "table", Title: "Rate", Viz: "table", Query: &Query{From: "metrics", Where: []string{"name='net'"}, Measures: []string{"rate()"}, By: []string{"service"}}, Options: &Options{Columns: []ColumnFormat{{Field: "rate", Format: "sparkline"}}}},
		{ID: "items", Title: "Rates", Viz: "scatter", Query: &Query{From: "metrics", Where: []string{"name='net'"}, Measures: []string{"rate() as x", "last(value) as y"}, By: []string{"service"}}},
		{ID: "states", Title: "Rates", Viz: "state_timeline", Query: &Query{From: "metrics", Where: []string{"name='net'"}, Measures: []string{"rate()"}, By: []string{"service"}, Bucket: "1m"}, Thresholds: []Threshold{{Value: 2, Status: "bad"}}},
	}
	compare := true
	results, err := e.Run(t.Context(), RunRequest{Dashboard: Dashboard{Name: "Metrics", Time: Time{From: &start, To: &end}, Panels: panels}, Compare: &compare})
	if err != nil {
		t.Fatal(err)
	}
	got := byID(results)
	if got["stat"].Frame.Totals[len(got["stat"].Frame.Totals)-1] != 1.0 || got["stat"].Previous == nil || got["stat"].Previous.Totals[len(got["stat"].Previous.Totals)-1] != 1.0 {
		t.Fatalf("stat=%+v", got["stat"])
	}
	if len(got["table"].Frame.Trends["rate"]) != 1 {
		t.Fatal("missing table trend")
	}
	for _, v := range got["table"].Frame.Trends["rate"][0] {
		if v != nil && v != 1.0 {
			t.Fatalf("trend=%v", v)
		}
	}
	if metricValues(t, got["items"], "x")[0] != 1.0 {
		t.Fatalf("items=%+v", got["items"])
	}
	for _, v := range metricValues(t, got["states"], "rate") {
		if v != 1.0 {
			t.Fatalf("state=%v", v)
		}
	}
}

func TestMetricRankingRecomputesOtherFromSeriesRates(t *testing.T) {
	e, db := metricFixture(t)
	for _, p := range []struct {
		seconds int
		value   float64
		id      string
	}{{-60, 0, "a"}, {0, 60, "a"}, {30, 120, "a"}, {-60, 0, "b"}, {0, 120, "b"}, {90, 300, "b"}} {
		m := metricCounter(fixtureStart.Add(time.Duration(p.seconds)*time.Second), p.value)
		m.ServiceName = p.id
		insertMetrics(t, db, m)
	}
	r := runMetricPanel(t, e, Panel{Viz: "timeseries", Options: &Options{Top: 1}, Query: &Query{
		From: "metrics", Where: []string{"name='net'"}, Measures: []string{"rate()"}, By: []string{"service"}, Bucket: "5m",
	}}, fixtureStart, fixtureStart.Add(2*time.Minute))
	dims, rates := metricValues(t, r, "service"), metricValues(t, r, "rate")
	got := map[string]any{}
	for i, d := range dims {
		got[d.(string)] = rates[i]
	}
	increase, elapsed := 120.0, 90.0
	wantOther := increase / elapsed
	if got["b"] != 2.0 || got["Other (1)"] != wantOther {
		t.Fatalf("ranking=%v", got)
	}
}

func TestMetricWindowClipsPointsAfterComputingIncrease(t *testing.T) {
	e, db := metricFixture(t)
	for _, p := range []struct {
		offset time.Duration
		value  float64
	}{{-11 * time.Minute, 0}, {-10 * time.Minute, 100}, {0, 160}, {time.Minute, 9999}} {
		insertMetrics(t, db, metricCounter(fixtureStart.Add(p.offset), p.value))
	}
	r := runMetricMeasure(t, e, []string{"sum(value)"}, "1m", nil, fixtureStart, fixtureStart.Add(time.Minute))
	if !reflect.DeepEqual(metricValues(t, r, "sum"), []any{60.0}) {
		t.Fatalf("clipped=%+v", r.Frame)
	}
	r = runMetricMeasure(t, e, []string{"rate()"}, "1m", nil, fixtureStart, fixtureStart.Add(time.Minute))
	if !reflect.DeepEqual(metricValues(t, r, "rate"), []any{.1}) {
		t.Fatalf("elapsed lookback=%+v", r.Frame)
	}
}

func TestMetricRankingUsesWholeWindowRatherThanBucketRates(t *testing.T) {
	e, db := metricFixture(t)
	for _, p := range []struct {
		seconds int
		value   float64
		service string
	}{
		{-60, 0, "a"}, {0, 60, "a"}, {30, 90, "a"}, {60, 120, "a"}, {90, 150, "a"},
		{-60, 0, "b"}, {0, 120, "b"}, {90, 300, "b"},
	} {
		m := metricCounter(fixtureStart.Add(time.Duration(p.seconds)*time.Second), p.value)
		m.ServiceName = p.service
		insertMetrics(t, db, m)
	}
	r := runMetricPanel(t, e, Panel{Viz: "timeseries", Options: &Options{Top: 1}, Query: &Query{
		From: "metrics", Where: []string{"name='net'"}, Measures: []string{"rate()"}, By: []string{"service"}, Bucket: "30s",
	}}, fixtureStart, fixtureStart.Add(2*time.Minute))
	names := metricValues(t, r, "service")
	for _, name := range names {
		if name != "b" && name != "Other (1)" {
			t.Fatalf("ranking=%v", names)
		}
	}
}

func TestMetricSeriesFiltersPreserveLiteralNamesAndAttributeKeys(t *testing.T) {
	e, db := metricFixture(t)
	for i := -1; i < 2; i++ {
		m := metricCounter(fixtureStart.Add(time.Duration(i)*time.Minute), float64(100+60*i))
		m.Name = "metric_points"
		m.Attributes = map[string]any{"metric_input": "metric_previous"}
		insertMetrics(t, db, m)
	}
	r := runMetricPanel(t, e, Panel{Viz: "table", Query: &Query{From: "metrics", Where: []string{
		"name='metric_points'", "attributes['metric_input']='metric_previous'",
	}, Measures: []string{"rate()", "sum(value)"}}}, fixtureStart, fixtureStart.Add(2*time.Minute))
	if !reflect.DeepEqual(metricValues(t, r, "rate"), []any{1.0}) || !reflect.DeepEqual(metricValues(t, r, "sum"), []any{120.0}) {
		t.Fatalf("filters=%+v", r.Frame)
	}
}

func TestMetricOtherRecomputesSeriesMeansPercentilesAndDistinctUnion(t *testing.T) {
	for _, tc := range []struct {
		measure string
		want    float64
	}{
		{"avg(value)", 55}, {"sum(value)", 220}, {"last(value)", 220}, {"p75(value)", 10},
		{"count_distinct(attributes['shared'])", 1},
	} {
		t.Run(tc.measure, func(t *testing.T) {
			e, db := metricFixture(t)
			for _, service := range []string{"a", "b", "c"} {
				n, value := 20, 1000.0
				if service == "b" {
					n, value = 3, 100
				}
				if service == "c" {
					n, value = 21, 10
				}
				for i := range n {
					m := metricCounter(fixtureStart.Add(time.Duration(i)*time.Second), value)
					m.Type, m.Temporality, m.Monotonic = "gauge", "", false
					m.ServiceName = service
					id := "one"
					if service == "b" && i > 0 {
						id = "two"
					}
					if service == "c" && i > 0 {
						id = "two"
					}
					m.Attributes = map[string]any{"series": id, "shared": "same"}
					insertMetrics(t, db, m)
				}
			}
			// Rank the full window, then recompute the folded measure from series.
			r := runMetricPanel(t, e, Panel{Viz: "bar", Options: &Options{Top: 1}, Query: &Query{
				From: "metrics", Where: []string{"name='net'"}, By: []string{"name", "service"}, Measures: []string{tc.measure + " as folded"},
			}}, fixtureStart, fixtureStart.Add(time.Minute))
			dims, values := metricValues(t, r, "service"), metricValues(t, r, "folded")
			found := false
			for i, d := range dims {
				if d == "Other (2)" {
					found = true
					if values[i] != tc.want {
						t.Fatalf("Other=%v want %v", values[i], tc.want)
					}
				}
			}
			if !found {
				t.Fatalf("groups=%v", dims)
			}
		})
	}
}

func TestMetricUndefinedValueKeepsNullWithCompatiblePointMeasure(t *testing.T) {
	e, db := metricFixture(t)
	m := metricCounter(fixtureStart, 0)
	m.Type, m.Monotonic = "histogram", false
	m.HistBoundsJSON, m.HistCountsJSON = "broken", "broken"
	insertMetrics(t, db, m)
	r := runMetricMeasure(t, e, []string{"sum(value)", "count()"}, "", nil, fixtureStart, fixtureStart.Add(time.Minute))
	if r.Status != StatusOK || metricValues(t, r, "sum")[0] != nil || metricValues(t, r, "count")[0] != 1.0 {
		t.Fatalf("mixed=%+v", r)
	}
	r = runMetricMeasure(t, e, []string{"sum(value)"}, "", nil, fixtureStart, fixtureStart.Add(time.Minute))
	if r.Status != StatusEmpty || metricValues(t, r, "sum")[0] != nil {
		t.Fatalf("undefined=%+v", r)
	}
}

func TestMetricNonValueLastIgnoresLaterInvalidSample(t *testing.T) {
	e, db := metricFixture(t)
	for i, v := range []float64{10, 30, math.NaN()} {
		m := metricCounter(fixtureStart.Add(time.Duration(i)*time.Second), v)
		m.HistSum = v
		m.ServiceName = "a"
		if i == 1 {
			m.ServiceName = "b"
		}
		insertMetrics(t, db, m)
	}
	r := runMetricMeasure(t, e, []string{"last(hist_sum)"}, "", nil, fixtureStart, fixtureStart.Add(time.Minute))
	if metricValues(t, r, "last")[0] != 30.0 {
		t.Fatalf("last=%+v", r.Frame)
	}
}

func TestMetricKindsAreSelectedBeforeSeriesWindows(t *testing.T) {
	e, db := metricFixture(t)
	for _, p := range []struct {
		seconds int
		value   float64
		gauge   bool
	}{
		{-60, 100, false}, {-30, 9999, true}, {0, 160, false}, {30, 42, true}, {60, 220, false},
	} {
		m := metricCounter(fixtureStart.Add(time.Duration(p.seconds)*time.Second), p.value)
		if p.gauge {
			m.Type, m.Monotonic, m.Temporality = "gauge", false, ""
		}
		insertMetrics(t, db, m)
	}
	r := runMetricMeasure(t, e, []string{"rate()", "avg(value)", "sum(value)"}, "", nil, fixtureStart, fixtureStart.Add(2*time.Minute))
	if metricValues(t, r, "rate")[0] != 1.0 || metricValues(t, r, "avg")[0] != 42.0 || metricValues(t, r, "sum")[0] != 162.0 {
		t.Fatalf("kinds=%+v", r.Frame)
	}
}

func TestMetricEmptyNumericListsReturnNull(t *testing.T) {
	e, db := metricFixture(t)
	m := metricCounter(fixtureStart, 1)
	m.HistSum = math.NaN()
	insertMetrics(t, db, m)
	for _, measure := range []string{"avg(hist_sum)", "sum(hist_sum)", "min(hist_sum)", "max(hist_sum)", "p95(hist_sum)", "last(hist_sum)"} {
		r := runMetricMeasure(t, e, []string{measure}, "", nil, fixtureStart, fixtureStart.Add(time.Minute))
		if r.Status != StatusEmpty || r.Frame.Values[0][0] != nil {
			t.Fatalf("%s=%+v", measure, r)
		}
	}
}

func TestMetricBaselineIsUndefinedButZeroIncreaseHasValue(t *testing.T) {
	e, db := metricFixture(t)
	insertMetrics(t, db, metricCounter(fixtureStart, 100))
	r := runMetricMeasure(t, e, []string{"rate()", "sum(value)"}, "", nil, fixtureStart, fixtureStart.Add(3*time.Minute))
	if r.Status != StatusEmpty || metricValues(t, r, "rate")[0] != nil || metricValues(t, r, "sum")[0] != nil || strings.Contains(r.Diagnosis, "No metrics match") {
		t.Fatalf("baseline=%+v", r)
	}
	insertMetrics(t, db, metricCounter(fixtureStart.Add(time.Minute), 100))
	r = runMetricMeasure(t, e, []string{"rate()", "sum(value)"}, "1m", nil, fixtureStart, fixtureStart.Add(3*time.Minute))
	if r.Status != StatusOK || !reflect.DeepEqual(metricValues(t, r, "rate"), []any{nil, 0.0}) || !reflect.DeepEqual(metricValues(t, r, "sum"), []any{nil, 0.0}) {
		t.Fatalf("zero=%+v", r)
	}
}

func TestMetricTableTrendShareUsesCompleteScopeBeforeDisplayedTuples(t *testing.T) {
	e, db := metricFixture(t)
	for i, service := range []string{"a", "a", "b"} {
		m := metricCounter(fixtureStart.Add(time.Duration(i)*time.Second), 10)
		m.ServiceName = service
		m.Type, m.Monotonic, m.Temporality = "gauge", false, ""
		insertMetrics(t, db, m)
	}
	r := runMetricPanel(t, e, Panel{Viz: "table", Options: &Options{Columns: []ColumnFormat{{Field: "portion", Format: "sparkline"}}}, Query: &Query{
		From: "metrics", Where: []string{"name='net'"}, By: []string{"service"}, Measures: []string{"count()", "share() as portion", "sum(value)"}, Sort: "portion", Limit: 1,
	}}, fixtureStart, fixtureStart.Add(2*time.Minute))
	want := 200.0 / 3.0
	if r.Frame.Rows != 1 || metricValues(t, r, "service")[0] != "a" || len(r.Frame.Trends) != 1 || len(r.Frame.Trends["portion"]) != 1 || !reflect.DeepEqual(r.Frame.Trends["portion"][0], []any{want, nil}) {
		t.Fatalf("trends=%+v", r.Frame)
	}
}

func TestMetricLastExpiresRestartedSeriesAtFiveMinutes(t *testing.T) {
	e, db := metricFixture(t)
	for _, p := range []struct {
		seconds int
		id      string
		value   float64
	}{
		{0, "old", 10}, {90, "old", 20}, {60, "new", 100}, {180, "new", 110}, {360, "new", 120}, {480, "new", 130},
	} {
		m := metricCounter(fixtureStart.Add(time.Duration(p.seconds)*time.Second), p.value)
		m.Resource = map[string]any{"container.id": p.id}
		insertMetrics(t, db, m)
	}
	for _, tc := range []struct {
		end  time.Duration
		want float64
	}{
		// A point exactly at the exclusive end belongs to the next bucket.
		{3 * time.Minute, 120}, {390 * time.Second, 140}, {390*time.Second + time.Nanosecond, 120},
	} {
		// Keep a three-minute stat window while its end advances past expiry.
		end := fixtureStart.Add(tc.end)
		r := runMetricPanel(t, e, Panel{Viz: "stat", Query: &Query{From: "metrics", Where: []string{"name='net'"}, Measures: []string{"last(value)"}}}, end.Add(-3*time.Minute), end)
		if got := r.Frame.Totals[len(r.Frame.Totals)-1]; got != tc.want {
			t.Fatalf("end=%s last=%v want %v", tc.end, got, tc.want)
		}
	}
	r := runMetricMeasure(t, e, []string{"last(value)"}, "1m", nil, fixtureStart, fixtureStart.Add(8*time.Minute))
	if got := metricValues(t, r, "last"); !reflect.DeepEqual(got, []any{10.0, 120.0, 120.0, 130.0, 130.0, 130.0, 120.0, 120.0}) {
		t.Fatalf("bucket ends=%v", got)
	}
}

func TestMetricLastStatCountsOnlyRecentlyReportingSeries(t *testing.T) {
	e, db := metricFixture(t)
	end := fixtureStart.Add(24 * time.Hour)
	for _, p := range []struct {
		at    time.Time
		id    string
		value float64
	}{
		{fixtureStart, "ancient", 1000}, {end.Add(-5 * time.Minute), "boundary", 10},
		{end.Add(-5*time.Minute - time.Nanosecond), "stale", 100}, {end.Add(-time.Nanosecond), "current", 20},
		{end, "outside", 500},
	} {
		m := metricCounter(p.at, p.value)
		m.Attributes = map[string]any{"series": p.id}
		insertMetrics(t, db, m)
	}
	r := runMetricPanel(t, e, Panel{Viz: "stat", Query: &Query{From: "metrics", Where: []string{"name='net'"}, Measures: []string{"last(value)"}}}, fixtureStart, end)
	if got := r.Frame.Totals[len(r.Frame.Totals)-1]; got != 30.0 {
		t.Fatalf("long window last=%v", got)
	}
}

func TestMetricPointOnlyMeasuresSkipLookback(t *testing.T) {
	e, db := metricFixture(t)
	for _, p := range []struct {
		offset time.Duration
		value  float64
	}{{-time.Nanosecond, 9999}, {0, 10}, {30 * time.Second, 30}} {
		m := metricCounter(fixtureStart.Add(p.offset), p.value)
		m.Type, m.Monotonic, m.Temporality = "gauge", false, ""
		insertMetrics(t, db, m)
	}
	r := runMetricMeasure(t, e, []string{"sum(value)", "avg(value)", "min(value)", "max(value)", "p95(value)"}, "", nil, fixtureStart, fixtureStart.Add(time.Minute))
	for i, want := range []float64{20, 20, 10, 30, 29} {
		if r.Frame.Values[i][0] != want {
			t.Fatalf("point measure %d=%v", i, r.Frame.Values[i])
		}
	}
	for _, measure := range []string{"min(value)", "max(value)", "avg(hist_sum)", "p95(hist_sum)"} {
		p := Panel{Query: &Query{From: "metrics", Measures: []string{measure}}}
		scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Minute)}
		if read := metricReadScope(&p, scope); !reflect.DeepEqual(read, scope) {
			t.Fatalf("%s read=%+v", measure, read)
		}
	}
}

func TestMetricMultiDayBucketsAndTableTrendsUseEpochOrigin(t *testing.T) {
	e, db := metricFixture(t)
	for i, value := range []float64{10, 20, 30} {
		m := metricCounter(fixtureStart.Add(time.Duration(i)*48*time.Hour), value)
		m.Type, m.Monotonic, m.Temporality = "gauge", false, ""
		insertMetrics(t, db, m)
	}
	p := Panel{ID: "m", Title: "Metric", Viz: "table", Query: &Query{From: "metrics", Where: []string{"name='net'"}, By: []string{"service"}, Measures: []string{"avg(value)"}}, Options: &Options{Columns: []ColumnFormat{{Field: "avg", Format: "sparkline"}}}}
	d := Dashboard{Name: "Metrics", Panels: []Panel{p}}
	Normalize(&d)
	checked, problems, err := Check(t.Context(), e.engine, &d)
	if err != nil || len(problems) > 0 {
		t.Fatalf("check: %v %v", err, problems)
	}
	p = d.Panels[0]
	scope := Scope{Start: fixtureStart, End: fixtureStart.Add(400 * 24 * time.Hour), Interval: 48 * time.Hour}
	f, _, err := e.runScope(t.Context(), &p, checked, scope)
	if err != nil {
		t.Fatal(err)
	}
	lo := alignedBucketMillis(scope.Start, scope.Interval)
	for i, value := range []float64{10, 20, 30} {
		if f.Values[0][i] != lo+int64(i)*scope.Interval.Milliseconds() || f.Values[2][i] != value {
			t.Fatalf("epoch buckets=%+v", f)
		}
	}
	scope.Interval = 0
	f, _, err = e.runScope(t.Context(), &p, checked, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.attachTableTrends(t.Context(), &p, checked, scope, f); err != nil {
		t.Fatal(err)
	}
	trend := f.Trends["avg"][0]
	if len(trend) != 201 || !reflect.DeepEqual(trend[:3], []any{10.0, 20.0, 30.0}) {
		t.Fatalf("epoch trend=%v", trend)
	}
}
