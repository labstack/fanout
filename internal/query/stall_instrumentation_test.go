package query

import (
	"context"
	"fmt"
	"sync"
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

	// Use the read pool first, so its idle count is non-zero and the assertion
	// below is about where the write went rather than about a pool nobody has
	// touched.
	if err := d.DB.QueryRowContext(ctx, "SELECT 1").Scan(new(int)); err != nil {
		t.Fatalf("read: %v", err)
	}
	if idle := d.DB.Stats().Idle; idle == 0 {
		t.Fatal("the read pool holds no connection after a read; the assertion below would prove nothing")
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
	admitted := fmt.Sprintf("test-admitted-%d", time.Now().UnixNano())
	refused := fmt.Sprintf("test-refused-%d", time.Now().UnixNano())
	d := &Duck{}
	before := testutil.CollectAndCount(metrics.ParquetReadWaitForTest())
	if err := d.lockParquetRead(context.Background(), admitted); err != nil {
		t.Fatalf("lockParquetRead: %v", err)
	}
	d.parquetMu.RUnlock()
	admittedCount := testutil.CollectAndCount(metrics.ParquetReadWaitForTest())
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
	if refusals := testutil.ToFloat64(metrics.ParquetReadRefusalsForTest(refused)); refusals != 1 {
		t.Fatalf("refusals = %v, want 1", refusals)
	}
	// A refusal is not also counted as a wait: they are different failures.
	if series := testutil.CollectAndCount(metrics.ParquetReadWaitForTest()); series != admittedCount {
		t.Fatalf("a refused reader added %d wait series, want 0", series-admittedCount)
	}
}

func TestPublisherGraceLeftTracksAWaitingSwap(t *testing.T) {
	gate := parquetReadGate{writerGrace: time.Second}
	if left := gate.publisherGraceLeft(); left != time.Second {
		t.Fatalf("an idle gate reports %v of grace left, want the whole %v", left, time.Second)
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
	for gate.publisherGraceLeft() == time.Second {
		if time.Now().After(deadline) {
			t.Fatal("a publisher waiting for readers did not start consuming its grace")
		}
		time.Sleep(time.Millisecond)
	}
	// The grace runs down rather than vanishing: a reader admitted now is not
	// blocking anything yet, which is what stops the rollup yielding on sight.
	if left := gate.publisherGraceLeft(); left <= 0 || left > time.Second {
		t.Fatalf("grace left = %v, want a positive remainder under %v", left, time.Second)
	}
	deadline = time.Now().Add(3 * time.Second)
	for gate.publisherGraceLeft() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("grace never ran out")
		}
		time.Sleep(5 * time.Millisecond)
	}
	gate.RUnlock()
}

// database/sql closes a connector that implements io.Closer when its DB is
// closed, and duckdb's connector closes the instance behind an unsynchronized
// bool. Two handles over one connector therefore meant two duckdb_close calls
// on one instance — a double free that kills the process rather than returning
// an error. The write handle borrows the instance; the read pool owns it.
func TestClosingTheWriteHandleLeavesTheInstanceAlive(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DataDir: t.TempDir(), RollupInterval: time.Minute, DuckDBMemory: "128MB"}
	repository, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatalf("open telemetry repository: %v", err)
	}
	defer repository.Close()
	d, err := NewDuck(ctx, cfg, repository)
	if err != nil {
		t.Fatalf("NewDuck: %v", err)
	}

	if err := d.writeDB.Close(); err != nil {
		t.Fatalf("close write handle: %v", err)
	}
	var one int
	if err := d.DB.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("read pool died with the write handle: %v", err)
	}

	// Close is idempotent, including under a second shutdown path arriving at
	// the same time.
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = d.Close()
		}()
	}
	wg.Wait()
	if err := d.Close(); err != nil {
		t.Fatalf("closing an already-closed Duck: %v", err)
	}
}

// Installing a gauge source is last-caller-wins; removing one is not. A Duck
// that closes after another has taken over must clear nothing, or the gauges
// go blank while a pool is still serving.
func TestClosingADisplacedDuckLeavesTheGaugeSourceAlone(t *testing.T) {
	ctx := context.Background()
	open := func(conns int) *Duck {
		t.Helper()
		cfg := config.Config{DataDir: t.TempDir(), RollupInterval: time.Minute, DuckDBMemory: "128MB", DuckDBMaxConns: conns}
		repository, err := telemetrystore.Open(cfg.TelemetryDir())
		if err != nil {
			t.Fatalf("open telemetry repository: %v", err)
		}
		t.Cleanup(func() { _ = repository.Close() })
		d, err := NewDuck(ctx, cfg, repository)
		if err != nil {
			t.Fatalf("NewDuck: %v", err)
		}
		return d
	}
	first := open(2)
	second := open(3)
	defer second.Close()
	// second installed itself over first. first closing must leave it alone.
	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}
	if got := metrics.DuckDBPoolStatsForTest().MaxOpenConnections; got != 3 {
		t.Fatalf("pool gauges read MaxOpenConnections = %d after a displaced Duck closed, want the live pool's 3", got)
	}
}
