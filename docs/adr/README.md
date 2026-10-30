# Architecture decision records

An ADR records one decision that shapes the code, with its context and
consequences, in a page or less. [AGENTS.md](../../AGENTS.md) holds the binding
rules; an ADR records why a rule exists. Write one when a decision would
otherwise be re-argued. Supersede an ADR with a new one rather than editing its
decision.

ADRs are numbered in order: `NNNN-short-slug.md`.

## Template

```markdown
# NNNN. Title

Status: Accepted | Superseded by NNNN
Date: YYYY-MM-DD

## Context
## Decision
## Consequences
```

## Index

| # | Decision | Status |
|---|---|---|
| 0001 | [Environment-only configuration](0001-environment-only-configuration.md) | Accepted |
| 0002 | [SQLite with Goose and sqlc for control state](0002-sqlite-goose-sqlc-control-state.md) | Accepted |
| 0003 | [Pinned DuckDB engine and batch format 4](0003-pinned-duckdb-and-batch-format-4.md) | Accepted |
| 0004 | [No legacy paths before release](0004-no-legacy-paths-before-release.md) | Accepted |
| 0005 | [CalVer releases](0005-calver-releases.md) | Accepted |
| 0006 | [One structured query path](0006-one-structured-query-path.md) | Accepted |
| 0007 | [A hand-written FanQL parser](0007-hand-written-fanql-parser.md) | Accepted |
