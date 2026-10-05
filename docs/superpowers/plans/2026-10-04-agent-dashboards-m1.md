# Agent dashboards — milestone 1 (Foundation) implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Fanout's fixed widget dashboards with typed, validated panel specs that an agent writes, that the server compiles to DuckDB SQL and runs in one batch, and that the browser renders as a professional dashboard with time, variables, inspect and six visualizations.

**Architecture:** A new `internal/panel` package owns the v1 spec (Go types are the JSON Schema source), validation with path-addressed problems, a filter guard built on DuckDB's own parser, a query compiler, an executor that runs panels concurrently through `query.Duck.QueryContext` with the read window in the context, and result frames. `internal/dashboard` stores specs and versions in SQLite through sqlc, packs layouts and applies typed edit operations. HTTP and MCP are thin adapters. The browser renders every panel through pure compile functions in `ui/panels`, shared later with chat views.

**Tech Stack:** Go 1.26, Echo v5, DuckDB 2 (pinned, `scripts/with-duckdb.sh`), SQLite (modernc) + Goose + sqlc, MCP Go SDK, React 19, Mantine 9, TanStack Router/Query, ECharts 6.1 (canvas renderer), `react-grid-layout` 2.2.4, `@tanstack/react-table` v9, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-04-agent-dashboards-design.md`

## Global Constraints

- Work only in the feature worktree on branch `feat/agent-dashboards`. Never push, merge or deploy.
- Run Go tests through the DuckDB wrapper: `just test ./internal/panel/...` (never bare `go test`, which links no engine).
- Control state: SQLite through `database/sql` and sqlc bindings generated from `internal/db/queries`, schema from Goose migrations in `internal/db/migrations`. New timestamped migration with `-- +goose Up`; never edit a published migration; add its checksum to `internal/db/migrations_test.go`; run `just db-gen` after schema or query changes.
- Time predicates keep native `TIMESTAMPTZ_NS` columns and bind Go `time.Time` (UTC) as `?::TIMESTAMP_NS::TIMESTAMPTZ_NS`. Cast only datetime-function arguments, e.g. `time_bucket(..., start_time::TIMESTAMP_NS)` (DuckDB 2 has no TIMESTAMPTZ_NS → TIMESTAMP cast; verified on the pinned engine).
- Attribute keys containing dots are literal: `attributes['http.route']`.
- Variables are always bound as parameters, never interpolated into SQL text.
- HTTP routes: `/api`, lowercase, no hyphens in static segments, plural collections, POST for actions. JSON fields and query parameters in snake_case.
- MCP tool names are verb-first snake_case; full replacement is `replace_…`.
- No backward compatibility (pre-release): delete superseded routes, handlers, widgets and tables; add no aliases or fallbacks.
- A breaking change updates registrations, `classifyRoute` in `internal/api/auth_middleware.go`, clients, tests and generated docs (`just docs-generate`, committed) together.
- The browser bundle is committed: after any `ui/` change run `just ui` and commit `internal/ui/dist` with the change (`just ui-check` enforces this).
- `ui/` shared files (`ui/*.ts`, `ui/panels/*.ts`) import only siblings by relative path; no packages.
- UI copy says "panel", never "widget". Colors come from `ui/tokens.ts`; fonts are Geist and Geist Mono.
- Spec values copied verbatim: panel ids `^[a-z][a-z0-9_]{0,39}$`; names 1–80 characters; descriptions ≤ 280; ≤ 12 variables; 1–40 panels; ≤ 6 measures; ≤ 3 `by` dimensions; `limit` ≤ 1000; per-panel timeout 10 s; 2000-point cap per series; ranges `5m 15m 1h 3h 6h 12h 24h 2d 7d 30d`; refresh `off 10s 30s 1m 5m`; units `ms s ns percent ratio count per_second per_minute bytes none`.
- Milestone 1 visualizations: `stat`, `gauge`, `timeseries`, `bar`, `table`, `text`. Others are rejected with a message naming the supported set until milestone 2 adds them.
- `just check` passes at the end of the milestone (Task 16).

## Review Focus

1. A variable value containing quotes or SQL (`o'brien`, `x'); DROP TABLE users; --`) is bound as data and matches literally — tests in Task 4 (compiler) and Task 3 (SQL panels).
2. A multi-value variable with nothing selected behaves as All (its filters are dropped), and a variable set to All never produces `= NULL` — tests in Task 4.
3. An absolute time range with `from` after `to`, or a range longer than retention, is rejected or clamped with a clear message rather than a SQL error — tests in Task 4 (`resolveWindow`).
4. The agent and a user edit the same dashboard at once: the second write fails with a stale-version error instead of silently overwriting — tests in Task 9.
5. One panel whose query fails at execution (for example, comparing an uncast VARIANT attribute) reports an error in its own result while the rest of the batch succeeds — tests in Task 5.

---

## File structure

**New Go**

| File | Responsibility |
|---|---|
| `internal/panel/spec.go` | v1 spec types; the JSON Schema source |
| `internal/panel/problems.go` | `Problem`, `Problems` (an `error`) |
| `internal/panel/suggest.go` | "did you mean" suggestions |
| `internal/panel/catalog.go` | signals, columns, field references, units |
| `internal/panel/measure.go` | measure grammar and default aliases |
| `internal/panel/validate.go` | `Normalize`, structural `Validate`, ranges |
| `internal/panel/filter.go` | filter guard over DuckDB's parse tree |
| `internal/panel/params.go` | `$name` → placeholders, NULL describe twin |
| `internal/panel/sqlpanel.go` | `$__window` / `$__bucket` macros |
| `internal/panel/check.go` | database-backed checks, `Checked` |
| `internal/panel/timerange.go` | concrete windows, overrides, clamping |
| `internal/panel/bucket.go` | auto intervals |
| `internal/panel/compile.go` | structured query → SQL |
| `internal/panel/frame.go` | columnar result frames |
| `internal/panel/exec.go` | `Executor`, `Run`, results, check cache |
| `internal/panel/diagnose.go` | empty-panel diagnosis |
| `internal/panel/variables.go` | variable values and options |
| `internal/panel/schema.go` | telemetry schema discovery |
| `internal/panel/testhelpers_test.go` | fixture engine for package tests |
| `internal/query/panel_sql.go` | `Duck.ParseSQL`, `Duck.PrepareTelemetrySQL` |
| `internal/db/migrations/20261004000000_dashboard_specs.sql` | v1 dashboard tables |
| `internal/db/queries/dashboards.sql` | sqlc queries |
| `internal/dashboard/layout.go` | first-fit packing |
| `internal/dashboard/edit.go` | typed edit operations |
| `internal/dashboard/defaults.go` | default dashboard spec |
| `internal/api/panels.go` | panel, variable and schema routes |
| `internal/mcp/panels.go` | `get_telemetry_schema`, `preview_panels` |

**Rewritten Go:** `internal/dashboard/service.go`, `internal/dashboard/service_test.go`, `internal/api/dashboard.go`, `internal/mcp/dashboards.go`, `internal/agent/runtime.go` (prompt), `internal/query/sql_boundary.go` (small refactor).

**New browser**

| File | Responsibility |
|---|---|
| `ui/panels/types.ts` | spec, frame and result types |
| `ui/panels/units.ts` | value and axis formatting by unit |
| `ui/panels/frame.ts` | frame accessors, series pivot, categories |
| `ui/panels/thresholds.ts` | status for a value |
| `ui/panels/compile.ts` | panel + frame → ECharts option (timeseries, bar, gauge) |
| `ui/host/src/dashboards/api.ts` | REST client for dashboards and panels |
| `ui/host/src/dashboards/search.ts` | URL view state |
| `ui/host/src/dashboards/use-panel-results.ts` | one batch request per refresh |
| `ui/host/src/dashboards/use-variables.ts` | variable options |
| `ui/host/src/dashboards/page.tsx` | dashboard page |
| `ui/host/src/dashboards/toolbar.tsx` | time, refresh, compare, edit |
| `ui/host/src/dashboards/variable-bar.tsx` | variable selects |
| `ui/host/src/dashboards/grid.tsx` | grid and edit mode |
| `ui/host/src/dashboards/panel-card.tsx` | chrome, states, menu |
| `ui/host/src/dashboards/inspect.tsx` | data, query, spec, timing |
| `ui/host/src/dashboards/echart-canvas.tsx` | canvas ECharts wrapper |
| `ui/host/src/dashboards/viz/*.tsx` | stat, gauge, timeseries, bar, table, text |

**Deleted browser:** `ui/host/src/dashboard.tsx`, `dashboard.test.tsx`, `dashboard-layout.ts`, `dashboard-layout.test.ts`, `dashboard-search.ts`, `dashboard-search.test.ts`, `ui/host/src/widgets/` (all files).

---

### Task 1: Spec types, catalog, measures and structural validation

**Files:**
- Create: `internal/panel/spec.go`, `internal/panel/problems.go`, `internal/panel/suggest.go`, `internal/panel/catalog.go`, `internal/panel/measure.go`, `internal/panel/validate.go`
- Test: `internal/panel/validate_test.go`, `internal/panel/measure_test.go`

**Interfaces:**
- Produces: `type Dashboard`, `Time`, `Annotations`, `Variable`, `Panel`, `Query`, `Histogram`, `Threshold`, `Options`, `Click`, `PanelTime`, `Grid`; `const SpecVersion = 1`, `const AllValue = "$__all"`; `type Problem struct{Path, Message, Hint string}`, `type Problems []Problem` (implements `error`); `func Normalize(d *Dashboard)`; `func Validate(d *Dashboard) Problems`; `func ParseSpan(s string) (time.Duration, bool)`; `func SignalNames() []string`; internal `lookupSignal(name string) (*signal, bool)`, `(*signal).field(text string) (FieldRef, error)`, `type FieldRef`, `type Measure`, `parseMeasures(sig *signal, texts []string, path string, problems *Problems) []Measure`, `func (p *Panel) Top() int`, `vizSpecs map[string]vizSpec`.

- [ ] **Step 1: Write the failing tests**

`internal/panel/validate_test.go`:

```go
package panel

import (
	"encoding/json"
	"strings"
	"testing"
)

const specExample = `{
  "version": 1,
  "name": "Checkout latency",
  "description": "Latency, errors and suspects for checkout.",
  "time": { "range": "1h", "refresh": "30s", "compare": "previous_period" },
  "variables": [
    { "name": "service", "kind": "query", "from": "spans", "field": "service", "default": "checkout" },
    { "name": "route", "kind": "query", "from": "spans", "field": "http_route",
      "where": ["service = $service", "kind = 'SPAN_KIND_SERVER'"], "include_all": true },
    { "name": "namespace", "kind": "constant", "value": "shop" }
  ],
  "panels": [
    {
      "id": "latency",
      "title": "Latency for $service",
      "viz": "timeseries",
      "width": 8,
      "query": {
        "from": "spans",
        "where": ["service = $service", "kind = 'SPAN_KIND_SERVER'", "http_route = $route"],
        "measures": ["p50(duration_ms)", "p95(duration_ms)", "p99(duration_ms)"],
        "bucket": "auto"
      },
      "unit": "ms",
      "thresholds": [{ "value": 1500, "status": "bad", "label": "p99 budget" }],
      "drill": "traces"
    }
  ]
}`

func decode(t *testing.T, text string) Dashboard {
	t.Helper()
	var d Dashboard
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestValidateAcceptsSpecExample(t *testing.T) {
	d := decode(t, specExample)
	Normalize(&d)
	if problems := Validate(&d); len(problems) > 0 {
		t.Fatalf("spec example rejected: %v", problems)
	}
}

func TestNormalizeFillsDefaults(t *testing.T) {
	d := Dashboard{Name: " Ops ", Panels: []Panel{
		{ID: "rps", Title: "Requests", Viz: "timeseries", Query: &Query{From: "spans", Measures: []string{"rate()"}}},
		{ID: "notes", Title: "Notes", Viz: "text", Content: "Hello"},
	}}
	Normalize(&d)
	if d.Version != 1 || d.Time.Range != "1h" || d.Time.Refresh != "30s" || d.Name != "Ops" {
		t.Fatalf("top-level defaults = %+v", d)
	}
	if d.Panels[0].Width != 6 || d.Panels[0].Height != "m" || d.Panels[0].Query.Bucket != "auto" {
		t.Fatalf("timeseries defaults = %+v %+v", d.Panels[0], d.Panels[0].Query)
	}
	if d.Panels[1].Width != 4 || d.Panels[1].Height != "s" {
		t.Fatalf("text defaults = %+v", d.Panels[1])
	}
}

func TestValidateReportsPathsAndHints(t *testing.T) {
	cases := []struct {
		name, mutate, path, contains string
	}{
		{"bad id", `{"panels":[{"id":"Latency","title":"x","viz":"stat","query":{"from":"spans","measures":["count()"]}}]}`, "panels[0].id", "lowercase"},
		{"duplicate id", `{"panels":[{"id":"a","title":"x","viz":"text","content":"x"},{"id":"a","title":"y","viz":"text","content":"y"}]}`, "panels[1].id", "duplicate"},
		{"unknown viz", `{"panels":[{"id":"a","title":"x","viz":"piechart","query":{"from":"spans","measures":["count()"]}}]}`, "panels[0].viz", "stat, gauge, timeseries, bar, table, text"},
		{"query and sql", `{"panels":[{"id":"a","title":"x","viz":"table","sql":"SELECT 1","query":{"from":"spans","measures":["count()"]}}]}`, "panels[0]", "exactly one of query or sql"},
		{"unknown field", `{"panels":[{"id":"a","title":"x","viz":"bar","query":{"from":"spans","measures":["count()"],"by":["route"]}}]}`, "panels[0].query.by[0]", "http_route"},
		{"unknown measure", `{"panels":[{"id":"a","title":"x","viz":"stat","query":{"from":"spans","measures":["p95(duraton_ms)"]}}]}`, "panels[0].query.measures[0]", "duration_ms"},
		{"mixed units", `{"panels":[{"id":"a","title":"x","viz":"timeseries","query":{"from":"spans","measures":["p95(duration_ms)","error_rate()"]}}]}`, "panels[0].query.measures", "split"},
		{"stack percentile", `{"panels":[{"id":"a","title":"x","viz":"timeseries","options":{"style":"stacked"},"query":{"from":"spans","measures":["p95(duration_ms)"],"by":["service"]}}]}`, "panels[0].options.style", "additive"},
		{"bar needs by", `{"panels":[{"id":"a","title":"x","viz":"bar","query":{"from":"spans","measures":["count()"]}}]}`, "panels[0].query.by", "at least 1"},
		{"unknown variable", `{"panels":[{"id":"a","title":"x for $svc","viz":"stat","query":{"from":"spans","measures":["count()"]}}]}`, "panels[0].title", "$svc"},
		{"gauge bounds", `{"panels":[{"id":"a","title":"x","viz":"gauge","query":{"from":"spans","measures":["count()"]}}]}`, "panels[0]", "min and max"},
		{"bad range", `{"time":{"range":"90m"},"panels":[{"id":"a","title":"x","viz":"text","content":"x"}]}`, "time.range", "1h"},
		{"click unknown var", `{"panels":[{"id":"a","title":"x","viz":"bar","click":{"set_variable":"route"},"query":{"from":"spans","measures":["count()"],"by":["http_route"]}}]}`, "panels[0].click.set_variable", "route"},
		{"attribute key", `{"panels":[{"id":"a","title":"x","viz":"bar","query":{"from":"spans","measures":["count()"],"by":["attributes['http.route']"]}}]}`, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := decode(t, `{"name":"t",`+strings.TrimPrefix(tc.mutate, "{"))
			Normalize(&d)
			problems := Validate(&d)
			if tc.path == "" {
				if len(problems) > 0 {
					t.Fatalf("unexpected problems: %v", problems)
				}
				return
			}
			for _, p := range problems {
				if p.Path == tc.path && strings.Contains(p.Message+" "+p.Hint, tc.contains) {
					return
				}
			}
			t.Fatalf("want problem at %s containing %q, got %v", tc.path, tc.contains, problems)
		})
	}
}

func TestProblemsError(t *testing.T) {
	err := Problems{{Path: "a", Message: "bad", Hint: "did you mean b?"}}
	if err.Error() != "a: bad (did you mean b?)" {
		t.Fatal(err.Error())
	}
}
```

`internal/panel/measure_test.go`:

```go
package panel

import "testing"

func TestParseMeasures(t *testing.T) {
	spans, _ := lookupSignal("spans")
	var problems Problems
	got := parseMeasures(spans, []string{"p50(duration_ms)", "p95(duration_ms)", "count()", "rate() as rps", "error_rate()", "quantile(duration_ms, 0.999)", "avg(attributes['db.rows'])"}, "m", &problems)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	want := []struct{ alias, fn, unit string }{
		{"p50", "p50", "ms"}, {"p95", "p95", "ms"}, {"count", "count", "count"}, {"rps", "rate", "per_second"},
		{"error_rate", "error_rate", "percent"}, {"quantile", "quantile", "ms"}, {"avg", "avg", "none"},
	}
	for i, w := range want {
		if got[i].Alias != w.alias || got[i].Func != w.fn || got[i].Unit != w.unit {
			t.Fatalf("measure %d = %+v, want %+v", i, got[i], w)
		}
	}
	if got[5].Q != 0.999 {
		t.Fatalf("quantile q = %v", got[5].Q)
	}
}

func TestMeasureAliasCollisions(t *testing.T) {
	metrics, _ := lookupSignal("metrics")
	var problems Problems
	got := parseMeasures(metrics, []string{"avg(value)", "avg(hist_sum)"}, "m", &problems)
	if len(problems) > 0 || got[0].Alias != "avg_value" || got[1].Alias != "avg_hist_sum" {
		t.Fatalf("aliases = %q %q, problems %v", got[0].Alias, got[1].Alias, problems)
	}
}

func TestMeasureErrors(t *testing.T) {
	spans, _ := lookupSignal("spans")
	logs, _ := lookupSignal("logs")
	cases := []struct {
		sig  *signal
		text string
	}{
		{spans, "median(duration_ms)"},
		{spans, "p95()"},
		{spans, "p95(service)"},
		{spans, "count(duration_ms)"},
		{spans, "quantile(duration_ms, 2)"},
		{spans, "last(duration_ms)"},
		{logs, "p95(body)"},
		{spans, "p95(duration_ms) as Bad-Alias"},
	}
	for _, tc := range cases {
		var problems Problems
		parseMeasures(tc.sig, []string{tc.text}, "m", &problems)
		if len(problems) == 0 {
			t.Errorf("%s accepted", tc.text)
		}
	}
}

func TestFieldRefs(t *testing.T) {
	spans, _ := lookupSignal("spans")
	ref, err := spans.field("attributes['http.route']")
	if err != nil || ref.Column != "attributes" || ref.Key != "http.route" {
		t.Fatalf("ref = %+v err %v", ref, err)
	}
	if ref.stringSQL() != "TRY_CAST(attributes['http.route'] AS VARCHAR)" {
		t.Fatal(ref.stringSQL())
	}
	col, _ := spans.field("duration_ms")
	if col.numberSQL() != `"duration_ms"` || col.Unit != "ms" {
		t.Fatalf("column = %+v", col)
	}
	if _, err := spans.field("attributes"); err == nil {
		t.Fatal("bare map column accepted")
	}
	if _, err := spans.field("attributes['a'] OR 1=1"); err == nil {
		t.Fatal("injected attribute accepted")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `just test ./internal/panel/...`
Expected: FAIL — package `internal/panel` has no non-test files (undefined: `Dashboard`, `Normalize`, …).

- [ ] **Step 3: Implement the spec types**

`internal/panel/spec.go`:

```go
// Package panel defines Fanout's dashboard spec: the typed, validated contract
// between the agent, the API, storage and the browser renderer. The Go types
// are the single source of the JSON Schema the MCP tools publish.
package panel

import "time"

// SpecVersion is the only dashboard spec version Fanout reads.
const SpecVersion = 1

// AllValue selects All for a variable that offers it.
const AllValue = "$__all"

type Dashboard struct {
	Version     int          `json:"version" jsonschema:"Spec version; always 1"`
	Name        string       `json:"name" jsonschema:"Short unique dashboard name, at most 80 characters"`
	Description string       `json:"description,omitempty" jsonschema:"What the dashboard is for, at most 280 characters"`
	Time        Time         `json:"time" jsonschema:"Default time range, refresh and comparison"`
	Variables   []Variable   `json:"variables,omitempty" jsonschema:"Variables referenced as $name in filters, titles and SQL"`
	Annotations *Annotations `json:"annotations,omitempty" jsonschema:"Deploy and anomaly markers on time panels"`
	Panels      []Panel      `json:"panels" jsonschema:"Panels in display order"`
}

type Time struct {
	Range   string     `json:"range,omitempty" jsonschema:"Relative range: 5m, 15m, 1h, 3h, 6h, 12h, 24h, 2d, 7d or 30d"`
	From    *time.Time `json:"from,omitempty" jsonschema:"Absolute start (RFC 3339); use with to instead of range"`
	To      *time.Time `json:"to,omitempty" jsonschema:"Absolute end (RFC 3339)"`
	Refresh string     `json:"refresh,omitempty" jsonschema:"off, 10s, 30s, 1m or 5m; default 30s"`
	Compare string     `json:"compare,omitempty" jsonschema:"previous_period to compare with the preceding range"`
}

type Annotations struct {
	Deploys   *bool `json:"deploys,omitempty" jsonschema:"Draw deploys; default true"`
	Anomalies *bool `json:"anomalies,omitempty" jsonschema:"Draw detector anomalies; default true"`
}

type Variable struct {
	Name       string   `json:"name" jsonschema:"Identifier referenced as $name: lowercase letters, digits, underscores"`
	Kind       string   `json:"kind" jsonschema:"query, custom, constant or text"`
	From       string   `json:"from,omitempty" jsonschema:"query variables: spans, logs or metrics"`
	Field      string   `json:"field,omitempty" jsonschema:"query variables: a column or attributes['key'] whose distinct values are the options"`
	Where      []string `json:"where,omitempty" jsonschema:"query variables: filter expressions; may reference earlier variables"`
	Options    []string `json:"options,omitempty" jsonschema:"custom variables: the fixed options"`
	Value      string   `json:"value,omitempty" jsonschema:"constant variables: the value"`
	Default    string   `json:"default,omitempty" jsonschema:"Initial value; $__all selects All when include_all is true"`
	Multi      bool     `json:"multi,omitempty" jsonschema:"Allow several values; use as IN $name"`
	IncludeAll bool     `json:"include_all,omitempty" jsonschema:"Offer All, which removes filters that reference this variable"`
}

type Panel struct {
	ID          string      `json:"id" jsonschema:"Stable identifier: lowercase letters, digits and underscores; edits address panels by id"`
	Title       string      `json:"title" jsonschema:"Panel title, at most 80 characters; may reference $variables"`
	Description string      `json:"description,omitempty" jsonschema:"Help text, at most 280 characters"`
	Viz         string      `json:"viz" jsonschema:"stat, gauge, timeseries, bar, table or text"`
	Width       int         `json:"width,omitempty" jsonschema:"Grid columns from 1 to 12; default depends on viz"`
	Height      string      `json:"height,omitempty" jsonschema:"s, m or l; default depends on viz"`
	Query       *Query      `json:"query,omitempty" jsonschema:"Structured query; exactly one of query or sql, except text panels"`
	SQL         string      `json:"sql,omitempty" jsonschema:"One read-only SELECT over spans, logs, metrics, service_rollup or edge_rollup; must use $__window(time_column)"`
	Unit        string      `json:"unit,omitempty" jsonschema:"ms, s, ns, percent, ratio, count, per_second, per_minute, bytes or none; inferred when omitted"`
	Reduce      string      `json:"reduce,omitempty" jsonschema:"stat and gauge: window (default), last, mean, min, max or sum"`
	Thresholds  []Threshold `json:"thresholds,omitempty" jsonschema:"Up to 4 status boundaries"`
	Better      string      `json:"better,omitempty" jsonschema:"lower or higher; inferred for known measures"`
	Min         *float64    `json:"min,omitempty" jsonschema:"gauge: scale minimum"`
	Max         *float64    `json:"max,omitempty" jsonschema:"gauge: scale maximum"`
	Options     *Options    `json:"options,omitempty"`
	Click       *Click      `json:"click,omitempty" jsonschema:"Clicking a bar, row or series sets a variable"`
	Drill       string      `json:"drill,omitempty" jsonschema:"traces or logs: what a click on a point opens"`
	Time        *PanelTime  `json:"time,omitempty" jsonschema:"Override the dashboard time for this panel"`
	Content     string      `json:"content,omitempty" jsonschema:"text panels: Markdown, at most 4000 characters"`
	Grid        *Grid       `json:"grid,omitempty" jsonschema:"Position set by the server or by dragging; omit when authoring"`
}

type Query struct {
	From      string     `json:"from" jsonschema:"spans, logs or metrics"`
	Where     []string   `json:"where,omitempty" jsonschema:"Filter expressions joined by AND, e.g. service = $service or attributes['http.route'] = '/cart'"`
	Measures  []string   `json:"measures" jsonschema:"1 to 6 of fn(field) [as alias]: count(), rate(), error_rate(), share(), avg, min, max, sum, last, p50, p75, p90, p95, p99, quantile(field, q), count_distinct(field)"`
	By        []string   `json:"by,omitempty" jsonschema:"Up to 3 grouping fields: columns or attributes['key']"`
	Bucket    string     `json:"bucket,omitempty" jsonschema:"auto or 10s, 30s, 1m, 5m, 10m, 15m, 30m, 1h, 3h, 6h, 12h, 1d; required for timeseries"`
	Histogram *Histogram `json:"histogram,omitempty" jsonschema:"heatmap and histogram panels (milestone 2)"`
	Sort      string     `json:"sort,omitempty" jsonschema:"Measure alias to order by, descending; prefix + for ascending"`
	Limit     int        `json:"limit,omitempty" jsonschema:"Row limit, at most 1000"`
}

type Histogram struct {
	Field   string `json:"field"`
	Buckets string `json:"buckets,omitempty"`
}

type Threshold struct {
	Value  float64 `json:"value"`
	Status string  `json:"status" jsonschema:"ok, warn or bad"`
	Label  string  `json:"label,omitempty"`
}

type Options struct {
	Style  string `json:"style,omitempty" jsonschema:"timeseries: line, area, bars or stacked (additive measures only)"`
	Scale  string `json:"scale,omitempty" jsonschema:"linear or log"`
	Top    int    `json:"top,omitempty" jsonschema:"Series limit; the rest become Other; default 8"`
	Legend string `json:"legend,omitempty" jsonschema:"auto or hidden"`
}

type Click struct {
	SetVariable string `json:"set_variable" jsonschema:"Variable set to the clicked value"`
}

type PanelTime struct {
	Range string `json:"range,omitempty" jsonschema:"Replace the dashboard range, e.g. 30d"`
	Shift string `json:"shift,omitempty" jsonschema:"Move the dashboard range back, e.g. 1d"`
}

type Grid struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// Top is the series limit for a grouped time series.
func (p *Panel) Top() int {
	if p.Options != nil && p.Options.Top > 0 {
		return min(p.Options.Top, 20)
	}
	return 8
}
```

- [ ] **Step 4: Implement problems and suggestions**

`internal/panel/problems.go`:

```go
package panel

import (
	"fmt"
	"strings"
)

// Problem is one reason a spec cannot be saved or run, addressed by a JSON
// path so the author can fix exactly that field.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// Problems is a validation result; empty means valid. It is an error so a
// caller can return it unchanged and an adapter can unwrap it with errors.As.
type Problems []Problem

func (p Problems) Error() string {
	parts := make([]string, 0, min(len(p), 6))
	for i, problem := range p {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("and %d more", len(p)-5))
			break
		}
		text := problem.Path + ": " + problem.Message
		if problem.Path == "" {
			text = problem.Message
		}
		if problem.Hint != "" {
			text += " (" + problem.Hint + ")"
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "; ")
}

func (p *Problems) add(path, message string) {
	*p = append(*p, Problem{Path: path, Message: message})
}

func (p *Problems) addHint(path, message, hint string) {
	*p = append(*p, Problem{Path: path, Message: message, Hint: hint})
}
```

`internal/panel/suggest.go`:

```go
package panel

import "strings"

// suggest returns "did you mean X?" for the closest candidate within a small
// edit distance, or "" when nothing is close.
func suggest(word string, candidates []string) string {
	// A candidate that contains the word ("route" in "http_route") is the
	// strongest hint; prefer the shortest such candidate.
	contained := ""
	for _, candidate := range candidates {
		if len(word) >= 3 && strings.Contains(candidate, word) && (contained == "" || len(candidate) < len(contained)) {
			contained = candidate
		}
	}
	if contained != "" && contained != word {
		return "did you mean " + contained + "?"
	}
	best, bestDistance := "", len(word)/3+2
	for _, candidate := range candidates {
		if d := editDistance(word, candidate); d < bestDistance {
			best, bestDistance = candidate, d
		}
	}
	if best == "" {
		return ""
	}
	return "did you mean " + best + "?"
}

func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}
```

- [ ] **Step 5: Implement the catalog**

`internal/panel/catalog.go`:

```go
package panel

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type FieldType string

const (
	TypeString FieldType = "string"
	TypeNumber FieldType = "number"
	TypeTime   FieldType = "time"
	TypeMap    FieldType = "map"
)

type Field struct {
	Name        string    `json:"name"`
	Type        FieldType `json:"type"`
	Unit        string    `json:"unit,omitempty"`
	Description string    `json:"description,omitempty"`
}

type signal struct {
	name     string
	time     string
	fields   []Field
	index    map[string]Field
	lowCard  []string // columns whose common values the schema lists
}

func newSignal(name, timeColumn string, lowCard []string, fields ...Field) *signal {
	s := &signal{name: name, time: timeColumn, fields: fields, index: map[string]Field{}, lowCard: lowCard}
	for _, f := range fields {
		s.index[f.Name] = f
	}
	return s
}

func str(name, description string) Field { return Field{Name: name, Type: TypeString, Description: description} }
func num(name, unit, description string) Field {
	return Field{Name: name, Type: TypeNumber, Unit: unit, Description: description}
}
func ts(name, description string) Field { return Field{Name: name, Type: TypeTime, Description: description} }
func dict(name, description string) Field { return Field{Name: name, Type: TypeMap, Description: description} }

// The columns of the spans, logs and metrics views in internal/query/views.go.
var signals = map[string]*signal{
	"spans": newSignal("spans", "start_time",
		[]string{"service", "kind", "status", "http_method", "db_system", "messaging_system", "deployment_env", "service_version"},
		str("namespace", "OpenTelemetry service namespace"),
		str("service", "Service name"),
		str("operation", "Span name"),
		str("kind", "SPAN_KIND_SERVER, SPAN_KIND_CLIENT, SPAN_KIND_INTERNAL, SPAN_KIND_PRODUCER or SPAN_KIND_CONSUMER"),
		str("status", "STATUS_CODE_OK, STATUS_CODE_ERROR or STATUS_CODE_UNSET"),
		str("status_message", "Status description"),
		str("trace_id", "Trace identifier"),
		str("span_id", "Span identifier"),
		str("parent_span_id", "Parent span identifier; empty for roots"),
		ts("start_time", "Span start"),
		ts("end_time", "Span end"),
		num("duration_ms", "ms", "Span duration in milliseconds"),
		str("http_method", "HTTP method"),
		str("http_status_code", "HTTP response status code as text"),
		str("http_route", "HTTP route template"),
		str("db_system", "Database system, e.g. postgresql, redis"),
		str("rpc_method", "RPC method"),
		str("rpc_service", "RPC service"),
		str("peer_service", "Remote service named by the caller"),
		str("service_version", "service.version resource attribute"),
		str("deployment_env", "deployment.environment resource attribute"),
		str("exception_type", "Exception type from the span's exception event"),
		str("exception_message", "Exception message"),
		str("messaging_system", "messaging.system attribute"),
		str("messaging_destination", "messaging.destination.name attribute"),
		str("scope_name", "Instrumentation scope"),
		dict("attributes", "Span attributes; use attributes['key']"),
		dict("resource", "Resource attributes; use resource['key']"),
	),
	"logs": newSignal("logs", "time",
		[]string{"service", "severity"},
		str("namespace", "OpenTelemetry service namespace"),
		str("service", "Service name"),
		ts("time", "Log time"),
		str("severity", "Severity text, e.g. ERROR, WARN, INFO"),
		num("severity_number", "none", "OpenTelemetry severity number; 17 and above is an error"),
		str("body", "Redacted log body"),
		str("body_template", "Body with variable parts replaced by <*>; groups similar messages"),
		str("trace_id", "Trace identifier"),
		str("span_id", "Span identifier"),
		str("scope_name", "Instrumentation scope"),
		dict("attributes", "Log attributes; use attributes['key']"),
		dict("resource", "Resource attributes; use resource['key']"),
	),
	"metrics": newSignal("metrics", "time",
		[]string{"service", "type"},
		str("namespace", "OpenTelemetry service namespace"),
		str("service", "Service name"),
		ts("time", "Data point time"),
		str("name", "Metric name"),
		str("unit", "Metric unit as reported"),
		str("type", "gauge, sum or histogram"),
		num("value", "none", "Gauge or sum value"),
		num("hist_count", "count", "Histogram point count"),
		num("hist_sum", "none", "Histogram point sum"),
		dict("attributes", "Data point attributes; use attributes['key']"),
		dict("resource", "Resource attributes; use resource['key']"),
	),
}

// SignalNames lists the queryable signals in display order.
func SignalNames() []string { return []string{"spans", "logs", "metrics"} }

func lookupSignal(name string) (*signal, bool) {
	s, ok := signals[name]
	return s, ok
}

func (s *signal) names() []string {
	out := make([]string, 0, len(s.fields))
	for _, f := range s.fields {
		if f.Type != TypeMap {
			out = append(out, f.Name)
		}
	}
	sort.Strings(out)
	return out
}

// FieldRef is a validated reference to a column or an attribute lookup.
type FieldRef struct {
	Text   string
	Column string
	Key    string
	Type   FieldType
	Unit   string
}

var attributePattern = regexp.MustCompile(`^(attributes|resource)\['([A-Za-z0-9_.\-/:@]{1,128})'\]$`)

func (s *signal) field(text string) (FieldRef, error) {
	text = strings.TrimSpace(text)
	if m := attributePattern.FindStringSubmatch(text); m != nil {
		return FieldRef{Text: text, Column: m[1], Key: m[2], Type: TypeString, Unit: "none"}, nil
	}
	f, ok := s.index[text]
	if !ok {
		message := fmt.Sprintf("%q is not a %s field", text, s.name)
		if hint := suggest(text, s.names()); hint != "" {
			return FieldRef{}, errors.New(message + "; " + hint)
		}
		return FieldRef{}, errors.New(message + "; use a column or attributes['key']")
	}
	if f.Type == TypeMap {
		return FieldRef{}, fmt.Errorf("%s is a map; use %s['key']", text, text)
	}
	unit := f.Unit
	if unit == "" {
		unit = "none"
	}
	return FieldRef{Text: text, Column: f.Name, Type: f.Type, Unit: unit}, nil
}

func quoteIdent(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func sqlString(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

// stringSQL renders the field as text, for grouping and option lists.
func (f FieldRef) stringSQL() string {
	if f.Key != "" {
		return fmt.Sprintf("TRY_CAST(%s[%s] AS VARCHAR)", f.Column, sqlString(f.Key))
	}
	if f.Type == TypeString {
		return quoteIdent(f.Column)
	}
	return fmt.Sprintf("CAST(%s AS VARCHAR)", quoteIdent(f.Column))
}

// numberSQL renders the field as a number, for aggregation.
func (f FieldRef) numberSQL() string {
	if f.Key != "" {
		return fmt.Sprintf("TRY_CAST(%s[%s] AS DOUBLE)", f.Column, sqlString(f.Key))
	}
	if f.Type == TypeNumber {
		return quoteIdent(f.Column)
	}
	return fmt.Sprintf("TRY_CAST(%s AS DOUBLE)", quoteIdent(f.Column))
}

// alias is the frame column name for a grouping field.
func (f FieldRef) alias() string {
	if f.Key != "" {
		return f.Key
	}
	return f.Column
}

var unitFamilies = map[string]string{
	"ms": "duration", "s": "duration", "ns": "duration",
	"percent": "percent", "ratio": "ratio", "count": "count",
	"per_second": "rate", "per_minute": "rate", "bytes": "bytes", "none": "none",
}

func unitNames() []string {
	out := make([]string, 0, len(unitFamilies))
	for name := range unitFamilies {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 6: Implement the measure grammar**

`internal/panel/measure.go`:

```go
package panel

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Measure is one parsed aggregate.
type Measure struct {
	Text     string
	Func     string
	Field    *FieldRef
	Q        float64
	Alias    string
	Unit     string
	Additive bool
}

type measureFunc struct {
	args     int
	numeric  bool
	additive bool
	unit     string // fixed unit; empty means the field's unit
	signals  []string
	quantile float64
}

var measureFuncs = map[string]measureFunc{
	"count":          {unit: "count", additive: true},
	"rate":           {unit: "per_second", additive: true},
	"error_rate":     {unit: "percent", signals: []string{"spans", "logs"}},
	"share":          {unit: "percent"},
	"avg":            {args: 1, numeric: true},
	"min":            {args: 1, numeric: true},
	"max":            {args: 1, numeric: true},
	"sum":            {args: 1, numeric: true, additive: true},
	"last":           {args: 1, numeric: true, signals: []string{"metrics"}},
	"p50":            {args: 1, numeric: true, quantile: 0.5},
	"p75":            {args: 1, numeric: true, quantile: 0.75},
	"p90":            {args: 1, numeric: true, quantile: 0.9},
	"p95":            {args: 1, numeric: true, quantile: 0.95},
	"p99":            {args: 1, numeric: true, quantile: 0.99},
	"quantile":       {args: 2, numeric: true},
	"count_distinct": {args: 1, unit: "count"},
}

func measureNames() []string {
	out := make([]string, 0, len(measureFuncs))
	for name := range measureFuncs {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

var (
	measurePattern = regexp.MustCompile(`^\s*([A-Za-z_0-9]+)\s*\((.*)\)\s*(?:(?i:as)\s+(\S+))?\s*$`)
	aliasPattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
)

func parseMeasure(sig *signal, text string) (Measure, string, string) {
	m := measurePattern.FindStringSubmatch(text)
	if m == nil {
		return Measure{}, "is not fn(field) [as alias]", "e.g. p95(duration_ms) or count()"
	}
	name := strings.ToLower(m[1])
	spec, ok := measureFuncs[name]
	if !ok {
		return Measure{}, fmt.Sprintf("unknown function %s", name), suggestOr(name, measureNames(), "functions: "+strings.Join(measureNames(), ", "))
	}
	if len(spec.signals) > 0 && !slices.Contains(spec.signals, sig.name) {
		return Measure{}, fmt.Sprintf("%s is not available for %s", name, sig.name), "available for " + strings.Join(spec.signals, ", ")
	}
	var args []string
	if inner := strings.TrimSpace(m[2]); inner != "" {
		for _, part := range strings.Split(inner, ",") {
			args = append(args, strings.TrimSpace(part))
		}
	}
	if len(args) != spec.args {
		return Measure{}, fmt.Sprintf("%s takes %d argument(s), got %d", name, spec.args, len(args)), ""
	}
	out := Measure{Text: strings.TrimSpace(text), Func: name, Additive: spec.additive, Unit: spec.unit, Q: spec.quantile}
	if spec.args >= 1 {
		ref, err := sig.field(args[0])
		if err != nil {
			return Measure{}, err.Error(), ""
		}
		if spec.numeric && ref.Type != TypeNumber && ref.Key == "" {
			return Measure{}, fmt.Sprintf("%s needs a numeric field; %s is %s", name, ref.Text, ref.Type), ""
		}
		out.Field = &ref
		if out.Unit == "" {
			out.Unit = ref.Unit
		}
	}
	if name == "quantile" {
		q, err := strconv.ParseFloat(args[1], 64)
		if err != nil || q <= 0 || q >= 1 {
			return Measure{}, "quantile q must be a number between 0 and 1", "e.g. quantile(duration_ms, 0.999)"
		}
		out.Q = q
	}
	if alias := m[3]; alias != "" {
		if !aliasPattern.MatchString(alias) {
			return Measure{}, fmt.Sprintf("alias %q must be lowercase letters, digits and underscores", alias), ""
		}
		out.Alias = alias
	}
	return out, "", ""
}

func suggestOr(word string, candidates []string, fallback string) string {
	if hint := suggest(word, candidates); hint != "" {
		return hint
	}
	return fallback
}

// parseMeasures parses every measure and gives each a unique alias: the
// function name, or function_field when two measures share a function.
func parseMeasures(sig *signal, texts []string, path string, problems *Problems) []Measure {
	out := make([]Measure, 0, len(texts))
	for i, text := range texts {
		m, message, hint := parseMeasure(sig, text)
		if message != "" {
			problems.addHint(fmt.Sprintf("%s[%d]", path, i), fmt.Sprintf("%q %s", text, message), hint)
			continue
		}
		out = append(out, m)
	}
	uses := map[string]int{}
	for _, m := range out {
		if m.Alias == "" {
			uses[m.Func]++
		}
	}
	seen := map[string]bool{}
	for i := range out {
		if out[i].Alias == "" {
			out[i].Alias = out[i].Func
			if uses[out[i].Func] > 1 && out[i].Field != nil {
				out[i].Alias = out[i].Func + "_" + sanitize(out[i].Field.alias())
			}
		}
		if seen[out[i].Alias] {
			problems.add(path, fmt.Sprintf("two measures are named %s; add distinct aliases with as", out[i].Alias))
		}
		seen[out[i].Alias] = true
	}
	return out
}

func sanitize(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}
```

- [ ] **Step 7: Implement normalization and structural validation**

`internal/panel/validate.go`:

```go
package panel

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type vizSpec struct {
	width   int
	height  string
	query   bool
	minBy   int
	maxBy   int
	bucket  bool
	reduces bool
}

var vizSpecs = map[string]vizSpec{
	"stat":       {width: 3, height: "s", query: true, reduces: true},
	"gauge":      {width: 3, height: "s", query: true, reduces: true},
	"timeseries": {width: 6, height: "m", query: true, maxBy: 1, bucket: true},
	"bar":        {width: 6, height: "m", query: true, minBy: 1, maxBy: 2},
	"table":      {width: 12, height: "m", query: true, maxBy: 3},
	"text":       {width: 4, height: "s"},
}

var vizOrder = []string{"stat", "gauge", "timeseries", "bar", "table", "text"}

var (
	idPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	varRefPattern = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*)`)
	ranges        = map[string]time.Duration{
		"5m": 5 * time.Minute, "15m": 15 * time.Minute, "1h": time.Hour, "3h": 3 * time.Hour,
		"6h": 6 * time.Hour, "12h": 12 * time.Hour, "24h": 24 * time.Hour, "2d": 48 * time.Hour,
		"7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour,
	}
	rangeOrder = []string{"5m", "15m", "1h", "3h", "6h", "12h", "24h", "2d", "7d", "30d"}
	refreshes  = []string{"off", "10s", "30s", "1m", "5m"}
	buckets    = map[string]time.Duration{
		"10s": 10 * time.Second, "30s": 30 * time.Second, "1m": time.Minute, "5m": 5 * time.Minute,
		"10m": 10 * time.Minute, "15m": 15 * time.Minute, "30m": 30 * time.Minute, "1h": time.Hour,
		"3h": 3 * time.Hour, "6h": 6 * time.Hour, "12h": 12 * time.Hour, "1d": 24 * time.Hour,
	}
	reduces = []string{"window", "last", "mean", "min", "max", "sum"}
)

// ParseSpan reads a span such as 90m, 6h or 2d.
func ParseSpan(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n <= 0 {
		return 0, false
	}
	switch s[len(s)-1] {
	case 's':
		return time.Duration(n) * time.Second, true
	case 'm':
		return time.Duration(n) * time.Minute, true
	case 'h':
		return time.Duration(n) * time.Hour, true
	case 'd':
		return time.Duration(n) * 24 * time.Hour, true
	}
	return 0, false
}

// Normalize trims text and fills defaults in place. It never rejects.
func Normalize(d *Dashboard) {
	if d.Version == 0 {
		d.Version = SpecVersion
	}
	d.Name = strings.TrimSpace(d.Name)
	d.Description = strings.TrimSpace(d.Description)
	if d.Time.Range == "" && d.Time.From == nil && d.Time.To == nil {
		d.Time.Range = "1h"
	}
	if d.Time.Refresh == "" {
		d.Time.Refresh = "30s"
	}
	for i := range d.Panels {
		p := &d.Panels[i]
		p.ID = strings.TrimSpace(p.ID)
		p.Title = strings.TrimSpace(p.Title)
		spec, ok := vizSpecs[p.Viz]
		if !ok {
			continue
		}
		if p.Width == 0 {
			p.Width = spec.width
		}
		if p.Height == "" {
			p.Height = spec.height
		}
		if p.Query != nil && spec.bucket && p.Query.Bucket == "" {
			p.Query.Bucket = "auto"
		}
		if spec.reduces && p.Reduce == "" {
			p.Reduce = "window"
		}
	}
}

// Validate checks everything that does not need the database. Filter
// expressions and SQL text are checked by Check.
func Validate(d *Dashboard) Problems {
	var problems Problems
	if d.Version != SpecVersion {
		problems.add("version", fmt.Sprintf("version must be %d", SpecVersion))
	}
	if n := utf8.RuneCountInString(d.Name); n == 0 || n > 80 {
		problems.add("name", "name must be 1 to 80 characters")
	}
	if utf8.RuneCountInString(d.Description) > 280 {
		problems.add("description", "description is limited to 280 characters")
	}
	validateTime(d.Time, &problems)
	vars := validateVariables(d.Variables, &problems)
	if len(d.Panels) == 0 || len(d.Panels) > 40 {
		problems.add("panels", "a dashboard has 1 to 40 panels")
	}
	ids := map[string]bool{}
	for i := range d.Panels {
		path := fmt.Sprintf("panels[%d]", i)
		p := &d.Panels[i]
		if ids[p.ID] {
			problems.add(path+".id", fmt.Sprintf("duplicate panel id %q", p.ID))
		}
		ids[p.ID] = true
		validatePanel(p, path, vars, &problems)
	}
	return problems
}

func validateTime(t Time, problems *Problems) {
	switch {
	case t.Range != "" && (t.From != nil || t.To != nil):
		problems.add("time", "use either range or from and to")
	case t.Range != "":
		if _, ok := ranges[t.Range]; !ok {
			problems.addHint("time.range", fmt.Sprintf("unsupported range %q", t.Range), "use one of "+strings.Join(rangeOrder, ", "))
		}
	case t.From == nil || t.To == nil:
		problems.add("time", "an absolute range needs both from and to")
	case !t.From.Before(*t.To):
		problems.add("time.from", "from must be before to")
	}
	if !slices.Contains(refreshes, t.Refresh) {
		problems.addHint("time.refresh", fmt.Sprintf("unsupported refresh %q", t.Refresh), "use one of "+strings.Join(refreshes, ", "))
	}
	if t.Compare != "" && t.Compare != "previous_period" {
		problems.add("time.compare", "compare must be previous_period or omitted")
	}
}

func validateVariables(variables []Variable, problems *Problems) map[string]Variable {
	out := map[string]Variable{}
	if len(variables) > 12 {
		problems.add("variables", "a dashboard has at most 12 variables")
	}
	for i, v := range variables {
		path := fmt.Sprintf("variables[%d]", i)
		if !idPattern.MatchString(v.Name) || strings.HasPrefix(v.Name, "__") {
			problems.add(path+".name", "variable names are lowercase letters, digits and underscores, starting with a letter")
		}
		if _, dup := out[v.Name]; dup {
			problems.add(path+".name", fmt.Sprintf("duplicate variable %q", v.Name))
		}
		switch v.Kind {
		case "query":
			sig, ok := lookupSignal(v.From)
			if !ok {
				problems.addHint(path+".from", fmt.Sprintf("unknown signal %q", v.From), "use spans, logs or metrics")
			} else if _, err := sig.field(v.Field); err != nil {
				problems.add(path+".field", err.Error())
			}
		case "custom":
			if len(v.Options) == 0 || len(v.Options) > 200 {
				problems.add(path+".options", "custom variables list 1 to 200 options")
			}
		case "constant":
			if v.Value == "" {
				problems.add(path+".value", "constant variables need a value")
			}
		case "text":
		default:
			problems.addHint(path+".kind", fmt.Sprintf("unknown kind %q", v.Kind), "use query, custom, constant or text")
		}
		if v.Default == AllValue && !v.IncludeAll {
			problems.add(path+".default", "default $__all needs include_all")
		}
		if v.Multi && v.Kind != "query" && v.Kind != "custom" {
			problems.add(path+".multi", "only query and custom variables can be multi-value")
		}
		for _, ref := range varRefs(strings.Join(v.Where, " ")) {
			if _, earlier := out[ref]; !earlier {
				problems.add(path+".where", fmt.Sprintf("$%s must be a variable declared earlier", ref))
			}
		}
		out[v.Name] = v
	}
	return out
}

func varRefs(text string) []string {
	var out []string
	for _, m := range varRefPattern.FindAllStringSubmatch(text, -1) {
		if !strings.HasPrefix(m[1], "__") && !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

func validatePanel(p *Panel, path string, vars map[string]Variable, problems *Problems) {
	if !idPattern.MatchString(p.ID) {
		problems.add(path+".id", "panel ids are lowercase letters, digits and underscores, starting with a letter")
	}
	if n := utf8.RuneCountInString(p.Title); n == 0 || n > 80 {
		problems.add(path+".title", "title must be 1 to 80 characters")
	}
	for _, ref := range varRefs(p.Title) {
		if _, ok := vars[ref]; !ok {
			problems.add(path+".title", fmt.Sprintf("$%s is not a dashboard variable", ref))
		}
	}
	if utf8.RuneCountInString(p.Description) > 280 {
		problems.add(path+".description", "description is limited to 280 characters")
	}
	spec, ok := vizSpecs[p.Viz]
	if !ok {
		problems.addHint(path+".viz", fmt.Sprintf("unsupported viz %q", p.Viz), "use one of "+strings.Join(vizOrder, ", "))
		return
	}
	if p.Width < 1 || p.Width > 12 {
		problems.add(path+".width", "width must be 1 to 12 columns")
	}
	if !slices.Contains([]string{"s", "m", "l"}, p.Height) {
		problems.add(path+".height", "height must be s, m or l")
	}
	if p.Viz == "text" {
		if p.Query != nil || p.SQL != "" {
			problems.add(path, "text panels have content, not a query")
		}
		if n := utf8.RuneCountInString(p.Content); n == 0 || n > 4000 {
			problems.add(path+".content", "content must be 1 to 4000 characters of Markdown")
		}
		return
	}
	if (p.Query == nil) == (strings.TrimSpace(p.SQL) == "") {
		problems.add(path, "set exactly one of query or sql")
		return
	}
	if p.Unit != "" {
		if _, ok := unitFamilies[p.Unit]; !ok {
			problems.addHint(path+".unit", fmt.Sprintf("unknown unit %q", p.Unit), "use one of "+strings.Join(unitNames(), ", "))
		}
	}
	if spec.reduces {
		if !slices.Contains(reduces, p.Reduce) {
			problems.add(path+".reduce", "reduce must be window, last, mean, min, max or sum")
		}
	} else if p.Reduce != "" {
		problems.add(path+".reduce", "reduce applies only to stat and gauge panels")
	}
	if p.Viz == "gauge" && (p.Min == nil || p.Max == nil || *p.Min >= *p.Max) {
		problems.add(path, "gauge panels need min and max, with min below max")
	}
	if len(p.Thresholds) > 4 {
		problems.add(path+".thresholds", "at most 4 thresholds")
	}
	for i, t := range p.Thresholds {
		if !slices.Contains([]string{"ok", "warn", "bad"}, t.Status) {
			problems.add(fmt.Sprintf("%s.thresholds[%d].status", path, i), "status must be ok, warn or bad")
		}
	}
	if p.Better != "" && p.Better != "lower" && p.Better != "higher" {
		problems.add(path+".better", "better must be lower or higher")
	}
	if p.Drill != "" && p.Drill != "traces" && p.Drill != "logs" {
		problems.add(path+".drill", "drill must be traces or logs")
	}
	if p.Click != nil {
		v, ok := vars[p.Click.SetVariable]
		if !ok || (v.Kind != "query" && v.Kind != "custom") {
			problems.add(path+".click.set_variable", fmt.Sprintf("%q must name a query or custom variable", p.Click.SetVariable))
		}
	}
	if p.Time != nil {
		if p.Time.Range != "" {
			if _, ok := ranges[p.Time.Range]; !ok {
				problems.addHint(path+".time.range", fmt.Sprintf("unsupported range %q", p.Time.Range), "use one of "+strings.Join(rangeOrder, ", "))
			}
		}
		if p.Time.Shift != "" {
			if _, ok := ParseSpan(p.Time.Shift); !ok {
				problems.add(path+".time.shift", "shift is a span such as 1h or 1d")
			}
		}
	}
	if p.Options != nil {
		if p.Options.Style != "" && !slices.Contains([]string{"line", "area", "bars", "stacked"}, p.Options.Style) {
			problems.add(path+".options.style", "style must be line, area, bars or stacked")
		}
		if p.Options.Scale != "" && p.Options.Scale != "linear" && p.Options.Scale != "log" {
			problems.add(path+".options.scale", "scale must be linear or log")
		}
		if p.Options.Legend != "" && p.Options.Legend != "auto" && p.Options.Legend != "hidden" {
			problems.add(path+".options.legend", "legend must be auto or hidden")
		}
	}
	if p.Query != nil {
		validateQuery(p, spec, path+".query", problems)
	} else {
		for _, ref := range varRefs(p.SQL) {
			v, ok := vars[ref]
			if !ok {
				problems.add(path+".sql", fmt.Sprintf("$%s is not a dashboard variable", ref))
			} else if v.Multi {
				problems.add(path+".sql", fmt.Sprintf("SQL panels cannot use the multi-value variable $%s", ref))
			}
		}
	}
	for _, ref := range varRefs(strings.Join(queryWhere(p), " ")) {
		if _, ok := vars[ref]; !ok {
			problems.addHint(path+".query.where", fmt.Sprintf("$%s is not a dashboard variable", ref), suggest(ref, mapKeys(vars)))
		}
	}
}

func queryWhere(p *Panel) []string {
	if p.Query == nil {
		return nil
	}
	return p.Query.Where
}

func mapKeys(vars map[string]Variable) []string {
	out := make([]string, 0, len(vars))
	for name := range vars {
		out = append(out, name)
	}
	return out
}

func validateQuery(p *Panel, spec vizSpec, path string, problems *Problems) {
	q := p.Query
	sig, ok := lookupSignal(q.From)
	if !ok {
		problems.addHint(path+".from", fmt.Sprintf("unknown signal %q", q.From), "use spans, logs or metrics")
		return
	}
	for i, w := range q.Where {
		if strings.TrimSpace(w) == "" || len(w) > 500 {
			problems.add(fmt.Sprintf("%s.where[%d]", path, i), "filters are 1 to 500 characters")
		}
	}
	if len(q.Measures) == 0 || len(q.Measures) > 6 {
		problems.add(path+".measures", "a query has 1 to 6 measures")
	}
	measures := parseMeasures(sig, q.Measures, path+".measures", problems)
	if len(q.By) < spec.minBy {
		problems.add(path+".by", fmt.Sprintf("%s panels group by at least %d field", p.Viz, spec.minBy))
	}
	if len(q.By) > spec.maxBy {
		problems.add(path+".by", fmt.Sprintf("%s panels group by at most %d field(s)", p.Viz, spec.maxBy))
	}
	for i, by := range q.By {
		if _, err := sig.field(by); err != nil {
			problems.add(fmt.Sprintf("%s.by[%d]", path, i), err.Error())
		}
	}
	switch {
	case spec.bucket && q.Bucket == "":
		problems.add(path+".bucket", "timeseries panels need a bucket; use auto")
	case !spec.bucket && q.Bucket != "":
		problems.add(path+".bucket", fmt.Sprintf("%s panels do not take a bucket", p.Viz))
	case q.Bucket != "" && q.Bucket != "auto":
		if _, ok := buckets[q.Bucket]; !ok {
			problems.add(path+".bucket", "bucket must be auto or one of 10s, 30s, 1m, 5m, 10m, 15m, 30m, 1h, 3h, 6h, 12h, 1d")
		}
	}
	if q.Histogram != nil {
		problems.add(path+".histogram", "histograms need the heatmap or histogram viz, which arrive in a later release")
	}
	if q.Limit < 0 || q.Limit > 1000 {
		problems.add(path+".limit", "limit must be 0 to 1000")
	}
	if q.Sort != "" {
		alias := strings.TrimPrefix(q.Sort, "+")
		if !slices.ContainsFunc(measures, func(m Measure) bool { return m.Alias == alias }) {
			problems.add(path+".sort", fmt.Sprintf("sort %q must name a measure alias", q.Sort))
		}
	}
	families := map[string]bool{}
	for _, m := range measures {
		families[unitFamilies[m.Unit]] = true
	}
	delete(families, "none")
	if len(families) > 1 && p.Viz != "table" {
		problems.add(path+".measures", "these measures have different units and would share one axis; split them into separate panels")
	}
	if p.Options != nil && p.Options.Style == "stacked" {
		for _, m := range measures {
			if !m.Additive {
				problems.add(strings.TrimSuffix(path, ".query")+".options.style", fmt.Sprintf("stacking needs additive measures such as count, rate or sum; %s is not", m.Alias))
				break
			}
		}
	}
	if spec.reduces && len(q.Measures) != 1 {
		problems.add(path+".measures", fmt.Sprintf("%s panels show one measure", p.Viz))
	}
}
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `just test ./internal/panel/...`
Expected: PASS (all tests in `validate_test.go` and `measure_test.go`).

- [ ] **Step 9: Commit**

```bash
git add internal/panel
git commit -m "feat(panel): add v1 dashboard spec, measures and validation"
```

---

### Task 2: Query engine hooks for parsing and preparing panel SQL

**Files:**
- Create: `internal/query/panel_sql.go`
- Modify: `internal/query/sql_boundary.go` (extract `parseStatement` and `describeProjection`)
- Test: `internal/query/panel_sql_test.go`

**Interfaces:**
- Consumes: existing `validateSQLNode`, `containsLogicalType`, `sqlIdentifier`, `lockParquetRead`, `acquireRead`, `newBatchCacheTest` (test helper in `file_snapshot_test.go`).
- Produces: `func (d *Duck) ParseSQL(ctx context.Context, query string) (map[string]any, error)`; `func (d *Duck) PrepareTelemetrySQL(ctx context.Context, query, describe string, maxRows int) (string, []bool, error)`.

- [ ] **Step 1: Write the failing test**

`internal/query/panel_sql_test.go`:

```go
package query

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestParseSQLReturnsTheStatementNode(t *testing.T) {
	d, _ := newBatchCacheTest(t)
	node, err := d.ParseSQL(t.Context(), "SELECT 1 FROM spans WHERE service = $service")
	if err != nil {
		t.Fatal(err)
	}
	where, _ := node["where_clause"].(map[string]any)
	if where["class"] != "COMPARISON" {
		t.Fatalf("where = %v", where)
	}
	if _, err := d.ParseSQL(t.Context(), "SELECT 1; SELECT 2"); err == nil {
		t.Fatal("two statements accepted")
	}
	if _, err := d.ParseSQL(t.Context(), "SELECT * FROM"); err == nil || !strings.Contains(err.Error(), "parsing failed") {
		t.Fatalf("parse error = %v", err)
	}
}

func TestPrepareTelemetrySQLGuardsAndProjects(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	batch := telemetrystore.Batch{ID: "panel-sql"}
	for i := range 4 {
		n := at.Add(time.Duration(i) * time.Minute).UnixNano()
		batch.Spans = append(batch.Spans, telemetry.Span{Namespace: "shop", ServiceName: "checkout", TraceID: "t", SpanID: string(rune('a' + i)), Kind: "SPAN_KIND_SERVER", StartUnixNanos: n, EndUnixNanos: n + 1e6, DurationMS: float64(i + 1), StatusCode: "STATUS_CODE_OK", Attributes: map[string]any{"http.route": "/cart"}, IngestedAt: n})
	}
	if err := repo.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{
		"SELECT * FROM read_csv('/etc/passwd')",
		"SELECT * FROM duckdb_settings()",
		"INSERT INTO spans SELECT * FROM spans",
	} {
		if _, _, err := d.PrepareTelemetrySQL(t.Context(), bad, bad, 10); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}

	query := "SELECT service, attributes, duration_ms FROM spans WHERE start_time >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS ORDER BY duration_ms"
	describe := strings.ReplaceAll(query, "?", "NULL")
	prepared, serialized, err := d.PrepareTelemetrySQL(t.Context(), query, describe, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(serialized) != 3 || serialized[0] || !serialized[1] || serialized[2] {
		t.Fatalf("serialized = %v", serialized)
	}
	ctx := queryrows.WithWindow(context.Background(), queryrows.Window{Start: at, End: at.Add(time.Hour)})
	rows, err := d.QueryContext(ctx, prepared, at, at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var service, attributes string
	var duration float64
	if !rows.Next() {
		t.Fatal("no rows")
	}
	if err := rows.Scan(&service, &attributes, &duration); err != nil {
		t.Fatal(err)
	}
	if service != "checkout" || duration != 1 || !strings.Contains(attributes, `"http.route"`) {
		t.Fatalf("row = %q %q %v", service, attributes, duration)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `just test ./internal/query/ -run 'TestParseSQL|TestPrepareTelemetrySQL' -v`
Expected: FAIL — `d.ParseSQL undefined`, `d.PrepareTelemetrySQL undefined`.

- [ ] **Step 3: Extract the shared parse and describe helpers**

In `internal/query/sql_boundary.go`, replace the body of `validateSQLAST` and `projectSQLResults` with calls to two new helpers, keeping their signatures:

```go
type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// parseStatement parses one statement with DuckDB's own parser. The query is
// a bound value, so nothing is prepared or executed, including statements
// before a SELECT.
func parseStatement(ctx context.Context, db rowQueryer, query string) (map[string]any, error) {
	var text string
	if err := db.QueryRowContext(ctx, "SELECT json_serialize_sql(?::VARCHAR)::VARCHAR", query).Scan(&text); err != nil {
		return nil, err
	}
	var parsed struct {
		Error      bool   `json:"error"`
		Message    string `json:"error_message"`
		Statements []struct {
			Node map[string]any `json:"node"`
		} `json:"statements"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil, err
	}
	if parsed.Error {
		return nil, fmt.Errorf("SQL parsing failed: %s", parsed.Message)
	}
	if len(parsed.Statements) != 1 {
		return nil, fmt.Errorf("exactly one SELECT statement is required")
	}
	return parsed.Statements[0].Node, nil
}

func validateSQLAST(ctx context.Context, conn *sql.Conn, query string) error {
	node, err := parseStatement(ctx, conn, query)
	if err != nil {
		return err
	}
	return validateSQLNode(node, nil)
}

// describeProjection describes a query and returns, per result column, the
// projection that keeps nested and VARIANT values away from the Go driver.
func describeProjection(ctx context.Context, conn *sql.Conn, query string) (projection, aliases []string, serialized []bool, err error) {
	rows, err := conn.QueryContext(ctx, "DESCRIBE "+query)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var name, logicalType string
		var nullable, key, defaultValue, extra sql.NullString
		if err := rows.Scan(&name, &logicalType, &nullable, &key, &defaultValue, &extra); err != nil {
			return nil, nil, nil, err
		}
		if names[strings.ToLower(name)] {
			return nil, nil, nil, fmt.Errorf("duplicate result column %q; use distinct aliases", name)
		}
		names[strings.ToLower(name)] = true
		alias := fmt.Sprintf("_c%d", len(aliases))
		aliases = append(aliases, alias)
		expression := alias
		encode := containsLogicalType(logicalType, "VARIANT") || containsLogicalType(logicalType, "JSON") ||
			containsLogicalType(logicalType, "STRUCT") || containsLogicalType(logicalType, "MAP") || containsLogicalType(logicalType, "UNION") || strings.Contains(logicalType, "[")
		if encode {
			expression = "to_json(" + expression + ")::VARCHAR"
		}
		if logicalType == "TIMESTAMPTZ_NS" {
			expression = expression + "::TIMESTAMP_NS"
		}
		projection = append(projection, expression+" AS "+sqlIdentifier(name))
		serialized = append(serialized, encode)
	}
	return projection, aliases, serialized, rows.Err()
}

func projectSQLResults(ctx context.Context, conn *sql.Conn, query string, maxRows int) (string, []bool, error) {
	projection, aliases, serialized, err := describeProjection(ctx, conn, query)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("SELECT %s FROM (%s) AS _result(%s) LIMIT %d", strings.Join(projection, ","), query, strings.Join(aliases, ","), maxRows), serialized, nil
}
```

- [ ] **Step 4: Add the panel entry points**

`internal/query/panel_sql.go`:

```go
package query

import (
	"context"
	"fmt"
	"strings"
)

// ParseSQL parses one statement with DuckDB's parser and returns its
// serialized node. It reads no telemetry and takes no snapshot.
func (d *Duck) ParseSQL(ctx context.Context, query string) (map[string]any, error) {
	return parseStatement(ctx, d.DB, query)
}

// PrepareTelemetrySQL checks one untrusted SELECT against the telemetry SQL
// rules and wraps it in the projection that keeps nested values away from the
// Go driver. describe is the same statement with every placeholder replaced
// by NULL, which DESCRIBE can bind; the returned statement keeps query's
// placeholders and is run with QueryContext under the panel's read window.
func (d *Duck) PrepareTelemetrySQL(ctx context.Context, query, describe string, maxRows int) (string, []bool, error) {
	if err := d.lockParquetRead(ctx, readerQuery); err != nil {
		return "", nil, err
	}
	defer d.parquetMu.RUnlock()
	conn, err := d.acquireRead(ctx)
	if err != nil {
		return "", nil, err
	}
	defer conn.Close()
	if err := validateSQLAST(ctx, conn, query); err != nil {
		return "", nil, err
	}
	trim := func(s string) string { return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), ";")) }
	projection, aliases, serialized, err := describeProjection(ctx, conn, trim(describe))
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("SELECT %s FROM (%s) AS _result(%s) LIMIT %d", strings.Join(projection, ","), trim(query), strings.Join(aliases, ","), maxRows), serialized, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `just test ./internal/query/ -run 'TestParseSQL|TestPrepareTelemetrySQL|TestExecuteSQL|SQL' -v`
Expected: PASS, including the existing SQL boundary tests (behavior unchanged).

- [ ] **Step 6: Commit**

```bash
git add internal/query/panel_sql.go internal/query/panel_sql_test.go internal/query/sql_boundary.go
git commit -m "feat(query): expose statement parsing and guarded panel SQL preparation"
```

---

### Task 3: Filter guard, parameter binding, SQL panel macros and `Check`

**Files:**
- Create: `internal/panel/filter.go`, `internal/panel/params.go`, `internal/panel/sqlpanel.go`, `internal/panel/check.go`, `internal/panel/testhelpers_test.go`
- Test: `internal/panel/filter_test.go`, `internal/panel/params_test.go`, `internal/panel/check_test.go`

**Interfaces:**
- Consumes: `Duck.ParseSQL`, `Duck.PrepareTelemetrySQL` (Task 2); `lookupSignal`, `parseMeasures`, `Validate` (Task 1).
- Produces:
  - `type Parser interface { ParseSQL(ctx context.Context, query string) (map[string]any, error); PrepareTelemetrySQL(ctx context.Context, query, describe string, maxRows int) (string, []bool, error) }`
  - `type Filter struct { Expr string; Params []string; InParams map[string]bool; EqField string }`
  - `type Checked struct { Filters map[string][]Filter; VarFilters map[string][]Filter; Measures map[string][]Measure }`
  - `func Check(ctx context.Context, parser Parser, d *Dashboard) (*Checked, Problems)`
  - `func bindParams(text string, lookup func(name string) (values []any, list bool, err error)) (query, describe string, args []any, err error)`
  - `func expandMacros(sql string, interval time.Duration) (string, error)`
  - test helper `newTestEngine(t *testing.T) (*query.Duck, *telemetrystore.Repository)` and `commitSpans(t, repo, spans...)`.

- [ ] **Step 1: Write the test helper and failing tests**

`internal/panel/testhelpers_test.go`:

```go
package panel

import (
	"fmt"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

// fixtureStart is the start of every fixture window: twelve hours of data
// would be unnecessary; tests query 12:00–13:00 UTC on 2026-10-01.
var fixtureStart = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newTestEngine(t *testing.T) (*query.Duck, *telemetrystore.Repository) {
	t.Helper()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(); _ = repo.Close() })
	return d, repo
}

var batchSeq int

func commit(t *testing.T, repo *telemetrystore.Repository, spans []telemetry.Span, logs []telemetry.Log) {
	t.Helper()
	batchSeq++
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprintf("fixture-%d", batchSeq), Spans: spans, Logs: logs}); err != nil {
		t.Fatal(err)
	}
}

// shopSpans is one minute-spaced server span per (service, route) for an
// hour: checkout /cart (slow after 12:30, with errors), checkout /quote, and
// frontend /api/cart.
func shopSpans() []telemetry.Span {
	var out []telemetry.Span
	for minute := range 60 {
		at := fixtureStart.Add(time.Duration(minute) * time.Minute)
		add := func(service, route string, ms float64, status string) {
			n := at.UnixNano()
			out = append(out, telemetry.Span{
				Namespace: "shop", ServiceName: service, TraceID: fmt.Sprintf("%s-%s-%d", service, route, minute), SpanID: fmt.Sprintf("%s-%d", route, minute),
				Name: "GET " + route, Kind: "SPAN_KIND_SERVER", StartUnixNanos: n, EndUnixNanos: n + int64(ms*1e6), DurationMS: ms,
				StatusCode: status, HTTPMethod: "GET", HTTPRoute: route, ServiceVersion: "v1",
				Attributes: map[string]any{"http.route": route, "customer.tier": "gold"}, IngestedAt: n,
			})
		}
		slow, status := 100.0, "STATUS_CODE_OK"
		if minute >= 30 {
			slow = 900
			if minute%5 == 0 {
				status = "STATUS_CODE_ERROR"
			}
		}
		add("checkout", "/cart", slow, status)
		add("checkout", "/quote", 40, "STATUS_CODE_OK")
		add("frontend", "/api/cart", 20, "STATUS_CODE_OK")
	}
	return out
}
```

`internal/panel/filter_test.go`:

```go
package panel

import (
	"strings"
	"testing"
)

func TestCheckFilterAcceptsSafeExpressions(t *testing.T) {
	d, _ := newTestEngine(t)
	spans, _ := lookupSignal("spans")
	vars := map[string]Variable{"service": {Name: "service", Kind: "query"}, "routes": {Name: "routes", Kind: "query", Multi: true}}
	cases := []struct {
		expr     string
		params   []string
		inParams []string
		eq       string
	}{
		{"service = $service", []string{"service"}, nil, "service"},
		{"attributes['http.route'] = '/cart'", nil, nil, "attributes['http.route']"},
		{"attributes['http.status_code']::INTEGER >= 500", nil, nil, ""},
		{"http_route IN $routes", []string{"routes"}, []string{"routes"}, ""},
		{"kind IN ('SPAN_KIND_SERVER', 'SPAN_KIND_CONSUMER') AND NOT (status = 'STATUS_CODE_OK')", nil, nil, ""},
		{"lower(operation) LIKE 'get %' OR http_route ILIKE '%cart%'", nil, nil, ""},
		{"parent_span_id IS NULL OR duration_ms BETWEEN 100 AND 2000", nil, nil, ""},
	}
	for _, tc := range cases {
		f, err := checkFilter(t.Context(), d, spans, vars, tc.expr)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if strings.Join(f.Params, ",") != strings.Join(tc.params, ",") || f.EqField != tc.eq {
			t.Errorf("%s: params %v eq %q", tc.expr, f.Params, f.EqField)
		}
		for _, name := range tc.inParams {
			if !f.InParams[name] {
				t.Errorf("%s: %s not marked IN", tc.expr, name)
			}
		}
	}
}

func TestCheckFilterRejectsUnsafeExpressions(t *testing.T) {
	d, _ := newTestEngine(t)
	spans, _ := lookupSignal("spans")
	vars := map[string]Variable{"service": {Name: "service", Kind: "query"}, "routes": {Name: "routes", Kind: "query", Multi: true}}
	cases := map[string]string{
		"1=1) UNION SELECT * FROM users WHERE (1=1":           "single boolean expression",
		"service IN (SELECT name FROM users)":                  "not allowed",
		"read_text('/etc/passwd') <> ''":                       "read_text",
		"servce = 'checkout'":                                  "service",
		"service = $svc":                                       "$svc",
		"http_route = $routes":                                 "IN $routes",
		"service = ?":                                          "variables",
		"attributes['a'] = 'x' AND attributes[service] = 'y'": "attribute",
		"service = 'a') GROUP BY (1":                           "single boolean expression",
		"getenv('HOME') = 'x'":                                 "getenv",
	}
	for expr, want := range cases {
		_, err := checkFilter(t.Context(), d, spans, vars, expr)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want containing %q", expr, err, want)
		}
	}
}
```

`internal/panel/params_test.go`:

```go
package panel

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func lookupFixed(values map[string][]any, lists map[string]bool) func(string) ([]any, bool, error) {
	return func(name string) ([]any, bool, error) {
		v, ok := values[name]
		if !ok {
			return nil, false, fmt.Errorf("unknown variable $%s", name)
		}
		return v, lists[name], nil
	}
}

func TestBindParams(t *testing.T) {
	query, describe, args, err := bindParams(
		"service = $service AND body LIKE '$notavar' AND \"$col\" = 1 AND route IN $routes",
		lookupFixed(map[string][]any{"service": {"o'brien"}, "routes": {"/a", "/b"}}, map[string]bool{"routes": true}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if query != `service = ? AND body LIKE '$notavar' AND "$col" = 1 AND route IN (?, ?)` {
		t.Fatal(query)
	}
	if describe != `service = NULL AND body LIKE '$notavar' AND "$col" = 1 AND route IN (NULL, NULL)` {
		t.Fatal(describe)
	}
	if !reflect.DeepEqual(args, []any{"o'brien", "/a", "/b"}) {
		t.Fatal(args)
	}
	for _, bad := range []string{"x = $1", "x = ?"} {
		if _, _, _, err := bindParams(bad, lookupFixed(nil, nil)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestExpandMacros(t *testing.T) {
	out, err := expandMacros("SELECT $__bucket(s.start_time) AS t, count(*) FROM spans s WHERE $__window(s.start_time) GROUP BY 1", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT time_bucket(INTERVAL '300 seconds', s.start_time::TIMESTAMP) AS t, count(*) FROM spans s WHERE s.start_time >= $__from::TIMESTAMP_NS::TIMESTAMPTZ_NS AND s.start_time < $__to::TIMESTAMP_NS::TIMESTAMPTZ_NS GROUP BY 1"
	if out != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
	if _, err := expandMacros("SELECT count(*) FROM spans", time.Minute); err == nil || !strings.Contains(err.Error(), "$__window") {
		t.Fatalf("missing window macro err = %v", err)
	}
	if _, err := expandMacros("SELECT $__interval FROM spans WHERE $__window(start_time)", time.Minute); err == nil {
		t.Fatal("unknown macro accepted")
	}
}
```

`internal/panel/check_test.go`:

```go
package panel

import (
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
	checked, problems := Check(t.Context(), d, &spec)
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `just test ./internal/panel/...`
Expected: FAIL — undefined `checkFilter`, `bindParams`, `expandMacros`, `Check`.

- [ ] **Step 3: Implement the filter guard**

`internal/panel/filter.go`:

```go
package panel

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Filter is one checked filter expression.
type Filter struct {
	Expr     string          `json:"expr"`
	Params   []string        `json:"params,omitempty"`
	InParams map[string]bool `json:"-"`
	// EqField is the field compared when the filter is `field = value`; the
	// empty-panel diagnosis lists values seen for it.
	EqField string `json:"-"`
}

var (
	filterFunctions = map[string]bool{"~~": true, "!~~": true, "~~*": true, "!~~*": true, "lower": true, "upper": true,
		"coalesce": true, "starts_with": true, "contains": true, "regexp_matches": true, "length": true}
	filterCasts     = map[string]bool{"VARCHAR": true, "BIGINT": true, "INTEGER": true, "DOUBLE": true, "BOOLEAN": true, "UBIGINT": true}
	filterOperators = map[string]bool{"OPERATOR_NOT": true, "OPERATOR_IS_NULL": true, "OPERATOR_IS_NOT_NULL": true, "COMPARE_IN": true, "COMPARE_NOT_IN": true}
)

// checkFilter parses expr with DuckDB as the WHERE clause of a query over the
// signal and walks the tree against an allowlist: columns of that signal,
// attribute lookups with literal keys, comparisons, boolean logic, IN, LIKE,
// IS NULL, BETWEEN, casts to scalar types, constants, declared variables, and
// a few scalar functions. Anything else is rejected.
func checkFilter(ctx context.Context, parser Parser, sig *signal, vars map[string]Variable, expr string) (Filter, error) {
	if strings.TrimSpace(expr) == "" || len(expr) > 500 {
		return Filter{}, errors.New("filters are 1 to 500 characters")
	}
	if strings.Contains(stripQuoted(expr), "?") {
		return Filter{}, errors.New("use $variables instead of ? placeholders")
	}
	node, err := parser.ParseSQL(ctx, "SELECT 1 FROM "+sig.name+" WHERE ("+expr+")")
	if err != nil {
		return Filter{}, fmt.Errorf("does not parse: %v", err)
	}
	if !onlyWhereClause(node, sig.name) {
		return Filter{}, errors.New("must be a single boolean expression")
	}
	f := Filter{Expr: strings.TrimSpace(expr), InParams: map[string]bool{}}
	uses := map[string]int{}
	if err := walkFilter(node["where_clause"], sig, vars, &f, uses); err != nil {
		return Filter{}, err
	}
	for _, name := range f.Params {
		if vars[name].Multi && !f.InParams[name] {
			return Filter{}, fmt.Errorf("$%s holds several values; use it as IN $%s", name, name)
		}
		if f.InParams[name] && uses[name] > 1 {
			return Filter{}, fmt.Errorf("use $%s either with IN or with a comparison, not both", name)
		}
	}
	f.EqField = equalityField(node["where_clause"])
	return f, nil
}

func onlyWhereClause(node map[string]any, table string) bool {
	if node["type"] != "SELECT_NODE" {
		return false
	}
	if mods, _ := node["modifiers"].([]any); len(mods) > 0 {
		return false
	}
	if cte, _ := node["cte_map"].(map[string]any); cte != nil {
		if entries, _ := cte["map"].([]any); len(entries) > 0 {
			return false
		}
	}
	if groups, _ := node["group_expressions"].([]any); len(groups) > 0 {
		return false
	}
	if sets, _ := node["grouping_sets"].([]any); len(sets) > 0 {
		return false
	}
	if node["having"] != nil || node["qualify"] != nil || node["sample"] != nil {
		return false
	}
	if list, _ := node["select_list"].([]any); len(list) != 1 {
		return false
	}
	from, _ := node["from_table"].(map[string]any)
	return from != nil && from["type"] == "BASE_TABLE" && from["table_name"] == table && from["schema_name"] == "" && from["catalog_name"] == ""
}

func walkFilter(n any, sig *signal, vars map[string]Variable, f *Filter, uses map[string]int) error {
	switch v := n.(type) {
	case nil:
		return nil
	case []any:
		for _, child := range v {
			if err := walkFilter(child, sig, vars, f, uses); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		class, _ := v["class"].(string)
		kind, _ := v["type"].(string)
		walk := func(keys ...string) error {
			for _, key := range keys {
				if err := walkFilter(v[key], sig, vars, f, uses); err != nil {
					return err
				}
			}
			return nil
		}
		switch class {
		case "CONJUNCTION":
			return walk("children")
		case "COMPARISON":
			return walk("left", "right")
		case "BETWEEN":
			return walk("input", "lower", "upper")
		case "OPERATOR":
			if kind == "ARRAY_EXTRACT" {
				return checkAttributeLookup(v)
			}
			if !filterOperators[kind] {
				return fmt.Errorf("%s is not allowed in a filter", strings.ToLower(kind))
			}
			return walk("children")
		case "CAST":
			castType, _ := v["cast_type"].(map[string]any)
			id, _ := castType["id"].(string)
			if !filterCasts[id] {
				return fmt.Errorf("casting to %s is not allowed in a filter", id)
			}
			return walk("child")
		case "COLUMN_REF":
			names, _ := v["column_names"].([]any)
			if len(names) != 1 {
				return errors.New("qualified column names are not allowed in a filter")
			}
			name, _ := names[0].(string)
			if _, err := sig.field(name); err != nil {
				return err
			}
			return nil
		case "CONSTANT":
			return nil
		case "PARAMETER":
			name, _ := v["identifier"].(string)
			if name == "" || (name[0] >= '0' && name[0] <= '9') {
				return errors.New("use $name variables, not positional parameters")
			}
			if _, ok := vars[name]; !ok {
				return fmt.Errorf("$%s is not a declared variable", name)
			}
			uses[name]++
			if !slices.Contains(f.Params, name) {
				f.Params = append(f.Params, name)
			}
			return nil
		case "FUNCTION":
			name, _ := v["function_name"].(string)
			if !filterFunctions[name] {
				return fmt.Errorf("function %s is not allowed in a filter", name)
			}
			if distinct, _ := v["distinct"].(bool); distinct {
				return errors.New("DISTINCT is not allowed in a filter")
			}
			if orders, _ := v["order_bys"].(map[string]any); orders != nil {
				if list, _ := orders["orders"].([]any); len(list) > 0 {
					return errors.New("ORDER BY is not allowed in a filter")
				}
			}
			if v["filter"] != nil {
				return errors.New("FILTER is not allowed in a filter")
			}
			children, _ := v["children"].([]any)
			if name == "contains" && len(children) == 2 {
				if param, _ := children[0].(map[string]any); param != nil && param["class"] == "PARAMETER" {
					id, _ := param["identifier"].(string)
					f.InParams[id] = true
				}
			}
			return walk("children")
		default:
			return fmt.Errorf("%s is not allowed in a filter", strings.ToLower(strings.TrimPrefix(kind, "VALUE_")))
		}
	}
	return nil
}

func checkAttributeLookup(v map[string]any) error {
	children, _ := v["children"].([]any)
	if len(children) != 2 {
		return errors.New("attribute lookups take one key")
	}
	column, _ := children[0].(map[string]any)
	names, _ := column["column_names"].([]any)
	if column["class"] != "COLUMN_REF" || len(names) != 1 || (names[0] != "attributes" && names[0] != "resource") {
		return errors.New("only attributes['key'] and resource['key'] lookups are allowed")
	}
	key, _ := children[1].(map[string]any)
	value, _ := key["value"].(map[string]any)
	text, _ := value["value"].(string)
	if key["class"] != "CONSTANT" || !attributePattern.MatchString(fmt.Sprintf("%s['%s']", names[0], text)) {
		return errors.New("attribute keys must be literal strings such as attributes['http.route']")
	}
	return nil
}

func equalityField(n any) string {
	v, _ := n.(map[string]any)
	if v == nil || v["class"] != "COMPARISON" || v["type"] != "COMPARE_EQUAL" {
		return ""
	}
	for _, side := range []string{"left", "right"} {
		if field := fieldText(v[side]); field != "" {
			return field
		}
	}
	return ""
}

func fieldText(n any) string {
	v, _ := n.(map[string]any)
	if v == nil {
		return ""
	}
	switch v["class"] {
	case "CAST":
		return fieldText(v["child"])
	case "COLUMN_REF":
		names, _ := v["column_names"].([]any)
		if len(names) == 1 {
			name, _ := names[0].(string)
			return name
		}
	case "OPERATOR":
		if v["type"] == "ARRAY_EXTRACT" {
			children, _ := v["children"].([]any)
			column, _ := children[0].(map[string]any)
			names, _ := column["column_names"].([]any)
			key, _ := children[1].(map[string]any)
			value, _ := key["value"].(map[string]any)
			text, _ := value["value"].(string)
			if len(names) == 1 {
				return fmt.Sprintf("%s['%s']", names[0], text)
			}
		}
	}
	return ""
}

// stripQuoted blanks quoted strings and identifiers so scans for tokens see
// only SQL syntax.
func stripQuoted(text string) string {
	var b strings.Builder
	var quote byte
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == quote {
				if i+1 < len(text) && text[i+1] == quote {
					i++
					continue
				}
				quote = 0
			}
			b.WriteByte(' ')
		case c == '\'' || c == '"':
			quote = c
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
```

- [ ] **Step 4: Implement parameter binding**

`internal/panel/params.go`:

```go
package panel

import (
	"errors"
	"fmt"
	"strings"
)

// bindParams replaces every $name outside quotes and comments with
// placeholders and returns the values in order. A name looked up as a list
// renders as (?, ?, …) for IN. describe is the same text with NULL in place
// of each placeholder, for DESCRIBE. A literal ? or a positional $1 is an
// error: values only ever enter through variables.
func bindParams(text string, lookup func(name string) (values []any, list bool, err error)) (query, describe string, args []any, err error) {
	var q, d strings.Builder
	write := func(s string) { q.WriteString(s); d.WriteString(s) }
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '\'' || c == '"':
			end := i + 1
			for end < len(text) {
				if text[end] == c {
					if end+1 < len(text) && text[end+1] == c {
						end += 2
						continue
					}
					break
				}
				end++
			}
			if end >= len(text) {
				return "", "", nil, errors.New("unterminated quoted text")
			}
			write(text[i : end+1])
			i = end
		case c == '-' && i+1 < len(text) && text[i+1] == '-':
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				end = len(text) - i
			}
			write(text[i : i+end])
			i += end - 1
		case c == '/' && i+1 < len(text) && text[i+1] == '*':
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				return "", "", nil, errors.New("unterminated comment")
			}
			write(text[i : i+2+end+2])
			i += 2 + end + 1
		case c == '?':
			return "", "", nil, errors.New("use $variables instead of ? placeholders")
		case c == '$' && i+1 < len(text) && text[i+1] >= '0' && text[i+1] <= '9':
			return "", "", nil, errors.New("positional parameters such as $1 are not allowed; use $name variables")
		case c == '$' && i+1 < len(text) && isIdentStart(text[i+1]):
			end := i + 1
			for end < len(text) && isIdentPart(text[end]) {
				end++
			}
			name := text[i+1 : end]
			values, list, lookupErr := lookup(name)
			if lookupErr != nil {
				return "", "", nil, lookupErr
			}
			if list {
				marks, nulls := make([]string, max(len(values), 1)), make([]string, max(len(values), 1))
				for j := range marks {
					marks[j], nulls[j] = "?", "NULL"
				}
				if len(values) == 0 {
					marks = []string{"NULL"}
				}
				q.WriteString("(" + strings.Join(marks, ", ") + ")")
				d.WriteString("(" + strings.Join(nulls, ", ") + ")")
			} else {
				if len(values) != 1 {
					return "", "", nil, fmt.Errorf("$%s has %d values where one is expected", name, len(values))
				}
				q.WriteString("?")
				d.WriteString("NULL")
			}
			args = append(args, values...)
			i = end - 1
		default:
			q.WriteByte(c)
			d.WriteByte(c)
		}
	}
	return q.String(), d.String(), args, nil
}

func isIdentStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isIdentPart(c byte) bool  { return isIdentStart(c) || c >= '0' && c <= '9' }
```

- [ ] **Step 5: Implement SQL panel macros**

`internal/panel/sqlpanel.go`:

```go
package panel

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

var (
	windowMacro = regexp.MustCompile(`\$__window\(\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)\s*\)`)
	bucketMacro = regexp.MustCompile(`\$__bucket\(\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)\s*\)`)
	macroToken  = regexp.MustCompile(`\$__([A-Za-z_]+)`)
)

// expandMacros rewrites $__window(col) into the binding the AGENTS.md
// timestamp rule requires and $__bucket(col) into a time_bucket call with the
// panel's interval. Only identifier arguments are accepted, so a macro can
// never carry SQL. Every SQL panel must filter time with $__window: the read
// window prunes files, and a panel that ignored it would silently see only
// the files that overlap the window.
func expandMacros(sql string, interval time.Duration) (string, error) {
	if !windowMacro.MatchString(sql) {
		return "", errors.New("SQL panels must filter time with $__window(time_column)")
	}
	out := windowMacro.ReplaceAllString(sql, `$1 >= $$__from::TIMESTAMP_NS::TIMESTAMPTZ_NS AND $1 < $$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS`)
	seconds := int64(interval / time.Second)
	if seconds <= 0 {
		seconds = 60
	}
	out = bucketMacro.ReplaceAllString(out, fmt.Sprintf(`time_bucket(INTERVAL '%d seconds', $1::TIMESTAMP)`, seconds))
	for _, m := range macroToken.FindAllStringSubmatch(stripQuoted(out), -1) {
		if m[1] != "from" && m[1] != "to" {
			return "", fmt.Errorf("unknown macro $__%s; use $__window(column) or $__bucket(column)", m[1])
		}
	}
	return out, nil
}
```

- [ ] **Step 6: Implement `Check`**

`internal/panel/check.go`:

```go
package panel

import (
	"context"
	"fmt"
	"time"
)

// Parser is the slice of the query engine that checks SQL text. *query.Duck
// implements it.
type Parser interface {
	ParseSQL(ctx context.Context, query string) (map[string]any, error)
	PrepareTelemetrySQL(ctx context.Context, query, describe string, maxRows int) (string, []bool, error)
}

// Checked is what the database-backed checks produced.
type Checked struct {
	Filters    map[string][]Filter  // panel id → filters
	VarFilters map[string][]Filter  // variable name → filters
	Measures   map[string][]Measure // panel id → measures
}

// Check runs Validate and then the checks that need DuckDB's parser: every
// filter expression and every SQL panel. It reports all problems it finds.
// The dashboard must already be normalized.
func Check(ctx context.Context, parser Parser, d *Dashboard) (*Checked, Problems) {
	problems := Validate(d)
	checked := &Checked{Filters: map[string][]Filter{}, VarFilters: map[string][]Filter{}, Measures: map[string][]Measure{}}
	earlier := map[string]Variable{}
	for i, v := range d.Variables {
		if v.Kind == "query" {
			if sig, ok := lookupSignal(v.From); ok {
				for j, expr := range v.Where {
					f, err := checkFilter(ctx, parser, sig, earlier, expr)
					if err != nil {
						problems.add(fmt.Sprintf("variables[%d].where[%d]", i, j), err.Error())
						continue
					}
					checked.VarFilters[v.Name] = append(checked.VarFilters[v.Name], f)
				}
			}
		}
		earlier[v.Name] = v
	}
	for i := range d.Panels {
		p := &d.Panels[i]
		path := fmt.Sprintf("panels[%d]", i)
		switch {
		case p.Query != nil:
			sig, ok := lookupSignal(p.Query.From)
			if !ok {
				continue
			}
			var ignored Problems
			checked.Measures[p.ID] = parseMeasures(sig, p.Query.Measures, "", &ignored)
			for j, expr := range p.Query.Where {
				f, err := checkFilter(ctx, parser, sig, earlier, expr)
				if err != nil {
					problems.add(fmt.Sprintf("%s.query.where[%d]", path, j), err.Error())
					continue
				}
				checked.Filters[p.ID] = append(checked.Filters[p.ID], f)
			}
		case p.SQL != "":
			if err := checkSQLPanel(ctx, parser, p, earlier); err != nil {
				problems.add(path+".sql", err.Error())
			}
		}
	}
	return checked, problems
}

func checkSQLPanel(ctx context.Context, parser Parser, p *Panel, vars map[string]Variable) error {
	expanded, err := expandMacros(p.SQL, time.Minute)
	if err != nil {
		return err
	}
	query, describe, _, err := bindParams(expanded, func(name string) ([]any, bool, error) {
		if name == "__from" || name == "__to" {
			return []any{time.Time{}}, false, nil
		}
		if _, ok := vars[name]; !ok {
			return nil, false, fmt.Errorf("$%s is not a dashboard variable", name)
		}
		return []any{""}, false, nil
	})
	if err != nil {
		return err
	}
	_, _, err = parser.PrepareTelemetrySQL(ctx, query, describe, 1)
	return err
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `just test ./internal/panel/...`
Expected: PASS. If a DuckDB node shape differs from the walker (for example a cast id name), print the node with `t.Logf("%v", node)` in a scratch test, adjust the allowlist key, and keep the test cases unchanged.

- [ ] **Step 8: Commit**

```bash
git add internal/panel
git commit -m "feat(panel): guard filters with DuckDB's parser and bind variables as parameters"
```

---

### Task 4: Time windows, buckets and the query compiler

**Files:**
- Create: `internal/panel/timerange.go`, `internal/panel/bucket.go`, `internal/panel/compile.go`
- Test: `internal/panel/timerange_test.go`, `internal/panel/compile_test.go`

**Interfaces:**
- Consumes: `Panel`, `Query`, `Measure`, `Filter`, `FieldRef`, `bindParams`, `quoteIdent`, `ranges`, `buckets`, `ParseSpan` (Tasks 1, 3).
- Produces:
  - `type Value struct { All bool; Values []string }` with JSON as a string, a list of strings, or `"$__all"`
  - `type Scope struct { Start, End time.Time; Interval time.Duration; Vars map[string]Value }`
  - `type Column struct { Name, Type, Role, Unit string }` (JSON `name`, `type` = `time|number|string|json`, `role` = `time|dimension|measure`, `unit`)
  - `type Compiled struct { SQL string; Args []any; Columns []Column }`
  - `func resolveWindow(t Time, override *PanelTime, now time.Time, maxWindow time.Duration) (time.Time, time.Time, error)`
  - `func AutoInterval(window time.Duration, widthPx int) time.Duration`; `func bucketInterval(bucket string, window time.Duration, widthPx int) time.Duration`; `func formatInterval(d time.Duration) string`
  - `func buildWhere(sig *signal, filters []Filter, scope Scope) (string, []any, error)`
  - `func compileQuery(p *Panel, measures []Measure, filters []Filter, scope Scope) (Compiled, error)`
  - `const maxSeriesPoints = 2000`

- [ ] **Step 1: Write the failing tests**

`internal/panel/timerange_test.go`:

```go
package panel

import (
	"strings"
	"testing"
	"time"
)

func TestResolveWindow(t *testing.T) {
	now := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)
	from, to := now.Add(-3*time.Hour), now.Add(-2*time.Hour)
	week := 7 * 24 * time.Hour
	cases := []struct {
		name       string
		t          Time
		override   *PanelTime
		start, end time.Time
		err        string
	}{
		{"relative", Time{Range: "1h"}, nil, now.Add(-time.Hour), now, ""},
		{"absolute", Time{From: &from, To: &to}, nil, from, to, ""},
		{"panel range clamped to retention", Time{Range: "1h"}, &PanelTime{Range: "30d"}, now.Add(-week), now, ""},
		{"panel shift", Time{Range: "1h"}, &PanelTime{Shift: "1d"}, now.Add(-25 * time.Hour), now.Add(-24 * time.Hour), ""},
		{"from after to", Time{From: &to, To: &from}, nil, time.Time{}, time.Time{}, "start before it ends"},
		{"bad range", Time{Range: "90m"}, nil, time.Time{}, time.Time{}, "unsupported range"},
	}
	for _, tc := range cases {
		start, end, err := resolveWindow(tc.t, tc.override, now, week)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: err = %v", tc.name, err)
			}
			continue
		}
		if err != nil || !start.Equal(tc.start) || !end.Equal(tc.end) {
			t.Errorf("%s: got %v – %v (%v), want %v – %v", tc.name, start, end, err, tc.start, tc.end)
		}
	}
}

func TestAutoInterval(t *testing.T) {
	cases := []struct {
		window time.Duration
		width  int
		want   time.Duration
	}{
		{5 * time.Minute, 800, 10 * time.Second},
		{time.Hour, 800, 30 * time.Second},
		{time.Hour, 0, 30 * time.Second},
		{24 * time.Hour, 800, 10 * time.Minute},
		{30 * 24 * time.Hour, 800, 6 * time.Hour},
	}
	for _, tc := range cases {
		if got := AutoInterval(tc.window, tc.width); got != tc.want {
			t.Errorf("AutoInterval(%v, %d) = %v, want %v", tc.window, tc.width, got, tc.want)
		}
	}
	if got := bucketInterval("10s", 30*24*time.Hour, 800); time.Duration(30*24)*time.Hour/got > maxSeriesPoints {
		t.Errorf("fixed bucket exceeds the point cap: %v", got)
	}
	if formatInterval(90*time.Second) != "90s" || formatInterval(5*time.Minute) != "5m" || formatInterval(24*time.Hour) != "1d" {
		t.Error("formatInterval")
	}
}

func TestValueJSON(t *testing.T) {
	var v Value
	for text, want := range map[string]Value{`"checkout"`: {Values: []string{"checkout"}}, `"$__all"`: {All: true}, `["a","b"]`: {Values: []string{"a", "b"}}, `[]`: {Values: []string{}}} {
		if err := v.UnmarshalJSON([]byte(text)); err != nil || v.All != want.All || strings.Join(v.Values, ",") != strings.Join(want.Values, ",") {
			t.Errorf("%s → %+v %v", text, v, err)
		}
	}
	if err := v.UnmarshalJSON([]byte(`3`)); err == nil {
		t.Error("number accepted")
	}
}
```

`internal/panel/compile_test.go`:

```go
package panel

import (
	"reflect"
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
	return Filter{Expr: expr, Params: params, InParams: map[string]bool{}}
}

func TestCompileTimeseriesBindsVariables(t *testing.T) {
	p := &Panel{ID: "latency", Viz: "timeseries", Query: &Query{From: "spans", Bucket: "1m"}}
	scope := Scope{Start: compileStart, End: compileEnd, Interval: time.Minute, Vars: map[string]Value{"service": {Values: []string{"x'); DROP TABLE spans; --"}}}}
	got, err := compileQuery(p, measuresOf(t, "spans", "p50(duration_ms)", "p95(duration_ms)"), []Filter{filter("service = $service", "service"), filter("kind = 'SPAN_KIND_SERVER'")}, scope)
	if err != nil {
		t.Fatal(err)
	}
	want := `WITH base AS (SELECT * FROM spans WHERE ` + spanWindow + ` AND (service = ?) AND (kind = 'SPAN_KIND_SERVER')) SELECT epoch_ms(time_bucket(INTERVAL '60 seconds', "start_time"::TIMESTAMP))::BIGINT AS "_t", quantile_cont("duration_ms", 0.5)::DOUBLE AS "p50", quantile_cont("duration_ms", 0.95)::DOUBLE AS "p95" FROM base GROUP BY time_bucket(INTERVAL '60 seconds', "start_time"::TIMESTAMP) ORDER BY "_t" LIMIT 18000`
	if got.SQL != want {
		t.Fatalf("sql\n got: %s\nwant: %s", got.SQL, want)
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
	bucket := `time_bucket(INTERVAL '300 seconds', "start_time"::TIMESTAMP)`
	other := `CASE WHEN "service" IN (SELECT d FROM top) THEN "service" ELSE 'Other' END`
	want := `WITH base AS (SELECT * FROM spans WHERE ` + spanWindow + `), top AS (SELECT "service" AS d FROM base GROUP BY 1 ORDER BY count(*) DESC LIMIT 3) SELECT epoch_ms(` + bucket + `)::BIGINT AS "_t", coalesce(` + other + `, '') AS "service", count(*) / 300.0 AS "rate" FROM base GROUP BY ` + bucket + `, ` + other + ` ORDER BY "_t", 2 LIMIT 8000`
	if got.SQL != want {
		t.Fatalf("sql\n got: %s\nwant: %s", got.SQL, want)
	}
}

func TestCompileTableDropsAllAndSorts(t *testing.T) {
	p := &Panel{ID: "routes", Viz: "table", Query: &Query{From: "spans", By: []string{"attributes['http.route']"}, Sort: "count", Limit: 5}}
	scope := Scope{Start: compileStart, End: compileEnd, Vars: map[string]Value{"service": {All: true}}}
	got, err := compileQuery(p, measuresOf(t, "spans", "error_rate()", "count()"), []Filter{filter("service = $service", "service")}, scope)
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
	in := Filter{Expr: "http_route IN $routes", Params: []string{"routes"}, InParams: map[string]bool{"routes": true}}
	got, err := compileQuery(p, measuresOf(t, "spans", "count()"), []Filter{in}, Scope{Start: compileStart, End: compileEnd, Vars: map[string]Value{"routes": {Values: []string{"/a", "/b"}}}})
	if err != nil {
		t.Fatal(err)
	}
	want := `WITH base AS (SELECT * FROM spans WHERE ` + spanWindow + ` AND (http_route IN (?, ?))) SELECT count(*)::DOUBLE AS "count" FROM base ORDER BY "count" DESC NULLS LAST LIMIT 1000`
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
	bucket := `time_bucket(INTERVAL '60 seconds', "time"::TIMESTAMP)`
	other := `CASE WHEN "severity" IN (SELECT d FROM top) THEN "severity" ELSE 'Other' END`
	want := `WITH base AS (SELECT * FROM logs WHERE "time" >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND "time" < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS), top AS (SELECT "severity" AS d FROM base GROUP BY 1 ORDER BY count(*) DESC LIMIT 8) SELECT epoch_ms(` + bucket + `)::BIGINT AS "_t", coalesce(` + other + `, '') AS "severity", 100.0 * count(*) / sum(count(*)) OVER (PARTITION BY ` + bucket + `) AS "share" FROM base GROUP BY ` + bucket + `, ` + other + ` ORDER BY "_t", 2 LIMIT 18000`
	if got.SQL != want {
		t.Fatalf("sql\n got: %s\nwant: %s", got.SQL, want)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `just test ./internal/panel/... -run 'TestResolveWindow|TestAutoInterval|TestValueJSON|TestCompile'`
Expected: FAIL — undefined `resolveWindow`, `AutoInterval`, `Value`, `compileQuery`.

- [ ] **Step 3: Implement windows and buckets**

`internal/panel/timerange.go`:

```go
package panel

import (
	"errors"
	"fmt"
	"time"
)

// resolveWindow turns a dashboard time, optionally overridden by a panel,
// into a concrete UTC range no longer than retention. A range longer than
// retention keeps its end and loses its oldest part rather than failing.
func resolveWindow(t Time, override *PanelTime, now time.Time, maxWindow time.Duration) (time.Time, time.Time, error) {
	var start, end time.Time
	if t.From != nil && t.To != nil {
		start, end = t.From.UTC(), t.To.UTC()
	} else {
		span, ok := ranges[t.Range]
		if !ok {
			return time.Time{}, time.Time{}, fmt.Errorf("unsupported range %q", t.Range)
		}
		end = now.UTC()
		start = end.Add(-span)
	}
	if override != nil {
		if override.Range != "" {
			span, ok := ranges[override.Range]
			if !ok {
				return time.Time{}, time.Time{}, fmt.Errorf("unsupported range %q", override.Range)
			}
			start = end.Add(-span)
		}
		if override.Shift != "" {
			shift, ok := ParseSpan(override.Shift)
			if !ok {
				return time.Time{}, time.Time{}, fmt.Errorf("unsupported shift %q", override.Shift)
			}
			start, end = start.Add(-shift), end.Add(-shift)
		}
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, errors.New("the time range must start before it ends")
	}
	if maxWindow > 0 && end.Sub(start) > maxWindow {
		start = end.Add(-maxWindow)
	}
	return start, end, nil
}
```

`internal/panel/bucket.go`:

```go
package panel

import (
	"fmt"
	"time"
)

const maxSeriesPoints = 2000

var autoIntervals = []time.Duration{
	10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute,
	30 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
}

// AutoInterval picks the finest standard interval, 10 seconds or longer,
// that keeps a series at one point per four pixels or fewer, so a 24-hour
// line stays legible instead of becoming a band.
func AutoInterval(window time.Duration, widthPx int) time.Duration {
	if widthPx <= 0 {
		widthPx = 640
	}
	points := time.Duration(max(widthPx/4, 30))
	for _, interval := range autoIntervals {
		if window/interval <= points {
			return interval
		}
	}
	return autoIntervals[len(autoIntervals)-1]
}

// bucketInterval resolves a query's bucket. A fixed bucket that would exceed
// the point cap for this window widens to the next standard interval.
func bucketInterval(bucket string, window time.Duration, widthPx int) time.Duration {
	switch bucket {
	case "":
		return 0
	case "auto":
		return AutoInterval(window, widthPx)
	}
	interval := buckets[bucket]
	for _, candidate := range autoIntervals {
		if candidate >= interval && window/candidate <= maxSeriesPoints {
			return candidate
		}
	}
	return autoIntervals[len(autoIntervals)-1]
}

func formatInterval(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return fmt.Sprintf("%ds", d/time.Second)
	}
}
```

- [ ] **Step 4: Implement the compiler**

`internal/panel/compile.go`:

```go
package panel

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Value is a variable's current value. JSON: a string, a list of strings, or
// "$__all".
type Value struct {
	All    bool
	Values []string
}

func valueOf(s string) Value {
	if s == AllValue {
		return Value{All: true}
	}
	return Value{Values: []string{s}}
}

func (v Value) MarshalJSON() ([]byte, error) {
	if v.All {
		return json.Marshal(AllValue)
	}
	if len(v.Values) == 1 {
		return json.Marshal(v.Values[0])
	}
	if v.Values == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(v.Values)
}

func (v *Value) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*v = valueOf(one)
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return errors.New("a variable value is a string or a list of strings")
	}
	if len(many) == 1 && many[0] == AllValue {
		*v = Value{All: true}
		return nil
	}
	*v = Value{Values: many}
	return nil
}

// Scope is everything a compiled query depends on besides the spec.
type Scope struct {
	Start, End time.Time
	Interval   time.Duration // bucket width; zero means no time grouping
	Vars       map[string]Value
}

// Column describes one frame column.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Role string `json:"role"`
	Unit string `json:"unit,omitempty"`
}

// Compiled is one statement ready to run.
type Compiled struct {
	SQL     string
	Args    []any
	Columns []Column
}

// dropped reports whether a filter references a variable set to All or left
// empty; such a filter is removed rather than compared with NULL.
func (s Scope) dropped(f Filter) bool {
	for _, name := range f.Params {
		v := s.Vars[name]
		if v.All || len(v.Values) == 0 {
			return true
		}
	}
	return false
}

func (s Scope) lookup(f Filter) func(string) ([]any, bool, error) {
	return func(name string) ([]any, bool, error) {
		v, ok := s.Vars[name]
		if !ok {
			return nil, false, fmt.Errorf("$%s has no value", name)
		}
		values := make([]any, len(v.Values))
		for i, value := range v.Values {
			values[i] = value
		}
		return values, f.InParams[name], nil
	}
}

// buildWhere renders the read window and the active filters. The window is
// always the first predicate: the snapshot binder prunes files by it.
func buildWhere(sig *signal, filters []Filter, scope Scope) (string, []any, error) {
	column := quoteIdent(sig.time)
	clauses := []string{fmt.Sprintf("%s >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND %s < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS", column, column)}
	args := []any{scope.Start.UTC(), scope.End.UTC()}
	for _, f := range filters {
		if scope.dropped(f) {
			continue
		}
		text, _, values, err := bindParams(f.Expr, scope.lookup(f))
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, "("+text+")")
		args = append(args, values...)
	}
	return strings.Join(clauses, " AND "), args, nil
}

// compileQuery turns a structured query into one statement. The bucket is
// selected as "_t" and named "time" in the frame, so it never collides with a
// signal's own time column.
func compileQuery(p *Panel, measures []Measure, filters []Filter, scope Scope) (Compiled, error) {
	q := p.Query
	sig, ok := lookupSignal(q.From)
	if !ok {
		return Compiled{}, fmt.Errorf("unknown signal %q", q.From)
	}
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return Compiled{}, err
	}
	dims := make([]FieldRef, 0, len(q.By))
	for _, by := range q.By {
		ref, err := sig.field(by)
		if err != nil {
			return Compiled{}, err
		}
		dims = append(dims, ref)
	}
	var selects, groups []string
	var columns []Column
	bucket := ""
	if scope.Interval > 0 {
		bucket = fmt.Sprintf("time_bucket(INTERVAL '%d seconds', %s::TIMESTAMP)", int64(scope.Interval/time.Second), quoteIdent(sig.time))
		selects = append(selects, "epoch_ms("+bucket+`)::BIGINT AS "_t"`)
		groups = append(groups, bucket)
		columns = append(columns, Column{Name: "time", Type: "time", Role: "time"})
	}
	top := 0
	if bucket != "" && len(dims) == 1 {
		top = p.Top()
	}
	for i, d := range dims {
		expr := d.stringSQL()
		if top > 0 && i == 0 {
			expr = fmt.Sprintf("CASE WHEN %s IN (SELECT d FROM top) THEN %s ELSE 'Other' END", expr, expr)
		}
		selects = append(selects, fmt.Sprintf("coalesce(%s, '') AS %s", expr, quoteIdent(d.alias())))
		groups = append(groups, expr)
		columns = append(columns, Column{Name: d.alias(), Type: "string", Role: "dimension"})
	}
	seconds := scope.Interval.Seconds()
	if seconds == 0 {
		seconds = scope.End.Sub(scope.Start).Seconds()
	}
	partition := ""
	if bucket != "" {
		partition = "PARTITION BY " + bucket
	}
	for _, m := range measures {
		selects = append(selects, measureSQL(m, sig, seconds, partition)+" AS "+quoteIdent(m.Alias))
		unit := m.Unit
		if p.Unit != "" {
			unit = p.Unit
		}
		columns = append(columns, Column{Name: m.Alias, Type: "number", Role: "measure", Unit: unit})
	}
	var b strings.Builder
	b.WriteString("WITH base AS (SELECT * FROM " + sig.name + " WHERE " + where + ")")
	if top > 0 {
		fmt.Fprintf(&b, ", top AS (SELECT %s AS d FROM base GROUP BY 1 ORDER BY count(*) DESC LIMIT %d)", dims[0].stringSQL(), top)
	}
	b.WriteString(" SELECT " + strings.Join(selects, ", ") + " FROM base")
	if len(groups) > 0 {
		b.WriteString(" GROUP BY " + strings.Join(groups, ", "))
	}
	b.WriteString(orderAndLimit(p, measures, bucket != "", len(dims)))
	return Compiled{SQL: b.String(), Args: args, Columns: columns}, nil
}

func measureSQL(m Measure, sig *signal, seconds float64, partition string) string {
	switch m.Func {
	case "count":
		return "count(*)::DOUBLE"
	case "rate":
		return "count(*) / " + sqlFloat(seconds)
	case "error_rate":
		if sig.name == "logs" {
			return "100.0 * avg(CASE WHEN upper(severity) IN ('ERROR', 'FATAL', 'CRITICAL') OR severity_number >= 17 THEN 1.0 ELSE 0.0 END)"
		}
		return "100.0 * avg(CASE WHEN status IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END)"
	case "share":
		return fmt.Sprintf("100.0 * count(*) / sum(count(*)) OVER (%s)", partition)
	case "avg", "min", "max", "sum":
		return fmt.Sprintf("%s(%s)::DOUBLE", m.Func, m.Field.numberSQL())
	case "last":
		return fmt.Sprintf("arg_max(%s, %s)::DOUBLE", m.Field.numberSQL(), quoteIdent(sig.time))
	case "count_distinct":
		return fmt.Sprintf("count(DISTINCT %s)::DOUBLE", m.Field.stringSQL())
	default:
		return fmt.Sprintf("quantile_cont(%s, %s)::DOUBLE", m.Field.numberSQL(), strconv.FormatFloat(m.Q, 'f', -1, 64))
	}
}

func sqlFloat(v float64) string {
	text := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.Contains(text, ".") {
		text += ".0"
	}
	return text
}

func orderAndLimit(p *Panel, measures []Measure, bucketed bool, dims int) string {
	if bucketed {
		order := ` ORDER BY "_t"`
		if dims > 0 {
			order += ", 2"
		}
		return order + fmt.Sprintf(" LIMIT %d", maxSeriesPoints*(p.Top()+1))
	}
	limit := p.Query.Limit
	if limit == 0 {
		switch p.Viz {
		case "bar":
			limit = 10
		case "table":
			limit = 100
		default:
			limit = 1000
		}
	}
	if len(measures) == 0 {
		return fmt.Sprintf(" LIMIT %d", limit)
	}
	alias, direction := measures[0].Alias, "DESC"
	if p.Query.Sort != "" {
		alias = strings.TrimPrefix(p.Query.Sort, "+")
		if strings.HasPrefix(p.Query.Sort, "+") {
			direction = "ASC"
		}
	}
	return fmt.Sprintf(" ORDER BY %s %s NULLS LAST LIMIT %d", quoteIdent(alias), direction, limit)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `just test ./internal/panel/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/panel
git commit -m "feat(panel): compile structured queries to snapshot-bound DuckDB SQL"
```

---

### Task 5: Executor, frames, comparison, empty-panel diagnosis and variables

**Files:**
- Create: `internal/panel/frame.go`, `internal/panel/exec.go`, `internal/panel/diagnose.go`, `internal/panel/variables.go`
- Test: `internal/panel/exec_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–4; `queryrows.Rows`, `queryrows.WithWindow`.
- Produces:
  - `type Engine interface { Parser; QueryContext(ctx context.Context, query string, args ...any) (queryrows.Rows, error) }`
  - `type Executor struct{…}`; `func NewExecutor(engine Engine, retentionDays int) *Executor`
  - `func (e *Executor) Validate(ctx context.Context, d *Dashboard) error` (normalizes, returns `Problems` or nil)
  - `type RunRequest struct { Dashboard Dashboard; Panels []string; Time *Time; Vars map[string]Value; Widths map[string]int; Compare *bool }` (JSON `dashboard`, `panels`, `time`, `vars`, `widths`, `compare`)
  - `type Result struct { ID, Status string; Frame, Previous *Frame; Error, Diagnosis, SQL, Interval string; ElapsedMS int64 }` (JSON `id`, `status`, `frame`, `previous`, `error`, `diagnosis`, `sql`, `interval`, `elapsed_ms`); statuses `ok`, `empty`, `error`
  - `type Frame struct { Columns []Column; Values [][]any; Rows int; Totals []any; Truncated bool }` (JSON `columns`, `values`, `rows`, `totals`, `truncated`)
  - `func (e *Executor) Run(ctx context.Context, req RunRequest) ([]Result, error)`
  - `type Option struct { Value string; Count int64 }`; `type ResolveRequest struct { Dashboard Dashboard; Time *Time; Vars map[string]Value }`
  - `func (e *Executor) ResolveVariables(ctx context.Context, req ResolveRequest) (map[string][]Option, error)`

- [ ] **Step 1: Write the failing tests**

`internal/panel/exec_test.go`:

```go
package panel

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func newFixtureExecutor(t *testing.T) *Executor {
	t.Helper()
	d, repo := newTestEngine(t)
	commit(t, repo, shopSpans(), nil)
	e := NewExecutor(d, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	return e
}

func shopDashboard() Dashboard {
	return Dashboard{
		Name: "Shop",
		Time: Time{Range: "1h"},
		Variables: []Variable{
			{Name: "service", Kind: "query", From: "spans", Field: "service", Default: "checkout"},
			{Name: "route", Kind: "query", From: "spans", Field: "http_route", Where: []string{"service = $service"}, IncludeAll: true, Default: AllValue},
		},
		Panels: []Panel{
			{ID: "latency", Title: "Latency", Viz: "timeseries", Query: &Query{From: "spans", Where: []string{"service = $service", "http_route = $route"}, Measures: []string{"p95(duration_ms)"}, Bucket: "5m"}},
			{ID: "requests", Title: "Requests", Viz: "stat", Query: &Query{From: "spans", Where: []string{"service = $service"}, Measures: []string{"count()"}}},
			{ID: "by_route", Title: "By route", Viz: "bar", Query: &Query{From: "spans", Where: []string{"service = $service"}, Measures: []string{"count()"}, By: []string{"http_route"}}},
			{ID: "services", Title: "Services", Viz: "table", SQL: "SELECT service, count(*) AS n FROM spans WHERE $__window(start_time) GROUP BY 1 ORDER BY 2 DESC"},
			{ID: "notes", Title: "Notes", Viz: "text", Content: "Checkout is the money path."},
		},
	}
}

func byID(results []Result) map[string]Result {
	out := map[string]Result{}
	for _, r := range results {
		out[r.ID] = r
	}
	return out
}

func TestRunProducesFrames(t *testing.T) {
	e := newFixtureExecutor(t)
	results, err := e.Run(t.Context(), RunRequest{Dashboard: shopDashboard(), Widths: map[string]int{"latency": 800}})
	if err != nil {
		t.Fatal(err)
	}
	got := byID(results)
	latency := got["latency"]
	if latency.Status != "ok" || latency.Interval != "5m" || latency.Frame.Rows != 12 {
		t.Fatalf("latency = %+v", latency)
	}
	p95 := latency.Frame.Values[1]
	if p95[0].(float64) >= p95[len(p95)-1].(float64) {
		t.Fatalf("checkout should slow down after 12:30: %v", p95)
	}
	requests := got["requests"]
	if requests.Frame.Totals[1].(float64) != 120 {
		t.Fatalf("requests totals = %v", requests.Frame.Totals)
	}
	if got["by_route"].Frame.Rows != 2 || got["by_route"].Frame.Values[0][0] == "" {
		t.Fatalf("by_route = %+v", got["by_route"].Frame)
	}
	services := got["services"].Frame
	if services.Rows != 2 || services.Columns[0].Role != "dimension" || services.Columns[1].Role != "measure" || services.Values[0][0] != "checkout" {
		t.Fatalf("services = %+v", services)
	}
	if got["notes"].Status != "ok" || got["notes"].Frame != nil {
		t.Fatalf("notes = %+v", got["notes"])
	}
}

func TestRunComparesWithThePreviousPeriod(t *testing.T) {
	e := newFixtureExecutor(t)
	compare := true
	results, err := e.Run(t.Context(), RunRequest{Dashboard: shopDashboard(), Panels: []string{"requests"}, Compare: &compare})
	if err != nil {
		t.Fatal(err)
	}
	r := results[0]
	if r.Previous == nil || r.Previous.Totals[1].(float64) != 0 {
		t.Fatalf("previous = %+v", r.Previous)
	}
}

func TestRunExplainsEmptyPanels(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = []Panel{{ID: "nope", Title: "Nope", Viz: "bar", Query: &Query{From: "spans", Where: []string{"service = $service", "http_route = '/nope'"}, Measures: []string{"count()"}, By: []string{"http_route"}}}}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	r := results[0]
	if r.Status != "empty" || !strings.Contains(r.Diagnosis, "/nope") || !strings.Contains(r.Diagnosis, "/cart") {
		t.Fatalf("result = %+v", r)
	}
}

func TestRunIsolatesPanelErrors(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = append(d.Panels, Panel{ID: "broken", Title: "Broken", Viz: "stat", Query: &Query{From: "spans", Where: []string{"service::INTEGER > 1"}, Measures: []string{"count()"}}})
	results, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	got := byID(results)
	if got["broken"].Status != "error" || got["broken"].Error == "" {
		t.Fatalf("broken = %+v", got["broken"])
	}
	if got["latency"].Status != "ok" {
		t.Fatalf("latency failed alongside: %+v", got["latency"])
	}
}

func TestRunBindsHostileValuesAsData(t *testing.T) {
	e := newFixtureExecutor(t)
	results, err := e.Run(t.Context(), RunRequest{Dashboard: shopDashboard(), Panels: []string{"requests"}, Vars: map[string]Value{"service": {Values: []string{"x'); DROP TABLE spans; --"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status == "error" || results[0].Frame.Totals[1].(float64) != 0 {
		t.Fatalf("hostile value = %+v", results[0])
	}
	again, err := e.Run(t.Context(), RunRequest{Dashboard: shopDashboard(), Panels: []string{"requests"}})
	if err != nil || again[0].Frame.Totals[1].(float64) != 120 {
		t.Fatalf("spans damaged: %+v %v", again, err)
	}
}

func TestRunRejectsInvalidSpecs(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels[0].Query.Measures = []string{"p95(duraton_ms)"}
	_, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	var problems Problems
	if !errors.As(err, &problems) || problems[0].Path != "panels[0].query.measures[0]" {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.Run(t.Context(), RunRequest{Dashboard: shopDashboard(), Panels: []string{"missing"}}); err == nil {
		t.Fatal("unknown panel id accepted")
	}
}

func TestResolveVariablesFollowsDependencies(t *testing.T) {
	e := newFixtureExecutor(t)
	options, err := e.ResolveVariables(t.Context(), ResolveRequest{Dashboard: shopDashboard()})
	if err != nil {
		t.Fatal(err)
	}
	if len(options["service"]) != 2 || options["service"][0].Value != "checkout" || options["service"][0].Count != 120 {
		t.Fatalf("service options = %+v", options["service"])
	}
	routes := []string{}
	for _, o := range options["route"] {
		routes = append(routes, o.Value)
	}
	if strings.Join(routes, ",") != "/cart,/quote" && strings.Join(routes, ",") != "/quote,/cart" {
		t.Fatalf("route options for checkout = %v", routes)
	}
	options, err = e.ResolveVariables(t.Context(), ResolveRequest{Dashboard: shopDashboard(), Vars: map[string]Value{"service": {Values: []string{"frontend"}}}})
	if err != nil || len(options["route"]) != 1 || options["route"][0].Value != "/api/cart" {
		t.Fatalf("route options for frontend = %+v %v", options["route"], err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `just test ./internal/panel/... -run 'TestRun|TestResolveVariables'`
Expected: FAIL — undefined `NewExecutor`, `RunRequest`, `Result`.

- [ ] **Step 3: Implement frames**

`internal/panel/frame.go`:

```go
package panel

import (
	"database/sql"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

// Frame is a columnar query result: one array of values per column.
type Frame struct {
	Columns   []Column `json:"columns"`
	Values    [][]any  `json:"values"`
	Rows      int      `json:"rows"`
	Totals    []any    `json:"totals,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
}

const (
	maxFrameRows = maxSeriesPoints * 21
	sqlPanelRows = 1000
)

func newFrame(columns []Column) *Frame {
	f := &Frame{Columns: columns, Values: make([][]any, len(columns))}
	for i := range f.Values {
		f.Values[i] = []any{}
	}
	return f
}

// scanFrame reads a compiled query whose column types are known.
func scanFrame(rows queryrows.Rows, columns []Column, maxRows int) (*Frame, error) {
	defer rows.Close()
	f := newFrame(columns)
	dest := make([]any, len(columns))
	for rows.Next() {
		if f.Rows >= maxRows {
			f.Truncated = true
			break
		}
		for i, c := range columns {
			switch c.Type {
			case "time":
				dest[i] = new(sql.NullInt64)
			case "number":
				dest[i] = new(sql.NullFloat64)
			default:
				dest[i] = new(sql.NullString)
			}
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		for i := range columns {
			f.Values[i] = append(f.Values[i], nullValue(dest[i]))
		}
		f.Rows++
	}
	return f, rows.Err()
}

func nullValue(v any) any {
	switch n := v.(type) {
	case *sql.NullInt64:
		if n.Valid {
			return n.Int64
		}
	case *sql.NullFloat64:
		if n.Valid && !math.IsNaN(n.Float64) && !math.IsInf(n.Float64, 0) {
			return n.Float64
		}
	case *sql.NullString:
		if n.Valid {
			return n.String
		}
	}
	return nil
}

// scanDynamicFrame reads an SQL panel, inferring each column's type from its
// first non-null value: timestamps become the time column, numbers become
// measures, everything else a dimension. serialized columns hold JSON text.
func scanDynamicFrame(rows queryrows.Rows, serialized []bool, maxRows int) (*Frame, error) {
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var raw [][]any
	truncated := false
	for rows.Next() {
		if len(raw) >= maxRows {
			truncated = true
			break
		}
		values := make([]any, len(names))
		pointers := make([]any, len(names))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		raw = append(raw, values)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	f := &Frame{Rows: len(raw), Truncated: truncated, Values: make([][]any, len(names))}
	timeTaken := false
	for i, name := range names {
		column := Column{Name: name, Type: "string", Role: "dimension"}
		if i < len(serialized) && serialized[i] {
			column.Type = "json"
		}
		for _, row := range raw {
			if row[i] != nil {
				column.Type = dynamicType(row[i], column.Type)
				break
			}
		}
		switch {
		case column.Type == "time" && !timeTaken:
			column.Role, timeTaken = "time", true
		case column.Type == "number":
			column.Role = "measure"
		}
		f.Columns = append(f.Columns, column)
		values := make([]any, len(raw))
		for r, row := range raw {
			values[r] = convertDynamic(row[i], column.Type)
		}
		f.Values[i] = values
	}
	return f, nil
}

type floater interface{ Float64() float64 }

func dynamicType(v any, fallback string) string {
	switch v.(type) {
	case time.Time:
		return "time"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, *big.Int, floater:
		return "number"
	}
	if fallback == "json" {
		return "json"
	}
	return "string"
}

func convertDynamic(v any, kind string) any {
	if v == nil {
		return nil
	}
	switch kind {
	case "time":
		if t, ok := v.(time.Time); ok {
			return t.UnixMilli()
		}
	case "number":
		var f float64
		switch n := v.(type) {
		case int:
			f = float64(n)
		case int8:
			f = float64(n)
		case int16:
			f = float64(n)
		case int32:
			f = float64(n)
		case int64:
			f = float64(n)
		case uint:
			f = float64(n)
		case uint8:
			f = float64(n)
		case uint16:
			f = float64(n)
		case uint32:
			f = float64(n)
		case uint64:
			f = float64(n)
		case float32:
			f = float64(n)
		case float64:
			f = n
		case *big.Int:
			f, _ = new(big.Float).SetInt(n).Float64()
		case floater:
			f = n.Float64()
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
		return f
	}
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return fmt.Sprint(s)
	}
}

// totalsOf turns a one-row aggregate into per-column totals.
func totalsOf(f *Frame) []any {
	out := make([]any, len(f.Columns))
	if f.Rows == 0 {
		return out
	}
	for i := range f.Columns {
		out[i] = f.Values[i][0]
	}
	return out
}
```

Note for stat totals: the series frame has columns `[time, measure]` and the totals query has columns `[measure]`. `runPanel` aligns them so `Totals[i]` matches `Frame.Columns[i]` (time slot is `nil`).

- [ ] **Step 4: Implement the executor**

`internal/panel/exec.go`:

```go
package panel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
	"golang.org/x/sync/errgroup"
)

// Engine is the query engine the executor runs on. *query.Duck implements it;
// QueryContext binds the Parquet snapshot for the window in the context.
type Engine interface {
	Parser
	QueryContext(ctx context.Context, query string, args ...any) (queryrows.Rows, error)
}

const (
	StatusOK    = "ok"
	StatusEmpty = "empty"
	StatusError = "error"
)

type RunRequest struct {
	Dashboard Dashboard        `json:"dashboard"`
	Panels    []string         `json:"panels,omitempty"`
	Time      *Time            `json:"time,omitempty"`
	Vars      map[string]Value `json:"vars,omitempty"`
	Widths    map[string]int   `json:"widths,omitempty"`
	Compare   *bool            `json:"compare,omitempty"`
}

type Result struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Frame     *Frame `json:"frame,omitempty"`
	Previous  *Frame `json:"previous,omitempty"`
	Error     string `json:"error,omitempty"`
	Diagnosis string `json:"diagnosis,omitempty"`
	SQL       string `json:"sql,omitempty"`
	Interval  string `json:"interval,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

type Executor struct {
	engine    Engine
	maxWindow time.Duration
	now       func() time.Time
	timeout   time.Duration
	parallel  int

	mu      sync.Mutex
	checks  map[[32]byte]*Checked
	schemas map[string]schemaEntry
}

func NewExecutor(engine Engine, retentionDays int) *Executor {
	maxWindow := 30 * 24 * time.Hour
	if retentionDays > 0 {
		maxWindow = time.Duration(retentionDays) * 24 * time.Hour
	}
	return &Executor{engine: engine, maxWindow: maxWindow, now: time.Now, timeout: 10 * time.Second, parallel: 4,
		checks: map[[32]byte]*Checked{}, schemas: map[string]schemaEntry{}}
}

// Validate normalizes d and runs every check, returning Problems or nil.
func (e *Executor) Validate(ctx context.Context, d *Dashboard) error {
	Normalize(d)
	_, err := e.check(ctx, d)
	return err
}

// check memoizes Check by the spec's bytes: a dashboard refreshes the same
// spec every 30 seconds, and its filters do not need re-parsing each time.
func (e *Executor) check(ctx context.Context, d *Dashboard) (*Checked, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256(raw)
	e.mu.Lock()
	cached, ok := e.checks[key]
	e.mu.Unlock()
	if ok {
		return cached, nil
	}
	checked, problems := Check(ctx, e.engine, d)
	if len(problems) > 0 {
		return nil, problems
	}
	e.mu.Lock()
	if len(e.checks) >= 256 {
		clear(e.checks)
	}
	e.checks[key] = checked
	e.mu.Unlock()
	return checked, nil
}

// Run validates the dashboard and runs the requested panels concurrently. An
// invalid spec returns its Problems; a panel that fails to run reports the
// failure in its own result, so one broken panel cannot blank a dashboard.
func (e *Executor) Run(ctx context.Context, req RunRequest) ([]Result, error) {
	d := req.Dashboard
	Normalize(&d)
	checked, err := e.check(ctx, &d)
	if err != nil {
		return nil, err
	}
	t := d.Time
	if req.Time != nil {
		t = *req.Time
		if t.Refresh == "" {
			t.Refresh = d.Time.Refresh
		}
		var problems Problems
		validateTime(t, &problems)
		if len(problems) > 0 {
			return nil, problems
		}
	}
	start, end, err := resolveWindow(t, nil, e.now(), e.maxWindow)
	if err != nil {
		return nil, Problems{{Path: "time", Message: err.Error()}}
	}
	vars, err := e.values(ctx, &d, checked, start, end, req.Vars)
	if err != nil {
		return nil, err
	}
	compare := d.Time.Compare == "previous_period"
	if req.Compare != nil {
		compare = *req.Compare
	}
	targets := make([]*Panel, 0, len(d.Panels))
	if len(req.Panels) == 0 {
		for i := range d.Panels {
			targets = append(targets, &d.Panels[i])
		}
	} else {
		for _, id := range req.Panels {
			index := slices.IndexFunc(d.Panels, func(p Panel) bool { return p.ID == id })
			if index < 0 {
				return nil, Problems{{Path: "panels", Message: fmt.Sprintf("no panel has id %q", id)}}
			}
			targets = append(targets, &d.Panels[index])
		}
	}
	results := make([]Result, len(targets))
	var group errgroup.Group
	group.SetLimit(e.parallel)
	for i, p := range targets {
		group.Go(func() error {
			results[i] = e.runPanel(ctx, p, checked, t, vars, req.Widths[p.ID], compare)
			return nil
		})
	}
	_ = group.Wait()
	return results, nil
}

func (e *Executor) runPanel(ctx context.Context, p *Panel, checked *Checked, t Time, vars map[string]Value, width int, compare bool) (res Result) {
	started := time.Now()
	res = Result{ID: p.ID, Status: StatusOK}
	defer func() { res.ElapsedMS = time.Since(started).Milliseconds() }()
	if p.Viz == "text" {
		return res
	}
	start, end, err := resolveWindow(t, p.Time, e.now(), e.maxWindow)
	if err != nil {
		return failed(res, err)
	}
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	reduces := vizSpecs[p.Viz].reduces
	var interval time.Duration
	switch {
	case reduces:
		interval = AutoInterval(end.Sub(start), 240)
	case p.Query != nil:
		interval = bucketInterval(p.Query.Bucket, end.Sub(start), width)
	default:
		interval = AutoInterval(end.Sub(start), width)
	}
	scope := Scope{Start: start, End: end, Interval: interval, Vars: vars}
	frame, sqlText, err := e.runScope(ctx, p, checked, scope)
	if err != nil {
		res.SQL = sqlText
		return failed(res, err)
	}
	res.Frame, res.SQL = frame, sqlText
	if interval > 0 && (reduces || p.Query == nil || p.Query.Bucket != "") {
		res.Interval = formatInterval(interval)
	}
	if reduces && p.Query != nil {
		totals, err := e.totals(ctx, p, checked, scope, frame)
		if err != nil {
			return failed(res, err)
		}
		frame.Totals = totals
	}
	if compare && p.Viz != "table" {
		shift := end.Sub(start)
		previous := scope
		previous.Start, previous.End = start.Add(-shift), end.Add(-shift)
		if pf, _, err := e.runScope(ctx, p, checked, previous); err == nil {
			if reduces && p.Query != nil {
				pf.Totals, _ = e.totals(ctx, p, checked, previous, pf)
			}
			res.Previous = pf
		}
	}
	if frame.Rows == 0 {
		res.Status = StatusEmpty
		res.Diagnosis = e.diagnose(ctx, p, checked.Filters[p.ID], scope)
	}
	return res
}

// totals runs the panel without a bucket and aligns the one-row result with
// the series frame's columns.
func (e *Executor) totals(ctx context.Context, p *Panel, checked *Checked, scope Scope, series *Frame) ([]any, error) {
	scope.Interval = 0
	total, _, err := e.runScope(ctx, p, checked, scope)
	if err != nil {
		return nil, err
	}
	row := totalsOf(total)
	out := make([]any, len(series.Columns))
	for i, column := range series.Columns {
		for j, totalColumn := range total.Columns {
			if column.Role == "measure" && totalColumn.Name == column.Name {
				out[i] = row[j]
			}
		}
	}
	return out, nil
}

func (e *Executor) runScope(ctx context.Context, p *Panel, checked *Checked, scope Scope) (*Frame, string, error) {
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: scope.Start, End: scope.End})
	if p.Query != nil {
		compiled, err := compileQuery(p, checked.Measures[p.ID], checked.Filters[p.ID], scope)
		if err != nil {
			return nil, "", err
		}
		rows, err := e.engine.QueryContext(ctx, compiled.SQL, compiled.Args...)
		if err != nil {
			return nil, compiled.SQL, err
		}
		frame, err := scanFrame(rows, compiled.Columns, maxFrameRows)
		return frame, compiled.SQL, err
	}
	expanded, err := expandMacros(p.SQL, scope.Interval)
	if err != nil {
		return nil, "", err
	}
	query, describe, args, err := bindParams(expanded, func(name string) ([]any, bool, error) {
		switch name {
		case "__from":
			return []any{scope.Start.UTC()}, false, nil
		case "__to":
			return []any{scope.End.UTC()}, false, nil
		}
		v, ok := scope.Vars[name]
		if !ok {
			return nil, false, fmt.Errorf("$%s has no value", name)
		}
		if v.All || len(v.Values) == 0 {
			return []any{nil}, false, nil
		}
		return []any{v.Values[0]}, false, nil
	})
	if err != nil {
		return nil, "", err
	}
	prepared, serialized, err := e.engine.PrepareTelemetrySQL(ctx, query, describe, sqlPanelRows)
	if err != nil {
		return nil, query, err
	}
	rows, err := e.engine.QueryContext(ctx, prepared, args...)
	if err != nil {
		return nil, query, err
	}
	frame, err := scanDynamicFrame(rows, serialized, sqlPanelRows)
	return frame, query, err
}

func failed(res Result, err error) Result {
	res.Status = StatusError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		res.Error = "The query took longer than 10 seconds. Narrow the time range or add filters."
	default:
		message := err.Error()
		if len(message) > 500 {
			message = message[:500] + "…"
		}
		res.Error = strings.TrimSpace(message)
	}
	return res
}
```

- [ ] **Step 5: Implement the diagnosis**

`internal/panel/diagnose.go`:

```go
package panel

import (
	"context"
	"fmt"
	"strings"

	"github.com/labstack/fanout/internal/queryrows"
)

const maxDiagnosisQueries = 6

// diagnose explains an empty structured panel. It counts the signal in the
// window, then reruns the count with each filter removed in turn; the first
// filter whose removal finds rows is named, with the values seen for its
// field when it was an equality.
func (e *Executor) diagnose(ctx context.Context, p *Panel, filters []Filter, scope Scope) string {
	if p.Query == nil {
		return "The query returned no rows for this time range."
	}
	sig, _ := lookupSignal(p.Query.From)
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: scope.Start, End: scope.End})
	active := make([]Filter, 0, len(filters))
	for _, f := range filters {
		if !scope.dropped(f) {
			active = append(active, f)
		}
	}
	total, err := e.count(ctx, sig, nil, scope)
	if err != nil {
		return ""
	}
	if total == 0 {
		return fmt.Sprintf("No %s were recorded in this time range.", sig.name)
	}
	for i, f := range active {
		if i >= maxDiagnosisQueries {
			break
		}
		others := append(append([]Filter{}, active[:i]...), active[i+1:]...)
		n, err := e.count(ctx, sig, others, scope)
		if err != nil || n == 0 {
			continue
		}
		message := fmt.Sprintf("No %s match %s. Without that filter, %d do.", sig.name, f.Expr, n)
		if f.EqField != "" {
			if seen := e.topValues(ctx, sig, f.EqField, others, scope); seen != "" {
				message += " Values seen: " + seen + "."
			}
		}
		return message
	}
	return fmt.Sprintf("%d %s are in this time range, but the filters together match none of them.", total, sig.name)
}

func (e *Executor) count(ctx context.Context, sig *signal, filters []Filter, scope Scope) (int64, error) {
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return 0, err
	}
	rows, err := e.engine.QueryContext(ctx, "SELECT count(*) FROM "+sig.name+" WHERE "+where, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var n int64
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return 0, err
		}
	}
	return n, rows.Err()
}

func (e *Executor) topValues(ctx context.Context, sig *signal, fieldText string, filters []Filter, scope Scope) string {
	ref, err := sig.field(fieldText)
	if err != nil {
		return ""
	}
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return ""
	}
	expr := ref.stringSQL()
	rows, err := e.engine.QueryContext(ctx, fmt.Sprintf("SELECT %s AS v, count(*) AS n FROM %s WHERE %s AND %s IS NOT NULL GROUP BY 1 ORDER BY 2 DESC LIMIT 5", expr, sig.name, where, expr), args...)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var value string
		var n int64
		if rows.Scan(&value, &n) == nil {
			parts = append(parts, fmt.Sprintf("%s (%d)", value, n))
		}
	}
	return strings.Join(parts, ", ")
}
```

- [ ] **Step 6: Implement variables**

`internal/panel/variables.go`:

```go
package panel

import (
	"context"
	"fmt"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

type Option struct {
	Value string `json:"value"`
	Count int64  `json:"count,omitempty"`
}

type ResolveRequest struct {
	Dashboard Dashboard        `json:"dashboard"`
	Time      *Time            `json:"time,omitempty"`
	Vars      map[string]Value `json:"vars,omitempty"`
}

// ResolveVariables lists the options of every query, custom and constant
// variable, resolving them in order so a variable that filters on an earlier
// one sees that variable's current value.
func (e *Executor) ResolveVariables(ctx context.Context, req ResolveRequest) (map[string][]Option, error) {
	d := req.Dashboard
	Normalize(&d)
	checked, err := e.check(ctx, &d)
	if err != nil {
		return nil, err
	}
	t := d.Time
	if req.Time != nil {
		t = *req.Time
	}
	start, end, err := resolveWindow(t, nil, e.now(), e.maxWindow)
	if err != nil {
		return nil, Problems{{Path: "time", Message: err.Error()}}
	}
	options := map[string][]Option{}
	values := map[string]Value{}
	for _, v := range d.Variables {
		opts, err := e.optionsFor(ctx, v, checked, start, end, values)
		if err != nil {
			return nil, err
		}
		if v.Kind != "text" {
			options[v.Name] = opts
		}
		provided, ok := req.Vars[v.Name]
		values[v.Name] = chooseValue(v, provided, ok, opts)
	}
	return options, nil
}

// values resolves the current value of every variable for a run. Options are
// queried only for a query variable with no provided value, no default and no
// All, which is the one case that needs them.
func (e *Executor) values(ctx context.Context, d *Dashboard, checked *Checked, start, end time.Time, provided map[string]Value) (map[string]Value, error) {
	values := map[string]Value{}
	for _, v := range d.Variables {
		given, ok := provided[v.Name]
		var opts []Option
		if v.Kind == "query" && !ok && v.Default == "" && !v.IncludeAll {
			var err error
			if opts, err = e.optionsFor(ctx, v, checked, start, end, values); err != nil {
				return nil, err
			}
		}
		if v.Kind == "custom" {
			opts, _ = e.optionsFor(ctx, v, checked, start, end, values)
		}
		values[v.Name] = chooseValue(v, given, ok, opts)
	}
	return values, nil
}

func (e *Executor) optionsFor(ctx context.Context, v Variable, checked *Checked, start, end time.Time, values map[string]Value) ([]Option, error) {
	switch v.Kind {
	case "constant":
		return []Option{{Value: v.Value}}, nil
	case "custom":
		out := make([]Option, len(v.Options))
		for i, o := range v.Options {
			out[i] = Option{Value: o}
		}
		return out, nil
	case "query":
		sig, _ := lookupSignal(v.From)
		ref, err := sig.field(v.Field)
		if err != nil {
			return nil, err
		}
		scope := Scope{Start: start, End: end, Vars: values}
		where, args, err := buildWhere(sig, checked.VarFilters[v.Name], scope)
		if err != nil {
			return nil, err
		}
		expr := ref.stringSQL()
		ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: start, End: end})
		rows, err := e.engine.QueryContext(ctx, fmt.Sprintf("SELECT %s AS v, count(*) AS n FROM %s WHERE %s AND %s IS NOT NULL AND %s <> '' GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 500", expr, sig.name, where, expr, expr), args...)
		if err != nil {
			return nil, fmt.Errorf("list values for $%s: %w", v.Name, err)
		}
		defer rows.Close()
		var out []Option
		for rows.Next() {
			var o Option
			if err := rows.Scan(&o.Value, &o.Count); err != nil {
				return nil, err
			}
			out = append(out, o)
		}
		return out, rows.Err()
	}
	return nil, nil
}

func chooseValue(v Variable, provided Value, ok bool, opts []Option) Value {
	if v.Kind == "constant" {
		return Value{Values: []string{v.Value}}
	}
	if ok && !(provided.All && !v.IncludeAll) {
		return provided
	}
	switch {
	case v.Default == AllValue:
		return Value{All: true}
	case v.Default != "":
		return Value{Values: []string{v.Default}}
	case v.IncludeAll:
		return Value{All: true}
	case len(opts) > 0:
		return Value{Values: []string{opts[0].Value}}
	}
	return Value{}
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `just test ./internal/panel/...`
Expected: PASS. `TestRunExplainsEmptyPanels` expects the diagnosis "No spans match http_route = '/nope'. Without that filter, 120 do. Values seen: /cart (60), /quote (60)."

- [ ] **Step 8: Commit**

```bash
git add internal/panel
git commit -m "feat(panel): run panels in one batch with frames, comparison and empty-panel diagnosis"
```

---

### Task 6: Telemetry schema discovery

**Files:**
- Create: `internal/panel/schema.go`
- Test: `internal/panel/schema_test.go`

**Interfaces:**
- Consumes: `Executor`, `signals`, `measureNames`, `unitNames`, `buildWhere`.
- Produces: `type SchemaRequest struct { Window, Namespace string }`; `type Schema struct { Window string; Signals map[string]SignalSchema; Services []Option; Measures []string; Units []string }`; `type SignalSchema struct { TimeColumn string; Columns []ColumnSchema; Attributes []AttributeSchema; Metrics []MetricSchema }`; `type ColumnSchema struct { Field; Values []Option }`; `type AttributeSchema struct { Key, Scope, Type string; Count int64; Services string }`; `type MetricSchema struct { Name, Unit, Type string; Count int64 }`; `func (e *Executor) Schema(ctx context.Context, req SchemaRequest) (*Schema, error)`; `type schemaEntry`.

- [ ] **Step 1: Write the failing test**

`internal/panel/schema_test.go`:

```go
package panel

import (
	"slices"
	"testing"
)

func TestSchemaDiscoversColumnsAttributesAndValues(t *testing.T) {
	e := newFixtureExecutor(t)
	schema, err := e.Schema(t.Context(), SchemaRequest{Window: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	spans := schema.Signals["spans"]
	if spans.TimeColumn != "start_time" {
		t.Fatalf("time column = %q", spans.TimeColumn)
	}
	keys := []string{}
	for _, a := range spans.Attributes {
		keys = append(keys, a.Scope+":"+a.Key+":"+a.Type)
	}
	if !slices.Contains(keys, "attributes:http.route:string") || !slices.Contains(keys, "attributes:customer.tier:string") {
		t.Fatalf("attributes = %v", keys)
	}
	var kinds []string
	for _, c := range spans.Columns {
		if c.Name == "kind" {
			for _, v := range c.Values {
				kinds = append(kinds, v.Value)
			}
		}
	}
	if !slices.Contains(kinds, "SPAN_KIND_SERVER") {
		t.Fatalf("kind values = %v", kinds)
	}
	if len(schema.Services) != 2 || schema.Services[0].Value != "checkout" {
		t.Fatalf("services = %+v", schema.Services)
	}
	if !slices.Contains(schema.Measures, "p95") || !slices.Contains(schema.Units, "ms") {
		t.Fatalf("measures %v units %v", schema.Measures, schema.Units)
	}
	again, err := e.Schema(t.Context(), SchemaRequest{Window: "1h"})
	if err != nil || again != schema {
		t.Fatal("schema was not cached")
	}
	if _, err := e.Schema(t.Context(), SchemaRequest{Window: "7d"}); err == nil {
		t.Fatal("window over 24h accepted")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `just test ./internal/panel/... -run TestSchema`
Expected: FAIL — undefined `SchemaRequest`, `Schema`.

- [ ] **Step 3: Implement discovery**

`internal/panel/schema.go`:

```go
package panel

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

type SchemaRequest struct {
	Window    string
	Namespace string
}

type Schema struct {
	Window   string                  `json:"window"`
	Signals  map[string]SignalSchema `json:"signals"`
	Services []Option                `json:"services"`
	Measures []string                `json:"measures"`
	Units    []string                `json:"units"`
}

type SignalSchema struct {
	TimeColumn string            `json:"time_column"`
	Columns    []ColumnSchema    `json:"columns"`
	Attributes []AttributeSchema `json:"attributes"`
	Metrics    []MetricSchema    `json:"metrics,omitempty"`
}

type ColumnSchema struct {
	Field
	Values []Option `json:"values,omitempty"`
}

type AttributeSchema struct {
	Key      string `json:"key"`
	Scope    string `json:"scope"`
	Type     string `json:"type"`
	Count    int64  `json:"count"`
	Services string `json:"services,omitempty"`
}

type MetricSchema struct {
	Name  string `json:"name"`
	Unit  string `json:"unit,omitempty"`
	Type  string `json:"type"`
	Count int64  `json:"count"`
}

type schemaEntry struct {
	schema  *Schema
	expires time.Time
}

const schemaTTL = time.Minute

// Schema describes what telemetry exists: columns with their common values,
// attribute keys sampled from recent rows, metric names and services. It is
// the agent's map before it drafts panels, and is cached for a minute.
func (e *Executor) Schema(ctx context.Context, req SchemaRequest) (*Schema, error) {
	if req.Window == "" {
		req.Window = "1h"
	}
	span, ok := ranges[req.Window]
	if !ok || span > 24*time.Hour {
		return nil, Problems{{Path: "window", Message: "window must be one of 5m, 15m, 1h, 3h, 6h, 12h or 24h"}}
	}
	key := req.Window + "|" + req.Namespace
	e.mu.Lock()
	entry, ok := e.schemas[key]
	e.mu.Unlock()
	if ok && e.now().Before(entry.expires) {
		return entry.schema, nil
	}
	end := e.now().UTC()
	start := end.Add(-span)
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: start, End: end, Namespace: req.Namespace})
	out := &Schema{Window: req.Window, Signals: map[string]SignalSchema{}, Measures: measureNames(), Units: unitNames()}
	for _, name := range SignalNames() {
		sig := signals[name]
		values, err := e.lowCardinality(ctx, sig, start, end, req.Namespace)
		if err != nil {
			return nil, err
		}
		schema := SignalSchema{TimeColumn: sig.time}
		for _, f := range sig.fields {
			schema.Columns = append(schema.Columns, ColumnSchema{Field: f, Values: values[f.Name]})
		}
		if schema.Attributes, err = e.attributes(ctx, sig, start, end, req.Namespace); err != nil {
			return nil, err
		}
		if name == "metrics" {
			if schema.Metrics, err = e.metricNames(ctx, start, end, req.Namespace); err != nil {
				return nil, err
			}
		}
		if name == "spans" {
			out.Services = values["service"]
		}
		out.Signals[name] = schema
	}
	e.mu.Lock()
	e.schemas[key] = schemaEntry{schema: out, expires: e.now().Add(schemaTTL)}
	e.mu.Unlock()
	return out, nil
}

func windowWhere(sig *signal, start, end time.Time, namespace string) (string, []any) {
	column := quoteIdent(sig.time)
	where := fmt.Sprintf("%s >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND %s < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS", column, column)
	args := []any{start, end}
	if namespace != "" {
		where += " AND namespace = ?"
		args = append(args, namespace)
	}
	return where, args
}

func (e *Executor) lowCardinality(ctx context.Context, sig *signal, start, end time.Time, namespace string) (map[string][]Option, error) {
	where, args := windowWhere(sig, start, end, namespace)
	parts := make([]string, 0, len(sig.lowCard))
	for _, column := range sig.lowCard {
		parts = append(parts, fmt.Sprintf("SELECT %s AS c, CAST(%s AS VARCHAR) AS v, count(*) AS n FROM base WHERE %s IS NOT NULL AND CAST(%s AS VARCHAR) <> '' GROUP BY 2",
			sqlString(column), quoteIdent(column), quoteIdent(column), quoteIdent(column)))
	}
	query := "WITH base AS (SELECT * FROM " + sig.name + " WHERE " + where + ") " + strings.Join(parts, " UNION ALL ")
	rows, err := e.engine.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("discover %s values: %w", sig.name, err)
	}
	defer rows.Close()
	out := map[string][]Option{}
	for rows.Next() {
		var column string
		var o Option
		if err := rows.Scan(&column, &o.Value, &o.Count); err != nil {
			return nil, err
		}
		out[column] = append(out[column], o)
	}
	for column, opts := range out {
		sort.Slice(opts, func(i, j int) bool { return opts[i].Count > opts[j].Count || opts[i].Count == opts[j].Count && opts[i].Value < opts[j].Value })
		limit := 12
		if column == "service" {
			limit = 200
		}
		out[column] = opts[:min(len(opts), limit)]
	}
	return out, rows.Err()
}

func (e *Executor) attributes(ctx context.Context, sig *signal, start, end time.Time, namespace string) ([]AttributeSchema, error) {
	where, args := windowWhere(sig, start, end, namespace)
	query := `WITH sample AS (
  SELECT service, to_json(attributes) AS a, to_json(resource) AS r FROM ` + sig.name + ` WHERE ` + where + ` LIMIT 5000
), keyed AS (
  SELECT service, 'attributes' AS scope, unnest(json_keys(a)) AS k, a AS j FROM sample
  UNION ALL
  SELECT service, 'resource' AS scope, unnest(json_keys(r)) AS k, r AS j FROM sample
)
SELECT scope, k, any_value(json_type(j, '$."' || replace(k, '"', '\"') || '"')) AS t, count(*) AS n,
       array_to_string(list(DISTINCT service ORDER BY service)[1:3], ', ') AS services
FROM keyed GROUP BY scope, k ORDER BY n DESC, k LIMIT 200`
	rows, err := e.engine.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("discover %s attributes: %w", sig.name, err)
	}
	defer rows.Close()
	var out []AttributeSchema
	for rows.Next() {
		var a AttributeSchema
		var jsonType *string
		if err := rows.Scan(&a.Scope, &a.Key, &jsonType, &a.Count, &a.Services); err != nil {
			return nil, err
		}
		a.Type = "string"
		if jsonType != nil {
			switch *jsonType {
			case "BIGINT", "UBIGINT", "DOUBLE":
				a.Type = "number"
			case "BOOLEAN":
				a.Type = "boolean"
			case "OBJECT", "ARRAY":
				a.Type = "object"
			}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (e *Executor) metricNames(ctx context.Context, start, end time.Time, namespace string) ([]MetricSchema, error) {
	where, args := windowWhere(signals["metrics"], start, end, namespace)
	rows, err := e.engine.QueryContext(ctx, "SELECT name, coalesce(any_value(unit), ''), coalesce(any_value(type), ''), count(*) FROM metrics WHERE "+where+" GROUP BY 1 ORDER BY 4 DESC, 1 LIMIT 300", args...)
	if err != nil {
		return nil, fmt.Errorf("discover metric names: %w", err)
	}
	defer rows.Close()
	var out []MetricSchema
	for rows.Next() {
		var m MetricSchema
		if err := rows.Scan(&m.Name, &m.Unit, &m.Type, &m.Count); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `just test ./internal/panel/... -run TestSchema -v`
Expected: PASS. If DuckDB rejects `to_json` on the VARIANT columns or `json_keys` on its output, print the error, then switch the sample to `to_json(attributes)::JSON` and keep the assertions.

- [ ] **Step 5: Commit**

```bash
git add internal/panel
git commit -m "feat(panel): discover columns, attribute keys, values and metric names"
```

---

### Task 7: HTTP routes for panels, variables and schema

**Files:**
- Create: `internal/api/panels.go`
- Modify: `internal/api/auth_middleware.go` (`classifyRoute`), `cmd/fanout/main.go` (wiring)
- Test: `internal/api/panels_test.go`
- Generated: `site/src/content/docs/reference/http-routes.mdx` (via `just docs-generate`)

**Interfaces:**
- Consumes: `panel.Executor` methods `Run`, `ResolveVariables`, `Schema`; `panel.Problems`; `RequireCapability`, `ReadTelemetry`.
- Produces: `type PanelEngine interface { Run(context.Context, panel.RunRequest) ([]panel.Result, error); ResolveVariables(context.Context, panel.ResolveRequest) (map[string][]panel.Option, error); Schema(context.Context, panel.SchemaRequest) (*panel.Schema, error) }`; `func RegisterPanelRoutes(e *echo.Echo, engine PanelEngine)`; `func decodeStrict(c *echo.Context, value any, limit int64) error`; `func writeProblems(c *echo.Context, err error) (bool, error)`.

- [ ] **Step 1: Write the failing test**

`internal/api/panels_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

type fakePanels struct {
	run     panel.RunRequest
	problem bool
}

func (f *fakePanels) Run(_ context.Context, req panel.RunRequest) ([]panel.Result, error) {
	f.run = req
	if f.problem {
		return nil, panel.Problems{{Path: "panels[0].viz", Message: "unsupported viz"}}
	}
	return []panel.Result{{ID: "a", Status: panel.StatusOK}}, nil
}

func (f *fakePanels) ResolveVariables(context.Context, panel.ResolveRequest) (map[string][]panel.Option, error) {
	return map[string][]panel.Option{"service": {{Value: "checkout", Count: 3}}}, nil
}

func (f *fakePanels) Schema(_ context.Context, req panel.SchemaRequest) (*panel.Schema, error) {
	return &panel.Schema{Window: req.Window}, nil
}

func servePanels(t *testing.T, engine PanelEngine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	s := newTestAuthServer(t)
	if _, err := s.users.Create("admin@example.com", "", "admin"); err != nil {
		t.Fatal(err)
	}
	viewer, err := s.users.Create("viewer@example.com", "", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	RegisterPanelRoutes(s.e, engine)
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := sessionRequest(method, path, reader, s.login(t, viewer))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	return rec
}

func TestPanelQueryRoute(t *testing.T) {
	engine := &fakePanels{}
	rec := servePanels(t, engine, http.MethodPost, "/api/panels/query", `{"dashboard":{"name":"x","panels":[]},"vars":{"service":"checkout","route":"$__all"},"widths":{"a":640}}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"results"`) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	if !engine.run.Vars["route"].All || engine.run.Widths["a"] != 640 {
		t.Fatalf("request = %+v", engine.run)
	}
	engine.problem = true
	rec = servePanels(t, engine, http.MethodPost, "/api/panels/query", `{"dashboard":{"name":"x","panels":[]}}`)
	var body struct {
		Problems []panel.Problem `json:"problems"`
	}
	if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Problems[0].Path != "panels[0].viz" {
		t.Fatalf("problems response %d %s", rec.Code, rec.Body)
	}
	rec = servePanels(t, engine, http.MethodPost, "/api/panels/query", `{"dashboard":{},"unknown":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status %d", rec.Code)
	}
}

func TestVariableAndSchemaRoutes(t *testing.T) {
	rec := servePanels(t, &fakePanels{}, http.MethodPost, "/api/variables/resolve", `{"dashboard":{"name":"x","panels":[]}}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"checkout"`) {
		t.Fatalf("resolve %d %s", rec.Code, rec.Body)
	}
	rec = servePanels(t, &fakePanels{}, http.MethodGet, "/api/telemetry/schema?window=6h", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"window":"6h"`) {
		t.Fatalf("schema %d %s", rec.Code, rec.Body)
	}
}

func TestPanelRoutesAreClassified(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/panels/query"},
		{http.MethodPost, "/api/variables/resolve"},
		{http.MethodGet, "/api/telemetry/schema"},
	} {
		policy, ok := classifyRoute(tc.method, tc.path)
		if !ok || policy.kind != routePolicyCapability || policy.capability != ReadTelemetry {
			t.Errorf("%s %s = %+v %v", tc.method, tc.path, policy, ok)
		}
	}
	if _, ok := classifyRoute(http.MethodGet, "/api/panels/query"); ok {
		t.Error("GET /api/panels/query allowed")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `just test ./internal/api/ -run 'TestPanel|TestVariableAndSchema' -v`
Expected: FAIL — undefined `RegisterPanelRoutes`, `PanelEngine`.

- [ ] **Step 3: Implement the handler**

`internal/api/panels.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/panel"
)

type PanelEngine interface {
	Run(context.Context, panel.RunRequest) ([]panel.Result, error)
	ResolveVariables(context.Context, panel.ResolveRequest) (map[string][]panel.Option, error)
	Schema(context.Context, panel.SchemaRequest) (*panel.Schema, error)
}

type PanelHandler struct{ engine PanelEngine }

// panelDeadline bounds one batch. Each panel has its own 10-second timeout;
// the batch allows a little more for queueing behind the read pool.
const panelDeadline = 20 * time.Second

func RegisterPanelRoutes(e *echo.Echo, engine PanelEngine) {
	h := &PanelHandler{engine: engine}
	read := RequireCapability(ReadTelemetry)
	e.POST("/api/panels/query", h.query, read)
	e.POST("/api/variables/resolve", h.resolve, read)
	e.GET("/api/telemetry/schema", h.schema, read)
}

func (h *PanelHandler) query(c *echo.Context) error {
	var req panel.RunRequest
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), panelDeadline)
	defer cancel()
	results, err := h.engine.Run(ctx, req)
	if handled, writeErr := writeProblems(c, err); handled {
		return writeErr
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "panels unavailable").Wrap(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"results": results})
}

func (h *PanelHandler) resolve(c *echo.Context) error {
	var req panel.ResolveRequest
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), panelDeadline)
	defer cancel()
	options, err := h.engine.ResolveVariables(ctx, req)
	if handled, writeErr := writeProblems(c, err); handled {
		return writeErr
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "variables unavailable").Wrap(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"options": options})
}

func (h *PanelHandler) schema(c *echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), panelDeadline)
	defer cancel()
	schema, err := h.engine.Schema(ctx, panel.SchemaRequest{Window: c.QueryParam("window"), Namespace: c.QueryParam("namespace")})
	if handled, writeErr := writeProblems(c, err); handled {
		return writeErr
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "schema unavailable").Wrap(err)
	}
	return c.JSON(http.StatusOK, schema)
}

// decodeStrict reads one JSON object, rejecting unknown fields and bodies
// over limit, so a misspelled field fails loudly instead of being ignored.
func decodeStrict(c *echo.Context, value any, limit int64) error {
	decoder := json.NewDecoder(io.LimitReader(c.Request().Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body: "+err.Error())
	}
	return nil
}

// writeProblems answers a spec validation failure with 400 and the problem
// list. It reports whether it handled err.
func writeProblems(c *echo.Context, err error) (bool, error) {
	var problems panel.Problems
	if !errors.As(err, &problems) {
		return false, nil
	}
	return true, c.JSON(http.StatusBadRequest, map[string]any{"message": "The dashboard spec is invalid.", "problems": problems})
}
```

- [ ] **Step 4: Classify the routes**

In `internal/api/auth_middleware.go` `classifyRoute`, add before the `/api/dashboards` case:

```go
	case path == "/api/panels/query" || path == "/api/variables/resolve":
		return routePolicy{kind: routePolicyCapability, capability: ReadTelemetry}, method == http.MethodPost
	case path == "/api/telemetry/schema":
		return routePolicy{kind: routePolicyCapability, capability: ReadTelemetry}, read
```

- [ ] **Step 5: Wire the executor**

In `cmd/fanout/main.go`, after `queries := observability.New(q, q, cfg.RetentionDays)`:

```go
	panels := panel.NewExecutor(q, cfg.RetentionDays)
	api.RegisterPanelRoutes(e, panels)
```

and add the import `"github.com/labstack/fanout/internal/panel"`.

- [ ] **Step 6: Run the tests, regenerate docs, and verify**

Run: `just test ./internal/api/ ./cmd/... -run 'TestPanel|TestVariableAndSchema|TestRoute|TestDocs'`
Expected: PASS.
Run: `just docs-generate && git diff --stat site/src/content/docs/reference`
Expected: `http-routes.mdx` (and `roles.mdx` if it lists routes) gain the three routes.

- [ ] **Step 7: Commit**

```bash
git add internal/api/panels.go internal/api/panels_test.go internal/api/auth_middleware.go cmd/fanout/main.go site/src/content/docs/reference
git commit -m "feat(api): add panel query, variable and telemetry schema routes"
```

---

### Task 8: Dashboard tables and sqlc queries

**Files:**
- Create: `internal/db/migrations/20261004000000_dashboard_specs.sql`, `internal/db/queries/dashboards.sql`
- Modify: `internal/db/migrations_test.go` (ledger)
- Generated: `internal/db/generated/dashboards.sql.go`, `internal/db/generated/models.go` (via `just db-gen`)
- Test: `internal/db/dashboards_schema_test.go`

**Interfaces:**
- Produces (sqlc, package `generated`): `ListDashboards(ctx, ownerID string) ([]ListDashboardsRow, error)`; `CountDashboards(ctx, ownerID string) (int64, error)`; `GetDashboard(ctx, GetDashboardParams{ID, OwnerID}) (Dashboard, error)`; `InsertDashboard(ctx, InsertDashboardParams) error`; `UpdateDashboard(ctx, UpdateDashboardParams{Name, Description, NextVersion, SpecJson, PanelCount, UpdatedAt, ID, OwnerID, BaseVersion}) (int64, error)`; `DeleteDashboard(ctx, DeleteDashboardParams{ID, OwnerID}) (int64, error)`; `PromoteNewestDashboard(ctx, ownerID string) error`; `InsertDashboardVersion(ctx, InsertDashboardVersionParams) error`; `ListDashboardVersions(ctx, dashboardID string) ([]ListDashboardVersionsRow, error)`; `GetDashboardVersion(ctx, GetDashboardVersionParams{DashboardID, Version}) (string, error)`; `PruneDashboardVersions(ctx, PruneDashboardVersionsParams{DashboardID, KeepFrom}) error`. Model `generated.Dashboard{ID, OwnerID, Name, Description string; IsDefault, Version int64; SpecJson string; PanelCount int64; CreatedAt, UpdatedAt string}`.

- [ ] **Step 1: Write the migration**

`internal/db/migrations/20261004000000_dashboard_specs.sql`:

```sql
-- +goose Up
-- Dashboards become typed v1 specs with a version history. The widget model
-- is replaced outright: Fanout is pre-release, so existing dashboards are
-- dropped and each owner receives the new default dashboard on next visit.
DROP TABLE dashboard_widgets;
DROP TABLE dashboards;

CREATE TABLE dashboards (
    id          TEXT PRIMARY KEY,
    owner_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL COLLATE NOCASE,
    description TEXT NOT NULL DEFAULT '',
    is_default  INTEGER NOT NULL DEFAULT 0,
    version     INTEGER NOT NULL,
    spec_json   TEXT NOT NULL,
    panel_count INTEGER NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE UNIQUE INDEX dashboards_owner_name ON dashboards(owner_id, name);
CREATE INDEX dashboards_owner_updated ON dashboards(owner_id, updated_at DESC);
CREATE UNIQUE INDEX dashboards_owner_default ON dashboards(owner_id) WHERE is_default = 1;

CREATE TABLE dashboard_versions (
    dashboard_id TEXT NOT NULL REFERENCES dashboards(id) ON DELETE CASCADE,
    version      INTEGER NOT NULL,
    spec_json    TEXT NOT NULL,
    author_kind  TEXT NOT NULL CHECK (author_kind IN ('user', 'agent', 'system')),
    author_id    TEXT NOT NULL DEFAULT '',
    message      TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    PRIMARY KEY (dashboard_id, version)
);
```

- [ ] **Step 2: Write the queries**

`internal/db/queries/dashboards.sql`:

```sql
-- name: ListDashboards :many
SELECT id, name, description, is_default, version, panel_count, updated_at
FROM dashboards
WHERE owner_id = ?
ORDER BY is_default DESC, updated_at DESC;

-- name: CountDashboards :one
SELECT COUNT(*) FROM dashboards WHERE owner_id = ?;

-- name: GetDashboard :one
SELECT * FROM dashboards WHERE id = ? AND owner_id = ?;

-- name: InsertDashboard :exec
INSERT INTO dashboards (id, owner_id, name, description, is_default, version, spec_json, panel_count, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateDashboard :execrows
UPDATE dashboards
SET name = sqlc.arg(name),
    description = sqlc.arg(description),
    version = sqlc.arg(next_version),
    spec_json = sqlc.arg(spec_json),
    panel_count = sqlc.arg(panel_count),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id) AND version = sqlc.arg(base_version);

-- name: DeleteDashboard :execrows
DELETE FROM dashboards WHERE id = ? AND owner_id = ?;

-- name: PromoteNewestDashboard :exec
UPDATE dashboards SET is_default = 1
WHERE id = (SELECT d.id FROM dashboards d WHERE d.owner_id = ? ORDER BY d.updated_at DESC LIMIT 1);

-- name: InsertDashboardVersion :exec
INSERT INTO dashboard_versions (dashboard_id, version, spec_json, author_kind, author_id, message, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListDashboardVersions :many
SELECT version, author_kind, author_id, message, created_at
FROM dashboard_versions
WHERE dashboard_id = ?
ORDER BY version DESC
LIMIT 100;

-- name: GetDashboardVersion :one
SELECT spec_json FROM dashboard_versions WHERE dashboard_id = ? AND version = ?;

-- name: PruneDashboardVersions :exec
DELETE FROM dashboard_versions
WHERE dashboard_id = sqlc.arg(dashboard_id) AND version < sqlc.arg(keep_from);
```

- [ ] **Step 3: Generate bindings and extend the ledger**

Run: `just db-gen`
Expected: `internal/db/generated/dashboards.sql.go` created; `models.go` loses `DashboardWidget` and gains `DashboardVersion`.

Run: `shasum -a 256 internal/db/migrations/20261004000000_dashboard_specs.sql`
Add the printed hash to the ledger map in `internal/db/migrations_test.go`:

```go
		"migrations/20261004000000_dashboard_specs.sql": "<the printed sha256>",
```

- [ ] **Step 4: Write the schema test**

`internal/db/dashboards_schema_test.go`:

```go
package db_test

import (
	"testing"

	"github.com/labstack/fanout/internal/db/generated"
	appstore "github.com/labstack/fanout/internal/store"
)

func TestDashboardVersionsCascadeAndOptimisticUpdate(t *testing.T) {
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	if _, err := sqlite.DB.Exec(`INSERT INTO users (id, email) VALUES ('u1', 'u1@example.com')`); err != nil {
		t.Fatal(err)
	}
	q := generated.New(sqlite.DB)
	ctx := t.Context()
	if err := q.InsertDashboard(ctx, generated.InsertDashboardParams{ID: "d1", OwnerID: "u1", Name: "Ops", Version: 1, SpecJson: "{}", PanelCount: 0, CreatedAt: "t", UpdatedAt: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := q.InsertDashboardVersion(ctx, generated.InsertDashboardVersionParams{DashboardID: "d1", Version: 1, SpecJson: "{}", AuthorKind: "system", CreatedAt: "t"}); err != nil {
		t.Fatal(err)
	}
	n, err := q.UpdateDashboard(ctx, generated.UpdateDashboardParams{Name: "Ops", NextVersion: 2, SpecJson: "{}", UpdatedAt: "t2", ID: "d1", OwnerID: "u1", BaseVersion: 1})
	if err != nil || n != 1 {
		t.Fatalf("update = %d %v", n, err)
	}
	if n, _ := q.UpdateDashboard(ctx, generated.UpdateDashboardParams{Name: "Ops", NextVersion: 2, SpecJson: "{}", UpdatedAt: "t3", ID: "d1", OwnerID: "u1", BaseVersion: 1}); n != 0 {
		t.Fatal("a stale base version updated the row")
	}
	if _, err := sqlite.DB.Exec(`DELETE FROM users WHERE id = 'u1'`); err != nil {
		t.Fatal(err)
	}
	versions, err := q.ListDashboardVersions(ctx, "d1")
	if err != nil || len(versions) != 0 {
		t.Fatalf("versions survived their dashboard: %v %v", versions, err)
	}
}
```

- [ ] **Step 5: Run the tests**

Run: `just test ./internal/db/... ./internal/store/...`
Expected: PASS (`TestMigrationChecksums` and the new test). If foreign keys are not enforced on this connection, check `internal/store/store.go` for the `foreign_keys` pragma before changing anything; the existing schema already relies on cascades.

- [ ] **Step 6: Commit**

```bash
git add internal/db
git commit -m "feat(db): store dashboards as v1 specs with versions"
```

---

### Task 9: Dashboard service — packing, edit operations, versions and the default spec

**Files:**
- Create: `internal/dashboard/layout.go`, `internal/dashboard/edit.go`, `internal/dashboard/defaults.go`
- Rewrite: `internal/dashboard/service.go`, `internal/dashboard/service_test.go`
- Test: `internal/dashboard/layout_test.go`, `internal/dashboard/edit_test.go`, `internal/dashboard/defaults_test.go`

**Interfaces:**
- Consumes: `generated` queries (Task 8); `panel.Dashboard`, `panel.Normalize`, `panel.Problems`, `panel.NewExecutor` (tests only).
- Produces:
  - `type Validator interface { Validate(ctx context.Context, d *panel.Dashboard) error }` (`*panel.Executor` implements it)
  - `type Author struct { Kind, ID string }` (`user`, `agent`, `system`)
  - `type Record struct { ID, Name, Description string; IsDefault bool; Version int; Spec panel.Dashboard; CreatedAt, UpdatedAt string }` (JSON `id`, `name`, `description`, `is_default`, `version`, `spec`, `created_at`, `updated_at`)
  - `type Summary struct { ID, Name, Description string; IsDefault bool; Version, PanelCount int; UpdatedAt string }` (JSON `panel_count`, …)
  - `type VersionInfo struct { Version int; AuthorKind, AuthorID, Message, CreatedAt string }`
  - `type Operation struct { Op, ID, After, Name string; Panel *panel.Panel; Set map[string]any; Variable *panel.Variable; Description *string; Time *panel.Time }`
  - `func New(db *sql.DB, validator Validator) *Service`
  - `(*Service).List`, `Get`, `Create(ctx, owner, spec, author)`, `Replace(ctx, owner, id, spec, baseVersion, author, message)`, `Edit(ctx, owner, id, ops, baseVersion, author, message)`, `Delete`, `Versions`, `Restore(ctx, owner, id, version, author)`
  - `var ErrNotFound, ErrConflict, ErrStale`
  - `func Pack(panels []panel.Panel)`; `func Apply(spec panel.Dashboard, ops []Operation) (panel.Dashboard, bool, error)`; `func DefaultSpec() panel.Dashboard`
  - `const RowHeight = 40` (pixels, mirrored by the browser grid) and heights `s`=3, `m`=6, `l`=10 rows.
  - Keep `identity.go` (`OwnerMetaKey`, `OAuthScope`, `WithOwner`, `OwnerFromContext`) unchanged.

- [ ] **Step 1: Write the failing tests**

`internal/dashboard/layout_test.go`:

```go
package dashboard

import (
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

func TestPackFillsGapsWithoutStretching(t *testing.T) {
	panels := []panel.Panel{
		{ID: "a", Width: 3, Height: "s"},
		{ID: "chart", Width: 9, Height: "m"},
		{ID: "b", Width: 3, Height: "s"},
		{ID: "table", Width: 12, Height: "m"},
	}
	Pack(panels)
	want := map[string]panel.Grid{
		"a":     {X: 0, Y: 0, W: 3, H: 3},
		"chart": {X: 3, Y: 0, W: 9, H: 6},
		"b":     {X: 0, Y: 3, W: 3, H: 3},
		"table": {X: 0, Y: 6, W: 12, H: 6},
	}
	for _, p := range panels {
		if *p.Grid != want[p.ID] {
			t.Errorf("%s = %+v, want %+v", p.ID, *p.Grid, want[p.ID])
		}
	}
	if needsPack(panels) {
		t.Fatal("packed layout reported as needing a pack")
	}
	panels[1].Grid.X = 1
	if !needsPack(panels) {
		t.Fatal("overlap not detected")
	}
}
```

`internal/dashboard/edit_test.go`:

```go
package dashboard

import (
	"errors"
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

func editBase() panel.Dashboard {
	return panel.Dashboard{Name: "Ops", Time: panel.Time{Range: "1h"}, Panels: []panel.Panel{
		{ID: "a", Title: "A", Viz: "text", Content: "a", Grid: &panel.Grid{W: 4, H: 3}},
		{ID: "b", Title: "B", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"count()"}}, Grid: &panel.Grid{X: 4, W: 3, H: 3}},
	}}
}

func TestApplyOperations(t *testing.T) {
	description := "New purpose"
	out, repack, err := Apply(editBase(), []Operation{
		{Op: "update_panel", ID: "b", Set: map[string]any{"title": "Requests", "thresholds": []any{map[string]any{"value": 10.0, "status": "warn"}}}},
		{Op: "add_panel", After: "a", Panel: &panel.Panel{ID: "c", Title: "C", Viz: "text", Content: "c"}},
		{Op: "move_panel", ID: "a"},
		{Op: "set_variable", Variable: &panel.Variable{Name: "service", Kind: "constant", Value: "checkout"}},
		{Op: "rename", Name: "Operations", Description: &description},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Panels[0].ID + "," + out.Panels[1].ID + "," + out.Panels[2].ID; got != "a,c,b" {
		t.Fatalf("order = %s", got)
	}
	if out.Panels[2].Title != "Requests" || out.Panels[2].Thresholds[0].Status != "warn" || out.Panels[2].Query.Measures[0] != "count()" {
		t.Fatalf("update_panel = %+v", out.Panels[2])
	}
	if !repack || out.Name != "Operations" || out.Description != description || len(out.Variables) != 1 {
		t.Fatalf("repack %v spec %+v", repack, out)
	}
	if editBase().Panels[1].Title != "B" {
		t.Fatal("Apply mutated its input")
	}
}

func TestApplyUpdateWithoutResizeKeepsLayout(t *testing.T) {
	_, repack, err := Apply(editBase(), []Operation{{Op: "update_panel", ID: "b", Set: map[string]any{"title": "Requests"}}})
	if err != nil || repack {
		t.Fatalf("repack %v err %v", repack, err)
	}
	_, repack, _ = Apply(editBase(), []Operation{{Op: "update_panel", ID: "b", Set: map[string]any{"width": 6.0}}})
	if !repack {
		t.Fatal("a width change must repack")
	}
}

func TestApplyRejectsBadOperations(t *testing.T) {
	cases := [][]Operation{
		{{Op: "update_panel", ID: "missing", Set: map[string]any{"title": "x"}}},
		{{Op: "update_panel", ID: "b", Set: map[string]any{"id": "c"}}},
		{{Op: "update_panel", ID: "b", Set: map[string]any{"colour": "red"}}},
		{{Op: "add_panel", Panel: &panel.Panel{ID: "a", Title: "dup", Viz: "text", Content: "x"}}},
		{{Op: "remove_variable", Name: "nope"}},
		{{Op: "explode"}},
	}
	for _, ops := range cases {
		_, _, err := Apply(editBase(), ops)
		var problems panel.Problems
		if !errors.As(err, &problems) || problems[0].Path != "operations[0]" {
			t.Errorf("%+v: err = %v", ops, err)
		}
	}
}
```

`internal/dashboard/defaults_test.go`:

```go
package dashboard

import (
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/query"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestDefaultSpecPassesTheFullCheck(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(); _ = repo.Close() })
	spec := DefaultSpec()
	if err := panel.NewExecutor(d, 30).Validate(t.Context(), &spec); err != nil {
		t.Fatal(err)
	}
}
```

`internal/dashboard/service_test.go` (replace the file):

```go
package dashboard

import (
	"context"
	"errors"
	"testing"

	"github.com/labstack/fanout/internal/panel"
	appstore "github.com/labstack/fanout/internal/store"
)

// structural validates without DuckDB; filter checks are covered in
// internal/panel and by TestDefaultSpecPassesTheFullCheck.
type structural struct{}

func (structural) Validate(_ context.Context, d *panel.Dashboard) error {
	panel.Normalize(d)
	if problems := panel.Validate(d); len(problems) > 0 {
		return problems
	}
	return nil
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	for _, id := range []string{"owner", "other"} {
		if _, err := sqlite.DB.Exec(`INSERT INTO users (id, email) VALUES (?, ?)`, id, id+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	return New(sqlite.DB, structural{})
}

func textSpec(name string) panel.Dashboard {
	return panel.Dashboard{Name: name, Panels: []panel.Panel{{ID: "notes", Title: "Notes", Viz: "text", Content: "hello"}}}
}

var agent = Author{Kind: "agent", ID: "owner"}

func TestFirstVisitCreatesTheDefaultDashboard(t *testing.T) {
	s := newTestService(t)
	items, err := s.List(t.Context(), "owner")
	if err != nil || len(items) != 1 || !items[0].IsDefault || items[0].PanelCount != len(DefaultSpec().Panels) {
		t.Fatalf("items = %+v err %v", items, err)
	}
	record, err := s.Get(t.Context(), "owner", items[0].ID)
	if err != nil || record.Spec.Panels[0].Grid == nil {
		t.Fatalf("default record = %+v err %v", record, err)
	}
}

func TestCreateEditRestoreAndVersions(t *testing.T) {
	s := newTestService(t)
	created, err := s.Create(t.Context(), "owner", textSpec("Ops"), agent)
	if err != nil || created.Version != 1 || created.Spec.Panels[0].Grid == nil {
		t.Fatalf("created = %+v err %v", created, err)
	}
	edited, err := s.Edit(t.Context(), "owner", created.ID, []Operation{{Op: "update_panel", ID: "notes", Set: map[string]any{"content": "changed"}}}, 1, agent, "Reword the note")
	if err != nil || edited.Version != 2 || edited.Spec.Panels[0].Content != "changed" {
		t.Fatalf("edited = %+v err %v", edited, err)
	}
	restored, err := s.Restore(t.Context(), "owner", created.ID, 1, Author{Kind: "user", ID: "owner"})
	if err != nil || restored.Version != 3 || restored.Spec.Panels[0].Content != "hello" {
		t.Fatalf("restored = %+v err %v", restored, err)
	}
	versions, err := s.Versions(t.Context(), "owner", created.ID)
	if err != nil || len(versions) != 3 || versions[0].Version != 3 || versions[1].Message != "Reword the note" || versions[1].AuthorKind != "agent" {
		t.Fatalf("versions = %+v err %v", versions, err)
	}
}

func TestStaleVersionsAreRejected(t *testing.T) {
	s := newTestService(t)
	created, _ := s.Create(t.Context(), "owner", textSpec("Ops"), agent)
	if _, err := s.Replace(t.Context(), "owner", created.ID, textSpec("Ops 2"), 1, agent, ""); err != nil {
		t.Fatal(err)
	}
	_, err := s.Replace(t.Context(), "owner", created.ID, textSpec("Ops 3"), 1, Author{Kind: "user", ID: "owner"}, "")
	if !errors.Is(err, ErrStale) {
		t.Fatalf("stale replace err = %v", err)
	}
	if _, err := s.Edit(t.Context(), "owner", created.ID, []Operation{{Op: "rename", Name: "Ops 4"}}, 0, agent, ""); err != nil {
		t.Fatalf("base version 0 means latest: %v", err)
	}
}

func TestOwnershipNamesAndValidation(t *testing.T) {
	s := newTestService(t)
	created, _ := s.Create(t.Context(), "owner", textSpec("Ops"), agent)
	if _, err := s.Get(t.Context(), "other", created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner read err = %v", err)
	}
	if _, err := s.Create(t.Context(), "owner", textSpec("ops"), agent); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name err = %v", err)
	}
	bad := textSpec("Bad")
	bad.Panels[0].Viz = "piechart"
	var problems panel.Problems
	if _, err := s.Create(t.Context(), "owner", bad, agent); !errors.As(err, &problems) {
		t.Fatalf("invalid spec err = %v", err)
	}
	if err := s.Delete(t.Context(), "owner", created.ID); err != nil {
		t.Fatal(err)
	}
	items, _ := s.List(t.Context(), "owner")
	if len(items) != 1 || !items[0].IsDefault {
		t.Fatalf("after delete = %+v", items)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `just test ./internal/dashboard/...`
Expected: FAIL — undefined `Pack`, `Apply`, `Operation`, `DefaultSpec`, new `New` signature.

- [ ] **Step 3: Implement packing**

`internal/dashboard/layout.go`:

```go
package dashboard

import "github.com/labstack/fanout/internal/panel"

// RowHeight is the grid row height in pixels; ui/host/src/dashboards/grid.tsx
// uses the same value.
const RowHeight = 40

const columns = 12

var heightRows = map[string]int{"s": 3, "m": 6, "l": 10}

// Pack assigns every panel a grid position. In order, each panel takes the
// first position, scanning from the top and then from the left, where its
// width and height fit. Short panels are never stretched to a taller
// neighbour; later panels fill the space under them (#232 item 24).
func Pack(panels []panel.Panel) {
	placed := make([]panel.Grid, 0, len(panels))
	for i := range panels {
		w := min(max(panels[i].Width, 1), columns)
		h := heightRows[panels[i].Height]
		if h == 0 {
			h = heightRows["m"]
		}
		x, y := firstFit(placed, w, h)
		grid := panel.Grid{X: x, Y: y, W: w, H: h}
		panels[i].Grid = &grid
		placed = append(placed, grid)
	}
}

func firstFit(placed []panel.Grid, w, h int) (int, int) {
	for y := 0; ; y++ {
		for x := 0; x+w <= columns; x++ {
			if fits(placed, x, y, w, h) {
				return x, y
			}
		}
	}
}

func fits(placed []panel.Grid, x, y, w, h int) bool {
	for _, g := range placed {
		if x < g.X+g.W && g.X < x+w && y < g.Y+g.H && g.Y < y+h {
			return false
		}
	}
	return true
}

// needsPack reports whether any panel lacks a usable position or two overlap.
func needsPack(panels []panel.Panel) bool {
	placed := make([]panel.Grid, 0, len(panels))
	for _, p := range panels {
		g := p.Grid
		if g == nil || g.W < 1 || g.H < 1 || g.X < 0 || g.Y < 0 || g.X+g.W > columns || !fits(placed, g.X, g.Y, g.W, g.H) {
			return true
		}
		placed = append(placed, *g)
	}
	return false
}
```

- [ ] **Step 4: Implement edit operations**

`internal/dashboard/edit.go`:

```go
package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/labstack/fanout/internal/panel"
)

// Operation is one typed edit. Panels are addressed by their stable id, never
// by position, so an edit lands on exactly the panel it names.
type Operation struct {
	Op          string          `json:"op" jsonschema:"add_panel, update_panel, remove_panel, move_panel, set_variable, remove_variable, set_time or rename"`
	ID          string          `json:"id,omitempty" jsonschema:"update_panel, remove_panel, move_panel: the panel id"`
	After       string          `json:"after,omitempty" jsonschema:"add_panel: insert after this panel id (default: last); move_panel: move after this panel id (default: first)"`
	Panel       *panel.Panel    `json:"panel,omitempty" jsonschema:"add_panel: the complete new panel"`
	Set         map[string]any  `json:"set,omitempty" jsonschema:"update_panel: panel fields to replace, e.g. {\"title\": \"…\", \"thresholds\": […]}; null removes a field"`
	Variable    *panel.Variable `json:"variable,omitempty" jsonschema:"set_variable: the variable, added or replaced by name"`
	Name        string          `json:"name,omitempty" jsonschema:"remove_variable: the variable name; rename: the new dashboard name"`
	Description *string         `json:"description,omitempty" jsonschema:"rename: the new description"`
	Time        *panel.Time     `json:"time,omitempty" jsonschema:"set_time: the new default time"`
}

func opError(i int, format string, args ...any) error {
	return panel.Problems{{Path: fmt.Sprintf("operations[%d]", i), Message: fmt.Sprintf(format, args...)}}
}

// Apply runs operations in order on a copy of spec. The bool reports whether
// panels were added, removed, moved or resized, which requires a fresh pack.
func Apply(spec panel.Dashboard, ops []Operation) (panel.Dashboard, bool, error) {
	out, err := clone(spec)
	if err != nil {
		return panel.Dashboard{}, false, err
	}
	repack := false
	index := func(id string) int { return slices.IndexFunc(out.Panels, func(p panel.Panel) bool { return p.ID == id }) }
	for i, op := range ops {
		switch op.Op {
		case "add_panel":
			if op.Panel == nil {
				return panel.Dashboard{}, false, opError(i, "add_panel needs panel")
			}
			if index(op.Panel.ID) >= 0 {
				return panel.Dashboard{}, false, opError(i, "a panel with id %q already exists", op.Panel.ID)
			}
			p := *op.Panel
			p.Grid = nil
			at := len(out.Panels)
			if op.After != "" {
				j := index(op.After)
				if j < 0 {
					return panel.Dashboard{}, false, opError(i, "no panel has id %q", op.After)
				}
				at = j + 1
			}
			out.Panels = slices.Insert(out.Panels, at, p)
			repack = true
		case "update_panel":
			j := index(op.ID)
			if j < 0 {
				return panel.Dashboard{}, false, opError(i, "no panel has id %q", op.ID)
			}
			for _, fixed := range []string{"id", "grid"} {
				if _, ok := op.Set[fixed]; ok {
					return panel.Dashboard{}, false, opError(i, "%s cannot be set; it is managed by the server", fixed)
				}
			}
			updated, err := mergePanel(out.Panels[j], op.Set)
			if err != nil {
				return panel.Dashboard{}, false, opError(i, "%v", err)
			}
			_, width := op.Set["width"]
			_, height := op.Set["height"]
			repack = repack || width || height
			out.Panels[j] = updated
		case "remove_panel":
			j := index(op.ID)
			if j < 0 {
				return panel.Dashboard{}, false, opError(i, "no panel has id %q", op.ID)
			}
			out.Panels = slices.Delete(out.Panels, j, j+1)
			repack = true
		case "move_panel":
			j := index(op.ID)
			if j < 0 {
				return panel.Dashboard{}, false, opError(i, "no panel has id %q", op.ID)
			}
			p := out.Panels[j]
			out.Panels = slices.Delete(out.Panels, j, j+1)
			at := 0
			if op.After != "" {
				k := index(op.After)
				if k < 0 {
					return panel.Dashboard{}, false, opError(i, "no panel has id %q", op.After)
				}
				at = k + 1
			}
			out.Panels = slices.Insert(out.Panels, at, p)
			repack = true
		case "set_variable":
			if op.Variable == nil {
				return panel.Dashboard{}, false, opError(i, "set_variable needs variable")
			}
			k := slices.IndexFunc(out.Variables, func(v panel.Variable) bool { return v.Name == op.Variable.Name })
			if k >= 0 {
				out.Variables[k] = *op.Variable
			} else {
				out.Variables = append(out.Variables, *op.Variable)
			}
		case "remove_variable":
			k := slices.IndexFunc(out.Variables, func(v panel.Variable) bool { return v.Name == op.Name })
			if k < 0 {
				return panel.Dashboard{}, false, opError(i, "no variable is named %q", op.Name)
			}
			out.Variables = slices.Delete(out.Variables, k, k+1)
		case "set_time":
			if op.Time == nil {
				return panel.Dashboard{}, false, opError(i, "set_time needs time")
			}
			out.Time = *op.Time
		case "rename":
			if op.Name == "" && op.Description == nil {
				return panel.Dashboard{}, false, opError(i, "rename needs name or description")
			}
			if op.Name != "" {
				out.Name = op.Name
			}
			if op.Description != nil {
				out.Description = *op.Description
			}
		default:
			return panel.Dashboard{}, false, opError(i, "unknown op %q; use add_panel, update_panel, remove_panel, move_panel, set_variable, remove_variable, set_time or rename", op.Op)
		}
	}
	return out, repack, nil
}

func clone(spec panel.Dashboard) (panel.Dashboard, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return panel.Dashboard{}, err
	}
	var out panel.Dashboard
	err = json.Unmarshal(raw, &out)
	return out, err
}

// mergePanel replaces the fields named in set and decodes strictly, so a
// misspelled field is an error rather than a silent no-op.
func mergePanel(p panel.Panel, set map[string]any) (panel.Panel, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return panel.Panel{}, err
	}
	fields := map[string]any{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return panel.Panel{}, err
	}
	for key, value := range set {
		if value == nil {
			delete(fields, key)
		} else {
			fields[key] = value
		}
	}
	merged, err := json.Marshal(fields)
	if err != nil {
		return panel.Panel{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(merged))
	decoder.DisallowUnknownFields()
	var out panel.Panel
	if err := decoder.Decode(&out); err != nil {
		return panel.Panel{}, err
	}
	return out, nil
}
```

- [ ] **Step 5: Implement the default spec**

`internal/dashboard/defaults.go`:

```go
package dashboard

import "github.com/labstack/fanout/internal/panel"

// DefaultSpec is every owner's first dashboard: the shape of the whole system,
// filterable to one service.
func DefaultSpec() panel.Dashboard {
	server := "kind = 'SPAN_KIND_SERVER'"
	service := "service = $service"
	where := []string{server, service}
	return panel.Dashboard{
		Version:     panel.SpecVersion,
		Name:        "System overview",
		Description: "Traffic, errors and latency across services, with the slowest endpoints.",
		Time:        panel.Time{Range: "1h", Refresh: "30s"},
		Variables:   []panel.Variable{{Name: "service", Kind: "query", From: "spans", Field: "service", IncludeAll: true, Default: panel.AllValue}},
		Panels: []panel.Panel{
			{ID: "requests", Title: "Requests per second", Viz: "stat", Better: "higher", Query: &panel.Query{From: "spans", Where: where, Measures: []string{"rate()"}}},
			{ID: "errors", Title: "Error rate", Viz: "stat", Query: &panel.Query{From: "spans", Where: where, Measures: []string{"error_rate()"}}, Thresholds: []panel.Threshold{{Value: 1, Status: "warn"}, {Value: 5, Status: "bad"}}},
			{ID: "latency_p95", Title: "p95 latency", Viz: "stat", Query: &panel.Query{From: "spans", Where: where, Measures: []string{"p95(duration_ms)"}}, Thresholds: []panel.Threshold{{Value: 750, Status: "warn"}, {Value: 2000, Status: "bad"}}},
			{ID: "error_logs", Title: "Error logs", Viz: "stat", Query: &panel.Query{From: "logs", Where: []string{"upper(severity) IN ('ERROR', 'FATAL')", service}, Measures: []string{"count()"}}, Thresholds: []panel.Threshold{{Value: 1, Status: "warn"}}},
			{ID: "traffic", Title: "Requests by service", Viz: "timeseries", Options: &panel.Options{Style: "stacked"}, Query: &panel.Query{From: "spans", Where: where, Measures: []string{"rate()"}, By: []string{"service"}, Bucket: "auto"}},
			{ID: "latency", Title: "p95 latency by service", Viz: "timeseries", Query: &panel.Query{From: "spans", Where: where, Measures: []string{"p95(duration_ms)"}, By: []string{"service"}, Bucket: "auto"}, Thresholds: []panel.Threshold{{Value: 2000, Status: "bad"}}},
			{ID: "error_services", Title: "Error rate by service", Viz: "bar", Width: 4, Click: &panel.Click{SetVariable: "service"}, Query: &panel.Query{From: "spans", Where: []string{server}, Measures: []string{"error_rate()"}, By: []string{"service"}, Limit: 10}},
			{ID: "endpoints", Title: "Slowest endpoints", Viz: "table", Width: 8, Query: &panel.Query{From: "spans", Where: []string{server, service, "http_route <> ''"}, Measures: []string{"p95(duration_ms)", "error_rate()", "rate()"}, By: []string{"service", "http_route"}, Sort: "p95", Limit: 15}},
		},
	}
}
```

- [ ] **Step 6: Rewrite the service**

`internal/dashboard/service.go` (replace the file):

```go
package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/db/generated"
	appid "github.com/labstack/fanout/internal/id"
	"github.com/labstack/fanout/internal/panel"
)

var (
	ErrNotFound = errors.New("dashboard not found")
	ErrConflict = errors.New("a dashboard with that name already exists")
	ErrStale    = errors.New("the dashboard changed since it was read")
)

// Validator checks a spec completely, including its filters and SQL.
// *panel.Executor implements it.
type Validator interface {
	Validate(ctx context.Context, d *panel.Dashboard) error
}

type Author struct {
	Kind string // user, agent or system
	ID   string
}

type Record struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	IsDefault   bool            `json:"is_default"`
	Version     int             `json:"version"`
	Spec        panel.Dashboard `json:"spec"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

type Summary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsDefault   bool   `json:"is_default"`
	Version     int    `json:"version"`
	PanelCount  int    `json:"panel_count"`
	UpdatedAt   string `json:"updated_at"`
}

type VersionInfo struct {
	Version    int    `json:"version"`
	AuthorKind string `json:"author_kind"`
	AuthorID   string `json:"author_id,omitempty"`
	Message    string `json:"message,omitempty"`
	CreatedAt  string `json:"created_at"`
}

type Service struct {
	db        *sql.DB
	validator Validator
	now       func() time.Time
}

const keepVersions = 100

func New(db *sql.DB, validator Validator) *Service {
	return &Service{db: db, validator: validator, now: time.Now}
}

func (s *Service) List(ctx context.Context, ownerID string) ([]Summary, error) {
	if err := s.ensureInitial(ctx, ownerID); err != nil {
		return nil, err
	}
	rows, err := generated.New(s.db).ListDashboards(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(rows))
	for _, r := range rows {
		out = append(out, Summary{ID: r.ID, Name: r.Name, Description: r.Description, IsDefault: r.IsDefault == 1, Version: int(r.Version), PanelCount: int(r.PanelCount), UpdatedAt: r.UpdatedAt})
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, ownerID, id string) (Record, error) {
	if err := s.ensureInitial(ctx, ownerID); err != nil {
		return Record{}, err
	}
	return s.get(ctx, ownerID, id)
}

func (s *Service) get(ctx context.Context, ownerID, id string) (Record, error) {
	row, err := generated.New(s.db).GetDashboard(ctx, generated.GetDashboardParams{ID: id, OwnerID: ownerID})
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	record := Record{ID: row.ID, Name: row.Name, Description: row.Description, IsDefault: row.IsDefault == 1, Version: int(row.Version), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if err := json.Unmarshal([]byte(row.SpecJson), &record.Spec); err != nil {
		return Record{}, fmt.Errorf("decode dashboard %s: %w", id, err)
	}
	return record, nil
}

// prepare normalizes, validates and packs a spec for storage.
func (s *Service) prepare(ctx context.Context, spec *panel.Dashboard, repack bool) error {
	panel.Normalize(spec)
	if err := s.validator.Validate(ctx, spec); err != nil {
		return err
	}
	if repack {
		for i := range spec.Panels {
			spec.Panels[i].Grid = nil
		}
	}
	if repack || needsPack(spec.Panels) {
		Pack(spec.Panels)
	}
	return nil
}

func (s *Service) Create(ctx context.Context, ownerID string, spec panel.Dashboard, author Author) (Record, error) {
	if err := s.prepare(ctx, &spec, false); err != nil {
		return Record{}, err
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return Record{}, err
	}
	id, err := appid.New()
	if err != nil {
		return Record{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := generated.New(tx)
	count, err := q.CountDashboards(ctx, ownerID)
	if err != nil {
		return Record{}, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	isDefault := int64(0)
	if count == 0 {
		isDefault = 1
	}
	if err := q.InsertDashboard(ctx, generated.InsertDashboardParams{ID: id, OwnerID: ownerID, Name: spec.Name, Description: spec.Description, IsDefault: isDefault, Version: 1, SpecJson: string(raw), PanelCount: int64(len(spec.Panels)), CreatedAt: now, UpdatedAt: now}); err != nil {
		if isUnique(err) {
			return Record{}, ErrConflict
		}
		return Record{}, err
	}
	if err := q.InsertDashboardVersion(ctx, generated.InsertDashboardVersionParams{DashboardID: id, Version: 1, SpecJson: string(raw), AuthorKind: author.Kind, AuthorID: author.ID, Message: "Created", CreatedAt: now}); err != nil {
		return Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return Record{}, err
	}
	return s.get(ctx, ownerID, id)
}

// Replace stores a whole new spec. baseVersion 0 means "the latest".
func (s *Service) Replace(ctx context.Context, ownerID, id string, spec panel.Dashboard, baseVersion int, author Author, message string) (Record, error) {
	return s.update(ctx, ownerID, id, baseVersion, author, message, func(panel.Dashboard) (panel.Dashboard, bool, error) {
		return spec, false, nil
	})
}

// Edit applies typed operations atomically. baseVersion 0 means "the latest".
func (s *Service) Edit(ctx context.Context, ownerID, id string, ops []Operation, baseVersion int, author Author, message string) (Record, error) {
	return s.update(ctx, ownerID, id, baseVersion, author, message, func(current panel.Dashboard) (panel.Dashboard, bool, error) {
		return Apply(current, ops)
	})
}

func (s *Service) Restore(ctx context.Context, ownerID, id string, version int, author Author) (Record, error) {
	if _, err := s.get(ctx, ownerID, id); err != nil {
		return Record{}, err
	}
	raw, err := generated.New(s.db).GetDashboardVersion(ctx, generated.GetDashboardVersionParams{DashboardID: id, Version: int64(version)})
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	var spec panel.Dashboard
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return Record{}, err
	}
	return s.update(ctx, ownerID, id, 0, author, fmt.Sprintf("Restored version %d", version), func(panel.Dashboard) (panel.Dashboard, bool, error) {
		return spec, false, nil
	})
}

// update reads, changes, validates and writes with an optimistic version
// check. With baseVersion 0 a concurrent write is retried once on the newer
// version; with an explicit baseVersion it fails with ErrStale instead of
// overwriting what someone else saved.
func (s *Service) update(ctx context.Context, ownerID, id string, baseVersion int, author Author, message string, change func(panel.Dashboard) (panel.Dashboard, bool, error)) (Record, error) {
	for attempt := 0; attempt < 2; attempt++ {
		current, err := s.get(ctx, ownerID, id)
		if err != nil {
			return Record{}, err
		}
		if baseVersion != 0 && current.Version != baseVersion {
			return Record{}, ErrStale
		}
		next, repack, err := change(current.Spec)
		if err != nil {
			return Record{}, err
		}
		if err := s.prepare(ctx, &next, repack); err != nil {
			return Record{}, err
		}
		raw, err := json.Marshal(next)
		if err != nil {
			return Record{}, err
		}
		written, err := s.write(ctx, ownerID, id, current.Version, next, string(raw), author, message)
		if err != nil {
			return Record{}, err
		}
		if written {
			return s.get(ctx, ownerID, id)
		}
		if baseVersion != 0 {
			return Record{}, ErrStale
		}
	}
	return Record{}, ErrStale
}

func (s *Service) write(ctx context.Context, ownerID, id string, base int, spec panel.Dashboard, raw string, author Author, message string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	q := generated.New(tx)
	now := s.now().UTC().Format(time.RFC3339Nano)
	next := int64(base + 1)
	affected, err := q.UpdateDashboard(ctx, generated.UpdateDashboardParams{Name: spec.Name, Description: spec.Description, NextVersion: next, SpecJson: raw, PanelCount: int64(len(spec.Panels)), UpdatedAt: now, ID: id, OwnerID: ownerID, BaseVersion: int64(base)})
	if err != nil {
		if isUnique(err) {
			return false, ErrConflict
		}
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	if err := q.InsertDashboardVersion(ctx, generated.InsertDashboardVersionParams{DashboardID: id, Version: next, SpecJson: raw, AuthorKind: author.Kind, AuthorID: author.ID, Message: strings.TrimSpace(message), CreatedAt: now}); err != nil {
		return false, err
	}
	if err := q.PruneDashboardVersions(ctx, generated.PruneDashboardVersionsParams{DashboardID: id, KeepFrom: next - keepVersions + 1}); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Service) Delete(ctx context.Context, ownerID, id string) error {
	current, err := s.get(ctx, ownerID, id)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := generated.New(tx)
	if _, err := q.DeleteDashboard(ctx, generated.DeleteDashboardParams{ID: id, OwnerID: ownerID}); err != nil {
		return err
	}
	if current.IsDefault {
		if err := q.PromoteNewestDashboard(ctx, ownerID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) Versions(ctx context.Context, ownerID, id string) ([]VersionInfo, error) {
	if _, err := s.get(ctx, ownerID, id); err != nil {
		return nil, err
	}
	rows, err := generated.New(s.db).ListDashboardVersions(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]VersionInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, VersionInfo{Version: int(r.Version), AuthorKind: r.AuthorKind, AuthorID: r.AuthorID, Message: r.Message, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// ensureInitial gives an owner with no dashboards the default one.
func (s *Service) ensureInitial(ctx context.Context, ownerID string) error {
	if strings.TrimSpace(ownerID) == "" {
		return errors.New("dashboard owner is required")
	}
	count, err := generated.New(s.db).CountDashboards(ctx, ownerID)
	if err != nil || count > 0 {
		return err
	}
	_, err = s.Create(ctx, ownerID, DefaultSpec(), Author{Kind: "system"})
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}

// isUnique detects SQLite unique-constraint violations by the driver message.
func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `just test ./internal/dashboard/...`
Expected: PASS. Packages that still call the old `dashboard.New(db, retention)` (`cmd/fanout`, `internal/api`, `internal/mcp`) fail to compile until Tasks 10 and 11; run only this package now.

- [ ] **Step 8: Commit**

```bash
git add internal/dashboard
git commit -m "feat(dashboard): store versioned specs with packing and typed edits"
```

---

### Task 10: Dashboard HTTP API

**Files:**
- Rewrite: `internal/api/dashboard.go`, `internal/api/dashboard_test.go`
- Modify: `cmd/fanout/main.go` (construct `dashboard.New(sqlite.DB, panels)` after the executor)
- Generated: `site/src/content/docs/reference/http-routes.mdx`

**Interfaces:**
- Consumes: `dashboard.Service` (Task 9), `decodeStrict`, `writeProblems` (Task 7).
- Produces routes: `GET /api/dashboards` → `{"dashboards":[Summary]}`; `POST /api/dashboards` body `{"spec":…}` → 201 `Record`; `GET /api/dashboards/:id` → `Record`; `PUT /api/dashboards/:id` body `{"spec":…,"base_version":n,"message":"…"}`; `PATCH /api/dashboards/:id` body `{"operations":[…],"base_version":n,"message":"…"}`; `DELETE /api/dashboards/:id` (header `Fanout-Confirm-Delete: <id>`); `GET /api/dashboards/:id/versions` → `{"versions":[VersionInfo]}`; `POST /api/dashboards/:id/versions/:version/restore` → `Record`. Errors: 404 not found, 409 name conflict or stale version, 400 problems.

- [ ] **Step 1: Write the failing test**

`internal/api/dashboard_test.go` (replace the file):

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
)

type structuralValidator struct{}

func (structuralValidator) Validate(_ context.Context, d *panel.Dashboard) error {
	panel.Normalize(d)
	if problems := panel.Validate(d); len(problems) > 0 {
		return problems
	}
	return nil
}

func TestAnonymousCannotOwnDashboards(t *testing.T) {
	s := newTestAuthServer(t)
	if _, err := s.users.Create("admin@example.com", "", "admin"); err != nil {
		t.Fatalf("Create admin: %v", err)
	}
	RegisterDashboardRoutes(s.e, dashboard.New(s.db.DB, structuralValidator{}))
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dashboards", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous dashboard list = %d, want 401", rec.Code)
	}
}

func TestDashboardLifecycleOverHTTP(t *testing.T) {
	s := newTestAuthServer(t)
	if _, err := s.users.Create("admin@example.com", "", "admin"); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.users.Create("owner@example.com", "", "operator")
	other, _ := s.users.Create("other@example.com", "", "operator")
	RegisterDashboardRoutes(s.e, dashboard.New(s.db.DB, structuralValidator{}))
	ownerCookie, otherCookie := s.login(t, owner), s.login(t, other)
	call := func(method, path, body string, cookie *http.Cookie, headers ...string) *httptest.ResponseRecorder {
		var reader *strings.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := sessionRequest(method, path, reader, cookie)
		req.Header.Set("Content-Type", "application/json")
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		return rec
	}

	rec := call(http.MethodPost, "/api/dashboards", `{"spec":{"name":"Ops","panels":[{"id":"notes","title":"Notes","viz":"text","content":"hi"}]}}`, ownerCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	var created dashboard.Record
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	if rec := call(http.MethodGet, "/api/dashboards/"+created.ID, "", otherCookie); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-owner read = %d", rec.Code)
	}
	rec = call(http.MethodPatch, "/api/dashboards/"+created.ID, `{"operations":[{"op":"update_panel","id":"notes","set":{"content":"changed"}}],"base_version":1,"message":"Reword"}`, ownerCookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"version":2`) {
		t.Fatalf("patch %d %s", rec.Code, rec.Body)
	}
	rec = call(http.MethodPut, "/api/dashboards/"+created.ID, `{"spec":{"name":"Ops","panels":[{"id":"notes","title":"Notes","viz":"text","content":"x"}]},"base_version":1}`, ownerCookie)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "changed since") {
		t.Fatalf("stale put %d %s", rec.Code, rec.Body)
	}
	rec = call(http.MethodPut, "/api/dashboards/"+created.ID, `{"spec":{"name":"Ops","panels":[{"id":"notes","title":"Notes","viz":"piechart"}]}}`, ownerCookie)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"problems"`) {
		t.Fatalf("invalid put %d %s", rec.Code, rec.Body)
	}
	rec = call(http.MethodGet, "/api/dashboards/"+created.ID+"/versions", "", ownerCookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"Reword"`) {
		t.Fatalf("versions %d %s", rec.Code, rec.Body)
	}
	rec = call(http.MethodPost, "/api/dashboards/"+created.ID+"/versions/1/restore", "", ownerCookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"version":3`) {
		t.Fatalf("restore %d %s", rec.Code, rec.Body)
	}
	if rec := call(http.MethodDelete, "/api/dashboards/"+created.ID, "", ownerCookie); rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("unconfirmed delete = %d", rec.Code)
	}
	if rec := call(http.MethodDelete, "/api/dashboards/"+created.ID, "", ownerCookie, "Fanout-Confirm-Delete", created.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `just test ./internal/api/ -run 'Dashboard' -v`
Expected: FAIL — compile errors against the old handler.

- [ ] **Step 3: Rewrite the handler**

`internal/api/dashboard.go` (replace the file):

```go
package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
)

type DashboardHandler struct{ dashboards *dashboard.Service }

func RegisterDashboardRoutes(e *echo.Echo, dashboards *dashboard.Service) {
	h := &DashboardHandler{dashboards: dashboards}
	own := RequireCapability(ManageOwnDashboards)
	e.GET("/api/dashboards", h.list, own)
	e.POST("/api/dashboards", h.create, own)
	e.GET("/api/dashboards/:id", h.get, own)
	e.PUT("/api/dashboards/:id", h.replace, own)
	e.PATCH("/api/dashboards/:id", h.edit, own)
	e.DELETE("/api/dashboards/:id", h.delete, own)
	e.GET("/api/dashboards/:id/versions", h.versions, own)
	e.POST("/api/dashboards/:id/versions/:version/restore", h.restore, own)
}

const dashboardBodyLimit = 512 << 10

type createDashboardBody struct {
	Spec panel.Dashboard `json:"spec"`
}

type replaceDashboardBody struct {
	Spec        panel.Dashboard `json:"spec"`
	BaseVersion int             `json:"base_version,omitempty"`
	Message     string          `json:"message,omitempty"`
}

type editDashboardBody struct {
	Operations  []dashboard.Operation `json:"operations"`
	BaseVersion int                   `json:"base_version,omitempty"`
	Message     string                `json:"message,omitempty"`
}

func userAuthor(owner string) dashboard.Author { return dashboard.Author{Kind: "user", ID: owner} }

func (h *DashboardHandler) list(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	items, err := h.dashboards.List(c.Request().Context(), owner)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"dashboards": items})
}

func (h *DashboardHandler) create(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	var body createDashboardBody
	if err := decodeStrict(c, &body, dashboardBodyLimit); err != nil {
		return err
	}
	record, err := h.dashboards.Create(c.Request().Context(), owner, body.Spec, userAuthor(owner))
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusCreated, record)
}

func (h *DashboardHandler) get(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	record, err := h.dashboards.Get(c.Request().Context(), owner, c.Param("id"))
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, record)
}

func (h *DashboardHandler) replace(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	var body replaceDashboardBody
	if err := decodeStrict(c, &body, dashboardBodyLimit); err != nil {
		return err
	}
	record, err := h.dashboards.Replace(c.Request().Context(), owner, c.Param("id"), body.Spec, body.BaseVersion, userAuthor(owner), body.Message)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, record)
}

func (h *DashboardHandler) edit(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	var body editDashboardBody
	if err := decodeStrict(c, &body, dashboardBodyLimit); err != nil {
		return err
	}
	record, err := h.dashboards.Edit(c.Request().Context(), owner, c.Param("id"), body.Operations, body.BaseVersion, userAuthor(owner), body.Message)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, record)
}

func (h *DashboardHandler) delete(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	if c.Request().Header.Get("Fanout-Confirm-Delete") != c.Param("id") {
		return echo.NewHTTPError(http.StatusPreconditionRequired, "dashboard deletion requires confirmation")
	}
	if err := h.dashboards.Delete(c.Request().Context(), owner, c.Param("id")); err != nil {
		return dashboardError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *DashboardHandler) versions(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	versions, err := h.dashboards.Versions(c.Request().Context(), owner, c.Param("id"))
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"versions": versions})
}

func (h *DashboardHandler) restore(c *echo.Context) error {
	owner, err := RequestOwner(c)
	if err != nil {
		return err
	}
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil || version < 1 {
		return echo.NewHTTPError(http.StatusBadRequest, "version must be a positive integer")
	}
	record, err := h.dashboards.Restore(c.Request().Context(), owner, c.Param("id"), version, userAuthor(owner))
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, record)
}

func dashboardError(c *echo.Context, err error) error {
	if handled, writeErr := writeProblems(c, err); handled {
		return writeErr
	}
	switch {
	case errors.Is(err, dashboard.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "dashboard not found")
	case errors.Is(err, dashboard.ErrConflict):
		return echo.NewHTTPError(http.StatusConflict, "a dashboard with that name already exists")
	case errors.Is(err, dashboard.ErrStale):
		return echo.NewHTTPError(http.StatusConflict, "The dashboard changed since you opened it. Reload to see the latest version.")
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, "dashboard operation failed").Wrap(err)
	}
}
```

- [ ] **Step 4: Rewire the binary**

In `cmd/fanout/main.go`, move the executor above the dashboards and pass it as the validator:

```go
	panels := panel.NewExecutor(q, cfg.RetentionDays)
	api.RegisterPanelRoutes(e, panels)
	dashboards := dashboard.New(sqlite.DB, panels)
	api.RegisterDashboardRoutes(e, dashboards)
```

- [ ] **Step 5: Run the tests and regenerate docs**

Run: `just test ./internal/api/ ./internal/dashboard/...`
Expected: PASS (other `internal/api` tests that referenced `dashboard.New(db, 30)` are updated to `dashboard.New(db, structuralValidator{})`).
Run: `just docs-generate`
Expected: route reference updated with PATCH, versions and restore.

- [ ] **Step 6: Commit**

```bash
git add internal/api cmd/fanout site/src/content/docs/reference
git commit -m "feat(api): serve versioned dashboard specs with typed edits and restore"
```

---

### Task 11: MCP tools and the agent prompt

**Files:**
- Create: `internal/mcp/panels.go`
- Rewrite: `internal/mcp/dashboards.go`
- Modify: `internal/mcp/server.go` (constructor takes the executor, instructions), `internal/mcp/describe.go`, `internal/agent/runtime.go` (system prompt), `cmd/fanout/main.go`
- Delete: `internal/mcp/dashboards_probe_test.go`
- Test: `internal/mcp/panels_test.go`; update `internal/mcp/server_test.go`, `internal/mcp/describe_test.go`, `internal/api/oauth_test.go`, `internal/agent/tools_integration_test.go`
- Generated: `site/src/content/docs/reference/mcp-tools.mdx`

**Interfaces:**
- Consumes: `panel.Executor` (`Run`, `Schema`), `dashboard.Service`, `dashboard.Operation`, `dashboardOwner` (kept).
- Produces: `func New(queries Observability, dashboards *dashboard.Service, panels *panel.Executor, version string) *Server`; `func NewWithIntelligence(queries Observability, dashboards *dashboard.Service, panels *panel.Executor, snapshots IntelligenceSnapshots, version string) *Server`; tools `get_telemetry_schema`, `preview_panels`, `list_dashboards`, `get_dashboard`, `create_dashboard`, `replace_dashboard`, `edit_dashboard`; `RequiredToolScope(name string) string` returns `dashboard:manage` for the five dashboard tools only.

- [ ] **Step 1: Write the failing test**

`internal/mcp/panels_test.go`:

```go
package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/query"
	appstore "github.com/labstack/fanout/internal/store"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newPanelServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	duck, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = duck.Close(); _ = repo.Close(); _ = sqlite.Close() })
	if _, err := sqlite.DB.Exec(`INSERT INTO users (id, email) VALUES ('owner', 'owner@example.com')`); err != nil {
		t.Fatal(err)
	}
	executor := panel.NewExecutor(duck, 30)
	return New(&fakeObservability{}, dashboard.New(sqlite.DB, executor), executor, "test")
}

func ownerRequest() *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{dashboard.OwnerMetaKey: "owner"}}}
}

func TestPreviewPanelsReportsEachPanel(t *testing.T) {
	s := newPanelServer(t)
	_, out, err := s.previewPanels(t.Context(), nil, PreviewInput{Panels: []panel.Panel{
		{ID: "requests", Title: "Requests", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"count()"}}},
		{ID: "typo", Title: "Typo", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"p95(duraton_ms)"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Panels[1].Status != "invalid" || !strings.Contains(out.Panels[1].Problems[0].Message+out.Panels[1].Problems[0].Hint, "duration_ms") {
		t.Fatalf("typo preview = %+v", out.Panels[1])
	}
	if out.Panels[0].Status != "not_run" {
		t.Fatalf("a valid panel is not run while another is invalid: %+v", out.Panels[0])
	}
	_, out, err = s.previewPanels(t.Context(), nil, PreviewInput{Panels: []panel.Panel{{ID: "requests", Title: "Requests", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"count()"}}}}})
	if err != nil || out.Panels[0].Status != "empty" || out.Panels[0].Diagnosis == "" {
		t.Fatalf("empty store preview = %+v %v", out.Panels, err)
	}
}

func TestCreateAndEditDashboardTools(t *testing.T) {
	s := newPanelServer(t)
	spec := panel.Dashboard{Name: "Checkout", Panels: []panel.Panel{
		{ID: "notes", Title: "Notes", Viz: "text", Content: "Money path."},
		{ID: "requests", Title: "Requests", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"count()"}}},
	}}
	result, out, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: spec})
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if out.Dashboard.Version != 1 || !strings.Contains(text, "requests") || !strings.Contains(text, "No spans") {
		t.Fatalf("create summary %q record %+v", text, out.Dashboard)
	}
	_, edited, err := s.dashboardEdit(t.Context(), ownerRequest(), DashboardEditInput{ID: out.Dashboard.ID, Operations: []dashboard.Operation{{Op: "remove_panel", ID: "requests"}}, Message: "Drop the empty panel"})
	if err != nil || edited.Dashboard.Version != 2 || len(edited.Dashboard.Spec.Panels) != 1 {
		t.Fatalf("edit = %+v %v", edited, err)
	}
}

func TestDashboardToolScopes(t *testing.T) {
	for _, name := range []string{"list_dashboards", "get_dashboard", "create_dashboard", "replace_dashboard", "edit_dashboard"} {
		if RequiredToolScope(name) != dashboard.OAuthScope {
			t.Errorf("%s scope = %q", name, RequiredToolScope(name))
		}
	}
	for _, name := range []string{"get_telemetry_schema", "preview_panels"} {
		if RequiredToolScope(name) != "" {
			t.Errorf("%s needs only telemetry:read", name)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `just test ./internal/mcp/ -run 'TestPreviewPanels|TestCreateAndEdit|TestDashboardToolScopes' -v`
Expected: FAIL — undefined `PreviewInput`, `previewPanels`, `dashboardEdit`, constructor arity.

- [ ] **Step 3: Implement the panel tools**

`internal/mcp/panels.go`:

```go
package mcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/panel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type SchemaInput struct {
	Window    string `json:"window,omitempty" jsonschema:"5m, 15m, 1h, 3h, 6h, 12h or 24h; default 1h"`
	Namespace string `json:"namespace,omitempty" jsonschema:"Limit discovery to one service namespace"`
}

type PreviewInput struct {
	Panels    []panel.Panel     `json:"panels" jsonschema:"Panels to check and run, in the same format as dashboard panels"`
	Variables []panel.Variable  `json:"variables,omitempty" jsonschema:"Variables the panels reference"`
	Time      *panel.Time       `json:"time,omitempty" jsonschema:"Time range; default the last hour"`
	Vars      map[string]string `json:"vars,omitempty" jsonschema:"Variable values to preview with; $__all selects All"`
}

type PanelPreview struct {
	ID        string          `json:"id"`
	Status    string          `json:"status" jsonschema:"ok, empty, error, invalid or not_run"`
	Rows      int             `json:"rows,omitempty"`
	Columns   []string        `json:"columns,omitempty"`
	Sample    [][]any         `json:"sample,omitempty"`
	Interval  string          `json:"interval,omitempty"`
	Diagnosis string          `json:"diagnosis,omitempty"`
	Error     string          `json:"error,omitempty"`
	Problems  []panel.Problem `json:"problems,omitempty"`
}

type PreviewOutput struct {
	Panels   []PanelPreview  `json:"panels"`
	Problems []panel.Problem `json:"problems,omitempty"`
}

const specGuide = `A dashboard is {name, description, time:{range}, variables, panels}. Each panel: {id (lowercase), title, viz, width 1-12, query or sql}. viz: stat or gauge (one measure; gauge needs min and max), timeseries (bucket auto, at most one by), bar (one or two by), table (up to three by), text (content in Markdown). query: {from: spans|logs|metrics, where: [filter expressions], measures: [fn(field) as alias], by: [fields], bucket, sort, limit}. Measures: count(), rate() per second, error_rate() percent, share() percent, avg/min/max/sum(field), last(value) for metrics, p50/p75/p90/p95/p99(field), quantile(field, 0.999), count_distinct(field). Fields are columns from get_telemetry_schema or attributes['key'] / resource['key'] with the key written literally. Filters are SQL boolean expressions: service = $service, kind = 'SPAN_KIND_SERVER', status = 'STATUS_CODE_ERROR', http_route IN $routes, attributes['http.response.status_code']::INTEGER >= 500. Variables: {name, kind: query|custom|constant|text, from, field, default, include_all}; $__all selects All and removes filters that use the variable. sql: one SELECT over spans, logs, metrics, service_rollup or edge_rollup that filters time with $__window(column). Use values exactly as get_telemetry_schema lists them. Example panel: {"id":"latency","title":"Checkout latency","viz":"timeseries","width":8,"query":{"from":"spans","where":["service = 'checkout'","kind = 'SPAN_KIND_SERVER'"],"measures":["p50(duration_ms)","p95(duration_ms)","p99(duration_ms)"],"bucket":"auto"},"unit":"ms","thresholds":[{"value":1500,"status":"bad"}]}.`

func (s *Server) registerPanelTools() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "get_telemetry_schema", Title: "Telemetry schema",
		Description: "List the signals, columns with their common values, attribute keys seen per service, metric names, services, measure functions and units. Read this before drafting panels so filters use values that exist.",
		Annotations: readOnly,
	}, s.telemetrySchema)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "preview_panels", Title: "Preview panels",
		Description: "Check and run panels without saving them. Each panel reports ok with rows and a sample, empty with the reason, error, or invalid with the exact field and a suggestion. Fix every invalid panel and replace or explain every empty one before saving. " + specGuide,
		Annotations: readOnly,
	}, s.previewPanels)
}

func (s *Server) telemetrySchema(ctx context.Context, _ *mcp.CallToolRequest, input SchemaInput) (*mcp.CallToolResult, *panel.Schema, error) {
	if s.panels == nil {
		return nil, nil, errors.New("panels are unavailable")
	}
	schema, err := s.panels.Schema(ctx, panel.SchemaRequest{Window: input.Window, Namespace: input.Namespace})
	if err != nil {
		return nil, nil, err
	}
	attributes := 0
	for _, sig := range schema.Signals {
		attributes += len(sig.Attributes)
	}
	return summary(fmt.Sprintf("%d services, %d attribute keys and %d metric names in the last %s.", len(schema.Services), attributes, len(schema.Signals["metrics"].Metrics), schema.Window)), schema, nil
}

var panelPath = regexp.MustCompile(`^panels\[(\d+)\]`)

func (s *Server) previewPanels(ctx context.Context, _ *mcp.CallToolRequest, input PreviewInput) (*mcp.CallToolResult, PreviewOutput, error) {
	if s.panels == nil {
		return nil, PreviewOutput{}, errors.New("panels are unavailable")
	}
	d := panel.Dashboard{Name: "Preview", Variables: input.Variables, Panels: input.Panels}
	if input.Time != nil {
		d.Time = *input.Time
	}
	vars := map[string]panel.Value{}
	for name, value := range input.Vars {
		if value == panel.AllValue {
			vars[name] = panel.Value{All: true}
		} else {
			vars[name] = panel.Value{Values: []string{value}}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out := PreviewOutput{Panels: make([]PanelPreview, len(input.Panels))}
	for i, p := range input.Panels {
		out.Panels[i] = PanelPreview{ID: p.ID, Status: "not_run"}
	}
	results, err := s.panels.Run(ctx, panel.RunRequest{Dashboard: d, Vars: vars})
	var problems panel.Problems
	if errors.As(err, &problems) {
		for _, problem := range problems {
			if m := panelPath.FindStringSubmatch(problem.Path); m != nil {
				i, _ := strconv.Atoi(m[1])
				if i < len(out.Panels) {
					out.Panels[i].Status = "invalid"
					out.Panels[i].Problems = append(out.Panels[i].Problems, problem)
					continue
				}
			}
			out.Problems = append(out.Problems, problem)
		}
		return summary(previewSummary(out)), out, nil
	}
	if err != nil {
		return nil, PreviewOutput{}, err
	}
	for i, r := range results {
		preview := PanelPreview{ID: r.ID, Status: r.Status, Interval: r.Interval, Diagnosis: r.Diagnosis, Error: r.Error}
		if r.Frame != nil {
			preview.Rows = r.Frame.Rows
			for _, c := range r.Frame.Columns {
				preview.Columns = append(preview.Columns, c.Name)
			}
			for row := 0; row < min(3, r.Frame.Rows); row++ {
				sample := make([]any, len(r.Frame.Columns))
				for c := range r.Frame.Columns {
					sample[c] = r.Frame.Values[c][row]
				}
				preview.Sample = append(preview.Sample, sample)
			}
		}
		out.Panels[i] = preview
	}
	return summary(previewSummary(out)), out, nil
}

func previewSummary(out PreviewOutput) string {
	counts := map[string]int{}
	var notes []string
	for _, p := range out.Panels {
		counts[p.Status]++
		switch p.Status {
		case "invalid":
			for _, problem := range p.Problems {
				notes = append(notes, fmt.Sprintf("%s: %s", p.ID, strings.TrimSpace(problem.Message+" "+problem.Hint)))
			}
		case "empty":
			notes = append(notes, fmt.Sprintf("%s is empty: %s", p.ID, p.Diagnosis))
		case "error":
			notes = append(notes, fmt.Sprintf("%s failed: %s", p.ID, p.Error))
		}
	}
	for _, problem := range out.Problems {
		notes = append(notes, problem.Path+": "+problem.Message)
	}
	text := fmt.Sprintf("%d ok, %d empty, %d error, %d invalid, %d not run.", counts["ok"], counts["empty"], counts["error"], counts["invalid"], counts["not_run"])
	if len(notes) > 0 {
		text += " " + strings.Join(notes, " ")
	}
	return text
}
```

- [ ] **Step 4: Rewrite the dashboard tools**

`internal/mcp/dashboards.go` (replace everything above `dashboardOwner`, keep `dashboardOwner`; replace `dashboardToolError`):

```go
package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type DashboardIDInput struct {
	ID string `json:"id" jsonschema:"Dashboard ID returned by list_dashboards or create_dashboard"`
}

type DashboardCreateInput struct {
	Dashboard panel.Dashboard `json:"dashboard" jsonschema:"Complete v1 dashboard spec"`
}

type DashboardReplaceInput struct {
	ID          string          `json:"id" jsonschema:"Dashboard ID"`
	Dashboard   panel.Dashboard `json:"dashboard" jsonschema:"Complete replacement spec; omitted panels are removed"`
	BaseVersion int             `json:"base_version,omitempty" jsonschema:"Version you read; the call fails if someone saved since"`
	Message     string          `json:"message,omitempty" jsonschema:"One line describing the change, shown in history"`
}

type DashboardEditInput struct {
	ID          string                `json:"id" jsonschema:"Dashboard ID"`
	Operations  []dashboard.Operation `json:"operations" jsonschema:"Edits applied in order, atomically"`
	BaseVersion int                   `json:"base_version,omitempty" jsonschema:"Version you read; the call fails if someone saved since"`
	Message     string                `json:"message,omitempty" jsonschema:"One line describing the change, shown in history"`
}

type dashboardListOutput struct {
	Dashboards []dashboard.Summary `json:"dashboards"`
}

type dashboardOutput struct {
	Dashboard dashboard.Record `json:"dashboard"`
	Warnings  []string         `json:"warnings,omitempty"`
}

const (
	listDashboardsTool = iota
	getDashboardTool
	createDashboardTool
	replaceDashboardTool
	editDashboardTool
)

// Registration and transport authorization use the same dashboard tool catalog.
var dashboardTools = [...]mcp.Tool{
	{
		Name: "list_dashboards", Title: "List dashboards",
		Description: "List the authenticated user's dashboards with panel counts and versions.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)},
	},
	{
		Name: "get_dashboard", Title: "Get dashboard",
		Description: "Read one dashboard's complete spec and version. Read it before editing so operations name real panel ids.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)},
	},
	{
		Name: "create_dashboard", Title: "Create dashboard",
		Description: "Create a dashboard for the authenticated user from a complete spec. Read get_telemetry_schema first and preview_panels until every panel is ok or deliberately empty. The result lists any panel that is still empty or failing. " + specGuide,
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)},
	},
	{
		Name: "replace_dashboard", Title: "Replace dashboard",
		Description: "Replace a dashboard's whole spec; omitted panels are removed. Use only for a redesign the user asked for; use edit_dashboard to change a few panels.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), IdempotentHint: true, OpenWorldHint: boolPtr(false)},
	},
	{
		Name: "edit_dashboard", Title: "Edit dashboard",
		Description: "Change a dashboard with typed operations, applied in order and saved as one version: add_panel, update_panel (set replaces the named fields), remove_panel, move_panel, set_variable, remove_variable, set_time, rename. Panels not named are left unchanged. Only edit when the user asks to change that dashboard.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false)},
	},
}

// RequiredToolScope reports additional delegated scope needed by a tool.
// All remote MCP requests already require telemetry:read.
func RequiredToolScope(name string) string {
	for _, tool := range dashboardTools {
		if tool.Name == name {
			return dashboard.OAuthScope
		}
	}
	return ""
}

func (s *Server) registerDashboardTools() {
	if s.dashboards == nil {
		return
	}
	// AddTool copies each tool before inferring schemas, so the catalog stays
	// shared and unmodified across servers.
	mcp.AddTool(s.mcp, &dashboardTools[listDashboardsTool], s.dashboardList)
	mcp.AddTool(s.mcp, &dashboardTools[getDashboardTool], s.dashboardGet)
	mcp.AddTool(s.mcp, &dashboardTools[createDashboardTool], s.dashboardCreate)
	mcp.AddTool(s.mcp, &dashboardTools[replaceDashboardTool], s.dashboardReplace)
	mcp.AddTool(s.mcp, &dashboardTools[editDashboardTool], s.dashboardEdit)
}

func agentAuthor(owner string) dashboard.Author { return dashboard.Author{Kind: "agent", ID: owner} }

func (s *Server) dashboardList(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, dashboardListOutput, error) {
	owner, err := dashboardOwner(req)
	if err != nil {
		return nil, dashboardListOutput{}, err
	}
	items, err := s.dashboards.List(ctx, owner)
	if err != nil {
		return nil, dashboardListOutput{}, dashboardToolError(err)
	}
	return summary(fmt.Sprintf("Found %d dashboards.", len(items))), dashboardListOutput{Dashboards: items}, nil
}

func (s *Server) dashboardGet(ctx context.Context, req *mcp.CallToolRequest, input DashboardIDInput) (*mcp.CallToolResult, dashboardOutput, error) {
	owner, err := dashboardOwner(req)
	if err != nil {
		return nil, dashboardOutput{}, err
	}
	record, err := s.dashboards.Get(ctx, owner, strings.TrimSpace(input.ID))
	if err != nil {
		return nil, dashboardOutput{}, dashboardToolError(err)
	}
	return summary(fmt.Sprintf("Loaded %q, version %d, with %d panels.", record.Name, record.Version, len(record.Spec.Panels))), dashboardOutput{Dashboard: record}, nil
}

func (s *Server) dashboardCreate(ctx context.Context, req *mcp.CallToolRequest, input DashboardCreateInput) (*mcp.CallToolResult, dashboardOutput, error) {
	owner, err := dashboardOwner(req)
	if err != nil {
		return nil, dashboardOutput{}, err
	}
	record, err := s.dashboards.Create(ctx, owner, input.Dashboard, agentAuthor(owner))
	if err != nil {
		return nil, dashboardOutput{}, dashboardToolError(err)
	}
	return s.saved(ctx, "Created", record)
}

func (s *Server) dashboardReplace(ctx context.Context, req *mcp.CallToolRequest, input DashboardReplaceInput) (*mcp.CallToolResult, dashboardOutput, error) {
	owner, err := dashboardOwner(req)
	if err != nil {
		return nil, dashboardOutput{}, err
	}
	record, err := s.dashboards.Replace(ctx, owner, strings.TrimSpace(input.ID), input.Dashboard, input.BaseVersion, agentAuthor(owner), input.Message)
	if err != nil {
		return nil, dashboardOutput{}, dashboardToolError(err)
	}
	return s.saved(ctx, "Replaced", record)
}

func (s *Server) dashboardEdit(ctx context.Context, req *mcp.CallToolRequest, input DashboardEditInput) (*mcp.CallToolResult, dashboardOutput, error) {
	owner, err := dashboardOwner(req)
	if err != nil {
		return nil, dashboardOutput{}, err
	}
	record, err := s.dashboards.Edit(ctx, owner, strings.TrimSpace(input.ID), input.Operations, input.BaseVersion, agentAuthor(owner), input.Message)
	if err != nil {
		return nil, dashboardOutput{}, dashboardToolError(err)
	}
	return s.saved(ctx, "Updated", record)
}

// saveCheckBudget bounds the post-save run that reports empty or failing
// panels. The dashboard is already saved; a slow check is reported as
// unchecked rather than holding the answer.
const saveCheckBudget = 8 * time.Second

func (s *Server) saved(ctx context.Context, verb string, record dashboard.Record) (*mcp.CallToolResult, dashboardOutput, error) {
	out := dashboardOutput{Dashboard: record}
	if s.panels != nil {
		checkCtx, cancel := context.WithTimeout(ctx, saveCheckBudget)
		results, err := s.panels.Run(checkCtx, panel.RunRequest{Dashboard: record.Spec})
		cancel()
		if err == nil {
			for _, r := range results {
				switch r.Status {
				case panel.StatusEmpty:
					out.Warnings = append(out.Warnings, fmt.Sprintf("%s is empty: %s", r.ID, r.Diagnosis))
				case panel.StatusError:
					out.Warnings = append(out.Warnings, fmt.Sprintf("%s failed: %s", r.ID, r.Error))
				}
			}
		}
	}
	text := fmt.Sprintf("%s %q, version %d, with %d panels.", verb, record.Name, record.Version, len(record.Spec.Panels))
	if len(out.Warnings) > 0 {
		text += " Needs attention: " + strings.Join(out.Warnings, " ") + " Fix these with edit_dashboard or tell the user why they are empty."
	}
	return summary(text), out, nil
}

func dashboardToolError(err error) error {
	var problems panel.Problems
	switch {
	case errors.As(err, &problems):
		return problems
	case errors.Is(err, dashboard.ErrNotFound):
		return errors.New("dashboard not found")
	case errors.Is(err, dashboard.ErrConflict):
		return errors.New("a dashboard with that name already exists")
	case errors.Is(err, dashboard.ErrStale):
		return errors.New("the dashboard changed since you read it; get_dashboard again and reapply the change")
	default:
		// MCP tool errors bypass the HTTP request logger, so this is the only
		// place the underlying storage failure gets recorded.
		slog.Error("dashboard tool operation failed", "error", err)
		return errors.New("dashboard operation failed")
	}
}
```

Keep `dashboardOwner` exactly as it is today (same file, below these definitions), and delete `probeLimit`, `probeBudget`, `emptyWidgetNote` and `widgetConfigString`; `slices` stays imported for `dashboardOwner`.

- [ ] **Step 5: Thread the executor through the server**

In `internal/mcp/server.go`:

```go
type Server struct {
	mcp          *mcp.Server
	queries      Observability
	intelligence IntelligenceSnapshots
	dashboards   *dashboard.Service
	panels       *panel.Executor
	now          func() time.Time
}

func New(queries Observability, dashboards *dashboard.Service, panels *panel.Executor, version string) *Server {
	return newServer(queries, dashboards, panels, nil, version)
}

func NewWithIntelligence(queries Observability, dashboards *dashboard.Service, panels *panel.Executor, snapshots IntelligenceSnapshots, version string) *Server {
	return newServer(queries, dashboards, panels, snapshots, version)
}
```

In `newServer`, add the parameter, set `panels: panels`, and call `s.registerPanelTools()` before `s.registerDashboardTools()`. Replace the dashboard sentence of `serverInstructions` with:

```go
"Dashboard tools are scoped to the authenticated user. To build a dashboard, read get_telemetry_schema, draft panels, run preview_panels until every panel is ok or deliberately empty, then create_dashboard. To change one, get_dashboard first and use edit_dashboard; replace only for a redesign the user asked for."
```

Update callers: `cmd/fanout/main.go` → `mcp.NewWithIntelligence(queries, dashboards, panels, detector, version)`; `internal/mcp/describe.go` and `describe_test.go` → `NewWithIntelligence(nil, dashboard.New(nil, nil), nil, describeIntelligence{}, "docgen")`; `internal/mcp/server_test.go` → `New(&fakeObservability{}, dashboard.New(database.DB, structural), nil, "test")` with a local structural validator like Task 9's; `internal/agent/tools_integration_test.go` → add `nil` for panels. Delete `internal/mcp/dashboards_probe_test.go` (its subject, the widget probe, is gone). In `describe_test.go` and `internal/api/oauth_test.go`, extend the dashboard tool name lists with `edit_dashboard`, and keep the destructive-hint assertion for `replace_dashboard`; add the same assertion for `edit_dashboard`.

- [ ] **Step 6: Update the agent prompt and chat labels**

In `internal/agent/runtime.go`, replace the sentences from "You can also list, inspect, create, and replace the user's named dashboards." through "inspect it first and preserve unrelated views." with:

```text
You can build and change the user's dashboards. To build one, read get_telemetry_schema, draft a complete spec of panels that answer the request, run preview_panels, fix every invalid panel, replace or explain every empty one, and only then call create_dashboard. Prefer a few precise panels over many vague ones: headline stats first, then the time series that explain them, then a table of the worst offenders. Use filter values exactly as the schema lists them. To change a dashboard, get_dashboard first and use edit_dashboard so unrelated panels stay as they are; replace only when the user asks for a redesign.
```

In `ui/host/src/app-context.ts`, add to `toolLabels`:

```ts
  get_telemetry_schema: { activity: "Reading what telemetry exists…", title: "Telemetry schema" },
  preview_panels: { activity: "Checking dashboard panels…", title: "Panel check" },
  create_dashboard: { activity: "Building your dashboard…", title: "Dashboard" },
  edit_dashboard: { activity: "Updating your dashboard…", title: "Dashboard" },
  replace_dashboard: { activity: "Redesigning your dashboard…", title: "Dashboard" },
```

- [ ] **Step 7: Run the tests and regenerate docs**

Run: `just test ./internal/mcp/... ./internal/agent/... ./internal/api/... ./cmd/...`
Expected: PASS.
Run: `just docs-generate && git diff --stat site/src/content/docs/reference/mcp-tools.mdx`
Expected: the tool reference lists `get_telemetry_schema`, `preview_panels` and `edit_dashboard`, and the dashboard tools take the v1 spec.

- [ ] **Step 8: Commit**

```bash
git add internal/mcp internal/agent internal/api cmd/fanout ui/host/src/app-context.ts site/src/content/docs/reference
git commit -m "feat(mcp): give the agent schema, preview and typed dashboard edit tools"
```

---

### Task 12: Shared panel compile layer, canvas charts and the API client

**Files:**
- Create: `ui/panels/types.ts`, `ui/panels/units.ts`, `ui/panels/frame.ts`, `ui/panels/thresholds.ts`, `ui/panels/compile.ts`
- Create: `ui/host/src/dashboards/api.ts`, `ui/host/src/dashboards/echart-canvas.tsx`
- Modify: `ui/host/package.json`, `ui/host/bun.lock` (add `@tanstack/react-table`)
- Test: `ui/host/src/dashboards/panels.test.ts`

**Interfaces:**
- Consumes: `ui/format.ts` (`duration`, `percent`), `ui/chart.ts` (`chartTheme`, `statusHex`, `seriesColor`), `ui/tokens.ts`, `authorizedFetch` from `ui/host/src/auth`.
- Produces (TypeScript):
  - `ui/panels/types.ts`: `Unit`, `Threshold`, `Query`, `Panel`, `Variable`, `DashboardTime`, `DashboardSpec`, `Column`, `Frame`, `PanelResult`, `VarValue`, `ALL = "$__all"`
  - `ui/panels/units.ts`: `formatValue(unit: string | undefined, value: number | null | undefined): string`, `formatAxis(unit?: string): (value: number) => string`
  - `ui/panels/frame.ts`: `toSeries(frame: Frame): Series[]`, `toCategories(frame: Frame): Categories`, `measureColumns(frame)`, `reduce(values: (number | null)[], reducer: string): number | null`, `statValue(panel: Panel, frame: Frame): number | null`, `previousStatValue(panel, frame?)`
  - `ui/panels/thresholds.ts`: `statusFor(value: number | null, thresholds?: Threshold[], better?: "lower" | "higher"): "ok" | "warn" | "bad" | null`
  - `ui/panels/compile.ts`: `type ChartTheme`, `chartThemeFor(dark: boolean): ChartTheme`, `timeseriesOption(panel, result, theme)`, `barOption(panel, frame, theme)`, `gaugeOption(panel, value, theme)`
  - `ui/host/src/dashboards/api.ts`: `ApiError`, `listDashboards`, `getDashboard`, `createDashboard`, `replaceDashboard`, `patchDashboard`, `deleteDashboard`, `listVersions`, `restoreVersion`, `queryPanels`, `resolveVariables`, types `DashboardRecord`, `DashboardSummary`, `Operation`, `QueryBody`
  - `ui/host/src/dashboards/echart-canvas.tsx`: `EChartCanvas({ option, height, label, onClick, group })`

- [ ] **Step 1: Add the table library**

Run: `cd ui/host && bun add @tanstack/react-table@^9 && cd ../..`
Then pin the installed version exactly in `ui/host/package.json` (the repo pins every dependency), e.g. `"@tanstack/react-table": "9.0.0"`, and run `cd ui/host && bun install && cd ../..`.
Expected: `bun.lock` updated; `just ui-audit` reports no new advisories.

- [ ] **Step 2: Write the failing tests**

`ui/host/src/dashboards/panels.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { barOption, chartThemeFor, gaugeOption, timeseriesOption } from "../../../panels/compile";
import { reduce, statValue, toCategories, toSeries } from "../../../panels/frame";
import { statusFor } from "../../../panels/thresholds";
import type { Frame, Panel, PanelResult } from "../../../panels/types";
import { formatAxis, formatValue } from "../../../panels/units";

const series: Frame = {
  columns: [{ name: "time", type: "time", role: "time" }, { name: "service", type: "string", role: "dimension" }, { name: "p95", type: "number", role: "measure", unit: "ms" }],
  values: [[1000, 1000, 2000, 2000], ["checkout", "cart", "checkout", "cart"], [120, 40, 900, null]],
  rows: 4,
};

describe("units", () => {
  it("formats by unit without mixing scales", () => {
    expect(formatValue("ms", 850)).toBe("850ms");
    expect(formatValue("ms", 600_000)).toBe("10m");
    expect(formatValue("percent", 4.2)).toBe("4.20%");
    expect(formatValue("per_second", 1234)).toBe("1.2K/s");
    expect(formatValue("bytes", 1536)).toBe("1.5 KiB");
    expect(formatValue("count", null)).toBe("—");
    expect(formatAxis("ms")(1500)).toBe("1.5s");
  });
});

describe("frames", () => {
  it("pivots a grouped series by dimension value", () => {
    const out = toSeries(series);
    expect(out.map((s) => s.name)).toEqual(["checkout", "cart"]);
    expect(out[0].points).toEqual([[1000, 120], [2000, 900]]);
    expect(out[1].points).toEqual([[1000, 40], [2000, null]]);
  });

  it("builds categories for bars", () => {
    const frame: Frame = { columns: [{ name: "route", type: "string", role: "dimension" }, { name: "count", type: "number", role: "measure", unit: "count" }], values: [["/a", "/b"], [5, 3]], rows: 2 };
    expect(toCategories(frame)).toEqual({ categories: ["/a", "/b"], series: [{ name: "count", unit: "count", values: [5, 3] }] });
  });

  it("reduces stat values, preferring the window total", () => {
    const panel = { id: "x", title: "x", viz: "stat", reduce: "window" } as Panel;
    const frame: Frame = { columns: [{ name: "time", type: "time", role: "time" }, { name: "count", type: "number", role: "measure" }], values: [[1, 2], [3, 4]], rows: 2, totals: [null, 10] };
    expect(statValue(panel, frame)).toBe(10);
    expect(statValue({ ...panel, reduce: "last" }, frame)).toBe(4);
    expect(reduce([1, null, 5], "mean")).toBe(3);
  });
});

describe("thresholds", () => {
  it("grades by direction", () => {
    const thresholds = [{ value: 750, status: "warn" as const }, { value: 2000, status: "bad" as const }];
    expect(statusFor(500, thresholds)).toBe("ok");
    expect(statusFor(900, thresholds)).toBe("warn");
    expect(statusFor(2500, thresholds)).toBe("bad");
    expect(statusFor(20, [{ value: 25, status: "bad" }], "higher")).toBe("bad");
    expect(statusFor(null, thresholds)).toBeNull();
    expect(statusFor(5, undefined)).toBeNull();
  });
});

describe("compile", () => {
  const theme = chartThemeFor(false);
  const panel = { id: "latency", title: "Latency", viz: "timeseries", unit: "ms", thresholds: [{ value: 1500, status: "bad", label: "budget" }] } as Panel;
  const result: PanelResult = { id: "latency", status: "ok", frame: series, previous: series, elapsed_ms: 3 };

  it("draws series, thresholds and the previous period", () => {
    const option = timeseriesOption(panel, result, theme) as { series: { name: string; lineStyle?: { type?: string }; markLine?: { data: unknown[] } }[]; legend: { show: boolean } };
    expect(option.series.map((s) => s.name)).toEqual(["checkout", "cart", "checkout · previous", "cart · previous"]);
    expect(option.series[2].lineStyle?.type).toBe("dashed");
    expect(option.series[0].markLine?.data).toHaveLength(1);
    expect(option.legend.show).toBe(true);
  });

  it("stacks only when asked", () => {
    const stacked = timeseriesOption({ ...panel, options: { style: "stacked" } }, { ...result, previous: undefined }, theme) as { series: { stack?: string }[] };
    expect(stacked.series.every((s) => s.stack === "total")).toBe(true);
  });

  it("draws horizontal bars and gauges", () => {
    const frame: Frame = { columns: [{ name: "route", type: "string", role: "dimension" }, { name: "count", type: "number", role: "measure", unit: "count" }], values: [["/a", "/b"], [5, 3]], rows: 2 };
    const bar = barOption({ id: "b", title: "b", viz: "bar" } as Panel, frame, theme) as { yAxis: { data: string[]; inverse: boolean } };
    expect(bar.yAxis.data).toEqual(["/a", "/b"]);
    expect(bar.yAxis.inverse).toBe(true);
    const gauge = gaugeOption({ id: "g", title: "g", viz: "gauge", min: 0, max: 50, unit: "count" } as Panel, 42, theme) as { series: { min: number; max: number }[] };
    expect(gauge.series[0].max).toBe(50);
  });
});
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd ui/host && bunx vitest run src/dashboards/panels.test.ts; cd ../..`
Expected: FAIL — cannot resolve `../../../panels/compile`.

- [ ] **Step 4: Implement the shared types and helpers**

`ui/panels/types.ts`:

```ts
/* The v1 dashboard spec and panel results, mirroring internal/panel. Pure
 * types: this directory has no node_modules and imports only siblings. */
export const ALL = "$__all";

export type Unit = "ms" | "s" | "ns" | "percent" | "ratio" | "count" | "per_second" | "per_minute" | "bytes" | "none";
export type Status = "ok" | "warn" | "bad";
export type Viz = "stat" | "gauge" | "timeseries" | "bar" | "table" | "text";

export type Threshold = { value: number; status: Status; label?: string };

export type Query = { from: "spans" | "logs" | "metrics"; where?: string[]; measures: string[]; by?: string[]; bucket?: string; sort?: string; limit?: number };

export type Panel = {
  id: string;
  title: string;
  description?: string;
  viz: Viz;
  width?: number;
  height?: "s" | "m" | "l";
  query?: Query;
  sql?: string;
  unit?: Unit;
  reduce?: "window" | "last" | "mean" | "min" | "max" | "sum";
  thresholds?: Threshold[];
  better?: "lower" | "higher";
  min?: number;
  max?: number;
  options?: { style?: "line" | "area" | "bars" | "stacked"; scale?: "linear" | "log"; top?: number; legend?: "auto" | "hidden" };
  click?: { set_variable: string };
  drill?: "traces" | "logs";
  time?: { range?: string; shift?: string };
  content?: string;
  grid?: { x: number; y: number; w: number; h: number };
};

export type Variable = {
  name: string;
  kind: "query" | "custom" | "constant" | "text";
  from?: string;
  field?: string;
  where?: string[];
  options?: string[];
  value?: string;
  default?: string;
  multi?: boolean;
  include_all?: boolean;
};

export type DashboardTime = { range?: string; from?: string; to?: string; refresh?: string; compare?: "previous_period" };

export type DashboardSpec = {
  version: 1;
  name: string;
  description?: string;
  time: DashboardTime;
  variables?: Variable[];
  annotations?: { deploys?: boolean; anomalies?: boolean };
  panels: Panel[];
};

export type Cell = string | number | null;

export type Column = { name: string; type: "time" | "number" | "string" | "json"; role: "time" | "dimension" | "measure"; unit?: string };

export type Frame = { columns: Column[]; values: Cell[][]; rows: number; totals?: Cell[]; truncated?: boolean };

export type PanelResult = {
  id: string;
  status: "ok" | "empty" | "error";
  frame?: Frame;
  previous?: Frame;
  error?: string;
  diagnosis?: string;
  sql?: string;
  interval?: string;
  elapsed_ms: number;
};

export type VarValue = string | string[];
```

`ui/panels/units.ts`:

```ts
import { duration, percent } from "../format";

const compact = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 });
const plain = new Intl.NumberFormat("en", { maximumFractionDigits: 2 });

function bytes(value: number): string {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = value;
  let i = 0;
  while (Math.abs(v) >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

/** One value in its unit. Durations always read in the largest unit that is
 *  still precise, so a column never prints 30.00s beside 25.0ms (#232 item 14). */
export function formatValue(unit: string | undefined, value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "—";
  switch (unit) {
    case "ms": return duration(value);
    case "s": return duration(value * 1000);
    case "ns": return duration(value / 1e6);
    case "percent": return percent(value / 100);
    case "ratio": return `${plain.format(value)}×`;
    case "per_second": return `${compact.format(value)}/s`;
    case "per_minute": return `${compact.format(value)}/min`;
    case "bytes": return bytes(value);
    case "count": return Math.abs(value) >= 10_000 ? compact.format(value) : plain.format(value);
    default: return Math.abs(value) >= 10_000 ? compact.format(value) : plain.format(value);
  }
}

/** Axis ticks: the same units, shorter. */
export function formatAxis(unit?: string): (value: number) => string {
  return (value: number) => {
    if (unit === "ms" || unit === "s" || unit === "ns") return formatValue(unit, value).replace(/(\.\d*?)0+([a-z]*)$/, "$1$2").replace(/\.([a-z]*)$/, "$1");
    if (unit === "percent") return `${plain.format(value)}%`;
    return formatValue(unit, value);
  };
}
```

`ui/panels/frame.ts`:

```ts
import type { Cell, Frame, Panel } from "./types";

export type Series = { name: string; unit?: string; points: [number, number | null][] };
export type Categories = { categories: string[]; series: { name: string; unit?: string; values: (number | null)[] }[] };

const num = (cell: Cell | undefined): number | null => (typeof cell === "number" && Number.isFinite(cell) ? cell : null);

export function measureColumns(frame: Frame): number[] {
  return frame.columns.flatMap((column, index) => (column.role === "measure" ? [index] : []));
}

/** Time series: with a dimension, one series per dimension value (first
 *  measure); without, one series per measure. Order of first appearance. */
export function toSeries(frame: Frame): Series[] {
  const time = frame.columns.findIndex((c) => c.role === "time");
  if (time < 0) return [];
  const dim = frame.columns.findIndex((c) => c.role === "dimension");
  const measures = measureColumns(frame);
  if (dim >= 0 && measures.length > 0) {
    const m = measures[0];
    const byName = new Map<string, Series>();
    for (let row = 0; row < frame.rows; row += 1) {
      const name = String(frame.values[dim][row] ?? "");
      let s = byName.get(name);
      if (!s) {
        s = { name: name || "(none)", unit: frame.columns[m].unit, points: [] };
        byName.set(name, s);
      }
      s.points.push([Number(frame.values[time][row]), num(frame.values[m][row])]);
    }
    return [...byName.values()];
  }
  return measures.map((m) => ({
    name: frame.columns[m].name,
    unit: frame.columns[m].unit,
    points: frame.values[time].map((t, row) => [Number(t), num(frame.values[m][row])] as [number, number | null]),
  }));
}

/** Bars: the first dimension is the category; a second dimension splits
 *  series; otherwise each measure is a series. */
export function toCategories(frame: Frame): Categories {
  const dims = frame.columns.flatMap((c, i) => (c.role === "dimension" ? [i] : []));
  const measures = measureColumns(frame);
  if (dims.length === 0 || measures.length === 0) return { categories: [], series: [] };
  const cat = dims[0];
  const categories: string[] = [];
  for (let row = 0; row < frame.rows; row += 1) {
    const value = String(frame.values[cat][row] ?? "");
    if (!categories.includes(value)) categories.push(value);
  }
  if (dims.length > 1) {
    const split = dims[1];
    const m = measures[0];
    const groups = new Map<string, (number | null)[]>();
    for (let row = 0; row < frame.rows; row += 1) {
      const group = String(frame.values[split][row] ?? "");
      if (!groups.has(group)) groups.set(group, categories.map(() => null));
      groups.get(group)![categories.indexOf(String(frame.values[cat][row] ?? ""))] = num(frame.values[m][row]);
    }
    return { categories, series: [...groups.entries()].map(([name, values]) => ({ name, unit: frame.columns[m].unit, values })) };
  }
  return { categories, series: measures.map((m) => ({ name: frame.columns[m].name, unit: frame.columns[m].unit, values: frame.values[m].map(num) })) };
}

export function reduce(values: (number | null)[], reducer: string): number | null {
  const present = values.filter((v): v is number => v !== null);
  if (present.length === 0) return null;
  switch (reducer) {
    case "last": return present[present.length - 1];
    case "min": return Math.min(...present);
    case "max": return Math.max(...present);
    case "sum": return present.reduce((a, b) => a + b, 0);
    default: return present.reduce((a, b) => a + b, 0) / present.length;
  }
}

/** The headline number: the window total for reduce=window, otherwise the
 *  series reduced in the browser. Never the last partial bucket by accident
 *  (#232 item 1). */
export function statValue(panel: Panel, frame: Frame): number | null {
  const m = measureColumns(frame)[0];
  if (m === undefined) return null;
  if ((panel.reduce ?? "window") === "window") {
    if (frame.totals) return num(frame.totals[m]);
    return reduce(frame.values[m].map(num), "mean");
  }
  return reduce(frame.values[m].map(num), panel.reduce ?? "mean");
}

export function sparkline(frame: Frame): (number | null)[] {
  const m = measureColumns(frame)[0];
  return m === undefined ? [] : frame.values[m].map(num);
}
```

`ui/panels/thresholds.ts`:

```ts
import type { Status, Threshold } from "./types";

const rank: Record<Status, number> = { ok: 0, warn: 1, bad: 2 };

/** The worst status whose threshold the value has crossed, in the direction
 *  that is worse: up for "lower is better" (the default), down otherwise. */
export function statusFor(value: number | null, thresholds?: Threshold[], better: "lower" | "higher" = "lower"): Status | null {
  if (value === null || !thresholds || thresholds.length === 0) return null;
  let status: Status = "ok";
  for (const t of thresholds) {
    const crossed = better === "lower" ? value >= t.value : value <= t.value;
    if (crossed && rank[t.status] > rank[status]) status = t.status;
  }
  return status;
}
```

- [ ] **Step 5: Implement the chart compile functions**

`ui/panels/compile.ts`:

```ts
import { chartTheme, seriesColor, statusHex } from "../chart";
import { fonts } from "../tokens";
import { toCategories, toSeries } from "./frame";
import type { Frame, Panel, PanelResult } from "./types";
import { formatAxis, formatValue } from "./units";

export type ChartTheme = { dark: boolean; text: string; muted: string; grid: string; surface: string; border: string; status: { ok: string; warn: string; bad: string }; font: string };

export function chartThemeFor(dark: boolean): ChartTheme {
  const base = chartTheme(dark);
  const status = statusHex(dark);
  return { dark, text: base.text, muted: base.muted, grid: base.grid, surface: base.surface, border: base.border, status: { ok: status.ok, warn: status.warn, bad: status.bad }, font: fonts.display };
}

type Option = Record<string, unknown>;

const colorFor = (name: string, theme: ChartTheme) => (name === "Other" ? theme.muted : seriesColor(name, theme.dark));

function baseOption(theme: ChartTheme, unit?: string): Option {
  return {
    animationDuration: 200,
    textStyle: { fontFamily: theme.font, color: theme.muted, fontSize: 11 },
    grid: { left: 8, right: 16, top: 28, bottom: 8, containLabel: true },
    tooltip: {
      trigger: "axis",
      confine: true,
      backgroundColor: theme.surface,
      borderColor: theme.border,
      textStyle: { color: theme.text, fontSize: 12 },
      valueFormatter: (value: number) => formatValue(unit, value),
      axisPointer: { type: "line", lineStyle: { color: theme.border } },
    },
    aria: { enabled: true },
  };
}

function thresholdLines(panel: Panel, theme: ChartTheme) {
  return (panel.thresholds ?? []).map((t) => ({
    yAxis: t.value,
    label: { formatter: t.label ?? `${t.status} ${formatValue(panel.unit, t.value)}`, position: "insideStartTop", color: theme.muted, fontSize: 10 },
    lineStyle: { color: theme.status[t.status], type: [4, 3], width: 1 },
  }));
}

/** A time series: one line per series, the previous period dashed and
 *  recessive, thresholds as labelled lines, stacking only when asked (the
 *  server refuses to stack non-additive measures). */
export function timeseriesOption(panel: Panel, result: PanelResult, theme: ChartTheme): Option {
  const current = result.frame ? toSeries(result.frame) : [];
  const unit = panel.unit ?? current[0]?.unit;
  const style = panel.options?.style ?? "line";
  const period = result.previous && result.frame ? shiftOf(result.frame, result.previous) : 0;
  const lines = current.map((s, i) => {
    const color = colorFor(s.name, theme);
    return {
      name: s.name,
      type: style === "bars" || style === "stacked" ? "bar" : "line",
      data: s.points,
      showSymbol: false,
      connectNulls: false,
      sampling: "lttb",
      stack: style === "stacked" ? "total" : undefined,
      areaStyle: style === "area" ? { opacity: 0.12 } : undefined,
      lineStyle: { width: 2, color },
      itemStyle: { color, borderRadius: style === "bars" || style === "stacked" ? [2, 2, 0, 0] : undefined },
      barMaxWidth: 12,
      emphasis: { focus: "series" },
      markLine: i === 0 && (panel.thresholds?.length ?? 0) > 0 ? { symbol: ["none", "none"], silent: true, data: thresholdLines(panel, theme) } : undefined,
    };
  });
  const previous = result.previous ? toSeries(result.previous).map((s) => ({
    name: `${s.name} · previous`,
    type: "line",
    data: s.points.map(([t, v]) => [t + period, v]),
    showSymbol: false,
    silent: true,
    z: 1,
    lineStyle: { width: 1, type: "dashed", color: theme.border },
    itemStyle: { color: theme.border },
  })) : [];
  const legend = (panel.options?.legend ?? "auto") !== "hidden" && current.length > 1;
  return {
    ...baseOption(theme, unit),
    legend: { show: legend, top: 0, left: 0, icon: "roundRect", itemWidth: 10, itemHeight: 10, textStyle: { color: theme.text, fontSize: 12 }, data: current.map((s) => s.name) },
    grid: { left: 8, right: 16, top: legend ? 30 : 12, bottom: 8, containLabel: true },
    xAxis: { type: "time", axisLine: { lineStyle: { color: theme.grid } }, axisTick: { show: false }, splitLine: { show: false }, axisLabel: { color: theme.muted, hideOverlap: true } },
    yAxis: { type: panel.options?.scale === "log" ? "log" : "value", axisLine: { show: false }, splitLine: { lineStyle: { color: theme.grid } }, axisLabel: { color: theme.muted, formatter: formatAxis(unit) } },
    series: [...lines, ...previous],
  };
}

function shiftOf(current: Frame, previous: Frame): number {
  const t = current.columns.findIndex((c) => c.role === "time");
  const a = Number(current.values[t]?.[0]);
  const b = Number(previous.values[t]?.[0]);
  return Number.isFinite(a) && Number.isFinite(b) ? a - b : 0;
}

/** Horizontal bars sorted by the server, value labels at the bar end. */
export function barOption(panel: Panel, frame: Frame, theme: ChartTheme): Option {
  const { categories, series } = toCategories(frame);
  const unit = panel.unit ?? series[0]?.unit;
  const multi = series.length > 1;
  return {
    ...baseOption(theme, unit),
    tooltip: { ...(baseOption(theme, unit).tooltip as Option), axisPointer: { type: "shadow" } },
    legend: { show: multi, top: 0, left: 0, icon: "roundRect", itemWidth: 10, itemHeight: 10, textStyle: { color: theme.text, fontSize: 12 } },
    grid: { left: 8, right: 56, top: multi ? 30 : 8, bottom: 8, containLabel: true },
    yAxis: { type: "category", inverse: true, data: categories, axisLine: { lineStyle: { color: theme.grid } }, axisTick: { show: false }, axisLabel: { color: theme.text, width: 180, overflow: "truncate" } },
    xAxis: { type: "value", splitLine: { lineStyle: { color: theme.grid } }, axisLabel: { color: theme.muted, formatter: formatAxis(unit), hideOverlap: true } },
    series: series.map((s, i) => ({
      name: s.name,
      type: "bar",
      data: s.values,
      barMaxWidth: 16,
      barGap: "20%",
      itemStyle: { color: multi ? colorFor(s.name, theme) : seriesColor(panel.id, theme.dark), borderRadius: [0, 3, 3, 0] },
      label: { show: !multi || i === 0, position: "right", color: theme.muted, formatter: ({ value }: { value: number }) => formatValue(unit, value) },
    })),
  };
}

/** A gauge with its thresholds as coloured bands. */
export function gaugeOption(panel: Panel, value: number | null, theme: ChartTheme): Option {
  const min = panel.min ?? 0;
  const max = panel.max ?? 100;
  const span = max - min || 1;
  const sorted = [...(panel.thresholds ?? [])].sort((a, b) => a.value - b.value);
  const bands: [number, string][] = [];
  let status: "ok" | "warn" | "bad" = panel.better === "higher" ? (sorted[sorted.length - 1]?.status ?? "ok") : "ok";
  for (const t of sorted) {
    bands.push([Math.min(1, Math.max(0, (t.value - min) / span)), theme.status[status]]);
    status = t.status;
  }
  bands.push([1, theme.status[status]]);
  return {
    series: [{
      type: "gauge",
      min,
      max,
      startAngle: 210,
      endAngle: -30,
      radius: "95%",
      center: ["50%", "60%"],
      axisLine: { lineStyle: { width: 10, color: sorted.length ? bands : [[1, theme.grid]] } },
      progress: { show: sorted.length === 0, width: 10, itemStyle: { color: seriesColor(panel.id, theme.dark) } },
      pointer: { show: sorted.length > 0, length: "55%", width: 4, itemStyle: { color: theme.text } },
      axisTick: { show: false },
      splitLine: { show: false },
      axisLabel: { show: false },
      anchor: { show: false },
      detail: { valueAnimation: true, offsetCenter: [0, "35%"], color: theme.text, fontSize: 22, fontWeight: 600, fontFamily: theme.font, formatter: () => formatValue(panel.unit, value) },
      data: [{ value: value ?? min }],
    }],
  };
}
```

- [ ] **Step 6: Implement the canvas wrapper**

`ui/host/src/dashboards/echart-canvas.tsx`:

```tsx
import { BarChart, GaugeChart, LineChart } from "echarts/charts";
import { AriaComponent, GridComponent, LegendComponent, MarkLineComponent, TooltipComponent } from "echarts/components";
import { connect, init, use, type EChartsCoreOption, type EChartsType } from "echarts/core";
import { CanvasRenderer } from "echarts/renderers";
import { useEffect, useRef } from "react";

use([CanvasRenderer, LineChart, BarChart, GaugeChart, GridComponent, LegendComponent, TooltipComponent, MarkLineComponent, AriaComponent]);

/* Dashboard panels draw on canvas: SVG stays smooth only to a few thousand
   points, and a dashboard of a dozen time series passes that. One instance
   lives for the component's lifetime; options are replaced in place. */
export function EChartCanvas({ option, height, label, onClick, group }: { option: EChartsCoreOption; height: number | string; label: string; onClick?: (params: { name?: string; seriesName?: string; value?: unknown }) => void; group?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const chart = useRef<EChartsType | null>(null);
  const click = useRef(onClick);
  click.current = onClick;

  useEffect(() => {
    if (!ref.current) return;
    const instance = init(ref.current, undefined, { renderer: "canvas" });
    chart.current = instance;
    instance.on("click", (params) => click.current?.(params as { name?: string; seriesName?: string; value?: unknown }));
    const observer = new ResizeObserver(() => instance.resize());
    observer.observe(ref.current);
    return () => { observer.disconnect(); instance.dispose(); chart.current = null; };
  }, []);

  useEffect(() => {
    chart.current?.setOption({ aria: { enabled: true, description: label }, ...option }, { notMerge: true });
  }, [option, label]);

  useEffect(() => {
    if (!chart.current || !group) return;
    chart.current.group = group;
    connect(group);
  }, [group]);

  return <div ref={ref} role="img" aria-label={label} style={{ height, width: "100%", minWidth: 0, cursor: onClick ? "pointer" : undefined }} />;
}
```

- [ ] **Step 7: Implement the API client**

`ui/host/src/dashboards/api.ts`:

```ts
import type { DashboardSpec, DashboardTime, Panel, PanelResult, Variable, VarValue } from "../../../panels/types";
import { authorizedFetch } from "../auth";

export type DashboardSummary = { id: string; name: string; description: string; is_default: boolean; version: number; panel_count: number; updated_at: string };
export type DashboardRecord = { id: string; name: string; description: string; is_default: boolean; version: number; spec: DashboardSpec; created_at: string; updated_at: string };
export type VersionInfo = { version: number; author_kind: "user" | "agent" | "system"; author_id?: string; message?: string; created_at: string };
export type Problem = { path: string; message: string; hint?: string };
export type Operation =
  | { op: "add_panel"; panel: Panel; after?: string }
  | { op: "update_panel"; id: string; set: Record<string, unknown> }
  | { op: "remove_panel"; id: string }
  | { op: "move_panel"; id: string; after?: string }
  | { op: "set_variable"; variable: Variable }
  | { op: "remove_variable"; name: string }
  | { op: "set_time"; time: DashboardTime }
  | { op: "rename"; name?: string; description?: string };
export type QueryBody = { dashboard: DashboardSpec; panels?: string[]; time?: DashboardTime; vars?: Record<string, VarValue>; widths?: Record<string, number>; compare?: boolean };

export const dashboardsKey = ["dashboards"] as const;

export class ApiError extends Error {
  constructor(message: string, readonly status: number, readonly problems: Problem[] = []) {
    super(message);
  }
}

async function request<T>(url: string, init?: RequestInit & { json?: unknown }): Promise<T> {
  const headers = new Headers(init?.headers);
  let body = init?.body;
  if (init?.json !== undefined) {
    headers.set("content-type", "application/json");
    body = JSON.stringify(init.json);
  }
  const response = await authorizedFetch(url, { ...init, headers, body });
  if (response.status === 204) return undefined as T;
  const payload = await response.json().catch(() => ({})) as { message?: string; problems?: Problem[] };
  if (!response.ok) throw new ApiError(payload.message ?? `Request failed (${response.status})`, response.status, payload.problems ?? []);
  return payload as T;
}

const path = (id: string) => `/api/dashboards/${encodeURIComponent(id)}`;

export const listDashboards = () => request<{ dashboards: DashboardSummary[] }>("/api/dashboards").then((r) => r.dashboards);
export const getDashboard = (id: string) => request<DashboardRecord>(path(id));
export const createDashboard = (spec: DashboardSpec) => request<DashboardRecord>("/api/dashboards", { method: "POST", json: { spec } });
export const replaceDashboard = (id: string, spec: DashboardSpec, baseVersion: number, message?: string) =>
  request<DashboardRecord>(path(id), { method: "PUT", json: { spec, base_version: baseVersion, message } });
export const patchDashboard = (id: string, operations: Operation[], baseVersion: number, message?: string) =>
  request<DashboardRecord>(path(id), { method: "PATCH", json: { operations, base_version: baseVersion, message } });
export const deleteDashboard = (id: string) => request<void>(path(id), { method: "DELETE", headers: { "Fanout-Confirm-Delete": id } });
export const listVersions = (id: string) => request<{ versions: VersionInfo[] }>(`${path(id)}/versions`).then((r) => r.versions);
export const restoreVersion = (id: string, version: number) => request<DashboardRecord>(`${path(id)}/versions/${version}/restore`, { method: "POST" });
export const queryPanels = (body: QueryBody) => request<{ results: PanelResult[] }>("/api/panels/query", { method: "POST", json: body }).then((r) => r.results);
export const resolveVariables = (body: Omit<QueryBody, "panels" | "widths" | "compare">) =>
  request<{ options: Record<string, { value: string; count?: number }[]> }>("/api/variables/resolve", { method: "POST", json: body }).then((r) => r.options);
```

- [ ] **Step 8: Run the tests**

Run: `cd ui/host && bunx vitest run src/dashboards/panels.test.ts && bunx tsc --noEmit; cd ../..`
Expected: PASS and no type errors. If `formatValue("ms", 600_000)` prints a different duration string, align the assertion with `ui/format.ts`'s `duration` (the existing source of truth) rather than changing that function.

- [ ] **Step 9: Commit**

```bash
git add ui/panels ui/host/src/dashboards ui/host/package.json ui/host/bun.lock
git commit -m "feat(ui): add the shared panel compile layer, canvas charts and dashboard client"
```

---

### Task 13: Dashboard page — URL state, time, refresh, comparison, variables and batched data

**Files:**
- Create: `ui/host/src/dashboards/search.ts`, `ui/host/src/dashboards/use-panel-results.ts`, `ui/host/src/dashboards/use-variables.ts`, `ui/host/src/dashboards/toolbar.tsx`, `ui/host/src/dashboards/variable-bar.tsx`, `ui/host/src/dashboards/page.tsx`
- Modify: `ui/host/src/routes/dashboards.$dashboardId.tsx`, `ui/host/src/routes/dashboards.index.tsx`
- Test: `ui/host/src/dashboards/search.test.ts`, `ui/host/src/dashboards/page.test.tsx`

**Interfaces:**
- Consumes: Task 12 (`api.ts`, types); `useFanoutApp` (`agentAvailable`, `openChat`).
- Produces:
  - `type DashboardSearch = { range?: string; from?: string; to?: string; compare?: "0" | "1"; view?: string; edit?: "1"; vars?: Record<string, VarValue> }` and `parseSearch(raw: Record<string, unknown>): DashboardSearch`, `toSearchParams(search: DashboardSearch): Record<string, unknown>`, `effectiveTime(spec, search): DashboardTime`
  - `usePanelResults(args): { results: Map<string, PanelResult>; fetching: boolean; error: Error | null; updatedAt: number | null; refetch(): void }`
  - `useVariableOptions(spec, time, vars)`
  - `DashboardPage({ dashboardId?: string; search: DashboardSearch; onSearch(next: DashboardSearch, replace?: boolean): void; onOpen(id: string, replace?: boolean): void })`
  - Grid and panel chrome are Task 14; this task renders panels through a temporary `PanelGrid` import that Task 14 provides, so implement Task 14's `grid.tsx` stub here: `export function PanelGrid(props: GridProps)` rendering each panel title and status in a simple CSS grid, replaced in Task 14.

- [ ] **Step 1: Write the failing tests**

`ui/host/src/dashboards/search.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { effectiveTime, parseSearch, toSearchParams } from "./search";
import type { DashboardSpec } from "../../../panels/types";

describe("dashboard search", () => {
  it("keeps only recognised view state", () => {
    expect(parseSearch({ range: "6h", "var-service": "checkout", "var-route": ["a", "b"], compare: "1", view: "latency", junk: 1, range2: "x" }))
      .toEqual({ range: "6h", compare: "1", view: "latency", vars: { service: "checkout", route: ["a", "b"] } });
    expect(parseSearch({ range: "90m", from: "2026-10-01T12:00:00Z", to: "nope" })).toEqual({});
    expect(parseSearch({ from: "2026-10-01T12:00:00Z", to: "2026-10-01T13:00:00Z" })).toEqual({ from: "2026-10-01T12:00:00Z", to: "2026-10-01T13:00:00Z" });
  });

  it("round-trips through router params", () => {
    const search = { range: "1h", vars: { service: "checkout" }, edit: "1" as const };
    expect(parseSearch(toSearchParams(search))).toEqual(search);
  });

  it("lets the URL override the saved time", () => {
    const spec = { version: 1, name: "x", time: { range: "1h", refresh: "30s" }, panels: [] } as DashboardSpec;
    expect(effectiveTime(spec, {})).toEqual({ range: "1h", refresh: "30s" });
    expect(effectiveTime(spec, { range: "24h" })).toEqual({ range: "24h", refresh: "30s" });
    expect(effectiveTime(spec, { from: "a", to: "b" })).toEqual({ from: "a", to: "b", refresh: "30s" });
  });
});
```

`ui/host/src/dashboards/page.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./echart-canvas", () => ({ EChartCanvas: ({ label }: { label: string }) => <div data-chart={label} /> }));
vi.mock("../app-context", () => ({ useFanoutApp: () => ({ agentAvailable: true, openChat: vi.fn() }) }));

import { DashboardPage } from "./page";

const spec = {
  version: 1, name: "Checkout", description: "Money path", time: { range: "1h", refresh: "30s" },
  variables: [{ name: "service", kind: "query", from: "spans", field: "service", default: "checkout" }],
  panels: [
    { id: "requests", title: "Requests", viz: "stat", width: 3, height: "s", query: { from: "spans", measures: ["count()"] }, grid: { x: 0, y: 0, w: 3, h: 3 } },
    { id: "latency", title: "Latency for $service", viz: "timeseries", width: 9, height: "m", unit: "ms", query: { from: "spans", measures: ["p95(duration_ms)"], bucket: "auto" }, grid: { x: 3, y: 0, w: 9, h: 6 } },
  ],
};
const record = { id: "d1", name: "Checkout", description: "Money path", is_default: true, version: 4, spec, created_at: "t", updated_at: "t" };
const frame = { columns: [{ name: "time", type: "time", role: "time" }, { name: "count", type: "number", role: "measure", unit: "count" }], values: [[1, 2], [3, 4]], rows: 2, totals: [null, 120] };

const fetchMock = vi.fn<typeof fetch>();
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
let queryBodies: unknown[] = [];

beforeEach(() => {
  queryBodies = [];
  fetchMock.mockImplementation(async (input, init) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/dashboards") return json({ dashboards: [{ id: "d1", name: "Checkout", description: "", is_default: true, version: 4, panel_count: 2, updated_at: "t" }] });
    if (url.pathname === "/api/dashboards/d1") return json(record);
    if (url.pathname === "/api/dashboards/missing") return json({ message: "dashboard not found" }, 404);
    if (url.pathname === "/api/variables/resolve") return json({ options: { service: [{ value: "checkout", count: 9 }, { value: "cart", count: 3 }] } });
    if (url.pathname === "/api/panels/query") {
      queryBodies.push(JSON.parse(String(init?.body)));
      return json({ results: [{ id: "requests", status: "ok", frame, elapsed_ms: 2 }, { id: "latency", status: "empty", diagnosis: "No spans match service = 'cart'.", elapsed_ms: 3 }] });
    }
    return json({}, 404);
  });
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal("IntersectionObserver", class { observe() {} unobserve() {} disconnect() {} });
});
afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ""; });

async function render(search = {}, dashboardId = "d1") {
  const host = document.createElement("div");
  document.body.append(host);
  const onSearch = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    createRoot(host).render(<MantineProvider><QueryClientProvider client={client}><DashboardPage dashboardId={dashboardId} search={search} onSearch={onSearch} onOpen={vi.fn()} /></QueryClientProvider></MantineProvider>);
  });
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });
  return { host, onSearch };
}

describe("DashboardPage", () => {
  it("loads the spec and sends one batch with the URL's view state", async () => {
    const { host } = await render({ range: "6h", vars: { service: "cart" } });
    expect(host.textContent).toContain("Checkout");
    expect(host.textContent).toContain("Latency for cart");
    expect(queryBodies).toHaveLength(1);
    expect(queryBodies[0]).toMatchObject({ time: { range: "6h" }, vars: { service: "cart" } });
    expect(host.textContent).toContain("No spans match service = 'cart'.");
  });

  it("answers a missing dashboard instead of showing another", async () => {
    const { host } = await render({}, "missing");
    expect(host.textContent).toContain("This dashboard isn't here");
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd ui/host && bunx vitest run src/dashboards; cd ../..`
Expected: FAIL — cannot resolve `./search`, `./page`.

- [ ] **Step 3: Implement URL state**

`ui/host/src/dashboards/search.ts`:

```ts
import type { DashboardSpec, DashboardTime, VarValue } from "../../../panels/types";

export const ranges = ["5m", "15m", "1h", "3h", "6h", "12h", "24h", "2d", "7d", "30d"] as const;
export const refreshes = ["off", "10s", "30s", "1m", "5m"] as const;

/** Everything about a dashboard view that belongs in the address bar, so a
 *  link reproduces what its sender saw (#232 item 12). Unrecognised values
 *  are dropped rather than rejected: a stale link still opens the dashboard. */
export type DashboardSearch = { range?: string; from?: string; to?: string; compare?: "0" | "1"; view?: string; edit?: "1"; vars?: Record<string, VarValue> };

const isInstant = (value: unknown): value is string => typeof value === "string" && !Number.isNaN(Date.parse(value));

export function parseSearch(raw: Record<string, unknown>): DashboardSearch {
  const out: DashboardSearch = {};
  if (typeof raw.range === "string" && (ranges as readonly string[]).includes(raw.range)) out.range = raw.range;
  else if (isInstant(raw.from) && isInstant(raw.to) && Date.parse(raw.from) < Date.parse(raw.to)) { out.from = raw.from; out.to = raw.to; }
  if (raw.compare === "1" || raw.compare === "0") out.compare = raw.compare;
  if (typeof raw.view === "string" && /^[a-z][a-z0-9_]{0,39}$/.test(raw.view)) out.view = raw.view;
  if (raw.edit === "1") out.edit = "1";
  const vars: Record<string, VarValue> = {};
  for (const [key, value] of Object.entries(raw)) {
    if (!key.startsWith("var-")) continue;
    const name = key.slice(4);
    if (!/^[a-z][a-z0-9_]{0,39}$/.test(name)) continue;
    if (typeof value === "string") vars[name] = value;
    else if (Array.isArray(value) && value.every((v) => typeof v === "string")) vars[name] = value as string[];
  }
  if (Object.keys(vars).length > 0) out.vars = vars;
  return out;
}

export function toSearchParams(search: DashboardSearch): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  if (search.range) out.range = search.range;
  if (search.from && search.to) { out.from = search.from; out.to = search.to; }
  if (search.compare) out.compare = search.compare;
  if (search.view) out.view = search.view;
  if (search.edit) out.edit = search.edit;
  for (const [name, value] of Object.entries(search.vars ?? {})) out[`var-${name}`] = value;
  return out;
}

export function effectiveTime(spec: DashboardSpec, search: DashboardSearch): DashboardTime {
  const refresh = spec.time.refresh ?? "30s";
  if (search.from && search.to) return { from: search.from, to: search.to, refresh };
  if (search.range) return { range: search.range, refresh };
  return { ...spec.time, refresh };
}
```

- [ ] **Step 4: Implement the data hooks**

`ui/host/src/dashboards/use-panel-results.ts`:

```ts
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useRef } from "react";
import type { DashboardSpec, DashboardTime, PanelResult, VarValue } from "../../../panels/types";
import { queryPanels } from "./api";

const refreshMs: Record<string, number | false> = { off: false, "10s": 10_000, "30s": 30_000, "1m": 60_000, "5m": 300_000 };

/** One request per refresh for every visible panel (#232 item 30). Results
 *  of panels scrolled out of view are kept from their last fetch. Widths are
 *  rounded so resizing a window does not refetch on every pixel. */
export function usePanelResults({ dashboardId, version, spec, time, vars, compare, widths, visible, refresh }: {
  dashboardId: string; version: number; spec: DashboardSpec; time: DashboardTime; vars: Record<string, VarValue>;
  compare: boolean; widths: Record<string, number>; visible: string[]; refresh: string;
}) {
  const rounded = useMemo(() => Object.fromEntries(Object.entries(widths).map(([id, w]) => [id, Math.max(100, Math.round(w / 100) * 100)])), [widths]);
  // Visibility decides which panels a refresh asks for, but is not part of
  // the key: scrolling must not refetch, and the first load asks for all.
  const visibleRef = useRef(visible);
  visibleRef.current = visible;
  const key = JSON.stringify([dashboardId, version, time, vars, compare, rounded]);
  const kept = useRef({ key, results: new Map<string, PanelResult>() });
  if (kept.current.key !== key) kept.current = { key, results: new Map() };
  const query = useQuery({
    queryKey: ["panels", key],
    queryFn: () => queryPanels({ dashboard: spec, panels: [...visibleRef.current].sort(), time, vars, widths: rounded, compare }),
    enabled: spec.panels.some((p) => p.viz !== "text"),
    refetchInterval: refreshMs[refresh] ?? 30_000,
    staleTime: 5_000,
    placeholderData: (previous) => previous,
  });
  const results = useMemo(() => {
    for (const r of query.data ?? []) kept.current.results.set(r.id, r);
    return new Map(kept.current.results);
  }, [query.data, key]);
  // A panel scrolled into view that has never loaded under this key is
  // fetched now rather than at the next refresh.
  useEffect(() => {
    if (!query.isFetching && visible.some((id) => !kept.current.results.has(id) && spec.panels.find((p) => p.id === id)?.viz !== "text")) void query.refetch();
  }, [visible]);
  return { results, fetching: query.isFetching, error: query.error, updatedAt: query.dataUpdatedAt || null, refetch: () => void query.refetch() };
}
```

`ui/host/src/dashboards/use-variables.ts`:

```ts
import { useQuery } from "@tanstack/react-query";
import type { DashboardSpec, DashboardTime, VarValue } from "../../../panels/types";
import { resolveVariables } from "./api";

export function useVariableOptions(dashboardId: string, version: number, spec: DashboardSpec, time: DashboardTime, vars: Record<string, VarValue>) {
  const hasVariables = (spec.variables?.length ?? 0) > 0;
  return useQuery({
    queryKey: ["variables", dashboardId, version, time, vars],
    queryFn: () => resolveVariables({ dashboard: spec, time, vars }),
    enabled: hasVariables,
    staleTime: 60_000,
    placeholderData: (previous) => previous,
  });
}
```

- [ ] **Step 5: Implement the toolbar and variable bar**

`ui/host/src/dashboards/toolbar.tsx`:

```tsx
import { Button, Group, Menu, SegmentedControl, Switch, Text, TextInput, Tooltip } from "@mantine/core";
import { ArrowClockwise, CaretDown, Clock, MagnifyingGlassMinus, PencilSimple } from "@phosphor-icons/react";
import { useState } from "react";
import type { DashboardTime } from "../../../panels/types";
import { exactTimestamp } from "../../../format";
import { ranges, refreshes } from "./search";

const rangeLabel: Record<string, string> = { "5m": "Last 5 minutes", "15m": "Last 15 minutes", "1h": "Last hour", "3h": "Last 3 hours", "6h": "Last 6 hours", "12h": "Last 12 hours", "24h": "Last 24 hours", "2d": "Last 2 days", "7d": "Last 7 days", "30d": "Last 30 days" };

export function timeLabel(time: DashboardTime): string {
  if (time.from && time.to) return `${exactTimestamp(time.from)} – ${exactTimestamp(time.to)}`;
  return rangeLabel[time.range ?? "1h"] ?? time.range ?? "Last hour";
}

export function Toolbar({ time, refresh, compare, editing, fetching, updatedAt, onRange, onAbsolute, onZoomOut, onRefresh, onRefreshNow, onCompare, onEdit }: {
  time: DashboardTime; refresh: string; compare: boolean; editing: boolean; fetching: boolean; updatedAt: number | null;
  onRange(range: string): void; onAbsolute(from: string, to: string): void; onZoomOut(): void; onRefresh(refresh: string): void; onRefreshNow(): void; onCompare(on: boolean): void; onEdit(): void;
}) {
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  return <Group gap="xs" wrap="wrap" role="group" aria-label="Dashboard time">
    <Menu position="bottom-end" withinPortal closeOnItemClick={false}>
      <Menu.Target>
        <Button variant="default" size="sm" leftSection={<Clock size={15} />} rightSection={<CaretDown size={12} weight="bold" />}>{timeLabel(time)}</Button>
      </Menu.Target>
      <Menu.Dropdown miw={260}>
        <Menu.Label>Relative</Menu.Label>
        {ranges.map((range) => <Menu.Item key={range} onClick={() => onRange(range)} fw={time.range === range ? 600 : undefined}>{rangeLabel[range]}</Menu.Item>)}
        <Menu.Divider />
        <Menu.Label>Absolute (your time zone)</Menu.Label>
        <Group gap={6} px="xs" pb="xs" wrap="nowrap">
          <TextInput type="datetime-local" size="xs" aria-label="From" value={from} onChange={(e) => setFrom(e.currentTarget.value)} />
          <TextInput type="datetime-local" size="xs" aria-label="To" value={to} onChange={(e) => setTo(e.currentTarget.value)} />
        </Group>
        <Group px="xs" pb="xs" justify="flex-end">
          <Button size="compact-sm" disabled={!from || !to || from >= to} onClick={() => onAbsolute(new Date(from).toISOString(), new Date(to).toISOString())}>Apply range</Button>
        </Group>
      </Menu.Dropdown>
    </Menu>
    <Tooltip label="Zoom out"><Button variant="default" size="sm" px={10} aria-label="Zoom out" onClick={onZoomOut}><MagnifyingGlassMinus size={15} /></Button></Tooltip>
    <Group gap={4} wrap="nowrap">
      <Tooltip label={updatedAt ? `Updated ${exactTimestamp(updatedAt)}` : "Refresh now"}><Button variant="default" size="sm" px={10} aria-label="Refresh now" loading={fetching} onClick={onRefreshNow}><ArrowClockwise size={15} /></Button></Tooltip>
      <SegmentedControl size="xs" aria-label="Auto refresh" value={refresh} onChange={onRefresh} data={refreshes.map((r) => ({ value: r, label: r === "off" ? "Off" : r }))} />
    </Group>
    <Switch size="sm" label="Compare with previous period" checked={compare} onChange={(e) => onCompare(e.currentTarget.checked)} />
    <Button variant={editing ? "filled" : "default"} size="sm" leftSection={<PencilSimple size={15} />} onClick={onEdit}>{editing ? "Done" : "Edit layout"}</Button>
    {updatedAt && <Text size="xs" c="dimmed" visibleFrom="md">Updated {new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit", second: "2-digit" }).format(updatedAt)}</Text>}
  </Group>;
}
```

`ui/host/src/dashboards/variable-bar.tsx`:

```tsx
import { Group, MultiSelect, Select, Text, TextInput } from "@mantine/core";
import { ALL, type Variable, type VarValue } from "../../../panels/types";

export function currentValue(variable: Variable, vars: Record<string, VarValue>, options?: { value: string }[]): VarValue {
  if (variable.kind === "constant") return variable.value ?? "";
  const given = vars[variable.name];
  if (given !== undefined) return given;
  if (variable.default) return variable.default;
  if (variable.include_all) return ALL;
  return options?.[0]?.value ?? "";
}

export function VariableBar({ variables, vars, options, onChange }: {
  variables: Variable[]; vars: Record<string, VarValue>; options: Record<string, { value: string; count?: number }[]>; onChange(name: string, value: VarValue): void;
}) {
  if (variables.length === 0) return null;
  return <Group gap="sm" wrap="wrap" role="group" aria-label="Dashboard variables">
    {variables.map((variable) => {
      const value = currentValue(variable, vars, options[variable.name]);
      const choices = [
        ...(variable.include_all ? [{ value: ALL, label: "All" }] : []),
        ...(options[variable.name] ?? variable.options?.map((o) => ({ value: o })) ?? []).map((o) => ({ value: o.value, label: o.value })),
      ];
      const label = <Text size="xs" c="dimmed" ff="monospace">${variable.name}</Text>;
      if (variable.kind === "constant") return <TextInput key={variable.name} label={label} size="xs" value={variable.value} readOnly w={160} />;
      if (variable.kind === "text") return <TextInput key={variable.name} label={label} size="xs" w={200} defaultValue={typeof value === "string" ? value : ""} onBlur={(e) => onChange(variable.name, e.currentTarget.value)} onKeyDown={(e) => { if (e.key === "Enter") e.currentTarget.blur(); }} />;
      if (variable.multi) return <MultiSelect key={variable.name} label={label} size="xs" w={260} searchable clearable data={choices.filter((c) => c.value !== ALL)} value={Array.isArray(value) ? value : value && value !== ALL ? [value] : []} placeholder="All" onChange={(next) => onChange(variable.name, next)} />;
      return <Select key={variable.name} label={label} size="xs" w={220} searchable allowDeselect={false} data={choices} value={Array.isArray(value) ? value[0] ?? null : value || null} onChange={(next) => next !== null && onChange(variable.name, next)} />;
    })}
  </Group>;
}
```

- [ ] **Step 6: Implement the page**

`ui/host/src/dashboards/page.tsx`:

```tsx
import { Alert, Box, Button, Center, Group, Loader, Stack, Text, Title } from "@mantine/core";
import { WarningCircle } from "@phosphor-icons/react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { ALL, type DashboardSpec, type VarValue } from "../../../panels/types";
import { useFanoutApp } from "../app-context";
import { ApiError, dashboardsKey, getDashboard, listDashboards } from "./api";
import { PanelGrid } from "./grid";
import { effectiveTime, type DashboardSearch } from "./search";
import { Toolbar } from "./toolbar";
import { usePanelResults } from "./use-panel-results";
import { useVariableOptions } from "./use-variables";
import { currentValue, VariableBar } from "./variable-bar";

const zoomOut: Record<string, string> = { "5m": "15m", "15m": "1h", "1h": "3h", "3h": "6h", "6h": "12h", "12h": "24h", "24h": "2d", "2d": "7d", "7d": "30d", "30d": "30d" };

export function DashboardPage({ dashboardId, search, onSearch, onOpen }: { dashboardId?: string; search: DashboardSearch; onSearch(next: DashboardSearch, replace?: boolean): void; onOpen(id: string, replace?: boolean): void }) {
  const { agentAvailable, openChat } = useFanoutApp();
  const list = useQuery({ queryKey: dashboardsKey, queryFn: listDashboards, staleTime: 15_000 });
  useEffect(() => {
    if (dashboardId || !list.data?.length) return;
    onOpen((list.data.find((d) => d.is_default) ?? list.data[0]).id, true);
  }, [dashboardId, list.data]);
  const record = useQuery({ queryKey: ["dashboard", dashboardId], queryFn: () => getDashboard(dashboardId!), enabled: Boolean(dashboardId), retry: (count, error) => !(error instanceof ApiError && error.status === 404) && count < 2 });

  if (!dashboardId || list.isLoading || record.isLoading) return <Center mih="50vh"><Loader size="sm" /><Text c="dimmed" size="sm" ml="sm">Loading your dashboard…</Text></Center>;
  if (record.error instanceof ApiError && record.error.status === 404) {
    return <Center mih="50vh"><Stack align="center" gap="xs">
      <Title order={1} fz={24}>This dashboard isn&apos;t here</Title>
      <Text c="dimmed" size="sm" ta="center">The link may be out of date, or the dashboard may have been deleted.</Text>
      {list.data?.length ? <Button mt="sm" variant="default" size="sm" onClick={() => onOpen((list.data.find((d) => d.is_default) ?? list.data[0]).id, true)}>Open your default dashboard</Button> : null}
    </Stack></Center>;
  }
  if (record.error || !record.data) return <Center mih="50vh"><Text c="dimmed" size="sm">Your dashboard is unavailable. Try refreshing.</Text></Center>;
  return <Loaded key={record.data.id} id={record.data.id} version={record.data.version} spec={record.data.spec} search={search} onSearch={onSearch} agentAvailable={agentAvailable} openChat={openChat} />;
}

function Loaded({ id, version, spec, search, onSearch, agentAvailable, openChat }: { id: string; version: number; spec: DashboardSpec; search: DashboardSearch; onSearch(next: DashboardSearch, replace?: boolean): void; agentAvailable: boolean; openChat(prompt?: string): void }) {
  const time = effectiveTime(spec, search);
  const [refresh, setRefresh] = useState(time.refresh ?? "30s");
  const compare = search.compare ? search.compare === "1" : spec.time.compare === "previous_period";
  const vars = search.vars ?? {};
  const options = useVariableOptions(id, version, spec, time, vars);
  const resolvedVars = useMemo(() => {
    const out: Record<string, VarValue> = {};
    for (const v of spec.variables ?? []) out[v.name] = currentValue(v, vars, options.data?.[v.name]);
    return out;
  }, [spec.variables, vars, options.data]);
  // Panel widths come from the layout and the viewport, known at first
  // render, so the first batch already carries them and no second request
  // follows once the grid has measured itself.
  const [viewport, setViewport] = useState(() => window.innerWidth);
  useEffect(() => {
    let timer = 0;
    const onResize = () => { window.clearTimeout(timer); timer = window.setTimeout(() => setViewport(window.innerWidth), 300); };
    window.addEventListener("resize", onResize);
    return () => { window.removeEventListener("resize", onResize); window.clearTimeout(timer); };
  }, []);
  const widths = useMemo(() => {
    const container = Math.max(320, Math.min(viewport - 64, 1600));
    return Object.fromEntries(spec.panels.map((p) => [p.id, (container / 12) * (p.grid?.w ?? p.width ?? 6)]));
  }, [viewport, spec]);
  const [visible, setVisible] = useState<string[]>(() => spec.panels.map((p) => p.id));
  const data = usePanelResults({ dashboardId: id, version, spec, time, vars: resolvedVars, compare, widths, visible, refresh });
  const setVar = (name: string, value: VarValue) => onSearch({ ...search, vars: { ...vars, [name]: value } }, true);

  return <Box component="main" maw={1600} mx="auto" px={{ base: "md", sm: "xl" }} pt="lg" pb="xl">
    <Stack gap="sm" mb="md">
      <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm">
        <Box miw={0}>
          <Title order={1} fz={28} lts="-0.02em">{spec.name}</Title>
          {spec.description && <Text c="dimmed" size="sm" mt={2}>{spec.description}</Text>}
        </Box>
        <Toolbar time={time} refresh={refresh} compare={compare} editing={search.edit === "1"} fetching={data.fetching} updatedAt={data.updatedAt}
          onRange={(range) => onSearch({ ...search, range, from: undefined, to: undefined })}
          onAbsolute={(from, to) => onSearch({ ...search, range: undefined, from, to })}
          onZoomOut={() => onSearch({ ...search, range: zoomOut[time.range ?? "1h"] ?? "1h", from: undefined, to: undefined })}
          onRefresh={setRefresh} onRefreshNow={data.refetch}
          onCompare={(on) => onSearch({ ...search, compare: on ? "1" : "0" }, true)}
          onEdit={() => onSearch({ ...search, edit: search.edit === "1" ? undefined : "1" })} />
      </Group>
      <VariableBar variables={spec.variables ?? []} vars={resolvedVars} options={options.data ?? {}} onChange={setVar} />
      {Object.entries(vars).filter(([, v]) => v !== ALL).length > 0 && <Group gap={6}>
        {Object.entries(vars).filter(([, v]) => v !== ALL).map(([name, value]) => <Button key={name} size="compact-xs" variant="light" onClick={() => { const next = { ...vars }; delete next[name]; onSearch({ ...search, vars: next }, true); }}>${name} = {Array.isArray(value) ? value.join(", ") : value} ×</Button>)}
      </Group>}
    </Stack>
    {data.error && <Alert color="bad" icon={<WarningCircle size={18} weight="fill" />} mb="md" title="Panels could not be loaded">{data.error.message}</Alert>}
    <PanelGrid dashboardId={id} version={version} spec={spec} vars={resolvedVars} results={data.results} fetching={data.fetching} editing={search.edit === "1"} view={search.view}
      agentAvailable={agentAvailable} onOpenChat={openChat} onVariable={setVar} onView={(view) => onSearch({ ...search, view })} onVisible={setVisible} />
  </Box>;
}
```

Provide the temporary grid so this task compiles and its test passes; Task 14 replaces it.

`ui/host/src/dashboards/grid.tsx` (temporary):

```tsx
import { Paper, SimpleGrid, Text } from "@mantine/core";
import type { DashboardSpec, PanelResult, VarValue } from "../../../panels/types";

export type GridProps = {
  dashboardId: string; version: number; spec: DashboardSpec; vars: Record<string, VarValue>; results: Map<string, PanelResult>; fetching: boolean; editing: boolean; view?: string;
  agentAvailable: boolean; onOpenChat(prompt?: string): void; onVariable(name: string, value: VarValue): void; onView(view?: string): void; onVisible(ids: string[]): void;
};

export const interpolate = (text: string, vars: Record<string, VarValue>) =>
  text.replace(/\$([a-z][a-z0-9_]*)/g, (match, name: string) => { const v = vars[name]; return v === undefined ? match : v === "$__all" ? "all" : Array.isArray(v) ? v.join(", ") : v; });

export function PanelGrid({ spec, vars, results }: GridProps) {
  return <SimpleGrid cols={2}>{spec.panels.map((p) => <Paper key={p.id} withBorder p="sm">
    <Text fw={600}>{interpolate(p.title, vars)}</Text>
    <Text size="sm" c="dimmed">{results.get(p.id)?.diagnosis ?? results.get(p.id)?.status ?? "…"}</Text>
  </Paper>)}</SimpleGrid>;
}
```

- [ ] **Step 7: Route to the page**

`ui/host/src/routes/dashboards.$dashboardId.tsx`:

```tsx
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { DashboardPage } from "../dashboards/page";
import { parseSearch, toSearchParams, type DashboardSearch } from "../dashboards/search";

export const Route = createFileRoute("/dashboards/$dashboardId")({
  component: DashboardDetail,
  validateSearch: (raw: Record<string, unknown>) => toSearchParams(parseSearch(raw)),
});

function DashboardDetail() {
  const { dashboardId } = Route.useParams();
  const search = parseSearch(Route.useSearch() as Record<string, unknown>);
  const navigate = useNavigate();
  const go = (id: string, next: DashboardSearch, replace?: boolean) => void navigate({ to: "/dashboards/$dashboardId", params: { dashboardId: id }, search: toSearchParams(next), replace });
  return <DashboardPage dashboardId={dashboardId} search={search} onSearch={(next, replace) => go(dashboardId, next, replace)} onOpen={(id, replace) => go(id, {}, replace)} />;
}
```

`ui/host/src/routes/dashboards.index.tsx`:

```tsx
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { DashboardPage } from "../dashboards/page";
import { parseSearch, toSearchParams } from "../dashboards/search";

export const Route = createFileRoute("/dashboards/")({
  component: DashboardIndex,
  validateSearch: (raw: Record<string, unknown>) => toSearchParams(parseSearch(raw)),
});

function DashboardIndex() {
  const search = parseSearch(Route.useSearch() as Record<string, unknown>);
  const navigate = useNavigate();
  return <DashboardPage search={search} onSearch={() => undefined} onOpen={(dashboardId, replace) => void navigate({ to: "/dashboards/$dashboardId", params: { dashboardId }, search: toSearchParams(search), replace })} />;
}
```

- [ ] **Step 8: Run the tests**

Run: `cd ui/host && bunx vitest run src/dashboards && bunx tsc --noEmit; cd ../..`
Expected: PASS. The old `dashboard.tsx` still exists and still compiles until Task 15 deletes it.

- [ ] **Step 9: Commit**

```bash
git add ui/host/src/dashboards ui/host/src/routes
git commit -m "feat(ui): dashboard page with URL view state, time, variables and batched panel data"
```

---

### Task 14: Grid, panel chrome, visualizations, inspect, full-screen view and edit mode

**Files:**
- Replace: `ui/host/src/dashboards/grid.tsx`
- Create: `ui/host/src/dashboards/panel-card.tsx`, `ui/host/src/dashboards/inspect.tsx`, `ui/host/src/dashboards/viz/stat.tsx`, `ui/host/src/dashboards/viz/gauge.tsx`, `ui/host/src/dashboards/viz/timeseries.tsx`, `ui/host/src/dashboards/viz/bar.tsx`, `ui/host/src/dashboards/viz/table.tsx`, `ui/host/src/dashboards/viz/text.tsx`, `ui/host/src/dashboards/viz/index.tsx`
- Modify: `ui/host/src/index.css` (panel markdown and grid rules)
- Test: `ui/host/src/dashboards/grid.test.tsx`

**Interfaces:**
- Consumes: Task 12 (`compile.ts`, `frame.ts`, `thresholds.ts`, `units.ts`, `EChartCanvas`, `api.ts`), Task 13 (`GridProps`, `interpolate`).
- Produces: `PanelGrid(props: GridProps)` (same props as the temporary one), `PanelCard`, `InspectDrawer`, `Viz({ panel, result, dark, height, onSelect })`, `rowHeight = 40` (matches `dashboard.RowHeight`).

- [ ] **Step 1: Write the failing test**

`ui/host/src/dashboards/grid.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./echart-canvas", () => ({ EChartCanvas: ({ label, onClick }: { label: string; onClick?: (p: { name: string }) => void }) => <button data-chart={label} onClick={() => onClick?.({ name: "cart" })}>{label}</button> }));

import { PanelGrid } from "./grid";
import type { DashboardSpec, PanelResult } from "../../../panels/types";

const spec: DashboardSpec = {
  version: 1, name: "Shop", time: { range: "1h" },
  variables: [{ name: "service", kind: "query", from: "spans", field: "service" }],
  panels: [
    { id: "requests", title: "Requests", viz: "stat", unit: "count", thresholds: [{ value: 100, status: "warn" }], query: { from: "spans", measures: ["count()"] }, grid: { x: 0, y: 0, w: 3, h: 3 } },
    { id: "by_service", title: "By service", viz: "bar", click: { set_variable: "service" }, query: { from: "spans", measures: ["count()"], by: ["service"] }, grid: { x: 3, y: 0, w: 6, h: 6 } },
    { id: "slow", title: "Slow", viz: "table", query: { from: "spans", measures: ["p95(duration_ms)"], by: ["http_route"] }, grid: { x: 0, y: 6, w: 12, h: 6 } },
    { id: "broken", title: "Broken", viz: "timeseries", query: { from: "spans", measures: ["count()"], bucket: "auto" }, grid: { x: 9, y: 0, w: 3, h: 6 } },
    { id: "notes", title: "Notes", viz: "text", content: "**Checkout** is the money path.", grid: { x: 0, y: 3, w: 3, h: 3 } },
  ],
};

const results = new Map<string, PanelResult>([
  ["requests", { id: "requests", status: "ok", frame: { columns: [{ name: "time", type: "time", role: "time" }, { name: "count", type: "number", role: "measure", unit: "count" }], values: [[1, 2], [60, 80]], rows: 2, totals: [null, 140] }, previous: { columns: [{ name: "time", type: "time", role: "time" }, { name: "count", type: "number", role: "measure", unit: "count" }], values: [[1], [70]], rows: 1, totals: [null, 70] }, elapsed_ms: 4, sql: "SELECT 1" }],
  ["by_service", { id: "by_service", status: "ok", frame: { columns: [{ name: "service", type: "string", role: "dimension" }, { name: "count", type: "number", role: "measure", unit: "count" }], values: [["checkout", "cart"], [90, 50]], rows: 2 }, elapsed_ms: 3 }],
  ["slow", { id: "slow", status: "ok", frame: { columns: [{ name: "http_route", type: "string", role: "dimension" }, { name: "p95", type: "number", role: "measure", unit: "ms" }], values: [["/cart", "/quote"], [900, 40]], rows: 2 }, elapsed_ms: 3 }],
  ["broken", { id: "broken", status: "error", error: "Conversion Error: Could not convert string 'checkout' to INT32", elapsed_ms: 1 }],
]);

beforeEach(() => {
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal("IntersectionObserver", class { observe() {} unobserve() {} disconnect() {} });
});
afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ""; });

async function render(overrides: Partial<Parameters<typeof PanelGrid>[0]> = {}) {
  const host = document.createElement("div");
  document.body.append(host);
  const props = { dashboardId: "d1", version: 2, spec, vars: { service: "checkout" }, results, fetching: false, editing: false, agentAvailable: true, onOpenChat: vi.fn(), onVariable: vi.fn(), onView: vi.fn(), onVisible: vi.fn(), ...overrides };
  await act(async () => {
    createRoot(host).render(<MantineProvider><QueryClientProvider client={new QueryClient()}><PanelGrid {...props} /></QueryClientProvider></MantineProvider>);
  });
  return { host, props };
}

describe("PanelGrid", () => {
  it("renders every visualization and state", async () => {
    const { host } = await render();
    expect(host.textContent).toContain("140");
    expect(host.textContent).toContain("Degraded");
    expect(host.textContent).toContain("+100%");
    expect(host.textContent).toContain("/cart");
    expect(host.textContent).toContain("900ms");
    expect(host.textContent).toContain("Could not convert");
    expect(host.querySelector("strong")?.textContent).toBe("Checkout");
  });

  it("sets the variable when a bar is clicked", async () => {
    const { host, props } = await render();
    await act(async () => { (host.querySelector('[data-chart^="By service"]') as HTMLButtonElement).click(); });
    expect(props.onVariable).toHaveBeenCalledWith("service", "cart");
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui/host && bunx vitest run src/dashboards/grid.test.tsx; cd ../..`
Expected: FAIL — the temporary grid renders none of these states.

- [ ] **Step 3: Implement the visualizations**

`ui/host/src/dashboards/viz/stat.tsx`:

```tsx
import { Box, Group, Text } from "@mantine/core";
import { previousStatValue as previousStat } from "./previous";
import { statValue, sparkline } from "../../../../panels/frame";
import { statusFor } from "../../../../panels/thresholds";
import type { Panel, PanelResult } from "../../../../panels/types";
import { formatValue } from "../../../../panels/units";
import { StatusChip } from "../panel-card";

export function StatViz({ panel, result }: { panel: Panel; result: PanelResult }) {
  const frame = result.frame!;
  const value = statValue(panel, frame);
  const unit = panel.unit ?? frame.columns.find((c) => c.role === "measure")?.unit;
  const status = statusFor(value, panel.thresholds, panel.better);
  const previous = previousStat(panel, result);
  const points = sparkline(frame);
  return <Box h="100%" style={{ display: "flex", flexDirection: "column", justifyContent: "space-between", gap: 4 }}>
    <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs">
      <Text fz={30} fw={600} lh={1.1} lts="-0.02em" ff="var(--mantine-font-family-monospace)">{formatValue(unit, value)}</Text>
      {status && <StatusChip status={status} />}
    </Group>
    {previous !== null && value !== null && <Delta value={value} previous={previous} better={panel.better ?? "lower"} unit={unit} />}
    <Sparkline points={points} />
  </Box>;
}

function Delta({ value, previous, better, unit }: { value: number; previous: number; better: "lower" | "higher"; unit?: string }) {
  const percentUnit = unit === "percent";
  const change = percentUnit ? value - previous : previous === 0 ? (value === 0 ? 0 : Infinity) : ((value - previous) / Math.abs(previous)) * 100;
  const up = change >= 0;
  const flat = Math.abs(change) < 2;
  const good = flat ? null : (up ? better === "higher" : better === "lower");
  const text = !Number.isFinite(change) ? "new" : percentUnit ? `${up ? "+" : "−"}${Math.abs(change).toFixed(1)} pts` : `${up ? "+" : "−"}${Math.abs(change).toFixed(0)}%`;
  return <Group gap={6}>
    <Text size="xs" fw={600} c={good === null ? "dimmed" : good ? "ok" : "bad"}>{up ? "▲" : "▼"} {text}</Text>
    <Text size="xs" c="dimmed">vs previous period</Text>
  </Group>;
}

function Sparkline({ points }: { points: (number | null)[] }) {
  const present = points.filter((p): p is number => p !== null);
  if (present.length < 2) return null;
  const min = Math.min(...present);
  const max = Math.max(...present);
  const w = 200;
  const h = 32;
  const x = (i: number) => (i / (points.length - 1)) * w;
  const y = (v: number) => h - 2 - ((v - min) / (max - min || 1)) * (h - 4);
  const line = points.map((p, i) => (p === null ? "" : `${i === 0 || points[i - 1] === null ? "M" : "L"}${x(i).toFixed(1)},${y(p).toFixed(1)}`)).join("");
  return <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" width="100%" height={h} aria-hidden>
    <path d={line} fill="none" stroke="var(--mantine-primary-color-filled)" strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
  </svg>;
}
```

`ui/host/src/dashboards/viz/gauge.tsx`:

```tsx
import { useMemo } from "react";
import { chartThemeFor, gaugeOption } from "../../../../panels/compile";
import { statValue } from "../../../../panels/frame";
import type { Panel, PanelResult } from "../../../../panels/types";
import { EChartCanvas } from "../echart-canvas";

export function GaugeViz({ panel, result, dark, height }: { panel: Panel; result: PanelResult; dark: boolean; height: number }) {
  const value = statValue(panel, result.frame!);
  const option = useMemo(() => gaugeOption(panel, value, chartThemeFor(dark)), [panel, value, dark]);
  return <EChartCanvas option={option} height={height} label={`${panel.title}: gauge`} />;
}
```

`ui/host/src/dashboards/viz/timeseries.tsx`:

```tsx
import { useMemo } from "react";
import { chartThemeFor, timeseriesOption } from "../../../../panels/compile";
import type { Panel, PanelResult } from "../../../../panels/types";
import { EChartCanvas } from "../echart-canvas";

export function TimeseriesViz({ panel, result, dark, height, group, onSelect }: { panel: Panel; result: PanelResult; dark: boolean; height: number; group: string; onSelect?: (value: string) => void }) {
  const option = useMemo(() => timeseriesOption(panel, result, chartThemeFor(dark)), [panel, result, dark]);
  const select = onSelect ? (params: { seriesName?: string }) => { if (params.seriesName && !params.seriesName.endsWith(" · previous") && params.seriesName !== "Other") onSelect(params.seriesName); } : undefined;
  return <EChartCanvas option={option} height={height} label={`${panel.title}: time series`} group={group} onClick={select} />;
}
```

`ui/host/src/dashboards/viz/bar.tsx`:

```tsx
import { useMemo } from "react";
import { barOption, chartThemeFor } from "../../../../panels/compile";
import type { Panel, PanelResult } from "../../../../panels/types";
import { EChartCanvas } from "../echart-canvas";

export function BarViz({ panel, result, dark, height, onSelect }: { panel: Panel; result: PanelResult; dark: boolean; height: number; onSelect?: (value: string) => void }) {
  const option = useMemo(() => barOption(panel, result.frame!, chartThemeFor(dark)), [panel, result, dark]);
  return <EChartCanvas option={option} height={height} label={`${panel.title}: bar chart`} onClick={onSelect ? (params) => params.name && onSelect(params.name) : undefined} />;
}
```

`ui/host/src/dashboards/viz/table.tsx`:

```tsx
import { Box, Table, Text, UnstyledButton } from "@mantine/core";
import { CaretDown, CaretUp } from "@phosphor-icons/react";
import { flexRender, getCoreRowModel, getSortedRowModel, useReactTable, type ColumnDef, type SortingState } from "@tanstack/react-table";
import { useMemo, useState } from "react";
import type { Cell, Panel, PanelResult } from "../../../../panels/types";
import { formatValue } from "../../../../panels/units";

type Row = Cell[];

export function TableViz({ panel, result, height, onSelect }: { panel: Panel; result: PanelResult; height: number; onSelect?: (value: string) => void }) {
  const frame = result.frame!;
  const rows = useMemo<Row[]>(() => Array.from({ length: frame.rows }, (_, r) => frame.columns.map((_, c) => frame.values[c][r])), [frame]);
  const firstMeasure = frame.columns.findIndex((c) => c.role === "measure");
  const maxima = useMemo(() => frame.columns.map((c, i) => (c.role === "measure" ? Math.max(0, ...frame.values[i].filter((v): v is number => typeof v === "number")) : 0)), [frame]);
  const columns = useMemo<ColumnDef<Row>[]>(() => frame.columns.map((column, index) => ({
    id: column.name,
    header: column.name,
    accessorFn: (row) => row[index],
    cell: (info) => {
      const value = info.getValue() as Cell;
      if (column.role === "measure" && typeof value === "number") {
        const unit = panel.unit ?? column.unit;
        return <Box style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", gap: 8 }}>
          <Text size="sm" ff="monospace">{formatValue(unit, value)}</Text>
          {index === firstMeasure && maxima[index] > 0 && <Box w={56} h={6} bg="var(--mantine-color-default-border)" style={{ borderRadius: 3, overflow: "hidden", flex: "none" }}><Box h="100%" w={`${(value / maxima[index]) * 100}%`} bg="var(--mantine-primary-color-filled)" /></Box>}
        </Box>;
      }
      if (column.type === "time" && typeof value === "number") return <Text size="sm" ff="monospace">{new Date(value).toLocaleString()}</Text>;
      return <Text size="sm" ff={column.type === "json" || /(_id|^id)$/.test(column.name) ? "monospace" : undefined} style={{ overflowWrap: "anywhere" }}>{value === null ? "—" : String(value)}</Text>;
    },
    sortingFn: column.role === "measure" ? "basic" : "alphanumeric",
  })), [frame, panel.unit, firstMeasure, maxima]);
  const [sorting, setSorting] = useState<SortingState>([]);
  const table = useReactTable({ data: rows, columns, state: { sorting }, onSortingChange: setSorting, getCoreRowModel: getCoreRowModel(), getSortedRowModel: getSortedRowModel() });
  const firstDimension = frame.columns.findIndex((c) => c.role === "dimension");
  return <Box mah={height} style={{ overflow: "auto" }}>
    <Table stickyHeader highlightOnHover={Boolean(onSelect)} fz="sm" verticalSpacing={6}>
      <Table.Thead>
        {table.getHeaderGroups().map((group) => <Table.Tr key={group.id}>
          {group.headers.map((header) => {
            const measure = frame.columns[header.index]?.role === "measure";
            return <Table.Th key={header.id} ta={measure ? "right" : undefined}>
              <UnstyledButton onClick={header.column.getToggleSortingHandler()} fz="xs" c="dimmed" ff="monospace" fw={500}>
                {flexRender(header.column.columnDef.header, header.getContext())}{header.column.getIsSorted() === "asc" ? <CaretUp size={10} /> : header.column.getIsSorted() === "desc" ? <CaretDown size={10} /> : null}
              </UnstyledButton>
            </Table.Th>;
          })}
        </Table.Tr>)}
      </Table.Thead>
      <Table.Tbody>
        {table.getRowModel().rows.map((row) => <Table.Tr key={row.id} style={{ cursor: onSelect ? "pointer" : undefined }} onClick={onSelect && firstDimension >= 0 ? () => onSelect(String(row.original[firstDimension] ?? "")) : undefined}>
          {row.getVisibleCells().map((cell) => <Table.Td key={cell.id}>{flexRender(cell.column.columnDef.cell, cell.getContext())}</Table.Td>)}
        </Table.Tr>)}
      </Table.Tbody>
    </Table>
    {frame.truncated && <Text size="xs" c="dimmed" mt={4}>Showing the first {frame.rows} rows.</Text>}
  </Box>;
}
```

`ui/host/src/dashboards/viz/text.tsx`:

```tsx
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Panel } from "../../../../panels/types";

export function TextViz({ panel }: { panel: Panel }) {
  return <div className="panel-markdown"><ReactMarkdown remarkPlugins={[remarkGfm]}>{panel.content ?? ""}</ReactMarkdown></div>;
}
```

`ui/host/src/dashboards/viz/index.tsx`:

```tsx
import type { Panel, PanelResult } from "../../../../panels/types";
import { BarViz } from "./bar";
import { GaugeViz } from "./gauge";
import { StatViz } from "./stat";
import { TableViz } from "./table";
import { TextViz } from "./text";
import { TimeseriesViz } from "./timeseries";

export function Viz({ panel, result, dark, height, group, onSelect }: { panel: Panel; result?: PanelResult; dark: boolean; height: number; group: string; onSelect?: (value: string) => void }) {
  if (panel.viz === "text") return <TextViz panel={panel} />;
  if (!result?.frame) return null;
  switch (panel.viz) {
    case "stat": return <StatViz panel={panel} result={result} />;
    case "gauge": return <GaugeViz panel={panel} result={result} dark={dark} height={height} />;
    case "timeseries": return <TimeseriesViz panel={panel} result={result} dark={dark} height={height} group={group} onSelect={onSelect} />;
    case "bar": return <BarViz panel={panel} result={result} dark={dark} height={height} onSelect={onSelect} />;
    case "table": return <TableViz panel={panel} result={result} height={height} onSelect={onSelect} />;
  }
}
```

Create `ui/host/src/dashboards/viz/previous.ts` (kept separate so `stat.tsx` and `index.tsx` do not import each other):

```ts
import { statValue } from "../../../../panels/frame";
import type { Panel, PanelResult } from "../../../../panels/types";

export function previousStatValue(panel: Panel, result: PanelResult): number | null {
  return result.previous ? statValue(panel, result.previous) : null;
}
```

- [ ] **Step 4: Implement panel chrome and inspect**

`ui/host/src/dashboards/panel-card.tsx`:

```tsx
import { ActionIcon, Badge, Box, Button, Center, Group, Loader, Menu, Paper, Stack, Text, Tooltip, useComputedColorScheme } from "@mantine/core";
import { ArrowsOut, ChatCircleText, Copy, DotsThree, Info, ListMagnifyingGlass, MagnifyingGlass, Trash, WarningCircle } from "@phosphor-icons/react";
import type { Panel, PanelResult, Status } from "../../../panels/types";
import { Viz } from "./viz";

const glyph: Record<Status, string> = { ok: "●", warn: "■", bad: "◆" };
const word: Record<Status, string> = { ok: "Healthy", warn: "Degraded", bad: "Unhealthy" };

/** Fanout's health shapes: the glyph is the second channel so state reads
 *  without colour. */
export function StatusChip({ status }: { status: Status }) {
  return <Badge color={status} variant="light" tt="none" leftSection={<span aria-hidden>{glyph[status]}</span>} style={{ minWidth: "max-content" }}>{word[status]}</Badge>;
}

export function PanelCard({ panel, title, result, loading, height, group, editing, agentAvailable, onSelect, onView, onInspect, onCopyLink, onExplain, onRemove }: {
  panel: Panel; title: string; result?: PanelResult; loading: boolean; height: number; group: string; editing: boolean; agentAvailable: boolean;
  onSelect?: (value: string) => void; onView(): void; onInspect(): void; onCopyLink(): void; onExplain(): void; onRemove?: () => void;
}) {
  const dark = useComputedColorScheme("light") === "dark";
  const bodyHeight = Math.max(80, height - 52);
  return <Paper withBorder radius="md" h="100%" p="sm" style={{ display: "flex", flexDirection: "column", minWidth: 0 }} data-panel={panel.id}>
    <Group justify="space-between" wrap="nowrap" gap="xs" mb={6} className={editing ? "panel-drag" : undefined} style={{ cursor: editing ? "grab" : undefined }}>
      <Group gap={6} wrap="nowrap" miw={0}>
        <Text fw={600} size="sm" truncate>{title}</Text>
        {panel.description && <Tooltip label={panel.description} multiline w={260}><Info size={14} color="var(--mantine-color-dimmed)" /></Tooltip>}
        {loading && result && <Loader size={12} />}
      </Group>
      <Menu position="bottom-end" withinPortal>
        <Menu.Target><ActionIcon variant="subtle" color="gray" size="sm" aria-label={`${title} menu`}><DotsThree size={18} weight="bold" /></ActionIcon></Menu.Target>
        <Menu.Dropdown>
          <Menu.Item leftSection={<ArrowsOut size={14} />} onClick={onView}>View</Menu.Item>
          {panel.viz !== "text" && <Menu.Item leftSection={<MagnifyingGlass size={14} />} onClick={onInspect}>Inspect</Menu.Item>}
          {agentAvailable && <Menu.Item leftSection={<ChatCircleText size={14} />} onClick={onExplain}>Explain in chat</Menu.Item>}
          <Menu.Item leftSection={<Copy size={14} />} onClick={onCopyLink}>Copy link</Menu.Item>
          {onRemove && <><Menu.Divider /><Menu.Item color="bad" leftSection={<Trash size={14} />} onClick={onRemove}>Remove panel</Menu.Item></>}
        </Menu.Dropdown>
      </Menu>
    </Group>
    <Box style={{ flex: 1, minHeight: 0 }}>
      {!result && panel.viz !== "text" ? <Center h="100%"><Loader size="sm" /></Center>
        : result?.status === "error" ? <Center h="100%"><Stack align="center" gap={4} maw={420}>
          <Group gap={6}><WarningCircle size={18} weight="fill" color="var(--mantine-color-bad-filled)" /><Text size="sm" fw={500} c="bad">This panel failed</Text></Group>
          <Text size="xs" c="dimmed" ta="center" style={{ overflowWrap: "anywhere" }}>{result.error}</Text>
          {agentAvailable && <Button size="compact-xs" variant="light" onClick={onExplain}>Ask Fanout to fix it</Button>}
        </Stack></Center>
        : result?.status === "empty" ? <Center h="100%"><Stack align="center" gap={4} maw={420}>
          <ListMagnifyingGlass size={20} color="var(--mantine-color-dimmed)" />
          <Text size="sm" c="dimmed" ta="center">{result.diagnosis || "No data in this time range."}</Text>
        </Stack></Center>
        : <Viz panel={panel} result={result} dark={dark} height={bodyHeight} group={group} onSelect={onSelect} />}
    </Box>
  </Paper>;
}
```

`ui/host/src/dashboards/inspect.tsx`:

```tsx
import { Code, Drawer, ScrollArea, Table, Tabs, Text } from "@mantine/core";
import type { Panel, PanelResult } from "../../../panels/types";
import { formatValue } from "../../../panels/units";

export function InspectDrawer({ panel, result, onClose }: { panel?: Panel; result?: PanelResult; onClose(): void }) {
  const frame = result?.frame;
  return <Drawer opened={Boolean(panel)} onClose={onClose} position="right" size="xl" title={panel ? `Inspect · ${panel.title}` : ""}>
    {panel && <Tabs defaultValue="data">
      <Tabs.List>
        <Tabs.Tab value="data">Data</Tabs.Tab>
        <Tabs.Tab value="query">Query</Tabs.Tab>
        <Tabs.Tab value="spec">Spec</Tabs.Tab>
        <Tabs.Tab value="timing">Timing</Tabs.Tab>
      </Tabs.List>
      <Tabs.Panel value="data" pt="sm">
        {frame ? <ScrollArea h="75vh"><Table fz="xs" striped>
          <Table.Thead><Table.Tr>{frame.columns.map((c) => <Table.Th key={c.name} ff="monospace">{c.name}</Table.Th>)}</Table.Tr></Table.Thead>
          <Table.Tbody>{Array.from({ length: Math.min(frame.rows, 500) }, (_, r) => <Table.Tr key={r}>{frame.columns.map((c, i) => {
            const v = frame.values[i][r];
            return <Table.Td key={c.name} ff="monospace">{v === null ? "—" : c.type === "time" ? new Date(Number(v)).toISOString() : c.role === "measure" ? formatValue(panel.unit ?? c.unit, Number(v)) : String(v)}</Table.Td>;
          })}</Table.Tr>)}</Table.Tbody>
        </Table></ScrollArea> : <Text c="dimmed" size="sm">No data.</Text>}
      </Tabs.Panel>
      <Tabs.Panel value="query" pt="sm"><Code block>{result?.sql ?? panel.sql ?? "—"}</Code></Tabs.Panel>
      <Tabs.Panel value="spec" pt="sm"><Code block>{JSON.stringify(panel, null, 2)}</Code></Tabs.Panel>
      <Tabs.Panel value="timing" pt="sm">
        <Text size="sm">Ran in {result?.elapsed_ms ?? 0} ms{result?.interval ? `, one point per ${result.interval}` : ""}{frame ? `, ${frame.rows} rows${frame.truncated ? " (truncated)" : ""}` : ""}.</Text>
      </Tabs.Panel>
    </Tabs>}
  </Drawer>;
}
```

- [ ] **Step 5: Implement the grid with edit mode and full-screen view**

`ui/host/src/dashboards/grid.tsx` (replace the temporary file; keep `GridProps` and `interpolate` exports):

```tsx
import { Alert, Button, Group, Modal, Text } from "@mantine/core";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { Responsive, WidthProvider } from "react-grid-layout/legacy";
import type { DashboardSpec, Panel, PanelResult, VarValue } from "../../../panels/types";
import { ApiError, patchDashboard, replaceDashboard } from "./api";
import { InspectDrawer } from "./inspect";
import { PanelCard } from "./panel-card";

const Grid = WidthProvider(Responsive);

/** Grid rows are 40 px with 12 px gaps; internal/dashboard/layout.go packs
 *  with the same row unit. */
export const rowHeight = 40;
const margin = 12;
const pixels = (h: number) => h * rowHeight + (h - 1) * margin;

export type GridProps = {
  dashboardId: string; version: number; spec: DashboardSpec; vars: Record<string, VarValue>; results: Map<string, PanelResult>; fetching: boolean; editing: boolean; view?: string;
  agentAvailable: boolean; onOpenChat(prompt?: string): void; onVariable(name: string, value: VarValue): void; onView(view?: string): void; onVisible(ids: string[]): void;
};

export const interpolate = (text: string, vars: Record<string, VarValue>) =>
  text.replace(/\$([a-z][a-z0-9_]*)/g, (match, name: string) => { const v = vars[name]; return v === undefined ? match : v === "$__all" ? "all" : Array.isArray(v) ? v.join(", ") : v; });

export function PanelGrid({ dashboardId, version, spec, vars, results, fetching, editing, view, agentAvailable, onOpenChat, onVariable, onView, onVisible }: GridProps) {
  const client = useQueryClient();
  const [layout, setLayout] = useState(() => spec.panels.map((p) => ({ i: p.id, x: p.grid?.x ?? 0, y: p.grid?.y ?? 0, w: p.grid?.w ?? 6, h: p.grid?.h ?? 6 })));
  useEffect(() => { setLayout(spec.panels.map((p) => ({ i: p.id, x: p.grid?.x ?? 0, y: p.grid?.y ?? 0, w: p.grid?.w ?? 6, h: p.grid?.h ?? 6 }))); }, [spec]);
  const [inspecting, setInspecting] = useState<string>();
  const container = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!container.current || typeof IntersectionObserver === "undefined") return;
    const seen = new Set<string>(spec.panels.map((p) => p.id));
    const observer = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        const id = (entry.target as HTMLElement).dataset.panel;
        if (!id) continue;
        if (entry.isIntersecting) seen.add(id); else seen.delete(id);
      }
      onVisible([...seen]);
    }, { rootMargin: "200px" });
    container.current.querySelectorAll("[data-panel]").forEach((el) => observer.observe(el));
    return () => observer.disconnect();
  }, [spec]);

  const save = useMutation({
    mutationFn: () => replaceDashboard(dashboardId, { ...spec, panels: spec.panels.map((p) => { const g = layout.find((l) => l.i === p.id)!; return { ...p, grid: { x: g.x, y: g.y, w: g.w, h: g.h } }; }) }, version, "Edited layout"),
    onSuccess: (record) => { client.setQueryData(["dashboard", dashboardId], record); },
  });
  const remove = useMutation({
    mutationFn: (id: string) => patchDashboard(dashboardId, [{ op: "remove_panel", id }], version, "Removed a panel"),
    onSuccess: (record) => { client.setQueryData(["dashboard", dashboardId], record); },
  });
  const dirty = useMemo(() => spec.panels.some((p) => { const g = layout.find((l) => l.i === p.id); return g && (g.x !== p.grid?.x || g.y !== p.grid?.y || g.w !== p.grid?.w || g.h !== p.grid?.h); }), [layout, spec]);
  const group = `dashboard-${dashboardId}`;

  const card = (panel: Panel, height: number) => <PanelCard panel={panel} title={interpolate(panel.title, vars)} result={results.get(panel.id)} loading={fetching} height={height} group={group} editing={editing} agentAvailable={agentAvailable}
    onSelect={panel.click ? (value) => onVariable(panel.click!.set_variable, value) : undefined}
    onView={() => onView(panel.id)} onInspect={() => setInspecting(panel.id)}
    onCopyLink={() => { const url = new URL(window.location.href); url.searchParams.set("view", panel.id); void navigator.clipboard?.writeText(url.toString()); }}
    onExplain={() => onOpenChat(`Explain the panel "${interpolate(panel.title, vars)}" on the dashboard "${spec.name}". ${results.get(panel.id)?.status === "error" ? `It fails with: ${results.get(panel.id)?.error}. Fix it with edit_dashboard.` : "What does it show right now, and is anything unusual?"}`)}
    onRemove={editing ? () => remove.mutate(panel.id) : undefined} />;

  const viewed = spec.panels.find((p) => p.id === view);
  const conflict = (save.error ?? remove.error) instanceof ApiError && ((save.error ?? remove.error) as ApiError).status === 409;
  return <div ref={container}>
    {editing && <Group justify="space-between" mb="sm" p="xs" style={{ border: "1px dashed var(--mantine-color-default-border)", borderRadius: 8 }}>
      <Text size="sm" c="dimmed">Drag a panel by its title and resize it from the corner. Changes are saved as a new version.</Text>
      <Button size="compact-sm" disabled={!dirty} loading={save.isPending} onClick={() => save.mutate()}>Save layout</Button>
    </Group>}
    {conflict && <Alert color="warn" mb="sm">Someone saved this dashboard since you opened it. Reload to see the latest version, then redo your change.</Alert>}
    <Grid className="dashboard-grid" layouts={{ lg: layout, md: layout, sm: layout.map((l) => ({ ...l, x: 0, w: 12 })) }} breakpoints={{ lg: 1100, md: 800, sm: 0 }} cols={{ lg: 12, md: 12, sm: 12 }}
      rowHeight={rowHeight} margin={[margin, margin]} containerPadding={[0, 0]} compactType="vertical" isDraggable={editing} isResizable={editing} draggableHandle=".panel-drag"
      onLayoutChange={(next: readonly { i: string; x: number; y: number; w: number; h: number }[]) => { if (editing) setLayout(next.map(({ i, x, y, w, h }) => ({ i, x, y, w, h }))); }}>
      {spec.panels.map((panel) => { const g = layout.find((l) => l.i === panel.id); return <div key={panel.id} data-panel={panel.id}>{card(panel, pixels(g?.h ?? 6))}</div>; })}
    </Grid>
    <Modal opened={Boolean(viewed)} onClose={() => onView(undefined)} fullScreen title={viewed ? interpolate(viewed.title, vars) : ""}>
      {viewed && <div style={{ height: "calc(100vh - 120px)" }}>{card(viewed, window.innerHeight - 140)}</div>}
    </Modal>
    <InspectDrawer panel={spec.panels.find((p) => p.id === inspecting)} result={inspecting ? results.get(inspecting) : undefined} onClose={() => setInspecting(undefined)} />
  </div>;
}
```

- [ ] **Step 6: Add the CSS rules**

Append to `ui/host/src/index.css`:

```css
.panel-markdown {
  font-size: 14px;
  line-height: 1.55;
  overflow-wrap: anywhere;
}

.panel-markdown :where(p, ul, ol) {
  margin: 0 0 0.5em;
}

.panel-markdown code {
  font-family: var(--mantine-font-family-monospace);
  font-size: 12px;
}
```

- [ ] **Step 7: Run the tests**

Run: `cd ui/host && bunx vitest run src/dashboards && bunx tsc --noEmit; cd ../..`
Expected: PASS. The `+100%` assertion comes from 140 against 70.

- [ ] **Step 8: Commit**

```bash
git add ui/host/src/dashboards ui/host/src/index.css
git commit -m "feat(ui): panel grid with six visualizations, inspect, full-screen view and layout editing"
```

---

### Task 15: Remove the widget dashboard, finish the shell, and pass `just check`

**Files:**
- Create: `ui/host/src/observability.ts` (moved helpers)
- Modify: `ui/host/src/rail.tsx`, `ui/host/src/api.ts`, `ui/host/src/rail.test.tsx`, `ui/host/src/app.test.tsx`
- Delete: `ui/host/src/dashboard.tsx`, `ui/host/src/dashboard.test.tsx`, `ui/host/src/dashboard-layout.ts`, `ui/host/src/dashboard-layout.test.ts`, `ui/host/src/dashboard-search.ts`, `ui/host/src/dashboard-search.test.ts`, `ui/host/src/widgets/` (every file)
- Generated: `internal/ui/dist/**` (via `just ui`)

**Interfaces:**
- Produces: `ui/host/src/observability.ts` exporting `freshFor`, `useObservability`, `widgetParams` renamed `observabilityParams`, `observabilityKey`, `ObservabilityKind`, `Filters` — moved verbatim from `widgets/data.ts` (only the rename differs).

- [ ] **Step 1: Move the observability helpers**

Create `ui/host/src/observability.ts` with the bodies of `freshFor`, `refetchInterval`, `observabilityKey`, `ObservabilityKind`, `useObservability`, `configString`, and `widgetParams` (renamed `observabilityParams`) copied from `ui/host/src/widgets/data.ts`. In `rail.tsx`, replace `import { freshFor, useObservability, widgetParams } from "./widgets/data";` with `import { freshFor, observabilityParams, useObservability } from "./observability";` and rename the one call site.

- [ ] **Step 2: Point the rail at the new list**

In `ui/host/src/api.ts`, replace `DashboardSummary` with `export type { DashboardSummary } from "./dashboards/api";` and delete `DashboardLayoutRecord`, `DashboardWidgetRecord`, `DashboardState`, `DashboardRecord`. In `rail.test.tsx` and `app.test.tsx`, change fixture fields from `widget_count: n` to `version: 1, panel_count: n`.

- [ ] **Step 3: Delete the widget dashboard**

```bash
git rm ui/host/src/dashboard.tsx ui/host/src/dashboard.test.tsx ui/host/src/dashboard-layout.ts ui/host/src/dashboard-layout.test.ts ui/host/src/dashboard-search.ts ui/host/src/dashboard-search.test.ts
git rm -r ui/host/src/widgets
```

Run: `rg -n "widgets/|dashboard-layout|dashboard-search|from \"./dashboard\"" ui/host/src`
Expected: no matches.

- [ ] **Step 4: Build and run the full gate**

Run: `just ui && just check`
Expected: `All checks passed`. Fix forward anything it reports (lint, notices for `@tanstack/react-table` via `just notices`, regenerated docs, committed `internal/ui/dist`). Do not weaken a check to pass it.

- [ ] **Step 5: Commit**

```bash
git add -A ui/host internal/ui/dist THIRD_PARTY_NOTICES* site
git commit -m "refactor(ui)!: replace widget dashboards with panel dashboards"
```

---

### Task 16: Verify on fanout-demo data and fix forward

**Files:**
- Create (untracked, scratch): a local data directory outside the repository
- Create: `docs/benchmarks/2026-10-agent-dashboards-m1.md` (measured results)
- Modify: whatever the verification finds broken, each fix with its own regression test and commit

**Interfaces:**
- Consumes: the built binary; the agent runtime; Playwright MCP.

- [ ] **Step 1: Copy the demo telemetry (read only on the demo host)**

Copy only `telemetry/batches/` from the demo host's data volume, read only, into a local scratch data directory. Use the access path in the deployment runbook; this plan records no host details. If the demo writes an older batch format than this branch accepts, replay the copied batches into the local instance through OTLP instead of loading them directly.

Never copy `control/` (users, sessions, tokens) or anything under `secrets/`. Expected: a few thousand `*.batch/` directories with `spans.parquet`, `logs.parquet`, `metrics.parquet`.

- [ ] **Step 2: Boot a local Fanout on the copy**

Follow `local_ui_screenshot_loop` in the project memory: export `FANOUT_DATA_DIR="$SCRATCH/demo-data"`, `FANOUT_AUTH_CODE_SECRET` (32+ characters), `FANOUT_AI_API_KEY`/`FANOUT_AI_MODEL` from `.env`, `FANOUT_HTTP_ADDR=127.0.0.1:7520`; run `just build && ./bin/fanout`; open the printed setup URL to create the admin. Expected: `/dashboards` shows "System overview" with every panel populated from demo data.

- [ ] **Step 3: Exercise the agent on the benchmark prompts**

In the chat pane, run at least prompts 1, 2, 4, 6 and 10 from the spec. For each, record: time to saved dashboard, panels ok/empty/error at save, and whether the result answers the question. Drive it in Playwright (the page is always visible there) and take a viewport screenshot of each dashboard in light and dark.

- [ ] **Step 4: Measure performance**

With a 12-panel, 24-hour dashboard open in Playwright, record the time from navigation to every panel rendered (S6, target ≤ 1.5 s p95 over 5 loads) and the number of `/api/panels/query` requests per refresh (S8, target 1). Record per-panel `elapsed_ms` from the responses (S7, target ≤ 500 ms p95 with a warm cache).

- [ ] **Step 5: Write the results and fix forward**

Write `docs/benchmarks/2026-10-agent-dashboards-m1.md` with the numbers, screenshots referenced by path, and a table of every defect found with its fix commit. For each defect: write the failing test first, fix, run `just check`, commit. Criteria still unmet at the end of the milestone (likely S1–S5, which milestone 3 targets) are listed with their measured values and carried into the milestone 2 plan.

- [ ] **Step 6: Commit**

```bash
git add docs/benchmarks/2026-10-agent-dashboards-m1.md
git commit -m "docs(benchmarks): measure milestone 1 dashboards on demo data"
```
