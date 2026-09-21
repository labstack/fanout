//go:build !race

package query

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// rollupUntrackedLimitMiB is how far past DuckDB's configured memory_limit one
// rollup pass may allocate.
//
// memory_limit bounds the buffer pool and nothing else. Holistic aggregates --
// quantile_cont, PERCENTILE_CONT -- keep every value of every group in a vector
// on the raw allocator, so they allocate proportionally to rows scanned with no
// ceiling and no pressure signal: DuckDB reports zero usage while the kernel
// sees gigabytes. That is what OOM-killed this process, and the configured
// limit could never have prevented it.
//
// Measured on 30M rows at memory_limit=100MB: the production expression shape
// grew RSS by 861 MiB with duckdb_memory() reporting 0.0 MiB, while the
// approximate form grew it by 8 MiB. The limit below sits far below the former
// and far above the latter.
const rollupUntrackedLimitMiB = 200

// processRSSMiB reads this process's resident size, or skips the test where it
// cannot: a busybox ps without -o/-p would otherwise fail the whole suite for a
// reason unrelated to the regression this guards.
func processRSSMiB(t *testing.T) float64 {
	t.Helper()
	if raw, err := os.ReadFile("/proc/self/statm"); err == nil {
		fields := strings.Fields(string(raw))
		if len(fields) > 1 {
			if pages, err := strconv.ParseFloat(fields[1], 64); err == nil {
				return pages * float64(os.Getpagesize()) / (1 << 20)
			}
		}
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		t.Skipf("no way to read RSS on this platform: %v", err)
	}
	kb, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Skipf("unreadable ps output %q: %v", out, err)
	}
	return kb / 1024
}

// A rollup pass scans an hour of ingest with no row bound, so its latency
// aggregation must not allocate in proportion to the rows it reads.
func TestServiceRollupLatencyDoesNotAllocatePerRow(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{"SET memory_limit='100MB'", "SET threads=4"} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	if testing.Short() {
		t.Skip("scans 30M rows")
	}
	baseline := processRSSMiB(t)
	query := fmt.Sprintf(`SELECT service, %s, %s
		FROM (
			SELECT (i %% 20)::VARCHAR AS service,
			       (i %% 1000)::DOUBLE AS duration_ms,
			       CASE WHEN i %% 7 = 0 THEN 'SPAN_KIND_CLIENT' ELSE 'SPAN_KIND_SERVER' END AS kind
			FROM range(30000000) t(i)
		) s
		GROUP BY service`, serviceRollupP50SQL("s"), serviceRollupP95SQL("s"))

	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("rollup latency query: %v", err)
	}
	groups := 0
	for rows.Next() {
		groups++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()

	grew := processRSSMiB(t) - baseline
	var tracked float64
	if err := db.QueryRow("SELECT COALESCE(sum(memory_usage_bytes), 0) / 1048576.0 FROM duckdb_memory()").Scan(&tracked); err != nil {
		t.Fatalf("duckdb_memory: %v", err)
	}
	t.Logf("groups=%d  RSS +%.0f MiB over a 100 MB memory_limit  duckdb_memory reports %.1f MiB", groups, grew, tracked)

	if groups != 20 {
		t.Fatalf("groups = %d, want 20: the fixture is wrong", groups)
	}
	if grew > rollupUntrackedLimitMiB {
		t.Errorf("one rollup pass grew RSS by %.0f MiB against a 100 MB limit (DuckDB reported %.1f MiB): the latency aggregation allocates per row and memory_limit cannot see it",
			grew, tracked)
	}
}
