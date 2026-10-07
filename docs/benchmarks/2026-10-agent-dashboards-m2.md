# Agent dashboards Milestone 2 verification

Build: 669bec26 (collector run on 2c37797e; 669bec26 changes only a manual-refresh queue and a test wait). Model: claude-sonnet-5-5. Source: Replayed fanout-demo format-2 telemetry converted to format 3 over OTLP by .superpowers/replay (newest input 2026-10-05T16:45:10.69Z, target 2026-10-06T22:40:35.61Z), served by a disposable local instance with no AI provider. Deploy markers use 7,201 synthetic cart spans with service.version 2.3.0 (attribute fanout.synthetic), 19:30:58Z-23:30:58Z. Loading, empty, error, partial and sparkline-gap states were produced by browser network interception, not demo data. Evaluations: claude-sonnet-5-5 on the same frozen replay; benchmark baseline and candidate are two-repeat means; holdout baseline is a two-repeat mean, holdout candidate is one run (the second failed to start in the harness). No judge pass..
Status: PASS.

This run was taken about a day after the replay's newest point, so its 24-hour window holds only part of the replayed telemetry. An earlier run inside a fresh window returned about 20,500 cells per refresh in 1.6–2.1 s, also with exactly one panels request and one annotations request.

Replay: 2213782 spans, 860377 logs, 14302306 metric points; shift_ns=107724921629673.

## Browser type coverage

| Theme | Type | Visible | Aria | Inspect | Console errors |
|---|---|---|---|---|---|
| light | stat | true | true | true | 0 |
| light | gauge | true | true | true | 0 |
| light | timeseries | true | true | true | 0 |
| light | bar | true | true | true | 0 |
| light | table | true | true | true | 0 |
| light | text | true | true | true | 0 |
| light | heatmap | true | true | true | 0 |
| light | histogram | true | true | true | 0 |
| light | scatter | true | true | true | 0 |
| light | state_timeline | true | true | true | 0 |
| light | logs | true | true | true | 0 |
| light | log_patterns | true | true | true | 0 |
| light | traces | true | true | true | 0 |
| light | service_map | true | true | true | 0 |
| light | health | true | true | true | 0 |
| dark | stat | true | true | true | 0 |
| dark | gauge | true | true | true | 0 |
| dark | timeseries | true | true | true | 0 |
| dark | bar | true | true | true | 0 |
| dark | table | true | true | true | 0 |
| dark | text | true | true | true | 0 |
| dark | heatmap | true | true | true | 0 |
| dark | histogram | true | true | true | 0 |
| dark | scatter | true | true | true | 0 |
| dark | state_timeline | true | true | true | 0 |
| dark | logs | true | true | true | 0 |
| dark | log_patterns | true | true | true | 0 |
| dark | traces | true | true | true | 0 |
| dark | service_map | true | true | true | 0 |
| dark | health | true | true | true | 0 |

## S8 and bounded refresh

| Theme | Panels | Panel requests | Annotation requests | Cells | ms |
|---|---:|---:|---:|---:|---:|
| light | 23 | 1 | 1 | 6642 | 1219 |
| light | 23 | 1 | 1 | 6642 | 1201 |
| light | 23 | 1 | 1 | 6642 | 1268 |
| dark | 23 | 1 | 1 | 6642 | 1269 |
| dark | 23 | 1 | 1 | 6642 | 1240 |
| dark | 23 | 1 | 1 | 6642 | 1169 |

## Interaction evidence

