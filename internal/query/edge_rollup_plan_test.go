package query

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// The edge rollup is the most expensive statement this process issues, and
// until this test nothing executed it. Both of its shape defects were therefore
// invisible: every form returns identical rows, so a row assertion passes on
// all of them, and the only symptom was a pass that did not finish inside its
// budget.
//
// The plan is the behaviour under test. This asserts it in both directions --
// a guard that passes on the defect it was written for is worse than none --
// and checks that the forms agree on rows, which is what makes the rewrite a
// performance change rather than a behaviour one.
func TestEdgeRollupPlanUsesHashJoins(t *testing.T) {
	db := openTestDuck(t)
	defer db.Close()
	seedEdgeRollupFixture(t, db)

	preFix, err := os.ReadFile("testdata/edge_rollup_pre_fix.sql")
	if err != nil {
		t.Fatal(err)
	}
	commaForm := edgeRollupCommaForm(t)

	// Params: the ingested window is the fresh minute only; the start_time
	// bounds are deliberately wide, as they are in production.
	const windowLo, windowHi = 50, 200
	spanLo := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	spanHi := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	shippedPlan := explain(t, db, edgeRollupInsertSQL, windowLo, windowHi, spanLo, spanHi)
	commaPlan := explain(t, db, commaForm, windowLo, windowHi, spanLo, spanHi)

	// The defect stated as an assertion. If DuckDB ever folds this away, the
	// guard below stops proving anything and this says so rather than passing.
	if !strings.Contains(commaPlan, "CROSS_PRODUCT") {
		t.Fatalf("the comma form no longer plans a CROSS_PRODUCT, so this guard proves nothing now:\n%s", commaPlan)
	}
	for _, bad := range []string{"CROSS_PRODUCT", "DELIM_JOIN", "DELIM_GET", "NESTED_LOOP_JOIN"} {
		if strings.Contains(shippedPlan, bad) {
			t.Errorf("edge rollup plans a %s; the FROM clause has regressed to the comma form:\n%s", bad, shippedPlan)
		}
	}
	if !strings.Contains(shippedPlan, "HASH_JOIN") {
		t.Errorf("edge rollup plans no HASH_JOIN at all:\n%s", shippedPlan)
	}

	// All three forms must agree on rows. Wall-clock is logged rather than
	// asserted: the ratios are consistent but the absolute numbers are not
	// stable enough across machines to fail a build on.
	var want []string
	for _, form := range []struct{ name, sql string }{
		{"shipped", edgeRollupInsertSQL},
		{"comma", commaForm},
		{"pre-fix (range predicate in the ON clause)", string(preFix)},
	} {
		start := time.Now()
		edges := runEdgeRollup(t, db, form.sql, windowLo, windowHi, spanLo, spanHi)
		t.Logf("%-44s %6.0fms  %d edges", form.name, float64(time.Since(start).Microseconds())/1000, len(edges))
		if want == nil {
			if len(edges) == 0 {
				t.Fatal("the fixture produced no edges, so no plan was exercised")
			}
			want = edges
			continue
		}
		if strings.Join(edges, "|") != strings.Join(want, "|") {
			t.Errorf("%s disagrees on rows:\n got:  %v\n want: %v", form.name, edges, want)
		}
	}
	// frontend -> api -> db from the hand-built trace, svc-4 -> svc-5 from the
	// bulk rows, and frontend -> worker across the queue.
	const expected = "api->db call 1|frontend->api call 1|frontend->worker messaging 1|svc-4->svc-5 call 1000"
	if strings.Join(want, "|") != expected {
		t.Errorf("edges = %v,\n want %s", want, expected)
	}
}

// edgeRollupCommaForm rebuilds the pre-fix FROM clause from the shipped
// statement, so the two differ in exactly the thing under test and the guard
// cannot rot into comparing two stale copies.
func edgeRollupCommaForm(t *testing.T) string {
	t.Helper()
	const joined = "  FROM spans child\n  JOIN parent_scope parent"
	if !strings.Contains(edgeRollupInsertSQL, joined) {
		t.Fatal("the edge rollup FROM clause no longer matches what this test rewrites")
	}
	form := strings.Replace(edgeRollupInsertSQL, joined, "  FROM spans child, bounds\n  JOIN parent_scope parent", 1)
	if !strings.Contains(form, "\n  CROSS JOIN bounds\n") {
		t.Fatal("no CROSS JOIN bounds to remove")
	}
	return strings.Replace(form, "\n  CROSS JOIN bounds\n", "\n", 1)
}

