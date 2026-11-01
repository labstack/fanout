package telemetry

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/format"
)

var updateSchemas = flag.Bool("update-schemas", false, "rewrite testdata/format*.schema")

// The writer, compaction, the loader's format check and the verifier all take
// the physical schema from the registry. A file written from it must carry
// exactly the stored columns, with their types and encodings, and the golden
// schema pins format 4: a registry edit that changes it needs a new format.
func TestRegistryMatchesWrittenSchema(t *testing.T) {
	for _, s := range Signals {
		t.Run(s.Name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), s.Name+".parquet")
			var err error
			switch s {
			case Spans:
				err = writeParquet(path, s, []Span{completeTestSpan()}, parquetPageSize)
			case Logs:
				err = writeParquet(path, s, []Log{completeTestLog()}, parquetPageSize)
			case Metrics:
				err = writeParquet(path, s, []Metric{completeTestMetric()}, parquetPageSize)
			}
			if err != nil {
				t.Fatal(err)
			}
			file := openParquetFile(t, path)
			if !parquet.EqualNodes(file.Schema(), s.schema) {
				t.Fatalf("written schema differs from the registry\n got: %s\nwant: %s", file.Schema(), s.schema)
			}

			stored := map[string]Column{}
			for _, c := range s.Columns {
				if c.Stored() {
					stored[c.Name] = c
				}
			}
			chunks := map[string]format.ColumnMetaData{}
			for _, chunk := range file.Metadata().RowGroups[0].Columns {
				chunks[strings.Join(chunk.MetaData.PathInSchema, ".")] = chunk.MetaData
			}
			for _, field := range file.Schema().Fields() {
				c, ok := stored[field.Name()]
				if !ok {
					t.Errorf("file column %s is not a stored registry column", field.Name())
					continue
				}
				delete(stored, field.Name())
				if c.Type == Variant {
					if !field.Optional() || field.Leaf() {
						t.Errorf("%s is not an optional VARIANT group", c.Name)
					}
					continue
				}
				if field.Optional() || !field.Leaf() {
					t.Errorf("%s is not a required leaf", c.Name)
				}
				want := map[Type]format.Type{String: format.ByteArray, Int64: format.Int64, Uint64: format.Int64,
					Float64: format.Double, Bool: format.Boolean, Time: format.Int64}[c.Type]
				if got := field.Type().Kind(); got != parquet.Kind(want) {
					t.Errorf("%s physical type = %v, want %v", c.Name, got, want)
				}
				dict := false
				for _, encoding := range chunks[c.Name].Encoding {
					dict = dict || encoding == format.RLEDictionary
				}
				if dict != c.Dict {
					t.Errorf("%s dictionary encoded = %v, registry says %v", c.Name, dict, c.Dict)
				}
			}
			for name := range stored {
				t.Errorf("registry column %s is missing from the file", name)
			}

			var sorting []string
			for _, column := range file.Metadata().RowGroups[0].SortingColumns {
				sorting = append(sorting, file.Schema().Columns()[column.ColumnIdx][0])
			}
			if fmt.Sprint(sorting) != fmt.Sprint(s.Sort) {
				t.Errorf("footer sort = %v, registry says %v", sorting, s.Sort)
			}
			if _, ok := s.Column(s.Time); !ok {
				t.Errorf("time column %s is not a registry column", s.Time)
			}

			golden := filepath.Join("testdata", fmt.Sprintf("format%d-%s.schema", batchMetadataVersion, s.Name))
			if *updateSchemas {
				if err := os.WriteFile(golden, []byte(s.schema.String()+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got := s.schema.String() + "\n"; got != string(want) {
				t.Errorf("format %d %s schema changed; a new physical schema needs a new batch format\n got: %s\nwant: %s", batchMetadataVersion, s.Name, got, want)
			}
		})
	}
}

// Each stored column's value comes from its accessor, so every column of a
// complete canonical row reads back as written.
func TestRegistryColumnsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	span, log, metric := completeTestSpan(), completeTestLog(), completeTestMetric()
	for _, test := range []struct {
		signal *Signal
		write  func(string) error
		want   map[string]any
	}{
		{Spans, func(path string) error { return writeParquet(path, Spans, []Span{span}, parquetPageSize) }, wantRow(Spans, &span)},
		{Logs, func(path string) error { return writeParquet(path, Logs, []Log{log}, parquetPageSize) }, wantRow(Logs, &log)},
		{Metrics, func(path string) error { return writeParquet(path, Metrics, []Metric{metric}, parquetPageSize) }, wantRow(Metrics, &metric)},
	} {
		path := filepath.Join(dir, test.signal.Name+".parquet")
		if err := test.write(path); err != nil {
			t.Fatal(err)
		}
		got := fileRows(t, path, test.signal)
		if len(got) != 1 || !reflect.DeepEqual(got[0], test.want) {
			t.Errorf("%s row mismatch\n got: %#v\nwant: %#v", test.signal.Name, got, test.want)
		}
	}
}

