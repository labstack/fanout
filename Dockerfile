# Build stage
FROM golang:1.25-bookworm AS builder

WORKDIR /app

# Copy go mod files first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build with CGO enabled
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o fanout ./cmd/fanout

# Runtime stage
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Create data directory (industry standard for app data)
RUN mkdir -p /var/lib/fanout

# Copy binary to standard location
COPY --from=builder /app/fanout /usr/local/bin/fanout

WORKDIR /var/lib/fanout

# Ports: HTTP (API + MCP), OTLP gRPC
EXPOSE 7520 4317

ENV LAKE_DIR=/var/lib/fanout
ENV HTTP_ADDR=:7520
ENV OTLP_GRPC_ADDR=:4317

ENTRYPOINT ["fanout"]
