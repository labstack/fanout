package panel

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
)

type confidenceCounter struct {
	Engine
	queries int
}

func (e *confidenceCounter) QueryContext(ctx context.Context, text string, args ...any) (queryrows.Rows, error) {
	if strings.Contains(text, "candidates AS (") {
		e.queries++
	}
	return e.Engine.QueryContext(ctx, text, args...)
}

func TestConfidenceRankingEngine(t *testing.T) {
	duck, repo := newTestEngine(t)
	spans := make([]telemetry.Span, 0, 262000)
	add := func(route, service string, n, errors int, latency float64) {
		for i := range n {
			status := "STATUS_CODE_OK"
			if i < errors {
				status = "STATUS_CODE_ERROR"
			}
			id := fmt.Sprintf("%s-%s-%d", route, service, i)
			spans = append(spans, telemetry.Span{Namespace: "shop", ServiceName: service, TraceID: id, SpanID: id, HTTPRoute: route, Kind: "SPAN_KIND_SERVER", StatusCode: status, StartUnixNanos: fixtureStart.UnixNano(), EndUnixNanos: fixtureStart.UnixNano() + int64(latency*1e6), DurationMS: latency})
		}
	}
	add("/rates", "A", 2, 1, 1)
	add("/rates", "B", 100000, 10000, 1)
	add("/rates", "C", 1000, 400, 1)
	for _, service := range []string{"D", "E", "F"} {
		add("/rates", service, 50000, 500, 1)
	}
	add("/low", "low", 500, 200, 1)
	add("/wilson", "small", 20, 8, 1)
	add("/wilson", "busy", 10000, 3000, 1)
	add("/latency", "outlier", 3, 0, 10000)
	add("/latency", "nineteen", 19, 0, 5000)
	add("/latency", "eligible-a", 20, 0, 100)
	add("/latency", "eligible-b", 25, 0, 100)
	add("/latency", "eligible-c", 21, 0, 80)
	add("/latency", "eligible-d", 20, 0, 50)
	// Twenty rows with only three actual numeric samples remain ineligible.
	add("/nullable", "outlier", 20, 0, 10000)
	for i := len(spans) - 20; i < len(spans)-17; i++ {
		spans[i].Attributes = map[string]any{"latency": 10000.0}
	}
	add("/nullable", "eligible", 20, 0, 100)
	for i := len(spans) - 20; i < len(spans); i++ {
		spans[i].Attributes = map[string]any{"latency": 100.0}
	}
	commit(t, repo, spans, nil)
	counter := &confidenceCounter{Engine: duck}
	e := NewExecutor(counter, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	cases := []struct {
		name, measure, where, better string
		top                          int
		kept                         []string
	}{
		{"tiny rate folded", "error_rate()", "http_route = '/rates'", "", 3, []string{"B", "C", "D"}},
		{"low volume real failure", "error_rate()", "http_route IN ('/low', '/rates') AND service NOT IN ('A','B','C')", "", 1, []string{"low"}},
		{"Wilson beats raw fraction", "error_rate()", "http_route = '/wilson'", "", 1, []string{"busy"}},
		{"volume unchanged", "count()", "http_route = '/rates'", "higher", 3, []string{"B", "D", "E"}},
		{"rate unchanged", "rate()", "http_route = '/rates'", "", 3, []string{"B", "D", "E"}},
		{"share unchanged", "share()", "http_route = '/rates'", "higher", 3, []string{"B", "D", "E"}},
		{"sum unchanged", "sum(duration_ms)", "http_route = '/rates'", "higher", 3, []string{"B", "D", "E"}},
		{"ineligible ordered by samples", "p95(duration_ms)", "http_route = '/latency' AND service IN ('nineteen','outlier')", "", 1, []string{"nineteen"}},
		{"nullable samples", "avg(attributes['latency'])", "http_route = '/nullable'", "", 1, []string{"eligible"}},
		{"higher better preserved", "avg(duration_ms)", "http_route = '/latency'", "higher", 1, []string{"eligible-d"}},
	}
	for _, measure := range []string{"p50(duration_ms)", "p75(duration_ms)", "p90(duration_ms)", "p95(duration_ms)", "p99(duration_ms)", "quantile(duration_ms, 0.999)", "avg(duration_ms)", "max(duration_ms)", "min(duration_ms)"} {
		cases = append(cases, struct {
			name, measure, where, better string
			top                          int
			kept                         []string
		}{measure, measure, "http_route = '/latency'", "", 3, []string{"eligible-a", "eligible-b", "eligible-c"}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := counter.queries
			d := Dashboard{Name: "Confidence", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "s", Title: "S", Viz: "timeseries", Better: tc.better, Options: &Options{Top: tc.top}, Query: &Query{From: "spans", Measures: []string{tc.measure}, By: []string{"service"}, Where: []string{tc.where}, Bucket: "1m"}}}}
			got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
			if err != nil || len(got) != 1 || got[0].Status != StatusOK || got[0].Frame == nil {
				t.Fatalf("run: %+v %v", got, err)
			}
			if counter.queries-before != 1 {
				t.Fatalf("ranking queries=%d, want one", counter.queries-before)
			}
			var kept []string
			folded := false
			for _, name := range got[0].Frame.Values[1] {
				if strings.HasPrefix(name.(string), "Other (") {
					folded = true
				} else {
					kept = append(kept, name.(string))
				}
			}
			if !reflect.DeepEqual(kept, tc.kept) || !folded {
				t.Fatalf("kept=%v want=%v folded=%v frame=%+v", kept, tc.kept, folded, got[0].Frame)
			}
			if tc.name == "tiny rate folded" {
				for row, name := range got[0].Frame.Values[1] {
					want, ok := map[string]float64{"B": 10, "C": 40, "D": 1}[name.(string)]
					if ok && got[0].Frame.Values[2][row] != want {
						t.Fatalf("display rate changed to confidence score: %+v", got[0].Frame)
					}
				}
			}
		})
	}
}

func TestConfidenceSchemaText(t *testing.T) {
	field, _ := reflect.TypeFor[Options]().FieldByName("Top")
	if !strings.Contains(field.Tag.Get("jsonschema"), "series are chosen worst-first by confidence (Wilson lower bound for error rates; at least 20 samples for latency)") {
		t.Fatal("missing confidence semantics in schema")
	}
}
