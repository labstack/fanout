# Fanout justfile

set shell := ["bash", "-euo", "pipefail", "-c"]
set dotenv-load

export CGO_ENABLED := "1"

bin := "bin/fanout"
sock := env("SOCK", "/tmp/pc-fanout.sock")

default:
    @just --list

# Install dev tools + web/site deps
install:
    go install github.com/air-verse/air@latest
    brew install process-compose pre-commit 2>/dev/null || true
    cd web && bun install
    cd site && bun install
    pre-commit install

# ── Dev ──────────────────────────────────────────────────────────────────────

# Start dev environment (Go auto-reload + Vite HMR)
up:
    process-compose up --unix-socket {{sock}}

# Stop dev environment
down:
    @if [ -S "{{sock}}" ]; then \
        process-compose down --unix-socket {{sock}}; \
    else \
        echo "process-compose already down (socket {{sock}} missing)"; \
    fi

# ── Build ────────────────────────────────────────────────────────────────────

# Build production binary (web + server). VERSION embeds via -ldflags;
# defaults to git describe so local builds are self-identifying.
build VERSION=`git describe --tags --always --dirty 2>/dev/null || echo dev`:
    cd web && bun run build
    rm -rf internal/ui/dist/*
    cp -r web/dist/* internal/ui/dist/
    go build -ldflags "-s -w -X main.version={{VERSION}}" -o {{bin}} ./cmd/fanout

# Generate TypeScript types from Go block structs + sqlc queries
gen:
    go generate ./internal/ai/...
    cd internal/db && sqlc generate

# Create a new Atlas migration from schema changes
migrate-diff NAME:
    mkdir -p data/control
    cd internal/db && atlas migrate diff {{NAME}} --env local

# Apply migrations (for development — production uses auto-apply on boot)
migrate-apply:
    mkdir -p data/control
    cd internal/db && atlas migrate apply --env local

# Build docker image
docker TAG="local":
    docker build -t fanout:{{TAG}} .

# ── Quality ──────────────────────────────────────────────────────────────────

# Full check (pre-commit + CI use this)
check:
    go fmt ./...
    go vet ./...
    just lint
    cd web && npx tsc --noEmit
    just build
    @echo "All checks passed"

# Go lint
lint:
    golangci-lint run

# Go format check (CI only — check uses go fmt which auto-fixes)
fmt-check:
    @if [ -n "$(gofmt -l .)" ]; then gofmt -d .; exit 1; fi

# Run Go tests
test *ARGS='./...':
    go test {{ARGS}}

# ── Release ───────────────────────────────────────────────────────────────────

# Tags follow {component}/v{YEAR.MONTH}.{NUM}, e.g. fanout/v2026.04.1, and
# each component has its own GitHub Actions workflow that fires on its prefix.
# Tag per-component releases, auto-detecting which components changed since their last tag.
release:
    #!/bin/bash
    # `set -eo pipefail` catches failures in the main shell and in pipelines.
    # Caveats on bash 3.2 (macOS system bash) with no `inherit_errexit`:
    #   * `local VAR=$(cmd)` masks the subshell exit — we always write
    #     `local VAR; VAR=$(cmd)` so `set -e` sees the assignment directly.
    #   * `set -e` does NOT propagate into $(…) subshells — functions called
    #     that way (`next_tag`) use explicit `|| exit 1` on critical commands.
    set -eo pipefail
    BRANCH=$(git rev-parse --abbrev-ref HEAD)
    if [ "$BRANCH" != "main" ]; then
      echo "release must run from main." >&2; exit 1
    fi
    if ! git diff --quiet || ! git diff --cached --quiet; then
      echo "release requires a clean tracked worktree." >&2; exit 1
    fi
    git fetch origin --tags main
    if [ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ]; then
      echo "release must run from an up-to-date main (HEAD must equal origin/main)." >&2; exit 1
    fi
    MONTH=$(date +%Y.%m)
    RELEASED=0
    next_tag() {
      local PREFIX="$1"
      # `next_tag` is invoked via $(next_tag …), so `set -e` inside this body
      # doesn't propagate back out on bash 3.2 (no `inherit_errexit`). The
      # explicit `|| exit 1` forces the subshell to abort on a real git failure.
      # The `|| true` on grep only swallows grep's "no match" exit — the
      # numeric-suffix filter keeps a stray `…-rc1` tag out of the arithmetic.
      local ALL
      ALL=$(git tag --list "${PREFIX}${MONTH}.*" --sort=-v:refname) || exit 1
      local LAST
      LAST=$(printf '%s\n' "$ALL" | { grep -E "^${PREFIX}${MONTH}\.[0-9]+$" || true; } | head -1)
      local NUM
      if [ -z "$LAST" ]; then NUM=1; else NUM=$(( ${LAST##*.} + 1 )); fi
      echo "${PREFIX}${MONTH}.${NUM}"
    }
    last_tag() {
      git tag --list "${1}*" --sort=-v:refname | head -1
    }
    maybe_release() {
      local NAME="$1" PREFIX="$2"; shift 2; local PATHS=("$@")
      local SINCE; SINCE=$(last_tag "$PREFIX")
      if [ -n "$SINCE" ]; then
        local CHANGES; CHANGES=$(git diff --name-only "$SINCE"..HEAD -- "${PATHS[@]}")
        if [ -z "$CHANGES" ]; then
          echo "  $NAME: no changes"
          return
        fi
      fi
      local TAG; TAG=$(next_tag "$PREFIX")
      echo "  $NAME: ${SINCE:-<first release>} → $TAG"
      git tag "$TAG"
      # Drop the local tag if push fails so next run doesn't skip a number.
      git push origin "$TAG" || { git tag -d "$TAG" >/dev/null; exit 1; }
      RELEASED=$((RELEASED + 1))
    }
    echo "Checking for changes since last release..."
    # Keep the fanout path list in sync with sources the binary embeds or
    # compiles from — anything the Dockerfile or release workflow pulls in
    # must be listed here, or changes there will silently skip the release.
    maybe_release "fanout" "fanout/v" cmd/ internal/ web/ go.mod go.sum Dockerfile
    maybe_release "site" "site/v" site/
    if [ "$RELEASED" -eq 0 ]; then
      echo "Nothing to release."
    fi

# ── Stress / performance suite ───────────────────────────────────────────────

# Performance suite dispatcher. Logic lives in scripts/ + cmd/loadgen; this just
# routes. Run `just stress` (no args) for the subcommand list.
#   local   [gens rate dur]    throwaway fanout + parallel loadgens → rows/s
#                              (gens auto-scales to CPU cores → max utilization)
#   hetzner [type key loc]     provision a Hetzner VM (default cpx32), test, tear down
#   profile [cpusec rate gens] capture CPU/heap/alloc/mutex/block → top hotspots
#   drive   [loadgen flags]    fire loadgen at an already-running fanout
#   watch   [host:port]        tail the incident-relevant metrics
stress *ARGS:
    #!/usr/bin/env bash
    set -euo pipefail
    set -- {{ARGS}}
    sub="${1:-}"; [ $# -gt 0 ] && shift || true
    case "$sub" in
      local)   exec ./scripts/bench.sh "$@" ;;
      hetzner) exec ./scripts/bench-hetzner.sh "$@" ;;
      profile) exec ./scripts/profile.sh "$@" ;;
      drive)   exec go run ./cmd/loadgen "$@" ;;
      watch)   exec ./scripts/stress-watch.sh "$@" ;;
      ""|help|-h|--help)
        echo "just stress <subcommand> [args]"
        echo "  local   [gens rate dur]    throwaway fanout + parallel loadgens → rows/s (auto-scales to cores)"
        echo "  hetzner [type key loc]     provision a Hetzner VM (default cpx32), test, tear down"
        echo "  profile [cpusec rate gens] capture CPU/heap/alloc/mutex/block → top hotspots"
        echo "  drive   [loadgen flags]    fire loadgen at an already-running fanout"
        echo "  watch   [host:port]        tail the incident-relevant metrics" ;;
      *) echo "unknown subcommand: $sub (run 'just stress' for the list)" >&2; exit 1 ;;
    esac

# ── Ops ──────────────────────────────────────────────────────────────────────

# Deploy site + demo to production (Traefik at the edge + site + demo + fanout)
deploy *ARGS='':
    ./scripts/yeet.sh {{ARGS}}

# Clean build artifacts (--all also removes data/)
clean *ARGS='':
    rm -f {{bin}} coverage.out coverage.html
    rm -rf tmp/
    find internal/ui/dist -mindepth 1 ! -name '.gitkeep' -delete 2>/dev/null || true
    {{ if ARGS == "--all" { "rm -rf data/" } else { "" } }}
