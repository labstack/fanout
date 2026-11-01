package telemetry

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/format"
)

var updateSchemas = flag.Bool("update-schemas", false, "rewrite testdata/format*.schema")

// The writer, compaction, the loader's format check and the verifier all take
// the physical schema from the registry. A file written from it must carry
// exactly the stored columns, with their types and encodings, and the golden
// schema pins format 5: a registry edit that changes it needs a new format.
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
				err = writeParquet(path, s, []Metric{completeTestMetric(), exponentialTestMetric()}, parquetPageSize)
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
			for _, field := range file.Schema().Fields() {
				c, ok := stored[field.Name()]
				if !ok {
					t.Errorf("file column %s is not a stored registry column", field.Name())
					continue
				}
				delete(stored, field.Name())
				if c.Type == Variant && !c.List {
					if !field.Optional() || field.Leaf() {
						t.Errorf("%s is not an optional VARIANT group", c.Name)
					}
					continue
				}
				if c.List {
					if !field.Optional() || field.Leaf() || field.Type().LogicalType() == nil || field.Type().LogicalType().Value.(*format.ListType) == nil {
						t.Errorf("%s is not an optional LIST", c.Name)
					}
					continue
				}
				if field.Optional() != c.Null || !field.Leaf() {
					t.Errorf("%s nullable = %v, registry says %v", c.Name, field.Optional(), c.Null)
				}
				want := map[Type]format.Type{String: format.ByteArray, Int32: format.Int32, Int64: format.Int64, Uint32: format.Int32,
					Uint64: format.Int64, Float64: format.Double, Bool: format.Boolean, Time: format.Int64}[c.Type]
				if got := field.Type().Kind(); got != parquet.Kind(want) {
					t.Errorf("%s physical type = %v, want %v", c.Name, got, want)
				}
			}
			for name := range stored {
				t.Errorf("registry column %s is missing from the file", name)
			}

			var sorting []string
			for _, column := range file.Metadata().RowGroups[0].SortingColumns {
				sorting = append(sorting, file.Schema().Columns()[column.ColumnIdx][0])
			}
			if fmt.Sprint(sorting) != fmt.Sprint(s.Order.claim()) {
				t.Errorf("footer sort = %v, registry claims %v", sorting, s.Order.claim())
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

// A complete canonical row of each signal reads back through the native
// engine as the values written out here by hand, column by column: required
// and nullable scalars, dedicated columns with one spilled key, lists and
// lists of VARIANT.
func TestRegistryColumnsRoundTrip(t *testing.T) {
	span, log, metric, exponential := completeTestSpan(), completeTestLog(), completeTestMetric(), exponentialTestMetric()
	nulls := func(want map[string]string, s *Signal) map[string]string {
		for _, c := range s.Columns {
			if _, ok := want[c.Name]; !ok && c.Key != nil {
				want[c.Name] = "NULL"
			}
		}
		return want
	}
	for _, test := range []struct {
		signal *Signal
		write  func(string) error
		want   []map[string]string
	}{
		{Spans, func(path string) error { return writeParquet(path, Spans, []Span{span}, parquetPageSize) }, []map[string]string{nulls(map[string]string{
			"namespace": "tenant", "trace_id": "0123456789abcdef0123456789abcdef", "_trace_hash": "77156422110593109",
			"span_id": "0123456789abcdef", "parent_span_id": "fedcba9876543210", "service": "checkout", "operation": "POST /orders",
			"kind": "SERVER", "start_time": "1970-01-01 00:00:00.00000001+00", "end_time": "1970-01-01 00:00:00.00000002+00",
			"start_unix_nano": "10", "end_unix_nano": "20", "duration_ms": "1e-05", "status": "STATUS_CODE_ERROR",
			"status_message":   "declined",
			"resource":         "{'host': one, 'k8s.container.restart_count': two, 'service.version': 4.5.6}",
			"attributes":       "{'http.request.method': POST, 'http.response.status_code': 500, 'server.port': 443}",
			"event_times":      "['1970-01-01 00:00:00.000000015+00', '1970-01-01 00:00:00.000000016+00']",
			"event_names":      "[exception, retry]",
			"event_attributes": "[{'attempt': 2, 'exception.type': CardDeclined}, NULL]",
			"link_trace_ids":   "[linked]", "link_span_ids": "[parent]", "link_trace_states": "['a=b']",
			"link_attributes": "[{'sampled': true}]",
			"trace_state":     "vendor=value", "flags": "1", "scope_name": "scope", "scope_version": "1.2.3",
			"ingested_at": "1970-01-01 00:00:00.00000003+00", "ingested_unix_nano": "30",
			"rpc_service": "orders.v1.Orders", "peer_service": "payments",
			"exception_type": "CardDeclined", "exception_message": "declined",
			"_a_http_request_method": "POST", "_a_http_response_status_code": "500", "_r_service_version": "4.5.6",
			// server.port and k8s.container.restart_count are integers sent as text.
			"_spill": "16842752",
		}, Spans)}},
		{Logs, func(path string) error { return writeParquet(path, Logs, []Log{log}, parquetPageSize) }, []map[string]string{nulls(map[string]string{
			"namespace": "tenant", "log_time": "1970-01-01 00:00:00.000000011+00", "observed_time": "1970-01-01 00:00:00.000000013+00",
			"time_unix_nano": "12", "observed_time_unix_nano": "13", "severity": "ERROR", "severity_number": "17",
			"body": "declined", "service": "checkout", "trace_id": "trace", "span_id": "span", "flags": "1",
			"resource": "{'host': one, 'k8s.pod.name': checkout-1}", "attributes": "{'attempt': 2}",
			"scope_name": "scope", "scope_version": "1.2.3", "ingested_at": "1970-01-01 00:00:00.000000014+00",
			"ingested_unix_nano": "14", "body_template": "declined: {reason}", "_r_k8s_pod_name": "checkout-1", "_spill": "0",
		}, Logs)}},
		{Metrics, func(path string) error {
			return writeParquet(path, Metrics, []Metric{metric, exponential}, parquetPageSize)
		}, []map[string]string{nulls(map[string]string{
			"series_id": "9223372036854775808", "namespace": "tenant", "metric_time": "1970-01-01 00:00:00.000000021+00",
			"time_unix_nano": "22", "name": "request.duration", "description": "request latency", "unit": "ms",
			"type": "histogram", "temporality": "cumulative", "start_time_unix_nano": "20", "service": "checkout",
			"flags": "1", "value": "NULL", "hist_count": "14", "hist_sum": "47.0", "hist_min": "0.5", "hist_max": "NULL",
			"hist_bounds": "[1.0, 5.0, 10.0]", "hist_counts": "[2, 3, 4, 5]",
			"exp_scale": "NULL", "exp_zero_count": "NULL", "exp_zero_threshold": "NULL", "exp_positive_offset": "NULL",
			"exp_positive_counts": "NULL", "exp_negative_offset": "NULL", "exp_negative_counts": "NULL",
			"quantiles": "NULL", "quantile_values": "NULL",
			"exemplar_times": "['1970-01-01 00:00:00.000000021+00']", "exemplar_values": "[3.0]",
			"exemplar_trace_ids": "[trace]", "exemplar_span_ids": "[span]", "exemplar_attributes": "[{'user': 7}]",
			"attributes": "{'route': /pay}", "resource": "{'host': one}", "scope_name": "scope", "scope_version": "1.2.3",
			"ingested_at": "1970-01-01 00:00:00.000000024+00", "ingested_unix_nano": "24", "_spill": "0",
		}, Metrics), nulls(map[string]string{
			"series_id": "9223372036854775809", "namespace": "tenant", "metric_time": "1970-01-01 00:00:00.000000031+00",
			"time_unix_nano": "31", "name": "request.duration", "description": "", "unit": "ms",
			"type": "histogram", "temporality": "delta", "start_time_unix_nano": "0", "service": "checkout",
			"flags": "0", "value": "NULL", "hist_count": "6", "hist_sum": "NULL", "hist_min": "NULL", "hist_max": "NULL",
			"hist_bounds": "NULL", "hist_counts": "NULL",
			"exp_scale": "-2", "exp_zero_count": "1", "exp_zero_threshold": "0.0", "exp_positive_offset": "3",
			"exp_positive_counts": "[2, 3]", "exp_negative_offset": "-1", "exp_negative_counts": "[]",
			"quantiles": "NULL", "quantile_values": "NULL",
			"exemplar_times": "[]", "exemplar_values": "[]", "exemplar_trace_ids": "[]", "exemplar_span_ids": "[]",
			"exemplar_attributes": "[]", "attributes": "NULL", "resource": "NULL", "scope_name": "", "scope_version": "",
			"ingested_at": "1970-01-01 00:00:00+00", "ingested_unix_nano": "0", "_spill": "0",
		}, Metrics)}},
	} {
		t.Run(test.signal.Name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.signal.Name+".parquet")
			if err := test.write(path); err != nil {
				t.Fatal(err)
			}
			got := engineRows(t, path)
			if len(got) != len(test.want) {
				t.Fatalf("read %d rows, want %d", len(got), len(test.want))
			}
			for i, want := range test.want {
				for name, value := range got[i] {
					if want[name] != value {
						t.Errorf("row %d %s = %s, want %s", i, name, value, want[name])
					}
				}
				for name := range want {
					if _, ok := got[i][name]; !ok {
						t.Errorf("row %d has no column %s", i, name)
					}
				}
			}
		})
	}
}

