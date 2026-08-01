## Purpose

Defines Fanout's persisted expression rules, per-service alert state machine, test surface, delivery behavior, retention, and role-based management boundaries.

## ADDED Requirements

### Requirement: Alert rules are durable and validated
Fanout SHALL persist named alert rules with enablement, service scope, optional namespace metadata, expression, hold duration, cooldown, repeat interval, webhook configuration, and resolve-notification preference. A rule expression MUST compile before creation or activation. The current evaluator does not apply the stored namespace selector.

#### Scenario: Operator submits an invalid expression
- **WHEN** the expression cannot be compiled safely
- **THEN** Fanout rejects the rule without adding it to the evaluation engine

### Requirement: Rules evaluate against bounded service evidence
The alert engine SHALL evaluate enabled rules at the configured interval against per-service telemetry evidence including traffic, error rate, latency, log count, anomaly score, health score, and baseline deltas.

#### Scenario: Rule targets all services
- **WHEN** a wildcard rule is evaluated
- **THEN** Fanout evaluates it independently for each service using instance-wide rollup evidence grouped by service name

### Requirement: Alerts follow pending, firing, and resolved states
Fanout SHALL create a pending alert when a condition first becomes true, transition it to firing only after `for_seconds` has elapsed, and mark it resolved when the condition becomes false. Zero hold duration SHALL allow immediate firing.

#### Scenario: Short spike clears before the hold duration
- **WHEN** an expression becomes true and then false before `for_seconds` elapses
- **THEN** the alert does not transition to firing

### Requirement: Repeat and cooldown controls limit notification noise
Fanout SHALL honor a rule's cooldown and repeat interval when deciding whether an already firing condition produces another delivery.

#### Scenario: Firing rule remains true
- **WHEN** the repeat interval has not elapsed
- **THEN** Fanout retains the firing state without sending a duplicate webhook

### Requirement: Webhook delivery is observable and configurable
Fanout SHALL deliver firing notifications to the configured webhook with supported custom headers and optional payload template, record the last delivery status and time, and optionally notify when an alert resolves.

#### Scenario: Downstream webhook rejects a notification
- **WHEN** the webhook returns a failing response or cannot be reached
- **THEN** Fanout records the failed delivery status without corrupting the alert state

### Requirement: Rules can be dry-run against live evidence
An authorized operator SHALL be able to test a persisted rule without changing its alert state. The result SHALL report, per resolved service, whether the expression triggered, the evaluation environment, and any safe evaluation error.

#### Scenario: Operator tests a wildcard rule
- **WHEN** the test endpoint is called
- **THEN** Fanout returns one independent result for each currently resolved service

### Requirement: Alert history is queryable and pruned
Fanout SHALL provide filterable alert history and pending/firing/resolved counts, and SHALL prune resolved records older than `ALERT_HISTORY_DAYS`.

#### Scenario: Client filters firing alerts for a service
- **WHEN** it supplies state and service filters
- **THEN** Fanout returns only matching persisted alert instances

### Requirement: Alert access follows role capabilities
Telemetry readers SHALL be able to read rules and alert state. Only operators and administrators SHALL be able to create, replace, delete, or test rules.

#### Scenario: Viewer attempts to create a rule
- **WHEN** a viewer sends a rule mutation
- **THEN** Fanout denies the request

### Requirement: Disabled alerting has explicit API behavior
When alerting is disabled, read endpoints SHALL return empty rule and alert state while mutation and test endpoints SHALL report that alerting is unavailable.

#### Scenario: Instance starts with `ALERT_ENABLED=false`
- **WHEN** a telemetry reader requests the alert summary
- **THEN** Fanout returns zero counts rather than failing the entire API
