package engine

import (
	"context"
	"fmt"
	"time"
)

func logRollupInsertSQL(keys string) string {
	return `INSERT INTO log_rollup
 SELECT l.namespace,date_trunc('minute',l.time::TIMESTAMP_NS),l.service,coalesce(l.severity,''),count(*)
 FROM logs l WHERE ` + keys + `
 AND l.time >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND l.time < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS
 GROUP BY ALL`
}

// Rank route populations before building histograms, then fold the rest before
// aggregation. The overflow row keeps every eligible span, including its errors
// and duration distribution; retained routes can vary between minutes. Queries
// selecting individual routes therefore stay raw.
func endpointRollupInsertSQL(keys string) string {
	return fmt.Sprintf(`INSERT INTO endpoint_rollup
 WITH eligible AS MATERIALIZED (
 SELECT s.namespace,date_trunc('minute',s.start_time::TIMESTAMP_NS) bucket,s.service,
 coalesce(s.kind,'') kind,coalesce(s.status,'') status,
 try_cast(s.attributes['http.route'] AS VARCHAR) route,s.duration_ms
 FROM spans s WHERE %s
 AND s.start_time >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND s.start_time < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS
 AND try_cast(s.attributes['http.route'] AS VARCHAR) <> ''),
 routes AS (SELECT namespace,bucket,service,route,
 row_number() OVER (PARTITION BY namespace,bucket,service ORDER BY count(*) DESC,route) r
 FROM eligible GROUP BY namespace,bucket,service,route),
 folded AS (SELECT e.* EXCLUDE(route),CASE WHEN r.r<=%d THEN e.route ELSE 'Other routes (overflow)' END route,r.r>%d overflow
 FROM eligible e JOIN routes r USING(namespace,bucket,service,route))
 SELECT namespace,bucket,service,kind,status,route,overflow,count(*),sum(duration_ms),min(duration_ms),max(duration_ms),
 histogram(CASE WHEN duration_ms>0 THEN coalesce(try_cast(ceil(log2(duration_ms)*8) AS INTEGER),2147483647) ELSE %d END)
 FROM folded GROUP BY namespace,bucket,service,kind,status,route,overflow`, keys, endpointRouteLimit, endpointRouteLimit, spanHistogramZero)
}

func insertDashboardRollups(ctx context.Context, tx rollupSQL, lo, hi time.Time) error {
	for _, signal := range []string{"spans", "logs"} {
		alias, clock := "s", "start_time"
		if signal == "logs" {
			alias, clock = "l", "time"
		}
		err := forAffectedKeys(ctx, tx,
			`SELECT DISTINCT namespace,epoch_ns(bucket),service FROM service_pass_affected WHERE namespace IS NOT NULL AND bucket IS NOT NULL AND service IS NOT NULL`, nil,
			fmt.Sprintf(`struct_pack(namespace := %[1]s.namespace,bucket := date_trunc('minute',%[1]s.%[2]s::TIMESTAMP_NS),service := %[1]s.service)`, alias, clock),
			`struct_pack(namespace := ?::VARCHAR,bucket := make_timestamp_ns(?::BIGINT),service := ?::VARCHAR)`,
			func(keys string, args []any) error {
				query := endpointRollupInsertSQL(keys)
				if signal == "logs" {
					query = logRollupInsertSQL(keys)
				}
				_, err := tx.ExecContext(ctx, query, append(args, lo, hi)...)
				return err
			})
		if err != nil {
			return err
		}
	}
	return nil
}
