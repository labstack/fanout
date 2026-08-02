// Package writegate serializes and measures DuckLake catalog writes shared by
// the query kernel and telemetry writer.
package writegate

import (
	"sync"
	"time"

	"github.com/labstack/fanout/internal/metrics"
)

// WriteOperation is a bounded metric label for a DuckLake catalog write.
// Keep this list exhaustive: arbitrary strings would create an unbounded
// Prometheus label surface.
type WriteOperation string

const (
	WriteIngestSpans    WriteOperation = "ingest_spans"
	WriteIngestLogs     WriteOperation = "ingest_logs"
	WriteIngestMetrics  WriteOperation = "ingest_metrics"
	WriteRollupSkip     WriteOperation = "rollup_skip_to_latest"
	WriteRollupService  WriteOperation = "rollup_service"
	WriteRollupEndpoint WriteOperation = "rollup_endpoint"
	WriteRollupEdge     WriteOperation = "rollup_edge"
	WriteMerge          WriteOperation = "merge"
	WriteMaintenance    WriteOperation = "maintenance"
)

var allWriteOperations = [...]WriteOperation{
	WriteIngestSpans,
	WriteIngestLogs,
	WriteIngestMetrics,
	WriteRollupSkip,
	WriteRollupService,
	WriteRollupEndpoint,
	WriteRollupEdge,
	WriteMerge,
	WriteMaintenance,
}

var instrumentationEnabled = metrics.DataPlaneInstrumentationEnabled

// WriteGate preserves the existing process-wide sync.Mutex semantics while
// measuring how long each operation waits for and holds the catalog write
// critical section.
type WriteGate struct {
	mu      sync.Mutex
	now     func() time.Time
	observe func(WriteOperation, time.Duration, time.Duration)
}

// NewWriteGate constructs the shared DuckLake catalog write gate.
func NewWriteGate() *WriteGate {
	return &WriteGate{}
}

func newWriteGate(
	now func() time.Time,
	observe func(WriteOperation, time.Duration, time.Duration),
) *WriteGate {
	return &WriteGate{now: now, observe: observe}
}

// Lock acquires the catalog write gate and returns its release function. Callers
// must defer the returned function before acquiring a database connection,
// transaction, or appender. The zero value is ready for use.
func (g *WriteGate) Lock(operation WriteOperation) func() {
	operation.validate()
	// Custom observers are used by unit tests and remain authoritative. The
	// normal link-time-disabled benchmark control preserves the mutex behavior
	// while bypassing clocks and Prometheus observations entirely.
	if g.observe == nil && !instrumentationEnabled() {
		g.mu.Lock()
		return g.mu.Unlock
	}
	now := g.now
	if now == nil {
		now = time.Now
	}
	observe := g.observe
	if observe == nil {
		observe = func(operation WriteOperation, wait, hold time.Duration) {
			metrics.RecordWriteGate(string(operation), wait.Seconds(), hold.Seconds())
		}
	}

	waitStarted := now()
	g.mu.Lock()
	acquired := now()
	return func() {
		hold := now().Sub(acquired)
		g.mu.Unlock()
		observe(operation, acquired.Sub(waitStarted), hold)
	}
}

// With runs fn while holding the catalog write gate. The gate is acquired
// before fn starts, so callers can acquire a database connection inside fn and
// preserve the required gate -> connection -> transaction/appender order.
// The defer releases the gate and records the observation on success, returned
// error, or panic.
func (g *WriteGate) With(operation WriteOperation, fn func() error) error {
	unlock := g.Lock(operation)
	defer unlock()
	return fn()
}

func (operation WriteOperation) validate() {
	for _, allowed := range allWriteOperations {
		if operation == allowed {
			return
		}
	}
	panic("writegate: invalid DuckLake write operation: " + string(operation))
}
