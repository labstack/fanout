# Repository Guidelines

## Project Structure & Module Organization
The full layout and the component architecture live in
[`docs/architecture.md`](docs/architecture.md); this is orientation only, and
that document wins if the two disagree.

`cmd/fanout` composes the single runtime binary; `cmd/bench` is the supported
load generator. Backend code lives under `internal/` by domain: `ingest/` for
OTLP/gRPC, `lake/` and `query/` for DuckLake/Parquet and DuckDB,
`observability/` for typed query contracts, `agent/` for AG-UI, `mcp/` for
standard tools and MCP Apps, `dashboard/` and `alert/` for control features,
and `api/` plus `auth/` for HTTP and identity.

The embedded React host is in `ui/host`; portable React MCP Apps are in
`ui/apps`; the public Astro site is in `site/`. Canonical shipped behavior lives
in `openspec/specs/`, proposed work in `openspec/changes/`, and `docs/` is a
concise index/runbook surface. Treat `data/`, `tmp/`, UI `dist/` directories,
and benchmark output as generated runtime output.

## Build, Test, and Development Commands
Use `just` for the normal workflow:

- `just install` installs Air, `process-compose`, Lefthook, the pinned OpenSpec CLI, and all UI/site dependencies.
- `just up` starts the Go server, UI build watcher, and public site through process-compose.
- `just build` builds MCP Apps and the browser host, embeds them, then compiles `./cmd/fanout` to `bin/fanout`.
- `just check` runs `go fmt`, `go vet`, `golangci-lint`, and a full build.
- `just test ./...` runs the Go test suite.
- `just docs-check` strictly validates every OpenSpec spec and change.
- `just gen` regenerates sqlc bindings after control-database query changes.

For UI-only work, run `bun run lint`, `bun run test` where available, and
`bun run build` in `ui/host` or `ui/apps`. Use `cd site && bun run build` for
public-documentation changes.

## Coding Style & Naming Conventions
Go code should remain `gofmt`-clean; package names stay lowercase and tests
should sit beside implementation in `*_test.go`. TypeScript uses 2-space
indentation and the package type-check scripts. Keep domain files aligned by
name, for example `trace.go` with `trace_test.go`. AG-UI is the browser stream
contract and MCP Apps are the rich tool-result contract.

## Testing Guidelines
Backend changes should include focused Go tests in the touched package. Match CI
locally with `just test -race -coverprofile=coverage.txt ./...` when changing
query, ingest, MCP, agent, auth, or API paths. `ui/host` has Vitest coverage;
frontend changes must at least pass package lint, tests where present, and
build. Behavioral changes require an OpenSpec change and synchronized public
docs before archive.

## Commit & Pull Request Guidelines
Recent history uses Conventional Commit prefixes like `feat:`, `fix:`,
`refactor:`, and `chore:`. Keep subjects imperative and scoped to one change.
PRs should target `main`, describe behavior changes clearly, link issues when
relevant, and include screenshots for UI updates. Call out changes to MCP/AG-UI
responses, MCP Apps, HTTP APIs, migrations, ingest, or canonical specs.

## Configuration & Safety
DuckDB requires CGO, so keep `CGO_ENABLED=1` for local builds. Store local
settings in `.env`, never commit secrets, and do not commit data from `data/`.
Ingest tokens, browser sessions, MCP OAuth tokens, and metrics credentials are
separate security domains and must never be documented or implemented as
interchangeable.
