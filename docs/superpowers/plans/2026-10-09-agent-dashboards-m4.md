# Agent dashboards — milestone 4 (Performance and finish) implementation plan

> **For the implementer:** Work through the checkboxes in order. Write the behavior test, observe its failure, implement the smallest correction, and observe its pass. Codex edits files; the controller reviews, commits and runs browsers. This document is a plan, not execution evidence.

**Goal:** Close the remaining correctness, backlog robustness, performance, accessibility, evaluation and documentation gaps. Establish measured S1–S13 evidence for the final candidate, including the two in-scope capability-preview boards.

**Architecture:** Preserve the existing panel compiler/executor and snapshot-bound native DuckDB reads. Reuse completed-batch trace candidates where they answer a structured query exactly. Keep analytical service/edge watermarks separate from completed-batch acknowledgements. Dashboard and chat rendering share `ui/panels` and `ui/host/src/dashboards`; the single MCP app remains `internal/mcp/apps/panels.html`. The existing disposable e2e server supplies deterministic telemetry for CI; real-data verification uses a controller-owned local replay.

**Tech Stack:** Go 1.27.1, pinned DuckDB 2 through `scripts/with-duckdb.sh`, modernc SQLite/database/sql, Goose/sqlc, React 19.3, Mantine 9.6.3, ECharts 6.1, TanStack Query/Router/Table, Bun 1.4.2, Vitest 5.0.3 and Playwright 1.63.0. Proposed test-only addition: exact pin `axe-core@4.10.3`; install availability is a controller preflight, not permission to substitute an unpinned version.

**Spec and inputs:** `docs/superpowers/specs/2026-10-04-agent-dashboards-design.md`; binding `.superpowers/sdd/2026-10-09-agent-dashboards-m4/plan-brief.md` and `codex-rules.md`; format precedent `docs/superpowers/plans/2026-10-08-agent-dashboards-m3.md`; measured history `docs/benchmarks/2026-10-agent-dashboards-m{1,2,3}.md`. Surveyed branch `feat/agent-dashboards-m4`, based on merged M3. Existing names below were observed in this tree. **Create** paths and interfaces explicitly called **new** are proposed additions. Hypotheses are labelled; no timings, CI ceilings or reproduction outcomes are invented.

## Global Constraints

- No release, deploy, push or deployed-host access. Production and demo remain untouched. **Reseeding the deployed demo is out of scope.** Local disposable generation/replay is verification setup. The preview error-budget board remains out of scope because it requires SLO data.
- Follow the dispatch rules. No git writes, questions, browser tools, MCP calls or CodeGraph initialization by Codex. Read with plain `rg`, `sed -n`, `cat`. A denied action is BLOCKED with the exact action and reason; do not try another access path. Each implementation dispatch writes `.superpowers/sdd/2026-10-09-agent-dashboards-m4/task-N-report.md` with files, red/green steps, test names, exit codes and deviations. These reports are future task outputs, not additional outputs of this planning task.
- Before any Go command, from the repository root, use the following environment. No bare `go test`; `just test` invokes the pinned engine wrapper. Alternation is quoted through Just exactly as shown below.

  ```bash
  rtk mkdir -p .superpowers/gocache .superpowers/gotmp
  export GOCACHE="$PWD/.superpowers/gocache"
  export GOTMPDIR="$PWD/.superpowers/gotmp"
  export GOFLAGS=-mod=readonly
  export GOPROXY=off
  ```

- Execution commands use RTK. Plain code reads follow the explicit dispatch-rule exception. Every implementation dispatch runs `rtk just fmt` and the individual full gates below, recording **every exit code**, including lint's issue count if its cache warns. Do not use aggregate `just check` in the Codex sandbox: its site build needs `npm ci`.

  ```bash
  rtk just fmt-check
  rtk just ui-boundaries-check
  rtk just lint
  rtk just test ./internal/... ./cmd/...
  rtk proxy sh -c 'cd ui/host && bun run test && bun run lint'
  rtk just ui-deadcode
  rtk just go-deadcode
  rtk just script-tests
  rtk just test-names-check
  rtk just dashboard-eval-generate-check
  rtk just docs-generate-check
  ```

- UI dispatches run host tests, host lint, then `rtk just ui`; include both generated embed trees in their controller review. Contract changes regenerate references with `rtk just docs-generate`; never hand-edit generated reference pages or `scripts/dashboard-eval/testdata/server.json`. Use `dashboard-eval-generate` if server goldens change.
- Every SQL-producing change has a **native-engine executed** test on format-3 Parquet, not only a SQL-string assertion or fake Engine. Retain UTC connections, `TIMESTAMPTZ_NS` predicates and `?::TIMESTAMP_NS::TIMESTAMPTZ_NS` binds, signed footer bounds, literal dotted attribute keys, typed attributes, no schema unioning/Hive inference and no engine-pin change.
- No new cache, cache table, individual-event cache or resurrected endpoint/log accelerator. Cache markers and rows share a read transaction under the pinned file snapshot; uncached active files are immediately visible. Compaction/retention/late publication cannot double count or disappear. Operational errors must propagate; they do not trigger a second raw attempt. The only routing alternative is **not covered → compile/run raw**.
- No general scheduler, retry framework, new UI or new configuration knob for these defects. Prefer a clamp, one existing engine option, or reverting a measured regression. Keep memory-governed asynchronous I/O defaults. Memory tuning requires recorded measurements before and after.
- No control-schema changes are anticipated. If evidence requires one, use a new timestamped Goose migration, extend immutable checksums, run `just db-gen` and storage tests; do not introduce PostgreSQL, another schema copy or migration system.
- Agent models: `claude-sonnet-5-5` and `gpt-6.1-sol`; never Haiku. Paid work is controller-run, with runtime-injected keys and **at most $5 total per task**, including retries, judges and parity prompts. Preserve the existing per-call ledger, unknown-usage stop and p95/$0.60 admission gate; a ledger cap is not a hard provider-side token cap. Do not begin a call that cannot fit a conservative completion reserve. The controller must obtain authorization before more spend; no automatic budget reset.
- Never read the sealed holdout, its run-level artifacts or individual failures to plan or tune. Reading the private runner source is permitted. Only the controller runs a frozen candidate on sealed inputs; keep holdout text/specs/answers/judge evidence outside the worktree in a controller-only location. Public output contains aggregates, IDs/hashes and skip/incomplete reasons.
- Public plans/docs use repository-relative paths and environment placeholders; no host details, IPs, local home-directory paths, credentials or raw telemetry. No compatibility aliases/shims: delete superseded code in the same task. Tests describe behavior and pass `just test-names-check`.
- Verify through tests, smoke, the controller's collector and real-data checks. No scripted tours. A fixture, screenshot capture or collector exit 0 alone is not a criterion PASS.

## Recorded rulings and scope decisions

1. **Retirement is verification only.** M3 deleted the old HTTP/widget contracts. `internal/api/traces_test.go:TestTraceRouteRejectsInvalidScopesAndRetiredPaths` already checks all six retired observability reads return 404. Keep active `internal/observability` services: health, map, trace and dependency adapters still use them. Do not delete that package merely to remove its name.
2. **Routing coverage is narrower than the historical spec suggests.** `internal/query/batch_cache.go` has `readCacheVersion=4` and only `read_batches`, `read_trace_candidates`, `read_trace_parts`. `queryrows.ReadKind` has RawRead and TraceCandidateRead. M3 deliberately removed endpoint/log histograms and minute/tail sources. `service_rollup` has approximate latency, weighted error rates, excluded empty services and analytical watermark lag; it has no per-active-batch acknowledgement. Its `metric_count` counts distinct metric names per minute, not metric points. It cannot establish exact raw count/rate/quantile equivalence for arbitrary windows. Do not route those measures or rebuild their retired caches. Initial exact coverage is the existing trace-candidate relation for unbucketed, ungrouped `count_distinct(trace_id)` with an explicit nonempty-ID predicate and supported namespace/service equalities. This is an intentional, evidence-based scope ruling, not a claim that broad aggregation routing is done. S7 still measures realistic raw and mixed panels; insufficient speed remains FAIL.
3. **Do not invent a minute cache.** Exact clipped boundaries are already handled by `aggregateSources` using footer-pruned raw files and complete in-window parts. Reuse it for trace counts. If a future cache offers minute aggregates, the same clipped-minute rule would apply; there is no such exact minute relation in this branch.
4. **Error visibility has two separate losses.** `failed` truncates the already path-redacted executor message at 500 bytes; `PanelCard` clamps the preview and `PanelData` has no error detail. MCP `saved` already puts `r.Error` in `savedPanelCheck.Error`. The checked-in eval `executePanels` discards it. Fix the actual losses; do not add another receipt or error endpoint.
5. **CI and local S6 targets are distinct.** Keep the spec's ≤1.5 s local p95 verbatim. Choose a checked-in CI ceiling only after measured runner calibration, state the margin, and report both results. A looser CI ceiling does not turn a local S6 failure into a PASS.
6. **Annotations use the shipped method.** `ui/host/src/dashboards/refresh.ts` uses the current POST annotation contract; the spec's old GET row does not authorize an API change. One annotations request per refresh is still the S8 target.
7. **Accessibility measures six slots.** The old spec prose says four categorical slots; current validated rendering has six ordered slots and a separate Other color. Validate all six in both themes. Exact arrays in `series-palette.test.ts` prove identity, not CVD separation. No `validate_palette.js` is present in the surveyed tooling; add a checked-in validator/test instead of claiming the pinned arrays already prove S10.
8. **Holdout is not ten dashboard prompts.** The private `eval2.ts` supports dashboard/answer intent; checked-in `main.ts` and `score.ts` still require ten prompts and five edits for every set. Keep benchmark S1–S5 unchanged; give holdout its own intent denominators and mark S5 not applicable. Quality judging is optional and skipped unless both judge keys exist. This machine lacks `JUDGE_OPENAI_KEY`; mock the two-model path and record the live quality score as unmeasured.
9. **Evidence-dependent memory correction.** A spill directory is already configured: `NewDuck` creates `cfg.QueryTempDir()` and `openDuckDB` sets `temp_directory` once per instance. Version passes already cap 64 batches/64,000 rows, retry oversized batches singly and start the query deadline after gate acquisition. Adding another temp directory or the same batch cap would not fix the observed defect. Task 2 must locate the dominant statement/wait before Task 3 selects a patch.

## Review Focus

1. Audit evidence and CI guards independently. M1 warm timings, M2 screenshots and M3 benchmark results are history, not measurements of the M4 candidate.
2. Backlog diagnosis must distinguish tracked memory, RSS/untracked allocation, spill, SQL execution and gate wait. Smaller ingest windows must not skip equal ingestion timestamps or late publications; bounded work must commit progress without incomplete markers.
3. Qualified-log AST changes must preserve aliases, lexical scopes, correlated references, redaction and the approved-relation boundary. No SQL-name string replacements.
4. Routing coverage is checked before execution for **every** measure/filter/dimension/bucket. Routed SQL uses the existing snapshot binder; failures are visible. Current, shifted, stat total and trend subqueries each make their own coverage decision.
5. Performance counts actual completed requests and real painted panels. No mocked response timing, intercepted IntersectionObserver, hidden panel IDs, retries removed from latency or extra cache warming included in an alleged cold sample.
6. Axe cannot read canvas text. Supplement browser AA scans with compiler/token contrast tests and controller screenshots; do not waive inconclusive contrast. Judge skips and unknown costs must remain explicit.

## Task 1: Audit S1–S13 and verify the M3 retirement

**Files:** Create `docs/benchmarks/2026-10-agent-dashboards-m4.md`; read the spec, three benchmark docs, `internal/api/traces_test.go`, `cmd/fanout-docgen/routes_test.go`, `internal/panel/*_test.go`, host dashboard tests, `scripts/dashboard-eval`, `.github/workflows/ci.yml`, `justfile`. No product edits. **Budget: $0.**

- [ ] **Step 1: Record this baseline audit, with exact guard names at dispatch**

| Criterion | Current evidence observed | CI today | Gap and owning task |
|---|---|---|---|
| S1: 10/10 saves | M3 final 10/10; `scripts/dashboard-eval/main.test.ts` ten-save loopback; `score.ts:score` | Mock only via script-tests | Fresh controller ten-prompt run, Task 11; intent runner Task 9 |
| S2: 100% good/explained | M3 67/67; `panelPass`, MCP `saved`, engine preview/executor tests | Deterministic tests, no model run | Backlog OOM and lost check messages, Tasks 2–4; fresh save checks Task 11 |
| S3: 0 final invalid specs | M3 0; `Check`, `Validate`, `score` require checked validation | Go + mock tests | Qualified logs fail closed, Task 5; fresh final validation Task 11 |
| S4: median ≤45 s | M3 final 18.7 s; runner measures scored save observation | Mock scoring, no provider | Fresh default-model measurement Task 11; no prompt tuning planned |
| S5: five localized edits | M3 5/5; `compareEdit`, main/mock tests | Yes, deterministic | No feature gap; retain guard and repeat chain in Task 11 |
| S6: 12-panel/24h ≤1.5 s p95 | M1 397 ms over six loads; current `e2e/smoke.spec.ts` checks geometry, not timing | Smoke yes; performance no | Routed-path test, measured runner ceiling, Task 7; local real-data Task 11 |
| S7: warm query ≤500 ms p95 | M1 121 ms sample; `observability/batch_reads_bench_test.go` is readbench-tagged and measures Trace, not panel executor | No panel benchmark | Exact coverage/routing and Go panel benchmark Task 6; demo run Task 11 |
| S8: 1 batch +1 annotations; variable options once/range change | M2 3 cycles/theme; host `use-panel-results.test.tsx` named S8 20-panel test and variable tests | Unit guards yes; browser counts no | Real Playwright network guard incl. variables/background/scroll Task 7 |
| S9: 15 types | `internal/panel/validate_test.go:TestVizOrder`; `grid.test.tsx` all types; smoke fixture | Yes, Go/UI/smoke | Already proven and guarded; no feature work; screenshots Task 8 |
| S10: AA + CVD both themes | theme/row-formatting/chart-units contrast tests; palette arrays pinned; M3 collector 30/30 | Token tests yes; axe/CVD no | Real both-surface scan + six-slot validator + screenshots Task 8 |
| S11: shared view state | `router.test.ts` full-screen/compare/drill link; `search.test.ts` All/None/empty/single/multi | Yes | Already guarded incl. M3 empty-selection fix; Task 11 checks live URLs |
| S12: #232 items 1,9,12,14,17,24,30 | See item map below | Go/UI guards; partial smoke | Keep guards, label item provenance without renaming to milestone names; Tasks 4–7 extend affected behavior |
| S13: preview parity | M2 capability table; M3 type/chat collector; two full boards not rebuilt | No live/paid parity | Verify retirement here; documentation Task 10; controller two-board review Task 11 |

