package telemetry

import (
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
	"github.com/zeebo/xxh3"
)

const (
	BatchSuffix          = ".batch"
	SchemaBatch          = "_schema" + BatchSuffix
	batchMetadataVersion = 2
	parquetPageSize      = 64 << 10
	parquetRowGroupRows  = 50_000
	maxTraceQueryResults = 500
)

var syncPublishedDirectory = syncDirectory

type BatchMetadata struct {
	Version           uint32 `json:"version"`
	ID                string `json:"id"`
	MinIngestedNanos  int64  `json:"min_ingested_nanos"`
	MaxIngestedNanos  int64  `json:"max_ingested_nanos"`
	MinSpanStartNanos int64  `json:"min_span_start_nanos"`
	MaxSpanStartNanos int64  `json:"max_span_start_nanos"`
	Generation        uint32 `json:"generation"`
	Spans             int    `json:"spans"`
	Logs              int    `json:"logs"`
	Metrics           int    `json:"metrics"`
}

type TraceQuery struct {
	TraceID    string
	Namespace  string
	StartNanos int64
	EndNanos   int64
	Limit      int
}

type storedBatch struct {
	metadata BatchMetadata
	dir      string
	traces   traceIndex
}

type ParquetStore struct {
	dir        string
	batchesDir string
	stagingDir string
	mu         sync.RWMutex
	publishMu  sync.Mutex
	batches    map[string]*storedBatch
}

type ParquetStats struct {
	Files int
	Bytes int64
}

func OpenParquetStore(dir string) (*ParquetStore, error) {
	p := &ParquetStore{
		dir: dir, batchesDir: filepath.Join(dir, "batches"), stagingDir: filepath.Join(dir, "staging"),
		batches: make(map[string]*storedBatch),
	}
	for _, path := range []string{p.dir, p.batchesDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return nil, err
		}
	}
	// Staging is never acknowledged or queried. Removing it is the complete
	// recovery protocol for writes interrupted before atomic publication.
	if err := os.RemoveAll(p.stagingDir); err != nil {
		return nil, fmt.Errorf("clear incomplete Parquet batches: %w", err)
	}
	if err := os.Mkdir(p.stagingDir, 0o755); err != nil {
		return nil, err
	}
	if err := p.ensureSchemaBatch(); err != nil {
		return nil, err
	}
	if err := p.loadBatches(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *ParquetStore) Close() error       { return nil }
func (p *ParquetStore) Dir() string        { return p.dir }
func (p *ParquetStore) BatchesDir() string { return p.batchesDir }

func (p *ParquetStore) Pattern(signal string) string {
	return filepath.ToSlash(filepath.Join(p.batchesDir, "*"+BatchSuffix, signal+".parquet"))
}

func (p *ParquetStore) BatchPath(id string) string {
	return filepath.Join(p.batchesDir, id+BatchSuffix)
}

func (p *ParquetStore) StagingPath(id string) string {
	return filepath.Join(p.stagingDir, id)
}

