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

const (
	maxBatchRows              = 50_000
	walDecoderMaxMemory       = 128 << 20
	repositoryVersion         = 2
	manifestCheckpointRecords = 4_096
)

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
	Epoch          uint64          `json:"epoch"`
	HotCutoffNanos int64           `json:"hot_cutoff_nanos"`
	Batches        []batchMetadata `json:"batches"`
}

type repositoryJournalRecord struct {
	Epoch uint64        `json:"epoch"`
	Batch batchMetadata `json:"batch"`
}

type Repository struct {
	mu sync.RWMutex
	// hotMu makes the persisted prune watermark and the hot-segment snapshot one
	// atomic read boundary. A query can never observe an old watermark after the
	// corresponding segments have been retired.
	hotMu          sync.RWMutex
	stageMu        sync.Mutex
	commitMu       sync.Mutex
	compactionMu   sync.Mutex
	parquetPublish sync.Locker
	root           string
	walDir         string
	Spans          *segment.Store
	Parquet        *telemetry.ParquetStore
	manifest       repositoryManifest
	consumed       map[string]struct{}
	journal        *os.File
	journalRecords int
}

// SetParquetPublishLock connects repository publication to the query engine's
// read gate. It must be called during startup, before the commit worker runs.
func (r *Repository) SetParquetPublishLock(lock sync.Locker) {
	r.parquetPublish = lock
}

