package panel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
)

func TestStructuredTableSparklineRejectsDimension(t *testing.T) {
	e := newFixtureExecutor(t)
	d := Dashboard{Name: "Invalid trend", Panels: []Panel{{ID: "p", Title: "P", Viz: "table", Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}}, Options: &Options{Columns: []ColumnFormat{{Field: "service", Format: "sparkline"}}}}}}
	Normalize(&d)
	_, problems, err := Check(t.Context(), e.engine, &d)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, problem := range problems {
		if problem.Path == "panels[0].options.columns" && problem.Message == "structured sparklines require a measure column" && problem.Hint == "choose a measure field; the executor attaches its trend" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing dimension trend validation: %+v", problems)
	}
}

func TestTableTrendsRedactedLogSource(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	commit(t, repo, nil, []telemetry.Log{{ServiceName: "checkout", BodyTemplate: "token=templatesecret failed <*>", EventUnixNanos: n, IngestedAt: n}})
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Log trends", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "P", Viz: "table", Query: &Query{From: "logs", By: []string{"body_template"}, Measures: []string{"count()"}}, Options: &Options{Columns: []ColumnFormat{{Field: "count", Format: "sparkline"}, {Field: "body_template", Format: "log_template"}}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || len(got) != 1 || got[0].Status != StatusOK {
		t.Fatalf("logs: %+v %v", got, err)
	}
	f := got[0].Frame
	if !strings.Contains(f.Values[0][0].(string), "[REDACTED]") {
		t.Fatalf("template not redacted: %v", f.Values)
	}
	var total float64
	for _, value := range f.Trends["count"][0] {
		if value != nil {
			total += value.(float64)
		}
	}
	if total != 1 {
		t.Fatalf("redacted grouping lost trend: %v", f.Trends)
	}
}

type failingTableTrendEngine struct{ Engine }

