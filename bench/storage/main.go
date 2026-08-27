// Command storage-bench compares Fanout's storage path with alternative formats
// with native DuckDB and Parquet on Fanout-shaped spans. It is an experiment,
// not a supported Fanout command.
package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/labstack/fanout/internal/storagebench"
	"github.com/labstack/fanout/internal/telemetry/segment"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

type result struct {
	name          string
	writeRate     float64
	rollupBuild   time.Duration
	maintenance   time.Duration
	diskBytes     int64
	endpoint      time.Duration
	trace         time.Duration
	rawService    time.Duration
	recovery      time.Duration
	queryRowCount uint64
	mixedWrite    float64
	mixedReadP95  time.Duration
}

func main() {
	rows := flag.Int("rows", 1_000_000, "number of synthetic spans")
	batch := flag.Int("batch", 50_000, "rows per durable append")
	repeats := flag.Int("repeats", 21, "query repetitions used for medians")
	mixedRows := flag.Int("mixed-rows", 200_000, "additional rows written while trace reads run at 100 qps")
	engine := flag.String("engine", "all", "engines to run: all, repository, custom, or duck")
	keep := flag.String("keep", "", "keep artifacts in this directory instead of a temporary directory")
	flag.Parse()
	if *rows <= 0 || *batch <= 0 || *repeats <= 0 {
		fmt.Fprintln(os.Stderr, "rows, batch, and repeats must be positive")
		os.Exit(2)
	}

	root := *keep
	if root == "" {
		var err error
		root, err = os.MkdirTemp("", "fanout-storage-bench-")
		if err != nil {
			fatal(err)
		}
		defer os.RemoveAll(root)
	} else if err := os.MkdirAll(root, 0o755); err != nil {
		fatal(err)
	}

	base := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC).UnixNano()
	targetTrace := storagebench.TraceID(uint64(*rows / 2 / 5))
	start := base
	end := base + storagebench.DayNanos
	fmt.Printf("Fanout storage benchmark: %d spans, %d-row commits, %s/%s, %d CPUs\n", *rows, *batch, runtime.GOOS, runtime.GOARCH, runtime.NumCPU())

	var results []result
	if *engine == "all" || *engine == "repository" {
		repositoryResult, err := runRepository(filepath.Join(root, "repository"), *rows, *batch, *repeats, *mixedRows, base, start, end, targetTrace)
		if err != nil {
			fatal(fmt.Errorf("production repository: %w", err))
		}
		results = append(results, repositoryResult)
	}
	var custom result
	if *engine == "all" || *engine == "custom" {
		var err error
		custom, err = runCustom(filepath.Join(root, "fanseg"), *rows, *batch, *repeats, *mixedRows, base, start, end, targetTrace)
		if err != nil {
			fatal(fmt.Errorf("custom segments: %w", err))
		}
		results = append(results, custom)
	}
	if *engine == "all" || *engine == "duck" {
		duck, parquet, err := runDuck(filepath.Join(root, "duck.db"), filepath.Join(root, "parquet"), *rows, *batch, *repeats, *mixedRows, base, start, end, targetTrace)
		if err != nil {
			fatal(fmt.Errorf("duckdb: %w", err))
		}
		results = append(results, duck, parquet)
	}
	if len(results) == 0 {
		fatal(fmt.Errorf("unknown engine %q", *engine))
	}

	fmt.Println()
	fmt.Printf("%-20s %14s %13s %12s %12s %12s %12s %12s\n", "storage / execution", "write rows/s", "rollup build", "maintenance", "disk MiB", "endpoint", "trace", "raw service")
	for _, r := range results {
		rollup := "live"
		if r.rollupBuild > 0 {
			rollup = formatDuration(r.rollupBuild)
		}
		fmt.Printf("%-20s %14.0f %13s %12s %12.1f %12s %12s %12s\n", r.name, r.writeRate, rollup, formatDuration(r.maintenance), float64(r.diskBytes)/(1<<20), formatDuration(r.endpoint), formatDuration(r.trace), formatDuration(r.rawService))
	}
	fmt.Println("\nMixed load: committed writes plus full trace reads at 100 qps")
	fmt.Printf("%-20s %14s %14s\n", "storage / execution", "write rows/s", "trace p95")
	for _, r := range results {
		if r.mixedWrite == 0 {
			continue
		}
		fmt.Printf("%-20s %14.0f %14s\n", r.name, r.mixedWrite, formatDuration(r.mixedReadP95))
	}
	if custom.name != "" {
		fmt.Printf("\nfanseg reopen/recovery: %s; trace rows: %d\n", formatDuration(custom.recovery), custom.queryRowCount)
	}
	if *keep != "" {
		fmt.Printf("artifacts: %s\n", root)
	}
}

