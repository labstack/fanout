# Browser smoke tests

With Go, Bun, just and **Google Chrome** installed, run `just e2e` from the
repository root. It rebuilds the embedded SPA and MCP app plus `bin/fanout`,
then runs the pinned Playwright suite in `ui/host/e2e/` using system Chrome;
no browser download or AI provider key is needed. `just check` remains browser-free.

Setup creates a disposable loopback server, first administrator and near-now
deterministic OTLP protobuf traces and logs. Teardown stops the server and removes
its data and temporary credentials. `FANOUT_E2E_BINARY` can select another built
server; `FANOUT_E2E_SEED_BINARY` can select a prebuilt seed command. Both paths are
runtime values; otherwise the seed runs through `scripts/with-duckdb.sh`.
Setup polls `/readyz` before creating the administrator within its 30-second
start budget, then retries data readiness for 90 seconds, requiring span and log
counts, six service nodes and a topology edge. Timeouts include the last cause.
SIGINT and SIGTERM stop the server, remove the temporary directory and re-raise
the signal. Server and seed output stays in private temporary files; setup
failures print their last 50 lines with tokens and cookies redacted before cleanup.

The suite covers all fifteen visualizations, terminal states, panel/header
geometry, service-map clipping and font sizes, and five Go-generated chat
fragments in sandboxed MCP app iframes. It checks 1100 and 1440 pixel widths in
light and dark themes and rejects every browser error or console warning/error.
Seeded panels must be `ok`; none are intentionally empty. Charts must name their
panel and show populated series or rows, and the gauge must have a numeric value.
The dashboard checks topology edge paths and a checkout deploy annotation's HTML
tooltip text without reading canvas pixels. Fragment height is capped at 2000
pixels to match the host, and messages must stop for five seconds after the first
settled frame. CI pins Node 24.19.0 and limits the job to 15 minutes. It runs the same recipe on
pull requests and pushes. Failures write `ui/host/playwright-report/index.html`
and retain traces/screenshots in `ui/host/test-results/`; both directories are
ignored by git and uploaded only on CI failure.

For browser-free verification, run `bunx playwright test --list` in `ui/host`.
`bun run test` runs unit tests and excludes Playwright specs.

## Dashboard performance measurements

Correct rendering and fast rendering need separate evidence. The checked-in
smoke suite verifies populated panels, geometry and bounded fragments; it does
not currently enforce a dashboard latency ceiling. A passing native-engine
correctness test is not a timing measurement either.

For a performance run, use a disposable instance with a recorded telemetry
snapshot and a twelve-panel dashboard over an absolute 24-hour window. Record
the candidate source identity, native engine version, platform, memory cap,
query connections, dataset bounds and row/file/byte counts. Keep cold and warm
samples separate and preserve the dashboard spec and variable values.

Measure at least twenty full-dashboard samples, from navigation or refresh to
the last visible panel being painted with its real result. Include requests,
retries and variable resolution in that duration. A completed HTTP response or
a hidden panel does not establish a completed render. Report nearest-rank p95
against the local target of **1500 ms** for the twelve-panel view. CI runner
timings and any calibrated CI ceiling must be reported separately.

For individual panels, collect at least one hundred warm executions per query
shape against the same snapshot and report p95 against **500 ms**. Cover the
headline totals, grouped latency/error measures, time series and logs actually
used by the board. Retain failures and timeout counts alongside latency; do not
make a slow or failed run disappear by retrying until it passes. These targets
are acceptance criteria, not guarantees for arbitrary telemetry volumes.

The current executor compiles ordinary structured aggregates against raw
telemetry. Its disposable read-cache version 4 stores completed-batch trace
candidates and parts, not general minute aggregates. Service map and health
panels use analytical rollups with their own watermark lag and approximate
latency semantics. Do not assume those rollups answer arbitrary raw counts,
rates or quantiles exactly, or report every dashboard query as cached.

The existing native tests exercise whole-window totals, bounded newest buckets
and width-aware 24-hour bucket selection:

```sh
rtk just test ./internal/panel -run '"TestGaugeTotalsMatchStat|TestBudgetKeepsCompleteNewestBuckets|TestWidthAwareCellBuckets"'
```