func (p *ParquetStore) BatchMetadata() []BatchMetadata {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]BatchMetadata, 0, len(p.batches))
	for _, batch := range p.batches {
		out = append(out, batch.metadata)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Generation != out[j].Generation {
			return out[i].Generation < out[j].Generation
		}
		if out[i].MinIngestedNanos != out[j].MinIngestedNanos {
			return out[i].MinIngestedNanos < out[j].MinIngestedNanos
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (p *ParquetStore) RowCount() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var count uint64
	for _, batch := range p.batches {
		count += uint64(batch.metadata.Spans + batch.metadata.Logs + batch.metadata.Metrics)
	}
	return count
}

func (p *ParquetStore) CommitBatch(metadata BatchMetadata, spans []Span, logs []Log, metrics []Metric) error {
	if err := validateBatchID(metadata.ID); err != nil {
		return err
	}
	metadata.Version = batchMetadataVersion
	metadata.Spans, metadata.Logs, metadata.Metrics = len(spans), len(logs), len(metrics)
	final := p.BatchPath(metadata.ID)
	if p.hasBatch(metadata.ID) {
		return nil
	}
	if info, err := os.Stat(final); err == nil && info.IsDir() {
		if err := syncDirectory(p.batchesDir); err != nil {
			return err
		}
		return p.registerBatch(final)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	stage := filepath.Join(p.stagingDir, metadata.ID)
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := os.Mkdir(stage, 0o755); err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(stage)
		}
	}()

	if len(spans) > 0 {
		rows := make([]spanParquetRow, len(spans))
		for i := range spans {
			rows[i] = makeSpanParquetRow(spans[i])
			rows[i].TraceHash = xxh3.HashString(rows[i].TraceID)
			if rows[i].StartUnixNano > 0 && (metadata.MinSpanStartNanos == 0 || rows[i].StartUnixNano < metadata.MinSpanStartNanos) {
				metadata.MinSpanStartNanos = rows[i].StartUnixNano
			}
			metadata.MaxSpanStartNanos = max(metadata.MaxSpanStartNanos, rows[i].StartUnixNano)
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].TraceHash != rows[j].TraceHash {
				return rows[i].TraceHash < rows[j].TraceHash
			}
			if rows[i].StartUnixNano != rows[j].StartUnixNano {
				return rows[i].StartUnixNano < rows[j].StartUnixNano
			}
			return rows[i].SpanID < rows[j].SpanID
		})
		if err := writeTypedParquet(filepath.Join(stage, "spans.parquet"), rows, parquetPageSize); err != nil {
			return fmt.Errorf("write span Parquet: %w", err)
		}
		if err := writeTraceIndex(filepath.Join(stage, "trace.fidx"), rows); err != nil {
			return fmt.Errorf("write trace index: %w", err)
		}
	}
	if len(logs) > 0 {
		rows := make([]logParquetRow, len(logs))
		for i := range logs {
			rows[i] = makeLogParquetRow(logs[i])
		}
		if err := writeTypedParquet(filepath.Join(stage, "logs.parquet"), rows, parquetPageSize); err != nil {
			return fmt.Errorf("write log Parquet: %w", err)
		}
	}
	if len(metrics) > 0 {
		rows := make([]metricParquetRow, len(metrics))
		for i := range metrics {
			rows[i] = makeMetricParquetRow(metrics[i])
		}
		if err := writeTypedParquet(filepath.Join(stage, "metrics.parquet"), rows, parquetPageSize); err != nil {
			return fmt.Errorf("write metric Parquet: %w", err)
		}
	}
	if err := writeJSONFile(filepath.Join(stage, "metadata.json"), metadata); err != nil {
		return err
	}
	if err := syncDirectory(stage); err != nil {
		return err
	}

	p.publishMu.Lock()
	defer p.publishMu.Unlock()
	if p.hasBatch(metadata.ID) {
		complete = true
		return os.RemoveAll(stage)
	}
	if err := os.Rename(stage, final); err != nil {
		if info, statErr := os.Stat(final); statErr == nil && info.IsDir() {
			complete = true
			if err := syncDirectory(p.batchesDir); err != nil {
				return err
			}
			return p.registerBatch(final)
		}
		return fmt.Errorf("publish Parquet batch: %w", err)
	}
	complete = true
	if err := syncDirectory(p.batchesDir); err != nil {
		return err
	}
	return p.registerBatch(final)
}

// CleanupRetired removes inputs hidden by a completed retention or compaction
// publication. It is called only after compaction recovery has consumed its
// durable marker, so no rollback can still need these directories.
func (p *ParquetStore) CleanupRetired() error {
	entries, err := os.ReadDir(p.batchesDir)
	if err != nil {
		return err
	}
	removed := false
	var cleanupErr error
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasSuffix(name, BatchSuffix) || !strings.Contains(name, ".retired") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(p.batchesDir, name)); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		} else {
			removed = true
		}
	}
	if removed {
		cleanupErr = errors.Join(cleanupErr, syncDirectory(p.batchesDir))
	}
	return cleanupErr
}

