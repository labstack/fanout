package panel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/labstack/fanout/internal/telemetry"
)

type interruptingParser struct{ Parser }

func (interruptingParser) PrepareTelemetrySQL(context.Context, string, string, int) (string, []bool, error) {
	return "", nil, &duckdb.Error{Type: duckdb.ErrorTypeInterrupt, Msg: "INTERRUPT Error: Interrupted!"}
}

func TestCheckCollectsFiltersAndSQLProblems(t *testing.T) {
	d, _ := newTestEngine(t)
	spec := decode(t, specExample)
	spec.Panels = append(spec.Panels,
		Panel{ID: "bad_filter", Title: "Bad", Viz: "stat", Query: &Query{From: "spans", Measures: []string{"count()"}, Where: []string{"servce = 'x'"}}},
		Panel{ID: "bad_sql", Title: "Bad SQL", Viz: "table", SQL: "SELECT * FROM read_csv('/etc/passwd') WHERE $__window(time)"},
		Panel{ID: "ok_sql", Title: "OK SQL", Viz: "table", SQL: "SELECT service, count(*) AS n FROM spans WHERE $__window(start_time) AND service = $service GROUP BY 1"},
	)
	Normalize(&spec)
	checked, problems, _ := Check(t.Context(), d, &spec)
	paths := map[string]string{}
	for _, p := range problems {
		paths[p.Path] = p.Message + " " + p.Hint
	}
	if !strings.Contains(paths["panels[1].query.where[0]"], "service") {
		t.Errorf("filter problem missing: %v", problems)
	}
	if !strings.Contains(paths["panels[2].sql"], "read_csv") {
		t.Errorf("sql problem missing: %v", problems)
	}
	if len(problems) != 2 {
		t.Errorf("problems = %v", problems)
	}
	if got := checked.Filters["latency"]; len(got) != 3 || got[0].Params[0] != "service" {
		t.Errorf("latency filters = %+v", got)
	}
	if got := checked.VarFilters["route"]; len(got) != 2 {
		t.Errorf("route variable filters = %+v", got)
	}
}

func TestSQLTimestampComparisonsReturnRepairHintOrExactResults(t *testing.T) {
	engine, repo := newTestEngine(t)
	cutoff := fixtureStart.Add(30 * time.Minute)
	var spans []telemetry.Span
	for i, offset := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
		n := cutoff.Add(offset).UnixNano()
		spans = append(spans, telemetry.Span{TraceID: "trace", SpanID: string(rune('a' + i)), StartUnixNanos: n, EndUnixNanos: n + 1, IngestedAt: n})
	}
	commit(t, repo, spans, nil)
	executor := NewExecutor(engine, 30)
	executor.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	for _, tc := range []struct {
		name, predicate string
		invalid         bool
	}{
		{"timestamptz", "start_time < TIMESTAMPTZ '2026-10-01 12:30:00+00'", true},
		{"timestamptz_reversed", "TIMESTAMPTZ '2026-10-01 12:30:00+00' > start_time", true},
		{"between_timestamptz", "start_time BETWEEN TIMESTAMPTZ '2026-10-01 12:30:00+00' AND TIMESTAMPTZ '2026-10-01 12:30:00+00'", true},
		{"between_timestamptz_reversed", "TIMESTAMPTZ '2026-10-01 12:30:00+00' BETWEEN start_time AND start_time", true},
		{"timestamp_utc", "start_time < TIMESTAMP '2026-10-01 12:30:00'", false},
		{"between_timestamp_utc", "start_time BETWEEN TIMESTAMP '2026-10-01 12:30:00' AND TIMESTAMP '2026-10-01 12:30:00'", false},
		{"explicit_cast", "start_time < TIMESTAMPTZ '2026-10-01 12:30:00+00'::TIMESTAMPTZ_NS", false},
		{"explicit_cast_offset", "start_time < TIMESTAMPTZ '2026-10-01 14:30:00+02'::TIMESTAMPTZ_NS", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Dashboard{Name: "Time literals", Panels: []Panel{{ID: "p", Title: "Calls", Viz: "table", SQL: "SELECT count(*) AS n FROM spans WHERE $__window(start_time) AND " + tc.predicate}}}
			results, err := executor.Run(t.Context(), RunRequest{Dashboard: d})
			if tc.invalid {
				var problems Problems
				if !errors.As(err, &problems) || len(problems) != 1 {
					t.Fatalf("expected one SQL problem, got %v", err)
				}
				// Each publication boundary may present the same problem again.
				for range 3 {
					problems = PresentProblems(problems)
					if p := problems[0]; p.Path != "panels[0].sql" || !strings.Contains(p.Message, "explicit cast is required") || p.Hint != SQLTimestampGuide || !strings.Contains(problems.Error(), SQLTimestampGuide) {
						t.Fatalf("lost repair hint: %+v", p)
					}
				}
				return
			}
			if err != nil || len(results) != 1 || results[0].Status != StatusOK {
				t.Fatalf("results=%+v err=%v", results, err)
			}
			if got := metricValues(t, results[0], "n"); len(got) != 1 || got[0] != float64(1) {
				t.Fatalf("nanosecond boundary count=%v, want [1]", got)
			}
		})
	}
}

