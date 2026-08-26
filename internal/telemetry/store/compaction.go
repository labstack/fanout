package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type compactionMarker struct {
	ID         string   `json:"id"`
	Inputs     []string `json:"inputs"`
	Signals    []string `json:"signals"`
	MinNanos   int64    `json:"min_nanos"`
	MaxNanos   int64    `json:"max_nanos"`
	Generation uint32   `json:"generation"`
}

const minCompactionInputs = 8

var parquetSignals = [...]string{"spans", "logs", "metrics"}

// CompactParquet combines the oldest small atomic batches into larger files.
// A durable marker makes the multi-signal swap recoverable after a crash.
func (r *Repository) CompactParquet(ctx context.Context, db *sql.DB, maxBatches int, publishLock sync.Locker) (int, error) {
	if db == nil || maxBatches < 2 {
		return 0, nil
	}
	r.compactionMu.Lock()
	defer r.compactionMu.Unlock()
	r.mu.RLock()
	selected := selectCompactionBatches(r.manifest.Batches, maxBatches)
	r.mu.RUnlock()
	if len(selected) < minCompactionInputs {
		return 0, nil
	}
	marker := compactionMarker{ID: fmt.Sprintf("compact-%d", time.Now().UnixNano()), MinNanos: math.MaxInt64, Generation: selected[0].Generation + 1}
	for _, batch := range selected {
		marker.Inputs = append(marker.Inputs, batch.ID)
		if batch.MinNanos > 0 {
			marker.MinNanos = min(marker.MinNanos, batch.MinNanos)
		}
		marker.MaxNanos = max(marker.MaxNanos, batch.MaxNanos)
	}
	if marker.MinNanos == math.MaxInt64 {
		marker.MinNanos = 0
	}
	stageDir := filepath.Join(r.root, marker.ID)
	if err := os.Mkdir(stageDir, 0o755); err != nil {
		return 0, err
	}
	recoverable := false
	defer func() {
		if !recoverable {
			_ = os.RemoveAll(stageDir)
		}
	}()
	for _, signal := range parquetSignals {
		var inputs []string
		for _, id := range marker.Inputs {
			path := filepath.Join(r.Parquet.Dir(), signal, id+".parquet")
			if _, err := os.Stat(path); err == nil {
				inputs = append(inputs, path)
			} else if !errors.Is(err, os.ErrNotExist) {
				return 0, err
			}
		}
		if len(inputs) == 0 {
			continue
		}
		marker.Signals = append(marker.Signals, signal)
		quoted := make([]string, len(inputs))
		for i, path := range inputs {
			quoted[i] = sqlQuote(path)
		}
		output := filepath.Join(stageDir, signal+".parquet")
		stmt := fmt.Sprintf("COPY (SELECT * FROM read_parquet([%s], union_by_name=true)) TO %s (FORMAT PARQUET, COMPRESSION ZSTD, ROW_GROUP_SIZE 122880)", strings.Join(quoted, ","), sqlQuote(output))
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return 0, fmt.Errorf("compact %s parquet: %w", signal, err)
		}
		if err := syncFile(output); err != nil {
			return 0, fmt.Errorf("sync compacted %s parquet: %w", signal, err)
		}
	}
	if len(marker.Signals) == 0 {
		return 0, errors.New("compaction selected batches without parquet inputs")
	}
	if err := syncDirectory(stageDir); err != nil {
		return 0, fmt.Errorf("sync compaction staging directory: %w", err)
	}
	data, err := json.Marshal(marker)
	if err != nil {
		return 0, err
	}
	if err := writeDurableFile(filepath.Join(r.root, "COMPACTION.json"), data); err != nil {
		return 0, err
	}
	if err := syncDirectory(r.root); err != nil {
		return 0, err
	}
	recoverable = true
	if publishLock != nil {
		publishLock.Lock()
		defer publishLock.Unlock()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.completeCompaction(marker); err != nil {
		return 0, err
	}
	return len(selected), nil
}

type compactionKey struct {
	day        int64
	generation uint32
}

// selectCompactionBatches implements a leveled, day-partitioned compaction
// policy. An output can only be merged with peers from the same day and
// generation, so maintenance never folds the complete retained corpus into
// one perpetually young file. At most minCompactionInputs-1 files remain at
// each level for a day.
func selectCompactionBatches(batches []batchMetadata, maxBatches int) []batchMetadata {
	if maxBatches < minCompactionInputs {
		return nil
	}
	counts := make(map[compactionKey]int)
	for _, batch := range batches {
		if batch.MaxNanos <= 0 {
			continue
		}
		key := compactionKey{day: batch.MaxNanos / int64(24*time.Hour), generation: batch.Generation}
		counts[key]++
	}
	var chosen compactionKey
	found := false
	for key, count := range counts {
		if count < minCompactionInputs {
			continue
		}
		if !found || key.day < chosen.day || (key.day == chosen.day && key.generation < chosen.generation) {
			chosen, found = key, true
		}
	}
	if !found {
		return nil
	}
	selected := make([]batchMetadata, 0, min(maxBatches, counts[chosen]))
	for _, batch := range batches {
		key := compactionKey{day: batch.MaxNanos / int64(24*time.Hour), generation: batch.Generation}
		if batch.MaxNanos > 0 && key == chosen {
			selected = append(selected, batch)
			if len(selected) == maxBatches {
				break
			}
		}
	}
	return selected
}

