package telemetry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet"
	"github.com/apache/arrow-go/v18/parquet/compress"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
)

type parquetColumn[T any] struct {
	name     string
	typeInfo arrow.DataType
	nullable bool
	value    func(T) any
}

type ParquetStore struct {
	dir string
}

type ParquetStats struct {
	Files int
	Bytes int64
}

func OpenParquetStore(dir string) (*ParquetStore, error) {
	for _, signal := range []string{"spans", "logs", "metrics"} {
		if err := os.MkdirAll(filepath.Join(dir, signal), 0o755); err != nil {
			return nil, fmt.Errorf("create parquet %s directory: %w", signal, err)
		}
	}
	store := &ParquetStore{dir: dir}
	if err := writeParquet(filepath.Join(dir, "spans", "_schema.parquet"), spanParquetColumns(), []Span{}); err != nil {
		return nil, fmt.Errorf("create span parquet schema: %w", err)
	}
	if err := writeParquet(filepath.Join(dir, "logs", "_schema.parquet"), logParquetColumns(), []Log{}); err != nil {
		return nil, fmt.Errorf("create log parquet schema: %w", err)
	}
	if err := writeParquet(filepath.Join(dir, "metrics", "_schema.parquet"), metricParquetColumns(), []Metric{}); err != nil {
		return nil, fmt.Errorf("create metric parquet schema: %w", err)
	}
	return store, nil
}

func (p *ParquetStore) Dir() string { return p.dir }

// Stats reports the current immutable-file footprint for each signal.
func (p *ParquetStore) Stats() (map[string]ParquetStats, error) {
	stats := make(map[string]ParquetStats, 3)
	for _, signal := range []string{"spans", "logs", "metrics"} {
		entries, err := os.ReadDir(filepath.Join(p.dir, signal))
		if err != nil {
			return nil, err
		}
		var signalStats ParquetStats
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".parquet" || entry.Name() == "_schema.parquet" {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}
			signalStats.Files++
			signalStats.Bytes += info.Size()
		}
		stats[signal] = signalStats
	}
	return stats, nil
}

// StageBatch writes durable files that DuckDB's *.parquet views cannot see.
// Publication is a separate, rename-only step so encoding and fsync never hold
// the query read gate.
func (p *ParquetStore) StageBatch(id string, spans []Span, logs []Log, metrics []Metric) error {
	for _, item := range []struct {
		signal string
		write  func(string) error
	}{
		{"spans", func(path string) error { return writeParquet(path, spanParquetColumns(), spans) }},
		{"logs", func(path string) error { return writeParquet(path, logParquetColumns(), logs) }},
		{"metrics", func(path string) error { return writeParquet(path, metricParquetColumns(), metrics) }},
	} {
		rows := len(spans)
		if item.signal == "logs" {
			rows = len(logs)
		} else if item.signal == "metrics" {
			rows = len(metrics)
		}
		if rows == 0 {
			continue
		}
		final := filepath.Join(p.dir, item.signal, id+".parquet")
		if _, err := os.Stat(final); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := item.write(final + ".pending"); err != nil {
			return fmt.Errorf("stage %s parquet: %w", item.signal, err)
		}
	}
	return nil
}

