package query

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWrapQuotes(t *testing.T) {
	tests := []struct {
		input    []string
		expected []string
	}{
		{[]string{"a", "b"}, []string{"'a'", "'b'"}},
		{[]string{"file.parquet"}, []string{"'file.parquet'"}},
		{[]string{}, []string{}},
		{[]string{"it's a test"}, []string{"'it''s a test'"}},
	}

	for _, tc := range tests {
		result := wrapQuotes(tc.input)
		if len(result) != len(tc.expected) {
			t.Errorf("wrapQuotes(%v) len = %d, want %d", tc.input, len(result), len(tc.expected))
			continue
		}
		for i, v := range result {
			if v != tc.expected[i] {
				t.Errorf("wrapQuotes(%v)[%d] = %q, want %q", tc.input, i, v, tc.expected[i])
			}
		}
	}
}

func TestSqlQuote(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "'hello'"},
		{"it's", "'it''s'"},
		{"file.parquet", "'file.parquet'"},
		{"", "''"},
		{"multiple 'quotes' here", "'multiple ''quotes'' here'"},
	}

	for _, tc := range tests {
		result := sqlQuote(tc.input)
		if result != tc.expected {
			t.Errorf("sqlQuote(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func TestCache_SetAndGet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cache := NewCache(ctx, 1*time.Second)

	// Set a value
	cache.Set("key1", "value1")

	// Get should return the value
	val, ok := cache.Get("key1")
	if !ok {
		t.Error("Get() should return true for existing key")
	}
	if val != "value1" {
		t.Errorf("Get() = %v, want %v", val, "value1")
	}

	// Get non-existent key
	_, ok = cache.Get("nonexistent")
	if ok {
		t.Error("Get() should return false for non-existent key")
	}
}

func TestCache_Expiry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cache := NewCache(ctx, 50*time.Millisecond)

	cache.Set("key1", "value1")

	// Should exist immediately
	_, ok := cache.Get("key1")
	if !ok {
		t.Error("Get() should return true immediately after Set()")
	}

	// Wait for expiry
	time.Sleep(100 * time.Millisecond)

	// Should be expired
	_, ok = cache.Get("key1")
	if ok {
		t.Error("Get() should return false after TTL expires")
	}
}

func TestCache_OverwriteValue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cache := NewCache(ctx, 1*time.Second)

	cache.Set("key1", "value1")
	cache.Set("key1", "value2")

	val, ok := cache.Get("key1")
	if !ok {
		t.Error("Get() should return true for existing key")
	}
	if val != "value2" {
		t.Errorf("Get() = %v, want %v (overwritten value)", val, "value2")
	}
}

func TestParquetGlob_OnlyReturnsExistingFiles(t *testing.T) {
	lakeDir := t.TempDir()

	now := time.Now().UTC() // Writer and glob both use UTC
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
	lakeDir := t.TempDir()

	glob := ParquetGlob(lakeDir, "spans", "test-tenant", "test-namespace", 15)
	if !strings.Contains(glob, "tenant=test-tenant/namespace=test-namespace/year=*/month=*/day=*/hour=*/*.parquet") {
		t.Fatalf("expected broad glob fallback, got: %s", glob)
	}
}

func TestParquetGlob_EmptyNamespaceUsesWildcard(t *testing.T) {
	lakeDir := t.TempDir()

	glob := ParquetGlob(lakeDir, "spans", "test-tenant", "", 15)
	if !strings.Contains(glob, "namespace=*") {
		t.Fatalf("expected namespace=* wildcard, got: %s", glob)
	}
}

func TestParquetGlob_WildcardNamespaceFindsMultipleNamespaces(t *testing.T) {
	lakeDir := t.TempDir()
	now := time.Now().UTC()
	tenant := "test-tenant"

	// Create files in two namespaces
	for _, ns := range []string{"prod", "staging"} {
		dir := filepath.Join(lakeDir, "spans", "tenant="+tenant, "namespace="+ns,
			now.Format("year=2006"), now.Format("month=01"), now.Format("day=02"), now.Format("hour=15"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "part-1.parquet"), []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	glob := ParquetGlob(lakeDir, "spans", tenant, "", 120)
	if !strings.Contains(glob, "namespace=prod") {
		t.Fatalf("expected prod namespace in result, got: %s", glob)
	}
	if !strings.Contains(glob, "namespace=staging") {
		t.Fatalf("expected staging namespace in result, got: %s", glob)
	}
}

func TestParquetGlob_WildcardNamespaceFindsCompactedFiles(t *testing.T) {
	lakeDir := t.TempDir()
	now := time.Now().UTC()
	tenant := "test-tenant"

	// Create compacted file at hour=00 (matches compactor output path)
	dayBase := filepath.Join(lakeDir, "spans", "tenant="+tenant, "namespace=myns",
		now.Format("year=2006"), now.Format("month=01"), now.Format("day=02"))
	compactedDir := filepath.Join(dayBase, "hour=00")
	if err := os.MkdirAll(compactedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(compactedDir, "compacted.parquet"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Use wildcard namespace
	glob := ParquetGlob(lakeDir, "spans", tenant, "", 120)
	if !strings.Contains(glob, "compacted.parquet") {
		t.Fatalf("expected compacted file in result, got: %s", glob)
	}
}
