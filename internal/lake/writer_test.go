package lake

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
)

func openWriterTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	if _, err := db.Exec(`ATTACH ':memory:' AS lake`); err != nil {
		t.Fatalf("attach lake catalog: %v", err)
	}
	if err := query.CreateTables(db); err != nil {
		t.Fatalf("CreateTables failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestWriterFlushBatchSize(t *testing.T) {
	db := openWriterTestDB(t)

	chSpans := make(chan SpanRow, 10)
	chLogs := make(chan LogRow, 10)
	chMetrics := make(chan MetricRow, 10)

	w := NewWriter(config.Config{
		FlushInterval:    time.Minute,
		FlushBatchSize:   2,
		DefaultNamespace: "default",
	}, db, chSpans, chLogs, chMetrics)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	now := time.Now().UnixNano()
	chSpans <- SpanRow{
		Namespace:      "ns-a",
		TraceID:        "trace-1",
		SpanID:         "span-1",
		ServiceName:    "svc",
		Name:           "op",
		StartUnixNanos: now,
		EndUnixNanos:   now + int64(time.Millisecond),
		DurationMs:     1,
		IngestedAt:     now,
	}
	chSpans <- SpanRow{
		Namespace:      "ns-a",
		TraceID:        "trace-1",
		SpanID:         "span-2",
		ServiceName:    "svc",
		Name:           "op",
		StartUnixNanos: now,
		EndUnixNanos:   now + int64(time.Millisecond),
		DurationMs:     1,
		IngestedAt:     now,
	}

	requireCount(t, db, `SELECT count(*) FROM lake.spans`, 2)
}

func TestWriterFlushesRemainderOnShutdown(t *testing.T) {
	db := openWriterTestDB(t)

	chSpans := make(chan SpanRow, 10)
	chLogs := make(chan LogRow, 10)
	chMetrics := make(chan MetricRow, 10)

	w := NewWriter(config.Config{
		FlushInterval:    time.Hour, // long, so only shutdown triggers the flush
		FlushBatchSize:   1000,      // large, so the single row never hits a size flush
		DefaultNamespace: "default",
	}, db, chSpans, chLogs, chMetrics)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()

	now := time.Now().UnixNano()
	chSpans <- SpanRow{
		Namespace: "ns-a", TraceID: "t", SpanID: "s", ServiceName: "svc", Name: "op",
		StartUnixNanos: now, EndUnixNanos: now + 1, DurationMs: 1, IngestedAt: now,
	}

	// Give the row time to be received into the buffer, then shut down. The
	// remaining buffered row must be drained and flushed before Wait() returns.
	time.Sleep(100 * time.Millisecond)
	cancel()
	w.Wait()

	var got int
	if err := db.QueryRow(`SELECT count(*) FROM lake.spans`).Scan(&got); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if got != 1 {
		t.Fatalf("count = %d, want 1 (row should be flushed on shutdown)", got)
	}
}

func TestEventTimeFallsBackWithoutSchemaDuplication(t *testing.T) {
	ingested := time.Now().UTC().Truncate(time.Microsecond)
	got, ok := eventTime(0, 0, ingested.UnixNano()).(time.Time)
	if !ok {
		t.Fatalf("eventTime() type = %T, want time.Time", got)
	}
	if !got.Equal(ingested) {
		t.Fatalf("eventTime() = %s, want %s", got, ingested)
	}

	observed := ingested.Add(-time.Second)
	got, ok = eventTime(0, observed.UnixNano(), ingested.UnixNano()).(time.Time)
	if !ok || !got.Equal(observed) {
		t.Fatalf("eventTime() secondary fallback = %v, want %s", got, observed)
	}

	primary := observed.Add(-time.Second)
	got, ok = eventTime(primary.UnixNano(), observed.UnixNano(), ingested.UnixNano()).(time.Time)
	if !ok || !got.Equal(primary) {
		t.Fatalf("eventTime() primary timestamp = %v, want %s", got, primary)
	}
}

func TestFlushWorkerReportsUnwrittenFinalCarry(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close duckdb: %v", err)
	}

	w := &Writer{db: db, cfg: config.Config{FlushBatchSize: 10}}
	flushCh := make(chan flushBatch, 1)
	done := make(chan error, 1)
	flushCh <- flushBatch{spans: []SpanRow{{TraceID: "t", SpanID: "s"}}}
	close(flushCh)
	w.flushWorker(flushCh, done)
	if err := <-done; err == nil {
		t.Fatal("flushWorker() error = nil, want final-flush error")
	}
}

func requireCount(t *testing.T, db *sql.DB, q string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var got int
		if err := db.QueryRow(q).Scan(&got); err == nil && got == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	var got int
	if err := db.QueryRow(q).Scan(&got); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	t.Fatalf("count = %d, want %d", got, want)
}