S12 map: **1** whole-window stat/gauge totals in `exec.go:totals`, `frame_semantics_test.go:TestGaugeTotalsMatchStat`, headline tests; **9** empty diagnosis tests `TestDiagnosisNamesOnlyEmptyingRouteFilter` and `TestDiagnosisBoundsReruns`; **12** `page.test.tsx` missing-dashboard test; **14** `TestCompileMeasureUnitsOverridePanelUnit`, `chart-units.test.ts` unit-axis guard; **17** `bucket.go:AutoInterval`, `compile_test.go`, `heat_density_test.go:TestWidthAwareCellBuckets`, distribution/series caps; **24** dashboard layout and host `layout.test.ts`, `grid-row-bands.test.ts`; **30** `use-panel-results.test.tsx` known-empty/scroll/in-flight tests and `use-variables.test.ts`. Validate these item associations against the spec's cited issue description, retain real behavior names, and explicitly identify any narrower existing test rather than claiming a new issue regression ran.

- [ ] **Step 2: Verify retirement without repeating deletion**

```bash
rg -n '/api/observability|widgets/' cmd internal/api internal/mcp ui/host/src scripts/dashboard-eval site/src/content/docs/reference
rg --files ui/host/src internal/api internal/mcp/apps
rtk just test ./internal/api -run TestTraceRouteRejectsInvalidScopesAndRetiredPaths -count=1
rtk just docs-generate-check
rtk just ui-deadcode
rtk just go-deadcode
```

Classify rejection tests and historical documentation as such. Confirm no registration, auth classification, generated public route, old widget renderer/decoder or retired app output exists; `panels.html` is the only app. Record verified paths/guard exit codes. An actual unexpected live contract is an audit failure for correction with a behavior regression, not authorization to remove active observability services.

- [ ] **Step 3: Start the evidence document**

Write S1–S13 rows as **PENDING** for the M4 candidate with baseline references, audit results, task owners and required measurement. Do not copy M3 PASS into M4. Every following task names its rows above; no unrelated polish, layout rewrite, release work or demo maintenance.

## Task 2: Reproduce the 4 GB small-batch backlog before choosing a fix

**Files:** Create `internal/query/backlog_pressure_test.go` and `internal/panel/backlog_pressure_test.go` under build tag `readbench`; modify test-only diagnostic wrapping in `internal/query/duck.go` / `annotations.go` only if statement attribution cannot otherwise be obtained, and only after a failing error-attribution test. Private measurements under `.superpowers/sdd/2026-10-09-agent-dashboards-m4/`; aggregate method/results in the M4 benchmark doc. **Budget: $0.** Closes S2/S6/S7 robustness gap.

- [ ] **Step 1: Generate a bounded reproduction, record the failure, do not fix yet**

Use native format-3 `telemetrystore.Open`, `Repository.Commit`, `query.NewDuck` as in `internal/observability/batch_reads_bench_test.go`. Seed **4096 immutable batches**, **512 spans and 192 logs per batch**, 24-hour event-time spread, six services, versions v1/v2 and a shared recent ingest interval. Seed before opening the engine so startup sees a real backlog. Use distinct IDs; do not materialize the entire backlog in Go. This scale is a **hypothesis drawn from M3's several-thousand-file backlog**, not a promised reproducer. Sweep 1024/4096/8192 batches in separate processes if necessary; preserve all attempts, including “not reproduced”. A finite 10-minute test deadline and explicit failure/inconclusive outcome prevent a hanging probe.

The complete fixture core below belongs in `package query` in `backlog_pressure_test.go`, with `//go:build readbench` and imports context, fmt, testing, time, config, telemetry and telemetrystore. It uses real production construction and bounded per-batch allocations; `pressureDuck` is **new test-only**.

```go
func pressureDuck(t *testing.T, batches int) (*Duck, time.Time) {
    t.Helper()
    base := time.Now().UTC().Truncate(time.Minute)
    cfg := config.Config{DataDir: t.TempDir(), RetentionDays: 30,
        DuckDBMemory: "4GB", DuckDBThreads: 4, DuckDBMaxConns: 4,
        RollupInterval: time.Second}
    repo, err := telemetrystore.Open(cfg.TelemetryDir())
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { _ = repo.Close() })
    for batch := range batches {
        b := telemetrystore.Batch{ID: fmt.Sprintf("pressure-%06d", batch)}
        for i := range 512 {
            n := base.Add(-24*time.Hour + time.Duration((batch*512+i)%86400)*time.Second).UnixNano()
            ingested := base.Add(time.Duration(batch)*time.Millisecond).UnixNano()
            version := "v1"
            if i%2 == 0 { version = "v2" }
            service := fmt.Sprintf("svc%d", i%6)
            trace := fmt.Sprintf("trace-%d-%d", batch, i/4)
            b.Spans = append(b.Spans, telemetry.Span{Namespace: "pressure",
                ServiceName: service, TraceID: trace, SpanID: fmt.Sprintf("s%d", i),
                StartUnixNanos: n, EndUnixNanos: n+int64(i%1000+1)*1000000,
                DurationMS: float64(i%1000+1), Kind: "SPAN_KIND_SERVER",
                StatusCode: "STATUS_CODE_OK", ServiceVersion: version, IngestedAt: ingested})
            if i < 192 {
                b.Logs = append(b.Logs, telemetry.Log{Namespace: "pressure",
                    ServiceName: service, TraceID: trace, EventUnixNanos: n,
                    TimeUnixNanos: n, IngestedAt: ingested, Body: "request complete", Severity: "INFO"})
            }
        }
        if err := repo.Commit(t.Context(), b); err != nil { t.Fatal(err) }
    }
    d, err := NewDuck(t.Context(), cfg, repo)
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { _ = d.Close() })
    return d, base
}

func TestBacklogPressureRecordsConcurrentStatementFailures(t *testing.T) {
    d, base := pressureDuck(t, 4096)
    ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
    defer cancel()
    started := make(chan struct{})
    done := make(chan error, 1)
    go func() { close(started); _, err := d.refreshServiceRollup(ctx); done <- err }()
    <-started
    panels := make(chan error, 4)
    for range 4 {
        go func() {
            rows, err := d.QueryContext(ctx, `SELECT service,quantile_cont(duration_ms,0.95)
                FROM spans WHERE start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS
                AND start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS GROUP BY service`,
                base.Add(-24*time.Hour), base.Add(time.Minute))
            if err == nil {
                for rows.Next() { var service string; var latency float64; err = rows.Scan(&service,&latency); if err != nil { break } }
                if err == nil { err = rows.Err() }
                _ = rows.Close()
            }
            panels <- err
        }()
    }
    for range 4 { t.Logf("statement=panel_latency error=%v", <-panels) }
    t.Logf("statement=service_rollup error=%v", <-done)
    if ctx.Err() != nil { t.Fatal(ctx.Err()) }
}
```

This diagnostic probe logs outcomes; it is **not the regression test** and cannot produce a robustness PASS. Set a queryrows.Window on the panel probe when measuring footer-pruned behavior (add queryrows import); measure both the unscoped analytical statements and the scoped executor. Reproduce the panel side separately in `package panel` with `NewExecutor(d,30).Run`, real table/count/latency specs and four concurrent requests; panel failures are Results with `status:error`, not necessarily a top-level error. Use the same generator parameters, clock bounds and absolute dashboard Time. Do not import panel into package query and create an import cycle.

- [ ] **Step 2: Attribute every error and wait**

Run service rollup alone, panels alone, then together; add concurrent `RefreshVersionRollup` and `RecordAnomalies` with one `annotations.Anomaly`. Record their exact returned redaction-safe errors and start/end times. Wrap each existing `ExecContext` failure with a fixed statement label if required (`service_rollup/delete`, `service_rollup/insert`, `version_rollup/insert`, `anomaly_log/bounds`, `anomaly_log/insert`), preserving `%w`. Never log SQL arguments or file paths to the public report. Use `errors.As` for DuckDB error type and `errors.Is` for cancellation/deadline; do not infer OOM from a slow request.

Collect `SELECT version()`, `current_setting('memory_limit')`, `current_setting('threads')`, redacted temp-directory presence, `duckdb_memory()` by tag, `duckdb_temporary_files()` total bytes, process RSS using the existing `processRSSMiB` methodology, read-pool stats, write-gate waiting time, source watermarks and active/cached batch counts. Sample diagnostics on a separate connection without serializing the workload inadvertently. Record peak RSS separately from total allocations. Add file count/rows/bytes, CPU/platform, native engine pin and whether storage spilled. Keep exact unredacted filesystem diagnostics private to the controller if needed.

```bash
rtk just test ./internal/query -tags=readbench -run TestBacklogPressureRecordsConcurrentStatementFailures -count=1 -timeout=12m -v
rtk just test ./internal/panel -tags=readbench -run TestPanelsRemainUsableDuringBacklog -count=1 -timeout=12m -v
```

The second command targets the **new failing behavior regression** written after the diagnostic data fixes its workload parameters. It asserts no OOM Results, bounded version/anomaly progress and exact counts after drain. It must fail on the uncorrected candidate with the observed defect; do not replace it with `t.Skip` when the expected failure is hard to reproduce. Resource-intensive probes remain opt-in; a reduced deterministic progress/correctness fixture is required in the normal CI suite in Task 3.

- [ ] **Step 3: Freeze a diagnosis for Task 3**

Report exact failing statement(s), solo/concurrent differences, whether spill already works, whether timeouts are gate wait or execution, and the minimum failing workload. “4096 batches should fail” is not evidence. If the finite sweep does not reproduce, report INCONCLUSIVE and retain the real-data controller reproduction as a required gate; do not claim a fix or change engine settings speculatively.

## Task 3: Apply one measured backlog correction and prove eventual exact progress

**Files:** Modify only the diagnosed owner: `internal/query/duck.go`, `internal/query/annotations.go` if gate contention is proven, or `internal/panel/exec.go` if panel concurrency is proven dominant. Extend `internal/query/rollup_watermark_test.go`, `rollup_yield_test.go`, `version_gate_test.go`, `annotation_maintenance_test.go` and the new pressure regressions as applicable. Record measurements in the M4 benchmark doc. **Budget: $0.** Depends on Task 2; closes S2/S6/S7.

- [ ] **Step 1: Select the smallest supported change, with a failing behavior test**

| Observation from Task 2 | Allowed first correction | Required negative/correctness case |
|---|---|---|
| Service affected-set work is huge because one pass covers a broad ingest interval; smaller interval measurably reduces peak | Clamp only the service rollup's existing `rollupWindow` result; retain idempotent delete/reaggregate | Equal-ingest bursts, old event times, late commit inside lag, plateau and cancellation do not skip rows |
| Memory rises primarily with engine workers and spill does not cover their state | One measured reduction of existing threads setting for an explicitly capped instance, or revert an identified worker-setting regression | Unset/default memory sizing remains unchanged; no read-ahead setting; report S7 tradeoff |
| Concurrent panel pressure alone tips otherwise successful rollup over the cap | Reduce existing `Executor.parallel` to the measured safe value, without a new scheduler | Batch/per-panel timeouts include queueing; partial results and cancellation still work; report S6 tradeoff |
| File-count pressure, including equal-ingest batches, dominates regardless of time clamp | Bound the existing affected-source batch selection for each service pass using captured metadata; recompute complete affected buckets | Never advance a watermark past an unprocessed batch or partially aggregate a bucket; oversized/equal-stamp cases must progress |
| Annotation deadlines expire waiting for an oversized analytical pass | Bound that pass/yield at a committed point | Do not extend 10-second annotation or version execution deadlines to conceal contention |

These are conditional alternatives, **not a task to implement them all**. The batch-selection alternative requires exact engine tests before use and is more work than a constant clamp; use it only if measurement eliminates simpler choices. Do not use the publication gate's exclusive writer lock as a “read gate”: `parquetReadGate` protects file replacement, and blocking all reads there for a 20-second analytical pass would worsen S6. There is no general rollup/read serialization gate in this tree to casually reuse.

For the first alternative the complete proposed helper is:

```go
// New, service-only; choose the width from Task 2 measurements, not guesswork.
func serviceRollupWindow(last, minimum, maximum, width int64) (start, end int64, chunked bool) {
    start, end, chunked = rollupWindow(last, minimum, maximum)
    if width > 0 && end-start > width {
        end = start + width
        chunked = true
    }
    return
}
```

Call it only from `refreshServiceRollup`, with one internal measured constant. Preserve rawmax/state-key advancement and the publication-safety clamp already implemented there. Do not change the edge window or make the width a new operator setting. The helper alone is insufficient for equal-ingest/file-count pressure; do not select it if that case still fails.

Append the complete interval-bound test below to `rollup_watermark_test.go` (existing imports testing/time):

```go
func TestServiceRollupWindowsVisitTheWholeBacklog(t *testing.T) {
    const minimum = int64(time.Hour)
    const maximum = minimum + int64(3*time.Hour)
    const width = int64(5*time.Minute)
    last := int64(0)
    for pass := 0; last < maximum && pass < 100; pass++ {
        start, end, _ := serviceRollupWindow(last, minimum, maximum, width)
        if end <= last || end <= start || end-start > width {
            t.Fatalf("no bounded progress: last=%d start=%d end=%d",last,start,end)
        }
        if last != 0 && start != last { t.Fatalf("gap at %d: %d",last,start) }
        last = end
    }
    if last != maximum { t.Fatalf("stopped at %d, want %d",last,maximum) }
}
```

This is only the arithmetic test; the native tests below are mandatory for this SQL-execution-shape change. If another alternative is selected, replace this helper/test rather than committing unused choices. Report the selected patch and the measured reason; adjust exact file ownership in the task report.

- [ ] **Step 2: Prove no lost work on the engine**

