package engine

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEdgeResumeSetupIsLinearAcrossRestart(t *testing.T) {
	for _, minutes := range []int{5, 15, 30} {
		t.Run(fmt.Sprintf("%d_minutes", minutes), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.duckdb")
			var db *sql.DB
			open := func() {
				var err error
				db, err = sql.Open("duckdb", path)
				if err != nil {
					t.Fatal(err)
				}
				db.SetMaxOpenConns(1)
				if _, err := db.Exec("SET TimeZone='UTC'"); err != nil {
					t.Fatal(err)
				}
				if err := CreateTables(db); err != nil {
					t.Fatal(err)
				}
			}
			open()
			defer func() { _ = db.Close() }()
			base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
			stamp := base.UnixNano()
			// N occupied event minutes, separated to force N resumable passes
			// without needing 250k spans per minute in the regular test suite.
			if _, err := db.Exec(`INSERT INTO telemetry.spans
 (namespace,trace_id,span_id,parent_span_id,service,kind,start_time,duration_ms,status,ingested_unix_nano)
 SELECT 'ns',(i//2)::VARCHAR,i::VARCHAR,CASE WHEN i%2=1 THEN (i-1)::VARCHAR END,
 CASE WHEN i%2=0 THEN 'gateway' ELSE 'checkout' END,'SPAN_KIND_SERVER',
 ?::TIMESTAMP_NS + (i//2)*INTERVAL 31 MINUTE,5,'OK',? FROM range(?) t(i)`, base, stamp, minutes*2); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("CREATE SEQUENCE edge_setup_reads"); err != nil {
				t.Fatal(err)
			}
			// Only discovery uses ingestion stamps. Count actual source-row
			// evaluations there, rather than timing the drain or inspecting SQL.
			view := strings.Replace(viewSpans, "  ingested_unix_nano,", "  CASE WHEN nextval('edge_setup_reads')>0 THEN ingested_unix_nano END AS ingested_unix_nano,", 1)
			if _, err := db.Exec(view); err != nil {
				t.Fatal(err)
			}
			var setupReads int64
			for pass := 0; pass < minutes; pass++ {
				d := &Duck{DB: db, writeDB: db, publicationBounds: func() (int64, int64, int64) { return stamp, stamp, stamp }}
				_, pending, err := d.refreshEdgeRollupPass(t.Context())
				if err != nil || pending != (pass < minutes-1) {
					t.Fatalf("pass=%d pending=%v err=%v", pass, pending, err)
				}
				var reads int64
				if err := db.QueryRow("SELECT last_value FROM duckdb_sequences() WHERE sequence_name='edge_setup_reads'").Scan(&reads); err != nil {
					t.Fatal(err)
				}
				if pass == 0 {
					setupReads = reads
					if reads < int64(minutes*2) || reads > int64(minutes*8) {
						t.Fatalf("setup reads=%d, want a constant number per input row", reads)
					}
				} else if reads != setupReads {
					t.Fatalf("resume repeated discovery: reads=%d first pass=%d", reads, setupReads)
				}
				var remaining int
				if err := db.QueryRow("SELECT count(*) FROM read_edge_buckets").Scan(&remaining); err != nil || remaining != minutes-pass-1 {
					t.Fatalf("remaining=%d pass=%d err=%v", remaining, pass, err)
				}
				if pass == 0 {
					// Close the native database: summaries and cursor must survive,
					// while the connection-local pass view must have disappeared.
					if err := db.Close(); err != nil {
						t.Fatal(err)
					}
					open()
				}
			}
			var calls, buckets int
			if err := db.QueryRow("SELECT sum(calls),count(DISTINCT bucket) FROM edge_rollup").Scan(&calls, &buckets); err != nil || calls != minutes || buckets != minutes {
				t.Fatalf("calls=%d buckets=%d err=%v", calls, buckets, err)
			}
			t.Logf("occupied_minutes=%d setup_row_evaluations=%d passes=%d", minutes, setupReads, minutes)
		})
	}
}

func TestEdgeResumeKeepsFrozenWindowAndRepairsNewArrivals(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	stamp := base.Add(time.Hour).UnixNano()
	horizon := stamp
	d := &Duck{DB: db, writeDB: db, publicationBounds: func() (int64, int64, int64) { return horizon, horizon, stamp }}
	for _, span := range []rollupTestSpan{
		{namespace: "ns", traceID: "late", spanID: "child", parentSpanID: "parent", service: "late-child", start: base, ingested: stamp},
		{namespace: "ns", traceID: "existing", spanID: "parent", service: "gateway", start: base, ingested: stamp},
		{namespace: "ns", traceID: "existing", spanID: "child", parentSpanID: "parent", service: "existing-child", start: base, ingested: stamp},
		{namespace: "ns", traceID: "tail", spanID: "parent", service: "gateway", start: base.Add(31 * time.Minute), ingested: stamp},
		{namespace: "ns", traceID: "tail", spanID: "child", parentSpanID: "parent", service: "tail-child", start: base.Add(31 * time.Minute), ingested: stamp},
	} {
		insertRollupTestSpan(t, db, span)
	}
	if _, pending, err := d.refreshEdgeRollupPass(t.Context()); err != nil || !pending {
		t.Fatalf("initial pass pending=%v err=%v", pending, err)
	}
	// Publish a parent for an already-completed event bucket while the old
	// ingestion window is paused. It belongs to the NEXT discovery window.
	horizon++
	insertRollupTestSpan(t, db, rollupTestSpan{namespace: "ns", traceID: "late", spanID: "parent", service: "late-gateway", start: base, ingested: horizon})
	if _, pending, err := d.refreshEdgeRollupPass(t.Context()); err != nil || !pending {
		t.Fatalf("resumed pass pending=%v err=%v", pending, err)
	}
	if watermark, cursor := readEdgeRollupState(t, d); watermark != stamp || cursor != 0 {
		t.Fatalf("resume changed frozen ingestion end: watermark=%d cursor=%d", watermark, cursor)
	}
	requireRowCount(t, db, "SELECT count(*) FROM edge_rollup WHERE callee=?", "late-child", 0)
	if _, pending, err := d.refreshEdgeRollupPass(t.Context()); err != nil || pending {
		t.Fatalf("repair pass pending=%v err=%v", pending, err)
	}
	// Repair replaces the complete bucket: both the new relationship and the
	// unrelated edge that was already aggregated must appear exactly once.
	for _, child := range []string{"late-child", "existing-child", "tail-child"} {
		requireRowCount(t, db, "SELECT count(*) FROM edge_rollup WHERE calls=1 AND callee=?", child, 1)
	}
	if watermark, cursor := readEdgeRollupState(t, d); watermark != horizon || cursor != 0 {
		t.Fatalf("repair did not reach publication horizon: watermark=%d cursor=%d", watermark, cursor)
	}
}
