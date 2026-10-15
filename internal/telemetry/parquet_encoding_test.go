package telemetry

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/format"
)

// Dashboard panels filter on span kind and status; a dictionary lets the
// engine evaluate those predicates once per distinct value instead of decoding
// one string per row. Identifiers stay plain: a dictionary over a near-unique
// column is a second copy of it. Namespaces, services and operation names stay
// plain because clients bound neither their length nor their count.
func TestSpanParquetDictionaryEncodesLowCardinalityColumns(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	spans := make([]Span, 300)
	for i := range spans {
		spans[i] = Span{Namespace: "prod", TraceID: strings.Repeat("a", 30) + string(rune('a'+i%26)) + string(rune('a'+i/26)), SpanID: strings.Repeat("b", 14) + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			ServiceName: "checkout", Name: "POST /orders", Kind: "SPAN_KIND_SERVER", StatusCode: "STATUS_CODE_UNSET", StartUnixNanos: int64(i + 1), EndUnixNanos: int64(i + 2), IngestedAt: 999}
	}
	if err := store.CommitBatch(t.Context(), BatchMetadata{ID: "encodings"}, spans, nil, nil); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(store.BatchPath("encodings"), "spans.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, _ := f.Stat()
	file, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	encodings := map[string][]format.Encoding{}
	for _, chunk := range file.Metadata().RowGroups[0].Columns {
		if len(chunk.MetaData.PathInSchema) == 1 {
			encodings[chunk.MetaData.PathInSchema[0]] = chunk.MetaData.Encoding
		}
	}
	for _, column := range []string{"kind", "status"} {
		if !slices.Contains(encodings[column], format.RLEDictionary) {
			t.Errorf("%s is not dictionary encoded: %v", column, encodings[column])
		}
	}
	for _, column := range []string{"namespace", "service", "operation", "trace_id", "span_id", "parent_span_id", "events_json", "links_json"} {
		if slices.Contains(encodings[column], format.RLEDictionary) {
			t.Errorf("%s is dictionary encoded: %v", column, encodings[column])
		}
	}
	// The encoding is physical layout, not schema: the fixed format-4 schema
	// still matches, so files written either way remain readable together.
	if !parquet.EqualNodes(file.Schema(), telemetrySchema[spanParquetRow]()) {
		t.Fatal("dictionary encoding changed the physical schema")
	}
	rows, totals, err := store.Trace(t.Context(), TraceQuery{TraceID: spans[7].TraceID, StartNanos: 0, EndNanos: 1000, Limit: 10})
	if err != nil || len(rows) != 1 || totals.Spans != 1 || rows[0].Kind != "SPAN_KIND_SERVER" || rows[0].StatusCode != "STATUS_CODE_UNSET" {
		t.Fatalf("round trip: %#v %#v %v", rows, totals, err)
	}
}

// HTTP routes are client-supplied text. Past parquetDictionaryMaxBytes the
// column continues in plain pages, so distinct routes stop growing its
// dictionary once a page flushes, and every row still reads back.
func TestSpanDictionaryFallsBackToPlainPastItsBound(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spans := make([]Span, 20_000)
	for i := range spans {
		spans[i] = Span{Namespace: "prod", TraceID: fmt.Sprintf("%032x", i), SpanID: fmt.Sprintf("%016x", i),
			ServiceName: "checkout", HTTPRoute: fmt.Sprintf("/%0100d", i), Name: "op", Kind: "SPAN_KIND_SERVER", StatusCode: "STATUS_CODE_OK",
			StartUnixNanos: int64(i + 1), EndUnixNanos: int64(i + 2), IngestedAt: 999}
	}
	if err := store.CommitBatch(t.Context(), BatchMetadata{ID: "bounded"}, spans, nil, nil); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(store.BatchPath("bounded"), "spans.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, _ := f.Stat()
	file, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	var stats []format.PageEncodingStats
	for _, chunk := range file.Metadata().RowGroups[0].Columns {
		if chunk.MetaData.PathInSchema[0] == "http_route" {
			stats = chunk.MetaData.EncodingStats
		}
	}
	plainPages := false
	for _, s := range stats {
		plainPages = plainPages || (s.PageType != format.DictionaryPage && s.Encoding == format.Plain)
	}
	if !plainPages {
		t.Fatalf("http_route stayed dictionary encoded past its bound: %+v", stats)
	}
	rows := make([]spanParquetRow, len(spans))
	if n, err := parquet.NewGenericReader[spanParquetRow](file).Read(rows); n != len(spans) || (err != nil && !errors.Is(err, io.EOF)) {
		t.Fatalf("read %d rows: %v", n, err)
	}
	routes := map[string]bool{}
	for _, row := range rows {
		routes[row.HTTPRoute] = true
	}
	if len(routes) != len(spans) || !routes[spans[len(spans)-1].HTTPRoute] {
		t.Fatalf("read back %d distinct routes, want %d", len(routes), len(spans))
	}
}