Extend the existing engine fixtures (`insertRollupTestSpan`, `requireServiceRollupSpans` in `rollup_test.go`; immutable repository fixtures in `rollup_watermark_test.go`). Add `TestServiceRollupBacklogDrainsWithoutDroppingLateBuckets`: several ingest windows with many small batches, all-equal ingest timestamps, spans/logs in two namespaces, service-only outbound spans, and a late batch published below the guarded live tip. Drain bounded passes; assert every raw `(namespace,event minute,service)` count agrees with rollup count; source watermark only advances through completely processed work. Compare raw log counts, not `metric_count` against metric points. Assert a canceled pass rolls back its deletes and markers; a retry converges exactly.

Add `TestAnalyticalBacklogAllowsVersionAndAnomalyProgress` on the real engine with channel-coordinated concurrent tasks. Require versions and one anomaly appear before the backlog's complete drain; no one-minute timeout storm. Verify `RefreshReadCaches` still uses its independent cache gate/writer slot, compaction can complete and maintenance still holds both gates. Avoid timing assertions based only on sleeping; use channels and existing gate helpers. If a measured clamp prevents memory failure but cannot meet progress bounds, the task is incomplete.

- [ ] **Step 3: Repeat the frozen workload and record the tradeoff**

```bash
rtk just test ./internal/query -run '"TestServiceRollup|TestRollup|TestVersion|TestAnalyticalBacklog"' -count=1
rtk just test ./internal/panel -tags=readbench -run TestPanelsRemainUsableDuringBacklog -count=1 -timeout=12m -v
rtk just test ./internal/query -tags=readbench -run TestBacklogPressureRecordsConcurrentStatementFailures -count=1 -timeout=12m -v
rtk just fmt
```

Record same workload/cap/batch totals, failing→passing statement errors, peak tracked/RSS/spill, pass duration, queue time, total drain duration, version/anomaly progress and concurrent panel p95. A larger cap, smaller fixture or disabled panel load cannot stand in for the required 4 GB result. Keep the fixture available for the controller's final verification.

## Task 4: Make the existing redaction-safe error fully reachable and retain it in eval checks

**Files:** Modify `internal/panel/exec.go`, `execution_limits_test.go`, `ui/host/src/dashboards/inspect.tsx`, `inspect-views.test.tsx`, `scripts/dashboard-eval/score.ts`, `transport.ts`, `transport.test.ts`. Verify existing `internal/mcp/dashboards.go:saved` and its receipt tests without adding a duplicate contract. **Budget: $0.** Closes S2/S12 error evidence.

- [ ] **Step 1: Fail preservation, visibility and scoring tests**

Append this complete Go test to `execution_limits_test.go` using its existing context/fmt/strings imports:

```go
func TestPanelErrorsKeepTheCompleteRedactedMessage(t *testing.T) {
    detail := strings.Repeat("memory pressure; ", 80) + "final diagnostic"
    got := failed(Result{ID:"p"}, fmt.Errorf("IO Error at /srv/fanout/private.parquet: %s", detail), nil)
    if strings.Contains(got.Error,"/srv/fanout/private.parquet") ||
        !strings.Contains(got.Error,"<path>") || !strings.HasSuffix(got.Error,"final diagnostic") {
        t.Fatalf("message lost or leaked: %q",got.Error)
    }
}
```

Append this complete Bun test to `transport.test.ts`, importing `executePanels` from transport:

```ts
it('keeps the executor error in a completed failing panel check', async () => {
  const message = 'Out of Memory Error: ' + 'details '.repeat(100) + 'final diagnostic';
  const spec = {panels:[{id:'p'}]};
  const got = await executePanels(async () => ({results:[
    {id:'p',status:'error',elapsed_ms:12,error:message},
  ]}), spec, new AbortController().signal);
  expect(got.checked).toBe(true);
  expect(got.valid).toBe(true);
  expect(got.checks[0]).toEqual({id:'p',status:'error',rows:0,error:message});
});
```

Append this complete host regression to `inspect-views.test.tsx`, using its existing `render`, `result` and `act` helpers. It exercises the existing Data button for fresh failure and stale successful data:

```tsx
it.each([false,true])('Data exposes the full refresh error as safe text (stale=%s)',async stale=>{
  const message='Out of Memory: '+'details '.repeat(120)+'<b id="injected">final diagnostic</b>';
  const h=await render({id:'p',title:'Requests',viz:'table'},
    {...result,status:'error',frame:stale?result.frame:undefined,error:message});
  const button=h.querySelector<HTMLButtonElement>('[data-panel-view="Data"]');
  expect(button).not.toBeNull();
  await act(async()=>button!.click());
  const detail=h.querySelector<HTMLElement>('[data-panel-error-detail]');
  expect(detail?.textContent).toBe(message);
  expect(detail?.style.userSelect).toBe('text');
  expect(detail?.style.whiteSpace).toBe('pre-wrap');
  expect(detail?.style.overflowWrap).toBe('anywhere');
  expect(h.querySelector('#injected')).toBeNull();
  expect(h.querySelector('[data-panel-data] tbody tr')!==null).toBe(stale);
});
```

Controller acceptance also opens Data through the narrow-card menu radio with the keyboard; preserve the existing card size. A title attribute alone fails acceptance.

- [ ] **Step 2: Make the minimal changes**

In `failed`, retain `strings.TrimSpace(RedactPaths(err.Error()))` and remove only the 500-byte truncation; keep retry classification, cancellation logging and the special timeout messages. No new error route or copy dialog. Add this rendering before data/query/timing in the existing `PanelData`:

```tsx
{result?.error && <Text data-panel-error-detail size="sm" role="status"
  style={{whiteSpace:"pre-wrap",overflowWrap:"anywhere",userSelect:"text"}}>
  {result.error}
</Text>}
```

The current Data body already scrolls. Keep compact card/tooltip clamps; Data now provides the complete detail. Verify the menu is usable when the main error fills a short card. Do not pass the message through `dangerouslySetInnerHTML`. Keep wire messages redaction-safe; existing trusted log projections and path redaction remain intact. Add a test for a long SQL binding error on the real engine so display expansion cannot expose raw log bodies or local paths.

Extend existing `Check` with `error?:string` and the `executePanels` map with:

```ts
...(typeof r.error === 'string' ? {error:r.error} : {})
```

This preserves the executor-owned sanitized message; no scorer rule changes. Keep `panelPass` false for an error even when it has a message. Add a no-error case proving no invented error field. MCP receipts already carry Error; their guard must continue to pass with the complete sanitized message. Regenerate references/goldens only if their generated shape changes.

- [ ] **Step 3: Verify red/green and UI build**

```bash
rtk just test ./internal/panel -run '"TestPanelErrors|TestLogReadRedaction|TestSQLLogRedactionKeepsBoundary"' -count=1
rtk just test ./internal/mcp
rtk proxy bun test scripts/dashboard-eval/transport.test.ts scripts/dashboard-eval/score.test.ts
rtk proxy sh -c 'cd ui/host && bun run test && bun run lint'
rtk just ui
rtk just fmt
```

Controller browser acceptance later: a long refresh failure remains stale, Data exposes the final sentinel with keyboard navigation, and Retry still targets the failed batch. Mocked failure injection tests visibility only; restore real responses before final screenshots.

## Task 5: Bind schema-qualified log columns through the redacted AST projection

**Files:** Modify `internal/panel/sqlpanel.go`, `log_redaction_test.go`; extend `internal/query/panel_sql_test.go`/`sql_boundary_test.go` only if a boundary case needs guarding. **Budget: $0.** Closes S3/S12 deferred correctness.

- [ ] **Step 1: Replace the fail-closed deferral with a failing engine acceptance test**

Delete `TestQualifiedLogColumnsFailClosed` after replacing its exact safety assertions with this acceptance regression; do not retain an obsolete expected-failure test. Complete test:

```go
func TestQualifiedLogColumnsReturnOnlyRedactedValues(t *testing.T) {
    engine, repo := newTestEngine(t)
    n := fixtureStart.UnixNano()
    commit(t,repo,nil,[]telemetry.Log{{Namespace:"shop",ServiceName:"checkout",
        Body:"token=private failed",BodyTemplate:"token=private failed",
        EventUnixNanos:n,TimeUnixNanos:n,IngestedAt:n}})
    e := NewExecutor(engine,30)
    e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
    for _, text := range []string{
        "SELECT main.logs.body,main.logs.body_template FROM main.logs WHERE $__window(time)",
        "SELECT telemetry.logs.body FROM telemetry.logs WHERE $__window(time)",
        "SELECT logs.body FROM main.logs WHERE $__window(logs.time)",
        "SELECT l.body FROM main.logs AS l WHERE $__window(l.time)",
        "WITH history AS (SELECT main.logs.body,time FROM main.logs) SELECT body FROM history WHERE $__window(time)",
        "SELECT main.logs.body FROM main.logs WHERE $__window(time) AND main.logs.body LIKE '%private%'",
    } {
        t.Run(text,func(t *testing.T) {
            d := Dashboard{Name:"Qualified logs",Time:Time{Range:"1h"},Panels:[]Panel{
                {ID:"p",Title:"Qualified",Viz:"table",SQL:text},
            }}
            got,err := e.Run(t.Context(),RunRequest{Dashboard:d})
            if err != nil || len(got)!=1 || got[0].Status==StatusError {
                t.Fatalf("qualified execution: %+v %v",got,err)
            }
            wire,_ := json.Marshal(got[0].Frame)
            if strings.Contains(string(wire),"private") { t.Fatalf("body leaked: %s",wire) }
            if strings.Contains(text,"LIKE") {
                if got[0].Status!=StatusEmpty { t.Fatalf("raw body matched: %+v",got[0]) }
            } else if got[0].Frame.Rows!=1 || !strings.Contains(string(wire),"[REDACTED]") {
                t.Fatalf("missing redacted row: %s",wire)
            }
        })
    }
}
```

Retain `TestSQLLogRedactionKeepsBoundary`. Add real-engine cases for SELECT/WHERE/ORDER BY/JOIN-qualified references, nested subquery/CTE, quoted column identifiers, two logs aliases, correlated outer alias, and `main.logs.*` if the native parsed STAR representation supports it. Explicit aliases must behave like native DuckDB: `main.logs.body` after `FROM main.logs AS l` is not silently rebound to l. Catalog/private schemas remain rejected; CTEs may not shadow telemetry relations. A literal containing `main.logs.body` is unchanged. Existing `expandMacros` permits at most a two-component argument; use `logs.time`, an alias time or unqualified time in these tests. Three-component macro arguments are not required to fix schema-qualified SELECT columns and stay outside this task.

- [ ] **Step 2: Normalize resolved column qualifiers in the native AST, before table replacement**

The observed failure is that `redactSQLLogTables` replaces `main.logs` with a derived relation aliased `logs` but leaves `COLUMN_REF.column_names=[main,logs,body]` referring to a relation that no longer exists. Reuse the native parser/rendering seam; `compileRelations` already demonstrates AST column-name rewriting for snapshots, but it operates later and cannot fix a column rejected during panel DESCRIBE.

Add **new** internal function:

```go
func normalizeLogQualifiers(node map[string]any) error
```

Its implementation must walk native query scopes, collect unaliased approved logs BaseTable refs **before replacing any**, and resolve three-part COLUMN_REF names to the resulting `logs` alias. Preserve explicit aliases, catalog/schema rejection, parameter indices and column aliases. Scope lookup starts local and continues only for legal correlated references. CTE definitions start their own relation scope; a local alias that shadows an outer name blocks accidental capture. Treat an unrecognized AST shape conservatively rather than globally deleting `main`/`telemetry` from all column references. Apply the same scope mapping to qualified STAR only after an engine parser test observes its representation; STAR shape is a **hypothesis until that test runs**, not a guessed map key.

Complete proposed scope/column implementation follows. The `SELECT_NODE` / `BASE_TABLE` / `SUBQUERY` / `JOIN`, common TableRef `alias`/`sample`, `from_table`, `cte_map.map` and column-name fields are observed in `sqlpanel.go`, `filter.go`, `query/sql_boundary.go`. Native acceptance tests validate this proposal; unfamiliar query scopes are left unchanged and must bind/reject normally. It handles columns; qualified STAR remains a separate parser-observation case above.

```go
type logQualifierBinding struct { schema string; unaliasedLogs bool }
type logQualifierScope struct {
    parent *logQualifierScope
    names map[string]logQualifierBinding
}
func (s *logQualifierScope) resolves(schema,name string) bool {
    for current:=s; current!=nil; current=current.parent {
        if b,ok:=current.names[strings.ToLower(name)]; ok {
            return b.unaliasedLogs && (b.schema==schema || b.schema=="" && schema=="main")
        }
    }
    return false
}
func (s *logQualifierScope) collect(value any) {
    switch node:=value.(type) {
    case []any:
        for _,child:=range node { s.collect(child) }
    case map[string]any:
        if node["type"]=="SELECT_NODE" { return } // A nested query owns its bindings.
        kind,_:=node["type"].(string)
        alias,_:=node["alias"].(string)
        _,tableRef:=node["sample"]
        if tableRef && alias!="" {
            // An aliased join/subquery/table shadows outer bindings; don't inspect its internals.
            s.add(alias,logQualifierBinding{})
            return
        }
        if kind=="BASE_TABLE" {
            name,_:=node["table_name"].(string)
            schema,_:=node["schema_name"].(string)
            catalog,_:=node["catalog_name"].(string)
            allowed:=strings.EqualFold(name,"logs") && catalog=="" &&
                (schema=="" || schema=="main" || schema=="telemetry")
            s.add(name,logQualifierBinding{schema:schema,unaliasedLogs:allowed})
            return
        }
        if kind=="SUBQUERY" { return } // Never collect its inner tables into this scope.
        for _,child:=range node { s.collect(child) }
    }
}
func (s *logQualifierScope) add(name string,b logQualifierBinding) {
    key:=strings.ToLower(name)
    if _,exists:=s.names[key]; exists { b=logQualifierBinding{} } // Ambiguous: preserve native rejection.
    s.names[key]=b
}
func normalizeLogQualifiers(node map[string]any) error {
    walkLogQualifiers(node,nil)
    return nil
}
func walkLogQualifiers(value any,scope *logQualifierScope) {
    switch node:=value.(type) {
    case []any:
        for _,child:=range node { walkLogQualifiers(child,scope) }
    case map[string]any:
        kind,_:=node["type"].(string)
        if kind=="SELECT_NODE" {
            scope=&logQualifierScope{parent:scope,names:map[string]logQualifierBinding{}}
            scope.collect(node["from_table"])
        } else if _,hasFrom:=node["from_table"]; hasFrom {
            return // Unknown query scope: don't capture an outer table accidentally.
        }
        if node["class"]=="COLUMN_REF" {
            names,_:=node["column_names"].([]any)
            if len(names)==3 {
                schema,schemaOK:=names[0].(string)
                table,tableOK:=names[1].(string)
                if schemaOK && tableOK && (schema=="main" || schema=="telemetry") && scope.resolves(schema,table) {
                    node["column_names"]=[]any{names[1],names[2]}
                }
            }
            return
        }
        if ctes,ok:=node["cte_map"].(map[string]any); ok {
            entries,_:=ctes["map"].([]any)
            for _,entry:=range entries {
                definition,_:=entry.(map[string]any)
                walkLogQualifiers(definition["value"],nil)
            }
        }
        for key,child:=range node {
            if key!="cte_map" { walkLogQualifiers(child,scope) }
        }
    }
}
```

