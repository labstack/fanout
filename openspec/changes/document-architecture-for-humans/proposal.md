# Document the architecture for humans

## Why

A person evaluating this repository has nowhere to learn how Fanout is put
together. `docs/architecture.svg` was deleted when OpenSpec was adopted
(`b0bd10e`) and nothing replaced it, so `docs/` now holds only an index. The
only accurate architecture description left is in `CLAUDE.md`, whose first line
addresses coding agents — a human reader has no reason to open it, and if they
do, it reads as written for someone else.

This is structural rather than an oversight. OpenSpec deliberately has no home
for standing high-level design: `openspec/specs/` carries observable behavior
only, and `design.md` exists inside a change and becomes history when that
change archives. The taxonomy answers *what must be true* and *why we decided
something once*, but not *how the system is put together right now*, which is
the first thing a newcomer needs.

## What Changes

- Add `docs/architecture.md`: a human-facing description of Fanout's runtime
  shape, request paths, persistence split, and repository layout. It links to
  `openspec/specs/` for behavioral contracts and does not restate them.
- Author its diagrams in d2 under `docs/diagrams/`, rendered to committed SVG
  so the document renders on GitHub without a local toolchain. d2 replaces the
  Mermaid used inline in `CLAUDE.md`.
- Add a `just diagrams` target that renders every `.d2` source to SVG, so the
  rendered output has a reproducible command rather than being hand-made.
- Verify every structural claim against the code before writing it, and record
  in the document how to re-verify it.
- Point `README.md`, `docs/README.md`, and `CLAUDE.md` at the new document.
  `CLAUDE.md` keeps its agent-specific working rules but stops carrying a
  second copy of the architecture diagram and layout table, so the two cannot
  drift apart.
- Correct known drift found while verifying: `CLAUDE.md`'s repository layout
  omits `internal/brand` and `cmd/bench`, and does not mention
  `internal/lake/writegate`.

Not breaking: no runtime behavior, public contract, data format, or security
surface changes.

## Capabilities

### New Capabilities

None. This change adds documentation and a render target; it introduces no
product capability.

### Modified Capabilities

None. No requirement changes, so `.openspec.yaml` sets `skip_specs: true`.
`openspec/specs/product-foundation/` already carries the normative runtime
boundaries this document describes informally, and stays the source of truth
for them.

## Impact

- **Added**: `docs/architecture.md`, `docs/diagrams/*.d2`, rendered
  `docs/diagrams/*.svg`, and a `diagrams` recipe in `justfile`.
- **Edited**: `README.md`, `docs/README.md`, `CLAUDE.md` (links, drift fixes,
  and removal of the duplicated architecture section).
- **Tooling**: d2 becomes an optional contributor dependency, needed only to
  re-render diagrams. It is not required to build, test, or run Fanout, and is
  not added to the Docker build or CI. Committed SVG keeps the document
  readable for everyone else.
- **Unaffected**: all Go and TypeScript source, the release and deploy
  workflows, and every canonical spec.
