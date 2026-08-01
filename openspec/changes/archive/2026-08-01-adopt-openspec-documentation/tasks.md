## 1. OpenSpec foundation

- [x] 1.1 Initialize OpenSpec 1.7.0 for Codex, Claude Code, and GitHub Copilot. Evidence: generated integrations under `.codex/`, `.claude/`, and `.github/` from `openspec init`.
- [x] 1.2 Add Fanout project context, artifact rules, and apply/archive guidance. Evidence: `openspec/config.yaml`.

## 2. Canonical baseline

- [x] 2.1 Map shipped runtime behavior and documentation conflicts to authoritative sources. Evidence: `design.md` source map grounded in `cmd/fanout/main.go`, `internal/`, `ui/host/src/routes`, `Dockerfile`, and `justfile`.
- [x] 2.2 Create the nine capability deltas and validate the change strictly. Evidence: `specs/*/spec.md`; `openspec validate adopt-openspec-documentation --strict --no-interactive` passed.

## 3. Documentation consolidation

- [x] 3.1 Consolidate shipped contracts from the former requirements, visions, and designs without retaining legacy copies. Evidence: nine capability deltas and the conflict map in `design.md`.
- [x] 3.2 Delete superseded plans, visions, aggregate requirements, references, and generated documentation prototypes after mapping them to canonical capabilities. Evidence: active `docs/` now contains only `README.md`, and no `legacy/` directory remains in the change.
- [x] 3.3 Add a concise `docs/README.md` that defines source-of-truth ownership, the capability map, and the OpenSpec lifecycle. Evidence: `docs/README.md`.

## 4. Current documentation

- [x] 4.1 Update the root README, repository guidance, and agent guidance to the current `ui/host`, `ui/apps`, OpenSpec, build, and test structure. Evidence: `README.md`, `AGENTS.md`, and `CLAUDE.md`.
- [x] 4.2 Rewrite the public installation and operations guide against current configuration validation, routes, credentials, UI, MCP tools, alerts, storage, and deployment behavior. Evidence: `site/src/pages/docs/index.mdx` and the synchronized quick start in `README.md`.

## 5. Verification and archive

- [x] 5.1 Add pinned `just docs-check` validation and run it in CI when OpenSpec or documentation integrations change. Evidence: OpenSpec 1.7.0 is pinned in `justfile`, and `.github/workflows/ci.yml` runs `just docs-check` explicitly.
- [x] 5.2 Validate all OpenSpec artifacts strictly, build the public site, check internal links and stale terminology, and review the final diff without modifying unrelated worktree files. Evidence: `just docs-check`, `cd site && bun run build`, the local Markdown link scan, `git diff --check`, and stale-path scans passed.
