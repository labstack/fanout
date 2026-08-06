# Redesign runtime configuration

## Why

Fanout currently exposes dozens of unrelated environment variables and
implicitly reads `.env` files from its working directory. The resulting
contract is difficult for self-hosted operators to review as a whole, and its
highest-precedence `.env.${ENV}` layer can silently override values injected by
the process supervisor or container runtime.

Before the project is opened to outside operators, configuration should become
an explicit, typed product surface: readable as one document, predictable when
overridden by a deployment platform, strict enough to catch misspellings, and
safe to inspect without revealing credentials.

This change affects runtime behavior, the public configuration and CLI
contracts, deployment manifests, and secret handling. It changes no persisted
data or telemetry/API schemas.

## What Changes

- Establish one coherent `FANOUT_` environment-variable namespace as the only
  process-environment configuration surface.
- **BREAKING**: Stop discovering or loading `.env` and `.env.${ENV}` files.
  Fanout will never derive production behavior from its current working
  directory.
- Add an optional YAML configuration document selected by an explicit
  `--config` path. Running without a document remains supported for immutable
  container deployments that use defaults and environment variables.
- Define one deterministic precedence order: built-in defaults, then the
  selected YAML document, then process environment. The fully merged result is
  parsed into the typed Go configuration and validated once before Fanout opens
  data files or listeners.
- Reject unknown YAML keys and unknown `FANOUT_` environment variables so
  spelling mistakes fail startup instead of silently selecting a default. This
  makes deployment configuration version-coupled by design: a manifest must
  contain only settings understood by the Fanout version it starts.
- Publish a complete, commented example document and update Docker, source
  quick-start, deployment, benchmark, and contribution guidance to use the new
  contract.

## Non-Goals

- No alternate environment-variable aliases or automatic name translation.
- No automatic config-file search path, per-environment filename convention,
  or implicit dotenv loading.
- No hot reload. Configuration is resolved once at process startup.
- No remote configuration service, database-backed operator configuration, or
  secret-manager client or `*_FILE` indirection in the Fanout process. Secrets
  use the same YAML or process-environment sources as other settings; examples
  keep them in the environment.
- No command-line flag for every setting. Flags select an operation and, where
  applicable, a config document; runtime values come from the document or
  environment.
- No config dump or other output mode that could reveal secret values.

## Capabilities

### New Capabilities

- `runtime-configuration`: Defines Fanout's configuration sources, schema,
  precedence, and strict validation.

### Modified Capabilities

None. `openspec/specs/` contains no canonical capabilities today, so there is
no existing requirement set to modify; this change introduces the first one.

## Impact

- **Affected code**: `cmd/fanout`, `internal/config` and its callers,
  configuration tests, and startup diagnostics.
- **Affected dependencies**: introduce a layered Go configuration library and
  YAML parser; remove the current environment and dotenv loaders if they are no
  longer used elsewhere.
- **Affected deployments**: Docker arguments and environment variables,
  Kubernetes manifests, systemd units, development shells, benchmark commands,
  and any secret mounts must adopt the new names and optional config path.
- **Security**: credentials may be supplied by YAML or the process environment.
  Documentation examples use environment injection, and startup errors and
  diagnostics must not include credential values.
- **Data and APIs**: no database migration, telemetry rewrite, HTTP response
  change, ingest protocol change, or MCP contract change.
