# Releases

Pitot publishes one release from a verified commit on `main`. A release contains:

- native Linux and macOS archives for amd64 and arm64;
- canonical `ghcr.io/labstack/pitot:<version>` and `latest`
  multi-architecture images;
- byte-identical `labstack/pitot:<version>` and `latest` Docker Hub mirrors;
- SHA-256 checksums and GitHub build-provenance attestations for every archive;
- an `io.github.labstack/pitot` entry in the official MCP Registry;
- the Apache-2.0 license, project notice, trademark policy, and generated
  third-party notices in every archive and container image.

## Version contract

Tags use unpadded CalVer: `vYYYY.M.N[-alpha|-beta|-rc]`. The suffix states product
maturity; GA has no suffix. The first release in a month is `.0`, and every
release increments `N` regardless of suffix. Pitot is alpha. For example,
`v2026.10.1-alpha` follows `v2026.10.0-alpha`.
Container tags omit the leading `v`, so `v2026.10.1-alpha` publishes
`ghcr.io/labstack/pitot:2026.10.1-alpha` and `labstack/pitot:2026.10.1-alpha`.
Every release moves `latest`. GitHub Releases are not marked as prereleases:
the tag carries maturity, and the installer's `/releases/latest` must resolve.
Only release and `latest` tags are mirrored to Docker Hub; `main` and commit
tags remain GHCR-only.

Deployments should resolve public tags to immutable registry digests. Keep live
manifests and secret profiles outside this public repository.

A tag without a suffix is a stable release; `-alpha`, `-beta` and `-rc` tags are
prereleases. Because `latest` follows both, it is not a stable channel. The
[upgrade policy](../site/src/content/docs/docs/guides/upgrade.mdx), published at
<https://pitot.dev/docs/guides/upgrade>, sets the notice period for breaking
changes, the route and API deprecation rules, and the same-version restore
requirement.

## Upgrade notes

Every release's notes start with an **Upgrade notes** section that
`scripts/release-notes.sh` writes during the tag workflow's `verify` job,
before anything is published. It compares the tag with the previous
published, non-draft GitHub Release, which the workflow lists with
`gh release list`. A tag whose workflow failed has no release and is skipped,
so the next published release reports its changes. The section marks:

- a batch format change, read from `batchMetadataVersion` in
  `internal/telemetry/parquet.go`, as a **Format change** caution: the release
  cannot read earlier telemetry;
- control migrations added under `internal/db/migrations`, which run forward
  at startup, as a **Control schema migration**;
- otherwise, a **Storage** line stating that neither changed;
- merged changes whose titles carry the Conventional Commits `!` marker, under
  **Breaking changes**.

The script fails the release when it cannot read the format version, so a
format change is never published unmarked. `just release-test` covers it. The
section is prepended to GitHub's generated changelog; maintainers add manual
upgrade steps and deprecation notices below it. A format-changing or breaking
pull request should say so in its title with `!` and describe the operator
steps in its description, so they can be copied into the notes.

## Creating a release

Run `just release` from a clean, current `main`. The script:

1. verifies that no other release is running;
2. waits for CI's push run on `main` for that commit, which runs every check and
   the race-enabled Go suite, and stops unless it passed;
3. confirms that neither local `HEAD` nor `origin/main` moved meanwhile;
4. calculates and pushes the next CalVer tag.

The tag workflow then independently validates tag syntax, ancestry, and ordering.
It reuses that CI result for the tagged commit, re-running only the dependency
audit, and runs the full suites itself when no successful run exists. It then
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
gh attestation verify pitot_v2026.10.1-alpha_linux_amd64.tar.gz \
  --repo labstack/pitot
```

For containers, pin or record the digest returned by the registry:

```sh
docker buildx imagetools inspect ghcr.io/labstack/pitot:2026.10.1-alpha
docker buildx imagetools inspect docker.io/labstack/pitot:2026.10.1-alpha
```

## Supply-chain scope

The release workflow uses SHA-pinned Actions, archive checksums, GitHub
build provenance, dependency and secret scanning, legal inventories, and
post-publication verification. The container is consumed by immutable digest.

Standalone signatures and SBOMs are outside the release artifact inventory.
GitHub build provenance identifies the source; `THIRD_PARTY_NOTICES` lists the
shipped dependencies.

## First Pitot release

After merging the rename pull request:

1. Rename the GitHub repository from `labstack/fanout` to `labstack/pitot` in
   repository settings. Update remotes and the repository website to
   `https://pitot.dev`; configure that domain for the site deployment. Configure
   the `pitot` Cloudflare Pages project used by `site.yml` and migrate the media
   hostname to `assets.pitot.dev` before enabling the renamed site workflow.
   Refresh the six historical landing captures and hosted clips with the new
   wordmark on a host that can launch Chrome; the committed captures still
   document the pre-rename alpha.
2. Verify the renamed repository's Actions permissions, branch protection,
   environments, OIDC trust and package access. Ensure `ghcr.io/labstack/pitot`
   is public and Actions can write it. Create or configure the public Docker Hub
   `labstack/pitot` image destination and retain the workflow's existing
   credential delivery. Do not overwrite old releases or image tags.
3. Wait for the merged `main` CI run to pass, then run `just release` from a
   clean, current `main`. This selects the next unused `vYYYY.M.N-alpha` tag;
   do not reset the monthly sequence because the product changed names.
4. Wait for the tag-triggered `release.yml` workflow, including anonymous image
   pulls, `pitot_` archives containing `./pitot`, checksums, attestations and
   installer verification. It publishes version and `latest` images to both new
   image destinations. Add the rename and one-alpha compatibility notice from
   `CHANGELOG.md` to the release notes.
5. The final workflow job authenticates with GitHub OIDC and publishes the tagged
   `server.json` as `io.github.labstack/pitot` in the MCP Registry. Verify that
   entry and its version after the job passes; retire the old registry listing
   separately according to registry policy. Do not publish from this branch.
6. In the following alpha, remove the legacy environment bridge and its installer
   overrides after repeating the deprecation notice. Keep storage filenames and
   token compatibility independently of that environment window.
