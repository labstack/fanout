package telemetry

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/variant"
)

// Each input's writer interns names in its own order, so its metadata blobs
// differ from every other input's. A merge must not keep them: the output
// shares sorted dictionaries, is no larger per row than its inputs, decodes
// to the same rows and is identical for identical inputs.
func TestCompactionSharesVariantMetadata(t *testing.T) {
	const inputs, perInput = 48, 600
	dir := t.TempDir()
	var paths []string
	var all []Log
	var inputBytes int64
	for f := range inputs {
		rows := make([]Log, perInput)
		for r := range rows {
			i := r*inputs + f // interleave inputs in event order
			attributes := map[string]any{"messaging.system": "kafka", "http.request.method": "GET"}
			// 120 names of 40 bytes: 4.8 KB, past one dictionary's bound.
			for j := range 6 {
				attributes[fmt.Sprintf("app.attribute.%03d.%s", (i*7+j*13+f)%120, "padding-padding-pad")] = int64(j)
			}
			if i%5 == 0 {
				attributes["nested"] = map[string]any{"inner": []any{map[string]any{fmt.Sprintf("k%d", i%9): true}}}
			}
			rows[r] = Log{EventUnixNanos: int64(i + 1), Attributes: attributes,
				Resource: map[string]any{"service.name": fmt.Sprint("svc-", i%7), "service.namespace": int64(i % 3), fmt.Sprint("host.", f%4): "h"}}
		}
		path := filepath.Join(dir, fmt.Sprintf("in-%02d.parquet", f))
		if err := writeParquet(path, Logs, rows, parquetPageSize); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		inputBytes += info.Size()
		paths = append(paths, path)
		all = append(all, rows...)
	}
	merge := func(name string) []byte {
		out := filepath.Join(dir, name)
		if err := mergeParquet(context.Background(), Logs, paths, out, &ParquetMergeStats{}); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	first, second := merge("first.parquet"), merge("second.parquet")
	if !bytes.Equal(first, second) {
		t.Fatal("merging the same inputs twice gave different files")
	}
	if in, out := float64(inputBytes)/float64(len(all)), float64(len(first))/float64(len(all)); out > in {
		t.Fatalf("merged file holds %.1f bytes a row, inputs %.1f", out, in)
	}
	checkVariantRows(t, filepath.Join(dir, "first.parquet"), all)
	for _, column := range []string{"attributes", "resource"} {
		blobs := variantMetadataBlobs(t, filepath.Join(dir, "first.parquet"), column)
		if len(blobs) > 2*variantSharedDictionaries {
			t.Fatalf("%s holds %d distinct metadata blobs", column, len(blobs))
		}
		for blob := range blobs {
			metadata, err := variant.DecodeMetadata([]byte(blob))
			if err != nil {
				t.Fatal(err)
			}
			size := 0
			for _, name := range metadata.Strings {
				size += len(name)
			}
			if !metadata.Sorted || size > variantMetadataBytes {
				t.Fatalf("%s metadata sorted=%v with %d name bytes", column, metadata.Sorted, size)
			}
		}
	}
}

// variantMetadataBlobs returns the distinct metadata blobs of a VARIANT column.
func variantMetadataBlobs(t *testing.T, path, column string) map[string]bool {
	t.Helper()
	file := openParquetFile(t, path)
	leaf, ok := file.Schema().Lookup(column, "metadata")
	if !ok {
		t.Fatalf("no %s.metadata", column)
	}
	blobs := map[string]bool{}
	for _, group := range file.RowGroups() {
		reader := parquet.NewColumnChunkValueReader(group.ColumnChunks()[leaf.ColumnIndex])
		values := make([]parquet.Value, 128)
		for {
			n, err := reader.ReadValues(values)
			for _, v := range values[:n] {
				if !v.IsNull() {
					blobs[string(v.ByteArray())] = true
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return blobs
}

// Readers must accept objects whose field values are not in field order and
// whose dictionary is unsorted; the rewrite lays them out canonically.
func TestVariantRewriteRemapsUnorderedObjects(t *testing.T) {
	var names variant.MetadataBuilder
	builder := variant.NewBuilderWithMetadata(&names)
	builder.BeginObject()
	builder.Field("zeta")
	builder.BeginArray()
	builder.BeginObject()
	builder.Field("beta")
	builder.String("b")
	builder.Field("alpha")
	builder.Int64(1)
	builder.EndObject()
	builder.EndArray()
	builder.Field("alpha")
	builder.Binary([]byte{0, 1})
	builder.EndObject()
	data, err := builder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	blob := names.AppendTo(nil)
	m := &variantMerge{nameIDs: map[string]int32{}}
	m.rw.m = m
	var known, set []int32
	if err := m.rw.input(blob, &known, &set); err != nil {
		t.Fatal(err)
	}
	interned, err := m.rw.rewrite(nil, data, false)
	if err != nil {
		t.Fatal(err)
	}
	m.dicts = []variantDict{{}}
	m.join(0, set)
	m.assign()
	m.rw.meta, m.rw.pos = nil, m.dicts[0].pos
	got, err := m.rw.rewrite(nil, interned, true)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := variant.DecodeMetadata(m.dicts[0].blob)
	if err != nil || !metadata.Sorted {
		t.Fatalf("metadata %v, sorted %v", err, metadata.Sorted)
	}
	want, err := variant.DecodeMetadata(blob)
	if err != nil {
		t.Fatal(err)
	}
	before, err := variant.Decode(want, data)
	if err != nil {
		t.Fatal(err)
	}
	after, err := variant.Decode(metadata, got)
	if err != nil {
		t.Fatal(err)
	}
	if !before.Equal(after) {
		t.Fatalf("rewrite changed %v to %v", before, after)
	}
	var canonical variant.MetadataBuilder
	for _, name := range metadata.Strings {
		canonical.Add(name)
	}
	if expected := variant.Encode(&canonical, before); !bytes.Equal(got, expected) {
		t.Fatalf("rewrite %x, builder %x", got, expected)
	}
}

// Wide, unique attribute sets must not make a merged row group's variant
// leaves one huge page with an uncapped dictionary: the verifier decodes them
// within its memory limit.
func TestCompactionBoundsUniqueVariantPages(t *testing.T) {
	if testing.Short() {
		t.Skip("merges 100,000 rows of unique attribute names")
	}
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var inputs []string
	for part := range 8 {
		logs := make([]Log, 12_500)
		for i := range logs {
			n := part*len(logs) + i
			attributes := map[string]any{"messaging.system": "kafka"}
			for k := range 50 {
				attributes[fmt.Sprintf("app.unique.attribute.name.row%07d.key%02d.x", n, k)] = int64(k)
			}
			logs[i] = Log{EventUnixNanos: int64(n*8%100_000 + part), Attributes: attributes, Resource: map[string]any{"service.name": "api"}}
		}
		id := fmt.Sprint("input-", part)
		if err := store.CommitBatch(t.Context(), &BatchMetadata{ID: id}, nil, logs, nil); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, filepath.Join(store.BatchPath(id), "logs.parquet"))
	}
	dir := filepath.Join(t.TempDir(), "output"+BatchSuffix)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "logs.parquet")
	stats, err := store.MergeParquet(t.Context(), "logs", inputs, output)
	if err != nil {
		t.Fatal(err)
	}
	// No page is larger than the largest an ingest writer wrote.
	largest := make([]int64, 2)
	for i, path := range []string{inputs[0], output} {
		file := openParquetFile(t, path)
		for _, group := range file.RowGroups() {
			for _, chunk := range group.ColumnChunks() {
				pages := chunk.Pages()
				for {
					page, err := pages.ReadPage()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					size := page.Size()
					if dictionary := page.Dictionary(); dictionary != nil {
						size = max(size, dictionary.Page().Size())
					}
					parquet.Release(page)
					largest[i] = max(largest[i], size)
				}
				if err := pages.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	t.Logf("largest page: input %d bytes, merged %d", largest[0], largest[1])
	if largest[1] > largest[0] {
		t.Fatalf("merged file holds a page of %d bytes, its input %d", largest[1], largest[0])
	}
	if err := store.PrepareReplacement(dir, BatchMetadata{ID: "output", Logs: int(stats.Rows)}); err != nil {
		t.Fatal(err)
	}
	verifier, err := NewBatchVerifier(t.Context(), filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer verifier.Close()
	if err := verifier.Validate(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
}
