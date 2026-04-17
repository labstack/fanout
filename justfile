# Fanout justfile

set shell := ["bash", "-euo", "pipefail", "-c"]
set dotenv-load

export CGO_ENABLED := "1"

bin := "fanout"
sock := env("SOCK", "/tmp/pc-fanout.sock")

default:
    @just --list

# Install dev tools + client deps
install:
    go install github.com/air-verse/air@latest
    brew install process-compose pre-commit 2>/dev/null || true
    cd client && bun install
    pre-commit install

# ── Dev ──────────────────────────────────────────────────────────────────────

# Start dev environment (Go auto-reload + Vite HMR)
up:
    process-compose up --unix-socket {{sock}}

# Stop dev environment
down:
    process-compose down --unix-socket {{sock}}

# ── Build ────────────────────────────────────────────────────────────────────

# Build production binary (client + server)
build:
    cd client && bun run build
    rm -rf internal/web/dist/*
    cp -r client/dist/* internal/web/dist/
    go build -o {{bin}} ./cmd/fanout

# Generate TypeScript types from Go block structs + sqlc queries
gen:
    go generate ./internal/ai/...
    cd internal/db && sqlc generate

# Create a new Atlas migration from schema changes
migrate-diff NAME:
    cd internal/db && atlas migrate diff {{NAME}} --env local
    cp internal/db/migrations/*.sql internal/store/migrations/

# Apply migrations (for development — production uses auto-apply on boot)
migrate-apply:
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
    cd client && npx tsc --noEmit
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

# Tag a release (v{YEAR.MONTH}.{NUM})
release:
    #!/bin/bash
    git fetch origin --tags main
    BRANCH=$(git rev-parse --abbrev-ref HEAD)
    if [ "$BRANCH" != "main" ]; then
      echo "release must run from main." >&2
      exit 1
    fi
    if ! git diff --quiet || ! git diff --cached --quiet; then
      echo "release requires a clean tracked worktree." >&2
      exit 1
    fi
    if [ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ]; then
      echo "release must run from an up-to-date main (HEAD must equal origin/main)." >&2
      exit 1
    fi
    MONTH=$(date +%Y.%m)
    LAST=$(git tag --list "v${MONTH}.*" --sort=-v:refname | head -1)
    if [ -z "$LAST" ]; then NUM=1; else NUM=$(( ${LAST##*.} + 1 )); fi
    TAG="v${MONTH}.${NUM}"
    echo "Tagging ${TAG}"
    if git rev-parse "$TAG" >/dev/null 2>&1; then
      echo "Tag $TAG already exists; pushing existing tag."
    else
      git tag "$TAG"
    fi
    git push origin "$TAG"

# ── Ops ──────────────────────────────────────────────────────────────────────

# Deploy to production
deploy *ARGS='':
    ./scripts/yeet.sh {{ARGS}}

# Deploy demo (otel-demo + fanout)
deploy-demo *ARGS='':
    ./demo/yeet.sh {{ARGS}}

# Clean build artifacts (--all also removes lake/)
clean *ARGS='':
    rm -f {{bin}} coverage.out coverage.html
    rm -rf tmp/
    find internal/web/dist -mindepth 1 ! -name '.gitkeep' -delete 2>/dev/null || true
    {{ if ARGS == "--all" { "rm -rf lake/" } else { "" } }}
