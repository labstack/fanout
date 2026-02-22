# Fanout justfile

set dotenv-load

export CGO_ENABLED := "1"

bin := "fanout"
addr := env("HTTP_ADDR", ":7520")

default:
    @just --list

# Build React client
build-client:
    cd client && bun run build
    rm -rf internal/web/dist/*
    cp -r client/dist/* internal/web/dist/

# Build binary
build: build-client
    go build -o {{bin}} ./cmd/fanout

# Run server
run: build
    ./{{bin}}

# Dev mode with auto-reload
dev:
    @command -v air >/dev/null || go install github.com/air-verse/air@latest
    air

# Run tests
test *ARGS='./...':
    go test {{ARGS}}

# Test with coverage
cov:
    go test -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html
    @echo "Coverage: coverage.html"

# Run benchmarks
bench:
    go test -bench=. -benchmem ./...

# Format code
fmt:
    go fmt ./...

# Run checks
check: fmt
    go vet ./...
    golangci-lint run
    just build
    @echo "All checks passed"

# Quick check (no lint)
qcheck: fmt
    go vet ./...
    just build

# Clean build artifacts (keeps lake data)
clean:
    rm -f {{bin}} coverage.out coverage.html
    rm -rf tmp/
    find internal/web/dist -mindepth 1 ! -name '.gitkeep' -delete 2>/dev/null || true

# Clean everything including data
clean-all: clean
    rm -rf lake/

# Update deps
update:
    go get -u ./... && go mod tidy

# Install tools
tools:
    go install github.com/air-verse/air@latest
    go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Health check
health:
    @curl -sf http://localhost{{addr}}/healthz && echo "OK" || echo "Not running"

# Show lake stats
lake:
    @du -sh lake 2>/dev/null || echo "No data"
    @find lake -name "*.parquet" 2>/dev/null | wc -l | tr -d ' '

# Tail logs
logs SERVICE="":
    @if [ -n "{{SERVICE}}" ]; then \
        curl -s "http://localhost{{addr}}/api/logs?service={{SERVICE}}" | jq; \
    else \
        curl -s "http://localhost{{addr}}/api/logs" | jq; \
    fi

# Tag release
tag VERSION:
    git tag v{{VERSION}}
    git push origin v{{VERSION}}

# Build docker image
docker TAG="local":
    docker build -t fanout:{{TAG}} .

# Install git hooks
hooks:
    echo 'just check' > .git/hooks/pre-commit
    chmod +x .git/hooks/pre-commit
    @echo "Hooks installed"

# Sync demo data from server
sync-data SERVER="ubuntu@fanout.run" SRC="/data/fanout-demo/":
    rsync -avz --progress {{SERVER}}:{{SRC}} lake/

# Deploy to production
deploy *ARGS='':
    ./scripts/yeet.sh {{ARGS}}