func runRepository(dir string, total, batch, repeats, mixedRows int, base, start, end int64, targetTrace string) (result, error) {
	repository, err := telemetrystore.Open(dir)
	if err != nil {
		return result{}, err
	}
	defer repository.Close()
	writeStart := time.Now()
	for offset := 0; offset < total; offset += batch {
		rows := storagebench.Rows(offset, min(batch, total-offset), total, base)
		if err := repository.Commit(telemetrystore.Batch{ID: fmt.Sprintf("initial-%08d", offset), Spans: rows}); err != nil {
			return result{}, err
		}
	}
	writeElapsed := time.Since(writeStart)
	var endpointSink []segment.Endpoint
	endpoint := median(repeats, func() error {
		endpointSink = repository.Spans.Endpoints("default", "service-00", start, end, 20)
		return nil
	})
	var traceSink []segment.Span
	trace := median(repeats, func() (err error) { traceSink, err = repository.Spans.Trace(targetTrace); return err })
	var aggregateSink segment.Aggregate
	raw := median(repeats, func() (err error) {
		aggregateSink, err = repository.Spans.ScanService("default", "service-00", start, end)
		return err
	})
	if len(endpointSink) == 0 || aggregateSink.Calls == 0 {
		return result{}, errors.New("production repository queries returned no rows")
	}
	disk, err := directoryBytes(dir)
	if err != nil {
		return result{}, err
	}
	mixedWrite, mixedP95, err := mixedLoad(mixedRows,
		func() error { _, err := repository.Spans.Trace(targetTrace); return err },
		func() error {
			finalTotal := total + mixedRows
			for offset := 0; offset < mixedRows; offset += batch {
				rows := storagebench.Rows(total+offset, min(batch, mixedRows-offset), finalTotal, base)
				if err := repository.Commit(telemetrystore.Batch{ID: fmt.Sprintf("mixed-%08d", offset), Spans: rows}); err != nil {
					return err
				}
			}
			return nil
		})
	if err != nil {
		return result{}, err
	}
	return result{name: "Fanout + Parquet", writeRate: float64(total) / writeElapsed.Seconds(), diskBytes: disk, endpoint: endpoint, trace: trace, rawService: raw, queryRowCount: uint64(len(traceSink)), mixedWrite: mixedWrite, mixedReadP95: mixedP95}, nil
}

func directoryBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func runCustom(dir string, total, batch, repeats, mixedRows int, base, start, end int64, targetTrace string) (result, error) {
	store, err := segment.Open(dir)
	if err != nil {
		return result{}, err
	}
	writeStart := time.Now()
	for offset := 0; offset < total; offset += batch {
		count := min(batch, total-offset)
		if err := store.Append(storagebench.Rows(offset, count, total, base)); err != nil {
			return result{}, err
		}
	}
	writeElapsed := time.Since(writeStart)
	maintenanceStart := time.Now()
	if err := store.CompactOldest(store.SegmentCount()); err != nil {
		return result{}, err
	}
	maintenance := time.Since(maintenanceStart)
	disk, err := store.DiskBytes()
	if err != nil {
		return result{}, err
	}
	if err := store.Close(); err != nil {
		return result{}, err
	}
	reopenStart := time.Now()
	store, err = segment.Open(dir)
	if err != nil {
		return result{}, err
	}
	recovery := time.Since(reopenStart)
	defer store.Close()

	var endpointSink []segment.Endpoint
	endpoint := median(repeats, func() error { endpointSink = store.Endpoints("default", "service-00", start, end, 20); return nil })
	var traceSink []segment.Span
	trace := median(repeats, func() (err error) { traceSink, err = store.Trace(targetTrace); return err })
	var aggSink segment.Aggregate
	raw := median(repeats, func() (err error) { aggSink, err = store.ScanService("default", "service-00", start, end); return err })
	if len(endpointSink) == 0 || aggSink.Calls == 0 {
		return result{}, fmt.Errorf("queries returned no rows")
	}
	mixedWrite, mixedP95, err := mixedCustom(store, total, mixedRows, batch, base, targetTrace)
	if err != nil {
		return result{}, err
	}
	return result{name: "fanseg + direct", writeRate: float64(total) / writeElapsed.Seconds(), maintenance: maintenance, diskBytes: disk, endpoint: endpoint, trace: trace, rawService: raw, recovery: recovery, queryRowCount: uint64(len(traceSink)), mixedWrite: mixedWrite, mixedReadP95: mixedP95}, nil
}