func explain(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query("EXPLAIN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan.String()
}

// runEdgeRollup executes the statement into an empty edge_rollup and returns
// the edges it wrote, so forms can be compared on rows as well as on plan.
func runEdgeRollup(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	if _, err := db.Exec("DELETE FROM edge_rollup"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("edge rollup: %v", err)
	}
	rows, err := db.Query(`SELECT caller, callee, edge_type, calls FROM edge_rollup ORDER BY caller, callee, edge_type`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var edges []string
	for rows.Next() {
		var caller, callee, edgeType string
		var calls int64
		if err := rows.Scan(&caller, &callee, &edgeType, &calls); err != nil {
			t.Fatal(err)
		}
		edges = append(edges, fmt.Sprintf("%s->%s %s %d", caller, callee, edgeType, calls))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return edges
}

// seedEdgeRollupFixture builds the production shape: a long history of spans
// that were rolled up long ago, and one freshly ingested minute. The two differ
// by orders of magnitude, which is what makes where the parent side is filtered
// matter. spans is a view over Parquet, as it is in production -- against a
// DuckDB table the planner sees different statistics and the forms converge.
func seedEdgeRollupFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(createEdgeRollupTable); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE spans (
		namespace TEXT, trace_id TEXT, span_id TEXT, parent_span_id TEXT,
		service TEXT, kind TEXT, status TEXT, duration_ms DOUBLE,
		start_time TIMESTAMP, ingested_unix_nano BIGINT, attributes_json TEXT
	)`); err != nil {
		t.Fatal(err)
	}

	// The smallest trace that exercises both halves: a two-hop call chain and a
	// producer/consumer pair, all inside the freshly ingested minute.
	fresh := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	for _, s := range []struct{ id, parent, service, kind, attributes string }{
		{id: "root", service: "frontend", kind: "SPAN_KIND_SERVER"},
		{id: "a", parent: "root", service: "api", kind: "SPAN_KIND_SERVER"},
		{id: "b", parent: "a", service: "db", kind: "SPAN_KIND_CLIENT"},
		{id: "p", parent: "root", service: "frontend", kind: "SPAN_KIND_PRODUCER",
			attributes: `{"messaging.destination.name":"jobs","messaging.system":"kafka"}`},
		{id: "c", service: "worker", kind: "SPAN_KIND_CONSUMER",
			attributes: `{"messaging.destination.name":"jobs","messaging.system":"kafka"}`},
	} {
		attributes := s.attributes
		if attributes == "" {
			attributes = "{}"
		}
		if _, err := db.Exec(`INSERT INTO spans VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"default", "trace-1", s.id, s.parent, s.service, s.kind,
			"STATUS_CODE_OK", 5.0, fresh, int64(100), attributes); err != nil {
			t.Fatal(err)
		}
	}
	// 2k more spans in that same minute, so the fresh window is not trivially
	// small relative to the joins.
	if _, err := db.Exec(`INSERT INTO spans
		SELECT 'default', 'new-' || (i // 2)::VARCHAR, 'new-' || i::VARCHAR,
		       CASE WHEN i % 2 = 1 THEN 'new-' || (i - 1)::VARCHAR ELSE '' END,
		       'svc-' || (i % 2 + 4)::VARCHAR, 'SPAN_KIND_SERVER', 'STATUS_CODE_OK', 5.0,
		       TIMESTAMP '2026-09-20 12:00:00', 100, '{}'
		FROM range(2000) t(i)`); err != nil {
		t.Fatal(err)
	}
	// 600k spans across 25 days, ingested long before the window under test.
	if _, err := db.Exec(`INSERT INTO spans
		SELECT 'default', 'old-' || (i // 2)::VARCHAR, 'old-' || i::VARCHAR,
		       CASE WHEN i % 2 = 1 THEN 'old-' || (i - 1)::VARCHAR ELSE '' END,
		       'svc-' || (i % 8)::VARCHAR, 'SPAN_KIND_SERVER', 'STATUS_CODE_OK', 5.0,
		       TIMESTAMP '2026-08-26 00:00:00' + INTERVAL (i % 36000) MINUTE, 1, '{}'
		FROM range(600000) t(i)`); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if _, err := db.Exec(`COPY (SELECT * FROM spans) TO '` + dir + `/spans.parquet' (FORMAT PARQUET)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE spans`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE VIEW spans AS SELECT * FROM read_parquet('` + dir + `/spans.parquet')`); err != nil {
		t.Fatal(err)
	}
}
