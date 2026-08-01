# identity-and-access Specification

## Purpose

Defines Fanout's first-admin setup, local and OIDC authentication, server-side sessions, role capabilities, user lifecycle, audit trail, MCP OAuth, and public demo boundaries.

## Requirements

### Requirement: First boot creates exactly one initial administrator
When no users exist, Fanout SHALL print a one-time setup token that expires after one hour. A valid setup request SHALL atomically create the first administrator with its setup audit event and prevent any concurrent or later request from creating a second initial administrator. Browser-session establishment, initial ingest-token persistence, and setup-token clearing occur as follow-on operations rather than in that database transaction.

#### Scenario: Setup token is reused
- **WHEN** a setup request succeeds and the same token is presented again
- **THEN** Fanout does not create another administrator or rotate an existing ingest token

#### Scenario: A post-creation setup step fails
- **WHEN** the first administrator commits but session or ingest-token setup returns an error
- **THEN** Fanout retains the committed administrator and a retry does not create a second initial administrator

### Requirement: Local login uses email verification codes
In local mode, Fanout SHALL send short-lived, single-use verification codes by configured SMTP, avoid passwords, normalize email identities, rate-limit requests and guesses, and return non-enumerating responses for missing or inactive users.

#### Scenario: Unknown email requests a code
- **WHEN** the address does not identify an active user
- **THEN** Fanout returns the same accepted response shape as for a known user without delivering access

### Requirement: OIDC login validates issuer identity and policy
In OIDC mode, Fanout SHALL use authorization code flow with state, nonce, and PKCE; require a canonical HTTPS public callback origin; validate configured email assurance; and enforce allowed domain or group policy where configured.

#### Scenario: OIDC identity fails the allow policy
- **WHEN** the identity has a valid provider token but no allowed group or domain
- **THEN** Fanout denies login without linking or provisioning an account

### Requirement: OIDC provisioning and roles are controlled
Fanout SHALL link identities by issuer and subject, use existing active users when eligible, and auto-provision only when explicitly enabled and constrained by an allow policy. Configured group mappings SHALL determine operator or administrator elevation; otherwise the validated default role applies.

#### Scenario: Allowed new identity signs in with auto-provisioning disabled
- **WHEN** no existing user or linked identity exists
- **THEN** Fanout denies access rather than creating an account

### Requirement: Browser sessions are opaque server-side records
Fanout SHALL use opaque HttpOnly cookies backed by server-side SQLite session records with configurable idle and absolute expiration. Logout MUST delete the server record and clear the cookie.

#### Scenario: User signs out
- **WHEN** logout succeeds
- **THEN** replaying the former cookie does not restore the session

### Requirement: Roles grant explicit capabilities
Fanout SHALL support `viewer`, `operator`, and `admin` roles. Viewers SHALL read telemetry, manage their own dashboards, and read ingest metadata. Operators SHALL additionally manage alerts and run the agent. Administrators SHALL additionally manage ingest credentials and users and read protected operations endpoints.

#### Scenario: Operator requests user administration
- **WHEN** an operator calls an administrator-only user endpoint
- **THEN** Fanout denies the request even though the operator can run investigations and manage alerts

### Requirement: User security changes revoke sessions
Changing a user's email, role, or active state SHALL revoke that user's browser sessions. Deletion and explicit logout-all SHALL also invalidate sessions, and Fanout MUST prevent removal or deactivation of the final active administrator.

#### Scenario: Administrator demotes a signed-in administrator
- **WHEN** another active administrator changes that user's role
- **THEN** the changed user's existing browser sessions are revoked

### Requirement: Public read is narrowly classified
When `PUBLIC_READ=true`, Fanout SHALL create only a synthetic anonymous viewer for explicitly classified read-only routes. It MUST NOT authorize agent runs, mutations, user management, ingest, protected metrics, or MCP OAuth consent.

#### Scenario: Anonymous visitor attempts a mutation
- **WHEN** public read is enabled and the visitor sends a protected write request
- **THEN** Fanout requires a real authenticated principal with the corresponding capability

### Requirement: Remote MCP uses OAuth credentials distinct from browser and ingest auth
Fanout SHALL protect `/mcp` with OAuth 2.1-style discovery, dynamic client registration, authorization code plus PKCE, audience-bound short-lived access tokens, rotating refresh-token families, and explicit scopes. The browser's same-origin MCP adapter SHALL use a validated browser session only at `/api/mcp`.

#### Scenario: Collector token is presented to `/mcp`
- **WHEN** a client uses the ingest token as an MCP bearer token
- **THEN** Fanout rejects it because it is not an audience-bound MCP access token

### Requirement: Security-sensitive actions are audited
Fanout SHALL persist bounded audit events for setup, login outcomes, logout, user changes, session revocation, identity linking, and ingest-token rotation without storing raw credentials. MCP OAuth consent approval is currently written to the process log rather than the persisted audit store.

#### Scenario: Administrator rotates ingest credentials
- **WHEN** the rotation commits
- **THEN** the audit trail records actor, event type, outcome, target, remote context, and safe metadata