// PublishBatch atomically exposes all present signal files to in-process
// readers when the caller holds the Parquet publication gate. The returned
// rollback is used if the hot span projection cannot be published afterward.
func (p *ParquetStore) PublishBatch(id string, hasSpans, hasLogs, hasMetrics bool) (func() error, error) {
	present := []struct {
		signal string
		has    bool
	}{{"spans", hasSpans}, {"logs", hasLogs}, {"metrics", hasMetrics}}
	renamed := make([]string, 0, len(present))
	rollback := func() error {
		var rollbackErr error
		for i := len(renamed) - 1; i >= 0; i-- {
			final := filepath.Join(p.dir, renamed[i], id+".parquet")
			if err := os.Rename(final, final+".pending"); err != nil && !errors.Is(err, os.ErrNotExist) {
				rollbackErr = errors.Join(rollbackErr, err)
			}
		}
		if len(renamed) > 0 {
			rollbackErr = errors.Join(rollbackErr, syncParquetSignalDirectories(p.dir, renamed))
		}
		return rollbackErr
	}
	for _, item := range present {
		if !item.has {
			continue
		}
		final := filepath.Join(p.dir, item.signal, id+".parquet")
		if _, err := os.Stat(final); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return rollback, errors.Join(err, rollback())
		}
		if err := os.Rename(final+".pending", final); err != nil {
			return rollback, errors.Join(err, rollback())
		}
		renamed = append(renamed, item.signal)
	}
	if err := syncParquetSignalDirectories(p.dir, renamed); err != nil {
		return rollback, errors.Join(err, rollback())
	}
	return rollback, nil
}

// DiscardBatch removes invisible staging files for a batch that another commit
// or compaction has already consumed.
func (p *ParquetStore) DiscardBatch(id string) error {
	var discardErr error
	changed := make([]string, 0, 3)
	for _, signal := range []string{"spans", "logs", "metrics"} {
		path := filepath.Join(p.dir, signal, id+".parquet.pending")
		if err := os.Remove(path); err == nil {
			changed = append(changed, signal)
		} else if !errors.Is(err, os.ErrNotExist) {
			discardErr = errors.Join(discardErr, err)
		}
	}
	return errors.Join(discardErr, syncParquetSignalDirectories(p.dir, changed))
}

func syncParquetSignalDirectories(root string, signals []string) error {
	seen := make(map[string]struct{}, len(signals))
	var syncErr error
	for _, signal := range signals {
		if _, ok := seen[signal]; ok {
			continue
		}
		seen[signal] = struct{}{}
		syncErr = errors.Join(syncErr, syncDirectory(filepath.Join(root, signal)))
	}
	return syncErr
}

func writeParquet[T any](path string, columns []parquetColumn[T], rows []T) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fields := make([]arrow.Field, len(columns))
	for i, column := range columns {
		fields[i] = arrow.Field{Name: column.name, Type: column.typeInfo, Nullable: column.nullable}
	}
	schema := arrow.NewSchema(fields, nil)
	builder := array.NewRecordBuilder(memory.DefaultAllocator, schema)
	defer builder.Release()
	for _, row := range rows {
		for i, column := range columns {
			if err := appendArrowValue(builder.Field(i), column.value(row)); err != nil {
				return fmt.Errorf("append parquet column %s: %w", column.name, err)
			}
		}
	}
	record := builder.NewRecordBatch()
	defer record.Release()

	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	writer, err := pqarrow.NewFileWriter(
		schema,
		f,
		parquet.NewWriterProperties(parquet.WithCompression(compress.Codecs.Zstd)),
		pqarrow.NewArrowWriterProperties(pqarrow.WithStoreSchema()),
	)
	if err != nil {
		return err
	}
	if err := writer.Write(record); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	_ = f.Close() // pqarrow may already have closed the sink.
	syncFile, err := os.OpenFile(tmp, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := syncFile.Sync(); err != nil {
		_ = syncFile.Close()
		return err
	}
	if err := syncFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	ok = true
	return nil
}

