package query

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/env"
)

// TestHourPartitionPrunesRecentWindow is the within-day-pruning gate experiment
// (design 2026-06-19). It writes a multi-hour span dataset into the real
// hour-partitioned lake, then proves a recent-window query
// (start_time >= now() - 15min) scans only ~the current hour's rows instead of
// the whole dataset — the failure that made the rollup hit 35s as a UTC day
// filled. Run with: go test ./internal/query/ -run HourPartitionPrunes -v
func TestHourPartitionPrunesRecentWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d, err := NewDuck(ctx, env.Config{DataDir: t.TempDir(), DuckDBMemory: "512MB"})
	if err != nil {
		if strings.Contains(err.Error(), "ducklake") || strings.Contains(err.Error(), "ATTACH") {
			t.Skipf("DuckLake unavailable: %v", err)
		}
		t.Fatalf("NewDuck: %v", err)
	}
	defer d.Close()

	// Three hours of data, 200k rows each (600k total), each hour in its own
	// hour-partition by start_time. ingested_unix_nano is "now" for all (as if
	// just ingested) so it can't help pruning — only start_time partitioning can.
	const perHour = 200_000
	for _, agoMin := range []int{150, 90, 5} { // 2.5h ago, 1.5h ago, 5min ago
		_, err := d.DB.ExecContext(ctx, `
INSERT INTO lake.spans (namespace, service, trace_id, span_id, start_time, status, status_message, ingested_unix_nano)
SELECT 'default', 'svc-' || (i % 50), md5(i::VARCHAR), md5((i+1)::VARCHAR),
       now() - INTERVAL `+strconv.Itoa(agoMin)+` MINUTE + (i % 60) * INTERVAL 1 SECOND,
       CASE WHEN i % 20 = 0 THEN 'STATUS_CODE_ERROR' ELSE 'STATUS_CODE_OK' END,
       'err', epoch_ns(now())
FROM range(`+strconv.Itoa(perHour)+`) t(i)`)
		if err != nil {
			t.Fatalf("insert hour -%dmin: %v", agoMin, err)
		}
	}
	if _, err := d.DB.ExecContext(ctx, "CALL ducklake_merge_adjacent_files('lake')"); err != nil {
		t.Fatalf("merge: %v", err)
	}

	var total int64
	if err := d.DB.QueryRowContext(ctx, "SELECT count(*) FROM spans").Scan(&total); err != nil {
		t.Fatalf("count total: %v", err)
	}

	var fileCount int64
	_ = d.DB.QueryRowContext(ctx,
		`SELECT file_count FROM ducklake_table_info('lake') WHERE table_name = 'spans'`).Scan(&fileCount)

	// EXPLAIN ANALYZE the recent-window query (same shape as overviewRecentErrors).
	plan := explainAnalyze(t, ctx, d, `
SELECT service, count(*) FROM spans
WHERE start_time >= now() - INTERVAL 15 MINUTE
  AND status = 'STATUS_CODE_ERROR'
GROUP BY service`)
	scanned := maxScanCardinality(plan)

	t.Logf("── within-day pruning experiment (hour-partitioned) ──")
	t.Logf("total rows           : %d  (across 3 hour-partitions)", total)
	t.Logf("parquet files (merged): %d", fileCount)
	t.Logf("recent-15min query scanned: %d rows", scanned)
	t.Logf("→ pruned to %.1f%% of the dataset (lower is better; ~1/3 = one hour)", 100*float64(scanned)/float64(total))

	// The recent-window query must scan well under half the dataset — i.e. it
	// pruned to ~the current hour, not all three. (No pruning would scan ~total.)
	if scanned >= total/2 {
		t.Fatalf("recent-window query scanned %d of %d rows — within-day pruning is NOT working", scanned, total)
	}
}

func explainAnalyze(t *testing.T, ctx context.Context, d *Duck, q string) string {
	t.Helper()
	rows, err := d.DB.QueryContext(ctx, "EXPLAIN ANALYZE "+q)
	if err != nil {
		t.Fatalf("explain analyze: %v", err)
	}
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		sb.WriteString(v)
		sb.WriteString("\n")
	}
	return sb.String()
}

// maxScanCardinality pulls the largest actual-rows count from an EXPLAIN ANALYZE
// plan — for a single-table scan+aggregate that is the rows the scan produced.
var cardRe = regexp.MustCompile(`(?i)(\d[\d,]*)\s*Rows`)

func maxScanCardinality(plan string) int64 {
	var max int64
	for _, m := range cardRe.FindAllStringSubmatch(plan, -1) {
		n, err := strconv.ParseInt(strings.ReplaceAll(m[1], ",", ""), 10, 64)
		if err == nil && n > max {
			max = n
		}
	}
	return max
}
