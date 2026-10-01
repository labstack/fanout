package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"
)

func TestVariantRoundTripAcrossRowGroupsAndCompaction(t *testing.T) {
	rows := make([]logParquetRow, parquetRowGroupRows+257)
	for i := range rows {
		// Churn past the bounded shared metadata dictionary, with conflicting
		// types in shredded fields and row boundaries in the middle of a chunk.
		key := fmt.Sprintf("key.%03d.%s", i%128, strings.Repeat("x", 40))
		var system any = "kafka"
		if i%3 == 0 {
			system = int64(i)
		}
		rows[i] = logParquetRow{LogTime: int64(i), Attributes: map[string]any{
			"messaging.system": system, key: int64(math.MaxInt64), "zero": int64(0), "false": false,
			"null": nil, "bytes": []byte{0, 255}, "nested": []any{int64(i), map[string]any{"ok": true}},
		}, Resource: map[string]any{"service.name": "test"}}
	}
	dir := t.TempDir()
	first := filepath.Join(dir, "first.parquet")
	if err := writeTypedParquet(first, rows, parquetPageSize); err != nil {
		t.Fatal(err)
	}
	checkVariantRows(t, first, rows)
	second := filepath.Join(dir, "second.parquet")
	if err := writeTypedParquet(second, rows[:3], parquetPageSize); err != nil {
		t.Fatal(err)
	}
	merged := filepath.Join(dir, "merged.parquet")
	if err := mergeTypedParquet[logParquetRow](context.Background(), []string{first, second}, merged, false); err != nil {
		t.Fatal(err)
	}
	checkVariantRows(t, merged, append(rows, rows[:3]...))
}

func checkVariantRows(t *testing.T, path string, want []logParquetRow) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader := parquet.NewGenericReader[logParquetRow](file, telemetrySchema[logParquetRow]())
	defer reader.Close()
	buffer := make([]logParquetRow, 256)
	for offset := 0; offset < len(want); {
		n, err := reader.Read(buffer[:min(len(buffer), len(want)-offset)])
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
		if n == 0 {
			t.Fatal("reader made no progress")
		}
		for i, got := range buffer[:n] {
			if !reflect.DeepEqual(got, want[offset+i]) {
				t.Fatalf("row %d changed: %#v", offset+i, got)
			}
		}
		offset += n
	}
}

func TestUnsupportedFormatLeavesStoreUntouched(t *testing.T) {
	for _, suffix := range []string{BatchSuffix, ".retired", ".retired-replacement"} {
		t.Run(suffix, func(t *testing.T) { testUnsupportedFormatLeavesStoreUntouched(t, suffix) })
	}
}

func testUnsupportedFormatLeavesStoreUntouched(t *testing.T, suffix string) {
	t.Helper()
	dir := t.TempDir()
	store, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitBatch(context.Background(), BatchMetadata{ID: "old"}, []Span{{TraceID: "trace"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(store.BatchPath("old"), "metadata.json")
	if err := os.WriteFile(metadata, []byte(`{"version":2,"id":"old","spans":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	if suffix != BatchSuffix {
		if err := os.Rename(store.BatchPath("old"), filepath.Join(store.batchesDir, "old"+suffix)); err != nil {
			t.Fatal(err)
		}
	}
	sentinel := filepath.Join(store.stagingDir, "preserve")
	if err := os.WriteFile(sentinel, []byte("unpublished bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			before[path] = data
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenParquetStore(dir); err == nil {
		t.Fatal("opened obsolete format")
	}
	for path, want := range before {
		got, err := os.ReadFile(path)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("startup changed %s: %v", path, err)
		}
	}
}
