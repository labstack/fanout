package env

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Runtime sizing. DuckDB is a library inside a Go process, but its own defaults
// are written as though it owned the machine: left alone it claims 80% of
// detected memory, and the Go heap, goroutine stacks, and GC headroom sit on
// top of that. On a 2 vCPU / 8 GB host that combination reached 7.5 GB RSS and
// the kernel killed the process. These functions decide what the process
// reserves before it starts, and every one of them defers to an explicit
// environment variable.

const (
	// duckDBMemoryPercent is DuckDB's share of detected memory. DuckDB's own
	// default is 80%, which is the setting that was OOM-killed with ~1.3 GB of
	// Go runtime alongside it. The remaining 40% is headroom that scales with
	// the machine instead of being a fixed constant that is generous on a large
	// host and useless on a small one.
	duckDBMemoryPercent = 60

	// minDetectableMemory guards against misreads. A limit of a few megabytes
	// is not a real machine, and capping DuckDB there would be worse than
	// leaving its own default in place.
	minDetectableMemory = 512 << 20

	// maxAutoDuckDBConns bounds the pool. Past the point where connections
	// exceed available parallelism they add contention rather than concurrency.
	maxAutoDuckDBConns = 16

	// minDuckDBConns preserves an invariant rather than a preference:
	// internal/lake rejects DUCKDB_MAX_CONNS > 1 without a shared write gate,
	// and a pool of 1 serializes reads behind writes.
	minDuckDBConns = 2
)

// sizingSource records which values the machine chose, so the startup log can
// distinguish a resolved value from one the operator pinned. Without that
// distinction an adaptive default is unfalsifiable in support: nobody can say
// what the machine picked, or whether it picked anything at all.
type sizingSource struct {
	MemoryAuto   bool
	MaxConnsAuto bool
	DetectedRAM  uint64
}

// resolveSizing fills in the values the operator left for the machine to
// decide. Explicit configuration always wins; this only touches zero values.
func (c *Config) resolveSizing() sizingSource {
	var src sizingSource
	if strings.TrimSpace(c.DuckDBMemory) == "" {
		src.MemoryAuto = true
		src.DetectedRAM = detectAvailableMemory()
		c.DuckDBMemory = resolveDuckDBMemory(src.DetectedRAM)
	}
	if c.DuckDBMaxConns <= 0 {
		src.MaxConnsAuto = true
		c.DuckDBMaxConns = resolveDuckDBMaxConns(runtime.GOMAXPROCS(0))
	}
	return src
}

// logResolvedSizing reports what the process will actually run with.
func (c Config) logResolvedSizing(src sizingSource) {
	memory := c.DuckDBMemory
	if memory == "" {
		// Detection declined, so DuckDB keeps its own 80%-of-RAM default — the
		// behavior that can get the process OOM-killed. Say so rather than
		// logging an empty string that reads like "nothing to see here".
		memory = "duckdb-default(80% of detected RAM)"
	}
	slog.Info("runtime sizing resolved",
		"duckdb_memory", memory,
		"duckdb_memory_auto", src.MemoryAuto,
		"duckdb_max_conns", c.DuckDBMaxConns,
		"duckdb_max_conns_auto", src.MaxConnsAuto,
		"detected_memory_bytes", src.DetectedRAM,
		"gomaxprocs", runtime.GOMAXPROCS(0),
	)
	if src.MemoryAuto && c.DuckDBMemory == "" {
		slog.Warn("could not detect available memory; DuckDB will size itself to 80% of RAM, which can exceed the machine once the Go runtime is added — set DUCKDB_MEMORY explicitly")
	}
}

// resolveDuckDBMemory returns a DuckDB memory budget for a machine of the given
// size, or "" to leave DuckDB's own behavior in place.
//
// Returning "" for an undetectable or implausibly small machine is deliberate:
// capping a host whose size is unknown risks pinning a large machine to a small
// number, which trades a hypothetical crash for a certain slowdown.
func resolveDuckDBMemory(available uint64) string {
	if available < minDetectableMemory {
		return ""
	}
	budgetMB := available / (1 << 20) * duckDBMemoryPercent / 100
	if budgetMB == 0 {
		return ""
	}
	return fmt.Sprintf("%dMB", budgetMB)
}

// resolveDuckDBMaxConns sizes the connection pool from available parallelism.
func resolveDuckDBMaxConns(cores int) int {
	conns := cores
	if conns < minDuckDBConns {
		conns = minDuckDBConns
	}
	if conns > maxAutoDuckDBConns {
		conns = maxAutoDuckDBConns
	}
	return conns
}

// detectAvailableMemory reports the memory this process may actually use, or 0
// when no source is conclusive.
//
// cgroup limits come first because a container limit — not the host's total —
// is what the kernel enforces, and reading the host's memory from inside a
// constrained container is precisely the mistake this code exists to fix.
func detectAvailableMemory() uint64 {
	for _, path := range []string{
		"/sys/fs/cgroup/memory.max",                   // cgroup v2
		"/sys/fs/cgroup/memory/memory.limit_in_bytes", // cgroup v1
	} {
		if limit, ok := readCgroupLimit(path); ok {
			return limit
		}
	}
	return readMemTotal("/proc/meminfo")
}

// readCgroupLimit reads a cgroup memory limit. It reports ok=false for "max"
// and for the effectively-unlimited sentinel cgroup v1 uses, so detection falls
// through to the next source rather than treating "no limit" as a limit.
func readCgroupLimit(path string) (uint64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	text := strings.TrimSpace(string(raw))
	if text == "max" {
		return 0, false
	}
	limit, err := strconv.ParseUint(text, 10, 64)
	if err != nil || limit == 0 {
		return 0, false
	}
	// cgroup v1 spells "unlimited" as a number near the top of the range.
	if limit >= 1<<62 {
		return 0, false
	}
	return limit, true
}

// readMemTotal reads MemTotal from a /proc/meminfo-formatted file, in bytes.
func readMemTotal(path string) uint64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb << 10
	}
	return 0
}
