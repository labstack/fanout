# Milestone 5: correct metrics, per-minute summaries and the system page

Status: draft for review · 2026-10-10 · branch `feat/agent-dashboards-m5`

This extends `2026-10-04-agent-dashboards-design.md`. Milestones 1 to 4 are
merged. Its success criteria S1 to S13 stay in force; this milestone adds S14
to S22.

## Goal

Fanout shows what is happening in a whole system, not only in its traced
services, and its numbers are right at a realistic scale.

A system is everything whose behaviour decides whether the software works:
the services, what they depend on, where they run, how people reach them, and
what changes. Everything arrives over OTLP through the OpenTelemetry
Collector, using its standard receivers (`hostmetrics`, `docker_stats`,
`httpcheck`) and semantic conventions. Fanout adds no per-source integrations.
It recognises the standard names and presents them.

The front page answers, in this order:

1. Is it reachable?
2. Is anything failing or slow?
3. Is anything running out: disk, memory, CPU?
4. What changed?

Then the user drills into traces and logs, or asks the agent.

## Why now

Three facts from milestone 4 decide the scope.

1. **Metric measures are wrong for counters and histograms.** `rate()` counts
   data points per second, so for a counter it reports how often the Collector
   exports, not traffic. A histogram's `p95` is taken over each data point's
   stored value, not its buckets, so it is not the 95th percentile of the
   observations. The front page's CPU, network and disk panels are counters,
   so it cannot be built on today's measures.
2. **Ingest discards metric meaning.** It stores a cumulative sum as `sum`
   whether it is monotonic or not, so `process.memory.usage` (an up-down
   counter) and `system.network.io` (a counter) are indistinguishable. The
   demo data holds 213 sum metrics of both kinds. Exponential histograms are
   converted with one bound per count instead of one fewer, so every metric
   histogram query silently drops them.
3. **The raw path stops being fast enough.** The replayed demo carries about
   15 spans per second. At 10x that volume a twelve-panel request takes 1.1 to
   1.6 s on the server, at or past S6's 1.5 s budget for the whole render.
   Milestone 3 removed the per-minute summaries. A system worth watching runs
   at 1,000 to 5,000 spans per second.

## Success criteria

| ID | Criterion | Target | Evidence |
| --- | --- | --- | --- |
| S14 | Counter rate | `rate()` on a counter equals the per-second increase across resets, export-interval changes, delta temporality and many series | engine-executed golden tests; demo-data cross-check against an independent window query |
| S15 | Histogram percentiles | `pNN`/`quantile` on a metric histogram equals linear interpolation over the summed bucket increases | engine-executed golden tests with known distributions |
| S16 | Metric fidelity | every OTLP metric kind keeps its kind, temporality and monotonicity; exponential histograms keep zero and negative counts | ingest round-trip tests |
| S17 | Route-invariant results | every measure the summaries serve returns identical values from summaries and from raw rows; span quantiles are within 1% relative error of the exact rank value (`quantile_disc`) | equality tests on the pinned engine; demo-data comparison |
| S18 | Scale | at 1,000 spans/s for 24 h with host and container metrics from 20 hosts: S7 (≤ 500 ms p95 per panel) for every routed shape, a twelve-panel request ≤ 1.0 s on the server, S6 (≤ 1.5 s first render) in the browser | Go benchmark and Playwright on generated data |
| S19 | Ingest headroom | sustained ingest at 2x the S18 rate with summaries on; summary upkeep adds ≤ 10% CPU | ingest benchmark |
| S20 | The system page shows real trouble | through one Collector, the page shows a filesystem above 90%, a failing external dependency, an unreachable endpoint, a check that stopped reporting and a version change within two refreshes | Playwright on a fixture stack; one local run with a real Collector |
| S21 | System page speed | first render ≤ 1.5 s p95 at the S18 volume | Playwright performance test |
| S22 | The agent uses metrics correctly | on host-metric prompts, counters use `rate()`, up-down counters and gauges use `last`/`avg`, histograms use percentiles; S1–S5 still hold | eval harness (public prompts; the sealed holdout is untouched) |

Paid model spend for the milestone stays within $5, all of it in the final
verification.

## What exists today

