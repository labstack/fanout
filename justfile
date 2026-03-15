# Fanout justfile

set dotenv-load

export CGO_ENABLED := "1"

bin := "fanout"

default:
    @just --list

# Build binary (client + server)
build:
    cd client && bun run build
    rm -rf internal/web/dist/*
    cp -r client/dist/* internal/web/dist/
    go build -o {{bin}} ./cmd/fanout

# Dev mode with auto-reload
dev:
    @command -v air >/dev/null || go install github.com/air-verse/air@latest
    air

# Generate TypeScript types from Go block structs
gen:
    go generate ./internal/ai/...

# Run tests
test *ARGS='./...':
    go test {{ARGS}}

# Check formatting
fmt-check:
    @if [ -n "$(gofmt -l .)" ]; then gofmt -d .; exit 1; fi

# Lint
lint:
    golangci-lint run

# Format + vet + lint + build (pre-commit)
check:
    go fmt ./...
    go vet ./...
    just lint
    just build
    @echo "All checks passed"

# Clean build artifacts
clean *ARGS='':
    rm -f {{bin}} coverage.out coverage.html
    rm -rf tmp/
    find internal/web/dist -mindepth 1 ! -name '.gitkeep' -delete 2>/dev/null || true
    {{ if ARGS == "--all" { "rm -rf lake/" } else { "" } }}

# Build docker image
docker TAG="local":
    docker build -t fanout:{{TAG}} .

# Deploy to production
deploy *ARGS='':
    ./scripts/yeet.sh {{ARGS}}

# Deploy demo (otel-demo + fanout)
deploy-demo *ARGS='':
    ./demo/yeet.sh {{ARGS}}
