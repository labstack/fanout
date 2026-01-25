package lake

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
)

func TestNewCompactor(t *testing.T) {
	cfg := config.Config{LakeDir: "/tmp/test"}
	c := NewCompactor(cfg)
	if c == nil {
		t.Fatal("NewCompactor returned nil")
	}
	if c.cfg.LakeDir != "/tmp/test" {
		t.Errorf("cfg.LakeDir = %q, want /tmp/test", c.cfg.LakeDir)
	}
}

func TestIsCompacted(t *testing.T) {
	// Create temp dir
	dir, err := os.MkdirTemp("", "compact-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	tests := []struct {
		name  string
		setup func(string)
		want  bool
	}{
		{
			name: "no compacted file",
			setup: func(d string) {
				os.MkdirAll(filepath.Join(d, "hour=10"), 0755)
			},
			want: false,
		},
		{
			name: "compacted with hour dirs",
			setup: func(d string) {
				os.WriteFile(filepath.Join(d, "compacted.parquet"), []byte("data"), 0644)
				os.MkdirAll(filepath.Join(d, "hour=10"), 0755)
			},
			want: false,
		},
		{
			name: "fully compacted",
			setup: func(d string) {
				os.WriteFile(filepath.Join(d, "compacted.parquet"), []byte("data"), 0644)
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			subDir := filepath.Join(dir, tc.name)
			os.MkdirAll(subDir, 0755)
			tc.setup(subDir)

			got := isCompacted(subDir)
			if got != tc.want {
				t.Errorf("isCompacted() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCompactSignal_EmptyDir(t *testing.T) {
	dir, err := os.MkdirTemp("", "compact-empty-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	c := &Compactor{cfg: config.Config{LakeDir: dir}}

	// Non-existent signal dir
	compacted, saved := c.compactSignal("spans", time.Now().Add(-48*time.Hour))
	if compacted != 0 || saved != 0 {
		t.Errorf("expected 0, 0 for non-existent dir, got %d, %d", compacted, saved)
	}
}

func TestCompactDay_NoFiles(t *testing.T) {
	dir, err := os.MkdirTemp("", "compact-day-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Create empty hour directory
	hourDir := filepath.Join(dir, "hour=10")
	os.MkdirAll(hourDir, 0755)

	c := &Compactor{cfg: config.Config{LakeDir: dir}}
	saved, err := c.compactDay("spans", dir)
	if err != nil {
		t.Errorf("compactDay() error = %v", err)
	}
	if saved != 0 {
		t.Errorf("saved = %d, want 0 for empty dir", saved)
	}
}

func TestCompactDay_SingleFile(t *testing.T) {
	dir, err := os.MkdirTemp("", "compact-single-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Create single parquet file
	hourDir := filepath.Join(dir, "hour=10")
	os.MkdirAll(hourDir, 0755)
	os.WriteFile(filepath.Join(hourDir, "part-1.parquet"), []byte("data"), 0644)

	c := &Compactor{cfg: config.Config{LakeDir: dir}}
	saved, err := c.compactDay("spans", dir)
	if err != nil {
		t.Errorf("compactDay() error = %v", err)
	}
	// Single file should be skipped
	if saved != 0 {
		t.Errorf("saved = %d, want 0 for single file", saved)
	}
}
