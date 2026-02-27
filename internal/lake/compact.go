package lake

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/labstack/fanout/internal/config"
)

// Compactor merges small hourly parquet files into larger daily files
type Compactor struct {
	cfg config.Config
	db  *sql.DB
	mu  sync.Mutex
}

// NewCompactor creates a new compactor using DuckDB COPY for streaming
// compaction (constant memory).
func NewCompactor(cfg config.Config, db *sql.DB) *Compactor {
	return &Compactor{cfg: cfg, db: db}
}

// Run starts the compaction loop
func (c *Compactor) Run(ctx context.Context) {
	// Run every hour
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Run once at startup after a delay (let writes settle)
	time.Sleep(30 * time.Second)
	c.compactAll()

	for {
		select {
		case <-ticker.C:
			c.compactAll()
		case <-ctx.Done():
			return
		}
	}
}

func (c *Compactor) compactAll() {
	if !c.mu.TryLock() {
		slog.Info("compaction already running, skipping")
		return
	}
	defer c.mu.Unlock()

	// Only compact data older than 24 hours
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	slog.Info("compacting partitions", "older_than", cutoff.Format("2006-01-02 15:04"))

	signals := []string{"spans", "logs", "metrics"}
	for _, signal := range signals {
		compacted, saved := c.compactSignal(signal, cutoff)
		if compacted > 0 {
			slog.Info("compaction complete", "signal", signal, "days", compacted, "bytes_saved", saved, "saved_mb", float64(saved)/(1024*1024))
		}
	}
}

func (c *Compactor) compactSignal(signal string, cutoff time.Time) (int, int64) {
	baseDir := filepath.Join(c.cfg.LakeDir, signal)
	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		return 0, 0
	}

	var compacted int
	var bytesSaved int64

	// Walk tenant directories
	tenants, err := os.ReadDir(baseDir)
	if err != nil {
		slog.Warn("readdir failed", "path", baseDir, "err", err)
		return 0, 0
	}
	for _, tenantDir := range tenants {
		if !tenantDir.IsDir() || !strings.HasPrefix(tenantDir.Name(), "tenant=") {
			continue
		}
		tenantPath := filepath.Join(baseDir, tenantDir.Name())

		// Walk namespace directories
		namespaces, err := os.ReadDir(tenantPath)
		if err != nil {
			slog.Warn("readdir failed", "path", tenantPath, "err", err)
			continue
		}
		for _, nsDir := range namespaces {
			if !nsDir.IsDir() || !strings.HasPrefix(nsDir.Name(), "namespace=") {
				continue
			}
			nsPath := filepath.Join(tenantPath, nsDir.Name())

			// Walk year directories
			years, err := os.ReadDir(nsPath)
			if err != nil {
				slog.Warn("readdir failed", "path", nsPath, "err", err)
				continue
			}
			for _, yearDir := range years {
				if !yearDir.IsDir() || !strings.HasPrefix(yearDir.Name(), "year=") {
					continue
				}
				year := parsePartition(yearDir.Name(), "year")
				yearPath := filepath.Join(nsPath, yearDir.Name())

				months, err := os.ReadDir(yearPath)
				if err != nil {
					slog.Warn("readdir failed", "path", yearPath, "err", err)
					continue
				}
				for _, monthDir := range months {
					if !monthDir.IsDir() || !strings.HasPrefix(monthDir.Name(), "month=") {
						continue
					}
					month := parsePartition(monthDir.Name(), "month")
					monthPath := filepath.Join(yearPath, monthDir.Name())

					days, err := os.ReadDir(monthPath)
					if err != nil {
						slog.Warn("readdir failed", "path", monthPath, "err", err)
						continue
					}
					for _, dayDir := range days {
						if !dayDir.IsDir() || !strings.HasPrefix(dayDir.Name(), "day=") {
							continue
						}
						day := parsePartition(dayDir.Name(), "day")
						dayPath := filepath.Join(monthPath, dayDir.Name())

						// Check if this day is old enough to compact
						dayTime := time.Date(year, time.Month(month), day, 23, 59, 59, 0, time.UTC)
						if dayTime.After(cutoff) {
							continue
						}

						// Check if already compacted (has compacted.parquet and no hour dirs)
						if isCompacted(dayPath) {
							continue
						}

						saved, err := c.compactDay(dayPath)
						if err != nil {
							slog.Error("compaction failed", "signal", signal, "path", dayPath, "err", err)
							continue
						}
						compacted++
						bytesSaved += saved
					}
				}
			}
		}
	}

	return compacted, bytesSaved
}

