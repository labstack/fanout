# Agent dashboards Milestone 4 — evidence audit

Task 1 audit, 2026-10-09. Source: `82ab72e9629475af52aadb337b63d91bc61138a9`,
branch `feat/agent-dashboards-m4`. This document initializes the evidence for
the M4 candidate; it does not record final acceptance. All S1–S13 verdicts are
**PENDING** until the owning tasks supply candidate-specific measurements.
Task 1 made no product changes and spent **$0**.

Requirements: [design spec](../superpowers/specs/2026-10-04-agent-dashboards-design.md)
and [M4 plan](../superpowers/plans/2026-10-09-agent-dashboards-m4.md), subject to
the controller's binding rulings. Historical baselines are
[M1](2026-10-agent-dashboards-m1.md),
[M2](2026-10-agent-dashboards-m2.md) and
[M3](2026-10-agent-dashboards-m3.md).
Their measurements, screenshots and PASS labels do not establish an M4 PASS.

## S1–S13 baseline and required evidence

Guard names below were read from the dispatch source. Go and host unit guards
run through the CI gate's `just check`; eval guards run through `script-tests`.
The separate Browser smoke job runs `just e2e`. Task 1 runs the individual
dispatch gates listed below; it does not run browsers or providers.

| Criterion and literal target | Historical evidence | Current guards and CI coverage | Gap, owner and required M4 measurement | M4 verdict |
|---|---|---|---|---|
| **S1:** Benchmark prompts produce a saved dashboard, **10 of 10** | M3 final: 10/10, authoring candidate `eee385d8`. | `scripts/dashboard-eval/main.test.ts`: `runs ten creates and five consecutive edits through loopback HTTP and the scorer`; `score.test.ts`: `requires all ten saves and all five consecutive edits`. CI uses loopback mocks, not a live model. | Task 9 promotes set-specific intent scoring. Task 11 runs all ten benchmark prompts on one frozen candidate with the default `claude-sonnet-5-5`, retained ledger and immutable save evidence. | **PENDING** |
| **S2:** Panels return rows, or carry an explained empty state, at save time, **100%** | M3 final: 67/67. M3 baseline: 56/59 under a 4 GB backlog cap; the successful iteration used default memory sizing. | `score.ts:panelPass`; `score.test.ts`: `never accepts unchecked, unexplained or duplicate panel evidence`; `regressions.test.ts`: `scores executor text results without inventing a frame`; `internal/panel/exec_test.go`: `TestRunProducesFrames`, `TestRunExplainsEmptyPanels`, `TestRunIsolatesPanelErrors`. MCP saved receipts already carry panel errors. Deterministic Go/mock CI coverage. | Tasks 2–3 diagnose and correct capped-backlog failures; Task 4 preserves complete sanitized errors and eval checks. Task 11 checks every saved panel, including text and authored explained-empty states, and reruns the 4 GB workload. Default sizing does not close the cap defect. | **PENDING** |
| **S3:** Specs fail validation after the agent's own correction loop, **0** | M3 final: 0. | `internal/panel/check_test.go`: `TestCheckCollectsFiltersAndSQLProblems`, `TestCheckReturnsOperationalErrorsNotProblems`, `TestSQLCTEsCannotShadowTelemetry`; `validate_test.go`: `TestValidateReportsPathsAndHints`; `exec_test.go`: `TestRunRejectsInvalidSpecs`; `regressions.test.ts`: `requires complete runs and counts only checked validation failures`. Go/mock CI coverage. | Task 5 accepts schema-qualified log columns through the scoped AST while preserving redaction and relation boundaries. `internal/panel/log_redaction_test.go:TestQualifiedLogColumnsFailClosed` currently asserts safe rejection, not successful execution. Task 11 requires checked final validation for every save. | **PENDING** |
| **S4:** Median prompt to saved dashboard, default model, **≤ 45 s** | M3 final: 18.7 s; earlier M3 runs: 15.5 and 19.9 s. These are individual runs. | `score.test.ts`: `fails missing latency and measures a true even-sized median`; `regressions.test.ts`: `times the last scored save and preserves first-save timing`, `does not pass save or latency evidence when only the tenth prompt truncates`. CI tests timing semantics without provider latency. | Task 11 records first and scored save timings and the ten-prompt median on fresh replay with the required default model. No prompt tuning is planned. | **PENDING** |
| **S5:** Only named panels change over **5 consecutive edits, 100%** | M3 final: 5/5. | `score.ts:compareEdit`; `main.test.ts` ten-create/five-edit loopback guard; `score.test.ts`: `rejects title no-ops, unrelated grids, order, metadata and authored changes`; `regressions.test.ts`: `keeps real edit receipts for authored fields, order and packed layout`. Deterministic CI coverage. | No feature gap identified. Task 11 repeats title, threshold, add, remove and unit edits consecutively against immutable versions; unrelated authored fields and unauthorized layout changes must remain exact. | **PENDING** |
| **S6:** First full render, **12 panels / 24 hours, ≤ 1.5 s p95** | M1: 397 ms p95 over six local loads. M2 also records 1.6–2.1 s refreshes in a fresher window; those refreshes are not the S6 measurement. | `ui/host/e2e/smoke.spec.ts`: `all fifteen dashboard panels settle with valid geometry`. CI smoke covers rendering/geometry at two widths and both themes; it has no twelve-panel latency assertion. | Task 7 adds an actual full-render performance guard and calibrates a separate runner ceiling. Task 11 measures local real-data p95 with at least 20 samples against **1500 ms**. A looser CI ceiling cannot satisfy the local target. Tasks 2–3 also own backlog robustness. | **PENDING** |
| **S7:** Warm-cache panel query latency, **≤ 500 ms p95** | M1: 121 ms p95 over 60 panel queries. | `internal/observability/batch_reads_bench_test.go:TestCompletedReadBenchmark` is `readbench`-tagged, exercises Trace reads and is excluded from normal CI. No current 24-hour panel-executor benchmark establishes S7. | Task 6 measures the raw 24-hour panel shapes first; add narrow trace-count routing only if raw S7 fails, per R1. Task 11 repeats at least 100 warm samples per shape on replay and records raw/routed equivalence, p95 and engine/platform/memory settings. Tasks 2–3 own capped-backlog failures. | **PENDING** |
| **S8:** **1 batch + 1 annotations request per refresh; variable options once per range change** | M2: three cycles per theme, each 1+1 across 23 panels. | `use-panel-results.test.tsx`: `S8 integrates one annotation request into every 20-panel manual refresh`, `loads only missing visible panels after visibility changes during an in-flight batch`, `makes zero requests when scrolling between panels already loaded under the current key`, `skips timed and manual refreshes after visibility becomes known empty`. `page.test.tsx` and `use-variables.test.ts` cover variable readiness/selection. Host CI mocks request functions; smoke does not assert S8 network counts. | Task 7 adds real network counts for variables/range changes, background tabs, scrolling and manual refresh. Task 11 repeats live cycles in both themes. Preserve the shipped **POST** annotations contract; the spec's historical GET row does not authorize a route change. | **PENDING** |
| **S9:** **15 visualization types** listed in the spec | M2 and M3 collectors: 30/30 type/theme rows. | `internal/panel/validate_test.go:TestVizOrder` checks fifteen unique registered entries. `grid.test.tsx`: `renders every visualization and state`; the fifteen-panel smoke fixture provides browser CI coverage. | Already implemented and guarded; no feature work identified. Task 8 supplies fresh screenshots, and Task 11 corroborates the candidate in both themes and widths. Historical coverage is not copied into this verdict. | **PENDING** |
| **S10:** **WCAG AA in both themes; categorical palette passes CVD validation** | M2 visual review and M3 collector provide rendering history; neither is a fresh M4 automated AA/CVD result. | `theme.test.ts`: `clears AA on every semantic filled surface, in both schemes`; `chart-units.test.ts`: `all categorical marks have 3:1 fill or outline without changing palette, dark=%s`, `threshold/anomaly text stays readable over the warm tint, dark=%s`; `row-formatting.test.tsx`: `semantic table ink reaches 4.5:1, severity badge uses text tokens, dark=%s`; `series-palette.test.ts`: `pins the validator-approved palette in exact slot order`. CI has token/compiler assertions, no axe scan or checked-in CVD validator. | Task 8 measures all **six** ordered categorical slots in both themes, adds negative-tested CVD validation and pinned AA scans on dashboard/chat, and supplements canvas with contrast tests/screenshots. Exact palette arrays and the old test's “validator-approved” label do not prove CVD separation. Task 11 records the review method and results. | **PENDING** |
| **S11:** Shared link reproduces **time, variables, comparison and focused panel** | M3 fixed explicit empty multi-selection URL state; M2/M3 live drill/share history exists. | `router.test.ts`: `copies full-screen compare and drill links with exact instants and distinct variable selections`; `search.test.ts`: `round-trips All, empty, empty-string, one and several selections distinctly`, `validates URL options while preserving an explicit empty selection`; `page.test.tsx`: `keeps explicit None selected in the URL and outgoing request`. Host CI guards exact state. | Already guarded; no feature gap identified. Task 11 opens live shared URLs and verifies the captured window, distinct selections, comparison, focused panel and drill state. | **PENDING** |
| **S12:** #232 **items 1, 9, 12, 14, 17, 24, 30 resolved** | The spec describes the seven behaviors; M1–M3 document fixes and limitations. | Existing Go/UI behavior guards are mapped below. CI runs them under their actual names; they are not newly added issue-number regressions. Smoke provides partial rendering coverage. | Tasks 4–7 extend error visibility, qualified logs, query/performance and actual refresh coverage. Task 11 retains the seven behavior checks and live evidence, respecting stored grids. | **PENDING** |
| **S13:** **Every in-scope preview capability shipped; checkout-incident and payments-database boards rebuilt from prompts** | M2 capability table covers heatmaps, log patterns, deploys/anomalies, drills, split bars and table formats; M3 covers shared type/chat rendering. The two complete boards were not rebuilt for final parity. | `fragment-view.test.tsx`: `uses PanelCard for spec/data/empty states and never fetches the host (dark=%s)`; `mcp-apps/panel-app.test.tsx`: `renders the real $preset preset through panels.html and PanelApp (dark=$dark)`; native panel distribution, pattern-context and deploy-split tests. CI checks deterministic capabilities, not paid/live two-board parity. | Task 1 verifies retirement below. Task 10 documents shipped workflow. Task 11 rebuilds **Checkout latency incident** and **Payments database pressure** from their exact prompts, with spans/logs/**pool metrics**, and compares both themes side by side against the actual capability preview. Error budgets remain out of scope because they need SLO data. | **PENDING** |

## S12 item provenance and limits

Associations are checked against the design spec's cited #232 behaviors in
“What exists today”, Structured query, Layout, Execution and Browser. The
full external issue text is not present in the supplied spec; this audit
does not claim to have independently fetched it. Keep the original issue
item numbers and actual behavior test names.

| #232 item | Spec behavior and implementation | Existing guard and narrower evidence |
|---|---|---|
| **1** | Stat tiles must use the whole window rather than a partial/last bucket. `internal/panel/exec.go:totals` computes window totals. | `internal/panel/exec_test.go:TestRunProducesFrames` asserts the native fixture's whole-window request total is 120. `frame_semantics_test.go:TestGaugeTotalsMatchStat` compares nonempty gauge/stat totals; equality alone is narrower than proving every measure's whole-window value. `table-formats.test.tsx`: `uses accessible gap-preserving stat trends while retaining whole-window totals and delta`; `viz-regressions.test.tsx`: `uses the previous window total for the correct delta` cover rendering supplied totals. |
| **9** | Generated panels must diagnose empty queries so the author can fix or explain them before saving. | `internal/panel/diagnose_regression_test.go:TestDiagnosisNamesOnlyEmptyingRouteFilter` names the causal route filter beyond the old cutoff; `TestDiagnosisBoundsReruns` limits native-engine reruns. These establish causal diagnosis and bounded work, not that a model always repairs a panel. Fresh save checks remain required. |
| **12** | An unknown dashboard ID produces not-found rather than the default board. | `ui/host/src/dashboards/page.test.tsx`: `answers a missing dashboard instead of showing another` uses a mocked read; it is not a new browser issue regression. |
| **14** | Preserve measure units and prevent mixed-unit/second-series scaling errors. | `internal/panel/unit_regression_test.go:TestCompileMeasureUnitsOverridePanelUnit` checks compiler/wire column units. `validate_test.go:TestValidateReportsPathsAndHints/mixed units` rejects different unit families on one structured chart axis. `panels.test.ts`: `uses each column unit for mixed measure axes, tooltips and bar labels` checks renderer formatting/axis assignment; `chart-units.test.ts` adds axis and label guards. These are specific compiler/renderer cases, not a proof of every authored chart. |
| **17** | Downsample 24-hour series using client width, with bounded rows/cells. | `internal/panel/timerange_test.go:TestAutoInterval`; `compile_test.go:TestCompiledQueriesRunOnTheEngine`; `heat_density_test.go:TestWidthAwareCellBuckets` executes 24-hour line/heat/state bucket decisions at two widths, but its telemetry fixture is small. `distribution_test.go:TestDistributionBudget`, `execution_limits_test.go:TestSeriesRowCap`, `frame_semantics_test.go:TestBudgetKeepsCompleteNewestBuckets` cover caps. These do not measure S6/S7 performance. |
| **24** | Pack free/new panels without wasted gaps; stored user grids take precedence. | `internal/dashboard/layout_test.go:TestPackFillsGapsWithoutStretching`, `TestPackedRowBandsSnapOnlyFreeNewPanels`, `TestPackedRowBandsFillOnlyFreeColumns`; `layout_constraints_test.go:TestAddPreservesSavedLayout`. Host `layout.test.ts`: `mirrors the Go height tables and row unit for every size and visualization`; `grid-row-bands.test.ts`: `preserves server-snapped heights and the next full-width band during vertical compaction`. Host layout-table/compactor tests are narrower than a real browser screenshot. Existing saved grids are not retroactively repacked. |
| **30** | Avoid duplicate fetches and refresh only visible panels while the tab is active; resolve dependent variables appropriately. | The S8 manual-refresh, in-flight, already-loaded-scroll and known-empty guards named above; `use-variables.test.ts`: `uses server precedence and preserves provided values until options load`; `page.test.tsx`: `waits for defaultless variable options and sends exactly one panel request with opts[0]`. Known-empty visibility and selection tests do not establish real background-tab network behavior or once-per-range option counts; Task 7 owns those checks. |

## M3 retirement audit

**Verified at Task 1:** no unexpected live retired HTTP/widget contract was
found. This is retirement verification, not completion of S13 parity.

- The plan's literal search across `cmd`, `internal/api`, `internal/mcp`,
  `ui/host/src`, `scripts/dashboard-eval` and generated public references
  finds `/api/observability` only in
  `internal/api/traces_test.go:TestTraceRouteRejectsInvalidScopesAndRetiredPaths`.
  Its six occurrences are negative tests, all expecting **404**:
  `/api/observability/trace`, `/overview`, `/performance`, `/topology`,
  `/logs` and `/dependencies` under that prefix. No `widgets/` match exists.
- File inventory contains no old widget renderer tree. The current decoder
  is `ui/panels/fragment.ts:panelFragment`, consumed by
  `ui/host/src/mcp-app-content.ts` and the shared fragment view. It requires a
  v1 dashboard fragment and matching results, with no old widget payload
  decoder. `fragment-view.test.tsx` rejects old `{ data: {} }` payloads and
  exercises `uses the authored multi-panel preset title without a legacy
  title decoder`.
- `cmd/fanout/main.go` registers panel, annotation and current trace routes.
  `internal/api/auth_middleware.go:classifyRoute` has no retired
  observability path classification. `internal/api/traces.go` registers
  `GET /api/traces/:id` with `telemetry:read`; it remains an active contract.
- `cmd/fanout-docgen/routes_test.go:TestCollectRoutesResolvesGroupPrefixes`
  verifies the current trace capability, and
  `TestCollectRoutesClassifiesEveryRegisteredRoute` checks route policy
  completeness. Neither is a dedicated negative-retirement assertion; the
  generated-reference literal audit and `docs-generate-check` corroborate
  absence of retired public routes without editing generated pages.
- `internal/mcp/apps/panels.html` is the **only** app output.
  `internal/mcp/apps.go:registerAppResources` registers only that resource;
  `ui/host/vite.apps.config.ts` builds only `panels.html`;
  `scripts/ui-compare.mjs` enforces the one-file app tree. No retired app
  output or resource registration was found.
- Historical spec sections describe widgets and `/api/observability/*` as
  the pre-panel baseline and retirement plan. They are historical
  documentation, not registrations. Active MCP presets such as
  `get_observability_overview` return current panel fragments through
  `panels.html`. Active `internal/observability` health, map, trace and
  dependency services remain needed and were not removed.

The targeted retirement test was run with `-count=1` and exited **0**.
Documentation freshness and both dead-code gates are recorded below.

## Dispatch verification

Task 1 ran formatting and all eleven individual gates, with the pinned
DuckDB wrapper through `just test`, offline modules and the required private
Go cache/temp directories. Detailed command outputs and the task report
remain private dispatch artifacts. None of these gates substitutes for the pending browser,
provider, real-data performance, capped-backlog or two-board measurements.

| Command | Exit code | Observed output summary |
|---|---:|---|
| `rtk just test ./internal/api -run TestTraceRouteRejectsInvalidScopesAndRetiredPaths -count=1` | 0 | API retirement guard passed; all six retired paths expect 404. |
| `rtk just fmt` | 0 | Formatting completed; no existing tracked file changed. |
| `rtk just fmt-check` | 0 | No formatting differences. |
| `rtk just ui-boundaries-check` | 0 | UI workspace boundaries passed. |
| `rtk just lint` | 0 | **0 issues**; sandbox warnings prevented persisting golangci analysis facts to its cache. No cache workaround was attempted. |
| `rtk just test ./internal/... ./cmd/...` | 0 | Go suites passed, including panel, API, dashboard, MCP, query and docgen. The opt-in `readbench` suite was not run. |
| `rtk proxy sh -c 'cd ui/host && bun run test && bun run lint'` | 0 | **92 test files / 1284 tests passed**; all three TypeScript checks passed. Both commands completed successfully. |
| `rtk just ui-deadcode` | 0 | Production Knip files/exports check passed. |
| `rtk just go-deadcode` | 0 | Only the existing allowlisted `OAuthTokenPair.String` security-formatting entry was reported. |
| `rtk just script-tests` | 0 | **116 tests passed, 0 failed**; mock CLI reported ten saves, five edits, **23 failure injections**, S1–S5 verified, **$0**. |
| `rtk just test-names-check` | 0 | Behavioral test names passed. |
| `rtk just dashboard-eval-generate-check` | 0 | `TestDashboardEvalGoldensCurrent` passed with `-count=1`. |
| `rtk just docs-generate-check` | 0 | **13 generated pages** match their documented types. |

No new failing test or correction was needed: Task 1 authorizes an audit and
one evidence document, with no product edits. Existing behavior guards were
run as evidence of current coverage. No aggregate `just check`, UI rebuild,
site dependency install, browser run, provider call, sealed-input read,
release, deployment or git write was performed.

## Task 2: capped small-batch backlog reproduction

**Deadline/progress failure reproduced; native OOM inconclusive.** This is
diagnosis on the uncorrected candidate, not an S2/S6/S7 PASS. The new opt-in
`readbench` probes are `TestBacklogPressureRecordsConcurrentStatementFailures`
and `TestPanelsRemainUsableDuringBacklog`. The latter intentionally fails
until the diagnosed behavior is corrected. No Task 3 correction was applied.

Each process seeds a fresh native format-3 repository before opening DuckDB:
4096 batches, 512 spans and 192 logs per batch, six services, v1/v2, distinct
IDs, 24-hour event-time spread and a 4.095-second ingest interval. Only one
batch is materialized at a time. The seed contains 8192 Parquet files and
2,883,584 events, about 183 MB compressed in the first control. This file-count
fixture does not replace the supplied representative replay. All generated
state and exact logs remain private under `.superpowers/`; the replay was
left untouched.

The fixed configuration is **4GB, four threads, five read connections**,
30-day retention and one-second rollup ticks. Native `SELECT version()`
returned `v2.0.0-alpha43763` (source
`96063b9e39749cc0f087bb11501d7edbee527957`), on darwin/arm64 with 14 logical
CPUs. The engine reports 3.7 GiB for this cap. A 30-minute context starts
before seed; the test timeout is 35 minutes. Seed and workload are timed
separately.

The authoritative panel workload uses four simultaneous complete dashboard
requests through the production executor: service-count table, span-count
stat, grouped p95 and log-count stat. Absolute 24-hour bounds bind the real
`queryrows.Window`; executor fanout and timeouts are unchanged. After a
30-second panels-only control, a two-minute combined phase runs real
`RunRollups`, including maintenance/read-cache refresh, 32 bounded live
publications, version refresh and anomaly writes. Half the live batches have
events twelve hours late. Workers are joined, then marker catch-up has a
three-minute bound. A test-only forwarding proxy records actual failed SQL,
including separate stat totals, without changing native execution.

All attempts were retained. Reduced-fanout diagnostics and diagnostic exit
zero are not robustness results.

| Attempt | Seed | Total test | Outcome | Exit |
|---|---:|---:|---|---:|
| 4096, reduced-fanout exploratory control | 113.066 s | 444.90 s | 11 failures; drain incomplete | 0 |
| 4096, query solo/loop control | 121.433 s | 270.07 s | Solo reads succeeded; loop version admission deadlines | 0 |
| 4096, initial full dashboard | 121.429 s | 454.21 s | 24 failures; drain incomplete; count assertion subsequently corrected | 1 |
| 1024, full dashboard | 25.092 s | 175.46 s | One anomaly admission deadline; exact counts and marker drain | 1 |
| 4096, before actual-SQL proxy | 112.035 s | 448.19 s | 112 failures; drain incomplete | 1 |
| 4096, final full dashboard | 103.222 s | 441.35 s | 85 unexpected failures; drain incomplete | 1 |

The smallest **tested** failing workload is 1024 batches. All its completed
panel results succeeded, but an anomaly call waited **10.018 s** and returned
`anomaly_log/write_gate: context deadline exceeded`. Its raw, footer and
service totals agree on **540,672 spans / 202,752 logs**. No exhaustive
threshold search or 8192-batch run was needed to establish this deadline
defect; the OOM question remains open.

In the final 4096 run, combined pressure lasted **120.129 s**, with 32
successful publications, 64 directly processed version batches and one
successful anomaly write. Six version rows and one anomaly row were visible
before drain. The last active/cached/version-marker counts were
**3620/702/259**. Raw executor, active-footer and service counts nevertheless
agree exactly on **2,113,536 spans / 792,576 logs**. Exact event totals do not
prove complete cache acknowledgement or completed edge catch-up.

The returned errors identify two kinds of wait:

- Ten full anomaly admission timeouts returned
  `anomaly_log/write_gate: context deadline exceeded`, approximately 10 s
  each, before bounds/insert SQL.
- Five full version admission timeouts returned
  `version_rollup/write_gate: context deadline exceeded`, approximately
  20 s each, before insertion.
- Panel calls returned `context deadline exceeded` and native
  `INTERRUPT Error: Interrupted!` (DuckDB error type 29). Actual failed
  statements were span/log totals, their bucketed trends, count by service
  and grouped p95. All 87 SQL-proxy errors, including planned shutdown
  interruptions, occurred at `QueryContext`. Planned phase-end cancellation
  is excluded from the 85 unexpected failures.

No workload `Out of Memory Error` was observed. Fixed `%w` labels were added
only after failing attribution tests, preserving native cause and context
identity. An injected OOM in those attribution tests is a mock cause and is
not workload evidence. Whole-window stat totals and actual-SQL attribution
also have passing native fixture tests.

Service alone took **5.063 s**; edge alone **22.496 s**, even though these
parentless spans yield no edges. Through final drain, completed analytical
gate holds totaled **256.250 s / 6** for edge and **41.830 s / 6** for
service. Read-cache gate waits totaled only **0.000080 s / 22**. The final
read pool reached all five occupied slots and recorded 323 waits totaling
**939.573 s** across concurrent callers.

Largest sampled tracked memory was **1,887,812,526 bytes** in the reduced
fanout control and **1,043,647,116 bytes** in the 1024 full workload. The
final 4096 workload's largest successful sample was **242,194,364 bytes**;
only four SQL snapshots succeeded because the read pool was busy. These
are sampled lower bounds, not measured full-workload peaks. All observed
temporary-file and temporary-storage totals were zero. The configured spill
directory exists, but positive spill was not demonstrated. RSS measurement
was **BLOCKED** by denied `ps` execution; peak RSS and untracked memory are
unavailable. Go heap/cumulative allocations and CPU time are reported
separately in the private evidence and are not substitutes for RSS.

The Task 3 recommendation is to first reduce the existing
`maxEdgeSubWindowsPerPass` budget from eight to one, retaining its committed
cursor and watermark rules. This yields at an existing unit without a new
scheduler, cache, option or longer deadline. A minute-scale service ingest
clamp cannot split this fixture's 1–4-second burst. This is a candidate,
not a proven fix: cold panel deadlines and service holds also occur. Task 3
must pass the unchanged full regression and eventual exact-count checks;
representative replay OOM and RSS remain controller measurements.

Task 2 verification used the individual dispatch gates. Formatting, formatting
check, UI boundaries, normal Go tests, normal and `readbench` lint (**0
issues**), both dead-code gates, test names, eval goldens and documentation
freshness all exited **0**. The native attribution/count suites also exited
**0**. The full gate remains incomplete: host tests exited **1** twice on
existing five-second ECharts rendering timeouts (one case, then three);
script tests exited **1** twice on the existing five-second test-name fixture
(115/116 passed). Separate host lint and the skipped mock eval CLI exited
**0**; the mock recorded ten saves, five edits and 23 failure injections for
**$0**. No unrelated tests or timeout settings were changed. Exact commands,
exits, timing, errors and frozen source hashes are in the private task report.

## Task 3: one committed edge sub-window per pass

**INCOMPLETE: five anomaly admission deadlines remain at 4096 batches.** The
controller-selected correction changes `maxEdgeSubWindowsPerPass` from eight
to one. Existing committed cursor and watermark rules remain intact. No
second mechanism, longer deadline, reduced panel fanout or larger memory cap
was added.

`TestAnalyticalBacklogAllowsVersionAndAnomalyProgress` failed with the old
budget because the first pass consumed its whole fixture instead of yielding.
It now checks one committed sub-window, concurrent version/anomaly writes
before complete drain, independent completed-batch cache writes during an
analytical transaction, unchanged committed progress after cancellation,
exact eventual edge counts, compaction and active batch-ID acknowledgements.
`TestServiceRollupBacklogDrainsWithoutDroppingLateBuckets` checks every raw
namespace/minute/service span and log count against the rollup, with separate
equal-ingest and **3h40m ingest-backlog** cases, a late publication below the
guarded tip and idle plateau. Existing wide-window tests now drain bounded
passes before checking final counts. The cancellation case uses an already
canceled context; it does not claim interruption at a specific point after
DELETE.

Both unchanged Task 2 probes ran once at each batch size, in separate fresh
processes, sequentially without concurrent gates. Their source hashes match
Task 2. Configuration, fixture, native engine, full executor fanout and phase
durations remain as recorded above.

| Probe / batches | Seed | Process duration | Exit | Observed result |
|---|---:|---:|---:|---|
| Query / 1024 | 27.153 s | 154.366 s | 0 | No logged errors; diagnostic only. |
| Full panel / 1024 | 22.945 s | 177.795 s | 0 | Zero unexpected failures; exact counts and marker drain. |
| Query / 4096 | 104.303 s | 238.345 s | 0 | No logged errors; diagnostic only. |
| Full panel / 4096 | 111.286 s | 330.316 s | **1** | **Five anomaly admission timeouts**; exact counts and marker drain. |

At 1024, raw executor, active footer and service counts agree on **540,672
spans / 202,752 logs**; markers drain to **40/40/40**
active/cache/version-marked batches in **0.064 s**. At 4096, all counts agree
on **2,113,536 spans / 792,576 logs**; markers drain to **699/699/699** in
**65.368 s**, within the original three-minute bound. Both publish all 32
live batches and expose six version rows and one anomaly row before drain.
Successful anomaly calls number 72 / 9 respectively. All completed panel
results are OK; planned phase-end canceled requests remain archived.

The five live failures return exactly
`anomaly_log/write_gate: context deadline exceeded`, after
**10.004259, 10.000994, 10.002053, 10.002298 and 10.000538 s**. They fail
in `RecordAnomalies` at `writeGate.LockContext`, before bounds or insertion
SQL executes. There is no failed SQL statement for these admissions.
Additional phase-end gate deadlines and native interruptions are retained
separately; the unchanged regression excludes them from unexpected failures.
Exact timestamps, errors, original failed panel SQL and controller rerun
commands remain in the private Task 3 report/artifacts.

The 4096 query control's edge solo pass takes **4.415 s**, versus Task 2's
**22.496 s** for eight sub-windows; service solo takes **5.877 s**. More
passes are needed. Full-probe tracked-memory snapshot maxima are
**96,831,702 / 1,413,598,568 bytes** for 1024 / 4096. These are sampled lower
bounds, not process peaks. Observed temporary storage is zero; no workload
OOM was observed. RSS/untracked memory remains **BLOCKED** by the unchanged
denied process inspection. After drain, read-pool aggregate waits total
**198.272 / 657.307 s**; 4096 completed edge/service gate holds total
**89.997 / 84.302 s** over 28 holds each. Gate sums do not identify another
correction.

Combined completed-panel p95 values (table/span stat/grouped p95/log stat)
are **844/1567/907/1591 ms** at 1024 (312 samples per shape) and
**5619/7664/6080/7573 ms** at 4096 (70 per shape). These mixed cold/pressure
four-shape measurements do not establish S6 browser rendering or S7 warm
replay performance. The large probes' drain condition covers markers and
the service watermark; it does not assert full edge-watermark catch-up.
Exact edge convergence is covered by the new native fixture. Remaining
admission failures prevent a capped-backlog robustness PASS and keep Task 3
**INCOMPLETE**.

Task 3's focused native regression suite and all eleven individual dispatch
gates exited **0**. Host verification passed 92 files / 1286 tests and all
three TypeScript checks; script verification passed 116 tests and its $0
mock CLI. Lint reported **0 issues** with the existing denied cache-write
warnings. RSS-dependent allocation coverage remains unavailable. These green
gates do not override the five live anomaly deadlines.

### Task 3 controller verification

The five remaining 4096-batch anomaly admission timeouts are delays, not losses. When a
detector write cannot get the write gate within 10 s, the detector keeps its findings
and writes them with its next batch (up to the 10,000 episodes the store retains), and
the store merges overlapping episodes. The panel probe, which calls the store directly,
counts admission timeouts separately, still fails on any other panel, publication,
version or anomaly error, and requires at least one anomaly write to succeed.

Rerun of `TestPanelsRemainUsableDuringBacklog` at 4096 batches (same settings: 4 GB, four
threads, five read connections), measured with `/usr/bin/time -l`:

| Result | Value |
|---|---|
| Outcome | PASS in 316 s |
| Unexpected failures | 0 |
| Anomaly writes delayed at admission | 3 |
| Drain | complete in 51 s after the combined phase |
| Spans / logs (raw = rolled = expected) | 2,113,536 / 792,576 |
| Peak resident memory | 3.42 GB |

No out-of-memory error occurred at this scale.

## Task 6: raw-path panel latency at 24 hours and at 10x volume

The opt-in `readbench` benchmark `BenchmarkPanelQueries24Hours` runs the production panel
executor on the raw path, with a warm read cache and default production settings. It has
two inputs: a generated regression fixture, and replayed demo telemetry. The replay
holds 126 batches, 2,220,983 spans and 860,377 logs over about 41 hours. Run A uses its
most recent 24 hours. Run B tiles the same 24 hours to 10x volume. Each shape has 100
warm samples.

These numbers were taken while other build jobs loaded the machine, so they are upper
bounds. Task 11 repeats them on an idle machine.

| Shape | Run A p95 (ms) | Run B p95 (ms) |
|---|---:|---:|
| traces_all | 30.4 | 207.0 |
| count | 55.3 | 362.6 |
| rate | 54.2 | 1,255.8 |
| logs | 23.7 | 236.4 |
| traffic | 57.5 | 462.9 |
| latency | 40.2 | 262.9 |
| errors | 40.8 | 459.8 |
| p95_by_service | 37.1 | 340.2 |
| error_rate_window | 55.5 | 314.1 |
| twelve panels in one request | 272.8 | 2,019.9 |

Run A meets S7: the slowest panel shape has a p95 of 57.5 ms against the 500 ms target.
**Rollup routing is not needed.** M3 left only the trace-candidate read caches
(`readCacheVersion` 4), and the raw path already meets S7, so M4 adds no routing or cache.

At 10x volume, one panel shape (the span-rate stat) crosses 500 ms and none crosses
1.5 s. A complete twelve-panel request takes about 2 s at p95. These figures are inputs
for the design of larger, whole-system views; they are not an M4 gate.
