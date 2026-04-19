# Settings Rename & Bootstrap Simplification

**Date:** 2026-04-18
**Status:** Draft

## Goal

Rename the SQLite `config` table and its surrounding Go code to `settings`, and move the bootstrap token out of the database into in-memory state on the auth service. Reduce the surface area of the config group currently without losing any functionality.

## Motivation

The project has two distinct things that both get called "config":

1. **Startup config** — env vars loaded at boot by `internal/config` (addresses, secrets, `DATA_DIR`). Ops-owned, restart to change.
2. **Runtime admin state** — the SQLite `config` table holding ingest mode and the bootstrap token. Admin-owned, mutable at runtime.

Industry convention (see research in conversation) is that *config* means the foundational, deploy-time arrangement, while *settings* means runtime-adjusted knobs. The SQLite table fits the settings definition; the env-var loader fits the config definition. Same word today, two different concepts.

Additionally, the current table carries two columns (`updated_by`, `last_reason`) that nothing reads, and the bootstrap-token row is an ephemeral credential (~1 hour lifetime) dressed up as a persistent setting.

## Design

### 1. Rename the table and package

**New table:**

```sql
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

Column renames from the old `config` table:
- `group_key` → `key` (the table name already says "settings", no need to repeat "group")
- `overrides` → `value` (no base exists to override; it's just the stored value)

Dropped columns:
- `updated_by` — not read anywhere
- `last_reason` — not read anywhere

**New package:** `internal/settings/` replacing `internal/config/store.go`, `bootstrap.go`, `ingest.go`, `tokens.go` (keeping only env-var loading in `internal/config`).

### 2. Simplified Store API

```go
// internal/settings/store.go
type Store struct {
    q *generated.Queries
}

func NewStore(db *sql.DB) *Store
func (s *Store) Get(ctx context.Context, key string, out any) error
func (s *Store) Upsert(ctx context.Context, key string, value any) error
func (s *Store) Delete(ctx context.Context, key string) error
```

`Upsert` loses its `updatedBy` and `reason` parameters. Callers across the codebase drop those args.

### 3. Ingest config stays, moves package

`internal/settings/ingest.go` holds the existing `IngestConfig`, `DefaultIngestConfig`, `Validate`, `GetIngest`, `SetIngest`, `ResetIngest`, and the token helpers (`GenerateIngestToken`, `HashIngestToken`, `CheckIngestToken`). Only the package name changes. JSON payload shape stays identical.

### 4. Bootstrap token moves in-memory

Remove entirely:
- `internal/config/bootstrap.go`
- The existing `bootstrap` row in the `config` table (not migrated into `settings`)
- `BootstrapConfig` struct, `reusable()`, `expiresAt()`, `EnsureBootstrap`, `GetBootstrap`, `RotateBootstrap` (Store method), `ClearBootstrap` (Store method)
- `HashBootstrapToken` (no at-rest storage means no hashing)

Add three fields on the auth service that owns bootstrap:

```go
type AuthService struct {
    // existing fields...
    bootstrapMu      sync.Mutex
    bootstrapToken   string
    bootstrapExpires time.Time
}

func (a *AuthService) RotateBootstrap() (string, time.Time)
func (a *AuthService) CheckBootstrap(token string) bool
func (a *AuthService) ClearBootstrap()
```

Semantics:
- **RotateBootstrap** — generates a new plaintext token, stores it with a 1-hour expiry, returns it. Called on startup when no users exist.
- **CheckBootstrap** — constant-time compares the supplied token against the in-memory value, checks expiry. Returns false if empty, expired, or mismatched.
- **ClearBootstrap** — zeroes the fields after successful first-user creation.

No hashing (same-process memory), no JSON, no SQL, no expiry parsing from RFC3339 strings.

**Restart behavior:** if the process restarts inside the bootstrap window, a new token is generated and logged. The operator uses the freshly-logged value. This is a deliberate simplification — the persistence that existed to preserve the token across restarts wasn't earning its complexity.

### 5. sqlc regeneration

Query renames in `internal/db/queries/config.sql` → `internal/db/queries/settings.sql`:
- `GetConfig` → `GetSetting`
- `UpsertConfig` → `UpsertSetting` (with fewer columns)
- `DeleteConfig` → `DeleteSetting`

Regenerate `internal/db/generated/`.

### 6. Migration

Single Atlas migration:

```sql
-- Create new table
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Preserve ingest row only; drop the bootstrap row (will be regenerated in memory)
INSERT INTO settings (key, value, updated_at)
SELECT group_key, overrides, updated_at
FROM config
WHERE group_key != 'bootstrap';

DROP TABLE config;
```

Bootstrap rows in existing databases are discarded — the next boot regenerates in memory.

## Call Sites To Update

Based on current grep results:

- `cmd/fanout/main.go` — wires `config.NewStore` → `settings.NewStore`; wires bootstrap into the auth service instead of the store
- `internal/api/auth.go` + `internal/api/auth_test.go` — replace `store.EnsureBootstrap`/`CheckBootstrapToken` with auth service methods
- `internal/api/config.go` + `internal/api/config_test.go` — update ingest handlers to use `settings.Store`; drop `updatedBy`/`reason` args
- `internal/ingest/auth.go` + `internal/ingest/auth_test.go` — update ingest token verification to read from `settings.Store`
- `internal/config/bootstrap_test.go`, `internal/config/store_test.go` — move to `internal/settings/` with adjusted expectations

## Non-Goals

- **Env→settings migration** for `RETENTION_DAYS`, `FLUSH_SECONDS`, `ROLLUP_EVERY`, `MCP_ENABLED`. These are candidates for future admin-UI tunables but are out of scope for this rename.
- **Audit logging.** The `updated_by` and `last_reason` columns are dropped because nothing consumes them. If auditing becomes a requirement, design it as a dedicated `audit_log` table, not as columns on `settings`.
- **Multi-admin attribution.** Single-tenant, single-admin today.

## Risks

1. **Existing deployments** — any installation with a populated `config` table runs the migration above. Ingest config survives; bootstrap rows are discarded (acceptable per the restart-behavior note).
2. **Race on first boot** — auth service's bootstrap fields must be initialized before any HTTP handler can call `CheckBootstrap`. Wire the rotate call into startup before `echo.Start`.
3. **Rename churn** — touches ~10 files, mostly mechanical. One focused PR.

## Validation

- Unit tests in the new `internal/settings/` package cover `Get`/`Upsert`/`Delete` and ingest-config round-tripping.
- Auth-service tests cover `RotateBootstrap` returns fresh tokens, `CheckBootstrap` rejects expired/empty/mismatched tokens with constant-time compare, `ClearBootstrap` invalidates subsequent checks.
- End-to-end: fresh DB → server starts → bootstrap token logged → token used to create first user → `ClearBootstrap` invoked → subsequent use of that token fails.
