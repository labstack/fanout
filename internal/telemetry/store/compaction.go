package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type compactionMarker struct {
	ID       string   `json:"id"`
	Inputs   []string `json:"inputs"`
	MaxNanos int64    `json:"max_nanos"`
}

// CompactParquet combines the oldest small atomic batches into larger files.
// A durable marker makes the multi-signal swap recoverable after a crash.
func (r *Repository) CompactParquet(ctx context.Context, db *sql.DB, maxBatches int) (int, error) {
	if db == nil || maxBatches < 2 {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.manifest.Batches) < 8 {
		return 0, nil
	}
	count := min(maxBatches, len(r.manifest.Batches))
	selected := append([]batchMetadata(nil), r.manifest.Batches[:count]...)
	marker := compactionMarker{ID: fmt.Sprintf("compact-%d", time.Now().UnixNano())}
	for _, batch := range selected {
		marker.Inputs = append(marker.Inputs, batch.ID)
		marker.MaxNanos = max(marker.MaxNanos, batch.MaxNanos)
	}
	stageDir := filepath.Join(r.root, marker.ID)
	if err := os.Mkdir(stageDir, 0o755); err != nil {
		return 0, err
	}
	for _, signal := range []string{"spans", "logs", "metrics"} {
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
		quoted := make([]string, len(inputs))
		for i, path := range inputs {
			quoted[i] = sqlQuote(path)
		}
		output := filepath.Join(stageDir, signal+".parquet")
		stmt := fmt.Sprintf("COPY (SELECT * FROM read_parquet([%s], union_by_name=true)) TO %s (FORMAT PARQUET, COMPRESSION ZSTD, ROW_GROUP_SIZE 122880)", strings.Join(quoted, ","), sqlQuote(output))
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return 0, fmt.Errorf("compact %s parquet: %w", signal, err)
		}
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
	if err := r.completeCompaction(marker); err != nil {
		return 0, err
	}
	return count, nil
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
	for _, signal := range []string{"spans", "logs", "metrics"} {
		dir := filepath.Join(r.Parquet.Dir(), signal)
		for _, id := range marker.Inputs {
			input := filepath.Join(dir, id+".parquet")
			retired := input + ".retired-" + marker.ID
			if _, err := os.Stat(input); err == nil {
				if err := os.Rename(input, retired); err != nil {
					return err
				}
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
	kept = append(kept, batchMetadata{ID: marker.ID, MaxNanos: marker.MaxNanos})
	next := repositoryManifest{Version: 1, Batches: kept}
	if err := writeRepositoryManifest(r.root, next); err != nil {
		return err
	}
	r.manifest = next
	for _, signal := range []string{"spans", "logs", "metrics"} {
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
	for _, signal := range []string{"spans", "logs", "metrics"} {
		err = errors.Join(err, syncDirectory(filepath.Join(root, signal)))
	}
	return err
}