- `metric_type` is `gauge`, `sum`, `sum_delta`, `histogram`,
  `exp_histogram` or `summary`. Start time and monotonicity are dropped at
  ingest. Histogram panels take a `temporality` option because the data does
  not say.
- Metric histogram panels (`heatmap`, `histogram`) already group by series
  identity and take last-minus-first per bound per period, clamped at zero.
  A counter reset contributes 0 for that period.
- The read caches are the trace caches only (`readCacheVersion` 4). The
  analytical `service_rollup` and `edge_rollup` tables refresh behind a
  watermark. `service_rollup` holds per-minute, per-service span counts, error
  rate, p50 and p95 (not mergeable across minutes), and log and metric counts.
  Health, overview, dependencies, latency, the alert engine and SQL panels
  read it.
- `/` redirects to chat or dashboards. Nothing presents host, container or
  reachability data, although the demo data holds `hostmetrics` and
  `docker_stats` output for one host and 24 containers.

## Approaches considered

**Metric meaning: new columns, or new type names in the existing column.**
Renaming values inside `metric_type` keeps the physical schema, but old format
3 files would carry `sum` with a different meaning, which is silent
misreading. New columns change the physical schema, so the batch format moves
to 4 and format 3 files are refused, which is explicit. The product is
pre-release and the deployed instances already need a wipe, so the cost is
local data only. **Chosen: format 4 with new columns.**

**Quantiles under summaries: exact raw, approximate summaries, or one sketch
on both paths.** Exact `quantile_cont` on raw rows with sketches in summaries
makes a panel's value depend on which path served it. One logarithmic sketch
used by both paths makes results identical by construction and testable by
equality, at a stated 1% relative error. Prometheus histogram quantiles are far
coarser, and DDSketch-based products use the same bound. **Chosen: one sketch,
both paths.**

**Where summaries live: per batch, or global per minute.** A global table
needs rewrites when late or compacted data arrives. Per-batch partial
summaries follow the existing trace-cache rules: they are written with the
batch's cache marker, compaction derives output contributions from cached
inputs, and retention drops them with the batch. Uncached batches and partial
edge minutes are read raw. **Chosen: per batch.**

**Metric summaries.** At the Collector's default 60 s interval each series has
one point per minute, so a per-minute metric summary saves nothing, and a
series' increase spans batches. **Chosen: no metric value summaries.** The
benchmark decides whether metric panels meet S7 raw; if they do not, the fix
is designed then, from numbers.

**The system page: a fixed React page, or a generated dashboard.** A fixed
page would be a second renderer. A dashboard spec generated on the server and
run through the panel executor reuses everything, including S8's single batch
request. **Chosen: generated dashboard, read-only.**

## Phase A: metric fidelity and series-aware measures

### Storage (batch format 4)

The metrics Parquet schema gains:

| Column | Type | Meaning |
| --- | --- | --- |
| `temporality` | dict string | `cumulative`, `delta`, or empty for gauges and summaries |
| `monotonic` | boolean | true for monotonic sums; false otherwise |
| `start_time_unix_nano` | int64 | the data point's start time; 0 when absent |
| `series_id` | uint64 | a hash of the series identity, computed once at ingest |

`metric_type` becomes the OTLP data kind only: `gauge`, `sum`, `histogram`,
`exponential_histogram`, `summary`. `sum_delta` is removed. Format 4 is the
only accepted format; format 3 files are refused like any unsupported format,
with no reader or conversion.

Exponential histograms convert to explicit bounds correctly: the negative
buckets keep their own negative bounds below the zero bucket, and counts have
exactly one more entry than bounds. The conversion keeps every observation's
count.

A sum or histogram point with unspecified temporality is invalid OTLP. The
export is rejected with `InvalidArgument`, as ingest already does for other
invalid input.

### The metrics view

The view's `type` column becomes the semantic kind: `gauge`, `counter`
(monotonic sum), `updown` (non-monotonic sum), `histogram` (both explicit and
exponential) or `summary`. It adds `temporality` and `start_time`.
`get_telemetry_schema` lists each metric's kind and temporality, so the agent
chooses measures from facts.

### Series and increases

A series is one combination of namespace, service, name, unit, scope name,
scope version, attributes and resource, as metric histogram panels already
use. Ingest hashes that identity into `series_id` from a canonical encoding
(sorted keys, typed values), so queries partition by one integer instead of
comparing attribute maps on every row. Metric measures work in two stages, the way PromQL's
`sum by (x) (rate(m[1m]))` does: first per series and bucket, then across the
series in each group.

