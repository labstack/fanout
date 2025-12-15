package lake

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
)

// parseTimeFromPath parses partition time from a path (test helper)
func parseTimeFromPath(path string) (time.Time, error) {
	re := regexp.MustCompile(`year=(\d{4})/month=(\d{2})/day=(\d{2})/hour=(\d{2})`)
	matches := re.FindStringSubmatch(path)
	if matches == nil {
		return time.Time{}, os.ErrNotExist
	}

	year, _ := strconv.Atoi(matches[1])
	month, _ := strconv.Atoi(matches[2])
	day, _ := strconv.Atoi(matches[3])
	hour, _ := strconv.Atoi(matches[4])

	return time.Date(year, time.Month(month), day, hour, 0, 0, 0, time.UTC), nil
}

func TestParsePartitionTime(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"valid", "lake/spans/year=2024/month=01/day=15/hour=10/part-123.parquet", false},
		{"missing year", "lake/spans/month=01/day=15/hour=10/part-123.parquet", true},
		{"just dir", "year=2024/month=01/day=15/hour=10", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseTimeFromPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseTimeFromPath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestPruneSignal(t *testing.T) {
	// Create temp lake dir
	lakeDir, err := os.MkdirTemp("", "fanout-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(lakeDir)

	// Create old and new partition dirs
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -40)   // 40 days ago
	recent := now.AddDate(0, 0, -5) // 5 days ago

	oldPath := filepath.Join(lakeDir, "spans",
		"year="+old.Format("2006"),
		"month="+old.Format("01"),
		"day="+old.Format("02"),
		"hour="+old.Format("15"))
	recentPath := filepath.Join(lakeDir, "spans",
		"year="+recent.Format("2006"),
		"month="+recent.Format("01"),
		"day="+recent.Format("02"),
		"hour="+recent.Format("15"))

	if err := os.MkdirAll(oldPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(recentPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Create dummy parquet files
	if err := os.WriteFile(filepath.Join(oldPath, "part-1.parquet"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recentPath, "part-2.parquet"), []byte("recent"), 0644); err != nil {
		t.Fatal(err)
	}

	// Run pruner
	p := &Pruner{cfg: config.Config{LakeDir: lakeDir, RetentionDays: 30}}
	cutoff := now.AddDate(0, 0, -30)
	deleted, _ := p.pruneSignal("spans", cutoff)

	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	// Old should be gone
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("old partition should be deleted")
	}

	// Recent should exist
	if _, err := os.Stat(recentPath); os.IsNotExist(err) {
		t.Error("recent partition should still exist")
	}
}

func TestRetentionDisabled(t *testing.T) {
	p := NewPruner(config.Config{RetentionDays: 0})
	if p.cfg.RetentionDays != 0 {
		t.Error("retention should be disabled with 0 days")
	}
}