func appendArrowValue(builder array.Builder, value any) error {
	if value == nil {
		builder.AppendNull()
		return nil
	}
	switch b := builder.(type) {
	case *array.StringBuilder:
		b.Append(value.(string))
	case *array.Int64Builder:
		b.Append(value.(int64))
	case *array.Float64Builder:
		b.Append(value.(float64))
	case *array.TimestampBuilder:
		b.Append(arrow.Timestamp(value.(int64)))
	default:
		return fmt.Errorf("unsupported Arrow builder %T", builder)
	}
	return nil
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func text(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func jsonText(v []byte) any {
	if len(v) == 0 {
		return nil
	}
	return string(v)
}

func nanos(primary, secondary, ingested int64) any {
	for _, value := range []int64{primary, secondary, ingested} {
		if value > 0 {
			return value
		}
	}
	return nil
}

func optionalNanos(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func optionalInt(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func spanParquetColumns() []parquetColumn[Span] {
	s, i, f, ts := arrow.BinaryTypes.String, arrow.PrimitiveTypes.Int64, arrow.PrimitiveTypes.Float64, arrow.FixedWidthTypes.Timestamp_ns
	return []parquetColumn[Span]{
		{"namespace", s, false, func(r Span) any { return r.Namespace }},
		{"trace_id", s, false, func(r Span) any { return r.TraceID }},
		{"span_id", s, false, func(r Span) any { return r.SpanID }},
		{"parent_span_id", s, true, func(r Span) any { return text(r.ParentSpanID) }},
		{"service", s, false, func(r Span) any { return r.ServiceName }},
		{"operation", s, false, func(r Span) any { return r.Name }},
		{"kind", s, false, func(r Span) any { return r.Kind }},
		{"start_time", ts, true, func(r Span) any { return nanos(r.StartUnixNanos, 0, r.IngestedAt) }},
		{"end_time", ts, true, func(r Span) any { return optionalNanos(r.EndUnixNanos) }},
		{"start_unix_nano", i, false, func(r Span) any { return r.StartUnixNanos }},
		{"end_unix_nano", i, false, func(r Span) any { return r.EndUnixNanos }},
		{"duration_ms", f, false, func(r Span) any { return r.DurationMS }},
		{"status", s, false, func(r Span) any { return r.StatusCode }},
		{"status_message", s, true, func(r Span) any { return text(r.StatusMsg) }},
		{"resource_json", s, true, func(r Span) any { return jsonText(r.ResourceJSON) }},
		{"attributes_json", s, true, func(r Span) any { return jsonText(r.AttributesJSON) }},
		{"events_json", s, true, func(r Span) any { return jsonText(r.EventsJSON) }},
		{"links_json", s, true, func(r Span) any { return jsonText(r.LinksJSON) }},
		{"trace_state", s, true, func(r Span) any { return text(r.TraceState) }},
		{"flags", i, false, func(r Span) any { return int64(r.Flags) }},
		{"scope_name", s, true, func(r Span) any { return text(r.ScopeName) }},
		{"scope_version", s, true, func(r Span) any { return text(r.ScopeVersion) }},
		{"ingested_at", ts, true, func(r Span) any { return optionalNanos(r.IngestedAt) }},
		{"ingested_unix_nano", i, false, func(r Span) any { return r.IngestedAt }},
		{"http_method", s, true, func(r Span) any { return text(r.HTTPMethod) }},
		{"http_status_code", s, true, func(r Span) any { return text(r.HTTPStatusCode) }},
		{"http_route", s, true, func(r Span) any { return text(r.HTTPRoute) }},
		{"db_system", s, true, func(r Span) any { return text(r.DBSystem) }},
		{"rpc_method", s, true, func(r Span) any { return text(r.RPCMethod) }},
		{"rpc_service", s, true, func(r Span) any { return text(r.RPCService) }},
		{"peer_service", s, true, func(r Span) any { return text(r.PeerService) }},
		{"service_version", s, true, func(r Span) any { return text(r.ServiceVersion) }},
		{"deployment_env", s, true, func(r Span) any { return text(r.DeploymentEnv) }},
		{"exception_type", s, true, func(r Span) any { return text(r.ExceptionType) }},
		{"exception_message", s, true, func(r Span) any { return text(r.ExceptionMessage) }},
	}
}

func logParquetColumns() []parquetColumn[Log] {
	s, i, ts := arrow.BinaryTypes.String, arrow.PrimitiveTypes.Int64, arrow.FixedWidthTypes.Timestamp_ns
	return []parquetColumn[Log]{
		{"namespace", s, false, func(r Log) any { return r.Namespace }},
		{"log_time", ts, true, func(r Log) any { return nanos(r.TimeUnixNanos, r.ObservedTimeNanos, r.IngestedAt) }},
		{"observed_time", ts, true, func(r Log) any { return nanos(r.ObservedTimeNanos, r.TimeUnixNanos, r.IngestedAt) }},
		{"time_unix_nano", i, false, func(r Log) any { return r.TimeUnixNanos }},
		{"observed_time_unix_nano", i, true, func(r Log) any { return optionalInt(r.ObservedTimeNanos) }},
		{"severity", s, false, func(r Log) any { return r.Severity }},
		{"severity_number", i, false, func(r Log) any { return int64(r.SeverityNumber) }},
		{"body", s, false, func(r Log) any { return r.Body }},
		{"service", s, true, func(r Log) any { return text(r.ServiceName) }},
		{"trace_id", s, true, func(r Log) any { return text(r.TraceID) }},
		{"span_id", s, true, func(r Log) any { return text(r.SpanID) }},
		{"flags", i, false, func(r Log) any { return int64(r.Flags) }},
		{"resource_json", s, true, func(r Log) any { return jsonText(r.ResourceJSON) }},
		{"attributes_json", s, true, func(r Log) any { return jsonText(r.AttributesJSON) }},
		{"scope_name", s, true, func(r Log) any { return text(r.ScopeName) }},
		{"scope_version", s, true, func(r Log) any { return text(r.ScopeVersion) }},
		{"ingested_at", ts, true, func(r Log) any { return optionalNanos(r.IngestedAt) }},
		{"ingested_unix_nano", i, false, func(r Log) any { return r.IngestedAt }},
		{"body_template", s, true, func(r Log) any { return text(r.BodyTemplate) }},
	}
}

func metricParquetColumns() []parquetColumn[Metric] {
	s, i, f, ts := arrow.BinaryTypes.String, arrow.PrimitiveTypes.Int64, arrow.PrimitiveTypes.Float64, arrow.FixedWidthTypes.Timestamp_ns
	return []parquetColumn[Metric]{
		{"namespace", s, false, func(r Metric) any { return r.Namespace }},
		{"metric_time", ts, true, func(r Metric) any { return nanos(r.TimeUnixNanos, 0, r.IngestedAt) }},
		{"time_unix_nano", i, false, func(r Metric) any { return r.TimeUnixNanos }},
		{"name", s, false, func(r Metric) any { return r.Name }},
		{"description", s, true, func(r Metric) any { return text(r.Description) }},
		{"unit", s, true, func(r Metric) any { return text(r.Unit) }},
		{"metric_type", s, false, func(r Metric) any { return r.Type }},
		{"service", s, true, func(r Metric) any { return text(r.ServiceName) }},
		{"value", f, false, func(r Metric) any { return r.Value }},
		{"hist_bounds_json", s, true, func(r Metric) any { return jsonText(r.HistBoundsJSON) }},
		{"hist_counts_json", s, true, func(r Metric) any { return jsonText(r.HistCountsJSON) }},
		{"hist_count", i, true, func(r Metric) any { return optionalInt(r.HistCount) }},
		{"hist_sum", f, false, func(r Metric) any { return r.HistSum }},
		{"exemplars_json", s, true, func(r Metric) any { return jsonText(r.ExemplarsJSON) }},
		{"attributes_json", s, true, func(r Metric) any { return jsonText(r.AttributesJSON) }},
		{"resource_json", s, true, func(r Metric) any { return jsonText(r.ResourceJSON) }},
		{"scope_name", s, true, func(r Metric) any { return text(r.ScopeName) }},
		{"scope_version", s, true, func(r Metric) any { return text(r.ScopeVersion) }},
		{"ingested_at", ts, true, func(r Metric) any { return optionalNanos(r.IngestedAt) }},
		{"ingested_unix_nano", i, false, func(r Metric) any { return r.IngestedAt }},
	}
}
