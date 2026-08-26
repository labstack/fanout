// Package writegate serializes and measures writes to DuckDB's rebuildable
// rollup cache.
package writegate

import (
	"sync"
	"time"

	"github.com/labstack/fanout/internal/metrics"
)

// WriteOperation is a bounded metric label for a rollup-cache write.
// Keep this list exhaustive: arbitrary strings would create an unbounded
// Prometheus label surface.
type WriteOperation string

const (
	WriteRollupSkip     WriteOperation = "rollup_skip_to_latest"
	WriteRollupService  WriteOperation = "rollup_service"
	WriteRollupEndpoint WriteOperation = "rollup_endpoint"
	WriteRollupEdge     WriteOperation = "rollup_edge"
	WriteMaintenance    WriteOperation = "maintenance"
)

// WriteGate preserves the existing process-wide sync.Mutex acquisition
// semantics while measuring how long each operation waits for and holds the
// catalog write critical section. The zero value is ready for use.
type WriteGate struct {
	mu sync.Mutex
}

// observe is a package-level seam so a test can prove the observation happens
// after Unlock. That ordering is load-bearing and is otherwise untestable: a
// Prometheus write is microseconds, so a test that only times the release
// cannot tell the two orderings apart.
var observe = metrics.RecordWriteGate

// Lock acquires the cache write gate and returns its release function.
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
		observe(string(operation), acquired.Sub(waitStarted).Seconds(), hold.Seconds())
	}
}
