# Fanout justfile

set shell := ["bash", "-euo", "pipefail", "-c"]
set dotenv-load

export CGO_ENABLED := "1"

bin := "fanout"
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

# Tag a per-component release. COMPONENT must be one of: fanout, site.
# Tags follow {COMPONENT}/v{YEAR.MONTH}.{NUM}, e.g. fanout/v2026.04.1.
# Each component has its own GitHub Actions workflow that fires on its prefix.
release COMPONENT:
    #!/bin/bash
    case "{{COMPONENT}}" in
      fanout|site) ;;
      *) echo "unknown component: {{COMPONENT}} (expected fanout|site)" >&2; exit 1 ;;
    esac
    BRANCH=$(git rev-parse --abbrev-ref HEAD)
    if [ "$BRANCH" != "main" ]; then
      echo "release must run from main." >&2
      exit 1
    fi
    if ! git diff --quiet || ! git diff --cached --quiet; then
      echo "release requires a clean tracked worktree." >&2
      exit 1
    fi
    git fetch origin --tags main
    if [ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ]; then
      echo "release must run from an up-to-date main (HEAD must equal origin/main)." >&2
      exit 1
    fi
    MONTH=$(date +%Y.%m)
    PREFIX="{{COMPONENT}}/v${MONTH}"
    LAST=$(git tag --list "${PREFIX}.*" --sort=-v:refname | head -1)
    if [ -z "$LAST" ]; then
      NUM=1
    else
      SUFFIX="${LAST##*.}"
      if [[ ! "$SUFFIX" =~ ^[0-9]+$ ]]; then
        echo "non-numeric suffix in tag '$LAST' — expected digits, got '$SUFFIX'" >&2
        exit 1
      fi
      NUM=$(( SUFFIX + 1 ))
    fi
    TAG="${PREFIX}.${NUM}"
    echo "Tagging ${TAG}"
    if git rev-parse "$TAG" >/dev/null 2>&1; then
      echo "Tag $TAG already exists; pushing existing tag."
    else
      git tag "$TAG"
    fi
    git push origin "$TAG"

# ── Ops ──────────────────────────────────────────────────────────────────────

# Deploy site + demo to production (Caddy at the edge + fanout-site + otel-demo)
deploy *ARGS='':
    ./scripts/yeet.sh {{ARGS}}

# Clean build artifacts (--all also removes data/)
clean *ARGS='':
    rm -f {{bin}} coverage.out coverage.html
    rm -rf tmp/
    find internal/ui/dist -mindepth 1 ! -name '.gitkeep' -delete 2>/dev/null || true
    {{ if ARGS == "--all" { "rm -rf data/" } else { "" } }}
