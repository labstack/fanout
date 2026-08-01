## Purpose

Defines the five deterministic investigation domains and the portable typed result contract shared by Fanout's HTTP, browser, MCP, dashboard, and agent consumers.

## ADDED Requirements

### Requirement: Every investigation returns a typed result envelope
Fanout SHALL return a schema identifier, concise textual summary, structured domain data, and provenance for every deterministic observability result.

#### Scenario: Host cannot render a rich app
- **WHEN** an MCP client calls an observability tool without MCP Apps support
- **THEN** it receives useful summary text and structured content with the same domain schema

### Requirement: Overview summarizes health across services
The overview domain SHALL report bounded per-service traffic, error rate, latency, log and metric counts, health classification, and aggregate service health for the selected scope.

#### Scenario: Operator starts incident triage
- **WHEN** the operator requests system health for a namespace and window
- **THEN** services are summarized and ordered so unhealthy, degraded, or high-impact services can be identified

### Requirement: Topology represents observed dependencies
The topology domain SHALL return service nodes and observed caller-to-callee edges with edge type, traffic, latency, and error information for the selected scope.

#### Scenario: Services call each other in the selected window
- **WHEN** the operator opens the service map
- **THEN** Fanout returns the observed dependency edges and the health of their service nodes

### Requirement: Performance supports service and system analysis
The performance domain SHALL provide time-series activity, latency and error information, endpoint aggregates, a latency heatmap, and a comparison between the first and second halves of the selected window. It SHALL work for one service or all services.

#### Scenario: Operator selects a service
- **WHEN** a performance request names that service
- **THEN** Fanout scopes its activity and endpoint analysis to that service while retaining the selected namespace and window

### Requirement: Trace detail supports exact and relevant selection
The trace domain SHALL inspect an explicitly requested trace identifier or, when no identifier is supplied, select a relevant recent error or slow trace within the requested scope. It SHALL return spans suitable for waterfall and flame views plus correlated logs.

#### Scenario: Operator follows a trace identifier from a log
- **WHEN** the trace request includes that identifier
- **THEN** Fanout returns that trace's ordered span detail and correlated log context

### Requirement: Log exploration is filterable and correlation-aware
The logs domain SHALL return reverse-chronological entries and a severity timeline, filtered by optional service, severity, and case-insensitive body search, with trace and span identifiers when present.

#### Scenario: Operator searches for an error fragment
- **WHEN** the operator supplies service, severity, or text filters
- **THEN** both returned entries and histogram counts use the same filters

### Requirement: Sensitive-looking log values are redacted consistently
Fanout MUST redact supported secret patterns before displaying log bodies and MUST apply free-text search to the redacted representation so search cannot be used as a secret-presence oracle.

#### Scenario: Public reader probes a candidate secret
- **WHEN** a stored log contains a redacted credential and the reader searches for its raw value
- **THEN** neither the entries nor histogram reveal whether the candidate appeared in the unredacted body

### Requirement: Rich results are portable MCP Apps
Fanout SHALL publish one optional MCP App resource for each of overview, topology, performance, trace, and logs, while preserving non-UI fallbacks for clients that do not negotiate the MCP Apps profile.

#### Scenario: MCP client negotiates MCP Apps
- **WHEN** the client lists and calls an observability tool with supported UI capabilities
- **THEN** Fanout advertises the matching app resource and returns the data needed to render it
