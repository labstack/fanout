# Browser tests

Install Go, Bun, just and Google Chrome, then run `just ui-deps` and
`just e2e-browsers`. The latter invokes the locked Playwright CLI to install its
bundled Chromium and required Linux OS libraries; CI smoke uses the same recipe
before running MCP tests on a clean Ubuntu runner. Linux may request sudo for OS
packages. From `ui/host`, run `bun run e2e --help` for the pinned Playwright CLI.
The app matrix uses system Google Chrome. No AI provider key is
needed. Run `just e2e` from the repository root to rebuild the embedded SPA,
MCP App and `bin/fanout`, then run every surface. `just check` remains browser-free.

One config owns `ui/host/e2e/specs/`: `app/` contains the existing seeded-server
specs, `mcp/` checks the built MCP App through the official AppBridge, and
`visual/` contains screenshot assertions. Each spec imports the composed
`e2e/fixtures/test` runner; `just ui-boundaries-check` enforces this, and its
failure-injection tests run in `just script-tests` and `just check`.
`fixtures/` separates app sessions, browser faults, mock-host navigation,
accessibility scans and keyboard navigation. `data/` contains synthetic JSON;
`support/` contains shared helpers and their unit tests. Vitest still discovers
all `*.test.ts` there and excludes Playwright specs. Site recording helper tests
still run through the site's existing test commands.

The app projects are `app-1100-light`, `app-1100-dark`, `app-1440-light` and
`app-1440-dark`, each with one worker. Their shared `setup` dependency starts the
seeded backend once; global teardown stops it after dependent projects finish.
Rerunning setup in UI Mode stops owned instances before replacement. Each launch
has a unique identity, PID and private directory in a separate ownership registry;
readiness must match the spawned PID and identity. Teardown checks ownership before
signalling and removes every registered instance and its session/data directory,
including failed launches, even if shared readiness was overwritten.
Run ownership regressions without a browser using
`bun test e2e/support/app-lifecycle.test.ts` from `ui/host`;
`bun run test -- e2e/support` runs all support tests with the configured Vitest
environment.
MCP and visual projects have no setup dependency and never boot Fanout.

```sh
just e2e-browsers                # locked Chromium and its OS dependencies
just e2e                         # every surface, including performance
just e2e-mcp                     # recorded MCP App protocol and accessibility
just e2e-visual                  # screenshot comparisons on this platform
just e2e-ui                      # Playwright UI Mode
just e2e-update-snapshots         # accept local visual changes after review
just e2e-linux-baselines          # accept Linux visuals in the pinned CI image
just e2e e2e/specs/app/performance.spec.ts
```

Screenshot baselines live under
`ui/host/e2e/__screenshots__/<project>/<platform>/<spec>/<name>.png`. Review
actual/diff images before accepting changes; update only the visual project.
Darwin baselines are generated locally. Linux baselines must come from the pinned
Playwright image used by `just e2e-linux-baselines` and CI, with `--ipc=host`.
The recipe mounts this checkout and installs Bun through npm because the image
lacks unzip; locked UI dependencies use a separate container volume.
In UI Mode, enable and run the `setup` project before app tests.
No Go build is needed for visual tests. `just e2e-image-check`
fails if the image's `/ms-playwright/.docker-info` driver version differs from
installed `@playwright/test`. CI uploads reports, results and any generated Linux
baselines. Missing Linux baselines fail the comparison and are available in the
artifact for review and acceptance; CI never silently accepts changed pixels.

The existing `app/screenshots.spec.ts` stays in app: it captures opt-in review
images and manifests, but has no screenshot assertions. Its
`FANOUT_E2E_SCREENSHOTS` gate is unchanged.

## Recorded MCP host

`e2e/host/server.mjs` builds current `panels.html` with `vite.apps.config.ts`,
setting `FANOUT_APPS_OUT` to a temporary directory, then builds the test host.
It serves only those production files on `127.0.0.1` port zero with
`Cache-Control: no-store`; Playwright captures `listening on <port>` into
`E2E_PORT`. SIGTERM closes the listener and removes build output. It never
changes the embedded HTML and adds no product route.

The mock host selects scenario/theme via query parameters and embeds the view
in an `allow-scripts` sandbox. AppBridge sends tool input and structured results
with a JSON text mirror, or cancellation, after initialization. A held result
checks loading; an unbridged scenario checks the SDK connection deadline with
Playwright's clock. Host context carries light/dark theme and inline display
mode. Recorded helper calls are matched by complete input; unrecorded calls,
resource reads, links and other requests fail loudly. The view currently requests
only server tools and size changes; the host can read the built panels resource.

To refresh recordings, build the server and run from the repository root:

```sh
just build
cd ui/host
bun e2e/host/record.ts
```

The recorder starts the same disposable `e2eseed` instance as app tests, calls
real MCP tools over its authenticated loopback connection, saves only synthetic
inputs/results to `e2e/data/mcp.json`, and cleans up. It rejects external-instance
configuration. It records overview health, topology, performance/Slow operations,
trace detail/correlated logs, log search, a custom panel, no-data guidance and a
tool error, plus refresh, variable, exemplar and trace helper results. Absolute recorded
windows keep assertions independent of the current clock. Credentials remain in
private temporary state and are never recorded.

MCP specs cover both themes, loading, every recorded view, no-data, errors,
cancellation, connection failure, serious/critical axe violations and keyboard
access to controls. Visual specs cover overview, performance and trace in both
themes. These tests do not cover real ChatGPT/Claude host integration, remote
MCP/OAuth handshakes, live telemetry, AI answers, arbitrary helper input, mobile
layouts or every visual state. App specs retain real-server and handcrafted
fragment coverage below.