func isCompacted(dayPath string) bool {
	// Check new location (hour=00/compacted.parquet)
	compactedFile := filepath.Join(dayPath, "hour=00", "compacted.parquet")
	if _, err := os.Stat(compactedFile); err == nil {
		// Already compacted if only hour=00 remains
		entries, err := os.ReadDir(dayPath)
		if err != nil {
			slog.Warn("readdir failed", "path", dayPath, "err", err)
			return false
		}
		hourDirs := 0
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "hour=") {
				hourDirs++
			}
		}
		return hourDirs <= 1 // Only hour=00 (the compacted dir)
	}
	return false
}

func (c *Compactor) compactDay(dayPath string) (int64, error) {
	// Collect all parquet files from hour directories
	var files []string
	var sizeBefore int64

	entries, err := os.ReadDir(dayPath)
	if err != nil {
		return 0, err
	}

	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "hour=") {
			continue
		}
		hourPath := filepath.Join(dayPath, e.Name())
		parquetFiles, _ := filepath.Glob(filepath.Join(hourPath, "*.parquet"))
		for _, f := range parquetFiles {
			info, _ := os.Stat(f)
			if info != nil {
				sizeBefore += info.Size()
			}
			files = append(files, f)
		}
	}

	if len(files) == 0 {
		return 0, nil
	}

	// Skip if only 1 file (nothing to compact)
	if len(files) == 1 {
		return 0, nil
	}

	sizeAfter, err := c.compactWithDuckDB(files, dayPath)
	if err != nil {
		return 0, err
	}

	// Remove old hour directories (keep hour=00 which has the compacted file).
	// Keep compacted file on partial failure — duplicates are safer than data loss.
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "hour=") && e.Name() != "hour=00" {
			if err := os.RemoveAll(filepath.Join(dayPath, e.Name())); err != nil {
				slog.Error("failed to remove hour dir after compaction, duplicates may exist",
					"dir", e.Name(), "path", dayPath, "err", err)
				return 0, fmt.Errorf("remove hour dir %s: %w", e.Name(), err)
			}
		}
	}
	// Remove old part-*.parquet files from hour=00, keeping only compacted.parquet
	hour0Path := filepath.Join(dayPath, "hour=00")
	if oldParts, _ := filepath.Glob(filepath.Join(hour0Path, "part-*.parquet")); len(oldParts) > 0 {
		for _, f := range oldParts {
			os.Remove(f)
		}
	}
	// Also remove old-style compacted.parquet at day level if present
	oldCompacted := filepath.Join(dayPath, "compacted.parquet")
	if _, err := os.Stat(oldCompacted); err == nil {
		os.Remove(oldCompacted)
	}

	return sizeBefore - sizeAfter, nil
}

// compactWithDuckDB uses DuckDB COPY for streaming compaction (constant memory).
func (c *Compactor) compactWithDuckDB(files []string, dayPath string) (int64, error) {
	// Write into hour=00 to maintain consistent Hive partitioning
	hourDir := filepath.Join(dayPath, "hour=00")
	if err := os.MkdirAll(hourDir, 0o755); err != nil {
		return 0, fmt.Errorf("create hour dir: %w", err)
	}
	compactedPath := filepath.Join(hourDir, "compacted.parquet")
	tmpPath := compactedPath + ".tmp"

	// Build file list for read_parquet
	quoted := make([]string, len(files))
	for i, f := range files {
		quoted[i] = "'" + strings.ReplaceAll(f, "'", "''") + "'"
	}
	fileList := "[" + strings.Join(quoted, ",") + "]"

	q := fmt.Sprintf(
		`COPY (SELECT * FROM read_parquet(%s, union_by_name=true)) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD)`,
		fileList, strings.ReplaceAll(tmpPath, "'", "''"),
	)

	if _, err := c.db.Exec(q); err != nil {
		if rmErr := os.Remove(tmpPath); rmErr != nil {
			slog.Warn("failed to clean up temp file", "path", tmpPath, "err", rmErr)
		}
		return 0, fmt.Errorf("duckdb compact: %w", err)
	}

	if err := os.Rename(tmpPath, compactedPath); err != nil {
		if rmErr := os.Remove(tmpPath); rmErr != nil {
			slog.Warn("failed to clean up temp file", "path", tmpPath, "err", rmErr)
		}
		return 0, fmt.Errorf("rename compacted file: %w", err)
	}

	info, err := os.Stat(compactedPath)
	if err != nil {
		slog.Warn("stat compacted file failed", "path", compactedPath, "err", err)
		return 0, nil
	}
	return info.Size(), nil
}
