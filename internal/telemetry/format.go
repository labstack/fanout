package telemetry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func validateBatchFormats(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		// Only an absent root denotes a new store. A missing file inside an existing
		// dataset is an error and must not trigger initialization.
		if _, rootErr := os.Stat(dir); errors.Is(rootErr, os.ErrNotExist) {
			return nil
		}
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		// Quarantine is outside the authoritative set. Retired inputs may still
		// be needed for compaction recovery and must be checked before cleanup.
		active := strings.HasSuffix(name, BatchSuffix)
		retired := strings.HasSuffix(name, ".retired") || strings.Contains(name, ".retired-")
		if !entry.IsDir() || name == SchemaBatch || (!active && (!retired || strings.Contains(name, ".quarantined-"))) {
			continue
		}
		if _, err := readBatchMetadata(filepath.Join(dir, name, "metadata.json")); err != nil {
			return err
		}
	}
	return nil
}
