package main

import (
	"bytes"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	_ "github.com/chdb-io/chdb-go/lib/embedded"
	"github.com/chdb-io/chdb-go/v2/chdb"
	"github.com/labstack/fanout/internal/storagebench"
	"github.com/labstack/fanout/internal/telemetry/segment"
)

func main() {
	rows := flag.Int("rows", 1_000_000, "number of synthetic spans")
	batch := flag.Int("batch", 50_000, "rows per insert")
	repeats := flag.Int("repeats", 11, "query repetitions")
	mixedRows := flag.Int("mixed-rows", 200_000, "additional rows written while trace reads run at 100 qps")
	dir := flag.String("dir", "", "session directory; temporary when empty")
	flag.Parse()
	root := *dir
	if root == "" {
		var err error
		root, err = os.MkdirTemp("", "fanout-chdb-poc-")
		if err != nil {
			fatal(err)
		}
		defer os.RemoveAll(root)
	} else if err := os.MkdirAll(root, 0o755); err != nil {
		fatal(err)
	}
	base := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	targetTrace := storagebench.TraceID(uint64(*rows / 2 / 5))
	initStart := time.Now()
	session, err := chdb.NewSession(filepath.Join(root, "engine"))
	if err != nil {
		fatal(err)
	}
	initElapsed := time.Since(initStart)
	defer session.Close()
	for _, statement := range []string{
		"CREATE DATABASE fanout", spansDDL, endpointDDL, endpointMVDDL,
		fmt.Sprintf("SET max_threads=%d", runtime.NumCPU()), "SET date_time_input_format='best_effort'",
	} {
		query(session, statement, "Null")
	}

	writeStart := time.Now()
	insertRange(session, 0, *rows, *rows, *batch, base.UnixNano())
	writeElapsed := time.Since(writeStart)
	maintenanceStart := time.Now()
	query(session, "OPTIMIZE TABLE fanout.spans FINAL", "Null")
	query(session, "OPTIMIZE TABLE fanout.endpoint_rollup FINAL", "Null")
	maintenanceElapsed := time.Since(maintenanceStart)
	activeBytes, err := strconv.ParseInt(queryText(session, "SELECT sum(bytes_on_disk) FROM system.parts WHERE active AND database='fanout'"), 10, 64)
	if err != nil {
		fatal(fmt.Errorf("parse active bytes: %w", err))
	}

	startLiteral, endLiteral := "2026-08-25 00:00:00", "2026-08-26 00:00:00"
	endpointSQL := fmt.Sprintf(`SELECT service_name,http_method,http_route,sum(calls),sum(errors),sum(duration_sum)/sum(calls),max(p95_ms)
		FROM fanout.endpoint_rollup WHERE namespace='default' AND service_name='service-00'
		AND bucket>=toDateTime64('%s',9,'UTC') AND bucket<toDateTime64('%s',9,'UTC')
		GROUP BY ALL ORDER BY sum(calls) DESC LIMIT 20`, startLiteral, endLiteral)
	traceSQL := fmt.Sprintf("SELECT * FROM fanout.spans WHERE trace_id='%s' ORDER BY start_unix_nano", targetTrace)
	rawSQL := fmt.Sprintf(`SELECT count(),countIf(status_code='ERROR'),sum(duration_ms) FROM fanout.spans
		WHERE namespace='default' AND service_name='service-00'
		AND start_time>=toDateTime64('%s',9,'UTC') AND start_time<toDateTime64('%s',9,'UTC')`, startLiteral, endLiteral)
	endpoint := median(*repeats, func() { query(session, endpointSQL, "JSONCompactEachRow") })
	trace := median(*repeats, func() { query(session, traceSQL, "JSONCompactEachRow") })
	raw := median(*repeats, func() { query(session, rawSQL, "JSONCompactEachRow") })
	reader, err := chdb.NewSession(filepath.Join(root, "engine"))
	if err != nil {
		fatal(err)
	}
	mixedWrite, mixedP95 := mixedLoad(*mixedRows,
		func() { query(reader, traceSQL, "JSONCompactEachRow") },
		func() { insertRange(session, *rows, *mixedRows, *rows+*mixedRows, *batch, base.UnixNano()) },
	)
	reader.Close()
	fmt.Printf("chDB + MergeTree rows=%d batch=%d write_rows_s=%.0f maintenance=%s active_mib=%.1f dir_mib=%.1f endpoint=%s trace=%s raw_service=%s init=%s mixed_write_rows_s=%.0f mixed_trace_p95=%s\n",
		*rows, *batch, float64(*rows)/writeElapsed.Seconds(), formatDuration(maintenanceElapsed), float64(activeBytes)/(1<<20), float64(treeSize(root))/(1<<20),
		formatDuration(endpoint), formatDuration(trace), formatDuration(raw), formatDuration(initElapsed), mixedWrite, formatDuration(mixedP95))
}