func (e failingTableTrendEngine) QueryContext(ctx context.Context, text string, args ...any) (queryrows.Rows, error) {
	if strings.HasPrefix(text, "SELECT epoch_ms(time_bucket(") {
		return nil, errors.New("trend unavailable")
	}
	return e.Engine.QueryContext(ctx, text, args...)
}
func TestTableTrendFailurePreservesTable(t *testing.T) {
	e := newFixtureExecutor(t)
	e.engine = failingTableTrendEngine{e.engine}
	d := shopDashboard()
	d.Panels = d.Panels[2:3]
	d.Panels[0].Viz = "table"
	d.Panels[0].Options = &Options{Columns: []ColumnFormat{{Field: "count", Format: "sparkline"}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != StatusOK || got[0].Frame.Rows != 2 || got[0].Frame.Note != "Table trends unavailable." {
		t.Fatalf("main table lost: %+v %v", got, err)
	}
}

func TestTableTrendsMultipleMeasuresDimensionsAndWindow(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = d.Panels[2:3]
	p := &d.Panels[0]
	p.Viz = "table"
	p.Query.By = []string{"service", "http_route"}
	p.Query.Measures = []string{"count()", "avg(duration_ms) as latency"}
	p.Options = &Options{Columns: []ColumnFormat{{Field: "count", Format: "sparkline"}, {Field: "latency", Format: "sparkline"}}}
	start, end := fixtureStart.Add(30*time.Second), fixtureStart.Add(31*time.Minute+30*time.Second)
	d.Time = Time{From: &start, To: &end}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != StatusOK {
		t.Fatalf("multi trend: %+v %v", got, err)
	}
	f := got[0].Frame
	for row := 0; row < f.Rows; row++ {
		counts, latency := f.Trends["count"][row], f.Trends["latency"][row]
		if len(counts) != 32 || len(latency) != 32 || counts[0] != nil {
			t.Fatalf("clipped series: %v %v", counts, latency)
		}
		var total float64
		for _, value := range counts {
			if value != nil {
				total += value.(float64)
			}
		}
		if total != f.Values[2][row].(float64) {
			t.Fatalf("row mismatch: %v %v", counts, f.Values[2][row])
		}
		if f.Values[1][row] == "/cart" && (latency[1] != float64(100) || latency[31] != float64(900)) {
			t.Fatalf("wrong measure: %v", latency)
		}
	}
}

func TestTableTrendsNanosecondEnd(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = d.Panels[2:3]
	d.Panels[0].Viz = "table"
	d.Panels[0].Options = &Options{Columns: []ColumnFormat{{Field: "count", Format: "sparkline"}}}
	start, end := fixtureStart, fixtureStart.Add(time.Minute+time.Nanosecond)
	d.Time = Time{From: &start, To: &end}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != StatusOK {
		t.Fatalf("ns end: %+v %v", got, err)
	}
	f := got[0].Frame
	for row, series := range f.Trends["count"] {
		if len(series) != 2 || series[0] != float64(1) || series[1] != float64(1) || f.Values[1][row] != float64(2) {
			t.Fatalf("ns-clipped series lost last point: %v", series)
		}
	}
}

func TestTableTrendsPerTableCellBudget(t *testing.T) {
	engine, repo := newTestEngine(t)
	spans := shopSpans()[:1]
	for i := 1; i < 200; i++ {
		span := spans[0]
		span.ServiceName = fmt.Sprintf("service-'%d", i)
		span.TraceID = fmt.Sprintf("trace-%d", i)
		spans = append(spans, span)
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Bounded trends", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "P", Viz: "table", Query: &Query{From: "spans", By: []string{"service"}, Measures: []string{"count()", "avg(duration_ms) as latency"}, Limit: 200}, Options: &Options{Columns: []ColumnFormat{{Field: "count", Format: "sparkline"}, {Field: "latency", Format: "sparkline"}}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != StatusOK {
		t.Fatalf("budget query: %+v %v", got, err)
	}
	f := got[0].Frame
	if f.Rows != 200 || !f.Truncated || f.Note != "Table trends are limited by the cell budget." {
		t.Fatalf("budget note/rows: %+v", f)
	}
	points := 0
	for name, series := range f.Trends {
		if len(series) != f.Rows {
			t.Fatalf("%s rows=%d", name, len(series))
		}
		for row, values := range series {
			points += len(values)
			if len(values) > 0 && (len(values) > 240 || values[0] != f.Values[map[string]int{"count": 1, "latency": 2}[name]][row]) {
				t.Fatalf("%s row %d not aligned: %v", name, row, values)
			}
		}
	}
	if points != 19980 || points > analysisCellLimit {
		t.Fatalf("points=%d", points)
	}
}

func TestTableTrendsBatchRowsHealthAndDeterminism(t *testing.T) {
	first := newFrame([]Column{{Name: "count", Type: "number", Role: "measure"}})
	first.bucketed = true
	for i := 0; i < 199995; i++ {
		appendPanelRow(first, float64(i))
	}
	last := newFrame([]Column{{Name: "count", Type: "number", Role: "measure"}})
	for i := 0; i < 10; i++ {
		appendPanelRow(last, float64(i))
	}
	last.Totals = []any{float64(10)}
	last.Trends = map[string][][]any{"count": make([][]any, 10)}
	for i := range last.Trends["count"] {
		last.Trends["count"][i] = []any{float64(i)}
	}
	results := []Result{{Frame: first}, {Frame: last}}
	limitBatchFrames(results)
	if last.Rows != 4 || len(last.Trends["count"]) != 4 || !last.Truncated {
		t.Fatalf("row alignment: %+v", last)
	}
	for _, series := range last.Trends["count"] {
		if len(series) != 0 {
			t.Fatalf("exceeded shared budget: %+v", last)
		}
	}
	first.Totals = []any{float64(1)}
	for i := 0; i < 10; i++ {
		f := newFrame([]Column{{Name: "n", Type: "number", Role: "measure"}})
		appendPanelRow(f, float64(1))
		f.Health = &HealthFrame{ErrorTrend: []float64{1, 2}}
		f.Trends = map[string][][]any{"z": {{1}}, "a": {{1}}}
		limitBatchFrames([]Result{{Frame: first}, {Frame: f}})
		if len(f.Health.ErrorTrend) != 2 || len(f.Trends["a"][0]) != 1 || f.Trends["z"][0] != nil {
			t.Fatalf("nondeterministic budget: %+v", f)
		}
	}
}

func TestStructuredTableSparklineMeasure(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = d.Panels[2:3]
	d.Panels[0].Viz = "table"
	d.Panels[0].Options = &Options{Columns: []ColumnFormat{{Field: "count", Format: "sparkline"}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != "ok" {
		t.Fatalf("table: %+v %v", got, err)
	}
	f := got[0].Frame
	series := f.Trends["count"]
	if len(series) != f.Rows {
		t.Fatalf("row trends: %+v", f)
	}
	points := 0
	for r, trend := range series {
		var total float64
		for _, v := range trend {
			points++
			if v != nil {
				total += v.(float64)
			}
		}
		if total != f.Values[1][r].(float64) {
			t.Fatalf("row %d trend=%v main=%v", r, total, f.Values[1][r])
		}
	}
	if points > analysisCellLimit {
		t.Fatalf("unbounded points=%d", points)
	}
	results := make([]Result, 20)
	for i := range results {
		f := newFrame([]Column{{Name: "service", Type: "string", Role: "dimension"}, {Name: "count", Type: "number", Role: "measure"}})
		f.Trends = map[string][][]any{"count": make([][]any, 100)}
		for row := 0; row < 100; row++ {
			appendPanelRow(f, fmt.Sprintf("service-%d", row), float64(200))
			f.Trends["count"][row] = make([]any, 200)
		}
		results[i] = Result{ID: fmt.Sprintf("table-%d", i), Status: StatusOK, Frame: f}
	}
	limitBatchFrames(results)
	cells := 0
	for _, r := range results {
		if r.Frame == nil {
			continue
		}
		cells += r.Frame.Rows * len(r.Frame.Columns)
		for _, rows := range r.Frame.Trends {
			for _, trend := range rows {
				cells += len(trend)
			}
		}
	}
	if cells > 200000 {
		t.Fatalf("batch cells=%d", cells)
	}
}

func TestLimitedShareTrendUsesOriginalScope(t *testing.T) {
	engine, repo := newTestEngine(t)
	spans := []telemetry.Span{}
	for minute := range 2 {
		for i := range 100 {
			at := fixtureStart.Add(time.Duration(minute) * time.Minute).UnixNano()
			service := "A"
			if i >= 90 {
				service = "B"
			}
			spans = append(spans, telemetry.Span{ServiceName: service, TraceID: fmt.Sprintf("%d-%d", minute, i), SpanID: fmt.Sprint(i), StartUnixNanos: at, EndUnixNanos: at + 1000000, IngestedAt: at})
		}
	}
	commit(t, repo, spans, nil)
	counter := &countingTableTrendEngine{Engine: engine}
	e := NewExecutor(counter, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Shares", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "P", Viz: "table", Query: &Query{From: "spans", By: []string{"service"}, Measures: []string{"share()"}, Limit: 1}, Options: &Options{Columns: []ColumnFormat{{Field: "share", Format: "sparkline"}}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != StatusOK {
		t.Fatalf("run: %+v %v", got, err)
	}
	if counter.queries != 1 {
		t.Fatalf("trend queries=%d", counter.queries)
	}
	f := got[0].Frame
	if f.Rows != 1 || f.Values[0][0] != "A" || f.Values[1][0] != float64(90) {
		t.Fatalf("main shares: %+v", f)
	}
	for _, v := range f.Trends["share"][0] {
		if v != nil && v != float64(90) {
			t.Fatalf("wrong denominator: %v", f.Trends)
		}
	}
	if f.Trends["share"][0][0] != float64(90) {
		t.Fatalf("missing share points: %+v", f)
	}
}