func Open(root string) (*Repository, error) {
	walDir := filepath.Join(root, "wal")
	for _, dir := range []string{root, walDir, filepath.Join(root, "hot", "spans")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	spans, err := openHotStore(root)
	hotRebuilt := false
	if err != nil {
		quarantine := filepath.Join(root, fmt.Sprintf("hot.corrupt-%d", time.Now().UnixNano()))
		if renameErr := os.Rename(filepath.Join(root, "hot"), quarantine); renameErr != nil {
			return nil, errors.Join(fmt.Errorf("open hot telemetry tier: %w", err), fmt.Errorf("quarantine corrupt hot tier: %w", renameErr))
		}
		if syncErr := syncDirectory(root); syncErr != nil {
			return nil, fmt.Errorf("sync quarantined hot tier: %w", syncErr)
		}
		spans, err = openHotStore(root)
		if err != nil {
			return nil, fmt.Errorf("rebuild hot telemetry tier: %w", err)
		}
		hotRebuilt = true
		slog.Warn("corrupt hot telemetry tier quarantined and rebuilt from authoritative Parquet", "path", quarantine)
	}
	parquet, err := telemetry.OpenParquetStore(filepath.Join(root, "parquet"))
	if err != nil {
		_ = spans.Close()
		return nil, err
	}
	r := &Repository{root: root, walDir: walDir, Spans: spans, Parquet: parquet}
	if err := r.loadManifest(); err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("load telemetry manifest: %w", err)
	}
	if hotRebuilt {
		r.manifest.HotCutoffNanos = max(r.manifest.HotCutoffNanos, time.Now().UnixNano())
		if err := r.checkpointManifestLocked(r.manifest); err != nil {
			_ = r.Close()
			return nil, fmt.Errorf("publish rebuilt hot-tier cutoff: %w", err)
		}
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

func openHotStore(root string) (*segment.Store, error) {
	return segment.Open(filepath.Join(root, "hot", "spans"))
}

func (r *Repository) Close() error {
	var journalErr error
	if r.journal != nil {
		journalErr = r.journal.Close()
		r.journal = nil
	}
	return errors.Join(journalErr, r.Spans.Close())
}

// PruneHot removes acceleration segments older than cutoff. Parquet remains
// authoritative for longer retention and SQL queries.
func (r *Repository) PruneHot(cutoff int64) (int, error) {
	r.commitMu.Lock()
	defer r.commitMu.Unlock()
	r.hotMu.Lock()
	defer r.hotMu.Unlock()

	// Publish the boundary before retiring segments. A crash or partial prune can
	// therefore create only harmless overlap (Parquet below the boundary and hot
	// segments above it), never a hole after restart.
	publishCutoff := func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		if cutoff > r.manifest.HotCutoffNanos {
			next := cloneRepositoryManifest(r.manifest)
			next.HotCutoffNanos = cutoff
			if err := r.checkpointManifestLocked(next); err != nil {
				return err
			}
		}
		return nil
	}
	if err := publishCutoff(); err != nil {
		return 0, fmt.Errorf("publish hot prune cutoff: %w", err)
	}

	return r.Spans.PruneBefore(cutoff)
}

// CompactHot drains committed raw span-index segments into larger immutable
// files. It intentionally excludes any segment not present in the
// repository manifest, because that file may belong to a partially applied WAL
// transaction that still needs exact-ID replay.
func (r *Repository) CompactHot(maxInputs int) (int, error) {
	if maxInputs < 2 {
		return 0, nil
	}
	r.mu.RLock()
	committed := make(map[string]struct{}, len(r.manifest.Batches))
	for _, batch := range r.manifest.Batches {
		committed[batch.ID] = struct{}{}
		for _, source := range batch.Sources {
			committed[source] = struct{}{}
		}
	}
	r.mu.RUnlock()
	total := 0
	var compactErr error
	for {
		n, err := r.Spans.CompactCommitted(committed, maxInputs)
		total += n
		compactErr = errors.Join(compactErr, err)
		if err != nil || n < 2 {
			break
		}
	}
	return total, compactErr
}

// HotTrace returns the hot trace snapshot and the durable prune boundary that
// was in force for that snapshot.
func (r *Repository) HotTrace(traceID string, scopeStartNanos int64) ([]telemetry.Span, int64, error) {
	r.hotMu.RLock()
	defer r.hotMu.RUnlock()
	r.mu.RLock()
	cutoff := r.manifest.HotCutoffNanos
	r.mu.RUnlock()
	if scopeStartNanos < cutoff {
		return nil, cutoff, nil
	}
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
	next := repositoryManifest{Version: repositoryVersion, HotCutoffNanos: r.manifest.HotCutoffNanos, Batches: kept}
	if err := r.checkpointManifestLocked(next); err != nil {
		return 0, errors.Join(removeErr, err)
	}
	return removed, removeErr
}

// Commit durably records a batch and publishes its three signal projections
// exactly once. A crash at any point leaves the WAL for replay on next boot.
func (r *Repository) Commit(batch Batch) error {
	normalizeBatch(&batch)
	if err := validateBatch(batch); err != nil {
		return err
	}
	r.stageMu.Lock()
	err := r.writeWAL(batch)
	r.stageMu.Unlock()
	if err != nil {
		return err
	}
	// Parquet encoding and fsync happen before either the query publication gate
	// or commit mutex is acquired. Only the final renames and manifest append are
	// serialized with readers and maintenance.
	if err := r.Parquet.StageBatch(batch.ID, batch.Spans, batch.Logs, batch.Metrics); err != nil {
		return err
	}
	if r.parquetPublish != nil {
		r.parquetPublish.Lock()
		defer r.parquetPublish.Unlock()
	}
	r.commitMu.Lock()
	defer r.commitMu.Unlock()
	if r.batchConsumedLocked(batch.ID) {
		return errors.Join(r.Parquet.DiscardBatch(batch.ID), r.removeWAL(batch.ID))
	}
	err = r.publish(batch)
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
	normalizeBatch(&batch)
	if err := validateBatch(batch); err != nil {
		return err
	}
	// WAL publication is independent from projection publication. Keeping this
	// lock separate lets the next OTLP request become durable while the commit
	// worker writes span indexes and Parquet for an earlier request.
	r.stageMu.Lock()
	defer r.stageMu.Unlock()
	consumed := r.batchConsumed(batch.ID)
	if consumed {
		return r.removeWAL(batch.ID)
	}
	return r.writeWAL(batch)
}

// validateBatch rejects a batch no projection could ever publish. The segment
// stores name their files after the batch ID, so an ID they would refuse must
// be caught before the WAL promises to replay it forever.
func validateBatch(batch Batch) error {
	if batch.ID == "" || strings.ContainsAny(batch.ID, `/\\`) {
		return errors.New("telemetry batch requires a safe ID")
	}
	if !segment.ValidID(batch.ID) {
		return fmt.Errorf("telemetry batch ID %q cannot name a segment", batch.ID)
	}
	if rows := len(batch.Spans) + len(batch.Logs) + len(batch.Metrics); rows > maxBatchRows {
		return fmt.Errorf("telemetry batch has %d rows; maximum is %d", rows, maxBatchRows)
	}
	if err := segment.ValidateSpanRows(batch.Spans); err != nil {
		return fmt.Errorf("telemetry batch cannot be represented by the hot span tier: %w", err)
	}
	return nil
}

func (r *Repository) publish(batch Batch) error {
	r.hotMu.Lock()
	defer r.hotMu.Unlock()
	rollback, err := r.Parquet.PublishBatch(batch.ID, len(batch.Spans) > 0, len(batch.Logs) > 0, len(batch.Metrics) > 0)
	if err != nil {
		return fmt.Errorf("publish parquet batch: %w", err)
	}
	if err := r.Spans.AppendID(batch.ID, batch.Spans); err != nil {
		return errors.Join(fmt.Errorf("commit span segment: %w", err), rollback())
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
	data, err := encodeWALBatch(batch, walDecoderMaxMemory)
	if err != nil {
		return err
	}
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

func encodeWALBatch(batch Batch, maxDecodedBytes int) ([]byte, error) {
	var plain bytes.Buffer
	if err := gob.NewEncoder(&plain).Encode(batch); err != nil {
		return nil, err
	}
	if plain.Len() > maxDecodedBytes {
		return nil, fmt.Errorf("telemetry batch encodes to %d bytes; maximum is %d", plain.Len(), maxDecodedBytes)
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderCRC(true), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	data := enc.EncodeAll(plain.Bytes(), nil)
	enc.Close()
	return data, nil
}

// newWALDecoder builds the bounded decoder every WAL read goes through.
func newWALDecoder() (*zstd.Decoder, error) {
	return newWALDecoderWithLimit(walDecoderMaxMemory)
}

func newWALDecoderWithLimit(limit uint64) (*zstd.Decoder, error) {
	return zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(limit))
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
	dec, err := newWALDecoder()
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
		// Poison is decided before anything is published: a payload the
		// projections can never accept is moved aside so it cannot abort every
		// boot. A publication or manifest failure after that point is environmental,
		// so the WAL is retained and startup fails loudly — a later healthy boot
		// must still be able to finish the batch, including one whose projection
		// prefix this attempt already published.
		normalizeBatch(&batch)
		if err := validateBatch(batch); err != nil {
			if quarantineErr := quarantineWAL(r.walDir, name, err); quarantineErr != nil {
				return errors.Join(fmt.Errorf("replay %s: %w", name, err), quarantineErr)
			}
			continue
		}
		if err := r.Parquet.StageBatch(batch.ID, batch.Spans, batch.Logs, batch.Metrics); err != nil {
			return fmt.Errorf("stage replay %s: %w", name, err)
		}
		if err := r.publish(batch); err != nil {
			return fmt.Errorf("replay %s: %w", name, err)
		}
		if err := r.recordBatch(batch); err != nil {
			return fmt.Errorf("record replayed %s: %w", name, err)
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
		r.manifest = repositoryManifest{Version: repositoryVersion, Epoch: 1}
		if err := writeRepositoryManifest(r.root, r.manifest); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		if err := json.Unmarshal(data, &r.manifest); err != nil {
			return err
		}
		if r.manifest.Version != repositoryVersion || r.manifest.Epoch == 0 {
			return fmt.Errorf("unsupported telemetry manifest version %d epoch %d", r.manifest.Version, r.manifest.Epoch)
		}
	}
	r.rebuildConsumedLocked()

	journalPath := filepath.Join(r.root, "MANIFEST.log")
	journalData, err := os.ReadFile(journalPath)
	journalNew := errors.Is(err, os.ErrNotExist)
	if err != nil && !journalNew {
		return err
	}
	validBytes := 0
	for validBytes < len(journalData) {
		relativeEnd := bytes.IndexByte(journalData[validBytes:], '\n')
		if relativeEnd < 0 {
			break
		}
		lineStart := validBytes
		lineEnd := lineStart + relativeEnd
		line := journalData[lineStart:lineEnd]
		validBytes = lineEnd + 1
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record repositoryJournalRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return fmt.Errorf("decode telemetry manifest journal at byte %d: %w", lineStart, err)
		}
		if record.Epoch != r.manifest.Epoch || r.batchConsumedLocked(record.Batch.ID) {
			continue
		}
		if record.Batch.ID == "" || !segment.ValidID(record.Batch.ID) {
			return fmt.Errorf("telemetry manifest journal contains invalid batch ID %q", record.Batch.ID)
		}
		r.manifest.Batches = append(r.manifest.Batches, record.Batch)
		r.addConsumedLocked(record.Batch)
		r.journalRecords++
	}
	r.journal, err = os.OpenFile(journalPath, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	if validBytes != len(journalData) {
		if err := r.journal.Truncate(int64(validBytes)); err != nil {
			return fmt.Errorf("truncate partial telemetry manifest journal: %w", err)
		}
		if err := r.journal.Sync(); err != nil {
			return fmt.Errorf("sync repaired telemetry manifest journal: %w", err)
		}
	}
	if journalNew {
		return syncDirectory(r.root)
	}
	return nil
}

func (r *Repository) recordBatch(batch Batch) error {
	if r.batchConsumedLocked(batch.ID) {
		return nil
	}
	metadata := batchMetadata{ID: batch.ID, MinNanos: batchMinNanos(batch), MaxNanos: batchMaxNanos(batch)}
	line, err := json.Marshal(repositoryJournalRecord{Epoch: r.manifest.Epoch, Batch: metadata})
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if r.journal == nil {
		return errors.New("telemetry manifest journal is closed")
	}
	if _, err := r.journal.Write(line); err != nil {
		return fmt.Errorf("append telemetry manifest journal: %w", err)
	}
	if err := r.journal.Sync(); err != nil {
		return fmt.Errorf("sync telemetry manifest journal: %w", err)
	}
	r.manifest.Batches = append(r.manifest.Batches, metadata)
	r.addConsumedLocked(metadata)
	r.journalRecords++
	if r.journalRecords >= manifestCheckpointRecords {
		if err := r.checkpointManifestLocked(r.manifest); err != nil {
			return fmt.Errorf("checkpoint telemetry manifest journal: %w", err)
		}
	}
	return nil
}

func (r *Repository) checkpointManifestLocked(next repositoryManifest) error {
	next = cloneRepositoryManifest(next)
	next.Version = repositoryVersion
	next.Epoch = max(next.Epoch, r.manifest.Epoch+1)
	if err := writeRepositoryManifest(r.root, next); err != nil {
		return err
	}
	r.manifest = next
	r.rebuildConsumedLocked()
	r.journalRecords = 0
	if r.journal == nil {
		return nil
	}
	if err := r.journal.Truncate(0); err != nil {
		return fmt.Errorf("truncate telemetry manifest journal: %w", err)
	}
	if _, err := r.journal.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind telemetry manifest journal: %w", err)
	}
	if err := r.journal.Sync(); err != nil {
		return fmt.Errorf("sync telemetry manifest journal checkpoint: %w", err)
	}
	return nil
}

func cloneRepositoryManifest(manifest repositoryManifest) repositoryManifest {
	clone := manifest
	clone.Batches = append([]batchMetadata(nil), manifest.Batches...)
	for i := range clone.Batches {
		clone.Batches[i].Sources = append([]string(nil), manifest.Batches[i].Sources...)
	}
	return clone
}

func (r *Repository) rebuildConsumedLocked() {
	r.consumed = make(map[string]struct{}, len(r.manifest.Batches))
	for _, batch := range r.manifest.Batches {
		r.addConsumedLocked(batch)
	}
}

func (r *Repository) addConsumedLocked(batch batchMetadata) {
	if r.consumed == nil {
		r.consumed = make(map[string]struct{})
	}
	r.consumed[batch.ID] = struct{}{}
	for _, source := range batch.Sources {
		r.consumed[source] = struct{}{}
	}
}

func (r *Repository) batchConsumed(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.batchConsumedLocked(id)
}

func (r *Repository) batchConsumedLocked(id string) bool {
	_, exists := r.consumed[id]
	return exists
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
			// Mirror the Parquet start_time coalesce so hot segments and SQL scans
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
