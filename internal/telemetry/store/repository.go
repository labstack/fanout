// Package store owns Fanout's authoritative telemetry commit path: a
// replayable ingest WAL, immutable hot segments, and open Parquet files.
package store

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/labstack/fanout/internal/telemetry"
	"github.com/labstack/fanout/internal/telemetry/segment"
)

type Batch struct {
	ID      string
	Spans   []telemetry.Span
	Logs    []telemetry.Log
	Metrics []telemetry.Metric
}

type batchMetadata struct {
	ID         string `json:"id"`
	MinNanos   int64  `json:"min_nanos"`
	MaxNanos   int64  `json:"max_nanos"`
	Generation uint32 `json:"generation"`
	// Sources retains the raw ingest batch IDs folded into a compacted output
	// by its own compaction pass. It is a one-generation replay ledger: a stale
	// WAL can never resurrect rows already present in this output, and earlier
	// generations need no entries because their WALs were removed durably when
	// their own compaction completed.
	Sources []string `json:"sources,omitempty"`
}

type repositoryManifest struct {
	Version        uint32          `json:"version"`
	HotCutoffNanos int64           `json:"hot_cutoff_nanos"`
	Batches        []batchMetadata `json:"batches"`
}

type Repository struct {
	mu sync.RWMutex
	// hotMu makes the persisted prune watermark and the hot-segment snapshot one
	// atomic read boundary. A query can never observe an old watermark after the
	// corresponding segments have been retired.
	hotMu        sync.RWMutex
	commitMu     sync.Mutex
	compactionMu sync.Mutex
	root         string
	walDir       string
	Spans        *segment.Store
	Logs         *segment.SignalStore[telemetry.Log]
	Metrics      *segment.SignalStore[telemetry.Metric]
	Parquet      *telemetry.ParquetStore
	manifest     repositoryManifest
}

func Open(root string) (*Repository, error) {
	legacyCatalog := filepath.Join(root, "ducklake.sqlite")
	if _, err := os.Stat(legacyCatalog); err == nil {
		return nil, fmt.Errorf("legacy DuckLake catalog %s is unsupported; start Fanout with a clean storage.data_dir", legacyCatalog)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect legacy DuckLake catalog: %w", err)
	}
	walDir := filepath.Join(root, "wal")
	for _, dir := range []string{root, walDir, filepath.Join(root, "hot", "spans"), filepath.Join(root, "hot", "logs"), filepath.Join(root, "hot", "metrics")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	spans, err := segment.Open(filepath.Join(root, "hot", "spans"))
	if err != nil {
		return nil, err
	}
	logs, err := segment.OpenSignalStore[telemetry.Log](filepath.Join(root, "hot", "logs"), "EventUnixNanos")
	if err != nil {
		_ = spans.Close()
		return nil, err
	}
	metricsStore, err := segment.OpenSignalStore[telemetry.Metric](filepath.Join(root, "hot", "metrics"), "EventUnixNanos")
	if err != nil {
		_ = logs.Close()
		_ = spans.Close()
		return nil, err
	}
	parquet, err := telemetry.OpenParquetStore(filepath.Join(root, "parquet"))
	if err != nil {
		_ = metricsStore.Close()
		_ = logs.Close()
		_ = spans.Close()
		return nil, err
	}
	r := &Repository{root: root, walDir: walDir, Spans: spans, Logs: logs, Metrics: metricsStore, Parquet: parquet}
	if err := r.loadManifest(); err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("load telemetry manifest: %w", err)
	}
	if err := r.recoverCompaction(); err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("recover parquet compaction: %w", err)
	}
	if err := r.recover(); err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("recover telemetry WAL: %w", err)
	}
	return r, nil
}

func (r *Repository) Close() error {
	return errors.Join(r.Spans.Close(), r.Logs.Close(), r.Metrics.Close())
}

