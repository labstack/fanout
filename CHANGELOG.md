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
- The server and installer refuse to start if any variable with the exact,
  case-sensitive pre-rename prefix is set, including empty assignments,
  installer overrides and secret-file settings. The error lists names only.
  Rename those variables to `PITOT_*` before upgrading; they no longer silently
  use defaults.
- The query language is now **PitotQL**. Existing structured dashboard queries,
  MCP `query_telemetry` arguments (`text`, `panel.query`, `panel.sql`), query
  keywords, Explore URL parameters and browser storage keys are unchanged.
  Previous query HTTP paths, MCP reference URI and error-code reader aliases
  are removed. Clients must use canonical PitotQL names. Permanent website
  redirects preserve existing documentation links. See
  [rename compatibility](site/src/content/docs/docs/reference/rename-compatibility.mdx) for the cutover requirements.
- Before upgrading, stop the service and take a cold backup of the complete
  data directory. On startup, Pitot automatically renames the previous control
  database and its SQLite WAL, shared-memory and rollback-journal sidecars in
  `control/` to `pitot.sqlite` and matching sidecar names, then syncs the directory.
  No manual filename change is needed, including for unattended agent upgrades.
  If both database names or an incomplete sidecar move exist, startup refuses
  before opening SQLite; stop the instance and recover a consistent file set
  from the cold backup before retrying. Never delete a database just to bypass
  this refusal or create a fresh volume by accident. For rollback to the previous
  binary, restore its complete cold backup before starting it; older binaries
  do not move the database name back.
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
