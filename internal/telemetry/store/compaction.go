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
	maxCompactionRows = 25_000_000

	// maxCompactionBytes bounds a pass by the bytes it admits, because that is
	// what a merge costs: parquet-go opens a cursor per row group across every
	// input and holds each one's dictionaries at once. Measured on real batches,
	// peak RSS tracks the on-disk input bytes almost exactly -- 4 inputs
	// totalling 442 MiB on disk merged at +413 MiB RSS.
	//
	// Row counts cannot stand in for it: wide spans carrying large attribute
	// payloads and bare log lines differ by an order of magnitude at equal row
	// counts.
	//
	// The figure is a memory budget, so it also decides how fast compaction can
	// consolidate. Set too low it throttles the cascade: at 256 MiB a pass
	// admitted only ~16 of the 16 MB generation-2 batches instead of the 128 the
	// count ceiling allows, and 290 of them piled up unpromoted. 768 MiB keeps
	// merge peak well inside the headroom the process now has -- it sits at
	// 1.5 GiB against a 4 GB DuckDB budget -- while admitting enough inputs per
	// pass for the higher generations to drain.
	maxCompactionBytes = 768 << 20

	// assumedCompactionBytesPerRow prices a batch whose size was never
	// measured. Measured batches run well under this -- span rows with JSON
	// attributes compress to roughly 75 bytes on disk -- so the estimate is
	// deliberately several times pessimistic: over-charging costs smaller
	// groups and more passes, while under-charging costs the merge that the
	// byte ceiling exists to prevent.
	assumedCompactionBytesPerRow = 256
	minCompactionInputs          = 8

	// minCompactionMergeInputs is the smallest group worth merging, and the
	// floor the interrupted-pass halving stops at.
	minCompactionMergeInputs = 2
)

var parquetSignals = [...]string{"spans", "logs", "metrics"}

type compactionMarker struct {
	Output telemetry.BatchMetadata `json:"output"`
	Inputs []string                `json:"inputs"`
}

// compactionAttemptFile records that a merge was started. The completion
// marker is written only after PrepareReplacement, so a process killed during
// the merge -- which is the likely way a large merge ends -- leaves no trace
// that it ever ran. The next start reselects the same group by the same rules
// and dies identically, and nothing in the system observes the loop. This file
// is what a restart reads to know the last attempt was too big.
const compactionAttemptFile = "COMPACTION-ATTEMPT.json"

type compactionAttempt struct {
	Inputs int `json:"inputs"`
}

// compactionBatchCap halves the ceiling after an interrupted pass, so repeated
// kills walk the group down instead of retrying the same one. It never goes
// below a pair: selection always admits two however large they are, so a lower
// floor would stop compaction rather than shrink it. A merge that still dies
// at two inputs is a single file too large to merge, which is a different
// problem and not one a smaller group can solve.
func compactionBatchCap(maxBatches, lastAttempt int) int {
	if lastAttempt <= 0 {
		return maxBatches
	}
	capped := min(maxBatches, lastAttempt) / 2
	return max(capped, minCompactionMergeInputs)
}

// readCompactionAttempt reports the size of an interrupted pass, or zero when
// there was none. A missing, damaged or nonsensical file reads as zero: losing
// this record costs one oversized merge, while treating it as fatal would cost
// the ability to compact at all.
func readCompactionAttempt(root string) int {
	data, err := os.ReadFile(filepath.Join(root, compactionAttemptFile))
	if err != nil {
		return 0
	}
	var attempt compactionAttempt
	if err := json.Unmarshal(data, &attempt); err != nil || attempt.Inputs <= 0 {
		return 0
	}
	return attempt.Inputs
}

func writeCompactionAttempt(root string, inputs int) error {
	data, err := json.Marshal(compactionAttempt{Inputs: inputs})
	if err != nil {
		return err
	}
	return writeDurableFile(filepath.Join(root, compactionAttemptFile), data)
}

