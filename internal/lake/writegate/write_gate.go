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

// WriteGate preserves the existing process-wide sync.Mutex acquisition
// semantics while measuring how long each operation waits for and holds the
// catalog write critical section. The zero value is ready for use.
type WriteGate struct {
	mu sync.Mutex
}

// Lock acquires the catalog write gate and returns its release function.
// Callers must defer the returned function before acquiring a database
// connection, transaction, or appender, and must call it exactly once.
func (g *WriteGate) Lock(operation WriteOperation) func() {
	waitStarted := time.Now()
	g.mu.Lock()
	acquired := time.Now()
	return func() {
		hold := time.Since(acquired)
		// Unlock before observing: a Prometheus histogram takes its own lock,
		// and holding the process's hottest critical section across that would
		// be a throughput regression in the code added to detect one.
		g.mu.Unlock()
		metrics.RecordWriteGate(string(operation), acquired.Sub(waitStarted).Seconds(), hold.Seconds())
	}
}
