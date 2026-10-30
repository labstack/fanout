# 0006. One structured query path

Status: Accepted
Date: 2026-10-30

## Context

Telemetry is queried from dashboards, FanQL text, MCP tools and the agent, and
a visual builder is planned. Separate query paths would each need their own validation,
limits and SQL generation, and could diverge.

## Decision

Every surface produces the same validated structured query (`from`, `where`,
`measures`, `by`, `bucket`, `histogram`, `sort`, `limit`), which one engine path
validates, budgets and turns into SQL. FanQL is the text form of that query and
compiles to it; its filters become predicates inside the query, checked by the
same validation, and it never runs SQL of its own. Arbitrary read-only SQL remains a
separate, locked-down gate for what the structured query cannot express.

## Consequences

- One place enforces relations, functions and budgets.
- A FanQL query executes through the same engine path as the equivalent
  dashboard panel.
- New query features are added once and become available everywhere.
