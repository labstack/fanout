//go:build readbench

package panel

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
	"github.com/zeebo/xxh3"
)

type panelBenchData struct {
	Dir                                string `json:"-"`
	SourceHash                         string
	SourceStart, SourceEnd, Start, End time.Time
	PeriodHours, Volume                int
	Spans, Logs, Metrics               int64
	Files                              int
	Bytes                              int64
	Services, Namespaces               []string
}

// Do not open the controller's repository: Open can clean staging and rewrite
// its schema batch. Validate every metadata record before copying anything.
func panelReplayMetadata(input string) ([]telemetry.BatchMetadata, error) {
	entries, err := os.ReadDir(filepath.Join(input, "parquet", "batches"))
	if err != nil {
		return nil, err
	}
	var out []telemetry.BatchMetadata
	for _, entry := range entries {
		if entry.Name() == telemetry.SchemaBatch || !strings.HasSuffix(entry.Name(), telemetry.BatchSuffix) {
			continue
		}
		if !entry.IsDir() {
			return nil, fmt.Errorf("batch is not a directory: %s", entry.Name())
		}
		raw, err := os.ReadFile(filepath.Join(input, "parquet", "batches", entry.Name(), "metadata.json"))
		if err != nil {
			return nil, err
		}
		var m telemetry.BatchMetadata
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		if m.Version != 3 {
			return nil, fmt.Errorf("%w: version %d", telemetry.ErrUnsupportedBatchFormat, m.Version)
		}
		if err := telemetry.ValidateBatchID(m.ID); err != nil {
			return nil, err
		}
		if entry.Name() != m.ID+telemetry.BatchSuffix || m.Spans < 0 || m.Logs < 0 || m.Metrics < 0 || m.Spans+m.Logs+m.Metrics == 0 {
			return nil, errors.New("invalid replay metadata")
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil, errors.New("replay contains no format-3 batches")
	}
	return out, nil
}

func panelBenchScratch(tb testing.TB) string {
	tb.Helper()
	root, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			tb.Fatal("cannot locate workspace root")
		}
		root = parent
	}
	base := filepath.Join(root, ".superpowers", "panel-bench")
	if err := os.MkdirAll(base, 0o755); err != nil {
		tb.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "scratch-")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			tb.Error(err)
		}
	})
	return dir
}

