package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/labstack/pitot/internal/telemetry"
	"golang.org/x/sync/errgroup"
)

// BatchIssue is one authoritative batch that startup cannot validate.
type BatchIssue struct {
	ID   string
	Path string
	Err  error
}

// VerifyBatches validates every authoritative batch without opening a live
// repository or changing the filesystem. Batches verify concurrently on one
// two-thread engine per two CPUs, each with its own fixed memory limit, so a
// large set uses the machine while no query grows with it; GOMAXPROCS bounds
// both. Issues keep directory order.
func VerifyBatches(root string) ([]BatchIssue, error) {
	batchesDir := filepath.Join(root, "parquet", "batches")
	entries, err := os.ReadDir(batchesDir)
	if err != nil {
		return nil, fmt.Errorf("read telemetry batches: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != telemetry.SchemaBatch && strings.HasSuffix(entry.Name(), telemetry.BatchSuffix) {
			names = append(names, entry.Name())
		}
	}
	// Every engine opens before any batch is read, so a worker's failure
	// cannot cancel a later open and hide its own error.
	verifiers := make([]*telemetry.BatchVerifier, max(1, min(runtime.GOMAXPROCS(0)/2, len(names))))
	for i := range verifiers {
		if verifiers[i], err = telemetry.NewBatchVerifier(context.Background(), batchesDir); err != nil {
			for _, v := range verifiers[:i] {
				_ = v.Close()
			}
			return nil, err
		}
	}
	group, ctx := errgroup.WithContext(context.Background())
	results := make([]error, len(names))
	var next atomic.Int64
	for _, verifier := range verifiers {
		group.Go(func() error {
			defer verifier.Close()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(names) {
					return nil
				}
				err := verifier.Validate(ctx, filepath.Join(batchesDir, names[i]))
				if err != nil && !telemetry.IsBatchCorrupt(err) {
					return fmt.Errorf("verify batch %s: %w", names[i], err)
				}
				results[i] = err
			}
		})
	}
	err = group.Wait()
	var issues []BatchIssue
	for i, verifyErr := range results {
		if verifyErr != nil {
			issues = append(issues, BatchIssue{
				ID: strings.TrimSuffix(names[i], telemetry.BatchSuffix), Path: filepath.Join(batchesDir, names[i]), Err: verifyErr,
			})
		}
	}
	return issues, err
}

// QuarantineBatch atomically sets aside one specifically named unreadable
// batch. Valid data and any batch protected by a live compaction transaction
// are refused. The returned directory remains beside the authoritative set so
// an operator can recover it from a backup or rename it back after repair.
func QuarantineBatch(root, id string) (string, error) {
	return quarantineBatch(root, id, telemetry.ValidatePublishedBatch)
}

func quarantineBatch(root, id string, validate func(string) error) (string, error) {
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
	if err := validate(source); err == nil {
		return "", fmt.Errorf("telemetry batch %s is valid; refusing to quarantine authoritative data", id)
	} else if !telemetry.IsBatchCorrupt(err) {
		return "", fmt.Errorf("cannot establish batch corruption; refusing quarantine: %w", err)
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
