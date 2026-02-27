# syntax=docker/dockerfile:1

# --- Client build stage ---
FROM oven/bun:latest AS client

WORKDIR /app/client
COPY client/package.json client/bun.lock ./
RUN bun install --frozen-lockfile
COPY client/ .
RUN bun run build

# --- Server build stage ---
FROM golang:1.26-bookworm AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
COPY --from=client /app/client/dist/ internal/web/dist/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -ldflags="-s -w" -o fanout ./cmd/fanout

# --- Runtime stage ---
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates wget \
    && rm -rf /var/lib/apt/lists/*

RUN mkdir -p /var/lib/fanout

COPY --from=builder /app/fanout /usr/local/bin/fanout

WORKDIR /var/lib/fanout

EXPOSE 7520 4317

ENV LAKE_DIR=/var/lib/fanout
ENV HTTP_ADDR=:7520
ENV OTLP_GRPC_ADDR=:4317

ENTRYPOINT ["fanout"]
