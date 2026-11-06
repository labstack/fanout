package query

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// compileMetricPanel compiles a table panel as Run does, without executing it.
func compileMetricPanel(t *testing.T, e *Executor, spec Spec, start, end time.Time) Compiled {
	t.Helper()
	p := Panel{ID: "p", Title: "p", Viz: "table", Query: &spec}
	d := Dashboard{Name: "columns", Time: Time{From: &start, To: &end}, Panels: []Panel{p}}
	Normalize(&d)
	checked, problems, err := checkWith(t.Context(), e.engine, &d, nil)
	if err != nil || len(problems) > 0 {
		t.Fatalf("check: %v %v", err, problems)
	}
	p = d.Panels[0]
	scope := metricReadScope(&p, Scope{Start: start, End: end})
	scope.Start = start
	c, err := compileQuery(&p, checked.Measures[p.ID], checked.Filters[p.ID], scope)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var metricListColumn = regexp.MustCompile(`\b(hist_bounds|hist_counts|exp_[a-z_]+)\b`)

// A rate or quantile names the columns it reads: the view's width never
// passes through the builder chain, and only the histogram read, restricted
// to histogram points, reads bucket lists.
func TestMetricQueriesProjectNarrowColumns(t *testing.T) {
	e, _ := metricFixture(t)
	for name, spec := range map[string]Spec{
		"rate": {From: "metrics", Where: []string{"name = 'aspnetcore.diagnostics.exceptions'"}, Measures: []string{"rate() as r"}, By: []string{"service"}, Sort: "r", Limit: 1000},
		"p95":  {From: "metrics", Where: []string{"name = 'http.server.request.duration'"}, Measures: []string{"p95(value) as p"}, By: []string{"service"}, Sort: "p", Limit: 1000},
	} {
		c := compileMetricPanel(t, e, spec, fixtureStart, fixtureStart.Add(24*time.Hour))
		if strings.Contains(c.SQL, "SELECT * FROM metrics") {
			t.Fatalf("%s reads the whole view:\n%s", name, c.SQL)
		}
		scan := regexp.MustCompile(`metric_scan AS (?:MATERIALIZED )?\(SELECT (.*?) FROM metrics WHERE`).FindStringSubmatch(c.SQL)
		if scan == nil {
			t.Fatalf("%s has no shared scan:\n%s", name, c.SQL)
		}
		want := []string{`"series_id"`, `"time"`, `"type"`, `"temporality"`, `"start_time"`, `"value"`, `"hist_count"`, `"hist_sum"`, `"ingested_unix_nano"`, `"service"`}
		if got := strings.Split(scan[1], ","); !slices.Equal(got, want) {
			t.Fatalf("%s scan columns=%v want %v", name, got, want)
		}
		reads := regexp.MustCompile(`(\w+) AS (?:MATERIALIZED )?\(SELECT ([^()]*?) FROM metrics WHERE `).FindAllStringSubmatch(c.SQL, -1)
		names := []string{}
		for _, read := range reads {
			names = append(names, read[1])
			if metricListColumn.MatchString(read[2]) != (read[1] == "metric_histogram_scan") {
				t.Fatalf("%s read %s projects %s", name, read[1], read[2])
			}
		}
		if !slices.Equal(names, []string{"metric_scan", "metric_histogram_scan"}) {
			t.Fatalf("%s reads=%v", name, names)
		}
		if !regexp.MustCompile(`metric_histogram_scan AS (?:MATERIALIZED )?\(SELECT [^()]* FROM metrics WHERE .*? AND type IN \('histogram'\)\)`).MatchString(c.SQL) {
			t.Fatalf("%s histogram scan reads other kinds:\n%s", name, c.SQL)
		}
		// Builders other than the histogram one never mention a list column.
		for _, part := range strings.Split(c.SQL, "metric_builder_")[1:] {
			if !strings.Contains(part, "type IN ('histogram')") && metricListColumn.MatchString(part) {
				t.Fatalf("%s builder reads bucket lists:\n%s", name, part)
			}
		}
	}
}

// Map lookups bind where the projection reads metrics, so a standard key
// reads its dedicated column even though later stages see only the alias.
func TestMetricDedicatedKeyDimensions(t *testing.T) {
	e, db := metricFixture(t)
	for i, pod := range []string{"pod-a", "pod-b", "pod-b"} {
		for minute := -1; minute < 3; minute++ {
			m := metricCounter(fixtureStart.Add(time.Duration(minute)*time.Minute), float64(100+60*(i+1)*(minute+1)))
			m.Resource = map[string]any{"k8s.pod.name": pod, "custom.key": pod}
			m.Attributes = map[string]any{"series": i}
			insertMetrics(t, db, m)
		}
		h := metricHistogram(fixtureStart.Add(30*time.Second), "[1,10]", "[1,2,0]", 3, 12, pod)
		h.Name, h.Temporality = "lat", "delta"
		h.Resource = map[string]any{"k8s.pod.name": pod}
		h.Attributes = map[string]any{"series": i}
		insertMetrics(t, db, h)
	}
	for _, by := range []string{"resource['k8s.pod.name']", "resource['custom.key']"} {
		r := runMetricMeasure(t, e, []string{"rate()", "count_distinct(resource['k8s.pod.name']) as pods"}, "", []string{by}, fixtureStart, fixtureStart.Add(3*time.Minute))
		key := strings.TrimSuffix(strings.TrimPrefix(by, "resource['"), "']")
		got := map[any][]any{}
		groups, rates, pods := metricValues(t, r, key), metricValues(t, r, "rate"), metricValues(t, r, "pods")
		for i, g := range groups {
			got[g] = []any{rates[i], pods[i]}
		}
		if len(got) != 2 || got["pod-a"] == nil || got["pod-b"] == nil || got["pod-a"][1] != 1.0 || got["pod-b"][1] != 1.0 {
			t.Fatalf("by %s=%v", by, got)
		}
		if got["pod-a"][0] != 1.0 || got["pod-b"][0] != 5.0 {
			t.Fatalf("by %s rates=%v", by, got)
		}
	}
	r := runMetricPanel(t, e, Panel{Viz: "table", Query: &Spec{From: "metrics", Where: []string{"name='lat'"},
		Measures: []string{"p50(value) as p"}, By: []string{"resource['k8s.pod.name']"}}}, fixtureStart, fixtureStart.Add(3*time.Minute))
	groups, quantiles := metricValues(t, r, "k8s.pod.name"), metricValues(t, r, "p")
	if len(groups) != 2 || !slices.Contains(groups, any("pod-a")) || !slices.Contains(groups, any("pod-b")) || quantiles[0] == nil || quantiles[0] != quantiles[1] {
		t.Fatalf("histogram groups=%v quantiles=%v", groups, quantiles)
	}
}
