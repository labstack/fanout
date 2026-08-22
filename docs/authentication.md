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

After the first administrator exists, access uses the normal login path, so
confirm that path works before the setup credential is consumed.

Fanout refuses to start without one configured: `local` mode requires
`FANOUT_SMTP_HOST`, `FANOUT_SMTP_PORT`, `FANOUT_SMTP_USERNAME`,
`FANOUT_SMTP_PASSWORD`, `FANOUT_SMTP_FROM`, and a 32-character
`FANOUT_AUTH_CODE_SECRET`; `oidc` mode requires an HTTPS issuer, a client, and
an HTTPS `FANOUT_PUBLIC_URL`.

Configuration being present is not the same as delivery working. Credentials
can be wrong, the relay can refuse the sender, and mail can land in spam. Send
yourself a code and confirm it arrives before you finish setup — a login path
that is configured but broken is the case the recovery section below exists
for.

### Recovery

If you are locked out — the first administrator exists, but no login path works
— work through these in order. Copy the database file before any of them.

**1. Fix the login path.** Configure SMTP (`local` mode) or OIDC and restart.
This resolves the common case, where setup completed before mail delivery was
configured.

**2. Repoint the administrator account.** If the administrator's address is one
you cannot receive mail at, change it. Stop Fanout, then against the SQLite
database:

```sql
SELECT id, email, role, active FROM users;

UPDATE users
   SET email = 'you@example.com', active = 1, role = 'admin'
 WHERE id = '<the administrator id>';
```

Restart and log in normally. Nothing is lost.

**3. Reopen first-run setup.** Only if the account itself is unrecoverable:

```sql
DELETE FROM users;
```

Restart Fanout. With zero users it prints a fresh setup banner.

> [!CAUTION]
> Step 3 cascades. Deleting users also deletes their browser sessions, MCP
> OAuth grants and tokens, and **every dashboard they own**. Audit history and
> telemetry survive — audit rows keep the event with the actor set to null —
> and the ingest token is unaffected. Prefer step 2.

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
| Browser session | Interactive access | Idle and absolute TTL; revoked on role, email, or status change |
| Ingest token | OTLP ingest | Settings page |
| Metrics token | `/metrics` scrape | `FANOUT_METRICS_TOKEN` |
| MCP OAuth tokens | MCP clients | Refresh rotation with replay-family revocation |

Local email codes allow three attempts per code and expire after five minutes.
Attempts are reserved atomically, so concurrent guesses cannot exceed the limit.
