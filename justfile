# Fanout justfile

set dotenv-load

export CGO_ENABLED := "1"

bin := "fanout"
sock := "{{sock}}"

default:
    @just --list

# Install dev tools + client deps
install:
    go install github.com/air-verse/air@latest
    brew install process-compose 2>/dev/null || true
    cd client && bun install

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

# Generate TypeScript types from Go block structs
gen:
    go generate ./internal/ai/...

# Build docker image
docker TAG="local":
    docker build -t fanout:{{TAG}} .

# ── Quality ──────────────────────────────────────────────────────────────────

# Full check (pre-commit + CI use this)
check:
    go fmt ./...
    go vet ./...
    just lint
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
    MONTH=$(date +%Y.%m)
    LAST=$(git tag --list "v${MONTH}.*" --sort=-v:refname | head -1)
    if [ -z "$LAST" ]; then NUM=1; else NUM=$(( ${LAST##*.} + 1 )); fi
    TAG="v${MONTH}.${NUM}"
    echo "Tagging ${TAG}"
    git tag "$TAG" && git push origin "$TAG"

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