The increase of a cumulative series between consecutive points is
`current - previous`, or `current` when the value decreased or the start time
changed (a reset). Each increase belongs to the bucket of its later point. The
query reads one lookback period before the window, 10 minutes, so the first
bucket has its previous point. A delta series' increase is its value. A series
first seen inside the window contributes from its second point. Histograms
apply the same rule per bound, with a reset detected on the total count.

### Measures on metrics

| Measure | gauge, updown | counter | histogram |
| --- | --- | --- | --- |
| `count()` | data points | data points | data points |
| `rate()` | not defined | Σ series (increase ÷ elapsed) | Σ series (count increase ÷ elapsed) |
| `sum(value)` | Σ series bucket average | Σ series increase | Σ sum increase |
| `avg(value)` | mean of series bucket averages | not defined | Σ sum increase ÷ Σ count increase |
| `min`/`max(value)` | over data points | over data points | not defined |
| `last(value)` | Σ series value at bucket end | Σ series value at bucket end | not defined |
| `pNN`/`quantile(value, q)` | over data points | not defined | linear interpolation over Σ bound increases |

Each delta between consecutive points of a cumulative series covers the
interval (previous time, time] at the rate increase ÷ elapsed. A series' rate
in a bucket is the time-weighted mean of that rate over the part of the bucket
its deltas cover, and its increase in a bucket is each delta's increase in
proportion to the overlap. Every series therefore contributes to every bucket
its reporting interval spans: a counter exported every 60 s reads the same
per-second rate in 10 s, 1 m and 1 h buckets, and a total across machines
that report every minute, every five minutes, or at drifting times does not
dip. Histogram count, sum and per-bound increases are spread the same way. For
`rate()` only, a series whose latest delta ends before a bucket carries that
rate into the bucket while the delta's end is at most 5 minutes old, so the
newest buckets of a live chart do not dip while reports are in flight;
increases are never extended.
Gauge and updown series in a bucket without a point use their latest earlier
value when it is at most 5 minutes old, the staleness rule `last` uses. The
automatic bucket for metric panels is never shorter than 1 minute.

A series' value at a bucket's end is its latest point strictly before the
earlier of the bucket end and the window end (both ends are exclusive, so a
point exactly on a boundary belongs to the next bucket), and the series counts only if
that point is at most 5 minutes old, as Prometheus treats staleness. A
container that stopped or restarted therefore stops adding to `last` after
5 minutes, and a stat over a whole day shows what is reporting now.

A measure that is not defined for the series' kind returns no value, and the
empty-panel diagnosis names the kind and the measures that fit, for example
"rate() needs a counter; system.memory.usage is an updown; use last(value) or
avg(value)". OTLP summary metrics have no aggregate measures, because their
stored quantiles cannot be combined.

Validation adds one rule: an aggregate metric query with any measure other
than `count()` or `count_distinct()` must fix the metric per group, with a `name = ...` filter on one value or `name`
among `by`. A `name = $var` filter counts only when the variable is
single-valued and has no All option, because All drops the filter. Different
metrics never sum into one number.

Histogram quantile interpolation follows Prometheus: the lowest bucket's lower
bound is 0 when its upper bound is positive, and an observation in the
overflow bucket returns the highest finite bound.

Histogram and heatmap panels use the same increase rule. Their `temporality`
option is removed, because the data now carries it.

### Phase A changes outside the panel compiler

- The agent's spec guide and examples teach metric kinds and the measure
  table, including one counter-rate and one histogram-percentile example.
- The dashboards guide documents metric kinds and measures.
- Outside the repository, the private benchmark replay restores monotonicity
  for the stored demo data from OpenTelemetry's semantic conventions, because
  that source lost it. The committed demo-data cross-check runs when that data
  is supplied and skips otherwise, like milestone 4's read benchmarks.

## Phase B: the scale benchmark

Measure before building. A generator writes realistic telemetry through OTLP
ingest:

- spans and logs at 1,000 spans per second for 24 hours, built by tiling the
  demo replay across namespaces and hosts, keeping its shape of services,
  operations, routes and errors;
