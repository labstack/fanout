# telemetry-ingestion Specification

## Purpose

Defines how Fanout accepts, authenticates, normalizes, namespaces, and safely queues OpenTelemetry signals before durable persistence.

## Requirements

### Requirement: Fanout accepts all three OTLP signal families over gRPC
Fanout SHALL implement the OTLP/gRPC export services for traces, logs, and metrics. It SHALL NOT claim native OTLP/HTTP support.

#### Scenario: Collector exports supported signals
- **WHEN** a collector sends trace, log, or metric export requests to the configured gRPC listener
- **THEN** Fanout accepts the corresponding OTLP service request and converts supported records into its ingest rows

### Requirement: Ingest credentials are independently authenticated
Fanout MUST authenticate OTLP requests with the current ingest token supplied as `x-fanout-ingest-token` or `Authorization: Bearer`. It MUST store only the token hash, reject ingest before a token exists, and MUST NOT accept browser-session or MCP OAuth credentials as ingest credentials.

#### Scenario: Collector presents the generated token
- **WHEN** a collector sends the current `fo_` token in either supported header form
- **THEN** the export is authorized for every supported signal type

#### Scenario: Collector presents a browser or MCP credential
- **WHEN** an OTLP request presents a browser cookie or MCP access token instead of the ingest token
- **THEN** Fanout rejects the request as unauthenticated

### Requirement: Ingest tokens have an explicit lifecycle
Fanout SHALL generate the first ingest token during first-admin setup, reveal the raw token once, and allow an administrator to rotate it atomically with an audit event.

#### Scenario: Administrator rotates the token
- **WHEN** an administrator requests ingest-token rotation
- **THEN** Fanout persists the new hash and its audit record together
- **AND** the previous raw token no longer authorizes exports

### Requirement: Public ingest is an isolated demo override
Fanout SHALL bypass OTLP authentication only when `PUBLIC_INGEST=true`. This setting MUST NOT grant browser, API, agent, or MCP access.

#### Scenario: Disposable demo enables public ingest
- **WHEN** `PUBLIC_INGEST=true` and an exporter sends no token
- **THEN** Fanout accepts the OTLP request
- **AND** protected query and management routes retain their own access policies

### Requirement: Missing namespaces receive the configured default
Fanout SHALL read `service.namespace` from each OTLP resource and use `DEFAULT_NAMESPACE` when the attribute is absent or empty.

#### Scenario: SDK omits service namespace
- **WHEN** a trace, log, or metric resource has no `service.namespace`
- **THEN** Fanout stores the record in the configured default namespace

### Requirement: Core OpenTelemetry context is preserved
Fanout SHALL preserve signal timestamps, service identity, resource attributes, instrumentation scope, signal attributes, and trace/log correlation identifiers. It SHALL extract commonly queried semantic attributes without discarding the original attribute payload.

#### Scenario: Span contains HTTP and exception context
- **WHEN** a span includes HTTP semantic attributes and an exception event
- **THEN** Fanout stores queryable HTTP and exception fields
- **AND** retains the original resource, attributes, events, and links

### Requirement: Ingest applies bounded backpressure
Fanout SHALL queue converted records through bounded signal channels and block an exporter while a channel is full rather than silently dropping accepted rows. Cancellation MAY end a partially queued OTLP batch.

#### Scenario: Storage falls behind ingest
- **WHEN** an ingest channel reaches capacity
- **THEN** the export handler waits for capacity or request cancellation
- **AND** does not acknowledge records that were never queued

### Requirement: Direct TLS is complete or absent
Fanout SHALL enable TLS for both HTTP and OTLP listeners only when both certificate and key are configured, and direct OTLP TLS SHALL require TLS 1.3 with HTTP/2.

#### Scenario: Operator configures only a certificate
- **WHEN** exactly one of `TLS_CERT_FILE` and `TLS_KEY_FILE` is set
- **THEN** Fanout rejects the configuration at startup
