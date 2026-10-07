package panel

import (
	"fmt"
	"testing"
	"time"
)

func TestSeriesTopDefaultAndClamp(t *testing.T) {
	for _, top := range []int{0, 6, 8, 20} {
		p := Panel{Options: &Options{Top: top}}
		if got := p.Top(); got != 6 {
			t.Errorf("top=%d: got %d, want 6", top, got)
		}
	}
	if got := (&Panel{}).Top(); got != 6 {
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
		if name == "Other" && f.Values[2][row] == float64(1) && f.Rows == 7 {
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
	d := Dashboard{Name: "Bars", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "s", Title: "S", Viz: "bar", Query: &Query{From: "spans", Measures: []string{"error_rate()"}, By: []string{"http_route", "service"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Frame == nil {
		t.Fatalf("run: %+v %v", got, err)
	}
	f := got[0].Frame
	for row, name := range f.Values[1] {
		if name == "Other" && f.Rows == 7 {
			if value := f.Values[2][row].(float64); value < 33.33 || value > 33.34 {
				t.Fatalf("Other error rate=%v, want weighted 100/3", value)
			}
			return
		}
	}
	t.Fatalf("missing grouped-bar Other: %+v", f)
}