// engineRows reads a file through the native engine, every column rendered
// as text in UTC.
func engineRows(t *testing.T, path string) []map[string]string {
	t.Helper()
	return engineRowsQuery(t, "SELECT COLUMNS(*)::VARCHAR FROM read_parquet("+sqlString(path)+")")
}

// engineRowsQuery runs a query through the native engine with every column
// read as text.
func engineRowsQuery(t *testing.T, query string) []map[string]string {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("SET TimeZone='UTC'"); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]string
	for rows.Next() {
		values := make([]sql.NullString, len(names))
		dest := make([]any, len(names))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		row := map[string]string{}
		for i, name := range names {
			row[name] = "NULL"
			if values[i].Valid {
				row[name] = values[i].String
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Each column's encoding, written out by hand: dictionaries for repeating
// values, delta encoding for instants and nanosecond counts, plain for the
// rest. Every VARIANT metadata leaf is dictionary-encoded.
func TestColumnEncodingsArePinned(t *testing.T) {
	dict, delta := map[string][]string{
		"spans": {"kind", "status", "event_names", "link_trace_states", "trace_state", "scope_name", "scope_version",
			"rpc_service", "peer_service", "exception_type", "_spill"},
		"logs":    {"namespace", "severity", "service", "scope_name", "scope_version", "body_template", "_spill"},
		"metrics": {"namespace", "name", "description", "unit", "type", "temporality", "service", "hist_bounds", "quantiles", "scope_name", "scope_version", "_spill"},
	}, map[string][]string{
		"spans":   {"start_time", "end_time", "start_unix_nano", "end_unix_nano", "ingested_at", "ingested_unix_nano"},
		"logs":    {"log_time", "observed_time", "time_unix_nano", "observed_time_unix_nano", "ingested_at", "ingested_unix_nano"},
		"metrics": {"metric_time", "time_unix_nano", "start_time_unix_nano", "ingested_at", "ingested_unix_nano"},
	}
	files := map[*Signal]func(string) error{
		Spans: func(path string) error { return writeParquet(path, Spans, []Span{completeTestSpan()}, parquetPageSize) },
		Logs:  func(path string) error { return writeParquet(path, Logs, []Log{completeTestLog()}, parquetPageSize) },
		Metrics: func(path string) error {
			return writeParquet(path, Metrics, []Metric{completeTestMetric(), exponentialTestMetric()}, parquetPageSize)
		},
	}
	for _, s := range Signals {
		t.Run(s.Name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), s.Name+".parquet")
			if err := files[s](path); err != nil {
				t.Fatal(err)
			}
			for _, chunk := range openParquetFile(t, path).Metadata().RowGroups[0].Columns {
				leaf := chunk.MetaData.PathInSchema
				name := leaf[0]
				want := "plain"
				switch {
				case leaf[len(leaf)-1] == "metadata" || slices.Contains(dict[s.Name], name) || strings.HasPrefix(name, "_a_") || strings.HasPrefix(name, "_r_"):
					want = "dict"
				case slices.Contains(delta[s.Name], name):
					want = "delta"
				}
				got := "plain"
				for _, encoding := range chunk.MetaData.Encoding {
					switch encoding {
					case format.RLEDictionary:
						got = "dict"
					case format.DeltaBinaryPacked:
						got = "delta"
					}
				}
				if got != want {
					t.Errorf("%s is %s encoded, want %s", strings.Join(leaf, "."), got, want)
				}
			}
		})
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
			if c.Stored() == (c.get == nil) != (c.Key != nil || c.Name == "_spill") {
				t.Errorf("%s.%s: stored columns need an accessor, derived and dedicated ones none", s.Name, c.Name)
			}
			if c.Private && c.View.Name != "" {
				t.Errorf("%s.%s is private but in the view", s.Name, c.Name)
			}
			if (c.Type == Variant && !c.List) != (len(c.Shred) > 0) {
				t.Errorf("%s.%s: only VARIANT columns shred keys", s.Name, c.Name)
			}
			if c.Parallel != "" && !c.List {
				t.Errorf("%s.%s: only lists are parallel", s.Name, c.Name)
			}
		}
		for _, part := range s.Order {
			if c, ok := s.Column(part.Column); !ok || !c.Stored() {
				t.Errorf("%s order column %s is not stored", s.Name, part.Column)
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
		TraceID: "trace", SpanID: "span", Flags: 1, Resource: map[string]any{"host": "one", "k8s.pod.name": "checkout-1"},
		Attributes: map[string]any{"attempt": int64(2)}, ScopeName: "scope", ScopeVersion: "1.2.3",
		IngestedAt: 14, BodyTemplate: "declined: {reason}",
	}
}

func completeTestMetric() Metric {
	return Metric{
		SeriesID: 1 << 63, Namespace: "tenant", EventUnixNanos: 21, TimeUnixNanos: 22, Name: "request.duration",
		Description: "request latency", Unit: "ms", Type: "histogram", Temporality: "cumulative",
		StartTimeUnixNanos: 20, ServiceName: "checkout", Flags: 1,
		Histogram:  Histogram{Count: 14, Sum: 47, HasSum: true, Min: 0.5, HasMin: true, Bounds: []float64{1, 5, 10}, Counts: []uint64{2, 3, 4, 5}},
		Exemplars:  []Exemplar{{TimeUnixNanos: 21, Value: 3, TraceID: "trace", SpanID: "span", Attributes: map[string]any{"user": int64(7)}}},
		Attributes: map[string]any{"route": "/pay"}, Resource: map[string]any{"host": "one"},
		ScopeName: "scope", ScopeVersion: "1.2.3", IngestedAt: 24,
	}
}

func exponentialTestMetric() Metric {
	return Metric{
		SeriesID: 1<<63 + 1, Namespace: "tenant", TimeUnixNanos: 31, Name: "request.duration", Unit: "ms",
		Type: "histogram", Temporality: "delta", ServiceName: "checkout",
		Histogram: Histogram{Count: 6, Exponential: true, Scale: -2, ZeroCount: 1, PositiveOffset: 3, PositiveCounts: []uint64{2, 3},
			NegativeOffset: -1, NegativeCounts: []uint64{}},
		Exemplars: []Exemplar{},
	}
}

// wantRow is the stored row the registry's accessors produce for one
// canonical row's required scalar and VARIANT columns, keyed by column name.
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
		case func(*R) uint32:
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

// fileRows reads a signal file's required scalar and VARIANT columns back,
// keyed by column name as wantRow is.
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
				c, _ := s.Column(path[0])
				if len(path) != 1 || c.Null || c.Key != nil || c.Name == "_spill" {
					continue
				}
				switch c.Type {
				case String:
					values[c.Name] = string(value.ByteArray())
				case Int64, Time:
					values[c.Name] = value.Int64()
				case Uint64:
					values[c.Name] = value.Uint64()
				case Uint32:
					values[c.Name] = value.Uint32()
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
