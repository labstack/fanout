# Design: Redesign runtime configuration

## Context

See `proposal.md` for motivation. Fanout needs one explicit configuration
surface across local binaries, supervisors, and containers. Source composition,
typed decoding, validation, diagnostics, and process exit need clear ownership,
and working-directory dotenv files must not affect runtime behavior.

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

Use Koanf v2 with its map, file, and YAML providers. Defaults load first, the
selected document second, and environment last. Environment assignments are
parsed against the reflected Go field type before entering Koanf; the final map
is unmarshaled with weak conversions disabled into the existing flat runtime
`Config` using dotted Koanf tags, so application packages do not acquire nested
source-layer types.

This is preferred over Viper because provider order is explicit and the API
surface is smaller. Direct `yaml.v3` plus hand-written pointer overlays would
avoid a dependency but duplicate presence and merge logic for every field.

### Keep configuration in `internal/config`

The package owns defaults, YAML, environment mapping, sizing, and validation;
Runtime field names stay stable unless a name itself is misleading, limiting
the blast radius for consumers.

### Make the struct declaration the source of schema metadata

Each exported configuration field declares a dotted `koanf` key, its exact
`FANOUT_` environment name, and an optional default in struct tags. Loader
reflection builds the defaults map, typed environment mapping, and allowlists
from those tags. This avoids maintaining separate maps that can drift while
retaining a flat struct for consumers.

The YAML document groups keys by operational area (`server`, `ingest`,
`storage`, `alerts`, `agent`, `smtp`, `auth`, `metrics`, and `mcp`). Environment
names use the short existing concepts with a mandatory `FANOUT_` prefix; they
do not mechanically derive from YAML paths.

### Validate each boundary without printing values

Load the selected YAML into an isolated Koanf instance, compare its flattened
leaf keys and scalar types with the schema, reject null leaves, then merge it.
Empty recognized sections are removed before the merge so they preserve
defaults. Scan environment names before loading, reject unknown `FANOUT_`
names, ignore variables outside that namespace, and exempt only the well-known
service variables injected by Kubernetes and Docker link-style networking.
Empty environment assignments are absent overrides. Decode errors and
invariant errors identify fields but never serialize the effective map,
preventing credentials from entering logs. A YAML document that contains
credentials must not grant group or other access.

`Load` returns `(Config, error)`. After a successful merge it resolves adaptive
sizing and calls `Validate`; `cmd/fanout` logs the error and exits before its
existing directory creation and listener setup.

### Keep the CLI bootstrap-only

Use the standard library flag package to add `--config PATH` and retain
`--version`/`version`. There are no per-setting flags and no subcommands. The
container image supplies `--config /etc/fanout/fanout.yaml` as its default
command so image-specific values do not outrank a mounted operator document.
Container-specific documents start from that shipped file and retain its
externally reachable HTTP and OTLP listener addresses.

## Risks / Trade-offs

- **Strict `FANOUT_` validation makes manifests version-coupled** → document
  that deployments must use settings supported by the selected image version;
  ignore only standard platform service-discovery collisions.
- **YAML can contain secrets** → examples inject secrets through environment,
  errors never dump the merged map, and the loader rejects group/world-readable
  documents that contain credential fields.
- **Reflection can hide schema mistakes until runtime** → loader tests require
  every exported field to have unique key/environment tags and verify defaults,
  precedence, unknown-key rejection, and type errors.

## Deployment Plan

1. Ship the example YAML and `FANOUT_` environment contract with the loader.
2. Update the Docker image defaults and all repository commands and docs.
3. Remove dotenv and environment-parser dependencies after their imports are
   gone.
4. Deployments either mount a YAML document and pass `--config`, or inject
   documented `FANOUT_` variables. No data migration is required.
