# Agent dashboards Milestone 3 — measurements

## Build measurements (Task 1b)

Task 1b measurements only. Controller light/dark live-chat, console/network and screenshot acceptance remains pending; automated fixtures are not browser evidence.

| MCP app output | Raw bytes | gzip -9 -n bytes |
|---|---:|---:|
| `internal/mcp/apps/panels.html` | 2,342,591 | 772,682 |

The five retired outputs totaled 7,076,818 raw bytes in the supplied baseline. The generic output is below the 2,500,000-byte target and reduces that aggregate by 4,734,227 bytes (66.90%).

SPA embedded tree: 2,972,143 → 2,974,801 raw bytes. Route-related chunk measurements below include shared chunks; no SPA threshold was specified.

| SPA chunk family | Before raw bytes | After raw bytes |
|---|---:|---:|
| `chat-*.js` | 17,950 | 19,565 |
| `chat.index-*.js` | 70 | 70 |
| `chat._threadId-*.js` | 70 | 70 |
| `dashboards.index-*.js` | 430 | 430 |
| `dashboards._dashboardId-*.js` | 517 | 517 |
| `page-*.js` | 1,118,815 | 1,118,815 |
| `settings-*.js` | 12,390 | 12,390 |
| `mcp-app-frame-*.js` | 242,399 | 243,366 |
| `index-*.js` | 705,786 | 705,786 |
| `preload-helper-*.js` | 16,963 | 17,039 |

`page-*.js` remains the shared dashboard page chunk. The MCP host frame remains lazy; the app build rejects runtime imports of SPA routes/auth/API modules and emits only `panels.html` with inlined styles, fonts and Blob worker code.

Verification: host Vitest and TypeScript gates, seeded MCP fragment calls, source boundary fixtures, app build graph/CSP fixtures, and temporary-output byte/path comparisons passed. Browser acceptance and the clean-source private audit binary run belong to the controller review gate.

### Task 1b fix round 1

The rebuilt `panels.html` is **2,343,092 raw bytes / 773,074 gzip -9 -n bytes**. The SPA embedded tree is **2,978,557 raw bytes**. Both regenerated trees match a fresh staged build. The app remains below the 2,500,000-byte target.

`BenchmarkBoundFragmentLarge`, one measured iteration over 40 × 5,000 cells of 346 characters (200,000 cells, about 69 MB), took **350,498,500 ns/op** (350.5 ms), with **1,363,672,496 B/op** total allocations and **1,203,179 allocs/op**. Total allocations are not peak memory. This is a local benchmark, not a browser or concurrency measurement.

Automated size-reporter tests see no additional notification over a simulated three-second settled interval and test wrapper-only measurement. Actual Chromium convergence, node-label font size, graph width and reader scroll acceptance still require the controller's browser collector.

### Task 1b fix round 2

The rebuilt `panels.html` is **2,333,439 raw bytes / 769,783 gzip -9 -n bytes**. The SPA embedded tree is **2,969,904 raw bytes**. Both trees match a fresh staged build; the app remains below the 2,500,000-byte target.

The shared map now uses one deterministic Dagre LR layout with two-dimensional contain fitting. The 20-node fixture passes box, route and label geometry assertions at 780 × 460 and 1100 × 220. The reusable DOM collector is `ui/host/tests/service-map-collector.ts`; actual Chromium runs on chat and dashboards remain the controller's acceptance gate.

### Task 1b fix round 3

The rebuilt `panels.html` is **2,334,158 raw bytes / 770,144 gzip -9 -n bytes**. The SPA embedded tree is **2,970,771 raw bytes**. Both trees match a fresh staged build; the app remains below the 2,500,000-byte target.

Full 44 px cards and compact 20 px cards now share the same Dagre LR algorithm. Cached geometries let resize choose the card mode without a fresh layout. The restored dashboard fixture passes containment and measured name/font budgets at 1100 × 190 and 1100 × 220, with all twenty names visible at or above 11 px. Call the DOM collector with `{requireNames:true}` for the controller's dashboard browser re-check; automated fixtures do not establish live browser acceptance.

## Authoring benchmark (Task 12)

Status: **PASS** on the iteration run (all of S1–S5). The baseline failed S2 for an environment reason, explained below. This file records Task 12 of the milestone 3 plan. Task 14 adds the final frozen-candidate acceptance run and the browser matrix.

### Method

