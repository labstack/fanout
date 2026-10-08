package panel

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

func TestCandidateBound(t *testing.T) {
	for _, tc := range []struct{ sort, order string }{{"", "max(duration_ms) DESC"}, {"+start", "min(start_time) ASC"}, {"errors", "max(duration_ms) DESC"}} {
		for _, limit := range []int{2, 1000, 2000} {
			p := Panel{Viz: "traces", Query: &Query{From: "spans", Sort: tc.sort, Limit: limit}}
			c, err := compileRows(&p, nil, Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			candidate, _, ok := strings.Cut(c.SQL, "), roots AS")
			want := fmt.Sprintf("ORDER BY %s,namespace,trace_id LIMIT %d", tc.order, min(limit, 1000)+1)
			if !ok || !strings.Contains(candidate, want) || strings.Contains(candidate, "OVER(") {
				t.Errorf("sort=%q: candidate must be bounded before windows: %s", tc.sort, c.SQL)
			}
			if tc.sort == "errors" && !strings.Contains(candidate, "status IN ('STATUS_CODE_ERROR','ERROR')") {
				t.Error("missing erroring filter")
			}
		}
	}
}

func TestProblems(t *testing.T) {
	base := func(viz string) Panel {
		p := Panel{ID: "p", Title: "Rows", Viz: viz, Query: &Query{From: "logs"}}
		if viz == "traces" {
			p.Query.From = "spans"
		}
		if viz == "log_patterns" {
			p.Query.Measures = []string{"count()"}
			p.Query.By = []string{"body_template"}
		}
		return p
	}
	for _, tc := range []struct {
		name, viz string
		edit      func(*Panel)
		want      Problems
	}{
		{"sql", "logs", func(p *Panel) { p.Query = nil; p.SQL = "SELECT * FROM logs" }, Problems{{Path: "panels[0].sql", Message: "row panels cannot use SQL", Hint: "use query {from: logs}"}}},
		{"trace sql", "traces", func(p *Panel) { p.Query = nil; p.SQL = "SELECT * FROM spans" }, Problems{{Path: "panels[0].sql", Message: "row panels cannot use SQL", Hint: "use query {from: spans}"}}},
		{"missing query", "logs", func(p *Panel) { p.Query = nil }, Problems{{Path: "panels[0].query", Message: "row panels require a structured query", Hint: "use query {from: logs}"}}},
		{"patterns sql", "log_patterns", func(p *Panel) { p.Query = nil; p.SQL = "SELECT * FROM logs" }, Problems{{Path: "panels[0].sql", Message: "row panels cannot use SQL", Hint: "use query {from: logs}"}}},
		{"patterns contract", "log_patterns", func(p *Panel) {
			p.Query.By = []string{"service"}
			p.Query.Measures = []string{"rate()"}
			p.Query.Sort = "+count"
		}, Problems{
			{Path: "panels[0].query.by", Message: "log_patterns groups by body_template", Hint: "use by [body_template]"},
			{Path: "panels[0].query.measures", Message: "log_patterns requires count()", Hint: "use measures [count()]"},
			{Path: "panels[0].query.sort", Message: "log_patterns sort is count", Hint: "use sort count or omit sort"},
		}},
		{"highlight scope", "traces", func(p *Panel) { p.Options = &Options{Highlight: "x"} }, Problems{{Path: "panels[0].options.highlight", Message: "highlight applies only to logs and log_patterns", Hint: "remove highlight or use a logs or log_patterns panel"}}},
		{"highlight size", "logs", func(p *Panel) { p.Options = &Options{Highlight: strings.Repeat("界", 201)} }, Problems{{Path: "panels[0].options.highlight", Message: "highlight is limited to 200 characters", Hint: "shorten highlight to at most 200 characters"}}},
		{"source", "logs", func(p *Panel) { p.Query.From = "spans" }, Problems{{Path: "panels[0].query.from", Message: "logs and log_patterns read logs", Hint: "use query {from: logs}"}}},
		{"trace source", "traces", func(p *Panel) { p.Query.From = "logs" }, Problems{{Path: "panels[0].query.from", Message: "traces reads spans", Hint: "use query {from: spans}"}}},
		{"log sort", "logs", func(p *Panel) { p.Query.Sort = "foo" }, Problems{{Path: "panels[0].query.sort", Message: "logs sort is time or +time", Hint: "use sort time, +time or omit sort"}}},
		{"trace sort", "traces", func(p *Panel) { p.Query.Sort = "foo" }, Problems{{Path: "panels[0].query.sort", Message: "traces sort is duration_ms, errors or +start", Hint: "use sort duration_ms, errors, +start or omit sort"}}},
		{"measures", "logs", func(p *Panel) { p.Query.Measures = []string{"count()"} }, Problems{{Path: "panels[0].query.measures", Message: "fixed row projections do not take aggregate measures", Hint: "remove measures; use log_patterns for counts or a table for aggregates"}}},
		{"by", "logs", func(p *Panel) { p.Query.By = []string{"service"} }, Problems{{Path: "panels[0].query.by", Message: "logs and traces panels do not group; use log_patterns or a table", Hint: "remove by or use log_patterns or a table"}}},
		{"bucket", "traces", func(p *Panel) { p.Query.Bucket = "auto" }, Problems{{Path: "panels[0].query.bucket", Message: "logs and traces panels do not take a bucket", Hint: "remove bucket"}}},
		{"style", "logs", func(p *Panel) { p.Options = &Options{Style: "line"} }, Problems{{Path: "panels[0].options.style", Message: "row panels cannot set chart styles", Hint: "remove options.style"}}},
		{"unit", "logs", func(p *Panel) { p.Unit = "ms" }, Problems{{Path: "panels[0].unit", Message: "row panels cannot set unit", Hint: "remove unit; row columns have fixed units"}}},
		{"thresholds", "traces", func(p *Panel) { p.Thresholds = []Threshold{{Value: 1, Status: "bad"}} }, Problems{{Path: "panels[0].thresholds", Message: "row panels cannot set thresholds", Hint: "remove thresholds"}}},
		{"top", "log_patterns", func(p *Panel) { p.Options = &Options{Top: 2} }, Problems{{Path: "panels[0].options.top", Message: "row panels cannot set options.top", Hint: "remove options.top; use query.limit"}}},
		{"scale", "logs", func(p *Panel) { p.Options = &Options{Scale: "log"} }, Problems{{Path: "panels[0].options.scale", Message: "row panels cannot set options.scale", Hint: "remove options.scale"}}},
		{"limit", "logs", func(p *Panel) { p.Query.Limit = 1001 }, Problems{{Path: "panels[0].query.limit", Message: "limit must be 0 to 1000", Hint: "use a limit from 1 to 1000 or omit it for the default"}}},
		{"histogram", "logs", func(p *Panel) { p.Query.Histogram = &Histogram{} }, Problems{{Path: "panels[0].query.histogram", Message: "histogram applies only to heatmap and histogram", Hint: "remove histogram or use a heatmap or histogram panel"}}},
		{"pattern bucket", "log_patterns", func(p *Panel) { p.Query.Bucket = "2s" }, Problems{{Path: "panels[0].query.bucket", Message: "bucket must be auto or a supported interval", Hint: "use auto, 10s, 30s, 1m, 5m, 10m, 15m, 30m, 1h, 3h, 6h, 12h or 1d"}}},
		{"empty filter", "logs", func(p *Panel) { p.Query.Where = []string{" "} }, Problems{{Path: "panels[0].query.where[0]", Message: "filters are 1 to 500 characters", Hint: "use a nonempty filter of at most 500 characters"}}},
		{"filter count", "logs", func(p *Panel) {
			for range 17 {
				p.Query.Where = append(p.Query.Where, "service = 's'")
			}
		}, Problems{{Path: "panels[0].query.where", Message: "at most 16 filters per query", Hint: "remove filters until at most 16 remain"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base(tc.viz)
			tc.edit(&p)
			d := Dashboard{Name: "Rows", Panels: []Panel{p}}
			Normalize(&d)
			if got := Validate(&d); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v; want %#v", got, tc.want)
			}
		})
	}
	// Every row type rejects every inapplicable option, independently.
	for _, viz := range []string{"logs", "traces", "log_patterns"} {
		p := base(viz)
		p.Unit = "count"
		p.Thresholds = []Threshold{{Value: 1, Status: "bad"}}
		p.Options = &Options{Top: 2, Scale: "linear"}
		d := Dashboard{Name: "Rows", Panels: []Panel{p}}
		Normalize(&d)
		got := Validate(&d)
		if len(got) != 4 {
			t.Fatalf("%s: %#v", viz, got)
		}
		for _, problem := range got {
			if problem.Hint == "" {
				t.Fatalf("missing hint: %#v", problem)
			}
		}
	}
	// Non-row panels receive the same specific highlight applicability rule.
	p := base("table")
	p.Query.Measures = []string{"count()"}
	p.Options = &Options{Highlight: "x"}
	d := Dashboard{Name: "Rows", Panels: []Panel{p}}
	Normalize(&d)
	want := Problems{{Path: "panels[0].options.highlight", Message: "highlight applies only to logs and log_patterns", Hint: "remove highlight or use a logs or log_patterns panel"}}
	if got := Validate(&d); !reflect.DeepEqual(got, want) {
		t.Fatalf("table highlight: %#v", got)
	}
}

func TestPatternRankingAndBound(t *testing.T) {
	engine, repo := newTestEngine(t)
	var logs []telemetry.Log
	for i, count := range []int{1, 3, 2} {
		for range count {
			n := fixtureStart.UnixNano()
			body := fmt.Sprintf("pattern-%d", i)
			logs = append(logs, telemetry.Log{Body: body, BodyTemplate: body, EventUnixNanos: n, TimeUnixNanos: n, IngestedAt: n})
		}
	}
	commit(t, repo, nil, logs)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	p := Panel{ID: "p", Title: "Patterns", Viz: "log_patterns", Query: &Query{From: "logs", Measures: []string{"count()"}, By: []string{"body_template"}, Bucket: "auto", Limit: 2}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: Dashboard{Name: "Patterns", Time: Time{Range: "1h"}, Panels: []Panel{p}}})
	if err != nil {
		t.Fatal(err)
	}
	f := got[0].Frame
	if got[0].Status != StatusOK || f.Rows != 2 || !f.Truncated || f.Values[0][0] != "pattern-1" || f.Values[0][1] != "pattern-2" || f.Values[1][0] != float64(3) || f.Values[1][1] != float64(2) {
		t.Fatalf("rank: %+v %+v", got, f)
	}
	c, err := compileRows(&p, nil, Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour), Interval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	buckets, _, _ := strings.Cut(strings.Split(c.SQL, "buckets AS (")[1], "),\ndense")
	if !strings.Contains(buckets, "SEMI JOIN patterns") {
		t.Fatalf("unbounded buckets: %s", buckets)
	}
	// With only two candidates (one row and its sentinel), ascending
	// candidate ranking would drop the most frequent pattern entirely.
	p.Query.Limit = 1
	got, err = e.Run(t.Context(), RunRequest{Dashboard: Dashboard{Name: "Patterns", Time: Time{Range: "1h"}, Panels: []Panel{p}}})
	if err != nil {
		t.Fatal(err)
	}
	f = got[0].Frame
	if got[0].Status != StatusOK || f.Rows != 1 || !f.Truncated || f.Values[0][0] != "pattern-1" || f.Values[1][0] != float64(3) {
		t.Fatalf("candidate rank: %+v", f)
	}
}

