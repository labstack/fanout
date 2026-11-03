package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// Run each memory case in a fresh process: allocator reuse and other tests must
// not hide the native t-digest heap, which memory_limit does not govern.
func rollupMemoryProcess(t *testing.T) bool {
	t.Helper()
	if os.Getenv("FANOUT_ROLLUP_MEMORY_CHILD") == t.Name() {
		return true
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v", "-test.timeout=90s")
	cmd.Env = append(os.Environ(), "FANOUT_ROLLUP_MEMORY_CHILD="+t.Name())
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if cmd.ProcessState != nil {
		usage := cmd.ProcessState.SysUsage().(*syscall.Rusage)
		peak := usage.Maxrss
		if runtime.GOOS != "darwin" {
			peak *= 1024
		}
		t.Logf("process peak RSS: %.1f MiB", float64(peak)/(1<<20))
	}
	if err != nil {
		t.Fatalf("memory child: %v", err)
	}
	return false
}

func peakRSS(t *testing.T) int64 {
	t.Helper()
	var r syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &r); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "darwin" {
		r.Maxrss *= 1024
	}
	return r.Maxrss
}

func TestServiceLateSpanQuantileMemory(t *testing.T) {
	serviceSpanRollupMemory(t, true)
}
func TestServiceActiveSpanRollupMemory(t *testing.T) {
	serviceSpanRollupMemory(t, false)
}

