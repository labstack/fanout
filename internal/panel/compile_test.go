package panel

import (
	"math"

	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	"reflect"
	"strings"
	"testing"
	"time"
)

var (
	compileStart = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	compileEnd   = compileStart.Add(time.Hour)
	spanWindow   = `"start_time" >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND "start_time" < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS`
)

func measuresOf(t *testing.T, from string, texts ...string) []Measure {
	t.Helper()
	sig, _ := lookupSignal(from)
	var problems Problems
	out := parseMeasures(sig, texts, "m", &problems)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	return out
}

func filter(expr string, params ...string) Filter {
	return Filter{Expr: expr, Source: expr, Params: params, InParams: map[string]bool{}}
}

func TestCompileTimeseriesBindsVariables(t *testing.T) {
	p := &Panel{ID: "latency", Viz: "timeseries", Query: &Query{From: "spans", Bucket: "1m"}}
	scope := Scope{Start: compileStart, End: compileEnd, Interval: time.Minute, Vars: map[string]Value{"service": {Values: []string{"x'); DROP TABLE spans; --"}}}}
	got, err := compileQuery(p, measuresOf(t, "spans", "p50(duration_ms)", "p95(duration_ms)"), []Filter{filter("(service = CAST($service AS VARCHAR))", "service"), filter("(kind = 'SPAN_KIND_SERVER')")}, scope)
	if err != nil {
		t.Fatal(err)
	}
	want := `WITH base AS (SELECT * FROM spans WHERE ` + spanWindow + ` AND ((service = CAST(? AS VARCHAR))) AND ((kind = 'SPAN_KIND_SERVER'))) SELECT epoch_ms(time_bucket(INTERVAL '60 seconds', "start_time"::TIMESTAMP_NS))::BIGINT AS "_t", quantile_cont("duration_ms", 0.5)::DOUBLE AS "p50", quantile_cont("duration_ms", 0.95)::DOUBLE AS "p95" FROM base GROUP BY time_bucket(INTERVAL '60 seconds', "start_time"::TIMESTAMP_NS) ORDER BY "_t" LIMIT 14000`
	if got.SQL != want {
		t.Fatalf("sql\n got: %s\nwant: %s", got.SQL, want)
	}
	if strings.Contains(got.SQL, "DROP TABLE") {
		t.Fatal("a variable value reached the SQL text")
	}
	if !reflect.DeepEqual(got.Args, []any{compileStart, compileEnd, "x'); DROP TABLE spans; --"}) {
		t.Fatalf("args = %v", got.Args)
	}
	wantCols := []Column{{Name: "time", Type: "time", Role: "time"}, {Name: "p50", Type: "number", Role: "measure", Unit: "ms"}, {Name: "p95", Type: "number", Role: "measure", Unit: "ms"}}
	if !reflect.DeepEqual(got.Columns, wantCols) {
		t.Fatalf("columns = %+v", got.Columns)
	}
}

