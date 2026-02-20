package lake

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
)

func TestIsCompacted(t *testing.T) {
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
			name: "compacted with other hour dirs",
			setup: func(d string) {
				os.MkdirAll(filepath.Join(d, "hour=00"), 0755)
				os.WriteFile(filepath.Join(d, "hour=00", "compacted.parquet"), []byte("data"), 0644)
				os.MkdirAll(filepath.Join(d, "hour=10"), 0755)
			},
			want: false,
		},
		{
			name: "fully compacted",
			setup: func(d string) {
				os.MkdirAll(filepath.Join(d, "hour=00"), 0755)
				os.WriteFile(filepath.Join(d, "hour=00", "compacted.parquet"), []byte("data"), 0644)
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(dir)

			if got := isCompacted(dir); got != tc.want {
				t.Errorf("isCompacted() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCompactSignal_EmptyDir(t *testing.T) {
	c := &Compactor{cfg: config.Config{LakeDir: t.TempDir()}}

	compacted, saved := c.compactSignal("spans", time.Now().Add(-48*time.Hour))
	if compacted != 0 || saved != 0 {
		t.Errorf("got (%d, %d), want (0, 0) for non-existent signal dir", compacted, saved)
	}
}

func TestCompactDay(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(dir string)
		wantSaved int64
	}{
		{
			name: "no files",
			setup: func(dir string) {
				os.MkdirAll(filepath.Join(dir, "hour=10"), 0755)
			},
			wantSaved: 0,
		},
		{
			name: "single file skipped",
			setup: func(dir string) {
				hourDir := filepath.Join(dir, "hour=10")
				os.MkdirAll(hourDir, 0755)
				os.WriteFile(filepath.Join(hourDir, "part-1.parquet"), []byte("data"), 0644)
			},
			wantSaved: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(dir)

			c := &Compactor{cfg: config.Config{LakeDir: dir}}
			saved, err := c.compactDay("spans", dir)
			if err != nil {
				t.Fatalf("compactDay: %v", err)
			}
			if saved != tc.wantSaved {
				t.Errorf("saved = %d, want %d", saved, tc.wantSaved)
			}
		})
	}
}