func serviceSpanRollupMemory(t *testing.T, late bool) {
	if !rollupMemoryProcess(t) {
		return
	}
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	schema, data := filepath.Join(dir, "schema.parquet"), filepath.Join(dir, "data.parquet")

	rows, perMinute, minutes, services := 552960, 384, 1440, 24
	affected := `SELECT 'ns' AS namespace,TIMESTAMP '1970-01-01' AS bucket,'svc-0' AS service
    UNION ALL SELECT 'ns',TIMESTAMP '1970-01-01 23:59:00','svc-0'`
	wantGroups, wantSpans := 2, 32
	if !late {
		rows, perMinute, minutes, services = 1000800, 33360, 30, 12
		affected = `SELECT 'ns' AS namespace,TIMESTAMP '1970-01-01 00:29:00' AS bucket,'svc-'||i::VARCHAR AS service FROM range(12) t(i)`
		wantGroups, wantSpans = 12, 33360
	}
	for _, q := range []string{
		"SET memory_limit='1GB'", "SET threads=4",
		fmt.Sprintf(`CREATE VIEW span_fixture AS SELECT 'ns' AS namespace,'svc-'||(i%%%d)::VARCHAR AS service,
    make_timestamp_ns((i//%d)*60000000000)::TIMESTAMPTZ_NS AS start_time,
    'SPAN_KIND_SERVER' AS kind,'STATUS_CODE_OK' AS status,5.0::DOUBLE AS duration_ms
    FROM range(%d) t(i)`, services, perMinute, rows),
		"COPY (SELECT * FROM span_fixture WHERE false) TO " + sqlLiteral(schema) + " (FORMAT PARQUET)",
		"COPY span_fixture TO " + sqlLiteral(data) + " (FORMAT PARQUET)",
		"CREATE OR REPLACE VIEW spans AS SELECT * FROM read_parquet([" + sqlLiteral(schema) + "," + sqlLiteral(data) + "],union_by_name=false,hive_partitioning=false)",
		"CREATE TEMP TABLE service_pass_affected AS " + affected,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	before := peakRSS(t)
	started := time.Now()
	lo, hi := time.Unix(0, 0).UTC(), time.Unix(int64(minutes*60), 0).UTC()
	if !late {
		lo = hi.Add(-time.Minute)
	}
	if _, err := insertServiceRollup(t.Context(), db, lo, hi); err != nil {
		t.Fatal(err)
	}
	growth := peakRSS(t) - before
	t.Logf("service rollup (late=%v): %s, peak RSS growth %.1f MiB", late, time.Since(started), float64(growth)/(1<<20))
	if growth > 96<<20 {
		t.Errorf("quantiles for %d affected keys grew RSS by %d, want <=96 MiB", wantGroups, growth)
	}
	var count, spans, served int
	var p50, p95, errorRate float64
	if err := db.QueryRow("SELECT count(*),sum(spans),sum(served_spans),sum(p50_ms),sum(p95_ms),sum(error_rate) FROM service_rollup").Scan(&count, &spans, &served, &p50, &p95, &errorRate); err != nil {
		t.Fatal(err)
	}
	var histogramSpans int
	if err := db.QueryRow("SELECT sum(list_sum(map_values(hist))) FROM span_rollup").Scan(&histogramSpans); err != nil || histogramSpans != wantSpans {
		t.Fatalf("histogram spans=%d want=%d: %v", histogramSpans, wantSpans, err)
	}
	if count != wantGroups || spans != wantSpans || served != wantSpans || p50 != float64(wantGroups*5) || p95 != float64(wantGroups*5) || errorRate != 0 {
		t.Fatalf("rollup=%d %d %d %g %g %g", count, spans, served, p50, p95, errorRate)
	}
}

func TestEdgeSchemaFirstRollupMemory(t *testing.T) {
	edgeSpanRollupMemory(t, false)
}
func TestEdgeLateSpanRollupMemory(t *testing.T) {
	edgeSpanRollupMemory(t, true)
}

func edgeSpanRollupMemory(t *testing.T, late bool) {
	if !rollupMemoryProcess(t) {
		return
	}
	db := openTestDuck(t)
	if _, err := db.Exec(createEdgeRollupTable); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	schema, data := filepath.Join(dir, "schema.parquet"), filepath.Join(dir, "data.parquet")

	eventTime := "0"
	bucketSQL := `SELECT 'ns' AS namespace,make_timestamp_ns(0)::TIMESTAMPTZ_NS AS bucket`
	seconds := int64(60)
	if late {
		eventTime = "(i//50%1440)*60000000000"
		bucketSQL += ` UNION ALL SELECT 'ns',make_timestamp_ns(86340000000000)::TIMESTAMPTZ_NS`
		seconds = 86400
	}
	for _, q := range []string{
		"SET memory_limit='256MB'", "SET threads=4",
		fmt.Sprintf(`CREATE VIEW span_fixture AS SELECT 'ns' AS namespace,'trace-'||(i//50)::VARCHAR AS trace_id,
    CASE WHEN i%%50=0 THEN 'root' ELSE 'child-'||i::VARCHAR END AS span_id,
    CASE WHEN i%%50=0 THEN '' ELSE 'root' END AS parent_span_id,
    CASE WHEN i%%50=0 THEN 'api' WHEN i%%3=0 THEN 'api' WHEN i%%3=1 THEN 'producer' ELSE 'consumer' END AS service,
    CASE WHEN i%%50=0 THEN 'SPAN_KIND_SERVER' WHEN i%%3=0 THEN 'SPAN_KIND_SERVER' WHEN i%%3=1 THEN 'SPAN_KIND_PRODUCER' ELSE 'SPAN_KIND_CONSUMER' END AS kind,
    'STATUS_CODE_OK' AS status,5.0::DOUBLE AS duration_ms,
    make_timestamp_ns(%s)::TIMESTAMPTZ_NS AS start_time,
    NULL::VARCHAR AS peer_service,'jobs' AS messaging_destination,'kafka' AS messaging_system FROM range(1000000) t(i)`, eventTime),
		"COPY (SELECT * FROM span_fixture WHERE false) TO " + sqlLiteral(schema) + " (FORMAT PARQUET)",
		"COPY span_fixture TO " + sqlLiteral(data) + " (FORMAT PARQUET)",
		"CREATE VIEW spans AS SELECT * FROM read_parquet([" + sqlLiteral(schema) + "," + sqlLiteral(data) + "],union_by_name=false,hive_partitioning=false)",
		"CREATE TEMP TABLE edge_pass_buckets AS " + bucketSQL,
		`CREATE TEMP TABLE edge_pass_parents AS SELECT namespace,trace_id,span_id,service FROM span_fixture WHERE parent_span_id=''`,
		"SET enable_profiling='json'", "SET profiling_output=" + sqlLiteral(filepath.Join(dir, "profile.json")),
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	lo, hi := time.Unix(0, 0).UTC(), time.Unix(seconds, 0).UTC()

	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	before := peakRSS(t)
	started := time.Now()
	// The empty statement profiles the production child materialization helper.
	statements := []string{"", createEdgeUnmatchedSQL, edgeRollupInsertSQL}
	for _, q := range statements {
		if q == "" {
			if err := createEdgeChildren(t.Context(), tx, lo, hi); err != nil {
				t.Fatal(err)
			}
		} else if _, err := tx.Exec(q, lo, hi); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "profile.json"))
		if err != nil {
			t.Fatal(err)
		}
		var p struct {
			System struct {
				Peak int64 `json:"peak_buffer_memory"`
			} `json:"system"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		t.Logf("peak buffer memory: %.1f MiB", float64(p.System.Peak)/(1<<20))
		limit := int64(192 << 20)
		if late {
			limit = 128 << 20
			if q == createEdgeUnmatchedSQL {
				limit = 32 << 20
			}
		}
		if p.System.Peak == 0 || p.System.Peak > limit {
			t.Errorf("edge raw hash build: peak=%d, want 1..%d bytes", p.System.Peak, limit)
		}
	}
	growth := peakRSS(t) - before
	t.Logf("edge rollup (late=%v): %s, peak RSS growth %.1f MiB", late, time.Since(started), float64(growth)/(1<<20))
	if growth > 160<<20 {
		t.Errorf("edge rollup grew RSS by %d, want <=160 MiB", growth)
	}

	// The fixture has one api root per trace, plus producer/consumer children.
	// Build an independent exact oracle directly from its generated rows.
	var differences int
	oracle := `WITH counts AS (
 SELECT namespace,date_trunc('minute',start_time::TIMESTAMP_NS) bucket,service,count(*) calls
 FROM span_fixture WHERE service!='api' AND date_trunc('minute',start_time::TIMESTAMP_NS) IN (SELECT bucket FROM edge_pass_buckets)
 GROUP BY namespace,bucket,service
 ), expected AS (
 SELECT namespace,bucket,'api' caller,service callee,calls,5.0 avg_ms,0.0 error_rate,'call' edge_type,0.0 p50_ms,0.0 p95_ms FROM counts
 UNION ALL SELECT namespace,bucket,'producer','consumer',calls,0.0,0.0,'messaging',0.0,0.0 FROM counts WHERE service='consumer'
 ), differences AS (
 (SELECT * FROM expected EXCEPT ALL SELECT * FROM edge_rollup)
 UNION ALL (SELECT * FROM edge_rollup EXCEPT ALL SELECT * FROM expected)
 ) SELECT count(*) FROM differences`
	if err := tx.QueryRow(oracle).Scan(&differences); err != nil || differences != 0 {
		t.Fatalf("edge differences=%d err=%v", differences, err)
	}
	var unmatched int
	if err := tx.QueryRow("SELECT count(*) FROM edge_pass_unmatched").Scan(&unmatched); err != nil || unmatched != 0 {
		t.Fatalf("unmatched=%d err=%v", unmatched, err)
	}
}
