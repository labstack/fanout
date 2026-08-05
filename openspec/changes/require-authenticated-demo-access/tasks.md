## 1. Remove server bypasses

- [x] 1.1 Remove both environment settings and startup warnings. Evidence: `internal/env/config.go` and `internal/env/config_test.go`.
- [x] 1.2 Remove the synthetic browser viewer and require a persisted session for telemetry reads. Evidence: `internal/api/auth_middleware.go` and `TestTelemetryReadsRequireAuthenticatedAccount`.
- [x] 1.3 Remove tokenless OTLP ingest and always enforce the stored ingest token. Evidence: `internal/ingest/auth.go` and its authorization tests.

## 2. Keep demos and benchmarks usable

- [x] 2.1 Simplify the browser auth contract to persisted users only and rebuild embedded assets. Evidence: `ui/host/src/auth*.ts*`, browser tests, and `internal/ui/dist`.
- [x] 2.2 Require the benchmark ingest token and add authenticated query-session forwarding. Evidence: `cmd/bench/config.go`, `newQueryRequest`, and focused tests.
- [x] 2.3 Update current reproduction instructions to provision an account and use scoped credentials. Evidence: `docs/benchmarks/two-vcpu.md`.

## 3. Align documentation and verify

- [x] 3.1 Update security guidance, code comments, and active OpenSpec context. Evidence: `SECURITY.md`, `internal/observability`, and `openspec/config.yaml`.
- [x] 3.2 Run focused Go and browser tests. Evidence: changed Go packages, 25 browser tests, and race tests all pass.
- [x] 3.3 Run the project checks and strict OpenSpec validation. Evidence: formatting, lint, all Go tests, browser lint/tests/build, and all three OpenSpec changes pass. The committed-asset dirty-tree guard cannot run before these intentional asset changes are committed; two consecutive `just ui-host` builds produced the same asset names.