func TestCompileGroupedSeriesFoldsIntoOther(t *testing.T) {
	p := &Panel{ID: "rps", Viz: "timeseries", Options: &Options{Top: 3}, Query: &Query{From: "spans", By: []string{"service"}, Bucket: "5m"}}
	got, err := compileQuery(p, measuresOf(t, "spans", "rate()"), nil, Scope{Start: compileStart, End: compileEnd, Interval: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	bucket := `time_bucket(INTERVAL '300 seconds', "start_time"::TIMESTAMP_NS)`
	other := `CASE WHEN "service" IN (SELECT d FROM top) THEN "service" ELSE 'Other' END`
	want := `WITH base AS (SELECT * FROM spans WHERE ` + spanWindow + `), top AS (SELECT "service" AS d FROM base GROUP BY 1 ORDER BY count(*) DESC LIMIT 3) SELECT epoch_ms(` + bucket + `)::BIGINT AS "_t", coalesce(` + other + `, '') AS "service", count(*) / 300.0 AS "rate" FROM base GROUP BY ` + bucket + `, ` + other + ` ORDER BY "_t", 2 LIMIT 8000`
	if got.SQL != want {
		t.Fatalf("sql\n got: %s\nwant: %s", got.SQL, want)
	}
}

func TestCompileTableDropsAllAndSorts(t *testing.T) {
	p := &Panel{ID: "routes", Viz: "table", Query: &Query{From: "spans", By: []string{"attributes['http.route']"}, Sort: "count", Limit: 5}}
	scope := Scope{Start: compileStart, End: compileEnd, Vars: map[string]Value{"service": {All: true}}}
	got, err := compileQuery(p, measuresOf(t, "spans", "error_rate()", "count()"), []Filter{filter("(service = CAST($service AS VARCHAR))", "service")}, scope)
	if err != nil {
		t.Fatal(err)
	}
	route := `TRY_CAST(attributes['http.route'] AS VARCHAR)`
	want := `WITH base AS (SELECT * FROM spans WHERE ` + spanWindow + `) SELECT coalesce(` + route + `, '') AS "http.route", 100.0 * avg(CASE WHEN status IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END) AS "error_rate", count(*)::DOUBLE AS "count" FROM base GROUP BY ` + route + ` ORDER BY "count" DESC NULLS LAST LIMIT 5`
	if got.SQL != want {
		t.Fatalf("sql\n got: %s\nwant: %s", got.SQL, want)
	}
	if len(got.Args) != 2 {
		t.Fatalf("All must drop the filter: args = %v", got.Args)
	}
}

func TestCompileMultiValueAndEmptySelection(t *testing.T) {
	p := &Panel{ID: "n", Viz: "stat", Query: &Query{From: "spans"}}
	in := Filter{Expr: "(http_route IN (CAST($routes AS VARCHAR)))", Source: "http_route IN $routes", Params: []string{"routes"}, InParams: map[string]bool{"routes": true}}
	got, err := compileQuery(p, measuresOf(t, "spans", "count()"), []Filter{in}, Scope{Start: compileStart, End: compileEnd, Vars: map[string]Value{"routes": {Values: []string{"/a", "/b"}}}})
	if err != nil {
		t.Fatal(err)
	}
	want := `WITH base AS (SELECT * FROM spans WHERE ` + spanWindow + ` AND ((http_route IN (CAST(? AS VARCHAR), CAST(? AS VARCHAR))))) SELECT count(*)::DOUBLE AS "count" FROM base ORDER BY "count" DESC NULLS LAST LIMIT 1000`
	if got.SQL != want || !reflect.DeepEqual(got.Args, []any{compileStart, compileEnd, "/a", "/b"}) {
		t.Fatalf("sql %s args %v", got.SQL, got.Args)
	}
	empty, err := compileQuery(p, measuresOf(t, "spans", "count()"), []Filter{in}, Scope{Start: compileStart, End: compileEnd, Vars: map[string]Value{"routes": {Values: []string{}}}})
	if err != nil || len(empty.Args) != 2 {
		t.Fatalf("an empty multi-value selection must act as All: %v %v", empty.Args, err)
	}
}

func TestCompileShareAndLogs(t *testing.T) {
	p := &Panel{ID: "sev", Viz: "timeseries", Query: &Query{From: "logs", By: []string{"severity"}, Bucket: "1m"}}
	got, err := compileQuery(p, measuresOf(t, "logs", "share()"), nil, Scope{Start: compileStart, End: compileEnd, Interval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	bucket := `time_bucket(INTERVAL '60 seconds', "time"::TIMESTAMP_NS)`
	other := `CASE WHEN "severity" IN (SELECT d FROM top) THEN "severity" ELSE 'Other' END`
	want := `WITH base AS (SELECT * FROM (` + redactedLogSource() + `) WHERE "time" >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND "time" < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS), top AS (SELECT "severity" AS d FROM base GROUP BY 1 ORDER BY count(*) DESC LIMIT 6) SELECT epoch_ms(` + bucket + `)::BIGINT AS "_t", coalesce(` + other + `, '') AS "severity", 100.0 * count(*) / sum(count(*)) OVER (PARTITION BY ` + bucket + `) AS "share" FROM base GROUP BY ` + bucket + `, ` + other + ` ORDER BY "_t", 2 LIMIT 14000`
	if got.SQL != want {
		t.Fatalf("sql\n got: %s\nwant: %s", got.SQL, want)
	}
}

func runCompiled(t *testing.T, d *query.Duck, c Compiled, start, end time.Time) [][]any {
	t.Helper()
	ctx := queryrows.WithWindow(t.Context(), queryrows.Window{Start: start, End: end})
	rows, err := d.QueryContext(ctx, c.SQL, c.Args...)
	if err != nil {
		t.Fatalf("%v\n%s", err, c.SQL)
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		vals := make([]any, len(c.Columns))
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		out = append(out, vals)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCompiledQueriesRunOnTheEngine(t *testing.T) {
	d, repo := newTestEngine(t)
	var logs []telemetry.Log
	for minute := range 3 {
		for i, sev := range []string{"INFO", "INFO", "ERROR"} {
			n := fixtureStart.Add(time.Duration(minute)*time.Minute + time.Duration(i)*time.Second).UnixNano()
			logs = append(logs, telemetry.Log{Namespace: "shop", ServiceName: "checkout", EventUnixNanos: n, TimeUnixNanos: n, Severity: sev, Body: "x", IngestedAt: n})
		}
	}
	commit(t, repo, shopSpans(), logs)
	end := fixtureStart.Add(time.Hour)

	p := &Panel{ID: "rps", Viz: "timeseries", Options: &Options{Top: 3}, Query: &Query{From: "spans", By: []string{"service"}, Bucket: "5m"}}
	c, err := compileQuery(p, measuresOf(t, "spans", "rate()"), nil, Scope{Start: fixtureStart, End: end, Interval: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	rows := runCompiled(t, d, c, fixtureStart, end)
	if len(rows) != 24 || rows[0][0] != fixtureStart.UnixMilli() {
		t.Fatalf("grouped series: %d rows, first %v", len(rows), rows[0])
	}

	svc := filter("(service = CAST($service AS VARCHAR))", "service")
	vars := map[string]Value{"service": {Values: []string{"checkout"}}}
	p = &Panel{ID: "lat", Viz: "timeseries", Query: &Query{From: "spans", Bucket: "5m"}}
	c, err = compileQuery(p, measuresOf(t, "spans", "p95(duration_ms)"), []Filter{svc}, Scope{Start: fixtureStart, End: end, Interval: 5 * time.Minute, Vars: vars})
	if err != nil {
		t.Fatal(err)
	}
	if rows := runCompiled(t, d, c, fixtureStart, end); len(rows) != 12 {
		t.Fatalf("p95 series: %d rows", len(rows))
	}

	p = &Panel{ID: "sev", Viz: "timeseries", Query: &Query{From: "logs", By: []string{"severity"}, Bucket: "1m"}}
	c, err = compileQuery(p, measuresOf(t, "logs", "share()"), nil, Scope{Start: fixtureStart, End: end, Interval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	sums := map[any]float64{}
	for _, r := range runCompiled(t, d, c, fixtureStart, end) {
		sums[r[0]] += r[2].(float64)
	}
	if len(sums) != 3 {
		t.Fatalf("share buckets = %v", sums)
	}
	for bucket, sum := range sums {
		if math.Abs(sum-100) > 1e-9 {
			t.Errorf("bucket %v shares sum to %v", bucket, sum)
		}
	}

	p = &Panel{ID: "n", Viz: "stat", Query: &Query{From: "spans"}}
	c, err = compileQuery(p, measuresOf(t, "spans", "count()"), []Filter{svc}, Scope{Start: fixtureStart, End: end, Vars: vars})
	if err != nil {
		t.Fatal(err)
	}
	if rows := runCompiled(t, d, c, fixtureStart, end); len(rows) != 1 || rows[0][0] != 120.0 {
		t.Fatalf("stat = %v", rows)
	}
}