func preparePanelReplay(tb testing.TB, input string, volume int) panelBenchData {
	tb.Helper()
	metadata, err := panelReplayMetadata(input)
	if err != nil {
		tb.Fatal(err)
	}
	if volume != 1 && volume != 10 && volume != 2 {
		tb.Fatal("invalid replay volume")
	}
	root := panelBenchScratch(tb)
	copied := filepath.Join(root, "input")
	hash := sha256.New()
	for _, m := range metadata {
		files := []string{"metadata.json"}
		if m.Spans > 0 {
			files = append(files, "spans.parquet", "trace.fidx")
		}
		if m.Logs > 0 {
			files = append(files, "logs.parquet")
		}
		if m.Metrics > 0 {
			files = append(files, "metrics.parquet")
		}
		for _, name := range files {
			rel := filepath.Join("parquet", "batches", m.ID+telemetry.BatchSuffix, name)
			if err := os.MkdirAll(filepath.Dir(filepath.Join(copied, rel)), 0o755); err != nil {
				tb.Fatal(err)
			}
			in, err := os.Open(filepath.Join(input, rel))
			if err != nil {
				tb.Fatal(err)
			}
			info, err := in.Stat()
			if err != nil || !info.Mode().IsRegular() {
				in.Close()
				tb.Fatal("replay input must contain regular files")
			}
			out, err := os.Create(filepath.Join(copied, rel))
			if err != nil {
				in.Close()
				tb.Fatal(err)
			}
			fmt.Fprintln(hash, filepath.ToSlash(rel))
			_, copyErr := io.Copy(io.MultiWriter(out, hash), in)
			if err := errors.Join(copyErr, out.Close(), in.Close()); err != nil {
				tb.Fatal(err)
			}
		}
	}
	repo, err := telemetrystore.Open(copied)
	if err != nil {
		tb.Fatal(err)
	}
	defer repo.Close()
	low, high := int64(math.MaxInt64), int64(math.MinInt64)
	for _, m := range repo.Parquet.BatchMetadata() {
		for _, r := range []telemetry.TimeRange{m.SpanTime, m.LogTime, m.MetricTime} {
			if r.Known {
				low = min(low, r.MinNanos)
				high = max(high, r.MaxNanos)
			}
		}
	}
	if low > high {
		tb.Fatal("replay has no known event-time footer bounds")
	}
	// Repeat the complete UTC-hour envelope for short captures. Longer captures
	// use their newest complete 24 hours, without compressing event time or
	// multiplying the demo's per-hour traffic. Edge hours are clipped exactly.
	end := time.Unix(0, high).UTC().Truncate(time.Hour).Add(time.Hour)
	start := time.Unix(0, low).UTC().Truncate(time.Hour)
	period := min(24*time.Hour, end.Sub(start))
	start = end.Add(-period)
	data := panelBenchData{Dir: filepath.Join(root, "state"), SourceHash: fmt.Sprintf("%x", hash.Sum(nil)), SourceStart: time.Unix(0, low).UTC(), SourceEnd: time.Unix(0, high).UTC(), Start: end.Add(-24 * time.Hour), End: end, PeriodHours: int(period / time.Hour), Volume: volume}
	services, namespaces := map[string]bool{}, map[string]bool{}
	for tile, at := 0, data.Start; at.Before(end); tile, at = tile+1, at.Add(period) {
		for copyIndex := range volume {
			for _, m := range metadata {
				prefix := fmt.Sprintf("tile%02d_copy%02d_", tile, copyIndex)
				dst := filepath.Join(data.Dir, "telemetry", "parquet", "batches", prefix+m.ID+telemetry.BatchSuffix)
				if err := os.MkdirAll(dst, 0o755); err != nil {
					tb.Fatal(err)
				}
				next := telemetry.BatchMetadata{Version: 3, ID: prefix + m.ID, Generation: m.Generation}
				for _, signal := range []string{"spans", "logs", "metrics"} {
					count := map[string]int{"spans": m.Spans, "logs": m.Logs, "metrics": m.Metrics}[signal]
					if count == 0 {
						continue
					}
					path := filepath.Join(copied, "parquet", "batches", m.ID+telemetry.BatchSuffix, signal+".parquet")
					err := tilePanelParquet(path, filepath.Join(dst, signal+".parquet"), signal, prefix, start, start.Add(min(period, end.Sub(at))), at.Sub(start), &next, services, namespaces)
					if err != nil {
						tb.Fatal(err)
					}
					written := map[string]int{"spans": next.Spans, "logs": next.Logs, "metrics": next.Metrics}[signal]
					if written == 0 {
						if err := os.Remove(filepath.Join(dst, signal+".parquet")); err != nil {
							tb.Fatal(err)
						}
					}
				}
				if next.Spans+next.Logs+next.Metrics == 0 {
					if err := os.RemoveAll(dst); err != nil {
						tb.Fatal(err)
					}
					continue
				}
				if next.Spans > 0 {
					if err := panelTraceIndex(dst); err != nil {
						tb.Fatal(err)
					}
				}
				raw, err := json.Marshal(next)
				if err != nil {
					tb.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dst, "metadata.json"), raw, 0o644); err != nil {
					tb.Fatal(err)
				}
				data.Spans += int64(next.Spans)
				data.Logs += int64(next.Logs)
				data.Metrics += int64(next.Metrics)
			}
		}
		tb.Logf("replay tile=%d volume=%d prepared_spans=%d prepared_logs=%d", tile, volume, data.Spans, data.Logs)
	}
	for service := range services {
		data.Services = append(data.Services, service)
	}
	sort.Strings(data.Services)
	for namespace := range namespaces {
		data.Namespaces = append(data.Namespaces, namespace)
	}
	sort.Strings(data.Namespaces)
	if len(data.Services) == 0 || data.Spans == 0 || data.Logs == 0 {
		tb.Fatal("representative replay needs populated spans/logs/services")
	}
	if err := filepath.WalkDir(filepath.Join(data.Dir, "telemetry"), func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		data.Files++
		data.Bytes += info.Size()
		return nil
	}); err != nil {
		tb.Fatal(err)
	}
	// Leave a bounded, reproducible manifest in the private scratch state.
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data.Dir, "manifest.json"), raw, 0o644); err != nil {
		tb.Fatal(err)
	}
	tb.Logf("manifest=%s", raw)
	return data
}

