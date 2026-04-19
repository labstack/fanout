package lake

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/labstack/fanout/internal/env"
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

	w := NewWriter(env.Config{
		FlushSeconds:   60,
		FlushBatchSize: 2,
		DefaultNS:      "default",
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