func insertRange(session *chdb.Session, offset, count, total, batch int, base int64) {
	for written := 0; written < count; written += batch {
		batchRows := storagebench.Rows(offset+written, min(batch, count-written), total, base)
		var buf bytes.Buffer
		buf.WriteString("INSERT INTO fanout.spans FORMAT CSV\n")
		writer := csv.NewWriter(&buf)
		for _, row := range batchRows {
			if err := writer.Write(csvRecord(row)); err != nil {
				fatal(err)
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			fatal(err)
		}
		query(session, buf.String(), "Null")
	}
}

func csvRecord(row segment.Span) []string {
	const layout = "2006-01-02 15:04:05.000000000"
	return []string{
		row.Namespace, row.TraceID, row.SpanID, row.ParentSpanID, row.ServiceName, row.Name, row.Kind,
		time.Unix(0, row.StartUnixNanos).UTC().Format(layout), time.Unix(0, row.EndUnixNanos).UTC().Format(layout),
		strconv.FormatInt(row.StartUnixNanos, 10), strconv.FormatInt(row.EndUnixNanos, 10), strconv.FormatFloat(row.DurationMS, 'g', -1, 64),
		row.StatusCode, row.StatusMsg, string(row.ResourceJSON), string(row.AttributesJSON), string(row.EventsJSON), string(row.LinksJSON),
		row.TraceState, strconv.FormatUint(uint64(row.Flags), 10), row.ScopeName, row.ScopeVersion,
		time.Unix(0, row.IngestedAt).UTC().Format(layout), strconv.FormatInt(row.IngestedAt, 10), row.HTTPMethod, row.HTTPStatusCode,
		row.HTTPRoute, row.DBSystem, row.RPCMethod, row.RPCService, row.PeerService, row.ServiceVersion, row.DeploymentEnv,
		row.ExceptionType, row.ExceptionMessage,
	}
}

func query(session *chdb.Session, statement, format string) {
	result, err := session.Query(statement, format)
	if err != nil {
		fatal(err)
	}
	defer result.Free()
	if err := result.Error(); err != nil {
		fatal(err)
	}
	if format != "Null" && result.Len() == 0 {
		fatal(fmt.Errorf("empty result for query: %.80s", statement))
	}
}

func queryText(session *chdb.Session, statement string) string {
	result, err := session.Query(statement, "TabSeparatedRaw")
	if err != nil {
		fatal(err)
	}
	defer result.Free()
	if err := result.Error(); err != nil {
		fatal(err)
	}
	return string(bytes.TrimSpace(result.Buf()))
}

func median(repeats int, fn func()) time.Duration {
	values := make([]time.Duration, repeats)
	for i := range values {
		start := time.Now()
		fn()
		values[i] = time.Since(start)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[len(values)/2]
}

func mixedLoad(rows int, read, write func()) (float64, time.Duration) {
	if rows <= 0 {
		return 0, 0
	}
	stop, ready := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	var latencies []time.Duration
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
				read()
				latencies = append(latencies, time.Since(started))
			}
		}
	}()
	<-ready
	started := time.Now()
	write()
	elapsed := time.Since(started)
	close(stop)
	wg.Wait()
	if len(latencies) == 0 {
		started := time.Now()
		read()
		latencies = append(latencies, time.Since(started))
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p95 := latencies[min(len(latencies)-1, int(float64(len(latencies))*.95))]
	return float64(rows) / elapsed.Seconds(), p95
}

