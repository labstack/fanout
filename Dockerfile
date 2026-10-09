# syntax=docker/dockerfile:1

# Bun is a build compiler only. Neither Bun nor Node is copied into the final
# image or launched by the Fanout process.
FROM oven/bun:1.4.2@sha256:9114c058aeae42162ee16dd5084b95fe9473970bb6bcb5b232ab1630f0546895 AS ui-build
WORKDIR /app
COPY ui/host/package.json ui/host/bun.lock ./ui/host/
RUN cd ui/host && bun install --frozen-lockfile
COPY ui/*.ts ./ui/
COPY ui/panels/ ./ui/panels/
COPY ui/host/ ./ui/host/
# The build type-checks the host tests, and one reads the eval golden fixture.
COPY scripts/dashboard-eval/testdata/server.json ./scripts/dashboard-eval/testdata/
RUN cd ui/host && bun run build

# DuckDB needs CGO, so this stage must run on the target architecture — there
# is no cross toolchain here. `--platform=$BUILDPLATFORM` is therefore only
# correct while TARGETPLATFORM equals BUILDPLATFORM. Adding an architecture to
# the CI matrix means either a native runner for it or QEMU, not a GOARCH flag.
FROM --platform=$BUILDPLATFORM golang:1.27.2-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=ui-build /app/internal/mcp/apps/ ./internal/mcp/apps/
COPY --from=ui-build /app/internal/ui/dist/ ./internal/ui/dist/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    bash scripts/with-duckdb.sh go build -ldflags="-s -w -X main.version=${VERSION}" -o fanout ./cmd/fanout

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

EXPOSE 7520

# Startup does DuckDB catalog attachment and maintenance, so the grace period
# is generous relative to the check interval.
#
HEALTHCHECK --interval=30s --timeout=3s --start-period=40s --retries=3 \
    CMD ["fanout", "healthcheck"]

ENTRYPOINT ["fanout"]
CMD ["--config", "/etc/fanout/fanout.yaml"]
