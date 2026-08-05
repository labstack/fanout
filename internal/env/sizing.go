package env

import (
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
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

// RuntimeSizing is the safe, non-secret sizing state exposed through runtime
// diagnostics. It reports the effective values after startup resolution and
// preserves whether each value came from the machine or the operator.
type RuntimeSizing struct {
	DuckDBMemory        string `json:"duckdb_memory"`
	DuckDBMemoryAuto    bool   `json:"duckdb_memory_auto"`
	DuckDBMaxConns      int    `json:"duckdb_max_conns"`
	DuckDBMaxConnsAuto  bool   `json:"duckdb_max_conns_auto"`
	DuckDBThreads       int    `json:"duckdb_threads"`
	DetectedMemoryBytes uint64 `json:"detected_memory_bytes"`
	GOMAXPROCS          int    `json:"gomaxprocs"`
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
	c.resolvedSizing = src
	return src
}

// RuntimeSizing returns the effective startup sizing without exposing any
// credentials or unrelated configuration.
func (c Config) RuntimeSizing() RuntimeSizing {
	return RuntimeSizing{
		DuckDBMemory:        c.DuckDBMemory,
		DuckDBMemoryAuto:    c.resolvedSizing.MemoryAuto,
		DuckDBMaxConns:      c.DuckDBMaxConns,
		DuckDBMaxConnsAuto:  c.resolvedSizing.MaxConnsAuto,
		DuckDBThreads:       c.DuckDBThreads,
		DetectedMemoryBytes: c.resolvedSizing.DetectedRAM,
		GOMAXPROCS:          runtime.GOMAXPROCS(0),
	}
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
	hostMemory := detectHostMemory()
	cgroupLimit := detectCgroupMemoryLimit("/proc/self/cgroup", "/proc/self/mountinfo")

	// A private cgroup namespace commonly exposes the process cgroup as the
	// mount root. Keep these fallbacks for minimal containers that omit procfs
	// metadata but still mount the memory controller.
	if cgroupLimit == 0 {
		for _, path := range []string{
			"/sys/fs/cgroup/memory.max",                   // cgroup v2
			"/sys/fs/cgroup/memory/memory.limit_in_bytes", // cgroup v1
		} {
			if limit, ok := readCgroupLimit(path); ok {
				cgroupLimit = limit
				break
			}
		}
	}

	// A configured cgroup limit can exceed physical RAM. The process is bound by
	// whichever ceiling is lower, so never turn an oversized cgroup value into a
	// DuckDB budget larger than the machine.
	return minPositive(hostMemory, cgroupLimit)
}

type cgroupMembership struct {
	version int
	path    string
}

type cgroupMount struct {
	version    int
	root       string
	mountPoint string
}

// detectCgroupMemoryLimit finds the process's actual memory cgroup from procfs
// and walks toward the controller mount, taking the tightest finite limit. A
// service such as systemd's fanout.service normally lives below the mount root;
// reading only /sys/fs/cgroup/memory.max would inspect the host, not the service.
func detectCgroupMemoryLimit(cgroupPath, mountInfoPath string) uint64 {
	memberships := readCgroupMemberships(cgroupPath)
	mounts := readCgroupMounts(mountInfoPath)
	limit := uint64(0)
	for _, membership := range memberships {
		for _, mount := range mounts {
			if membership.version != mount.version {
				continue
			}
			dir, ok := cgroupDirectory(mount, membership.path)
			if !ok {
				continue
			}
			filename := "memory.max"
			if membership.version == 1 {
				filename = "memory.limit_in_bytes"
			}
			limit = minPositive(limit, readCgroupHierarchyLimit(dir, mount.mountPoint, filename))
		}
	}
	return limit
}

func readCgroupMemberships(filename string) []cgroupMembership {
	raw, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	var memberships []cgroupMembership
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(parts) != 3 || parts[2] == "" {
			continue
		}
		if parts[0] == "0" && parts[1] == "" {
			memberships = append(memberships, cgroupMembership{version: 2, path: parts[2]})
			continue
		}
		for _, controller := range strings.Split(parts[1], ",") {
			if controller == "memory" {
				memberships = append(memberships, cgroupMembership{version: 1, path: parts[2]})
				break
			}
		}
	}
	return memberships
}

func readCgroupMounts(filename string) []cgroupMount {
	raw, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	var mounts []cgroupMount
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		separator := -1
		for i, field := range fields {
			if field == "-" {
				separator = i
				break
			}
		}
		if separator < 0 || separator+3 >= len(fields) || len(fields) < 5 {
			continue
		}
		fsType := fields[separator+1]
		version := 0
		switch fsType {
		case "cgroup2":
			version = 2
		case "cgroup":
			for _, option := range strings.Split(fields[separator+3], ",") {
				if option == "memory" {
					version = 1
					break
				}
			}
		}
		if version == 0 {
			continue
		}
		mounts = append(mounts, cgroupMount{
			version:    version,
			root:       unescapeMountInfoPath(fields[3]),
			mountPoint: unescapeMountInfoPath(fields[4]),
		})
	}
	return mounts
}

func unescapeMountInfoPath(value string) string {
	return strings.NewReplacer(
		`\040`, " ",
		`\011`, "\t",
		`\012`, "\n",
		`\134`, `\`,
	).Replace(value)
}

func cgroupDirectory(mount cgroupMount, membershipPath string) (string, bool) {
	root := path.Clean(mount.root)
	membership := path.Clean(membershipPath)
	var relative string
	switch {
	case root == "/":
		relative = strings.TrimPrefix(membership, "/")
	case membership == root:
		relative = "."
	case strings.HasPrefix(membership, root+"/"):
		relative = strings.TrimPrefix(membership, root+"/")
	default:
		return "", false
	}

	mountPoint := filepath.Clean(mount.mountPoint)
	dir := filepath.Clean(filepath.Join(mountPoint, filepath.FromSlash(relative)))
	if dir != mountPoint && !strings.HasPrefix(dir, mountPoint+string(os.PathSeparator)) {
		return "", false
	}
	return dir, true
}

func readCgroupHierarchyLimit(dir, mountPoint, filename string) uint64 {
	dir = filepath.Clean(dir)
	mountPoint = filepath.Clean(mountPoint)
	limit := uint64(0)
	for {
		if value, ok := readCgroupLimit(filepath.Join(dir, filename)); ok {
			limit = minPositive(limit, value)
		}
		if dir == mountPoint {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir || (parent != mountPoint && !strings.HasPrefix(parent, mountPoint+string(os.PathSeparator))) {
			break
		}
		dir = parent
	}
	return limit
}

func minPositive(values ...uint64) uint64 {
	minimum := uint64(0)
	for _, value := range values {
		if value > 0 && (minimum == 0 || value < minimum) {
			minimum = value
		}
	}
	return minimum
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
