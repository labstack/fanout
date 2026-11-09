# Authentication and access operations

Operator reference for running Fanout's authentication safely. Settings are
environment variables; see the [settings reference](https://fanout.run/docs/reference/configuration).

## First run

At first boot, with zero users in the database, Fanout prints a setup banner to
stderr containing a **setup URL**:

```
============================================================
 FANOUT SETUP

 Open:  http://127.0.0.1:7520/login?setup_token=<credential>
 Valid: one-time use, expires in 1 hour
 Note:  this URL disappears after the first admin is created
 Warn:  it contains an administrator credential — this output may
        persist in container logs, log aggregators, and scrollback
============================================================
```

Setup credential behavior:

- The credential carries 256 bits of entropy and lives only in memory. It is
  never written to the database.
- It is consumed the moment the first administrator is committed, before any
  other setup work. A later failure in that request does not leave a live
  credential behind.
- It creates exactly **one** administrator. A consumed credential returns `410`
  and establishes no session; a concurrent request reaching account creation
  after the administrator already exists returns `403`.
- Ingest stays closed until setup succeeds.
- The banner is printed once. Restarting only reopens setup while the user
  count is still zero.

> [!WARNING]
> The setup URL is an administrator credential. Anywhere the process's stderr
> is collected — container logs, a log shipper, a terminal recording, a shared
> screen — can hold it until the credential is consumed or expires. Treat a
> leaked banner as a compromised installation and rebuild the database.

### Before you complete setup

Local mode requires only a 32-character `FANOUT_AUTH_CODE_SECRET`. SMTP is
optional: when all SMTP settings are present, users can request email codes;
without them, an operator with access to the control database can mint a login
link. OIDC mode instead requires an HTTPS issuer, a client, and an HTTPS
`FANOUT_PUBLIC_URL`; set `FANOUT_AUTH_CODE_SECRET` as well to keep
[break-glass recovery](#oidc-break-glass-recovery) available.

After setup in local mode, send yourself a code if SMTP is configured and
confirm it arrives. Credentials
can be wrong, the relay can refuse the sender, and mail can land in spam. A
login link remains available when delivery fails.

### Local self-signup

Local accounts are invitation-only by default. To let any verified email
address create an account, set `FANOUT_SELF_SIGNUP=true`. Fanout sends the
email code and creates an active `viewer` only after that code is
successfully verified. The role is fixed: self-signup can never create an
operator or administrator.
The setting requires complete SMTP configuration and local auth mode.

Self-signup remains unavailable until the first administrator completes setup.
Suspended accounts are not recreated or reactivated. Public installations
should enforce traffic and AI-budget limits at the deployment edge; Fanout also
retains its per-IP, per-address, expiry, and attempt limits described below.

### Recovery

If you are locked out in local mode, run the command with the same configuration
and data directory as the server:

```sh
fanout login-link admin@example.com
```

For a running container:

```sh
docker exec <container> fanout \
  login-link admin@example.com
```

The command refuses missing or suspended users and a missing control database.
It writes a hashed credential and a `login_link.issued` audit event, then prints
one URL to stderr. The URL expires after 15 minutes, works once, and writes a
distinct `login_link.redeemed` audit event when used. Running it while Fanout is
online is supported by the control database's WAL and busy-timeout settings.

Fix SMTP after regaining access if email-code login is expected. Ordinary
login links are unavailable in OIDC mode; use a break-glass link instead.

### OIDC break-glass recovery

When the identity provider is unavailable, an operator with access to the
control database can issue a one-time link for an active administrator:

```sh
fanout login-link --break-glass admin@example.com
```

`FANOUT_AUTH_CODE_SECRET` must be set to at least 32 characters, on both the
command and the server, because it protects the stored credential. OIDC mode
does not require the secret at startup, so an installation that wants this
recovery path must set it and restart Fanout before the outage. Without it the
command fails with an error naming the variable and `/api/auth/link/verify`
stays disabled.

The command refuses other roles, suspended users, and local mode. The link
expires after 15 minutes and works once. In OIDC mode `/api/auth/link/verify`
accepts only these links, refuses an ordinary login link without consuming it,
and re-checks that the account is still an active administrator. Issuing writes
a `login_link.break_glass_issued` audit event and redeeming writes a
`login_link.break_glass_redeemed` audit event. If the redemption event cannot be
written, Fanout creates no session and the request fails; the link is spent, so
issue a new one.

`/api/auth/link/verify` is rate limited per client address. Behind a reverse
proxy, set `FANOUT_TRUSTED_PROXY_CIDRS`; otherwise every request shares the
proxy's address and one limit, and other traffic can block recovery.

Fanout starts while the identity provider is unavailable, so break-glass access
also works after a restart during the outage. See
[Provider availability](#provider-availability).

### User account API

All user-management routes require an administrator. User projections, including
`GET /api/auth/me` and `GET /api/users`, expose `display_name` and `status`.
Status is either `active` or `suspended`; roles remain `viewer`, `operator`,
and `admin`. `GET /api/users` returns `{"users": [...]}`, including an empty array
when there are no users.

- `POST /api/users` provisions an account with `email`, `display_name`, and
  `role`. New accounts have status `active`.
- `PATCH /api/users/{id}` changes only `email` and `display_name`; omitted
  profile fields retain their values.
- `PATCH /api/users/{id}/role` accepts only `{"role":"viewer"}` (or
  `operator` or `admin`).
- `PATCH /api/users/{id}/status` accepts only `{"status":"suspended"}` or
  `{"status":"active"}`.

Unknown fields return `400`. Email, role, and status changes revoke browser
sessions. Suspension also blocks sign-in and revokes the user's OAuth tokens
and pending authorization codes. Reactivation revokes any that remain, so a
reactivated user starts with no credentials and signs in again. Demotion,
suspension, and deletion cannot remove the last active administrator. With an
OIDC role mapping, some of these routes are refused; see
[Who owns accounts](#who-owns-accounts).
The first-run `POST /api/auth/setup` request uses `display_name` alongside
`email` and `setup_token`.

### Adding local users without SMTP

User creation remains available when SMTP is not configured. The API returns
`201` with `invite_delivery: "not_configured"` and
`login_link_required: true`; mint the new active user a login link with the
same command shown above and deliver it through a trusted channel. When SMTP is
configured, Fanout sends the invitation synchronously and reports relay
failure rather than claiming it was delivered.

If the local account is suspended or its address is no longer usable, the
command refuses to mint a credential. Stop Fanout, copy the control database,
and repair that account without deleting it or its owned data:

```sql
SELECT id, email, display_name, role, status FROM users;

UPDATE users
   SET email = 'you@example.com', status = 'active', role = 'admin'
 WHERE id = '<the administrator id>';
```

Restart Fanout, mint a login link for the repaired address, and sign in. Never
recover by deleting the user: user deletion cascades to its sessions, OAuth
grants, and dashboards.

## Reverse proxies and client IP

Sign-in rate limiting and audit attribution use the client IP. MCP tool invocation
limits use the authenticated user and OAuth client. Behind a proxy, the client
address arrives in `X-Forwarded-For`; trust it only from configured proxies.

- **Default (no configuration):** forwarded headers are ignored and the direct
  peer address is used. Safe when Fanout is exposed directly.
- **Behind a proxy:** set `FANOUT_TRUSTED_PROXY_CIDRS` to the proxy networks,
  comma separated — for example `10.0.0.0/8,192.168.1.0/24`. Only these
  networks may present a forwarded address. Configuring this list also disables
  Echo's broad built-in trust of loopback, link-local, and RFC1918 ranges: your
  list becomes the complete trust set.

Set it to the address range of your proxy, never to `0.0.0.0/0`.

Related: `FANOUT_PUBLIC_URL` must be the externally reachable HTTPS URL so
session cookies are issued with `Secure`.

## OIDC

### Provider availability

Fanout runs OIDC discovery once at startup, for at most 5 seconds. If the
provider is unreachable or answers with an error, Fanout starts anyway and
serves ingest, MCP, and existing browser sessions. Discovery is retried in the
background, starting after 1 second and doubling to at most 1 minute between
attempts, until it succeeds or Fanout shuts down. No restart is needed.

Until then `/api/auth/oidc/start` and `/api/auth/oidc/callback` return 503 with
`Retry-After`, and the `oidc` check in `/readyz` reports `degraded` while the
endpoint still returns 200. The cause is in the logs.

Invalid configuration still fails startup: a value rejected by configuration
validation, or a provider that answers for an issuer other than
`FANOUT_OIDC_ISSUER_URL`. If a mismatch first appears on a background retry,
because the provider was down at startup, Fanout keeps running and retrying;
correct the issuer and restart.

### Public and community access

OIDC can provision a separate viewer account for every visitor whose provider
asserts a verified email. This keeps sessions, conversations, dashboards, and
audit attribution isolated without shared credentials or an anonymous
authorization bypass:

```sh
export FANOUT_AUTH_MODE=oidc
export FANOUT_OIDC_EMAIL_VERIFICATION=required
export FANOUT_OIDC_AUTO_PROVISION=true
export FANOUT_OIDC_ALLOWED_DOMAINS="*"
export FANOUT_OIDC_DEFAULT_ROLE=viewer
```

The wildcard is rejected with
`FANOUT_OIDC_EMAIL_VERIFICATION=issuer`. Use a specific domain or group allowlist
for a private installation. A wildcard makes the installation available to every
identity accepted by the configured provider, so rate-limit public traffic and
budget AI usage at the deployment edge.

Fanout’s roles apply across the instance:

- `viewer` reads telemetry, uses chat and MCP tools within viewer permissions,
  and manages its own dashboards.
- `operator` additionally manages alert rules.
- `admin` additionally manages users, ingest credentials, and operations.

### Eligibility is enforced on every login

`FANOUT_OIDC_ALLOWED_GROUPS` and `FANOUT_OIDC_ALLOWED_DOMAINS` are evaluated on
**every** login, including logins by an identity that is already linked.
Removing a user from an allowed group at the identity provider prevents their
next login; it does not wait for a provisioning cycle.

If neither list is configured there is no allow policy to enforce. Membership
is then managed locally through user administration, unless a role mapping is
set; see [Who owns accounts](#who-owns-accounts).

### Role ownership

If `FANOUT_OIDC_ADMIN_GROUPS` or `FANOUT_OIDC_OPERATOR_GROUPS` is set, the
identity provider owns each linked user's fixed role. On every login the mapped
role is applied; users in no mapped group fall back to
`FANOUT_OIDC_DEFAULT_ROLE`. A role change increments the user's auth version,
revokes their existing browser sessions, and writes a `role.changed` audit
event in one transaction.

If neither group list is configured, roles are managed locally and logins never
change them.

The last active administrator is never demoted by this reconciliation — the
installation would lose its ability to administer itself. Promote a second
administrator before expecting the mapping to apply to the first.

### Who owns accounts

Role mapping also moves account ownership to the identity provider, because a
later login with the same verified email would otherwise link to a local account
an administrator created. `GET /api/auth/status` reports the result as
`account_ownership`, and Settings shows it to everyone who is signed in:

| `account_ownership` | Configuration | Browser user routes refuse |
|---|---|---|
| `local` | No role mapping, with or without a SCIM token | Nothing |
| `identity_provider_roles` | Role mapping and `FANOUT_OIDC_AUTO_PROVISION`, no SCIM token | Creating a user and changing a role |
| `identity_provider_roles_no_provisioning` | Role mapping, no `FANOUT_OIDC_AUTO_PROVISION`, no SCIM token | Creating a user and changing a role |
| `identity_provider_directory` | Role mapping and a SCIM token | Creating and deleting a user; changing a role, status or email |

A refused request returns `403` with the code `managed_by_identity_provider`,
and it changes nothing. The check runs before the request body is validated,
except on `PATCH /api/users/{id}`, which has to decode the body to see whether
`email` is present. Malformed JSON there returns `400`.

To add users under a role mapping, set `FANOUT_OIDC_AUTO_PROVISION` with
`FANOUT_OIDC_ALLOWED_GROUPS` or `FANOUT_OIDC_ALLOWED_DOMAINS`, or configure a
SCIM token. With neither, `POST /api/users` is refused and a first login is
denied as not provisioned, so nobody new can get an account. Fanout still starts
in this configuration, logs a warning at startup, and Settings says so. Existing
accounts keep working.

Only `POST /api/users` and the `/api/users/{id}` routes are affected. These keep
working in every mode:

- SCIM provisioning and login-time role reconciliation and provisioning.
- `PATCH /api/users/{id}` with only `display_name`. A directory that sends
  `displayName` overwrites the value on its next update.
- `POST /api/users/{id}/access/revoke`, which ends sessions and OAuth tokens
  without changing the account.
- `POST /api/auth/setup` for the first administrator when no SCIM token is set.
- `fanout login-link --break-glass`.

Without a SCIM token, an administrator can still suspend, reactivate, delete and
change the email of a user from the browser routes.

### Microsoft Entra ID group overage

Past roughly 200 group memberships, Entra omits the `groups` claim from the
token entirely and emits `_claim_names`/`_claim_sources` pointing at the Graph
API instead. An omitted claim does not mean the user belongs to no groups.

When any group-based policy is configured and the token signals overage, Fanout
**denies the login** and names the group claim in the error.

Fix it at the identity provider: configure the application registration to emit
only **groups assigned to the application** rather than all group memberships.
Installations that use only `FANOUT_OIDC_ALLOWED_DOMAINS` are unaffected.

### Email verification

`FANOUT_OIDC_EMAIL_VERIFICATION` defaults to `required`: an identity is linked
to a local account by matching email only when the provider asserts the email is
verified.

The `issuer` value relaxes that check and is accepted only when an allow policy
is configured, on the basis that the issuer itself vouches for the address. Use
it only with a provider you administer.

## MCP OAuth connections

Signed-in users can review their connected agents in Settings and disconnect
one agent at a time. Disconnecting immediately revokes that client's access and
refresh tokens for the current user. Other agents and browser sessions stay
connected. To reconnect, authorize the agent again.

`GET /api/auth/oauth/connections` lists the current user's live connections;
`POST /api/auth/oauth/connections/{id}/revoke` disconnects one client. Both
require a browser session. Administrators can still revoke all of a user's
access, including browser sessions and OAuth tokens, through
`POST /api/users/{id}/access/revoke`.

## SCIM provisioning

Set `FANOUT_SCIM_TOKEN` (or `FANOUT_SCIM_TOKEN_FILE`) to let an identity provider
or automation create, update and deactivate accounts. The setting requires
`FANOUT_AUTH_MODE=oidc` and a value of at least 32 characters with no whitespace.
Without it the routes do not exist and nothing below applies.

With a token configured, Fanout prints no setup banner, `/api/auth/setup` returns
`410`, and `/api/auth/status` reports `setup_required: false`. The first
administrator comes from SCIM. Until one exists, signed-out API requests return
`401` with `no accounts have been provisioned yet`.

Fanout implements a small subset of SCIM 2.0 (RFC 7644) under `/scim/v2`.
Requests send `Authorization: Bearer <token>`, and bodies use
`application/scim+json` or `application/json`. Responses use
`application/scim+json`, and failures use the RFC 7644 error format.

| Route | Purpose |
|---|---|
| `POST /scim/v2/Users` | Create an account and link its identity |
| `GET /scim/v2/Users?filter=...` | Find an account |
| `GET /scim/v2/Users/{id}` | Read an account |
| `PATCH /scim/v2/Users/{id}` | Change `active`, the role, the email or `displayName` |

Other methods return `405` and other paths under `/scim/v2` return `404`. Delete
is not supported; deactivate with `active: false`. The `ServiceProviderConfig`,
`Schemas` and `ResourceTypes` discovery endpoints are not implemented.

### Creating accounts

`POST` requires the core User schema, `externalId`, `userName` and one value in
`roles`. `externalId` is the subject (`sub`) at the configured issuer, and is
treated as an exact opaque string. `userName` is the email address. If `emails`
is sent, its primary value must be the same address. The role is `admin`,
`operator` or `viewer`. `active` defaults to `true`.

The account and the identity `(FANOUT_OIDC_ISSUER_URL, externalId)` are written
in one transaction with their audit events. The user's first OIDC login finds
that identity, so no match by email takes place.

A `POST` for an identity that already exists returns `200` with the current
account and changes nothing, so a delayed retry cannot undo a later change. Send
a `PATCH` to change an existing account. A new account returns `201`. An email
held by a different account returns `409` with `scimType` `uniqueness`; Fanout
never links an existing account by email.

While there are no users, only an active administrator can be created, using the
same transactional check as setup. Any other request returns `409`, so it can be
retried once an administrator exists. Concurrent administrators are all created.

### Finding accounts

`filter` accepts exactly `externalId eq "value"` and `userName eq "value"`.
Attribute names and `eq` are case-insensitive, and the value is a JSON string,
so `"` and `\` must be escaped. `externalId` matches exactly; `userName` is
normalized like any email address. Anything else, including a missing filter,
returns `400` with `scimType` `invalidFilter`. `startIndex` and `count` are
accepted.

### Updating accounts

`PATCH` takes the `PatchOp` schema with `add` or `replace` operations on `active`,
`roles`, `userName`, `emails` and `displayName`, with or without a `path`. A path
filter such as `emails[type eq "work"].value` is not supported. `remove` returns
`400`. All operations in a request apply as one update, so the checks below see
the final state.

A request that changes nothing returns `200` and writes nothing: no session
revocation, no token revocation and no audit event. Resending the same state is
safe. Changing the email or reactivating an account keeps its identity link.

Deactivating or demoting the last active administrator returns `409`. The check
runs in the same write transaction as the update.

Deactivation suspends the account. In one transaction it revokes the user's
browser sessions, revokes their MCP OAuth access and refresh tokens, and deletes
their pending OAuth authorization codes. Reactivation restores none of these, so
the user signs in and connects MCP clients again. Deactivating an account that
is already suspended removes anything still live and writes a `session.revoked`
event; if nothing is live it writes nothing.

Requests to `/scim/v2` are logged without their query string, because a filter
holds an email address or subject.

Every change writes an audit event: `user.provisioned` and `identity.linked` on
creation, then `user.suspended`, `user.reactivated`, `role.changed` or
`user.updated`. Metadata records `mode: scim`, which fields changed and any role
or status transition. Addresses and the token are never recorded.

The token is compared in constant time and is never logged. Rotate it by
changing the setting and restarting Fanout.

## Ingest tokens

OTLP exporters authenticate with a bearer token, `foi_` followed by 43 base64url
characters. An instance can hold up to 50 active tokens, each with its own name,
so one Collector can be revoked without touching the rest. Names are 1 to 64
printable characters and unique among active tokens, ignoring ASCII case.

Fanout stores only each token's SHA-256 digest, plus its last four characters
as a hint. The value appears once, in the response that creates it, and that
response is sent with `Cache-Control: no-store`. It is never written to logs,
audit events, or any later response.

Authenticating an export costs one digest and one lookup on an indexed column.
Fanout keeps no credential cache, so a revoked token is refused on the very next
request, with no delay or restart. Last-used times are coalesced in memory and
written about once a minute and at shutdown, so a listing can lag real use by up
to a minute.

Administrators manage tokens on the Settings page or at `/settings/ingest`, a
stable URL that also works as an OIDC `return_to` target. Every other role can
read connection details but cannot list token names or hints; those routes
require `ingest:manage`. A token is only ever created by an explicit POST.

To rotate, create a replacement, move the Collector across, then revoke the old
token. Revoking the last active token is allowed and stops ingest until another
is created; the UI warns first.

First-admin setup issues a token named `default`, and `FANOUT_INGEST_TOKEN` seeds
one named `configuration`, but only when no token row has ever existed. A revoked
row counts, so a restart that still carries the setting cannot bring back a token
an administrator revoked. An instance upgraded from a single-token release keeps
its existing digest as a token named `default`, and Collectors using it are not
affected. A hint is not available for that migrated token.

Revoked rows are retained indefinitely: they record who used which name when, and
the seed rule depends on them. The name of a revoked token can be reused. Creates
and revokes write `ingest_token.created` and `ingest_token.revoked` audit events
with the token id and name.

## Credentials at a glance

| Credential | Purpose | Rotation |
|---|---|---|
| Setup URL | Creates the first administrator | One-time, expires in 1 hour; not issued when `FANOUT_SCIM_TOKEN` is set |
| Login URL | Authenticates one existing active local user | One-time, expires in 15 minutes; mint with `fanout login-link` |
| Break-glass URL | Authenticates one active administrator in OIDC mode | One-time, expires in 15 minutes; mint with `fanout login-link --break-glass` |
| Browser session | Interactive access | Idle and absolute TTL; revoked on role, email, or status change |
| Ingest tokens | OTLP ingest | Up to 50 named tokens at `/settings/ingest`; rotate by creating then revoking. `FANOUT_INGEST_TOKEN` seeds only the first token |
| Metrics token | `/metrics` scrape | `FANOUT_METRICS_TOKEN` |
| SCIM token | User provisioning at `/scim/v2` | `FANOUT_SCIM_TOKEN`; change the setting and restart |
| MCP OAuth tokens | MCP clients | 30-day access tokens, checked on every request; 90-day refresh tokens renewed on each use. Refresh rotation with replay-family revocation, admitting the same client's rotated token for one access-token lifetime so processes sharing a credential can refresh from stale copies. Users disconnect individual agents in Settings; admins can still revoke all of a user's access |

Local email codes allow three attempts per code and expire after five minutes.
Attempts are reserved atomically, so concurrent guesses cannot exceed the limit.
