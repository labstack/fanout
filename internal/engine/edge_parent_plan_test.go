package engine

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
	store "github.com/labstack/fanout/internal/telemetry/store"
)

// A schema-first catalog estimates zero raw rows while materialized children
// have real statistics. Neither parent lookup may hash the raw span history.
func TestEdgeParentScansStayOnProbeSide(t *testing.T) {
	for _, batches := range []int{1, 63, 64} {
		t.Run(fmt.Sprint(batches, " batches"), func(t *testing.T) {
			d, repo := newBatchCacheTest(t)
			at := time.Now().UTC().Truncate(time.Minute)
			for i := range batches {
				if err := repo.Commit(t.Context(), store.Batch{ID: fmt.Sprintf("001-catalog-%03d", i), Spans: []telemetry.Span{
					{Namespace: "ns", TraceID: "trace", SpanID: fmt.Sprint(i), ServiceName: "svc", StartUnixNanos: at.UnixNano()},
					{Namespace: "ns", SpanID: "no-trace", ServiceName: "svc", StartUnixNanos: at.UnixNano()},
				}}); err != nil {
					t.Fatal(err)
				}
			}
			pin := d.pinParquet()
			defer d.unpinParquet(pin)
			tx, err := d.writer().BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			unbind, err := d.bindRollupSnapshot(t.Context(), tx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec("CREATE TEMP TABLE edge_pass_buckets AS SELECT 'ns' namespace,?::TIMESTAMP_NS::TIMESTAMPTZ_NS bucket", at); err != nil {
				t.Fatal(err)
			}
			hi := at.Add(time.Minute)
			if err := createEdgeChildren(t.Context(), tx, at, hi); err != nil {
				t.Fatal(err)
			}
			capture := &planCapture{rollupSQL: tx}
			if _, err := capture.ExecContext(t.Context(), createEdgeParentsSQL, at, hi); err != nil {
				t.Fatal(err)
			}
			requireProbedSpans(t, capture.plan)
			var count int
			if err := tx.QueryRow("SELECT count(*) FROM edge_pass_parents").Scan(&count); err != nil || count != batches*2 {
				t.Fatalf("near parents=%d: %v", count, err)
			}
			if _, err := tx.Exec("CREATE TEMP TABLE edge_pass_unmatched AS SELECT DISTINCT namespace,trace_id FROM edge_pass_children"); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec("DELETE FROM edge_pass_parents"); err != nil {
				t.Fatal(err)
			}
			if _, err := capture.ExecContext(t.Context(), insertEdgeDistantParentsSQL, at, hi); err != nil {
				t.Fatal(err)
			}
			requireProbedSpans(t, capture.plan)
			if err := tx.QueryRow("SELECT count(*) FROM edge_pass_parents").Scan(&count); err != nil || count != batches*2 {
				t.Fatalf("distant parents=%d: %v", count, err)
			}
			if err := unbind(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Compare both parent windows and complete rollup rows with the original
// equality semi-joins. NULL struct fields compare equal in DuckDB, unlike SQL
// equality, so NULL keys on either side must never contribute parents.
func TestEdgeParentMembershipMatchesSemiJoin(t *testing.T) {
	db := openTestDuck(t)
	for _, q := range []string{"SET threads=1", createEdgeRollupTable} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	lo, hi := time.Unix(0, 0).UTC(), time.Unix(60, 0).UTC()
	for seed := range 12 {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := db.Exec(q, args...); err != nil {
					t.Fatal(err)
				}
			}
			exec(fmt.Sprintf(`CREATE OR REPLACE TABLE spans AS
WITH keys AS (SELECT i, CASE WHEN i%%11=0 THEN NULL ELSE ['ns','other','o''k',''][1+(hash(i,%d)%%4)::INTEGER] END namespace,
 CASE WHEN i%%13=0 THEN NULL ELSE 'trace-'||i::VARCHAR END trace_id,
 [-3600000000001,-3600000000000,-300000000001,-300000000000,0,300000000000,300000000001,3600000000000,3600000000001][1+(i%%9)::INTEGER] offset_ns
 FROM range(180) t(i))
SELECT namespace,trace_id, CASE WHEN c=0 THEN 'parent' ELSE 'child' END span_id,
 CASE WHEN c=0 THEN '' ELSE 'parent' END parent_span_id,
 CASE WHEN i%%17=0 THEN NULL WHEN i%%19=0 THEN '' WHEN c=0 THEN 'api' ELSE 'db' END service,
 make_timestamp_ns(CASE WHEN c=0 THEN offset_ns ELSE (hash(i,%d)%%59000000000)::BIGINT END)::TIMESTAMPTZ_NS start_time,
 (hash(i,9)%%100)::DOUBLE duration_ms,
 CASE WHEN i%%7=0 THEN 'STATUS_CODE_ERROR' ELSE 'STATUS_CODE_OK' END status,
 CASE WHEN i%%5=0 THEN CASE WHEN c=0 THEN 'SPAN_KIND_PRODUCER' ELSE 'SPAN_KIND_CONSUMER' END ELSE 'SPAN_KIND_SERVER' END kind, NULL::VARCHAR peer_service,
 CASE WHEN i%%5=0 THEN 'queue' END messaging_destination,CASE WHEN i%%5=0 THEN 'kafka' END messaging_system
 FROM keys CROSS JOIN range(2) t(c)
 UNION ALL SELECT namespace,trace_id,'parent','','duplicate',make_timestamp_ns(offset_ns)::TIMESTAMPTZ_NS,1,'STATUS_CODE_OK','SPAN_KIND_SERVER',NULL,NULL,NULL FROM keys WHERE i%%7=0`, seed, seed))
			exec(`CREATE OR REPLACE TEMP TABLE edge_pass_buckets AS SELECT DISTINCT namespace,make_timestamp_ns(0)::TIMESTAMPTZ_NS bucket FROM spans`)
			// Include NULL namespaces here as well as NULL traces to exercise both
			// membership predicates independently of the child-key prefilter.
			exec(`CREATE OR REPLACE TEMP TABLE edge_pass_children AS SELECT namespace,trace_id,parent_span_id,service,start_time,duration_ms,status FROM spans WHERE span_id='child'`)
			compare := func(left, right string) {
				t.Helper()
				var diff, n int
				q := fmt.Sprintf(`SELECT (SELECT count(*) FROM ((SELECT * FROM %s EXCEPT ALL SELECT * FROM %s) UNION ALL (SELECT * FROM %s EXCEPT ALL SELECT * FROM %s))),(SELECT count(*) FROM %s)`, left, right, right, left, left)
				if err := db.QueryRow(q).Scan(&diff, &n); err != nil || diff != 0 || n == 0 {
					t.Fatalf("%s vs %s: diff=%d rows=%d: %v", left, right, diff, n, err)
				}
			}
			for _, reference := range []bool{false, true} {
				parents, distant := createEdgeParentsSQL, insertEdgeDistantParentsSQL
				if reference {
					for _, pair := range []struct {
						q        *string
						relation string
					}{{&parents, "child_traces"}, {&distant, "edge_pass_unmatched"}} {
						predicate := `CROSS JOIN bounds
WHERE ((parent.namespace, parent.trace_id) IN (
  SELECT (child.namespace, child.trace_id) FROM ` + pair.relation + ` child
  WHERE child.namespace IS NOT NULL AND child.trace_id IS NOT NULL
)) IS TRUE
  AND`
						replacement := `SEMI JOIN ` + pair.relation + ` child ON parent.namespace=child.namespace AND parent.trace_id=child.trace_id
CROSS JOIN bounds
WHERE`
						old := *pair.q
						*pair.q = strings.Replace(old, predicate, replacement, 1)
						if *pair.q == old {
							t.Fatal("parent membership predicate not found")
						}
					}
				}
				exec("DROP TABLE IF EXISTS edge_pass_parents")
				exec(parents, lo, hi)
				if reference {
					compare("near_parents", "edge_pass_parents")
				} else {
					exec("CREATE OR REPLACE TEMP TABLE near_parents AS SELECT * FROM edge_pass_parents")
				}
				exec("DROP TABLE IF EXISTS edge_pass_unmatched")
				exec(createEdgeUnmatchedSQL, lo, hi)
				var unmatched int
				if err := db.QueryRow("SELECT count(*) FROM edge_pass_unmatched").Scan(&unmatched); err != nil || unmatched == 0 {
					t.Fatalf("unmatched=%d: %v", unmatched, err)
				}
				exec(deleteEdgeUnmatchedParentsSQL)
				exec(distant, lo, hi)
				if reference {
					compare("wide_parents", "edge_pass_parents")
				} else {
					exec("CREATE OR REPLACE TEMP TABLE wide_parents AS SELECT * FROM edge_pass_parents")
				}
				var invalid int
				if err := db.QueryRow(`SELECT count(*) FROM edge_pass_parents WHERE namespace IS NULL OR trace_id IS NULL`).Scan(&invalid); err != nil || invalid != 0 {
					t.Fatalf("NULL parents=%d: %v", invalid, err)
				}
				exec("DELETE FROM edge_rollup")
				exec(edgeRollupInsertSQL, lo, hi)
				if reference {
					compare("new_edges", "edge_rollup")
				} else {
					exec("CREATE OR REPLACE TEMP TABLE new_edges AS SELECT * FROM edge_rollup")
				}
			}
		})
	}
}

// Audit the other raw-signal joins with both data-first and schema-first
// estimates. Arrival repair must build incoming keys, messaging must aggregate
// before joining, and service rollups must aggregate logs and metrics first.
func TestOtherRollupJoinsKeepHistoryOffHashBuildSide(t *testing.T) {
	db := openTestDuck(t)
	seedEdgeRollupFixture(t, db)
	dir := t.TempDir()
	for _, signal := range []string{"spans", "logs", "metrics"} {
		query := "SELECT * FROM spans"
		if signal != "spans" {
			query = "SELECT namespace,service,start_time AS time,'requests' AS name FROM spans"
		}
		for _, empty := range []bool{false, true} {
			suffix := "data"
			if empty {
				suffix = "schema"
				query += " WHERE false"
			}
			if _, err := db.Exec("COPY (" + query + ") TO " + sqlLiteral(filepath.Join(dir, signal+"-"+suffix+".parquet")) + " (FORMAT PARQUET)"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Exec(createServiceRollupTable); err != nil {
		t.Fatal(err)
	}
	for _, schemaFirst := range []bool{false, true} {
		t.Run(fmt.Sprint("schema first=", schemaFirst), func(t *testing.T) {
			for _, signal := range []string{"spans", "logs", "metrics"} {
				files := sqlLiteral(filepath.Join(dir, signal+"-data.parquet"))
				if schemaFirst {
					files = sqlLiteral(filepath.Join(dir, signal+"-schema.parquet")) + "," + files
				}
				if _, err := db.Exec("CREATE OR REPLACE VIEW " + signal + " AS SELECT * FROM read_parquet([" + files + "],union_by_name=false,hive_partitioning=false)"); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			plan := func(q string, args ...any) any {
				t.Helper()
				var key, raw string
				if err := tx.QueryRow("EXPLAIN (FORMAT JSON) "+q, args...).Scan(&key, &raw); err != nil {
					t.Fatal(err)
				}
				var value any
				if err := json.Unmarshal([]byte(raw), &value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			buckets := plan(createEdgePassBucketsSQL, 50, 200)
			requireRawScansNotHashed(t, buckets)
			// These two semi-joins read materialized incoming/recent relations. With
			// equal estimates the shipped plan must still probe recent, build incoming.
			var checkRepair func(any)
			checkRepair = func(v any) {
				switch n := v.(type) {
				case []any:
					for _, c := range n {
						checkRepair(c)
					}
				case map[string]any:
					extra, _ := n["extra_info"].(map[string]any)
					if extra["Join Type"] == "RIGHT_SEMI" {
						t.Errorf("arrival repair reversed its recent history join:\n%s", planText(buckets))
					}
					checkRepair(n["children"])
				}
			}
			checkRepair(buckets)
			if _, err := tx.Exec(createEdgePassBucketsSQL, 50, 200); err != nil {
				t.Fatal(err)
			}
			lo, hi := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			if _, err := prepareEdgeParents(t.Context(), tx, lo, hi); err != nil {
				t.Fatal(err)
			}
			requireRawScansNotHashed(t, plan(edgeRollupInsertSQL, lo, hi))
			if _, err := tx.Exec("CREATE TEMP TABLE service_pass_affected AS SELECT namespace,date_trunc('minute',start_time::TIMESTAMP_NS) bucket,service FROM spans GROUP BY ALL"); err != nil {
				t.Fatal(err)
			}
			requireRawScansNotHashed(t, plan(serviceRollupInsertSQL("true"), lo, hi, lo, hi, lo, hi))
		})
	}
}

// The other rollups legitimately use nested loops against singleton time
// bounds. Guard hash builds specifically, allowing aggregates below a build.
func requireRawScansNotHashed(t *testing.T, plan any) {
	t.Helper()
	scans := 0
	var visit func(any, bool)
	visit = func(v any, build bool) {
		switch n := v.(type) {
		case []any:
			for _, c := range n {
				visit(c, build)
			}
		case map[string]any:
			name := fmt.Sprint(n["name"])
			if name == "READ_PARQUET" {
				scans++
				if build {
					t.Errorf("raw signal scan feeds a hash build:\n%s", planText(plan))
				}
			}
			if strings.Contains(name, "GROUP_BY") || strings.Contains(name, "AGGREGATE") {
				build = false
			}
			children, _ := n["children"].([]any)
			for i, c := range children {
				visit(c, build || (name == "HASH_JOIN" && i > 0))
			}
		}
	}
	visit(plan, false)
	if scans == 0 {
		t.Fatal("no raw signal scan in plan")
	}
}
