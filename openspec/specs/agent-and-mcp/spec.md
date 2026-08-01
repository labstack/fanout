# agent-and-mcp Specification

## Purpose

Defines Fanout's streamed AG-UI investigation runtime, persistent conversation ownership, shared MCP tool registry, remote MCP transport, and portable rich results.

## Requirements

### Requirement: The agent streams standard AG-UI runs
Fanout SHALL accept AG-UI run input, assign missing thread and run identifiers, stream run, text, tool-call, activity, completion, and error events over server-sent events, and persist the final run outcome.

#### Scenario: Model produces text and a tool call
- **WHEN** an authenticated user starts an agent run
- **THEN** the browser receives incremental text and tool events before the run finishes
- **AND** the completed event history is stored with the thread

### Requirement: Conversation history is owner-scoped
Fanout SHALL persist threads, messages, runs, and streamed events in the control database and MUST scope list, search, read, rename, delete, and continuation operations to the authenticated owner.

#### Scenario: User guesses another thread identifier
- **WHEN** the user requests a thread they do not own
- **THEN** Fanout returns not found and discloses no thread content

### Requirement: Agent execution is bounded and failures are safe
Fanout SHALL bound model output and tool iterations, mark truncated output explicitly, emit a run error when the bound is exceeded, and prevent provider-internal error detail from leaking to the client.

#### Scenario: Model repeatedly requests tools
- **WHEN** the configured maximum tool steps is exhausted
- **THEN** Fanout ends the run with a safe error event and persists the failed outcome

### Requirement: Anthropic and OpenAI-compatible providers are supported
Fanout SHALL support `anthropic` and `openai` provider modes with configurable model and base URL, and SHALL reject startup without a provider API key or with an unsupported provider name.

#### Scenario: Operator configures an OpenAI-compatible endpoint
- **WHEN** `AI_PROVIDER=openai` and valid key, model, and optional base URL are configured
- **THEN** agent runs use that provider while retaining Fanout's AG-UI and tool contracts

### Requirement: The agent uses the standard MCP registry in process
The browser agent SHALL discover and execute the same MCP tools exposed to remote clients through an in-memory MCP connection. Fanout MUST NOT maintain a second agent-only tool implementation or make an HTTP self-call.

#### Scenario: An observability tool contract changes
- **WHEN** the registered MCP tool schema is updated
- **THEN** both the internal agent and remote MCP clients discover the updated schema from the same server registry

### Requirement: Fanout exposes a Streamable HTTP MCP server
When MCP is enabled, Fanout SHALL serve the standard MCP endpoint at `/mcp` with stateful Streamable HTTP sessions and a canonical HTTPS resource URL ending in `/mcp`.

#### Scenario: Remote client initializes MCP
- **WHEN** it connects to the configured public MCP resource with a valid audience-bound access token
- **THEN** Fanout exposes its fixed tool and resource catalog for that session

### Requirement: The observability MCP tool set is stable and read-only
Fanout SHALL expose `observability_overview`, `service_topology`, `service_performance`, `trace_detail`, and `search_logs` as read-only, closed-world tools with bounded namespace and window inputs.

#### Scenario: MCP client lists tools
- **WHEN** tool discovery completes
- **THEN** the five observability tools are annotated read-only and do not mutate telemetry or control state

### Requirement: Dashboard tools use verified ownership
Fanout SHALL expose `dashboard_list`, `dashboard_get`, `dashboard_create`, and `dashboard_update` only with dashboard scope, derive the owner from the verified MCP identity, and annotate replacement as destructive.

#### Scenario: Client supplies a dashboard document
- **WHEN** it calls a dashboard tool with valid authorization
- **THEN** Fanout reads or writes only the authenticated owner's dashboard collection

### Requirement: MCP Apps metadata is capability-negotiated
Fanout SHALL advertise optional UI resource metadata only to clients that negotiate the MCP Apps HTML profile. Tool schemas, summary text, and structured results MUST remain available to every compatible MCP client.

#### Scenario: Text-only MCP client lists tools
- **WHEN** it does not advertise MCP Apps support
- **THEN** Fanout omits UI metadata without omitting or changing the tools
