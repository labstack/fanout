# Auth & Tenancy — Vision & Approach

## The Problem

Fanout currently has a single `API_TOKEN` for all access. No user accounts, no org boundary, no role enforcement. This is fine for solo use but blocks:

- Selling to teams (who can see/do what?)
- Cloud deployment (whose data is whose?)
- Enterprise (SSO, audit trail)

## Hierarchy

```
Organization (Acme Corp)
  └── Memberships (user + role)
       └── Teams (mapped to namespaces)
            └── Resources (services, alerts, investigations)
```

## Data Model

Adapted from the Vetted project (`/Users/v/Projects/labstack/vetted/src/db/schema.ts`) which already has a production-tested auth + org + membership model.

### Users

```sql
CREATE TABLE users (
  id          TEXT PRIMARY KEY,  -- UUIDv7
  email       TEXT NOT NULL UNIQUE,
  name        TEXT,
  role        TEXT NOT NULL DEFAULT 'user',  -- global: user | superadmin
  active      BOOLEAN NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

### Organizations

```sql
CREATE TABLE organizations (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  slug        TEXT NOT NULL UNIQUE,
  plan        TEXT NOT NULL DEFAULT 'free',  -- free | pro | enterprise
  owner_id    TEXT NOT NULL REFERENCES users(id),
  created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

### Memberships

```sql
CREATE TABLE memberships (
  id          TEXT PRIMARY KEY,
  org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role        TEXT NOT NULL DEFAULT 'viewer',  -- owner | admin | viewer
  created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(org_id, user_id)
);
```

### Roles

| Role | View data | Create alerts | Manage users | Manage org/billing |
|------|-----------|---------------|--------------|-------------------|
| Viewer | Yes | No | No | No |
| Admin | Yes | Yes | Yes | No |
| Owner | Yes | Yes | Yes | Yes |

### Auth: Verification Codes (passwordless login)

```sql
CREATE TABLE verification_codes (
  id          TEXT PRIMARY KEY,
  user_id     TEXT REFERENCES users(id) ON DELETE CASCADE,
  email       TEXT NOT NULL,
  code        TEXT NOT NULL,
  expires_at  TIMESTAMP NOT NULL,
  used_at     TIMESTAMP,
  created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

### Auth: Refresh Tokens

```sql
CREATE TABLE refresh_tokens (
  id          TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash  TEXT NOT NULL UNIQUE,
  expires_at  TIMESTAMP NOT NULL,
  revoked_at  TIMESTAMP,
  created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

## Mapping to Fanout Concepts

| Concept | Fanout today | With auth |
|---------|-------------|-----------|
| Who's accessing | API_TOKEN (shared) | User (email + JWT) |
| Whose data | tenant_id (captured, not enforced) | Organization → tenant_id |
| What they see | Everything | Membership role + namespace filter |
| What they can do | Everything | Role enforcement in API middleware |
| Team scoping | Namespace (exists, manual) | Team → namespace mapping on membership |

### Organization = Tenant

The `x-tenant-id` gRPC metadata from OTLP ingest maps to an organization's ID. Each org's telemetry data (spans, logs, metrics) is tagged with their org ID as the tenant. Query layer filters by the authenticated user's org.

### Team = Namespace

OTLP's `service.namespace` provides logical grouping. Teams don't need their own table — a membership can optionally include namespace filters:

```sql
ALTER TABLE memberships ADD COLUMN namespaces TEXT;  -- JSON array, null = all
```

If `namespaces` is null, the user sees all services. If set to `["backend","platform"]`, queries are filtered to those namespaces.

## Auth Flow

Passwordless email login (same as Vetted):

```
1. User enters email
2. Server sends 6-digit code to email
3. User enters code
4. Server issues JWT (access token, 15min) + refresh token (30 days)
5. Frontend stores JWT, sends as Authorization: Bearer header
6. Refresh token rotates on each use
```

### JWT Claims

```json
{
  "sub": "user-id",
  "org": "org-id",
  "role": "admin",
  "ns": ["backend", "platform"],
  "exp": 1234567890
}
```

The org, role, and namespace filter are in the JWT so the API middleware can enforce access without a database lookup on every request.

## API Middleware

Replace the current `API_TOKEN` check with JWT verification:

```go
// Current: single shared token
if subtle.ConstantTimeCompare(provided, tokenBytes) != 1 {
    return 401
}

// New: JWT verification
claims, err := jwt.Verify(token)
if err != nil {
    return 401
}
ctx.Set("user_id", claims.Sub)
ctx.Set("org_id", claims.Org)
ctx.Set("role", claims.Role)
ctx.Set("namespaces", claims.Namespaces)
```

Query layer reads `org_id` from context and adds `AND tenant = ?` to all queries. This is the single enforcement point — no per-handler filtering.

## Deployment Modes

### Self-Hosted (Phase 1)

- One org, created on first boot
- Users + memberships in SQLite (existing app state DB)
- First user is owner (created via CLI or env var)
- API_TOKEN still works as a fallback for MCP/CLI access
- No billing, no plan enforcement

### Cloud (Phase 2)

- Multiple orgs in PostgreSQL
- Signup flow creates org + owner membership
- Instance-per-tenant: each org gets its own DuckDB + lake directory
- Routing layer maps org slug to instance
- Billing via Stripe, plan field on org

### Cloud Shared Infra (Phase 3)

- Single DuckDB, all orgs share storage
- tenant_id enforced on every query path
- Cheaper per customer, higher security audit bar
- Only build this when instance-per-tenant costs become a problem

## Implementation Order

### For self-hosted v1 (minimal):

1. **Users table** in SQLite
2. **Passwordless email login** (send code, verify, issue JWT)
3. **JWT middleware** replacing API_TOKEN check
4. **Login page** in React
5. **Role enforcement** — viewers can't create alerts or rules
6. **User management page** for admins

### Defer:

- Org management UI (implicit single org for self-hosted)
- Team/namespace mapping on memberships (manual namespace param for now)
- SSO/SAML (enterprise ask)
- Passkeys/WebAuthn (nice-to-have)
- Audit logs (compliance ask)
- Billing/plan enforcement (cloud only)

## What Not to Build

- Custom RBAC editor — three hardcoded roles is enough
- Permission matrix UI — roles are simple enough to document
- Org switching — self-hosted has one org, cloud uses subdomains
- User profile page — email and name, that's it
