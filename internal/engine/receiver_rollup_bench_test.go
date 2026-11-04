package engine

import (
	"database/sql"
	"fmt"
	_ "github.com/labstack/fanout/internal/duckdb"
	"testing"
	"time"
)

// Keep the number of resources fixed while increasing points per resource.
// This isolates minute inventory work from ingest and Parquet encoding.
func BenchmarkResourceMinuteSummary(b *testing.B) {
	for _, n := range []int{10000, 1000000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			db, err := sql.Open("duckdb", "")
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			for _, stmt := range []string{
				"SET threads=2", "SET TimeZone='UTC'", createResourceRollupTable,
				fmt.Sprintf(`CREATE TABLE metrics AS SELECT 'ns' namespace,'svc' service,
 make_timestamp_ns(1700000000000000000 + i*1000)::TIMESTAMPTZ_NS time,
 '1' _r_service_version,struct_pack("host.name":='host-'||(i%%10)::VARCHAR,"service.version":='1',"process.runtime.name":='go',"process.runtime.version":='1.26',"telemetry.sdk.language":='go',"telemetry.sdk.version":='1.0')::VARIANT resource
 FROM range(%d) t(i)`, n),
			} {
				if _, err := db.Exec(stmt); err != nil {
					b.Fatal(err)
				}
			}
			lo := time.Unix(1699999980, 0).UTC()
			hi := lo.Add(time.Minute)
			b.ResetTimer()
			for b.Loop() {
				if _, err := db.Exec("DELETE FROM resource_rollup"); err != nil {
					b.Fatal(err)
				}
				if _, err := db.Exec(resourceRollupInsertSQL("metrics", "time", "true"), lo, hi); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
