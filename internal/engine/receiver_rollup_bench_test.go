package engine

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	_ "github.com/labstack/fanout/internal/duckdb"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
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

// Measure the completed-batch cache without timing OTLP decode or publication.
func BenchmarkMetricReadCacheResources(b *testing.B) {
	cfg := config.Config{DataDir: b.TempDir(), DuckDBMemory: "1GB", DuckDBThreads: 2, DuckDBMaxConns: 2, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		b.Fatal(err)
	}
	defer repo.Close()
	d, err := NewDuck(b.Context(), cfg, repo)
	if err != nil {
		b.Fatal(err)
	}
	defer d.Close()
	at := time.Now().UTC().Truncate(time.Minute)
	resources := make([]telemetry.Metric, 10)
	for i := range resources {
		m := telemetry.Metric{Namespace: "ns", ServiceName: "svc", Name: "gen", Type: "gauge", Resource: map[string]any{"host.name": fmt.Sprint("host-", i), "service.version": "1", "telemetry.sdk.language": "go"}}
		m.SeriesID = telemetry.MetricSeriesID(m)
		resources[i] = m
	}
	points := make([]telemetry.Metric, 1000000)
	for i := range points {
		points[i] = resources[i%len(resources)]
		points[i].TimeUnixNanos = at.Add(time.Duration(i) * time.Microsecond).UnixNano()
		points[i].Value = float64(i)
	}
	if err := repo.Commit(b.Context(), telemetrystore.Batch{ID: "metrics", Metrics: points}); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		for _, table := range []string{"read_batches", "read_resource_inventory", "read_resource_seconds", "read_metric_names"} {
			if _, err := d.writer().ExecContext(b.Context(), "DELETE FROM "+table); err != nil {
				b.Fatal(err)
			}
		}
		if _, err := d.RefreshReadCaches(b.Context()); err != nil {
			b.Fatal(err)
		}
	}
}