// RestoreRetiredInputs rolls back a compaction whose durable output vanished.
// The complete namespace change is hidden from readers by publish.
func (p *ParquetStore) RestoreRetiredInputs(inputs []string, replacementID string, publish func(func() error) error) error {
	if err := validateBatchID(replacementID); err != nil {
		return err
	}
	for _, id := range inputs {
		if err := validateBatchID(id); err != nil {
			return err
		}
	}
	type restoredInput struct {
		id      string
		active  string
		retired string
		batch   *storedBatch
		move    bool
	}
	prepared := make([]restoredInput, 0, len(inputs))
	for _, id := range inputs {
		active := p.BatchPath(id)
		path := active
		move := false
		if _, err := os.Stat(active); errors.Is(err, os.ErrNotExist) {
			path = filepath.Join(p.batchesDir, id+".retired-"+replacementID)
			move = true
		} else if err != nil {
			return err
		}
		batch, err := loadStoredBatch(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("input %s is missing", id)
			}
			return fmt.Errorf("load input %s: %w", id, err)
		}
		batch.dir = active
		if batch.metadata.Spans > 0 {
			batch.traces.path = filepath.Join(active, "trace.fidx")
		}
		prepared = append(prepared, restoredInput{id: id, active: active, retired: path, batch: batch, move: move})
	}
	return publish(func() error {
		p.publishMu.Lock()
		defer p.publishMu.Unlock()
		installActive := func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			for _, input := range prepared {
				if _, err := os.Stat(input.active); err == nil {
					p.batches[input.id] = input.batch
				} else {
					delete(p.batches, input.id)
				}
			}
		}

		moved := make([]restoredInput, 0, len(prepared))
		for _, input := range prepared {
			if !input.move {
				continue
			}
			if err := os.Rename(input.retired, input.active); err != nil {
				var rollbackErr error
				for i := len(moved) - 1; i >= 0; i-- {
					rollbackErr = errors.Join(rollbackErr, os.Rename(moved[i].active, moved[i].retired))
				}
				installActive()
				return errors.Join(err, rollbackErr, syncDirectory(p.batchesDir))
			}
			moved = append(moved, input)
		}
		var syncErr error
		if len(moved) > 0 {
			syncErr = syncDirectory(p.batchesDir)
		}
		installActive()
		return syncErr
	})
}

// Trace reads only ranges selected by the persistent hash index. Scope filters
// and the limit are applied while decoding so a pathological trace cannot grow
// request memory without bound.
func (p *ParquetStore) Trace(ctx context.Context, query TraceQuery) ([]IndexedSpan, error) {
	if query.TraceID == "" {
		return nil, nil
	}
	if query.Limit <= 0 {
		return nil, errors.New("trace query limit must be positive")
	}
	if query.Limit > maxTraceQueryResults {
		return nil, fmt.Errorf("trace query limit exceeds %d", maxTraceQueryResults)
	}
	if query.StartNanos >= query.EndNanos {
		return nil, errors.New("trace query time range must be positive")
	}
	hash := xxh3.HashString(query.TraceID)
	p.mu.RLock()
	batches := make([]*storedBatch, 0, len(p.batches))
	for _, batch := range p.batches {
		batches = append(batches, batch)
	}
	p.mu.RUnlock()
	selected := make(indexedSpanHeap, 0, query.Limit)
	for _, batch := range batches {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if batch.metadata.MaxSpanStartNanos > 0 &&
			(batch.metadata.MaxSpanStartNanos < query.StartNanos || batch.metadata.MinSpanStartNanos >= query.EndNanos) {
			continue
		}
		match, found, err := batch.traces.Lookup(hash)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		if err := readIndexedTrace(ctx, batch, match, query, &selected); err != nil {
			return nil, err
		}
	}
	out := []IndexedSpan(selected)
	sort.Slice(out, func(i, j int) bool {
		return indexedSpanEarlier(out[i], out[j])
	})
	return out, nil
}

