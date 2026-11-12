package store

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func previousControlDatabasePath(current string) string {
	// Intentional compatibility exception: unattended upgrades must find the
	// previous on-disk database instead of initializing an unclaimed instance.
	return filepath.Join(filepath.Dir(current), "fanout.sqlite")
}

// MigrateControlDatabase preserves control state before any SQLite open can
// create a new, empty database. The service must be stopped during upgrades.
func MigrateControlDatabase(current string) error {
	if filepath.Base(current) != "pitot.sqlite" || filepath.Base(filepath.Dir(current)) != "control" {
		return nil
	}
	dir, err := os.Open(filepath.Dir(current))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect control directory: %w", err)
	}
	defer dir.Close()
	// Serialize startup and CLI migrations without creating another on-disk name.
	if err := unix.Flock(int(dir.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock control directory for database rename: %w", err)
	}
	defer unix.Flock(int(dir.Fd()), unix.LOCK_UN) //nolint:errcheck // Closing the descriptor also releases the lock.

	exists := func(path string) (bool, error) {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("control database and sidecars must be regular files: %s", path)
		}
		return true, nil
	}
	previous := previousControlDatabasePath(current)
	oldExists, err := exists(previous)
	if err != nil {
		return fmt.Errorf("inspect previous control database: %w", err)
	}
	newExists, err := exists(current)
	if err != nil {
		return fmt.Errorf("inspect current control database: %w", err)
	}
	if oldExists && newExists {
		return fmt.Errorf("both control database names exist; stop the instance and reconcile control/ from a trusted backup before restarting")
	}
	var sidecars []string
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		oldSidecar, err := exists(previous + suffix)
		if err != nil {
			return fmt.Errorf("inspect previous control sidecar: %w", err)
		}
		newSidecar, err := exists(current + suffix)
		if err != nil {
			return fmt.Errorf("inspect current control sidecar: %w", err)
		}
		// An interrupted move must fail closed, never open a database without its
		// journal or discard a sidecar that might contain committed transactions.
		if (oldSidecar && !oldExists) || (newSidecar && !newExists) {
			return fmt.Errorf("orphaned or conflicting control database sidecars; stop the instance and recover the complete database file set before restarting")
		}
		if oldSidecar {
			sidecars = append(sidecars, suffix)
		}
	}
	if !oldExists {
		return nil
	}
	// Rename sidecars first, then sync their directory entries before committing
	// the main filename. A crash partway through leaves a refused partial state.
	for _, suffix := range sidecars {
		if err := os.Rename(previous+suffix, current+suffix); err != nil {
			return fmt.Errorf("rename control database sidecar: %w", err)
		}
	}
	if len(sidecars) > 0 {
		if err := dir.Sync(); err != nil {
			return fmt.Errorf("sync control sidecar renames: %w", err)
		}
	}
	if err := os.Rename(previous, current); err != nil {
		return fmt.Errorf("rename control database: %w", err)
	}
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync control database rename: %w", err)
	}
	slog.Info("migrated control database to the current name")
	return nil
}
