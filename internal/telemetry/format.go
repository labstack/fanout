package telemetry

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

func validateBatchFormats(dir string) error {
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == "metadata.json" {
			_, err := readBatchMetadata(path)
			return err
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		// Only an absent root denotes a new store. A missing file inside an existing
		// dataset is an error and must not trigger initialization.
		if _, rootErr := os.Stat(dir); errors.Is(rootErr, os.ErrNotExist) {
			return nil
		}
	}
	return err
}
