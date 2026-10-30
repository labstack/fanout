# 0007. A hand-written FanQL parser

Status: Accepted
Date: 2026-10-30

## Context

FanQL needs precise, recovering diagnostics with source spans and suggested
fixes, position-aware completion, and canonical printing, all fast enough to
run on every keystroke.

## Decision

FanQL uses a hand-written lexer and recursive-descent parser in Go, with no
parser-generator or grammar library. It recovers after errors so it can report
every independent problem in one pass.

## Consequences

- Diagnostics, completion and printing share one parser and one tree.
- No generated code or external grammar toolchain to maintain.
- Grammar changes are code changes, covered by the corpus, round-trip and fuzz
  tests.