Call `normalizeLogQualifiers` between `ParseSQL` and `redactSQLLogTables` in `canonicalSQL`. This proposal deliberately preserves explicit aliases, ambiguous names and unknown scopes for native binding; expand recognition only for an observed legitimate shape with an engine regression. The existing redaction/boundary walk remains authoritative for CTE/catalog/private restrictions. Do not add SQL-name string replacement, a second query path or broad permission for physical logs. Preserve trusted inner projection's stop-recursion behavior. Render, bind and DESCRIBE/project on the guarded existing connection/snapshot path.

- [ ] **Step 3: Execute the full boundary regression suite**

```bash
rtk just test ./internal/panel -run '"TestQualifiedLogColumns|TestLogReadRedaction|TestSQLLogRedaction|TestSQLPanel"' -count=1
rtk just test ./internal/query -run '"TestPanelSQL|TestSQLBoundary|TestVariant"' -count=1
rtk just fmt
```

Report each executed SQL shape and returned value/status, including rejection cases. Rendered SQL equality alone does not establish redaction or binding correctness.

## Task 6: Route only exact completed-trace aggregates and measure the panel executor

**Files:** Create `internal/panel/cache_route.go`, `internal/panel/cache_route_test.go`, `internal/panel/cache_route_bench_test.go`; modify `internal/panel/exec.go`; reuse `internal/query/batch_cache.go:aggregateSources`, `traceCandidateSource`, `internal/query/file_snapshot.go:bindSnapshot` without new tables/read kinds. Read `internal/observability/trace.go:completedTraceQuery`, `internal/queryrows/window.go`, compaction/cache tests. **Budget: $0.** Closes S7 and supports S6/S8.

- [ ] **Step 1: Freeze and test a coverage matrix before routing**

| Aspect | Covered initially | Raw otherwise |
|---|---|---|
| Signal | spans | logs, metrics |
| Measures | every measure is `count_distinct(trace_id)`, with distinct aliases | count/rate/error_rate/share, percentile, duration/min/max/sum/avg, distinct other field, mixed list |
| Grouping | none | service/namespace/trace ID/route/attribute dimensions, including combinations |
| Bucket | execution Scope.Interval=0 | all positive intervals, including auto; candidate bounds cannot distribute each trace across buckets |
| Filters | explicit `trace_id <> ''` plus standalone service/namespace equalities that resolve to nonempty literals or single values; dropped All filters allowed | no nonempty-ID guarantee, OR/NOT/IN/list/range/body/status/kind/route/attributes, unresolved/None variables, duplicate/conflicting scopes |
| Visualization/subquery | ordinary ungrouped table; unbucketed stat/gauge totals reached through `totals` | fixed row/distribution/item/health/map/deploy-split paths; stat sparkline stays raw |
| SQL panel | never | always guarded raw SQL, including a query spelling count-distinct |

Do not route through service_rollup even after its watermark catches up: that is not a complete active-file generation proof. Broader shapes need a later separately specified exact accelerator; they are explicitly raw here.

Add this complete **new** coverage helper in `cache_route.go` (package panel; imports context and queryrows). `Filter.Source` is already checked by `Check`; parse it without the canonical injected parameter casts, as `rollupFilterValue` does. That existing helper returns errors for ordinary unsupported shapes too: only operational errors propagate; an unsupported valid expression returns uncovered and is compiled raw.

```go
func traceCountScope(ctx context.Context, parser Parser, p *Panel, measures []Measure, filters []Filter, scope Scope) (queryrows.Window,bool,error) {
    w := queryrows.Window{Start:scope.Start,End:scope.End,Kind:queryrows.TraceCandidateRead}
    if p.SQL!="" || p.Query==nil || p.Query.From!="spans" ||
        len(p.Query.By)!=0 || scope.Interval!=0 || len(measures)==0 ||
        p.Options!=nil && p.Options.Split=="deploy" {
        return w,false,nil
    }
    switch p.Viz { case "table","stat","gauge": default: return w,false,nil }
    for _,m := range measures {
        if m.Func!="count_distinct" || m.Field==nil || m.Field.Column!="trace_id" || m.Field.Key!="" {
            return w,false,nil
        }
    }
    nonempty := false
    seen := map[string]bool{}
    for _,f := range filters {
        if scope.dropped(f) { continue }
        tree,err := parser.ParseSQL(ctx,"SELECT 1 FROM spans WHERE ("+f.Source+")")
        if err!=nil { return w,false,err }
        node := rollupFilterNode(tree["where_clause"])
        if node["class"]=="COMPARISON" && node["type"]=="COMPARE_NOTEQUAL" {
            left,right := rollupFilterNode(node["left"]),rollupFilterNode(node["right"])
            if left["class"]!="COLUMN_REF" { left,right=right,left }
            literal := rollupFilterNode(right["literal"])
            text,isString := literal["text"].(string)
            if left["class"]=="COLUMN_REF" && fieldText(left)=="trace_id" &&
                right["class"]=="CONSTANT" && isString && text=="" {
                if nonempty { return w,false,nil }
                nonempty=true
                continue
            }
        }
        field,value,err := rollupFilterValue(ctx,parser,f,scope)
        if err!=nil {
            if isOperational(err) { return w,false,err }
            return w,false,nil
        }
        if seen[field] { return w,false,nil }
        seen[field]=true
        if field=="namespace" { w.Namespace=value } else { w.Service=value }
    }
    return w,nonempty,nil
}
```

`literal.text` and `COLUMN_REF.column_names` follow native access conventions in `filter.go`; confirm the proposed `COMPARE_NOTEQUAL` and empty-string node with an engine parser test before committing. Do not accept a null/numeric constant merely because `constantText` returns an empty default. All-dropped filters are skipped; None, multivalue, empty/whitespace scope equality, OR/casts/other expressions stay raw. Empty namespace/service equality is raw because Window's empty string means unscoped. A single unsupported active filter makes the **whole** query raw. No textual SQL-name or predicate matching.

Add a table-driven test for every cell above with real DuckDB parser/Check. Require multiple aliases all checked; one unsupported second measure or second grouping field rejects the entire route. All/None, apostrophes in bound service, multiple/conflicting equality filters and positive Scope.Interval must be explicit tests.

- [ ] **Step 2: Compile through the existing candidate reader**

After coverage succeeds, call a **new** `compileTraceCount(p,measures,scope,window) Compiled`. The complete SQL aggregation core is:

```go
func compileTraceCount(p *Panel, measures []Measure, scope Scope, w queryrows.Window) Compiled {
    selects := make([]string,len(measures))
    columns := make([]Column,len(measures))
    for i,m := range measures {
        selects[i] = "count(DISTINCT trace_id)::DOUBLE AS " + quoteIdent(m.Alias)
        columns[i] = Column{Name:m.Alias,Type:"number",Role:"measure",Unit:m.Unit}
    }
    text := `WITH ids AS (
      SELECT trace_id FROM trace_candidates
      UNION ALL
      SELECT trace_id FROM trace_tail
      WHERE start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS
      AND start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS
      AND (?='' OR namespace=?) AND (?='' OR service=?) AND trace_id<>''
    ) SELECT ` + strings.Join(selects,",") + " FROM ids" + orderAndLimit(p,measures,false,0)
    return Compiled{SQL:text,Args:[]any{scope.Start.UTC(),scope.End.UTC(),
        w.Namespace,w.Namespace,w.Service,w.Service},Columns:columns}
}
```

Use `queryrows.WithWindow` with Kind TraceCandidateRead before `e.engine.QueryContext`. `CompletedBatchReads()` must be true, through the existing `queryrows.BatchReader` type assertion; other engines run raw. QueryContext already holds publication lease, begins one transaction, reads batch markers, repairs retired contributions and binds raw tail/boundaries in that transaction. Do not directly query `read_trace_candidates` from panel code or call RefreshReadCaches during a request. `UNION ALL` plus DISTINCT handles touched traces present in both cache and raw tail. Keep the original frame scanner/limits/columns; a zero count is the same one-row aggregate as raw.

Use this complete **new** selector in the same file (add strings import for the compiler above). The existing `Compiled` needs no new field: the returned reader Window carries kind and scope together.

```go
func (e *Executor) compileStructured(ctx context.Context,p *Panel,checked *Checked,scope Scope) (Compiled,queryrows.Window,error) {
    w := queryrows.Window{Start:scope.Start,End:scope.End,Kind:queryrows.RawRead}
    if reader,ok := e.engine.(queryrows.BatchReader); ok && reader.CompletedBatchReads() {
        candidate,covered,err := traceCountScope(ctx,e.engine,p,checked.Measures[p.ID],checked.Filters[p.ID],scope)
        if err!=nil { return Compiled{},w,err }
        if covered {
            compiled := compileTraceCount(p,checked.Measures[p.ID],scope,candidate)
            compiled.SQL="/* path: completed_trace_candidates */ "+compiled.SQL
            return compiled,candidate,nil
        }
    }
    compiled,err := compileQuery(p,checked.Measures[p.ID],checked.Filters[p.ID],scope)
    if err==nil { compiled.SQL="/* path: raw */ "+compiled.SQL }
    return compiled,w,err
}
```

In `runScope`'s structured branch replace only `compileQuery(...)` with `e.compileStructured(...)`; set `ctx=queryrows.WithWindow(ctx,w)` after its error check, before the existing QueryContext/scan block. Do not change the SQL-panel branch. Keep metadata in existing `Result.SQL`/timing inspection, no new badge, setting or endpoint. A routed query can include an uncached tail. Previous-period runScope and stat totals use the same selector; bucketed sparks stay raw. For stat totals/comparison, retain each executed statement in existing SQL inspect text with fixed `current`/`totals`/`previous` separators, rather than showing only the raw sparkline. Change `totals`' internal return to `([]any,string,error)` to retain its actual SQL; update its two callers. Each displayed statement is the actual executed text, including its path comment; add a real-engine stat/previous inspection test. Preserve inferred and explicit per-measure units from Check.

- [ ] **Step 3: Execute raw/routed equivalence on identical immutable data**

Complete smoke equivalence test for `cache_route_test.go` (package panel; imports reflect, testing, time, queryrows and telemetry). The engine helper and shop fixture already exist; `compileTraceCount` is the proposed compiler above:

```go
func TestTraceCountCacheMatchesRawAndSeesNewBatches(t *testing.T) {
    engine,repo := newTestEngine(t)
    commit(t,repo,shopSpans(),nil)
    if _,err := engine.RefreshReadCaches(t.Context()); err != nil { t.Fatal(err) }
    e := NewExecutor(engine,30)
    p := Panel{ID:"p",Title:"Traces",Viz:"table",Query:&Query{From:"spans",
        Measures:[]string{"count_distinct(trace_id)"},Where:[]string{"trace_id <> ''","service = 'checkout'"}}}
    d := Dashboard{Name:"Counts",Time:Time{Range:"1h"},Panels:[]Panel{p}}
    Normalize(&d)
    checked,err := e.check(t.Context(),&d)
    if err != nil { t.Fatal(err) }
    p = d.Panels[0]
    scope := Scope{Start:fixtureStart.Add(30*time.Second),End:fixtureStart.Add(59*time.Minute+30*time.Second)}
    compare := func() {
        t.Helper()
        raw,err := compileQuery(&p,checked.Measures[p.ID],checked.Filters[p.ID],scope)
        if err != nil { t.Fatal(err) }
        rawCtx := queryrows.WithWindow(t.Context(),queryrows.Window{Start:scope.Start,End:scope.End})
        rows,err := engine.QueryContext(rawCtx,raw.SQL,raw.Args...)
        if err != nil { t.Fatal(err) }
        want,err := scanFrame(rows,raw.Columns,1000)
        if err != nil { t.Fatal(err) }
        w,covered,err := traceCountScope(t.Context(),engine,&p,checked.Measures[p.ID],checked.Filters[p.ID],scope)
        if err != nil || !covered { t.Fatalf("not covered: %+v %v",w,err) }
        routed := compileTraceCount(&p,checked.Measures[p.ID],scope,w)
        rows,err = engine.QueryContext(queryrows.WithWindow(t.Context(),w),routed.SQL,routed.Args...)
        if err != nil { t.Fatal(err) }
        got,err := scanFrame(rows,routed.Columns,1000)
        if err != nil { t.Fatal(err) }
        if !reflect.DeepEqual(got,want) { t.Fatalf("cache=%+v raw=%+v",got,want) }
    }
    compare()
    late := shopSpans()[1]
    late.TraceID = "new-trace"
    late.StartUnixNanos = fixtureStart.Add(31*time.Minute).UnixNano()
    late.EndUnixNanos = late.StartUnixNanos+1000000
    late.DurationMS = 1
    commit(t,repo,[]telemetry.Span{late},nil)
    compare() // newly active, no cache refresh: must be immediately visible
    if _,err := engine.RefreshReadCaches(t.Context()); err != nil { t.Fatal(err) }
    compare()
}
```

Complete the matrix on the same data: no-cache/all-cached/mixed cache; unaligned and signed-nanosecond windows; start/end-boundary traces spanning multiple files; same trace across namespaces/services; empty and missing trace IDs with uncovered raw behavior; namespace/service alone/together and literal/single variable/All; two aliases; prior shifted window; stat total versus raw sparkline. Reuse query compaction tests' `PublishParquetReplacement` and repository retention helpers for before/after replacement/retirement; assert count exactly agrees at every pinned snapshot and after late publication. A concurrent mutation test uses channels around marker and row reads to show one transaction, not two independent reads. Existing batch cache tests remain the snapshot authority; do not copy their cache implementation.