func readIndexedTrace(ctx context.Context, batch *storedBatch, match traceRange, query TraceQuery, selected *indexedSpanHeap) (err error) {
	if match.row > uint64(math.MaxInt64) {
		return errors.New("trace index row exceeds Parquet reader limit")
	}
	file, err := os.Open(filepath.Join(batch.dir, "spans.parquet"))
	if err != nil {
		return err
	}
	reader := parquet.NewGenericReader[indexedSpanParquetRow](file)
	defer func() { err = errors.Join(err, reader.Close(), file.Close()) }()
	if err := reader.SeekToRow(int64(match.row)); err != nil {
		return err
	}
	remaining := match.count
	buffer := make([]indexedSpanParquetRow, min(uint64(8192), remaining))
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		want := min(uint64(len(buffer)), remaining)
		n, readErr := reader.Read(buffer[:int(want)])
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
		for i := range n {
			row := buffer[i]
			if row.StartUnixNano >= query.EndNanos {
				return nil
			}
			if row.TraceID != query.TraceID || row.StartUnixNano < query.StartNanos ||
				(query.Namespace != "" && row.Namespace != query.Namespace) {
				continue
			}
			selected.Add(row.span(), query.Limit)
		}
		remaining -= uint64(n)
	}
	return nil
}

type indexedSpanHeap []IndexedSpan

func (h indexedSpanHeap) Len() int { return len(h) }
func (h indexedSpanHeap) Less(i, j int) bool {
	return indexedSpanEarlier(h[j], h[i])
}
func (h indexedSpanHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *indexedSpanHeap) Push(value any) {
	*h = append(*h, value.(IndexedSpan))
}
func (h *indexedSpanHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}

func (h *indexedSpanHeap) Add(span IndexedSpan, limit int) {
	if len(*h) < limit {
		heap.Push(h, span)
		return
	}
	if indexedSpanEarlier(span, (*h)[0]) {
		(*h)[0] = span
		heap.Fix(h, 0)
	}
}

func indexedSpanEarlier(left, right IndexedSpan) bool {
	if left.StartUnixNanos != right.StartUnixNanos {
		return left.StartUnixNanos < right.StartUnixNanos
	}
	if left.DurationMS != right.DurationMS {
		return left.DurationMS > right.DurationMS
	}
	return left.SpanID < right.SpanID
}

// PruneBefore hides complete batches while readers are pinned, then deletes
// the retired directories after publication.
func (p *ParquetStore) PruneBefore(cutoff int64, maxBatches int, publish func(func() error) error) (int, error) {
	if maxBatches <= 0 {
		return 0, nil
	}
	type candidate struct {
		id  string
		max int64
	}
	p.mu.RLock()
	candidates := make([]candidate, 0, len(p.batches))
	for id, batch := range p.batches {
		if batch.metadata.MaxIngestedNanos > 0 && batch.metadata.MaxIngestedNanos < cutoff {
			candidates = append(candidates, candidate{id: id, max: batch.metadata.MaxIngestedNanos})
		}
	}
	p.mu.RUnlock()
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].max != candidates[j].max {
			return candidates[i].max < candidates[j].max
		}
		return candidates[i].id < candidates[j].id
	})
	if len(candidates) > maxBatches {
		candidates = candidates[:maxBatches]
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	var retired []string
	var pruneErr error
	err := publish(func() error {
		p.publishMu.Lock()
		defer p.publishMu.Unlock()
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, candidate := range candidates {
			batch, exists := p.batches[candidate.id]
			if !exists {
				continue
			}
			path := filepath.Join(p.batchesDir, candidate.id+".retired")
			if err := os.Rename(batch.dir, path); err != nil {
				pruneErr = errors.Join(pruneErr, err)
				continue
			}
			delete(p.batches, candidate.id)
			retired = append(retired, path)
		}
		if len(retired) > 0 {
			pruneErr = errors.Join(pruneErr, syncDirectory(p.batchesDir))
		}
		return nil
	})
	pruneErr = errors.Join(pruneErr, err)
	for _, path := range retired {
		pruneErr = errors.Join(pruneErr, os.RemoveAll(path))
	}
	if len(retired) > 0 {
		pruneErr = errors.Join(pruneErr, syncDirectory(p.batchesDir))
	}
	return len(retired), pruneErr
}

