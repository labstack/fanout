package telemetry

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/variant"
)

// Each input's writer interns names in its own order and grows one dictionary
// across its rows, so its metadata blobs differ from every other input's and
// hold names their rows do not use. A merge must not keep them: the output
// gives each row the sorted dictionary of exactly its names, deduplicated, is
// no larger per row than its inputs, decodes to the same rows and is
// identical for identical inputs. Merging compacted files keeps those
// dictionaries.
func TestCompactionGivesRowsTheirOwnMetadata(t *testing.T) {
	dir := t.TempDir()
	// 120 names of 40 bytes: 4.8 KB, past one dictionary's bound, in 120
	// sets of 6 that no row group's rows exhaust.
	paths, all, inputBytes := writeVariantInputs(t, dir, 48, 600, func(i, f int) map[string]any {
		attributes := map[string]any{"messaging.system": "kafka", "http.request.method": "GET"}
		for j := range 6 {
			attributes[fmt.Sprintf("app.attribute.%03d.%s", (i*7+j*13+f)%120, "padding-padding-pad")] = int64(j)
		}
		if i%5 == 0 {
			attributes["nested"] = map[string]any{"inner": []any{map[string]any{fmt.Sprintf("k%d", i%9): true}}}
		}
		return attributes
	})
	first, second := mergeFile(t, dir, "first.parquet", paths), mergeFile(t, dir, "second.parquet", paths)
	if !bytes.Equal(first, second) {
		t.Fatal("merging the same inputs twice gave different files")
	}
	if in, out := float64(inputBytes)/float64(len(all)), float64(len(first))/float64(len(all)); out > in {
		t.Fatalf("merged file holds %.1f bytes a row, inputs %.1f", out, in)
	}
	checkVariantRows(t, filepath.Join(dir, "first.parquet"), all)
	checkExactMetadata(t, filepath.Join(dir, "first.parquet"), all)
	// Compacted halves carry exact dictionaries, which the merge keeps; the
	// output is the rewrite's.
	halves := []string{filepath.Join(dir, "a.parquet"), filepath.Join(dir, "b.parquet")}
	mergeFile(t, dir, "a.parquet", paths[:24])
	mergeFile(t, dir, "b.parquet", paths[24:])
	kept := mergeFile(t, dir, "kept.parquet", halves)
	variantKeepSorted = false
	rewritten := mergeFile(t, dir, "rewritten.parquet", halves)
	variantKeepSorted = true
	if !bytes.Equal(kept, rewritten) {
		t.Fatal("merging compacted files wrote a different file from the rewrite")
	}
	checkExactMetadata(t, filepath.Join(dir, "kept.parquet"), all)
}

// Rows whose name sets would outgrow the Parquet dictionary share a few
// sorted dictionaries within the ingest writer's bound instead.
func TestCompactionSharesVariantMetadata(t *testing.T) {
	dir := t.TempDir()
	// Six of 120 padded names, in a set that varies with the row: thousands
	// of sets of 300 bytes, more than the Parquet dictionary holds.
	paths, all, inputBytes := writeVariantInputs(t, dir, 48, 600, func(i, f int) map[string]any {
		attributes := map[string]any{"messaging.system": "kafka", "http.request.method": "GET"}
		for j := range 6 {
			attributes[fmt.Sprintf("app.attribute.%03d.%s", (i*7+i/120*(j+1)*31+j*13)%120, "padding-padding-pad")] = int64(j)
		}
		return attributes
	})
	sets, total := map[string]bool{}, 0
	for _, row := range all {
		names := slices.Sorted(maps.Keys(row.Attributes))
		if key := fmt.Sprint(names); !sets[key] {
			sets[key] = true
			total += variantMetadataSize(len(names), len(strings.Join(names, "")))
		}
	}
	if total <= parquetDictionaryMaxBytes {
		t.Fatalf("rows use %d name sets of %d bytes; the test needs more than %d", len(sets), total, parquetDictionaryMaxBytes)
	}
	first := mergeFile(t, dir, "first.parquet", paths)
	if in, out := float64(inputBytes)/float64(len(all)), float64(len(first))/float64(len(all)); out > in {
		t.Fatalf("merged file holds %.1f bytes a row, inputs %.1f", out, in)
	}
	checkVariantRows(t, filepath.Join(dir, "first.parquet"), all)
	blobs := variantMetadataBlobs(t, filepath.Join(dir, "first.parquet"), "attributes")
	if len(blobs) > 2*variantSharedDictionaries {
		t.Fatalf("attributes holds %d distinct metadata blobs", len(blobs))
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
			t.Fatalf("attributes metadata sorted=%v with %d name bytes", metadata.Sorted, size)
		}
	}
}

