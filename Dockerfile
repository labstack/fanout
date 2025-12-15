# Build stage
FROM golang:1.23-bookworm AS builder

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

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Create data directory
RUN mkdir -p /data

COPY --from=builder /app/fanout .

# Ports: HTTP (API + MCP), OTLP gRPC
EXPOSE 7520 4317

ENV LAKE_DIR=/data
ENV HTTP_ADDR=:7520
ENV OTLP_GRPC_ADDR=:4317

ENTRYPOINT ["./fanout"]