- host and container metrics for 20 hosts with 24 containers each, at the
  Collector's 10 s and 60 s intervals, using the demo's `hostmetrics` and
  `docker_stats` names;
- a 6-hour informational run at 5,000 spans per second.

The run checks free disk first and refuses to start below twice its estimated
output. It records, for every M4 benchmark shape plus the new metric shapes:
raw-path p95 per panel, the twelve-panel request time, ingest throughput, and,
for Phase C sizing, the number of distinct summary keys per minute for each
candidate dimension set.

Results go into `docs/benchmarks/2026-10-agent-dashboards-m5.md`. The
metric shapes include the system page's: CPU from counter rates, memory and
filesystem from updown `last`, container CPU and memory from gauges, each at
24 hours. If any metric shape misses S7 at the S18 volume, Phase C adds a
metric fix, designed from those numbers, before Phase D starts.

## Phase C: per-minute summaries

### What is summarised

Per batch and per minute, three private read tables:

- **Spans.** Keys: namespace, minute, and the dimension set below. Values:
  span count, error count, served-span count, duration sum, minimum and
  maximum, and a duration sketch.
- **Logs.** Keys: namespace, minute, service, severity, severity number.
  Value: count.
- **Metric counts.** Keys: namespace, minute, service. Value: data points.

The span dimension set starts as service, operation, kind, status,
`http_method`, `http_status_code`, `http_route`, `rpc_service`, `rpc_method`,
`db_system`, `messaging_system`, `peer_service`, `service_version` and
`deployment_env`. Phase B's key counts confirm it. The rule: summary rows per
minute stay at or below one twentieth of spans per minute at the S18 rate. A
dimension that breaks the rule is dropped, and panels that use it read raw.

### The sketch

Durations are bucketed logarithmically with relative accuracy α = 0.01:
γ = (1 + α) / (1 − α), and a positive duration `x` falls in bucket
`ceil(log_γ(x))`. Zero and negative durations share one bucket valued 0; NaN
is excluded, as today. A quantile walks the summed bucket counts and returns
the bucket's representative value, `2γ^i / (γ + 1)`. Raw-path span quantiles
use exactly this computation, so both paths return the same number.

### Routing

The compiler serves a query from summaries when all of these hold:

- the signal is spans or logs, and the panel is an aggregate visualization
  (stat, gauge, timeseries, bar or table);
- every `by` field and every column referenced by a filter is a summary key;
  attribute lookups always read raw;
- every measure is `count`, `rate`, `error_rate`, `share`, or `avg`, `sum`,
  `min`, `max`, `pNN` or `quantile` of `duration_ms`;
- the bucket is a whole number of minutes.

A routed query reads summaries for whole minutes of cached batches, and raw
rows for uncached batches and for the partial minutes at each end of the
window. The read happens in one transaction under the pinned file snapshot,
as the trace caches do. Panel results report which path served them, for the
Inspect view and for tests.

### Lifecycle

Summaries follow the read-cache rules in `AGENTS.md`. They are written with
the batch's cache marker in one transaction. Compaction derives the output
batch's summaries from its inputs' summaries. Retention drops them with the
batch. `readCacheVersion` moves to 5, and a version mismatch rebuilds every
private read table.

### `service_rollup` retires as a table

Two per-minute span aggregates would be redundant. `service_rollup` and its
refresh loop are removed. Health, overview, dependencies, latency and the
alert engine read the span, log and metric-count summaries through one Go
reader. The public SQL relation `service_rollup` stays as a view with the same
columns, computed from the summary tables; p50 and p95 come from the merged
sketch. `edge_rollup` stays, because caller and callee pairs join spans across
batches.

## Phase D: the system page

`/` opens the system page, and the rail gains a System entry. The page is a
dashboard spec generated on the server from what telemetry exists in the
selected range and namespace, run through the panel executor in one batch
request, and rendered by the dashboard renderer. It is read-only. A section
whose data is absent is replaced by one text panel naming the Collector
receiver that supplies it, linked to the setup guide.

Sections, in the page's question order:

1. **Reachable.** From `httpcheck` metrics: a state timeline per endpoint and
   a latency timeseries. Each check names where it ran from, the reporting
   Collector's `host.name`. A check with no result for two of its intervals
   shows "no recent result" in the bad state, never a blank: silence is not
   health. A check run on the same machine as the services cannot see DNS,
   CDN or proxy failures that outside visitors hit, so the section says when
   every check runs on a host that also runs observed services.
2. **Failing or slow.** The health panel and the service map, which already
   show external dependencies from client spans.
3. **Running out.** Two tables, worst first, with warning at 80% and bad at
   90%. Hosts: CPU busy from `system.cpu.time` rates by state, memory used
   from `system.memory.usage` by state, and the fullest filesystem from
   `system.filesystem.usage` by mountpoint. Containers: CPU from
   `container.cpu.utilization`, memory as `container.memory.usage.total` over
   `container.memory.usage.limit`. These are SQL panels written by the server
   over the metrics view, using the Phase A increase and latest-value rules.
4. **What changed.** Deploy markers on the health trend, and a versions table:
   service, `service.version` and when it was first seen, runtime name and
   version, and SDK language and version, from resource attributes.

The documentation gains a guide to watching a system: a Collector
configuration with `hostmetrics`, `docker_stats`, `httpcheck` and OTLP
forwarding, and what each section needs. It recommends running `httpcheck`
from a second Collector on a machine outside the observed stack, and states
the limit plainly: if the machine running Fanout fails, nothing on the page
can report it, so Fanout itself still needs an outside monitor.

### The self-observation test

The page is right if it would have shown the problems a real stack had. S20's
fixture stack replays the demo plus five scenarios through OTLP: a filesystem
filling past 90%, an external dependency returning errors, an `httpcheck`
target going down, a check that stops reporting, and a new
`service.version`. Playwright checks that each
appears in its section within two refreshes. Once, on a development machine, a
real Collector observing real local containers confirms the same page.

## Phase E: final verification

S1 to S22 run on the final commit: the eval harness (within $5, with new
public host-metric prompts for S22), the browser collector, the Playwright
performance and accessibility tests, the Go benchmarks at the S18 volume, and
the equality tests. Results complete the M5 benchmark document.

## Testing

- Every SQL-producing change is tested by executing on the pinned engine,
  not by comparing SQL text.
- Counter, histogram and sketch logic have golden tests with known answers:
  resets, interval changes, delta series, late and out-of-order points,
  series first seen inside a window, empty buckets and overflow buckets.
- Route equality: each routed shape runs both ways on the same snapshot and
  must return identical frames.
- Lifecycle: compaction, retention and late batches never double count or
  lose a summary contribution.
- The fixture-stack e2e test runs in CI. The real-Collector run is recorded
  in the benchmark document.

## Out of scope

Alert rules created from system-page sections, incidents and notifications,
reachability checks run by Fanout itself (a Collector with `httpcheck` on an
outside machine covers them), cost and usage tracking, vulnerability matching for runtime
versions, and summaries for metric values. Each is designed separately if the
page shows it is needed.

## Risks

- **Summary cardinality.** A route-rich service can produce many keys per
  minute. Phase B measures it, and the one-twentieth rule drops dimensions
  rather than letting summaries grow.
- **Lookback cost.** Reading 10 minutes before the window adds work to every
  counter query. The benchmark includes counter shapes at 10 s intervals.
- **Changed numbers.** Span quantiles move from exact to within 1%. The
  dashboards guide states it, and Inspect shows the path.
- **Histogram percentile approximations.** Interpolation is linear within a
  bucket, as in Prometheus. Two known effects: a quantile that falls in an
  exponential histogram's zero bucket interpolates across
  (−threshold, threshold], so positive-only data can read slightly below 0, by
  at most the zero threshold (usually 0); and series with different bucket
  layouts in one group are combined on the union of their bounds, which can
  lean a quantile upward by up to one coarse bucket width.
- **Metric query cost.** Phase A's per-series math costs more than the old
  per-point math. Phase B measures, and Phase C fixes any miss of S7. Known
  costs: decoding and comparing exponential histogram bucket lists per point
  (about 600–740 ms at one hour for 160-bucket series), gauge and counter
  panels also scanning for histogram rows, and stat panels running the series
  builder for both the trend and the totals.
- **Format break.** Format 4 refuses existing local data. Deployed instances
  are already on a release whose data needs a wipe before upgrading.
