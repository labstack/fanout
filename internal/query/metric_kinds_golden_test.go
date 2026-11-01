package query

import (
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

var updateMetricKindGolden = flag.Bool("update-metric-kinds", false, "rewrite testdata/metric_kinds.golden.json")

// metricKindFixture writes every stored metric kind: gauge, updown sum,
// cumulative and delta counters, explicit and exponential histograms
// (cumulative and delta) and a summary. Each kind has its own name, and
// "mixed" holds all of them at once, including one identity that changes
// kind. Retries, resets, gaps and non-finite values exercise every branch.
func metricKindFixture(t *testing.T) *Executor {
	e, db := metricFixture(t)
	type kind struct {
		name, metricType, temporality string
		exponential                   bool
	}
	kinds := []kind{
		{"gauge", "gauge", "", false},
		{"updown", "updown", "cumulative", false},
		{"counter", "counter", "cumulative", false},
		{"delta_counter", "counter", "delta", false},
		{"histogram", "histogram", "cumulative", false},
		{"delta_histogram", "histogram", "delta", false},
		{"exponential", "histogram", "cumulative", true},
		{"summary", "summary", "", false},
	}
	write := func(name string, k kind, series string, step int, at time.Time, ingest time.Time) {
		m := metricCounter(at, 0)
		m.Name, m.Type, m.Temporality = name, k.metricType, k.temporality
		m.ServiceName = "svc-" + series
		m.Attributes = map[string]any{"series": series}
		n := float64(step)
		cumulative := k.temporality == "cumulative"
		switch {
		case k.metricType == "histogram":
			c := []uint64{uint64(step%3 + 1), uint64(step%5 + 1), uint64(step%2 + 1)}
			if cumulative {
				c = []uint64{uint64(2*step + 1), uint64(3*step + 1), uint64(step + 1)}
				if step == 9 {
					c = []uint64{1, 1, 1} // reset
				}
			}
			var total uint64
			for _, v := range c {
				total += v
			}
			m.Value = 0
			m.Histogram = telemetry.Histogram{Count: total, Sum: float64(total) * 12.5, HasSum: true}
			switch {
			case k.exponential && step >= 14:
				// Scale 1 splits every scale-0 bucket in two (rebinned bounds).
				m.Exponential, m.Scale, m.PositiveOffset = true, 1, 6
				m.PositiveCounts, m.NegativeCounts = []uint64{c[0], 0, c[1] / 2, c[1] - c[1]/2, c[2], 0}, []uint64{}
			case k.exponential:
				// Buckets (8,16], (16,32] and (32,64].
				m.Exponential, m.Scale, m.PositiveOffset = true, 0, 3
				m.PositiveCounts, m.NegativeCounts = c, []uint64{}
			case step >= 14:
				m.Bounds = []float64{10, 20, 50} // rebinned bounds
				m.Counts = []uint64{c[0], c[1] / 2, c[1] - c[1]/2, c[2]}
			default:
				m.Bounds, m.Counts = []float64{10, 50}, c
			}
		case k.metricType == "summary":
			m.Value = 0
			m.Histogram = telemetry.Histogram{Count: uint64(step), Sum: n * 3, HasSum: true,
				Quantiles: []float64{0.5, 0.99}, QuantileValues: []float64{n, 2 * n}}
		case cumulative && k.metricType == "counter":
			m.Value = 7 * n
			if step == 9 {
				m.Value = 2 // reset
			} else if step > 9 {
				m.Value = 7*n - 60
			}
		default:
			m.Value = 10 + float64((step*7)%13)
			if k.metricType == "gauge" && step == 5 {
				m.Value = math.NaN()
			}
		}
		if k.temporality == "delta" {
			m.StartTimeUnixNanos = at.Add(-30 * time.Second).UnixNano()
		}
		m.TimeUnixNanos, m.EventUnixNanos, m.IngestedAt = at.UnixNano(), at.UnixNano(), ingest.UnixNano()
		m.SeriesID = telemetry.MetricSeriesID(m)
		insertMetrics(t, db, m)
	}
	for _, k := range kinds {
		for _, name := range []string{k.name, "mixed"} {
			for _, series := range []string{"a", "b"} {
				for step := 0; step < 50; step++ {
					// Series b has a gap in the middle of the window.
					if series == "b" && step >= 30 && step < 40 {
						continue
					}
					at := fixtureStart.Add(-12*time.Minute + time.Duration(step)*30*time.Second)
					if series == "b" {
						at = at.Add(10 * time.Second)
					}
					identity := series
					if name == "mixed" {
						identity = k.name + "-" + series
					}
					write(name, k, identity, step, at, at)
					if step == 20 {
						write(name, k, identity, step+1, at, at.Add(-time.Second)) // older retry loses
					}
				}
			}
		}
	}
	// One identity reports as a gauge and then as a cumulative histogram.
	for step := 0; step < 50; step++ {
		at := fixtureStart.Add(-12*time.Minute + time.Duration(step)*30*time.Second)
		k := kinds[0]
		if step >= 25 {
			k = kinds[4]
		}
		write("mixed", k, "changes", step, at, at)
	}
	return e
}

func TestMetricKindResultsArePinned(t *testing.T) {
	e := metricKindFixture(t)
	names := []string{"gauge", "updown", "counter", "delta_counter", "histogram", "delta_histogram", "exponential", "summary", "mixed"}
	shapes := []struct {
		label, viz, bucket string
		measures, by       []string
	}{
		{"stat", "table", "", []string{"avg(value)", "sum(value)", "min(value)", "max(value)", "last(value)", "p95(value)"}, nil},
		{"stat_more", "table", "", []string{"quantile(value, 0.5)", "rate()", "count()", "count_distinct(attributes['series'])", "share()", "sum(hist_sum)"}, nil},
		{"stat_avg", "stat", "", []string{"avg(value)"}, nil},
		{"stat_sum", "stat", "", []string{"sum(value)"}, nil},
		{"stat_p95", "stat", "", []string{"p95(value)"}, nil},
		{"stat_rate", "stat", "", []string{"rate()"}, nil},
		{"stat_last", "stat", "", []string{"last(value)"}, nil},
		{"by_series", "table", "", []string{"avg(value)", "sum(value)", "last(value)", "p95(value)", "rate()"}, []string{"attributes['series']"}},
		{"series_1m", "timeseries", "1m", []string{"avg(value)", "max(value)", "p95(value)", "rate()"}, nil},
		{"avg_1m", "timeseries", "1m", []string{"avg(value)"}, nil},
		{"sum_1m_by_series", "timeseries", "1m", []string{"sum(value)"}, []string{"attributes['series']"}},
		{"last_1m_by_series", "timeseries", "1m", []string{"last(value)"}, []string{"attributes['series']"}},
		{"count_1m", "timeseries", "1m", []string{"count()"}, nil},
	}
	start, end := fixtureStart, fixtureStart.Add(15*time.Minute)
	got := map[string]map[string]any{}
	for _, name := range names {
		got[name] = map[string]any{}
		for _, shape := range shapes {
			r := runMetricPanel(t, e, Panel{Viz: shape.viz, Query: &Spec{From: "metrics", Where: []string{"name=" + sqlString(name)},
				Measures: shape.measures, By: shape.by, Bucket: shape.bucket}}, start, end)
			if r.Status != StatusOK && r.Status != StatusEmpty {
				t.Fatalf("%s %s: %+v", name, shape.label, r)
			}
			var values any = [][]any{}
			if r.Frame != nil {
				values = r.Frame.Values
			}
			b, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			var decoded any
			if err := json.Unmarshal(b, &decoded); err != nil {
				t.Fatal(err)
			}
			got[name][shape.label] = decoded
		}
	}
	path := filepath.Join("testdata", "metric_kinds.golden.json")
	if *updateMetricKindGolden {
		b, err := json.MarshalIndent(got, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]map[string]any
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		for _, shape := range shapes {
			if !sameMetricValues(metricRowSet(want[name][shape.label]), metricRowSet(got[name][shape.label])) {
				t.Errorf("%s %s:\n got=%v\nwant=%v", name, shape.label, got[name][shape.label], want[name][shape.label])
			}
		}
	}
}

// Rows tied on the displayed order may come back in any order, so frames
// compare as sorted row sets.
func metricRowSet(frame any) any {
	columns, _ := frame.([]any)
	rows := []any{}
	for i := range columns {
		values, _ := columns[i].([]any)
		for j, v := range values {
			if j == len(rows) {
				rows = append(rows, make([]any, len(columns)))
			}
			rows[j].([]any)[i] = v
		}
	}
	slices.SortFunc(rows, func(a, b any) int {
		x, _ := json.Marshal(a)
		y, _ := json.Marshal(b)
		return strings.Compare(string(x), string(y))
	})
	return rows
}

// Parallel aggregation may reorder floating-point additions.
func sameMetricValues(a, b any) bool {
	switch a := a.(type) {
	case []any:
		b, ok := b.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for i := range a {
			if !sameMetricValues(a[i], b[i]) {
				return false
			}
		}
		return true
	case float64:
		b, ok := b.(float64)
		return ok && math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
	default:
		return a == b
	}
}
