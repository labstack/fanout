package telemetry

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/format"
)

// Levels-only chunks must distinguish an absent list, an empty list and a
// NULL leaf inside a present struct; scalar constants keep their exact bits.
func TestMergeConstantChunksPreserveRows(t *testing.T) {
	dir := t.TempDir()
	var inputs []string
	values := []Metric{
		{Namespace: "ns", ServiceName: "api", Name: "a", TimeUnixNanos: 1, SeriesID: 1, Histogram: Histogram{PositiveCounts: []uint64{1}}},
		{Namespace: "ns", ServiceName: "api", Name: "b", TimeUnixNanos: 2, SeriesID: 2, Exemplars: []Exemplar{{Value: 3}}},
		{Namespace: "ns", ServiceName: "api", Name: "c", TimeUnixNanos: 3, SeriesID: 3, Resource: map[string]any{"service.name": "api"}},
	}
	for i := range values {
		path := filepath.Join(dir, values[i].Name+".parquet")
		if err := writeParquet(path, Metrics, []Metric{values[i]}, parquetPageSize); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, path)
	}
	out := filepath.Join(dir, "merged.parquet")
	if err := mergeParquet(t.Context(), Metrics, inputs, out, &ParquetMergeStats{}); err != nil {
		t.Fatal(err)
	}
	var want []map[string]string
	for _, input := range inputs {
		want = append(want, engineRows(t, input)...)
	}
	if got := engineRows(t, out); !reflect.DeepEqual(got, want) {
		t.Fatalf("constant chunks changed rows: got %#v want %#v", got, want)
	}
	first, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	again := filepath.Join(dir, "again.parquet")
	if err := mergeParquet(t.Context(), Metrics, inputs, again, &ParquetMergeStats{}); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(again)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("constant encoding is not deterministic")
	}
}

func TestConstantChunkSizeDoesNotScaleWithRows(t *testing.T) {
	for _, value := range []parquet.Value{
		parquet.NullValue().Level(0, 0, 0),
		parquet.Int64Value(-123).Level(0, 1, 0),
		parquet.Int32Value(-321).Level(0, 1, 0),
		parquet.ByteArrayValue([]byte("constant")).Level(0, 1, 0),
		parquet.ByteArrayValue([]byte{}).Level(0, 1, 0),
		parquet.BooleanValue(true).Level(0, 1, 0),
		parquet.BooleanValue(false).Level(0, 1, 0),
	} {
		typ := value.Kind()
		if value.IsNull() {
			typ = parquet.Int64
		}
		node := parquet.Leaf(map[parquet.Kind]parquet.Type{
			parquet.Int64: parquet.Int64Type, parquet.Int32: parquet.Int32Type,
			parquet.ByteArray: parquet.ByteArrayType, parquet.Boolean: parquet.BooleanType,
		}[typ])
		schema := parquet.NewSchema("test", parquet.Group{"value": parquet.Optional(node)})
		c := newMergeConstant(schema, 0, value)
		small, err := c.chunk(context.Background(), 1000)
		if err != nil {
			t.Fatal(err)
		}
		large, err := c.chunk(context.Background(), 50000)
		if err != nil {
			t.Fatal(err)
		}
		if len(large.data) > len(small.data)+32 {
			t.Fatalf("constant %v grew from %d to %d bytes", value, len(small.data), len(large.data))
		}
		var encoded bytes.Buffer
		assembler, err := newMergeAssembler(&encoded, &Signal{schema: schema}, spills{})
		if err != nil {
			t.Fatal(err)
		}
		if err := assembler.append(mergeRowGroup{rows: 50_000, chunks: []mergeChunk{large}}); err != nil {
			t.Fatal(err)
		}
		if err := assembler.close(); err != nil {
			t.Fatal(err)
		}
		file, err := parquet.OpenFile(bytes.NewReader(encoded.Bytes()), int64(encoded.Len()))
		if err != nil {
			t.Fatal(err)
		}
		reader := parquet.NewColumnChunkValueReader(file.RowGroups()[0].ColumnChunks()[0])
		var values [512]parquet.Value
		rows := 0
		for {
			n, err := reader.ReadValues(values[:])
			for _, got := range values[:n] {
				if !parquet.Equal(got, value) || got.DefinitionLevel() != value.DefinitionLevel() {
					t.Fatalf("decoded %v at level %d, want %v at level %d", got, got.DefinitionLevel(), value, value.DefinitionLevel())
				}
			}
			rows += n
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
		if rows != 50_000 {
			t.Fatalf("decoded %d constant rows", rows)
		}
	}
}

// The surviving dictionary may contain one canonical blob even though later
// plain pages use different field IDs. It is not a passthrough proof.
func TestMergeMetadataRejectsPlainFallback(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.parquet")
	if err := writeParquet(input, Metrics, []Metric{{Resource: map[string]any{"key": int64(1)}}}, parquetPageSize); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "canonical.parquet")
	if err := mergeParquet(t.Context(), Metrics, []string{input}, out, &ParquetMergeStats{}); err != nil {
		t.Fatal(err)
	}
	pf := openParquetFile(t, out)
	leaf, _ := pf.Schema().Lookup("resource", "metadata")
	chunk := pf.RowGroups()[0].ColumnChunks()[leaf.ColumnIndex]
	if _, keep, err := mergeMetadataBlob(chunk); err != nil || !keep {
		t.Fatalf("canonical metadata: keep=%v err=%v", keep, err)
	}
	meta := &pf.Metadata().RowGroups[0].Columns[leaf.ColumnIndex].MetaData
	meta.EncodingStats = append(meta.EncodingStats, format.PageEncodingStats{PageType: format.DataPageV2, Encoding: format.Plain, Count: 1})
	if _, keep, err := mergeMetadataBlob(chunk); err != nil || keep {
		t.Fatalf("plain fallback accepted: keep=%v err=%v", keep, err)
	}
}