// PruneHot removes acceleration segments older than cutoff. Parquet remains
// authoritative for longer retention and SQL queries.
func (r *Repository) PruneHot(cutoff int64) (int, error) {
	r.hotMu.Lock()
	defer r.hotMu.Unlock()

	// Publish the boundary before retiring segments. A crash or partial prune can
	// therefore create only harmless overlap (Parquet below the boundary and hot
	// segments above it), never a hole after restart.
	publishCutoff := func() error {
		r.commitMu.Lock()
		defer r.commitMu.Unlock()
		r.mu.Lock()
		defer r.mu.Unlock()
		if cutoff > r.manifest.HotCutoffNanos {
			next := r.manifest
			next.HotCutoffNanos = cutoff
			if err := writeRepositoryManifest(r.root, next); err != nil {
				return err
			}
			r.manifest = next
		}
		return nil
	}
	if err := publishCutoff(); err != nil {
		return 0, fmt.Errorf("publish hot prune cutoff: %w", err)
	}

	spans, spanErr := r.Spans.PruneBefore(cutoff)
	logs, logErr := r.Logs.PruneBefore(cutoff)
	metricRows, metricErr := r.Metrics.PruneBefore(cutoff)
	return spans + logs + metricRows, errors.Join(spanErr, logErr, metricErr)
}

// ScanHotLogs reads the portion of [start,end) that is guaranteed complete in
// the hot tier and returns the durable boundary below which Parquet is
// authoritative. The boundary and scan are serialized with PruneHot.
func (r *Repository) ScanHotLogs(start, end int64, visit func(telemetry.Log) bool) (int64, error) {
	r.hotMu.RLock()
	defer r.hotMu.RUnlock()
	r.mu.RLock()
	cutoff := r.manifest.HotCutoffNanos
	r.mu.RUnlock()
	return cutoff, r.Logs.Scan(max(start, cutoff), end, visit)
}

// HotTrace returns the hot trace snapshot and the durable prune boundary that
// was in force for that snapshot.
func (r *Repository) HotTrace(traceID string) ([]telemetry.Span, int64, error) {
	r.hotMu.RLock()
	defer r.hotMu.RUnlock()
	r.mu.RLock()
	cutoff := r.manifest.HotCutoffNanos
	r.mu.RUnlock()
	spans, err := r.Spans.Trace(traceID)
	return spans, cutoff, err
}

// PruneParquet removes complete ingest batches older than cutoff. A batch that
// straddles the boundary is retained intact, so retention never removes newer
// telemetry from another signal in the same atomic commit.
func (r *Repository) PruneParquet(cutoff int64) (int, error) {
	r.commitMu.Lock()
	defer r.commitMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := make([]batchMetadata, 0, len(r.manifest.Batches))
	removed := 0
	var removeErr error
	for _, batch := range r.manifest.Batches {
		if batch.MaxNanos <= 0 || batch.MaxNanos >= cutoff {
			kept = append(kept, batch)
			continue
		}
		batchOK := true
		for _, signal := range []string{"spans", "logs", "metrics"} {
			path := filepath.Join(r.Parquet.Dir(), signal, batch.ID+".parquet")
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				removeErr = errors.Join(removeErr, err)
				batchOK = false
			}
		}
		if batchOK {
			removed++
		} else {
			kept = append(kept, batch)
		}
	}
	if removed == 0 {
		return 0, removeErr
	}
	if err := syncParquetDirectories(r.Parquet.Dir()); err != nil {
		return 0, errors.Join(removeErr, err)
	}
	next := repositoryManifest{Version: 1, HotCutoffNanos: r.manifest.HotCutoffNanos, Batches: kept}
	if err := writeRepositoryManifest(r.root, next); err != nil {
		return 0, errors.Join(removeErr, err)
	}
	r.manifest = next
	return removed, removeErr
}

// Commit durably records a batch and publishes its three signal projections
// exactly once. A crash at any point leaves the WAL for replay on next boot.
func (r *Repository) Commit(batch Batch) error {
	if batch.ID == "" || strings.ContainsAny(batch.ID, `/\\`) {
		return errors.New("telemetry batch requires a safe ID")
	}
	normalizeBatch(&batch)
	// Commits are serialized, but their segment and Parquet fsyncs do not hold
	// the repository metadata lock. Each projection has its own atomic publish
	// protocol; the WAL keeps a partially applied transaction replayable.
	r.commitMu.Lock()
	defer r.commitMu.Unlock()
	consumed := r.batchConsumedLocked(batch.ID)
	if consumed {
		return r.removeWAL(batch.ID)
	}
	if err := r.writeWAL(batch); err != nil {
		return err
	}
	err := r.apply(batch)
	if err == nil {
		r.mu.Lock()
		err = r.recordBatch(batch)
		r.mu.Unlock()
	}
	if err != nil {
		return err
	}
	return r.removeWAL(batch.ID)
}

