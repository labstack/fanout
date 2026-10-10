# Agent dashboards Milestone 4 — evidence audit

Task 1 audit, 2026-10-09. Source: `82ab72e9629475af52aadb337b63d91bc61138a9`,
branch `feat/agent-dashboards-m4`. Task 1 initialized the evidence for the M4
candidate and made no product changes ($0). Task 11 replaced its PENDING
verdicts with the final measurements below. **M4 does not meet every
criterion:** S9, S11, S12 and S13 FAIL, S1–S5 are BLOCKED by an evaluation
runner defect, and the S6 CI ceiling and S7 cells belong to the controller.

Requirements: [design spec](../superpowers/specs/2026-10-04-agent-dashboards-design.md)
and [M4 plan](../superpowers/plans/2026-10-09-agent-dashboards-m4.md), subject to
the controller's binding rulings. Historical baselines are
[M1](2026-10-agent-dashboards-m1.md),
[M2](2026-10-agent-dashboards-m2.md) and
[M3](2026-10-agent-dashboards-m3.md).
Their measurements, screenshots and PASS labels do not establish an M4 PASS.

## S1–S13 final verification

Task 11, 2026-10-10 UTC. Frozen candidate: HEAD
`fed0a916cfef31bd7e0c213831467e1fa66e2aac`. The production binary was built
from `1fd54332`, which differs from HEAD only in this document; the collector
used a development audit build of the same tree. Native engine
`SELECT version()`: `v2.0.0-alpha43763`. Platform: darwin/arm64, 14 logical
CPUs, default DuckDB sizing (about 22 GB, 14 connections). The machine was
shared: other workloads kept the load average between 10 and 52 during the
runs. Model: `claude-sonnet-5-5` only. Paid spend: **$2.087733** on one shared
ledger, $5 cap ($1.667786 benchmark, $0.419947 parity). Holdout: unrun, budget
reserved for benchmark and parity. Judge: skipped, `judge_keys_unconfigured`.

Telemetry: the immutable fanout-demo format-2 source (483 files, 2,213,782
spans, 860,377 logs, 14,302,306 metric points, 40.8 hours) was replayed over
OTLP/HTTP into a fresh local instance with fresh control state. One signed
shift (+387,651,540,613,673 ns) was shared by all signals and twelve parallel
converter processes. It placed the newest event 60 s after replay start; the
replay took 167 s. Read back from the instance: exact source counts in 71
format-3 batches (1,881,413,858 bytes), event time from 2026-10-08T11:36:51Z
to 2026-10-10T04:26:02Z, one namespace and 17 span services. Metrics: 342
names (gauge 95, histogram 35, sum 213 by type and name). The pool metrics are
`db.client.connection.count`/`.max` (npgsql) and `db.sql.connection.open`,
`.max_open`, `.wait` and `.wait_duration` (database/sql), with typed
attributes such as `attributes['db.client.connection.state']` = `idle`/`used`.
The source has no `service.version` change. Before the parity prompts and the
collector, a labelled synthetic deploy was injected: 7,201 `cart` server spans
at version 2.3.0 over the last four hours, 432 of them errors, marked
`fanout.synthetic=deploy-verification`.

