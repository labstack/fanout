package query

import (
	"testing"
	"time"
)

func TestEdgePassBucketsPreserveRepairCounts(t *testing.T) {
	db := openTestDuck(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`CREATE TABLE spans AS
 SELECT namespace,trace_id,span_id,parent_span_id,start_time::TIMESTAMPTZ_NS AS start_time,
        kind,messaging_destination,messaging_system,ingested_unix_nano
 FROM (VALUES
 ('ns','trace','child','parent',?::TIMESTAMP_NS,'SPAN_KIND_CONSUMER','jobs','kafka',100),
 ('ns','trace','parent','',?::TIMESTAMP_NS,'SPAN_KIND_PRODUCER','jobs','kafka',101),
 ('ns','other','other','',?::TIMESTAMP_NS,'SPAN_KIND_SERVER',NULL,NULL,101)
 ) s(namespace,trace_id,span_id,parent_span_id,start_time,kind,messaging_destination,messaging_system,ingested_unix_nano)`,
		at.Add(time.Nanosecond), at.Add(2*time.Nanosecond), at.Add(time.Minute+3*time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), createEdgePassBucketsSQL, 100, 101); err != nil {
		t.Fatal(err)
	}
	// The recent child matches both repairs. Round 5 counted it twice in
	// adaptive probes; the bucket counts must retain that multiplicity.
	var buckets, spans int64
	if err := tx.QueryRow("SELECT count(*), sum(spans)::BIGINT FROM edge_pass_buckets").Scan(&buckets, &spans); err != nil || buckets != 2 || spans != 4 {
		t.Fatalf("buckets=%d spans=%d err=%v", buckets, spans, err)
	}
	// Later probes consume only the materialized relation, even without source.
	if _, err := tx.Exec("DROP TABLE spans"); err != nil {
		t.Fatal(err)
	}
	hi, err := edgeSubWindowEnd(t.Context(), tx, at, at.Add(time.Minute), 3)
	if err != nil || !hi.Equal(at.Add(time.Minute)) {
		t.Fatalf("sub-window end=%s err=%v", hi, err)
	}
}

func TestEdgePassRelationIsPrivateAndRollsBack(t *testing.T) {
	db := openTestDuck(t)
	db.SetMaxOpenConns(2)
	if err := CreateTables(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), createEdgePassBucketsSQL, 0, 1); err != nil {
		t.Fatal(err)
	}
	// Another connection shares the durable schema, but cannot see pass state.
	var count int
	if err := db.QueryRow("SELECT count(*) FROM spans").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM edge_pass_buckets").Scan(&count); err == nil {
		t.Fatal("pass relation visible outside its connection")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT count(*) FROM edge_pass_buckets").Scan(&count); err == nil {
		t.Fatal("rollback retained pass relation on its connection")
	}
}

func TestEdgePassDropsRelationOnEveryExit(t *testing.T) {
	for _, empty := range []bool{false, true} {
		name := "resume and retry"
		if empty {
			name = "empty window"
		}
		t.Run(name, func(t *testing.T) {
			db := openTestDuck(t)
			if err := CreateTables(db); err != nil {
				t.Fatal(err)
			}
			if err := CreateViews(db); err != nil {
				t.Fatal(err)
			}
			at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
			stamp := at.UnixNano()
			if !empty {
				if _, err := db.Exec(`INSERT INTO telemetry.spans
 (namespace,trace_id,span_id,service,start_time,start_unix_nano,ingested_at,ingested_unix_nano)
 VALUES ('ns','a','a','api',?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?,?,?),
        ('ns','b','b','api',?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?,?,?)`,
					at, at.UnixNano(), at, stamp,
					at.Add(31*time.Minute), at.Add(31*time.Minute).UnixNano(), at, stamp); err != nil {
					t.Fatal(err)
				}
			}
			d := &Duck{DB: db, writeDB: db, publicationBounds: func() (int64, int64, int64) { return stamp, stamp, stamp }}
			checkAbsent := func() {
				var count int
				if err := db.QueryRow("SELECT count(*) FROM duckdb_tables() WHERE table_name='edge_pass_buckets'").Scan(&count); err != nil || count != 0 {
					t.Fatalf("pass relation survived: count=%d err=%v", count, err)
				}
			}
			if !empty {
				// Fail after materialization, then retry on the same SQL connection.
				if _, err := db.Exec("ALTER TABLE edge_rollup RENAME TO saved_edge_rollup"); err != nil {
					t.Fatal(err)
				}
				if _, _, err := d.refreshEdgeRollupPass(t.Context()); err == nil {
					t.Fatal("missing rollup table did not fail the pass")
				}
				checkAbsent()
				if watermark, cursor := readEdgeRollupState(t, d); watermark != 0 || cursor != 0 {
					t.Fatalf("failed pass advanced state: watermark=%d cursor=%d", watermark, cursor)
				}
				if _, err := db.Exec("ALTER TABLE saved_edge_rollup RENAME TO edge_rollup"); err != nil {
					t.Fatal(err)
				}
			}
			if _, pending, err := d.refreshEdgeRollupPass(t.Context()); err != nil || pending == empty {
				t.Fatalf("first pass pending=%v err=%v", pending, err)
			}
			checkAbsent()
			if !empty {
				if _, pending, err := d.refreshEdgeRollupPass(t.Context()); err != nil || pending {
					t.Fatalf("resumed pass pending=%v err=%v", pending, err)
				}
				checkAbsent()
			}
			if watermark, cursor := readEdgeRollupState(t, d); watermark != stamp || cursor != 0 {
				t.Fatalf("completed state: watermark=%d cursor=%d", watermark, cursor)
			}
		})
	}
}
