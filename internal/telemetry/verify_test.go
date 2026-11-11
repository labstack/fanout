package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	driver "github.com/duckdb/duckdb-go/v2"
	"github.com/labstack/pitot/internal/telemetry/semconv"
	"github.com/parquet-go/parquet-go"
)

func verificationFixture(t *testing.T) (string, []Span) {
	t.Helper()
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	spans := []Span{
		{TraceID: "trace", SpanID: "a", StartUnixNanos: 10, Resource: map[string]any{"service.name": "api", "extra": []any{int64(1), true}}, Attributes: map[string]any{"messaging.system": "kafka", "other": []byte{0, 255}, "server.port": "80"}},
		{TraceID: "trace", SpanID: "b", StartUnixNanos: 20},
		{TraceID: "trace", SpanID: "c", StartUnixNanos: 30},
		{TraceID: "trace", SpanID: "d", StartUnixNanos: 40, Attributes: map[string]any{"other": []byte{0, 255}, "server.port": "81"}, Resource: map[string]any{"service.name": "last", "extra": []any{int64(2), false}}},
	}
	if err := store.CommitBatch(context.Background(), &BatchMetadata{ID: "verify"}, spans, nil, nil); err != nil {
		t.Fatal(err)
	}
	dir := store.BatchPath("verify")
	perm, _ := sortRows(Spans, spans)
	rows := make([]Span, len(perm))
	for i, row := range perm {
		rows[i] = spans[row]
	}
	// Multiple tiny row groups make a late-page failure observable cheaply.
	path := filepath.Join(dir, "spans.parquet")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := parquet.NewGenericWriter[any](f, parquetWriterOptions(1024, Spans.writerOptions()...)...)
	var spilled spills
	for i := 0; i < len(rows); i += 2 {
		if err := writeColumns(writer, Spans, rows[i:i+2], []int32{0, 1}, &spilled); err != nil {
			t.Fatal(err)
		}
		if err := writer.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	spilled.footer(writer, Spans)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePublishedBatch(dir); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePublishedBatch(dir); err != nil {
		t.Fatal(err)
	}
	return dir, rows
}

func TestNativeVerifierDecodesEveryVariantColumnAndRowGroup(t *testing.T) {
	for _, column := range []string{"resource.metadata", "resource.value", "resource.typed_value.service.name.typed_value", "attributes.value"} {
		t.Run(column, func(t *testing.T) {
			dir, _ := verificationFixture(t)
			path := filepath.Join(dir, "spans.parquet")
			f, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			info, _ := f.Stat()
			data, err := parquet.OpenFile(f, info.Size())
			if err != nil {
				t.Fatal(err)
			}
			var offset int64
			if len(data.Metadata().RowGroups) != 2 {
				t.Fatalf("row groups = %d", len(data.Metadata().RowGroups))
			}
			for _, c := range data.Metadata().RowGroups[1].Columns {
				if strings.Join(c.MetaData.PathInSchema, ".") == column {
					offset = c.MetaData.DataPageOffset
					break
				}
			}
			if offset == 0 {
				t.Fatalf("missing column %s: %#v", column, data.Schema().Columns())
			}
			// Destroy a late page header; the footer, index and unrelated columns
			// remain readable. A count-only scan would incorrectly pass this file.
			if _, err := f.WriteAt(make([]byte, 16), offset); err != nil {
				t.Fatal(err)
			}
			if err := ValidatePublishedBatch(dir); !IsBatchCorrupt(err) {
				t.Fatalf("late %s page error = %v", column, err)
			}
		})
	}
}

func TestNativeVerifierRejectsWrongSchemaAndTraceOrdering(t *testing.T) {
	for _, kind := range []string{"schema", "time_order", "id_order", "row_count"} {
		t.Run(kind, func(t *testing.T) {
			dir, rows := verificationFixture(t)
			path := filepath.Join(dir, "spans.parquet")
			switch kind {
			case "schema":
				if err := parquet.WriteFile(path, []struct {
					TraceID string `parquet:"trace_id"`
				}{{"trace"}, {"trace"}, {"trace"}, {"trace"}}); err != nil {
					t.Fatal(err)
				}
			case "time_order":
				rows[2].StartUnixNanos = 5
			case "id_order":
				rows[1].StartUnixNanos = rows[0].StartUnixNanos
				rows[1].SpanID = "0"
			case "row_count":
				rows = rows[:3]
			}
			if kind != "schema" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := writeUnsorted(path, Spans, rows, 1024); err != nil {
					t.Fatal(err)
				}
			}
			err := ValidatePublishedBatch(dir)
			if kind == "schema" {
				if !errors.Is(err, ErrUnsupportedBatchFormat) || IsBatchCorrupt(err) {
					t.Fatalf("unsupported schema classified as corruption: %v", err)
				}
			} else if !IsBatchCorrupt(err) {
				t.Fatalf("%s error = %v", kind, err)
			}
		})
	}
}

