package panel

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestSeriesTopDefaultAndClamp(t *testing.T) {
	for _, top := range []int{0, 6, 8, 20} {
		p := Panel{Viz: "timeseries", Options: &Options{Top: top}}
		if got := p.Top(); got != 6 {
			t.Errorf("top=%d: got %d, want 6", top, got)
		}
	}
	if got := (&Panel{Viz: "timeseries"}).Top(); got != 6 {
		t.Errorf("omitted options: got %d, want 6", got)
	}
}

func TestSeriesTopEightValidationProblem(t *testing.T) {
	d := Dashboard{Name: "Series", Panels: []Panel{{ID: "s", Title: "S", Viz: "timeseries", Options: &Options{Top: 8}, Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}}}}}
	Normalize(&d)
	for _, problem := range Validate(&d) {
		if problem.Path == "panels[0].options.top" && problem.Hint != "" {
			return
		}
	}
	t.Fatal("top: 8 must produce an options.top Problem with a hint")
}

func TestSeriesSeventhFoldsIntoOther(t *testing.T) {
	duck, repo := newTestEngine(t)
	spans := shopSpans()[:0]
	base := shopSpans()[0]
	for service := range 7 {
		for event := range 7 - service {
			sp := base
			sp.SpanID = fmt.Sprintf("s%d_e%d", service, event)
			sp.TraceID = sp.SpanID
			sp.ServiceName = fmt.Sprintf("svc%d", service)
			sp.StartUnixNanos = fixtureStart.UnixNano()
			spans = append(spans, sp)
		}
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(duck, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Series", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "s", Title: "S", Viz: "timeseries", Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Bucket: "1m"}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Frame == nil {
		t.Fatalf("run: %+v %v", got, err)
	}
	f := got[0].Frame
	for row, name := range f.Values[1] {
		if name == "Other (1)" && f.Values[2][row] == float64(1) && f.Rows == 7 {
			return
		}
	}
	t.Fatalf("seventh service was not folded into Other: %+v", f)
}

func TestGroupedBarOtherAggregatesBeforeErrorRate(t *testing.T) {
	duck, repo := newTestEngine(t)
	spans := shopSpans()[:0]
	base := shopSpans()[0]
	for service := range 8 {
		for event := range 8 - service {
			sp := base
			sp.SpanID = fmt.Sprintf("s%d_e%d", service, event)
			sp.TraceID = sp.SpanID
			sp.ServiceName = fmt.Sprintf("svc%d", service)
			sp.HTTPRoute = "/cart"
			sp.StatusCode = "STATUS_CODE_OK"
			if service == 7 {
				sp.StatusCode = "STATUS_CODE_ERROR"
			}
			spans = append(spans, sp)
		}
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(duck, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Bars", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "s", Title: "S", Viz: "bar", Better: "higher", Query: &Query{From: "spans", Measures: []string{"error_rate()"}, By: []string{"http_route", "service"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Frame == nil {
		t.Fatalf("run: %+v %v", got, err)
	}
	f := got[0].Frame
	for row, name := range f.Values[1] {
		if name == "Other (2)" && f.Rows == 7 {
			if value := f.Values[2][row].(float64); value < 33.33 || value > 33.34 {
				t.Fatalf("Other error rate=%v, want weighted 100/3", value)
			}
			return
		}
	}
	t.Fatalf("missing grouped-bar Other: %+v", f)
}

func TestM3TimelineTwelveRows(t *testing.T) {
	duck, repo := newTestEngine(t)
	spans := shopSpans()[:0]
	for i := range 12 {
		sp := shopSpans()[0]
		sp.SpanID = fmt.Sprint(i)
		sp.TraceID = sp.SpanID
		sp.ServiceName = fmt.Sprintf("svc%02d", i)
		spans = append(spans, sp)
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(duck, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Rows", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "states", Title: "States", Viz: "state_timeline", Options: &Options{Top: 12}, Thresholds: []Threshold{{Value: 5, Status: "bad"}}, Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Bucket: "1m"}}}}
	Normalize(&d)
	if problems := Validate(&d); len(problems) != 0 {
		t.Fatalf("timeline rejected: %+v", problems)
	}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Frame == nil || got[0].Frame.Rows != 12 {
		t.Fatalf("12 rows: %+v %v", got, err)
	}
	if (&Panel{Viz: "state_timeline"}).Top() != 8 || (&Panel{Viz: "state_timeline", Options: &Options{Top: 30}}).Top() != 20 {
		t.Fatal("timeline defaults and cap")
	}
	d.Panels[0].Viz = "timeseries"
	if problems := Validate(&d); len(problems) == 0 {
		t.Fatal("categorical top 12 accepted")
	}
}

func TestI4WorstFirstSeries(t *testing.T) {
	duck, repo := newTestEngine(t)
	spans := shopSpans()[:0]
	for service := range 6 {
		volume := 100
		if service == 5 {
			volume = 25
		}
		for event := range volume {
			sp := shopSpans()[0]
			sp.SpanID = fmt.Sprintf("s%d_e%d", service, event)
			sp.TraceID = sp.SpanID
			sp.ServiceName = fmt.Sprintf("svc%d", service)
			sp.StatusCode = "STATUS_CODE_OK"
			if event == 0 || service == 5 && event < 10 {
				sp.StatusCode = "STATUS_CODE_ERROR"
			}
			sp.DurationMS = float64(10 + service)
			if service == 0 {
				sp.DurationMS = 2000
			}
			if service == 5 {
				sp.DurationMS = 1000
			}
			sp.EndUnixNanos = sp.StartUnixNanos + int64(sp.DurationMS*1e6)
			spans = append(spans, sp)
		}
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(duck, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	for _, tc := range []struct {
		measure, better string
		kept            []string
	}{
		{"error_rate()", "", []string{"svc0", "svc1", "svc5"}},
		{"count()", "", []string{"svc0", "svc1", "svc2"}},
		{"p95(duration_ms)", "", []string{"svc0", "svc4", "svc5"}},
		{"avg(duration_ms)", "higher", []string{"svc1", "svc2", "svc3"}},
	} {
		t.Run(tc.measure+tc.better, func(t *testing.T) {
			d := Dashboard{Name: "Ranking", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "s", Title: "S", Viz: "timeseries", Better: tc.better, Options: &Options{Top: 3}, Query: &Query{From: "spans", Measures: []string{tc.measure}, By: []string{"service"}, Bucket: "1m"}}}}
			got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil || got[0].Frame == nil {
				t.Fatalf("run: %+v %v", got, err)
			}
			f := got[0].Frame
			var kept []string
			folded := false
			for row, name := range f.Values[1] {
				if name == "Other (3)" {
					folded = true
					continue
				}
				kept = append(kept, name.(string))
				if tc.measure == "error_rate()" && name == "svc5" && f.Values[2][row] != float64(40) {
					t.Fatalf("display error rate %v", f.Values[2][row])
				}
			}
			slices.Sort(kept)
			if !slices.Equal(kept, tc.kept) || !folded || f.Rows != 4 {
				t.Fatalf("kept=%v want=%v fold=%v frame=%+v", kept, tc.kept, folded, f)
			}
		})
	}
}

func TestM3NonCategoricalLimits(t *testing.T) {
	for _, viz := range []string{"bar", "table", "state_timeline", "scatter", "heatmap"} {
		p := Panel{Viz: viz}
		if p.Top() != 8 {
			t.Errorf("%s default %d, want 8", viz, p.Top())
		}
		p.Options = &Options{Top: 12}
		if p.Top() != 12 {
			t.Errorf("%s top12 clamped", viz)
		}
	}
}
