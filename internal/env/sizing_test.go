package env

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDuckDBMemoryLeavesHeadroomForTheGoRuntime(t *testing.T) {
	// The failure this exists to prevent: DuckDB's own default claimed ~6.2GB
	// of a 7.7GB machine, the Go runtime added ~1.3GB on top, and the kernel
	// killed the process at 7.5GB RSS.
	const eightGB = 8 << 30

	got := resolveDuckDBMemory(eightGB)

	if got == "" {
		t.Fatal("a detectable machine must produce a budget")
	}
	budget := parseByteSizeForTest(t, got)
	if budget >= eightGB*8/10 {
		t.Fatalf("budget %s is at or above DuckDB's own 80%%, which is the setting that OOM-killed the process", got)
	}
	if budget < eightGB/4 {
		t.Fatalf("budget %s is so conservative it would cripple a healthy machine", got)
	}
}

func TestResolveDuckDBMemoryScalesWithTheMachine(t *testing.T) {
	small := parseByteSizeForTest(t, resolveDuckDBMemory(4<<30))
	large := parseByteSizeForTest(t, resolveDuckDBMemory(64<<30))

	if large <= small {
		t.Fatalf("budget did not scale: 4GB machine got %d, 64GB machine got %d", small, large)
	}
}

func TestResolveDuckDBMemoryDeclinesWhenMemoryIsUnknown(t *testing.T) {
	// Capping an unknown machine risks pinning a large host to a small number,
	// turning a hypothetical crash into a certain slowdown.
	if got := resolveDuckDBMemory(0); got != "" {
		t.Fatalf("resolveDuckDBMemory(0) = %q, want empty so DuckDB keeps its own behavior", got)
	}
}

func TestResolveDuckDBMemoryDeclinesForAbsurdlySmallMachines(t *testing.T) {
	// A few megabytes is not a real limit; it is a misread. Better to defer.
	if got := resolveDuckDBMemory(16 << 20); got != "" {
		t.Fatalf("resolveDuckDBMemory(16MB) = %q, want empty", got)
	}
}

func TestResolveDuckDBMaxConnsKeepsTheWriteGateInvariant(t *testing.T) {
	// internal/lake refuses to start when DUCKDB_MAX_CONNS > 1 without a write
	// gate; at or below 1 it serializes everything through one handle. The
	// floor is an invariant, not a preference.
	for _, cores := range []int{0, 1, 2} {
		if got := resolveDuckDBMaxConns(cores); got < 2 {
			t.Fatalf("resolveDuckDBMaxConns(%d) = %d, want at least 2", cores, got)
		}
	}
}

func TestResolveDuckDBMaxConnsScalesButIsBounded(t *testing.T) {
	if resolveDuckDBMaxConns(16) <= resolveDuckDBMaxConns(2) {
		t.Fatal("connection pool must grow with cores")
	}
	// Past CPU saturation, more connections add contention rather than
	// concurrency.
	if got := resolveDuckDBMaxConns(512); got > maxAutoDuckDBConns {
		t.Fatalf("resolveDuckDBMaxConns(512) = %d, want <= %d", got, maxAutoDuckDBConns)
	}
}

func TestParseCgroupMemoryLimitTreatsMaxAsUnlimited(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.max")
	if err := os.WriteFile(path, []byte("max\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// "max" means no limit, so this source has nothing to say and detection
	// must fall through rather than reporting zero as a real limit.
	if got, ok := readCgroupLimit(path); ok {
		t.Fatalf("readCgroupLimit(max) = (%d, true), want ok=false", got)
	}
}

func TestParseCgroupMemoryLimitReadsAByteCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.max")
	if err := os.WriteFile(path, []byte("6442450944\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, ok := readCgroupLimit(path)
	if !ok || got != 6442450944 {
		t.Fatalf("readCgroupLimit = (%d, %v), want (6442450944, true)", got, ok)
	}
}

func TestResolveLeavesExplicitValuesAlone(t *testing.T) {
	cfg := Config{DuckDBMemory: "12GB", DuckDBMaxConns: 9}

	cfg.resolveSizing()

	if cfg.DuckDBMemory != "12GB" {
		t.Fatalf("DuckDBMemory = %q, want the operator's 12GB untouched", cfg.DuckDBMemory)
	}
	if cfg.DuckDBMaxConns != 9 {
		t.Fatalf("DuckDBMaxConns = %d, want the operator's 9 untouched", cfg.DuckDBMaxConns)
	}
}

func TestResolveFillsUnsetValues(t *testing.T) {
	cfg := Config{DuckDBMaxConns: 0}

	cfg.resolveSizing()

	if cfg.DuckDBMaxConns < 2 {
		t.Fatalf("DuckDBMaxConns = %d, want an auto-sized pool of at least 2", cfg.DuckDBMaxConns)
	}
}

// parseByteSizeForTest reads back the "<n>MB" form resolveDuckDBMemory emits.
func parseByteSizeForTest(t *testing.T, value string) uint64 {
	t.Helper()
	var mb uint64
	if _, err := fmt.Sscanf(value, "%dMB", &mb); err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return mb << 20
}
