## 1. Verify the architecture against the code

- [x] 1.1 Confirm the runtime composition claims: one process owns OTLP gRPC ingest, HTTP/AG-UI/MCP, and the embedded browser assets. Evidence: `cmd/fanout/main.go`, `internal/ingest/server.go`, `internal/env/config.go` (`HTTP_ADDR`, `OTLP_GRPC_ADDR`).
- [x] 1.2 Confirm the agent reaches MCP over an in-memory transport with no HTTP self-call. Evidence: `internal/agent/tools.go` (`mcp.NewInMemoryTransports`).
- [x] 1.3 Confirm the MCP tool set is exactly the five typed observability tools plus four dashboard tools. Evidence: registrations in `internal/mcp/`.
- [x] 1.4 Confirm the persistence split: DuckLake/Parquet telemetry, DuckDB query state, control SQLite, and that all catalog writes serialize through one gate. Evidence: `internal/env/config.go` (`telemetry`/`query`/`control`), `internal/db/schema.sql`, `internal/lake/writegate/write_gate.go`.
- [x] 1.5 Confirm the HTTP surface from the registered routes rather than from prose, excluding test-only fixtures such as `/api/new-unreviewed-route` in `internal/api/auth_test.go`.
- [x] 1.6 Record the drift found against `CLAUDE.md`: missing `internal/brand`, missing `cmd/bench`, missing `internal/lake/writegate`.

## 2. Author the diagrams

- [x] 2.1 Write `docs/diagrams/architecture.d2`: the request paths through the single process, with the process boundary as a container.
- [x] 2.2 Write `docs/diagrams/persistence.d2`: the three data stores, what each owns, and the shared catalog write gate.
- [x] 2.3 Add a `diagrams` recipe to `justfile` that renders every `docs/diagrams/*.d2` to SVG beside its source, and keep it out of `build` and `check`.
- [x] 2.4 Render both diagrams with `just diagrams` and commit the SVG alongside the source.

## 3. Write the document

- [x] 3.1 Write `docs/architecture.md` covering the runtime shape, the request paths, the persistence split, and the repository layout, embedding the rendered SVG.
- [x] 3.2 Link each behavioral topic to its capability spec under `openspec/specs/` instead of describing the behavior. Verify no requirement is restated. Review caught a first pass that restated the `fanout:dashboard` scope, tool-call ownership, and session rules; the Interfaces section now points at `agent-and-mcp` and `identity-and-access` rather than enumerating them.
- [x] 3.3 Record how to re-verify the structural claims, so the document can be checked rather than trusted.

## 4. Make it discoverable and single-owned

- [x] 4.1 Link the document from `README.md` and from the table in `docs/README.md`.
- [x] 4.2 Replace the architecture diagram and repository-layout table in `CLAUDE.md` with a link, keeping its agent-specific working rules. Review found the first pass left the persistence split, UI package naming, and the auth paragraph duplicated while the new text claimed `docs/architecture.md` solely owned them; those now link too.
- [x] 4.3 Fix the drift from 1.6 wherever the layout survives, including the inherited `internal/lake` entry that credited it with maintenance code living in `internal/query`.
- [x] 4.4 Point `AGENTS.md` at the architecture document. It is a third copy of the layout and the file non-Claude agents read.

## 5. Verify

- [x] 5.1 Confirm every internal link resolves and every referenced path exists.
- [x] 5.2 Confirm the committed SVG matches its `.d2` source by re-running `just diagrams` and checking the tree is clean.
- [x] 5.3 Run `just docs-check` and `just check`.
