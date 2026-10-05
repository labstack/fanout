# Agent-built dashboards, Milestone 1 browser verification — 2026-10-05

This records a browser verification of Milestone 1 against a local Fanout instance on replayed demo data. It measures; it does not fix. Every value below was read from a real Chromium session driven through Playwright, the HTTP API (`curl` with the admin session cookie) and the control database (read-only).

## Setup

- **Build.** Branch `feat/agent-dashboards`, commit `c3353cce`, one local instance at `http://127.0.0.1:7520`.
- **Data.** Twenty-four hours of fanout-demo telemetry (the OpenTelemetry Demo services: spans, logs, metrics), time-shifted so it ends near the replay time. The newest data point is 2026-10-05T17:00:25Z, and the replay finished at about 17:25Z. The last 25 or more minutes therefore have no telemetry. That is a property of the setup, not a defect. The "Last hour" views show roughly 44 minutes of data, and rate-style stats are computed over that window.
- **Severity data.** The logs hold only `INFO` (12,624 in the last 24 hours at first check) and `UNSPECIFIED` (3,394) severities. There are no `ERROR` or `FATAL` logs.
- **Model.** The chat pane ran `claude-haiku-4-5`, the model in the local `.env` at the time. The model comparison below replaced it.
- **Browser.** Playwright Chromium, 1440 by 900 viewport, session cookie injected into the browser context.
- **Timing method.** Agent timings poll `GET /api/dashboards` every 2 seconds from the moment the prompt is sent, so they have 2-second resolution. Render timings use `performance.now()` from navigation start, polled on every animation frame.

## Success criteria

| Criterion | Target | Measured | Verdict |
|---|---|---|---|
| S1 Benchmark prompts produce a saved dashboard | 10 of 10 | Verbatim prompts: 2 of 5 (prompts 4 and 10). Prompts 1, 2 and 6 were answered in text with no dashboard. Re-run with a "Build me a dashboard" prefix: 3 of 3 saved. Prompts 3, 5, 7, 8 and 9 not run. | Not met on verbatim prompts |
| S2 Panels with rows or an explained empty state at save | 100% | 100% on the 5 dashboards run: 24 of 24 panels `ok`, 0 empty without a reason, 0 error. | Met on this sample |
| S3 Specs failing validation after the agent's own correction loop | 0 | 0 of 5 saved dashboards (the agent corrected an over-strict error filter in prompt 10 before saving). | Met on this sample |
| S4 Median prompt to saved dashboard | at most 45 s | 16 s median over 5 saved dashboards (12, 14, 16, 28, 32 s; maximum 32 s). | Met |
| S5 Follow-up edits change only the panels they name | 100% over 5 edits | 1 of 1 edit. Version 2 differs from version 1 by one added panel and nothing else. Five consecutive edits were not run. | Met on 1 edit |
| S6 First full render of a 12-panel, 24-hour dashboard | at most 1.5 s p95 | 397 ms p95 (361, 368, 371, 376, 382, 397 ms over 6 loads; median 374 ms). | Met |
| S7 Panel query latency, warm read cache | at most 500 ms p95 | 121 ms p95 server `elapsed_ms` over 60 panel queries (12 panels, loads 2 to 6); median 70 ms, maximum 135 ms. | Met |
| S8 Requests per dashboard refresh | 1 batch request, plus 1 for annotations; variable options once per range change | 1 `POST /api/panels/query` per manual refresh. 1 `POST /api/variables/resolve` plus 1 `POST /api/panels/query` per range change and per variable change. No annotation request was observed in M1. A dashboard with no variables makes 0 resolve requests on load. | Met, annotations not present |
| S9 Panel types | 15 | Not measured in M1 | n/a |

S6 note: the 12-panel dashboard is built from the default spec's panel shapes: 4 stats, 4 time series, 2 bars and 2 tables, plus 2 more time series. Its range is 24 hours with auto-refresh off. "Rendered" means 6 chart images, 2 tables and the stat values are present with no loader. The single `panels/query` call finished at about 300 ms of that.

## Default dashboard ("System overview")

| Panel | State |
|---|---|
| Requests per second | data (4.2 to 4.4/s) |
| Error rate | data (2.4%, "Degraded") |
| p95 latency | data (42 ms, "Healthy") |
| Error logs | empty, with a stated reason: "No logs match upper(severity) IN ('ERROR', 'FATAL'). Without that filter, N do." Correct: the data holds no error logs. |
| Requests by service | data (stacked time series) |
| p95 latency by service | data |
| Error rate by service | data (bar) |
| Slowest endpoints | data (15 rows) |

Controls checked: all worked.

