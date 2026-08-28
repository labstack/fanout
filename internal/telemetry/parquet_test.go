package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

func TestParquetStorePublishesCompleteBatchAndRecovers(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	span := completeTestSpan()
	metadata := BatchMetadata{ID: "batch-1", MinIngestedNanos: span.IngestedAt, MaxIngestedNanos: span.IngestedAt}
	if err := store.CommitBatch(metadata, []Span{span}, []Log{{Namespace: "tenant", Body: "ready", TimeUnixNanos: 12}}, []Metric{{Namespace: "tenant", Name: "requests", TimeUnixNanos: 13}}); err != nil {
		t.Fatal(err)
	}

	batchDir := store.BatchPath(metadata.ID)
	for _, name := range []string{"spans.parquet", "logs.parquet", "metrics.parquet", "trace.fidx", "metadata.json"} {
		if _, err := os.Stat(filepath.Join(batchDir, name)); err != nil {
			t.Fatalf("published batch is missing %s: %v", name, err)
		}
	}
	if got := store.RowCount(); got != 3 {
		t.Fatalf("row count = %d, want 3", got)
	}
	if err := store.CommitBatch(metadata, []Span{span}, nil, nil); err != nil {
		t.Fatalf("idempotent commit: %v", err)
	}
	if got := store.RowCount(); got != 3 {
		t.Fatalf("idempotent row count = %d, want 3", got)
	}

	// An interrupted, unpublished directory is neither cataloged nor retained.
	orphan := store.StagingPath("interrupted")
	if err := os.Mkdir(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "partial"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("unpublished staging directory survived restart: %v", err)
	}
	spans, err := traceAll(reopened, span.TraceID)
	if err != nil {
		t.Fatal(err)
	}
	wantIndexed := IndexedSpan{
		Namespace: span.Namespace, TraceID: span.TraceID, SpanID: span.SpanID, ParentSpanID: span.ParentSpanID,
		ServiceName: span.ServiceName, Name: span.Name, Kind: span.Kind, StartUnixNanos: span.StartUnixNanos,
		DurationMS: span.DurationMS, StatusCode: span.StatusCode, StatusMsg: span.StatusMsg,
	}
	if !reflect.DeepEqual(spans, []IndexedSpan{wantIndexed}) {
		t.Fatalf("trace projection mismatch\n got: %#v\nwant: %#v", spans, []IndexedSpan{wantIndexed})
	}
	gotSpan := readOneParquetRow[spanParquetRow](t, filepath.Join(batchDir, "spans.parquet"))
	wantSpan := makeSpanParquetRow(span)
	wantSpan.TraceHash = gotSpan.TraceHash
	if !reflect.DeepEqual(gotSpan, wantSpan) {
		t.Fatalf("complete span row mismatch\n got: %#v\nwant: %#v", gotSpan, wantSpan)
	}
}

func TestParquetStoreTraceUsesExactIDAndEventOrder(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spans := []Span{
		{TraceID: "wanted", SpanID: "later", StartUnixNanos: 30},
		{TraceID: "other", SpanID: "other", StartUnixNanos: 20},
		{TraceID: "wanted", SpanID: "earlier", StartUnixNanos: 10},
	}
	if err := store.CommitBatch(BatchMetadata{ID: "batch"}, spans, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := traceAll(store, "wanted")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SpanID != "earlier" || got[1].SpanID != "later" {
		t.Fatalf("ordered trace = %#v", got)
	}
	missing, err := traceAll(store, "missing")
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing trace = %#v, %v", missing, err)
	}
}