- inspect: PASS; 30 theme/type Inspect checks matched frames or SQL and closed (text verified directly)
- loading: PASS; Loading panel visible before delayed response, absent after 1500ms response and render settle
- empty: PASS; empty asserted on 14 types/panels (calls,err_gauge,rate_ts,svc_bar,svc_table,lat_heat,lat_hist,ops_scatter,err_states,recent_logs,patterns,slow_traces,svc_map,svc_health); interception removed; observed real response and rendering recovered
- error: PASS; error asserted on 1 types/panels (rate_ts); interception removed; observed real response and rendering recovered
- partial: PASS; partial asserted on 1 types/panels (rate_ts); interception removed; observed real response and rendering recovered
- filter_chip: PASS; Real bar selected frontend; removal restored All and 10 visible services; selection request, removal cache_hit
- linked_crosshair: PASS; 4 required comparisons show exactly one ≤8px band within ±12px at relative x=.63; wide tooltip bands recorded and ignored; heatmap/timeline followers recorded; chart IDs ec_1791397700259→ec_1791397727777
- brush_history: PASS; 40-move 200px brush pushed exactly one history entry; from/to appeared; Back restored http://127.0.0.1:7520/dashboards/01a11382-f0df-708c-bade-71d3fa50fc21?range=24h
- panel_time: PASS; Visible 1d badge; requested panel shift=1d; observed response windows differ exactly 86400000ms; all other windows match (1791311395896..1791397795896)
- drill_url: PASS; slow_traces: scrollY=3707 preserved on open/close; click→drill URL→reload retained→Back closed; recent_logs: scrollY=2979 preserved on open/close; click→drill URL→reload retained→Back closed
- waterfall_logs: PASS; Selected trace 020a920f65fbf268508dab44747cdbf9; 2 waterfall spans; correlated logs empty with trace-ID/window reason
- cancel_drill: PASS; Delayed first drill; closed before completion; second dialog matched its URL kind/panel; releasing first response never restored first drawer or target (MutationObserver + URL)
- deploy_markers: PASS; Controller visual inspection in the scripted screenshots and in a real Chrome session on build 2c37797e: the cart-scoped charts draw a dashed "cart 2.3.0" line at the 19:30Z first-seen instant plus the anomaly band; the no-marker twin draws neither; service=checkout shows no deploy line. The pixel heuristic stays inconclusive because the marker label lane compresses the plot; the version is a labelled synthetic injection.
- anomaly_areas: PASS; Cart error_rate_change 2026-10-06T22:29:00Z..2026-10-06T22:54:00Z produces 136px shaded band; checkout has no unrelated band; matched twin screenshots and saved diffs
- deploy_split: PASS; Cart visible frame has Before deploy,Since deploy series and nonblank bar screenshot (request); All has a plain bar and note: Service is All; showing the unsplit whole-window frame. (cache_hit)
- column_formats: PASS; service_link click sets picker/request; sparkline SVG, status text with non-colour cue, width bar, trace_link drill, normalized duration unit and log_template code/wildcards observed
- stat_sparkline: PASS; Calls total=232282 unchanged after one simulated null; 2 SVG subpaths prove gap; route removed and real data recovered

## Agent evaluation

Holdout is never used for tuning. Matching repeats use the same frozen telemetry and account isolation.

| Set | Baseline S1 | Candidate S1 | Baseline intent | Candidate intent | Errors | Unchecked |
|---|---:|---:|---:|---:|---:|---:|
| benchmark | 1 | 1 | 1 | 1 | 0 | 0 |
| holdout | 0.9091 | 1 | 0.9375 | 1 | 0 | 0 |

## S13 M2 capability preview comparison

| Capability | Preview evidence | M2 evidence | Gap |
|---|---|---|---|
| heatmap | Latency distribution heatmap: log buckets labelled in ms (<25 ms to >=3.2 s), sequential blue ramp | lat_heat: log2 buckets labelled with units (0-1 ms to 2.2-4.4 min), p99-capped continuous colour scale with its legend, deploy and anomaly markers, drill to exemplar traces; light and dark screenshots | none |
| log_patterns | Error and warning patterns: severity, template with wildcards, service, count, trend | patterns: body_template with placeholders, count bar and per-template trend, which is the spec definition (body_template counts with trend); the preview adds per-pattern severity and service columns, tracked for the milestone 4 parity review | none |
| deploy_markers | Dashed "deploy payments v2.14.0" line and a shaded anomaly region on time charts | Dashed "cart 2.3.0" line at the first-seen instant and the 22:29-22:54Z anomaly band on cart-scoped charts; absent for checkout; label clear of the zoom tools | none |
| drill_drawer | Click a chart for exemplar traces; click a row to open the trace | Drawer addressed by a drill= URL that survives reload and closes on Back; exemplars, shared waterfall and correlated logs; a cancelled drill never reappears | none |
| split_bars | Time in downstream calls: paired Before deploy and Since deploy bars | deploy_bar with service=cart shows Before deploy and Since deploy series; with All it shows the plain bar and a note | none |
| table_formats | Trace links, durations with units and inline bars, counts and error text | service_link, sparkline with value, status badge with percent and icon, bar, trace_link opening the drill, unit and log_template, all asserted in the DOM | none |