| Criterion and literal target | Measured on the M4 candidate | Evidence | Verdict |
|---|---|---|---|
| **S1:** Benchmark prompts produce a saved dashboard, **10 of 10** | Runner: **0/10** recognised (exit 1). Persisted threads and version lists: 10/10 saved, each one immutable version 1. | The runner recognises a save only when the persisted tool message carries an explicit `error` flag. The AG-UI Go `Message` omits an empty `error`, so no save is recognised (finding D1). A read-only reconstruction applied the runner's mutation catalogue and single-dashboard rule to the persisted tool results and confirmed each version. | **BLOCKED** (D1) |
| **S2:** Panels return rows, or carry an explained empty state, at save time, **100%** | Runner: no checks. Executor checks of the ten saved specs, run 3–6 minutes after save: **66/66** (63 with rows, 3 authored explained-empty). | Post-hoc, not at save time. The agent's own save-time check hit its 8 s bound during the replay backlog on 2 of 10 boards and recorded `not_run`. | **BLOCKED** (D1) |
| **S3:** Specs fail validation after the agent's own correction loop, **0** | Runner: unchecked. Post-hoc: 10/10 saved specs valid, **0** invalid. | Same reconstruction. | **BLOCKED** (D1) |
| **S4:** Median prompt to saved dashboard, default model, **≤ 45 s** | Runner: no save timing. Run end (request to end of stream, an upper bound on the last save): median **24.6 s**, range 18.3–52.4 s. | Bound only; the runner measures the save itself. | **BLOCKED** (D1) |
| **S5:** Only named panels change over **5 consecutive edits, 100%** | Not run. | The runner starts the edits on the first recognised save, and none was recognised. | **BLOCKED** (D1) |
| **S6:** First full render, **12 panels / 24 hours, ≤ 1.5 s p95** | Real data, local: light **634 ms**, dark **582 ms** p95 (24 fresh loads each). Controller's local e2e: 361 / 393 ms. CI ceiling: controller. | The checked-in 12-panel fixture on the replay instance, timed with the in-page `allPanelsPainted` callback imported from the performance spec. Each load made one panel batch; all 12 panels returned rows; zero console, page or network faults. | **PASS** (local); CI: **controller** |
| **S7:** Warm-cache panel query latency, **≤ 500 ms p95** | controller | controller | controller |
| **S8:** **1 batch + 1 annotations request per refresh; variable options once per range change** | Collector: **6/6** refresh cycles on the 23-panel board (3 per theme) each made exactly 1 panel batch and 1 annotations request. | The controller's `just e2e` performance spec also passed refresh and range-change counts. The drill re-request is reported under S12. | **PASS** |
| **S9:** **15 visualization types** listed in the spec | Collector: 30/30 type/theme rows visible, labelled and inspectable. The `service_map` rendering assertion failed in both themes ("Map must mark its entry services"): 20 services and 0 routes in the last 24 hours at capture. Page smoke: 60/60 across 15 dashboards. | The map was empty because edge-rollup catch-up after a bulk replay is slow (finding D4), not because of the renderer. Routes appeared later in the same run. | **FAIL** (2/30 rows, D4) |
| **S10:** **WCAG AA in both themes; categorical palette passes CVD validation** | Task 8 guards: axe-core 4.13.0 scans of every dashboard and chat panel type in both themes within the controller's passing `just e2e`, and the six-slot CVD unit test (Machado 2009, OKLab thresholds). | Screenshot review at 1100 and 1440 px in both themes found no contrast failure. One clipping defect (D2) is listed separately. | **PASS** |
| **S11:** Shared link reproduces **time, variables, comparison and focused panel** | Collector: `panel_time`, `brush_history` and `empty_multiselect` (copied URL with comparison and an explicit empty selection reopens exactly) PASS. `fullscreen_panel` (focused-panel link) FAIL: not executed. | The collector's chat/full-screen seeds mock the thread list in its pre-#292 shape, which crashes the shell (H1). The focused-panel link was therefore not observed live. | **FAIL** (H1) |
| **S12:** #232 **items 1, 9, 12, 14, 17, 24, 30 resolved** | 1: `stat_sparkline` PASS. 9: `empty` PASS on 14 types. 14: `column_formats`, `legend_geometry` PASS. 17 and 24: `preview_geometry`, `resize_geometry`, `route_scroll` PASS. 30: refresh counts PASS, but `drill_url` FAIL. 12: the missing-panel case in `fullscreen_panel` was not executed (H1). | `drill_url`: after a reload and Back from the recent-logs drill, all 23 loaded panels were requested again (one aborted, one complete). The slow-traces sequence did not repeat them (finding D3). | **FAIL** (D3, H1) |
| **S13:** **Every in-scope preview capability shipped; checkout-incident and payments-database boards rebuilt from prompts** | Both prompts saved valid boards (13 and 8 panels; 21/21 checks good). Five of eight capabilities PASS; deploy markers, database statement detail and trace drill FAIL. | Side-by-side comparison below. | **FAIL** |

