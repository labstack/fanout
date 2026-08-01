# operations Specification

## Purpose

Defines Fanout's deployable artifact, startup validation, observable health, protected diagnostics, configuration boundaries, and safe instance lifecycle.

## Requirements

### Requirement: Production is a self-contained native artifact
Fanout SHALL ship as a native executable and container image with the compiled browser host and MCP Apps embedded. Bun and Node SHALL be build-time dependencies only.

#### Scenario: Container starts on a production host
- **WHEN** the published image runs with valid configuration and persistent storage
- **THEN** one Fanout process serves HTTP, OTLP, APIs, agent, MCP, and browser assets

### Requirement: Invalid required configuration fails fast
Fanout MUST reject startup for unsupported authentication or AI provider modes, missing AI credentials, incomplete local SMTP or code-secret configuration, incomplete OIDC configuration, invalid session lifetimes, invalid trusted-proxy CIDRs, invalid MCP public URL, negative retention, non-positive flush or rollup values, or a partial TLS pair.

#### Scenario: Local authentication lacks SMTP credentials
- **WHEN** `AUTH_MODE=local` and required SMTP fields are absent
- **THEN** Fanout exits with a configuration error before opening its listeners

### Requirement: Configuration sources have deterministic precedence
Fanout SHALL load `.env` without overwriting process environment, then load `.env.${ENV}` as the environment-specific override, with field defaults applying only when no higher-precedence value exists.

#### Scenario: Production override changes retention
- **WHEN** `.env.production` sets `RETENTION_DAYS` and `ENV=production`
- **THEN** that value overrides the baseline `.env` and process value according to the documented profile behavior

### Requirement: Liveness and readiness are distinct
Fanout SHALL expose `/healthz` for process liveness and `/readyz` plus `/api/health` for dependency-aware readiness.

#### Scenario: Query storage is unavailable
- **WHEN** the process is alive but its required query dependency fails the readiness probe
- **THEN** liveness remains distinguishable from a non-ready service

### Requirement: Operational metrics are protected by default
Fanout SHALL expose Prometheus metrics at `/-/metrics` to a valid metrics bearer token or an authenticated administrator with operations-read capability unless `METRICS_PUBLIC=true`. Public read and non-administrator browser roles MUST NOT make metrics public.

#### Scenario: Unauthenticated scraper reaches a private instance
- **WHEN** metrics are not public and no valid metrics token is supplied
- **THEN** Fanout rejects the scrape

### Requirement: Profiling is opt-in and administrator-only
Fanout SHALL register `/debug/pprof/*` only when `PPROF_ENABLED=true`, require an administrator with operations-read capability, and warn that profiling must not be exposed to an untrusted network.

#### Scenario: Viewer requests a heap profile
- **WHEN** profiling is enabled but the session has viewer role
- **THEN** Fanout denies the request

### Requirement: Reverse-proxy trust is explicit
Fanout SHALL honor forwarded client IP headers only from configured trusted proxy CIDRs. A canonical HTTPS `PUBLIC_URL` behind plaintext local HTTP MUST be paired with trusted proxy configuration so secure-cookie and audit context are not based on arbitrary headers.

#### Scenario: Direct client spoofs a forwarded address
- **WHEN** its source is outside the trusted proxy CIDRs
- **THEN** Fanout ignores the forwarded client IP for security decisions and auditing

### Requirement: Direct TLS is shared and modern
When a complete certificate pair is configured, Fanout SHALL serve HTTPS and OTLP gRPC with the same credentials and a TLS 1.3 minimum. Operators MAY instead terminate TLS at a trusted reverse proxy.

#### Scenario: Direct TLS is enabled
- **WHEN** both TLS files are valid
- **THEN** both listeners use encrypted transport and reject protocol versions below TLS 1.3

### Requirement: Schema upgrades are automatic and backups are whole-instance
Fanout SHALL apply control-database migrations during startup. Operators SHALL be able to restore an instance by stopping it, restoring the complete `DATA_DIR`, and starting a compatible or newer binary; downgrade across a migration is not guaranteed.

#### Scenario: Operator upgrades a backed-up instance
- **WHEN** a newer Fanout version starts against the restored data root
- **THEN** required forward migrations run before the instance serves dependent features

### Requirement: Shutdown is ordered and observable
Fanout SHALL handle termination signals and fatal background errors by gracefully stopping OTLP, draining telemetry writes, cancelling background work, and stopping HTTP within a bounded grace period.

#### Scenario: Lake writer reports a fatal error
- **WHEN** the error reaches the process supervisor path
- **THEN** Fanout logs the failure and executes the same ordered shutdown used for a termination signal
