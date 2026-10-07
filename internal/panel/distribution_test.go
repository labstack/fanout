package panel

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestM2DistributionCounts(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = []Panel{{ID: "h", Title: "Latency distribution", Viz: "histogram", Query: &Query{From: "spans", Where: []string{"service = $service"}, Measures: []string{"count()"}, Histogram: &Histogram{Field: "duration_ms", Buckets: "log2"}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	f := got[0].Frame
	if got[0].Status != "ok" || f == nil || f.Rows != 3 {
		t.Fatalf("distribution: %+v", got)
	}
	var n float64
	for _, v := range f.Values[2] {
		n += v.(float64)
	}
	if n != 120 {
		t.Fatalf("count=%v", n)
	}
	engine, repo := newTestEngine(t)
	at := fixtureStart.UnixNano()
	err = repo.Commit(t.Context(), telemetrystore.Batch{ID: "hist-metric", Metrics: []telemetry.Metric{{Namespace: "shop", ServiceName: "checkout", Name: "latency", Type: "histogram", TimeUnixNanos: at, EventUnixNanos: at, IngestedAt: at, HistBoundsJSON: "[10,20]", HistCountsJSON: "[2,3,5]", HistCount: 10}}})
	if err != nil {
		t.Fatal(err)
	}
	e = NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d.Variables = nil
	d.Panels[0].Query = &Query{From: "metrics", Where: []string{"name = 'latency'"}, Measures: []string{"count()"}, Histogram: &Histogram{Field: "value", Buckets: "explicit", Temporality: "delta"}}
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	f = got[0].Frame
	if got[0].Status != "ok" || f.Rows != 3 || f.Values[2][2].(float64) != 5 || f.Values[1][2] != nil {
		t.Fatalf("weighted overflow: %+v", got)
	}
}
func TestM2MetricHistogramTemporality(t *testing.T) {
	engine, repo := newTestEngine(t)
	metrics := []telemetry.Metric{}
	for scrape, counts := range []string{"[10,20,30]", "[12,23,34]", "[15,27,40]"} {
		n := fixtureStart.Add(time.Duration(scrape) * time.Minute).UnixNano()
		metrics = append(metrics, telemetry.Metric{Namespace: "shop", ServiceName: "checkout", Name: "latency", Type: "histogram", Unit: "ms", TimeUnixNanos: n, EventUnixNanos: n, IngestedAt: n, HistBoundsJSON: "[10,20]", HistCountsJSON: counts, HistCount: 100, Attributes: map[string]any{"series": "a"}})
	}
	for scrape, counts := range []string{"[100,100,100]", "[101,101,101]", "[102,102,102]"} {
		n := fixtureStart.Add(time.Duration(scrape) * time.Minute).UnixNano()
		metrics = append(metrics, telemetry.Metric{Namespace: "shop", ServiceName: "checkout", Name: "latency", Type: "histogram", Unit: "ms", TimeUnixNanos: n, EventUnixNanos: n, IngestedAt: n, HistBoundsJSON: "[10,20]", HistCountsJSON: counts, Attributes: map[string]any{"series": "b"}})
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "scrapes", Metrics: metrics}); err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Hist", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "h", Title: "H", Viz: "histogram", Unit: "ms", Query: &Query{From: "metrics", Where: []string{"name = 'latency'"}, Measures: []string{"count()"}, Histogram: &Histogram{Field: "value", Buckets: "explicit"}}}}}
	for _, temporality := range []string{"", "cumulative", "delta"} {
		d.Panels[0].Query.Histogram.Temporality = temporality
		got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
		if err != nil || got[0].Status != "ok" {
			t.Fatalf("hist: %+v %v", got, err)
		}
		want := []float64{7, 9, 12}
		if temporality == "delta" {
			want = []float64{340, 373, 407}
		}
		f := got[0].Frame
		if f.Rows != 3 {
			t.Fatalf("rows=%d", f.Rows)
		}
		for i, n := range want {
			if f.Values[2][i] != n {
				t.Fatalf("%q bucket %d=%v want %v", temporality, i, f.Values[2][i], n)
			}
		}
	}
	// A resetting series contributes max(last-first,0), not unsigned underflow.
	d.Panels[0].Query.Histogram.Temporality = ""
	n := fixtureStart.UnixNano()
	reset := []telemetry.Metric{{ServiceName: "reset", Name: "reset", Type: "histogram", EventUnixNanos: n, IngestedAt: n, HistBoundsJSON: "[10]", HistCountsJSON: "[20,30]"}, {ServiceName: "reset", Name: "reset", Type: "histogram", EventUnixNanos: n + int64(time.Minute), IngestedAt: n + 1, HistBoundsJSON: "[10]", HistCountsJSON: "[1,2]"}}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "reset", Metrics: reset}); err != nil {
		t.Fatal(err)
	}
	d.Panels[0].Query.Where = []string{"name = 'reset'"}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != "empty" {
		t.Fatalf("reset: %+v %v", got, err)
	}
}
func TestM2DistributionBudget(t *testing.T) {
	engine, repo := newTestEngine(t)
	spans := []telemetry.Span{}
	for m := range 1440 {
		for b := range 32 {
			n := fixtureStart.Add(time.Duration(m) * time.Minute).UnixNano()
			spans = append(spans, telemetry.Span{TraceID: "t", SpanID: "s", ServiceName: "checkout", Kind: "SPAN_KIND_SERVER", StartUnixNanos: n, EndUnixNanos: n + 1000, DurationMS: float64(uint64(1) << b), IngestedAt: n})
		}
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(24 * time.Hour) }
	d := Dashboard{Name: "Heat", Time: Time{Range: "24h"}, Panels: []Panel{{ID: "heat", Title: "Heat", Viz: "heatmap", Query: &Query{From: "spans", Measures: []string{"count()"}, Bucket: "1m", Histogram: &Histogram{Field: "duration_ms", Buckets: "log2"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	f := got[0].Frame
	if f == nil || !f.Truncated || f.Rows*len(f.Columns) > analysisCellLimit {
		t.Fatalf("unbounded heat: %+v", got)
	}
	// Auto must obey the same transfer and time-point budgets.
	d.Panels[0].Query.Bucket = "auto"
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != "ok" || got[0].Frame == nil {
		t.Fatalf("auto heat: %+v %v", got, err)
	}
	f = got[0].Frame
	if f.Rows*len(f.Columns) > analysisCellLimit {
		t.Fatalf("auto heat has %d cells", f.Rows*len(f.Columns))
	}
	times := map[any]bool{}
	for _, at := range f.Values[0] {
		times[at] = true
	}
	if len(times) > maxSeriesPoints || got[0].Interval == "" {
		t.Fatalf("auto heat time budget: %+v", got[0])
	}
	d.Panels[0].Query.Bucket = "1m"
	original := d.Panels[0]
	d.Panels = nil
	for i := 0; i < 11; i++ {
		copy := original
		copy.ID = fmt.Sprintf("bounded_%d", i)
		d.Panels = append(d.Panels, copy)
	}
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	cells := 0
	for _, r := range got {
		if r.Frame != nil {
			cells += r.Frame.Rows * len(r.Frame.Columns)
		}
	}
	if cells > 200000 {
		t.Fatalf("batch has %d cells", cells)
	}

}
func TestM2DistributionValidation(t *testing.T) {
	cases := []struct {
		p    Panel
		want Problem
	}{
		{Panel{ID: "h", Title: "H", Viz: "heatmap", Query: &Query{From: "spans", Measures: []string{"count()"}}}, Problem{Path: "panels[0].query.histogram", Message: "distribution panels require a histogram", Hint: "spans: duration_ms/log2; metrics: value/explicit"}},
		{Panel{ID: "h", Title: "H", Viz: "histogram", Query: &Query{From: "spans", Measures: []string{"p95(duration_ms)"}, Histogram: &Histogram{Field: "duration_ms", Buckets: "log2"}}}, Problem{Path: "panels[0].query.measures", Message: "distribution counts require count()", Hint: "use count() as the observation weight"}},
		{Panel{ID: "h", Title: "H", Viz: "heatmap", Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Bucket: "auto", Histogram: &Histogram{Field: "duration_ms", Buckets: "log2"}}}, Problem{Path: "panels[0].query.by", Message: "heatmap panels group by at most 0 field(s)", Hint: ""}},
		{Panel{ID: "h", Title: "H", Viz: "histogram", Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service", "name"}, Histogram: &Histogram{Field: "duration_ms", Buckets: "log2"}}}, Problem{Path: "panels[0].query.by", Message: "histogram panels group by at most 1 field(s)", Hint: ""}},
		{Panel{ID: "h", Title: "H", Viz: "histogram", Query: &Query{From: "spans", Measures: []string{"count()"}, Histogram: &Histogram{Field: "duration_ms", Buckets: "explicit"}}}, Problem{Path: "panels[0].query.histogram", Message: "use spans duration_ms/log2 or metrics value/explicit", Hint: ""}},
		{Panel{ID: "h", Title: "H", Viz: "histogram", Query: &Query{From: "spans", Measures: []string{"count()"}, Histogram: &Histogram{Field: "value", Buckets: "log2"}}}, Problem{Path: "panels[0].query.histogram", Message: "use spans duration_ms/log2 or metrics value/explicit", Hint: ""}},
		{Panel{ID: "h", Title: "H", Viz: "heatmap", SQL: "SELECT count(*) FROM spans"}, Problem{Path: "panels[0].query", Message: "distribution panels require a structured query", Hint: ""}},
		{Panel{ID: "h", Title: "H", Viz: "histogram", SQL: "SELECT count(*) FROM spans"}, Problem{Path: "panels[0].query", Message: "distribution panels require a structured query", Hint: ""}},
	}
	for _, tc := range cases {
		d := Dashboard{Name: "Rule", Panels: []Panel{tc.p}}
		Normalize(&d)
		got := Validate(&d)
		if !slices.Contains(got, tc.want) {
			t.Fatalf("got %+v want %+v", got, tc.want)
		}
	}
}

func TestM2DistributionMalformedHistogram(t *testing.T) {
	for _, tc := range []struct{ name, bounds, counts string }{
		{"invalid_bounds", "not json", "[100,200]"},
		{"invalid_counts", "[10]", "not json"},
		{"null_bounds", "null", "[100,200]"},
		{"null_counts", "[10]", "null"},
		{"mismatched_lengths", "[10,20]", "[100,200]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, repo := newTestEngine(t)
			at := fixtureStart.UnixNano()
			base := telemetry.Metric{ServiceName: "checkout", Name: "latency", Type: "histogram", EventUnixNanos: at, IngestedAt: at}
			bad, good := base, base
			bad.HistBoundsJSON, bad.HistCountsJSON = tc.bounds, tc.counts
			good.HistBoundsJSON, good.HistCountsJSON = "[10]", "[2,3]"
			if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "malformed", Metrics: []telemetry.Metric{bad, good}}); err != nil {
				t.Fatal(err)
			}
			e := NewExecutor(engine, 30)
			e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
			d := Dashboard{Name: "Hist", Panels: []Panel{{ID: "h", Title: "H", Viz: "histogram", Query: &Query{From: "metrics", Measures: []string{"count()"}, Histogram: &Histogram{Field: "value", Buckets: "explicit", Temporality: "delta"}}}}}
			got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil || got[0].Status != "ok" || got[0].Frame == nil {
				t.Fatalf("malformed histogram: %+v %v", got, err)
			}
			f := got[0].Frame
			if f.Rows != 2 || !slices.Equal(f.Values[2], []any{float64(2), float64(3)}) {
				t.Fatalf("bad row contributed: %+v", f)
			}
		})
	}
}

func TestM2DistributionSpanBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		durations            []float64
		lower, upper, counts []any
	}{
		{"unusual", []float64{-5, math.NaN(), math.Inf(1), 8}, []any{float64(0), float64(8), float64(1 << 32)}, []any{float64(1), float64(16), nil}, []any{float64(1), float64(1), float64(1)}},
		{"boundaries", []float64{0, (1 << 32) - 1, 1 << 32}, []any{float64(0), float64(1 << 31), float64(1 << 32)}, []any{float64(1), float64(1 << 32), nil}, []any{float64(1), float64(1), float64(1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, repo := newTestEngine(t)
			var spans []telemetry.Span
			for i, duration := range tc.durations {
				at := fixtureStart.Add(time.Duration(i) * time.Second).UnixNano()
				spans = append(spans, telemetry.Span{TraceID: fmt.Sprint(i), SpanID: "s", ServiceName: "checkout", StartUnixNanos: at, EndUnixNanos: at + 1000, IngestedAt: at, DurationMS: duration})
			}
			commit(t, repo, spans, nil)
			e := NewExecutor(engine, 30)
			e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
			d := Dashboard{Name: "Hist", Panels: []Panel{{ID: "h", Title: "H", Viz: "histogram", Query: &Query{From: "spans", Measures: []string{"count()"}, Histogram: &Histogram{Field: "duration_ms", Buckets: "log2"}}}}}
			got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil || got[0].Status != "ok" || got[0].Frame == nil {
				t.Fatalf("span histogram: %+v %v", got, err)
			}
			f := got[0].Frame
			if f.Rows != len(tc.counts) || !slices.Equal(f.Values[0], tc.lower) || !slices.Equal(f.Values[1], tc.upper) || !slices.Equal(f.Values[2], tc.counts) {
				t.Fatalf("buckets: %+v", f)
			}
			if f.Columns[0].Unit != "ms" || f.Columns[1].Unit != "ms" || f.Columns[2].Unit != "count" {
				t.Fatalf("units: %+v", f.Columns)
			}
		})
	}
}

func TestM2DistributionSpanOther(t *testing.T) {
	engine, repo := newTestEngine(t)
	var spans []telemetry.Span
	for i, service := range []string{"a", "a", "a", "b", "b", "c"} {
		at := fixtureStart.Add(time.Duration(i) * time.Second).UnixNano()
		spans = append(spans, telemetry.Span{TraceID: fmt.Sprint(i), SpanID: "s", ServiceName: service, StartUnixNanos: at, EndUnixNanos: at + 1000, IngestedAt: at, DurationMS: 8})
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Hist", Panels: []Panel{{ID: "h", Title: "H", Viz: "histogram", Options: &Options{Top: 1}, Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Histogram: &Histogram{Field: "duration_ms", Buckets: "log2"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != "ok" || got[0].Frame == nil {
		t.Fatalf("split histogram: %+v %v", got, err)
	}
	f := got[0].Frame
	if f.Rows != 2 || f.Columns[0].Name != "service" || !slices.Equal(f.Values[0], []any{"Other (2)", "a"}) || !slices.Equal(f.Values[3], []any{float64(3), float64(3)}) {
		t.Fatalf("folded histogram: %+v", f)
	}
}

func TestM2DistributionSchemaDescriptions(t *testing.T) {
	for _, tc := range []struct{ field, phrase string }{
		{"Field", "NaN is excluded"},
		{"Temporality", "last minus first"},
		{"Temporality", "counter resets mid-window contributes 0"},
	} {
		field, ok := reflect.TypeFor[Histogram]().FieldByName(tc.field)
		if !ok || !strings.Contains(field.Tag.Get("jsonschema"), tc.phrase) {
			t.Errorf("%s description must include %q", tc.field, tc.phrase)
		}
	}
}

func TestM2MetricHistogramHeatmap(t *testing.T) {
	engine, repo := newTestEngine(t)
	var metrics []telemetry.Metric
	for i, counts := range []string{"[10,20]", "[12,24]", "[15,30]", "[18,36]"} {
		minute := []int{0, 1, 5, 6}[i]
		n := fixtureStart.Add(time.Duration(minute) * time.Minute).UnixNano()
		metrics = append(metrics, telemetry.Metric{ServiceName: "checkout", Name: "latency", Type: "histogram", EventUnixNanos: n, IngestedAt: n, HistBoundsJSON: "[10]", HistCountsJSON: counts})
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "heat-scrapes", Metrics: metrics}); err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Heat", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "h", Title: "H", Viz: "heatmap", Unit: "ms", Query: &Query{From: "metrics", Measures: []string{"count()"}, Bucket: "5m", Histogram: &Histogram{Field: "value", Buckets: "explicit"}}}}}
	for _, temporality := range []string{"", "delta"} {
		d.Panels[0].Query.Histogram.Temporality = temporality
		got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
		if err != nil || got[0].Status != "ok" {
			t.Fatalf("heat: %+v %v", got, err)
		}
		f := got[0].Frame
		want := []any{float64(2), float64(4), float64(3), float64(6)}
		if temporality == "delta" {
			want = []any{float64(22), float64(44), float64(33), float64(66)}
		}
		if f.Rows != 4 || !slices.Equal(f.Values[3], want) || f.Values[0][0] != fixtureStart.UnixMilli() || f.Values[0][2] != fixtureStart.Add(5*time.Minute).UnixMilli() {
			t.Fatalf("%q frame: %+v", temporality, f)
		}
		if f.Columns[1].Unit != "ms" || f.Columns[3].Unit != "count" || f.Values[1][0] != nil || f.Values[2][1] != nil {
			t.Fatalf("bounds/units: %+v", f)
		}
	}
	// A transfer cap must retain the newest complete bucket, dropping the
	// partially transferred older bucket before rendering.
	d.Panels[0].Query.Limit = 3
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != "ok" {
		t.Fatalf("limited heat: %+v %v", got, err)
	}
	f := got[0].Frame
	if !f.Truncated || f.Rows != 2 || f.Values[0][0] != fixtureStart.Add(5*time.Minute).UnixMilli() {
		t.Fatalf("clipped heat: %+v", f)
	}
}

func TestM2MetricHistogramSeriesIdentity(t *testing.T) {
	engine, repo := newTestEngine(t)
	base := telemetry.Metric{Namespace: "shop", ServiceName: "checkout", Name: "latency", Type: "histogram", Unit: "ms", HistBoundsJSON: "[10]"}
	series := []telemetry.Metric{base, base, base, base, base, base, base, base}
	series[1].Namespace = "other"
	series[2].ServiceName = "other"
	series[3].Name = "other"
	series[4].Unit = "s"
	series[5].ScopeName = "other"
	series[6].Attributes = map[string]any{"identity": int64(1)}
	series[7].Resource = map[string]any{"identity": true}
	var metrics []telemetry.Metric
	for _, metric := range series {
		for scrape, counts := range []string{"[10,20]", "[11,22]"} {
			m := metric
			n := fixtureStart.Add(time.Duration(scrape) * time.Minute).UnixNano()
			m.EventUnixNanos, m.IngestedAt, m.HistCountsJSON = n, n, counts
			metrics = append(metrics, m)
		}
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "identity", Metrics: metrics}); err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Hist", Panels: []Panel{{ID: "h", Title: "H", Viz: "histogram", Query: &Query{From: "metrics", Measures: []string{"count()"}, Histogram: &Histogram{Field: "value", Buckets: "explicit"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != "ok" {
		t.Fatalf("identity: %+v %v", got, err)
	}
	f := got[0].Frame
	if f.Rows != 2 || !slices.Equal(f.Values[2], []any{float64(8), float64(16)}) {
		t.Fatalf("series merged: %+v", f)
	}
}

