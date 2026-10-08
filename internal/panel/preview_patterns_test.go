package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
)

type previewPatternCounter struct {
	Engine
	queries int
}

func (e *previewPatternCounter) QueryContext(ctx context.Context, text string, args ...any) (queryrows.Rows, error) {
	if strings.Contains(text, "patterns AS (") {
		e.queries++
	}
	return e.Engine.QueryContext(ctx, text, args...)
}

func TestPreviewPatternContextEngine(t *testing.T) {
	engine, repo := newTestEngine(t)
	logs := []telemetry.Log{}
	add := func(pattern, severity, service string, n int) {
		for range n {
			at := fixtureStart.Add(time.Duration(len(logs)) * time.Second).UnixNano()
			logs = append(logs, telemetry.Log{Namespace: "shop", Body: pattern, BodyTemplate: pattern, Severity: severity, ServiceName: service, EventUnixNanos: at, TimeUnixNanos: at, IngestedAt: at})
		}
	}
	add("token=secret failed <*> <num>", "WARN", "checkout", 3)
	add("token=other failed <*> <num>", "ERROR", "payments", 2)
	add("token=other failed <*> <num>", "INFO", "payments", 2)
	add("tie <*> ", "WARN", "zeta", 2)
	add("tie <*> ", "ERROR", "alpha", 2)
	add("fatal <*> ", "ERROR", "payments", 1)
	add("fatal <*> ", "fatal", "checkout", 1)
	add("numbered <*> ", "INFO", "zeta", 1)
	logs[len(logs)-1].SeverityNumber = 9
	add("numbered <*> ", "INFO2", "alpha", 1)
	logs[len(logs)-1].SeverityNumber = 10
	// This out-of-window context must not influence the dominant values.
	at := fixtureStart.Add(-time.Second).UnixNano()
	logs = append(logs, telemetry.Log{Body: "token=secret failed <*> <num>", BodyTemplate: "token=secret failed <*> <num>", Severity: "FATAL", ServiceName: "outside", EventUnixNanos: at, TimeUnixNanos: at, IngestedAt: at})
	commit(t, repo, nil, logs)
	counter := &previewPatternCounter{Engine: engine}
	e := NewExecutor(counter, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Context", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "Patterns", Viz: "log_patterns", Query: &Query{From: "logs", By: []string{"body_template"}, Measures: []string{"count()"}, Bucket: "auto"}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if counter.queries != 1 {
		t.Fatalf("context queries=%d", counter.queries)
	}
	f := got[0].Frame
	if got[0].Status != StatusOK || f == nil {
		t.Fatalf("result=%+v", got)
	}
	cols := map[string]int{}
	for i, c := range f.Columns {
		cols[c.Name] = i
	}
	for _, name := range []string{"severity", "service"} {
		if _, ok := cols[name]; !ok {
			t.Fatalf("missing %s: %+v", name, f.Columns)
		}
	}
	want := map[string]struct {
		severity, service string
		count             float64
	}{"token=[REDACTED] failed <*> <num>": {"WARN", "payments", 7}, "tie <*> ": {"ERROR", "alpha", 4}, "fatal <*> ": {"FATAL", "checkout", 2}, "numbered <*> ": {"INFO2", "alpha", 2}}
	for r := 0; r < f.Rows; r++ {
		pattern := f.Values[cols["body_template"]][r].(string)
		w, ok := want[pattern]
		if !ok {
			t.Fatalf("unredacted/unexpected %q", pattern)
		}
		if f.Values[cols["severity"]][r] != w.severity || f.Values[cols["service"]][r] != w.service || f.Values[cols["count"]][r] != w.count {
			t.Fatalf("context %q: %+v", pattern, f.Values)
		}
		var trend []float64
		if err := json.Unmarshal([]byte(f.Values[cols["trend"]][r].(string)), &trend); err != nil {
			t.Fatal(err)
		}
		var total float64
		for _, n := range trend {
			total += n
		}
		if total != w.count || len(trend) > 240 {
			t.Fatalf("trend=%v", trend)
		}
	}
	if f.Rows != len(want) {
		t.Fatalf("rows=%d", f.Rows)
	}
}

func TestFinal2PatternContextIsAggregated(t *testing.T) {
	p := &Panel{ID: "p", Viz: "log_patterns", Query: &Query{From: "logs", Measures: []string{"count()"}, By: []string{"body_template"}}}
	c, err := compileRows(p, nil, Scope{Start: compileStart, End: compileEnd, Interval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(c.SQL, "context AS (")
	if start < 0 {
		t.Fatalf("missing context aggregate: %s", c.SQL)
	}
	end := strings.Index(c.SQL[start:], "),\nseverity_counts")
	if end < 0 {
		t.Fatalf("missing context aggregate: %s", c.SQL)
	}
	contextSQL := c.SQL[start : start+end]
	if !strings.Contains(contextSQL, "GROUP BY") || !strings.Contains(contextSQL, "count(*)") {
		t.Fatalf("row-level materialized context: %s", contextSQL)
	}
}

func TestPreviewPatternContextBudgetsEngine(t *testing.T) {
	engine, repo := newTestEngine(t)
	logs := []telemetry.Log{}
	for i := 0; i < 60; i++ {
		at := fixtureStart.UnixNano()
		pattern := fmt.Sprintf("pattern %d <*> ", i)
		logs = append(logs, telemetry.Log{Namespace: "shop", Body: pattern, BodyTemplate: pattern, Severity: "INFO", ServiceName: "s", EventUnixNanos: at, TimeUnixNanos: at, IngestedAt: at})
	}
	commit(t, repo, nil, logs)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(3 * time.Hour) }
	d := Dashboard{Name: "Budgets", Time: Time{Range: "3h"}}
	for i := 0; i < 30; i++ {
		d.Panels = append(d.Panels, Panel{ID: fmt.Sprintf("p%d", i), Title: "Patterns", Viz: "log_patterns", Query: &Query{From: "logs", By: []string{"body_template"}, Measures: []string{"count()"}, Bucket: "10s", Limit: 80}})
	}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	cells := 0
	for _, r := range got {
		if r.Status != StatusOK || r.Frame == nil {
			t.Fatalf("result=%+v", r)
		}
		f := r.Frame
		if f.Rows > 50 || !f.Truncated || len(f.Columns) != 5 {
			t.Fatalf("budget: rows=%d columns=%d truncated=%v", f.Rows, len(f.Columns), f.Truncated)
		}
		cells += f.Rows * len(f.Columns)
		trendIndex := -1
		for i, c := range f.Columns {
			if c.Name == "trend" {
				trendIndex = i
			}
		}
		for _, value := range f.Values[trendIndex] {
			var trend []float64
			if err := json.Unmarshal([]byte(value.(string)), &trend); err != nil {
				t.Fatal(err)
			}
			if len(trend) > 240 {
				t.Fatal("unbounded trend")
			}
			cells += len(trend)
		}
	}
	if cells > 200000 {
		t.Fatalf("batch cells=%d", cells)
	}
}
