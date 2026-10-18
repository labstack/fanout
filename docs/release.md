# Releases

Fanout publishes one release from a verified commit on `main`. A release contains:

- native Linux and macOS archives for amd64 and arm64;
- canonical `ghcr.io/labstack/fanout:<version>` and `latest`
  multi-architecture images;
- byte-identical `labstack/fanout:<version>` and `latest` Docker Hub mirrors;
- SHA-256 checksums and GitHub build-provenance attestations for every archive;
- an `io.github.labstack/fanout` entry in the official MCP Registry;
- the Apache-2.0 license, project notice, trademark policy, and generated
  third-party notices in every archive and container image.

## Version contract

Tags use unpadded CalVer: `vYYYY.M.N[-alpha|-beta|-rc]`. The suffix states product
maturity; GA has no suffix. The first release in a month is `.0`, and every
release increments `N` regardless of suffix. Fanout is alpha. For example,
`v2026.10.1-alpha` follows `v2026.10.0-alpha`.
Container tags omit the leading `v`, so `v2026.10.1-alpha` publishes
`ghcr.io/labstack/fanout:2026.10.1-alpha` and `labstack/fanout:2026.10.1-alpha`.
Every release moves `latest`. GitHub Releases are not marked as prereleases:
the tag carries maturity, and the installer's `/releases/latest` must resolve.
Only release and `latest` tags are mirrored to Docker Hub; `main` and commit
tags remain GHCR-only.

Deployments should resolve public tags to immutable registry digests. Keep live
manifests and secret profiles outside this public repository.

## Creating a release

Run `just release` from a clean, current `main`. The script:

1. verifies that no other release is running;
2. runs `just check` and the race-enabled Go suite;
3. confirms that neither local `HEAD` nor `origin/main` moved during testing;
4. calculates and pushes the next CalVer tag.

The tag workflow then independently validates tag syntax, ancestry, and ordering;
builds every target on its native runner; publishes the multi-architecture image;
copies that accepted manifest to Docker Hub; requires the registry digests to
match; and creates the GitHub release only after anonymous pulls from both GHCR
and Docker Hub succeed. A final job downloads every release asset without
credentials, verifies checksums, inspects the legal payload, exercises the
installer, and verifies provenance. Only after those checks pass does the
workflow publish the release's `server.json` to the MCP Registry with GitHub
OIDC; no long-lived registry credential is stored in the repository.

The release workflow builds CGO targets on four native runners. It creates
archives, checksums and attestations, then verifies the published artifacts.

## Consumer verification

The installer verifies `SHA256SUMS` before extraction. A downloaded archive can
also be verified directly:

```sh
sha256sum -c SHA256SUMS
gh attestation verify fanout_v2026.10.1-alpha_linux_amd64.tar.gz \
  --repo labstack/fanout
```

For containers, pin or record the digest returned by the registry:

```sh
docker buildx imagetools inspect ghcr.io/labstack/fanout:2026.10.1-alpha
docker buildx imagetools inspect docker.io/labstack/fanout:2026.10.1-alpha
```

## Supply-chain scope

The release workflow uses SHA-pinned Actions, archive checksums, GitHub
build provenance, dependency and secret scanning, legal inventories, and
post-publication verification. The container is consumed by immutable digest.

Standalone signatures and SBOMs are outside the release artifact inventory.
GitHub build provenance identifies the source; `THIRD_PARTY_NOTICES` lists the
shipped dependencies.
