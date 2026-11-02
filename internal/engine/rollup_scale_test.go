package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type scaleCapture struct {
	rollupSQL
	t                   *testing.T
	prepare, plan       time.Duration
	maxArgs, statements int
}

func (p *scaleCapture) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if strings.Contains(q, "FROM spans") && !strings.Contains(q, "WHERE false") {
		start := time.Now()
		stmt, err := p.rollupSQL.(*sql.Tx).PrepareContext(ctx, q)
		if err != nil {
			return nil, err
		}
		stmt.Close()
		p.prepare += time.Since(start)
		start = time.Now()
		rows, err := p.rollupSQL.QueryContext(ctx, "EXPLAIN (FORMAT JSON) "+q, args...)
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
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		p.plan += time.Since(start)
		var tree any
		if err := json.Unmarshal([]byte(plan), &tree); err != nil {
			return nil, err
		}
		requireProbedSpans(p.t, tree)
		if !strings.Contains(plan, "MARK") {
			p.t.Fatal("missing mark join")
		}
		p.maxArgs = max(p.maxArgs, len(args))
		p.statements++
	}
	return p.rollupSQL.ExecContext(ctx, q, args...)
}

// Opt-in measurements run each case in a fresh process for peak RSS accounting.
// Build with scripts/with-duckdb.sh go test -c, then invoke the test binary with
// FANOUT_SCALE_KEYS=1000|10000|60000 and FANOUT_SCALE_KIND=service|edge.
// Prepare is unbound preparation; bound_explain includes binding, optimization
// and JSON plan serialization. Pass excludes those extra instrumentation calls.
// Peak RSS includes fixture setup and instrumentation; timings exclude setup.
func TestRollupKeyScaleMeasurement(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("FANOUT_SCALE_KEYS"))
	if n == 0 {
		t.Skip("set FANOUT_SCALE_KEYS")
	}
	kind := os.Getenv("FANOUT_SCALE_KIND")
	measureRollupKeyScale(t, n, kind, false)
}

func TestRollupManyKeysBoundedAndEquivalent(t *testing.T) {
	for _, kind := range []string{"service", "edge"} {
		t.Run(kind, func(t *testing.T) { measureRollupKeyScale(t, 10007, kind, true) })
	}
}

func measureRollupKeyScale(t *testing.T, n int, kind string, verify bool) {
	t.Helper()
	rows := 240000
	if verify {
		rows = 48000
	} // Keep the unbounded reference oracle small.
	db, _ := sparseServiceFixture(t, rows, 4000, 1000, fmt.Sprintf(`SELECT 'ns' namespace,make_timestamp_ns((i//1000)*60000000000) bucket,'svc-'||(i%%1000)::VARCHAR service FROM range(%d) t(i)`, n))
	if kind == "edge" {
		ids := "'trace' trace_id, 'parent' parent_span_id"
		if verify {
			ids = "'trace-'||service trace_id, CASE WHEN duration_ms=0 THEN '' ELSE 'parent' END parent_span_id"
		}
		for _, q := range []string{
			"CREATE OR REPLACE VIEW spans AS SELECT *, " + ids + " FROM span_parquet",
			fmt.Sprintf(`CREATE TEMP TABLE edge_pass_buckets AS SELECT CASE WHEN i%%1000=0 THEN 'ns' ELSE 'ns-'||(i%%1000)::VARCHAR END namespace,make_timestamp_ns((i//1000)*60000000000)::TIMESTAMPTZ_NS bucket FROM range(%d) t(i)`, n),
		} {
			if _, err := db.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
	}
	if verify {
		if _, err := db.Exec("SET threads=1"); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	p := &scaleCapture{rollupSQL: tx, t: t}
	lo, hi := time.Unix(0, 0).UTC(), time.Unix(3600, 0).UTC()
	start := time.Now()
	if kind == "edge" {
		err = createEdgeChildren(t.Context(), p, lo, hi)
	} else {
		_, err = insertServiceRollup(t.Context(), p, lo, hi)
	}
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
	}
	rss := usage.Maxrss
	if runtime.GOOS != "darwin" {
		rss *= 1024
	}
	t.Logf("peak_rss_bytes=%d", rss)
	t.Logf("kind=%s keys=%d prepare=%s bound_explain=%s pass=%s measured_total=%s max_args=%d statements=%d", kind, n, p.prepare, p.plan, elapsed-p.prepare-p.plan, elapsed, p.maxArgs, p.statements)
	if !verify {
		return
	}
	width := 3
	if kind == "edge" {
		width = 2
	}
	if p.maxArgs > width*affectedKeyChunkSize+2 || p.statements != (n+affectedKeyChunkSize-1)/affectedKeyChunkSize {
		t.Fatalf("unbounded pass: args=%d statements=%d", p.maxArgs, p.statements)
	}
	var reference string
	if kind == "edge" {
		reference = `SELECT child.namespace,child.trace_id,child.parent_span_id,child.service,child.start_time,child.duration_ms,child.status FROM spans child
 SEMI JOIN edge_pass_buckets a ON a.namespace=child.namespace AND a.bucket=date_trunc('minute',child.start_time::TIMESTAMP_NS)
 WHERE child.service IS NOT NULL AND child.service!='' AND child.start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND child.start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS`
		if _, err := tx.ExecContext(t.Context(), "CREATE TEMP TABLE expected AS "+reference, lo, hi); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err := tx.ExecContext(t.Context(), "CREATE TEMP TABLE actual AS SELECT * FROM service_rollup; DELETE FROM service_rollup"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), serviceRollupInsertSQL("true"), lo, hi, lo, hi, lo, hi); err != nil {
			t.Fatal(err)
		}
	}
	actual, expected := "actual", "service_rollup"
	if kind == "edge" {
		actual, expected = "edge_pass_children", "expected"
	}
	var diff, count int
	q := fmt.Sprintf(`SELECT (SELECT count(*) FROM ((SELECT * FROM %s EXCEPT ALL SELECT * FROM %s) UNION ALL (SELECT * FROM %s EXCEPT ALL SELECT * FROM %s))),(SELECT count(*) FROM %s)`, actual, expected, expected, actual, actual)
	if err := tx.QueryRowContext(t.Context(), q).Scan(&diff, &count); err != nil || diff != 0 || count == 0 {
		t.Fatalf("different output: diff=%d count=%d err=%v", diff, count, err)
	}
}
