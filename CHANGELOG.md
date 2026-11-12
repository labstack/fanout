# Changelog

## Unreleased — Pitot rename (2026-11-11)

The product is now **Pitot** (pronounced **PEE-toh**) at
[pitot.dev](https://pitot.dev). The owner approved this rename on 2026-11-11 to avoid a
conflict with a third party's registered US trademark in cloud services.

- The Go module is `github.com/labstack/pitot`; the binary is `pitot` and native
  archives are `pitot_<version>_<os>_<arch>.tar.gz`, containing `./pitot`.
- Container images are `ghcr.io/labstack/pitot` and `docker.io/labstack/pitot`.
  Update deployment manifests, service names, install URLs and MCP client entries.
  The MCP Registry identity is `io.github.labstack/pitot`.
- The pre-rename environment variable prefix is no longer read, including
  installer overrides and secret-file settings. Use `PITOT_*` settings.
- Before upgrading, stop the service and take a cold backup of the complete
  data directory. Rename the pre-rename control database in `control/` to
  `pitot.sqlite` before starting the new binary. Do not start against an empty
  control database or create a fresh volume by accident.
- Telemetry spill footer keys retain their persisted format-5 spelling so
  existing Parquet files remain valid and byte compatibility stays unchanged.
- Sessions use the `pitot_session` cookie (with the `__Host-` prefix over HTTPS),
  so users must sign in again. Keyboard shortcut preferences reset. Saved chat
  activities using the previous MCP App URI no longer render; begin a new chat.
  Reserved diagnostic context metadata now uses `_pitot_diagnostic_context`;
  discard pre-rename chat history before resuming chat.
- Published SQL migrations remain unchanged. Existing ingest and OAuth secrets
  and their token prefixes remain valid. The image defaults to
  `/var/lib/pitot/data`: mount the existing volume there, or set `PITOT_DATA_DIR`
  to its existing mount location.
- HTTP request headers and metrics use the Pitot name. Update custom clients
  using the product request header to `Pitot-Request` (and dashboard deletion confirmation to
  `Pitot-Confirm-Delete`), and update Prometheus
  queries and alert rules from the old metric prefix to `pitot_`.

Take a cold backup before upgrading, stop the old service, replace the binary or
image, and start the new service against the same data and secrets. Verify
`/readyz`, sign-in, ingest and a representative query before resuming traffic.
