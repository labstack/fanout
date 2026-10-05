package panel

import (
	"context"
	"strings"
	"testing"
)

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
		if !strings.Contains(paths[path], "does not bind") || !strings.Contains(paths[path], "Cannot compare") {
			t.Errorf("%s: problem = %q", path, paths[path])
		}
	}
	if !strings.Contains(paths["panels[1].query.where[0]"], "CAST($service AS DOUBLE)") {
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
