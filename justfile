# Fanout justfile

default:
    @just --list

# Install dependencies and tools
deps:
    go mod download
    go install github.com/a-h/templ/cmd/templ@latest

# Generate templ files
gen:
    templ generate

# Build binary (CGO required for DuckDB)
build: gen
    CGO_ENABLED=1 go build -o fanout ./cmd/fanout

# Run server
run: build
    ./fanout

# Dev mode with auto-reload
dev:
    @which air > /dev/null || go install github.com/air-verse/air@latest
    air

# Run tests
test:
    CGO_ENABLED=1 go test ./...

# Format code
fmt:
    go fmt ./...
    templ fmt .

# Run go vet
vet:
    go vet ./...

# Run golangci-lint
lint:
    golangci-lint run

# All checks (used by pre-commit)
check: gen fmt vet lint build
    @echo "✓ All checks passed"

# Quick check (no lint)
qcheck: gen vet build
    @echo "✓ Quick check passed"

# Clean build artifacts
clean:
    rm -f fanout
    rm -rf lake/

# Update dependencies
update:
    go get -u ./...
    go mod tidy

# Health check
health:
    @curl -s http://localhost:7520/healthz || echo "Server not running"

# Show endpoints
endpoints:
    @echo "HTTP:  http://localhost:7520"
    @echo "OTLP:  localhost:4317"
    @echo "MCP:   http://localhost:7520/mcp"

# Lake stats
lake:
    @du -sh ./lake 2>/dev/null || echo "No lake dir"
    @find ./lake -name "*.parquet" 2>/dev/null | wc -l | xargs echo "Parquet files:"

# Create and push a release tag (triggers CI release)
tag VERSION:
    git tag v{{VERSION}}
    git push origin v{{VERSION}}

# Build Docker image locally
docker:
    docker build -t fanout:local .
