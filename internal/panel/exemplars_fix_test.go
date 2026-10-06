package panel

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
)

type exemplarProbeEngine struct {
	Engine
	query func(context.Context, string, ...any) (queryrows.Rows, error)
}

func (e exemplarProbeEngine) QueryContext(ctx context.Context, text string, args ...any) (queryrows.Rows, error) {
	return e.query(ctx, text, args...)
}

func exemplarDashboard(from string) Dashboard {
	return Dashboard{Name: "Selection", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "P", Viz: "bar", Query: &Query{From: from, Measures: []string{"count()"}, By: []string{"service"}}}}}
}

func exemplarRequest(d Dashboard) ExemplarRequest {
	return ExemplarRequest{Dashboard: d, PanelID: "p", From: fixtureStart, To: fixtureStart.Add(time.Hour)}
}

func TestM2FixExemplarDeadlineAndCancel(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint(canceled), func(t *testing.T) {
			engine, _ := newTestEngine(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			probe := exemplarProbeEngine{Engine: engine, query: func(ctx context.Context, _ string, _ ...any) (queryrows.Rows, error) {
				if canceled {
					cancel()
				}
				<-ctx.Done()
				// Native engines may return an interrupt rather than ctx.Err().
				return nil, errors.New("engine interrupted")
			}}
			e := NewExecutor(probe, 30)
			e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
			e.timeout = 20 * time.Millisecond
			_, err := e.Exemplars(ctx, exemplarRequest(exemplarDashboard("spans")))
			want := context.DeadlineExceeded
			if canceled {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
		})
	}
}

func TestM2FixExemplarSafeErrors(t *testing.T) {
	engine, _ := newTestEngine(t)
	failure := fmt.Errorf("read '/private/telemetry/batch/logs.parquet': %w", query.ErrParquetReadWait)
	probe := exemplarProbeEngine{Engine: engine, query: func(context.Context, string, ...any) (queryrows.Rows, error) { return nil, failure }}
	e := NewExecutor(probe, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	_, err := e.Exemplars(t.Context(), exemplarRequest(exemplarDashboard("spans")))
	if !errors.Is(err, query.ErrParquetReadWait) || strings.Contains(err.Error(), "/private/telemetry") || !strings.Contains(err.Error(), "<path>") {
		t.Fatalf("lost safe error chain: %v", err)
	}
}

func TestM2FixExemplarDimensionBindingAndProjection(t *testing.T) {
	engine, _ := newTestEngine(t)
	value := "x'); DROP TABLE spans; --"
	called := false
	probe := exemplarProbeEngine{Engine: engine, query: func(ctx context.Context, text string, args ...any) (queryrows.Rows, error) {
		called = true
		if strings.Contains(text, value) || strings.Contains(text, sqlString(value)) || !slices.Contains(args, any(value)) {
			t.Fatalf("dimension not bound: %s %+v", text, args)
		}
		if strings.Contains(text, "s.*") {
			t.Fatal("roots reads unused columns")
		}
		node, err := engine.ParseSQL(ctx, text)
		if err != nil {
			t.Fatal(err)
		}
		ctes := node["cte_map"].(map[string]any)["map"].([]any)
		for _, entry := range ctes {
			entry := entry.(map[string]any)
			if entry["key"] == "roots" {
				root := entry["value"].(map[string]any)["query_node"].(map[string]any)
				if got := len(root["select_list"].([]any)); got != 8 {
					t.Fatalf("roots selects %d columns, want 8", got)
				}
			}
		}
		return engine.QueryContext(ctx, text, args...)
	}}
	e := NewExecutor(probe, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	req := exemplarRequest(exemplarDashboard("spans"))
	req.Dimensions = map[string]string{"service": value}
	if _, err := e.Exemplars(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("no candidate query")
	}
}

type exemplarFailureRows struct {
	queryrows.Rows
	next func() bool
	err  error
	scan func() error
}

func (r *exemplarFailureRows) Close() error      { return nil }
func (r *exemplarFailureRows) Next() bool        { return r.next() }
func (r *exemplarFailureRows) Err() error        { return r.err }
func (r *exemplarFailureRows) Scan(...any) error { return r.scan() }

func TestM2FixExemplarRowFailures(t *testing.T) {
	for _, phase := range []string{"rows", "scan"} {
		for _, canceled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", phase, canceled), func(t *testing.T) {
				engine, _ := newTestEngine(t)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				probe := exemplarProbeEngine{Engine: engine, query: func(ctx context.Context, _ string, _ ...any) (queryrows.Rows, error) {
					wait := func() error {
						if canceled {
							cancel()
						}
						<-ctx.Done()
						return errors.New("engine interrupted")
					}
					r := &exemplarFailureRows{}
					if phase == "rows" {
						r.next = func() bool { r.err = wait(); return false }
					} else {
						r.next = func() bool { return true }
						r.scan = wait
					}
					return r, nil
				}}
				e := NewExecutor(probe, 30)
				e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
				e.timeout = 20 * time.Millisecond
				_, err := e.Exemplars(ctx, exemplarRequest(exemplarDashboard("spans")))
				want := context.DeadlineExceeded
				if canceled {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatalf("%v, want %v", err, want)
				}
			})
		}
	}
}

