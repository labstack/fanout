package telemetry

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestVariantRoundTripAcrossRowGroupsAndCompaction(t *testing.T) {
	rows := make([]Log, parquetRowGroupRows+257)
	for i := range rows {
		// Churn past the bounded shared metadata dictionary, with conflicting
		// types in shredded fields and row boundaries in the middle of a chunk.
		key := fmt.Sprintf("key.%03d.%s", i%128, strings.Repeat("x", 40))
		var system any = "kafka"
		if i%3 == 0 {
			system = int64(i)
		}
		rows[i] = Log{EventUnixNanos: int64(i + 1), Attributes: map[string]any{
			"messaging.system": system, key: int64(math.MaxInt64), "zero": int64(0), "false": false,
			"null": nil, "bytes": []byte{0, 255}, "nested": []any{int64(i), map[string]any{"ok": true}},
		}, Resource: map[string]any{"service.name": "test", "service.namespace": int64(i), key: int64(math.MaxInt64), "bytes": []byte{0, 255}, "nested": []any{false, map[string]any{"zero": int64(0)}}, "null": nil}}
	}
	dir := t.TempDir()
	first := filepath.Join(dir, "first.parquet")
	if err := writeParquet(first, Logs, rows, parquetPageSize); err != nil {
		t.Fatal(err)
	}
	checkVariantRows(t, first, rows)
	second := filepath.Join(dir, "second.parquet")
	if err := writeParquet(second, Logs, rows[:3], parquetPageSize); err != nil {
		t.Fatal(err)
	}
	merged := filepath.Join(dir, "merged.parquet")
	if err := mergeParquet(context.Background(), Logs, []string{first, second}, merged, &ParquetMergeStats{}); err != nil {
		t.Fatal(err)
	}
	checkVariantRows(t, merged, append(rows, rows[:3]...))
}

func TestSharedResourceCellsPreserveInterleavedRows(t *testing.T) {
	shared := map[string]any{"service.name": "shared", "integer": int64(math.MaxInt64), "bytes": []byte{0, 255}, "nested": []any{false, map[string]any{"null": nil}}}
	objects := make([]map[string]any, 80)
	for i := range objects {
		objects[i] = map[string]any{"service.name": fmt.Sprint(i), "integer": int64(i)}
	}
	rows := make([]Log, parquetRowGroupRows+257)
	for i := range rows {
		var resource map[string]any
		switch i % 5 {
		case 0:
			resource = shared
		case 1:
			resource = map[string]any{"unique": int64(i), "bytes": []byte{byte(i)}}
		case 2:
			resource = objects[(i/5)%len(objects)]
		case 3, 4:
			resource = nil
		}
		rows[i] = Log{EventUnixNanos: int64(i + 1), Resource: resource}
	}
	path := filepath.Join(t.TempDir(), "interleaved.parquet")
	if err := writeParquet(path, Logs, rows, parquetPageSize); err != nil {
		t.Fatal(err)
	}
	// An absent canonical resource is SQL NULL, not VARIANT null.
	checkVariantRows(t, path, rows)
}

// Go randomizes map order, and variant metadata interns names in first-use
// order. Equal rows must still encode to equal files.
func TestVariantEncodingIsDeterministic(t *testing.T) {
	shared := map[string]any{"service.name": "shared", "host.name": "one", "k8s.pod.name": "pod", "cloud.region": "eu"}
	rows := make([]Log, 600)
	for i := range rows {
		resource := shared
		if i%3 == 0 {
			resource = map[string]any{"service.name": "own", "zone": fmt.Sprint(i % 7), "rack": "r1", "row": "a"}
		}
		rows[i] = Log{EventUnixNanos: int64(i + 1), Resource: resource, Attributes: map[string]any{
			"alpha": int64(i), "beta": "b", "gamma": true, "delta": 1.5, "epsilon": "e", "zeta": nil,
			"nested": map[string]any{"z": int64(1), "y": []any{map[string]any{"b": "1", "a": "2"}}, "x": "3"},
		}}
	}
	dir := t.TempDir()
	var files [][]byte
	for i := range 4 {
		path := filepath.Join(dir, fmt.Sprintf("%d.parquet", i))
		if err := writeParquet(path, Logs, rows, parquetPageSize); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, data)
	}
	for i := 1; i < len(files); i++ {
		if !bytes.Equal(files[0], files[i]) {
			t.Fatalf("write %d differs from write 0 for the same rows", i)
		}
	}
}

// checkVariantRows compares a file with rows in the signal's order.
func checkVariantRows(t *testing.T, path string, want []Log) {
	t.Helper()
	want = inOrder(Logs, want)
	got := fileRows(t, path, Logs)
	if len(got) != len(want) {
		t.Fatalf("read %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if row := wantRow(Logs, &want[i]); !reflect.DeepEqual(got[i], row) {
			t.Fatalf("row %d changed: %#v", i, got[i])
		}
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
	if err := store.CommitBatch(context.Background(), &BatchMetadata{ID: "old"}, []Span{{TraceID: "trace"}}, nil, nil); err != nil {
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

// inOrder returns rows in the signal's order, ties in input order.
func inOrder[R any](s *Signal, rows []R) []R {
	perm, _ := sortRows(s, rows)
	out := make([]R, len(rows))
	for i, row := range perm {
		out[i] = rows[row]
	}
	return out
}
