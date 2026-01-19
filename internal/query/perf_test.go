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
	// Path structure: lake/{signal}/tenant=*/namespace=*/year=*/month=*/day=*/hour=*/
	dir := filepath.Join(
		lakeDir,
		"spans",
		"tenant=00000000-0000-0000-0000-000000000000",
		"namespace=default",
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

	glob := ParquetGlob(lakeDir, "spans", "00000000-0000-0000-0000-000000000000", "default", 120)
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

	glob := ParquetGlob(lakeDir, "spans", "test-tenant", "test-namespace", 15)
	if !strings.Contains(glob, "tenant=test-tenant/namespace=test-namespace/year=*/month=*/day=*/hour=*/part-*.parquet") {
		t.Fatalf("expected broad glob fallback, got: %s", glob)
	}
}
