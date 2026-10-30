# Design proposals

A proposal describes a significant change before it is built: what it does,
why, and what was considered instead. Write one when a change adds or changes
a product capability, a public contract (HTTP, MCP, configuration, storage
format), or a cross-cutting design. Small fixes and refactors do not need one.

Proposals are numbered in order: `NNNN-short-slug.md`. Keep the number and file
name when the status changes, so links stay stable.

## Template

```markdown
# Title

Status: Draft | Accepted | Implemented | Superseded by NNNN
Date: YYYY-MM-DD
Pull request: #NNN

## Summary
## Motivation
## Goals and non-goals
## Design
## Alternatives
## Risks
## Open questions
```

Earlier proposals predate the template and keep their original structure.

## Index

| # | Proposal | Status |
|---|---|---|
| 0001 | [Logged-in UI: shell, chat pane, native widgets, sign-in code step](0001-logged-in-ui-shell.md) | Implemented |
| 0002 | [Agent-built dashboards](0002-agent-dashboards.md) | Implemented |
| 0003 | [Milestone 5: correct metrics and the system page](0003-agent-dashboards-m5.md) | Implemented |
| 0004 | [Full UI without an LLM key, and FanQL](0004-fanql.md) | Accepted |

Settled decisions that a proposal leads to are recorded in [../adr](../adr/README.md).
