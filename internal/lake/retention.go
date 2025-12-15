package lake

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/config"
)

// Pruner handles data retention by deleting old partitions
type Pruner struct {
	cfg config.Config
}

// NewPruner creates a new retention pruner
func NewPruner(cfg config.Config) *Pruner {
	return &Pruner{cfg: cfg}
}

// Run starts the retention pruner loop
func (p *Pruner) Run(ctx context.Context) {
	if p.cfg.RetentionDays <= 0 {
		log.Println("[retention] disabled (RETENTION_DAYS=0)")
		return
	}

	ticker := time.NewTicker(time.Duration(p.cfg.RetentionHours) * time.Hour)
	defer ticker.Stop()

	// Run once at startup
	p.pruneAll()

	for {
		select {
		case <-ticker.C:
			p.pruneAll()
		case <-ctx.Done():
			return
		}
	}
}

func (p *Pruner) pruneAll() {
	cutoff := time.Now().AddDate(0, 0, -p.cfg.RetentionDays)
	log.Printf("[retention] pruning data older than %s", cutoff.Format("2006-01-02"))

	signals := []string{"spans", "logs", "metrics"}
	for _, signal := range signals {
		deleted, bytes := p.pruneSignal(signal, cutoff)
		if deleted > 0 {
			log.Printf("[retention] %s: deleted %d partitions (%.2f MB)", signal, deleted, float64(bytes)/(1024*1024))
		}
	}
}

func (p *Pruner) pruneSignal(signal string, cutoff time.Time) (int, int64) {
	baseDir := filepath.Join(p.cfg.LakeDir, signal)
	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		return 0, 0
	}

	var deleted int
	var bytesDeleted int64

	// Walk year directories
	years, _ := os.ReadDir(baseDir)
	for _, yearDir := range years {
		if !yearDir.IsDir() || !strings.HasPrefix(yearDir.Name(), "year=") {
			continue
		}
		year := parsePartition(yearDir.Name(), "year")
		if year == 0 {
			continue
		}

		yearPath := filepath.Join(baseDir, yearDir.Name())
		months, _ := os.ReadDir(yearPath)

		for _, monthDir := range months {
			if !monthDir.IsDir() || !strings.HasPrefix(monthDir.Name(), "month=") {
				continue
			}
			month := parsePartition(monthDir.Name(), "month")
			if month == 0 {
				continue
			}

			monthPath := filepath.Join(yearPath, monthDir.Name())
			days, _ := os.ReadDir(monthPath)

			for _, dayDir := range days {
				if !dayDir.IsDir() || !strings.HasPrefix(dayDir.Name(), "day=") {
					continue
				}
				day := parsePartition(dayDir.Name(), "day")
				if day == 0 {
					continue
				}

				dayPath := filepath.Join(monthPath, dayDir.Name())
				hours, _ := os.ReadDir(dayPath)

				for _, hourDir := range hours {
					if !hourDir.IsDir() || !strings.HasPrefix(hourDir.Name(), "hour=") {
						continue
					}
					hour := parsePartition(hourDir.Name(), "hour")

					// Construct partition time
					partTime := time.Date(year, time.Month(month), day, hour, 0, 0, 0, time.UTC)

					if partTime.Before(cutoff) {
						hourPath := filepath.Join(dayPath, hourDir.Name())
						bytes := dirSize(hourPath)
						if err := os.RemoveAll(hourPath); err == nil {
							deleted++
							bytesDeleted += bytes
						}
					}
				}

				// Clean up empty day directory
				cleanEmptyDir(dayPath)
			}

			// Clean up empty month directory
			cleanEmptyDir(monthPath)
		}

		// Clean up empty year directory
		cleanEmptyDir(yearPath)
	}

	return deleted, bytesDeleted
}

func parsePartition(name, prefix string) int {
	// Parse "year=2025" -> 2025
	parts := strings.Split(name, "=")
	if len(parts) != 2 || parts[0] != prefix {
		return 0
	}
	val, _ := strconv.Atoi(parts[1])
	return val
}

func dirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}

func cleanEmptyDir(path string) {
	entries, err := os.ReadDir(path)
	if err == nil && len(entries) == 0 {
		os.Remove(path)
	}
}

// PruneStats returns retention statistics
type PruneStats struct {
	Signal         string    `json:"signal"`
	OldestData     time.Time `json:"oldest_data"`
	PartitionCount int       `json:"partition_count"`
	TotalBytes     int64     `json:"total_bytes"`
}

// Stats returns current data statistics per signal
func (p *Pruner) Stats() []PruneStats {
	signals := []string{"spans", "logs", "metrics"}
	var stats []PruneStats

	for _, signal := range signals {
		s := PruneStats{Signal: signal}
		baseDir := filepath.Join(p.cfg.LakeDir, signal)

		filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".parquet") {
				s.PartitionCount++
				s.TotalBytes += info.Size()

				// Parse time from path
				if t := parsePathTime(path, baseDir); !t.IsZero() {
					if s.OldestData.IsZero() || t.Before(s.OldestData) {
						s.OldestData = t
					}
				}
			}
			return nil
		})

		stats = append(stats, s)
	}

	return stats
}

func parsePathTime(path, baseDir string) time.Time {
	rel, _ := filepath.Rel(baseDir, path)
	parts := strings.Split(rel, string(filepath.Separator))

	var year, month, day, hour int
	for _, part := range parts {
		if strings.HasPrefix(part, "year=") {
			year = parsePartition(part, "year")
		} else if strings.HasPrefix(part, "month=") {
			month = parsePartition(part, "month")
		} else if strings.HasPrefix(part, "day=") {
			day = parsePartition(part, "day")
		} else if strings.HasPrefix(part, "hour=") {
			hour = parsePartition(part, "hour")
		}
	}

	if year > 0 && month > 0 && day > 0 {
		return time.Date(year, time.Month(month), day, hour, 0, 0, 0, time.UTC)
	}
	return time.Time{}
}

// FormatBytes formats bytes as human-readable string
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
