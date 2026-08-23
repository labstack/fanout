# Releases

Fanout publishes one release from a verified commit on `main`. A release contains:

- native Linux and macOS archives for amd64 and arm64;
- `ghcr.io/labstack/fanout:<version>` and `latest` multi-architecture images;
- SHA-256 checksums and GitHub build-provenance attestations for every archive;
- the Apache-2.0 license, project notice, trademark policy, and generated
  third-party notices in every archive and container image.

## Version contract

Tags use unpadded CalVer: `vYYYY.M.N`. The first release in a month is `.0`.
Container tags omit the leading `v`, so `v2026.8.0` publishes
`ghcr.io/labstack/fanout:2026.8.0`.

Onebox consumes that public container reference. Its deployment planner resolves
the tag to an immutable registry digest before release, so production manifests
can remain readable without making the running revision mutable. Live Onebox
manifests and secret files are deployment state and do not belong in this public
repository.

## Creating a release

Run `just release` from a clean, current `main`. The script:

1. verifies that no other release is running;
2. runs `just check` and the race-enabled Go suite;
3. confirms that neither local `HEAD` nor `origin/main` moved during testing;
4. calculates and pushes the next CalVer tag.

The tag workflow then independently validates tag syntax, ancestry, and ordering;
builds every target on its native runner; publishes the multi-architecture image;
and creates the GitHub release only after an anonymous image pull succeeds. A
final job downloads every release asset without credentials, verifies checksums,
inspects the legal payload, exercises the installer, and verifies provenance.

Fanout does not currently use GoReleaser. DuckDB requires CGO and the supported
targets are built on four native runners. GoReleaser's supported split-and-merge
pipeline for native CGO builds is a Pro-only feature, while maintaining four
cross-compilation toolchains would add risk without improving the artifact. The
custom workflow follows the same clean-tree, tag, archive, checksum, and
post-publish verification discipline without introducing a commercial release
dependency. Revisit this if the build becomes cross-compilable or a GoReleaser
Pro subscription is intentionally adopted.

## Consumer verification

The installer verifies `SHA256SUMS` before extraction. A downloaded archive can
also be verified directly:

```sh
sha256sum -c SHA256SUMS
gh attestation verify fanout_v2026.8.0_linux_amd64.tar.gz \
  --repo labstack/fanout
```

For containers, pin or record the digest returned by the registry:

```sh
docker buildx imagetools inspect ghcr.io/labstack/fanout:2026.8.0
```

## Supply-chain scope

The first public release provides SHA-pinned Actions, archive checksums, GitHub
build provenance, dependency and secret scanning, legal inventories, and
post-publication verification. The container is consumed by immutable digest.

Standalone signatures and SBOM publication are deferred until Fanout has a
consumer that verifies them. Adding unconsumed artifacts would create a policy
claim without an operational control. This decision does not block adding either
later; provenance and the deterministic `THIRD_PARTY_NOTICES` inventory already
establish the release source and shipped dependency set.