// Stage durably records a batch in the WAL without publishing its projections.
// Writers call this before handing a batch to an asynchronous commit worker, so
// every queued or in-flight batch is replayable if shutdown interrupts retries.
func (r *Repository) Stage(batch Batch) error {
	if batch.ID == "" || strings.ContainsAny(batch.ID, `/\\`) {
		return errors.New("telemetry batch requires a safe ID")
	}
	normalizeBatch(&batch)
	r.commitMu.Lock()
	defer r.commitMu.Unlock()
	consumed := r.batchConsumedLocked(batch.ID)
	if consumed {
		return r.removeWAL(batch.ID)
	}
	return r.writeWAL(batch)
}

func (r *Repository) apply(batch Batch) error {
	if err := r.Spans.AppendID(batch.ID, batch.Spans); err != nil {
		return fmt.Errorf("commit span segment: %w", err)
	}
	if err := r.Logs.Append(batch.ID, batch.Logs); err != nil {
		return fmt.Errorf("commit log segment: %w", err)
	}
	if err := r.Metrics.Append(batch.ID, batch.Metrics); err != nil {
		return fmt.Errorf("commit metric segment: %w", err)
	}
	if err := r.Parquet.WriteSpans(batch.ID, batch.Spans); err != nil {
		return fmt.Errorf("commit span parquet: %w", err)
	}
	if err := r.Parquet.WriteLogs(batch.ID, batch.Logs); err != nil {
		return fmt.Errorf("commit log parquet: %w", err)
	}
	if err := r.Parquet.WriteMetrics(batch.ID, batch.Metrics); err != nil {
		return fmt.Errorf("commit metric parquet: %w", err)
	}
	return nil
}

func (r *Repository) writeWAL(batch Batch) error {
	final := filepath.Join(r.walDir, batch.ID+".wal")
	if _, err := os.Stat(final); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var plain bytes.Buffer
	if err := gob.NewEncoder(&plain).Encode(batch); err != nil {
		return err
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderCRC(true), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return err
	}
	data := enc.EncodeAll(plain.Bytes(), nil)
	enc.Close()
	tmp := final + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
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
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	return syncDirectory(r.walDir)
}

func (r *Repository) recover() error {
	entries, err := os.ReadDir(r.walDir)
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".wal") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return err
	}
	defer dec.Close()
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(r.walDir, name))
		if err != nil {
			return err
		}
		plain, err := dec.DecodeAll(data, nil)
		if err != nil {
			if quarantineErr := quarantineWAL(r.walDir, name, err); quarantineErr != nil {
				return quarantineErr
			}
			continue
		}
		var batch Batch
		if err := gob.NewDecoder(bytes.NewReader(plain)).Decode(&batch); err != nil {
			if quarantineErr := quarantineWAL(r.walDir, name, err); quarantineErr != nil {
				return quarantineErr
			}
			continue
		}
		if r.batchConsumed(batch.ID) {
			if err := r.removeWAL(batch.ID); err != nil {
				return fmt.Errorf("remove consumed replay %s: %w", name, err)
			}
			continue
		}
		// A batch that cannot be applied would otherwise abort every boot. Move
		// it aside so the instance starts; if quarantine itself fails the
		// environment is broken, and failing loudly is then the right answer.
		if err := r.apply(batch); err != nil {
			if quarantineErr := quarantineWAL(r.walDir, name, err); quarantineErr != nil {
				return errors.Join(fmt.Errorf("replay %s: %w", name, err), quarantineErr)
			}
			continue
		}
		if err := r.recordBatch(batch); err != nil {
			if quarantineErr := quarantineWAL(r.walDir, name, err); quarantineErr != nil {
				return errors.Join(fmt.Errorf("record replayed %s: %w", name, err), quarantineErr)
			}
			continue
		}
		if err := os.Remove(filepath.Join(r.walDir, name)); err != nil {
			return err
		}
	}
	return syncDirectory(r.walDir)
}

func quarantineWAL(dir, name string, cause error) error {
	source := filepath.Join(dir, name)
	target := source + ".corrupt"
	if _, err := os.Stat(target); err == nil {
		target = fmt.Sprintf("%s.%d", target, time.Now().UnixNano())
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect WAL quarantine target: %w", err)
	}
	if err := os.Rename(source, target); err != nil {
		return fmt.Errorf("quarantine corrupt WAL %s: %w", name, err)
	}
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("sync WAL quarantine: %w", err)
	}
	slog.Error("quarantined corrupt telemetry WAL", "file", filepath.Base(target), "error", cause)
	return nil
}

