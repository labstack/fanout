package panel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRound2SeriesKeepsNewestBucket(t *testing.T) {
	duck, repo := newTestEngine(t)
	spans := shopSpans()[:0]
	base := shopSpans()[0]
	for bucket := range 120 {
		for service := range 9 {
			sp := base
			sp.SpanID = fmt.Sprintf("b%d_s%d", bucket, service)
			sp.TraceID = sp.SpanID
			sp.ServiceName = fmt.Sprintf("svc%d", service)
			sp.StartUnixNanos = fixtureStart.Add(time.Duration(bucket) * 30 * time.Second).UnixNano()
			spans = append(spans, sp)
		}
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(duck, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Series", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "series", Title: "Series", Viz: "timeseries", Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Bucket: "30s"}, Options: &Options{Top: 6}}}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	f := results[0].Frame
	if f == nil {
		t.Fatalf("missing frame: %+v", results[0])
	}
	if f.Rows != 840 || f.Truncated {
		t.Fatalf("rows=%d truncated=%v", f.Rows, f.Truncated)
	}
	if got := f.Values[0][f.Rows-1]; got != fixtureStart.Add(time.Hour-30*time.Second).UnixMilli() {
		t.Fatalf("last bucket=%v", got)
	}
}

func TestRound2GaugeTotalsMatchStat(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	p := d.Panels[1]
	g := p
	g.ID, g.Viz = "gauge", "gauge"
	lo, hi := 0.0, 1000.0
	g.Min, g.Max = &lo, &hi
	d.Panels = []Panel{p, g}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(results[0].Frame.Totals, results[1].Frame.Totals) || len(results[1].Frame.Totals) == 0 {
		t.Fatalf("totals: stat=%v gauge=%v", results[0].Frame.Totals, results[1].Frame.Totals)
	}
	if results[1].Previous != nil {
		t.Fatal("gauge queried previous period")
	}
}

func TestRound2BetterOnlyInResult(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = d.Panels[:1]
	Normalize(&d)
	if d.Panels[0].Better != "" {
		t.Fatal("Normalize persisted an inferred direction")
	}
	for _, explicit := range []string{"", "higher"} {
		d.Panels[0].Better = explicit
		results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(results[0])
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]any
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if explicit == "" && wire["better"] != "lower" {
			t.Fatalf("inferred result=%s", raw)
		}
		if explicit != "" && wire["better"] != nil {
			t.Fatalf("explicit direction repeated: %s", raw)
		}
	}
}

func TestRound2ErrorCountDirections(t *testing.T) {
	for signal, filters := range map[string][]string{
		"logs":  {"severity = 'ERROR'", "severity IN ('ERROR', 'FATAL')", "upper(severity) = 'ERROR'", "upper(severity) IN ('ERROR', 'FATAL')", "severity_number >= 17"},
		"spans": {"status = 'ERROR'", "status = 'STATUS_CODE_ERROR'"},
	} {
		for _, filter := range filters {
			p := Panel{Query: &Query{From: signal, Measures: []string{"count()"}, Where: []string{filter}}}
			if got := inferBetter(&p); got != "lower" {
				t.Errorf("%s: %s direction=%q", signal, filter, got)
			}
		}
	}
}

func TestRound2SafeErrorWarn(t *testing.T) {
	var output bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(old)
	if got := SafeError(errors.New("bad filter /srv/data/file.parquet")); !strings.Contains(got, "<path>") {
		t.Fatal(got)
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["level"] != "WARN" {
		t.Fatalf("level=%v", record["level"])
	}
}

func TestRound2SingleEmptySelectionFallsBack(t *testing.T) {
	for _, defaultValue := range []string{"frontend", ""} {
		e := newFixtureExecutor(t)
		d := shopDashboard()
		d.Variables[0].Default = defaultValue
		given := map[string]Value{"service": {Values: []string{}}}
		Normalize(&d)
		checked, err := e.check(t.Context(), &d)
		if err != nil {
			t.Fatal(err)
		}
		values, err := e.values(t.Context(), &d, checked, fixtureStart, fixtureStart.Add(time.Hour), given)
		if err != nil {
			t.Fatal(err)
		}
		want := defaultValue
		if want == "" {
			want = "checkout"
		}
		if !reflect.DeepEqual(values["service"].Values, []string{want}) {
			t.Fatalf("default=%q value=%+v", defaultValue, values["service"])
		}
	}
}

func TestRound2BudgetKeepsCompleteNewestBuckets(t *testing.T) {
	// Two series per bucket, with a budget leaving space for an odd row count:
	// the oldest partial bucket must be dropped as a unit.
	f := newFrame([]Column{{Name: "time", Type: "time", Role: "time"}, {Name: "service", Type: "string", Role: "dimension"}, {Name: "count", Type: "number", Role: "measure"}})
	f.bucketed = true
	for i := range 70000 {
		f.Values[0] = append(f.Values[0], int64(i/2))
		f.Values[1] = append(f.Values[1], fmt.Sprintf("svc%d", i%2))
		f.Values[2] = append(f.Values[2], float64(i))
	}
	f.Rows = 70000
	f.Totals = []any{nil, nil, 70000.0, nil}
	previous := newFrame(f.Columns)
	previous.bucketed = true
	previous.Rows = 2
	previous.Values = [][]any{{int64(-1), int64(-1)}, {"a", "b"}, {1.0, 1.0}}
	results := []Result{{Frame: f, Previous: previous}}
	limitBatchFrames(results)
	if !f.Truncated || f.Rows != 66664 || f.Values[0][0] != int64(1668) || f.Values[0][f.Rows-1] != int64(34999) {
		t.Fatalf("rows=%d first=%v last=%v truncated=%v", f.Rows, f.Values[0][0], f.Values[0][f.Rows-1], f.Truncated)
	}
	if f.Rows*len(f.Columns)+len(f.Totals)+previous.Rows*len(previous.Columns) > 200000 {
		t.Fatal("budget exceeded")
	}
	if !previous.Truncated || previous.Rows != 0 {
		t.Fatalf("previous budget=%+v", previous)
	}
}