// Derived columns are not stored, and the clean view hides only what it must.
func TestRegistryColumnsAreConsistent(t *testing.T) {
	for _, s := range Signals {
		names, views := map[string]bool{}, map[string]bool{}
		for _, c := range s.Columns {
			if names[c.Name] {
				t.Errorf("%s.%s is declared twice", s.Name, c.Name)
			}
			names[c.Name] = true
			if c.View.Name != "" {
				if views[c.View.Name] {
					t.Errorf("%s view column %s is declared twice", s.Name, c.View.Name)
				}
				views[c.View.Name] = true
			}
			if c.Stored() == (c.get == nil) {
				t.Errorf("%s.%s: stored columns need an accessor, derived ones none", s.Name, c.Name)
			}
			if c.Private && c.View.Name != "" {
				t.Errorf("%s.%s is private but in the view", s.Name, c.Name)
			}
			if (c.Type == Variant) != (len(c.Shred) > 0) {
				t.Errorf("%s.%s: only VARIANT columns shred keys", s.Name, c.Name)
			}
		}
		for _, name := range s.Sort {
			if c, ok := s.Column(name); !ok || !c.Stored() {
				t.Errorf("%s sort column %s is not stored", s.Name, name)
			}
		}
	}
}

// Narrow readers project registry columns by name; a tag that names no
// stored column, or a Go type the column does not hold, fails here.
func TestReadProjectionsMatchRegistry(t *testing.T) {
	kinds := map[Type]reflect.Kind{String: reflect.String, Int64: reflect.Int64, Uint64: reflect.Uint64, Float64: reflect.Float64, Bool: reflect.Bool, Time: reflect.Int64}
	for _, row := range []reflect.Type{reflect.TypeFor[indexedSpanRow](), reflect.TypeFor[verificationSpanRow]()} {
		for field := range row.Fields() {
			name, _, _ := strings.Cut(field.Tag.Get("parquet"), ",")
			c, ok := Spans.Column(name)
			if !ok || !c.Stored() {
				t.Errorf("%s.%s reads %q, which span files do not store", row.Name(), field.Name, name)
			} else if kinds[c.Type] != field.Type.Kind() {
				t.Errorf("%s.%s is %s; column %s holds %v", row.Name(), field.Name, field.Type, name, kinds[c.Type])
			}
		}
	}
}

func completeTestLog() Log {
	return Log{
		Namespace: "tenant", EventUnixNanos: 11, TimeUnixNanos: 12, ObservedTimeNanos: 13,
		Severity: "ERROR", SeverityNumber: 17, Body: "declined", ServiceName: "checkout",
		TraceID: "trace", SpanID: "span", Flags: 1, Resource: map[string]any{"host": "one"},
		Attributes: map[string]any{"attempt": int64(2)}, ScopeName: "scope", ScopeVersion: "1.2.3",
		IngestedAt: 14, BodyTemplate: "declined: {reason}",
	}
}

func completeTestMetric() Metric {
	return Metric{
		SeriesID: 1 << 63, Namespace: "tenant", EventUnixNanos: 21, TimeUnixNanos: 22, Name: "request.duration",
		Description: "request latency", Unit: "ms", Type: "sum", Temporality: "cumulative", Monotonic: true,
		StartTimeUnixNanos: 20, ServiceName: "checkout", Value: 23.5,
		HistBoundsJSON: `[1,5,10]`, HistCountsJSON: `[2,3,4,5]`, HistCount: 14, HistSum: 47,
		ExemplarsJSON: `[{"trace_id":"trace"}]`, Attributes: map[string]any{"route": "/pay"},
		Resource: map[string]any{"host": "one"}, ScopeName: "scope", ScopeVersion: "1.2.3", IngestedAt: 24,
	}
}

// wantRow is the stored row the registry's accessors produce for one
// canonical row, keyed by column name.
func wantRow[R any](s *Signal, row *R) map[string]any {
	out := map[string]any{}
	for _, c := range s.Columns {
		switch get := c.get.(type) {
		case func(*R) string:
			out[c.Name] = get(row)
		case func(*R) int64:
			out[c.Name] = get(row)
		case func(*R) uint64:
			out[c.Name] = get(row)
		case func(*R) float64:
			out[c.Name] = get(row)
		case func(*R) bool:
			out[c.Name] = get(row)
		case func(*R) map[string]any:
			if value := get(row); value != nil {
				out[c.Name] = value
			} else {
				out[c.Name] = nil
			}
		}
	}
	return out
}

// fileRows reads a signal file back, keyed by column name as wantRow is.
func fileRows(t *testing.T, path string, s *Signal) []map[string]any {
	t.Helper()
	file := openParquetFile(t, path)
	var out []map[string]any
	reader := parquet.NewReader(file)
	defer reader.Close()
	columns := file.Schema().Columns()
	buffer := make([]parquet.Row, 256)
	for {
		n, err := reader.ReadRows(buffer)
		for _, row := range buffer[:n] {
			values := map[string]any{}
			for _, value := range row {
				path := columns[value.Column()]
				if len(path) != 1 {
					continue
				}
				c, _ := s.Column(path[0])
				switch c.Type {
				case String:
					values[c.Name] = string(value.ByteArray())
				case Int64, Time:
					values[c.Name] = value.Int64()
				case Uint64:
					values[c.Name] = value.Uint64()
				case Float64:
					values[c.Name] = value.Double()
				case Bool:
					values[c.Name] = value.Boolean()
				}
			}
			out = append(out, values)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	type variants struct {
		Attributes any `parquet:"attributes,variant"`
		Resource   any `parquet:"resource,variant"`
	}
	objects := parquet.NewGenericReader[variants](file)
	defer objects.Close()
	for offset := 0; offset < len(out); {
		batch := make([]variants, min(256, len(out)-offset))
		n, err := objects.Read(batch)
		for _, row := range batch[:n] {
			out[offset]["attributes"], out[offset]["resource"] = row.Attributes, row.Resource
			offset++
		}
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
		if n == 0 {
			t.Fatal("variant reader made no progress")
		}
	}
	return out
}

func openParquetFile(t *testing.T, path string) *parquet.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	file, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	return file
}
