package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

// BatchIssue is one authoritative batch that startup cannot validate.
type BatchIssue struct {
	ID   string
	Path string
	Err  error
}

// VerifyBatches validates every authoritative batch without opening a live
// repository or changing the filesystem.
func VerifyBatches(root string) ([]BatchIssue, error) {
	batchesDir := filepath.Join(root, "parquet", "batches")
	entries, err := os.ReadDir(batchesDir)
	if err != nil {
		return nil, fmt.Errorf("read telemetry batches: %w", err)
	}
	var issues []BatchIssue
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == telemetry.SchemaBatch || !strings.HasSuffix(entry.Name(), telemetry.BatchSuffix) {
			continue
		}
		path := filepath.Join(batchesDir, entry.Name())
		if err := telemetry.ValidatePublishedBatch(path); err != nil {
			issues = append(issues, BatchIssue{
				ID: strings.TrimSuffix(entry.Name(), telemetry.BatchSuffix), Path: path, Err: err,
			})
		}
	}
	return issues, nil
}

// QuarantineBatch atomically sets aside one specifically named unreadable
// batch. Valid data and any batch protected by a live compaction transaction
// are refused. The returned directory remains beside the authoritative set so
// an operator can recover it from a backup or rename it back after repair.
func QuarantineBatch(root, id string) (string, error) {
	if err := telemetry.ValidateBatchID(id); err != nil {
		return "", err
	}
	if id+telemetry.BatchSuffix == telemetry.SchemaBatch {
		return "", errors.New("the Parquet schema batch cannot be quarantined")
	}
	if err := ensureBatchOutsideLiveCompaction(root, id); err != nil {
		return "", err
	}
	batchesDir := filepath.Join(root, "parquet", "batches")
	source := filepath.Join(batchesDir, id+telemetry.BatchSuffix)
	info, err := os.Lstat(source)
	if err != nil {
		return "", fmt.Errorf("inspect telemetry batch %s: %w", id, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("telemetry batch %s is not a directory", id)
	}
	if err := telemetry.ValidatePublishedBatch(source); err == nil {
		return "", fmt.Errorf("telemetry batch %s is valid; refusing to quarantine authoritative data", id)
	}
	destination := filepath.Join(batchesDir, fmt.Sprintf("%s.quarantined-%d", id, time.Now().UTC().UnixNano()))
	if err := os.Rename(source, destination); err != nil {
		return "", fmt.Errorf("quarantine telemetry batch %s: %w", id, err)
	}
	if err := syncDirectory(batchesDir); err != nil {
		return destination, fmt.Errorf("quarantined telemetry batch %s at %s but could not sync the directory: %w", id, destination, err)
	}
	return destination, nil
}

func ensureBatchOutsideLiveCompaction(root, id string) error {
	path := filepath.Join(root, "COMPACTION.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect live compaction marker: %w", err)
	}
	var marker compactionMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return fmt.Errorf("read live compaction marker: %w", err)
	}
	if err := validateCompactionMarker(marker); err != nil {
		return fmt.Errorf("read live compaction marker: %w", err)
	}
	if marker.Output.ID == id {
		return fmt.Errorf("telemetry batch %s belongs to a live compaction; resolve that transaction before repair", id)
	}
	for _, input := range marker.Inputs {
		if input == id {
			return fmt.Errorf("telemetry batch %s belongs to a live compaction; resolve that transaction before repair", id)
		}
	}
	return nil
}
