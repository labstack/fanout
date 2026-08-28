package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

type Batch struct {
	ID      string
	Spans   []telemetry.Span
	Logs    []telemetry.Log
	Metrics []telemetry.Metric
}

// Repository publishes self-contained Parquet batch directories. The
// directory rename is the transaction and the filesystem is the catalog.
type Repository struct {
	root         string
	Parquet      *telemetry.ParquetStore
	compactionMu sync.Mutex
}

func Open(root string) (*Repository, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	parquetStore, err := telemetry.OpenParquetStore(filepath.Join(root, "parquet"))
	if err != nil {
		return nil, err
	}
	r := &Repository{root: root, Parquet: parquetStore}
	if err := r.recoverCompaction(context.Background(), func(ctx context.Context, publish func(context.Context) error) error { return publish(ctx) }); err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("recover Parquet compaction: %w", err)
	}
	if err := r.cleanupCompactionArtifacts(); err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("clean Parquet compaction staging: %w", err)
	}
	if err := r.cleanupRetired(); err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("clean retired Parquet batches: %w", err)
	}
	return r, nil
}

func (r *Repository) cleanupCompactionArtifacts() error {
	if err := os.RemoveAll(filepath.Join(r.root, "compaction")); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(r.root, "COMPACTION.json.tmp")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(r.root)
}

func (r *Repository) Close() error { return r.Parquet.Close() }

// CleanupParquet removes retired inputs only while no retention or compaction
// transaction can still need them for rollback.
func (r *Repository) CleanupParquet() error {
	r.compactionMu.Lock()
	defer r.compactionMu.Unlock()
	return r.cleanupRetired()
}

func (r *Repository) cleanupRetired() error {
	entries, err := os.ReadDir(r.Parquet.BatchesDir())
	if err != nil {
		return err
	}
	removed := false
	var cleanupErr error
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasSuffix(name, telemetry.BatchSuffix) || !strings.Contains(name, ".retired") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(r.Parquet.BatchesDir(), name)); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		} else {
			removed = true
		}
	}
	if removed {
		cleanupErr = errors.Join(cleanupErr, syncDirectory(r.Parquet.BatchesDir()))
	}
	return cleanupErr
}

func (r *Repository) Commit(batch Batch) error {
	normalizeBatch(&batch)
	if err := validateBatch(batch); err != nil {
		return err
	}
	metadata := telemetry.BatchMetadata{
		ID: batch.ID, MinIngestedNanos: batchMinIngestedNanos(batch), MaxIngestedNanos: batchMaxIngestedNanos(batch),
	}
	return r.Parquet.CommitBatch(metadata, batch.Spans, batch.Logs, batch.Metrics)
}

func (r *Repository) Trace(ctx context.Context, query telemetry.TraceQuery) ([]telemetry.IndexedSpan, error) {
	return r.Parquet.Trace(ctx, query)
}

func (r *Repository) RowCount() uint64 { return r.Parquet.RowCount() }

func (r *Repository) PruneParquet(ctx context.Context, publisher ParquetPublisher, cutoff int64, maxBatches int) (int, error) {
	r.compactionMu.Lock()
	defer r.compactionMu.Unlock()
	return r.Parquet.PruneBefore(cutoff, maxBatches, func(prune func(context.Context) error) error {
		return publisher.PublishParquet(ctx, prune)
	})
}

// PruneParquetPass starts bounded publications until the phase budget expires.
// The budget is checked between publications so an atomic swap is never
// canceled halfway through.
func (r *Repository) PruneParquetPass(ctx context.Context, publisher ParquetPublisher, cutoff int64, maxBatches int, budget time.Duration) (int, error) {
	if maxBatches <= 0 || budget <= 0 {
		return 0, nil
	}
	deadline := time.Now().Add(budget)
	total := 0
	for {
		count, err := r.PruneParquet(ctx, publisher, cutoff, maxBatches)
		total += count
		if err != nil || count < maxBatches || !time.Now().Before(deadline) {
			return total, err
		}
	}
}

func validateBatch(batch Batch) error {
	if batch.ID == "" || strings.ContainsAny(batch.ID, `/\\`) {
		return errors.New("telemetry batch requires a safe ID")
	}
	if rows := batchRows(batch); rows == 0 {
		return errors.New("telemetry batch is empty")
	}
	return nil
}

func normalizeBatch(batch *Batch) {
	ingestedAt := time.Now().UnixNano()
	for i := range batch.Spans {
		batch.Spans[i].Namespace = telemetry.NormalizeNamespace(batch.Spans[i].Namespace)
		if batch.Spans[i].IngestedAt == 0 {
			batch.Spans[i].IngestedAt = ingestedAt
		}
		if batch.Spans[i].StartUnixNanos == 0 {
			batch.Spans[i].StartUnixNanos = batch.Spans[i].IngestedAt
		}
	}
	for i := range batch.Logs {
		batch.Logs[i].Namespace = telemetry.NormalizeNamespace(batch.Logs[i].Namespace)
		if batch.Logs[i].IngestedAt == 0 {
			batch.Logs[i].IngestedAt = ingestedAt
		}
		if batch.Logs[i].EventUnixNanos == 0 {
			batch.Logs[i].EventUnixNanos = telemetry.FirstPositiveNanos(batch.Logs[i].TimeUnixNanos, batch.Logs[i].ObservedTimeNanos, batch.Logs[i].IngestedAt)
		}
	}
	for i := range batch.Metrics {
		batch.Metrics[i].Namespace = telemetry.NormalizeNamespace(batch.Metrics[i].Namespace)
		if batch.Metrics[i].IngestedAt == 0 {
			batch.Metrics[i].IngestedAt = ingestedAt
		}
		if batch.Metrics[i].EventUnixNanos == 0 {
			batch.Metrics[i].EventUnixNanos = telemetry.FirstPositiveNanos(batch.Metrics[i].TimeUnixNanos, batch.Metrics[i].IngestedAt)
		}
	}
}

func batchMaxIngestedNanos(batch Batch) int64 {
	var value int64
	for _, row := range batch.Spans {
		value = max(value, row.IngestedAt)
	}
	for _, row := range batch.Logs {
		value = max(value, row.IngestedAt)
	}
	for _, row := range batch.Metrics {
		value = max(value, row.IngestedAt)
	}
	return value
}

func batchMinIngestedNanos(batch Batch) int64 {
	value := int64(math.MaxInt64)
	include := func(candidate int64) {
		if candidate > 0 {
			value = min(value, candidate)
		}
	}
	for _, row := range batch.Spans {
		include(row.IngestedAt)
	}
	for _, row := range batch.Logs {
		include(row.IngestedAt)
	}
	for _, row := range batch.Metrics {
		include(row.IngestedAt)
	}
	if value == math.MaxInt64 {
		return 0
	}
	return value
}
