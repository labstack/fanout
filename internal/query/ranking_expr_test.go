package query

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

func TestRankingFixtures(t *testing.T) {
	duck, repo := newTestEngine(t)
	var spans []telemetry.Span
	add := func(service string, n, errors, servers int, latency float64) {
		for i := range n {
			status := "STATUS_CODE_OK"
			if i < errors {
				status = "STATUS_CODE_ERROR"
			}
			kind := "SPAN_KIND_CLIENT"
			if i < servers {
				kind = "SPAN_KIND_SERVER"
			}
			id := fmt.Sprintf("%s-%d", service, i)
			attrs := map[string]any{}
			if service != "B" || i < 1 {
				attrs["user.score"] = latency
			}
			spans = append(spans, telemetry.Span{Namespace: "test", ServiceName: service, TraceID: id, SpanID: id, StatusCode: status, Kind: kind, DurationMS: latency, StartUnixNanos: fixtureStart.UnixNano(), EndUnixNanos: fixtureStart.UnixNano() + int64(latency*1e6), Attributes: attrs})
		}
	}
	add("A", 1, 1, 1, 10000)
	add("B", 1000, 100, 900, 100)
	add("C", 1000, 10, 100, 50)
	add("D", 40, 0, 0, 20)
	add("E", 40, 0, 0, 10)
	add("F", 40, 0, 0, 10)
	add("G", 2, 1, 0, 5000)
	commit(t, repo, spans, nil)
	e := NewExecutor(duck, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	run := func(text, better string, top int) Result {
		t.Helper()
		d := exprDashboard(text)
		p := &d.Panels[0]
		p.Viz = "timeseries"
		p.Better = better
		p.Query.By = []string{"service"}
		p.Query.Bucket = "1m"
		p.Options = &Options{Top: top}
		r, err := e.Run(t.Context(), RunRequest{Dashboard: d})
		if err != nil || len(r) != 1 || r[0].Status != StatusOK {
			t.Fatalf("%s: %+v %v", text, r, err)
		}
		return r[0]
	}
	names := func(r Result) []string {
		out := []string{}
		for _, v := range r.Frame.Values[1] {
			if !strings.HasPrefix(v.(string), "Other (") {
				out = append(out, v.(string))
			}
		}
		return out
	}
	baseline := names(run("error_rate()", "", 3))
	for _, text := range []string{"error_rate() + 0 as e", "error_rate() * 100 as e", "100 - error_rate() as e", "-error_rate() as e"} {
		r := run(text, "", 3)
		if !reflect.DeepEqual(names(r), baseline) || r.Ranking == nil || r.Ranking.Rule != "inherit" {
			t.Fatalf("%s: %v baseline %v ranking %+v", text, names(r), baseline, r.Ranking)
		}
	}
	if run("error_rate()", "", 3).Ranking.Rule != "builtin" {
		t.Fatal("builtin metadata")
	}
	sparse := run("count_if(status = 'STATUS_CODE_ERROR') / count() as r", "", 1)
	if !reflect.DeepEqual(names(sparse), []string{"B"}) {
		t.Fatalf("sparse ratio displaced supported B: %v", names(sparse))
	}
	r := run("count_if(status = 'STATUS_CODE_ERROR') / count() as r", "", 6)
	if r.Ranking == nil || r.Ranking.Rule != "wilson" || r.Ranking.Better != "lower" || r.Ranking.Floor != 20 || r.Ranking.BelowFloor != 2 || !strings.Contains(r.Frame.Note, "2 series had fewer than 20 samples and were ranked last") || !reflect.DeepEqual(r.Ranking.TieBreakers, []string{"n desc", "dimension asc"}) {
		t.Fatalf("%+v %+v", r.Ranking, r.Frame)
	}
	if !reflect.DeepEqual(names(run("100 - count_if(status = 'STATUS_CODE_ERROR') / count() * 100 as ok", "higher", 3)), baseline) {
		t.Fatal("success ratio changed ranking")
	}
	standalone := run("count_if(status = 'STATUS_CODE_ERROR') as errors", "", 2)
	if standalone.Ranking.Rule != "value_floor" || !reflect.DeepEqual(names(standalone), []string{"B", "C"}) {
		t.Fatalf("standalone conditional: %+v %v", standalone.Ranking, names(standalone))
	}
	neutral := run("count_if(kind = 'SPAN_KIND_SERVER') / count() as r", "", 2)
	if neutral.Ranking.Better != "neutral" || !reflect.DeepEqual(names(neutral), []string{"B", "C"}) {
		t.Fatalf("%+v %v", neutral.Ranking, names(neutral))
	}
	for range 3 {
		if !reflect.DeepEqual(names(run("count_if(kind = 'SPAN_KIND_SERVER') / count() as r", "", 4)), []string{"B", "C", "D", "E"}) {
			t.Fatal("non-deterministic tie")
		}
	}
	numeric := run("avg(attributes['user.score']) / 2 as half", "", 2)
	if numeric.Ranking.Rule != "inherit" || numeric.Ranking.BelowFloor != 3 || !strings.Contains(numeric.Ranking.SupportColumn, "count(TRY_CAST(") || reflect.DeepEqual(names(numeric), []string{"A", "B"}) {
		t.Fatalf("%+v", numeric.Ranking)
	}
	generic := run("avg(attributes['user.score']) / (count()+1) as half", "", 2)
	if generic.Ranking.Rule != "value_floor" || !strings.Contains(generic.Ranking.SupportColumn, "count(TRY_CAST(") {
		t.Fatalf("%+v", generic.Ranking)
	}
	if !reflect.DeepEqual(names(run("-p95(duration_ms) as neg", "higher", 3)), names(run("p95(duration_ms)", "lower", 3))) {
		t.Fatal("negative latency ranking")
	}
}
func TestApdexFixtures(t *testing.T) {
	duck, repo := newTestEngine(t)
	var spans []telemetry.Span
	for _, ms := range []int{100, 500, 2000, 5000} {
		for i := range 20 {
			id := fmt.Sprintf("%d-%d", ms, i)
			spans = append(spans, telemetry.Span{Namespace: "test", ServiceName: fmt.Sprint(ms), TraceID: id, SpanID: id, DurationMS: float64(ms), StartUnixNanos: fixtureStart.UnixNano(), EndUnixNanos: fixtureStart.UnixNano() + int64(ms)*1e6})
		}
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(duck, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	for _, tc := range []struct {
		service string
		want    any
	}{{"100", 1.0}, {"500", 1.0}, {"2000", .5}, {"5000", 0.0}, {"absent", nil}} {
		d := exprDashboard("(count_if(duration_ms <= 500) + count_if(duration_ms > 500 AND duration_ms <= 2000) / 2) / count() as apdex")
		p := &d.Panels[0]
		p.Viz = "stat"
		p.Reduce = "window"
		p.Query.Where = []string{"service = '" + tc.service + "'"}
		r, err := e.Run(t.Context(), RunRequest{Dashboard: d})
		if err != nil || len(r) != 1 || r[0].Status == StatusError || r[0].Frame.Totals[1] != tc.want || (tc.want == nil && r[0].Status != StatusEmpty) {
			t.Fatalf("%s: %+v %v", tc.service, r, err)
		}
	}
}

func TestRankingExpressionUsesWindowSeconds(t *testing.T) {
	duck, repo := newTestEngine(t)
	var spans []telemetry.Span
	for _, tc := range []struct {
		service   string
		n, errors int
	}{{"A", 1000, 0}, {"B", 100, 10}} {
		for i := range tc.n {
			status := "STATUS_CODE_OK"
			if i < tc.errors {
				status = "STATUS_CODE_ERROR"
			}
			id := fmt.Sprintf("%s-%d", tc.service, i)
			spans = append(spans, telemetry.Span{Namespace: "test", ServiceName: tc.service, TraceID: id, SpanID: id, StatusCode: status, StartUnixNanos: fixtureStart.UnixNano(), EndUnixNanos: fixtureStart.UnixNano() + 1e6, DurationMS: 1})
		}
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(duck, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := exprDashboard("rate() + count_if(status = 'STATUS_CODE_ERROR') as score")
	p := &d.Panels[0]
	p.Viz = "timeseries"
	p.Query.By = []string{"service"}
	p.Query.Bucket = "1m"
	p.Options = &Options{Top: 1}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || len(got) != 1 || got[0].Status != StatusOK {
		t.Fatalf("%+v %v", got, err)
	}
	names := got[0].Frame.Values[1]
	if !reflect.DeepEqual(names, []any{"B", "Other (1)"}) {
		t.Fatalf("ranked %v: expected B's 10+100/3600 to outrank A's 1000/3600", names)
	}
}

func TestRankingBuiltinSumKeepsRowSupport(t *testing.T) {
	duck, repo := newTestEngine(t)
	var spans []telemetry.Span
	for _, tc := range []struct {
		service       string
		rows, present int
		value         float64
	}{{"A", 50, 1, 100}, {"B", 20, 20, 5}, {"C", 1, 1, 1000}} {
		for i := range tc.rows {
			id := fmt.Sprintf("%s-%d", tc.service, i)
			attrs := map[string]any{}
			if i < tc.present {
				attrs["score"] = tc.value
			}
			spans = append(spans, telemetry.Span{Namespace: "test", ServiceName: tc.service, TraceID: id, SpanID: id, StartUnixNanos: fixtureStart.UnixNano(), EndUnixNanos: fixtureStart.UnixNano() + 1e6, DurationMS: 1, Attributes: attrs})
		}
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(duck, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	run := func(measure, where string) Result {
		t.Helper()
		d := exprDashboard(measure)
		p := &d.Panels[0]
		p.Viz = "timeseries"
		p.Query.By = []string{"service"}
		p.Query.Bucket = "1m"
		p.Options = &Options{Top: 1}
		if where != "" {
			p.Query.Where = []string{where}
		}
		got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
		if err != nil || len(got) != 1 || got[0].Status != StatusOK {
			t.Fatalf("%+v %v", got, err)
		}
		return got[0]
	}
	for _, measure := range []string{"sum(attributes['score'])", "sum(attributes['score']) * 2 as s"} {
		r := run(measure, "service != 'C'")
		if r.Ranking.SupportColumn != "count(*)" || !reflect.DeepEqual(r.Frame.Values[1], []any{"A", "Other (1)"}) {
			t.Fatalf("sum row support changed: %+v %+v", r.Ranking, r.Frame)
		}
	}
	r := run("sum(attributes['score'])", "")
	if r.Ranking.BelowFloor != 1 || !reflect.DeepEqual(r.Frame.Values[1], []any{"C", "Other (2)"}) || strings.Contains(r.Frame.Note, "ranked last") {
		t.Fatalf("no-floor sum disclosure: %+v %+v", r.Ranking, r.Frame)
	}
	generic := run("sum(attributes['score']) + count_if(status = 'STATUS_CODE_ERROR') as s", "service != 'C'")
	if generic.Ranking.Rule != "value_floor" || !strings.Contains(generic.Ranking.SupportColumn, "count(TRY_CAST(") || !reflect.DeepEqual(generic.Frame.Values[1], []any{"B", "Other (1)"}) {
		t.Fatalf("generic support: %+v %+v", generic.Ranking, generic.Frame)
	}
}

func TestRankingWilsonHigherUsesUpperBound(t *testing.T) {
	duck, repo := newTestEngine(t)
	var spans []telemetry.Span
	for _, tc := range []struct {
		service       string
		rows, servers int
	}{{"A", 20, 0}, {"B", 1000, 10}, {"C", 1000, 900}} {
		for i := range tc.rows {
			id := fmt.Sprintf("%s-%d", tc.service, i)
			kind := "SPAN_KIND_CLIENT"
			if i < tc.servers {
				kind = "SPAN_KIND_SERVER"
			}
			spans = append(spans, telemetry.Span{Namespace: "test", ServiceName: tc.service, TraceID: id, SpanID: id, Kind: kind, StartUnixNanos: fixtureStart.UnixNano(), EndUnixNanos: fixtureStart.UnixNano() + 1e6, DurationMS: 1})
		}
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(duck, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := exprDashboard("count_if(kind = 'SPAN_KIND_SERVER') / count() as r")
	p := &d.Panels[0]
	p.Viz = "timeseries"
	p.Better = "higher"
	p.Query.By = []string{"service"}
	p.Query.Bucket = "1m"
	p.Options = &Options{Top: 1}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || len(got) != 1 || got[0].Status != StatusOK {
		t.Fatalf("%+v %v", got, err)
	}
	r := got[0]
	if !reflect.DeepEqual(r.Frame.Values[1], []any{"B", "Other (2)"}) || r.Ranking.Rule != "wilson" || r.Ranking.Better != "higher" || !strings.Contains(r.SQL, "least(1.0,") || !strings.Contains(r.SQL, "confidence END ASC NULLS LAST") {
		t.Fatalf("upper-bound ranking: %+v %+v %s", r.Ranking, r.Frame, r.SQL)
	}
}