Add `TestSQLPanelsNeverUseCompletedTraceRouting`, `TestUncoveredMeasuresStayRaw` and `TestRoutedErrorsDoNotRetryRaw` (failure spy for retry count plus a real operational engine error). Include exact status, values, column roles/units, totals and row caps, not just `rows>0`. Floating point calculations elsewhere require justified tolerances; trace integer counts compare exactly.

- [ ] **Step 4: Benchmark raw, routed and mixed 24-hour panel execution**

Create `cache_route_bench_test.go` under readbench. Complete generated benchmark core below uses production format-3 writes, both executors over the same engine/data, no public ForceRaw flag, and absolute 24h ranges. Imports: sort, strings, testing, time, fmt, config, query, telemetry, telemetrystore. Setup is outside ResetTimer; the normal Just command below requests 100 timed samples. The proposed 196,608-span/96-file fixture is a baseline, not the required larger real-data verdict.

```go
//go:build readbench

package panel

type rawPanelEngine struct { Engine }
func (rawPanelEngine) CompletedBatchReads() bool { return false }

func BenchmarkPanelQueries24Hours(b *testing.B) {
    start:=time.Date(2026,10,1,0,0,0,0,time.UTC)
    end:=start.Add(24*time.Hour)
    cfg:=config.Config{DataDir:b.TempDir(),RetentionDays:30,DuckDBMemory:"1GB",
        DuckDBThreads:2,DuckDBMaxConns:4,RollupInterval:time.Hour}
    repo,err:=telemetrystore.Open(cfg.TelemetryDir())
    if err!=nil { b.Fatal(err) }
    b.Cleanup(func(){ _=repo.Close() })
    for batch:=range 96 {
        data:=telemetrystore.Batch{ID:fmt.Sprintf("bench-%04d",batch)}
        for i:=range 2048 {
            at:=start.Add(time.Duration((batch*2048+i)%86400)*time.Second).UnixNano()
            latency:=float64(i%1000+1)
            data.Spans=append(data.Spans,telemetry.Span{Namespace:"bench",
                ServiceName:fmt.Sprintf("svc%d",i%6),TraceID:fmt.Sprintf("t%d-%d",batch,i/4),
                SpanID:fmt.Sprintf("s%d",i),StartUnixNanos:at,
                EndUnixNanos:at+int64(latency*1000000),DurationMS:latency,
                IngestedAt:at,Kind:"SPAN_KIND_SERVER",StatusCode:"STATUS_CODE_OK"})
        }
        if err:=repo.Commit(b.Context(),data);err!=nil { b.Fatal(err) }
    }
    engine,err:=query.NewDuck(b.Context(),cfg,repo)
    if err!=nil { b.Fatal(err) }
    b.Cleanup(func(){ _=engine.Close() })
    for pass:=0;pass<4;pass++ {
        if _,err:=engine.RefreshReadCaches(b.Context());err!=nil { b.Fatal(err) }
    }
    var acknowledged int
    if err:=engine.DB.QueryRowContext(b.Context(),"SELECT count(*) FROM read_batches").Scan(&acknowledged);err!=nil || acknowledged!=96 {
        b.Fatalf("warm markers=%d error=%v",acknowledged,err)
    }
    shapes:=[]struct{name string;query *Query}{
        {"distinct_traces",&Query{From:"spans",Measures:[]string{"count_distinct(trace_id)"},Where:[]string{"trace_id <> ''"}}},
        {"scoped_traces",&Query{From:"spans",Measures:[]string{"count_distinct(trace_id)"},Where:[]string{"trace_id <> ''","service = 'svc1'"}}},
        {"raw_p95",&Query{From:"spans",Measures:[]string{"p95(duration_ms)"},By:[]string{"service"}}},
    }
    for _,shape:=range shapes {
        for _,routed:=range []bool{false,true} {
            mode:="raw";var source Engine=rawPanelEngine{engine}
            if routed { mode="selector";source=engine }
            b.Run(shape.name+"/"+mode,func(b *testing.B){
                b.StopTimer()
                e:=NewExecutor(source,30);e.now=func()time.Time{return end}
                spec:=Dashboard{Name:"Benchmark",Time:Time{From:&start,To:&end},
                    Panels:[]Panel{{ID:"p",Title:shape.name,Viz:"table",Query:shape.query}}}
                warm,err:=e.Run(b.Context(),RunRequest{Dashboard:spec})
                if err!=nil || len(warm)!=1 || warm[0].Status!=StatusOK { b.Fatalf("warm: %+v %v",warm,err) }
                if routed && shape.name!="raw_p95" && !strings.Contains(warm[0].SQL,"path: completed_trace_candidates") {
                    b.Fatalf("expected route: %s",warm[0].SQL)
                }
                samples:=make([]float64,0,b.N)
                b.ReportAllocs();b.ResetTimer();b.StartTimer()
                for i:=0;i<b.N;i++ {
                    at:=time.Now()
                    results,err:=e.Run(b.Context(),RunRequest{Dashboard:spec})
                    samples=append(samples,float64(time.Since(at))/float64(time.Millisecond))
                    if err!=nil || len(results)!=1 || results[0].Status!=StatusOK { b.Fatalf("query: %+v %v",results,err) }
                }
                b.StopTimer()
                sort.Float64s(samples)
                rank:=(95*len(samples)+99)/100
                if rank>0 { b.ReportMetric(samples[rank-1],"p95_ms") }
                b.ReportMetric(float64(len(samples)),"samples")
                b.ReportMetric(196608,"fixture_spans")
            })
        }
    }
}
```

The direct marker assertion is test-only, never a request-time panel query. Extend this core with the actual twelve-panel fixture and mixed cached/uncached variants, tracking independent shape/path p95s. Add controller-supplied scratch repository input as a **new benchmark-only** env option and reuse the same executor/time loop; retain that input's actual 24h bounds, files/bytes and copy provenance. Do not call `newTestEngine(*testing.T)` from testing.B, mutate original telemetry, measure setup/cache draining, or average shapes into a favorable p95. Warm once per shape, report ns/op/allocations plus sample p95 and cap/threads. S7≤500ms applies to each reported warm query shape; the mixed dashboard is separately labelled.

```bash
rtk just test ./internal/panel -run '"TestTraceCountCache|TestUncovered|TestSQLPanelsNever|TestRouted"' -count=1
rtk just test ./internal/query ./internal/observability -count=1
rtk just test ./internal/panel -tags=readbench -run '^$' -bench BenchmarkPanelQueries24Hours -benchtime=100x -count=3 -timeout=15m
rtk just fmt
```

Record before/after on identical data and same hardware, including non-routed latency/log panels. S7 ≤500 ms is evaluated on the controller's replayed demo data at Task 11. A generated fixture benchmark is repeatable performance evidence, not proof of the demo target.

## Task 7: Add the 12-panel/24-hour performance and refresh-request CI guard

**Files:** Modify `internal/cmd/e2eseed/main.go`, `main_test.go`, `ui/host/e2e/global-setup.ts`, `smoke.spec.ts` only to extract shared test helpers, `harness.test.ts`, `.github/workflows/ci.yml`; Create `ui/host/e2e/test.ts`, `performance.spec.ts`, `performance-support.ts`, `performance-support.test.ts`, `fixtures/performance.json`. Current Playwright config projects/one worker remain. **Budget: $0.** Depends on Task 6; closes S6/S8.

- [ ] **Step 1: Fail deterministic 24-hour seed and fixture validation tests**

Simplest seed extension: in `payloads` change the 120-minute loop and two-hour origin to `1440` and `-24*time.Hour`, preserving seed 13, stable protobuf marshal, six services and version split one hour ago. Update the existing `TestSeedCoversDistributedTelemetry`'s two near-now bounds to -24h and its exact totals to spans=8640, parents=7200, errors=432, logs=8640; retain all service/kind/version/log-correlation checks. Default smoke range remains 3h and continues to have populated panels; the new performance fixture uses 24h. No new generator program or demo files. Report the **8640 spans and 8640 logs** honestly. CI catches regressions on this workload; real-data performance remains Task 11's much larger replay/Go benchmark.

Append complete Go test to `main_test.go` (add collectorlogs import if absent):

```go
func TestSeedCoversTwentyFourHoursDeterministically(t *testing.T) {
    base := time.Date(2026,10,9,12,0,0,0,time.UTC)
    payload,err := payloads(13,base)
    if err != nil { t.Fatal(err) }
    var spans collectortrace.ExportTraceServiceRequest
    if err := proto.Unmarshal(payload["traces"],&spans); err != nil { t.Fatal(err) }
    var lo,hi uint64
    count := 0
    for _,r := range spans.ResourceSpans { for _,s := range r.ScopeSpans { for _,p := range s.Spans {
        if count==0 || p.StartTimeUnixNano<lo { lo=p.StartTimeUnixNano }
        if p.StartTimeUnixNano>hi { hi=p.StartTimeUnixNano }
        count++
    } } }
    if count!=8640 || int64(lo)!=base.Add(-24*time.Hour).UnixNano() ||
        int64(hi)<base.Add(-time.Minute).UnixNano() {
        t.Fatalf("count=%d bounds=%d..%d",count,lo,hi)
    }
    var logs collectorlogs.ExportLogsServiceRequest
    if err := proto.Unmarshal(payload["logs"],&logs); err != nil { t.Fatal(err) }
    n := 0
    for _,r := range logs.ResourceLogs { for _,s := range r.ScopeLogs { n+=len(s.LogRecords) } }
    if n!=8640 { t.Fatalf("logs=%d",n) }
}
```

Create the complete performance spec fixture (spec/config only, no telemetry):

```json
{
  "version": 1, "name": "Dashboard performance", "time": {"range":"24h","refresh":"off"},
  "variables":[{"name":"service","kind":"query","from":"spans","field":"service","default":"$__all","include_all":true}],
  "annotations":{"deploys":true,"anomalies":true},
  "panels":[
    {"id":"traces_all","title":"Distinct traces","viz":"table","width":4,"height":"s","query":{"from":"spans","measures":["count_distinct(trace_id)"],"where":["trace_id <> ''"]}},
    {"id":"traces_checkout","title":"Checkout traces","viz":"table","width":4,"height":"s","query":{"from":"spans","measures":["count_distinct(trace_id)"],"where":["trace_id <> ''","service = 'checkout'"]}},
    {"id":"traces_selected","title":"Selected traces","viz":"table","width":4,"height":"s","query":{"from":"spans","measures":["count_distinct(trace_id)"],"where":["trace_id <> ''","service = $service"]}},
    {"id":"count","title":"Spans","viz":"stat","width":4,"height":"s","query":{"from":"spans","measures":["count()"]}},
    {"id":"rate","title":"Request rate","viz":"stat","width":4,"height":"s","query":{"from":"spans","measures":["rate()"]}},
    {"id":"logs","title":"Log count","viz":"stat","width":4,"height":"s","query":{"from":"logs","measures":["count()"]}},
    {"id":"traffic","title":"Traffic by service","viz":"timeseries","width":4,"height":"s","query":{"from":"spans","measures":["count()"],"by":["service"],"bucket":"auto"}},
    {"id":"latency","title":"p95 latency","viz":"timeseries","width":4,"height":"s","query":{"from":"spans","measures":["p95(duration_ms)"],"bucket":"auto"}},
    {"id":"errors","title":"Error rate","viz":"timeseries","width":4,"height":"s","query":{"from":"spans","measures":["error_rate()"],"bucket":"auto"}},
    {"id":"log_volume","title":"Log volume","viz":"timeseries","width":4,"height":"s","query":{"from":"logs","measures":["count()"],"bucket":"auto"}},
    {"id":"service_counts","title":"Spans by service","viz":"bar","width":4,"height":"s","query":{"from":"spans","measures":["count()"],"by":["service"]}},
    {"id":"severity_counts","title":"Logs by severity","viz":"bar","width":4,"height":"s","query":{"from":"logs","measures":["count()"],"by":["severity"]}}
  ]
}
```

Extend `TestAllPanelsFixtureValidates`' native validation seam to read/normalize/check/execute this fixture, verify 12 terminal populated panels and 24h bounds. The three tables must show the routed inspect comment once caches warm; raw shapes stay raw. Readiness must verify exact telemetry counts and cached trace-count path, not sleep a fixed time. Global setup creates this board alongside the all-panels board through the current authenticated POST, stores `FANOUT_E2E_PERFORMANCE_ID`, and awaits the native results; preserve cleanup and secret redaction. Existing e2eseed credential-file contract is unchanged.

- [ ] **Step 2: Test the statistics and request accounting without a browser**

Complete `performance-support.ts` core and Vitest tests:

```ts
export function p95(values:number[]):number {
  if(values.length<20 || values.some(x=>!Number.isFinite(x)||x<0))throw new Error('Twenty finite samples required');
  const ordered=[...values].sort((a,b)=>a-b);
  return ordered[Math.ceil(.95*ordered.length)-1];
}
export function assertRefreshRequests(paths:string[]) {
  const count=(path:string)=>paths.filter(p=>p===path).length;
  if(count('/api/panels/query')!==1 || count('/api/annotations')!==1 ||
     count('/api/variables/resolve')!==0)throw new Error('Refresh request budget exceeded');
}
```

```ts
import {expect,it} from 'vitest';
import {p95,assertRefreshRequests} from './performance-support';
it('uses nearest-rank p95 without dropping slow samples',()=>{
  expect(p95(Array.from({length:20},(_,i)=>i+1))).toBe(19);
  expect(()=>p95([1,2])).toThrow();
  expect(()=>p95([...Array(19).fill(1),NaN])).toThrow();
});
it('counts completed refresh requests including duplicate annotations and variable reloads',()=>{
  const valid=['/api/panels/query','/api/annotations'];
  expect(()=>assertRefreshRequests(valid)).not.toThrow();
  for(const extra of ['/api/panels/query','/api/annotations','/api/variables/resolve'])
    expect(()=>assertRefreshRequests([...valid,extra])).toThrow();
  expect(()=>assertRefreshRequests(valid.slice(0,1))).toThrow();
});
```