### S13 side-by-side comparison

The two exact preview prompts ran once through the agent chat endpoint, metered by
the runner's admission, usage and settlement functions on the same ledger.
The rebuilt boards were captured at 1100 and 1440 px in both themes, next to a
network-free copy of the private preview. Capabilities are compared, not values.
The preview's error-budget board is out of scope (SLO).

| Capability | Preview | Rebuilt from the prompt | Live evidence | Verdict |
|---|---|---|---|---|
| Heatmap | Log2 latency distribution with exemplar drill | Checkout: log2 duration heatmap with trace drill (421 cells) | Heatmap type rows, both themes | PASS |
| Contextual log patterns | Error and warning patterns with trends | Checkout: `body_template` patterns with log drill | `pattern_context` PASS on dashboard and chat, both themes | PASS |
| Deploy markers and anomaly bands | Deploy line and anomaly shading on latency and pool charts | Both boards enable deploys and anomalies; an anomaly band shows on payment latency. Checkout panels show no marker because only `cart` deployed. | Screenshot shows the `cart 2.3.0` marker and an anomaly band; `anomaly_areas` PASS; `deploy_markers` FAIL on its twin-colour precondition (H2) | FAIL (H2) |
| Before/since bars | Downstream calls split at the deploy | Checkout: operations bar with `split: deploy`; notes that no deploy falls in its scope | `deploy_split` PASS (cart Before/Since series) | PASS |
| Database and cache detail | Share of p99 by statement, duration against rows, statements in timeout traces | Payments: database calls by service and system (cart Redis, product-reviews PostgreSQL) and an explained-empty panel for payment, which has no database spans. No statement breakdown, although `db.statement` exists for product-reviews. | Benchmark board "Database and cache calls by service" | FAIL |
| Trace and log drill | Exemplar traces, trace table, logs from a trace | Trace drill on heatmap, timeline, latency and tables; log drill on patterns; traces panel | `waterfall_logs`, `cancel_drill` PASS; `drill_url` FAIL (D3) | FAIL (D3) |
| Table formats | Trace link, duration bar, template chips | Bar and unit column formats; trace links and duration bars in the traces panel; no template chips | `column_formats` PASS | PASS |
| Pool metrics | Pool in-use time series with a pool-max threshold, peak stat | Payments: table of six real pool series and their peaks, with typed attributes. No time series or threshold; the source pools never exceed 2 in use. | Verified pool names and attributes on the instance | PASS |

### Findings to correct

| ID | Kind | Evidence | Smallest correction |
|---|---|---|---|
| D1 | Eval runner | `findSaved` sets `is_error` to null unless the persisted tool message has an `error`/`isError`/`is_error` key. The AG-UI Go `Message.Error` is `omitempty`, so successful tool messages have no flag. Every benchmark and parity save went unrecognised. The mock writes `error: ''`, so mock tests pass. | Treat an absent `error` on a single persisted tool message as success (as before Task 9). Make the mock omit `error` on success, and pin a real persisted thread read in the server golden. |
| D2 | UI | An empty panel's diagnosis with a long unbroken token (`name IN ('db.client.connection.count',…)`) overflows and is clipped at both card edges, in both themes and widths. | Add `overflowWrap: "anywhere"` to the empty-state diagnosis text in `panel-card.tsx`, as the error text already has, with a rendering test. |
| D3 | UI (cause not isolated) | `drill_url`: closing the recent-logs drill with Back after a reload re-requested all 23 loaded panels. | Rerun `drill_url` alone on an idle machine and log the panel query key around the close. If the relative window re-anchors on close, reuse the existing anchor. |
| D4 | Backlog robustness | After a 41-hour bulk replay, edge rollup processes one ≤ 30-minute start-time sub-window per one-minute pass, so it needs at least 82 passes. Recent windows had no routes for over an hour. Progress gauges reported watermark 0 and 497,669 backlog chunks during the first window. | Bound each edge pass by elapsed time while no writer waits, instead of a fixed single sub-window. Report progress from the window start or sub-cursor instead of zero. |
| H1 | Collector | The receipt and full-screen seeds return `{threads, nextCursor}`; the current list contract is `{items, next_cursor}`. The rail then throws "Cannot read properties of undefined (reading 'updated_at')", and seven checks (five receipt checks, `fullscreen_panel`, `explain_panel`) time out on the theme toggle. | Return `{items: [], next_cursor: null}` from both seeds and rerun the seven checks. |
| H2 | Collector | The `deploy_markers` twin precondition compares colour strings (`#3987e5` against `rgba(57,135,229,1)`, the same colour), with a tooltip left open on the twin. | Normalize colours before comparing and move the pointer off the chart before the twin capture. |

