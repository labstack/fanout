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
type ParquetCompactor interface {
	MergeParquet(context.Context, string, []string, string) error
	PublishParquet(func() error) error
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
		if err := compactor.PublishParquet(r.recoverCompaction); err != nil {
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
	if err := compactor.PublishParquet(func() error { return r.completeCompaction(marker) }); err != nil {
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
		if (compactionKey{day: batch.MaxIngestedNanos / int64(24*time.Hour), generation: batch.Generation}) == chosen {
			selected = append(selected, batch)
			if len(selected) == maxBatches {
				break
			}
		}
	}
	return selected
}

func (r *Repository) CompactParquetBacklog(ctx context.Context, compactor ParquetCompactor, maxBatches int) (int, error) {
	total := 0
	for {
		count, err := r.CompactParquet(ctx, compactor, maxBatches)
		total += count
		if err != nil || count == 0 {
			return total, err
		}
	}
}

func (r *Repository) recoverCompaction() error {
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
	return r.completeCompaction(marker)
}

func (r *Repository) completeCompaction(marker compactionMarker) error {
	if err := r.Parquet.PublishReplacement(r.compactionStage(marker.Output.ID), marker.Output, marker.Inputs); err != nil {
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
