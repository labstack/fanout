package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/pitot/internal/queryrows"
	"github.com/labstack/pitot/internal/telemetry"
	telemetrystore "github.com/labstack/pitot/internal/telemetry/store"
)

// A NUL byte in a namespace must neither fail a rollup pass nor stop the
// watermark for every other namespace.
func TestRollupsAcceptNULNamespace(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	const namespace = "a\x00b'c"
	spans := []telemetry.Span{
		{Namespace: namespace, TraceID: "t", SpanID: "parent", ServiceName: "gateway", Kind: "SPAN_KIND_SERVER", StartUnixNanos: base.UnixNano()},
		{Namespace: namespace, TraceID: "t", SpanID: "child", ParentSpanID: "parent", ServiceName: "checkout", Kind: "SPAN_KIND_SERVER", StartUnixNanos: base.Add(time.Second).UnixNano()},
		{Namespace: "shop", TraceID: "t", SpanID: "parent", ServiceName: "gateway", Kind: "SPAN_KIND_SERVER", StartUnixNanos: base.UnixNano()},
		{Namespace: "shop", TraceID: "t", SpanID: "child", ParentSpanID: "parent", ServiceName: "checkout", Kind: "SPAN_KIND_SERVER", StartUnixNanos: base.Add(time.Second).UnixNano()},
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "nul", Spans: spans}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatalf("service rollup: %v", err)
	}
	if _, _, err := d.refreshEdgeRollupPass(t.Context()); err != nil {
		t.Fatalf("edge rollup: %v", err)
	}
	for _, ns := range []string{namespace, "shop"} {
		requireRowCount(t, d.DB, "SELECT count(*) FROM service_rollup WHERE spans=1 AND namespace=?", ns, 2)
		requireRowCount(t, d.DB, "SELECT count(*) FROM edge_rollup WHERE caller='gateway' AND callee='checkout' AND calls=1 AND namespace=?", ns, 1)
	}
	for _, key := range []string{serviceRollupStateKey, edgeRollupStateKey} {
		if d.rollupWatermarks[key] == 0 {
			t.Fatalf("%s watermark did not advance", key)
		}
	}
}

func TestSQLLiteralPreservesNUL(t *testing.T) {
	db := openTestDuck(t)
	for _, value := range []string{"", "'", "\x00", "\x00a'\x00b\x00", "🙂\x00é"} {
		var got string
		if err := db.QueryRow("SELECT " + sqlLiteral(value)).Scan(&got); err != nil || got != value {
			t.Fatalf("literal %q returned %q: %v", value, got, err)
		}
	}
}

