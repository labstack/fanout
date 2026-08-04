# Design

## Context

See `proposal.md` — Why. Three constraints shape the approach.

`docs/README.md` already sets the rule this document must satisfy: material in
`docs/` links to the behavioral specs and does not redefine them. An
architecture overview complies because it describes *structure* — which process
owns what, which package talks to which — while every behavioral claim defers
to `openspec/specs/`.

`CLAUDE.md` currently holds an accurate architecture section, so this is mostly
relocation rather than authorship. It has already drifted: the layout table
omits `internal/brand` and `cmd/bench`, and `internal/lake/writegate` (merged in
`65b2efa4`) is absent. That drift is the argument against leaving a second copy
behind after the move.

The repository has no `.d2` files and no diagram render step today. Mermaid is
used inline in `CLAUDE.md` and `docs/README.md`; GitHub renders it natively.
Choosing d2 gives up that native rendering and must pay for it.

## Goals / Non-Goals

**Goals:**

- One current, codebase-verified description of how Fanout is assembled, at a
  path a newcomer will actually find.
- Diagrams with a reproducible render command, not hand-maintained images.
- Exactly one owner per fact, so the architecture cannot drift between files
  the way it already has.

**Non-Goals:**

- Restating behavior that `openspec/specs/` already defines normatively.
- Package-level or API reference documentation — the code and typed contracts
  own that.
- Converting the existing Mermaid lifecycle diagram in `docs/README.md`. It is
  small, it renders natively, and churning it buys nothing.
- Adding d2 to CI, the Dockerfile, or any required developer path.

## Decisions

**Put it at `docs/architecture.md`, not in a spec.**
`openspec/specs/` is observable behavior only, by config rule; an architecture
diagram is not a requirement and would violate that. A change's `design.md`
archives into history when the change lands, so it cannot hold standing design.
`docs/` is the only location whose stated rule this fits. Alternative
considered: a new `architecture` capability under `openspec/specs/`. Rejected —
it would invent requirements to justify a document, which the OpenSpec config
explicitly warns against, and `product-foundation` already carries the runtime
boundaries normatively.

**Author in d2, commit the rendered SVG.**
The user asked for d2, and it handles the layered container shapes this
architecture needs better than Mermaid. The cost is that GitHub renders Mermaid
natively and d2 not at all, so an unrendered `.d2` file would leave the document
blank for most readers. Committing SVG alongside source resolves that: readers
need nothing installed, contributors need d2 only to change a diagram.
Alternative considered: keep Mermaid. Rejected on the explicit request, and
because d2's containers express the one-process boundary — the central claim of
this architecture — more directly. Alternative considered: render in CI.
Rejected as disproportionate for a file that changes rarely, and it would make
d2 a required build dependency to avoid a broken doc.

**Verify every structural claim against the code before writing it.**
The document's value is being true, and the file it replaces drifted precisely
because nothing checked it. Each claim is confirmed by grep or build output —
the nine MCP tools from their registrations, the in-memory agent transport from
`internal/agent/tools.go`, the HTTP surface from the route table, the data
directories from `internal/env/config.go`. The document records the commands so
a reader can re-run them. Alternative considered: copy `CLAUDE.md` verbatim.
Rejected — it is already wrong in three places, and copying would propagate that.

**Move the architecture out of `CLAUDE.md`, leave a link.**
Two copies drift; that is the observed failure, not a hypothetical one.
`CLAUDE.md` keeps what is genuinely agent-specific — verification commands,
working rules, the documentation-source-of-truth pointer — and links for
structure. Alternative considered: keep both and accept duplication. Rejected
for the reason above.

**`just diagrams` renders all sources.**
One recipe globbing `docs/diagrams/*.d2` means adding a diagram needs no
justfile edit, and the render is reproducible rather than a remembered
invocation. It stays out of `just build` and `just check` so no one without d2
is blocked.

## Risks / Trade-offs

- **Rendered SVG goes stale against its `.d2` source** → `just diagrams` is
  deterministic, so a reviewer can re-run it and diff. Both files sit in the
  same directory and change in the same commit.
- **The document drifts from the code, exactly as `CLAUDE.md` did** → Keep it
  structural. Component relationships and the repository layout change far more
  slowly than behavior, and behavior is deferred to the specs. Recording the
  verification commands makes checking it cheap.
- **d2 as a contributor dependency excludes someone without it** → It is needed
  only to re-render, never to build, test, run, or read. The committed SVG
  serves every other reader.
- **Documenting structure invites restating behavior** → Explicit non-goal, and
  each behavioral topic links to its capability spec instead of describing it.

## Migration Plan

Additive. New files plus link and drift edits to three existing documents. No
runtime, build, or deployment change; nothing to roll back beyond reverting the
commit. `just docs-check` and `just check` must pass, as for any change.