func TestM2FixExemplarProblems(t *testing.T) {
	e := newFixtureExecutor(t)
	for _, name := range []string{"missing", "sql", "metrics", "outside", "other", "dimensions", "early", "late", "selection_early"} {
		t.Run(name, func(t *testing.T) {
			req := exemplarRequest(exemplarDashboard("spans"))
			path := "panel_id"
			switch name {
			case "missing":
				req.PanelID = ""
			case "sql":
				req.Dashboard.Panels[0].Query = nil
				req.Dashboard.Panels[0].Viz = "table"
				req.Dashboard.Panels[0].SQL = "SELECT service FROM spans WHERE $__window(start_time)"
			case "metrics":
				req.Dashboard.Panels[0].Query.From = "metrics"
				req.Dashboard.Panels[0].Query.Measures = []string{"sum(value)"}
			case "outside":
				req.From = fixtureStart.Add(-time.Hour)
				req.To = fixtureStart
				path = "from"
			case "other":
				req.Dashboard.Panels[0].Viz = "timeseries"
				req.Dimensions = map[string]string{"service": "Other"}
				path = "dimensions"
			case "dimensions":
				req.Dimensions = map[string]string{"a": "1", "b": "2", "c": "3", "d": "4"}
				path = "dimensions"
			case "early", "late":
				year := 1600
				if name == "late" {
					year = 2300
				}
				start := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
				end := start.Add(time.Hour)
				req.Time = &Time{From: &start, To: &end}
				req.From, req.To = start, end
				path = "time"
			case "selection_early":
				req.From = time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC)
				path = "from"
			}
			_, err := e.Exemplars(t.Context(), req)
			var problems Problems
			if !errors.As(err, &problems) || len(problems) == 0 || problems[0].Path != path {
				t.Fatalf("got %v, want %s Problem", err, path)
			}
			if name == "other" && problems[0].Message != "Other groups several values; pick a named series" {
				t.Fatal(problems)
			}
			if name == "dimensions" && problems[0].Hint == "" {
				t.Fatal("missing hint")
			}
		})
	}
}

func TestM2FixExemplarClipsMatchingSpan(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	commit(t, repo, []telemetry.Span{
		{Namespace: "n", TraceID: "t", SpanID: "outside", ServiceName: "match", StartUnixNanos: n - int64(time.Minute), EndUnixNanos: n - int64(time.Minute) + 1000000, DurationMS: 1, IngestedAt: n},
		{Namespace: "n", TraceID: "t", SpanID: "inside", ServiceName: "other", StartUnixNanos: n, EndUnixNanos: n + 1000000, DurationMS: 1, IngestedAt: n},
	}, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	req := exemplarRequest(exemplarDashboard("spans"))
	req.Dimensions = map[string]string{"service": "match"}
	// The selection starts before the panel window, so only the clip keeps
	// the outside span out.
	req.From = fixtureStart.Add(-2 * time.Minute)
	got, err := e.Exemplars(t.Context(), req)
	if err != nil || len(got.Traces) != 0 {
		t.Fatalf("outside match escaped clip: %+v %v", got, err)
	}
}

func TestM2FixExemplarCandidateCapAndSlowest(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	var spans []telemetry.Span
	for i := range 1005 {
		spans = append(spans, telemetry.Span{Namespace: "n", TraceID: fmt.Sprintf("t%04d", i), SpanID: "root", ServiceName: "s", StartUnixNanos: n, EndUnixNanos: n + int64(i+1)*1000000, DurationMS: float64(i + 1), IngestedAt: n})
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	got, err := e.Exemplars(t.Context(), exemplarRequest(exemplarDashboard("spans")))
	if err != nil || len(got.Traces) != 20 || !got.Truncated || got.Traces[0].DurationMS != 1005 {
		t.Fatalf("slowest: %+v %v", got, err)
	}
	p := Panel{Query: &Query{From: "spans"}}
	where, args, _ := selectionWhere(&p, nil, Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour)}, nil, nil)
	args = append(args, fixtureStart, fixtureStart.Add(time.Hour))
	rows, err := engine.QueryContext(t.Context(), checkedTraceSQL("spans", where, 2000, "duration_ms DESC,trace_id,namespace", "start_time::TIMESTAMP_NS"), args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 1000 {
		t.Fatalf("candidate cap: got %d", count)
	}
}

func TestM2FixExemplarTruncation(t *testing.T) {
	for _, count := range []int{20, 21} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			engine, repo := newTestEngine(t)
			n := fixtureStart.UnixNano()
			var spans []telemetry.Span
			for i := range count {
				spans = append(spans, telemetry.Span{Namespace: "n", TraceID: fmt.Sprint(i), SpanID: "r", StartUnixNanos: n, EndUnixNanos: n + 1000000, DurationMS: 1, IngestedAt: n})
			}
			commit(t, repo, spans, nil)
			e := NewExecutor(engine, 30)
			e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
			got, err := e.Exemplars(t.Context(), exemplarRequest(exemplarDashboard("spans")))
			if err != nil || got.Truncated != (count > 20) {
				t.Fatalf("count %d: %+v %v", count, got, err)
			}
		})
	}
}

func TestM2FixExemplarLogCapWithoutRoots(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	var logs []telemetry.Log
	for i := range 1001 {
		logs = append(logs, telemetry.Log{Namespace: "n", TraceID: fmt.Sprintf("t%04d", i), Body: "failed", EventUnixNanos: n, IngestedAt: n})
	}
	commit(t, repo, nil, logs)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	got, err := e.Exemplars(t.Context(), exemplarRequest(exemplarDashboard("logs")))
	if err != nil || len(got.Traces) != 0 || !got.Truncated {
		t.Fatalf("lost cap summary: %+v %v", got, err)
	}
}
