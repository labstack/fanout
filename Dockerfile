# syntax=docker/dockerfile:1

# Bun is a build compiler only. Neither Bun nor Node is copied into the final
# image or launched by the Fanout process.
FROM oven/bun:1.4.0@sha256:5ff609364c049b54eb0ff560ec96319729a972078ef2c755d758f0c6ef89c2d6 AS ui-apps-build
WORKDIR /app
COPY ui/apps/package.json ui/apps/bun.lock ./ui/apps/
RUN cd ui/apps && bun install --frozen-lockfile
COPY ui/*.ts ./ui/
COPY ui/apps/ ./ui/apps/
COPY internal/mcp/apps/ ./internal/mcp/apps/
RUN cd ui/apps && bun run build

FROM oven/bun:1.4.0@sha256:5ff609364c049b54eb0ff560ec96319729a972078ef2c755d758f0c6ef89c2d6 AS ui-host-build
WORKDIR /app
COPY ui/host/package.json ui/host/bun.lock ./ui/host/
RUN cd ui/host && bun install --frozen-lockfile
COPY ui/*.ts ./ui/
COPY ui/host/ ./ui/host/
COPY internal/ui/ ./internal/ui/
RUN cd ui/host && bun run build

# DuckDB needs CGO, so this stage must run on the target architecture — there
# is no cross toolchain here. `--platform=$BUILDPLATFORM` is therefore only
# correct while TARGETPLATFORM equals BUILDPLATFORM. Adding an architecture to
# the CI matrix means either a native runner for it or QEMU, not a GOARCH flag.
FROM --platform=$BUILDPLATFORM golang:1.27-bookworm@sha256:ded31c68586d2e49e760acc2e65a884b23d032e9bbbed0ae0c55abd3fcaf4452 AS build
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

# Distroless has no shell with which to create mutable paths. Prepare the data
# directory here, then copy it with the runtime user's ownership below.
RUN mkdir -p /runtime/var/lib/fanout/data

FROM cgr.dev/chainguard/glibc-dynamic:latest@sha256:205572d5e48117e14b44b42627890fa8d3e8e65bb37a80abb3317e5151e7f35b AS fanout
ARG VERSION=dev

LABEL org.opencontainers.image.title="Fanout" \
      org.opencontainers.image.description="Single-binary, agent-native OpenTelemetry investigation" \
      org.opencontainers.image.source="https://github.com/labstack/fanout" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}"

COPY --from=build /app/fanout /usr/local/bin/fanout
COPY fanout.docker.yaml /etc/fanout/fanout.yaml
COPY LICENSE NOTICE THIRD_PARTY_NOTICES TRADEMARK.md /usr/share/licenses/fanout/
COPY --from=build --chown=nonroot:nonroot /runtime/var/lib/fanout /var/lib/fanout

# Runs unprivileged. A host directory bind-mounted at /var/lib/fanout/data must
# be writable by UID/GID 65532, or the process cannot open its data directory.
USER nonroot:nonroot
WORKDIR /var/lib/fanout

EXPOSE 7520 4317 4318

# Startup does DuckDB catalog attachment and maintenance, so the grace period
# is generous relative to the check interval.
#
HEALTHCHECK --interval=30s --timeout=3s --start-period=40s --retries=3 \
    CMD ["fanout", "healthcheck"]

ENTRYPOINT ["fanout"]
CMD ["--config", "/etc/fanout/fanout.yaml"]
