package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServiceMetricRollupAggregatesBeforeJoiningAffectedBuckets(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	schema, data := filepath.Join(dir, "schema.parquet"), filepath.Join(dir, "data.parquet")
	// Like the production schema-first snapshot, the first file is empty. DuckDB
	// estimates this scan at zero rows and can choose it as a hash build side.
	for _, q := range []string{
		"SET memory_limit='128MB'", "SET threads=4",
		`CREATE VIEW metric_fixture AS SELECT
    CASE WHEN i%5=0 THEN 'other' ELSE 'ns' END AS namespace,
    CASE WHEN i%7=0 THEN 'unaffected' ELSE 'api' END AS service,
    CASE WHEN i%11=0 THEN NULL ELSE 'metric-'||(i%4)::VARCHAR END AS name,
    make_timestamp_ns(-120000000000+(i%3)*60000000000)::TIMESTAMPTZ_NS AS time
    FROM range(1000000) t(i)`,
		"COPY (SELECT * FROM metric_fixture WHERE false) TO " + sqlLiteral(schema) + " (FORMAT PARQUET)",
		"COPY metric_fixture TO " + sqlLiteral(data) + " (FORMAT PARQUET)",
		"CREATE OR REPLACE VIEW metrics AS SELECT * FROM read_parquet([" + sqlLiteral(schema) + "," + sqlLiteral(data) + "],union_by_name=false,hive_partitioning=false)",
		`CREATE TEMP TABLE service_pass_affected AS SELECT 'ns' AS namespace, TIMESTAMP '1969-12-31 23:58:00' AS bucket, 'api' AS service
    UNION ALL SELECT 'ns',TIMESTAMP '1970-01-01 00:00:00','api'
    UNION ALL SELECT 'ns',TIMESTAMP '1969-12-31 23:58:00','missing'`,
		"SET enable_profiling='json'", "SET profiling_output=" + sqlLiteral(filepath.Join(dir, "profile.json")),
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	lo, hi := time.Unix(-120, 0).UTC(), time.Unix(60, 0).UTC()
	if _, err := insertServiceRollup(t.Context(), db, lo, hi); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	var profile struct {
		System struct {
			PeakBufferMemory int64 `json:"peak_buffer_memory"`
		} `json:"system"`
	}
	if err := json.Unmarshal(raw, &profile); err != nil {
		t.Fatal(err)
	}
	// One million repeated points describe only two affected service buckets. A hash build
	// of the raw points used about 90 MiB; the distinct-name aggregation needs
	// under 10 MiB. Keep this independent of host RSS and allocator reuse.
	t.Logf("peak DuckDB buffer memory: %.1f MiB", float64(profile.System.PeakBufferMemory)/(1<<20))
	if peak := profile.System.PeakBufferMemory; peak == 0 || peak > 32<<20 {
		t.Errorf("metric rollup peak buffer memory = %d, want 1..32 MiB", peak)
	}
	rows, err := db.Query("SELECT epoch_ns(bucket),service,metric_count FROM service_rollup ORDER BY bucket,service")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var bucket, count int64
		var service string
		if err := rows.Scan(&bucket, &service, &count); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%d/%s/%d", bucket, service, count))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := "[-120000000000/api/4 -120000000000/missing/0 0/api/4]"
	if fmt.Sprint(got) != want {
		t.Fatalf("rollups = %v, want %s", got, want)
	}
}