func TestVerifierResourceFailuresAreNotCorruption(t *testing.T) {
	dir, _ := verificationFixture(t)
	v, err := NewBatchVerifier(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	var zone, temp string
	if err := v.db.QueryRow("SELECT current_setting('TimeZone'), current_setting('temp_directory')").Scan(&zone, &temp); err != nil || zone != "UTC" || temp != "" {
		t.Fatalf("verifier settings timezone=%q spill=%q error=%v", zone, temp, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := v.Validate(ctx, dir); !errors.Is(err, context.Canceled) || IsBatchCorrupt(err) {
		t.Fatalf("cancellation = %v", err)
	}
	if _, err := v.db.Exec("SET memory_limit='1KB'"); err != nil {
		t.Fatal(err)
	}
	if err := v.Validate(context.Background(), dir); err == nil || IsBatchCorrupt(err) {
		t.Fatalf("resource failure = %v", err)
	}
	for _, kind := range []driver.ErrorType{driver.ErrorTypeOutOfMemory, driver.ErrorTypeInterrupt, driver.ErrorTypePermission, driver.ErrorTypeSettings, driver.ErrorTypeInternal, driver.ErrorTypeIO} {
		if IsBatchCorrupt(nativeVerificationError(&driver.Error{Type: kind})) {
			t.Fatalf("operational error %v classified as corruption", kind)
		}
	}
}

func TestNativeVerifierRejectsUnexpectedZeroCountSignal(t *testing.T) {
	dir, _ := verificationFixture(t)
	if err := parquet.WriteFile(filepath.Join(dir, "logs.parquet"), []struct {
		Body string `parquet:"body"`
	}{{"unaccounted"}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePublishedBatch(dir); !IsBatchCorrupt(err) || !strings.Contains(err.Error(), "metadata declares zero") {
		t.Fatalf("zero-count file = %v", err)
	}
}

// writeUnsorted writes rows in the order given, which may break the
// signal's order.
func writeUnsorted[R any](path string, s *Signal, rows []R, pageSize int) error {
	perm := make([]int32, len(rows))
	for i := range perm {
		perm[i] = int32(i)
	}
	return writeOrdered(path, s, rows, perm, pageSize)
}

// Log and metric files follow their signal's order, which names time buckets
// no column stores; verification decodes the key columns and rejects a file
// out of that order. Both fixtures are listed out of order.
func TestNativeVerifierRejectsLogAndMetricOrder(t *testing.T) {
	minute := int64(time.Minute)
	logs := []Log{
		{Namespace: "a", ServiceName: "y", EventUnixNanos: 1 * minute},
		{Namespace: "a", ServiceName: "x", EventUnixNanos: 2 * minute},
		{Namespace: "a", ServiceName: "x", EventUnixNanos: 6 * minute},
	}
	metrics := []Metric{
		{Namespace: "a", ServiceName: "x", Name: "m", SeriesID: 2, EventUnixNanos: 1 * minute},
		{Namespace: "a", ServiceName: "x", Name: "m", SeriesID: 1, EventUnixNanos: 2 * minute},
		{Namespace: "a", ServiceName: "x", Name: "m", SeriesID: 1, EventUnixNanos: 61 * minute},
	}
	for _, s := range []*Signal{Logs, Metrics} {
		t.Run(s.Name, func(t *testing.T) {
			store, err := OpenParquetStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := store.CommitBatch(context.Background(), &BatchMetadata{ID: "order"}, nil, logs, metrics); err != nil {
				t.Fatal(err)
			}
			dir := store.BatchPath("order")
			if err := ValidatePublishedBatch(dir); err != nil {
				t.Fatalf("committed batch: %v", err)
			}
			path := filepath.Join(dir, s.Name+".parquet")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if s == Logs {
				err = writeUnsorted(path, s, logs, 1024)
			} else {
				err = writeUnsorted(path, s, metrics, 1024)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidatePublishedBatch(dir); !IsBatchCorrupt(err) {
				t.Fatalf("unsorted %s error = %v", s.Name, err)
			}
		})
	}
}

// Field-name churn makes every row carry a variant metadata dictionary of up
// to 4 KiB, as compacted files of high-cardinality attributes do. The
// verifier checks all of a map's keys in one query over that dictionary, so
// it must still decode every column and check every key and row within its
// fixed, non-spilling memory limit.
func TestNativeVerifierFitsChurnedVariantMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("writes and verifies a batch of wide variant metadata")
	}
	const rows = 6_000
	spans := make([]Span, rows)
	for i := range spans {
		attributes, resource := map[string]any{}, map[string]any{}
		for _, key := range Spans.Keys() {
			var value any = fmt.Sprintf("%s-%d", key.Name, i%97)
			if key.Type == semconv.Int {
				value = int64(i % 1013)
			}
			if i%10 == 0 {
				value = fmt.Sprint(value) // converted at ingest
			}
			if key.Scope == semconv.Resource {
				resource[key.Name] = value
			} else {
				attributes[key.Name] = value
			}
		}
		for j := range 24 {
			attributes[fmt.Sprintf("app.generated.attribute.k%05d", (i*31+j*7919)%50_000)] = int64(j)
		}
		resource[fmt.Sprintf("host.generated.label.k%04d", i%2000)] = "x"
		spans[i] = Span{TraceID: fmt.Sprintf("%032x", i/4), SpanID: fmt.Sprintf("%016x", i), ServiceName: "api",
			Name: "op", StartUnixNanos: int64(i) * 1000, EndUnixNanos: int64(i)*1000 + 500, Resource: resource, Attributes: attributes}
	}
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitBatch(context.Background(), &BatchMetadata{ID: "churn"}, spans, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePublishedBatch(store.BatchPath("churn")); err != nil {
		t.Fatal(err)
	}
}
