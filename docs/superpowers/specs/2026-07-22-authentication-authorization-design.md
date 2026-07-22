# Authentication & Authorization — Design

> Status: proposed · Date: 2026-07-22 · Supersedes the browser-authentication,
> session, and self-hosted user-management sections of
> `docs/auth-tenancy-vision.md` and
> `docs/superpowers/plans/2026-04-16-auth-backend.md`. It does not supersede
> their longer-term tenancy discussion or the existing MCP OAuth design.

## Decision

Fanout will remain a **single-tenant application per installation**. It will own
the local user directory, active status, roles, capabilities, and resource
ownership. It will not become an identity provider.

Authentication is split by responsibility:

- [`github.com/alexedwards/scs/v2`](https://pkg.go.dev/github.com/alexedwards/scs/v2)
  owns browser session mechanics: opaque tokens, cookie lifecycle, expiry,
  renewal, and destruction.
- [`github.com/coreos/go-oidc/v3/oidc`](https://pkg.go.dev/github.com/coreos/go-oidc/v3/oidc)
  validates upstream OpenID Connect providers and ID tokens.
- [`golang.org/x/oauth2`](https://pkg.go.dev/golang.org/x/oauth2) performs the
  OIDC authorization-code exchange with state, nonce, and PKCE.
- Fanout's existing SQLite stores own users, linked identities, role mappings,
  verification codes, and security audit events.
- Fanout's existing OAuth server remains responsible for external MCP clients.
  Browser sessions authenticate the consent screen, but browser and MCP tokens
  remain separate credentials with separate lifecycles.

There is no anonymous user registration. The first administrator is created with
the one-time setup token. Later users are created by an administrator, by an
explicitly configured OIDC group-provisioning rule, or eventually through SCIM.

## Why This Fits Fanout

Fanout's product promise is a small, simple observability system that can run as
one binary. Requiring a bundled identity service would add more operational
weight than the telemetry stack itself. Reimplementing session and OIDC
protocol details would add security risk without creating product value.

The chosen boundary preserves the single Go process and existing SQLite control
database while delegating protocol-sensitive behavior to focused libraries.
Fanout keeps the small amount of policy that is genuinely product-specific.

The design intentionally avoids multi-tenant authorization. An installation is
one security boundary. `service.namespace` is a telemetry filter inside that
boundary, not a tenant boundary.

## Goals

- Keep first installation and first-admin setup simple.
- Prevent public self-registration on internet-exposed instances.
- Support small teams without requiring an external identity provider.
- Support company identity providers through standard OIDC.
- Make browser logout, user deactivation, and session revocation effective
  immediately.
- Make authorization explicit and deny by default for every API route.
- Preserve personal ownership of dashboards and agent threads.
- Preserve the existing, stronger MCP OAuth token model.
- Leave a clean path to group provisioning, SCIM, and enterprise audit export.

## Non-Goals

- Password storage, password reset, or native password policy.
- Native MFA, passkeys, or account recovery beyond an operator-controlled
  break-glass flow. An upstream IdP should provide these features.
- A custom role or permission editor.
- Organizations, organization switching, or shared-infrastructure tenancy.
- SAML or SCIM in the first implementation.
- Replacing the current MCP OAuth authorization server.
- Making email address the permanent identity key for OIDC users.

## Current State

The current implementation already has useful foundations:

- One-time first-admin setup with an expiring token.
- Admin-managed users with `viewer`, `operator`, and `admin` roles.
- Six-digit email codes that expire after five minutes, allow three attempts,
  and are marked used after successful verification.
- Fifteen-minute access JWTs and seven-day refresh JWTs.
- An `HttpOnly`, `SameSite=Lax` refresh cookie.
- Per-request user lookup, so deactivation and role changes are observed by API
  middleware.
- Owner-scoped dashboards and agent threads.
- Hashed ingest credentials.
- MCP OAuth with PKCE, explicit scopes, opaque hashed tokens, refresh-token
  rotation, and reuse detection.

The gaps this design addresses are:

- Browser access JWTs are readable from `localStorage`.
- Browser refresh JWTs cannot be revoked before expiry; logout only clears the
  browser cookie.
- Login initiation has no IP/email throttling and can be abused to send email.
- Authorization defaults to "any authenticated user" unless a handler adds a
  role guard. Several alert and agent mutations therefore lack an explicit
  capability requirement.
- `PUBLIC_READ` also disables ingest authentication, coupling two unrelated and
  materially different risks.
- Authentication and authorization events are present in general logs but not
  in a durable, queryable security audit trail.
- SMTP is mandatory even when an installation intends to use OIDC.
- `/-/metrics` is currently fully public, including before any user is
  authenticated. Moving it behind a browser capability alone would break
  non-interactive Prometheus scrapers, so it needs its own service credential.

## Authentication Modes

Exactly one primary browser authentication mode is configured per installation:

| Mode | Intended use | User authentication |
|---|---|---|
| `local` | Small self-hosted team | Existing passwordless email code |
| `oidc` | Company/team deployment | External OIDC provider |
| `proxy` | Existing identity-aware reverse proxy | Trusted asserted identity |

`local` is the default. `oidc` is included in the standard product. `proxy` is a
later phase because trusting request headers safely requires explicit proxy
network configuration and careful deployment documentation.

The first-admin setup token is independent of the selected mode. Host-controlled
OIDC recovery codes are not implemented yet; until they are, operators must use
the documented emergency local-mode procedure below if the IdP is unavailable
or misconfigured.

### Local Mode

1. An operator starts Fanout with SMTP configured.
2. If there are no users, Fanout prints a one-hour, one-time setup token.
3. The holder creates the first admin.
4. An admin creates subsequent users and assigns roles.
5. An existing active user requests a login code by email.
6. Successful verification creates a server-side browser session.

The start endpoint always returns the same response for known and unknown email
addresses. It must not expose whether an account exists.

### OIDC Mode

1. The browser requests `/api/auth/oidc/start`.
2. Fanout creates short-lived state, nonce, and PKCE verifier values in the
   browser session and redirects to the configured issuer.
3. The IdP redirects to `/api/auth/oidc/callback`.
4. Fanout verifies state, exchanges the code, and verifies the ID-token
   signature, issuer, audience, nonce, and expiry.
5. Fanout resolves the user by the stable `(issuer, subject)` identity key.
6. If no identity is linked, Fanout applies the provisioning policy described
   below.
7. Fanout creates or renews a server-side browser session and redirects to the
   original same-origin path.

For initial email-based linking, the default policy requires a non-empty email
claim and `email_verified=true`. Some enterprise issuers omit the verification
claim or email claim even for managed accounts. Missing claims deny linking by
default and produce an actionable administrator-facing error. An operator may
configure a different email claim name and explicitly choose
`OIDC_EMAIL_VERIFICATION=issuer` for a trusted issuer that contractually vouches
for the claim but omits `email_verified`; this exception is never inferred from
the issuer name. If the configured email claim is absent, linking is denied
rather than falling back silently to a mutable username claim.

After initial linking, email is descriptive data, not the identity key. A later
email change at the IdP must not silently relink the identity to another Fanout
user.

### Trusted Proxy Mode

This mode is deferred until after OIDC. When implemented, it must:

- Accept identity headers only from configured trusted proxy CIDRs or a
  cryptographically verified proxy assertion.
- Reject direct requests that contain reserved identity headers.
- Normalize the asserted email and map it through the same provisioning policy
  as OIDC.
- Never infer trust merely from `X-Forwarded-*` headers.

## User Creation and Identity Linking

Fanout always creates the local user record. Authentication systems prove an
identity; they do not automatically receive authority over Fanout roles.

### First Administrator

The first administrator is created only with the one-time setup token. The setup
endpoint remains publicly reachable before initialization, but possession of the
high-entropy token is required. Operators should complete setup before exposing
an instance to an untrusted network.

### Admin-Provisioned Users (Default)

The secure default in both local and OIDC modes is admin provisioning:

1. An admin creates a user with email, display name, and role.
2. In local mode, Fanout sends the existing invitation email.
3. In OIDC mode, the user's first successful login with an email accepted by the
   configured verification policy links `(issuer, subject)` to the pre-created
   user.
4. If the email does not match exactly one active, unlinked user, access is
   denied and an audit event is recorded.

The link is performed transactionally and `(issuer, subject)` is globally unique
within the installation.

### Optional OIDC Auto-Provisioning

OIDC just-in-time provisioning is disabled by default. When enabled, it requires
an allow rule. A bare `AUTO_PROVISION=true` is invalid configuration.

Supported allow rules, in priority order:

1. Membership in one of `OIDC_ALLOWED_GROUPS`.
2. A verified email domain in `OIDC_ALLOWED_DOMAINS` when group claims are not
   available.

Group membership is preferred because employment at a company does not imply a
need to access production telemetry. Newly provisioned users receive
`OIDC_DEFAULT_ROLE`, which defaults to `viewer`.

Optional operator/admin group mappings are explicit. Unknown or missing group
claims never grant elevated access. The first-admin invariant still applies: an
OIDC claim cannot create the first administrator or remove the final active
administrator.

### Future SCIM Provisioning

SCIM may later create, update, and deactivate users. It will call the same user
service and preserve the same last-admin invariant. SCIM is provisioning, not
browser authentication; provisioned users still authenticate through OIDC.

## Browser Session Design

Browser JWTs are replaced by SCS-backed opaque sessions. The browser and API are
same-origin, so a bearer token exposed to JavaScript provides no required
capability. The current middleware already loads the user from SQLite on every
request, so a stateless JWT does not avoid a database lookup.

### Cookie

Production cookies use:

```text
Name:     __Host-fanout_session
Path:     /
HttpOnly: true
Secure:   true
SameSite: Lax
```

The `__Host-` prefix forbids a `Domain` attribute and requires `Secure` plus
`Path=/`. Loopback development may use a separate non-prefixed cookie because
browsers do not consistently treat local HTTP like production HTTPS.

Cookie security is derived from a validated external URL or a configured trusted
proxy, not from an arbitrary incoming `X-Forwarded-Proto` header.

### Lifetime

- Idle timeout: 12 hours by default, enforced from indexed session metadata.
- Absolute timeout: 7 days.
- Successful authentication starts a new absolute-lifetime clock.
- No "remember me" option in the first implementation.

### Stored Session Values

The session contains only:

```text
user_id
auth_version
last_authenticated_at
return_to (short-lived, same-origin only)
oidc_state / oidc_nonce / oidc_pkce_verifier (short-lived, during login only)
```

`user_id` and `auth_version` are duplicated in the encoded payload for convenient
request-context access and in queryable session/user columns for enforcement and
operations. They are written together by the Fanout session layer. Roles and
capabilities are never trusted from session data. Middleware loads the current
user on every request and fails closed if the user is missing, inactive, or has
a different `auth_version`.

### Revocation

- Logout destroys the current SCS session and expires the cookie.
- "Log out all sessions" increments `users.auth_version`, deletes that
  user's browser-session rows, and revokes every MCP OAuth access and refresh
  token for the user in the same transaction.
- Account deactivation and security-sensitive identity changes increment
  `auth_version` and delete the affected user's sessions in the same transaction.
- Every role change, including promotion, increments `auth_version` and deletes
  the affected user's sessions in the same transaction. The user signs in again
  under the new role. This deliberately favors one simple revocation invariant
  over ordinal role comparisons and a special session-continuity path for a rare
  administrative action.

### Token Renewal and Absolute Deadlines

SCS `RenewToken` resets its internal deadline to `now + Lifetime`, and its
`SetDeadline` method can independently replace a session's deadline. Fanout must
not call either method directly outside the session package.

The session package exposes two explicit operations:

- `EstablishAuthenticatedSession` rotates the token and starts a new configured
  absolute lifetime after successful local, OIDC, or recovery authentication.
- `BeginPreAuthenticationSession(flowTTL)` rotates away from any current
  authenticated session, clears identity data, creates the session used for
  local or OIDC login state, and calls SCS `SetDeadline` internally with a
  bounded, flow-specific lifetime. The requested deadline cannot exceed the
  configured maximum for that flow.

Role changes, deactivation, identity-security changes, and explicit revocation
delete affected sessions. Only an actual authentication may create a new session
and start a new absolute clock.

### Session Storage

SCS receives a custom store backed by Fanout's existing SQLite connection. Raw
browser tokens are not stored. `SessionManager.HashTokenInStore` is enabled, so
SCS passes a SHA-256 digest to the adapter for find, commit, and delete
operations.

```sql
CREATE TABLE sessions (
  token_hash          TEXT PRIMARY KEY,
  user_id             TEXT REFERENCES users(id) ON DELETE CASCADE,
  data                BLOB NOT NULL,
  created_at          INTEGER NOT NULL,
  last_activity_at    INTEGER NOT NULL,
  absolute_expires_at INTEGER NOT NULL
);

CREATE INDEX sessions_user_idx ON sessions(user_id);
CREATE INDEX sessions_idle_idx ON sessions(last_activity_at);
CREATE INDEX sessions_expiry_idx ON sessions(absolute_expires_at);
```

`user_id` is nullable only for short-lived pre-authentication sessions carrying
OIDC state/nonce/PKCE or a local login flow. Those sessions receive a short
absolute deadline through `BeginPreAuthenticationSession`; callers cannot change
SCS deadlines directly.

The existing hourly auth-state sweep deletes rows where either:

```text
last_activity_at < now - SESSION_IDLE_TTL
absolute_expires_at <= now
```

This makes cleanup, active/expired-session metrics, per-user session revocation,
and the future session-management UI direct indexed queries rather than attempts
to decode every SCS blob. "Log out all sessions" deletes by `user_id` in addition
to incrementing `auth_version` as defense in depth.

SCS owns payload encoding and token hashing. Fanout's adapter owns SQLite
persistence and the indexed metadata contract. It must implement SCS's
context-aware `FindCtx`, `CommitCtx`, and `DeleteCtx` methods; application paths
must not silently fall back to context-free store operations. The context-aware
commit receives Fanout session metadata from the request context and writes the
blob and indexed columns atomically.

Cancellation semantics differ deliberately by operation:

- `FindCtx` uses the request context directly, so abandoned requests stop waiting
  for session reads.
- `CommitCtx` and `DeleteCtx` derive their database context with
  `context.WithTimeout(context.WithoutCancel(ctx), sessionStoreWriteTimeout)`.
  They retain request values used for session metadata, survive client disconnect
  and handler cancellation, and remain bounded by the session-store write
  timeout; the adapter always calls the derived context's cancel function.
- Metadata touch, rotation, and per-user revocation writes use the same bounded,
  cancellation-detached write context. Store errors and deadline expiry are
  reported; they are never converted into apparent success.

This split is security-sensitive. If a disconnected login client never receives
its cookie, the completed commit leaves only an unreachable row that cleanup
later removes. A cancelled logout delete would leave a copied cookie valid,
while a cancelled renewal delete would leave both the old and new tokens valid.
Security writes therefore must not inherit request cancellation.

The write timeout and SQLite busy timeout are one configuration invariant, not
independent constants:

```text
sessionStoreWriteTimeout >= controlDBBusyTimeout + 2 seconds
```

The current control database uses a 5-second SQLite busy timeout, so the session
store write timeout is 7 seconds. Both values come from the same store
configuration, and startup validation rejects an inverted relationship. This
lets SQLite use its full busy-retry window under writer contention while still
placing a finite bound on a detached security write.

For metadata-only lookup and touch operations, the session package exposes one
tested token-digest helper implementing SCS's base64url-encoded SHA-256 format.
An integration test commits a cookie through SCS and proves the helper addresses
the same stored row. This test is required when upgrading SCS so a future token-
hashing change cannot silently orphan activity updates or revocation.

### Session Write Pattern

SCS's built-in `IdleTimeout` marks every loaded session modified so it can extend
the store expiry. With Fanout's polling UI, enabling it directly would cause one
SQLite write per authenticated request.

Fanout therefore configures SCS with `SESSION_ABSOLUTE_TTL` as its `Lifetime` and
leaves SCS `IdleTimeout` disabled. Authentication middleware enforces the configured
idle limit from the indexed `last_activity_at` column. The activity checkpoint
interval is derived as:

```text
min(5 minutes, SESSION_IDLE_TTL / 10)
```

Configuration requires `SESSION_IDLE_TTL >= 5m`, so the checkpoint is never
larger than the idle window. A request updates activity only when the stored
timestamp is older than the checkpoint threshold, using a conditional SQL update
so concurrent requests do not each write. A continuously active browser therefore
causes at most one small metadata update per checkpoint interval, regardless of
its API polling frequency. Idle expiry remains conservative within that interval:
it may expire slightly early, never late. The activity update does not rewrite
the SCS blob or reset `absolute_expires_at`. Login, logout, token rotation, and
OIDC transient-state changes still write immediately.

When request middleware observes idle or absolute expiry, it destroys the row,
expires the cookie, and returns `401` immediately; it does not wait for the
hourly sweep.

The session-store tests and a focused benchmark must assert this bound. Activity
must remain indexed metadata; the implementation must not encode or `Put` it into
the SCS blob on every request.

## Data Model

### Users

The existing `users` table remains authoritative. Add `auth_version`:

```sql
ALTER TABLE users ADD COLUMN auth_version INTEGER NOT NULL DEFAULT 1;
```

Email stays unique after normalization. Roles remain `viewer`, `operator`, and
`admin`. Roles have no numeric or total ordering; handlers authorize named
capabilities. The last-active-admin invariant remains enforced transactionally.

### Linked Identities

```sql
CREATE TABLE user_identities (
  id             TEXT PRIMARY KEY,
  user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  issuer         TEXT NOT NULL,
  subject        TEXT NOT NULL,
  email_at_link  TEXT NOT NULL,
  created_at     TEXT NOT NULL,
  last_login_at  TEXT,
  UNIQUE (issuer, subject)
);

CREATE INDEX user_identities_user_idx ON user_identities(user_id);
```

One user may have more than one linked identity in the schema, although the first
UI supports only the installation's configured issuer.

### Security Audit Events

```sql
CREATE TABLE auth_audit_events (
  id            TEXT PRIMARY KEY,
  actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  event_type    TEXT NOT NULL,
  outcome       TEXT NOT NULL,
  target_type   TEXT,
  target_id     TEXT,
  remote_ip     TEXT,
  user_agent    TEXT,
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at    TEXT NOT NULL
);

CREATE INDEX auth_audit_events_created_idx
  ON auth_audit_events(created_at DESC);
CREATE INDEX auth_audit_events_actor_idx
  ON auth_audit_events(actor_user_id, created_at DESC);
```

Audit metadata must never contain session tokens, OAuth codes, verification
codes, ingest keys, SMTP credentials, or raw OIDC tokens. Retention is
configurable; the initial default is 90 days.

Required event families include login requested/succeeded/failed, logout,
session revoked, setup attempted/completed, user created/updated/deactivated/
deleted, role changed, identity linked/unlinked, OIDC denied, ingest key rotated,
and authorization denied.

## Authorization Model

Roles are convenient assignments; capabilities are the enforcement contract.
Handlers require named capabilities instead of comparing numeric role levels.

```go
type Capability string

const (
    ReadTelemetry       Capability = "telemetry:read"
    ManageOwnDashboards Capability = "dashboards:manage-own"
    ManageAlerts        Capability = "alerts:manage"
    RunAgent            Capability = "agent:run"
    ReadIngestMetadata  Capability = "ingest:read-metadata"
    ManageIngest        Capability = "ingest:manage"
    ManageUsers         Capability = "users:manage"
    ReadOperations      Capability = "operations:read"
)
```

The initial mapping is compiled into Fanout:

| Capability | Viewer | Operator | Admin |
|---|:---:|:---:|:---:|
| Read telemetry, traces, and logs | ✓ | ✓ | ✓ |
| Manage own dashboards | ✓ | ✓ | ✓ |
| Create/test/update/delete alert rules |  | ✓ | ✓ |
| Run the agent while it has write-capable tools |  | ✓ | ✓ |
| Read non-secret ingest connection metadata | ✓ | ✓ | ✓ |
| Rotate and manage ingest credentials |  |  | ✓ |
| Manage users and roles |  |  | ✓ |
| Read operational metrics/debug data |  |  | ✓ |

If the agent is later split into read-only and write-capable tool sets, viewers
may receive the read-only capability without receiving agent writes.

Every API route must explicitly declare one of:

- `Public`
- `Authenticated`
- `RequireCapability(...)`
- `ServiceCredential(...)`, optionally combined with a human capability or an
  explicit public configuration flag
- A protocol-specific guard such as MCP OAuth scope validation

There is no implicit "authenticated users may proceed" fallback for API routes.
New endpoints without a declaration fail closed in tests and code review.

The synthetic principal used by `PUBLIC_READ` does not inherit the complete
`viewer` role mapping. It receives only the explicit public telemetry-read
capability, preventing future viewer features from becoming public by accident.

### Route Policy

| Surface | Policy |
|---|---|
| `/healthz`, `/readyz`, minimal `/api/health` | Public |
| Setup, local login start/verify, OIDC start/callback | Public, flow-specific validation and throttling |
| Static SPA assets | Public |
| `/api/auth/me`, logout | Authenticated |
| `/api/observability/*` | `telemetry:read` |
| Personal dashboard reads/writes | `dashboards:manage-own` plus owner check |
| Alert and rule reads | `telemetry:read` |
| Alert and rule mutations/tests | `alerts:manage` |
| `/api/agent/*` | `agent:run` |
| Ingest settings read | `ingest:read-metadata`; real users only |
| Ingest credential rotation | `ingest:manage` |
| `/api/users/*` | `users:manage` |
| `/-/metrics` | `ServiceCredential(metrics)` with `operations:read` and explicit-public alternatives |
| `/debug/*` | `operations:read` or private network policy |
| `/mcp` and `/oauth/*` | Existing MCP OAuth protocol and scopes |

Ownership checks remain in the resource service or handler. Possessing
`dashboards:manage-own` does not permit guessing another user's dashboard ID.

The ingest-metadata response is intentionally limited to the fields already
needed by the authenticated empty-state UI:

```json
{
  "token_required": true,
  "suggested_endpoint": "ingest.example.com:4317",
  "tls_configured": true,
  "header_name": "x-fanout-ingest-token"
}
```

It never returns a token, token hash, token prefix, collector identity, or secret
configuration. This is not a privilege loosening from the current implementation:
today the route has no `RequireRole` guard and is readable by any authenticated
user. The new capability preserves that product behavior while removing access
from the synthetic public viewer.

### Metrics Scraper Authentication

Prometheus and similar scrapers cannot use an interactive browser session.
`/-/metrics` therefore accepts either:

- An admin browser session with `operations:read`; or
- `Authorization: Bearer <METRICS_TOKEN>`, compared in constant time.

`METRICS_TOKEN` is a separate service credential. It is not an ingest key and
cannot access telemetry APIs, user APIs, or debug handlers. Prometheus supports
reading a bearer token from a file, so deployments do not need to place it in a
scrape URL.

`METRICS_PUBLIC=false` is the secure default. Existing installations that
intentionally scrape the currently public endpoint must configure a token or
temporarily set `METRICS_PUBLIC=true` during upgrade. Startup emits a prominent
warning whenever metrics are public. A future dedicated loopback/private metrics
listener may replace the token for network-isolated deployments, but is not
required for Phase 1.

Route policy is resolved **before** any generic credential parser. The metrics
route is no longer placed on the global public-route bypass. Its
`ServiceCredential(metrics)` evaluator performs these checks in order:

1. Allow when `METRICS_PUBLIC=true`.
2. Compare a non-empty bearer value with `METRICS_TOKEN` in constant time.
3. Allow a valid browser session with `operations:read`; reject an authenticated
   lower-privilege user with `403`.
4. Otherwise return `401`.

The metrics bearer is evaluated only by this service-credential policy and is
never interpreted as a browser credential. The exhaustive route-policy test
treats `ServiceCredential(metrics)` as a first-class classification and verifies
all accepted alternatives and denial paths.

## Public Demo Mode

Public telemetry reading and unauthenticated ingest are separate settings:

```text
PUBLIC_READ=false
PUBLIC_INGEST=false
TRUSTED_PROXY_CIDRS=
```

`PUBLIC_READ=true` creates the existing synthetic viewer only for explicitly
listed telemetry `GET`/`HEAD` routes. It does not expose user data, settings,
operational metrics, debug handlers, personal dashboards, agent threads, or any
mutation.

`PUBLIC_INGEST=true` is a separate, loudly warned demo-only option. Enabling
public reads must never disable OTLP authentication. Production documentation
must recommend leaving both public values disabled on an internet-facing instance.
TRUSTED_PROXY_CIDRS is independent: leave it empty for direct traffic, or set
only the concrete reverse-proxy network. Fanout disables Echo's default trust
for loopback, link-local, and all RFC1918 addresses before adding these ranges.

## Login Abuse Controls

[`golang.org/x/time/rate`](https://pkg.go.dev/golang.org/x/time/rate) provides
the token-bucket primitive. Fanout maintains bounded, expiring keyed limiters.
Both relevant buckets must allow a request.

Initial defaults:

| Endpoint | Source-IP bucket | Account/email bucket |
|---|---:|---:|
| Setup attempts | 10 per 15 min | setup-token verification already constant-time |
| Login-code start | 20 per 15 min | 3 emails per 15 min |
| Login-code verify | 60 per 15 min | 30 per 15 min per email plus 3 attempts per code |
| OIDC start | 30 per 15 min | n/a |

Successful login does not immediately erase the source-IP limiter. Responses for
known and unknown users remain indistinguishable. `Retry-After` is returned for
throttled requests. The current store already selects only the latest unused
code, which makes older codes ineffective after a new code is issued. Phase 1
may additionally mark older rows used for clearer state and earlier cleanup, but
that is hardening rather than a new authentication behavior.

The first implementation is single-process, so in-memory source-IP throttles are
acceptable. Verify uses independent IP-only and normalized-email buckets, so
attacker-controlled email input cannot reset the source throttle. The bounded
key map uses constant-time LRU bookkeeping, never evicts a throttled bucket, and
rejects new keys with a rate-limited saturation warning when all capacity is
active. Email-send cooldown and verification-code attempt counts remain in
SQLite so a restart cannot be used to trigger an email burst.

## CSRF and Browser Request Policy

Moving authentication entirely to a cookie requires explicit request-origin
protection:

- State-changing browser requests require a same-origin `Origin` header, with a
  `Referer` fallback for supported legacy clients.
- The SPA sends a fixed custom header such as `X-Fanout-Request: 1` on
  state-changing API requests.
- `Sec-Fetch-Site: cross-site` is rejected for unsafe methods.
- `GET`, `HEAD`, and `OPTIONS` never mutate application state.
- OIDC callbacks are protected by state, nonce, PKCE, and exact redirect URI
  validation rather than the SPA header.
- MCP OAuth and token endpoints retain their protocol-specific protections and
  do not use the browser CSRF middleware blindly.

## Ingest Identities

Human users, browser sessions, MCP clients, and OTLP collectors are different
principal types and must not share credentials.

The existing single hashed ingest token remains acceptable during the browser
session migration. The next iteration replaces it with named ingest keys:

```text
id, name, token_prefix, token_hash, created_by, created_at,
expires_at, last_used_at, revoked_at
```

Multiple active keys allow overlap during rotation and attribute collector
traffic without exposing secret material. Only an admin with `ingest:manage`
can create or revoke them.

## MCP OAuth Boundary

The MCP OAuth implementation remains unchanged:

- Dynamic clients are not Fanout users.
- A client registration grants no data access.
- An existing browser user must authenticate and approve consent.
- OAuth access is restricted by resource and scope.
- OAuth refresh tokens remain opaque, hashed, rotated, and reuse-detecting.

The consent handler reads the current user from the SCS browser session. MCP tokens are never accepted as
browser sessions, and browser cookies are never accepted as MCP bearer tokens.

## Configuration

Representative configuration:

```dotenv
AUTH_MODE=local
SESSION_IDLE_TTL=12h
SESSION_ABSOLUTE_TTL=168h

# local mode
SMTP_HOST=
SMTP_PORT=587
SMTP_USER=
SMTP_PASS=
SMTP_FROM=
AUTH_CODE_SECRET=

# oidc mode
OIDC_ISSUER_URL=
OIDC_CLIENT_ID=
OIDC_CLIENT_SECRET=
OIDC_EMAIL_CLAIM=email
OIDC_EMAIL_VERIFICATION=required
OIDC_AUTO_PROVISION=false
OIDC_ALLOWED_GROUPS=
OIDC_ALLOWED_DOMAINS=
OIDC_DEFAULT_ROLE=viewer
OIDC_OPERATOR_GROUPS=
OIDC_ADMIN_GROUPS=

METRICS_TOKEN=
METRICS_PUBLIC=false
PUBLIC_READ=false
PUBLIC_INGEST=false
TRUSTED_PROXY_CIDRS=
```

Configuration validation is mode-specific:

- `SESSION_IDLE_TTL` must be at least five minutes and shorter than or equal to
  `SESSION_ABSOLUTE_TTL`. The activity checkpoint is derived from the idle TTL;
  it is not a separately configurable constant.
- `local` requires complete SMTP configuration and an `AUTH_CODE_SECRET` of at
  least 32 characters. There is no JWT-secret fallback; rotating this secret
  invalidates outstanding five-minute verification codes.
- `oidc` requires an HTTPS issuer, client ID, client secret, and a canonical
  external HTTPS URL used to construct an exact callback URI.
- `OIDC_EMAIL_VERIFICATION` is `required` by default. The only alternative is
  the explicit `issuer` trust override described in the OIDC flow; unknown
  values fail startup validation.
- Auto-provisioning requires at least one allowed group or domain.
- Default and mapped roles must be one of the compiled Fanout roles.
- An internet-facing metrics endpoint requires `METRICS_TOKEN` unless the
  operator explicitly accepts exposure with `METRICS_PUBLIC=true`.
- `proxy` requires an explicit trusted-proxy configuration when implemented.

Changing `AUTH_CODE_SECRET` immediately invalidates all outstanding email
verification codes because their HMACs were produced with the old key. Rotation
must therefore be documented as a brief login interruption bounded by the
five-minute code TTL. A user who submits a code issued before rotation receives
the existing generic invalid/expired-code response and must request a new code.

## Recovery

Host-controlled recovery codes are not implemented in this phase. Until the
future `fanout auth recovery-code --email <admin>` command exists, an OIDC outage
uses this explicit emergency procedure:

1. Stop Fanout and back up `DATA_DIR/control/fanout.sqlite`.
2. If no active administrator remains, use `sqlite3` locally to select the
   intended existing user and set `role = admin`, `active = 1`, increment
   `auth_version`, and delete that user’s rows from `sessions`. Never create an
   unknown identity or bypass the final-admin invariant. For example, after
   replacing the email placeholder and confirming it identifies the intended
   operator:

   ```sql
   BEGIN IMMEDIATE;
   UPDATE users
   SET role = admin, active = 1, auth_version = auth_version + 1,
       updated_at = strftime(%Y-%m-%dT%H:%M:%fZ, now)
   WHERE lower(email) = lower(operator@example.com);
   DELETE FROM sessions
   WHERE user_id = (SELECT id FROM users
                    WHERE lower(email) = lower(operator@example.com));
   COMMIT;
   ```

3. Restart with `AUTH_MODE=local`, complete SMTP settings, and a dedicated
   `AUTH_CODE_SECRET`; request a normal email code for that administrator.
4. Repair OIDC configuration, restore `AUTH_MODE=oidc`, and restart. Review the
   audit log and rotate credentials if compromise was suspected.

This is an operator-only break-glass procedure and causes a brief maintenance
window. The planned recovery command will require host access, work only for an
existing active administrator, issue one ten-minute single-use code, and audit
both generation and redemption. The normal web application will not expose
recovery-code generation.

## Administrative UX

The existing user-management page remains the primary control surface. It gains:

- Authentication source: local, linked OIDC issuer, or SCIM when available.
- Pending/unlinked status for pre-provisioned OIDC users.
- Activate/deactivate and role assignment.
- "Log out all sessions" action.
- Identity unlink action requiring confirmation and recent authentication.
- Audit history for the selected user.

The UI must explain that creating a user is authorization to enter Fanout; OIDC
login merely proves that the person controls the linked corporate identity.

## Failure and Security Semantics

- Authentication and authorization fail closed on SQLite errors.
- Invalid credentials return `401`; valid users lacking capability return `403`.
- Infrastructure failures return `500` and are not disguised as bad credentials.
- Unknown-user and known-user login-start responses remain identical.
- Secret-bearing responses use `Cache-Control: no-store`.
- Logout may return success even when the local session is already absent.
- Redirect targets are relative, same-origin paths only.
- The configured public URL and trusted-proxy rules determine secure-cookie and
  callback behavior; untrusted forwarded headers never do.
- Audit writes for authorization-changing mutations—user, role, identity,
  session-revocation, and service-key changes—are committed in the same SQLite
  transaction as the mutation. If the audit record cannot be written, the
  mutation rolls back and fails.
- Login request/success/failure, logout, and authorization-denial audit events
  are best-effort. An audit-table failure does not lock every user out or replace
  an intended `401`/`403`; it emits an error log, increments a high-severity
  metric, and triggers the configured operational alert.

## Migration

The browser-session migration deliberately favors simplicity over preserving old
sessions:

1. Add `users.auth_version`, `sessions`, `user_identities`, and
   `auth_audit_events` migrations.
2. Require a dedicated `AUTH_CODE_SECRET`; rotating it invalidates only the
   outstanding five-minute verification codes.
3. Wire SCS and the context-aware SQLite store behind the existing auth
   middleware, including indexed user/activity/absolute-expiry metadata and the
   idle-or-absolute sweep. Session reads honor request cancellation; session
   writes use the bounded cancellation-detached context defined above.
4. Change local setup and verification to create an SCS session instead of
   returning an access JWT.
5. Change the SPA to use same-origin cookie requests and remove
   `fanout.access-token` from `localStorage` on first boot.
6. Change MCP consent to read the SCS session.
7. Add metrics bearer authentication and document the required Prometheus
   configuration before changing the public route default.
8. Remove browser access/refresh JWT issuance, verification, cookies, endpoints,
   and configuration in the same release. Existing browser logins are invalidated
   at upgrade; MCP OAuth opaque tokens remain intact.

For an early-stage product, forcing one login after upgrade is preferable to a
dual session system. Existing users, roles, verification codes, dashboards,
agent threads, ingest tokens, MCP clients, and MCP OAuth tokens remain intact.

## Implementation Phases

### Phase 1 — Production Browser Sessions and Authorization

- Add SCS and the context-aware SQLite session adapter with queryable metadata,
  bounded activity checkpoints, per-user deletion, and expiry cleanup.
- Replace browser JWT/local-storage authentication.
- Add CSRF/origin middleware.
- Add login throttling and persistent email cooldown.
- Introduce typed capabilities and annotate every API route.
- Guard alert mutations, agent execution, metrics, and debug routes.
- Add the metrics bearer credential and an explicit compatibility migration for
  existing Prometheus scrapers.
- Add `ServiceCredential(...)` route classification and evaluate route policy
  before generic bearer parsing.
- Split `PUBLIC_READ` from `PUBLIC_INGEST`.
- Add durable authentication/authorization audit events.
- Make the exhaustive route-policy test a Phase 1 release blocker: the phase is
  not complete while any API route is unclassified.

### Phase 2 — Upstream OIDC

- Add go-oidc and promote `x/oauth2` to a direct dependency.
- Add OIDC configuration validation and login/callback routes.
- Add linked identities and admin-provisioned linking.
- Add optional group/domain-gated JIT provisioning.
- Add host-controlled recovery codes.

### Phase 3 — Service Identity and Proxy Integration

- Replace the global ingest token with named, overlapping ingest keys.
- Add trusted-proxy authentication with explicit network trust.
- Add session/audit management UI.

### Phase 4 — Enterprise Provisioning

- SCIM user lifecycle.
- Multiple OIDC issuers if demanded.
- IdP-group policy preview and synchronization.
- Audit export, retention policy, and security-event webhooks.
- Namespace/project authorization only if the product introduces a real
  multi-tenant or multi-project boundary.

Basic OIDC is included in the standard product. Potential separately packaged
features are automated provisioning, multiple IdPs, advanced group policy, audit
export/retention, and compliance integrations—not safe authentication itself.
This packaging statement is non-normative product policy based on the repository's
current AGPL-3.0 direction; a future licensing decision may update packaging
without changing the security architecture in this document.

## Test Strategy

### Session Tests

- Cookie flags, name, path, development exception, idle expiry, and absolute
  expiry.
- Successful authentication starts a new absolute deadline.
- `BeginPreAuthenticationSession` sets a bounded flow deadline, and callers
  outside the session package cannot invoke SCS renewal or deadline mutation.
- Every role change, identity-security change, deactivation, and log-out-all
  increments `auth_version` and deletes affected session rows.
- Current logout destroys the server record.
- `auth_version` invalidates every previous session without a role-specific
  exception.
- Missing, inactive, deleted, and database-error user cases fail closed.
- The sweep deletes sessions expired by idle time or absolute deadline.
- Active/expired counts and per-user session listing are computed from indexed
  columns without decoding session blobs.
- An active polling session writes at most once per derived checkpoint interval;
  requests inside the interval perform no session update.
- Concurrent checkpoint requests produce one conditional metadata update.
- Tests prove SCS selects `FindCtx`, `CommitCtx`, and `DeleteCtx` instead of the
  context-free fallback methods.
- Cancelling the request context cancels `FindCtx`, but does not cancel an
  in-flight `CommitCtx`, `DeleteCtx`, activity touch, authentication renewal, or
  per-user revocation write.
- Configuration rejects a session-store write timeout that is less than the
  control-database busy timeout plus the required margin.
- Under proportionally shortened test timeouts, a session write blocked for less
  than the SQLite busy window succeeds once the competing writer releases its
  lock.
- A stalled detached write fails at the bounded session-store write timeout.
- Disconnecting during logout cannot leave the submitted server-side session row
  valid; disconnecting during renewal cannot leave the old token row valid.

### Local Authentication Tests

- Setup token expiry, one-time use, and last-admin invariant.
- Known and unknown login-start responses are indistinguishable.
- A newly issued code makes older codes ineffective; optional eager invalidation
  preserves the same behavior.
- Verification expiry, attempt count, replay prevention, and concurrency.
- Rotating `AUTH_CODE_SECRET` invalidates outstanding codes and newly requested
  codes verify under the new secret.
- Source-IP and email throttling, cleanup, and `Retry-After`.

### OIDC Tests

- Discovery and JWKS validation using a local test issuer.
- State, nonce, PKCE, issuer, audience, expiry, and email-policy failures.
- Exact `(issuer, subject)` lookup.
- Safe initial linking by normalized email under both `required` and explicit
  `issuer` verification policies.
- Missing email and missing `email_verified` deny by default; no automatic
  fallback to `preferred_username` or another mutable claim.
- Disabled auto-provisioning denies unknown identities.
- Group/domain allow rules and default role.
- Missing/unknown claims never elevate a user.
- Concurrent first-login linking produces one identity.

### Authorization Tests

- A table-driven test covers every registered API route and role.
- Unclassified API routes fail the policy test.
- Viewer/operator/admin capability matrix.
- Owner checks prevent cross-user dashboard and agent-thread access.
- Public-read mode exposes only its explicit telemetry allowlist.
- Public-read mode never enables public ingest.
- Metrics accepts operations access or the dedicated bearer token, rejects an
  invalid token, and is public only with explicit opt-in; debug routes require
  operations access.
- The metrics bearer is evaluated only by `ServiceCredential(metrics)` and is
  never treated as a browser credential.
- Authorization failures emit redacted audit events.

### Integration Tests

- Local login → session → API request → logout.
- OIDC login → identity link → session → API request.
- OIDC consent for MCP using a browser session, followed by MCP token exchange.
- Deactivate user while browser and MCP sessions exist: browser access stops on
  the next request and MCP refresh is rejected/revoked by its existing logic.
- Upgrade fixture preserves users and resources while invalidating old browser
  JWT state cleanly; no JWT endpoint remains after migration.

## Operational Signals

Expose bounded metrics without user email labels:

- Authentication attempts and outcomes by mode/reason.
- Rate-limited requests by endpoint and bucket type.
- Active and expired session counts.
- Authorization denials by capability and route template.
- OIDC discovery/JWKS refresh failures.
- Provisioning and identity-link outcomes.
- Audit-event write failures.

An audit-write failure for an authorization-changing mutation fails and rolls
back that mutation. Authentication-event and denial audit failures are
best-effort so an audit outage cannot lock out the installation; they must emit
an error and high-severity metric. A metrics-recording failure does not affect
the request.

## Rejected Alternatives

### Keep Browser JWTs

Rejected. Fanout already performs a user database lookup on each request, so
stateless JWTs provide no scaling benefit. They make immediate logout and
revocation harder and currently require a JavaScript-readable access token.

### Full Authentication Framework Owns Users

Rejected. Most frameworks bring password, reset, organization, and policy models
that Fanout does not need. Adopting those models would increase migration and
operational complexity without eliminating Fanout's product-specific membership
and resource ownership rules.

### Bundled Identity Provider

Rejected. Keycloak, Authentik, Ory, and similar systems are valid upstream IdPs,
but bundling one would contradict Fanout's simple single-binary deployment goal.

### Casbin/OpenFGA Now

Rejected for the initial three-role, single-instance model. A typed capability
map and route-policy tests are smaller and easier to audit. Reconsider a policy
engine when users can hold different roles across organizations, projects, or
namespaces.

### Password Authentication

Rejected. Secure password storage is only a fraction of the required lifecycle;
reset, breach response, MFA, and recovery would all become Fanout's burden.

### Open Registration

Rejected. Telemetry contains operational and potentially sensitive business
data. Access must originate from an administrator or an explicit IdP
provisioning policy.

## References

- [OWASP Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
- [OWASP Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)
- [OWASP Authorization Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html)
- [OWASP CSRF Prevention Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)
- [OpenID Connect Core 1.0](https://openid.net/specs/openid-connect-core-1_0-18.html)
- [OAuth 2.0 Security Best Current Practice, RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html)
- [SCIM Protocol, RFC 7644](https://www.rfc-editor.org/rfc/rfc7644.html)
- [SCS session manager source: idle-timeout persistence](https://github.com/alexedwards/scs/blob/v2.9.0/data.go)
- [Microsoft Entra ID-token claims reference](https://learn.microsoft.com/en-us/entra/identity-platform/id-token-claims-reference)