// CompactParquetBacklog drains every currently eligible compaction group so a
// maintenance interval cannot create files faster than it retires them.
func (r *Repository) CompactParquetBacklog(ctx context.Context, db *sql.DB, maxBatches int, publishLock sync.Locker) (int, error) {
	total := 0
	for {
		compacted, err := r.CompactParquet(ctx, db, maxBatches, publishLock)
		total += compacted
		if err != nil || compacted == 0 {
			return total, err
		}
	}
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
	stageDir := filepath.Join(r.root, marker.ID)
	if err := r.validateCompactionOutputs(marker, stageDir); err != nil {
		return errors.Join(err, r.restoreCompactionInputs(marker))
	}
	for _, signal := range marker.Signals {
		dir := filepath.Join(r.Parquet.Dir(), signal)
		for _, id := range marker.Inputs {
			input := filepath.Join(dir, id+".parquet")
			retired := input + ".retired-" + marker.ID
			if _, err := os.Stat(input); err == nil {
				if err := os.Rename(input, retired); err != nil {
					return err
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		stage := filepath.Join(stageDir, signal+".parquet")
		final := filepath.Join(dir, marker.ID+".parquet")
		if _, err := os.Stat(stage); err == nil {
			if err := os.Rename(stage, final); err != nil {
				return err
			}
		}
	}
	if err := syncParquetDirectories(r.Parquet.Dir()); err != nil {
		return err
	}
	inputSet := make(map[string]struct{}, len(marker.Inputs))
	for _, id := range marker.Inputs {
		inputSet[id] = struct{}{}
	}
	kept := make([]batchMetadata, 0, len(r.manifest.Batches)-len(marker.Inputs)+1)
	for _, batch := range r.manifest.Batches {
		if _, compacted := inputSet[batch.ID]; !compacted && batch.ID != marker.ID {
			kept = append(kept, batch)
		}
	}
	kept = append(kept, batchMetadata{ID: marker.ID, MinNanos: marker.MinNanos, MaxNanos: marker.MaxNanos, Generation: marker.Generation})
	next := repositoryManifest{Version: 1, Batches: kept}
	if err := writeRepositoryManifest(r.root, next); err != nil {
		return err
	}
	r.manifest = next
	for _, signal := range marker.Signals {
		for _, id := range marker.Inputs {
			_ = os.Remove(filepath.Join(r.Parquet.Dir(), signal, id+".parquet.retired-"+marker.ID))
		}
	}
	_ = os.RemoveAll(stageDir)
	if err := os.Remove(filepath.Join(r.root, "COMPACTION.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(r.root)
}

func (r *Repository) validateCompactionOutputs(marker compactionMarker, stageDir string) error {
	if len(marker.Signals) == 0 {
		return errors.New("compaction marker has no required signals")
	}
	for _, signal := range marker.Signals {
		stage := filepath.Join(stageDir, signal+".parquet")
		final := filepath.Join(r.Parquet.Dir(), signal, marker.ID+".parquet")
		stageExists, err := pathExists(stage)
		if err != nil {
			return fmt.Errorf("inspect staged %s output: %w", signal, err)
		}
		finalExists, err := pathExists(final)
		if err != nil {
			return fmt.Errorf("inspect final %s output: %w", signal, err)
		}
		if !stageExists && !finalExists {
			return fmt.Errorf("compaction %s is missing required %s output", marker.ID, signal)
		}
	}
	return nil
}

func (r *Repository) restoreCompactionInputs(marker compactionMarker) error {
	var restoreErr error
	for _, signal := range marker.Signals {
		dir := filepath.Join(r.Parquet.Dir(), signal)
		for _, id := range marker.Inputs {
			input := filepath.Join(dir, id+".parquet")
			retired := input + ".retired-" + marker.ID
			retiredExists, err := pathExists(retired)
			if err != nil {
				restoreErr = errors.Join(restoreErr, fmt.Errorf("inspect retired %s input %s: %w", signal, id, err))
				continue
			}
			if !retiredExists {
				continue
			}
			inputExists, err := pathExists(input)
			if err != nil {
				restoreErr = errors.Join(restoreErr, fmt.Errorf("inspect active %s input %s: %w", signal, id, err))
				continue
			}
			if inputExists {
				restoreErr = errors.Join(restoreErr, fmt.Errorf("restore retired %s input %s: active input already exists", signal, id))
				continue
			}
			if err := os.Rename(retired, input); err != nil {
				restoreErr = errors.Join(restoreErr, fmt.Errorf("restore retired %s input %s: %w", signal, id, err))
			}
		}
	}
	return errors.Join(restoreErr, syncParquetDirectories(r.Parquet.Dir()))
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
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
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
	return os.Rename(tmp, path)
}

func sqlQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func syncParquetDirectories(root string) error {
	var err error
	for _, signal := range parquetSignals {
		err = errors.Join(err, syncDirectory(filepath.Join(root, signal)))
	}
	return err
}
