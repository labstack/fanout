# syntax=docker/dockerfile:1

# Bun is a build compiler only. Neither Bun nor Node is copied into the final
# image or launched by the Fanout process.
FROM oven/bun:1.3.14 AS ui-apps-build
WORKDIR /app
COPY ui/apps/package.json ui/apps/bun.lock ./ui/apps/
RUN cd ui/apps && bun install --frozen-lockfile
COPY ui/theme.ts ./ui/
COPY ui/apps/ ./ui/apps/
COPY internal/mcp/apps/ ./internal/mcp/apps/
RUN cd ui/apps && bun run build

FROM oven/bun:1.3.14 AS ui-host-build
WORKDIR /app
COPY ui/host/package.json ui/host/bun.lock ./ui/host/
RUN cd ui/host && bun install --frozen-lockfile
COPY ui/theme.ts ./ui/
COPY ui/host/ ./ui/host/
COPY internal/ui/ ./internal/ui/
RUN cd ui/host && bun run build

# DuckDB needs CGO, so this stage must run on the target architecture — there
# is no cross toolchain here. `--platform=$BUILDPLATFORM` is therefore only
# correct while TARGETPLATFORM equals BUILDPLATFORM. Adding an architecture to
# the CI matrix means either a native runner for it or QEMU, not a GOARCH flag.
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
ARG VERSION=dev

LABEL org.opencontainers.image.title="Fanout" \
      org.opencontainers.image.description="Single-binary, agent-native OpenTelemetry investigation" \
      org.opencontainers.image.source="https://github.com/labstack/fanout" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.version="${VERSION}"

# wget is here for HEALTHCHECK below, nothing else.
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates wget \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system fanout \
    && useradd --system --gid fanout --home-dir /var/lib/fanout fanout \
    && mkdir -p /var/lib/fanout/data \
    && chown -R fanout:fanout /var/lib/fanout

COPY --from=build /app/fanout /usr/local/bin/fanout

# Runs unprivileged. A host directory bind-mounted at /var/lib/fanout/data must
# be writable by UID/GID of the `fanout` user, or the process cannot open its
# data directory.
USER fanout
WORKDIR /var/lib/fanout

EXPOSE 7520 4317
ENV DATA_DIR=/var/lib/fanout/data \
    HTTP_ADDR=:7520 \
    OTLP_GRPC_ADDR=:4317

# Startup does DuckDB catalog attachment and maintenance, so the grace period
# is generous relative to the check interval.
#
# GET, not `--spider`: /healthz rejects HEAD with 405, so a spider probe marks
# a healthy container unhealthy.
HEALTHCHECK --interval=30s --timeout=3s --start-period=40s --retries=3 \
    CMD wget --quiet --tries=1 --output-document=- http://127.0.0.1:7520/healthz >/dev/null || exit 1

ENTRYPOINT ["fanout"]
