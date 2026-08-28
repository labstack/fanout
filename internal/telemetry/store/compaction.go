package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

const minCompactionInputs = 8

var parquetSignals = [...]string{"spans", "logs", "metrics"}

type compactionMarker struct {
	Output telemetry.BatchMetadata `json:"output"`
	Inputs []string                `json:"inputs"`
}

type compactionKey struct {
	day        int64
	generation uint32
}

// ParquetCompactor keeps DuckDB execution and publication locking in the query
// layer while storage owns batch selection and crash-safe replacement state.
type ParquetPublisher interface {
	PublishParquet(context.Context, func(context.Context) error) error
}

type ParquetCompactor interface {
	ParquetPublisher
	MergeParquet(context.Context, string, []string, string) error
}

// CompactParquet combines one same-day, same-generation group. The output is
// prepared outside the query gate and swapped as one batch directory.
func (r *Repository) CompactParquet(ctx context.Context, compactor ParquetCompactor, maxBatches int) (int, error) {
	if compactor == nil || maxBatches < minCompactionInputs {
		return 0, nil
	}
	r.compactionMu.Lock()
	defer r.compactionMu.Unlock()
	markerPath := filepath.Join(r.root, "COMPACTION.json")
	if exists, err := pathExists(markerPath); err != nil {
		return 0, err
	} else if exists {
		if err := r.recoverCompaction(ctx, compactor.PublishParquet); err != nil {
			return 0, fmt.Errorf("recover pending Parquet compaction: %w", err)
		}
	}
	selected := selectCompactionBatches(r.Parquet.BatchMetadata(), maxBatches)
	if len(selected) < minCompactionInputs {
		return 0, nil
	}
	output := telemetry.BatchMetadata{
		ID: fmt.Sprintf("compact-%d", time.Now().UnixNano()), MinIngestedNanos: math.MaxInt64,
		Generation: selected[0].Generation + 1,
	}
	marker := compactionMarker{Output: output, Inputs: make([]string, 0, len(selected))}
	for _, batch := range selected {
		marker.Inputs = append(marker.Inputs, batch.ID)
		if batch.MinIngestedNanos > 0 {
			marker.Output.MinIngestedNanos = min(marker.Output.MinIngestedNanos, batch.MinIngestedNanos)
		}
		marker.Output.MaxIngestedNanos = max(marker.Output.MaxIngestedNanos, batch.MaxIngestedNanos)
		marker.Output.Spans += batch.Spans
		marker.Output.Logs += batch.Logs
		marker.Output.Metrics += batch.Metrics
	}
	if marker.Output.MinIngestedNanos == math.MaxInt64 {
		marker.Output.MinIngestedNanos = 0
	}
	stage := r.compactionStage(marker.Output.ID)
	if err := os.RemoveAll(stage); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return 0, err
	}
	if err := errors.Join(syncDirectory(filepath.Dir(stage)), syncDirectory(r.root)); err != nil {
		return 0, err
	}
	prepared := false
	defer func() {
		if !prepared {
			_ = os.RemoveAll(stage)
		}
	}()
	for _, signal := range parquetSignals {
		inputs := make([]string, 0, len(selected))
		for _, batch := range selected {
			path := filepath.Join(r.Parquet.BatchPath(batch.ID), signal+".parquet")
			if _, err := os.Stat(path); err == nil {
				inputs = append(inputs, path)
			} else if !errors.Is(err, os.ErrNotExist) {
				return 0, err
			}
		}
		if len(inputs) == 0 {
			continue
		}
		outputPath := filepath.Join(stage, signal+".parquet")
		if err := compactor.MergeParquet(ctx, signal, inputs, outputPath); err != nil {
			return 0, fmt.Errorf("compact %s Parquet: %w", signal, err)
		}
		if err := syncFile(outputPath); err != nil {
			return 0, err
		}
	}
	if err := r.Parquet.PrepareReplacement(stage, marker.Output); err != nil {
		return 0, err
	}
	data, err := json.Marshal(marker)
	if err != nil {
		return 0, err
	}
	if err := writeDurableFile(markerPath, data); err != nil {
		return 0, err
	}
	if err := syncDirectory(r.root); err != nil {
		return 0, err
	}
	prepared = true
	if err := r.completeCompaction(ctx, marker, compactor.PublishParquet); err != nil {
		return 0, err
	}
	return len(selected), nil
}