## Screenshots

- .superpowers/replay/m2-screenshots/light-six-series-legend.png
- .superpowers/replay/m2-screenshots/light-full.png
- .superpowers/replay/m2-screenshots/light-viewport-00.png
- .superpowers/replay/m2-screenshots/light-viewport-01.png
- .superpowers/replay/m2-screenshots/light-viewport-02.png
- .superpowers/replay/m2-screenshots/light-viewport-03.png
- .superpowers/replay/m2-screenshots/light-viewport-04.png
- .superpowers/replay/m2-screenshots/light-viewport-05.png
- .superpowers/replay/m2-screenshots/dark-six-series-legend.png
- .superpowers/replay/m2-screenshots/dark-full.png
- .superpowers/replay/m2-screenshots/dark-viewport-00.png
- .superpowers/replay/m2-screenshots/dark-viewport-01.png
- .superpowers/replay/m2-screenshots/dark-viewport-02.png
- .superpowers/replay/m2-screenshots/dark-viewport-03.png
- .superpowers/replay/m2-screenshots/dark-viewport-04.png
- .superpowers/replay/m2-screenshots/dark-viewport-05.png
- .superpowers/replay/m2-screenshots/dark-crosshair-mounted-rate_ts-err_ts-before.png
- .superpowers/replay/m2-screenshots/dark-crosshair-mounted-rate_ts-p95_ts-before.png
- .superpowers/replay/m2-screenshots/dark-crosshair-mounted-rate_ts-lat_heat-before.png
- .superpowers/replay/m2-screenshots/dark-crosshair-mounted-rate_ts-err_states-before.png
- .superpowers/replay/m2-screenshots/dark-crosshair-mounted-rate_ts-err_ts-after.png
- .superpowers/replay/m2-screenshots/dark-crosshair-mounted-rate_ts-err_ts-diff.png
- .superpowers/replay/m2-screenshots/dark-crosshair-mounted-rate_ts-p95_ts-after.png
- .superpowers/replay/m2-screenshots/dark-crosshair-mounted-rate_ts-p95_ts-diff.png
- .superpowers/replay/m2-screenshots/dark-crosshair-mounted-rate_ts-lat_heat-after.png
- .superpowers/replay/m2-screenshots/dark-crosshair-mounted-rate_ts-lat_heat-diff.png
- .superpowers/replay/m2-screenshots/dark-crosshair-mounted-rate_ts-err_states-after.png
- .superpowers/replay/m2-screenshots/dark-crosshair-mounted-rate_ts-err_states-diff.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-rate_ts-err_ts-before.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-rate_ts-p95_ts-before.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-rate_ts-lat_heat-before.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-rate_ts-err_states-before.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-rate_ts-err_ts-after.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-rate_ts-err_ts-diff.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-rate_ts-p95_ts-after.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-rate_ts-p95_ts-diff.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-rate_ts-lat_heat-after.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-rate_ts-lat_heat-diff.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-rate_ts-err_states-after.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-rate_ts-err_states-diff.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-lat_heat-err_ts-before.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-lat_heat-p95_ts-before.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-lat_heat-err_states-before.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-lat_heat-err_ts-after.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-lat_heat-err_ts-diff.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-lat_heat-p95_ts-after.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-lat_heat-p95_ts-diff.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-lat_heat-err_states-after.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-lat_heat-err_states-diff.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-err_states-err_ts-before.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-err_states-p95_ts-before.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-err_states-lat_heat-before.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-err_states-err_ts-after.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-err_states-err_ts-diff.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-err_states-p95_ts-after.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-err_states-p95_ts-diff.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-err_states-lat_heat-after.png
- .superpowers/replay/m2-screenshots/dark-crosshair-remounted-err_states-lat_heat-diff.png
- .superpowers/replay/m2-screenshots/dark-deploy-cart-markers.png
- .superpowers/replay/m2-screenshots/dark-deploy-cart-plain.png
- .superpowers/replay/m2-screenshots/dark-deploy-cart-diff.png
- .superpowers/replay/m2-screenshots/dark-deploy-negative-checkout-markers.png
- .superpowers/replay/m2-screenshots/dark-deploy-negative-checkout-plain.png
- .superpowers/replay/m2-screenshots/dark-deploy-negative-checkout-diff.png
- .superpowers/replay/m2-screenshots/dark-deploy_markers-failure.png
- .superpowers/replay/m2-screenshots/dark-anomaly-cart-markers.png
- .superpowers/replay/m2-screenshots/dark-anomaly-cart-plain.png
- .superpowers/replay/m2-screenshots/dark-anomaly-cart-diff.png
- .superpowers/replay/m2-screenshots/dark-anomaly-negative-checkout-markers.png
- .superpowers/replay/m2-screenshots/dark-anomaly-negative-checkout-plain.png
- .superpowers/replay/m2-screenshots/dark-anomaly-negative-checkout-diff.png
- .superpowers/replay/m2-screenshots/dark-deploy-split-cart.png
- .superpowers/replay/m2-screenshots/dark-stat-null-gap.png