## Seeded backend lifecycle

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
and retain failure screenshots and first-retry traces in `ui/host/test-results/`; both directories are
ignored by git. CI uploads available browser evidence after every run.

For browser-free discovery, run `bun run e2e --list` in `ui/host`.
`bun run test` runs unit tests and excludes Playwright specs.

## Dashboard performance measurements

Correct rendering and fast rendering need separate evidence. The checked-in
smoke suite verifies populated panels, geometry and bounded fragments. A
separate performance spec records twenty navigation-to-paint samples
per theme for a twelve-panel, 24-hour fixture and enforces a calibrated
1800 ms p95 ceiling in CI. The local representative-data target remains
1500 ms. A passing native-engine
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
used by the dashboard. Retain failures and timeout counts alongside latency; do not
make a slow or failed run disappear by retrying until it passes. These targets
are acceptance criteria, not guarantees for arbitrary telemetry volumes.

The executor compiles ordinary structured aggregates against raw
telemetry. Its disposable read-cache version 4 stores completed-batch trace
candidates and parts, not general minute aggregates. Service maps combine
analytical rollups with a raw client-span query for external peers. That query
materializes the filtered client projection once for reuse by peer and edge
aggregates; its child-span and observed-service checks also read spans. Health
panels use analytical rollups. Rollup data has separate watermark lag and approximate latency
semantics. Do not assume those rollups answer arbitrary raw counts,
rates or quantiles exactly, or report every dashboard query as cached.

The native tests exercise whole-window totals, bounded newest buckets
and width-aware 24-hour bucket selection:

```sh
just test ./internal/panel -run 'TestGaugeTotalsMatchStat|TestBudgetKeepsCompleteNewestBuckets|TestWidthAwareCellBuckets'
```

Use the pinned engine wrapper through `just test`, with the workspace's offline
Go cache/temp environment configured. Those small fixtures verify semantics and
budgets; they do not replace the representative performance run. Keep actual
measurements and limitations with the milestone's verification records, outside
the repository.

## Dashboard accessibility verification

Run the browser-free host tests and type checks from `ui/host`:

```sh
bun run test
bun run lint
```

The theme, chart-unit and row-formatting tests check semantic text and
mark contrast in both schemes. Keyboard tests cover dashboard focus, chart
navigation, range cancellation, full-screen and shortcut preferences. The exact
palette-order test preserves six categorical colour slots; its name alone does
not prove colour-vision-deficiency separation. The checked-in suite runs axe-core
WCAG AA scans; it has no automated CVD validator.

For an accessibility review, inspect dashboard and chat panels in both themes
at both smoke widths. Tab through controls and charts; exercise arrows, Enter,
Shift+Left/Right, Cancel, Escape and full-screen return focus. Check the
single-key shortcut switch, typing/menu/dialog exceptions and error inspection
in Data, including an error longer than its Chart preview. Review populated,
empty, failed, stale and truncated states.

Check that accessible names contain the visible labels of trace links, service-map
nodes, filter chips, flame-graph spans and the account menu. Service-map node names
must also include health; full trace IDs and service details remain available in
titles.

Assess WCAG AA text contrast and chart marks against their actual surfaces.
Check all six ordered categorical colours plus Other under colour-vision
deficiency simulation. Browser accessibility scans cannot read canvas text, so
supplement any scan with compiler/token assertions and screenshots.
CI runs the performance spec (`just e2e e2e/specs/app/performance.spec.ts`) in its own job and
the rest with `FANOUT_E2E_SKIP_PERFORMANCE=1`. `just e2e` runs the axe-core AA scan of dashboards and chat panels in both
themes; `FANOUT_E2E_SCREENSHOTS=1 just e2e` also captures every panel type in
both themes and states for review under `ui/host/test-results/`. Record the
scanner or simulation method and version, findings and manual review results;
an unrun or inconclusive check is not an accessibility pass.

## Agent dashboard benchmark

The evaluation runner tests scoring and transport offline without a provider:

```sh
bun test scripts/dashboard-eval
bun scripts/dashboard-eval/main.ts --mock --no-output
just dashboard-eval-generate-check
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
`site/src/content/docs/docs/guides/dashboards.mdx`. HTTP, MCP and role references
are generated from the server; change their generator sources when necessary,
then regenerate and check:

```sh
just docs-generate
just docs-generate-check
just test ./cmd/fanout-docgen
```

With site dependencies installed, run `npm run build` from `site/`
to check the sidebar, compile pages and run its table, social-card, canonical
and benchmark guards. `just site-build` also installs site
dependencies; use the installed build directly when dependency installation is
outside the verification environment's scope. Neither command publishes the site.

Refresh the README's light and dark System overview captures with `just landing-captures` (real seeded server and local Chrome). To capture an existing instance, set `FANOUT_LANDING_BASE_URL` to its URL and `FANOUT_LANDING_SESSION_COOKIE` to a `name=value` session cookie before running the same command; no server starts and no data is seeded.

To record chat and dashboard clips, set `FANOUT_LANDING_RECORDINGS=1` and the external-instance variables above, then run `bun run e2e e2e/specs/app/landing-recordings.spec.ts --project=app-1440-light --project=app-1440-dark` from `ui/host`. Follow [landing-recordings.md](../site/scripts/landing-recordings.md) to encode and upload them.