- Time picker: 11 presets plus an absolute range. Changing to 24 hours sent 1 resolve and 1 query.
- Refresh: 1 query.
- Auto-refresh radios: present (Off, 10s, 30s, 1m, 5m).
- Compare: turned on and off. Stats show "vs previous period" deltas.
- Variable `service`: selecting `checkout` re-resolved and re-queried, and the URL gained `var-service=checkout`.
- Panel menu: View, Inspect, Explain in chat, Copy link.
- Full-screen view: opens a dialog and sets `view=<panel id>`. Escape closes it.
- Inspect drawer: tabs Data, Query, Spec, Timing, all populated. Timing reads "Ran in 28 ms, one point per 30s, 70 rows."
- Copy link: the clipboard held the dashboard URL with `view=errors`, and a "Link copied" notice showed.
- Edit mode at 1440 px: dragged "Requests per second" to the right of the first row, saved. `PUT` created version 2 ("Edited layout"). After a full reload the panel was still at the right of the first row.
- Light and dark themes both render with readable contrast. The browser console had no errors or warnings across the session.

## Agent prompts

All times are seconds from send to a saved dashboard. Status is `ok`, `empty` or `error` panels at save, read from `GET /api/dashboards/{id}` and `POST /api/panels/query`.

| # | Prompt | Dashboard | Seconds | Panels (ok/empty/error) | Answers the question? |
|---|---|---|---|---|---|
| 1 | Give me a health overview of the shop. | none; answered in text | n/a | n/a | Text answer was accurate, but no dashboard. |
| 1b | Build me a dashboard: give me a health overview of the shop. | Shop Health Overview (7 panels) | 16 | 7/0/0 | Yes. Four stats, error-rate and latency timelines, and a per-service health table. |
| 2 | Why is checkout slow right now? | none; answered in text | n/a | n/a | Text answer found the 84 s Kafka "orders publish" span and said there was no telemetry in the last 15 minutes. |
| 2b | Build me a dashboard: why is checkout slow right now? | Checkout Performance Diagnosis (8 panels) | 32 | 8/0/0 | Partly. It isolates PlaceOrder and the "orders publish" span, but the "Checkout Health Status" stat shows p50 latency, and the three latency charts look identical at this scale. |
| 4 | Frontend latency by route with p50, p95 and p99. | Frontend latency by route (2 panels) | 12 | 2/0/0 | Partly. The table has p50, p95 and p99 per route. The time series shows p95 only. |
| 6 | Show the slowest PlaceOrder traces and what they logged. | none; answered in text | n/a | n/a | Text answer was good. |
| 6b | Build me a dashboard of the slowest PlaceOrder traces and what they logged. | Slowest PlaceOrder Traces (4 panels) | 28 | 4/0/0 | Partly. The trace table is right, but the log panels are totals for the hour, not tied to those traces. |
| 10 | Log volume by severity and service, with a stream of the errors. | Log Volume by Severity and Service (3 panels) | 14 | 3/0/0 | Partly. Volume by severity and by service is right. There is no error stream, because there are no error logs. The agent said so in its reply but left no explained-empty panel. |

Follow-up edit, on Shop Health Overview: "Add a panel with error rate for payment." It took 6 seconds to save version 2 (author kind `agent`, message "Add payment service error rate panel"). Comparing the stored specs of versions 1 and 2 shows one added panel, `payment_error_rate`, and no other change. The panel returns data.

## Screenshots


- `overview-light.png`, `overview-dark.png`: System overview.
- `compare24.png`: System overview, 24 hours, compare on.
- `service-checkout.png`: System overview with `service` set to checkout.
- `inspect.png`: inspect drawer.
- `fullscreen.png`: full-screen view of a panel.
- `edit.png`: edit mode.
- `dash-p1-light.png`, `dash-p2-light.png`, `dash-p4-light.png`, `dash-p6-light.png`, `dash-p10-light.png`: the agent-built dashboards, light theme.

## Defects

The fix-commit column is left for the controller.

