## 1. Measurement Foundation

- [x] 1.1 Add a bounded-label write-gate abstraction with wait/hold histograms and unit tests proving mutual exclusion, lock ordering, and release on failure. Evidence: `internal/lake/writegate/write_gate.go`, `internal/lake/writegate/write_gate_test.go`, `internal/metrics/metrics.go`; `go test -race ./internal/lake/writegate ./internal/lake ./internal/query`.
- [x] 1.2 Route ingest spans/logs/metrics, startup rollup skip-to-latest, service/endpoint/edge rollups, adjacent-file merge, and maintenance through the instrumented gate. Evidence: `internal/lake/writer.go`, `internal/query/duck.go`, `cmd/fanout/main.go`; `go test -race ./internal/lake/writegate ./internal/lake ./internal/query`.
- [x] 1.3 Add bounded merge/maintenance duration and outcome metrics plus rollup lag/watermark/backlog indicators, with tests for success, partial failure, disabled rollups, and zero-work intervals. Evidence: `internal/metrics/metrics.go`, `internal/metrics/metrics_test.go`, `internal/query/duck.go`, `internal/query/duck_test.go`, `internal/query/edge_backlog_test.go`; `go test -race ./internal/metrics ./internal/query ./internal/lake/writegate ./internal/lake`.
- [x] 1.4 Make `cmd/bench` numbers comparable between runs: quantize latency finely enough to resolve the smallest change worth acting on, seed the synthetic workload deterministically, fail runs that produced no usable evidence, and read the new per-operation metrics. Evidence: `cmd/bench/main.go` (latency bounds, `-seed`), `cmd/bench/verdict.go`, `cmd/bench/metrics_report.go`, and their focused tests; `go test -race ./cmd/bench`, `go vet ./cmd/bench`.
- [x] 1.5 Record a secret-free run manifest so a stored report stays interpretable: build/VCS identity, host shape, cgroup memory limit, workload parameters, and a workload hash. Record nothing about server configuration that `bench` cannot observe. Evidence: `cmd/bench/manifest.go`, `cmd/bench/manifest_test.go`; `go test -race ./cmd/bench`.
- [x] 1.6 Accept measurement overhead by construction rather than by screening. Catalog writes are batched (`FLUSH_BATCH_SIZE` defaults to 50,000 rows), so the gate is entered a few times per second, and each entry is a DuckDB transaction doing disk I/O under a process-wide mutex — milliseconds. The instrumentation adds three clock reads and two Prometheus histogram observations per entry, with the observations taken outside the critical section. That is nanoseconds against milliseconds, and it does not need a decision host to bound. The link-time disable switch and its A/B screen were removed: the "disabled" build still constructed, registered, and served every metric vec, so the comparison could not have resolved the question it was built to ask.
- [ ] 1.7 Capture three 30-minute mixed-load baseline runs on the declared Linux host class, keeping manifests and profiles. Record the host class and resource limits; results from any other host class are screening evidence only.

## 2. Write Scheduling

This is the one optimization this change actually intends to attempt. The known
limit is rollup/maintenance contention on the shared catalog write gate; stage 1
exists to measure it.

- [ ] 2.1 From the baseline, document write-gate wait and hold time by operation. Declare one scheduling target and its stop conditions, or record an evidence-backed no-go if contention is not material — a no-go here is a successful outcome and ends the change.
- [ ] 2.2 Implement the single selected candidate, preserving one catalog commit at a time, gate-before-connection ordering, bounded memory, shutdown flushes, and forward progress for ingest, rollups, merge, and maintenance. Extract collaborators only where the experiment requires it; keep DuckDB SQL and `database/sql` inside the query kernel.
- [ ] 2.3 Run three comparable 30-minute trials. Retain the change only if the median declared target improves by at least 10 percent and no guardrail median regresses by more than 5 percent, with no per-run correctness or SLO failure. Otherwise revert it and record the no-go.

## 3. Conditional Follow-Ups

None of these are planned work. Each begins only if stage 2's profiles
attribute material cost to it, and each then needs its own predeclared target,
one changed variable, and the same three-run acceptance gate as 2.3.

- [ ] 3.1 Physical layout (sort order, partition shape, file size, compaction policy). Requires a canonical-spec review first, and a compatibility matrix covering restart, backup/restore, previous-binary readability, raw/typed equivalence, and interrupted maintenance before anything is retained. Never runs against non-disposable data.
- [ ] 3.2 Allocation and marshaling in OTLP normalization, JSON materialization, batch handoff, or row decoding. Ordinary Go copy-reduction work only. Arrow stays out of scope unless profiles attribute at least 15 percent of targeted CPU or allocated bytes to a boundary it can actually remove.
- [ ] 3.3 Rollup tuning or an additional rollup shape, for a named repeated typed query with measured cost. Requires equivalence fixtures for late arrivals, empty windows, wide backlogs, restart, partial failure, and disabled state, plus preserved fallback and provenance. Any change to freshness, provenance, retention, or fallback behavior needs a delta spec before code.

## 4. Close Out

- [ ] 4.1 Run focused and full Go tests, race tests for the touched concurrent paths, `just check`, the production build, and strict OpenSpec validation on whatever was retained.
- [ ] 4.2 Run three final 30-minute mixed-load trials and one two-hour Linux soak on the retained stack, verifying liveness, correctness, the query SLO, bounded storage growth, rollup freshness, and CPU/RSS/allocation.
- [ ] 4.3 Write the decision record: what was retained, what was rejected and why, measured gains and regressions, and remaining risks. Synchronize canonical specs and public docs if any checkpoint produced a behavior change, or record why none was required. Confirm the evidence contains no secrets or generated telemetry, then validate strictly before archive.
