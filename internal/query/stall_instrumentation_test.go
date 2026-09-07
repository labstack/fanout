package query

import (
	"context"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/metrics"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// A rollup or a maintenance pass used to take its connection from the same
// pool every read draws from. On a four-core host that pool holds four
// connections, so one background pass occupied a quarter of the machine's read
// capacity for its whole duration — and a request that waits for a connection
// waits invisibly.
func TestWritesDoNotDrawFromTheReadPool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := config.Config{DataDir: t.TempDir(), RollupInterval: time.Minute, DuckDBMemory: "128MB", DuckDBMaxConns: 4}
	repository, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatalf("open telemetry repository: %v", err)
	}
	defer repository.Close()
	d, err := NewDuck(ctx, cfg, repository)
	if err != nil {
		t.Fatalf("NewDuck: %v", err)
	}
	defer d.Close()

	if got := d.DB.Stats().MaxOpenConnections; got != 4 {
		t.Fatalf("read pool MaxOpenConnections = %d, want 4", got)
	}
	if got := d.writeDB.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("write handle MaxOpenConnections = %d, want 1 — writes are serialized by the gate anyway", got)
	}
	if d.writer() != d.writeDB {
		t.Fatal("writes are not going through the write handle")
	}

	// Hold a write transaction open and prove the read pool is untouched.
	tx, err := d.writer().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin write: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "CREATE OR REPLACE TABLE stall_probe (n INTEGER)"); err != nil {
		t.Fatalf("write inside transaction: %v", err)
	}
	if inUse := d.DB.Stats().InUse; inUse != 0 {
		t.Fatalf("read pool has %d connections in use while a write is open", inUse)
	}
	if inUse := d.writeDB.Stats().InUse; inUse != 1 {
		t.Fatalf("write handle has %d connections in use, want 1", inUse)
	}

	// The read pool is the one worth watching, so it is the one the gauges read.
	if got := metrics.DuckDBPoolStatsForTest().MaxOpenConnections; got != 4 {
		t.Fatalf("pool gauges read a pool with MaxOpenConnections = %d, want the read pool's 4", got)
	}
}

// A stalled query asks one question — how long did I wait to enter the
// snapshot? — and until this metric existed the answer was unavailable, which
// is why the first diagnosis of the stalls named a lock that does not exist.
func TestSnapshotWaitIsMeasuredForReaders(t *testing.T) {
	// Label values of this test's own, so the counts are this test's alone
	// however the package's tests are ordered.
	const admitted, refused = "test-admitted", "test-refused"
	d := &Duck{}
	before := testutil.CollectAndCount(metrics.ParquetReadWait)
	if err := d.lockParquetRead(context.Background(), admitted); err != nil {
		t.Fatalf("lockParquetRead: %v", err)
	}
	d.parquetMu.RUnlock()
	admittedCount := testutil.CollectAndCount(metrics.ParquetReadWait)
	if admittedCount != before+1 {
		t.Fatalf("an admitted reader added %d wait series, want 1", admittedCount-before)
	}

	// A reader that gives up is counted separately: a refusal and a long wait
	// are different failures.
	if err := d.parquetMu.LockContext(context.Background()); err != nil {
		t.Fatalf("take the publisher side: %v", err)
	}
	defer d.parquetMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := d.lockParquetRead(ctx, refused); err == nil {
		t.Fatal("reader entered a snapshot held by a publisher")
	}
	if got := testutil.ToFloat64(metrics.ParquetReadRefusals.WithLabelValues(refused)); got != 1 {
		t.Fatalf("refusals = %v, want 1", got)
	}
	// A refusal is not also counted as a wait: they are different failures.
	if got := testutil.CollectAndCount(metrics.ParquetReadWait); got != admittedCount {
		t.Fatalf("a refused reader added %d wait series, want 0", got-admittedCount)
	}
}

func TestPublisherQueuedReportsAWaitingSwap(t *testing.T) {
	var gate parquetReadGate
	if gate.publisherQueued() {
		t.Fatal("an idle gate reports a queued publisher")
	}
	if err := gate.RLockContext(context.Background()); err != nil {
		t.Fatalf("RLockContext: %v", err)
	}
	queued := make(chan struct{})
	go func() {
		close(queued)
		if err := gate.LockContext(context.Background()); err == nil {
			gate.Unlock()
		}
	}()
	<-queued
	deadline := time.Now().Add(time.Second)
	for !gate.publisherQueued() {
		if time.Now().After(deadline) {
			t.Fatal("a publisher waiting for readers is not reported as queued")
		}
		time.Sleep(time.Millisecond)
	}
	gate.RUnlock()
}