- **Runner:** `scripts/dashboard-eval/main.ts --set benchmark`, the promoted runner from Task 3. Each run is the ten benchmark prompts from the design spec (S1–S4), followed by five consecutive edits on the first saved board (S5): title, threshold, add, remove and unit. Every provider call is metered into one cost ledger shared across both runs, capped at $5 for the task. No paid judge. The sealed holdout set was not opened.
- **Model:** `claude-sonnet-5-5` (Anthropic), the default agent model. Every metered call matched the configured model.
- **Telemetry:** the immutable fanout-demo format-2 source (483 files; source hash `7131c02c93179d51…`). It was replayed over OTLP/HTTP into a fresh disposable instance, which stores format 3.
  - Each run used one signed shift that places the newest replayed event at the replay start, so "right now" questions read fresh telemetry against the running clock.
  - Traces (2,213,782 spans) and logs (860,377 records) were replayed by two parallel processes sharing that one shift. Metrics were excluded so the replay finishes inside the runner's five-minute freshness gate. None of the ten prompts needs metric points.
  - Control state was fresh: no copied database and no secrets.
- **Instance:** a production build of the candidate, on a local native Fanout with a fresh data directory.

### Results

| Run | Candidate | S1 saved | S2 good / total | S3 invalid | S4 median (s) | S5 edits | Cost (USD) |
|---|---|---:|---:|---:|---:|---:|---:|
| Baseline | d5bdc79c | 10/10 | 56/59 | 0 | 15.5 | 5/5 | 1.388 |
| Iteration | fb00809e | 10/10 | 58/58 | 0 | 19.9 | 5/5 | 1.445 |

Targets: S1 10 of 10, S2 100%, S3 0, S4 a median of 45 s or less, S5 100%. S4 measures from the prompt to the save that S1–S3 score. Total spend for the task: $2.83 of $5.

#### Baseline per prompt

| Prompt | Panels | Explained empty | Errors | First save (s) | Scored save (s) | Cost (USD) |
|---|---:|---:|---:|---:|---:|---:|
| frontend-latency | 3 | 0 | 0 | 14.0 | 14.0 | 0.165 |
| health-overview | 7 | 0 | 0 | 25.2 | 25.2 | 0.150 |
| checkout-slow | 9 | 0 | 0 | 25.6 | 25.6 | 0.154 |
| errors-by-service | 7 | 1 | 0 | 15.8 | 15.8 | 0.114 |
| hour-compare | 6 | 0 | 3 | 20.5 | 20.5 | 0.122 |
| placeorder-traces | 3 | 0 | 0 | 14.9 | 14.9 | 0.111 |
| kafka-throughput | 7 | 0 | 0 | 15.1 | 15.1 | 0.098 |
| db-cache | 6 | 0 | 0 | 14.4 | 14.4 | 0.109 |
| deploys | 5 | 0 | 0 | 20.1 | 20.1 | 0.117 |
| log-volume | 6 | 2 | 0 | 12.6 | 12.6 | 0.100 |

#### Iteration per prompt

| Prompt | Panels | Explained empty | Errors | First save (s) | Scored save (s) | Cost (USD) |
|---|---:|---:|---:|---:|---:|---:|
| frontend-latency | 3 | 0 | 0 | 12.1 | 12.1 | 0.162 |
| health-overview | 11 | 0 | 0 | 21.8 | 21.8 | 0.140 |
| checkout-slow | 8 | 0 | 0 | 15.6 | 15.6 | 0.109 |
| errors-by-service | 8 | 0 | 0 | 24.3 | 24.3 | 0.154 |
| hour-compare | 5 | 0 | 0 | 22.9 | 22.9 | 0.124 |
| placeorder-traces | 4 | 0 | 0 | 14.7 | 14.7 | 0.118 |
| kafka-throughput | 5 | 0 | 0 | 18.0 | 18.0 | 0.111 |
| db-cache | 5 | 0 | 0 | 46.4 | 46.4 | 0.181 |
| deploys | 5 | 0 | 0 | 41.1 | 41.1 | 0.118 |
| log-volume | 4 | 1 | 0 | 14.2 | 14.2 | 0.081 |

"Explained empty" panels returned no rows and carry both the executor's diagnosis and an authored description. S2 counts them as good. Every explained empty panel in these runs asks for error-severity logs, and the replayed demo logs have none.

### What changed between the runs