| ID | Severity | Title | Steps to reproduce | Expected | Actual | Fix commit |
|---|---|---|---|---|---|---|
| D1 | Medium | Dashboard-shaped prompts answered in text, no dashboard saved | In a new chat send "Give me a health overview of the shop.", "Why is checkout slow right now?" or "Show the slowest PlaceOrder traces and what they logged." | A saved dashboard, as S1 requires (10 of 10) | Text-only answers; no dashboard created. With "Build me a dashboard" prefixed, all three saved one. || 02c73ff5 (agent prompt) |
| D2 | Medium | A panel-level `unit` applies to every measure column | Open "Slowest PlaceOrder Traces". The spec sets `unit: ms` on a table with `duration_ms` and `count_distinct(service)`. | `service_count` shown as a plain count (1) | It shows "1.0ms". The column of per-trace durations is correct. || 9b19faca |
| D3 | Medium | Error-stream request dropped silently instead of an explained empty panel | Send "Log volume by severity and service, with a stream of the errors." on data with no ERROR logs. | A logs panel for errors with an explained empty state (S2 allows this) | No error panel at all; only mentioned in chat. || 02c73ff5 (agent prompt) |
| D4 | Low | Small rates display as "0/s" | Open System overview, set `service` to checkout. "Requests per second" and the table's `rate` column. | About 0.04 to 0.1/s | Stat shows "0/s" while the same service's chart shows 0.1/s. || 9b19faca |
| D5 | Low | Empty-state reason names the wrong filter and prints a literal `$service` | Set `service` to checkout. "Slowest endpoints" has the filter `http_route <> ''`, which gRPC-only checkout does not satisfy. | A reason that names the filter that removed the rows | "No spans match service = $service. Without that filter, 4612 do." The variable filter was not the cause. || 9b19faca |
| D6 | Low | Chart legend overlaps the top y-axis label | System overview: "Requests by service" and "p95 latency by service" at 1440 px, legend wrapped to two rows. | Legend and axis do not overlap | The second legend row overlaps the top axis tick ("10/s", "10m"). || 9b19faca |
| D7 | Low | Narrow text wrap and clipped tables | System overview "Slowest endpoints": `frontend` wraps as "fronten / d". "Slowest PlaceOrder Traces" table is cut off after 6 rows with a nested scrollbar. | Cells do not break words; panels size to content or scroll clearly | As described. A large blank area also sits under the single-row table in "Checkout Performance Diagnosis". || 9b19faca |
| D8 | Low | URL parameters carry JSON-quoted values | Toggle compare or open edit mode. | `compare=1`, `edit=1` | `compare=%221%22`, `edit=%221%22` || 9b19faca |
| D9 | Low | Agent output quality in the checkout dashboard | Open "Checkout Performance Diagnosis". | A health stat that shows health; three charts that show different things | "Checkout Health Status" displays p50 latency (15 s). The checkout, PlaceOrder and orders-publish charts look identical. || 02c73ff5 (agent prompt) |
| D10 | Low | No API to read a stored dashboard version | `GET /api/dashboards/{id}/versions` lists metadata only; there is no route for a version's spec. | A way to diff versions through the API | I diffed versions by reading the `dashboard_versions` table directly. || Deferred to milestone 3 (version history) |

## Concerns

- S1 depends on prompt wording. The agent chooses between a dashboard and a text answer, and the verbatim spec prompts for 1, 2 and 6 take the text path.
- Only 5 of 10 benchmark prompts and 1 of 5 follow-up edits were run, so S1 and S5 are sampled, not complete. The model name was not recorded.
- Timings and the render check run on a warm local machine with a single-user data set. S6 and S7 pass with wide margin, but they are not a load test.
- The new payment panel uses thresholds with status `ok` at 1 and `warn` at 5, and no server-kind filter, which is inconsistent with the default dashboard. I did not judge this beyond noting it.
- The "Last hour" and "right now" views end at 17:00Z because of the setup. The agent handled this sensibly in prompt 2 ("no active telemetry ... in the last 15 minutes").

## Model comparison

After the browser run, the agent prompt was changed so dashboard-shaped questions build a dashboard (`02c73ff5`), and the OpenAI provider moved to the Responses API so current OpenAI reasoning models can call tools (`6a87d2bb`). Three models then ran the ten benchmark prompts, each on its own instance with a fresh control database and a copy-on-write clone of the same replayed telemetry. Two runs per model.

| Model | S1 saved | S2 panels ok or explained | S3 invalid | S5 scoped edit | Median seconds to save |
|---|---|---|---|---|---|
| `claude-sonnet-5-5` | 10/10, 10/10 | 100%, 98.5% | 0 | 2/2 | 28, 21 |
| `claude-opus-5-5` | 10/10, 10/10 | 100%, 100% | 0 | 2/2 | 36, 39 |
| `gpt-6.1-sol` | 10/10, 10/10 | 100%, 98.2% | 0 | 2/2 | 47, 44 |

Answer quality on the first run, scored 0 to 5 by two graders that were not contestants (`claude-fable-5-1` and `gpt-5.6-terra`) on four criteria: Sonnet 4.58, Opus 4.67. Sol was not graded.

Held-out prompts: sixteen requests written by a separate agent that never saw the agent prompt or the benchmark failures, eleven that call for a dashboard and five that call for a short answer. They were not used for tuning. Sonnet's two runs: 11/11 and 10/11 dashboards, 5/5 and 5/5 answers without a dashboard, intent accuracy 1.00 and 0.94. The Opus held-out runs were stopped early for cost, and Sol's were not run.

Decision: `claude-sonnet-5-5` is the default. It matches the others on every hard criterion, ties Opus on graded quality within the graders' spread, and saves dashboards about 40% sooner. The OpenAI default is `gpt-6.1-sol`.

S1 on the benchmark prompts is now 10 of 10 for every model, against 2 of 5 in the browser run above, which used the old prompt and Haiku. The held-out set is the better guide to real wording; it should be rewritten before any further prompt tuning.

