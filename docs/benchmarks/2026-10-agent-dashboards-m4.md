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
