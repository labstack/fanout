# Agent dashboards Milestone 4 — evidence audit

Task 1 audit, 2026-10-09. Source: `82ab72e9629475af52aadb337b63d91bc61138a9`,
branch `feat/agent-dashboards-m4`. Task 1 initialized the evidence for the M4
candidate and made no product changes ($0). Task 11 replaced its PENDING
verdicts. A Task 11 rerun on the corrected candidate then replaced the first
run's verdicts with the measurements below. **Every criterion the rerun
measured passes:** S1–S5, S8, S9, S11, S12 and S13. S6 (local) and S10 keep
their first-run PASS, and S7 passes on the raw path. The S6 CI ceiling is set from
three runner measurements.

Requirements: [design spec](../superpowers/specs/2026-10-04-agent-dashboards-design.md)
and [M4 plan](../superpowers/plans/2026-10-09-agent-dashboards-m4.md), subject to
the controller's binding rulings. Historical baselines are
[M1](2026-10-agent-dashboards-m1.md),
[M2](2026-10-agent-dashboards-m2.md) and
[M3](2026-10-agent-dashboards-m3.md).
Their measurements, screenshots and PASS labels do not establish an M4 PASS.

## S1–S13 final verification

Task 11 rerun, 2026-10-10 UTC.

- **Candidate:** HEAD `ae10f2f67969b0f6ded4a49ea3d799188f16ee31`. It carries the
  first run's corrections D1, D2 and D4 and the card-corner fix. It differs from
  `2d17645a` only in a panel benchmark test.
- **Binaries:** the production binary was built from the clean tree with its
  committed browser assets. The collector used the
  development audit build of the same tree.
- **Engine:** the pin is unchanged since the first run (`SELECT version()`:
  `v2.0.0-alpha43763`).
- **Platform:** darwin/arm64, 14 logical CPUs, default DuckDB sizing (22,118 MB,
  14 connections). The machine was shared, with a load average of 7–20 during
  the benchmark and collector runs.
- **Model and spend:** `claude-sonnet-5-5` only. Paid spend was **$1.905655**
  on a new rerun ledger with a $2.80 cap. The first run spent $2.087733 on its
  own ledger, so both runs together spent $3.993388.
- **Holdout and judge:** the holdout was not run. The judge was skipped
  (`judge_keys_unconfigured`).
- **Product changes:** none in the rerun. Two corrections went to the browser
  collector instead (see the first-run history below).

Telemetry:

- **Source:** the same immutable fanout-demo format-2 source (483 files,
  2,213,782 spans, 860,377 logs, 14,302,306 metric points).
- **Replay:** it was replayed again, with the same converter and transport
  groups, into a fresh local instance with fresh control state. The first
  run's copy was older than the evaluation runner's five-minute freshness gate
  allows.
- **Shift:** one signed shift (+394,040,685,048,673 ns), shared by all signals
  and twelve converter processes. The newest event landed 60 s after the replay
  started, and the replay took 160.6 s.
- **Read back from the instance:** exact source counts. Event time runs from
  2026-10-08T13:23:21Z to 2026-10-10T06:12:31Z, with one namespace and 17 span
  services. There are 342 metric names (gauge 95, histogram 35, sum 213),
  including the first run's pool metrics.
- **Timing:** the benchmark started 1.8 s after the replay ended.
- **Synthetic deploy:** injected after the benchmark, as in the first run:
  7,201 `cart` server spans at version 2.3.0 over the last four hours, 432 of
  them errors, marked `fanout.synthetic=deploy-verification`.
- **Edge rollup:** the backlog and lag were 0 eight minutes after the replay,
  and the one-hour service map returned 20 services and 23 routes.

