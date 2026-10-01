package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"
)

func TestEventBoundsUsePhysicalNanosecondsAfterReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	spans := []Span{{TraceID: "trace", SpanID: "negative", StartUnixNanos: -7, EndUnixNanos: -3, IngestedAt: 999}, {TraceID: "trace", SpanID: "positive", StartUnixNanos: 23, EndUnixNanos: 24, IngestedAt: 999}}
	logs := []Log{{EventUnixNanos: 5, TimeUnixNanos: 1, IngestedAt: 999}, {ObservedTimeNanos: 17, IngestedAt: 999}}
	metrics := []Metric{{EventUnixNanos: 11, TimeUnixNanos: 1, IngestedAt: 999}, {IngestedAt: 999}}
	if err := store.CommitBatch(t.Context(), BatchMetadata{ID: "bounds"}, spans, logs, metrics); err != nil {
		t.Fatal(err)
	}
	store, err = OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := store.BatchMetadata()[0]
	if b.SpanTime != (TimeRange{-7, 23, true}) || b.LogTime != (TimeRange{5, 17, true}) || b.MetricTime != (TimeRange{11, 999, true}) {
		t.Fatalf("physical bounds: %#v", b)
	}
	rows, totals, err := store.Trace(t.Context(), TraceQuery{TraceID: "trace", StartNanos: -10, EndNanos: 0, Limit: 10})
	if err != nil || len(rows) != 1 || totals.Spans != 1 {
		t.Fatalf("negative trace: %#v %#v %v", rows, totals, err)
	}
	metadata, err := os.ReadFile(filepath.Join(store.BatchPath("bounds"), "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(metadata, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["version"] != float64(3) || fields["SpanTime"] != nil {
		t.Fatalf("format changed: %s", metadata)
	}
}
func TestEventBoundsRequireCompleteRowGroupStatistics(t *testing.T) {
	rows := make([]logParquetRow, parquetRowGroupRows+1)
	for i := range rows {
		rows[i].LogTime = int64(i) - 25000
	}
	for _, missing := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "logs.parquet")
		options := []parquet.WriterOption{}
		if missing {
			options = append(options, parquet.SkipPageBounds("log_time"))
		}
		if err := writeTypedParquet(path, rows, parquetPageSize, options...); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		info, _ := f.Stat()
		file, err := parquet.OpenFile(f, info.Size())
		if err != nil {
			t.Fatal(err)
		}
		bounds := parquetTimeRange(file, "log_time")
		f.Close()
		if missing {
			if bounds.Known {
				t.Fatalf("missing stats considered known: %#v", bounds)
			}
		} else if bounds != (TimeRange{-25000, 25000, true}) {
			t.Fatalf("row groups: %#v", bounds)
		}
	}
}