func TestCachedReadScopesAcceptNUL(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	const ns, svc = "a\x00b'c", "api\x00'"
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "scope", Spans: []telemetry.Span{
		{Namespace: ns, ServiceName: svc, TraceID: "trace", SpanID: "span", StartUnixNanos: at.UnixNano(), EndUnixNanos: at.Add(time.Second).UnixNano()},
		{Namespace: "other", ServiceName: svc, TraceID: "other", SpanID: "span", StartUnixNanos: at.UnixNano()},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	w := queryrows.Window{Start: at.Add(-time.Second), End: at.Add(time.Minute), Namespace: ns, Service: svc, Kind: queryrows.TraceCandidateRead}
	ctx := inventoryContext(t.Context(), w)
	for _, relation := range []string{"trace_candidates", "resource_inventory"} {
		var count int
		if err := d.QueryRowScan(ctx, []any{&count}, "SELECT count(*) FROM "+relation); err != nil || count != 1 {
			t.Fatalf("%s: count=%d: %v", relation, count, err)
		}
	}
}

// The raw span scan must only be probed: as a hash-join build side its memory
// grows with every span in range rather than with the affected keys.
func TestServiceRollupSpanScanIsNeverBuildSide(t *testing.T) {
	for _, keys := range []int{1, 2, 40} {
		t.Run(fmt.Sprint(keys, " keys"), func(t *testing.T) {
			stride := 36
			if keys == 2 {
				stride = 1439 // A late span widens the scan across the whole day.
			}
			db, _ := sparseServiceFixture(t, 552960, 384, 24, fmt.Sprintf(`SELECT 'ns' AS namespace,make_timestamp((i*%d)::BIGINT*60000000) AS bucket,'svc-0' AS service FROM range(%d) t(i)`, stride, keys))
			tx, err := db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			plan := &planCapture{rollupSQL: tx}
			lo, hi := time.Unix(0, 0).UTC(), time.Unix(1440*60, 0).UTC()
			if _, err := insertServiceRollup(t.Context(), plan, lo, hi); err != nil {
				t.Fatal(err)
			}
			if len(plan.plans) != 2 {
				t.Fatalf("span scan plans=%d want 2", len(plan.plans))
			}
			for _, scan := range plan.plans {
				requireProbedSpans(t, scan)
			}
		})
	}
}

func TestEdgeChildrenSpanScanIsNeverBuildSide(t *testing.T) {
	db, _ := sparseServiceFixture(t, 552960, 384, 24, `SELECT 'ns' AS namespace, TIMESTAMP '1970-01-01' AS bucket, 'svc-0' AS service`)
	for _, q := range []string{
		"CREATE OR REPLACE VIEW spans AS SELECT *, 'trace' AS trace_id, 'parent' AS parent_span_id FROM span_parquet",
		`CREATE TEMP TABLE edge_pass_buckets AS SELECT 'ns' AS namespace, make_timestamp_ns(i*60000000000)::TIMESTAMPTZ_NS AS bucket FROM range(0,1440,36) t(i)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	plan := &planCapture{rollupSQL: tx}
	if err := createEdgeChildren(t.Context(), plan, time.Unix(0, 0).UTC(), time.Unix(1440*60, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	requireProbedSpans(t, plan.plan)
}

// planCapture explains each statement before running it and keeps the plan
// of the last raw span scan.
type planCapture struct {
	rollupSQL
	plan  any
	plans []any
}

func (p *planCapture) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if !strings.Contains(query, "FROM spans") || strings.Contains(query, "WHERE false") {
		return p.rollupSQL.ExecContext(ctx, query, args...)
	}
	rows, err := p.rollupSQL.QueryContext(ctx, "EXPLAIN (FORMAT JSON) "+query, args...)
	if err != nil {
		return nil, err
	}
	var key, plan string
	for rows.Next() {
		if err := rows.Scan(&key, &plan); err != nil {
			rows.Close()
			return nil, err
		}
	}
	rows.Close()
	if err := json.Unmarshal([]byte(plan), &p.plan); err != nil {
		return nil, err
	}
	p.plans = append(p.plans, p.plan)
	return p.rollupSQL.ExecContext(ctx, query, args...)
}

// requireProbedSpans fails if a raw Parquet scan feeds any join's build
// (right) side without an aggregate between them, or if there is no raw scan.
func requireProbedSpans(t *testing.T, plan any) {
	t.Helper()
	scans := 0
	var visit func(v any, build bool)
	visit = func(v any, build bool) {
		switch x := v.(type) {
		case []any:
			for _, c := range x {
				visit(c, build)
			}
		case map[string]any:
			name := fmt.Sprint(x["name"])
			if name == "READ_PARQUET" {
				scans++
				if build {
					t.Errorf("raw span scan is under a join build side:\n%s", planText(plan))
				}
			}
			// An aggregate below a build side is what is built, not its input.
			if strings.Contains(name, "GROUP_BY") || strings.Contains(name, "AGGREGATE") {
				build = false
			}
			children, _ := x["children"].([]any)
			for i, c := range children {
				visit(c, build || (strings.Contains(name, "JOIN") && i > 0))
			}
		}
	}
	visit(plan, false)
	if scans == 0 {
		t.Fatalf("no raw span scan in plan:\n%s", planText(plan))
	}
}

func planText(plan any) string {
	var b strings.Builder
	var visit func(any, int)
	visit = func(v any, depth int) {
		switch x := v.(type) {
		case []any:
			for _, c := range x {
				visit(c, depth)
			}
		case map[string]any:
			extra, _ := json.Marshal(x["extra_info"])
			fmt.Fprintf(&b, "%s%v %.200s\n", strings.Repeat("  ", depth), x["name"], extra)
			visit(x["children"], depth+1)
		}
	}
	visit(plan, 0)
	return b.String()
}

// sparseServiceFixture writes a schema-first Parquet span relation, as
// production reads it, with the given affected service keys.
func sparseServiceFixture(t *testing.T, rows, perMinute, services int, affected string) (*sql.DB, string) {
	t.Helper()
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	schema, data := filepath.Join(dir, "schema.parquet"), filepath.Join(dir, "data.parquet")
	for _, q := range []string{
		"SET threads=4",
		fmt.Sprintf(`CREATE VIEW span_fixture AS SELECT CASE WHEN i%%13=0 THEN 'other' ELSE 'ns' END AS namespace,'svc-'||(i%%%d)::VARCHAR AS service,
    make_timestamp_ns((i//%d)*60000000000+(i%%59)*1000000000)::TIMESTAMPTZ_NS AS start_time,
    CASE i%%3 WHEN 0 THEN 'SPAN_KIND_CLIENT' WHEN 1 THEN 'SPAN_KIND_SERVER' ELSE NULL END AS kind,
    CASE WHEN i%%17=0 THEN 'STATUS_CODE_ERROR' ELSE 'STATUS_CODE_OK' END AS status,(i%%97)::DOUBLE AS duration_ms
    FROM range(%d) t(i)`, services, perMinute, rows),
		"COPY (SELECT * FROM span_fixture WHERE false) TO " + sqlLiteral(schema) + " (FORMAT PARQUET)",
		"COPY span_fixture TO " + sqlLiteral(data) + " (FORMAT PARQUET)",
		"CREATE OR REPLACE VIEW span_parquet AS SELECT * FROM read_parquet([" + sqlLiteral(schema) + "," + sqlLiteral(data) + "],union_by_name=false,hive_partitioning=false)",
		"CREATE OR REPLACE VIEW spans AS SELECT * FROM span_parquet",
		"CREATE TEMP TABLE service_pass_affected AS " + affected,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db, dir
}

// The bound-key filter must produce exactly what the previous semi-join did,
// including NULL and empty keys, NUL bytes, quotes and unspanned keys.
func TestServiceRollupKeysMatchSemiJoin(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatal(err)
	}
	names := []string{"ns", "a\x00b", "o'k", "", "\x00"}
	services := []string{"api", "db", "x\x00y", "it's"}
	if _, err := db.Exec("SET threads=1; DROP VIEW spans"); err != nil {
		t.Fatal(err)
	}
	for seed := range uint64(6) {
		r := rand.New(rand.NewPCG(seed, 1))
		if _, err := db.Exec(`CREATE OR REPLACE TABLE spans (namespace VARCHAR, service VARCHAR, start_time TIMESTAMPTZ_NS, kind VARCHAR, status VARCHAR, duration_ms DOUBLE)`); err != nil {
			t.Fatal(err)
		}
		for range 400 + r.IntN(400) {
			var ns, svc any = names[r.IntN(len(names))], services[r.IntN(len(services))]
			if r.IntN(20) == 0 {
				ns = nil
			}
			if r.IntN(20) == 0 {
				svc = nil
			}
			at := time.Unix(int64(r.IntN(120*60)), int64(r.IntN(1e9))).UTC()
			kind := []any{"SPAN_KIND_CLIENT", "SPAN_KIND_SERVER", "SPAN_KIND_PRODUCER", nil}[r.IntN(4)]
			status := []any{"STATUS_CODE_OK", "STATUS_CODE_ERROR", nil}[r.IntN(3)]
			if _, err := db.Exec(`INSERT INTO spans VALUES (?,?,?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?,?,?)`, ns, svc, at, kind, status, float64(r.IntN(1000))/7); err != nil {
				t.Fatal(err)
			}
		}
		affected := fmt.Sprintf(`SELECT DISTINCT namespace, date_trunc('minute', start_time::TIMESTAMP_NS) AS bucket, service FROM spans
 WHERE namespace IS NOT NULL AND service IS NOT NULL AND hash(namespace, service, start_time, %d) %% 3 = 0
 UNION SELECT 'ns', TIMESTAMP '1970-01-01 00:03:00', 'nospans'`, seed)
		if _, err := db.Exec("CREATE OR REPLACE TEMP TABLE service_pass_affected AS " + affected); err != nil {
			t.Fatal(err)
		}
		lo, hi := time.Unix(0, 0).UTC(), time.Unix(120*60, 0).UTC()
		if _, err := db.Exec("DELETE FROM service_rollup; DELETE FROM span_rollup"); err != nil {
			t.Fatal(err)
		}
		if _, err := insertServiceRollup(t.Context(), db, lo, hi); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("CREATE OR REPLACE TABLE new_rollup AS SELECT * FROM service_rollup; DELETE FROM service_rollup"); err != nil {
			t.Fatal(err)
		}
		semi := strings.Replace(serviceRollupInsertSQL("true"), "  FROM spans s\n  WHERE true\n", `  FROM spans s
  SEMI JOIN service_pass_affected a
    ON a.namespace = s.namespace
   AND a.bucket = date_trunc('minute', s.start_time::TIMESTAMP_NS)
   AND a.service = s.service
  WHERE true
`, 1)
		if semi == serviceRollupInsertSQL("true") {
			t.Fatal("span filter text not found")
		}
		if _, err := db.Exec(semi, lo, hi, lo, hi, lo, hi); err != nil {
			t.Fatal(err)
		}
		var diff, n int
		if err := db.QueryRow(`SELECT (SELECT count(*) FROM ((SELECT * FROM new_rollup EXCEPT ALL SELECT * FROM service_rollup) UNION ALL (SELECT * FROM service_rollup EXCEPT ALL SELECT * FROM new_rollup))), (SELECT count(*) FROM new_rollup WHERE spans > 0)`).Scan(&diff, &n); err != nil {
			t.Fatal(err)
		}
		if diff != 0 || n == 0 {
			t.Fatalf("seed %d: %d differing rows of %d spanned", seed, diff, n)
		}
	}
}

// Compare complete edge rows against the previous constant-list child filter.
// A single thread makes approximate quantiles repeatable across both executions.
func TestEdgeRollupKeysMatchConstantList(t *testing.T) {
	db := openTestDuck(t)
	for _, q := range []string{"SET threads=1", createEdgeRollupTable} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	lo, hi := time.Unix(-3600, 0).UTC(), time.Unix(3600, 0).UTC()
	for seed := range 6 {
		for _, q := range []string{
			fmt.Sprintf(`CREATE OR REPLACE TABLE spans AS SELECT
 CASE WHEN i%%19=0 THEN NULL ELSE ['ns','other','o''k',''][1+(hash(i//4,%d)%%4)::INTEGER] END namespace,
 'trace-'||(i//4)::VARCHAR trace_id, 'span-'||i::VARCHAR span_id,
 CASE WHEN i%%4=0 THEN '' ELSE 'span-'||(i-1)::VARCHAR END parent_span_id,
 CASE WHEN i%%23=0 THEN NULL ELSE ['api','db','worker',''][1+(hash(i,7)%%4)::INTEGER] END service,
 make_timestamp_ns(((hash(i//4,3)%%7200)::BIGINT-3600)*1000000000+i%%4)::TIMESTAMPTZ_NS start_time,
 (hash(i,9)%%100)::DOUBLE duration_ms,
 CASE WHEN i%%7=0 THEN 'STATUS_CODE_ERROR' ELSE 'STATUS_CODE_OK' END status,
 'SPAN_KIND_SERVER' kind, NULL::VARCHAR peer_service, NULL::VARCHAR messaging_destination, NULL::VARCHAR messaging_system
 FROM range(800) t(i)`, seed),
			fmt.Sprintf(`CREATE OR REPLACE TEMP TABLE edge_pass_buckets AS SELECT DISTINCT namespace,date_trunc('minute',start_time::TIMESTAMP_NS)::TIMESTAMPTZ_NS bucket FROM spans WHERE hash(trace_id,%d)%%3=0`, seed),
			"DELETE FROM edge_rollup",
		} {
			if _, err := db.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
		tx, err := db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer tx.Rollback()
			if _, err := prepareEdgeParents(t.Context(), tx, lo, hi); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(edgeRollupInsertSQL, lo, hi); err != nil {
				t.Fatal(err)
			}
			rows, err := tx.Query(`SELECT namespace,epoch_ns(bucket) FROM edge_pass_buckets WHERE namespace IS NOT NULL`)
			if err != nil {
				t.Fatal(err)
			}
			var keys []string
			for rows.Next() {
				var ns string
				var bucket int64
				if err := rows.Scan(&ns, &bucket); err != nil {
					t.Fatal(err)
				}
				keys = append(keys, fmt.Sprintf("struct_pack(namespace := %s,bucket := make_timestamp_ns(%d))", sqlLiteral(ns), bucket))
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			for _, q := range []string{
				"CREATE OR REPLACE TEMP TABLE new_edges AS SELECT * FROM edge_rollup",
				"CREATE OR REPLACE TEMP TABLE new_children AS SELECT * FROM edge_pass_children",
				"DELETE FROM edge_rollup",
				`CREATE OR REPLACE TEMP TABLE edge_pass_children AS SELECT namespace,trace_id,parent_span_id,service,start_time,duration_ms,status FROM spans child WHERE service IS NOT NULL AND service!='' AND list_contains([` + strings.Join(keys, ",") + `]::STRUCT(namespace VARCHAR,bucket TIMESTAMP_NS)[],struct_pack(namespace := namespace,bucket := date_trunc('minute',start_time::TIMESTAMP_NS)))`,
			} {
				if _, err := tx.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := tx.Exec(edgeRollupInsertSQL, lo, hi); err != nil {
				t.Fatal(err)
			}
			for _, pair := range [][2]string{{"new_edges", "edge_rollup"}, {"new_children", "edge_pass_children"}} {
				var diff, count int
				q := fmt.Sprintf(`SELECT (SELECT count(*) FROM ((SELECT * FROM %s EXCEPT ALL SELECT * FROM %s) UNION ALL (SELECT * FROM %s EXCEPT ALL SELECT * FROM %s))),(SELECT count(*) FROM %s)`, pair[0], pair[1], pair[1], pair[0], pair[0])
				if err := tx.QueryRow(q).Scan(&diff, &count); err != nil || diff != 0 || count == 0 {
					t.Fatalf("seed %d %v: diff=%d rows=%d: %v", seed, pair, diff, count, err)
				}
			}
		}()
	}
}

// Unchunked reference for differential tests of the bounded implementation.
func serviceRollupInsertSQL(keys string) string {
	return serviceRollupSpanSQL(keys) + serviceRollupJoinSQL
}
