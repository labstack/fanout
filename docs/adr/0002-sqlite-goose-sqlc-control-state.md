# 0002. SQLite with Goose and sqlc for control state

Status: Accepted
Date: 2026-10-30

## Context

Fanout needs a transactional store for users, sessions, dashboards, alerts and
settings, without adding a database server to operate.

## Decision

Control state lives in SQLite through `modernc.org/sqlite` and `database/sql`,
with sqlc-generated query bindings and timestamped Goose SQL migrations.
`internal/db/migrations` is the single schema source for both Goose and sqlc.
Published migrations are immutable and checksummed in
`internal/db/migrations_test.go`. PostgreSQL and other migration frameworks are
not used for control state.

## Consequences

- No external database to deploy; backups are one file plus telemetry.
- Schema changes always add a new migration and run `just db-gen`.
- A database with application tables must have a positive applied Goose
  version before initialization; there is no schema fallback.
