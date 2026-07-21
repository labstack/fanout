# syntax=docker/dockerfile:1

# Bun is a build compiler only. Neither Bun nor Node is copied into the final
# image or launched by the Fanout process.
FROM oven/bun:1.3.14 AS ui-apps-build
WORKDIR /app
COPY ui/apps/package.json ui/apps/bun.lock ./ui/apps/
RUN cd ui/apps && bun install --frozen-lockfile
COPY ui/apps/ ./ui/apps/
COPY internal/mcp/apps/ ./internal/mcp/apps/
RUN cd ui/apps && bun run build

FROM oven/bun:1.3.14 AS ui-host-build
WORKDIR /app
COPY ui/host/package.json ui/host/bun.lock ./ui/host/
RUN cd ui/host && bun install --frozen-lockfile
COPY ui/host/ ./ui/host/
COPY internal/ui/ ./internal/ui/
RUN cd ui/host && bun run build

# CGO is on (DuckDB), so release CI builds on each target architecture.
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=ui-apps-build /app/internal/mcp/apps/ ./internal/mcp/apps/
COPY --from=ui-host-build /app/internal/ui/dist/ ./internal/ui/dist/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w -X main.version=${VERSION}" -o fanout ./cmd/fanout

FROM debian:bookworm-slim AS fanout
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates wget \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /var/lib/fanout
COPY --from=build /app/fanout /usr/local/bin/fanout
WORKDIR /var/lib/fanout
EXPOSE 7520 4317
ENV DATA_DIR=/var/lib/fanout/data \
    HTTP_ADDR=:7520 \
    OTLP_GRPC_ADDR=:4317
ENTRYPOINT ["fanout"]