The first collector run was BLOCKED by a 12-second screenshot timeout under
machine load, after the light type rows completed. The rerun is the run
reported above: 24/33 checks pass.

### Limitations

- S1–S5 have no valid runner score on this candidate. Post-hoc evidence
  suggests S1–S4 would pass; S5 is unmeasured. Spending more requires the
  controller's authorization after D1 is corrected.
- Parity data is the demo: `payment` has no database spans and no pool
  pressure, and the only deploy is the labelled synthetic `cart` injection.
  The checkout board therefore cannot show a checkout deploy. The payments
  board correctly reports that the premise does not hold.
- The rebuilt boards use no variables, click-to-filter or saved comparison,
  which the preview shows. These are outside the brief's capability list.
- Anomalies are written for all namespaces (empty namespace), so an
  annotation request scoped to `otel-demo` returns none. Unscoped panels show
  them.
- The machine was shared and heavily loaded. S6 timings are conservative.

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

Measured on an idle machine (Apple silicon, 14 logical CPUs, default production DuckDB
sizing):

| Shape | Run A p95 (ms) | Run B p95 (ms) |
|---|---:|---:|
| traces_all | 24.4 | 176.8 |
| traces_checkout | 23.4 | 164.2 |
| traces_selected | 24.0 | 191.4 |
| count | 47.1 | 304.3 |
| rate | 42.2 | 292.5 |
| logs | 17.4 | 105.8 |
| traffic | 38.5 | 265.9 |
| latency | 31.0 | 219.6 |
| errors | 30.0 | 215.2 |
| log_volume | 12.6 | 71.0 |
| service_counts | 23.8 | 139.5 |
| severity_counts | 6.6 | 38.1 |
| p95_by_service | 26.1 | 184.8 |
| error_rate_window | 19.0 | 130.1 |
| twelve panels in one request | 199.9 | 1,588.0 |

Run A meets S7: the slowest panel shape has a p95 of 47.1 ms against the 500 ms target.
**Rollup routing is not needed.** M3 left only the trace-candidate read caches
(`readCacheVersion` 4), and the raw path already meets S7, so M4 adds no routing or cache.

At 10x volume no panel shape crosses 500 ms; the slowest is the span-count stat at
304 ms. A complete twelve-panel request takes 1.59 s at p95. An earlier run on a loaded
machine measured up to 1.26 s for one shape, which shows how much contention matters.
The replay has no metric points, so these figures cover trace and log panels only. They
are inputs for the design of larger, whole-system views; they are not an M4 gate.

Final rerun on the M4 candidate, idle machine, same settings: PASS in 334 s; 0 unexpected
failures; 13 anomaly writes succeeded and 1 was delayed at admission (its findings carry
into the next write); the backlog drained in 56 s with exact span and log counts
(2,113,536 / 792,576); peak resident memory 3.21 GB.
