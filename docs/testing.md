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