- [ ] **Step 3: Implement a measured Playwright test on the existing disposable setup**

Extract the existing baseURL/storageState/faults fixtures from `smoke.spec.ts` to `e2e/test.ts` verbatim, preserving its zero warning/error/pageerror assertions. Tests import that shared `test`; do not add a second server/session mechanism. New `performance.spec.ts` uses Playwright response/request listeners before navigation, collects every panel result and elapsed_ms, and asserts all twelve IDs reach status ok and DOM paint (stat value/table row/canvas). Use known `.mantine-Paper-root[data-panel]`, `[data-stat-value]`, `[data-panel-body]`, loading/error selectors and `Refresh now` aria-label.

Measure first-full-render from a navigation init-script `performance.now()` origin through **all twelve painted panels**, no loaders/failed states, annotation completion, fonts ready and two animation frames. On a standard 1100/1440×900 viewport, explicitly scroll to mount each offscreen card and include that in the measurement; do not label first viewport's completion as full render. Also record a fixed all-visible viewport measurement (set before navigation, actual IntersectionObserver, no request body changes) for repeatable S8 refresh counting. Record viewport/procedure separately so numbers are comparable. The spec does not require one request during first lazy rendering; it requires one batch per completed refresh of visible panels.

Take 20 fresh browser contexts per measured project, using the existing storage-state file but fresh browser query caches. Reuse the seeded/warmed server, close each context after its sample. Do not warm its page before timing. Capture aborted/failed request and retry counts separately; errors fail the test and cannot be discarded as outliers. Keep the existing smoke four-project matrix. The performance test can run at 1440 in each theme with an explicit project skip for 1100, while smoke/a11y still covers both widths; state this reduction to keep CI under the current 12-minute global timeout. Measure total duration before changing timeout; no blind inflation.

After initial load settles, enlarge viewport to show all 12 cards, record three manual refresh cycles per theme with no navigation/scroll/range changes inside a cycle. Start listeners before clicking Refresh now, await both responses and all twelve DOM settles; assert `assertRefreshRequests`, every selected panel ID returned once, frames≤200000 cells, zero console faults. Variable options load once at initial range, zero per refresh. Change the range once and require exactly one `/api/variables/resolve`; repeat refreshes after the change without another variable request. Preserve the host's existing variable-selection re-resolution behavior separately; S8's once-per-range statement is not authority to break dependent variable updates. Include known-empty/background and scroll-no-refetch unit guards in regression commands.

Complete initial `performance.spec.ts` below uses the new shared `test` (export `expect` from `@playwright/test` there too). Add `CI_RENDER_CEILING_MS:number|null=null` to support during calibration only; final commit replaces null with the measured positive ceiling. This is runnable test code, not a mock timing sketch. Expand its captured bounded metrics with panel elapsed_ms/path fields and the range-change case described above; the baseline records every completed response and refuses failures.

```ts
import {test,expect} from './test';
import spec from './fixtures/performance.json' with {type:'json'};
import {p95,assertRefreshRequests,CI_RENDER_CEILING_MS} from './performance-support';
test('twelve panels over twenty-four hours meet render and refresh budgets',async({browser})=>{
  const viewport=test.info().project.use.viewport!;
  test.skip(viewport.width!==1440,'Smoke/accessibility cover both widths; timing uses 1440');
  const theme=test.info().project.use.colorScheme as 'light'|'dark';
  const id=process.env.FANOUT_E2E_PERFORMANCE_ID;
  const storageState=process.env.FANOUT_E2E_STORAGE_STATE;
  const baseURL=process.env.FANOUT_E2E_BASE_URL;
  if(!id||!storageState||!baseURL)throw new Error('Global performance setup missing');
  const samples:number[]=[],cycles:{sample:number;cycle:number;paths:string[]}[]=[];
  const faults:string[]=[];
  for(let sample=0;sample<20;sample++){
    const context=await browser.newContext({baseURL,storageState,colorScheme:theme,viewport});
    try {
      const page=await context.newPage();
      page.on('pageerror',e=>faults.push(e.message));
      page.on('console',m=>{if(['error','warning'].includes(m.type()))faults.push(m.text());});
      page.on('requestfailed',r=>faults.push(`request failed: ${new URL(r.url()).pathname}`));
      const events:string[]=[],results=new Map<string,string>();
      page.on('response',async response=>{
        const path=new URL(response.url()).pathname;
        if(!path.startsWith('/api/'))return;
        try {
          if(!response.ok())throw new Error(`HTTP ${response.status()} ${path}`);
          // Count a response only after its full body arrives, including annotations/variables.
          const body=await response.body();
          if(path==='/api/panels/query'){
            const json=JSON.parse(body.toString());
            for(const result of json.results??[]){
              results.set(result.id,result.status);
              if(result.status==='error')faults.push(`panel failed: ${result.id}`);
            }
          }
          events.push(path);
        }catch(e){faults.push(String(e));}
      });
      await page.addInitScript(t=>localStorage.setItem('mantine-color-scheme-value',t),theme);
      await page.goto(`/dashboards/${id}`);
      await expect(page.locator('html')).toHaveAttribute('data-mantine-color-scheme',theme);
      const settle=async()=>{
        for(const panel of spec.panels){
          const card=page.locator(`.mantine-Paper-root[data-panel="${panel.id}"]`);
          await card.scrollIntoViewIfNeeded();
          await expect.poll(()=>results.get(panel.id)).toBe('ok');
          await expect(card.locator('[aria-label="Loading panel"], [aria-label="Refreshing"], [data-panel-error]')).toHaveCount(0);
          const selector=panel.viz==='table'?'tbody tr':panel.viz==='stat'?'[data-stat-value]':'canvas';
          await expect(card.locator(selector).first()).toBeVisible();
        }
        await expect.poll(()=>events.filter(p=>p==='/api/annotations').length).toBeGreaterThan(0);
        await page.evaluate(async()=>{
          await document.fonts.ready;
          await new Promise<void>(r=>requestAnimationFrame(()=>requestAnimationFrame(()=>r())));
        });
      };
      await settle();
      // performance.now measures from this navigation's time origin, including initial transfer.
      samples.push(await page.evaluate(()=>performance.now()));
      expect(events.filter(p=>p==='/api/variables/resolve')).toHaveLength(1);
      if(sample===0){
        await page.setViewportSize({width:viewport.width,height:3000});
        await settle();
        const visible=await page.locator('.mantine-Paper-root[data-panel]').evaluateAll(cards=>cards.every(card=>{
          const r=card.getBoundingClientRect();return r.top>=0&&r.bottom<=innerHeight;
        }));
        expect(visible,'All twelve cards must really be visible for the S8 cycle').toBe(true);
        for(let cycle=0;cycle<3;cycle++){
          results.clear();
          const start=events.length;
          await page.getByRole('button',{name:'Refresh now',exact:true}).click();
          await expect.poll(()=>events.slice(start).filter(p=>p==='/api/annotations').length).toBe(1);
          await settle();
          const paths=events.slice(start);
          assertRefreshRequests(paths);
          expect([...results.keys()].sort()).toEqual(spec.panels.map(p=>p.id).sort());
          cycles.push({sample,cycle,paths});
        }
      }
      expect(faults).toEqual([]);
    } finally { await context.close(); }
  }
  const render_p95_ms=p95(samples);
  await test.info().attach('performance.json',{contentType:'application/json',body:JSON.stringify({
    fixture:'performance.json',theme,viewport,method:'fresh-context navigation and full scroll/paint',
    samples_ms:samples,render_p95_ms,local_target_ms:1500,ci_ceiling_ms:CI_RENDER_CEILING_MS,cycles,faults,
  },null,2)});
  if(process.env.FANOUT_E2E_CALIBRATE==='1')return;
  const limit=process.env.CI?CI_RENDER_CEILING_MS:1500;
  if(limit===null||!Number.isFinite(limit)||limit<=0)throw new Error('CI ceiling has not been calibrated');
  expect(render_p95_ms).toBeLessThanOrEqual(limit);
});
```

The 3000-pixel all-visible viewport is a proposed fixture procedure, not an assumed fit: its geometry assertion must pass. Return the scroll container to its top before checking visibility if settling the last card changes scroll; use the actual `main` scroll container observed by smoke/collector, not an IntersectionObserver mock. Add a quiet-frame check after network completion so an unexpected delayed duplicate request cannot escape the cycle's accounting. The range-change guard and max-frame-cell assertions remain required additions before Task 7 is complete. Keep every timing sample; never subtract Playwright scrolling overhead or drop a slow sample.

Attach bounded `performance.json` with raw timings, p95, first/full-visible method, viewport/theme, server/fixture identity, individual panel paths/timings and cycle network logs. No response bodies containing credentials. Output lives in Playwright artifacts, not committed measurements masquerading as fixtures.

- [ ] **Step 4: Calibrate CI, then enable the guard**

Controller runs the test with **recording enabled and no invented CI limit** on at least three ubuntu-24.04 CI runs of the frozen routed candidate, 20 samples/theme/run. Proposed new calibration mode `FANOUT_E2E_CALIBRATE=1` records timing while continuing to fail correctness/network/console errors; it does not count as final S6 acceptance. Check in a positive numeric ceiling in `performance-support.ts` after measuring: `ceil(max(per-run p95) * 1.25 / 50) * 50` milliseconds (25% margin, rounded up to 50 ms). Record raw runner p95s, runner class/browser version and resulting ceiling in the M4 benchmark doc. Never raise it because a later implementation fails. If runner variance exceeds the margin, investigate the fixture and concurrency before finalizing a stable ceiling.

Normal CI requires a ceiling and asserts p95≤ceiling; local report independently evaluates ≤1500 ms. Remove calibration from final CI environment. Modify only the e2e job name/artifact step as needed: it already runs `just e2e`, which discovers every `.spec.ts`; no separate fake benchmark job. Upload performance/a11y/screenshots artifacts with `if: always()` using the currently pinned upload-artifact action, including `test-results/` on passing runs and failure-report on failures. Retain finite retention. These edits do not trigger a publish/deploy task; do not touch image/release jobs.

```bash
rtk just test ./internal/cmd/e2eseed -count=1
rtk proxy sh -c 'cd ui/host && bun run test && bun run lint'
rtk just ui
```

Controller only:

```bash
rtk proxy env FANOUT_E2E_CALIBRATE=1 just e2e
rtk just e2e
```

Codex writes/typechecks the test but launches no browser. Runner calibration, local 1.5s verdict, network counts and real paint remain controller evidence.

## Task 8: Enforce AA contrast, validate six categorical colors, and capture both surfaces

**Files:** Modify `ui/host/package.json`, `ui/host/bun.lock`, `ui/host/e2e/smoke.spec.ts` to share its existing mountApp helper; Create `ui/host/e2e/chat-harness.ts`, `ui/host/e2e/accessibility.spec.ts`, `ui/host/e2e/screenshots.spec.ts`, `ui/host/src/dashboards/palette-accessibility.test.ts`, `scripts/validate-palette.ts`, `scripts/validate-palette.test.ts`. Fix only demonstrated failures in `ui/tokens.ts`, `ui/theme.ts`, `ui/panels/style.ts`, existing panel/inspect/table/map files and their owning tests. **Budget: $0.** Closes S10/S9/S13 visual evidence.

- [ ] **Step 1: Add the pinned test dependency and failing browser assertions**

Controller dependency preflight: install exact `axe-core@4.10.3` as a host devDependency with the single Bun lockfile. A denied/unavailable install is BLOCKED with the exact command, not a reason to fetch another version. No runtime bundle import. Browser test imports `axe-core` only to locate/inject its pinned source, then runs WCAG2A/AA + WCAG21A/AA rules including `color-contrast`, with no disable/exception list. In each theme and width, scroll/mount each of the 15 dashboard types, scan the visible dashboard controls and each card, then scan Data/Spec/menu/history/full-screen states as applicable. Test report attaches violations and incomplete results; unresolved color-contrast incomplete needs a computed-style proof or controller review, not silent exclusion.

```bash
rtk proxy sh -c 'cd ui/host && bun add --dev --exact axe-core@4.10.3'
```

Extract existing `mountApp(page,html,fragment,theme)` from smoke unchanged into chat-harness; no exported production test-only API. Scan actual **iframe content**, not just its parent. Use the current generic app HTML and `tests/go-fragments/*.json`; these are explicitly fixtures. For all fifteen chat types, obtain a real same-spec/result panel fragment from the e2e server `/api/panels/query` and feed it through that existing bridge fixture (no hand-written frames), then scan/capture its iframe. Controller collector additionally validates live persisted chat integration on replayed data in Task 11. The iframe's opaque-origin sandbox remains `allow-scripts`; do not add allow-same-origin for axe. Playwright's frame execution can inject the local axe source in the test environment without changing product CSP.

Complete scan core for the new Playwright spec (helpers are new test-only; Frame/Page types imported from @playwright/test):

```ts
import axe from 'axe-core';
import {expect,type Frame,type Page,type TestInfo} from '@playwright/test';
async function scan(surface:Frame|Page, info:TestInfo, label:string) {
  await surface.evaluate(source => {
    const script=document.createElement('script'); script.textContent=source;
    document.documentElement.append(script);
  },axe.source);
  const report=await surface.evaluate(async()=>{
    const api=(window as typeof window & {axe:typeof axe}).axe;
    return api.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21a','wcag21aa']}});
  });
  await info.attach(`${label}-axe.json`,{body:JSON.stringify(report,null,2),contentType:'application/json'});
  expect(report.violations,`${label}: WCAG AA violations`).toEqual([]);
  const unresolved=report.incomplete.filter(item=>item.id==='color-contrast');
  expect(unresolved,`${label}: unresolved contrast needs computed-style evidence`).toEqual([]);
}
```

Use test-aware injection compatible with the fixture's CSP; if inline source injection is blocked, instrument the test harness document before product app mounting or use the pinned axe execution API in the isolated test frame. Do not modify product CSP to enable testing. Prove both dashboard and iframe theme match the requested `data-mantine-color-scheme`; no theme inferred just from project label.

- [ ] **Step 2: Add an actual CVD validator and negative fixtures**

