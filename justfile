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

# Tags are CalVer: v{YYYY.MM}.{N}, numbered from 1 within each month. Pushing
# the tag triggers release.yml, which builds the multi-architecture image,
# publishes native archives, and moves `latest`.

# Tag the next CalVer release and push it.
release:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ "$(git rev-parse --abbrev-ref HEAD)" != "main" ]; then
      echo "release must run from main." >&2; exit 1
    fi
    if [ -n "$(git status --porcelain)" ]; then
      echo "release requires a clean worktree." >&2; exit 1
    fi
    git fetch origin --tags main
    if [ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ]; then
      echo "release must run from an up-to-date main." >&2; exit 1
    fi
    MONTH=$(date +%Y.%m)
    # Filter to a numeric suffix so a stray pre-release tag cannot break the
    # arithmetic. `|| true` swallows only grep's no-match exit.
    LAST=$(git tag --list "v${MONTH}.*" --sort=-v:refname \
      | { grep -E "^v${MONTH}\.[0-9]+$" || true; } | head -1)
    if [ -z "$LAST" ]; then NUM=1; else NUM=$(( ${LAST##*.} + 1 )); fi
    TAG="v${MONTH}.${NUM}"
    echo "${LAST:-<first release this month>} → ${TAG}"
    git tag "$TAG"
    # Drop the local tag if the push fails, so the next run does not skip a number.
    git push origin "$TAG" || { git tag -d "$TAG" >/dev/null; exit 1; }

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

# ── Gate ─────────────────────────────────────────────────────────────────────

# lefthook's pre-push hook and CI both run this.
check: fmt-check lint ui-check test ui-test
    @echo "All checks passed"

clean:
    rm -rf bin
