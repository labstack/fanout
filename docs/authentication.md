# Authentication and access operations

Operator reference for running Fanout's authentication safely. Every setting
below is available as an environment variable and as a key in the layered
configuration file.

## First run

At first boot, with zero users in the database, Fanout prints a setup banner to
stderr containing a **setup URL**:

```
============================================================
 FANOUT SETUP

 Open:  http://127.0.0.1:8080/login?setup_token=<credential>
 Valid: one-time use, expires in 1 hour
 Note:  this URL disappears after the first admin is created
 Warn:  it contains an administrator credential — this output may
        persist in container logs, log aggregators, and scrollback
============================================================
```

Properties worth knowing:

- The credential carries 128 bits of entropy and lives only in memory. It is
  never written to the database.
- It is consumed the moment the first administrator is committed, before any
  other setup work. A later failure in that request does not leave a live
  credential behind.
- It creates exactly **one** administrator. Presenting it again after setup is
  complete returns `403` and establishes no session.
- Ingest stays closed until setup succeeds.
- The banner is printed once. Restarting only reopens setup while the user
  count is still zero.

> [!WARNING]
> The setup URL is an administrator credential. Anywhere the process's stderr
> is collected — container logs, a log shipper, a terminal recording, a shared
> screen — now holds it until the credential is consumed or expires. Treat a
> leaked banner as a compromised installation and rebuild the database.

### Before you complete setup

Local mode requires only a 32-character `FANOUT_AUTH_CODE_SECRET`. SMTP is
optional: when all SMTP settings are present, users can request email codes;
without them, an operator with access to the control database can mint a login
link. OIDC mode instead requires an HTTPS issuer, a client, and an HTTPS
`FANOUT_PUBLIC_URL`.

If you configure SMTP, send yourself a code and confirm it arrives. Credentials
can be wrong, the relay can refuse the sender, and mail can land in spam. A
login link remains available when delivery fails.

### Local self-signup

Local accounts are invitation-only by default. To let any verified email
address create an account, enable `auth.self_signup` or
`FANOUT_SELF_SIGNUP=true`. Fanout sends the existing email code and
creates an active `viewer` only after that code is successfully verified. The
role is fixed: self-signup can never create an operator or administrator.
The setting requires complete SMTP configuration and local auth mode.

Self-signup remains unavailable until the first administrator completes setup.
Inactive accounts are not recreated or reactivated. Public installations
should enforce traffic and AI-budget limits at the deployment edge; Fanout also
retains its per-IP, per-address, expiry, and attempt limits described below.

### Recovery

If you are locked out in local mode, run the command with the same configuration
and data directory as the server:

```sh
fanout --config /etc/fanout/fanout.yaml login-link admin@example.com
```

For a container using the bundled configuration:

```sh
docker exec <container> fanout --config /etc/fanout/fanout.yaml \
  login-link admin@example.com
```

The command refuses missing or inactive users and a missing control database.
It writes a hashed credential and a `login_link.issued` audit event, then prints
one URL to stderr. The URL expires after 15 minutes, works once, and writes a
distinct `login_link.redeemed` audit event when used. Running it while Fanout is
online is supported by the control database's WAL and busy-timeout settings.

Fix SMTP after regaining access if email-code login is expected. OIDC-mode
recovery remains at the identity provider or OIDC configuration layer; local
login links are deliberately unavailable in that mode.

### Adding local users without SMTP

User creation remains available when SMTP is not configured. The API returns
`201` with `invite_delivery: "not_configured"` and
`login_link_required: true`; mint the new active user a login link with the
same command shown above and deliver it through a trusted channel. When SMTP is
configured, Fanout sends the invitation synchronously and reports relay
failure rather than claiming it was delivered.

If the local account is inactive or its address is no longer usable, the
command refuses to mint a credential. Stop Fanout, copy the control database,
and repair that account without deleting it or its owned data:

```sql
SELECT id, email, role, active FROM users;

UPDATE users
   SET email = 'you@example.com', active = 1, role = 'admin'
 WHERE id = '<the administrator id>';
```

Restart Fanout, mint a login link for the repaired address, and sign in. Never
recover by deleting the user: user deletion cascades to its sessions, OAuth
grants, and dashboards.

## Reverse proxies and client IP

Rate limiting and audit attribution both key on the client IP. Behind a proxy,
that address arrives in `X-Forwarded-For`, and trusting the header blindly lets
any client forge both.

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

### Public and community access

OIDC can provision a separate viewer account for every visitor whose provider
asserts a verified email. This keeps sessions, conversations, dashboards, and
audit attribution isolated without shared credentials or an anonymous
authorization bypass:

```yaml
auth:
  mode: oidc
  oidc:
    email_verification: required
    auto_provision: true
    allowed_domains: "*"
    default_role: viewer
```

The wildcard is deliberately explicit and is rejected with
`email_verification: issuer`. Use a specific domain or group allowlist for a
private installation. A wildcard makes the installation available to every
identity accepted by the configured provider, so rate-limit public traffic and
budget AI usage at the deployment edge.

Fanout's roles remain installation-wide rather than deployment-specific:

- `viewer` reads telemetry, uses chat and MCP tools within viewer permissions,
  and manages its own dashboards.
- `operator` additionally manages alert rules.
- `admin` additionally manages users, ingest credentials, and operations.

### Eligibility is enforced on every login

`FANOUT_OIDC_ALLOWED_GROUPS` and `FANOUT_OIDC_ALLOWED_DOMAINS` are evaluated on
**every** login, including logins by an identity that is already linked.
Removing a user from an allowed group at the identity provider prevents their
next login; it does not wait for a provisioning cycle.

If neither list is configured there is no allow policy to enforce, and
membership is managed locally through user administration.

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

### Microsoft Entra ID group overage

Past roughly 200 group memberships, Entra omits the `groups` claim from the
token entirely and emits `_claim_names`/`_claim_sources` pointing at the Graph
API instead. An omitted claim does not mean the user belongs to no groups.

When any group-based policy is configured and the token signals overage, Fanout
**denies the login** rather than reading the absence as an empty group set —
the alternative would demote or lock out every member of a large directory at
once. The denial names the group claim so the remediation is clear.

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

## Credentials at a glance

| Credential | Purpose | Rotation |
|---|---|---|
| Setup URL | Creates the first administrator | One-time, expires in 1 hour |
| Login URL | Authenticates one existing active local user | One-time, expires in 15 minutes; mint with `fanout login-link` |
| Browser session | Interactive access | Idle and absolute TTL; revoked on role, email, or status change |
| Ingest token | OTLP ingest | Settings page |
| Metrics token | `/metrics` scrape | `FANOUT_METRICS_TOKEN` |
| MCP OAuth tokens | MCP clients | Refresh rotation with replay-family revocation |

Local email codes allow three attempts per code and expire after five minutes.
Attempts are reserved atomically, so concurrent guesses cannot exceed the limit.