func treeSize(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
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

func fatal(err error) { fmt.Fprintln(os.Stderr, "storage-bench-chdb:", err); os.Exit(1) }

const spansDDL = `CREATE TABLE fanout.spans (
 namespace String,trace_id String,span_id String,parent_span_id String,service_name LowCardinality(String),name LowCardinality(String),kind LowCardinality(String),
 start_time DateTime64(9,'UTC') CODEC(DoubleDelta,LZ4),end_time DateTime64(9,'UTC') CODEC(DoubleDelta,LZ4),
 start_unix_nano Int64 CODEC(DoubleDelta,LZ4),end_unix_nano Int64 CODEC(DoubleDelta,LZ4),duration_ms Float64 CODEC(Gorilla,LZ4),
 status_code LowCardinality(String),status_msg String CODEC(ZSTD(1)),resource_json String CODEC(ZSTD(1)),attributes_json String CODEC(ZSTD(1)),
 events_json String CODEC(ZSTD(1)),links_json String CODEC(ZSTD(1)),trace_state String,flags UInt32,scope_name LowCardinality(String),scope_version String,
 ingested_at DateTime64(9,'UTC') CODEC(DoubleDelta,LZ4),ingested_unix_nano Int64 CODEC(DoubleDelta,LZ4),http_method LowCardinality(String),
 http_status_code LowCardinality(String),http_route LowCardinality(String),db_system LowCardinality(String),rpc_method LowCardinality(String),
 rpc_service LowCardinality(String),peer_service LowCardinality(String),service_version LowCardinality(String),deployment_env LowCardinality(String),
 exception_type LowCardinality(String),exception_message String CODEC(ZSTD(1)),
 tenant_id LowCardinality(String) MATERIALIZED JSONExtractString(attributes_json,'tenant'),
 PROJECTION by_trace INDEX trace_id TYPE basic,PROJECTION by_tenant INDEX tenant_id TYPE basic
) ENGINE=MergeTree PARTITION BY toDate(start_time) ORDER BY (namespace,start_time,service_name) SETTINGS old_parts_lifetime=0`

const endpointDDL = `CREATE TABLE fanout.endpoint_rollup (
 namespace String,bucket DateTime64(9,'UTC'),service_name LowCardinality(String),http_method LowCardinality(String),http_route LowCardinality(String),
 calls SimpleAggregateFunction(sum,UInt64),errors SimpleAggregateFunction(sum,UInt64),duration_sum SimpleAggregateFunction(sum,Float64),
 p95_ms SimpleAggregateFunction(max,Float64)
) ENGINE=AggregatingMergeTree PARTITION BY toDate(bucket) ORDER BY (namespace,bucket,service_name,http_method,http_route) SETTINGS old_parts_lifetime=0`

const endpointMVDDL = `CREATE MATERIALIZED VIEW fanout.endpoint_rollup_mv TO fanout.endpoint_rollup AS
SELECT namespace,toStartOfInterval(start_time,INTERVAL 5 MINUTE) AS bucket,service_name,http_method,http_route,
 count() AS calls,countIf(status_code='ERROR') AS errors,sum(duration_ms) AS duration_sum,quantileTDigest(0.95)(duration_ms) AS p95_ms
FROM fanout.spans GROUP BY namespace,bucket,service_name,http_method,http_route`