func (p *ParquetStore) Stats() (map[string]ParquetStats, error) {
	p.mu.RLock()
	dirs := make([]string, 0, len(p.batches))
	for _, batch := range p.batches {
		dirs = append(dirs, batch.dir)
	}
	p.mu.RUnlock()
	stats := map[string]ParquetStats{"spans": {}, "logs": {}, "metrics": {}}
	for _, dir := range dirs {
		for signal := range stats {
			info, err := os.Stat(filepath.Join(dir, signal+".parquet"))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			value := stats[signal]
			value.Files++
			value.Bytes += info.Size()
			stats[signal] = value
		}
	}
	return stats, nil
}

// PrepareReplacement completes metadata and the trace sidecar for Parquet
// files produced by the compactor.
func (p *ParquetStore) PrepareReplacement(dir string, metadata BatchMetadata) error {
	metadata.Version = batchMetadataVersion
	metadata.MinSpanStartNanos = 0
	metadata.MaxSpanStartNanos = 0
	if metadata.Spans > 0 {
		f, err := os.Open(filepath.Join(dir, "spans.parquet"))
		if err != nil {
			return err
		}
		reader := parquet.NewGenericReader[traceParquetRow](f)
		index, err := newTraceIndexWriter(filepath.Join(dir, "trace.fidx"))
		if err != nil {
			_ = reader.Close()
			_ = f.Close()
			return err
		}
		rows := 0
		buffer := make([]traceParquetRow, min(8192, metadata.Spans))
		for {
			n, readErr := reader.Read(buffer)
			for i := range n {
				if start := buffer[i].StartUnixNano; start > 0 && (metadata.MinSpanStartNanos == 0 || start < metadata.MinSpanStartNanos) {
					metadata.MinSpanStartNanos = start
				}
				metadata.MaxSpanStartNanos = max(metadata.MaxSpanStartNanos, buffer[i].StartUnixNano)
				if err := index.Append(buffer[i].TraceHash); err != nil {
					index.Abort()
					_ = reader.Close()
					_ = f.Close()
					return err
				}
			}
			rows += n
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				index.Abort()
				_ = reader.Close()
				_ = f.Close()
				return readErr
			}
			if n == 0 {
				index.Abort()
				_ = reader.Close()
				_ = f.Close()
				return io.ErrNoProgress
			}
		}
		if err := errors.Join(reader.Close(), f.Close()); err != nil {
			index.Abort()
			return err
		}
		if rows != metadata.Spans {
			index.Abort()
			return fmt.Errorf("compacted span count: got %d want %d", rows, metadata.Spans)
		}
		if err := index.Close(); err != nil {
			return err
		}
	}
	if err := writeJSONFile(filepath.Join(dir, "metadata.json"), metadata); err != nil {
		return err
	}
	return syncDirectory(dir)
}