func clearCompactionAttempt(root string) error {
	if err := os.Remove(filepath.Join(root, compactionAttemptFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
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
	// A pass that was killed mid-merge left a record of how much it tried to
	// take. Take less.
	selected := selectCompactionBatches(r.Parquet.BatchMetadata(), compactionBatchCap(maxBatches, readCompactionAttempt(r.root)))
	if len(selected) < minCompactionMergeInputs {
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
	// Written before the merge, on purpose: the completion marker below is
	// only reached if the merge returns, and the failure this guards against
	// is the merge not returning.
	if err := writeCompactionAttempt(r.root, len(selected)); err != nil {
		return 0, err
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
	if err := clearCompactionAttempt(r.root); err != nil {
		return 0, err
	}
	return len(selected), nil
}

func selectCompactionBatches(batches []telemetry.BatchMetadata, maxBatches int) []telemetry.BatchMetadata {
	// minCompactionInputs is a "worth the effort" policy and belongs to the
	// caller, which applies it to the ceiling it was asked for. Here the floor
	// is only what a merge needs: after an interrupted pass the ceiling is
	// deliberately halved below that policy, and refusing it would stop
	// compaction at exactly the moment it most needs to make smaller progress.
	if maxBatches < minCompactionMergeInputs {
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
	// Within a day, take the HIGHEST generation that has a compactable group,
	// not the lowest.
	//
	// Lowest-first starves everything above it. Ingest refills generation 0
	// continuously, so if it always wins no later generation is ever selected,
	// while each pass it does run adds one more batch to the generation above.
	// Observed on the live box as 508 / 371 / 290 batches at generations
	// 0 / 1 / 2, generation 2 untouched for hours, and the live file count
	// settling at an equilibrium instead of falling. That count is what every
	// query pays per-file overhead on, so an equilibrium there is a permanent
	// tax rather than a backlog that clears.
	//
	// Highest-first cannot starve anything. A generation above 0 only grows by
	// one batch per pass of the generation below it, so its group fills slowly
	// and empties in a few passes; once it no longer has a full group it stops
	// being a candidate and generation 0 -- which is almost always ready --
	// takes every remaining pass. Reducing the count by a group is worth the
	// same wherever it happens, because the cost being reduced is per file.
	//
	// The older day still wins outright. Reclaiming space retention is about to
	// drop matters more than consolidating today, and a day no longer being
	// written finishes and stays finished.
	newestDay := int64(math.MinInt64)
	for key := range groups {
		if key.day > newestDay {
			newestDay = key.day
		}
	}
	var chosen compactionKey
	var selected []telemetry.BatchMetadata
	found := false
	for key, group := range groups {
		candidate := selectBoundedCompactionGroup(group, maxBatches, key.day < newestDay)
		if len(candidate) < 2 {
			continue
		}
		better := !found ||
			key.day < chosen.day ||
			key.day == chosen.day && key.generation > chosen.generation
		if better {
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
func selectBoundedCompactionGroup(group []telemetry.BatchMetadata, maxBatches int, finished bool) []telemetry.BatchMetadata {
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
	var rows, bytes int64
	saturated := false
	var smallest int64
	for _, batch := range ordered {
		batchRows := compactionBatchRows(batch)
		if batchRows <= 0 || batchRows > maxCompactionRows {
			continue
		}
		batchBytes := compactionBatchBytes(batch)
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
		// A merge opens a cursor per row group across every input and holds
		// each one's dictionaries at once, so the live cost of a pass tracks
		// the bytes it admits. Rows cannot stand in for that: wide spans and
		// bare log lines differ by an order of magnitude at equal row counts.
		// A pair is always admitted, however large: a merge of two inputs is
		// bounded by those inputs, and refusing it would strand files that are
		// individually over budget instead of ever shrinking them. Past a pair
		// the ceiling binds.
		if len(candidate) >= 2 && bytes > maxCompactionBytes-batchBytes {
			saturated = true
			break
		}
		candidate = append(candidate, batch)
		rows += batchRows
		bytes += batchBytes
	}
	if rows == maxCompactionRows || smallest > 0 && smallest > maxCompactionRows-rows {
		saturated = true
	}
	if bytes == maxCompactionBytes {
		saturated = true
	}
	// A group is worth merging when it is full, when a ceiling stopped it, or
	// when it belongs to a day that has finished and already holds enough files
	// to be worth the merge.
	//
	// Holding out for a full group is right while a day is still being written:
	// more batches are coming, and merging early wastes the work. It is wrong
	// once the day is over, because the group will never grow again. On the
	// live box that stranded almost everything -- 1,243 batches in 69
	// (day, generation) groups across 25 days, not one of them a candidate,
	// because only the current day ever reached 128 files. Every compaction
	// output was generation 1 and every older day was untouchable, which is why
	// the live file count held at an equilibrium that neither reordering nor a
	// larger byte budget could move.
	if len(candidate) == maxBatches || saturated && len(candidate) >= 2 {
		return candidate
	}
	if finished && len(candidate) >= minCompactionInputs {
		return candidate
	}
	return nil
}

// compactionBatchBytes prices a batch for the byte ceiling, falling back to a
// pessimistic per-row estimate when the batch was never measured.
func compactionBatchBytes(batch telemetry.BatchMetadata) int64 {
	if batch.Bytes > 0 {
		return batch.Bytes
	}
	rows := compactionBatchRows(batch)
	if rows <= 0 || rows > math.MaxInt64/assumedCompactionBytesPerRow {
		return math.MaxInt64
	}
	return rows * assumedCompactionBytesPerRow
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
