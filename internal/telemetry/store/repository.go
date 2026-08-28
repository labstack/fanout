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
	root    string
	Parquet *telemetry.ParquetStore
	// compactionMu guards the compaction marker and the retired directories a
	// pending marker may still need, along with recoveryFailures.
	compactionMu     sync.Mutex
	recoveryFailures int
}

// maxCompactionRecoveryAttempts bounds how many passes a marker may fail
// recovery before it is set aside. A marker that cannot be recovered gates
// retention, compaction, and retired-directory cleanup — correctly, since all
// three could destroy what a rollback needs — so without a give-up path one
// bad marker latches every form of maintenance off for the process lifetime
// and storage grows unreclaimed behind a healthy-looking probe.
const maxCompactionRecoveryAttempts = 3

// quarantinedMarkerName is the marker set aside by quarantineCompactionMarker.
const quarantinedMarkerName = "COMPACTION.json.failed"

func Open(root string) (*Repository, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	parquetStore, err := telemetry.OpenParquetStore(filepath.Join(root, "parquet"))
	if err != nil {
		return nil, err
	}
	r := &Repository{root: root, Parquet: parquetStore}
	// A marker that cannot be recovered must not keep the process from
	// booting: refusing to start leaves the operator with no way to run the
	// cleanup that would clear it, so the only recovery was a manual rm -rf of
	// live storage. Set it aside instead and come up degraded. Nothing is
	// deleted — the staged output and the retired inputs both survive — so the
	// compaction can still be completed or rolled back by hand.
	if err := r.recoverCompaction(context.Background(), func(ctx context.Context, publish func(context.Context) error) error { return publish(ctx) }); err != nil {
		slog.Error("Parquet compaction recovery failed at open; setting marker aside",
			"err", err, "marker", quarantinedMarkerName)
		if quarantineErr := r.quarantineCompactionMarker(); quarantineErr != nil {
			_ = r.Close()
			return nil, errors.Join(fmt.Errorf("recover Parquet compaction: %w", err), quarantineErr)
		}
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

// cleanupCompactionArtifacts drops staging left by an interrupted compaction.
//
// A set-aside marker keeps its staged output: that directory holds the only
// copy of the compaction's merged rows, and it has to survive every boot the
// marker survives, not just the one that set it aside — otherwise the promise
// that an operator can still complete the compaction by hand lasts exactly one
// restart.
func (r *Repository) cleanupCompactionArtifacts() error {
	setAside, err := pathExists(filepath.Join(r.root, quarantinedMarkerName))
	if err != nil {
		return err
	}
	if !setAside {
		if err := os.RemoveAll(filepath.Join(r.root, "compaction")); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(r.root, "COMPACTION.json.tmp")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(r.root)
}

// quarantineCompactionMarker sets aside a marker whose recovery keeps failing
// so it stops gating retention, compaction, and retired-directory cleanup.
//
// Nothing is deleted: the staged output stays, and the retired inputs are
// protected from cleanup by protectedRetiredSuffixes, so the operator can
// still complete or roll back the compaction by hand. This is not the
// batch-level quarantine that was rejected — no authoritative telemetry is
// discarded, and no batch becomes unreadable that was readable before.
func (r *Repository) quarantineCompactionMarker() error {
	if err := os.Rename(filepath.Join(r.root, "COMPACTION.json"), filepath.Join(r.root, quarantinedMarkerName)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
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
	protected, protectAll, err := r.protectedRetiredSuffixes()
	if err != nil {
		return err
	}
	if protectAll {
		return nil
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
		if protectedRetired(name, protected) {
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

func protectedRetired(name string, protected map[string]bool) bool {
	for suffix := range protected {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// protectedRetiredSuffixes names the retired-input sets that a live or
// set-aside compaction marker may still need in order to roll back. Deleting
// one of those is unrecoverable: the input is gone and its rows were never
// published under the replacement.
//
// A marker whose contents cannot be parsed names nothing, so protectAll tells
// the caller to delete no retired directory at all. Returning an error instead
// would fail Open on every boot — the marker is already set aside and stays on
// disk, so the failure repeats forever and cleanup can never run, which is the
// outcome setting it aside exists to avoid.
func (r *Repository) protectedRetiredSuffixes() (protected map[string]bool, protectAll bool, err error) {
	protected = make(map[string]bool)
	for _, name := range []string{"COMPACTION.json", quarantinedMarkerName} {
		data, err := os.ReadFile(filepath.Join(r.root, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		var marker compactionMarker
		if err := json.Unmarshal(data, &marker); err != nil {
			slog.Error("compaction marker is unreadable; retaining every retired batch",
				"marker", name, "err", err)
			return nil, true, nil
		}
		protected[".retired-"+marker.Output.ID] = true
	}
	return protected, false, nil
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