The exact six arrays are in `ui/tokens.ts:series`; existing tests only pin them. Implement `scripts/validate-palette.ts` as test/tool code with a published, fixed full-severity CVD transform (Machado 2009 protanopia, deuteranopia and tritanopia), linear-sRGB conversion and a perceptual distance metric. Record its matrices/metric, source citation and acceptance threshold in the validator comments; use controller/vendor source verification at implementation, no guessed “approved” threshold. Preserve matrix constants and deterministic clamp/gamut handling in checked-in code; no browser dependency. It must return every failing `(theme,simulation,slot pair,distance)` rather than a single palette-equals-fixture boolean. Distinguish CVD separation from WCAG foreground/background contrast.

Proposed **new** interface:

```ts
export type PaletteFailure={theme:'light'|'dark';simulation:string;a:number;b:number;distance:number};
export function validatePalette(palettes:{light:readonly string[];dark:readonly string[]}):PaletteFailure[];
```

Complete behavior test consuming that new implementation:

```ts
import {expect,it} from 'bun:test';
import {validatePalette} from './validate-palette';
import {series} from '../ui/tokens';
it('validates every categorical pair in both themes under all three CVD simulations',()=>{
  expect(series.light).toHaveLength(6); expect(series.dark).toHaveLength(6);
  expect(validatePalette(series)).toEqual([]);
});
it('rejects identical and near-identical slots instead of accepting a pinned array',()=>{
  for(const replacement of ['#2a78d6','#2a79d6']){
    const invalid={light:[...series.light],dark:[...series.dark]};
    invalid.light[1]=replacement;
    expect(validatePalette(invalid).some(f=>f.theme==='light'&&f.a===0&&f.b===1)).toBe(true);
  }
});
```

Use the same pure validator from the host palette test (test-only import, never runtime) or run it through existing `script-tests`; ensure both themes and all 15 pairs×3 simulations execute in CI. Test known red/green confusion, grayscale pairs, malformed hex and threshold-edge inputs. Do not lower the threshold to make current hues pass. If six slots fail, correct the smallest affected token(s), rerun all affected renderers, arrays and contrast tests. The brief requires categorical CVD checks; Other/status colors retain labels/shapes and their separate contrast tests.

- [ ] **Step 3: Check canvas ink and capture the review matrix**

Extend existing `chart-units.test.ts`, theme/row-formatting tests to assert text≥4.5:1 (3:1 only for genuinely large text) and visible non-text marks/boundaries≥3:1, including annotation labels, threshold text, end labels, legends, state/map labels and dark/light surface colors. Existing markStyle/lineStyle outlines do not prove text contrast. Resolve translucent backgrounds by compositing against actual surface, not treating rgba as an opaque hex. Axe covers browser text; compiler tests cover canvas paint; screenshots cover layout/meaning.

Screenshots spec captures every type × two themes × two widths for dashboard **and chat**, after fonts/render settle. Use `test.info().outputPath` and attach PNGs plus a manifest with surface, theme, width, viz, panel ID and query status. Capture menus/Data for long-error reachability and representative empty/stale/truncated states separately. Artifacts are uploaded even on pass; no committed pixel baselines, since font/engine antialiasing and replay timestamps vary and the criterion is a controller review, not pixel identity. No screenshot can be PASS until reviewed.

Controller reviews labels, clipping, legends, focus, contrast, state cues and every failed axe/CVD result. Each failure gets a small failing owning test, smallest token/clamp/option/revert correction and a rerun of its affected matrix. No redesign task. Shared rendering fixes require both embed outputs.

```bash
rtk proxy bun test scripts/validate-palette.test.ts
rtk proxy sh -c 'cd ui/host && bun run test && bun run lint'
rtk just ui
rtk just script-tests
```

Controller: `rtk just e2e`. Record axe version/rules, CVD method/pair minima, canvas contrast minima, artifact counts and review outcomes. Do not cite M3's 30/30 coverage as new AA evidence.

## Task 9: Promote intent scoring and optional dual judging without breaking the cost ledger

**Files:** Modify `scripts/dashboard-eval/{main.ts,score.ts,transport.ts,mock.ts,README.md,main.test.ts,score.test.ts,transport.test.ts,regressions.test.ts}`; Create `intent.ts`, `intent.test.ts`, `judge.ts`, `judge.test.ts`, `judge-mock.ts`. Read only private `.superpowers/eval/{eval2.ts,judge.ts,mock2.ts,report.ts,README.md}` sources; never holdout data/artifacts. `justfile:script-tests` already covers the directory. **Budget: $0; mocks only.** Closes S1–S5/S13 scoring/evidence gap.

- [ ] **Step 1: Add explicit prompt intent and keep benchmark rules strict**

New `Prompt` is `{id:string;prompt:string;expect:'dashboard'|'answer';rationale:string}`. Benchmark inputs require exactly ten unique prompts all expecting dashboard plus the existing five exact edits. Holdout accepts any positive number with validated explicit expect/rationale and does **not** require/read edits or run them. Add explicit **new** `--set parity` for exactly two dashboard intents/no edits, with IDs `checkout_incident` and `payments_database`; use `scoreIntent` and label S5 N/A. Change required CLI options by set; reject an edits-file on holdout/parity instead of silently ignoring it. Extend parseOptions' set allowlist/help and validateInputs with the set argument. `validateOutput` applies the existing private benchmark output rules to parity, and the existing sealed-filename refusal applies to every non-holdout set. Holdout alone requires the frozen sealed hash/external output. No legacy absent-expect fallback: controller updates its benchmark input once. Preserve frozen full SHA checking before decoding sealed input, ledger/source-hash guards and set identity; never enumerate holdout artifacts in candidateSourceHash. Synthetic option tests reject benchmark-sized/answer-containing parity inputs, parity with edits, and holdout inputs under either nonsealed set.

Add assistant text collection to `readSSE` using TEXT_MESSAGE_START/CONTENT IDs and the final assistant message; nonblank prose with a missing/error terminal does not count as an answer. Add `mutation_observed:boolean` and `mutation_evidence_complete:boolean` from the **existing generated `mutatingTools` catalog**, not a second hard-coded list. A successful create/edit/replace/restore, ambiguous persisted error flag or unavailable thread/mutation evidence prevents answer success. `findSaved===null` alone is insufficient: multiple saved boards are ambiguous rather than “no dashboard”. Preserve persisted error flags, exact immutable saved record/version checks and all usage records.

Complete `intent.ts` scoring core (new types and functions):

```ts
import {median,panelPass,type Run} from './score';
export type IntentRun=Run & {expect:'dashboard'|'answer';final_text:string;
  mutation_observed:boolean;mutation_evidence_complete:boolean;incomplete:boolean};
export function answerPass(r:IntentRun):boolean {
  return r.complete&&!r.incomplete&&Boolean(r.final_text.trim())&&!r.saved&&
    !r.mutation_observed&&r.mutation_evidence_complete;
}
export function scoreIntent(runs:IntentRun[],expected_count:number){
  const complete=runs.length===expected_count&&expected_count>0&&runs.every(r=>r.complete&&!r.incomplete);
  const dashboards=runs.filter(r=>r.expect==='dashboard'),answers=runs.filter(r=>r.expect==='answer');
  const saved=dashboards.filter(r=>r.saved&&r.checked&&r.valid&&r.panels.length>0);
  const correct_answers=answers.filter(answerPass).length;
  const total=saved.reduce((n,r)=>n+r.panels.length,0);
  const good=saved.reduce((n,r)=>n+r.panels.filter(p=>panelPass(p,r.checks)).length,0);
  const correct_outcomes=saved.length+correct_answers;
  const times=saved.map(r=>r.elapsed_ms);
  const latency=times.every(t=>t!==null&&Number.isFinite(t)&&t>=0)?median(times as number[]):null;
  const fully_checked=dashboards.every(r=>r.saved&&r.checked&&r.valid&&r.panels.length>0&&
    r.checks.length===r.panels.length&&new Set(r.checks.map(c=>c.id)).size===r.panels.length);
  return {complete,dashboard_prompts:dashboards.length,answer_prompts:answers.length,
    correct_outcomes,dashboard_rate:dashboards.length?saved.length/dashboards.length:null,
    answer_rate:answers.length?correct_answers/answers.length:null,
    intent_accuracy:runs.length?correct_outcomes/runs.length:null,
    good,total,median_ms:latency,s5:null,
    passed:complete&&correct_outcomes===expected_count&&fully_checked&&good===total};
}
```

Complete test using only synthetic, unsealed prompts/outcomes:

```ts
import {expect,it} from 'bun:test';
import {answerPass,scoreIntent,type IntentRun} from './intent';
const answer=():IntentRun=>({expect:'answer',final_text:'p95 is a percentile.',complete:true,
  incomplete:false,saved:false,checked:false,valid:false,elapsed_ms:null,panels:[],checks:[],
  mutation_observed:false,mutation_evidence_complete:true});
it('scores an all-answer set without requiring saves, panels or edits',()=>{
  expect(scoreIntent([answer()],1)).toMatchObject({passed:true,answer_rate:1,dashboard_rate:null,s5:null});
});
it('rejects unintended mutations, blank text, failures and unknown mutation evidence',()=>{
  for(const patch of [{saved:true},{mutation_observed:true},{final_text:' '},{complete:false},
    {incomplete:true},{mutation_evidence_complete:false}])expect(answerPass({...answer(),...patch})).toBe(false);
});
it('does not score a partial set as complete',()=>{
  expect(scoreIntent([answer()],2).passed).toBe(false);
});
```

Add mixed synthetic fixture with 11 dashboard intents and 5 answers (counts only mimic the documented holdout shape, **no sealed strings**); inject unintended create/replace/restore, two saves, HTTP/thread failure, blank response, missing terminal and duplicate/missing panel check. Every prompt, including answers and failed saves, goes through beginPrompt/recordUsage/settlePrompt. Benchmark still calls existing strict `score`, preserving S4≤45000 and all five exact edits. Holdout reports intent outcomes, S2/S3 checked denominators and median save time without imposing benchmark-only all-dashboard/S5 rules. Do not weaken `panelPass`'s authored explained-empty requirement to the older private runner's looser rule.

- [ ] **Step 2: Retain compact evidence for judging in controller-only storage**

Extend executePanels with bounded column/role/unit/totals and first-five-row samples for the judge, preserving Task 4 error. Never serialize unredacted physical logs. Use the saved immutable spec and observed final question, not a later latest spec. For benchmark this data may stay private under `.superpowers/eval`; for holdout the in-memory/private judge input must stay under the external controller-only output root. `publicEvidence` remains a whitelist: no prompt text, rationale, answer text, spec, rows, tool input, individual error or judge rationale. Add publicEvidence leakage tests with sentinel strings in every newly added field. Public reports include cost and aggregate judge coverage only.

- [ ] **Step 3: Promote the optional two-model judge and meter every attempt**

Read the private judge's unchanged RUBRIC, four criteria, structured JSON schemas, byte-identical input rule, disagreement≥2, independent scoring and token normalization. Promote its behavior into the checked-in module; remove imports of private eval/mock files. No copying its legacy compatibility reader, fixed private paths, unmetered retry loop or “missing keys throws after writing artifacts” behavior.

Proposed **new** exports:

```ts
export type JudgeState={status:'skipped'|'complete'|'incomplete';reason?:string;records:unknown[];cost_usd:number|null};
export type JudgeConfig={
  env:Record<string,string|undefined>;
  endpoints:{anthropic:string;openai:string};
  ledger:Ledger;cap_usd:number;persist_ledger:()=>void;
  fetch:(url:string,init:RequestInit)=>Promise<Response>;
  evidence:Record<string,unknown>[];
};
export async function runJudges(config:JudgeConfig):Promise<JudgeState>;
```

Import Ledger from transport; these are proposed interfaces, with the provider implementation ported from the observed private judge and its metering replaced as below. `JudgeConfig` explicitly injects runtime env/key availability, endpoints, ledger/cap/persist callback, a fetch transport and prepared evidence. Key values only go into request headers, never config JSON/artifacts/logs. Missing **either** key returns `{status:'skipped',reason:'judge_keys_unconfigured',records:[],cost_usd:0}` **before any request or output allocation**; no partial single-judge quality score. The machine's missing OpenAI key therefore skips cleanly. Complete missing-key regression for `judge.test.ts`:

```ts
import {expect,it} from 'bun:test';
import {runJudges} from './judge';
it('skips both judges before transport or ledger changes when either key is missing',async()=>{
  for(const env of [{},{JUDGE_ANTHROPIC_KEY:'mock-only'},{JUDGE_OPENAI_KEY:'mock-only'}]){
    let requests=0,persisted=0;
    const ledger={schema:1 as const,rates:[],calls:[],prompts:[]};
    const result=await runJudges({env,endpoints:{anthropic:'http://localhost',openai:'http://localhost'},
      ledger,cap_usd:5,persist_ledger:()=>{persisted++;},
      fetch:async()=>{requests++;throw new Error('Transport must not run');},
      evidence:[{question:'Synthetic unsealed fixture',spec:{panels:[]}}]});
    expect(result).toEqual({status:'skipped',reason:'judge_keys_unconfigured',records:[],cost_usd:0});
    expect(requests).toBe(0);expect(persisted).toBe(0);
    expect(ledger.calls).toEqual([]);expect(ledger.prompts).toEqual([]);
  }
});
```

Retain the private rubric and original judge identities `claude-fable-5-1` / `gpt-5.6-terra` as evaluation-only models; the brief's Sonnet/Sol ruling controls the authoring agent. Record these separately, never treat a judge as the default agent or use Haiku. If the controller's configured judge identity differs, freeze and report it before comparison; do not silently substitute a model.

Use the existing Ledger Rate/CallUsage machinery for judge provider input/output/cache token usage and every retry with a unique `(run_id,step)`; normalize OpenAI reasoning as an output subset. Debit successful response usage **before** validating rubric JSON, so truncation/refusal/malformed output remains spent. HTTP/transport failures lacking billable usage leave an unsettled reservation and stop further paid calls pending controller settlement; they are not free retries. For a known nonbillable retryable error, only retry within both deadline and conservative task reserve; use bounded retry counts as in the private runner. Both judges receive identical serialized evidence and cannot see each other's result. All combined fields stay null unless both valid scores exist; independent judge failure is visible.

Add CLI `--judge` (explicit) and `--mock` judge path using dummy headers and loopback-only endpoints. Main runs judges after agent scoring with the **same task ledger**, not a fresh budget. Absent judge flag also returns an explicit skipped/not_requested summary. A clean optional skip does not fail otherwise conclusive S1–S5/intent scoring, and is never a quality PASS.

