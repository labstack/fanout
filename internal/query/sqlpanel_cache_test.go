package query

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type sqlPanelPrepareCounter struct {
	Engine
	prepares atomic.Int32
}

func (c *sqlPanelPrepareCounter) PrepareTelemetrySQL(ctx context.Context, q, d string, n int) (string, []bool, error) {
	c.prepares.Add(1)
	return c.Engine.PrepareTelemetrySQL(ctx, q, d, n)
}

func TestSQLPanelTemplatesReuseChecksAndBindEachWindowAndVariable(t *testing.T) {
	e := newFixtureExecutor(t)
	defer e.Close()
	counter := &sqlPanelPrepareCounter{Engine: e.engine}
	e.engine = counter
	start := fixtureStart
	end := start.Add(time.Hour)
	d := Dashboard{Name: "SQL templates", Time: Time{From: &start, To: &end}, Variables: []Variable{{Name: "label", Kind: "text"}}, Panels: []Panel{{ID: "sql", Title: "SQL", Viz: "table", SQL: `SELECT $__from::TIMESTAMP_NS AS from_time,$__to::TIMESTAMP_NS AS to_time,$label AS label FROM spans WHERE $__window(start_time) LIMIT 1`}}}

	run := func(label string) Result {
		t.Helper()
		r, err := e.Run(WithoutCachedResults(t.Context()), RunRequest{Dashboard: d, Vars: map[string]Value{"label": {Values: []string{label}}}})
		if err != nil || len(r) != 1 || r[0].Status != StatusOK {
			t.Fatalf("results=%+v err=%v", r, err)
		}
		return r[0]
	}
	first := run("first")
	n := counter.prepares.Load()
	start = start.Add(time.Minute)
	end = end.Add(time.Minute)
	second := run("second")
	if counter.prepares.Load() != n {
		t.Fatal("a new window repeated SQL validation and describe")
	}
	if first.Frame.Values[0][0] == second.Frame.Values[0][0] || second.Frame.Values[0][0] != start.UnixMilli() || second.Frame.Values[1][0] != end.UnixMilli() || second.Frame.Values[2][0] != "second" {
		t.Fatalf("stale arguments: %+v", second.Frame)
	}
}
