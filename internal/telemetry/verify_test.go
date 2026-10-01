package telemetry

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	driver "github.com/duckdb/duckdb-go/v2"
	"github.com/parquet-go/parquet-go"
)

func verificationFixture(t *testing.T) (string, []spanParquetRow) {
	t.Helper()
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	spans := []Span{
		{TraceID: "trace", SpanID: "a", StartUnixNanos: 10, Resource: map[string]any{"service.name": "api", "extra": []any{int64(1), true}}, Attributes: map[string]any{"messaging.system": "kafka", "other": []byte{0, 255}}},
		{TraceID: "trace", SpanID: "b", StartUnixNanos: 20},
		{TraceID: "trace", SpanID: "c", StartUnixNanos: 30},
		{TraceID: "trace", SpanID: "d", StartUnixNanos: 40, Attributes: map[string]any{"other": []byte{0, 255}}, Resource: map[string]any{"service.name": "last", "extra": []any{int64(2), false}}},
	}
	if err := store.CommitBatch(context.Background(), BatchMetadata{ID: "verify"}, spans, nil, nil); err != nil {
		t.Fatal(err)
	}
	dir := store.BatchPath("verify")
	f, err := os.Open(filepath.Join(dir, "spans.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	r := parquet.NewGenericReader[spanParquetRow](f, telemetrySchema[spanParquetRow]())
	rows := make([]spanParquetRow, len(spans))
	if n, err := r.Read(rows); n != len(rows) || (err != nil && !errors.Is(err, io.EOF)) {
		t.Fatal(err)
	}
	r.Close()
	f.Close()
	// Multiple tiny row groups make a late-page failure observable cheaply.
	path := filepath.Join(dir, "spans.parquet")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	f, err = os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := parquet.NewGenericWriter[spanParquetRow](f, parquetWriterOptions(1024, telemetrySchema[spanParquetRow]())...)
	for i := 0; i < len(rows); i += 2 {
		if err := writeTelemetryColumns(writer, rows[i:i+2]); err != nil {
			t.Fatal(err)
		}
		if err := writer.Flush(); err != nil {
			t.Fatal(err)
		}
	}
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
				rows[2].StartUnixNano = 5
			case "id_order":
				rows[1].StartUnixNano = rows[0].StartUnixNano
				rows[1].SpanID = "0"
			case "row_count":
				rows = rows[:3]
			}
			if kind != "schema" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := writeTypedParquet(path, rows, 1024); err != nil {
					t.Fatal(err)
				}
			}
			if err := ValidatePublishedBatch(dir); !IsBatchCorrupt(err) {
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