func runDuck(dbPath, parquetDir string, total, batch, repeats, mixedRows int, base, start, end int64, targetTrace string) (result, result, error) {
	connector, err := duckdb.NewConnector(dbPath, nil)
	if err != nil {
		return result{}, result{}, err
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(4)
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE spans (
		namespace VARCHAR, trace_id VARCHAR, span_id VARCHAR, parent_span_id VARCHAR,
		service_name VARCHAR, name VARCHAR, kind VARCHAR, start_time TIMESTAMP_NS,
		end_time TIMESTAMP_NS, start_ns BIGINT, end_ns BIGINT, duration_ms DOUBLE,
		status_code VARCHAR, status_msg VARCHAR, resource JSON, attributes JSON,
		events JSON, links JSON, trace_state VARCHAR, flags UINTEGER,
		scope_name VARCHAR, scope_version VARCHAR, ingested_at TIMESTAMP_NS,
		ingested_ns BIGINT, http_method VARCHAR, http_status_code VARCHAR,
		http_route VARCHAR, db_system VARCHAR, rpc_method VARCHAR, rpc_service VARCHAR,
		peer_service VARCHAR, service_version VARCHAR, deployment_env VARCHAR,
		exception_type VARCHAR, exception_message VARCHAR
	)`); err != nil {
		return result{}, result{}, err
	}

	writeStart := time.Now()
	for offset := 0; offset < total; offset += batch {
		rows := storagebench.Rows(offset, min(batch, total-offset), total, base)
		if err := appendDuck(db, rows); err != nil {
			return result{}, result{}, err
		}
	}
	writeElapsed := time.Since(writeStart)
	rollupStart := time.Now()
	if _, err := db.Exec(`CREATE TABLE endpoint_rollup AS
		SELECT start_ns - start_ns % 300000000000 AS bucket, namespace, service_name, http_method, http_route,
		       count(*)::UBIGINT AS calls, count(*) FILTER (WHERE status_code='ERROR')::UBIGINT AS errors,
		       sum(duration_ms) AS duration_ms, approx_quantile(duration_ms, 0.95) AS p95_ms
		FROM spans GROUP BY ALL`); err != nil {
		return result{}, result{}, err
	}
	rollupBuild := time.Since(rollupStart)
	checkpointStart := time.Now()
	if _, err := db.Exec("CHECKPOINT"); err != nil {
		return result{}, result{}, err
	}
	checkpointElapsed := time.Since(checkpointStart)
	info, err := os.Stat(dbPath)
	if err != nil {
		return result{}, result{}, err
	}

	endpointQuery := `SELECT service_name, http_method, http_route, sum(calls) calls, sum(errors) errors,
		sum(duration_ms) / sum(calls) average_ms, max(p95_ms) p95_ms
		FROM endpoint_rollup WHERE namespace=? AND service_name=? AND bucket>=? AND bucket<?
		GROUP BY ALL ORDER BY calls DESC LIMIT 20`
	traceQuery := `SELECT * FROM spans WHERE trace_id=? ORDER BY start_ns`
	rawQuery := `SELECT count(*), count(*) FILTER (WHERE status_code='ERROR'), sum(duration_ms)
		FROM spans WHERE namespace=? AND service_name=? AND start_ns>=? AND start_ns<?`
	duckEndpoint := median(repeats, func() error { return consumeRows(db, endpointQuery, "default", "service-00", start, end) })
	duckTrace := median(repeats, func() error { return consumeRows(db, traceQuery, targetTrace) })
	duckRaw := median(repeats, func() error { return consumeRows(db, rawQuery, "default", "service-00", start, end) })

	if err := os.MkdirAll(parquetDir, 0o755); err != nil {
		return result{}, result{}, err
	}
	spansParquet := filepath.Join(parquetDir, "spans.parquet")
	rollupParquet := filepath.Join(parquetDir, "endpoint_rollup.parquet")
	exportStart := time.Now()
	if _, err := db.Exec(fmt.Sprintf("COPY spans TO %s (FORMAT PARQUET, COMPRESSION ZSTD, ROW_GROUP_SIZE 122880)", quoteSQL(spansParquet))); err != nil {
		return result{}, result{}, err
	}
	if _, err := db.Exec(fmt.Sprintf("COPY endpoint_rollup TO %s (FORMAT PARQUET, COMPRESSION ZSTD)", quoteSQL(rollupParquet))); err != nil {
		return result{}, result{}, err
	}
	exportElapsed := time.Since(exportStart)
	spansInfo, err := os.Stat(spansParquet)
	if err != nil {
		return result{}, result{}, err
	}
	rollupInfo, err := os.Stat(rollupParquet)
	if err != nil {
		return result{}, result{}, err
	}
	pEndpointQuery := fmt.Sprintf(`SELECT service_name, http_method, http_route, sum(calls) calls, sum(errors) errors,
		sum(duration_ms) / sum(calls) average_ms, max(p95_ms) p95_ms
		FROM read_parquet(%s) WHERE namespace=? AND service_name=? AND bucket>=? AND bucket<?
		GROUP BY ALL ORDER BY calls DESC LIMIT 20`, quoteSQL(rollupParquet))
	pTraceQuery := fmt.Sprintf(`SELECT * FROM read_parquet(%s) WHERE trace_id=? ORDER BY start_ns`, quoteSQL(spansParquet))
	pRawQuery := fmt.Sprintf(`SELECT count(*), count(*) FILTER (WHERE status_code='ERROR'), sum(duration_ms)
		FROM read_parquet(%s) WHERE namespace=? AND service_name=? AND start_ns>=? AND start_ns<?`, quoteSQL(spansParquet))
	pEndpoint := median(repeats, func() error { return consumeRows(db, pEndpointQuery, "default", "service-00", start, end) })
	pTrace := median(repeats, func() error { return consumeRows(db, pTraceQuery, targetTrace) })
	pRaw := median(repeats, func() error { return consumeRows(db, pRawQuery, "default", "service-00", start, end) })
	mixedWrite, mixedP95, err := mixedDuck(db, total, mixedRows, batch, base, traceQuery, targetTrace)
	if err != nil {
		return result{}, result{}, err
	}

	duck := result{name: "DuckDB native", writeRate: float64(total) / writeElapsed.Seconds(), rollupBuild: rollupBuild, maintenance: checkpointElapsed, diskBytes: info.Size(), endpoint: duckEndpoint, trace: duckTrace, rawService: duckRaw, mixedWrite: mixedWrite, mixedReadP95: mixedP95}
	parquet := result{name: "Parquet + DuckDB", writeRate: float64(total) / (writeElapsed + exportElapsed).Seconds(), rollupBuild: rollupBuild, maintenance: exportElapsed, diskBytes: spansInfo.Size() + rollupInfo.Size(), endpoint: pEndpoint, trace: pTrace, rawService: pRaw}
	return duck, parquet, nil
}

func appendDuck(db *sql.DB, rows []segment.Span) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(raw any) error {
		driverConn, ok := raw.(driver.Conn)
		if !ok {
			return fmt.Errorf("unexpected DuckDB connection %T", raw)
		}
		appender, err := duckdb.NewAppender(driverConn, "", "", "spans")
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := appender.AppendRow(
				row.Namespace, row.TraceID, row.SpanID, row.ParentSpanID,
				row.ServiceName, row.Name, row.Kind, time.Unix(0, row.StartUnixNanos),
				time.Unix(0, row.EndUnixNanos), row.StartUnixNanos, row.EndUnixNanos, row.DurationMS,
				row.StatusCode, row.StatusMsg, string(row.ResourceJSON), string(row.AttributesJSON),
				string(row.EventsJSON), string(row.LinksJSON), row.TraceState, row.Flags,
				row.ScopeName, row.ScopeVersion, time.Unix(0, row.IngestedAt), row.IngestedAt,
				row.HTTPMethod, row.HTTPStatusCode, row.HTTPRoute, row.DBSystem,
				row.RPCMethod, row.RPCService, row.PeerService, row.ServiceVersion,
				row.DeploymentEnv, row.ExceptionType, row.ExceptionMessage,
			); err != nil {
				_ = appender.Close()
				return err
			}
		}
		return appender.Close()
	})
}

func mixedCustom(store *segment.Store, existing, additional, batch int, base int64, traceID string) (float64, time.Duration, error) {
	return mixedLoad(additional,
		func() error { _, err := store.Trace(traceID); return err },
		func() error {
			finalTotal := existing + additional
			for offset := 0; offset < additional; offset += batch {
				rows := storagebench.Rows(existing+offset, min(batch, additional-offset), finalTotal, base)
				if err := store.Append(rows); err != nil {
					return err
				}
			}
			return nil
		},
	)
}

func mixedDuck(db *sql.DB, existing, additional, batch int, base int64, traceQuery, traceID string) (float64, time.Duration, error) {
	return mixedLoad(additional,
		func() error { return consumeRows(db, traceQuery, traceID) },
		func() error {
			finalTotal := existing + additional
			for offset := 0; offset < additional; offset += batch {
				rows := storagebench.Rows(existing+offset, min(batch, additional-offset), finalTotal, base)
				if err := appendDuck(db, rows); err != nil {
					return err
				}
			}
			return nil
		},
	)
}

func mixedLoad(rows int, read, write func() error) (float64, time.Duration, error) {
	if rows <= 0 {
		return 0, 0, nil
	}
	stop := make(chan struct{})
	ready := make(chan struct{})
	var wg sync.WaitGroup
	var latencies []time.Duration
	var readErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		close(ready)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				started := time.Now()
				if err := read(); err != nil {
					readErr = err
					return
				}
				latencies = append(latencies, time.Since(started))
			}
		}
	}()
	<-ready
	started := time.Now()
	writeErr := write()
	elapsed := time.Since(started)
	close(stop)
	wg.Wait()
	if writeErr != nil {
		return 0, 0, writeErr
	}
	if readErr != nil {
		return 0, 0, readErr
	}
	if len(latencies) == 0 {
		started := time.Now()
		if err := read(); err != nil {
			return 0, 0, err
		}
		latencies = append(latencies, time.Since(started))
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p95 := latencies[min(len(latencies)-1, int(float64(len(latencies))*.95))]
	return float64(rows) / elapsed.Seconds(), p95, nil
}

func consumeRows(db *sql.DB, query string, args ...any) error {
	rows, err := db.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	values := make([]any, len(columns))
	targets := make([]any, len(columns))
	for i := range values {
		targets[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(targets...); err != nil {
			return err
		}
	}
	return rows.Err()
}

func median(repeats int, fn func() error) time.Duration {
	durations := make([]time.Duration, 0, repeats)
	for range repeats {
		started := time.Now()
		if err := fn(); err != nil {
			fatal(err)
		}
		durations = append(durations, time.Since(started))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return durations[len(durations)/2]
}

func quoteSQL(value string) string {
	out := "'"
	for _, r := range value {
		if r == '\'' {
			out += "''"
		} else {
			out += string(r)
		}
	}
	return out + "'"
}

func formatDuration(value time.Duration) string {
	if value >= time.Second {
		return fmt.Sprintf("%.2fs", value.Seconds())
	}
	if value >= time.Millisecond {
		return fmt.Sprintf("%.2fms", float64(value)/float64(time.Millisecond))
	}
	return fmt.Sprintf("%.2fµs", float64(value)/float64(time.Microsecond))
}

func fatal(err error) { fmt.Fprintln(os.Stderr, "storage-bench:", err); os.Exit(1) }
