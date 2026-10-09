# Contributing to Fanout

Thanks for your interest. Issues and pull requests are both welcome.

## Getting set up

```sh
just install   # browser dependencies and git hooks
just build     # browser assets, then the binaries
just check     # the full gate
```

You need Go 1.27.2 and a C/C++ toolchain on native Linux or macOS (amd64 or arm64),
[Bun](https://bun.sh), [just](https://just.systems),
[golangci-lint](https://golangci-lint.run/), and
[Lefthook](https://github.com/evilmartians/lefthook). Running in local auth mode
also requires a 32-character authentication code secret; SMTP and an AI key are
optional — see [README](README.md#requirements).

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

`just install` also installs [Lefthook](https://lefthook.dev), which runs
formatting and linting on commit and the full gate on push. That is the fastest
way to avoid a red CI run.

The browser workspace uses `package.json` overrides for transitive security
fixes that their direct dependencies have not locked yet. Keep those overrides
until the upstream ranges resolve to patched versions; `just ui-audit` accepts
only the existing build-only braces advisory exception documented in the justfile.

## Things that surprise people

**The browser assets are committed.** `ui/host` builds into `internal/ui/dist`
and its MCP app builds into `internal/mcp/apps`; both outputs are tracked in
git because `go:embed` needs them present in a source checkout. If you change
anything under `ui/`, run `just ui` and commit the regenerated output.
`just ui-check` fails when the embedded bytes do not match a fresh build, so
CI catches this, but it is friendlier to catch it yourself.

**Diagrams are generated.** `docs/diagrams/*.svg` is rendered from the `.d2`
source beside it. Edit the `.d2`, run `just diagrams`, and commit both. The
committed SVG comes from d2 0.7.1; a different version re-renders every file
and produces a large diff that is not a real change.

**The social preview card is generated.** `site/public/social-card.png` is
rendered from `docs/media/social-card.typ` by `just social-card`, which needs Inter on a
font path — set `FANOUT_FONT_PATH` to a directory holding it. CI has no such
directory, so nothing re-renders the card to compare it; what `site-build` does
assert is that the committed PNG exists and still matches the dimensions the
site's head advertises. If you edit the source, run the recipe and commit the
PNG in the same change. The recipe fails when the
font is missing rather than shipping a card in a substitute face, which typst
would otherwise do without complaint.

**Runtime configuration is strict.** [`fanout.example.yaml`](fanout.example.yaml)
is the complete schema. New settings need a YAML key, a `FANOUT_` environment
name, a test, and matching documentation; unknown names deliberately fail
startup.

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

## Commits and pull requests

Write commit messages that explain why the change is needed, not only what it
does. Keep a pull request to one concern; several small ones are easier to
review than one large one.

If a change affects runtime behavior, public contracts, data, or security, say
so explicitly in the description.

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