| Criterion and literal target | Measured on the M4 candidate | Evidence | Verdict |
|---|---|---|---|
| **S1:** Benchmark prompts produce a saved dashboard, **10 of 10** | **10/10** saved, each as one immutable version 1. The runner recognised every save (exit 0, aggregate pass). | Ten-prompt benchmark with the five edits: `--set benchmark` with the plan's Task 11 Step 4 inputs and a $2.80 cap. | **PASS** |
| **S2:** Panels return rows, or carry an explained empty state, at save time, **100%** | **65/65** panels good at save time: 63 with rows and 2 authored explained-empty. | The runner's save-time executor checks. Both empty panels filter `severity_number >= 17`, which matches no demo logs, and each panel's description says so. | **PASS** |
| **S3:** Specs fail validation after the agent's own correction loop, **0** | **0** invalid. 10/10 final specs valid and checked. | Runner validation. | **PASS** |
| **S4:** Median prompt to saved dashboard, default model, **≤ 45 s** | Median first save **21.5 s** (range 10.7–54.9 s). | Runner save timing. The slowest prompt, `db-cache`, took 54.9 s. | **PASS** |
| **S5:** Only named panels change over **5 consecutive edits, 100%** | **5/5** exact: title, threshold, add, remove and unit (versions 2–6). Only the named panel changed. Layout changed only for add and remove. | Runner edit comparison. | **PASS** |
| **S6:** First full render, **12 panels / 24 hours, ≤ 1.5 s p95** | Not re-measured. First run, real data, local: light **634 ms**, dark **582 ms** p95 (24 fresh loads each). Controller's local e2e: 361 / 393 ms. CI ceiling: controller. | First run: the checked-in 12-panel fixture on the replay instance, timed with the performance spec's in-page `allPanelsPainted` callback. Each load made one panel batch with zero faults. Since then, the product changes are the empty-state wrap, card corners and edge rollup. | **PASS** (local, first run); CI: **controller** |
| **S7:** Warm-cache panel query latency, **≤ 500 ms p95** | Slowest shape **47.1 ms** p95 on the traces-and-logs replay; **33.1 ms** on the three-signal replay, including metric panels (gauge 33.1, counter 27.4, histogram 23.2 ms). 100 warm samples per shape, idle machine. | `BenchmarkPanelQueries24Hours` (readbench) on replayed demo data, 24 hours, default production sizing. See Task 6 below. | **PASS** |
| **S8:** **1 batch + 1 annotations request per refresh; variable options once per range change** | Collector: **6/6** refresh cycles on the 23-panel board (3 per theme). Each made exactly 1 panel batch and 1 annotations request. | `drill_url`: opening and closing drills, including Back after a reload, sent 0 panel requests. The controller's `just e2e` performance spec covers range-change counts. | **PASS** |
| **S9:** **15 visualization types** listed in the spec | Collector: **30/30** type/theme rows visible, labelled, inspectable and passing their rendering assertions. Page smoke: 56/56 across 14 dashboards at 1100 and 1440 px. | The service map showed 20 services and 23 routes in both themes. Its top and bottom overflow fades were drawn, and the whole-graph control removed the overflow. | **PASS** |
| **S10:** **WCAG AA in both themes; categorical palette passes CVD validation** | Not re-measured. First run: Task 8 guards (axe-core 4.13.0 scans of every dashboard and chat panel type in both themes, in the controller's passing `just e2e`) and the six-slot CVD unit test (Machado 2009, OKLab thresholds). | First-run screenshot review at 1100 and 1440 px found no contrast failure. The clipping it found (D2) is fixed. | **PASS** (first run) |
| **S11:** Shared link reproduces **time, variables, comparison and focused panel** | Collector: `panel_time`, `brush_history`, `empty_multiselect` and `fullscreen_panel` PASS. | `fullscreen_panel` checked the focused-panel dashboard URL, shared links, a missing panel and Esc/Close/Back in both themes. `empty_multiselect` reopened a copied URL with comparison and an explicit empty selection exactly. | **PASS** |
| **S12:** #232 **items 1, 9, 12, 14, 17, 24, 30 resolved** | 1: `stat_sparkline` PASS. 9: `empty` PASS on 14 types. 12: the missing-panel case in `fullscreen_panel` PASS. 14: `column_formats` and `legend_geometry` PASS. 17 and 24: `preview_geometry`, `resize_geometry` and `route_scroll` PASS. 30: refresh counts and `drill_url` PASS. | Collector: 33/33 checks, zero defects. | **PASS** |
| **S13:** **Every in-scope preview capability shipped; checkout-incident and payments-database boards rebuilt from prompts** | All eight in-scope capabilities PASS. Five come from the first run's prompt-built boards. Deploy markers, database statement detail and trace/log drill were re-measured on this candidate. | Side-by-side comparison below. | **PASS** |

### S13 side-by-side comparison

In the first run, the two exact preview prompts each ran once through the agent
chat endpoint. They were metered on that run's ledger and saved valid boards
(13 and 8 panels; 21/21 checks good). The rebuilt boards were captured at 1100
and 1440 px in both themes, next to a network-free copy of the private preview.
The rerun spent nothing on parity. It re-measured the three capabilities that
had failed, using the collector and the product's `preview_panels` tool, and
re-rendered the first run's checkout board on this build. Capabilities are
compared, not values. The preview's error-budget board is out of scope (SLO).

| Capability | Preview | Product evidence | Live evidence | Verdict |
|---|---|---|---|---|
| Heatmap | Log2 latency distribution with exemplar drill | Checkout (first run): log2 duration heatmap with trace drill | Heatmap type rows, both themes | PASS |
| Contextual log patterns | Error and warning patterns with trends | Checkout (first run): `body_template` patterns with log drill | `pattern_context` PASS on dashboard and chat, both themes | PASS |
| Deploy markers and anomaly bands | Deploy line and anomaly shading on latency and pool charts | Both rebuilt boards enable deploys and anomalies. The checkout board, re-rendered on this build, lists `cart` 2.3.0 and an anomaly band, and shows no marker because only `cart` deployed. | `deploy_markers` PASS: one aligned 1-px `cart 2.3.0` band, and no band at that time on checkout. `anomaly_areas` PASS. | PASS |
| Before/since bars | Downstream calls split at the deploy | Checkout (first run): operations bar with `split: deploy` | `deploy_split` PASS (cart Before/Since series) | PASS |
| Database and cache detail | Share of p99 by statement, duration against rows, statements in timeout traces | `preview_panels` (no model call) ran three structured `product-reviews` panels, all `ok`: p99 by `db.statement` (3.8 ms), share by statement (100%), and a table of statement, database, rows (6,421), share, average, p99 and maximum duration. They rendered as a board in both themes at 1100 and 1440 px. | `preview_panels` over the session MCP route on the replay | PASS |
| Trace and log drill | Exemplar traces, trace table, logs from a trace | Trace drill on the heatmap, state timeline and traces panel; log drill on patterns. The re-rendered checkout board shows all of them. | `drill_url`, `waterfall_logs` and `cancel_drill` PASS | PASS |
| Table formats | Trace link, duration bar, template chips | Bar and unit column formats; trace links and duration bars in the traces panel | `column_formats` PASS | PASS |
| Pool metrics | Pool in-use time series with a pool-max threshold, peak stat | Payments (first run): table of six real pool series and their peaks, with typed attributes | Verified pool names and attributes on both replays | PASS |

### First run (history)

The first run (HEAD `fed0a916`, production binary from `1fd54332`, $2.087733)
measured these verdicts:

- S1–S5: BLOCKED.
- S9, S11, S12 and S13: FAIL.
- S6 (local), S8 and S10: PASS.

Its findings and their outcomes:

| ID | Finding | Outcome |
|---|---|---|
| D1 | Eval runner: `findSaved` required an explicit `error` flag, and the AG-UI Go message omits an empty one, so no save was recognised. | Fixed in `bbfa48dc`. The rerun scored S1–S5. |
| D2 | UI: a long unbroken empty-state diagnosis overflowed its card. | Fixed in `2d17645a`. |
| D3 | `drill_url`: Back after a drill reload re-requested all 23 panels. | Not a product defect. In 28 diagnostic trials on the unchanged UI, Back after a reload stayed in the reloaded document and sent 0 panel requests, including with 6 s stale data and under 6× CPU throttling. The first run's traffic (`panels:aborted, panels:200, annotations:200`, 23 ids, page at the top) matches a Back that lands in a new document. The browser automation launches Chromium with the back/forward cache disabled, so that document must load every panel. The collector now marks the reloaded document and classifies a Back into a new document as a fresh load, as it already did for the reload. |
| D4 | Edge rollup took over an hour to catch up after a bulk replay. | Fixed in `bbfa48dc`. Backlog 0 within eight minutes; map routes present. |
| H1, H2 | Collector: a stale thread-list mock, and a colour-string comparison. | Fixed in the collector. |
| H3 | Collector (found in the rerun): the service-map overflow assertions still required the M2 fade element and the "+N below" hint, which M3 replaced with `data-map-overflow` / `data-map-fade`. | The collector now reads the current markers and still fails an overflow without a drawn fade. |
| H4 | Collector (found in the rerun): the deploy-marker negative check counted raw pixel differences. The marker board's annotation label strip shortens its plot and rescales every series point against the twin (939 px). | The collector now applies the positive check's narrow-band rule at the `cart` deploy time (largest column 3 rows, threshold 22.4). It still fails when a marker is drawn there. |

The first collector rerun, with only the D3 correction, passed 32/33 checks
and failed on H3 and H4. The run reported above includes all three collector
corrections. Each correction has node tests showing it still fails on the
defect it guards.

### Limitations

- S6 and S10 were not re-measured on this candidate.
- Parity data is the demo:
  - `payment` has no database spans.
  - `product-reviews` issues one distinct statement, so its share is 100%.
  - The only deploy is the labelled synthetic `cart` injection, so the
    checkout board cannot show a checkout deploy.
- Statement detail was verified with `preview_panels` and a hand-written
  structured spec, not by rerunning the payments prompt.
- The rebuilt boards use no variables, click-to-filter or saved comparison,
  which the preview shows. These are outside the brief's capability list.
- In the first run, anomalies were written for all namespaces (empty namespace),
  so an annotation request scoped to `otel-demo` returned none. Unscoped panels
  show them.
- The machine was shared.

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

### Metric panels

A second replay carries all three signals: 8,221,759 metric points in the 24-hour window,
alongside traces and logs. The benchmark adds three metric shapes, each on the most
common metric of its type in the replay (the gauge is `container.cpu.utilization`).

| Shape | Demo volume p95 (ms) | 10x volume p95 (ms) |
|---|---:|---:|
| gauge average by service | 33.1 | 256.7 |
| counter rate | 27.4 | 212.7 |
| histogram p95 | 23.2 | 166.7 |
| twelve panels in one request | 120.2 | 1,098.7 |

Two limits of the metric measures show up here. A counter's rate is the rate of
data points, not the rate of the counter's increase, and a histogram's p95 is computed
from each point's sum, not from its buckets.

## Scale limit and next step

The replayed demo carries about 15 spans per second. At 10x, about 150 spans per second,
every single panel stays under 500 ms, but a complete twelve-panel request takes 1.1 to
1.6 s on the server alone, at or past S6's 1.5 s budget for the whole first render, on a
14-core development machine. A larger system will need precomputed per-minute summaries
for spans, logs and metrics, which milestone 3 removed. They are planned for the next
milestone, together with a whole-system view, and sized against a target rate of 1,000 to
5,000 spans per second with host metrics. M4 adds no summaries or routing.
