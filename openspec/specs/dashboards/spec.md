# dashboards Specification

## Purpose

Defines durable, owner-scoped dashboard collections whose validated widgets compose the same observability kernel used throughout Fanout.

## Requirements

### Requirement: Dashboards are named and owner-scoped
Fanout SHALL persist multiple named dashboards per authenticated owner and MUST resolve every list, read, update, and delete against the verified owner rather than an owner supplied in the document.

#### Scenario: User requests another owner's dashboard identifier
- **WHEN** the identifier is not owned by the current principal
- **THEN** Fanout returns not found and does not expose the dashboard

### Requirement: Each owner receives an initial dashboard
Fanout SHALL create a default System overview dashboard the first time an owner accesses an empty collection. If a valid legacy single-canvas state exists, Fanout SHALL migrate it into that initial dashboard.

#### Scenario: Existing user opens dashboards after the collection upgrade
- **WHEN** the user has legacy dashboard state but no named dashboards
- **THEN** Fanout creates the initial named dashboard from the valid legacy state

### Requirement: Dashboard documents are validated atomically
Fanout SHALL validate the name, shared filters, widgets, per-widget configuration, layout bounds, identifiers, and document size before replacing a dashboard. Unknown request fields and invalid widget documents MUST be rejected without a partial update.

#### Scenario: Client submits an invalid layout
- **WHEN** a widget is missing a valid matching layout entry or exceeds the layout contract
- **THEN** Fanout rejects the document and retains the previous dashboard unchanged

### Requirement: Dashboard filters are shared
Each dashboard SHALL persist a positive time window and optional namespace that scope every enabled widget unless the widget contract explicitly provides a narrower supported filter.

#### Scenario: User changes the dashboard namespace
- **WHEN** the dashboard is saved with a different namespace
- **THEN** its widgets query the new namespace through the shared observability contract

### Requirement: Browser layouts are durable and editable
The browser SHALL render dashboard widgets on a draggable twelve-column canvas and persist validated layout changes, widget enablement, titles, and configurations in the owner-scoped dashboard.

#### Scenario: User rearranges widgets
- **WHEN** the user saves a new valid layout
- **THEN** the same arrangement is restored when that named dashboard is reopened

### Requirement: Dashboard deletion requires explicit confirmation
The REST API MUST require the dashboard identifier in `X-Fanout-Confirm-Delete` before deleting a named dashboard.

#### Scenario: Client sends an unconfirmed delete
- **WHEN** the confirmation header is absent or does not match the route identifier
- **THEN** Fanout rejects the request without deleting the dashboard

### Requirement: Legacy default-dashboard endpoints remain compatible
Fanout SHALL retain the singular `/api/dashboard` read and replace operations as aliases for the current owner's default dashboard.

#### Scenario: Existing client saves the singular dashboard
- **WHEN** it sends a valid state to the compatibility endpoint
- **THEN** Fanout replaces the state of the owner's default named dashboard

### Requirement: MCP can manage complete dashboard designs
Fanout SHALL expose owner-derived MCP tools to list and get dashboards, create a complete named dashboard, and explicitly replace a selected dashboard design. Replacement MUST be annotated as destructive to MCP clients.

#### Scenario: OAuth-connected client creates a dashboard
- **WHEN** an authorized client calls `dashboard_create` with a valid complete design
- **THEN** the dashboard appears in the same owner collection used by the browser