1. **Environment: the DuckDB memory cap.**
   - The baseline instance inherited a 4 GB `FANOUT_DUCKDB_MEMORY` cap from an earlier local setup. Bulk-replaying 40 hours of telemetry in about two minutes left a backlog of several thousand small batches. The service rollup that worked through that backlog repeatedly hit "Out of Memory Error (3.7 GiB/3.7 GiB used)", and so did three table panels the post-save check ran at the same moment.
   - Those three errors are the baseline's entire S2 failure. The authored queries were valid.
   - The iteration run uses Fanout's default sizing (unset; derived from detected memory) and saw no memory errors.
2. **Product: guidance against pinned identifiers (fb00809e).**
   - In the baseline, the "slowest PlaceOrder traces" board saved a logs panel filtered by three trace IDs copied from one query result. Such a panel goes stale as soon as newer traces arrive.
   - The analysis guidance now forbids filtering saved panels on trace, span or request IDs copied from a single result, and points to a traces panel with drill to its logs. `internal/agent/dashboard_guidance_test.go` pins the rule.
   - The iteration run's board follows it: a traces panel with drill, a latency series, log patterns and service logs.

No scorer, threshold or post-save check was changed.

### Findings for milestone 4

- **Memory under an ingest backlog:**
  - With a 4 GB DuckDB cap, the service rollup and concurrent panel queries fail with out-of-memory errors while a large backlog of small batches is pending, instead of spilling or degrading.
  - The version rollup also hit its execution timeout.
  - This belongs with the S6/S7 performance work. The post-save check records only `status: error` for such panels, without the executor message.
- **Run-to-run variation:** the S4 median moved from 15.5 s to 19.9 s between runs. The slowest iteration prompts (database and cache, recent deploys) took 41–46 s and made more tool calls. These are single runs, not repeated measurements.

### Limitations

- Each column is one run, not a mean.
- Metric points were not replayed.
- The edits target the first saved board (frontend latency by route).
- The sealed holdout set is untouched and was not used for tuning. Task 14 records it as unrun; see below.

## Final acceptance (Task 14)

Status: **PASS** for the authoring benchmark, the browser collector, the checked-in smoke test and the per-panel check. The sealed holdout set was **not run**.

### Authoring benchmark on the frozen candidate

| Run | Candidate | S1 saved | S2 good / total | S3 invalid | S4 median (s) | S5 edits | Cost (USD) |
|---|---|---:|---:|---:|---:|---:|---:|
| Final | eee385d8 | 10/10 | 67/67 | 0 | 18.7 | 5/5 | 1.449 |

- The method is the same as Task 12: a production build, a fresh replay with one shared shift, default DuckDB sizing, and its own $5 cost ledger.
- The fix rounds after eee385d8 change rendering, the server's packing of new panels and query-cancellation logging. They do not change agent guidance, tools or panel execution results, so the run was not repeated.

### Browser evidence

- **Collector** (private, development build of a1395856, fresh replay with deploy markers, light and dark): 33 of 33 checks pass, and theme coverage passes 30 of 30 visualization and theme rows.
- **Smoke test** (`just e2e`, 1100 and 1440 px, light and dark): 24 of 24 on every fix round, including 0d8a5123.
- **Per-panel check** (0d8a5123, a 23-panel verification board and a 10-panel agent-built board, light and dark):
  - all 33 panels render with no failed panels, stuck loaders, truncated titles or overflow outside the card;
  - every row has one height and spans all twelve columns;
  - there are no console errors.
- **Service map overflow:** at half width, 7 of 18 services fit at the readable floor. The cropped edge fades and Fit appears at first render. Fit shows all 18 services, and a second press returns to the readable view. A map that fits shows neither.

### Panel app size

`internal/mcp/apps/panels.html` is the only MCP app: 2,380,413 bytes raw and 784,374 bytes with `gzip -9 -n`. The five milestone 2 apps were 7,076,818 and 2,412,876 bytes, so the new app is 66% smaller raw and 67% smaller gzipped.

### Holdout set

- The sealed holdout has 16 prompts: 11 ask for a dashboard and 5 ask for a direct answer. The prompt text was not read.
- The promoted runner grades exactly ten dashboard prompts, so it cannot score the holdout unchanged. Every M3 benchmark number therefore comes from prompts the work was tuned against, and unseen requests may score lower.
- Milestone 4 needs a runner that grades both answer and dashboard intent before the holdout can run.

### Findings for milestone 4 (Task 14)

- A long refresh error is truncated in the panel and its full text cannot be reached.
- The rollup and panel out-of-memory errors under a DuckDB memory cap during an ingest backlog, recorded in Task 12, remain open.
- Saved dashboards keep their stored grid. Row snapping and twelve-column fill apply only when the server packs new panels.