func TestM2DistributionTemporalityValidation(t *testing.T) {
	for _, tc := range []struct{ from, field, buckets, temporality string }{
		{"spans", "duration_ms", "log2", "delta"},
		{"spans", "duration_ms", "log2", "cumulative"},
		{"metrics", "value", "explicit", "unknown"},
	} {
		d := Dashboard{Name: "Rule", Panels: []Panel{{ID: "h", Title: "H", Viz: "histogram", Query: &Query{From: tc.from, Measures: []string{"count()"}, Histogram: &Histogram{Field: tc.field, Buckets: tc.buckets, Temporality: tc.temporality}}}}}
		Normalize(&d)
		want := Problem{Path: "panels[0].query.histogram.temporality", Message: "temporality applies to metric histograms and must be cumulative or delta", Hint: "omit for the OTel cumulative default"}
		if got := Validate(&d); !slices.Contains(got, want) {
			t.Fatalf("got %+v want %+v", got, want)
		}
	}
}

func TestM2DistributionTimePointBound(t *testing.T) {
	f := newFrame([]Column{{Name: "time", Type: "time", Role: "time"}, {Name: "count", Type: "number", Role: "measure"}})
	for i := maxSeriesPoints; i >= 0; i-- {
		for b := range 2 {
			f.Values[0] = append(f.Values[0], int64(i))
			f.Values[1] = append(f.Values[1], float64(2*i+b))
			f.Rows++
		}
	}
	boundAnalysisFrame(f)
	if !f.Truncated || f.Rows != 2*maxSeriesPoints || f.Values[0][0] != int64(1) || f.Values[0][f.Rows-1] != int64(maxSeriesPoints) {
		t.Fatalf("time cap: rows=%d truncated=%v", f.Rows, f.Truncated)
	}
	for i, count := range f.Values[1] {
		if count != float64(i+2) {
			t.Fatalf("column alignment at %d: %v", i, count)
		}
	}
}