// writeVariantInputs writes inputs files of perInput logs each, interleaved
// in event order, with the attributes of row i of file f.
func writeVariantInputs(t *testing.T, dir string, inputs, perInput int, attributes func(i, f int) map[string]any) (paths []string, all []Log, size int64) {
	t.Helper()
	for f := range inputs {
		rows := make([]Log, perInput)
		for r := range rows {
			i := r*inputs + f
			rows[r] = Log{EventUnixNanos: int64(i + 1), Attributes: attributes(i, f),
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
		size += info.Size()
		paths = append(paths, path)
		all = append(all, rows...)
	}
	return paths, all, size
}

func mergeFile(t *testing.T, dir, name string, inputs []string) []byte {
	t.Helper()
	out := filepath.Join(dir, name)
	if err := mergeParquet(context.Background(), Logs, inputs, out, &ParquetMergeStats{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// checkExactMetadata checks that every row's attributes and resource
// metadata is the sorted dictionary of exactly the names its value uses.
func checkExactMetadata(t *testing.T, path string, rows []Log) {
	t.Helper()
	rows = inOrder(Logs, rows)
	for column, get := range map[string]func(*Log) map[string]any{"attributes": func(l *Log) map[string]any { return l.Attributes }, "resource": func(l *Log) map[string]any { return l.Resource }} {
		got := variantRowMetadata(t, path, column)
		if len(got) != len(rows) {
			t.Fatalf("%s: read %d rows, want %d", column, len(got), len(rows))
		}
		for i := range rows {
			names := map[string]bool{}
			variantNames(get(&rows[i]), names)
			if want := slices.Sorted(maps.Keys(names)); !got[i].Sorted || !slices.Equal(got[i].Strings, want) {
				t.Fatalf("%s row %d metadata %v (sorted %v), want %v", column, i, got[i].Strings, got[i].Sorted, want)
			}
		}
	}
}

// variantNames collects the object field names of a value.
func variantNames(value any, names map[string]bool) {
	switch v := value.(type) {
	case map[string]any:
		for name, child := range v {
			names[name] = true
			variantNames(child, names)
		}
	case []any:
		for _, child := range v {
			variantNames(child, names)
		}
	}
}

// variantRowMetadata returns each row's decoded metadata of a VARIANT column.
func variantRowMetadata(t *testing.T, path, column string) []variant.Metadata {
	t.Helper()
	file := openParquetFile(t, path)
	leaf, ok := file.Schema().Lookup(column, "metadata")
	if !ok {
		t.Fatalf("no %s.metadata", column)
	}
	decoded := map[string]variant.Metadata{}
	var out []variant.Metadata
	for _, group := range file.RowGroups() {
		reader := parquet.NewColumnChunkValueReader(group.ColumnChunks()[leaf.ColumnIndex])
		values := make([]parquet.Value, 128)
		for {
			n, err := reader.ReadValues(values)
			for _, v := range values[:n] {
				blob := string(v.ByteArray())
				metadata, ok := decoded[blob]
				if !ok && !v.IsNull() {
					if metadata, err = variant.DecodeMetadata([]byte(blob)); err != nil {
						t.Fatal(err)
					}
					decoded[blob] = metadata
				}
				out = append(out, metadata)
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
	return out
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

// An ingest file of one row carries the sorted dictionary of exactly its
// names, laid out by the variant builder. Merging such files keeps the rows
// as read, which must be the file the rewrite writes.
func TestCompactionKeepsExactIngestRowsAsRewritten(t *testing.T) {
	dir := t.TempDir()
	paths, all, _ := writeVariantInputs(t, dir, 40, 1, func(i, f int) map[string]any {
		// Nested names sort between their parents' siblings, so a row's
		// interned names stay sorted while its objects nest.
		attributes := map[string]any{"a": map[string]any{"b": int64(i)}, "c": []any{map[string]any{"d": true}}, "http.request.method": "GET"}
		if i%3 == 0 {
			attributes[fmt.Sprintf("e%d", i%4)] = map[string]any{"f": "x"}
		}
		return attributes
	})
	for i, path := range paths {
		checkExactMetadata(t, path, all[i:i+1])
	}
	kept := mergeFile(t, dir, "kept.parquet", paths)
	variantKeepSorted = false
	rewritten := mergeFile(t, dir, "rewritten.parquet", paths)
	variantKeepSorted = true
	if !bytes.Equal(kept, rewritten) {
		t.Fatal("merging exact ingest rows wrote a different file from the rewrite")
	}
	checkVariantRows(t, filepath.Join(dir, "kept.parquet"), all)
	checkExactMetadata(t, filepath.Join(dir, "kept.parquet"), all)
}

// The fast path keeps only blobs the rewrite would write: canonical
// encodings of distinct sorted names.
func TestVariantKeepsOnlyCanonicalBlobs(t *testing.T) {
	encode := func(names ...string) []byte {
		return appendVariantMetadata(nil, len(names), func(i int) string { return names[i] })
	}
	reserved := encode("a", "b")
	reserved[0] |= 1 << 5
	for _, c := range []struct {
		blob []byte
		fits bool
	}{
		{encode("a", "b"), true},
		{encode(), true},
		{encode("a", "a"), false},
		{encode("b", "a"), false},
		{reserved, false},
	} {
		m := &variantMerge{blobs: [][]byte{c.blob}}
		if got := m.blobFits(); got != c.fits {
			t.Errorf("blobFits(%x) = %v, want %v", c.blob, got, c.fits)
		}
	}
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
	m.rw.meta, m.rw.own = nil, m.dicts[0].names
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
