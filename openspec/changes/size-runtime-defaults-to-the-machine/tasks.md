## 1. Make resolution observable

- [x] 1.1 Log the resolved runtime configuration once at startup, distinguishing values the machine chose from values the operator pinned. Evidence: `logResolvedSizing` in `internal/env/sizing.go`, emitting `duckdb_memory`, `duckdb_memory_auto`, `duckdb_max_conns`, `duckdb_max_conns_auto`, `detected_memory_bytes`, and `gomaxprocs`.
- [x] 1.2 Return the same non-secret effective values from health diagnostics. Evidence: `HealthResponse.runtime_sizing` and `TestReadiness_HealthyDuckLakeAndRollups`.

## 2. Size the DuckDB memory budget

- [x] 2.1 Detect available memory cgroup-first, treating the unlimited sentinel as "not this source". Evidence: `detectAvailableMemory`, `readCgroupLimit`; verified against a real container, where `docker run --memory 6g` reports `6442450944` at `/sys/fs/cgroup/memory.max`.
- [x] 2.2 Resolve `DUCKDB_MEMORY` from detected memory with headroom for the Go runtime, only when unset. Evidence: `resolveDuckDBMemory` at 60%; the 6 GB container resolves to 3686 MB against the previous 5.2 GB, which with ~1.3 GB of Go runtime exceeded the limit.
- [x] 2.3 Detect host memory on Linux and macOS after considering the process cgroup, while leaving the value empty and warning on unsupported or unreadable platforms. Evidence: `host_memory_linux.go`, `host_memory_darwin.go`, and `TestDetectHostMemoryFindsSupportedHosts`.
- [x] 2.4 Cover detection and resolution with tests, including nested cgroups, ancestor limits, the unlimited sentinel, supported host detection, and the undetectable case. Evidence: `internal/env/sizing_test.go`.

## 3. Scale the connection pool

- [x] 3.1 Resolve `DUCKDB_MAX_CONNS` from cores when set to 0, bounded below by the write-gate invariant and above by the point where connections stop adding concurrency. Evidence: `resolveDuckDBMaxConns`, floor `minDuckDBConns=2`, ceiling `maxAutoDuckDBConns=16`.
- [x] 3.2 Verify the write gate invariant still holds at the floor. Evidence: `TestResolveDuckDBMaxConnsKeepsTheWriteGateInvariant`; `internal/lake` rejects `DUCKDB_MAX_CONNS > 1` without a gate, and the floor keeps the resolved value above 1.

## 4. Verify

- [x] 4.1 Confirm an explicitly set variable always wins over a resolved one. Evidence: `TestResolveLeavesExplicitValuesAlone`.
- [x] 4.2 Run `just check`.
- [x] 4.3 Note in `docs/benchmarks/two-vcpu.md` that the manually pinned `DUCKDB_MEMORY=3GB` is close to what the default now resolves to on that machine, so the published numbers still stand.
- [x] 4.4 Document DuckDB configuration as advanced overrides rather than normal deployment requirements. Evidence: README `Advanced DuckDB sizing`.
