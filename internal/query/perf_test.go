package query

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParquetGlob_OnlyReturnsExistingFiles(t *testing.T) {
	lakeDir, err := os.MkdirTemp("", "fanout-parquetglob-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(lakeDir)

	now := time.Now()
	dir := filepath.Join(
		lakeDir,
		"spans",
		now.Format("year=2006"),
		now.Format("month=01"),
		now.Format("day=02"),
		now.Format("hour=15"),
	)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	fpath := filepath.Join(dir, "part-1.parquet")
	if err := os.WriteFile(fpath, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	glob := ParquetGlob(lakeDir, "spans", 120)
	if !strings.Contains(glob, "part-1.parquet") {
		t.Fatalf("expected glob to include parquet file path, got: %s", glob)
	}
	if strings.Contains(glob, "part-*.parquet") {
		t.Fatalf("expected glob to expand to concrete files (not patterns), got: %s", glob)
	}
}

func TestParquetGlob_NoFilesFallsBackToBroadGlob(t *testing.T) {
	lakeDir, err := os.MkdirTemp("", "fanout-parquetglob-nofiles-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(lakeDir)

	glob := ParquetGlob(lakeDir, "spans", 15)
	if !strings.Contains(glob, "year=*/month=*/day=*/hour=*/part-*.parquet") {
		t.Fatalf("expected broad glob fallback, got: %s", glob)
	}
}