func (r *Repository) loadManifest() error {
	path := filepath.Join(r.root, "MANIFEST.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		r.manifest = repositoryManifest{Version: 1}
		return writeRepositoryManifest(r.root, r.manifest)
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &r.manifest); err != nil {
		return err
	}
	if r.manifest.Version != 1 {
		return fmt.Errorf("unsupported telemetry manifest version %d", r.manifest.Version)
	}
	return nil
}

func (r *Repository) recordBatch(batch Batch) error {
	for _, existing := range r.manifest.Batches {
		if existing.ID == batch.ID {
			return nil
		}
		for _, source := range existing.Sources {
			if source == batch.ID {
				return nil
			}
		}
	}
	next := r.manifest
	next.Batches = append(append([]batchMetadata(nil), r.manifest.Batches...), batchMetadata{ID: batch.ID, MinNanos: batchMinNanos(batch), MaxNanos: batchMaxNanos(batch)})
	if err := writeRepositoryManifest(r.root, next); err != nil {
		return err
	}
	r.manifest = next
	return nil
}

func (r *Repository) batchConsumed(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.batchConsumedLocked(id)
}

func (r *Repository) batchConsumedLocked(id string) bool {
	for _, batch := range r.manifest.Batches {
		for _, source := range batch.Sources {
			if source == id {
				return true
			}
		}
	}
	return false
}

func (r *Repository) removeWAL(id string) error {
	if err := os.Remove(filepath.Join(r.walDir, id+".wal")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove committed telemetry WAL: %w", err)
	}
	return syncDirectory(r.walDir)
}

func batchMaxNanos(batch Batch) int64 {
	var maxNanos int64
	for _, row := range batch.Spans {
		maxNanos = max(maxNanos, max(row.StartUnixNanos, row.IngestedAt))
	}
	for _, row := range batch.Logs {
		maxNanos = max(maxNanos, max(row.EventUnixNanos, row.IngestedAt))
	}
	for _, row := range batch.Metrics {
		maxNanos = max(maxNanos, max(row.EventUnixNanos, row.IngestedAt))
	}
	return maxNanos
}

func batchMinNanos(batch Batch) int64 {
	minNanos := int64(math.MaxInt64)
	include := func(value int64) {
		if value > 0 {
			minNanos = min(minNanos, value)
		}
	}
	for _, row := range batch.Spans {
		include(row.StartUnixNanos)
		include(row.IngestedAt)
	}
	for _, row := range batch.Logs {
		include(row.EventUnixNanos)
		include(row.IngestedAt)
	}
	for _, row := range batch.Metrics {
		include(row.EventUnixNanos)
		include(row.IngestedAt)
	}
	if minNanos == math.MaxInt64 {
		return 0
	}
	return minNanos
}

func writeRepositoryManifest(root string, manifest repositoryManifest) error {
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	tmp := filepath.Join(root, "MANIFEST.json.tmp")
	final := filepath.Join(root, "MANIFEST.json")
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	return syncDirectory(root)
}

func normalizeBatch(batch *Batch) {
	for i := range batch.Spans {
		batch.Spans[i].Namespace = telemetry.NormalizeNamespace(batch.Spans[i].Namespace)
		if batch.Spans[i].StartUnixNanos == 0 {
			// Mirror the Parquet start_time coalesce so hot segments and cold SQL
			// key a zero-start span on the same instant.
			batch.Spans[i].StartUnixNanos = batch.Spans[i].IngestedAt
		}
	}
	for i := range batch.Logs {
		batch.Logs[i].Namespace = telemetry.NormalizeNamespace(batch.Logs[i].Namespace)
		if batch.Logs[i].EventUnixNanos == 0 {
			batch.Logs[i].EventUnixNanos = firstNonzero(batch.Logs[i].TimeUnixNanos, batch.Logs[i].ObservedTimeNanos, batch.Logs[i].IngestedAt)
		}
	}
	for i := range batch.Metrics {
		batch.Metrics[i].Namespace = telemetry.NormalizeNamespace(batch.Metrics[i].Namespace)
		if batch.Metrics[i].EventUnixNanos == 0 {
			batch.Metrics[i].EventUnixNanos = firstNonzero(batch.Metrics[i].TimeUnixNanos, batch.Metrics[i].IngestedAt)
		}
	}
}

func firstNonzero(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return err
	}
	return nil
}
