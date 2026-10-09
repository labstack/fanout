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

The suite covers all fifteen visualizations, terminal states, panel/header
geometry, service-map clipping and font sizes, and five Go-generated chat
fragments in sandboxed MCP app iframes. It checks 1100 and 1440 pixel widths in
light and dark themes and rejects every browser error or console warning/error.
Fragment height messages must stop after settling. CI runs the same recipe on
pull requests and pushes. Failures write `ui/host/playwright-report/index.html`
and retain traces/screenshots in `ui/host/test-results/`; both directories are
ignored by git and uploaded only on CI failure.

For browser-free verification, run `bunx playwright test --list` in `ui/host`.
`bun run test` runs unit tests and excludes Playwright specs.