func TestRedactedExemplarHighlight(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	spans := []telemetry.Span{{Namespace: "shop", TraceID: "secret", SpanID: "root", ServiceName: "s", Name: "root", StartUnixNanos: n, EndUnixNanos: n + int64(time.Millisecond), DurationMS: 1, IngestedAt: n}}
	logs := []telemetry.Log{{Namespace: "shop", TraceID: "secret", Body: "token=s3cr3t", EventUnixNanos: n, TimeUnixNanos: n, IngestedAt: n}}
	commit(t, repo, spans, logs)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Logs", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "Logs", Viz: "logs", Query: &Query{From: "logs"}, Options: &Options{}}}}
	for _, tc := range []struct {
		highlight string
		want      int
	}{{"s3cr3t", 0}, {"[REDACTED]", 1}} {
		d.Panels[0].Options.Highlight = tc.highlight
		got, err := e.Exemplars(t.Context(), ExemplarRequest{Dashboard: d, PanelID: "p", From: fixtureStart, To: fixtureStart.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Traces) != tc.want || (tc.want > 0 && (got.Traces[0].TraceID != "secret" || got.Traces[0].Namespace != "shop")) {
			t.Fatalf("highlight=%q: %+v", tc.highlight, got)
		}
	}
}

func TestErroringRootRanking(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	var spans []telemetry.Span
	for i, duration := range []float64{10, 100, 1000} {
		root := telemetry.Span{Namespace: "shop", TraceID: fmt.Sprintf("t%d", i), SpanID: "root", ServiceName: "s", StartUnixNanos: n, EndUnixNanos: n + int64(time.Millisecond), DurationMS: duration, IngestedAt: n}
		spans = append(spans, root)
		if i < 2 {
			child := root
			child.SpanID = "child"
			child.ParentSpanID = "root"
			child.DurationMS = float64(200 - i*100)
			child.StatusCode = "ERROR"
			spans = append(spans, child)
		}
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Erroring", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "Traces", Viz: "traces", Query: &Query{From: "spans", Sort: "errors", Limit: 2}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	f := got[0].Frame
	if got[0].Status != StatusOK || f.Rows != 2 || f.Values[0][0] != "t1" || f.Values[4][0] != float64(100) || f.Values[0][1] != "t0" || f.Values[5][0] != "STATUS_CODE_ERROR" {
		t.Fatalf("error filter then root duration: %+v %+v", got, f)
	}
}

func TestTrendMetadata(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	commit(t, repo, nil, []telemetry.Log{{Body: "pattern", BodyTemplate: "pattern", EventUnixNanos: n, TimeUnixNanos: n, IngestedAt: n}})
	e := NewExecutor(engine, 30)
	for _, tc := range []struct {
		window       time.Duration
		bucket, want string
		step         time.Duration
	}{{30 * 24 * time.Hour, "auto", "6h", 6 * time.Hour}, {7 * 24 * time.Hour, "10s", "1h", time.Hour}} {
		start := fixtureStart.Add(-time.Hour)
		end := start.Add(tc.window)
		e.now = func() time.Time { return end }
		p := Panel{ID: "p", Title: "Patterns", Viz: "log_patterns", Query: &Query{From: "logs", Measures: []string{"count()"}, By: []string{"body_template"}, Bucket: tc.bucket}}
		got, err := e.Run(t.Context(), RunRequest{Dashboard: Dashboard{Name: "Trends", Time: Time{From: &start, To: &end}, Panels: []Panel{p}}, Widths: map[string]int{"p": 1600}})
		if err != nil {
			t.Fatal(err)
		}
		if got[0].Status != StatusOK || got[0].Interval != tc.want {
			t.Errorf("window=%v: %+v", tc.window, got[0])
		}
		c, err := compileRows(&p, nil, Scope{Start: start, End: end, Interval: bucketInterval(tc.bucket, tc.window, 1600)})
		if err != nil {
			t.Fatal(err)
		}
		wantStart := start.Truncate(tc.step).UTC()
		if c.TrendInterval != tc.step || !c.TrendStart.Equal(wantStart) {
			t.Errorf("compiler metadata: interval=%v start=%v", c.TrendInterval, c.TrendStart)
		}
		raw, _ := json.Marshal(got[0].Frame)
		var frame struct {
			Trend *struct {
				StartMS int64 `json:"start_ms"`
				StepMS  int64 `json:"step_ms"`
			} `json:"trend"`
		}
		if err := json.Unmarshal(raw, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Trend == nil || frame.Trend.StartMS != wantStart.UnixMilli() || frame.Trend.StepMS != tc.step.Milliseconds() {
			t.Errorf("frame metadata: %s", raw)
		}
		var trend []float64
		_ = json.Unmarshal([]byte(got[0].Frame.Values[2][0].(string)), &trend)
		var total float64
		for _, v := range trend {
			total += v
		}
		if len(trend) > 240 || total != 1 {
			t.Fatalf("trend=%v", trend)
		}
	}
}

func TestEmptyDiagnosisAndBodyCap(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	long := strings.Repeat("界", 1990) + " token=s3cr3t " + strings.Repeat("尾", 30)
	// The JSON password straddles the 2,000-character cut. Its redaction
	// needs the closing quote, so cutting before redacting would leak a
	// prefix of the secret.
	straddle := strings.Repeat("a", 1975) + `{"password":"hunter2hunter2hunter2"} tail`
	logs := []telemetry.Log{{Body: long, EventUnixNanos: n, TimeUnixNanos: n, IngestedAt: n}, {Body: strings.Repeat("界", 2000), EventUnixNanos: n + 1, TimeUnixNanos: n + 1, IngestedAt: n}, {Body: straddle, EventUnixNanos: n + 2, TimeUnixNanos: n + 2, IngestedAt: n}}
	span := telemetry.Span{TraceID: "ok", SpanID: "root", StartUnixNanos: n, EndUnixNanos: n + int64(time.Millisecond), DurationMS: 1, IngestedAt: n}
	commit(t, repo, []telemetry.Span{span}, logs)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Rows", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "Logs", Viz: "logs", Query: &Query{From: "logs", Sort: "+time"}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat("界", 1990) + " token=[R…"
	if got[0].Status != StatusOK || got[0].Frame.Values[3][0] != want || got[0].Frame.Values[3][1] != strings.Repeat("界", 2000) {
		t.Errorf("body cap after redaction: got %d characters", len([]rune(got[0].Frame.Values[3][0].(string))))
	}
	if body, _ := got[0].Frame.Values[3][2].(string); strings.Contains(body, "hunter") {
		t.Errorf("secret straddling the cut leaked: %q", body[len(body)-40:])
	}
	d.Panels[0].Options = &Options{Highlight: "s3cr3t"}
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Status != StatusEmpty || got[0].Diagnosis != `No logs contain "s3cr3t" in this range` {
		t.Fatalf("highlight diagnosis: %+v", got)
	}
	d.Panels[0] = Panel{ID: "p", Title: "Traces", Viz: "traces", Query: &Query{From: "spans", Sort: "errors"}}
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Status != StatusEmpty || got[0].Diagnosis != "No erroring traces in this range" {
		t.Fatalf("erroring diagnosis: %+v", got)
	}
}

func TestRowsExecuteAndRedact(t *testing.T) {
	engine, repo := newTestEngine(t)
	at := fixtureStart.UnixNano()
	logs := []telemetry.Log{
		{Namespace: "shop", ServiceName: "checkout", TimeUnixNanos: at, EventUnixNanos: at, IngestedAt: at, Body: "payment token=hidden failed", BodyTemplate: "payment token=hidden failed", Severity: "ERROR", SeverityNumber: 17, TraceID: "t"},
		{Namespace: "shop", ServiceName: "checkout", TimeUnixNanos: at + int64(time.Minute), EventUnixNanos: at + int64(time.Minute), IngestedAt: at, Body: "payment token=hidden failed", BodyTemplate: "payment token=hidden failed", Severity: "ERROR", SeverityNumber: 17, TraceID: "t"},
	}
	commit(t, repo, shopSpans(), logs)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Rows", Time: Time{Range: "1h"}, Panels: []Panel{
		{ID: "logs", Title: "Errors", Viz: "logs", Options: &Options{Highlight: "failed"}, Query: &Query{From: "logs", Where: []string{"service = 'checkout'"}}},
		{ID: "patterns", Title: "Patterns", Viz: "log_patterns", Query: &Query{From: "logs", Measures: []string{"count()"}, By: []string{"body_template"}, Bucket: "auto"}},
		{ID: "traces", Title: "Slow traces", Viz: "traces", Query: &Query{From: "spans", Where: []string{"service = 'checkout'"}, Sort: "duration_ms", Limit: 20}},
	}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r.Status != "ok" {
			t.Fatalf("row panel: %+v", r)
		}
		raw, _ := json.Marshal(r.Frame)
		if strings.Contains(string(raw), "hidden") {
			t.Fatal("secret in rows")
		}
	}
	if got[0].Frame.Rows != 2 || got[1].Frame.Rows != 1 || got[1].Frame.Values[1][0].(float64) != 2 || got[2].Frame.Rows != 20 {
		t.Fatalf("frames: %+v", got)
	}
	var trend []float64
	if err := json.Unmarshal([]byte(got[1].Frame.Values[2][0].(string)), &trend); err != nil || len(trend) > 240 {
		t.Fatalf("trend %v %v", trend, err)
	}
	var total float64
	for _, n := range trend {
		total += n
	}
	if total != 2 {
		t.Fatalf("trend count %v", total)
	}
	d.Panels = d.Panels[:1]
	d.Panels[0].Options.Highlight = "hidden"
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != "empty" {
		t.Fatalf("unredacted search: %+v %v", got, err)
	}
}
func TestRowsCap(t *testing.T) {
	engine, repo := newTestEngine(t)
	logs := make([]telemetry.Log, 1002)
	for i := range logs {
		n := fixtureStart.Add(time.Duration(i) * time.Millisecond).UnixNano()
		logs[i] = telemetry.Log{ServiceName: "s", TimeUnixNanos: n, EventUnixNanos: n, IngestedAt: n, Body: fmt.Sprint(i)}
	}
	commit(t, repo, nil, logs)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Rows", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "logs", Title: "Logs", Viz: "logs", Query: &Query{From: "logs"}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Frame.Rows != 1000 || !got[0].Frame.Truncated {
		t.Fatalf("cap: %+v", got)
	}
	d.Panels[0].Query.Where = []string{"body IN (SELECT body FROM logs)"}
	if _, err := e.Run(t.Context(), RunRequest{Dashboard: d}); err == nil {
		t.Fatal("row guard bypass")
	}
}

func TestLogPatternsHonoursLimit50(t *testing.T) {
	engine, repo := newTestEngine(t)
	logs := []telemetry.Log{}
	for i := range 60 {
		n := fixtureStart.UnixNano()
		logs = append(logs, telemetry.Log{ServiceName: "s", Body: fmt.Sprintf("pattern-%d", i), BodyTemplate: fmt.Sprintf("pattern-%d", i), EventUnixNanos: n, IngestedAt: n})
	}
	commit(t, repo, nil, logs)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Patterns", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "P", Viz: "log_patterns", Query: &Query{From: "logs", Measures: []string{"count()"}, By: []string{"body_template"}, Bucket: "auto"}}}}
	for _, tc := range []struct{ limit, want int }{{0, 20}, {30, 30}, {50, 50}, {80, 50}} {
		d.Panels[0].Query.Limit = tc.limit
		got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
		if err != nil || got[0].Frame == nil || got[0].Frame.Rows != tc.want {
			t.Fatalf("limit=%d: %+v %v", tc.limit, got, err)
		}
	}
}

func TestRowsNanosecondOrderingAndPatternTail(t *testing.T) {
	engine, repo := newTestEngine(t)
	start := fixtureStart
	end := start.Add(30*time.Second + time.Nanosecond)
	logs := []telemetry.Log{}
	for _, tc := range []struct {
		offset time.Duration
		body   string
	}{
		{time.Nanosecond, "first"}, {2 * time.Nanosecond, "second"}, {30 * time.Second, "tail"},
	} {
		n := start.Add(tc.offset).UnixNano()
		logs = append(logs, telemetry.Log{Namespace: "shop", ServiceName: "s", Body: tc.body, BodyTemplate: "pattern", EventUnixNanos: n, TimeUnixNanos: n, IngestedAt: n})
	}
	commit(t, repo, nil, logs)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return end }
	d := Dashboard{Name: "Precise rows", Time: Time{From: &start, To: &end}, Panels: []Panel{
		{ID: "logs", Title: "Logs", Viz: "logs", Query: &Query{From: "logs", Limit: 2}},
		{ID: "patterns", Title: "Patterns", Viz: "log_patterns", Query: &Query{From: "logs", Measures: []string{"count()"}, By: []string{"body_template"}, Bucket: "30s"}},
	}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Status != StatusOK || got[0].Frame.Values[3][0] != "tail" || got[0].Frame.Values[3][1] != "second" || !got[0].Frame.Truncated {
		t.Fatalf("nanosecond sort: %+v", got[0])
	}
	var trend []float64
	if got[1].Status != StatusOK {
		t.Fatalf("patterns: %+v", got[1])
	}
	if err := json.Unmarshal([]byte(got[1].Frame.Values[2][0].(string)), &trend); err != nil {
		t.Fatal(err)
	}
	var total float64
	for _, n := range trend {
		total += n
	}
	if total != 3 {
		t.Fatalf("tail lost: count=%v trend=%v", got[1].Frame.Values[1][0], trend)
	}
}

func TestRowsTraceSortsAndSentinel(t *testing.T) {
	engine, repo := newTestEngine(t)
	spans := make([]telemetry.Span, 1002)
	for i := range spans {
		n := fixtureStart.Add(time.Duration(i) * time.Millisecond).UnixNano()
		spans[i] = telemetry.Span{Namespace: "shop", TraceID: fmt.Sprintf("t%04d", i), SpanID: "root", ServiceName: "s", Name: "root", StartUnixNanos: n, EndUnixNanos: n + int64(time.Millisecond), DurationMS: float64(i + 1), IngestedAt: n}
	}
	child := spans[0]
	child.ParentSpanID, child.SpanID, child.StatusCode, child.DurationMS = "root", "child", "ERROR", 5000
	other := spans[0]
	other.Namespace, other.DurationMS = "other", 2000
	spans = append(spans, child, other)
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Traces", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "traces", Title: "Traces", Viz: "traces", Query: &Query{From: "spans"}}}}
	for _, tc := range []struct {
		sort             string
		limit, rows      int
		first, namespace string
		truncated        bool
	}{
		{"", 0, 1000, "t0000", "other", true},
		{"+start", 2, 2, "t0000", "other", true},
		{"errors", 20, 1, "t0000", "shop", false},
	} {
		d.Panels[0].Query.Sort, d.Panels[0].Query.Limit = tc.sort, tc.limit
		got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
		if err != nil {
			t.Fatal(err)
		}
		f := got[0].Frame
		if got[0].Status != StatusOK || f.Rows != tc.rows || f.Truncated != tc.truncated || len(f.Columns) != 7 || f.Values[0][0] != tc.first || f.Values[1][0] != tc.namespace || f.Values[6][0] != fixtureStart.UnixMilli() {
			t.Fatalf("sort=%q: %+v frame=%+v", tc.sort, got[0], f)
		}
		if tc.sort == "errors" && (f.Values[5][0] != "STATUS_CODE_ERROR" || f.Values[4][0] != float64(1)) {
			t.Fatalf("child error/root metadata: %+v", f)
		}
	}
	d.Panels[0].Query.Where = []string{"service = 'absent'"}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != StatusEmpty || got[0].Frame.Rows != 0 {
		t.Fatalf("empty: %+v %v", got, err)
	}
}

func TestRowsValidation(t *testing.T) {
	for _, tc := range []struct {
		name, viz, from, sort, bucket, highlight string
		measures, by                             []string
		valid                                    bool
	}{
		{name: "logs", viz: "logs", from: "logs", sort: "+time", valid: true},
		{name: "traces", viz: "traces", from: "spans", sort: "+start", valid: true},
		{name: "patterns", viz: "log_patterns", from: "logs", sort: "count", measures: []string{"count()"}, by: []string{"body_template"}, valid: true},
		{name: "unicode highlight", viz: "logs", from: "logs", highlight: strings.Repeat("界", 200), valid: true},
		{name: "long highlight", viz: "logs", from: "logs", highlight: strings.Repeat("界", 201)},
		{name: "trace highlight", viz: "traces", from: "spans", highlight: "error"},
		{name: "nonlog highlight", viz: "table", from: "logs", measures: []string{"count()"}, highlight: "error"},
		{name: "log measure", viz: "logs", from: "logs", measures: []string{"count()"}},
		{name: "trace measure", viz: "traces", from: "spans", measures: []string{"count()"}},
		{name: "log grouping", viz: "logs", from: "logs", by: []string{"service"}},
		{name: "trace grouping", viz: "traces", from: "spans", by: []string{"service"}},
		{name: "log bucket", viz: "logs", from: "logs", bucket: "auto"},
		{name: "trace bucket", viz: "traces", from: "spans", bucket: "auto"},
		{name: "log source", viz: "logs", from: "spans"},
		{name: "trace source", viz: "traces", from: "logs"},
		{name: "log sql sort", viz: "logs", from: "logs", sort: "time DESC; SELECT 1"},
		{name: "trace sort", viz: "traces", from: "spans", sort: "start"},
		{name: "pattern grouping", viz: "log_patterns", from: "logs", measures: []string{"count()"}, by: []string{"service"}},
		{name: "pattern measure", viz: "log_patterns", from: "logs", measures: []string{"rate()"}, by: []string{"body_template"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Dashboard{Name: "Rows", Panels: []Panel{{ID: "p", Title: "Rows", Viz: tc.viz, Options: &Options{Highlight: tc.highlight}, Query: &Query{From: tc.from, Sort: tc.sort, Bucket: tc.bucket, Measures: tc.measures, By: tc.by}}}}
			Normalize(&d)
			if problems := Validate(&d); (len(problems) == 0) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, problems)
			}
		})
	}
	for _, viz := range []string{"logs", "log_patterns", "traces"} {
		d := Dashboard{Name: "SQL rows", Panels: []Panel{{ID: "p", Title: "Rows", Viz: viz, SQL: "SELECT * FROM logs"}}}
		Normalize(&d)
		if len(Validate(&d)) == 0 {
			t.Fatalf("%s accepted SQL", viz)
		}
	}
}
