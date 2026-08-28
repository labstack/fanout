package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
		r.logUnresolvedCompaction(err)
		_ = r.Close()
		return nil, fmt.Errorf("recover Parquet compaction (unresolved marker at %s): %w",
			filepath.Join(root, "COMPACTION.json"), err)
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

// logUnresolvedCompaction spells out the operator's options for a marker that
// blocks startup.
//
// Refusing to boot is only the safe half of the decision. A marker names the
// retired inputs a rollback still needs, and those directories hold the only
// copy of the rows the compaction was merging, so a startup that guessed at
// the rollback set could delete them — which is why recovery fails closed. The
// other half is saying where to look, because the reachable next step for an
// instance that will not start is rm -rf of the data directory, and that
// destroys exactly what failing closed preserved.
//
// The guidance is logged rather than wrapped into the error: the paths and the
// ordering constraint do not fit an error string that composes, and the
// rollback warning is the kind of thing an operator has to be able to read
// once, in full, at the moment the process refuses to come up.
func (r *Repository) logUnresolvedCompaction(err error) {
	slog.Error("Parquet compaction is unresolved; Fanout will not start",
		"err", err,
		"marker", filepath.Join(r.root, "COMPACTION.json"),
		"compaction_staging", filepath.Join(r.root, "compaction"),
		"retired_inputs", r.Parquet.BatchesDir(),
		"nothing_deleted", "the replacement — staged as compaction/<output id>, or already published as parquet/batches/<output id>.batch — and the retired inputs (named <id>.retired-<output id>) are all intact",
		"retry", "clear the underlying cause and start again; recovery re-runs on its own",
		"rollback", "rename every <id>.retired-<output id> directory to <id>.batch, delete both possible replacement locations (compaction/<output id> and parquet/batches/<output id>.batch), then delete the marker",
		"warning", "deleting the marker on its own is not a rollback; cleanup then treats the retired inputs as reclaimable and removes them")
}

// cleanupCompactionArtifacts drops staging left after compaction recovery has
// completed and consumed its live marker.
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
	protected, err := r.protectedRetiredSuffix()
	if err != nil {
		return err
	}
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
		// The live marker is the only thing that makes a retired directory
		// unreclaimable, and it protects exactly one output's inputs. So
		// removing COMPACTION.json without first renaming its
		// <id>.retired-<output id> directories back to <id>.batch does not
		// roll the compaction back — it makes the rows here deletable, and
		// this loop deletes them on the next pass.
		if protected != "" && strings.HasSuffix(name, protected) {
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

// protectedRetiredSuffix names the retired-input set a live compaction marker
// may still need. An unreadable marker fails cleanup closed.
func (r *Repository) protectedRetiredSuffix() (string, error) {
	data, err := os.ReadFile(filepath.Join(r.root, "COMPACTION.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var marker compactionMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return "", fmt.Errorf("read live compaction marker: %w", err)
	}
	if err := validateCompactionMarker(marker); err != nil {
		return "", err
	}
	return ".retired-" + marker.Output.ID, nil
}

func (r *Repository) Commit(ctx context.Context, batch Batch) error {
	normalizeBatch(&batch)
	if err := validateBatch(batch); err != nil {
		return err
	}
	metadata := telemetry.BatchMetadata{
		ID: batch.ID, MinIngestedNanos: batchMinIngestedNanos(batch), MaxIngestedNanos: batchMaxIngestedNanos(batch),
	}
	return r.Parquet.CommitBatch(ctx, metadata, batch.Spans, batch.Logs, batch.Metrics)
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
