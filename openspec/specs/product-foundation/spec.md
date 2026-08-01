# product-foundation Specification

## Purpose

Defines Fanout's shipped product promise, supported telemetry scope, runtime boundaries, and primary interaction model so every surface describes the same system.

## Requirements

### Requirement: Fanout provides self-hosted telemetry investigation
Fanout SHALL let an operator ingest, retain, query, and investigate OpenTelemetry traces, logs, and metrics on infrastructure they control.

#### Scenario: Operator deploys Fanout
- **WHEN** an operator starts a configured Fanout instance and sends supported OTLP signals
- **THEN** the operator can investigate those signals without a hosted Fanout control plane

### Requirement: One runtime owns the complete product path
The production artifact SHALL run OTLP ingest, telemetry storage and queries, application state, alerts, authentication, the AG-UI agent, HTTP APIs, MCP, and the embedded browser assets in one Go process.

#### Scenario: Production binary starts
- **WHEN** Fanout starts successfully
- **THEN** no Node, Bun, browser build server, agent sidecar, or separate database service is required at runtime

### Requirement: Deterministic telemetry semantics are shared
Fanout SHALL use one typed query contract for ordinary browser navigation, dashboards, MCP tools, and agent tool execution.

#### Scenario: Same investigation is requested through two surfaces
- **WHEN** a browser consumer and an MCP consumer request the same domain with the same namespace and time window
- **THEN** both receive the same schema and telemetry semantics

### Requirement: The browser focuses on chat and dashboards
The shipped browser client SHALL provide persisted investigation chat and named dashboards as its primary authenticated routes.

#### Scenario: User opens the root route
- **WHEN** an authenticated user opens Fanout without a specific route
- **THEN** Fanout opens a new or migrated chat thread
- **AND** the user can navigate between chat investigations and dashboards

### Requirement: Namespaces scope telemetry rather than identity
Fanout SHALL use `service.namespace` to partition query and display scope, with a configured default for signals that omit it. A namespace MUST NOT be represented as a user, organization, or authorization boundary.

#### Scenario: Two products share an instance
- **WHEN** their telemetry carries distinct `service.namespace` resource attributes
- **THEN** investigations can select either namespace independently
- **AND** access remains governed by the signed-in user's role rather than namespace membership

### Requirement: Agent assistance remains optional to deterministic navigation
Fanout SHALL expose ordinary telemetry reads without requiring a model call, while allowing the agent to invoke those same reads for guided investigation.

#### Scenario: Model provider is slow during an incident
- **WHEN** an authenticated client calls a deterministic observability endpoint directly
- **THEN** the telemetry result does not depend on completion of an agent run
