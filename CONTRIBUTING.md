# Contributing to Fanout

Thanks for your interest. Issues and pull requests are both welcome.

## Getting set up

```sh
just install   # browser dependencies and git hooks
just build     # browser assets, then the binary
just check     # the full gate
```

You need Go 1.27.2 and a C/C++ toolchain on native Linux or macOS (amd64 or arm64),
[Bun](https://bun.sh), Node.js 24.19.0, [just](https://just.systems),
[golangci-lint](https://golangci-lint.run/), and
[Lefthook](https://github.com/evilmartians/lefthook). Running in local auth mode
also requires a 32-character authentication code secret. SMTP and AI chat are
optional; selecting an AI provider requires its key — see [README](README.md#build-from-source).

Copy `.env.example` to ignored `.env` for personal development settings and
credentials. Every entry is commented out, with types, defaults, and secret file
forms in comments; uncomment only values you want to supply. Copying the template
sets nothing and preserves the image's environment defaults. After building,
`just run` loads optional `./.env` with Node's native dotenv
parser (`parseEnv`); inherited environment values win, including empty values.
The binary itself reads only process environment and validates the settings;
production launchers and deployments supply their own values.
Keep this file out of Git and Docker inputs. See
[runtime secret delivery](docs/operations.md#runtime-secret-delivery).

The DuckDB wrapper downloads the pinned, checksum-verified engine from
[Fanout's dependency artifact mirror](https://github.com/labstack/fanout/releases/tag/duckdb-v2.0.0-alpha43763)
on the first build. Linux needs the C++ runtime (`libstdc++`); macOS needs
Xcode Command Line Tools and `libc++`. Cross-compilation is unsupported.
Use `just` recipes or prefix Go commands with the wrapper:

```sh
bash scripts/with-duckdb.sh go build ./cmd/fanout
bash scripts/with-duckdb.sh go test ./internal/ingest
```

Editor tooling also needs the same build tags, headers, and linker flags.
Launch a fresh editor process through the wrapper, for example
`bash scripts/with-duckdb.sh code .`, so its Go tools inherit that environment.
Alternatively configure your editor to launch `gopls` through
`bash scripts/with-duckdb.sh gopls serve`. An already-running editor process
must be restarted to inherit these variables.

The dependency mirror preserves the original DuckDB `v2.0.0-alpha43763`
archives at upstream commit `96063b9e39` byte for byte. Its `duckdb-` tag
namespace is separate from Fanout's product CalVer releases; new engine bytes
require a new artifact tag and checksum pins. It never supplies a second engine
or downloads extensions at runtime.

## Before you open a pull request

Run `just check` and `just test-race`. Together they match the CI gate:
formatting, linting, browser dependency audits, embedded-asset freshness, Go
and browser tests, then the Go suite under the race detector.

Run `just e2e` with Google Chrome installed for the separate CI browser smoke
suite. See [browser smoke testing](docs/testing.md) for coverage and failure artifacts.

`just install` also installs [Lefthook](https://lefthook.dev)'s Git hooks, which run
formatting and linting on commit and the full gate on push. That is the fastest
way to avoid a red CI run.
The pre-push hook may be skipped when Lefthook detects no changed files;
CI runs the same `just check` unconditionally and enforces the gate.

The browser workspace uses `package.json` overrides for transitive security
fixes that their direct dependencies have not locked yet. Keep those overrides
until the upstream ranges resolve to patched versions; `just ui-audit` accepts
only the existing build-only braces advisory exception documented in the justfile.

## Things that surprise people

**The browser assets are committed.** `ui/host` builds into `internal/ui/dist`
and its MCP app builds into `internal/mcp/apps`; both outputs are tracked in
git because `go:embed` needs them present in a source checkout. If you change
anything under `ui/`, run `just ui` and commit the regenerated output.
The binary is only as fresh as the last UI build.
`just ui-check` fails when the embedded bytes do not match a fresh build, so
CI catches this; it is part of `just check`.

**Diagrams are generated.** Edit the JSON source in `docs/diagrams/`, run
`just diagrams`, and commit the JSON and both SVG copies: `docs/diagrams/`
and `site/public/diagrams/`. Each component cites repository evidence at the
revision pinned in `meta.repository`. Rendering requires archify v3.1.0 from
a local install (`ARCHIFY_HOME`, default `$HOME/.agents/skills/archify`), the
Playwright dependencies in `ui/host/node_modules`, and Chrome for the dual-theme
SVG export. Archify is never installed through npm. Committed SVGs serve the
README and docs site, so only diagram editors need the rendering environment.
The evidence checkout defaults to this repository. If your `origin` points to
a fork, use `DIAGRAMS_EVIDENCE_ROOT=/path/to/upstream-clone just diagrams`, where
the clone's credential-free `origin` matches `meta.repository.url`
(`https://github.com/labstack/fanout.git`) and contains the pinned revision.
The script checks both before rendering. JSON inputs, SVG outputs, and the site
copy still use your contributor checkout.

**The social preview card is generated.** `site/public/social-card.png` is
rendered from `docs/media/social-card.typ` by `just social-card`, which needs Inter on a
font path — set `FANOUT_FONT_PATH` to a directory holding it. CI has no such
directory, so nothing re-renders the card to compare it; what `site-build` does
assert is that the committed PNG exists and still matches the dimensions the
site's head advertises. If you edit the source, run the recipe and commit the
PNG in the same change. The recipe fails when the
font is missing rather than shipping a card in a substitute face, which typst
would otherwise do without complaint.

**Runtime configuration is strict.** `internal/config/config.go` owns the
schema. New settings need an environment name, a documentation `group` tag,
a purpose comment, a test, and matching documentation. Custom settings use
`FANOUT_` plus a clear name, adding the area only when it clarifies; vendor
credentials and endpoints keep the vendor's documented names. Unknown owned
names fail startup, while unrelated shared-vendor/tool variables are ignored.
Secrets use `secret:"true"` and automatically accept an `_FILE` form; do not
set both forms nonempty. Run `just docs-generate` to regenerate the settings
reference and `.env.example`; `just docs-generate-check` verifies them.
The template leaves all values empty.
Personal environment files and operator profiles stay ignored and outside
Docker. Never shell-source dotenv.

**SQLite migrations own the control schema.** Write forward SQL changes in
`internal/db/migrations` using Goose annotations. `just db-migrate-create NAME`
creates a timestamped migration; `just db-gen` regenerates the sqlc bindings
from that directory. There is no separate schema file. Published migrations
are immutable: add a migration rather than editing one already applied.
The checksum ledger in `internal/db/migrations_test.go` makes the local and CI
test gates reject edits, renames, and deletions. Extend it for new migrations;
do not update checksums of published migrations.
Fanout embeds and applies them at startup; `just db-migrate-apply DB` applies
them manually with the same database guard used at startup. A version table
without a positive applied version does not make an existing schema managed.
DuckDB maintains its own query schema.

## Project layout

```text
cmd/fanout/        process composition and the single entry point
internal/          ingest, storage, query, agent, MCP, auth, alerts
ui/host/           React browser host and shared MCP app renderer (build-time)
docs/diagrams/     archify JSON sources and exported dual-theme SVGs
```

## Commits and pull requests

Write commit messages that explain why the change is needed, not only what it
does. Keep a pull request to one concern; several small ones are easier to
review than one large one.

If a change affects runtime behavior, public contracts, data, or security, say
so explicitly in the description.

Keep credentials, private telemetry, local workstation paths, and private AI
session links out of source files, documentation, commit messages, and pull
request descriptions. Local agent caches and evaluation records belong in
ignored directories. CI scans the repository source tree for secrets as well
as scanning the runtime container.

## Reporting bugs

Include the version (`fanout --version`), how Fanout is configured, and what
you expected instead of what happened. For anything security-related, follow
[SECURITY.md](SECURITY.md) rather than opening an issue.

## Contributor License Agreement

Before your first pull request can be merged, you need to sign the
[CLA](CLA.md). A bot comments on the pull request with a sentence to reply
with; that is the whole process, and it is only needed once.

The CLA grants LabStack the right to relicense contributions. That is what
keeps future licensing decisions possible — without it, any change would
require permission from every past contributor individually.

## License

Fanout is [Apache-2.0](LICENSE). Contributions are accepted under the same
license, subject to the CLA above.