// TestCheckBindsFilters pins that a filter which parses and passes the
// allowlist but cannot bind against its signal is a Check problem, not an
// error on every run.
func TestCheckBindsFilters(t *testing.T) {
	d, _ := newTestEngine(t)
	spec := decode(t, specExample)
	spec.Variables[1].Where = append(spec.Variables[1].Where, "start_time > CAST($service AS VARCHAR)")
	spec.Panels = append(spec.Panels,
		Panel{ID: "unbindable", Title: "Unbindable", Viz: "stat", Query: &Query{From: "spans", Measures: []string{"count()"}, Where: []string{
			"duration_ms > CAST($service AS VARCHAR)",
			"start_time > 5",
			"duration_ms > $service",
		}}},
	)
	Normalize(&spec)
	checked, problems, _ := Check(t.Context(), d, &spec)
	paths := map[string]string{}
	for _, p := range problems {
		paths[p.Path] = p.Message
	}
	for _, path := range []string{"variables[1].where[2]", "panels[1].query.where[0]", "panels[1].query.where[1]"} {
		if !strings.Contains(paths[path], "Cannot compare") {
			t.Errorf("%s: problem = %q", path, paths[path])
		}
	}
	if !strings.Contains(paths["panels[1].query.where[0]"], "explicit cast is required") {
		t.Errorf("hint missing: %q", paths["panels[1].query.where[0]"])
	}
	if len(problems) != 3 {
		t.Errorf("problems = %v", problems)
	}
	if got := checked.Filters["unbindable"]; len(got) != 1 || !strings.Contains(got[0].Expr, "CAST($service AS DOUBLE)") {
		t.Errorf("unbindable filters = %+v", got)
	}
}

func TestCheckSQLPanelsUseCanonicalText(t *testing.T) {
	d, _ := newTestEngine(t)
	cases := map[string]string{
		"SELECT $$'$$ AS a, ? AS b, $$'$$ AS c FROM spans WHERE $__window(start_time)":   "",
		"SELECT * FROM spans -- $__window(start_time)":                                   "$__window",
		"SELECT * FROM spans WHERE start_time > now() AND 'x' = '$__window(start_time)'": "",
	}
	for sql, want := range cases {
		spec := decode(t, specExample)
		spec.Panels = []Panel{{ID: "p", Title: "P", Viz: "table", SQL: sql}}
		spec.Variables = nil
		Normalize(&spec)
		_, problems, _ := Check(t.Context(), d, &spec)
		found := false
		for _, p := range problems {
			if p.Path == "panels[0].sql" && strings.Contains(p.Message, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: problems = %v", sql, problems)
		}
	}
}

func TestCheckReturnsOperationalErrorsNotProblems(t *testing.T) {
	d, _ := newTestEngine(t)
	spec := decode(t, specExample)
	Normalize(&spec)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, problems, err := Check(ctx, d, &spec)
	if err == nil || len(problems) != 0 {
		t.Fatalf("err = %v, problems = %v", err, problems)
	}
}

func TestCheckReturnsEngineInterruptAsOperationalError(t *testing.T) {
	d, _ := newTestEngine(t)
	spec := decode(t, specExample)
	Normalize(&spec)
	_, problems, err := Check(t.Context(), interruptingParser{d}, &spec)
	if err == nil || len(problems) != 0 {
		t.Fatalf("err = %v, problems = %v", err, problems)
	}
}

func TestSQLCTEsCannotShadowTelemetry(t *testing.T) {
	engine, _ := newTestEngine(t)
	for _, name := range []string{"logs", "spans", "metrics", "service_rollup", "edge_rollup", "resource_inventory", "RESOURCE_INVENTORY", "LOGS", "safe", "resources"} {
		t.Run(name, func(t *testing.T) {
			d := Dashboard{Name: "CTE", Panels: []Panel{{ID: "p", Title: "P", Viz: "table", SQL: "WITH " + name + " AS (SELECT * FROM logs WHERE $__window(time)) SELECT count(*) FROM " + name}}}
			Normalize(&d)
			_, problems, err := Check(t.Context(), engine, &d)
			if err != nil {
				t.Fatal(err)
			}
			if name == "safe" || name == "resources" {
				if len(problems) > 0 {
					t.Fatalf("normal CTE rejected: %v", problems)
				}
				return
			}
			for _, p := range problems {
				if p.Path == "panels[0].sql" && p.Message == "CTE names must not shadow telemetry relations" {
					return
				}
			}
			t.Fatalf("shadow accepted: %v", problems)
		})
	}
}