func selectCompactionBatches(batches []telemetry.BatchMetadata, maxBatches int) []telemetry.BatchMetadata {
	if maxBatches < minCompactionInputs {
		return nil
	}
	counts := make(map[compactionKey]int)
	for _, batch := range batches {
		if batch.MaxIngestedNanos > 0 {
			counts[compactionKey{day: batch.MaxIngestedNanos / int64(24*time.Hour), generation: batch.Generation}]++
		}
	}
	var chosen compactionKey
	found := false
	for key, count := range counts {
		if count >= minCompactionInputs && (!found || key.day < chosen.day || key.day == chosen.day && key.generation < chosen.generation) {
			chosen, found = key, true
		}
	}
	if !found {
		return nil
	}
	selected := make([]telemetry.BatchMetadata, 0, min(maxBatches, counts[chosen]))
	for _, batch := range batches {
		if batch.MaxIngestedNanos <= 0 {
			continue
		}
		if (compactionKey{day: batch.MaxIngestedNanos / int64(24*time.Hour), generation: batch.Generation}) == chosen {
			selected = append(selected, batch)
			if len(selected) == maxBatches {
				break
			}
		}
	}
	return selected
}

// CompactParquetPass starts complete compactions until the phase budget
// expires. An in-flight merge keeps the caller context so slow, valid work
// commits instead of restarting the same input group on every pass.
func (r *Repository) CompactParquetPass(ctx context.Context, compactor ParquetCompactor, maxBatches int, budget time.Duration) (int, error) {
	if maxBatches <= 0 || budget <= 0 {
		return 0, nil
	}
	deadline := time.Now().Add(budget)
	total := 0
	for {
		count, err := r.CompactParquet(ctx, compactor, maxBatches)
		total += count
		if err != nil || count == 0 || !time.Now().Before(deadline) {
			return total, err
		}
	}
}

type parquetPublishFunc func(context.Context, func(context.Context) error) error

// RecoverParquet resolves a pending compaction marker before any cleanup,
// retention, or new compaction can mutate its rollback set.
func (r *Repository) RecoverParquet(ctx context.Context, publisher ParquetPublisher) error {
	r.compactionMu.Lock()
	defer r.compactionMu.Unlock()
	return r.recoverCompaction(ctx, publisher.PublishParquet)
}

func (r *Repository) recoverCompaction(ctx context.Context, publish parquetPublishFunc) error {
	data, err := os.ReadFile(filepath.Join(r.root, "COMPACTION.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var marker compactionMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return err
	}
	if err := validateCompactionMarker(marker); err != nil {
		return err
	}
	stageExists, err := pathExists(r.compactionStage(marker.Output.ID))
	if err != nil {
		return err
	}
	finalExists, err := pathExists(r.Parquet.BatchPath(marker.Output.ID))
	if err != nil {
		return err
	}
	if !stageExists && !finalExists {
		if err := r.Parquet.RestoreRetiredInputs(marker.Inputs, marker.Output.ID, func(swap func(context.Context) error) error {
			return publish(ctx, swap)
		}); err != nil {
			return fmt.Errorf("restore compaction %s inputs: %w", marker.Output.ID, err)
		}
		if err := os.Remove(filepath.Join(r.root, "COMPACTION.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return syncDirectory(r.root)
	}
	return r.completeCompaction(ctx, marker, publish)
}

func (r *Repository) completeCompaction(ctx context.Context, marker compactionMarker, publish parquetPublishFunc) error {
	if err := r.Parquet.PublishReplacement(r.compactionStage(marker.Output.ID), marker.Output, marker.Inputs, func(swap func(context.Context) error) error {
		return publish(ctx, swap)
	}); err != nil {
		return err
	}
	markerPath := filepath.Join(r.root, "COMPACTION.json")
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(r.root)
}

func (r *Repository) compactionStage(id string) string {
	return filepath.Join(r.root, "compaction", id)
}

func validateCompactionMarker(marker compactionMarker) error {
	if err := telemetry.ValidateBatchID(marker.Output.ID); err != nil {
		return fmt.Errorf("invalid compaction output: %w", err)
	}
	if len(marker.Inputs) == 0 {
		return errors.New("compaction marker has no inputs")
	}
	for _, id := range marker.Inputs {
		if err := telemetry.ValidateBatchID(id); err != nil {
			return fmt.Errorf("invalid compaction input: %w", err)
		}
	}
	return nil
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func writeDurableFile(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
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
	return os.Rename(tmp, path)
}
