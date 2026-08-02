## 1. Measurement Foundation

- [x] 1.1 Add a bounded-label write-gate abstraction with wait/hold histograms and unit tests proving mutual exclusion, lock ordering, and release on failure. Evidence: `internal/lake/writegate/write_gate.go`, `internal/lake/writegate/write_gate_test.go`, `internal/metrics/metrics.go`; `go test -race ./internal/lake/writegate ./internal/lake ./internal/query`.
- [x] 1.2 Route ingest spans/logs/metrics, startup rollup skip-to-latest, service/endpoint/edge rollups, adjacent-file merge, and maintenance through the instrumented gate; add a test-backed inventory showing that every DuckLake catalog-writing path is covered. Evidence: `internal/lake/writegate/inventory_test.go`, `internal/lake/writer.go`, `internal/query/duck.go`, `cmd/fanout/main.go`; `go test -race ./internal/lake/writegate ./internal/lake ./internal/query`.
- [x] 1.3 Add bounded merge/maintenance duration and outcome metrics plus rollup lag/watermark/backlog indicators, with tests for success, partial failure, disabled rollups, and zero-work intervals. Evidence: `internal/metrics/metrics.go`, `internal/metrics/metrics_test.go`, `internal/query/duck.go`, `internal/query/duck_test.go`, `internal/query/edge_backlog_test.go`; `go test -race ./internal/metrics ./internal/query ./internal/lake/writegate ./internal/lake`.
- [x] 1.4 Extend `cmd/bench` reporting with a secret-free reproducibility manifest, operation-level write/background distributions, CPU/RSS/allocation/storage summaries, and deterministic JSON tests. Evidence: `cmd/bench/manifest.go`, `cmd/bench/metrics_report.go`, `cmd/bench/main_test.go`, `cmd/bench/manifest_test.go`, `cmd/bench/metrics_report_test.go`; `go test -race ./cmd/bench`, `go vet ./cmd/bench`, `go build ./cmd/bench`, modified-script `bash -n`, and `just --list`.
- [x] 1.5 Add a benchmark orchestration/analysis command that captures warm-up separately, runs three comparable trials, normalizes metric direction, applies the 10-percent target and 5-percent guardrail rules, and preserves per-run zero-tolerance failures in machine-readable evidence. Evidence: `cmd/bench/orchestrate.go`, `cmd/bench/analysis.go`, `cmd/bench/verdict.go`, their focused tests, and manifest/config validation; `go test -race ./cmd/bench`, `go vet ./cmd/bench`, `go run ./cmd/bench orchestrate -h`, and `go run ./cmd/bench analyze -h`.
- [ ] 1.6 Run focused tests, race tests for the touched concurrent paths, full Go tests, `just check`, and strict OpenSpec validation; record the evidence and confirm measurement-only behavior plus at most five-percent screening overhead. Local code validation passes. Darwin null screening exceeded the noise budget, and the first Linux screen mixed HEAD with unrelated worktree changes, so the accepted screen must compare link-time disabled/enabled binaries from one recorded source archive on the isolated Linux decision host. Evidence: `evidence/measurement-screening.md`.
- [ ] 1.7 Capture and record three accepted 30-minute mixed-load baseline runs on the declared production-shaped Linux host class, including manifests, profiles, target-independent guardrails, and dataset identity.

## 2. Internal Boundaries and Write Scheduling

- [ ] 2.1 Use the accepted baseline to document write-gate wait/hold time by operation, declare one scheduling target and its stop conditions, or record an evidence-backed no-go if contention is not material.
- [ ] 2.2 Extract only the concrete write/maintenance/rollup collaborators required by the selected experiment while keeping DuckDB SQL and `database/sql` inside the query kernel and typed contracts above it; add compatibility tests for the existing facade.
- [ ] 2.3 Implement the single selected scheduling candidate while preserving one catalog commit, gate-before-connection ordering, bounded memory, shutdown flushes, and progress for ingest, rollups, merge, and maintenance.
- [ ] 2.4 Run three comparable 30-minute candidate trials and retain the scheduling change only if its declared target improves by at least 10 percent with no guardrail median regressing over 5 percent; otherwise revert the candidate and record the no-go evidence.

## 3. Physical Layout Experiments

- [ ] 3.1 Review `telemetry-storage-query`, `telemetry-ingestion`, and `operations` against the proposed layout candidate; create a delta spec before code if restart, backup/restore, downgrade, logical behavior, or operator-visible configuration would change.
- [ ] 3.2 From query, file-growth, merge, and maintenance profiles, declare exactly one layout variable and one primary target, or record an evidence-backed no-go when no material layout bottleneck exists.
- [ ] 3.3 Build a disposable-data compatibility harness covering current/candidate binary reads, restart, backup/restore, previous-binary rollback, raw/typed equivalence, and interrupted maintenance recovery.
- [ ] 3.4 Implement and benchmark only the declared sort, partition, file-size, or compaction candidate against an identical logical dataset; do not add an automatic startup rewrite.
- [ ] 3.5 Retain the physical-layout candidate only after the compatibility matrix and three-run acceptance gate pass; otherwise revert it and record the failed matrix or performance evidence.

## 4. Allocation and Marshaling

- [ ] 4.1 Capture CPU, heap, allocation, mutex, and blocking profiles under the accepted mixed workload and identify one attributable allocation/marshaling target, or record a no-go if none is material.
- [ ] 4.2 Implement the smallest Go/DuckDB buffer-ownership, copy-reduction, row-scan, or marshaling candidate and add race, lifetime, and content-equivalence tests.
- [ ] 4.3 Run three comparable candidate trials and retain the ordinary Go optimization only if the declared target and all guardrails pass; otherwise revert it and record the evidence.
- [ ] 4.4 Evaluate Arrow only if profiles attribute at least 15 percent of targeted CPU or allocated bytes to a removable interchange boundary and simpler changes cannot address it; if eligible, prototype it internally and retain it only after its own acceptance gate, binary-size record, and single-binary build verification.

## 5. Rollup Evaluation

- [ ] 5.1 Profile named repeated typed queries and review `telemetry-storage-query` plus the consumer capability spec; create a delta spec before code if freshness, provenance, fallback, retention, or result behavior would change.
- [ ] 5.2 Declare one existing-rollup tuning or additional-rollup candidate with a primary target, or record an evidence-backed no-go if repeated query cost is not material.
- [ ] 5.3 Implement the selected candidate with fixtures for late arrivals, empty windows, wide backlogs, restart, partial failure, disabled/not-ready state, raw equivalence, fallback, and provenance.
- [ ] 5.4 Run three comparable candidate trials and retain the rollup change only when the declared target, freshness/correctness requirements, and all guardrails pass; otherwise revert it and record the evidence.

## 6. Final Validation and Handoff

- [ ] 6.1 Run focused and full Go tests, race tests, `just check`, the production build, vulnerability checks, and strict OpenSpec validation on the fully retained stack; attach command/result evidence.
- [ ] 6.2 Run three final 30-minute mixed-load trials and one two-hour production-shaped Linux soak, verifying liveness, zero-tolerance correctness, query SLO, bounded storage growth, rollup freshness, CPU/RSS/allocation, and single-binary artifact size.
- [ ] 6.3 Synchronize canonical specs and public/operator documentation for any checkpointed behavior change, or record why no synchronization was required; ensure evidence contains no secrets or generated telemetry data.
- [ ] 6.4 Write the final decision record listing retained and rejected candidates, measured gains and regressions, compatibility results, deployment/rollback instructions, and remaining risks, then complete strict OpenSpec validation before archive.
