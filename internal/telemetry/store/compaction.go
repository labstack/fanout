package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
	"golang.org/x/sync/errgroup"
)

const (
	maxCompactionRows   = 25_000_000
	minCompactionInputs = 8
)

var parquetSignals = [...]string{"spans", "logs", "metrics"}

type compactionMarker struct {
	Output telemetry.BatchMetadata `json:"output"`
	Inputs []string                `json:"inputs"`
}

type compactionKey struct {
	day        int64
	generation uint32
}

// ParquetPublisher lets the query layer exclude readers only for the atomic
// namespace swap; storage owns native merges and crash-safe replacement state.
type ParquetPublisher interface {
	PublishParquet(context.Context, func(context.Context) error) error
}

// CompactParquet combines one same-day, same-generation group. The output is
// prepared outside the query gate and swapped as one batch directory.
func (r *Repository) CompactParquet(ctx context.Context, publisher ParquetPublisher, maxBatches int) (int, error) {
	if publisher == nil || maxBatches < minCompactionInputs {
		return 0, nil
	}
	r.compactionMu.Lock()
	defer r.compactionMu.Unlock()
	markerPath := filepath.Join(r.root, "COMPACTION.json")
	if exists, err := pathExists(markerPath); err != nil {
		return 0, err
	} else if exists {
		if err := r.recoverCompaction(ctx, publisher.PublishParquet); err != nil {
			return 0, fmt.Errorf("recover pending Parquet compaction: %w", err)
		}
	}
	selected := selectCompactionBatches(r.Parquet.BatchMetadata(), maxBatches)
	if len(selected) < 2 {
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
	type mergePlan struct {
		signal string
		inputs []string
		output string
	}
	plans := make([]mergePlan, 0, len(parquetSignals))
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
		plans = append(plans, mergePlan{signal: signal, inputs: inputs, output: filepath.Join(stage, signal+".parquet")})
	}
	group, mergeCtx := errgroup.WithContext(ctx)
	for _, plan := range plans {
		group.Go(func() error {
			if err := r.Parquet.MergeParquet(mergeCtx, plan.signal, plan.inputs, plan.output); err != nil {
				return fmt.Errorf("compact %s Parquet: %w", plan.signal, err)
			}
			if err := mergeCtx.Err(); err != nil {
				return err
			}
			if err := syncFile(plan.output); err != nil {
				return fmt.Errorf("sync compacted %s Parquet: %w", plan.signal, err)
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return 0, err
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
	if err := r.completeCompaction(ctx, marker, publisher.PublishParquet); err != nil {
		return 0, err
	}
	return len(selected), nil
}

func selectCompactionBatches(batches []telemetry.BatchMetadata, maxBatches int) []telemetry.BatchMetadata {
	if maxBatches < minCompactionInputs {
		return nil
	}
	groups := make(map[compactionKey][]telemetry.BatchMetadata)
	for _, batch := range batches {
		if batch.MaxIngestedNanos <= 0 {
			continue
		}
		key := compactionKey{day: batch.MaxIngestedNanos / int64(24*time.Hour), generation: batch.Generation}
		groups[key] = append(groups[key], batch)
	}
	var chosen compactionKey
	var selected []telemetry.BatchMetadata
	found := false
	for key, group := range groups {
		candidate := selectBoundedCompactionGroup(group, maxBatches)
		if len(candidate) < 2 {
			continue
		}
		if !found || key.day < chosen.day || key.day == chosen.day && key.generation < chosen.generation {
			chosen, selected, found = key, candidate, true
		}
	}
	if !found {
		return nil
	}
	return selected
}

// selectBoundedCompactionGroup keeps the high-reclaim full-group behavior for
// small files, but admits a smaller group when the row ceiling fills first.
// Without the latter, one successful generation can make every later group too
// large for the ceiling and permanently strand those files.
func selectBoundedCompactionGroup(group []telemetry.BatchMetadata, maxBatches int) []telemetry.BatchMetadata {
	ordered := append([]telemetry.BatchMetadata(nil), group...)
	sort.Slice(ordered, func(i, j int) bool {
		left, right := compactionBatchRows(ordered[i]), compactionBatchRows(ordered[j])
		if left != right {
			return left < right
		}
		if ordered[i].MinIngestedNanos != ordered[j].MinIngestedNanos {
			return ordered[i].MinIngestedNanos < ordered[j].MinIngestedNanos
		}
		return ordered[i].ID < ordered[j].ID
	})
	candidate := make([]telemetry.BatchMetadata, 0, min(maxBatches, len(ordered)))
	var rows int64
	saturated := false
	var smallest int64
	for _, batch := range ordered {
		batchRows := compactionBatchRows(batch)
		if batchRows <= 0 || batchRows > maxCompactionRows {
			continue
		}
		if smallest == 0 {
			smallest = batchRows
		}
		if len(candidate) == maxBatches {
			saturated = true
			break
		}
		if rows > maxCompactionRows-batchRows {
			saturated = true
			break
		}
		candidate = append(candidate, batch)
		rows += batchRows
	}
	if rows == maxCompactionRows || smallest > 0 && smallest > maxCompactionRows-rows {
		saturated = true
	}
	if len(candidate) == maxBatches || saturated && len(candidate) >= 2 {
		return candidate
	}
	return nil
}

func compactionBatchRows(batch telemetry.BatchMetadata) int64 {
	if batch.Spans < 0 {
		return math.MaxInt64
	}
	rows := int64(batch.Spans)
	for _, count := range [...]int{batch.Logs, batch.Metrics} {
		if count < 0 || rows > math.MaxInt64-int64(count) {
			return math.MaxInt64
		}
		rows += int64(count)
	}
	return rows
}

// CompactParquetPass starts complete compactions until the phase budget
// expires. An in-flight merge keeps the caller context so slow, valid work
// commits instead of restarting the same input group on every pass.
func (r *Repository) CompactParquetPass(ctx context.Context, publisher ParquetPublisher, maxBatches int, budget time.Duration) (int, error) {
	if maxBatches <= 0 || budget <= 0 {
		return 0, nil
	}
	deadline := time.Now().Add(budget)
	total := 0
	for {
		count, err := r.CompactParquet(ctx, publisher, maxBatches)
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
