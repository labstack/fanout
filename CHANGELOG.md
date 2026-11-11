# Changelog

## Unreleased — Pitot rename (2026-11-11)

The product is now **Pitot** (pronounced **PEE-toh**), formerly Fanout, at
[pitot.dev](https://pitot.dev). The owner approved this rename on 2026-11-11 to avoid a
conflict with a third party's registered US trademark in cloud services.

- The Go module is `github.com/labstack/pitot`; the binary is `pitot` and native
  archives are `pitot_<version>_<os>_<arch>.tar.gz`, containing `./pitot`.
- Container images are `ghcr.io/labstack/pitot` and `docker.io/labstack/pitot`.
  Update deployment manifests, service names, install URLs and MCP client entries.
  The MCP Registry identity is `io.github.labstack/pitot`.
- Use `PITOT_*` settings. For the first Pitot alpha only, an unset primary
  variable falls back to its `FANOUT_*` equivalent, including secret `_FILE`
  forms and installer version/prefix overrides. A primary variable explicitly
  set to empty blocks its legacy equivalent and uses the ordinary default.
  Startup emits one deprecation warning listing used old names, never values.
  Mixed value/file forms still fail when both are nonempty. Replace old names
  before upgrading to the following alpha, when this bridge is due for removal.
- Keep the complete existing data directory. Native default `./data`, the
  `control/fanout.sqlite` filename, all other storage filenames, telemetry format,
  marker files, Parquet `fanout.spill.*` footer keys, stored
  `_fanout_diagnostic_context` metadata and published migrations are unchanged; there is no data migration.
  The new image defaults to `/var/lib/pitot/data`: mount the **existing** volume
  there, or explicitly set `PITOT_DATA_DIR` to its existing mount location.
  Do not accidentally create a fresh named volume when changing deployment names.
- Existing sessions keep their cookie names and keyboard preferences keep their
  storage key. Saved chat activities resolve their old MCP App resource URI to
  the new URI in memory, without rewriting history. Existing ingest and OAuth secrets
  remain valid; token prefixes (`foi_`, `foa_`, `for_`, `foc_`) are unchanged for
  both existing and new tokens because there is no central token-prefix registry.
- HTTP request headers and metrics use the Pitot name. Update custom clients
  using the product request header to `Pitot-Request` (and dashboard deletion confirmation to
  `Pitot-Confirm-Delete`), and update Prometheus
  queries and alert rules from the old metric prefix to `pitot_`.

Take a cold backup before upgrading, stop the old service, replace the binary or
image, and start the new service against the same data and secrets. Verify
`/readyz`, sign-in, ingest and a representative query before resuming traffic.
