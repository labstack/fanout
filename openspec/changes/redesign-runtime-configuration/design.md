# Design: Redesign runtime configuration

## Context

See `proposal.md` for motivation. The current `internal/env.Config` combines
environment names and defaults in struct tags. `env.Load` implicitly reads
`.env`, overloads it with `.env.${ENV}`, parses process environment with
`caarlos0/env`, validates, logs warnings, and exits the process on error.
`cmd/fanout` calls it before opening storage or listeners. README and Docker
examples expose the unprefixed variables directly.

The configuration type is consumed throughout ingest, storage, query, auth,
agent, MCP, and API packages. Those consumers need effective typed values, not
knowledge of where a value originated.

## Goals / Non-Goals

**Goals**

- Keep source composition in one small package and leave runtime consumers
  source-agnostic.
- Make every external name and default discoverable from the typed config
  declaration and the example document.
- Return errors from configuration loading; only the command owns process exit.
- Preserve existing machine-aware DuckDB sizing after source resolution.

**Non-Goals**

- Track or report the source of every effective value.
- Add subcommands or a general CLI framework.
- Add duration- or collection-specific syntactic sugar beyond the documented
  Go-compatible scalar forms.

## Decisions

### Use Koanf for ordered composition

Use Koanf v2 with its map, file, YAML, and environment providers. Defaults load
first, the selected document second, and environment last. The final map is
unmarshaled once into the existing flat runtime `Config` using dotted Koanf
tags, so application packages do not acquire nested source-layer types.

This is preferred over Viper because provider order is explicit and the API
surface is smaller. Direct `yaml.v3` plus hand-written pointer overlays would
avoid a dependency but duplicate presence and merge logic for every field.

### Rename the package to `internal/config`

The package owns defaults, YAML, environment mapping, sizing, and validation;
`env` no longer describes it. The rename is internal and mechanical. Runtime
field names stay stable unless a name itself is misleading, limiting the blast
radius to imports.

### Make the struct declaration the source of schema metadata

Each exported configuration field declares a dotted `koanf` key, its exact
`FANOUT_` environment name, and an optional default in struct tags. Loader
reflection builds the defaults map and the allowlists from those tags. This
avoids maintaining separate maps that can drift while retaining a flat struct
for consumers.

The YAML document groups keys by operational area (`server`, `ingest`,
`storage`, `alerts`, `agent`, `smtp`, `auth`, `metrics`, and `mcp`). Environment
names use the short existing concepts with a mandatory `FANOUT_` prefix; they
do not mechanically derive from YAML paths.

### Validate each boundary without printing values

Load the selected YAML into an isolated Koanf instance, compare its flattened
leaf keys with the schema allowlist, then merge it. Scan environment names
before loading and reject unknown `FANOUT_` names. Decode errors and invariant
errors identify fields but never serialize the effective map, preventing
credentials from entering logs.

`Load` returns `(Config, error)`. After a successful merge it resolves adaptive
sizing and calls `Validate`; `cmd/fanout` logs the error and exits before its
existing directory creation and listener setup.

### Keep the CLI bootstrap-only

Use the standard library flag package to add `--config PATH` and retain
`--version`/`version`. There are no per-setting flags and no subcommands. The
container entrypoint continues to start the server with no arguments.

## Risks / Trade-offs

- **Strict `FANOUT_` validation makes manifests version-coupled** → document
  that deployments must use settings supported by the selected image version;
  unprefixed platform variables remain outside Fanout's namespace and ignored.
- **YAML can contain secrets** → examples inject secrets through environment,
  errors never dump the merged map, and operators remain responsible for file
  permissions.
- **Reflection can hide schema mistakes until runtime** → loader tests require
  every exported field to have unique key/environment tags and verify defaults,
  precedence, unknown-key rejection, and type errors.
- **Breaking names can make an upgrade fail validation** → this is intentional;
  README and the example document provide the complete replacement contract.

## Migration Plan

1. Add the example YAML and new `FANOUT_` names in the same release as the
   loader.
2. Update the Docker image defaults and all repository commands and docs.
3. Remove dotenv and environment-parser dependencies after their imports are
   gone.
4. Deployments either mount a YAML document and pass `--config`, or rename their
   injected environment variables. No data migration is required.

Rollback requires restoring the previous binary and its previous unprefixed
environment names; configuration files introduced here are ignored by that
binary unless separately translated.