// PublishReplacement validates a prepared compacted batch, atomically swaps it
// for its inputs while readers are pinned, then deletes retired inputs.
func (p *ParquetStore) PublishReplacement(stage string, metadata BatchMetadata, inputs []string, publish func(func() error) error) error {
	if err := validateBatchID(metadata.ID); err != nil {
		return err
	}
	for _, id := range inputs {
		if err := validateBatchID(id); err != nil {
			return err
		}
	}
	final := p.BatchPath(metadata.ID)
	source := stage
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		source = final
	} else if err != nil {
		return err
	}
	replacement, err := loadStoredBatch(source)
	if err != nil {
		return err
	}
	replacement.dir = final
	if replacement.metadata.Spans > 0 {
		replacement.traces.path = filepath.Join(final, "trace.fidx")
	}
	inputBatches := make(map[string]*storedBatch, len(inputs))
	for _, id := range inputs {
		p.mu.RLock()
		batch, exists := p.batches[id]
		p.mu.RUnlock()
		if exists {
			inputBatches[id] = batch
			continue
		}
		active := p.BatchPath(id)
		path := active
		if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
			path = filepath.Join(p.batchesDir, id+".retired-"+metadata.ID)
		} else if statErr != nil {
			return statErr
		}
		batch, loadErr := loadStoredBatch(path)
		if errors.Is(loadErr, os.ErrNotExist) {
			continue
		}
		if loadErr != nil {
			return fmt.Errorf("load compaction input %s: %w", id, loadErr)
		}
		batch.dir = active
		if batch.metadata.Spans > 0 {
			batch.traces.path = filepath.Join(active, "trace.fidx")
		}
		inputBatches[id] = batch
	}
	retired := make([][2]string, 0, len(inputs))
	err = publish(func() error {
		p.publishMu.Lock()
		defer p.publishMu.Unlock()
		p.mu.Lock()
		defer p.mu.Unlock()
		installReplacement := func() {
			for _, id := range inputs {
				delete(p.batches, id)
			}
			p.batches[metadata.ID] = replacement
		}
		rollback := func() error {
			var rollbackErr error
			for i := len(retired) - 1; i >= 0; i-- {
				rollbackErr = errors.Join(rollbackErr, os.Rename(retired[i][1], retired[i][0]))
			}
			delete(p.batches, metadata.ID)
			for id, batch := range inputBatches {
				if _, err := os.Stat(batch.dir); err == nil {
					p.batches[id] = batch
				} else {
					delete(p.batches, id)
				}
			}
			return errors.Join(rollbackErr, syncDirectory(p.batchesDir))
		}
		// Recovery may resume with the output already published. Move it back
		// to staging before touching inputs, so rollback can only expose the
		// complete old set or the complete replacement, never both.
		if source == final {
			if err := os.MkdirAll(filepath.Dir(stage), 0o755); err != nil {
				return err
			}
			if err := os.Rename(final, stage); err != nil {
				return err
			}
			source = stage
			delete(p.batches, metadata.ID)
		}
		for _, id := range inputs {
			active := p.BatchPath(id)
			retiredPath := filepath.Join(p.batchesDir, id+".retired-"+metadata.ID)
			if _, statErr := os.Stat(active); statErr == nil {
				if err := os.Rename(active, retiredPath); err != nil {
					return errors.Join(err, rollback())
				}
				retired = append(retired, [2]string{active, retiredPath})
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return errors.Join(statErr, rollback())
			} else if _, retiredErr := os.Stat(retiredPath); retiredErr == nil {
				retired = append(retired, [2]string{active, retiredPath})
			} else if !errors.Is(retiredErr, os.ErrNotExist) {
				return errors.Join(retiredErr, rollback())
			}
		}
		if source != final {
			if err := os.Rename(stage, final); err != nil {
				return errors.Join(err, rollback())
			}
			source = final
		}
		if err := syncPublishedDirectory(p.batchesDir); err != nil {
			// The namespace already contains only the replacement. Keep the
			// in-memory view consistent and let marker recovery retry the fsync.
			installReplacement()
			return err
		}
		installReplacement()
		return nil
	})
	if err != nil {
		return err
	}
	var removeErr error
	for _, pair := range retired {
		removeErr = errors.Join(removeErr, os.RemoveAll(pair[1]))
	}
	return errors.Join(removeErr, syncDirectory(p.batchesDir))
}