Use the pinned engine wrapper through `just test`, with the workspace's offline
Go cache/temp environment configured. Those small fixtures verify semantics and
budgets; they do not replace the representative performance run. Record actual
measurements and limitations in the milestone's `docs/benchmarks/` document.

## Dashboard accessibility verification

Run the browser-free host tests and type checks from `ui/host`:

```sh
rtk bun run test
rtk bun run lint
```

The existing theme, chart-unit and row-formatting tests check semantic text and
mark contrast in both schemes. Keyboard tests cover dashboard focus, chart
navigation, range cancellation, full-screen and shortcut preferences. The exact
palette-order test preserves six categorical colour slots; its name alone does
not prove colour-vision-deficiency separation. There is currently no automated
axe scan or CVD validator in the checked-in smoke suite.

For an accessibility review, inspect dashboard and chat panels in both themes
at both smoke widths. Tab through controls and charts; exercise arrows, Enter,
Shift+Left/Right, Cancel, Escape and full-screen return focus. Check the
single-key shortcut switch, typing/menu/dialog exceptions and error inspection
in Data, including an error longer than its Chart preview. Review populated,
empty, failed, stale and truncated states.

Assess WCAG AA text contrast and chart marks against their actual surfaces.
Check all six ordered categorical colours plus Other under colour-vision
deficiency simulation. Browser accessibility scans cannot read canvas text, so
supplement any scan with compiler/token assertions and screenshots.
CI runs the performance spec (`just e2e e2e/performance.spec.ts`) in its own job and
the rest with `FANOUT_E2E_SKIP_PERFORMANCE=1`. `just e2e` runs the axe-core AA scan of dashboards and chat panels in both
themes; `FANOUT_E2E_SCREENSHOTS=1 just e2e` also captures every panel type in
both themes and states for review under `ui/host/test-results/`. Record the
scanner or simulation method and version, findings and manual review results;
an unrun or inconclusive check is not an accessibility pass.

## Agent dashboard benchmark

The evaluation runner tests scoring and transport offline without a provider:

```sh
rtk proxy bun test scripts/dashboard-eval
rtk proxy bun scripts/dashboard-eval/main.ts --mock --no-output
rtk just dashboard-eval-generate-check
```

The mock exercises ten saves and five consecutive edits with synthetic usage.
It is $0 fixture evidence, not an agent-quality or real-data performance result.
The generated server-response fixtures remain generator-owned.

For a real benchmark, the controller supplies private prompts, exact edit
expectations, a fresh replay manifest, authenticated runtime inputs and one
retained cost ledger. Follow [the runner's input and metering instructions](../scripts/dashboard-eval/README.md).
The benchmark requires ten completed dashboard saves, executed checks for every
saved panel, no final invalid spec, median save latency at most 45 seconds and
five exact edits that preserve unrelated authored fields. Save receipts must
identify the immutable versions being scored; reading the latest dashboard is
not a substitute.

Holdout uses explicit dashboard/answer intent denominators instead of the fixed
ten-save/five-edit benchmark. Optional paired judging needs both judge keys and
shares the authoring ledger; missing keys produce an explicit skip and no quality
pass. Keep sealed inputs and their detailed outputs outside the worktree and
do not use them to tune the candidate. The runner exits **0** for a complete pass,
**1** for a measured failure and **2** for invalid, incomplete or budget-blocked
evidence. Unknown cost stops further paid calls pending settlement.

## Documentation checks

Dashboard workflow prose lives in
`site/src/content/docs/guides/build-dashboards.mdx`. HTTP, MCP and role references
are generated from the server; change their generator sources when necessary,
then regenerate and check:

```sh
rtk just docs-generate
rtk just docs-generate-check
rtk just test ./cmd/fanout-docgen
```

With site dependencies already installed, run `rtk bun run build` from `site/`
to check the sidebar and compile pages, then run the table, social-card and
canonical guards included in that script. `just site-build` also installs site
dependencies; use the installed build directly when dependency installation is
outside the verification environment's scope. Neither command publishes the site.
