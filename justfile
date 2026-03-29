# Fanout justfile

set dotenv-load

export CGO_ENABLED := "1"

bin := "fanout"

default:
    @just --list

# Start dev environment (Go + Vite with auto-reload)
up:
    process-compose up

# Stop dev environment
down:
    process-compose down

# Build binary (client + server)
build:
    cd client && bun run build
    rm -rf internal/web/dist/*
    cp -r client/dist/* internal/web/dist/
    go build -o {{bin}} ./cmd/fanout

# Generate TypeScript types from Go block structs
gen:
    go generate ./internal/ai/...

# Run Go tests
test *ARGS='./...':
    go test {{ARGS}}

# Check Go formatting
fmt-check:
    @if [ -n "$(gofmt -l .)" ]; then gofmt -d .; exit 1; fi

# Lint Go
lint:
    golangci-lint run

# Check client (TypeScript + build)
client-check:
    cd client && bun run build

# Full pre-commit check (Go + client)
check:
    go fmt ./...
    go vet ./...
    just lint
    just client-check
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

# Run fanout binary directly
run:
    ./{{bin}}
