# Alerts Page — Vision & Approach

## The Job

The Alerts page answers: **what's firing, what rules exist, and are webhooks working?**

Deterministic CRUD UI for alert management. No LLM calls. The AI chat can create rules via MCP tools ("set up an alert for checkout error rate above 5%"), and this page is where you see and manage them.

## Principles

1. **Deterministic** — forms, tables, toggles. No AI in the loop for CRUD.
2. **Operational confidence** — show webhook delivery status so you know notifications are reaching you.
3. **Test before deploy** — dry-run expressions against live data before enabling a rule.
4. **Consistent with Home/Service** — same nav, same visual patterns, same monk-aligned style.

## Route

```
/alerts
```

Add "Alerts" to the nav between Home and Investigate.

## API Endpoints

New REST endpoints wrapping the existing `alert.Store` and `alert.Engine` methods:

| Method | Path | Store Method | Purpose |
|--------|------|-------------|---------|
| GET | `/api/alerts` | `ListAlerts(state, service, ruleID)` | List alerts with optional filters |
| GET | `/api/alerts/summary` | `AlertSummary()` | Firing/pending/resolved counts |
| GET | `/api/alert-rules` | `ListRules()` | List all rules |
| POST | `/api/alert-rules` | `CreateRule(rule)` | Create a rule |
| PUT | `/api/alert-rules/:id` | `UpdateRule(rule)` | Update a rule |
| DELETE | `/api/alert-rules/:id` | `DeleteRule(id)` | Delete a rule |
| POST | `/api/alert-rules/:id/test` | `Engine.BuildEnvForService()` + `SafeEval()` | Test expression against live data |
| POST | `/api/alert-rules/:id/test-webhook` | `FireWebhook()` | Send a test webhook |

All methods already exist on `alert.Store` and `alert.Engine`. The handlers are thin wrappers.

## Page Layout

Top to bottom, vertical stack (same as Service Detail):

### 1. Header

"Alerts" title, firing/pending/resolved counts as badges, "Create Rule" button (primary).

### 2. Firing Alerts

Cards for each firing alert, sorted by fired_at (newest first):
- Service name, rule name, value snapshot, fired at, duration
- Webhook delivery status badge: success (green), failed (red), skipped (gray)
- Last delivery timestamp
- "Investigate" button → opens chat with alert context

If no alerts firing: green banner "No alerts firing."

### 3. Alert Rules

Table with columns: Enabled (toggle), Name, Expression (mono, truncated), Service, Webhook (truncated URL or "none"), Last Triggered, Actions (edit/delete).

Click a rule → expands inline to show full details + edit form.

Toggle enabled/disabled with a switch — calls `UpdateRule` with `enabled: true/false`.

### 4. Create Rule — AI-Assisted

The primary way to create a rule is natural language. A text input at the top of the rules section:

```
"Alert me if checkout error rate goes above 5% for 2 minutes, notify Slack"
```

This sends the prompt to the AI chat endpoint, which uses the existing `alert_rules create` MCP tool to generate a complete rule: expression, service filter, for_seconds, webhook URL. The AI returns the proposed rule as a preview card. The user reviews and clicks "Save" or "Edit" to tweak.

**Why AI-first:** Nobody should have to learn expr-lang syntax to set up an alert. The AI already knows the available fields, the expression syntax, and the webhook format. It can infer "2 minutes" → `for_seconds: 120` and "Slack" → webhook URL if configured.

**Manual form fallback:** An "Advanced" toggle reveals the full form for power users who want to write expressions directly:

| Field | Type | Notes |
|-------|------|-------|
| Name | text | Required |
| Expression | text (mono) | Required |
| Service | text | `*` for all, or specific service name |
| For (seconds) | number | 0 = fire immediately |
| Cooldown (seconds) | number | Prevent re-fire after resolve |
| Repeat interval (seconds) | number | 0 = no repeat |
| Webhook URL | text | Optional |
| Webhook headers | text (mono) | JSON object, optional |
| Webhook template | textarea (mono) | Go template, optional |
| Notify on resolve | toggle | Send webhook when alert clears |

**Test button**: Dry-runs the expression against live data. Shows which services would trigger and current metric values.

### Editing Rules

Click "Edit" on any rule → opens the manual form pre-filled with current values. Or describe the change in natural language: "change the threshold to 10%" and the AI updates the expression.

### 5. Recent Alert History

Collapsed by default. Shows recently resolved alerts with duration and resolution time. Useful for "what fired overnight?" context.

## Data Flow

```
Browser                         Server
  │                               │
  │  GET /api/alerts?state=firing │
  │  GET /api/alert-rules         │
  │ ────────────────────────────→ │
  │                               │  store.ListAlerts()
  │                               │  store.ListRules()
  │  ← JSON ─────────────────── │
  │                               │
  │  POST /api/alert-rules        │
  │ ────────────────────────────→ │
  │                               │  store.CreateRule()
  │                               │  engine.RecompileRule()
  │  ← 201 Created ───────────  │
```

## Backend Changes

1. **New handler file** — `internal/api/alerts.go` with all alert REST endpoints.
2. **Wire into RegisterUIRoutes** — needs access to `alert.Engine` (not just `alert.Store`) for test/compile functionality.
3. **Update main.go** — pass `alertEngine` to `RegisterUIRoutes`.

No new store methods needed. Everything wraps existing `alert.Store` and `alert.Engine` methods.

## Frontend Components

| Component | Responsibility |
|-----------|---------------|
| `AlertsPage.tsx` | Page shell: fetch alerts + rules, layout sections |
| `FiringAlerts.tsx` | Cards for firing alerts with delivery status |
| `AlertRulesTable.tsx` | Table of rules with inline expand/edit |
| `RuleForm.tsx` | Create/edit form with test button |
| `ExpressionHelp.tsx` | Available fields + example expressions |
| `AlertHistory.tsx` | Recently resolved alerts (collapsed) |

## Navigation

- **Nav**: Add "Alerts" link between Home and Investigate. Show firing count badge if > 0.
- **Home → Alerts**: Clicking the firing alerts footer on Home navigates to `/alerts`.
- **Alerts → Investigate**: "Investigate" button on firing alert opens chat with context.
- **Alerts → Service Detail**: Clicking service name navigates to `/service/:name`.

## What Alerts Page Is Not

- **Not AI-generated** — all CRUD is deterministic forms and tables
- **Not a log viewer** — webhook delivery shows status, not full response bodies
- **Not a dashboard** — no charts on this page, just operational state

## Build Scope

Everything above ships as one unit. The page is straightforward since the data layer is complete.
