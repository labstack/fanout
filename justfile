# Fanout — task runner. Run `just` with no arguments to list every recipe.
#
# Naming: recipes are named <subsystem>-<action>, so `just --list` (which sorts
# alphabetically) groups them. Unprefixed recipes act on the Go module, which
# is the primary artifact; `ui-*` and `db-*` namespace the rest. A `-check`
# suffix always means non-mutating verification, safe to run on a dirty tree.
#
# The ordering rule this file exists to encode: the browser workspaces write
# into `internal/ui/dist` and `internal/mcp/apps`, both of which are committed
# and compiled in via `go:embed`. The binary is therefore only as fresh as the
# last UI build, so `build` always runs the UI first and `ui-check` guards the
# committed bytes against drifting from their source.

set shell := ["bash", "-euo", "pipefail", "-c"]

export CGO_ENABLED := "1"

# Everything `go:embed` compiles into the binary. Keep in sync with `outDir` in
# ui/host/vite.config.ts and the `cp` targets in ui/apps/package.json.
embedded := "internal/ui/dist internal/mcp/apps"

default:
    @just --list

# Split out so CI installs exactly what a contributor does, without also
# writing git hooks into the runner.

# Browser dependencies only.
ui-deps:
    cd ui/apps && bun install --frozen-lockfile
    cd ui/host && bun install --frozen-lockfile

# Everything needed to work on the repo: dependencies plus the git hooks.
install: ui-deps
    lefthook install

# ── Build ────────────────────────────────────────────────────────────────────

# Portable React MCP Apps → internal/mcp/apps/*.html
ui-apps:
    cd ui/apps && bun run build

# AG-UI browser host → internal/ui/dist
ui-host:
    cd ui/host && bun run build

# Every embedded browser asset.
ui: ui-apps ui-host

# Browser assets, then the binaries that embed them.
build VERSION=`git describe --tags --always --dirty 2>/dev/null || echo dev`: ui
    go build -ldflags "-s -w -X main.version={{VERSION}}" -o bin/fanout ./cmd/fanout
    go build -ldflags "-s -w" -o bin/bench ./cmd/bench

# CI publishes ghcr.io/labstack/fanout; this is for trying the image locally
# without pushing anything.

# Build the container image.
docker TAG="local":
    docker build --build-arg VERSION={{TAG}} -t fanout:{{TAG}} .

# ── Release ──────────────────────────────────────────────────────────────────

# Tags are CalVer: v{YYYY.M}.{N}, numbered from 0 within each month. Pushing
# the tag triggers release.yml, which builds the multi-architecture image,
# mirrors it to Docker Hub, publishes native archives, and moves `latest`.

# Tag the next CalVer release and push it.
release:
    ./scripts/release.sh

# Regenerate the legal inventory for every shipped Go target and browser app.
notices:
    go run ./internal/cmd/notices -root .

# Fail if THIRD_PARTY_NOTICES does not match the release dependency graph.
notices-check:
    go run ./internal/cmd/notices -root . -check

# ── Database ─────────────────────────────────────────────────────────────────

# Regenerate the SQLite query bindings with sqlc.
db-gen:
    cd internal/db && sqlc generate

# Create a migration from schema changes.
db-migrate-diff NAME:
    mkdir -p data/control
    cd internal/db && atlas migrate diff {{NAME}} --env local

# Apply migrations (development; production auto-applies on boot).
db-migrate-apply:
    mkdir -p data/control
    cd internal/db && atlas migrate apply --env local

# ── Go quality ───────────────────────────────────────────────────────────────

fmt:
    gofmt -w .

# Non-mutating counterpart to `fmt`, for the gate.
fmt-check:
    @if [ -n "$(gofmt -l .)" ]; then gofmt -d .; exit 1; fi

# Standalone; `lint` already runs govet via golangci-lint's standard set.
vet:
    go vet ./...

lint:
    golangci-lint run

test *ARGS='./...':
    go test {{ARGS}}

# duckdb-go's CGO vector readers trip checkptr's unsafe-pointer *alignment*
# check under -race on linux/amd64 and abort with "misaligned pointer
# conversion". The read is benign at runtime. `-d=checkptr=0` disables only
# that alignment check — the -race data-race detector stays on.

# Run the Go tests under the race detector.
test-race:
    go test -race -gcflags=all=-d=checkptr=0 ./...

# ── Browser quality ──────────────────────────────────────────────────────────

# `ui` and `ui-check` already type-check via `bun run build`, so the gate does
# not invoke this separately — it exists for fast feedback on its own.

# Type-check both browser workspaces.
ui-lint:
    cd ui/apps && bun run lint
    cd ui/host && bun run lint

# Run the browser host's vitest suite.
ui-test:
    cd ui/host && bun run test

# Audit both independent browser dependency graphs. Security overrides live in
# each package.json so installs, local checks, and CI all resolve the same fixes.
ui-audit:
    cd ui/apps && bun audit
    cd ui/host && bun audit

# Uses git as the backup, which is sound only because these outputs are
# committed: the check refuses to run when they are already dirty, and always
# restores them afterwards, so it never mutates the working tree.

