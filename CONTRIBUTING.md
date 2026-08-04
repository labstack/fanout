# Contributing to Fanout

Thanks for your interest. Issues and pull requests are both welcome.

## Getting set up

```sh
just install   # browser dependencies and git hooks
just build     # browser assets, then the binaries
just check     # the full gate
```

You need Go with `CGO_ENABLED=1` (DuckDB is a cgo dependency),
[Bun](https://bun.sh), and [just](https://just.systems). Running the server
additionally needs SMTP credentials and an AI provider key — see
[README](README.md#requirements).

## Before you open a pull request

Run `just check`. It is exactly what CI runs, so a green local gate means a
green build. It covers formatting, linting, embedded-asset freshness, Go and
browser tests, and spec validation.

`just install` also installs [Lefthook](https://lefthook.dev), which runs
formatting and linting on commit and the full gate on push. That is the fastest
way to avoid a red CI run.

## Things that surprise people

**The browser assets are committed.** `ui/host` builds into `internal/ui/dist`
and `ui/apps` builds into `internal/mcp/apps`, and both outputs are tracked in
git because `go:embed` needs them present in a source checkout. If you change
anything under `ui/`, run `just ui` and commit the regenerated output.
`just ui-check` fails when the committed bytes do not match a fresh build, so
CI catches this, but it is friendlier to catch it yourself.

**Diagrams are generated.** `docs/diagrams/*.svg` is rendered from the `.d2`
source beside it. Edit the `.d2`, run `just diagrams`, and commit both. The
committed SVG comes from d2 0.7.1; a different version re-renders every file
and produces a large diff that is not a real change.

**Behavior is specified.** Product behavior lives in `openspec/`. Changes to
shipped behavior belong in an OpenSpec change under `openspec/changes/` rather
than only in code. Run `just spec-check` to validate.

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

## License

Fanout is [AGPL-3.0](LICENSE). Contributions are accepted under the same
license.