Mock tests assert endpoint/request-schema/model IDs, identical inputs, skipped path zero calls, retries/reservations, malformed/truncated/refusal response usage charged, temperature retry, cache accounting, one judge failure, score/rationale schema, means/disagreement and no external endpoint/secret leakage. Add integration test through main with synthetic intents and fake usage. All tests are offline $0.

- [ ] **Step 4: Update runner docs and verification**

Document set-specific inputs/denominators, clean skip, separate agent/judge identities, shared actual ledger, abort/unknown-cost semantics, external sealed output and exit codes 0 pass/1 measured failure/2 incomplete. Delete superseded checked-in fixed-ten holdout logic; the untracked old tools can remain as historical sources but are not required to run current evals. Do not read or amend sealed prompt files.

```bash
rtk proxy bun test scripts/dashboard-eval
rtk proxy bun scripts/dashboard-eval/main.ts --mock --no-output
rtk just script-tests
rtk just dashboard-eval-generate-check
rtk just fmt
```

Report mock injection counts, explicit missing-key skip and $0 spend. Paid holdout/judge run is controller-only and optional within Task 11's ledger; no paid test during promotion.

## Task 10: Document the shipped agent-built dashboard workflow

**Files:** Create `site/src/content/docs/guides/build-dashboards.mdx`; modify `site/astro.config.mjs` Guides sidebar, `site/src/content/docs/guides/connect-over-mcp.mdx` with one link, `docs/testing.md` with performance/a11y/benchmark instructions, `site/src/content/docs/status/capabilities.mdx` only to reflect measured capabilities. Generated `reference/http-routes.mdx`, `mcp-tools.mdx`, `roles.mdx` remain generator-owned. **Budget: $0.** Closes S13 documentation gap; no product UI work.

- [ ] **Step 1: Inventory shipped controls and write the guide**

Use actual `page.tsx`, `toolbar.tsx`, `variable-bar.tsx`, `search.ts`, `panel-card.tsx`, `history.tsx`, `shortcuts.ts`, `chart-keyboard.tsx`, MCP registrations and generated tool descriptions. Document:

1. Ask chat to build a dashboard, schema→preview→correction→save receipt, open its link; follow-up edit names a panel and preserves others. Explain is answer-only; “Ask Fanout to fix it” is separate and manager-only.
2. All fifteen visualization types and their use; default whole-window stat/gauge semantics, units and explained-empty/error Data inspection. Log patterns include dominant severity/top service; SQL escape hatch is guarded, not a SQL styling editor.
3. Relative/absolute ranges, refresh, query/custom/constant/text variables, All versus None, dependent options, comparison and per-panel overrides. Sharing copies time/variables/comparison/focused panel/drill URL; recipient needs account/dashboard access, not a public snapshot.
4. Saved grids/layout edits, immutable version history and restore as a **new** version, owner scope and optimistic edit conflict behavior.
5. Shortcuts r/e/h/f/?, global chat /, Escape, chart arrows/Enter/range behavior as observed; input/modifier/modal exceptions. Use keyboard help's actual labels.
6. MCP schema/preview/query/create/replace/edit/get/list/version/restore tools, permissions and link to **generated** reference. No invented delete MCP tool or retired observability endpoint.

Complete guide frontmatter and opening copy:

```mdx
---
title: Build and refine dashboards
description: Build a telemetry dashboard from chat, refine its panels, and share the current view.
---

Open chat and describe the system view you need. For example: “Build a dashboard of checkout latency and errors by service.” Fanout reads the telemetry schema, previews the panels and saves a dashboard. The save receipt links to the saved version and reports panels that need attention.

To refine the result, name the panel and the change: “On the checkout latency panel, add a warning threshold at 500 ms.” Use the receipt's changes and version history to inspect what changed.
```

Write remaining sections from the inventory above, not historical spec promises. Examples stay generic, no demo hosts/IDs. Do not claim every query is cached or the performance target is guaranteed at arbitrary scale. Explain exact routing limitations only in testing/performance docs, not as a new product authoring choice.

- [ ] **Step 2: Check docs without hand-editing generated references**

```bash
rtk just docs-generate
rtk just docs-generate-check
rtk just test ./cmd/fanout-docgen
rtk just fmt
```

Controller runs the installed site's `bun run build` from `site/` (its script invokes astro check/build and table/social/canonical guards); `just site-build` may install npm dependencies and remains controller-owned. Verify internal links/sidebar through the site build. No publication or deployment. For this prose-only task, use documentation and site checks, not tests that merely compare new wording to itself; dispatch full gates still apply as required.

## Task 11: Controller-run final measurements and S13 side-by-side parity

**Files:** Finalize `docs/benchmarks/2026-10-agent-dashboards-m4.md`. Private artifacts under controller-supplied browser/eval output locations; holdout artifacts outside the worktree. Product fixes require their own failing behavior regression in the owning task and an affected rerun; this is not a tuning task. **Budget: ≤$5 total shared across all agent/judge/parity calls.** Closes fresh S1–S8/S10–S13 evidence.

- [ ] **Step 1: Run individual gates, freeze the candidate and prepare local replay**

Run the Global Constraints gates, native SQL suites, Go benchmark, mocked intent/judge tests and collector source tests. The checked-in smoke, performance, a11y and screenshots suite is controller-run `rtk just e2e`. Controller reviews/commits; Codex performs no git writes. Record frozen source/build identity, engine version, platform/cap/threads, exact versions of Chrome/Playwright/axe and dataset manifest. No release or deploy. Production and demo stay unchanged.

Use the existing immutable demo source/replay tooling under `.superpowers/replay` in a disposable **local** instance, fresh control state and one recorded signed shift shared across signals so latest events end near run start. Observed converter interfaces are `main.go`'s `-in`, `-signals traces,logs,metrics`, `-shift-ns`, `-dry-run`, and runtime `FANOUT_REPLAY_TOKEN`; `mapping.go:row.metric` and `batch.go` handle gauge/sum/histogram points. Record source/replay-tool hash, original/resulting event-time bounds, row/file/byte totals, namespace and freshness. Both parity boards require pool metrics as well as spans/logs: replay all three signals and verify actual metric names/typed attributes before agent calls. Missing pool metrics make that capability unproven/FAIL; omission is not a full parity PASS. Replay older-format immutable input only through this controller-owned offline OTLP converter as M3 did; never add a product legacy reader. No new demo snapshot copy or remote host access is required. If source is unavailable, report BLOCKED/incomplete rather than substituting generated CI telemetry for preview parity.

- [ ] **Step 2: Run collector and genuine both-theme review**

Existing observed helpers are `.superpowers/replay/m2-browser.mjs`, `m2-browser.test.mjs`, `m3-receipts.test.mjs`, `m3-history.test.mjs`, `m3-fullscreen.test.mjs`, `build-audit.sh`; browser contract guards are `ui/host/tests/browser-evidence.ts` and `ui/host/src/dashboards/browser-evidence.test.ts`. Do not reference a guessed `m3-browser.test.mjs`. The audit helper requires a clean committed source, stages a copy and keeps production assets untouched. Controller supplies audit binary/output privately.

```bash
rtk proxy node --check .superpowers/replay/m2-browser.mjs
rtk proxy node --test .superpowers/replay/m2-browser.test.mjs .superpowers/replay/m3-receipts.test.mjs .superpowers/replay/m3-history.test.mjs .superpowers/replay/m3-fullscreen.test.mjs
```

Controller only, with its existing nonempty FANOUT_BROWSER_BASE/COOKIES/OUT/MODULE environment:

```bash
rtk proxy node .superpowers/replay/m2-browser.mjs --theme both --deploys --headed --classic-scrollbars --audit
rtk just e2e
```

Run full collector coverage, not `--only`; preserve all M2/M3 interaction checks, 15×2 type/theme rows, live chat/dashboard parity, bounded frames and three refresh cycles per theme. Collector exit 0 means written output, not PASS: require observed=true/passed=true for each required row. Review screenshots at both widths, include long-error Data detail, refresh recovery and zero console/CSP failures; real-data network/query checks corroborate the panels. Record automated axe/CVD and screenshots from Task 8 separately from the live collector. Production smoke must pass without DEV audit hooks. No scripted tour or narration substitutes for tests.

- [ ] **Step 3: Measure S6/S7 on real data and rerun the 4 GB backlog**

Run the same 12-panel 24-hour fixture/spec against the replay, with measured data-specific grouping values if needed, preserving its raw/routed mix and full-render method. Record local ≥20 samples and p95 versus **1500 ms**; report CI ceiling/margin and runner p95 separately. Run `BenchmarkPanelQueries24Hours` in controller demo-source mode, ≥100 warm samples/shape, comparing raw/routed over the identical pinned data; compare every relevant p95 to **500 ms**. Repeat the frozen Task 2 pressure workload at **4GB**, concurrent panels, version and detector annotation writes. Report errors and exact eventual counts, progress and peak memory; defaults with a larger cap do not close the cap regression.

- [ ] **Step 4: Run the benchmark and both parity prompts under one ledger**

Reserve approximately **$2 for the ten-create/five-edit benchmark, $1 for two parity prompts, $2 contingency**; these are planning envelopes, not fixed costs. Admission remains actual shared cost/p95 plus completion reserve. Missing usage stops further spend. Sonnet is the required default-agent run; no paid Sol comparison required. Do not spend merely because funds remain.

```bash
rtk proxy bun scripts/dashboard-eval/main.ts --base "$FANOUT_EVAL_BASE" --cookies "$FANOUT_EVAL_COOKIES" --model-label claude-sonnet-5-5 --prompts-file "$FANOUT_BENCHMARK_PROMPTS" --edits-file "$FANOUT_EDIT_EXPECTATIONS" --snapshot-manifest "$FANOUT_SNAPSHOT_MANIFEST" --cost-ledger "$FANOUT_EVAL_COST_LEDGER" --budget-usd 5 --out .superpowers/eval/m4-final --set benchmark
```

Require one frozen-candidate run: S1=10/10, S2=100% rows/text or authored explained-empty, S3=0 final invalid, S4 median≤45s, S5=5/5 consecutive exact edits. Record first/scored save timings, incomplete/mutation/validation evidence and actual cost; don't merge favorable prompts across runs. Performance and robustness failures cannot be explained away as valid authoring.

The actual capability preview is **private `.superpowers/m2env/preview.html`**, `BOARDS` entries named `Checkout latency incident` / `Payments database pressure` (their runtime IDs are `incident` / `database`). The checked-in landing `site/src/components/landing/Preview.astro` is a different service-health illustration. Freeze the private preview's hash and capture its two board states in both themes. Its observed authoring prompts are:

- “Checkout got slow after today's deploy. Build me something to find out why.”
- “Which database queries drive payments' tail latency, and does it line up with pool exhaustion?”

Use these exact prompts as two explicit dashboard-intent private inputs, with nonsealed IDs/rationales described in Task 9. Do not copy the preview's obsolete proposed spec syntax into product specs. Run the promoted runner's **new** parity set through the same retained ledger and immutable-save checks:

```bash
rtk proxy bun scripts/dashboard-eval/main.ts --base "$FANOUT_EVAL_BASE" --cookies "$FANOUT_EVAL_COOKIES" --model-label claude-sonnet-5-5 --prompts-file "$FANOUT_PARITY_PROMPTS" --snapshot-manifest "$FANOUT_SNAPSHOT_MANIFEST" --cost-ledger "$FANOUT_EVAL_COST_LEDGER" --budget-usd 5 --out .superpowers/eval/m4-parity --set parity
```

No edits-file; no benchmark/holdout fallback or separate $5 reset. Do not use the old unmetered private runner or bypass usage admission. Missing private preview is BLOCKED and must not be replaced by the unrelated landing illustration.

Rebuild from prompts on replayed data, not by importing/copying preview panel specs. Collect saved specs and every panel's real check. Capture both boards in light/dark, at preview-comparable widths and time/variable states; side by side with the preview. Compare heatmap, contextual log patterns, deploy markers/anomaly bands, paired before/since bars, database/cache detail, trace/log drill and table formats. The preview's error-budget board is explicitly N/A, SLO out of scope. If replay lacks version changes/anomalies, label a small synthetic injection separately and preserve provenance; do not silently call it original demo data. Semantic parity is capabilities/behavior, not numerically identical fixture values or exact pixel match. Each missing capability is FAIL with panel/source evidence and owning correction, not excused by a pretty screenshot.

- [ ] **Step 5: Optional sealed holdout/judge, with clean skips**

After all mandatory benchmark/parity work fits, and only within the **remaining same $5 ledger**, controller may run the sealed set once through the promoted intent-aware runner using a verified full SHA and external output/ledger. Omit edits-file; supply `--holdout-sha "$FANOUT_HOLDOUT_SHA" --set holdout --out "$FANOUT_HOLDOUT_OUT_ROOT/m4-final"`. Codex never opens sealed inputs or run-level failures. No tuning/rerun based on holdout results. If budget cannot fit the entire set, record unrun-budget; don't score an early subset as the full holdout.

Judge needs both configured keys and spare reserve. Here `JUDGE_OPENAI_KEY` is absent, so expect **SKIPPED judge_keys_unconfigured**, zero judge calls and no quality PASS. The mandatory two-model request/score/usage behavior was tested with mocks in Task 9. No new credential retrieval/install request is required for this plan. If the controller later injects both keys, `--judge` charges the same ledger and records distinct author/judge identities and paired quality coverage. Optional skip does not remove required S13 human side-by-side review.

- [ ] **Step 6: Publish measured verdicts and controller handoff**

M4 benchmark document has one S1–S13 table with literal targets, measured values, candidate/source identity, exact guard/command/artifact references and **PASS/FAIL** per completed criterion. Unrun/inconclusive/blocking evidence is labelled PENDING/BLOCKED and prevents a claim of all-criteria completion. Include per-prompt benchmark metrics and five edit results, raw/routed shape timings, all memory errors/progress before/after, local vs CI S6, S8 requests/variable behavior, AA/CVD method/results, screenshot-review matrix, two-board S13 capability comparison, optional holdout/judge skip/cost and limitations. Verify retirement again and documentation generation. No credentials, host details, raw/sealed telemetry or local home paths.

At the task cap, preserve FAIL/incomplete and name the next correction; controller obtains authorization for more paid verification. A final report does not trigger release, deploy, demo reseed or git write by Codex.
