# Playwright surfaces

One `playwright.config.ts` runs `specs/app/`, `specs/mcp/` and `specs/visual/`.
App retains the real seeded-server viewport/theme matrix; MCP uses the official
AppBridge with synthetic recorded tool results; visual compares built views in
both themes. Only app depends on seeded setup.

Specs use `fixtures/test.ts`, composed with `mergeTests` from one-concern fixture
modules. `data/` holds synthetic JSON, `host/` builds and serves the test-only
mock, and `support/` holds helpers and their Vitest unit tests. The failure
reporter stays at `failure-report.ts`. Baselines live in
`__screenshots__/<project>/<platform>/<spec>/`.

Covered: existing dashboard rendering, geometry, accessibility and performance;
MCP loading, recorded views, guidance, errors, cancellation, connection fallback,
axe and keyboard controls; overview/performance/trace screenshots. Uncovered:
real third-party hosts, remote OAuth, live telemetry and AI, mobile and arbitrary
helper requests. `app/screenshots.spec.ts` remains opt-in review capture because
it does not assert screenshots.

See [the contribution testing guide](../../../docs/testing.md) for setup,
browser installation, backend lifecycle, recording data, commands and CI images.
