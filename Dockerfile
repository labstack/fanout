# Build stage
FROM golang:1.24-bookworm AS builder

WORKDIR /app

# Copy go mod files first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build with CGO enabled
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o fanout ./cmd/fanout

# Runtime stage - minimal Debian
FROM debian:bookworm-slim

WORKDIR /app

# Runtime deps only
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*

# Create lake directory
RUN mkdir -p /app/lake

COPY --from=builder /app/fanout .

# HTTP (API + MCP at /mcp), OTLP gRPC
EXPOSE 7520 4317

ENV LAKE_DIR=/app/lake

ENTRYPOINT ["./fanout"]