# Fail if the committed embedded assets differ from a fresh build.
ui-check:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ -n "$(git status --porcelain -- {{embedded}})" ]; then
      echo "ui-check: embedded assets have uncommitted changes." >&2
      echo "  Commit or stash them first — this check restores them from git." >&2
      exit 1
    fi
    just ui
    # --porcelain rather than `git diff`: a rebuild can introduce a new
    # content-hashed filename, which is untracked and so invisible to git diff.
    if [ -z "$(git status --porcelain -- {{embedded}})" ]; then
      echo "ui-check: embedded assets match a fresh build."
      exit 0
    fi
    echo "ui-check: embedded assets are STALE — they do not match a fresh build:" >&2
    git status --porcelain -- {{embedded}} >&2
    git checkout -- {{embedded}}
    git clean -qfd {{embedded}}
    echo "  Run 'just ui' and commit the result." >&2
    exit 1

# ── Diagrams ─────────────────────────────────────────────────────────────────

# Renders docs/diagrams/*.d2 beside each source. Deliberately outside `build`
# and `check`: the SVG is committed, so only someone editing a diagram needs d2
# installed at all.
#
# The committed SVG was produced by d2 0.7.1. d2's output changes between minor
# versions, so a different version re-renders every file and produces a large
# diff that looks like a change but is not — check `d2 --version` before
# committing one.

# Render every d2 diagram to SVG.
diagrams:
    #!/usr/bin/env bash
    set -euo pipefail
    if ! command -v d2 >/dev/null; then
      echo "d2 not found — install with: brew install d2" >&2
      exit 1
    fi
    for src in docs/diagrams/*.d2; do
      d2 "$src" "${src%.d2}.svg"
    done

# ── Documentation site ───────────────────────────────────────────────────────

# Rewrite the generated settings reference from internal/config.
docs-generate:
    go run ./cmd/fanout-docgen

# Fail when a committed settings page is behind the configuration type.
docs-generate-check:
    go run ./cmd/fanout-docgen --check

# Install the documentation site's dependencies from the lockfile.
#
# Named to match `ui-deps`, and used by CI for the same reason: `check` ends with
# site-build, which fails as a bare exit 127 when node_modules is absent.
site-deps:
    cd site && npm ci --no-audit --no-fund

# Add or update a site dependency. `site-deps` is the reproducible install;
# this is the one that may change the lockfile.
site-install:
    cd site && npm install --no-audit --no-fund

# Serve the documentation site locally with live reload.
site: docs-generate
    cd site && npm run dev

# Build the documentation site into site/dist.
#
# Depends on the *checking* recipe, not the writing one: `check` is a gate, and
# a gate that rewrites the tree it is inspecting cannot tell you whether the
# tree was already right. Use `just site` for the writing path during
# development.
site-build: docs-generate-check site-deps
    cd site && npm run build

# Renders the GitHub social preview card from docs/media/social-card.typ.
# Outside `build` and `check` for the same reason as the diagrams above: the PNG
# is committed, and CI has no font path to re-render it with.
#
# The card needs Inter, which is not vendored here — the browser bundle carries
# its own copy for the UI, and a second copy in the tree to draw one image is a
# poor trade. Typst warns about an unknown font family and still exits 0, so a
# missing face does not fail the render; it quietly produces a card set in
# whatever typst found instead. This reads the warning back and fails on it.
#
# The version is pinned for the same reason d2 is: typst re-lays out text
# between releases, so a different one rewrites every glyph position and
# produces a diff that is not a real change.
#
# The mark comes from ui/host/public/favicon.svg, the canonical asset that
# internal/brand tracks, so the card cannot drift from the product logo.
social-card:
    #!/usr/bin/env bash
    set -euo pipefail
    want_typst="0.15.1"
    if ! command -v typst >/dev/null; then
      echo "typst not found — install with: brew install typst" >&2
      exit 1
    fi
    have_typst=$(typst --version | awk '{print $2}')
    if [ "$have_typst" != "$want_typst" ]; then
      echo "typst $have_typst is installed; the committed card was rendered with $want_typst" >&2
      echo "another version re-lays out every glyph, so the diff would not be a real change" >&2
      exit 1
    fi
    if [ -z "${FANOUT_FONT_PATH:-}" ]; then
      echo "set FANOUT_FONT_PATH to a directory holding Inter" >&2
      echo "the OFL original is at https://github.com/google/fonts/tree/main/ofl/inter" >&2
      exit 1
    fi
    # Rendered aside and moved into place only once it is known good, so a run
    # that fell back to a substitute face cannot leave that card in the tree.
    # A directory, not `mktemp -t <name>`: that reserves a name without the .png
    # suffix typst needs, so appending one both leaves the reserved file behind
    # and writes to a path nothing reserved.
    staged_dir=$(mktemp -d)
    trap 'rm -rf "$staged_dir"' EXIT
    staged="$staged_dir/social-card.png"
    render=$(typst compile --root . --font-path "$FANOUT_FONT_PATH" --ppi 96 --format png \
      docs/media/social-card.typ "$staged" 2>&1)
    if [ -n "$render" ]; then echo "$render"; fi
    if echo "$render" | grep -qi "unknown font family"; then
      echo "typst could not find a font it was asked for — the card above is set in a fallback face" >&2
      exit 1
    fi
    mv "$staged" docs/media/social-card.png
    echo "rendered docs/media/social-card.png"

# ── Gate ─────────────────────────────────────────────────────────────────────

# lefthook's pre-push hook and CI both run this.
#
# docs-generate-check comes before the site build for the same reason it exists:
# a settings page that no longer matches the type the loader binds is a
# documented setting the binary would reject, and it should fail here rather
# than be published.
check: fmt-check lint ui-audit ui-check notices-check test ui-test docs-generate-check site-build
    @echo "All checks passed"

clean:
    rm -rf bin