func TestParquetStoreTraceFiltersAndBoundsResultsDuringRead(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spans := make([]Span, 0, 102)
	for i := 100; i >= 1; i-- {
		spans = append(spans, Span{
			Namespace: "prod", TraceID: "large-trace", SpanID: fmt.Sprintf("span-%03d", i),
			StartUnixNanos: int64(i),
		})
	}
	spans = append(spans,
		Span{Namespace: "staging", TraceID: "large-trace", SpanID: "wrong-namespace", StartUnixNanos: 11},
		Span{Namespace: "prod", TraceID: "large-trace", SpanID: "outside-window", StartUnixNanos: 200},
	)
	if err := store.CommitBatch(BatchMetadata{ID: "large"}, spans, nil, nil); err != nil {
		t.Fatal(err)
	}

	got, err := store.Trace(context.Background(), TraceQuery{
		TraceID: "large-trace", Namespace: "prod", StartNanos: 10, EndNanos: 90, Limit: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].SpanID != "span-010" || got[1].SpanID != "span-011" || got[2].SpanID != "span-012" {
		t.Fatalf("bounded trace = %#v", got)
	}
}

func TestParquetStorePreservesCompleteLogAndMetricRows(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logRow := Log{
		Namespace: "tenant", EventUnixNanos: 11, TimeUnixNanos: 12, ObservedTimeNanos: 13,
		Severity: "ERROR", SeverityNumber: 17, Body: "declined", ServiceName: "checkout",
		TraceID: "trace", SpanID: "span", Flags: 1, ResourceJSON: []byte(`{"host":"one"}`),
		AttributesJSON: []byte(`{"attempt":2}`), ScopeName: "scope", ScopeVersion: "1.2.3",
		IngestedAt: 14, BodyTemplate: "declined: {reason}",
	}
	metricRow := Metric{
		Namespace: "tenant", EventUnixNanos: 21, TimeUnixNanos: 22, Name: "request.duration",
		Description: "request latency", Unit: "ms", Type: "histogram", ServiceName: "checkout", Value: 23.5,
		HistBoundsJSON: []byte(`[1,5,10]`), HistCountsJSON: []byte(`[2,3,4,5]`), HistCount: 14, HistSum: 47,
		ExemplarsJSON: []byte(`[{"trace_id":"trace"}]`), AttributesJSON: []byte(`{"route":"/pay"}`),
		ResourceJSON: []byte(`{"host":"one"}`), ScopeName: "scope", ScopeVersion: "1.2.3", IngestedAt: 24,
	}
	if err := store.CommitBatch(BatchMetadata{ID: "complete-signals"}, nil, []Log{logRow}, []Metric{metricRow}); err != nil {
		t.Fatal(err)
	}
	batchDir := store.BatchPath("complete-signals")
	gotLog := readOneParquetRow[logParquetRow](t, filepath.Join(batchDir, "logs.parquet"))
	if gotLog.LogTime != logRow.EventUnixNanos {
		t.Fatalf("log event time = %d, want canonical %d", gotLog.LogTime, logRow.EventUnixNanos)
	}
	if want := makeLogParquetRow(logRow); !reflect.DeepEqual(gotLog, want) {
		t.Fatalf("log row mismatch\n got: %#v\nwant: %#v", gotLog, want)
	}
	gotMetric := readOneParquetRow[metricParquetRow](t, filepath.Join(batchDir, "metrics.parquet"))
	if gotMetric.MetricTime != metricRow.EventUnixNanos {
		t.Fatalf("metric event time = %d, want canonical %d", gotMetric.MetricTime, metricRow.EventUnixNanos)
	}
	if want := makeMetricParquetRow(metricRow); !reflect.DeepEqual(gotMetric, want) {
		t.Fatalf("metric row mismatch\n got: %#v\nwant: %#v", gotMetric, want)
	}
}

func TestParquetStoreSkipsTraceIndexesOutsideTimeWindow(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitBatch(BatchMetadata{ID: "old-traces"}, []Span{{
		TraceID: "wanted", SpanID: "old", StartUnixNanos: 100,
	}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.BatchPath("old-traces"), "trace.fidx")); err != nil {
		t.Fatal(err)
	}
	got, err := store.Trace(context.Background(), TraceQuery{
		TraceID: "wanted", StartNanos: 1_000, EndNanos: 2_000, Limit: 10,
	})
	if err != nil {
		t.Fatalf("non-overlapping query opened old trace index: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("non-overlapping trace query returned %#v", got)
	}
}

func TestPublishReplacementValidatesBeforePublication(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	called := false
	err = store.PublishReplacement(stage, BatchMetadata{ID: "replacement"}, nil, func(publish func() error) error {
		called = true
		return publish()
	})
	if err == nil {
		t.Fatal("invalid replacement was accepted")
	}
	if called {
		t.Fatal("publication gate entered before replacement validation")
	}
}

func TestPruneOwnsStoragePublicationBeforeExcludingReaders(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitBatch(BatchMetadata{ID: "expired", MaxIngestedNanos: 1}, []Span{{TraceID: "trace"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	store.publishMu.Lock()
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := store.PruneBefore(2, 1, func(prune func() error) error {
			close(entered)
			return prune()
		})
		done <- err
	}()
	select {
	case <-entered:
		store.publishMu.Unlock()
		t.Fatal("reader exclusion began before storage publication ownership")
	case <-time.After(20 * time.Millisecond):
	}
	store.publishMu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPublishReplacementKeepsOutputOnlyAfterSyncFailure(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	span := []Span{{TraceID: "trace", SpanID: "span", StartUnixNanos: 1}}
	if err := store.CommitBatch(BatchMetadata{ID: "input"}, span, nil, nil); err != nil {
		t.Fatal(err)
	}
	output := BatchMetadata{ID: "output"}
	if err := store.CommitBatch(output, span, nil, nil); err != nil {
		t.Fatal(err)
	}
	originalSync := syncPublishedDirectory
	syncPublishedDirectory = func(string) error { return errors.New("injected directory sync failure") }
	t.Cleanup(func() { syncPublishedDirectory = originalSync })

	err = store.PublishReplacement(filepath.Join(t.TempDir(), "missing-stage"), output, []string{"input"}, func(publish func() error) error {
		return publish()
	})
	if err == nil {
		t.Fatal("replacement succeeded despite injected sync failure")
	}
	if _, err := os.Stat(store.BatchPath("input")); !os.IsNotExist(err) {
		t.Fatalf("input was restored beside live output: %v", err)
	}
	if _, err := os.Stat(store.BatchPath("output")); err != nil {
		t.Fatalf("replacement output missing: %v", err)
	}
	metadata := store.BatchMetadata()
	if len(metadata) != 1 || metadata[0].ID != "output" {
		t.Fatalf("live batches after sync failure = %#v, want output only", metadata)
	}
}

func TestParquetStoreRejectsUnsafeBatchID(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", ".hidden", "../escape", "has space"} {
		if err := store.CommitBatch(BatchMetadata{ID: id}, []Span{{TraceID: "t"}}, nil, nil); err == nil {
			t.Fatalf("CommitBatch accepted unsafe ID %q", id)
		}
	}
}

func TestParquetStoreCleansOnlyRetiredDirectories(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	retired := filepath.Join(store.BatchesDir(), "old.retired-compacted")
	if err := os.Mkdir(retired, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitBatch(BatchMetadata{ID: "contains.retired"}, []Span{{TraceID: "trace"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.CleanupRetired(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(retired); !os.IsNotExist(err) {
		t.Fatalf("retired directory remains: %v", err)
	}
	if _, err := os.Stat(store.BatchPath("contains.retired")); err != nil {
		t.Fatalf("published batch with retired in its ID was removed: %v", err)
	}
}

func TestParquetStoreRejectsMetadataRowCountMismatchOnOpen(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenParquetStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitBatch(BatchMetadata{ID: "mismatch"}, []Span{{TraceID: "trace"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	metadataPath := filepath.Join(store.BatchPath("mismatch"), "metadata.json")
	metadata, err := readBatchMetadata(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	metadata.Spans++
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metadataPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenParquetStore(dir); err == nil {
		t.Fatal("OpenParquetStore accepted metadata with the wrong row count")
	}
}

func completeTestSpan() Span {
	return Span{
		Namespace: "tenant", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef",
		ParentSpanID: "fedcba9876543210", ServiceName: "checkout", Name: "POST /orders", Kind: "SERVER",
		StartUnixNanos: 10, EndUnixNanos: 20, DurationMS: 0.00001, StatusCode: "ERROR", StatusMsg: "declined",
		ResourceJSON: []byte(`{"host":"one"}`), AttributesJSON: []byte(`{"http.request.method":"POST"}`),
		EventsJSON: []byte(`[{"name":"exception"}]`), LinksJSON: []byte(`[{"trace_id":"linked"}]`),
		TraceState: "vendor=value", Flags: 1, ScopeName: "scope", ScopeVersion: "1.2.3", IngestedAt: 30,
		HTTPMethod: "POST", HTTPStatusCode: "500", HTTPRoute: "/orders", DBSystem: "postgresql",
		RPCMethod: "Create", RPCService: "orders.v1.Orders", PeerService: "payments", ServiceVersion: "4.5.6",
		DeploymentEnv: "production", ExceptionType: "CardDeclined", ExceptionMessage: "declined",
	}
}

func traceAll(store *ParquetStore, traceID string) ([]IndexedSpan, error) {
	return store.Trace(context.Background(), TraceQuery{
		TraceID: traceID, StartNanos: math.MinInt64, EndNanos: math.MaxInt64, Limit: maxTraceQueryResults,
	})
}

func readOneParquetRow[T any](t *testing.T, path string) T {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader := parquet.NewGenericReader[T](file)
	defer reader.Close()
	var rows [1]T
	if n, err := reader.Read(rows[:]); err != nil && !errors.Is(err, io.EOF) || n != 1 {
		t.Fatalf("read %s: rows=%d err=%v", path, n, err)
	}
	return rows[0]
}