func (p *ParquetStore) ensureSchemaBatch() error {
	final := filepath.Join(p.batchesDir, SchemaBatch)
	if info, err := os.Stat(final); err == nil && info.IsDir() {
		return nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stage := filepath.Join(p.stagingDir, "_schema")
	if err := os.Mkdir(stage, 0o755); err != nil {
		return err
	}
	if err := writeTypedParquet(filepath.Join(stage, "spans.parquet"), []spanParquetRow{}, parquetPageSize); err != nil {
		return err
	}
	if err := writeTypedParquet(filepath.Join(stage, "logs.parquet"), []logParquetRow{}, parquetPageSize); err != nil {
		return err
	}
	if err := writeTypedParquet(filepath.Join(stage, "metrics.parquet"), []metricParquetRow{}, parquetPageSize); err != nil {
		return err
	}
	if err := syncDirectory(stage); err != nil {
		return err
	}
	if err := os.Rename(stage, final); err != nil {
		return err
	}
	return syncDirectory(p.batchesDir)
}

func (p *ParquetStore) loadBatches() error {
	entries, err := os.ReadDir(p.batchesDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == SchemaBatch || !strings.HasSuffix(entry.Name(), BatchSuffix) {
			continue
		}
		if err := p.registerBatch(filepath.Join(p.batchesDir, entry.Name())); err != nil {
			return fmt.Errorf("load Parquet batch %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (p *ParquetStore) registerBatch(dir string) error {
	batch, err := loadStoredBatch(dir)
	if err != nil {
		return err
	}
	if filepath.Base(dir) != batch.metadata.ID+BatchSuffix {
		return fmt.Errorf("parquet batch directory %q does not match metadata ID %q", filepath.Base(dir), batch.metadata.ID)
	}
	p.mu.Lock()
	p.batches[batch.metadata.ID] = batch
	p.mu.Unlock()
	return nil
}

func loadStoredBatch(dir string) (*storedBatch, error) {
	metadata, err := readBatchMetadata(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return nil, err
	}
	signals := [...]struct {
		name  string
		count int
	}{
		{name: "spans", count: metadata.Spans},
		{name: "logs", count: metadata.Logs},
		{name: "metrics", count: metadata.Metrics},
	}
	for _, signal := range signals {
		if signal.count == 0 {
			continue
		}
		path := filepath.Join(dir, signal.name+".parquet")
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s Parquet is not a regular file", signal.name)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		parquetFile, openErr := parquet.OpenFile(file, info.Size())
		closeErr := file.Close()
		if err := errors.Join(openErr, closeErr); err != nil {
			return nil, fmt.Errorf("open %s Parquet: %w", signal.name, err)
		}
		if rows := parquetFile.NumRows(); rows != int64(signal.count) {
			return nil, fmt.Errorf("%s Parquet has %d rows; metadata declares %d", signal.name, rows, signal.count)
		}
	}
	var traces traceIndex
	if metadata.Spans > 0 {
		traces, err = loadTraceIndex(filepath.Join(dir, "trace.fidx"), metadata.Spans)
		if err != nil {
			return nil, err
		}
	}
	return &storedBatch{metadata: metadata, dir: dir, traces: traces}, nil
}

func (p *ParquetStore) hasBatch(id string) bool {
	p.mu.RLock()
	_, ok := p.batches[id]
	p.mu.RUnlock()
	return ok
}

func writeTypedParquet[T any](path string, rows []T, pageSize int) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	writer := parquet.NewGenericWriter[T](f,
		parquet.Compression(&zstd.Codec{Level: zstd.SpeedFastest, Concurrency: 1}),
		parquet.MaxRowsPerRowGroup(parquetRowGroupRows), parquet.PageBufferSize(pageSize))
	if _, err := writer.Write(rows); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

func readBatchMetadata(path string) (BatchMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BatchMetadata{}, err
	}
	var metadata BatchMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return BatchMetadata{}, err
	}
	if metadata.Version != batchMetadataVersion {
		return BatchMetadata{}, fmt.Errorf("unsupported batch metadata version %d", metadata.Version)
	}
	if err := validateBatchID(metadata.ID); err != nil {
		return BatchMetadata{}, err
	}
	if metadata.Spans < 0 || metadata.Logs < 0 || metadata.Metrics < 0 {
		return BatchMetadata{}, errors.New("parquet batch metadata has a negative row count")
	}
	if metadata.MinIngestedNanos > 0 && metadata.MaxIngestedNanos > 0 && metadata.MinIngestedNanos > metadata.MaxIngestedNanos {
		return BatchMetadata{}, errors.New("parquet batch metadata has an inverted time range")
	}
	return metadata, nil
}

func writeJSONFile(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeBytesFile(path, data)
}

func writeBytesFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func validateBatchID(id string) error {
	if id == "" || len(id) > 128 || id[0] == '.' || strings.ContainsAny(id, `/\\`) {
		return fmt.Errorf("invalid telemetry batch ID %q", id)
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return fmt.Errorf("invalid telemetry batch ID %q", id)
		}
	}
	return nil
}

type traceParquetRow struct {
	TraceHash     uint64 `parquet:"_trace_hash"`
	StartUnixNano int64  `parquet:"start_unix_nano"`
}
