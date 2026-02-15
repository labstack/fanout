package lake

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"

	"github.com/labstack/fanout/internal/config"
)

// Compactor merges small hourly parquet files into larger daily files
type Compactor struct {
	cfg config.Config
}

// NewCompactor creates a new compactor
func NewCompactor(cfg config.Config) *Compactor {
	return &Compactor{cfg: cfg}
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
	tenants, _ := os.ReadDir(baseDir)
	for _, tenantDir := range tenants {
		if !tenantDir.IsDir() || !strings.HasPrefix(tenantDir.Name(), "tenant=") {
			continue
		}
		tenantPath := filepath.Join(baseDir, tenantDir.Name())

		// Walk namespace directories
		namespaces, _ := os.ReadDir(tenantPath)
		for _, nsDir := range namespaces {
			if !nsDir.IsDir() || !strings.HasPrefix(nsDir.Name(), "namespace=") {
				continue
			}
			nsPath := filepath.Join(tenantPath, nsDir.Name())

			// Walk year directories
			years, _ := os.ReadDir(nsPath)
			for _, yearDir := range years {
				if !yearDir.IsDir() || !strings.HasPrefix(yearDir.Name(), "year=") {
					continue
				}
				year := parsePartition(yearDir.Name(), "year")
				yearPath := filepath.Join(nsPath, yearDir.Name())

				months, _ := os.ReadDir(yearPath)
				for _, monthDir := range months {
					if !monthDir.IsDir() || !strings.HasPrefix(monthDir.Name(), "month=") {
						continue
					}
					month := parsePartition(monthDir.Name(), "month")
					monthPath := filepath.Join(yearPath, monthDir.Name())

					days, _ := os.ReadDir(monthPath)
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

						saved, err := c.compactDay(signal, dayPath)
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
	compactedFile := filepath.Join(dayPath, "compacted.parquet")
	if _, err := os.Stat(compactedFile); err == nil {
		// Check if there are still hour directories
		entries, _ := os.ReadDir(dayPath)
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "hour=") {
				return false // Has hour dirs, needs compaction
			}
		}
		return true // Already compacted
	}
	return false
}

func (c *Compactor) compactDay(signal, dayPath string) (int64, error) {
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

	// Compact based on signal type
	var sizeAfter int64
	var compactErr error

	switch signal {
	case "spans":
		sizeAfter, compactErr = compactFiles[SpanRow](files, dayPath)
	case "logs":
		sizeAfter, compactErr = compactFiles[LogRow](files, dayPath)
	case "metrics":
		sizeAfter, compactErr = compactFiles[MetricRow](files, dayPath)
	}

	if compactErr != nil {
		return 0, compactErr
	}

	// Remove old hour directories
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "hour=") {
			os.RemoveAll(filepath.Join(dayPath, e.Name()))
		}
	}

	return sizeBefore - sizeAfter, nil
}

func compactFiles[T any](files []string, dayPath string) (int64, error) {
	// Read all rows from all files
	var allRows []T

	for _, f := range files {
		rows, err := readParquet[T](f)
		if err != nil {
			slog.Error("compact read failed", "file", f, "err", err)
			continue
		}
		allRows = append(allRows, rows...)
	}

	if len(allRows) == 0 {
		return 0, nil
	}

	// Write compacted file
	compactedPath := filepath.Join(dayPath, "compacted.parquet")
	tmpPath := compactedPath + ".tmp"

	tmp, err := os.Create(tmpPath)
	if err != nil {
		return 0, err
	}

	if err := parquet.Write(tmp, allRows, parquet.Compression(&zstd.Codec{})); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return 0, err
	}

	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return 0, err
	}
	tmp.Close()

	if err := os.Rename(tmpPath, compactedPath); err != nil {
		os.Remove(tmpPath)
		return 0, err
	}

	info, _ := os.Stat(compactedPath)
	if info != nil {
		return info.Size(), nil
	}
	return 0, nil
}

func readParquet[T any](path string) ([]T, error) {
	return parquet.ReadFile[T](path)
}