// Raw row rewriting retains the exact physical schema and all VARIANT bytes.
// The disk-backed sorting writer bounds setup memory even for compacted input.
func tilePanelParquet(input, output, signal, prefix string, start, end time.Time, shift time.Duration, m *telemetry.BatchMetadata, services, namespaces map[string]bool) (err error) {
	in, err := os.Open(input)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	reader := parquet.NewReader(in)
	defer func() { err = errors.Join(err, reader.Close()) }()
	cols := map[string]int{}
	for i, path := range reader.Schema().Columns() {
		if len(path) == 1 {
			cols[path[0]] = i
		}
	}
	event := map[string]string{"spans": "start_time", "logs": "log_time", "metrics": "metric_time"}[signal]
	timeColumns := map[int]string{}
	for _, name := range []string{"start_time", "end_time", "start_unix_nano", "end_unix_nano", "log_time", "observed_time", "time_unix_nano", "observed_time_unix_nano", "metric_time", "ingested_at", "ingested_unix_nano"} {
		if column, ok := cols[name]; ok {
			timeColumns[column] = name
		}
	}
	identityColumns := map[int]bool{}
	for _, name := range []string{"trace_id", "span_id", "parent_span_id"} {
		if column, ok := cols[name]; ok {
			identityColumns[column] = true
		}
	}
	out, err := os.Create(output)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	options := []parquet.WriterOption{reader.Schema(), parquet.Compression(&zstd.Codec{Level: zstd.SpeedFastest, Concurrency: 1}), parquet.MaxRowsPerRowGroup(50000), parquet.PageBufferSize(64 << 10)}
	type rowWriter interface {
		WriteRows([]parquet.Row) (int, error)
		Close() error
	}
	var writer rowWriter
	if signal == "spans" {
		options = append(options, parquet.SortingWriterConfig(parquet.SortingColumns(parquet.Ascending("_trace_hash"), parquet.Ascending("start_unix_nano"), parquet.Ascending("span_id")), parquet.SortingBuffers(parquet.NewFileBufferPool(filepath.Dir(output), "sort-*"))))
		writer = parquet.NewSortingWriter[any](out, 8192, options...)
	} else {
		writer = parquet.NewGenericWriter[any](out, options...)
	}
	defer func() { err = errors.Join(err, writer.Close()) }()
	rows := make([]parquet.Row, 256)
	for {
		n, readErr := reader.ReadRows(rows)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		kept := make([]parquet.Row, 0, n)
		for _, row := range rows[:n] {
			at := panelRowValue(row, cols[event]).Int64()
			if at < start.UnixNano() || at >= end.UnixNano() {
				continue
			}
			service := panelRowValue(row, cols["service"]).String()
			if service != "" {
				services[service] = true
			}
			namespaces[panelRowValue(row, cols["namespace"]).String()] = true
			for i, value := range row {
				column := value.Column()
				if name, ok := timeColumns[column]; ok && !value.IsNull() {
					v := value.Int64()
					// Zero optional raw timestamps remain absent; native event
					// timestamps, including signed and zero instants, are shifted.
					if v != 0 || name == event || name == "end_time" || name == "start_time" {
						row[i] = parquet.Int64Value(v+int64(shift)).Level(value.RepetitionLevel(), value.DefinitionLevel(), column)
					}
				}
				if identityColumns[column] && value.String() != "" {
					row[i] = parquet.ByteArrayValue([]byte(prefix+value.String())).Level(value.RepetitionLevel(), value.DefinitionLevel(), column)
				}
			}
			if signal == "spans" {
				for i, value := range row {
					if value.Column() == cols["_trace_hash"] {
						row[i] = parquet.Int64Value(int64(xxh3.HashString(panelRowValue(row, cols["trace_id"]).String()))).Level(0, 0, value.Column())
					}
				}
				if m.Spans == 0 {
					m.MinSpanStartNanos = at + int64(shift)
					m.MaxSpanStartNanos = at + int64(shift)
				}
				m.MinSpanStartNanos = min(m.MinSpanStartNanos, at+int64(shift))
				m.MaxSpanStartNanos = max(m.MaxSpanStartNanos, at+int64(shift))
				m.Spans++
			} else if signal == "logs" {
				m.Logs++
			} else {
				m.Metrics++
			}
			ingested := panelRowValue(row, cols["ingested_unix_nano"]).Int64()
			if m.Spans+m.Logs+m.Metrics == 1 {
				m.MinIngestedNanos, m.MaxIngestedNanos = ingested, ingested
			}
			m.MinIngestedNanos = min(m.MinIngestedNanos, ingested)
			m.MaxIngestedNanos = max(m.MaxIngestedNanos, ingested)
			kept = append(kept, row)
		}
		if _, err := writer.WriteRows(kept); err != nil {
			return err
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	return nil
}

func panelRowValue(row parquet.Row, column int) parquet.Value {
	for _, v := range row {
		if v.Column() == column {
			return v
		}
	}
	return parquet.Value{}
}

// Same format-3 sidecar contract as the production writer, streamed from the
// sorted output. No private production symbol or new product API is needed.
func panelTraceIndex(dir string) (err error) {
	in, err := os.Open(filepath.Join(dir, "spans.parquet"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	reader := parquet.NewGenericReader[struct {
		Hash uint64 `parquet:"_trace_hash"`
	}](in)
	defer func() { err = errors.Join(err, reader.Close()) }()
	out, err := os.Create(filepath.Join(dir, "trace.fidx"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	if _, err := out.Write(make([]byte, 16)); err != nil {
		return err
	}
	var hash, begin, count, entries, rowNumber uint64
	flush := func() error {
		var buf [24]byte
		binary.LittleEndian.PutUint64(buf[:8], hash)
		binary.LittleEndian.PutUint64(buf[8:16], begin)
		binary.LittleEndian.PutUint64(buf[16:], count)
		_, err := out.Write(buf[:])
		entries++
		return err
	}
	buffer := make([]struct {
		Hash uint64 `parquet:"_trace_hash"`
	}, 8192)
	for {
		n, readErr := reader.Read(buffer)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		for _, r := range buffer[:n] {
			if count > 0 && r.Hash != hash {
				if err := flush(); err != nil {
					return err
				}
				count = 0
			}
			if count == 0 {
				hash, begin = r.Hash, rowNumber
			}
			count++
			rowNumber++
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	if count > 0 {
		if err := flush(); err != nil {
			return err
		}
	}
	var header [16]byte
	copy(header[:8], "FANIDX02")
	binary.LittleEndian.PutUint64(header[8:], entries)
	_, err = out.WriteAt(header[:], 0)
	return err
}

func panelBenchEngine(tb testing.TB, data panelBenchData) (*query.Duck, config.Config) {
	tb.Helper()
	// Load validates authentication even though this harness never serves HTTP.
	// Only a dummy test auth value is supplied; storage sizing stays automatic.
	cfg, err := config.Load(config.LoadOptions{Environ: []string{"FANOUT_DATA_DIR=" + data.Dir, "FANOUT_AUTH_CODE_SECRET=" + strings.Repeat("benchmark-only-", 3)}})
	if err != nil {
		tb.Fatal(err)
	}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		tb.Fatal(err)
	}
	engine, err := query.NewDuck(tb.Context(), cfg, repo)
	if err != nil {
		repo.Close()
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := errors.Join(engine.Close(), repo.Close()); err != nil {
			tb.Error(err)
		}
	})
	return engine, cfg
}

func assertPanelReplay(t *testing.T, data panelBenchData, want int64) {
	t.Helper()
	engine, _ := panelBenchEngine(t, data)
	var count, distinct int64
	if err := engine.DB.QueryRowContext(t.Context(), "SELECT count(*),count(DISTINCT trace_id) FROM spans WHERE start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS", data.Start, data.End).Scan(&count, &distinct); err != nil {
		t.Fatal(err)
	}
	if count != want || distinct != want {
		t.Fatalf("count=%d distinct=%d want=%d", count, distinct, want)
	}
	var value int64
	if err := engine.DB.QueryRowContext(t.Context(), "SELECT attributes['literal.dot']::BIGINT FROM spans LIMIT 1").Scan(&value); err != nil || value != 7 {
		t.Fatalf("typed dotted attribute=%d error=%v", value, err)
	}
}