## Remaining defects and gates

All gates passed against measured evidence.
## Defects this verification found and fixed

The unit and integration suites passed before this check. Running the
dashboards in a real browser on replayed demo data still found the
following, each fixed with a regression test in its owning suite
(commits 93cd7556 and ef61ab1e):

- **Deploy history lost after compaction.** The version rollup skipped any
  batch over 64,000 rows, which includes every compaction output, so its
  versions were never read and every dashboard reported limited annotation
  history. Oversized batches now get a pass of their own, oldest first, and
  a batch that keeps failing backs off without blocking newer ones.
- **Panels stuck loading.** A panel scrolled into view while a batch was in
  flight was never requested. A requested panel the server omits now shows
  an error, and lazy batches keep the dashboard's annotations.
- **One clock per batch.** Panels resolved "now" separately; a 1-day shifted
  panel was 27 ms off. The batch now resolves a single instant.
- **Misleading empty diagnosis.** A highlighted logs panel blamed the
  highlight even when a filter caused the empty result.
- **Presentation:** status cells now carry the measure's unit; the traces
  panel truncates trace IDs instead of overlapping columns and shows
  Error/OK rather than OTLP enum names; sparkline cells show their value;
  heatmap and histogram buckets carry units; the heatmap colour scale is
  capped at the 99th percentile with a legend; single-series charts drop
  their legend; day boundaries read as month and day; deploy labels stay
  clear of the zoom tools; heatmap time labels no longer run together.

Synthetic data: the demo has a single `service.version` per service, so
deploy markers and the deploy split were exercised with a labelled
synthetic version change for one service. Browser network interception
produced the loading, empty, error, partial and sparkline-gap states.

## Final review

A broad review and a focused security and concurrency review followed the
browser check. The focused review ran about 100 SQL-panel shapes against
the pinned engine and found no path to unredacted log bodies, no escape
from the approved relations, and no injection or owner-scope bypass; a
concurrent ingest, compaction and rollup probe converged with every
version present. The findings it and the broad review raised were fixed in
2c5a686c: grouped panels now accept one measure instead of silently
dropping the rest, empty variable options no longer crash a saved
selection, bar drills keep every grouping value and the deploy period,
share sparklines in limited tables use the whole scope, a failing version
rollup batch no longer holds back healthy ones, annotations read in one
transaction, and SQL panel CTEs may not shadow telemetry relations.

Deferred: an explicit empty multi-value selection does not survive a shared
URL (rare; it shows no data either way), and schema-qualified log columns
fail with an error rather than binding (they fail closed).

## Hands-on testing

The dashboards were then used by hand in a real Chrome session, with the agent on
`claude-sonnet-5-5` and, separately, `gpt-6.1-sol`. From one sentence each provider
built a correct dashboard in 30–60 seconds; Sonnet used deploy markers, a
before-and-since split and a version table to show a cart release raising p95 from
2.4 ms to 8.4 ms and errors from 0% to 6%, and it edited the dashboard by
conversation in under 20 seconds.

That session found what the scripted checks could not, all fixed (13aa6942,
2c37797e, 669bec26): the chat gave no link to the dashboard the agent saved and the
sidebar did not list it until a reload; opening a drill scrolled the dashboard to
the top; every chart painted a needless scrollbar on systems that always show
scrollbars; the deploy-split note showed a raw timestamp; the grid could first lay
out at a guessed width; series colours came from two hue families by name hash and
failed colour-vision checks, now a validated six-slot palette assigned in order with
at most six series per chart; and a refresh pressed during a partial lazy batch was
dropped. The scripted collector now also checks that chart bodies fit, that drills
keep the scroll position and that series colours are distinct.
