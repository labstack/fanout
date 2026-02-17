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
		"tenant=default", "namespace=default",
		"year="+old.Format("2006"),
		"month="+old.Format("01"),
		"day="+old.Format("02"),
		"hour="+old.Format("15"))
	recentPath := filepath.Join(lakeDir, "spans",
		"tenant=default", "namespace=default",
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

func TestParsePartition(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		prefix string
		want   int
	}{
		{"valid year", "year=2024", "year", 2024},
		{"valid month", "month=06", "month", 6},
		{"valid day", "day=15", "day", 15},
		{"valid hour", "hour=10", "hour", 10},
		{"wrong prefix", "year=2024", "month", 0},
		{"invalid format", "invalid", "year", 0},
		{"empty value", "year=", "year", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePartition(tc.input, tc.prefix)
			if got != tc.want {
				t.Errorf("parsePartition(%q, %q) = %d, want %d", tc.input, tc.prefix, got, tc.want)
			}
		})
	}
}

func TestDirSize(t *testing.T) {
	dir, err := os.MkdirTemp("", "dirsize-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Create files
	os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("hello"), 0644)
	os.WriteFile(filepath.Join(dir, "file2.txt"), []byte("world!"), 0644)

	size := dirSize(dir)
	// "hello" = 5, "world!" = 6
	if size != 11 {
		t.Errorf("dirSize() = %d, want 11", size)
	}
}

func TestDirSize_Empty(t *testing.T) {
	dir, err := os.MkdirTemp("", "dirsize-empty-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	size := dirSize(dir)
	if size != 0 {
		t.Errorf("dirSize() = %d, want 0 for empty dir", size)
	}
}

func TestCleanEmptyDir(t *testing.T) {
	dir, err := os.MkdirTemp("", "cleandir-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	emptyDir := filepath.Join(dir, "empty")
	os.MkdirAll(emptyDir, 0755)

	cleanEmptyDir(emptyDir)

	if _, err := os.Stat(emptyDir); !os.IsNotExist(err) {
		t.Error("empty dir should be removed")
	}
}

func TestCleanEmptyDir_NotEmpty(t *testing.T) {
	dir, err := os.MkdirTemp("", "cleandir-notempty-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	notEmptyDir := filepath.Join(dir, "notempty")
	os.MkdirAll(notEmptyDir, 0755)
	os.WriteFile(filepath.Join(notEmptyDir, "file.txt"), []byte("data"), 0644)

	cleanEmptyDir(notEmptyDir)

	if _, err := os.Stat(notEmptyDir); os.IsNotExist(err) {
		t.Error("non-empty dir should not be removed")
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			got := FormatBytes(tc.bytes)
			if got != tc.want {
				t.Errorf("FormatBytes(%d) = %q, want %q", tc.bytes, got, tc.want)
			}
		})
	}
}

func TestParsePathTime(t *testing.T) {
	baseDir := "/lake/spans"
	tests := []struct {
		name     string
		path     string
		wantZero bool
	}{
		{"valid path", "/lake/spans/year=2024/month=06/day=15/hour=10/part.parquet", false},
		{"missing hour", "/lake/spans/year=2024/month=06/day=15/part.parquet", false},
		{"incomplete", "/lake/spans/year=2024/part.parquet", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePathTime(tc.path, baseDir)
			if tc.wantZero && !got.IsZero() {
				t.Errorf("parsePathTime() = %v, want zero", got)
			}
			if !tc.wantZero && got.IsZero() {
				t.Error("parsePathTime() returned zero, want non-zero")
			}
		})
	}
}

func TestPrunerStats(t *testing.T) {
	dir, err := os.MkdirTemp("", "stats-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Create parquet file
	partitionDir := filepath.Join(dir, "spans", "year=2024", "month=06", "day=15", "hour=10")
	os.MkdirAll(partitionDir, 0755)
	os.WriteFile(filepath.Join(partitionDir, "part-1.parquet"), []byte("data"), 0644)

	p := NewPruner(config.Config{LakeDir: dir})
	stats := p.Stats()

	if len(stats) != 3 {
		t.Errorf("Stats() returned %d signals, want 3", len(stats))
	}

	var spanStats *PruneStats
	for i := range stats {
		if stats[i].Signal == "spans" {
			spanStats = &stats[i]
			break
		}
	}

	if spanStats == nil {
		t.Fatal("spans stats not found")
	}

	if spanStats.PartitionCount != 1 {
		t.Errorf("PartitionCount = %d, want 1", spanStats.PartitionCount)
	}
	if spanStats.TotalBytes == 0 {
		t.Error("TotalBytes should not be 0")
	}
	if spanStats.OldestData.IsZero() {
		t.Error("OldestData should not be zero")
	}
}
